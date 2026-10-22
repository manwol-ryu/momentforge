package detect

import (
	"math"
	"slices"
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

	// Adaptive gate: the finished valid bins of the lookback in time order,
	// their message counts sorted (for the median), and the next bin to finish.
	recent  []finishedBin
	sorted  []int
	nextBin int64

	// Scratch sets reused by stats (cleared on each call).
	perAuthor map[string]int
	reacted   map[keyAuthor]bool
}

type position struct{ offset, seq int64 }

type entry struct {
	ev   domain.ChatEvent
	skip bool     // empty, ignored phrase, command, hidden by the platform, or not a viewer message
	keys []string // message-rule keys
}

// counted reports whether an event is a viewer's message: chat or the text
// of a donation. Subscription and system notices, and messages the platform
// hid (bot filter), are not reactions.
func counted(ev domain.ChatEvent) bool {
	if ev.Hidden {
		return false
	}
	switch ev.KindOrChat() {
	case domain.ChatKindChat, domain.ChatKindDonation:
		return true
	}
	return false
}

type windowStats struct {
	n, u     int
	valid    bool
	ids      []string            // counted event ids (current windows only)
	counts   map[string]int      // key → distinct authors
	evidence map[string][]string // key → counted event ids (current windows only)
}

// keyAuthor is one author's use of a reaction key within a window.
type keyAuthor struct{ key, author string }

// NewEngine validates params and rules.
func NewEngine(p Params, rules []Rule) (*Engine, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	rs, err := newRuleSet(rules)
	if err != nil {
		return nil, err
	}
	e := &Engine{p: p, rules: rs, ignored: map[string]bool{}, byID: map[string]position{}, bins: map[int64]*windowStats{},
		perAuthor: map[string]int{}, reacted: map[keyAuthor]bool{}}
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
	if len(e.byID) == 0 {
		e.byID = make(map[string]position, len(events)) // a VOD adds everything at once
	}
	e.events = slices.Grow(e.events, len(events))
	for _, ev := range events {
		text := Normalize(ev.Text)
		en := entry{ev: ev}
		en.ev.Text = text
		en.skip = text == "" || e.ignored[text] || strings.HasPrefix(text, "!") || strings.HasPrefix(text, "/") || !counted(ev)
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
// Dropped events are cleared in place and the slice is advanced past them;
// the next growing append moves only the kept ones to a new array, so a
// capture does not copy its whole horizon on every call.
func (e *Engine) Prune(before int64) {
	if cut := e.search(before); cut > 0 {
		for _, en := range e.events[:cut] {
			delete(e.byID, en.ev.ID)
		}
		clear(e.events[:cut])
		e.events = e.events[cut:]
	}
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

// stats counts messages in [start, end). History bins (withIDs false) keep
// only the numbers; the window being judged also lists its counted event
// ids, overall and per key, for evidence.
func (e *Engine) stats(start, end int64, withIDs bool) *windowStats {
	ws := &windowStats{valid: true, counts: map[string]int{}}
	if withIDs {
		ws.evidence = map[string][]string{}
	}
	for _, g := range e.gaps {
		if start < g.EndMs && end > g.StartMs {
			ws.valid = false
			break
		}
	}
	perAuthor, reacted := e.perAuthor, e.reacted
	clear(perAuthor)
	clear(reacted)
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
		ws.n++
		if withIDs {
			ws.ids = append(ws.ids, en.ev.ID)
		}
		for _, k := range en.keys {
			if ka := (keyAuthor{k, author}); !reacted[ka] {
				reacted[ka] = true
				ws.counts[k]++
			}
			if withIDs {
				ws.evidence[k] = append(ws.evidence[k], en.ev.ID)
			}
		}
	}
	ws.u = len(perAuthor)
	return ws
}

// bin returns the cached stats of the aligned history bin starting at start.
func (e *Engine) bin(start int64) *windowStats {
	if ws, ok := e.bins[start]; ok {
		return ws
	}
	ws := e.stats(start, start+e.p.WindowMs, false)
	e.bins[start] = ws
	return ws
}

// Evaluate judges the window [t-WindowMs, t). baselineFrom > 0 limits the
// history to bins starting at or after it (viewer-count epochs, phase 3).
// strong reports a window that may start a candidate on its own.
func (e *Engine) Evaluate(t, baselineFrom int64) (w domain.Window, strong bool) {
	p := e.p
	cur := e.stats(t-p.WindowMs, t, true)
	first := max(0, ceilDiv(t-p.BaselineLookback, p.WindowMs)*p.WindowMs)
	if baselineFrom > 0 {
		first = max(first, ceilDiv(baselineFrom, p.WindowMs)*p.WindowMs)
	}
	last := floorDiv(t-p.BaselineExclude, p.WindowMs) * p.WindowMs
	e.finishBins(last)
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
	open, minReaction := e.gate(cur, base, &w)
	if cur.valid && open {
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
			if count < minReaction || share < rule.Share(p) {
				continue
			}
			// The key's pooled share in history, only for keys that pass the cheap checks.
			if base.Ready {
				historical := 0.0
				if denominator > 0 {
					sum := 0
					for _, h := range history {
						sum += h.counts[key]
					}
					historical = float64(sum) / float64(denominator)
				}
				if share-historical < p.ReactionShareIncrease-1e-12 {
					continue
				}
			}
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

type finishedBin struct {
	start int64
	n     int
}

// finishBins records the message counts of bins that ended before the
// excluded recent span (bins start at multiples of WindowMs from 0) and
// forgets those older than GateLookbackMs. Recorded bins never change: VOD
// has all chat up front, and LIVE drops chat older than its watermark. Bins
// in gaps are left out.
func (e *Engine) finishBins(last int64) {
	p := e.p
	if p.GateBusyRatio <= 0 {
		return
	}
	for ; e.nextBin < last; e.nextBin += p.WindowMs {
		if h := e.bin(e.nextBin); h.valid {
			e.recent = append(e.recent, finishedBin{e.nextBin, h.n})
			e.sorted = slices.Insert(e.sorted, sort.SearchInts(e.sorted, h.n), h.n)
		}
	}
	for len(e.recent) > 0 && e.recent[0].start < last-p.GateLookbackMs {
		i := sort.SearchInts(e.sorted, e.recent[0].n)
		e.sorted = slices.Delete(e.sorted, i, i+1)
		e.recent = e.recent[1:]
	}
}

// gate reports whether window cur has enough chat for its signals to be
// judged, and how many authors a message rule then needs (REDESIGN 6.2.1).
// It records the adaptive thresholds in w.
//
// Fixed (CzzEdit, or sensitive): MinMessages/MinAuthors (or the sensitive
// values). Adaptive: closed until GateWarmupBins bins have finished (a fixed
// gate there would depend on the channel's size), then open when either
//   - busy for the broadcast: messages ≥ GateBusyRatio × the median of the
//     recent finished bins (at least the sensitive minimum, no upper cap),
//     with authors and reaction authors in the normal gate's proportion
//     (5 of 15);
//   - a sudden spike: authors ≥ GateSpikeRatio × the baseline's median
//     authors (at least the sensitive minimum), messages at least as many.
func (e *Engine) gate(cur *windowStats, base Baseline, w *domain.Window) (open bool, reaction int) {
	p := e.p
	minN, minU, minR := e.minGate[0], e.minGate[1], e.minGate[2]
	if p.GateBusyRatio <= 0 || p.Sensitive {
		return cur.n >= minN && cur.u >= minU, minR
	}
	if len(e.sorted) < p.GateWarmupBins {
		return false, minR // still learning what this channel's chat is like
	}
	scaled := func(n, full, floor int) int { return max((n*full+p.MinMessages-1)/p.MinMessages, floor) }
	mid := len(e.sorted) / 2
	typical := float64(e.sorted[mid])
	if len(e.sorted)%2 == 0 {
		typical = float64(e.sorted[mid-1]+e.sorted[mid]) / 2
	}
	busyN := max(int(math.Ceil(p.GateBusyRatio*typical-1e-9)), p.SensitiveMinMessages)
	w.Gate = busyN
	open = cur.n >= busyN && cur.u >= scaled(busyN, p.MinAuthors, p.SensitiveMinAuthors)
	reaction = scaled(busyN, p.MinReactionAuthors, p.SensitiveMinReaction)
	if base.Ready && p.GateSpikeRatio > 0 {
		spikeU := max(int(math.Ceil(p.GateSpikeRatio*base.U-1e-9)), p.SensitiveMinAuthors)
		w.SpikeGate = spikeU
		if cur.u >= spikeU && cur.n >= max(spikeU, p.SensitiveMinMessages) {
			open, reaction = true, min(reaction, p.SensitiveMinReaction)
		}
	}
	return open, reaction
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
