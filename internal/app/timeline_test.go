package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

func TestHostsForFindsTheMomentByTime(t *testing.T) {
	h := momentHosts{analysisID: "a2", rules: []ruleClip{
		{id: "rule:m0001", start: 30_000, end: 90_000},
		{id: "rule:m0002", start: 265_000, end: 355_000},
		{id: "rule:m0003", start: 356_000, end: 400_000},
	}}
	for _, tc := range []struct {
		name           string
		ids            []string
		analysis       string
		from, to, peak int64
		want           []string
	}{
		{"the current analysis keeps its IDs", []string{"rule:m0001"}, "a2", 265_000, 355_000, -1, []string{"rule:m0001"}},
		{"the current analysis drops an ID it lacks", []string{"rule:m0009"}, "a2", 265_000, 355_000, -1, []string{}},
		{"an earlier analysis is matched by time", []string{"rule:m0001"}, "a1", 265_000, 355_000, -1, []string{"rule:m0002"}},
		{"a short result inside a clip", []string{"rule:m0001"}, "a1", 300_000, 301_000, -1, []string{"rule:m0002"}},
		{"a brush of the edge is not enough", []string{"rule:m0001"}, "a1", 352_000, 420_000, -1, []string{"rule:m0003"}},
		{"nothing near: a row of its own", []string{"rule:m0001"}, "a1", 120_000, 200_000, -1, []string{}},
		{"the largest overlap wins", []string{"rule:m0007"}, "a1", 340_000, 380_000, -1, []string{"rule:m0003"}},
		{"a tie goes to the original ID", []string{"rule:m0003"}, "a1", 350_000, 361_000, -1, []string{"rule:m0003"}},
		{"then to the clip that holds the peak", []string{"rule:m0009"}, "a1", 350_000, 361_000, 358_000, []string{"rule:m0003"}},
		{"then to the earlier", []string{"rule:m0009"}, "a1", 350_000, 361_000, -1, []string{"rule:m0002"}},
		{"viewer items keep their IDs", []string{"viewer_comment:x", "rule:m0001"}, "a1", 265_000, 355_000, -1,
			[]string{"viewer_comment:x", "rule:m0002"}},
		{"two candidates explained together", []string{"rule:m0001", "rule:m0002"}, "a1", 60_000, 300_000, -1,
			[]string{"rule:m0002", "rule:m0001"}},
		{"no span: rule IDs of another analysis go", []string{"rule:m0001", "viewer_comment:x"}, "a1", -1, -1, -1,
			[]string{"viewer_comment:x"}},
		{"a new moment names nothing", nil, "a1", 265_000, 355_000, -1, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.hostsFor(tc.ids, tc.analysis, tc.from, tc.to, tc.peak); !slices.Equal(got, tc.want) {
				t.Fatalf("hostsFor = %v, want %v", got, tc.want)
			}
		})
	}
	// A LIVE capture has no analysis: its candidate IDs never change.
	live := momentHosts{rules: []ruleClip{{id: "rule:m0002", start: 0, end: 10_000}}}
	if got := live.hostsFor([]string{"rule:m0002"}, "", 50_000, 60_000, -1); !slices.Equal(got, []string{"rule:m0002"}) {
		t.Fatalf("live = %v", got)
	}
}

// reanalyse saves a newer analysis of the sample in which an earlier
// moment comes first: the reaction that was m0001 is m0002 now, and m0001
// names the new, earlier moment.
func reanalyse(t *testing.T, a *App, sid string) {
	t.Helper()
	ctx := context.Background()
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	an, err := a.cfg.Store.Analysis(ctx, s.LatestAnalysisID)
	if err != nil || len(an.Candidates) != 1 || an.Candidates[0].ID != "m0001" {
		t.Fatalf("the sample's analysis: %+v %v", an, err)
	}
	moved := an.Candidates[0]
	moved.ID = "m0002"
	early := domain.Candidate{ID: "m0001", GroupID: "g40000", ReactionStartMs: 40_000, ReactionEndMs: 60_000, PeakMs: 50_000,
		ClipStartMs: 30_000, ClipEndMs: 90_000, Signals: []string{"laugh"}, Reasons: []string{"웃음"}, Strength: domain.StrengthWeak, Score: 3.1}
	next := &store.Analysis{SessionID: sid, RulesVersion: an.RulesVersion, ParamsJSON: an.ParamsJSON, DurationMs: an.DurationMs,
		MaxSeq: an.MaxSeq, EventCount: an.EventCount, DataSHA256: an.DataSHA256, SourceState: an.SourceState, Windows: an.Windows,
		Candidates: []domain.Candidate{early, moved}, CreatedAt: an.CreatedAt.Add(time.Hour).Truncate(time.Second)}
	if err := a.cfg.Store.SaveAnalysis(ctx, next); err != nil {
		t.Fatal(err)
	}
}

func cutItems(tl *UnifiedTimeline) []TimelineItem {
	var out []TimelineItem
	for _, it := range tl.Items {
		if it.Kind == "cut" {
			out = append(out, it)
		}
	}
	return out
}

// After "다시 분석" of the session renumbers its candidates, AI results stay
// with their moment: cut plans and explanations fold under the candidate
// that is now that moment, a new analysis of it continues the plan's
// versions with its notes, and the candidate that took the old number is a
// moment of its own.
func TestAIResultsFollowTheirMomentAfterReanalysis(t *testing.T) {
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	a.SetAIKey("fakeai", "k", false)
	analyse := func(item, continuePlan string) AIPreview {
		t.Helper()
		req := AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{item}}
		if continuePlan != "" {
			req.ContinuePlans = map[string]string{item: continuePlan}
		}
		pv, err := a.PreviewAI(ctx, req)
		if err != nil {
			t.Fatal(err)
		}
		id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, true)
		if err != nil {
			t.Fatal(err)
		}
		if v := waitAI(t, a, id); v.State != store.AICompleted {
			t.Fatalf("run %+v", v)
		}
		waitIdle(t, a)
		return pv
	}
	analyse("rule:m0001", "")
	v1 := lastPlan(t, a, demo.ID)
	if v1.CandidateClip == nil || v1.CandidateClip.StartMs != 265_000 {
		t.Fatalf("the plan does not keep its candidate's clip: %+v", v1.CandidateClip)
	}
	if _, err := a.AddCutNote(ctx, v1.ID, "두 번째 시도라 앞선 약속과 연결하면 안 됨", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmCut(ctx, v1.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	// A first-generation explanation of the same candidate, made against the first analysis.
	s, _ := a.cfg.Store.Session(ctx, demo.ID)
	run, err := a.cfg.Store.CreateAIRun(ctx, store.AIRun{SessionID: demo.ID, Provider: "fakeai", Model: "m", AnalysisID: s.LatestAnalysisID})
	if err != nil {
		t.Fatal(err)
	}
	run.State = store.AICompleted
	run.RecommendationsJSON = ai.Encode([]ai.Recommendation{
		{ID: "explained", SourceCandidateIDs: []string{"rule:m0001"}, StartMs: 296_000, EndMs: 320_000, Title: "웃음", Priority: "high"},
		{ID: "found", SourceCandidateIDs: []string{}, StartMs: 500_000, EndMs: 510_000, Title: "새 장면", Priority: "low"},
	})
	if err := a.cfg.Store.SaveAIRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	tl, _ := a.Timeline(ctx, demo.ID)
	if cuts := cutItems(tl); len(cuts) != 1 || !slices.Equal(cuts[0].SourceCandidateIDs, []string{"rule:m0001"}) || cuts[0].Cut.AnalyzedCandidateIDs != nil {
		t.Fatalf("before: %+v", cuts)
	}

	reanalyse(t, a, demo.ID)
	tl, _ = a.Timeline(ctx, demo.ID)
	cuts := cutItems(tl)
	if len(cuts) != 1 || !slices.Equal(cuts[0].SourceCandidateIDs, []string{"rule:m0002"}) ||
		!slices.Equal(cuts[0].Cut.CandidateIDs, []string{"rule:m0002"}) || !slices.Equal(cuts[0].Cut.AnalyzedCandidateIDs, []string{"rule:m0001"}) {
		t.Fatalf("the plan did not follow its moment: %+v", cuts)
	}
	for _, it := range tl.Items {
		switch it.ID {
		case "ai:explained":
			if !slices.Equal(it.SourceCandidateIDs, []string{"rule:m0002"}) {
				t.Fatalf("explanation %+v", it)
			}
		case "ai:found":
			if len(it.SourceCandidateIDs) != 0 {
				t.Fatalf("a new moment got a host: %+v", it)
			}
		}
	}
	// The result list (and MCP) agree with the timeline.
	details, err := a.CutPlanDetails(ctx, demo.ID, false)
	if err != nil || len(details) != 1 || !slices.Equal(details[0].CandidateIDs, []string{"rule:m0002"}) {
		t.Fatalf("details %+v %v", details, err)
	}
	if d, err := a.CutPlanDetail(ctx, v1.ID); err != nil || !slices.Equal(d.CandidateIDs, []string{"rule:m0002"}) {
		t.Fatalf("detail %+v %v", d, err)
	}

	// Analysing the moment under its new number makes v2 of the same plan, planned with the note.
	if pv := analyse("rule:m0002", ""); pv.Cut.NoteCount != 1 || pv.Cut.Inputs[0].Candidates[0].Reaction == nil {
		t.Fatalf("preview %+v", pv.Cut)
	}
	v2 := lastPlan(t, a, demo.ID)
	if v2.Version != 2 || v2.Supersedes != v1.ID || len(v2.NotesApplied) != 1 || !slices.Equal(v2.CandidateIDs, []string{"rule:m0002"}) {
		t.Fatalf("v2 %+v", v2)
	}
	tl, _ = a.Timeline(ctx, demo.ID)
	cuts = cutItems(tl)
	if len(cuts) != 2 || !cuts[0].Cut.Confirmed || cuts[1].Cut.KeptVersion != 1 {
		t.Fatalf("versions %+v", cuts)
	}
	for _, c := range cuts {
		if !slices.Equal(c.SourceCandidateIDs, []string{"rule:m0002"}) {
			t.Fatalf("version %d is under %v", c.Version, c.SourceCandidateIDs)
		}
	}
	// Confirming v2 releases v1, though v1 was made for another candidate number.
	if _, err := a.ConfirmCut(ctx, v2.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	if confirmed, _ := a.cfg.Store.ConfirmedCutPlans(ctx, demo.ID); len(confirmed) != 1 || confirmed[0].ID != v2.ID {
		t.Fatalf("confirmed %+v", confirmed)
	}

	// The candidate that took the old number is another moment: its plan starts anew, without the note.
	if pv := analyse("rule:m0001", ""); pv.Cut.NoteCount != 0 {
		t.Fatalf("the other moment got the note: %+v", pv.Cut)
	}
	other := lastPlan(t, a, demo.ID)
	if other.Version != 1 || len(other.NotesApplied) != 0 {
		t.Fatalf("other moment %+v", other)
	}
	// The screen can name the plan a new analysis continues.
	if pv := analyse("rule:m0001", v1.ID); pv.Cut.NoteCount != 1 {
		t.Fatalf("continue %+v", pv.Cut)
	}
	if p := lastPlan(t, a, demo.ID); p.Version != 3 || p.Supersedes != v2.ID {
		t.Fatalf("continued %+v", p)
	}
	_, err = a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{"rule:m0001"},
		ContinuePlans: map[string]string{"rule:m0001": "nope"}})
	if !isCode(err, "INPUT") || !strings.Contains(err.Error(), "이어서 분석할") {
		t.Fatalf("unknown plan: %v", err)
	}
}
