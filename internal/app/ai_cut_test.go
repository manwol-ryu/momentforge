package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// stepAI answers the cut analysis like the E2E fake: the earliest "벌칙"
// utterance is the required setup; with dropSetup it leaves that span out,
// so the structural check fails twice and the plan is saved as failed.
type stepAI struct {
	fakeAI
	dropSetup bool
	keyAside  bool // the clip check passes, filing a key ref as a side remark
	// The first source check fails and the re-plan that follows leaves the
	// setup out (a plan and a verdict of different attempts must not mix).
	sourceFailsOnce bool
	badAnalyze      string // the candidate whose analysis answers in the wrong shape
	steps           []string
	notes           []cut.UserNote // what the last plan step was given
}

func (f *stepAI) Step(ctx context.Context, key, model string, step ai.StepRequest) (json.RawMessage, ai.Usage, error) {
	f.mu.Lock()
	f.steps = append(f.steps, step.Name)
	plans, sources := 0, 0
	for _, s := range f.steps {
		switch s {
		case "plan":
			plans++
		case "verify_source":
			sources++
		}
	}
	f.mu.Unlock()
	u := ai.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}
	var out any
	switch step.Name {
	case "analyze":
		in := step.Input.(cut.Input)
		if in.Candidates[0].ID == f.badAnalyze {
			return json.RawMessage(`{"target":""}`), u, nil
		}
		out = map[string]any{"target": "약속과 실패의 대비", "cause_status": "likely", "questions": []string{"'아까'는 무엇인가"},
			"search_requests": []any{map[string]any{"tool": "search", "query": "벌칙", "in": "both", "before_ms": in.WindowStartMs, "at_ms": 0, "from_ms": 0, "to_ms": 0, "window_s": 0}},
			"causes": []any{map[string]any{"kind": "callback_to_earlier", "summary": "앞선 장담", "support": []string{in.Evidence[0].Ref},
				"against": []string{}, "direction": "broadcast_to_viewers"}}}
	case "plan":
		in := step.Input.(map[string]any)["input"].(cut.Input)
		f.mu.Lock()
		f.notes, _ = step.Input.(map[string]any)["user_notes"].([]cut.UserNote)
		f.mu.Unlock()
		var promise, first, last cut.Evidence
		for _, e := range in.Evidence {
			if e.Kind != cut.KindUtterance {
				continue
			}
			if promise.Ref == "" && strings.Contains(e.Text, "벌칙") {
				promise = e
			}
			if first.Ref == "" && e.StartMs >= in.Candidates[0].ClipStartMs {
				first = e
			}
			if e.StartMs <= in.Candidates[0].ClipEndMs {
				last = e
			}
		}
		spans := []any{map[string]any{"start_ref": first.Ref, "end_ref": last.Ref, "role": "main"}}
		if !f.dropSetup && !(f.sourceFailsOnce && plans > 1) {
			spans = append([]any{map[string]any{"start_ref": promise.Ref, "end_ref": promise.Ref, "role": "setup"}}, spans...)
		}
		out = map[string]any{"story_origin_ref": promise.Ref, "origin_confidence": "found_in_search_range", "event_start_ref": first.Ref,
			"analysis_evidence_refs": []string{promise.Ref, first.Ref, last.Ref}, "required_context_refs": []string{promise.Ref, first.Ref},
			"optional_context_refs": []string{}, "boundary_candidates": []any{map[string]any{"name": "context", "spans": spans}},
			"chosen": "context", "start_reason": "약속부터", "end_reason": "인정까지",
			"ending": map[string]any{"state": "confirmed", "event_kind": "promise_penalty", "checks": []string{"결과 확인"}}, "unresolved_questions": []string{}}
	case "verify_clip":
		asides := []any{}
		if f.keyAside {
			for _, s := range step.Input.(map[string]any)["spans"].([]cut.ClipSpan) {
				for _, e := range s.Evidence {
					if e.Key && len(asides) == 0 {
						asides = append(asides, map[string]any{"refs": []string{e.Ref, "chat:nowhere"}, "text": "'아까'가 가리키는 일"})
					}
				}
			}
		}
		out = map[string]any{"pass": true, "answers": map[string]string{"what": "도전", "condition": "지면 벌칙", "result": "실패", "references": "약속", "why_reaction": "대비"},
			"gaps": []string{}, "asides": asides}
	case "verify_source":
		out = map[string]any{"pass": true, "mixed_attempt": false, "cut_condition": false, "missed_correction": false, "ended_early": false, "notes": []string{}}
		if f.sourceFailsOnce && sources == 1 {
			out = map[string]any{"pass": false, "mixed_attempt": false, "cut_condition": false, "missed_correction": false, "ended_early": true,
				"notes": []string{"벌칙을 받아들이는 말 전에 끊음"}}
		}
	}
	b, _ := json.Marshal(out)
	return b, u, nil
}

func TestCutAnalysisSavesAPlanWithSpans(t *testing.T) {
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := a.Timeline(ctx, demo.ID)
	item := tl.Items[0].ID
	// Scopes other than picked candidates are refused for now; a provider without Step too.
	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", StartMs: 0, EndMs: 60000}); !isCode(err, "INPUT") {
		t.Fatalf("range: %v", err)
	}
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{item}})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Cut == nil || len(pv.Cut.Inputs) != 1 || !pv.Cut.HasTranscript || pv.Cut.UtteranceCount == 0 || pv.Plan.Kind != store.AIKindCut ||
		pv.Plan.RequestCount != cut.MaxRequests(cut.DefaultBudget()) || !pv.Cut.SearchExecuted || pv.Cut.SearchRounds != cut.DefaultSearchRounds || len(f.steps) != 0 {
		t.Fatalf("preview %+v cut %+v", pv.Plan, pv.Cut)
	}
	// The window holds the planted promise utterance and the callback chat, as the sample promises.
	var hasPromise, hasCallback bool
	for _, e := range pv.Cut.Inputs[0].Evidence {
		hasPromise = hasPromise || (e.Kind == cut.KindUtterance && strings.Contains(e.Text, "벌칙으로 노래"))
		hasCallback = hasCallback || (e.Kind == cut.KindChat && strings.Contains(e.Text, "아까 벌칙"))
	}
	if !hasPromise || !hasCallback {
		t.Fatalf("window lacks the planted case: promise %v callback %v", hasPromise, hasCallback)
	}
	a.SetAIKey("fakeai", "k", false)
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, id)
	if v.State != store.AICompleted || v.Kind != store.AIKindCut || v.Recommendations != 1 || v.CompletedRequests != 4 {
		t.Fatalf("run %+v steps %v", v, f.steps)
	}
	if strings.Join(f.steps, ",") != "analyze,plan,verify_clip,verify_source" {
		t.Fatalf("steps %v", f.steps)
	}
	plans, err := a.CutPlans(ctx, demo.ID)
	if err != nil || len(plans) != 1 || plans[0].State != cut.StateOK || plans[0].Version != 1 {
		t.Fatalf("plans %+v err %v", plans, err)
	}
	_, raw, err := a.CutPlan(ctx, plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var p cut.Plan
	if json.Unmarshal(raw, &p) != nil || len(p.SelectedSpans) != 2 || p.SelectedSpans[0].Role != "setup" || p.Times.StoryOriginMs != 220000 ||
		len(p.SearchedRanges) != 1 || p.Verification.ClipOnly == nil || !p.Verification.ClipOnly.Pass {
		t.Fatalf("plan %s", raw)
	}
	// The plan folds under its candidate on the timeline and is exported for an editor.
	tl, _ = a.Timeline(ctx, demo.ID)
	var cutItem *TimelineItem
	for i := range tl.Items {
		if tl.Items[i].Kind == "cut" {
			cutItem = &tl.Items[i]
		}
	}
	if cutItem == nil || len(cutItem.Spans) != 2 || !cutItem.Verified || cutItem.SourceCandidateIDs[0] != item {
		t.Fatalf("timeline item %+v", cutItem)
	}
	dir := filepath.Join(a.exportDir(demo.ID), "cuts", plans[0].ID)
	csv, err := os.ReadFile(filepath.Join(dir, "cuts.csv"))
	if err != nil || !strings.Contains(string(csv), ",setup,") {
		t.Fatalf("cuts.csv %q %v", csv, err)
	}
	if edl, _ := os.ReadFile(filepath.Join(dir, "cuts.edl")); !strings.Contains(string(edl), "TITLE: MomentForge") || !strings.Contains(string(edl), "00:03:39:21") { // 0.3 s before the promise utterance
		t.Fatalf("cuts.edl %q", edl)
	}
	run, _ := a.cfg.Store.AIRun(ctx, id)
	var sent []sentLogEntry
	if json.Unmarshal(run.SentLogJSON, &sent) != nil || len(sent) != 4 || sent[0].Step != "analyze" || sent[0].Refs == 0 {
		t.Fatalf("sent log %s", run.SentLogJSON)
	}

	// A plan that leaves the required context out is re-planned once, then saved as failed with the reason, as version 2.
	waitIdle(t, a)
	f.dropSetup = true
	f.steps = nil
	pv, _ = a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{item}})
	id, err = a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, true)
	if err != nil {
		t.Fatal(err)
	}
	v = waitAI(t, a, id)
	if v.State != store.AICompleted || strings.Join(f.steps, ",") != "analyze,plan,plan" {
		t.Fatalf("run %+v steps %v", v, f.steps)
	}
	plans, _ = a.CutPlans(ctx, demo.ID)
	if len(plans) != 2 || plans[0].State != cut.StateVerifyFailed || plans[0].Version != 2 || plans[0].Supersedes != plans[1].ID {
		t.Fatalf("plans %+v", plans)
	}
	tl, _ = a.Timeline(ctx, demo.ID)
	cuts := 0
	for _, it := range tl.Items {
		if it.Kind == "cut" {
			cuts++
			if it.Verified || !strings.Contains(it.Uncertainty, "필수 맥락") {
				t.Fatalf("current plan %+v", it)
			}
		}
	}
	if cuts != 1 {
		t.Fatalf("the newest plan alone is shown, got %d", cuts)
	}

	// A check that passes while filing key evidence as a side remark is failed by the server, which says why.
	waitIdle(t, a)
	f.dropSetup, f.keyAside = false, true
	pv, _ = a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{item}})
	id, err = a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, true)
	if err != nil {
		t.Fatal(err)
	}
	if v = waitAI(t, a, id); v.State != store.AICompleted {
		t.Fatalf("run %+v", v)
	}
	plans, _ = a.CutPlans(ctx, demo.ID)
	if len(plans) != 3 || plans[0].State != cut.StateVerifyFailed {
		t.Fatalf("plans %+v", plans)
	}
	d, err := a.CutPlanDetail(ctx, plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	cv := d.Verification
	if cv.ClipPass == nil || *cv.ClipPass || len(cv.Gaps) != 1 || len(cv.Asides) != 0 || len(cv.Gaps[0].Refs) != 1 || cv.Gaps[0].Refs[0].AtMs == nil ||
		len(cv.ServerChanges) != 3 || !strings.Contains(cv.ServerChanges[0], "chat:nowhere") || !strings.Contains(cv.ServerChanges[1], "참고 → 실패 사유") ||
		!strings.Contains(cv.ServerChanges[2], "판정 통과 → 실패") || !strings.Contains(cv.Issues[0], "(근거 "+cv.Gaps[0].Refs[0].Ref+")") {
		t.Fatalf("verification %+v", cv)
	}
}

// waitIdle waits until no AI work is registered (the run's goroutine has ended).
func waitIdle(t *testing.T, a *App) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		a.ai.mu.Lock()
		n := len(a.ai.active)
		a.ai.mu.Unlock()
		if n == 0 {
			return
		}
	}
	t.Fatal("AI work still registered")
}

// farTranscript replaces the sample's transcript with one whose penalty
// promise is at 0:30, far before the first candidate's window, so the
// analysis has to search for it.
func farTranscript(t *testing.T, a *App, sid string) {
	t.Helper()
	utts := []domain.Utterance{{StartMs: 30000, EndMs: 34000, Speaker: "방송자", Text: "이번 판 지면 벌칙으로 노래 부를게요. 약속"}}
	for ms := int64(65000); ms < domain.DemoDurationMs; ms += 30000 {
		utts = append(utts, domain.Utterance{StartMs: ms, EndMs: ms + 4000, Speaker: "방송자", Text: "계속 가 볼게요"})
	}
	if _, err := a.cfg.Store.SaveTranscript(context.Background(), store.Transcript{SessionID: sid, Source: "demo"}, utts); err != nil {
		t.Fatal(err)
	}
}

func cutSetup(t *testing.T, confirm bool) (*App, *stepAI, string, AIPreview) {
	t.Helper()
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	farTranscript(t, a, demo.ID)
	tl, _ := a.Timeline(ctx, demo.ID)
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{tl.Items[0].ID},
		ConfirmSearches: confirm})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range pv.Cut.Inputs[0].Evidence {
		if strings.Contains(e.Text, "벌칙으로 노래") {
			t.Fatal("the promise is inside the previewed window; the test needs it outside")
		}
	}
	if pv.Cut.ConfirmSearches != confirm || !strings.Contains(pv.Plan.EstimateNote, "검색해") {
		t.Fatalf("preview %+v note %q", pv.Cut, pv.Plan.EstimateNote)
	}
	a.SetAIKey("fakeai", "k", false)
	return a, f, demo.ID, pv
}

func lastPlan(t *testing.T, a *App, sid string) cut.Plan {
	t.Helper()
	plans, err := a.CutPlans(context.Background(), sid)
	if err != nil || len(plans) == 0 {
		t.Fatalf("plans %+v err %v", plans, err)
	}
	_, raw, err := a.CutPlan(context.Background(), plans[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var p cut.Plan
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCutAnalysisSearchesForTheStoryOrigin(t *testing.T) {
	a, f, sid, pv := cutSetup(t, false)
	ctx := context.Background()
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, id)
	if v.State != store.AICompleted || v.CompletedRequests != 5 || strings.Join(f.steps, ",") != "analyze,analyze,plan,verify_clip,verify_source" {
		t.Fatalf("run %+v steps %v", v, f.steps)
	}
	p := lastPlan(t, a, sid)
	// Round 1 found the promise; the fake asks the same thing again in round 2,
	// which is recorded as skipped, not run, and ends the searching.
	if p.State != cut.StateOK || p.Times.StoryOriginMs != 30000 || p.Times.OriginConfidence != "found_in_search_range" ||
		len(p.Searches) != 2 || !p.Searches[0].Ran || len(p.Searches[0].FoundRefs) == 0 || p.Searches[0].Request.BeforeMs == 0 ||
		p.Searches[1].Ran || p.Searches[1].Note == "" ||
		p.SearchBudget.Rounds != 1 || p.SearchBudget.Requests != 1 || p.SearchBudget.Bytes == 0 || p.SearchBudget.Exhausted ||
		len(p.SearchedRanges) != 1 || p.SelectedSpans[0].Role != "setup" || p.SelectedSpans[0].StartMs != 30000-cut.UtteranceLeadMs {
		t.Fatalf("plan %+v", p)
	}
	// The second analyze step sent what the search found, and the log says so.
	run, err := a.cfg.Store.AIRun(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var log []sentLogEntry
	if json.Unmarshal(run.SentLogJSON, &log) != nil || len(log) != 5 || log[0].Round != 0 || len(log[0].NewRefs) != 0 ||
		log[1].Step != "analyze" || log[1].Round != 1 || len(log[1].NewRefs) == 0 || !strings.HasPrefix(log[1].NewRefs[0], "utt:") ||
		log[1].Refs != log[0].Refs+len(log[1].NewRefs) || len(log[2].NewRefs) != 0 {
		t.Fatalf("sent log %s", run.SentLogJSON)
	}
	// The timeline item carries the search and the origin.
	tl, _ := a.Timeline(ctx, sid)
	for _, it := range tl.Items {
		if it.Kind == "cut" {
			if it.SearchRounds != 1 || it.SearchAdded == 0 || !it.HasOrigin || it.StoryOriginMs != 30000 {
				t.Fatalf("timeline item %+v", it)
			}
		}
	}
}

func waitPending(t *testing.T, a *App, id string) AIRunView {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		v, err := a.AIRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.Pending != nil || v.State != store.AIRunning {
			return v
		}
	}
	t.Fatal("the run never waited for the user")
	return AIRunView{}
}

func TestCutAnalysisWaitsForTheUserBeforeSendingSearchResults(t *testing.T) {
	a, f, sid, pv := cutSetup(t, true)
	ctx := context.Background()
	if err := a.ContinueAI("nope", true); !isCode(err, "NOT_WAITING") {
		t.Fatalf("continue without a run: %v", err)
	}
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitPending(t, a, id)
	if v.Pending == nil || v.Pending.Round != 1 || v.Pending.Utterances != 1 || len(v.Pending.Evidence) != v.Pending.Utterances+v.Pending.Chats ||
		!strings.Contains(v.Pending.Evidence[0].Text, "벌칙") || len(f.steps) != 1 {
		t.Fatalf("pending %+v steps %v", v.Pending, f.steps)
	}
	if err := a.ContinueAI(id, true); err != nil {
		t.Fatal(err)
	}
	v = waitAI(t, a, id)
	if v.State != store.AICompleted || v.Pending != nil || strings.Join(f.steps, ",") != "analyze,analyze,plan,verify_clip,verify_source" {
		t.Fatalf("run %+v steps %v", v, f.steps)
	}
	if p := lastPlan(t, a, sid); p.Times.StoryOriginMs != 30000 || p.SearchBudget.Declined {
		t.Fatalf("plan %+v", p)
	}
}

func TestCutAnalysisGoesOnWithoutDeclinedSearchResults(t *testing.T) {
	a, f, sid, pv := cutSetup(t, true)
	ctx := context.Background()
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	waitPending(t, a, id)
	if err := a.ContinueAI(id, false); err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, id)
	// No second analyze step: the plan is made from the previewed window alone.
	if v.State != store.AICompleted || !strings.HasPrefix(strings.Join(f.steps, ","), "analyze,plan") {
		t.Fatalf("run %+v steps %v", v, f.steps)
	}
	p := lastPlan(t, a, sid)
	if !p.SearchBudget.Declined || p.Times.StoryOriginMs != 0 || len(p.UnresolvedQuestions) == 0 || p.State == cut.StateOK {
		t.Fatalf("plan state %s origin %d questions %v budget %+v", p.State, p.Times.StoryOriginMs, p.UnresolvedQuestions, p.SearchBudget)
	}
	run, _ := a.cfg.Store.AIRun(ctx, id)
	if strings.Contains(string(run.SentLogJSON), "utt:"+p.ID) || strings.Contains(string(run.PlanJSON), "벌칙으로 노래") {
		t.Fatalf("the declined evidence was sent: %s", run.SentLogJSON)
	}
}

func TestCutAnalysisWithoutSearchBudgetSendsNoSearch(t *testing.T) {
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	st := a.Settings().Settings
	st.CutSearchRounds = 0
	if _, err := a.SaveSettings(st); err != nil {
		t.Fatal(err)
	}
	demo, _ := a.CreateDemo(ctx, false)
	farTranscript(t, a, demo.ID)
	tl, _ := a.Timeline(ctx, demo.ID)
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{tl.Items[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Cut.SearchExecuted || pv.Plan.RequestCount != cut.BaseRequestsPerCandidate || !strings.Contains(pv.Plan.EstimateNote, "검색하지 않습니다") {
		t.Fatalf("preview %+v", pv.Cut)
	}
	a.SetAIKey("fakeai", "k", false)
	id, _ := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	waitAI(t, a, id)
	if p := lastPlan(t, a, demo.ID); len(p.Searches) != 0 || !p.SearchBudget.Exhausted || p.SearchBudget.Requests != 0 {
		t.Fatalf("plan %+v", p.SearchBudget)
	}
	if st.CutSearchRounds = 99; true {
		if _, err := a.SaveSettings(st); !isCode(err, "INPUT") {
			t.Fatalf("rounds 99: %v", err)
		}
	}
}

// A note on a plan goes into the next analysis, which saves a new version;
// the confirmed version stays current beside it and is what the confirmed
// exports for editnest hold.
func TestCutNotesMakeANewVersionAndTheConfirmedOneStays(t *testing.T) {
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := a.Timeline(ctx, demo.ID)
	item := tl.Items[0].ID
	a.SetAIKey("fakeai", "k", false)
	analyse := func() AIPreview {
		t.Helper()
		pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{item}})
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
	if pv := analyse(); pv.Cut.NoteCount != 0 || len(f.notes) != 0 {
		t.Fatalf("no notes yet: %+v %v", pv.Cut, f.notes)
	}
	v1 := lastPlan(t, a, demo.ID)

	for _, bad := range []string{"", "   ", strings.Repeat("가", maxNoteRunes+1)} {
		if _, err := a.AddCutNote(ctx, v1.ID, bad, ""); !isCode(err, "INPUT") {
			t.Fatalf("note %q: %v", bad, err)
		}
	}
	if _, err := a.AddCutNote(ctx, v1.ID, "두 번째 시도라 앞선 약속과 연결하면 안 됨", "causes[0]"); err != nil {
		t.Fatal(err)
	}
	if _, err := a.ConfirmCut(ctx, v1.ID, true, "full_event"); !isCode(err, "INPUT") {
		t.Fatalf("a boundary the plan does not have: %v", err)
	}
	view, err := a.ConfirmCut(ctx, v1.ID, true, "")
	if err != nil || !view.Confirmed || view.ConfirmedBoundary != "context" {
		t.Fatalf("confirm %+v %v", view, err)
	}
	cuts := filepath.Join(a.exportDir(demo.ID), "cuts")
	edl, err := os.ReadFile(filepath.Join(cuts, "confirmed.edl"))
	if err != nil || !strings.Contains(string(edl), "00:03:39:21") || !strings.Contains(string(edl), "* COMMENT: 약속과 실패의 대비") {
		t.Fatalf("confirmed.edl %q %v", edl, err)
	}
	if csv, _ := os.ReadFile(filepath.Join(cuts, "confirmed.csv")); !strings.HasPrefix(string(csv), "plan_id,version,boundary,") ||
		!strings.Contains(string(csv), v1.ID+",1,context,1,setup,219700,") {
		t.Fatalf("confirmed.csv %q", csv)
	}
	var memo struct {
		Segments []struct {
			Start float64 `json:"start"`
			End   float64 `json:"end"`
			Title string  `json:"title"`
		} `json:"segments"`
	}
	raw, _ := os.ReadFile(filepath.Join(cuts, "confirmed.editnest.json"))
	if json.Unmarshal(raw, &memo) != nil || len(memo.Segments) != 2 || memo.Segments[0].Start != 219.7 || memo.Segments[0].Title == "" {
		t.Fatalf("editnest json %s", raw)
	}

	// The next analysis plans with the note and becomes v2; v1 stays confirmed and current.
	if pv := analyse(); pv.Cut.NoteCount != 1 || !strings.Contains(pv.Plan.EstimateNote, "메모 1개") {
		t.Fatalf("preview %+v %q", pv.Cut, pv.Plan.EstimateNote)
	}
	if len(f.notes) != 1 || f.notes[0].Text != "두 번째 시도라 앞선 약속과 연결하면 안 됨" || f.notes[0].AppliesTo != "causes[0]" {
		t.Fatalf("plan step notes %+v", f.notes)
	}
	v2 := lastPlan(t, a, demo.ID)
	if v2.Version != 2 || v2.ConfirmedByUser || len(v2.NotesApplied) != 1 || len(v2.UserNotes) != 0 {
		t.Fatalf("v2 %+v", v2)
	}
	tl, _ = a.Timeline(ctx, demo.ID)
	var shown []*CutDetail
	for _, it := range tl.Items {
		if it.Kind == "cut" {
			shown = append(shown, it.Cut)
		}
	}
	if len(shown) != 2 || !shown[0].Confirmed || shown[0].Version != 1 || shown[1].Version != 2 || shown[1].KeptVersion != 1 ||
		len(shown[0].Notes) != 1 || len(shown[1].NotesApplied) != 1 || len(shown[0].Boundaries) != 1 || shown[0].Origin == nil || shown[0].Origin.AtMs == nil {
		t.Fatalf("timeline cut details %+v", shown)
	}
	// Confirming v2 releases v1; taking it back leaves nothing confirmed and removes the confirmed files.
	if _, err := a.ConfirmCut(ctx, v2.ID, true, "context"); err != nil {
		t.Fatal(err)
	}
	confirmed, _ := a.cfg.Store.ConfirmedCutPlans(ctx, demo.ID)
	if len(confirmed) != 1 || confirmed[0].ID != v2.ID {
		t.Fatalf("confirmed %+v", confirmed)
	}
	if _, err := a.ConfirmCut(ctx, v2.ID, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cuts, "confirmed.edl")); !os.IsNotExist(err) {
		t.Fatalf("confirmed.edl left behind: %v", err)
	}
	tl, _ = a.Timeline(ctx, demo.ID)
	n := 0
	for _, it := range tl.Items {
		if it.Kind == "cut" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("without a confirmation the newest alone is shown, got %d", n)
	}
}

// A plan whose source check fails is re-planned; when the re-plan fails the
// structural checks, the saved plan is the verified one with its own
// verdict, never the re-plan with the earlier verdict.
func TestCutPlanAndVerdictComeFromTheSameAttempt(t *testing.T) {
	f := &stepAI{sourceFailsOnce: true}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	a.SetAIKey("fakeai", "k", false)
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{"rule:m0001"}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := waitAI(t, a, id); v.State != store.AICompleted || strings.Join(f.steps, ",") != "analyze,plan,verify_clip,verify_source,plan" {
		t.Fatalf("run %+v steps %v", v, f.steps)
	}
	p := lastPlan(t, a, demo.ID)
	src := p.Verification.AgainstSource
	if p.State != cut.StateVerifyFailed || len(p.SelectedSpans) != 2 || p.SelectedSpans[0].Role != "setup" || src == nil || src.Pass ||
		len(p.Verification.Issues) != 1 || !strings.HasPrefix(p.Verification.Issues[0], "원본 비교: ") {
		t.Fatalf("plan spans %+v verdict %+v issues %v", p.SelectedSpans, src, p.Verification.Issues)
	}
}

// One candidate whose answer cannot be read fails alone: the others are
// analysed and saved, and the run says which one failed and why.
func TestCutRunGoesOnPastAFailedCandidate(t *testing.T) {
	f := &stepAI{badAnalyze: "rule:m0001"}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	reanalyse(t, a, demo.ID) // two candidates: m0001 (00:00:30) and m0002
	a.SetAIKey("fakeai", "k", false)
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut",
		ItemIDs: []string{"rule:m0001", "rule:m0002"}})
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, id)
	if v.State != store.AIPartial || v.Recommendations != 1 || v.ErrorCode != "AI_FORMAT" ||
		!strings.Contains(v.ErrorMessage, "후보 2개 중 1개") || !strings.Contains(v.ErrorMessage, "00:00:30 후보") {
		t.Fatalf("run %+v", v)
	}
	if p := lastPlan(t, a, demo.ID); !slices.Equal(p.CandidateIDs, []string{"rule:m0002"}) || p.State != cut.StateOK {
		t.Fatalf("plan %+v", p)
	}
	// A limit still stops the whole run.
	for code, stops := range map[string]bool{"CANCELLED": true, "COST_CAP": true, "AI_HTTP_401": true, "AI_UNAVAILABLE": true,
		"AI_FORMAT": false, "AI_REFUSAL": false, "AI_HTTP_529": false} {
		if got := cutRunStops(&ai.ProviderError{UserError: domain.NewUserError(code, "x")}); got != stops {
			t.Errorf("%s stops the run: %v", code, got)
		}
	}
}

// The cut input keeps the reaction itself: the detector's account (real
// peak, strength, what stood out), a chat curve over the clip, and chat
// chosen from the reaction outward.
func TestCutInputCarriesTheReaction(t *testing.T) {
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", Kind: "cut", ItemIDs: []string{"rule:m0001"}})
	if err != nil {
		t.Fatal(err)
	}
	in := pv.Cut.Inputs[0]
	r := in.Candidates[0].Reaction
	if r == nil || r.PeakMs <= in.Candidates[0].ClipStartMs || r.StartMs < in.Candidates[0].ClipStartMs || r.Strength == "" ||
		len(r.Measures) == 0 || len(r.Measures) > cutMaxMeasures || r.Measures[0].WindowS != 10 {
		t.Fatalf("reaction %+v", r)
	}
	c := in.Curve
	if c == nil || c.BinMs != cutCurveBinMs || c.Partial || len(c.Bins) != int((in.Candidates[0].ClipEndMs-in.Candidates[0].ClipStartMs)/cutCurveBinMs) {
		t.Fatalf("curve %+v", c)
	}
	peakBin, quiet := 0, 0
	for _, b := range c.Bins {
		if b.StartMs <= r.PeakMs && r.PeakMs < b.StartMs+c.BinMs {
			peakBin = b.Reacting
		}
		if b.StartMs < r.StartMs-20_000 {
			quiet = max(quiet, b.Reacting)
		}
	}
	if peakBin < 5 || quiet >= peakBin || c.UsualChats <= 0 {
		t.Fatalf("curve does not show the reaction: peak bin %d, before %d, usual %v", peakBin, quiet, c.UsualChats)
	}
	// Every message of the window is either sent or counted as left out.
	chats, total, err := a.cfg.Store.ChatsBetween(ctx, demo.ID, in.WindowStartMs, in.WindowEndMs, store.AllSeq, 1_000_000, 0)
	if err != nil {
		t.Fatal(err)
	}
	sent := 0
	for _, e := range in.Evidence {
		if e.Kind == cut.KindChat {
			sent++
		}
	}
	if sent+in.ChatsLeftOut != total || sent > cutMaxChats || len(chats) != total {
		t.Fatalf("sent %d left out %d of %d", sent, in.ChatsLeftOut, total)
	}
	// The plan keeps the real peak, not the clip start.
	a.SetAIKey("fakeai", "k", false)
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	waitAI(t, a, id)
	if p := lastPlan(t, a, demo.ID); p.Reaction.PeakMs != r.PeakMs {
		t.Fatalf("plan peak %d, reaction peak %d", p.Reaction.PeakMs, r.PeakMs)
	}
}

func TestPickChatsKeepsTheReactionFirst(t *testing.T) {
	var chats []domain.ChatEvent
	add := func(ms int64, kind string) {
		chats = append(chats, domain.ChatEvent{ID: fmt.Sprint(len(chats)), OffsetMs: ms, Kind: kind, Text: "ㅋㅋ"})
	}
	for ms := int64(0); ms < 100_000; ms += 1000 { // one a second for 100 s
		add(ms, "")
	}
	add(5_500, domain.ChatKindDonation)
	slices.SortStableFunc(chats, func(x, y domain.ChatEvent) int { return int(x.OffsetMs - y.OffsetMs) })
	times := func(list []domain.ChatEvent) []int64 {
		var out []int64
		for _, c := range list {
			out = append(out, c.OffsetMs)
		}
		return out
	}
	for _, tc := range []struct {
		name     string
		from, to int64
		n        int
		check    func([]domain.ChatEvent) bool
	}{
		{"everything fits", 40_000, 60_000, 200, func(got []domain.ChatEvent) bool { return len(got) == len(chats) }},
		{"the focus and then the nearest", 40_000, 50_000, 21, func(got []domain.ChatEvent) bool {
			// The donation, the ten focus seconds, then five on each side.
			return len(got) == 21 && got[0].Kind == domain.ChatKindDonation && got[1].OffsetMs == 35_000 && got[len(got)-1].OffsetMs == 54_000
		}},
		{"a crowded focus is thinned across its bins", 0, 100_000, 21, func(got []domain.ChatEvent) bool {
			// The donation and one message from each 5 s bin, in time order.
			ts := times(got)
			return len(got) == 21 && slices.IsSorted(ts) && ts[0] == 0 && ts[1] == 5_000 && ts[2] == 5_500 && ts[len(ts)-1] == 95_000
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := pickChats(chats, tc.from, tc.to, tc.n)
			if !tc.check(got) {
				t.Fatalf("picked %v", times(got))
			}
		})
	}
}

func TestChatEvidenceDropsHiddenAndMarksNotices(t *testing.T) {
	ev := chatEvidence([]domain.ChatEvent{
		{ID: "a", OffsetMs: 1000, AuthorKey: "u1", Text: "ㅋㅋㅋ"},
		{ID: "b", OffsetMs: 2000, AuthorKey: "u2", Text: "광고", Hidden: true},
		{ID: "c", OffsetMs: 3000, AuthorKey: "u3", Text: "후원 감사", Kind: domain.ChatKindDonation, Amount: 1000},
		{ID: "d", OffsetMs: 4000, Text: "구독", Kind: domain.ChatKindSubscription},
	})
	if len(ev) != 3 || ev[0].ChatKind != "" || ev[1].Ref != "chat:c" || ev[1].ChatKind != domain.ChatKindDonation ||
		ev[2].ChatKind != domain.ChatKindSubscription || ev[2].Author != nil {
		t.Fatalf("evidence %+v", ev)
	}
}

// A search returns the matches nearest before the moment, and keeps room
// for chat when utterances alone would fill it.
func TestCutSearchReturnsTheNearestMatchesWithChat(t *testing.T) {
	f := &stepAI{}
	a := newAIApp(t, &f.fakeAI)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var utts []domain.Utterance
	for i := range 40 {
		ms := int64(i) * 5000
		utts = append(utts, domain.Utterance{StartMs: ms, EndMs: ms + 3000, Speaker: "방송자", Text: fmt.Sprintf("벌칙 이야기 %d", i)})
	}
	tr, err := a.cfg.Store.SaveTranscript(ctx, store.Transcript{SessionID: demo.ID, Source: "demo"}, utts)
	if err != nil {
		t.Fatal(err)
	}
	chats, err := a.cfg.Store.ChatMatchesBefore(ctx, demo.ID, "벌칙", 400_000, store.AllSeq, 100)
	if err != nil || len(chats) == 0 {
		t.Fatalf("the sample's chat about the penalty: %d %v", len(chats), err)
	}
	s := &cutSearcher{a: a, sid: demo.ID, transcriptID: tr.ID, maxSeq: store.AllSeq, endMs: domain.DemoDurationMs}
	texts := func(in string) []string {
		t.Helper()
		found, _, err := s.search(ctx, cut.SearchRequest{Tool: cut.ToolSearch, Query: "벌칙", In: in, BeforeMs: 400_000})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range found {
			out = append(out, e.Text)
		}
		return out
	}
	// Utterances alone: the 30 nearest, oldest first.
	if got := texts("utterances"); len(got) != cut.SearchMaxResults || got[0] != "벌칙 이야기 10" || got[29] != "벌칙 이야기 39" {
		t.Fatalf("utterances %v", got)
	}
	// Both: chat keeps its share, the utterances give way from the far end.
	nChat := min(len(chats), searchChatShare)
	got := texts("both")
	if len(got) != cut.SearchMaxResults || got[0] != fmt.Sprintf("벌칙 이야기 %d", 40-(cut.SearchMaxResults-nChat)) ||
		got[len(got)-1] != chats[len(chats)-1].Text {
		t.Fatalf("both (%d chats) %v", nChat, got)
	}
}

// The first-generation review sends a rule candidate's reaction span, peak
// and strength with its peak numbers.
func TestExplainStatisticsCarryTheReaction(t *testing.T) {
	f := &fakeAI{}
	a := newAIApp(t, f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", ItemIDs: []string{"rule:m0001"}})
	if err != nil {
		t.Fatal(err)
	}
	st := pv.Plan.Chunks[0].Candidates[0].Statistics
	for _, k := range []string{"n", "u", "reaction_start_ms", "reaction_end_ms", "peak_ms", "strength", "score"} {
		if _, ok := st[k]; !ok {
			t.Fatalf("statistics lack %s: %v", k, st)
		}
	}
	if !strings.Contains(pv.Plan.Prompt, "reaction_start_ms") || pv.Plan.PromptVersion != ai.PromptVersion {
		t.Fatalf("the prompt does not explain the new numbers")
	}
}

// What a search finds goes into the run's copy of the input; the preview
// the user confirmed stays as it was shown.
func TestCutSearchLeavesThePreviewAlone(t *testing.T) {
	a, _, _, pv := cutSetup(t, false)
	before := slices.Clone(pv.Cut.Inputs[0].Evidence)
	id, err := a.StartAI(context.Background(), pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	if v := waitAI(t, a, id); v.State != store.AICompleted {
		t.Fatalf("run %+v", v)
	}
	if after := pv.Cut.Inputs[0].Evidence; !slices.Equal(after, before) || len(pv.Cut.Inputs[0].Searched) != 0 {
		t.Fatalf("the preview changed: %d → %d evidence, %d searches", len(before), len(after), len(pv.Cut.Inputs[0].Searched))
	}
}
