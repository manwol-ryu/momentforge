package main

import (
	"context"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/detect/rules"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// buildCutData writes the sample with two versions of a cut plan for its
// first candidate: v1 confirmed with the whole-event boundary and a note,
// v2 newer and not confirmed. It returns the session and both plan IDs.
func buildCutData(t *testing.T, dir string) (sid, v1, v2 string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dir, "eval")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	core := app.New(app.Config{Version: "eval", DataDir: dir, Store: st, Params: detect.DefaultParams(), Rules: rules.All(),
		Platforms: registerPlatforms(nil), AI: &ai.Registry{}})
	defer core.Shutdown(ctx)
	demo, err := core.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	known := cut.Known([]cut.Evidence{
		{Ref: "utt:p", Kind: cut.KindUtterance, StartMs: 220_000, EndMs: 226_000, Text: "이번 판 지면 벌칙으로 노래"},
		{Ref: "utt:m", Kind: cut.KindUtterance, StartMs: 290_000, EndMs: 294_000, Text: "아 죽었다"},
		{Ref: "chat:l", Kind: cut.KindChat, StartMs: 302_000, Text: "ㅋㅋ 아까 벌칙"},
	})
	plan := func(target string) []byte {
		p := cut.Plan{SessionID: demo.ID, CandidateIDs: []string{"rule:m0001"}, State: cut.StateOK, Sources: []string{"utterances", "chat"},
			Causes:      []cut.Cause{{Kind: cut.CauseCallback, Summary: "앞선 벌칙 약속과 실패의 대비", Support: []string{"utt:p", "chat:l"}, Against: []string{}}},
			CauseStatus: "likely", RequiredContextRefs: []string{"utt:p", "utt:m"}}
		p.Reaction.Target = target
		p.Times.StoryOriginRef, p.Times.EventStartRef = "utt:p", "utt:m"
		p.Boundaries.Candidates = []cut.BoundaryCandidate{
			{Name: "short", Spans: []cut.SpanRefs{{StartRef: "utt:m", EndRef: "utt:m", Role: "main"}}},
			{Name: "context", Spans: []cut.SpanRefs{{StartRef: "utt:p", EndRef: "utt:p", Role: "setup"}, {StartRef: "utt:m", EndRef: "chat:l", Role: "main"}}},
			{Name: "full_event", Spans: []cut.SpanRefs{{StartRef: "utt:p", EndRef: "chat:l", Role: "main"}}},
		}
		p.Boundaries.Chosen = "context"
		p.Boundaries.Resolved = cut.ResolveBoundaries(p.Boundaries.Candidates, known, 600_000)
		p.SelectedSpans = p.Boundaries.Resolved[1].Spans
		p.Ending = cut.Ending{State: "confirmed", EventKind: "promise_penalty", Checks: []string{"결과 확인"}}
		p.Verification = cut.Verification{ClipOnly: &cut.ClipVerification{Pass: true, Answers: map[string]string{"what": "도전", "condition": "지면 벌칙",
			"result": "실패", "references": "약속", "why_reaction": "대비"}}, EvidenceMatch: "all_read", BoundaryOK: true}
		cut.IndexRefs(&p, known)
		return ai.Encode(p)
	}
	run, err := st.CreateAIRun(ctx, store.AIRun{SessionID: demo.ID, Provider: "evalai", Model: "m", Kind: store.AIKindCut, State: store.AICompleted})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, target := range []string{"벌칙 약속과 실패", "벌칙 약속과 실패 (다시 분석)"} {
		rec, err := st.CreateCutPlan(ctx, store.CutPlan{RunID: run.ID, SessionID: demo.ID, CandidateIDs: []string{"rule:m0001"}, State: cut.StateOK})
		if err != nil {
			t.Fatal(err)
		}
		rec.PlanJSON = plan(target)
		if err := st.SaveCutPlan(ctx, rec); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, rec.ID)
	}
	if _, err := core.AddCutNote(ctx, ids[0], "두 번째 시도라 앞선 약속과 연결하면 안 됨", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := core.ConfirmCut(ctx, ids[0], true, "full_event"); err != nil {
		t.Fatal(err)
	}
	return demo.ID, ids[0], ids[1]
}

func TestMCPCutPlanTools(t *testing.T) {
	dir := t.TempDir()
	sid, v1, v2 := buildCutData(t, dir)
	cs := mcpClient(t, dir)

	var list struct {
		Total int `json:"total"`
		Plans []struct {
			PlanID            string `json:"plan_id"`
			Version           int    `json:"version"`
			Confirmed         bool   `json:"confirmed"`
			ConfirmedBoundary string `json:"confirmed_boundary"`
			KeptVersion       int    `json:"kept_version"`
			NoteCount         int    `json:"note_count"`
			Spans             []struct {
				StartMs int64 `json:"start_ms"`
			} `json:"spans"`
		} `json:"plans"`
	}
	text, isErr := call(t, cs, "mf_list_cut_plans", map[string]any{"session_id": sid}, &list)
	// The confirmed v1 stays beside the newer v2, which says which version is kept.
	if isErr || list.Total != 2 || list.Plans[0].PlanID != v1 || !list.Plans[0].Confirmed || list.Plans[0].ConfirmedBoundary != "full_event" ||
		list.Plans[0].NoteCount != 1 || len(list.Plans[0].Spans) != 1 || list.Plans[1].PlanID != v2 || list.Plans[1].KeptVersion != 1 ||
		len(list.Plans[1].Spans) != 2 {
		t.Fatalf("list %+v %q", list, text)
	}
	if !strings.Contains(text, "확정(전체)") || !strings.Contains(text, "확정본 v1 유지") {
		t.Fatalf("list text %q", text)
	}
	if text, isErr := call(t, cs, "mf_list_cut_plans", map[string]any{"session_id": "nope"}, nil); !isErr || !strings.Contains(text, "mf_list_sessions") {
		t.Fatalf("unknown session: %q", text)
	}

	var plan struct {
		Causes []struct {
			Support []struct {
				Ref  string `json:"ref"`
				Time string `json:"time"`
				Text string `json:"text"`
			} `json:"support"`
		} `json:"causes"`
		StoryOrigin struct {
			Time string `json:"time"`
		} `json:"story_origin"`
		Boundaries []struct {
			Name      string `json:"name"`
			Chosen    bool   `json:"chosen"`
			Confirmed bool   `json:"confirmed"`
		} `json:"boundaries"`
		Verification struct {
			Answers map[string]string `json:"answers"`
		} `json:"verification"`
		Notes []struct {
			Text string `json:"text"`
		} `json:"notes"`
	}
	text, isErr = call(t, cs, "mf_get_cut_plan", map[string]any{"plan_id": v1}, &plan)
	if isErr || len(plan.Causes) != 1 || plan.Causes[0].Support[0].Time != "0:03:40" || plan.Causes[0].Support[1].Time != "0:04:56" ||
		plan.StoryOrigin.Time != "0:03:40" || len(plan.Boundaries) != 3 || !plan.Boundaries[1].Chosen || !plan.Boundaries[2].Confirmed ||
		plan.Verification.Answers["condition"] != "지면 벌칙" || len(plan.Notes) != 1 {
		t.Fatalf("plan %+v %q", plan, text)
	}
	for _, want := range []string{"가장 유력", "이야기 기원: 0:03:40 utt:p", "전체 (확정)", "맥락 (AI 선택)", "condition: 지면 벌칙", "사용자 메모"} {
		if !strings.Contains(text, want) {
			t.Fatalf("plan text lacks %q: %s", want, text)
		}
	}
	if text, isErr := call(t, cs, "mf_get_cut_plan", map[string]any{"plan_id": "nope"}, nil); !isErr || !strings.Contains(text, "mf_list_cut_plans") {
		t.Fatalf("unknown plan: %q", text)
	}
}
