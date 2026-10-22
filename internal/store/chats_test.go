package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(context.Background(), dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, dir
}

func TestChatCountFollowsWrites(t *testing.T) {
	ctx := context.Background()
	s, dir := newTestStore(t)
	vod, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "v", Title: "x", DurationMs: 60000, Salt: []byte("s")})
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.CreateLive(ctx, NewSession{Platform: "demo", TargetID: "c", Title: "y", Salt: []byte("t")}, LiveState{TimeBasis: "capture_elapsed"})
	if err != nil {
		t.Fatal(err)
	}
	page := func(ids ...string) Page {
		p := Page{Raw: []byte(fmt.Sprint(ids)), Next: 1000}
		for i, id := range ids {
			p.Events = append(p.Events, domain.ChatEvent{ID: id, OffsetMs: int64(i), AuthorKey: "a", Text: "t"})
		}
		return p
	}
	want := func(step string, vodCount, liveCount int) {
		t.Helper()
		for sid, n := range map[string]int{vod.ID: vodCount, live.ID: liveCount} {
			got, err := s.Session(ctx, sid)
			if err != nil || got.ChatCount != n {
				t.Fatalf("%s: session %s count %d (%v), want %d", step, sid, got.ChatCount, err, n)
			}
		}
		list, err := s.Sessions(ctx, true)
		if err != nil || len(list) != 2 {
			t.Fatalf("%s: list %v %v", step, list, err)
		}
		for _, x := range list {
			if n := map[string]int{vod.ID: vodCount, live.ID: liveCount}[x.ID]; x.ChatCount != n {
				t.Fatalf("%s: listed %s count %d, want %d", step, x.ID, x.ChatCount, n)
			}
		}
	}
	want("empty", 0, 0) // cached as 0
	if _, err := s.SavePage(ctx, vod.ID, page("e1", "e2", "e3")); err != nil {
		t.Fatal(err)
	}
	want("first page", 3, 0)
	if _, err := s.SavePage(ctx, vod.ID, page("e3", "e4")); err != nil { // e3 overlaps
		t.Fatal(err)
	}
	if _, err := s.SavePage(ctx, vod.ID, page("e4")); err != nil { // nothing new
		t.Fatal(err)
	}
	want("overlapping pages", 4, 0)
	evs := []LiveEvent{{ChatEvent: domain.ChatEvent{ID: "l1", OffsetMs: 10, Text: "a"}, Generation: 1, TimeQuality: "provider"},
		{ChatEvent: domain.ChatEvent{ID: "l2", OffsetMs: 20, Text: "b"}, Generation: 1, TimeQuality: "provider"}}
	if _, _, err := s.AppendLive(ctx, live.ID, evs, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	want("live", 4, 2)

	// A read-only store counts without a cache and agrees.
	ro, err := OpenReadOnly(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if got, err := ro.Session(ctx, live.ID); err != nil || got.ChatCount != 2 || ro.counts != nil {
		t.Fatalf("read-only count %d (%v)", got.ChatCount, err)
	}
}

func TestChatsByIDDeduplicatesAndSortsAcrossBatches(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	sess, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "v", Title: "x", DurationMs: 60000, Salt: []byte("s")})
	if err != nil {
		t.Fatal(err)
	}
	// 1,200 messages stored newest first, so arrival order is the reverse of time order.
	var events []domain.ChatEvent
	for i := 1199; i >= 0; i-- {
		events = append(events, domain.ChatEvent{ID: fmt.Sprintf("e%04d", i), OffsetMs: int64(i / 2), AuthorKey: "a", Text: "t"})
	}
	if _, err := s.SavePage(ctx, sess.ID, Page{Raw: []byte(`{}`), Events: events, Next: 60000, Finished: true}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for i := 0; i < 1200; i += 2 { // every other one, then all again: duplicates span batches
		ids = append(ids, fmt.Sprintf("e%04d", i))
	}
	for i := 0; i < 1200; i++ {
		ids = append(ids, fmt.Sprintf("e%04d", i))
	}
	ids = append(ids, "missing")
	got, err := s.ChatsByID(ctx, sess.ID, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1200 {
		t.Fatalf("got %d messages, want 1200", len(got))
	}
	for i := 1; i < len(got); i++ {
		a, b := got[i-1], got[i]
		if a.OffsetMs > b.OffsetMs || (a.OffsetMs == b.OffsetMs && a.Seq >= b.Seq) {
			t.Fatalf("not in time order at %d: %+v then %+v", i, a, b)
		}
	}
}

func TestAnalysisLoaders(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	sess, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "v", Title: "x", DurationMs: 60000, Salt: []byte("s")})
	if err != nil {
		t.Fatal(err)
	}
	an := &Analysis{SessionID: sess.ID, RulesVersion: "3.0.0", ParamsJSON: []byte(`{"windowMs":10000}`), DurationMs: 60000,
		MaxSeq: 7, EventCount: 5, DataSHA256: "abc", SourceState: StateFinished,
		Windows: []domain.Window{{T: 10000, N: 3, U: 2, Valid: true, Signals: []string{}}},
		Candidates: []domain.Candidate{{ID: "m0002", ReactionStartMs: 30000, ReactionEndMs: 40000},
			{ID: "m0001", ReactionStartMs: 10000, ReactionEndMs: 20000, EvidenceIDs: []string{"e1"}}}}
	if err := s.SaveAnalysis(ctx, an); err != nil {
		t.Fatal(err)
	}
	full, err := s.Analysis(ctx, an.ID)
	if err != nil || len(full.Windows) != 1 || len(full.Candidates) != 2 || full.Candidates[0].ID != "m0001" {
		t.Fatalf("full %+v %v", full, err)
	}
	meta, err := s.AnalysisMeta(ctx, an.ID)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Windows != nil || meta.Candidates != nil {
		t.Fatalf("meta loaded windows or candidates: %+v", meta)
	}
	full.Windows, full.Candidates = nil, nil
	if fmt.Sprint(*meta) != fmt.Sprint(*full) {
		t.Fatalf("meta %+v\nwant %+v", *meta, *full)
	}
	c, ok, err := s.AnalysisCandidate(ctx, an.ID, "m0001")
	if err != nil || !ok || c.ReactionStartMs != 10000 || len(c.EvidenceIDs) != 1 {
		t.Fatalf("candidate %+v %v %v", c, ok, err)
	}
	if _, ok, err := s.AnalysisCandidate(ctx, an.ID, "m0009"); ok || err != nil {
		t.Fatalf("unknown candidate: ok %v err %v", ok, err)
	}
	for name, load := range map[string]func(context.Context, string) (*Analysis, error){"Analysis": s.Analysis, "AnalysisMeta": s.AnalysisMeta} {
		if _, err := load(ctx, "nope"); !isNotFound(err) {
			t.Errorf("%s of an unknown id: %v, want NOT_FOUND", name, err)
		}
	}
}

func isNotFound(err error) bool {
	ue, ok := domain.AsUserError(err)
	return ok && ue.Code == "NOT_FOUND"
}

func TestChatCountUnderConcurrentReads(t *testing.T) {
	ctx := context.Background()
	s, _ := newTestStore(t)
	sess, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "v", Title: "x", DurationMs: 60000, Salt: []byte("s")})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	errs := make(chan error, 4)
	for r := 0; r < 4; r++ {
		go func() {
			for {
				select {
				case <-done:
					errs <- nil
					return
				default:
				}
				if _, err := s.Session(ctx, sess.ID); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	for p := 0; p < 60; p++ {
		var evs []domain.ChatEvent
		for i := 0; i < 10; i++ {
			evs = append(evs, domain.ChatEvent{ID: fmt.Sprintf("e%d-%d", p, i), OffsetMs: int64(p*10 + i), AuthorKey: "a", Text: "t"})
		}
		if _, err := s.SavePage(ctx, sess.ID, Page{Cursor: int64(p), Raw: []byte(fmt.Sprint(p)), Events: evs, Next: int64(p)}); err != nil {
			t.Fatal(err)
		}
	}
	close(done)
	for r := 0; r < 4; r++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Session(ctx, sess.ID)
	if err != nil || got.ChatCount != 600 {
		t.Fatalf("count %d (%v), want 600", got.ChatCount, err)
	}
}
