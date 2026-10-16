package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// fakeVOD is a platform whose VOD has `pages` pages of 10 messages each.
type fakeVOD struct {
	mu        sync.Mutex
	duration  int64
	pages     int
	failAt    int64         // cursor whose page is rejected (-1: never)
	block     chan struct{} // when set, VODChats waits on it (to test cancel)
	requested []int64
}

func (f *fakeVOD) ID() string             { return "fake" }
func (f *fakeVOD) Name() string           { return "가짜" }
func (f *fakeVOD) MediaOrigins() []string { return []string{"https://media.example"} }
func (f *fakeVOD) Match(raw string) (platform.Target, bool, error) {
	if id, ok := strings.CutPrefix(raw, "fake:"); ok {
		return platform.Target{Platform: "fake", Kind: platform.KindVOD, ID: id}, true, nil
	}
	return platform.Target{}, false, nil
}
func (f *fakeVOD) VODMeta(ctx context.Context, t platform.Target) (platform.VODMeta, error) {
	return platform.VODMeta{Title: "가짜 방송 " + t.ID, DurationMs: f.duration}, nil
}
func (f *fakeVOD) VODChats(ctx context.Context, t platform.Target, cursor, dur int64) (platform.ChatPage, error) {
	f.mu.Lock()
	f.requested = append(f.requested, cursor)
	block := f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return platform.ChatPage{}, ctx.Err()
		}
	}
	if cursor == f.failAt {
		return platform.ChatPage{}, &platform.PageError{Raw: []byte(`{"bad":true}`),
			Err: domain.NewUserError("PAGE_STALLED", "다음 채팅 위치가 진행되지 않거나 페이지와 일치하지 않습니다.")}
	}
	step := f.duration / int64(f.pages)
	var msgs []platform.RawChat
	for i := int64(0); i < 10; i++ {
		msgs = append(msgs, platform.RawChat{OffsetMs: cursor + 1 + i*step/10, AuthorID: fmt.Sprintf("u%d", i), Text: "ㅋㅋㅋ"})
	}
	// Overlap: pages after the first also carry the previous page's last
	// message (as CHZZK's previousVideoChats does); it must not be stored twice.
	if cursor > 0 {
		prev := cursor - step
		msgs = append([]platform.RawChat{{OffsetMs: prev + 1 + 9*step/10, AuthorID: "u9", Text: "ㅋㅋㅋ"}}, msgs...)
	}
	next := cursor + step
	if next >= f.duration {
		return platform.ChatPage{Messages: msgs, Finished: true, Raw: []byte(`{}`)}, nil
	}
	return platform.ChatPage{Messages: msgs, Next: next, Raw: []byte(fmt.Sprintf(`{"c":%d}`, cursor))}, nil
}
func (f *fakeVOD) WatchURL(t platform.Target, ms int64) string {
	return fmt.Sprintf("https://watch.example/%s?t=%d", t.ID, ms/1000)
}

// laughRule is a minimal message rule (the real rules live in detect/rules).
type laughRule struct{}

func (laughRule) ID() string                    { return "laugh" }
func (laughRule) Name() string                  { return "웃음 증가" }
func (laughRule) Share(p detect.Params) float64 { return 0.35 }
func (laughRule) Keys(text string) []string {
	if strings.Contains(text, "ㅋㅋ") {
		return []string{"laugh"}
	}
	return nil
}

func newTestApp(t *testing.T, src *fakeVOD) (*App, *events.Bus) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := &platform.Registry{}
	reg.Register(src)
	bus := events.NewBus(1024)
	a := New(Config{Version: "test", DataDir: dir, Store: st, Bus: bus, Params: detect.DefaultParams(),
		Rules: []detect.Rule{laughRule{}}, Platforms: reg})
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a, bus
}

// wait blocks until the job is no longer busy.
func wait(t *testing.T, a *App) JobStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if s := a.Job(); !s.Busy {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return JobStatus{}
}

func TestCollectFinishesAnalyzesAndExports(t *testing.T) {
	src := &fakeVOD{duration: 600000, pages: 6, failAt: -1}
	a, bus := newTestApp(t, src)
	sub := bus.Subscribe()
	defer sub.Close()

	if _, err := a.StartCollect(CollectRequest{URL: "fake:42"}); err != nil {
		t.Fatal(err)
	}
	st := wait(t, a)
	if st.Phase != "done" || st.AnalysisID == "" || st.Pages != 6 {
		t.Fatalf("job = %+v", st)
	}
	s, err := a.Session(context.Background(), st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if s.State != store.StateFinished || s.ChatCount != 60 || s.LatestAnalysisID != st.AnalysisID || s.CanResume {
		t.Fatalf("session = %+v (want 60 chats: overlap must be deduplicated)", s)
	}
	if s.WatchURL != "https://watch.example/42?t=0" {
		t.Fatalf("watch url = %q", s.WatchURL)
	}
	files, err := a.Files(context.Background(), s.ID)
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, f := range files {
		have[f.Path] = true
	}
	for _, want := range []string{"chat.txt", "chat.jsonl", "chat.received.txt", "manifest.json",
		st.AnalysisID + "/timeline.csv", st.AnalysisID + "/timeline.txt", st.AnalysisID + "/analysis-manifest.json"} {
		if !have[want] {
			t.Errorf("missing export %s (have %v)", want, files)
		}
	}
	// Progress reached subscribers as "job" events.
	sawCollecting := false
	for len(sub.C()) > 0 {
		e := <-sub.C()
		if js, ok := e.Data.(JobStatus); ok && e.Type == "job" && js.Phase == "collecting" {
			sawCollecting = true
		}
	}
	if !sawCollecting {
		t.Error("no collecting progress event published")
	}
	// Author IDs are stored only as pseudonyms.
	chat, _ := os.ReadFile(filepath.Join(a.exportDir(s.ID), "chat.txt"))
	if strings.Contains(string(chat), "][u1]") {
		t.Error("original author id leaked into chat.txt")
	}
}

func TestRejectedPageIsQuarantinedAndResumeContinues(t *testing.T) {
	src := &fakeVOD{duration: 600000, pages: 6, failAt: 300000}
	a, _ := newTestApp(t, src)
	a.StartCollect(CollectRequest{URL: "fake:7"})
	st := wait(t, a)
	if st.Phase != "error" || st.Code != "PAGE_STALLED" {
		t.Fatalf("job = %+v", st)
	}
	s, _ := a.Session(context.Background(), st.SessionID)
	if s.State != store.StatePaused || s.Cursor != 300000 || s.ErrorCode != "PAGE_STALLED" || !s.CanResume {
		t.Fatalf("session = %+v", s)
	}

	src.failAt = -1
	src.requested = nil
	if _, err := a.ResumeSession(context.Background(), s.ID, false); err != nil {
		t.Fatal(err)
	}
	st = wait(t, a)
	s, _ = a.Session(context.Background(), s.ID)
	if st.Phase != "done" || s.State != store.StateFinished || src.requested[0] != 300000 {
		t.Fatalf("resume: job %+v, session %+v, first cursor %v", st, s, src.requested)
	}
}

func TestCancelPausesAndBusyRejectsSecondJob(t *testing.T) {
	src := &fakeVOD{duration: 600000, pages: 6, failAt: -1, block: make(chan struct{})}
	a, _ := newTestApp(t, src)
	if _, err := a.StartCollect(CollectRequest{URL: "fake:1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.StartCollect(CollectRequest{URL: "fake:2"}); !isCode(err, "BUSY") {
		t.Fatalf("second job err = %v, want BUSY", err)
	}
	for a.Job().SessionID == "" {
		time.Sleep(time.Millisecond)
	}
	a.CancelJob()
	st := wait(t, a)
	s, _ := a.Session(context.Background(), st.SessionID)
	if st.Phase != "paused" || s.State != store.StatePaused || s.ErrorCode != "CANCELLED" {
		t.Fatalf("job %+v, session %+v", st, s)
	}
}

func TestResumeRefusesChangedSource(t *testing.T) {
	src := &fakeVOD{duration: 600000, pages: 6, failAt: 0}
	a, _ := newTestApp(t, src)
	a.StartCollect(CollectRequest{URL: "fake:9"})
	st := wait(t, a)
	src.duration, src.failAt = 700000, -1
	a.ResumeSession(context.Background(), st.SessionID, false)
	if st = wait(t, a); st.Code != "SOURCE_CHANGED" {
		t.Fatalf("job = %+v, want SOURCE_CHANGED", st)
	}
}

func TestUnsupportedURL(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	if _, err := a.StartCollect(CollectRequest{URL: "https://example.com/x"}); !isCode(err, "INVALID_URL") {
		t.Fatalf("err = %v", err)
	}
}

func TestDemoSessionAndReviewQueries(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	s, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if !s.IsDemo || s.CanPlay || s.CanResume || s.ChatCount != len(domain.DemoChat()) || s.LatestAnalysisID == "" {
		t.Fatalf("demo session = %+v", s)
	}
	an, err := a.Analysis(ctx, s.LatestAnalysisID)
	if err != nil {
		t.Fatal(err)
	}
	if len(an.Candidates) == 0 || an.Candidates[0].WatchURL != "" {
		t.Fatalf("analysis = %+v", an)
	}
	c := an.Candidates[0]
	chats, total, err := a.CandidateChats(ctx, an.ID, c.ID, 0, 500)
	if err != nil || total == 0 || len(chats) != min(total, 500) {
		t.Fatalf("context chats: %d/%d, %v", len(chats), total, err)
	}
	evidence := 0
	for _, ch := range chats {
		if ch.IsEvidence {
			evidence++
		}
	}
	if evidence == 0 {
		t.Fatal("no evidence marked in context")
	}
	pc, err := a.ChatsAt(ctx, s.ID, an.ID, 300000)
	if err != nil || len(pc.Chats) == 0 || pc.StartMs != 270000 || pc.EndMs != 330000 {
		t.Fatalf("chats at: %+v, %v", pc, err)
	}
	if _, err := a.Playback(ctx, s.ID); !isCode(err, "DEMO") {
		t.Fatalf("playback err = %v, want DEMO", err)
	}
}

func TestOpenFileStaysInsideExportFolder(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	s, _ := a.CreateDemo(ctx, false)
	f, err := a.OpenFile(ctx, s.ID, "chat.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	for _, bad := range []string{"../../data/momentforge.db", "../other/chat.txt", "/etc/passwd", "chat.exe", ""} {
		if _, err := a.OpenFile(ctx, s.ID, bad); !isCode(err, "NOT_FOUND") {
			t.Errorf("OpenFile(%q) err = %v, want NOT_FOUND", bad, err)
		}
	}
}

func isCode(err error, code string) bool {
	ue, ok := domain.AsUserError(err)
	return ok && ue.Code == code && !errors.Is(err, context.Canceled)
}

func TestToEventsKeepsRepeatsWithinAPage(t *testing.T) {
	salt := []byte("salt")
	msg := platform.RawChat{OffsetMs: 1000, AuthorID: "u1", Text: "ㅋㅋ"}
	evs, maxEnd := toEvents(salt, []platform.RawChat{msg, msg})
	if len(evs) != 2 || evs[0].ID == evs[1].ID || maxEnd != 1001 {
		t.Fatalf("identical messages in one page are genuine repeats: %+v", evs)
	}
	again, _ := toEvents(salt, []platform.RawChat{msg})
	if again[0].ID != evs[0].ID || again[0].AuthorKey != evs[0].AuthorKey || evs[0].AuthorKey == "u1" {
		t.Fatalf("the same message on the next page must get the same pseudonymous id: %+v vs %+v", again[0], evs[0])
	}
}
