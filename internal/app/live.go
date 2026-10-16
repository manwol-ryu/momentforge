package app

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"strconv"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/privacy"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// liveTiming is how often the LIVE job does things (tests shorten it).
type liveTiming struct {
	queue        int           // inbox capacity (CzzEdit: 10,000)
	publishEvery time.Duration // store/analyze at least this often (1 s)
	publishBatch int           // …or after this many messages (200)
	metaEvery    time.Duration // re-check the broadcast (60 s)
	closedWait   time.Duration // wait before re-checking an apparently closed broadcast (60 s)
	giveUpAfter  time.Duration // stop reconnecting after failing this long (10 min)
	maxBackoff   time.Duration // (60 s)
	delayMs      int64         // analysis trails the newest healthy time by this (5 s)
}

func defaultLiveTiming() liveTiming {
	return liveTiming{queue: 10000, publishEvery: time.Second, publishBatch: 200, metaEvery: 60 * time.Second,
		closedWait: 60 * time.Second, giveUpAfter: 10 * time.Minute, maxBackoff: 60 * time.Second, delayMs: 5000}
}

// fatalLive codes end the capture instead of reconnecting.
var fatalLive = map[string]bool{"LIVE_ACCESS": true, "LIVE_SCHEMA": true, "LIVE_CLOCK": true, "LIVE_OVERLOAD": true}

// LiveRequest starts a LIVE capture.
type LiveRequest struct {
	URL            string
	Sensitive      bool
	ViewerAdaptive bool
}

// LiveInfo is what "연결 확인" shows before capturing.
type LiveInfo struct {
	PlatformName string
	ChannelID    string
	LiveID       string
	Title        string
	Open         bool
	Viewers      int // -1 unknown
	StartedAt    time.Time
}

type liveRun struct {
	sid     string
	channel string
	cancel  context.CancelFunc
	done    chan struct{}
}

type liveJobs struct {
	mu     sync.Mutex
	active *liveRun
	start  sync.Mutex // one start or resume at a time, so two cannot both pass the busy check
	export sync.Mutex // one checkpoint at a time
}

func (a *App) resolveLive(raw string) (platform.Platform, platform.LiveSource, platform.Target, error) {
	p, t, err := a.cfg.Platforms.Resolve(raw)
	if err != nil {
		return nil, nil, t, err
	}
	src, ok := p.(platform.LiveSource)
	if !ok || t.Kind != platform.KindLive {
		return nil, nil, t, domain.NewUserError("LIVE_URL", "생방송 주소를 입력해 주세요. 다시보기는 위의 다시보기 수집 칸을 사용하세요.")
	}
	return p, src, t, nil
}

// InspectLive checks a channel without capturing.
func (a *App) InspectLive(ctx context.Context, raw string) (LiveInfo, error) {
	p, src, t, err := a.resolveLive(raw)
	if err != nil {
		return LiveInfo{}, err
	}
	m, err := src.LiveMeta(ctx, t)
	if err != nil {
		return LiveInfo{}, err
	}
	return LiveInfo{PlatformName: p.Name(), ChannelID: t.ID, LiveID: m.LiveID, Title: m.Title, Open: m.Open,
		Viewers: m.Viewers, StartedAt: m.StartedAt}, nil
}

var errLiveOffline = domain.NewUserError("LIVE_OFFLINE", "현재 공개 채팅을 받을 수 있는 생방송이 아닙니다.")

// StartLive begins capturing a broadcast. Only one LIVE capture runs at a
// time; starting the same channel again returns the running session.
func (a *App) StartLive(ctx context.Context, req LiveRequest) (SessionInfo, error) {
	a.live.start.Lock()
	defer a.live.start.Unlock()
	p, src, t, err := a.resolveLive(req.URL)
	if err != nil {
		return SessionInfo{}, err
	}
	if running, ok := a.runningLive(); ok {
		if running.channel == t.ID {
			return a.Session(ctx, running.sid)
		}
		return SessionInfo{}, domain.NewUserError("LIVE_BUSY", "진행 중인 라이브를 중단한 뒤 다른 방송을 시작하세요.")
	}
	meta, err := src.LiveMeta(ctx, t)
	if err != nil {
		return SessionInfo{}, err
	}
	if !meta.Open || meta.ChatRoom == "" || meta.LiveID == "" {
		return SessionInfo{}, errLiveOffline
	}
	salt, err := privacy.NewSalt()
	if err != nil {
		return SessionInfo{}, err
	}
	params := a.params(req.Sensitive)
	pj, _ := json.Marshal(params)
	origin, basis := time.Now().UnixMilli(), "capture_elapsed"
	if !meta.StartedAt.IsZero() {
		origin, basis = meta.StartedAt.UnixMilli(), "broadcast_elapsed"
	}
	s, err := a.cfg.Store.CreateLive(ctx, store.NewSession{Platform: p.ID(), TargetID: t.ID, Title: meta.Title, Salt: salt},
		store.LiveState{ChannelID: t.ID, LiveID: meta.LiveID, OriginMs: origin, TimeBasis: basis, ParamsJSON: pj,
			ViewerAdaptive: req.ViewerAdaptive})
	if err != nil {
		return SessionInfo{}, err
	}
	if err := a.launchLive(s.ID, src, t); err != nil {
		return SessionInfo{}, err
	}
	return a.Session(ctx, s.ID)
}

// ResumeLive continues a stopped capture of the same broadcast.
func (a *App) ResumeLive(ctx context.Context, sid string) (SessionInfo, error) {
	a.live.start.Lock()
	defer a.live.start.Unlock()
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return SessionInfo{}, err
	}
	ls, err := a.cfg.Store.LiveState(ctx, sid)
	if err != nil {
		return SessionInfo{}, err
	}
	p, ok := a.cfg.Platforms.Get(s.Platform)
	src, isLive := p.(platform.LiveSource)
	if !ok || !isLive {
		return SessionInfo{}, domain.NewUserError("UNSUPPORTED", "이 기록은 이어 기록할 수 없습니다.")
	}
	if !store.LiveTerminal(s.State) {
		return SessionInfo{}, domain.NewUserError("LIVE_BUSY", "이미 수집 중인 기록입니다.")
	}
	if _, running := a.runningLive(); running {
		return SessionInfo{}, domain.NewUserError("LIVE_BUSY", "진행 중인 라이브를 중단한 뒤 다른 방송을 시작하세요.")
	}
	t := platform.Target{Platform: s.Platform, Kind: platform.KindLive, ID: ls.ChannelID}
	meta, err := src.LiveMeta(ctx, t)
	if err != nil {
		return SessionInfo{}, err
	}
	if !meta.Open || meta.ChatRoom == "" {
		return SessionInfo{}, errLiveOffline
	}
	if meta.LiveID != ls.LiveID {
		return SessionInfo{}, domain.NewUserError("LIVE_CHANGED", "이전 기록과 방송 회차가 다릅니다. 새 수집을 시작하세요.")
	}
	if err := a.cfg.Store.SaveLiveState(ctx, sid, store.LiveStarting, "", "", ls); err != nil {
		return SessionInfo{}, err
	}
	if err := a.launchLive(sid, src, t); err != nil {
		return SessionInfo{}, err
	}
	return a.Session(ctx, sid)
}

func (a *App) runningLive() (liveRun, bool) {
	a.live.mu.Lock()
	defer a.live.mu.Unlock()
	if a.live.active == nil {
		return liveRun{}, false
	}
	select {
	case <-a.live.active.done:
		return liveRun{}, false
	default:
		return *a.live.active, true
	}
}

func (a *App) launchLive(sid string, src platform.LiveSource, t platform.Target) error {
	a.live.mu.Lock()
	defer a.live.mu.Unlock()
	ctx, cancel := context.WithCancel(a.ctx)
	run := &liveRun{sid: sid, channel: t.ID, cancel: cancel, done: make(chan struct{})}
	a.live.active = run
	go func() {
		defer close(run.done)
		defer cancel()
		w := &liveWorker{a: a, sid: sid, src: src, target: t, timing: a.liveTiming}
		if err := w.run(ctx); err != nil {
			a.logError("live capture", err)
		}
	}()
	return nil
}

// StopLive asks a running capture to save what it has and finish.
func (a *App) StopLive(sid string) {
	a.live.mu.Lock()
	run := a.live.active
	a.live.mu.Unlock()
	if run != nil && run.sid == sid {
		run.cancel()
	}
}

// stopLive cancels the running capture and waits (shutdown).
func (a *App) stopLive(ctx context.Context) {
	a.live.mu.Lock()
	run := a.live.active
	a.live.mu.Unlock()
	if run == nil {
		return
	}
	run.cancel()
	select {
	case <-run.done:
	case <-ctx.Done():
	}
}

// RecoverLive marks captures left running by a crash as incomplete.
func (a *App) RecoverLive(ctx context.Context) error {
	_, err := a.cfg.Store.MarkLiveIncomplete(ctx, "앱이 종료되어 수집이 중단되었습니다. 같은 방송이면 이어 기록할 수 있습니다.")
	return err
}

// --- the capture itself ---

type liveKind int

const (
	evConnected liveKind = iota
	evChat
	evHeartbeat
	evDisconnected
	evMetadata
	evViewerMissing
)

type liveMsg struct {
	kind    liveKind
	gen     int
	at      time.Time
	chat    platform.LiveChat
	meta    platform.LiveMeta
	code    string
	message string
}

var errOverload = domain.NewUserError("LIVE_OVERLOAD", "채팅 저장 대기열이 가득 찼습니다. 미저장 구간을 확인하세요.")

// liveWorker owns one capture: a receiver goroutine feeds a bounded inbox,
// and run (the only writer) stores, analyzes and publishes.
type liveWorker struct {
	a      *App
	sid    string
	src    platform.LiveSource
	target platform.Target
	timing liveTiming
	inbox  chan liveMsg

	salt     []byte
	ls       store.LiveState
	params   detect.Params
	analyzer *detect.LiveAnalyzer
	dir      string

	state, errCode, errMsg string
	pending                []platform.LiveChat
	pendingGen             []int
	connected              bool
	healthy                int64 // wall ms of the last sign of life; 0 none
	gapStart, segmentStart int64 // timeline ms
	seen                   map[string]int

	// fatal stops the capture from the writer side (storage failure);
	// lostFrom is the earliest timeline ms of chat that could not be stored, -1 none.
	fatal       error
	lostFrom    int64
	stopReceive context.CancelFunc
}

func (w *liveWorker) emit(m liveMsg) error {
	select {
	case w.inbox <- m:
		return nil
	default:
		return errOverload
	}
}

type liveSink struct {
	w   *liveWorker
	gen *int
	on  func()
}

func (s liveSink) Connected(at time.Time) {
	*s.gen++
	s.w.emit(liveMsg{kind: evConnected, gen: *s.gen, at: at})
	s.on()
}
func (s liveSink) Chat(c platform.LiveChat) error {
	return s.w.emit(liveMsg{kind: evChat, gen: *s.gen, chat: c})
}
func (s liveSink) Heartbeat(at time.Time) { s.w.emit(liveMsg{kind: evHeartbeat, gen: *s.gen, at: at}) }

const (
	endedNewLive = "방송 회차가 바뀌었습니다. 새 수집을 시작하세요."
	endedClosed  = "방송이 종료되었습니다."
)

var errRoomChanged = errors.New("chat room changed")

// pollResult says why poll stopped the stream: ended is a reason the
// broadcast is over, or err is errRoomChanged.
type pollResult struct {
	ended string
	err   error
}

// receive connects and reconnects until ctx ends, the broadcast ends
// (ended is the reason), or a permanent error. A user stop returns "", nil.
func (w *liveWorker) receive(ctx context.Context, startGen int) (ended string, _ error) {
	gen, failures, closed := startGen, 0, 0
	var failedSince time.Time
	for ctx.Err() == nil {
		meta, err := w.src.LiveMeta(ctx, w.target)
		if err == nil {
			switch {
			case meta.LiveID != w.ls.LiveID:
				return endedNewLive, nil
			case !meta.Open:
				if closed++; closed >= 2 {
					return endedClosed, nil
				}
				w.emit(liveMsg{kind: evDisconnected, at: time.Now(), code: "LIVE_CLOSED_CHECK", message: "방송 종료 여부를 확인하고 있습니다."})
				if sleepCtx(ctx, w.timing.closedWait) != nil {
					return "", nil
				}
				continue
			case meta.ChatRoom == "":
				err = domain.NewUserError("LIVE_NO_ROOM", "공개 채팅방을 찾지 못해 다시 확인합니다.")
			default:
				closed = 0
				streamCtx, stop := context.WithCancel(ctx)
				polled := make(chan pollResult, 1)
				sink := liveSink{w: w, gen: &gen, on: func() {
					failures, failedSince = 0, time.Time{}
					w.emit(liveMsg{kind: evMetadata, meta: meta})
				}}
				go func() { polled <- w.poll(streamCtx, stop, meta.ChatRoom, &closed) }()
				err = w.src.LiveStream(streamCtx, w.target, meta.ChatRoom, sink)
				stop()
				if r := <-polled; r.ended != "" {
					return r.ended, nil
				} else if r.err != nil {
					err = domain.NewUserError("LIVE_ROOM", "채팅방이 바뀌어 다시 연결합니다.")
				}
			}
		}
		if ctx.Err() != nil {
			return "", nil
		}
		code, msg := "LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다."
		if ue, ok := domain.AsUserError(err); ok {
			code, msg = ue.Code, ue.Message
		}
		if fatalLive[code] {
			return "", err
		}
		if w.emit(liveMsg{kind: evDisconnected, at: time.Now(), code: code, message: msg}) != nil {
			return "", errOverload
		}
		if failedSince.IsZero() {
			failedSince = time.Now()
		}
		if time.Since(failedSince) >= w.timing.giveUpAfter {
			return "", domain.NewUserError("LIVE_TIMEOUT", "10분간 연결을 복구하지 못했습니다. 저장한 작업에서 이어 기록하세요.")
		}
		delay := min(w.timing.maxBackoff, time.Duration(1<<min(failures, 6))*time.Second) + time.Duration(rand.IntN(300))*time.Millisecond
		var ra interface{ RetryAfter() time.Duration }
		if errors.As(err, &ra) {
			delay = max(delay, ra.RetryAfter())
		}
		failures++
		if sleepCtx(ctx, delay) != nil {
			return "", nil
		}
	}
	return "", nil
}

// poll re-checks the broadcast while connected: a new broadcast or two
// closed checks end the capture, a new chat room forces a reconnect.
func (w *liveWorker) poll(ctx context.Context, stop context.CancelFunc, room string, closed *int) pollResult {
	t := time.NewTicker(w.timing.metaEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return pollResult{}
		case <-t.C:
		}
		cur, err := w.src.LiveMeta(ctx, w.target)
		if err != nil {
			if ctx.Err() != nil {
				return pollResult{}
			}
			w.emit(liveMsg{kind: evViewerMissing, at: time.Now()})
			continue
		}
		var r pollResult
		switch {
		case cur.LiveID != w.ls.LiveID:
			r.ended = endedNewLive
		case !cur.Open:
			if *closed++; *closed >= 2 {
				r.ended = endedClosed
			}
		case cur.ChatRoom != room:
			*closed = 0
			r.err = errRoomChanged
		default:
			*closed = 0
			w.emit(liveMsg{kind: evMetadata, meta: cur})
		}
		if r.ended != "" || r.err != nil {
			stop()
			return r
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (w *liveWorker) run(ctx context.Context) error {
	st := w.a.cfg.Store
	bg := context.WithoutCancel(ctx) // writes after a stop must still land
	var err error
	if w.salt, err = st.SessionSalt(bg, w.sid); err != nil {
		return err
	}
	if w.ls, err = st.LiveState(bg, w.sid); err != nil {
		return err
	}
	json.Unmarshal(w.ls.ParamsJSON, &w.params)
	w.dir = w.a.exportDir(w.sid)
	w.inbox = make(chan liveMsg, w.timing.queue)
	w.state = store.LiveStarting

	// TXT may lag the DB after a crash; rebuild it once from the DB.
	if received, err := st.Chats(bg, w.sid, store.AllSeq, true); err == nil {
		if export.WriteLiveReceived(w.dir, w.ls.TimeBasis, w.ls.LiveID, received) != nil {
			w.errMsg = "TXT 복구에 실패했습니다. DB는 보존되며 저장 공간을 확인하면 다시 출력할 수 있습니다."
		}
	}
	start := max(0, time.Now().UnixMilli()-w.ls.OriginMs)
	if w.analyzer, err = detect.NewLiveAnalyzer(start, w.params, w.a.cfg.Rules, w.ls.ViewerAdaptive); err != nil {
		return err
	}
	prev, _, err := st.LiveCandidates(bg, w.sid, 0)
	if err != nil {
		return err
	}
	for _, c := range prev {
		if n, err := strconv.Atoi(c.ID[1:]); err == nil {
			w.analyzer.ContinueNumbering(n)
		}
	}
	if w.ls.LastHealthyAtMs > 0 {
		w.gapStart = max(0, w.ls.LastHealthyAtMs-w.ls.OriginMs)
	}
	w.segmentStart = start
	w.seen = map[string]int{}
	w.save(bg)

	var failure error
	var ended string
	finished := make(chan struct{})
	rctx, stopReceive := context.WithCancel(ctx)
	defer stopReceive()
	w.stopReceive, w.lostFrom = stopReceive, -1
	go func() {
		defer close(finished)
		ended, failure = w.receive(rctx, w.ls.Generation)
	}()

	terminal := store.LiveStopped
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	last := time.Now()
	done := false
	for !done || len(w.inbox) > 0 {
		select {
		case m := <-w.inbox:
			w.handle(bg, m)
		case <-finished:
			done = true
			finished = nil
		case <-tick.C:
		}
		if ctx.Err() != nil && w.state != store.LiveStopping {
			w.state = store.LiveStopping
		}
		if len(w.pending) >= w.timing.publishBatch || time.Since(last) >= w.timing.publishEvery {
			w.publish(bg, false)
			last = time.Now()
		}
	}

	w.publish(bg, false) // store what is left first, so a storage failure is known below
	if ended != "" {
		terminal, w.ls.ClosedReason = store.LiveEnded, ended
	}
	if w.fatal != nil {
		ended, failure = "", w.fatal
	}
	if failure != nil {
		terminal = store.LiveError
		w.errCode, w.errMsg = "LIVE_NETWORK", failure.Error()
		if ue, ok := domain.AsUserError(failure); ok {
			w.errCode, w.errMsg = ue.Code, ue.Message
		}
		now := time.Now().UnixMilli()
		from := now
		if w.healthy > 0 {
			from = w.healthy
		}
		start := max(0, from-w.ls.OriginMs)
		if w.lostFrom >= 0 {
			start = min(start, w.lostFrom)
		}
		w.addGap(bg, start, max(0, now-w.ls.OriginMs), w.errCode)
	} else {
		w.errCode = ""
		if terminal == store.LiveStopped {
			w.ls.ClosedReason = "수집을 중단했습니다."
			if w.a.ctx.Err() != nil {
				w.ls.ClosedReason = "앱을 종료해 수집을 마쳤습니다."
			}
		}
	}
	w.publish(bg, true)
	if _, err := w.a.checkpoint(bg, w.sid, terminal); err != nil {
		w.errMsg = "최종 TXT 내보내기에 실패했습니다. 현재까지 저장을 다시 눌러 주세요."
	}
	w.state = terminal
	w.save(bg)
	w.notify(nil)
	return nil
}

func (w *liveWorker) handle(ctx context.Context, m liveMsg) {
	switch m.kind {
	case evChat:
		if m.chat.ReceivedAt.IsZero() {
			m.chat.ReceivedAt = time.Now()
		}
		if m.gen == w.ls.Generation {
			w.pending = append(w.pending, m.chat)
			w.pendingGen = append(w.pendingGen, m.gen)
			w.healthy = m.chat.ReceivedAt.UnixMilli()
			w.ls.LastHealthyAtMs = w.healthy
		}
	case evConnected:
		w.publish(ctx, false)
		w.connected = true
		w.healthy = m.at.UnixMilli()
		w.ls.LastHealthyAtMs = w.healthy
		w.segmentStart = max(0, w.healthy-w.ls.OriginMs)
		if w.segmentStart > w.gapStart {
			reason := "disconnected"
			if w.ls.Generation == 0 {
				reason = "unobserved"
			}
			w.addGap(ctx, w.gapStart, w.segmentStart, reason)
		}
		w.ls.Generation = m.gen
		w.state, w.errCode, w.errMsg = store.LiveCollecting, "", ""
		w.seen = map[string]int{}
		w.save(ctx)
		w.notify(nil)
	case evHeartbeat:
		if m.gen == w.ls.Generation {
			w.healthy = m.at.UnixMilli()
			w.ls.LastHealthyAtMs = w.healthy
		}
	case evDisconnected:
		w.publish(ctx, false)
		w.connected = false
		from := m.at.UnixMilli()
		if w.healthy > 0 {
			from = w.healthy
		}
		w.gapStart = max(0, from-w.ls.OriginMs)
		w.state, w.errCode, w.errMsg = store.LiveReconnecting, m.code, m.message
		w.save(ctx)
		w.notify(nil)
	case evMetadata:
		if m.meta.LiveID == w.ls.LiveID || m.meta.LiveID == "" {
			w.sample(ctx, m.meta.ObservedAt, m.meta.Viewers)
		}
	case evViewerMissing:
		w.sample(ctx, m.at, -1)
	}
}

func (w *liveWorker) addGap(ctx context.Context, start, end int64, reason string) {
	if end <= start {
		return
	}
	w.a.cfg.Store.AddGap(ctx, w.sid, domain.Gap{StartMs: start, EndMs: end}, reason)
	w.analyzer.Gap(start, end)
}

func (w *liveWorker) sample(ctx context.Context, at time.Time, viewers int) {
	w.ls.ViewerCount = viewers
	if !w.ls.ViewerAdaptive {
		return
	}
	if at.IsZero() {
		at = time.Now()
	}
	t := max(0, at.UnixMilli()-w.ls.OriginMs)
	s := detect.ViewerSample{TimeMs: t, BroadcastMs: t, LiveID: w.ls.LiveID, Segment: w.ls.Generation, Quality: "missing"}
	if viewers > 0 {
		s.Viewers, s.Quality = viewers, "ok"
	}
	body, _ := json.Marshal(s)
	w.a.cfg.Store.AddViewerSample(ctx, w.sid, body)
	w.analyzer.Sample(s)
}

// normalize turns a received message into a stored row (CzzEdit live_store.normalize).
func (w *liveWorker) normalize(c platform.LiveChat, gen int) store.LiveEvent {
	origin := w.ls.OriginMs
	received := c.ReceivedAt.UnixMilli()
	var event int64
	if !c.EventTime.IsZero() {
		event = c.EventTime.UnixMilli()
	}
	valid := event != 0 && origin <= event && event <= received+2000
	base, quality := received, "invalid"
	if valid {
		base, quality = event, "provider"
	}
	elapsed := max(0, base-origin)
	history := valid && elapsed < w.segmentStart
	e := store.LiveEvent{
		ChatEvent: domain.ChatEvent{ID: randomID(), OffsetMs: elapsed,
			AuthorKey: privacy.AuthorKey(w.salt, c.AuthorID), Text: c.Text},
		ReceivedAtMs: received, EventTimeMs: event, Generation: gen, TimeQuality: quality, History: history,
		Late: elapsed < w.analyzer.Watermark() || history || !valid,
	}
	if c.MessageID != "" {
		e.ReplayKey = privacy.ReplayKey(w.salt, event, c.AuthorID, c.MessageID, c.Text)
	}
	return e
}

func randomID() string {
	b := make([]byte, 16)
	crand.Read(b)
	return hex.EncodeToString(b)
}

// publish stores pending chat, advances analysis, saves candidates and
// state, appends the received TXT, and notifies the UI.
func (w *liveWorker) publish(ctx context.Context, final bool) {
	st := w.a.cfg.Store
	var saved []store.LiveEvent
	if len(w.pending) > 0 {
		rows := make([]store.LiveEvent, len(w.pending))
		for i, c := range w.pending {
			rows[i] = w.normalize(c, w.pendingGen[i])
		}
		w.pending, w.pendingGen = w.pending[:0], w.pendingGen[:0]
		var dups int
		var err error
		saved, dups, err = st.AppendLive(ctx, w.sid, rows, w.seen)
		if err != nil {
			// Never drop chat silently: stop, and mark what was lost as a gap.
			w.a.logError("store live chat", err)
			for _, r := range rows {
				if w.lostFrom < 0 || r.OffsetMs < w.lostFrom {
					w.lostFrom = r.OffsetMs
				}
			}
			if w.fatal == nil {
				w.fatal = domain.WrapUserError("LIVE_STORAGE", "채팅을 저장하지 못해 수집을 멈췄습니다. 저장 공간을 확인한 뒤 이어 기록하세요.", err)
				w.stopReceive()
			}
		}
		w.ls.DuplicateCount += dups
		var analyzable []domain.ChatEvent
		for _, e := range saved {
			if e.Late {
				w.ls.LateCount++
			}
			if e.TimeQuality == "invalid" {
				w.ls.InvalidTimeCount++
			}
			if !e.Late && e.TimeQuality != "invalid" {
				analyzable = append(analyzable, e.ChatEvent)
			}
		}
		w.analyzer.Add(analyzable)
		evs := make([]domain.ChatEvent, len(saved))
		for i, e := range saved {
			evs[i] = e.ChatEvent
		}
		if export.AppendReceived(w.dir, evs) != nil {
			w.errMsg = "TXT 갱신에 실패했습니다. DB는 보존되며 현재까지 저장으로 다시 출력할 수 있습니다."
		}
	}
	target := w.analyzer.Watermark()
	if w.connected && w.healthy > 0 {
		target = max(target, w.healthy-w.ls.OriginMs-w.timing.delayMs)
	}
	if final {
		h := w.healthy
		if h == 0 {
			h = w.ls.OriginMs
		}
		target = max(target, h-w.ls.OriginMs)
	}
	released, err := w.analyzer.Advance(target, final)
	if err != nil {
		w.a.logError("live analysis", err)
	}
	previews, _ := w.analyzer.Previews()
	if rev, err := st.UpsertLiveCandidates(ctx, w.sid, append(released, previews...)); err == nil {
		w.ls.Revision = rev
	}
	w.ls.WatermarkMs = w.analyzer.Watermark()
	if lw := w.analyzer.LastWindow(); lw != nil {
		w.ls.LastWindowJSON, _ = json.Marshal(lw)
		w.ls.ViewerStatus = "missing"
		if lw.Viewer != nil {
			w.ls.ViewerStatus = lw.Viewer.Status
		}
	}
	w.save(ctx)
	w.notify(saved)
}

func (w *liveWorker) save(ctx context.Context) {
	if err := w.a.cfg.Store.SaveLiveState(ctx, w.sid, w.state, w.errCode, w.errMsg, w.ls); err != nil {
		w.a.logError("save live state", err)
	}
}

// LiveUpdate is published on the event bus as "live" after each save.
type LiveUpdate struct {
	SessionID   string     `json:"sessionId"`
	State       string     `json:"state"`
	WatermarkMs int64      `json:"watermarkMs"`
	Generation  int        `json:"generation"`
	Revision    int64      `json:"revision"`
	Message     string     `json:"message,omitempty"`
	Chats       []LiveChat `json:"chats"`
}

// LiveChat is a LIVE message as screens show it.
type LiveChat struct {
	ID       string `json:"id"`
	Seq      int64  `json:"seq"`
	OffsetMs int64  `json:"offsetMs"`
	Author   string `json:"author"`
	Text     string `json:"text"`
	Late     bool   `json:"late"`
}

func (w *liveWorker) notify(saved []store.LiveEvent) {
	if w.a.cfg.Bus == nil {
		return
	}
	chats := make([]LiveChat, 0, min(len(saved), 200))
	for _, e := range saved[max(0, len(saved)-200):] {
		chats = append(chats, LiveChat{ID: e.ID, Seq: e.Seq, OffsetMs: e.OffsetMs, Author: e.AuthorKey, Text: e.Text, Late: e.Late})
	}
	w.a.cfg.Bus.Publish(events.Event{Type: "live", Data: LiveUpdate{SessionID: w.sid, State: w.state,
		WatermarkMs: w.ls.WatermarkMs, Generation: w.ls.Generation, Revision: w.ls.Revision, Message: w.errMsg, Chats: chats}})
}
