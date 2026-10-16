package detect

import (
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// liveHorizonMs is how much past chat a LIVE analyzer keeps in memory.
const liveHorizonMs = 1_000_000

// LiveAnalyzer evaluates an unbounded stream with the same engine and
// grouping as VOD analysis (CzzEdit live_analyzer). Callers add only
// analyzable messages (not late, valid time) and advance a watermark that
// trails real time; candidates are released once their after-context exists.
type LiveAnalyzer struct {
	p         Params
	e         *Engine
	g         *Grouper
	viewers   *ViewerBaseline // nil without viewer adaptation
	watermark int64
	nextT     int64
	pending   []domain.Candidate
	last      *domain.Window
}

// NewLiveAnalyzer starts at startMs; earlier time is a gap (not observed).
func NewLiveAnalyzer(startMs int64, p Params, rules []Rule, viewerAdaptive bool) (*LiveAnalyzer, error) {
	e, err := NewEngine(p, rules)
	if err != nil {
		return nil, err
	}
	e.Gap(0, startMs)
	a := &LiveAnalyzer{p: p, e: e, g: NewGrouper(e), watermark: startMs,
		nextT: max(p.WindowMs, ceilDiv(startMs, p.StepMs)*p.StepMs)}
	if viewerAdaptive {
		a.viewers = NewViewerBaseline(nil)
	}
	return a, nil
}

// ContinueNumbering makes the next candidate ID follow n (resumed sessions).
func (a *LiveAnalyzer) ContinueNumbering(n int) { a.g.serial = max(a.g.serial, n) }

// Watermark is the time analysis has reached.
func (a *LiveAnalyzer) Watermark() int64 { return a.watermark }

// LastWindow is the most recent evaluation (nil before the first).
func (a *LiveAnalyzer) LastWindow() *domain.Window { return a.last }

// ViewerEpochs lists viewer-count reference changes (nil without adaptation).
func (a *LiveAnalyzer) ViewerEpochs() []ViewerEpoch {
	if a.viewers == nil {
		return nil
	}
	return a.viewers.Epochs()
}

// Add takes analyzable messages; ones before the watermark are ignored
// (already-decided windows are never revised).
func (a *LiveAnalyzer) Add(events []domain.ChatEvent) {
	kept := make([]domain.ChatEvent, 0, len(events))
	for _, e := range events {
		if e.OffsetMs >= a.watermark {
			kept = append(kept, e)
		}
	}
	a.e.Add(kept)
}

// Gap marks [start, end) as unobserved.
func (a *LiveAnalyzer) Gap(start, end int64) { a.e.Gap(start, end) }

// Sample adds a viewer-count observation.
func (a *LiveAnalyzer) Sample(s ViewerSample) {
	if a.viewers != nil {
		a.viewers.Append([]ViewerSample{s})
	}
}

func (a *LiveAnalyzer) evaluate(t int64) (domain.Window, bool) {
	var vs *domain.ViewerState
	from := int64(0)
	if a.viewers != nil {
		st := a.viewers.At(t)
		vs, from = &st, st.EpochStartMs
	}
	w, strong := a.e.Evaluate(t, from)
	w.Viewer = vs
	return w, strong
}

// Advance evaluates up to endMs and returns candidates whose after-context
// is complete (or everything open when final). Clips are cut at endMs, and
// candidates overlapping a gap or missing context are marked partial.
func (a *LiveAnalyzer) Advance(endMs int64, final bool) ([]domain.Candidate, error) {
	if endMs < a.watermark {
		return nil, nil
	}
	p := a.p
	for a.nextT <= endMs {
		t := a.nextT
		w, strong := a.evaluate(t)
		lw := w
		lw.Evidence = nil
		a.last = &lw
		found, err := a.g.Step(w, strong, t+p.PostRollMs)
		if err != nil {
			return nil, err
		}
		a.pending = append(a.pending, found...)
		a.nextT += p.StepMs
	}
	a.watermark = endMs
	if final {
		if endMs >= p.WindowMs && endMs%p.StepMs != 0 {
			w, strong := a.evaluate(endMs)
			found, err := a.g.Step(w, strong, endMs+p.PostRollMs)
			if err != nil {
				return nil, err
			}
			a.pending = append(a.pending, found...)
		}
		rest, err := a.g.Finish(endMs)
		if err != nil {
			return nil, err
		}
		a.pending = append(a.pending, rest...)
	}
	var out, waiting []domain.Candidate
	for _, c := range a.pending {
		due := c.ReactionEndMs + p.PostRollMs
		if !final && due > endMs {
			waiting = append(waiting, c)
			continue
		}
		c.ClipEndMs = min(c.ClipEndMs, endMs)
		if due > endMs || a.overlapsGap(c.ClipStartMs, c.ClipEndMs) {
			c.PartialContext = true
		}
		c.State = domain.CandidateConfirmed
		if c.PartialContext {
			c.State = domain.CandidatePartial
		}
		out = append(out, c)
	}
	a.pending = waiting
	a.e.Prune(endMs - liveHorizonMs)
	return out, nil
}

func (a *LiveAnalyzer) overlapsGap(start, end int64) bool {
	for _, g := range a.e.gaps {
		if g.StartMs < end && g.EndMs > start {
			return true
		}
	}
	return false
}

// Previews are candidates not yet released: ones waiting for after-context,
// and the one still growing.
func (a *LiveAnalyzer) Previews() ([]domain.Candidate, error) {
	var out []domain.Candidate
	for _, c := range a.pending {
		c.State = domain.CandidateWaitingContext
		c.ClipEndMs = min(c.ClipEndMs, a.watermark)
		c.PartialContext = true
		out = append(out, c)
	}
	if a.g.active != nil {
		c, err := a.g.candidate(a.g.active, a.watermark, -1, true)
		if err != nil {
			return nil, err
		}
		if c != nil {
			// Still growing while signals were seen within the last window.
			c.State = domain.CandidateWaitingContext
			if a.watermark <= c.ReactionEndMs+a.p.WindowMs {
				c.State = domain.CandidateGenerating
			}
			out = append(out, *c)
		}
	}
	return out, nil
}
