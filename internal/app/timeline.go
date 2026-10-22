package app

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// Timeline item sources.
const (
	SourceRule   = "rule"
	SourceViewer = "viewer_comment"
	SourceAI     = "ai"
)

// TimelineItem is one moment of the unified timeline, from any source.
type TimelineItem struct {
	ID          string // source:origin
	Source      string // rule | viewer_comment | ai
	OriginID    string // candidate, recommendation or AI recommendation ID
	AnchorMs    int64  // where to start watching
	ClipStartMs int64
	ClipEndMs   int64
	Title       string
	CommentText string // viewer recommendations: the comment
	Rationale   string // AI recommendations
	Uncertainty string // AI recommendations
	Priority    string // AI recommendations: high | medium | low
	// AI recommendations: the timeline items (rule or viewer) the AI explained;
	// the screen shows such a recommendation under those items.
	SourceCandidateIDs []string
	WatchURL           string
	// Cut plans (Kind "cut"): the spans to keep, the plan to open, whether both checks passed.
	Kind      string
	Spans     []TimelineSpan
	CutPlanID string
	Verified  bool
	Version   int
	// Cut plans: how far the searches went (rounds run, evidence they added)
	// and where the story the reaction calls back to begins, when found.
	SearchRounds  int
	SearchAdded   int
	StoryOriginMs int64
	HasOrigin     bool
	// Cut plans: everything the result box shows (6-D).
	Cut *CutDetail

	rule *domain.Candidate // rule candidates: the detector's candidate (AI inputs read it)
}

// TimelineSpan is one span of a cut plan.
type TimelineSpan struct {
	StartMs int64  `json:"startMs"`
	EndMs   int64  `json:"endMs"`
	Role    string `json:"role"`
}

// UnifiedTimeline merges a video's latest rule analysis, comment capture and
// AI run in time order (CzzEdit's 통합 타임라인); each item keeps its source.
// For a LIVE capture it holds the rule candidates and the latest AI run.
type UnifiedTimeline struct {
	SessionID  string
	Title      string
	DurationMs int64
	AnalysisID string
	CaptureID  string
	AIRunID    string
	Items      []TimelineItem

	maxSeq int64 // the analysis's chat snapshot (AI inputs read up to it)
}

// ruleItem is a rule candidate as a timeline item.
func ruleItem(c domain.Candidate, watch func(int64) string) TimelineItem {
	return TimelineItem{ID: SourceRule + ":" + c.ID, Source: SourceRule, OriginID: c.ID, AnchorMs: c.ClipStartMs,
		ClipStartMs: c.ClipStartMs, ClipEndMs: c.ClipEndMs, Title: strings.Join(c.Reasons, " · "), WatchURL: watch(c.ClipStartMs), rule: &c}
}

// Timeline builds the unified timeline of a VOD session or LIVE capture.
func (a *App) Timeline(ctx context.Context, sid string) (*UnifiedTimeline, error) {
	st := a.cfg.Store
	s, err := st.Session(ctx, sid)
	if err != nil {
		return nil, err
	}
	t := &UnifiedTimeline{SessionID: s.ID, Title: s.Title, DurationMs: s.DurationMs, Items: []TimelineItem{}}
	live := s.Kind == string(platform.KindLive)
	if s.Kind != string(platform.KindVOD) && !live {
		return t, nil
	}
	// A LIVE moment has no link of its own: the platform opens the broadcast now.
	link := a.linker(s)
	watch := func(ms int64) string {
		if link == nil || live {
			return ""
		}
		return link(ms)
	}
	if live {
		cands, _, err := st.LiveCandidates(ctx, sid, 0)
		if err != nil {
			return nil, err
		}
		for _, c := range shownLive(cands) {
			t.Items = append(t.Items, ruleItem(c, watch))
		}
	}
	if s.LatestAnalysisID != "" {
		an, err := a.analysisBrief(ctx, s.LatestAnalysisID)
		if err != nil {
			return nil, err
		}
		t.AnalysisID, t.maxSeq = an.ID, an.MaxSeq
		for _, c := range an.Candidates {
			t.Items = append(t.Items, ruleItem(c, watch))
		}
	}
	// AI results made against an earlier analysis find their candidates by time.
	hosts := newMomentHosts(t.AnalysisID, t.Items)
	if v, err := a.Comments(ctx, sid); err != nil {
		return nil, err
	} else if v != nil {
		t.CaptureID = v.CaptureID
		for _, r := range v.Recommendations {
			end := r.EndMs
			if end == 0 {
				end = min(s.DurationMs, r.StartMs+30000)
			}
			title := r.Description
			if title == "" {
				title = "시청자 추천 시각"
			}
			t.Items = append(t.Items, TimelineItem{ID: SourceViewer + ":" + r.ID, Source: SourceViewer, OriginID: r.ID,
				AnchorMs: r.StartMs, ClipStartMs: r.StartMs, ClipEndMs: end, Title: title, CommentText: r.CommentText,
				WatchURL: r.WatchURL})
		}
	}
	ai, err := a.aiTimelineItems(ctx, s.ID, watch, hosts)
	if err != nil {
		return nil, err
	}
	t.AIRunID = ai.runID
	t.Items = append(t.Items, ai.items...)
	cuts, err := a.cutTimelineItems(ctx, s.ID, hosts)
	if err != nil {
		return nil, err
	}
	t.Items = append(t.Items, cuts...)
	sort.SliceStable(t.Items, func(i, j int) bool {
		x, y := t.Items[i], t.Items[j]
		if x.AnchorMs != y.AnchorMs {
			return x.AnchorMs < y.AnchorMs
		}
		if x.Source != y.Source {
			return x.Source < y.Source
		}
		// Versions of one cut plan start together; IDs are random, so order them by version.
		if x.Cut != nil && y.Cut != nil && x.Cut.Version != y.Cut.Version {
			return x.Cut.Version < y.Cut.Version
		}
		return x.ID < y.ID
	})
	return t, nil
}

// analysisBrief is an analysis with its candidates but not its windows (a
// long broadcast has thousands), which the timeline does not need.
func (a *App) analysisBrief(ctx context.Context, id string) (*store.Analysis, error) {
	an, err := a.cfg.Store.AnalysisMeta(ctx, id)
	if err != nil {
		return nil, err
	}
	an.Candidates, err = a.cfg.Store.AnalysisCandidates(ctx, id)
	return an, err
}

// writeUnified rewrites exports/<session>/timeline/ after a source changed.
func (a *App) writeUnified(ctx context.Context, sid string) {
	f, ok, err := a.unifiedFile(ctx, sid)
	if err != nil {
		a.logError("build unified timeline", err)
		return
	}
	if !ok {
		return
	}
	if err := export.WriteTimeline(filepath.Join(a.exportDir(sid), "timeline"), f); err != nil {
		a.logError("write unified timeline", err)
	}
}

// unifiedFile is a VOD's unified timeline as written to timeline/ (ok is false for a LIVE capture).
func (a *App) unifiedFile(ctx context.Context, sid string) (export.TimelineFile, bool, error) {
	t, err := a.Timeline(ctx, sid)
	if err != nil {
		return export.TimelineFile{}, false, err
	}
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil || s.Kind != string(platform.KindVOD) {
		return export.TimelineFile{}, false, err
	}
	f := export.TimelineFile{SessionID: sid, VideoID: s.TargetID, Title: t.Title, DurationMs: t.DurationMs,
		AnalysisID: t.AnalysisID, CaptureID: t.CaptureID, AIRunID: t.AIRunID}
	for _, it := range t.Items {
		f.Items = append(f.Items, export.TimelineItem{ID: it.ID, Source: it.Source, OriginID: it.OriginID, AnchorMs: it.AnchorMs,
			ClipStartMs: it.ClipStartMs, ClipEndMs: it.ClipEndMs, Title: it.Title, CommentText: it.CommentText,
			Rationale: it.Rationale, Uncertainty: it.Uncertainty, WatchURL: it.WatchURL})
	}
	return f, true, nil
}

// aiItems are the AI part of the unified timeline.
type aiItems struct {
	runID string
	items []TimelineItem
}

// AI results name the timeline items they explain, but a rule candidate's
// ID is its number in one analysis (m0001… in time order): after the
// session is analysed again the same ID may name another moment. Runs and
// cut plans record the analysis their IDs belong to (migration 0012); while
// it is the current one the IDs stand, otherwise each result is matched to
// the current candidate by time and left on a row of its own when none fits.
const remapMinOverlapMs = 5000 // the clip overlap that ties a result to a candidate (or half the shorter span)

// momentHosts are the current rule candidates AI results can belong under.
type momentHosts struct {
	analysisID string
	rules      []ruleClip
}

type ruleClip struct {
	id         string // timeline item ID
	start, end int64
}

func newMomentHosts(analysisID string, items []TimelineItem) momentHosts {
	h := momentHosts{analysisID: analysisID}
	for _, it := range items {
		if it.Source == SourceRule {
			h.rules = append(h.rules, ruleClip{id: it.ID, start: it.ClipStartMs, end: it.ClipEndMs})
		}
	}
	return h
}

// currentHosts are the rule candidates the session's timeline lists now,
// for placing AI results outside Timeline (result details, MCP).
func (a *App) currentHosts(ctx context.Context, s store.Session) (momentHosts, error) {
	st := a.cfg.Store
	h := momentHosts{analysisID: s.LatestAnalysisID}
	var cands []domain.Candidate
	switch s.Kind {
	case string(platform.KindLive):
		live, _, err := st.LiveCandidates(ctx, s.ID, 0)
		if err != nil {
			return h, err
		}
		cands = shownLive(live)
	case string(platform.KindVOD):
	default:
		return h, nil
	}
	if s.LatestAnalysisID != "" {
		more, err := st.AnalysisCandidates(ctx, s.LatestAnalysisID)
		if err != nil {
			return h, err
		}
		cands = append(cands, more...)
	}
	for _, c := range cands {
		h.rules = append(h.rules, ruleClip{id: SourceRule + ":" + c.ID, start: c.ClipStartMs, end: c.ClipEndMs})
	}
	return h, nil
}

func (h momentHosts) has(id string) bool {
	for _, c := range h.rules {
		if c.id == id {
			return true
		}
	}
	return false
}

// hostsFor is where an AI result made for ids (against analysisID) belongs
// now. Viewer items keep their IDs. Rule IDs stand while their analysis is
// the current one; otherwise each is replaced by a current rule candidate
// whose clip overlaps [from, to) by remapMinOverlapMs or half the shorter
// span, the largest overlap first (ties: an original ID, then the clip that
// holds peakMs, then the earlier). from < 0 means the result has no span:
// its rule IDs are dropped unless their analysis is current. An empty
// result is a row of its own.
func (h momentHosts) hostsFor(ids []string, analysisID string, from, to, peakMs int64) []string {
	out := []string{}
	var rules []string
	for _, id := range ids {
		switch {
		case !strings.HasPrefix(id, SourceRule+":"):
			out = append(out, id)
		case analysisID == h.analysisID:
			if h.has(id) {
				out = append(out, id)
			}
		default:
			rules = append(rules, id)
		}
	}
	if len(rules) == 0 || from < 0 {
		return out
	}
	to = max(to, from+1)
	type match struct {
		c          ruleClip
		ov         int64
		orig, peak bool
	}
	var found []match
	for _, c := range h.rules {
		ov := min(to, c.end) - max(from, c.start)
		if ov <= 0 || ov < min(remapMinOverlapMs, (min(to-from, c.end-c.start)+1)/2) {
			continue
		}
		found = append(found, match{c: c, ov: ov, orig: slices.Contains(rules, c.id), peak: peakMs >= c.start && peakMs < c.end})
	}
	sort.SliceStable(found, func(i, j int) bool {
		x, y := found[i], found[j]
		switch {
		case x.ov != y.ov:
			return x.ov > y.ov
		case x.orig != y.orig:
			return x.orig
		case x.peak != y.peak:
			return x.peak
		}
		return x.c.start < y.c.start
	})
	for _, m := range found[:min(len(found), len(rules))] {
		out = append(out, m.c.id)
	}
	return out
}

// planHosts is where a stored cut plan belongs now (hostsFor), found by
// the span it is about (planMoment).
func (h momentHosts) planHosts(rec store.CutPlan, p cut.Plan) []string {
	from, to, ok := planMoment(p)
	if !ok {
		from = -1
	}
	return h.hostsFor(rec.CandidateIDs, rec.AnalysisID, from, to, p.Reaction.PeakMs)
}

// planMoment is the span a cut plan is about: the analysed candidate's clip
// (pipeline 4), else its chosen spans without the setup ones (a setup span
// may lie minutes before the moment), else all of them.
func planMoment(p cut.Plan) (from, to int64, ok bool) {
	if c := p.CandidateClip; c != nil && c.EndMs > c.StartMs {
		return c.StartMs, c.EndMs, true
	}
	var main []cut.Span
	for _, s := range p.SelectedSpans {
		if s.Role != "setup" {
			main = append(main, s)
		}
	}
	if len(main) == 0 {
		main = p.SelectedSpans
	}
	if len(main) == 0 {
		return 0, 0, false
	}
	return main[0].StartMs, main[len(main)-1].EndMs, true
}
