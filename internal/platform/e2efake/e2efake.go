//go:build e2efake

// Package e2efake is a synthetic LIVE platform for end-to-end tests. It is
// compiled only with the e2efake build tag (make e2e), never into releases.
//
// Its broadcast runs on a synthetic clock: on connect it sends 20 minutes of
// chat at once (a laugh burst at minute 12 makes one candidate), then one
// message every 500 ms of real time, one synthetic second apart. The clock
// survives reconnects; each reconnect skips 30 synthetic seconds, so stop and
// resume leave a gap the way a real disconnect does. Its stream is silent
// audio from a loopback server (media.go).
package e2efake

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Host is the only address it accepts: https://live.e2e.test/{channel}.
const Host = "live.e2e.test"

type Fake struct {
	mu      sync.Mutex
	started time.Time // broadcast start, 30 minutes before first use
	clock   time.Time // synthetic now; zero until the first connection
	seq     int

	mediaOnce sync.Once // starts the stream server (media.go)
	media     string    // its origin, http://127.0.0.1:port
	mediaErr  error
}

func New() *Fake { return &Fake{} }

func (*Fake) ID() string   { return "e2efake" }
func (*Fake) Name() string { return "E2E 가짜 방송" }

func (*Fake) Match(raw string) (platform.Target, bool, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != Host {
		return platform.Target{}, false, nil
	}
	ch := strings.Trim(u.Path, "/")
	if video, ok := strings.CutPrefix(ch, "video/"); ok && video != "" && !strings.Contains(video, "/") {
		return platform.Target{Platform: "e2efake", Kind: platform.KindVOD, ID: video}, true, nil
	}
	if ch == "" || strings.Contains(ch, "/") {
		return platform.Target{}, true, domain.NewUserError("INVALID_URL", "https://"+Host+"/채널 형식으로 입력해 주세요.")
	}
	return platform.Target{Platform: "e2efake", Kind: platform.KindLive, ID: ch}, true, nil
}

// --- synthetic VOD: https://live.e2e.test/video/{id} ---

const vodMs = 20 * 60_000

// VODMeta describes a 20-minute synthetic VOD.
func (*Fake) VODMeta(ctx context.Context, t platform.Target) (platform.VODMeta, error) {
	return platform.VODMeta{Title: "E2E 가짜 다시보기 " + t.ID, DurationMs: vodMs}, nil
}

// VODChats returns 10 minutes per page: one message a second and, at
// 12:00, 25 viewers laughing for 20 seconds.
func (*Fake) VODChats(ctx context.Context, t platform.Target, cursor, durationMs int64) (platform.ChatPage, error) {
	end := min(cursor+10*60_000, vodMs)
	var msgs []platform.RawChat
	for ms := cursor + 1000; ms < end; ms += 1000 {
		msgs = append(msgs, platform.RawChat{OffsetMs: ms, AuthorID: fmt.Sprintf("v%d", ms/1000%17), Text: casual[ms/1000%int64(len(casual))]})
		if ms >= 12*60_000 && ms < 12*60_000+20_000 {
			for i := range 25 {
				msgs = append(msgs, platform.RawChat{OffsetMs: ms + int64(i)*30, AuthorID: fmt.Sprintf("r%d", i), Text: "ㅋㅋㅋㅋㅋ"})
			}
		}
	}
	page := platform.ChatPage{Messages: msgs, Raw: []byte(`{"synthetic":true}`)}
	if end >= vodMs {
		page.Finished = true
	} else {
		page.Next = end
	}
	return page, nil
}

// VODComments returns four synthetic comments: two times (one a range), a
// clock time that needs review, a hidden comment and a reply.
func (*Fake) VODComments(ctx context.Context, t platform.Target, maxPages int, progress func(int, int)) (platform.CommentResult, error) {
	cs := []platform.Comment{
		{ID: "1", AuthorID: "fan-1", Text: "12:00 웃음 터짐 / 15:30~16:00 반복 구간"},
		{ID: "2", AuthorID: "fan-2", Text: "오후 3:00에 봤어요"},
		{ID: "3", AuthorID: "fan-3", Text: "5:00 비밀 댓글", Hidden: true},
		{ID: "4", ParentID: "1", AuthorID: "fan-4", Text: "ㅇㅈ"},
	}
	if progress != nil {
		progress(1, len(cs))
	}
	return platform.CommentResult{Comments: cs, ReportedCount: 3, Pages: 1}, nil
}

func (f *Fake) LiveMeta(ctx context.Context, t platform.Target) (platform.LiveMeta, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.started.IsZero() {
		f.started = time.Now().Add(-30 * time.Minute).Truncate(time.Second)
	}
	open := t.ID != "offline"
	return platform.LiveMeta{LiveID: "E2E-" + t.ID, Title: "E2E 가짜 생방송 " + t.ID, Open: open, ChatRoom: "room-" + t.ID,
		StartedAt: f.started, Viewers: 1234, ObservedAt: time.Now()}, nil
}

func (f *Fake) LiveStream(ctx context.Context, t platform.Target, room string, sink platform.LiveSink) error {
	f.mu.Lock()
	first := f.clock.IsZero()
	if !first {
		f.clock = f.clock.Add(30 * time.Second) // chat missed while disconnected
	}
	f.clock = later(f.clock, time.Now())
	at := f.clock
	f.mu.Unlock()
	sink.Connected(at)

	if first {
		for ms := int64(1000); ms < 20*60_000; ms += 1000 {
			if err := f.send(sink, at, ms, fmt.Sprintf("v%d", ms/1000%17), casual[ms/1000%int64(len(casual))]); err != nil {
				return err
			}
			if ms >= 12*60_000 && ms < 12*60_000+20_000 {
				for i := range 25 {
					if err := f.send(sink, at, ms+int64(i)*30, fmt.Sprintf("r%d", i), "ㅋㅋㅋㅋㅋ"); err != nil {
						return err
					}
				}
			}
		}
		f.advance(at.Add(20 * time.Minute))
		sink.Heartbeat(at.Add(20 * time.Minute))
	}

	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		f.mu.Lock()
		f.clock = f.clock.Add(time.Second)
		now := f.clock
		f.mu.Unlock()
		if err := f.send(sink, now, 0, fmt.Sprintf("v%d", now.Unix()%17), casual[now.Unix()%int64(len(casual))]); err != nil {
			return err
		}
		sink.Heartbeat(now)
	}
}

func (f *Fake) send(sink platform.LiveSink, base time.Time, ms int64, author, text string) error {
	f.mu.Lock()
	f.seq++
	id := fmt.Sprintf("e2e-%d", f.seq)
	f.mu.Unlock()
	at := base.Add(time.Duration(ms) * time.Millisecond)
	return sink.Chat(platform.LiveChat{AuthorID: author, Text: text, MessageID: id, EventTime: at, ReceivedAt: at.Add(100 * time.Millisecond)})
}

func (f *Fake) advance(to time.Time) {
	f.mu.Lock()
	f.clock = later(f.clock, to)
	f.mu.Unlock()
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

var casual = []string{"안녕하세요", "오늘 방송 재밌다", "GG", "이거 뭐임", "굿굿", "화이팅", "음악 좋네", "오 좋다"}

// WatchURL links to the fake channel or video (never opened by tests).
func (*Fake) WatchURL(t platform.Target, offsetMs int64) string {
	if t.Kind == platform.KindVOD {
		return fmt.Sprintf("https://%s/video/%s?t=%d", Host, t.ID, offsetMs/1000)
	}
	return "https://" + Host + "/" + t.ID
}
