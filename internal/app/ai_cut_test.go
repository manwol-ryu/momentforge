package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// stepAI answers the cut analysis like the E2E fake: the earliest "벌칙"
// utterance is the required setup; with dropSetup it leaves that span out,
// so the structural check fails twice and the plan is saved as failed.
type stepAI struct {
	fakeAI
	dropSetup bool
	steps     []string
}

func (f *stepAI) Step(ctx context.Context, key, model string, step ai.StepRequest) (json.RawMessage, ai.Usage, error) {
	f.mu.Lock()
	f.steps = append(f.steps, step.Name)
	f.mu.Unlock()
	u := ai.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}
	var out any
	switch step.Name {
	case "analyze":
		in := step.Input.(cut.Input)
		out = map[string]any{"target": "약속과 실패의 대비", "cause_status": "likely", "questions": []string{"'아까'는 무엇인가"},
			"search_requests": []any{map[string]any{"tool": "search", "query": "벌칙", "in": "both", "before_ms": in.WindowStartMs, "at_ms": 0, "from_ms": 0, "to_ms": 0, "window_s": 0}},
			"causes": []any{map[string]any{"kind": "callback_to_earlier", "summary": "앞선 장담", "support": []string{in.Evidence[0].Ref},
				"against": []string{}, "direction": "broadcast_to_viewers"}}}
	case "plan":
		in := step.Input.(map[string]any)["input"].(cut.Input)
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
		if !f.dropSetup {
			spans = append([]any{map[string]any{"start_ref": promise.Ref, "end_ref": promise.Ref, "role": "setup"}}, spans...)
		}
		out = map[string]any{"story_origin_ref": promise.Ref, "origin_confidence": "found_in_search_range", "event_start_ref": first.Ref,
			"analysis_evidence_refs": []string{promise.Ref, first.Ref, last.Ref}, "required_context_refs": []string{promise.Ref, first.Ref},
			"optional_context_refs": []string{}, "boundary_candidates": []any{map[string]any{"name": "context", "spans": spans}},
			"chosen": "context", "start_reason": "약속부터", "end_reason": "인정까지",
			"ending": map[string]any{"state": "confirmed", "event_kind": "promise_penalty", "checks": []string{"결과 확인"}}, "unresolved_questions": []string{}}
	case "verify_clip":
		out = map[string]any{"pass": true, "answers": map[string]string{"what": "도전", "condition": "지면 벌칙", "result": "실패", "references": "약속", "why_reaction": "대비"}, "gaps": []string{}}
	case "verify_source":
		out = map[string]any{"pass": true, "mixed_attempt": false, "cut_condition": false, "missed_correction": false, "ended_early": false, "notes": []string{}}
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
		pv.Plan.RequestCount != cut.RequestsPerCandidate || len(f.steps) != 0 {
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
	if edl, _ := os.ReadFile(filepath.Join(dir, "cuts.edl")); !strings.Contains(string(edl), "TITLE: MomentForge") || !strings.Contains(string(edl), "00:03:40:00") {
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
