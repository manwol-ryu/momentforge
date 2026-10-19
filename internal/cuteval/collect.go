package cuteval

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/app"
)

// Reader is what the evaluation reads from the app (the same read-only
// use cases the MCP server calls).
type Reader interface {
	ExploreSessions(ctx context.Context, includeHidden bool) ([]app.SessionInfo, error)
	Timeline(ctx context.Context, sid string) (*app.UnifiedTimeline, error)
	CutPlanDetails(ctx context.Context, sid string, all bool) ([]app.CutDetail, error)
}

// Moment is a case resolved against the saved data.
type Moment struct {
	SessionID string
	Session   string // title
	Candidate app.TimelineItem
	Attempts  []Attempt
}

// FindSession picks the newest visible or hidden session the reference names.
func FindSession(ctx context.Context, r Reader, ref SessionRef) (app.SessionInfo, error) {
	list, err := r.ExploreSessions(ctx, true)
	if err != nil {
		return app.SessionInfo{}, err
	}
	for _, s := range list { // newest first
		switch {
		case ref.Demo && s.IsDemo:
		case ref.Video != "" && s.Platform+":"+s.TargetID == ref.Video:
		case ref.Title != "" && strings.TrimSpace(s.Title) == strings.TrimSpace(ref.Title):
		default:
			continue
		}
		return s, nil
	}
	return app.SessionInfo{}, fmt.Errorf("no saved session for %+v", ref)
}

// Resolve finds the case's moment and every method's attempt at it.
func Resolve(ctx context.Context, r Reader, c Case) (Moment, error) {
	s, err := FindSession(ctx, r, c.Session)
	if err != nil {
		return Moment{}, err
	}
	tl, err := r.Timeline(ctx, s.ID)
	if err != nil {
		return Moment{}, err
	}
	at := int64(c.MomentMs)
	var cand *app.TimelineItem
	best := int64(60_000) + 1
	for i, it := range tl.Items {
		if it.Source != app.SourceRule || it.Kind == "cut" {
			continue
		}
		d := outside(at, Window{From: Ms(it.ClipStartMs), To: Ms(it.ClipEndMs)})
		if d < best {
			cand, best = &tl.Items[i], d
		}
	}
	if cand == nil {
		return Moment{}, fmt.Errorf("%s: no detected candidate within a minute of %s in %q", c.ID, Timecode(at), s.Title)
	}
	m := Moment{SessionID: s.ID, Session: s.Title, Candidate: *cand}
	m.Attempts = append(m.Attempts, Attempt{Method: MethodDetector, Spans: []Span{{cand.ClipStartMs, cand.ClipEndMs}}})

	// The first-generation review: the AI range that explains this candidate
	// (or overlaps its clip the most).
	explain := Attempt{Method: MethodExplain}
	var overlap int64 = -1
	for _, it := range tl.Items {
		if it.Source != app.SourceAI || it.Kind == "cut" {
			continue
		}
		o := kept([]Span{{it.ClipStartMs, it.ClipEndMs}}, cand.ClipStartMs, cand.ClipEndMs)
		if slices.Contains(it.SourceCandidateIDs, cand.ID) {
			o += 1 << 40
		}
		if o > 0 && o > overlap {
			overlap, explain.Spans = o, []Span{{it.ClipStartMs, it.ClipEndMs}}
		}
	}
	m.Attempts = append(m.Attempts, explain)

	// The newest cut plan for this candidate, with each of its boundary options.
	details, err := r.CutPlanDetails(ctx, s.ID, true)
	if err != nil {
		return Moment{}, err
	}
	var plan *app.CutDetail
	for i, d := range details {
		if slices.Contains(d.CandidateIDs, cand.ID) && (plan == nil || d.Version > plan.Version) {
			plan = &details[i]
		}
	}
	if plan == nil {
		m.Attempts = append(m.Attempts, Attempt{Method: MethodCut, IsCut: true})
		return m, nil
	}
	base := cutAttempt(*plan)
	chosen := base
	chosen.Method, chosen.Spans = MethodCut, boundarySpans(*plan, plan.Chosen)
	m.Attempts = append(m.Attempts, chosen)
	for _, name := range []string{"short", "context", "full_event"} {
		a := base
		a.Method, a.Spans = MethodCut+":"+name, boundarySpans(*plan, name)
		m.Attempts = append(m.Attempts, a)
	}
	return m, nil
}

func cutAttempt(d app.CutDetail) Attempt {
	a := Attempt{IsCut: true, PlanID: d.PlanID, Version: d.Version, State: d.State, Sources: d.Sources,
		Ending: d.Ending.State, Searches: d.SearchRounds, Added: d.SearchAdded}
	if len(d.Causes) > 0 {
		for _, e := range d.Causes[0].Support {
			if e.AtMs != nil {
				a.CauseMs = append(a.CauseMs, *e.AtMs)
			}
		}
	}
	if d.Origin != nil && d.Origin.AtMs != nil {
		t := *d.Origin.AtMs
		a.OriginMs = &t
	}
	return a
}

func boundarySpans(d app.CutDetail, name string) []Span {
	for _, b := range d.Boundaries {
		if b.Name != name {
			continue
		}
		out := make([]Span, 0, len(b.Spans))
		for _, s := range b.Spans {
			out = append(out, Span{s.StartMs, s.EndMs})
		}
		return out
	}
	return nil
}

// Run resolves and scores every case. A case whose session or moment is
// not in the data is reported in skipped, not scored.
func Run(ctx context.Context, r Reader, cases []Case) (results []Result, skipped []string, err error) {
	for _, c := range cases {
		m, err := Resolve(ctx, r, c)
		if err != nil {
			if ctx.Err() != nil {
				return nil, nil, ctx.Err()
			}
			skipped = append(skipped, fmt.Sprintf("%s: %v", c.ID, err))
			continue
		}
		for _, a := range m.Attempts {
			results = append(results, Score(c, a))
		}
	}
	return results, skipped, nil
}
