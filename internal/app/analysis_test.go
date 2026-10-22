package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

func TestAnalysisEvidenceMatchesTheWholeSnapshot(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	s, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Analysis(ctx, s.LatestAnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	// The summary as it was made from every message of the snapshot.
	an, err := a.cfg.Store.Analysis(ctx, s.LatestAnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	events, err := a.cfg.Store.Chats(ctx, s.ID, an.MaxSeq, false)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]domain.ChatEvent{}
	for _, e := range events {
		byID[e.ID] = e
	}
	if len(view.Candidates) == 0 {
		t.Fatal("the sample has no candidates")
	}
	for i, c := range an.Candidates {
		got := view.Candidates[i]
		if want := summarize(c.EvidenceIDs, byID); !reflect.DeepEqual(got.Evidence, want) || got.EvidenceTotal != len(c.EvidenceIDs) {
			t.Fatalf("%s evidence %+v (%d), want %+v (%d)", c.ID, got.Evidence, got.EvidenceTotal, want, len(c.EvidenceIDs))
		}
	}
}

func TestReviewQueriesKeepTheirErrors(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	s, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	other, err := a.cfg.Store.CreateSession(ctx, store.NewSession{Platform: "demo", Kind: "vod", TargetID: "x", Title: "다른 작업",
		DurationMs: 60000, Salt: []byte("salt")})
	if err != nil {
		t.Fatal(err)
	}
	an := s.LatestAnalysisID
	cases := []struct {
		name, code, message string
		err                 error
	}{
		{"chats at: unknown session", "NOT_FOUND", "저장된 수집 작업", chatsAtErr(a, "nope", an)},
		{"chats at: unknown analysis", "NOT_FOUND", "분석 결과", chatsAtErr(a, s.ID, "nope")},
		{"chats at: another session's analysis", "INPUT", "맞지 않습니다", chatsAtErr(a, other.ID, an)},
		{"chats at: unknown session and analysis", "NOT_FOUND", "저장된 수집 작업", chatsAtErr(a, "nope", "nope")},
		{"candidate chats: unknown analysis", "NOT_FOUND", "분석 결과", candidateChatsErr(a, "nope", "m0001")},
		{"candidate chats: unknown candidate", "NOT_FOUND", "편집 후보", candidateChatsErr(a, an, "m9999")},
	}
	for _, c := range cases {
		ue, ok := domain.AsUserError(c.err)
		if !ok || ue.Code != c.code || !strings.Contains(ue.Message, c.message) {
			t.Errorf("%s: %v, want %s (%s…)", c.name, c.err, c.code, c.message)
		}
	}
	if pc, err := a.ChatsAt(ctx, s.ID, "", 300000); err != nil || len(pc.Chats) == 0 || pc.EndMs != 330000 {
		t.Fatalf("chats at without an analysis: %+v, %v", pc, err)
	}
}

func chatsAtErr(a *App, sid, analysisID string) error {
	_, err := a.ChatsAt(context.Background(), sid, analysisID, 1000)
	return err
}

func candidateChatsErr(a *App, analysisID, candidateID string) error {
	_, _, err := a.CandidateChats(context.Background(), analysisID, candidateID, 0, 10)
	return err
}

func TestBundleAnalysisHoldsItsSnapshot(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	s, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	view, err := a.Analysis(ctx, s.LatestAnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	c := view.Candidates[0]
	// A message stored after the analysis, inside its first candidate's clip.
	late := domain.ChatEvent{ID: "late-1", OffsetMs: c.ClipStartMs + 1, AuthorKey: "x", Text: "분석 뒤에 들어온 채팅"}
	if _, err := a.cfg.Store.SavePage(ctx, s.ID, store.Page{Raw: []byte(`{"late":1}`), Events: []domain.ChatEvent{late},
		Next: domain.DemoDurationMs, Finished: true}); err != nil {
		t.Fatal(err)
	}
	if st := bundleOf(t, a, s.ID, false); st.Phase != "done" {
		t.Fatalf("bundle: %+v", st)
	}
	dir := filepath.Join(a.cfg.DataDir, "backups", s.ID)
	chat, err := os.ReadFile(filepath.Join(dir, "chat.jsonl"))
	if err != nil || !strings.Contains(string(chat), late.ID) {
		t.Fatalf("the bundle's chat should hold every message (%v)", err)
	}
	marker, err := os.ReadFile(filepath.Join(dir, "analysis", s.LatestAnalysisID, "markers", c.ID+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(marker), late.ID) || !strings.Contains(string(marker), "* ") {
		t.Fatalf("the analysis markers should hold only its snapshot:\n%s", marker)
	}
}

func TestCoveredBuckets(t *testing.T) {
	gaps := [][]domain.Gap{
		nil,
		{{StartMs: 5000, EndMs: 20000}},
		{{StartMs: 0, EndMs: 7000}, {StartMs: 6000, EndMs: 12000}}, // together they cover [5000,10000), neither alone
		{{StartMs: 30000, EndMs: 50000}, {StartMs: 0, EndMs: 10000}, {StartMs: 31000, EndMs: 39000}},
		{{StartMs: 10000, EndMs: 10000}, {StartMs: 44000, EndMs: 99000}},
	}
	const bucket, end = 5000, 47000
	for i, gs := range gaps {
		got := coveredBuckets(gs, bucket, end)
		var want []bool
		for start := int64(0); start < end; start += bucket {
			covered := false
			for _, g := range gs {
				if g.StartMs <= start && start+bucket <= g.EndMs {
					covered = true
				}
			}
			want = append(want, covered)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("gaps %d: %v, want %v", i, got, want)
		}
	}
}
