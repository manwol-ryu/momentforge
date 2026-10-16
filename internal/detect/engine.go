package detect

import (
	"math"
	"sort"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Engine holds chat for causal window statistics. It serves a finished VOD
// (Analyze) and an unbounded LIVE stream (Add, Gap, Evaluate, Prune) with the
// same rules. History bins are computed once and reused across evaluations.
type Engine struct {
	p       Params
	rules   *ruleSet
	ignored map[string]bool
	events  []entry             // sorted by (OffsetMs, Seq)
	byID    map[string]position // event ID → order key, for evidence sorting
	gaps    []domain.Gap
	bins    map[int64]*windowStats // aligned history bins keyed by start
	minGate [3]int
}

type position struct{ offset, seq int64 }

type entry struct {
	ev   domain.ChatEvent
	skip bool     // empty, ignored phrase, or command
	keys []string // message-rule keys
}

type windowStats struct {
	n, u     int
	valid    bool
	ids      []string
	counts   map[string]int      // key → distinct authors
	evidence map[string][]string // key → counted event ids
}

// NewEngine validates params and rules.
func NewEngine(p Params, rules []Rule) (*Engine, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	rs, err := newRuleSet(rules)
	if err != nil {
		return nil, err
	}
	e := &Engine{p: p, rules: rs, ignored: map[string]bool{}, byID: map[string]position{}, bins: map[int64]*windowStats{}}
	for _, phrase := range p.IgnoredPhrases {
		e.ignored[Normalize(phrase)] = true
	}
	e.minGate[0], e.minGate[1], e.minGate[2] = p.Gate()
	return e, nil
}

// Add stores events. Late events are inserted in order and invalidate the
// cached history bins they fall into.
func (e *Engine) Add(events []domain.ChatEvent) {
	if len(events) == 0 {
		return
	}
	earliest := events[0].OffsetMs
	needSort := false
	for _, ev := range events {
		text := Normalize(ev.Text)
		en := entry{ev: ev}
		en.ev.Text = text
		en.skip = text == "" || e.ignored[text] || strings.HasPrefix(text, "!") || strings.HasPrefix(text, "/")
		if !en.skip {
			for _, r := range e.rules.message {
				en.keys = append(en.keys, r.Keys(text)...)
			}
		}
		if n := len(e.events); n > 0 && less(en.ev, e.events[n-1].ev) {
			needSort = true
		}
		e.events = append(e.events, en)
		e.byID[ev.ID] = position{ev.OffsetMs, ev.Seq}
		earliest = min(earliest, ev.OffsetMs)
	}
	if needSort {
		sort.SliceStable(e.events, func(i, j int) bool { return less(e.events[i].ev, e.events[j].ev) })
	}
	for start := range e.bins {
		if start+e.p.WindowMs > earliest {
			delete(e.bins, start)
		}
	}
}

func less(a, b domain.ChatEvent) bool {
	if a.OffsetMs != b.OffsetMs {
		return a.OffsetMs < b.OffsetMs
	}
	return a.Seq < b.Seq
}

// Gap marks [start, end) as unreliable.
func (e *Engine) Gap(start, end int64) {
	if end > start {
		e.gaps = append(e.gaps, domain.Gap{StartMs: start, EndMs: end})
		clear(e.bins)
	}
}

// Prune drops events and bins before the given offset (LIVE memory bound).
func (e *Engine) Prune(before int64) {
	cut := e.search(before)
	for _, en := range e.events[:cut] {
		delete(e.byID, en.ev.ID)
	}
	e.events = append([]entry(nil), e.events[cut:]...)
	kept := e.gaps[:0]
	for _, g := range e.gaps {
		if g.EndMs > before {
			kept = append(kept, g)
		}
	}
	e.gaps = kept
	for start := range e.bins {
		if start < before {
			delete(e.bins, start)
		}
	}
}

// position returns an event's order key; ok is false once it was pruned.
func (e *Engine) position(id string) (position, bool) {
	pos, ok := e.byID[id]
	return pos, ok
}

func (e *Engine) search(offset int64) int {
	return sort.Search(len(e.events), func(i int) bool { return e.events[i].ev.OffsetMs >= offset })
}

// stats counts messages in [start, end).
func (e *Engine) stats(start, end int64) *windowStats {
	ws := &windowStats{valid: true, counts: map[string]int{}, evidence: map[string][]string{}}
	for _, g := range e.gaps {
		if start < g.EndMs && end > g.StartMs {
			ws.valid = false
			break
		}
	}
	perAuthor := map[string]int{}
	reactors := map[string]map[string]bool{}
	for _, en := range e.events[e.search(start):e.search(end)] {
		if en.skip {
			continue
		}
		author := en.ev.AuthorKey
		if author == "" {
			ws.valid = false
			continue
		}
		perAuthor[author]++
		if perAuthor[author] > e.p.PerAuthorCap {
			continue
		}
		ws.ids = append(ws.ids, en.ev.ID)
		for _, k := range en.keys {
			if reactors[k] == nil {
				reactors[k] = map[string]bool{}
			}
			reactors[k][author] = true
			ws.evidence[k] = append(ws.evidence[k], en.ev.ID)
		}
	}
	ws.n = len(ws.ids)
	ws.u = len(perAuthor)
	for k, authors := range reactors {
		ws.counts[k] = len(authors)
	}
	return ws
}

// bin returns the cached stats of the aligned history bin starting at start.
func (e *Engine) bin(start int64) *windowStats {
	if ws, ok := e.bins[start]; ok {
		return ws
	}
	ws := e.stats(start, start+e.p.WindowMs)
	e.bins[start] = ws
	return ws
}

// Evaluate judges the window [t-WindowMs, t). baselineFrom > 0 limits the
// history to bins starting at or after it (viewer-count epochs, phase 3).
// strong reports a window that may start a candidate on its own.
func (e *Engine) Evaluate(t, baselineFrom int64) (w domain.Window, strong bool) {
	p := e.p
	cur := e.stats(t-p.WindowMs, t)
	first := max(0, ceilDiv(t-p.BaselineLookback, p.WindowMs)*p.WindowMs)
	if baselineFrom > 0 {
		first = max(first, ceilDiv(baselineFrom, p.WindowMs)*p.WindowMs)
	}
	last := floorDiv(t-p.BaselineExclude, p.WindowMs) * p.WindowMs
	var history []*windowStats
	for b := first; b < last; b += p.WindowMs {
		if h := e.bin(b); h.valid {
			history = append(history, h)
		}
	}

	base := Baseline{Ready: len(history) >= p.MinBaselineBins}
	denominator := 0
	for _, h := range history {
		denominator += h.u
	}
	w = domain.Window{T: t, N: cur.n, U: cur.u, Valid: cur.valid, BaselineReady: base.Ready, Signals: []string{}}
	if base.Ready {
		ns, us := make([]float64, len(history)), make([]float64, len(history))
		for i, h := range history {
			ns[i], us[i] = float64(h.n), float64(h.u)
		}
		base.N, base.U = median(ns), median(us)
		dev := make([]float64, len(history))
		for i, v := range ns {
			dev[i] = math.Abs(v - base.N)
		}
		mad := median(dev)
		base.Ratio = float64(cur.n) / math.Max(base.N, p.CountFloor)
		base.Z = (float64(cur.n) - base.N) / math.Max(math.Max(p.MADScale*mad, math.Sqrt(base.N+1)), 1)
		w.Ratio, w.Z, w.BaselineN = ptr(base.Ratio), ptr(base.Z), ptr(base.N)
	}

	signals := map[string]bool{}
	evidence := map[string]bool{}
	minN, minU, minReaction := e.minGate[0], e.minGate[1], e.minGate[2]
	if cur.valid && cur.n >= minN && cur.u >= minU {
		stats := WindowStats{N: cur.n, U: cur.u}
		for _, r := range e.rules.window {
			if r.Fires(stats, base, p) {
				signals[r.ID()] = true
				for _, id := range cur.ids {
					evidence[id] = true
				}
			}
		}
		for key, count := range cur.counts {
			rule, ok := e.rules.ruleOf(key).(MessageRule)
			if !ok {
				continue
			}
			share := float64(count) / float64(cur.u)
			historical := 0.0
			if denominator > 0 {
				sum := 0
				for _, h := range history {
					sum += h.counts[key]
				}
				historical = float64(sum) / float64(denominator)
			}
			increased := !base.Ready || share-historical >= p.ReactionShareIncrease-1e-12
			if count >= minReaction && share >= rule.Share(p) && increased {
				signals[key] = true
				for _, id := range cur.evidence[key] {
					evidence[id] = true
				}
				if w.ReactionRatio == nil {
					w.ReactionRatio = map[string]float64{}
				}
				w.ReactionRatio[key] = share
			}
		}
	}
	for k := range signals {
		w.Signals = append(w.Signals, k)
	}
	sort.Strings(w.Signals)
	for id := range evidence {
		w.Evidence = append(w.Evidence, id)
	}
	sort.Strings(w.Evidence)

	strong = base.Ready && cur.valid && cur.n >= p.StrongMinMessages && cur.u >= p.StrongMinAuthors &&
		base.Ratio >= p.StrongRatio && base.Z >= p.StrongZ

	// Bins that slid out of every future lookback are no longer needed.
	for start := range e.bins {
		if start < first-p.WindowMs {
			delete(e.bins, start)
		}
	}
	return w, strong
}

// median matches Python's statistics.median (mean of the middle pair).
func median(v []float64) float64 {
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}

func ceilDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a > 0 {
		q++
	}
	return q
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func ptr(v float64) *float64 { return &v }
