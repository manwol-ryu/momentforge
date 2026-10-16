package detect

import (
	"fmt"
	"sort"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Grouper turns evaluated windows into candidates. A finished VOD and an
// unbounded LIVE stream use the same state machine.
type Grouper struct {
	e        *Engine
	active   *group
	previous *domain.Window
	serial   int
}

type group struct {
	start, end int64
	id         string
	windows    []domain.Window
}

// NewGrouper returns a grouper that reads evidence order from e.
func NewGrouper(e *Engine) *Grouper { return &Grouper{e: e} }

// Step consumes the next window in time order. endMs bounds clips (the
// VOD length, or for LIVE the time the clip context will be available).
func (g *Grouper) Step(w domain.Window, strong bool, endMs int64) ([]domain.Candidate, error) {
	p := g.e.p
	var out []domain.Candidate
	emit := func(boundary int64, partial bool) error {
		c, err := g.candidate(g.active, endMs, boundary, partial)
		if err != nil || c == nil {
			return err
		}
		g.serial++
		out = append(out, *c)
		return nil
	}

	consecutive := g.previous != nil && g.previous.Valid && sharesSignal(w.Signals, g.previous.Signals)
	if !w.Valid {
		if g.active != nil {
			if err := emit(-1, true); err != nil {
				return nil, err
			}
			g.active = nil
		}
		g.previous = nil
		return out, nil
	}

	t := w.T
	switch {
	case g.active != nil && len(w.Signals) > 0 && t-p.WindowMs-g.active.end <= p.MergeGapMs:
		g.active.end = t
		g.active.windows = append(g.active.windows, w)
	case len(w.Signals) > 0 && (strong || consecutive):
		if g.active != nil {
			if err := emit(-1, false); err != nil {
				return nil, err
			}
		}
		from, windows := t, []domain.Window{w}
		if consecutive {
			from, windows = g.previous.T, []domain.Window{*g.previous, w}
		}
		start := max(0, from-p.WindowMs)
		g.active = &group{start: start, end: t, id: fmt.Sprintf("g%d", start), windows: windows}
	}

	if g.active != nil {
		for g.active.end-g.active.start > p.MaxSpanMs {
			boundary := g.active.start + p.MaxSpanMs
			if err := emit(boundary, false); err != nil {
				return nil, err
			}
			g.active.start = boundary
			kept := g.active.windows[:0]
			for _, aw := range g.active.windows {
				if aw.T > boundary {
					kept = append(kept, aw)
				}
			}
			g.active.windows = kept
		}
		if t >= g.active.end+max(p.CloseAfterMs, p.PostRollMs) {
			if err := emit(-1, false); err != nil {
				return nil, err
			}
			g.active = nil
		}
	}
	prev := w
	g.previous = &prev
	return out, nil
}

// Finish closes an open candidate at the end of the data (partial context).
func (g *Grouper) Finish(endMs int64) ([]domain.Candidate, error) {
	if g.active == nil {
		return nil, nil
	}
	c, err := g.candidate(g.active, endMs, -1, true)
	g.active = nil
	if err != nil || c == nil {
		return nil, err
	}
	g.serial++
	return []domain.Candidate{*c}, nil
}

// candidate builds the candidate for gr ending at boundary (-1: the group end).
func (g *Grouper) candidate(gr *group, endMs, boundary int64, partial bool) (*domain.Candidate, error) {
	p := g.e.p
	start := gr.start
	end := gr.end
	if boundary >= 0 {
		end = boundary
	}
	end = min(end, endMs)
	if end <= start {
		return nil, nil
	}
	var relevant []domain.Window
	for _, w := range gr.windows {
		if w.T > start && w.T-p.WindowMs < end {
			relevant = append(relevant, w)
		}
	}
	if len(relevant) == 0 {
		return nil, nil
	}
	peak := relevant[0]
	for _, w := range relevant[1:] {
		if peakLess(peak, w) {
			peak = w
		}
	}

	keys := map[string]bool{}
	evidence := map[string]position{}
	ready := true
	for _, w := range relevant {
		for _, k := range w.Signals {
			keys[k] = true
		}
		for _, id := range w.Evidence {
			if pos, ok := g.e.position(id); ok && pos.offset >= start && pos.offset < end {
				evidence[id] = pos
			}
		}
		ready = ready && w.BaselineReady
	}
	signals := sortedKeys(keys)
	reasonSet := map[string]bool{}
	for _, k := range signals {
		if r := g.e.rules.ruleOf(k); r != nil {
			reasonSet[r.Name()] = true
		}
	}
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := evidence[ids[i]], evidence[ids[j]]
		if a.offset != b.offset {
			return a.offset < b.offset
		}
		return a.seq < b.seq
	})

	clipStart := max(0, start-p.ReactionDelayMs-p.PreRollMs)
	clipEnd := min(endMs, end-p.ReactionDelayMs+p.PostRollMs)
	if clipEnd < clipStart {
		return nil, domain.NewUserError("INVALID_RANGE", "반응 보정값 때문에 추천 구간이 유효하지 않습니다.")
	}
	return &domain.Candidate{
		ID:              fmt.Sprintf("m%04d", g.serial+1),
		GroupID:         gr.id,
		ReactionStartMs: start,
		ReactionEndMs:   end,
		PeakMs:          min(end-1, max(start, peak.T-p.WindowMs/2)),
		ClipStartMs:     clipStart,
		ClipEndMs:       clipEnd,
		Signals:         signals,
		Reasons:         sortedKeys(reasonSet),
		Peak: domain.PeakStats{
			N: peak.N, U: peak.U, Ratio: peak.Ratio, Z: peak.Z,
			BaselineN: peak.BaselineN, ReactionRatio: peak.ReactionRatio,
		},
		BaselineReady:  ready,
		EvidenceIDs:    ids,
		PartialContext: partial || end+p.PostRollMs > endMs || start-p.ReactionDelayMs-p.PreRollMs < 0,
	}, nil
}

// peakLess orders windows by (signal count, ratio, authors, earlier time).
func peakLess(a, b domain.Window) bool {
	if len(a.Signals) != len(b.Signals) {
		return len(a.Signals) < len(b.Signals)
	}
	ra, rb := deref(a.Ratio), deref(b.Ratio)
	if ra != rb {
		return ra < rb
	}
	if a.U != b.U {
		return a.U < b.U
	}
	return a.T > b.T
}

func deref(v *float64) float64 {
	if v == nil {
		return 0
	}
	return *v
}

func sharesSignal(a, b []string) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
