package cuteval

import (
	"slices"
	"strings"
)

// Methods compared on every case (docs/AI-CUT-ANALYSIS.md 9).
const (
	MethodDetector = "detector" // the rule candidate's own clip (chat statistics only)
	MethodExplain  = "explain"  // the first-generation AI review's range for the candidate
	MethodCut      = "cut"      // the newest cut plan, the boundary the AI chose
	// "cut:short", "cut:context", "cut:full_event": that plan's other boundary options.
)

// Span is a kept part of the video.
type Span struct {
	StartMs int64
	EndMs   int64
}

// Attempt is what one method produced for a case's moment.
type Attempt struct {
	Method  string
	Spans   []Span
	IsCut   bool     // a cut plan: cause, origin, ending and searches are scored
	PlanID  string   // cut plans
	Version int      // cut plans
	State   string   // cut plans: ok | verify_failed | failed
	Sources []string // cut plans: utterances | chat | comment
	// CauseMs are the times of the leading cause's supporting evidence.
	CauseMs []int64
	// OriginMs is where the plan says the story begins; nil when it found none.
	OriginMs *int64
	Ending   string
	Searches int // search rounds run
	Added    int // evidence the searches added
}

// Transcript says whether the plan read what the broadcaster said.
func (a Attempt) Transcript() bool { return slices.Contains(a.Sources, "utterances") }

// Result is one attempt scored against a case.
type Result struct {
	Case    Case
	Attempt Attempt
	Missing bool // the method left nothing for this moment (no clip, no plan, no spans)

	StartOK, EndOK bool
	// BoundaryErrMs is how far the start and end are outside the allowed
	// windows, added up: the editing the clip still needs.
	BoundaryErrMs int64
	// Coverage is the share of the must-include time the clip keeps (1 when
	// the case lists none); Kept counts the spans kept at 90% or more.
	Coverage float64
	Kept     int
	LengthMs int64

	// Cut plans only (nil for the other methods or when the case skips it).
	CauseOK  *bool
	OriginOK *bool
	EndingOK *bool
}

// Score compares an attempt with the case's answer.
func Score(c Case, a Attempt) Result {
	r := Result{Case: c, Attempt: a}
	spans := merged(a.Spans)
	if len(spans) == 0 {
		r.Missing = true
		return r
	}
	g := c.Gold
	start, end := spans[0].StartMs, spans[len(spans)-1].EndMs
	r.StartOK, r.EndOK = g.Start.has(start), g.End.has(end)
	r.BoundaryErrMs = outside(start, g.Start) + outside(end, g.End)
	for _, s := range spans {
		r.LengthMs += s.EndMs - s.StartMs
	}
	r.Coverage = 1
	if len(g.MustInclude) > 0 {
		var need, got int64
		for _, w := range g.MustInclude {
			n := int64(w.To - w.From)
			k := kept(spans, int64(w.From), int64(w.To))
			need, got = need+n, got+k
			if n == 0 || float64(k) >= 0.9*float64(n) {
				r.Kept++
			}
		}
		if need > 0 {
			r.Coverage = float64(got) / float64(need)
		}
	}
	if !a.IsCut {
		return r
	}
	ok := false
	for _, t := range a.CauseMs {
		for _, w := range g.Cause {
			if w.has(t) {
				ok = true
			}
		}
	}
	r.CauseOK = &ok
	origin := false
	switch {
	case g.Origin == nil:
		origin = a.OriginMs == nil
	case a.OriginMs != nil:
		origin = g.Origin.has(*a.OriginMs)
	}
	r.OriginOK = &origin
	if g.Ending != "" {
		e := a.Ending == g.Ending
		r.EndingOK = &e
	}
	return r
}

// outside is how far t is from w (0 inside).
func outside(t int64, w Window) int64 {
	switch {
	case t < int64(w.From):
		return int64(w.From) - t
	case t > int64(w.To):
		return t - int64(w.To)
	}
	return 0
}

// merged sorts spans and joins the ones that overlap.
func merged(in []Span) []Span {
	var out []Span
	for _, s := range in {
		if s.EndMs > s.StartMs {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b Span) int { return int(a.StartMs - b.StartMs) })
	var res []Span
	for _, s := range out {
		if n := len(res); n > 0 && s.StartMs <= res[n-1].EndMs {
			res[n-1].EndMs = max(res[n-1].EndMs, s.EndMs)
			continue
		}
		res = append(res, s)
	}
	return res
}

// kept is how much of [from, to] the spans hold.
func kept(spans []Span, from, to int64) int64 {
	var n int64
	for _, s := range spans {
		a, b := max(s.StartMs, from), min(s.EndMs, to)
		if b > a {
			n += b - a
		}
	}
	return n
}

// Boundary names a cut plan's boundary option the way the screen does.
func Boundary(method string) string {
	switch strings.TrimPrefix(method, MethodCut+":") {
	case "short":
		return "짧게"
	case "context":
		return "맥락"
	case "full_event":
		return "전체"
	}
	return ""
}
