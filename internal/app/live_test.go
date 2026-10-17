package app

import (
	"context"
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

// fakeLive is a LIVE platform whose chat comes from a per-connection script.
// Chat times are synthetic: a script can send 20 minutes of chat at once.
type fakeLive struct {
	mu     sync.Mutex
	meta   platform.LiveMeta
	conns  int
	script func(ctx context.Context, n int, sink platform.LiveSink) error
}

func (f *fakeLive) ID() string             { return "fakelive" }
func (f *fakeLive) Name() string           { return "가짜 생방송" }
func (f *fakeLive) MediaOrigins() []string { return nil }
func (f *fakeLive) Match(raw string) (platform.Target, bool, error) {
	if id, ok := strings.CutPrefix(raw, "live:"); ok {
		return platform.Target{Platform: "fakelive", Kind: platform.KindLive, ID: id}, true, nil
	}
	return platform.Target{}, false, nil
}
func (f *fakeLive) LiveMeta(ctx context.Context, t platform.Target) (platform.LiveMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.meta
	m.ObservedAt = time.Now()
	return m, nil
}
func (f *fakeLive) LiveStream(ctx context.Context, t platform.Target, room string, sink platform.LiveSink) error {
	f.mu.Lock()
	f.conns++
	n, script := f.conns, f.script
	f.mu.Unlock()
	return script(ctx, n, sink)
}
func (f *fakeLive) set(fn func(m *platform.LiveMeta)) {
	f.mu.Lock()
	fn(&f.meta)
	f.mu.Unlock()
}

func newFakeLive(origin time.Time) *fakeLive {
	return &fakeLive{meta: platform.LiveMeta{LiveID: "L1", Title: "가짜 생방송", Open: true, ChatRoom: "R1",
		StartedAt: origin, Viewers: 1000}}
}

func newLiveApp(t *testing.T, src platform.Platform, tune func(*liveTiming)) *App {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), dir, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	reg := &platform.Registry{}
	reg.Register(src)
	a := New(Config{Version: "test", DataDir: dir, Store: st, Bus: events.NewBus(1024), Params: detect.DefaultParams(),
		Rules: []detect.Rule{laughRule{}}, Platforms: reg})
	a.liveTiming = liveTiming{queue: 10000, publishEvery: 10 * time.Millisecond, publishBatch: 200,
		metaEvery: 20 * time.Millisecond, closedWait: 10 * time.Millisecond, giveUpAfter: 5 * time.Second,
		maxBackoff: 10 * time.Millisecond, delayMs: 5000}
	if tune != nil {
		tune(&a.liveTiming)
	}
	t.Cleanup(func() { a.Shutdown(context.Background()) })
	return a
}

// msg is a chat sent at origin+ms and received 100 ms later.
func msg(origin time.Time, ms int64, author, text, id string) platform.LiveChat {
	at := origin.Add(time.Duration(ms) * time.Millisecond)
	return platform.LiveChat{AuthorID: author, Text: text, MessageID: id, EventTime: at, ReceivedAt: at.Add(100 * time.Millisecond)}
}

// feed sends one background message a second over [from, to) and a laugh
// burst from 20 viewers at laughAt (when inside the range). It returns how
// many messages it sent.
func feed(sink platform.LiveSink, origin time.Time, from, to, laughAt int64) (int, error) {
	n := 0
	for ms := from; ms < to; ms += 1000 {
		if err := sink.Chat(msg(origin, ms, fmt.Sprintf("b%d", ms/1000%15), "안녕하세요", fmt.Sprintf("m%d", ms))); err != nil {
			return n, err
		}
		n++
		if ms >= laughAt && ms < laughAt+20000 {
			for i := range 20 {
				if err := sink.Chat(msg(origin, ms+int64(i)*40, fmt.Sprintf("r%d", i), "ㅋㅋㅋㅋ", fmt.Sprintf("x%d-%d", ms, i))); err != nil {
					return n, err
				}
				n++
			}
		}
	}
	sink.Heartbeat(origin.Add(time.Duration(to) * time.Millisecond))
	return n, nil
}

func waitLive(t *testing.T, a *App, sid string, what string, ok func(LiveView) bool) LiveView {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := a.LiveStatus(context.Background(), sid)
		if err != nil {
			t.Fatal(err)
		}
		if ok(v) {
			return v
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; state=%s code=%s msg=%s", what, v.Session.State, v.Session.ErrorCode, v.Session.ErrorMessage)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stopped(v LiveView) bool { return !v.Running && store.LiveTerminal(v.Session.State) }

const minute = int64(60000)

func TestLiveCaptureStoresAnalyzesStopsAndExports(t *testing.T) {
	origin := time.Now().Add(-10 * time.Minute)
	src := newFakeLive(origin)
	sent := make(chan int, 1)
	src.script = func(ctx context.Context, n int, sink platform.LiveSink) error {
		sink.Connected(origin.Add(10 * time.Minute))
		count, err := feed(sink, origin, 10*minute+1000, 30*minute, 25*minute)
		if err != nil {
			return err
		}
		sent <- count
		<-ctx.Done()
		return ctx.Err()
	}
	a := newLiveApp(t, src, nil)
	ctx := context.Background()
	info, err := a.StartLive(ctx, LiveRequest{URL: "live:ch1"})
	if err != nil {
		t.Fatal(err)
	}
	if info.Kind != "live" {
		t.Fatalf("kind = %q", info.Kind)
	}
	count := <-sent
	v := waitLive(t, a, info.ID, "analysis to catch up", func(v LiveView) bool {
		return v.Session.State == store.LiveCollecting && v.WatermarkMs >= 30*minute-6000
	})
	if v.TimeBasis != "broadcast_elapsed" || v.Generation != 1 {
		t.Fatalf("basis=%s generation=%d", v.TimeBasis, v.Generation)
	}
	cands, rev, err := a.LiveCandidates(ctx, info.ID, 0)
	if err != nil || len(cands) == 0 || rev == 0 {
		t.Fatalf("candidates=%d rev=%d err=%v", len(cands), rev, err)
	}
	if c := cands[0]; c.ClipStartMs > 25*minute || c.ClipEndMs < 25*minute || len(c.Evidence) == 0 {
		t.Fatalf("candidate %+v", c.Candidate)
	}
	if again, _, _ := a.LiveCandidates(ctx, info.ID, rev); len(again) != 0 {
		t.Fatalf("nothing changed after rev %d, got %d", rev, len(again))
	}
	recent, _ := a.LiveChats(ctx, info.ID, 0)
	if len(recent) != 300 || recent[len(recent)-1].Seq < recent[0].Seq {
		t.Fatalf("recent = %d", len(recent))
	}
	d, err := a.LiveDensity(ctx, info.ID)
	if err != nil || d.BucketMs != 5000 || len(d.Windows) == 0 || d.Windows[0].Valid {
		t.Fatalf("density: bucket=%d windows=%d err=%v (the unobserved start comes first)", d.BucketMs, len(d.Windows), err)
	}
	peak := domain.Window{}
	for _, w := range d.Windows {
		if w.N > peak.N {
			peak = w
		}
	}
	if peak.T < 25*minute || peak.T > 25*minute+25000 {
		t.Fatalf("densest bucket ends at %d, want the laugh burst", peak.T)
	}
	around, _ := a.LiveContext(ctx, info.ID, 25*minute)
	if len(around) == 0 || len(around) > 300 {
		t.Fatalf("context = %d", len(around))
	}

	// Starting the same channel again returns the running capture; another channel is refused.
	if same, err := a.StartLive(ctx, LiveRequest{URL: "live:ch1"}); err != nil || same.ID != info.ID {
		t.Fatalf("same channel: %v %v", same.ID, err)
	}
	if _, err := a.StartLive(ctx, LiveRequest{URL: "live:ch2"}); !isCode(err, "LIVE_BUSY") {
		t.Fatalf("other channel: %v", err)
	}

	a.StopLive(info.ID)
	v = waitLive(t, a, info.ID, "stop", stopped)
	if v.Session.State != store.LiveStopped || v.ClosedReason != "수집을 중단했습니다." || v.Session.ChatCount != count {
		t.Fatalf("state=%s reason=%q chats=%d/%d", v.Session.State, v.ClosedReason, v.Session.ChatCount, count)
	}
	if len(v.Gaps) == 0 || v.Gaps[0].StartMs != 0 || v.Gaps[0].EndMs < 10*minute-1000 {
		t.Fatalf("gaps = %+v (want the unobserved start)", v.Gaps)
	}
	final, _, _ := a.LiveCandidates(ctx, info.ID, 0)
	for _, c := range final {
		if c.State != domain.CandidateConfirmed && c.State != domain.CandidatePartial {
			t.Fatalf("%s left %s after stop", c.ID, c.State)
		}
	}
	if !v.Session.CanResume {
		t.Fatal("a stopped capture of an open broadcast can resume")
	}

	// The final checkpoint is complete by the time the stop is visible.
	files, _ := a.Files(ctx, info.ID)
	have := map[string]bool{}
	var checkpoint string
	for _, f := range files {
		have[f.Path] = true
		if strings.HasSuffix(f.Path, "/analysis-manifest.json") {
			checkpoint = strings.TrimSuffix(f.Path, "analysis-manifest.json")
		}
	}
	if manifest, err := os.ReadFile(filepath.Join(a.exportDir(info.ID), filepath.FromSlash(checkpoint), "manifest.json")); err != nil ||
		!strings.Contains(string(manifest), `"state": "stopped"`) {
		t.Fatalf("final checkpoint manifest: %v %s", err, manifest)
	}
	for _, want := range []string{"chat.received.txt", checkpoint + "chat.txt", checkpoint + "chat.jsonl",
		checkpoint + "manifest.json", checkpoint + "timeline.txt", checkpoint + "timeline.csv"} {
		if !have[want] {
			t.Errorf("missing %s in %v", want, files)
		}
	}
	received, _ := os.ReadFile(filepath.Join(a.exportDir(info.ID), "chat.received.txt"))
	lines := strings.Split(strings.TrimSpace(string(received)), "\n")
	if !strings.HasPrefix(lines[0], "# MomentForge chat v1 | mode=LIVE | time_basis=broadcast_elapsed | live_id=L1") || len(lines) != count+1 {
		t.Fatalf("received TXT: %d lines, header %q", len(lines), lines[0])
	}
	timeline, _ := os.ReadFile(filepath.Join(a.exportDir(info.ID), filepath.FromSlash(checkpoint), "timeline.txt"))
	if !strings.Contains(string(timeline), "시간 기준: 방송 시작 후 경과") || !strings.Contains(string(timeline), "LIVE 상태:") {
		t.Fatalf("timeline.txt:\n%s", timeline)
	}
}

func TestLiveReconnectDropsReplaysAndRecordsGap(t *testing.T) {
	origin := time.Now().Add(-10 * time.Minute)
	src := newFakeLive(origin)
	ready := make(chan struct{})
	src.script = func(ctx context.Context, n int, sink platform.LiveSink) error {
		switch n {
		case 1:
			sink.Connected(origin.Add(10 * time.Minute))
			if _, err := feed(sink, origin, 10*minute+1000, 12*minute, -1); err != nil {
				return err
			}
			return domain.NewUserError("LIVE_DISCONNECTED", "채팅 연결이 끊겼습니다. 다시 연결합니다.")
		default:
			sink.Connected(origin.Add(13 * time.Minute))
			// The platform re-sends recent chat on connect: 5 already stored, 2 never seen.
			for ms := 12*minute - 5000; ms < 12*minute; ms += 1000 {
				sink.Chat(msg(origin, ms, fmt.Sprintf("b%d", ms/1000%15), "안녕하세요", fmt.Sprintf("m%d", ms)))
			}
			sink.Chat(msg(origin, 12*minute+30000, "z1", "끊긴 사이 채팅", "gap-1"))
			sink.Chat(msg(origin, 12*minute+31000, "z2", "끊긴 사이 채팅", "gap-2"))
			if _, err := feed(sink, origin, 13*minute+1000, 15*minute, -1); err != nil {
				return err
			}
			close(ready)
			<-ctx.Done()
			return ctx.Err()
		}
	}
	a := newLiveApp(t, src, nil)
	info, err := a.StartLive(context.Background(), LiveRequest{URL: "live:ch1"})
	if err != nil {
		t.Fatal(err)
	}
	<-ready
	v := waitLive(t, a, info.ID, "second connection to be stored", func(v LiveView) bool {
		return v.Generation == 2 && v.WatermarkMs >= 15*minute-6000
	})
	if v.DuplicateCount != 5 || v.LateCount < 2 {
		t.Fatalf("duplicates=%d late=%d", v.DuplicateCount, v.LateCount)
	}
	var gap *domain.Gap
	for i, g := range v.Gaps {
		if g.EndMs == 13*minute {
			gap = &v.Gaps[i]
		}
	}
	if gap == nil || gap.StartMs != 12*minute { // the last heartbeat before the disconnect
		t.Fatalf("gaps = %+v (want the disconnect from the last received message to the reconnect)", v.Gaps)
	}
	// 119 + 119 messages, the 2 history messages kept, the 5 replays dropped.
	if v.Session.ChatCount != 119+119+2 {
		t.Fatalf("chats = %d", v.Session.ChatCount)
	}
	a.StopLive(info.ID)
	waitLive(t, a, info.ID, "stop", stopped)
}

func TestLiveEndsWhenBroadcastCloses(t *testing.T) {
	origin := time.Now().Add(-10 * time.Minute)
	src := newFakeLive(origin)
	src.script = func(ctx context.Context, n int, sink platform.LiveSink) error {
		sink.Connected(origin.Add(10 * time.Minute))
		if _, err := feed(sink, origin, 10*minute+1000, 14*minute, 12*minute); err != nil {
			return err
		}
		src.set(func(m *platform.LiveMeta) { m.Open = false })
		<-ctx.Done()
		return ctx.Err()
	}
	a := newLiveApp(t, src, nil)
	info, err := a.StartLive(context.Background(), LiveRequest{URL: "live:ch1", ViewerAdaptive: true})
	if err != nil {
		t.Fatal(err)
	}
	v := waitLive(t, a, info.ID, "end", stopped)
	if v.Session.State != store.LiveEnded || v.ClosedReason != "방송이 종료되었습니다." || v.Session.CanResume {
		t.Fatalf("state=%s reason=%q canResume=%v", v.Session.State, v.ClosedReason, v.Session.CanResume)
	}
	if v.ViewerCount != 1000 {
		t.Fatalf("viewer count = %d", v.ViewerCount)
	}
	if v.WatermarkMs < 14*minute-1000 {
		t.Fatalf("final watermark %d did not reach the last message", v.WatermarkMs)
	}
	if _, err := a.ResumeLive(context.Background(), info.ID); !isCode(err, "LIVE_OFFLINE") {
		t.Fatalf("resume after end: %v", err)
	}
}

// fakeLivePlay is fakeLive with a public stream of the current broadcast.
type fakeLivePlay struct {
	*fakeLive
	asked []platform.Target
}

func (f *fakeLivePlay) LivePlayback(ctx context.Context, t platform.Target) (platform.Playback, error) {
	f.asked = append(f.asked, t)
	return platform.Playback{URL: "https://media.example/" + t.ID + ".m3u8?sig=secret", Type: "hls"}, nil
}

func TestLivePlaybackResolvesTheBroadcastWithoutStoringIt(t *testing.T) {
	ctx := context.Background()
	origin := time.Now().Add(-10 * time.Minute)
	idle := func(ctx context.Context, n int, sink platform.LiveSink) error {
		sink.Connected(origin.Add(10 * time.Minute))
		<-ctx.Done()
		return ctx.Err()
	}
	src := newFakeLive(origin)
	src.script = idle
	play := &fakeLivePlay{fakeLive: src}
	a := newLiveApp(t, play, nil)
	info, err := a.StartLive(ctx, LiveRequest{URL: "live:ch1"})
	if err != nil {
		t.Fatal(err)
	}
	if !info.CanPlay {
		t.Fatal("a LIVE capture on a platform with a live stream should be playable")
	}
	pb, err := a.Playback(ctx, info.ID)
	if err != nil || pb.URL != "https://media.example/ch1.m3u8?sig=secret" || pb.Type != "hls" {
		t.Fatalf("playback = %+v, %v", pb, err)
	}
	if len(play.asked) != 1 || play.asked[0] != (platform.Target{Platform: "fakelive", Kind: platform.KindLive, ID: "ch1"}) {
		t.Fatalf("asked %+v", play.asked)
	}
	a.StopLive(info.ID)
	waitLive(t, a, info.ID, "stop", stopped)
	a.Shutdown(ctx)
	// The signed URL was returned, never written down.
	filepath.WalkDir(a.cfg.DataDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if b, err := os.ReadFile(path); err == nil && strings.Contains(string(b), "sig=secret") {
			t.Errorf("%s contains the stream URL", path)
		}
		return nil
	})

	// A platform without a live stream: not playable, refused.
	plain := newFakeLive(origin)
	plain.script = idle
	b := newLiveApp(t, plain, nil)
	info, err = b.StartLive(ctx, LiveRequest{URL: "live:ch1"})
	if err != nil {
		t.Fatal(err)
	}
	if info.CanPlay {
		t.Fatal("a platform without LivePlayback should not be playable")
	}
	if _, err := b.Playback(ctx, info.ID); !isCode(err, "PLAYBACK_UNAVAILABLE") {
		t.Fatalf("playback without a stream: %v", err)
	}
}

func TestLiveResumeContinuesSameBroadcastOnly(t *testing.T) {
	origin := time.Now().Add(-10 * time.Minute)
	src := newFakeLive(origin)
	connected := make(chan int, 4)
	src.script = func(ctx context.Context, n int, sink platform.LiveSink) error {
		sink.Connected(time.Now())
		sink.Chat(platform.LiveChat{AuthorID: "a", Text: fmt.Sprintf("연결 %d", n), ReceivedAt: time.Now(), EventTime: time.Now()})
		connected <- n
		<-ctx.Done()
		return ctx.Err()
	}
	a := newLiveApp(t, src, nil)
	ctx := context.Background()
	info, err := a.StartLive(ctx, LiveRequest{URL: "live:ch1", Sensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	<-connected
	waitLive(t, a, info.ID, "first message", func(v LiveView) bool { return v.Session.ChatCount == 1 })
	a.StopLive(info.ID)
	waitLive(t, a, info.ID, "stop", stopped)

	if _, err := a.ResumeLive(ctx, info.ID); err != nil {
		t.Fatal(err)
	}
	<-connected
	v := waitLive(t, a, info.ID, "second connection", func(v LiveView) bool { return v.Session.ChatCount == 2 && v.Generation == 2 })
	if !v.Sensitive {
		t.Fatal("resume keeps the saved settings")
	}
	if _, err := a.ResumeLive(ctx, info.ID); !isCode(err, "LIVE_BUSY") {
		t.Fatalf("resume while running: %v", err)
	}
	a.StopLive(info.ID)
	waitLive(t, a, info.ID, "stop", stopped)

	src.set(func(m *platform.LiveMeta) { m.LiveID = "L2" })
	if _, err := a.ResumeLive(ctx, info.ID); !isCode(err, "LIVE_CHANGED") {
		t.Fatalf("resume of another broadcast: %v", err)
	}
}

func TestLiveFailures(t *testing.T) {
	origin := time.Now().Add(-time.Minute)
	cases := []struct {
		name   string
		tune   func(*liveTiming)
		script func(ctx context.Context, n int, sink platform.LiveSink) error
		code   string
	}{
		{"schema change stops at once", nil, func(ctx context.Context, n int, sink platform.LiveSink) error {
			sink.Connected(time.Now())
			return domain.NewUserError("LIVE_SCHEMA", "채팅 응답 형식이 바뀌었습니다.")
		}, "LIVE_SCHEMA"},
		{"no recovery within the limit", func(lt *liveTiming) { lt.giveUpAfter = 50 * time.Millisecond },
			func(ctx context.Context, n int, sink platform.LiveSink) error {
				return domain.NewUserError("LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다.")
			}, "LIVE_TIMEOUT"},
		{"full queue is an error, not silent loss", func(lt *liveTiming) { lt.queue = 1 },
			func(ctx context.Context, n int, sink platform.LiveSink) error {
				sink.Connected(time.Now())
				for i := range 100000 {
					if err := sink.Chat(platform.LiveChat{Text: "ㅋㅋ", ReceivedAt: time.Now(), MessageID: fmt.Sprint(i)}); err != nil {
						return err
					}
				}
				return nil
			}, "LIVE_OVERLOAD"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := newFakeLive(origin)
			src.script = tc.script
			a := newLiveApp(t, src, tc.tune)
			info, err := a.StartLive(context.Background(), LiveRequest{URL: "live:ch1"})
			if err != nil {
				t.Fatal(err)
			}
			v := waitLive(t, a, info.ID, "failure", stopped)
			if v.Session.State != store.LiveError || v.Session.ErrorCode != tc.code {
				t.Fatalf("state=%s code=%s", v.Session.State, v.Session.ErrorCode)
			}
		})
	}
}

func TestLiveStartChecksAndRecovery(t *testing.T) {
	src := newFakeLive(time.Time{})
	src.set(func(m *platform.LiveMeta) { m.Open = false })
	a := newLiveApp(t, src, nil)
	ctx := context.Background()
	if _, err := a.StartLive(ctx, LiveRequest{URL: "live:ch1"}); !isCode(err, "LIVE_OFFLINE") {
		t.Fatalf("offline: %v", err)
	}
	if _, err := a.StartCollect(CollectRequest{URL: "live:ch1"}); !isCode(err, "LIVE_URL") {
		t.Fatalf("LIVE URL in the VOD form: %v", err)
	}

	// A capture left running by a crash becomes incomplete at the next start.
	s, err := a.cfg.Store.CreateLive(ctx, store.NewSession{Platform: "fakelive", TargetID: "ch1", Title: "t", Salt: []byte("s")},
		store.LiveState{ChannelID: "ch1", LiveID: "L1", OriginMs: 1, TimeBasis: "capture_elapsed", ParamsJSON: []byte(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RecoverLive(ctx); err != nil {
		t.Fatal(err)
	}
	v, _ := a.LiveStatus(ctx, s.ID)
	if v.Session.State != store.LiveIncomplete || v.Session.ErrorCode != "PROCESS_RESTART" || !v.Session.CanResume {
		t.Fatalf("after recovery: %+v", v.Session)
	}
	if v.TimeBasis != "capture_elapsed" {
		t.Fatalf("basis = %s", v.TimeBasis)
	}
}
