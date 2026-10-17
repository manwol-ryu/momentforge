package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// liveForAI stores a LIVE capture as it stands mid-broadcast: a message a
// second over the first two minutes, a confirmed candidate at 0:20–0:50 and
// one still forming at 1:20–1:50, analysed up to 1:40.
func liveForAI(t *testing.T, a *App) string {
	t.Helper()
	ctx := context.Background()
	st := a.cfg.Store
	s, err := st.CreateLive(ctx, store.NewSession{Platform: "fakelive", TargetID: "ch1", Title: "생방송", Salt: []byte("salt")},
		store.LiveState{ChannelID: "ch1", LiveID: "L1", TimeBasis: "broadcast_elapsed"})
	if err != nil {
		t.Fatal(err)
	}
	var rows []store.LiveEvent
	for ms := int64(0); ms < 120000; ms += 1000 {
		rows = append(rows, store.LiveEvent{ChatEvent: domain.ChatEvent{ID: fmt.Sprintf("e%03d", ms/1000), OffsetMs: ms,
			AuthorKey: fmt.Sprintf("a%d", ms/1000%7), Text: "ㅋㅋㅋ"}, ReceivedAtMs: ms + 100, EventTimeMs: ms, TimeQuality: "provider"})
	}
	if _, _, err := st.AppendLive(ctx, s.ID, rows, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpsertLiveCandidates(ctx, s.ID, []domain.Candidate{
		{ID: "m0001", ReactionStartMs: 25000, ReactionEndMs: 40000, ClipStartMs: 20000, ClipEndMs: 50000, Reasons: []string{"웃음"},
			Peak: domain.PeakStats{N: 12, U: 7}, BaselineReady: true, State: domain.CandidateConfirmed},
		{ID: "m0002", ReactionStartMs: 85000, ReactionEndMs: 95000, ClipStartMs: 80000, ClipEndMs: 110000, Reasons: []string{"웃음"},
			Peak: domain.PeakStats{N: 9, U: 6}, BaselineReady: true, State: domain.CandidateGenerating},
	}); err != nil {
		t.Fatal(err)
	}
	ls, err := st.LiveState(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	ls.Generation, ls.WatermarkMs = 1, 100000
	if err := st.SaveLiveState(ctx, s.ID, store.LiveCollecting, "", "", ls); err != nil {
		t.Fatal(err)
	}
	return s.ID
}

func TestAIPreviewLiveCapture(t *testing.T) {
	f := &fakeAI{}
	a := newAIApp(t, f)
	ctx := context.Background()
	sid := liveForAI(t, a)
	preview := func(req AIPreviewRequest) (AIPreview, error) {
		req.SessionID, req.Provider, req.Model = sid, "fakeai", "m"
		return a.PreviewAI(ctx, req)
	}

	// A confirmed candidate is sent with its peak numbers and its clip's chat.
	pv, err := preview(AIPreviewRequest{ItemIDs: []string{"rule:m0001"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Plan.Chunks) != 1 || len(pv.Plan.Chunks[0].Candidates) != 1 || pv.Plan.Chunks[0].Candidates[0].Statistics["n"] != 12 {
		t.Fatalf("plan %+v", pv.Plan.Chunks)
	}
	chunk := pv.Plan.Chunks[0]
	if len(chunk.Evidence) != 30 || chunk.Evidence[0].TimeMs != 20000 || chunk.Evidence[29].TimeMs != 49000 || f.calls != 0 {
		t.Fatalf("evidence %d from %d (nothing is sent before the user confirms: %d)", len(chunk.Evidence), chunk.Evidence[0].TimeMs, f.calls)
	}

	// A candidate still forming is not offered yet; an unknown one is refused.
	if _, err := preview(AIPreviewRequest{ItemIDs: []string{"rule:m0002"}}); !isCode(err, "INPUT") || !strings.Contains(err.Error(), "확정") {
		t.Fatalf("forming candidate: %v", err)
	}
	if _, err := preview(AIPreviewRequest{ItemIDs: []string{"rule:m0009"}}); !isCode(err, "INPUT") || strings.Contains(err.Error(), "확정") {
		t.Fatalf("unknown candidate: %v", err)
	}

	// A range may reach as far as the analysis.
	if r, err := preview(AIPreviewRequest{StartMs: 60000, EndMs: 100000}); err != nil || r.Plan.Chunks[0].Evidence[39].TimeMs != 99000 {
		t.Fatalf("range to the analysed point: %v", err)
	}
	if _, err := preview(AIPreviewRequest{StartMs: 60000, EndMs: 110000}); !isCode(err, "INPUT") || !strings.Contains(err.Error(), "00:01:40") {
		t.Fatalf("range past the analysed point: %v", err)
	}
	if r, err := preview(AIPreviewRequest{StartMs: 60000, EndMs: 999999, ToEnd: true}); err != nil || r.Plan.Ranges[0].EndMs != 100000 {
		t.Fatalf("range to the end: %+v %v", r.Plan.Ranges, err)
	}

	// Automatic selection takes only settled candidates.
	priced, _, _ := newPricedApp(t)
	psid := liveForAI(t, priced)
	all, err := priced.PreviewAI(ctx, AIPreviewRequest{SessionID: psid, Provider: "priced", Model: "m", All: true, CostCapUSD: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Plan.Chunks) != 1 || len(all.Plan.Chunks[0].Candidates) != 1 || all.Plan.Chunks[0].Candidates[0].ID != "rule:m0001" {
		t.Fatalf("automatic selection %+v", all.Plan.Chunks)
	}

	// Chat stored after the preview is not sent, even inside the previewed range.
	if _, _, err := a.cfg.Store.AppendLive(ctx, sid, []store.LiveEvent{{ChatEvent: domain.ChatEvent{ID: "late", OffsetMs: 30500,
		AuthorKey: "a9", Text: "늦게 받은 채팅"}, ReceivedAtMs: 101000, EventTimeMs: 30500, TimeQuality: "provider", Late: true}}, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	a.SetAIKey("fakeai", "k")
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, id)
	if v.State != store.AICompleted || v.Recommendations != 1 || len(f.chunks) != 1 || len(f.chunks[0].Evidence) != 30 {
		t.Fatalf("run %+v sent %d chunks", v, len(f.chunks))
	}
	again, err := preview(AIPreviewRequest{ItemIDs: []string{"rule:m0001"}})
	if err != nil || len(again.Plan.Chunks[0].Evidence) != 31 || again.Plan.Fingerprint == pv.Plan.Fingerprint {
		t.Fatalf("a new preview reads the new chat: %v", err)
	}

	// The capture's timeline and moments carry its candidates and the review.
	tl, err := a.Timeline(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]int{}
	for _, it := range tl.Items {
		sources[it.Source]++
		if it.WatchURL != "" {
			t.Fatalf("a LIVE moment has no link of its own: %+v", it)
		}
	}
	if sources[SourceRule] != 2 || sources[SourceAI] != 1 || tl.AIRunID != id {
		t.Fatalf("timeline sources %v run %s", sources, tl.AIRunID)
	}
	moments, err := a.Moments(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	var rules, ais int
	for _, m := range moments {
		switch {
		case m.Source == SourceRule && m.Peak != nil:
			rules++
		case m.Source == SourceAI && m.RunID == id:
			ais++
		}
	}
	if rules != 2 || ais != 1 {
		t.Fatalf("moments %+v", moments)
	}
	if runs, _ := a.AIRuns(ctx, sid); len(runs) != 1 || runs[0].ID != id {
		t.Fatalf("runs %+v", runs)
	}
}
