//go:build e2efake

// Package e2efake is a synthetic LIVE platform for end-to-end tests. It is
// compiled only with the e2efake build tag (make e2e), never into releases.
//
// Its broadcast runs on a synthetic clock: on connect it sends 20 minutes of
// chat at once (a laugh burst at minute 12 makes one candidate), then one
// message every 500 ms of real time, one synthetic second apart. The clock
// survives reconnects; each reconnect skips 30 synthetic seconds, so stop and
// resume leave a gap the way a real disconnect does.
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
}

func New() *Fake { return &Fake{} }

func (*Fake) ID() string             { return "e2efake" }
func (*Fake) Name() string           { return "E2E 가짜 방송" }
func (*Fake) MediaOrigins() []string { return nil }

func (*Fake) Match(raw string) (platform.Target, bool, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != Host {
		return platform.Target{}, false, nil
	}
	ch := strings.Trim(u.Path, "/")
	if ch == "" || strings.Contains(ch, "/") {
		return platform.Target{}, true, domain.NewUserError("INVALID_URL", "https://"+Host+"/채널 형식으로 입력해 주세요.")
	}
	return platform.Target{Platform: "e2efake", Kind: platform.KindLive, ID: ch}, true, nil
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

// WatchURL links to the fake channel (never opened by tests).
func (*Fake) WatchURL(t platform.Target, offsetMs int64) string {
	return "https://" + Host + "/" + t.ID
}
