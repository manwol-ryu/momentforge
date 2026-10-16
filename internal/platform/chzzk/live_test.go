package chzzk

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

var liveTarget = platform.Target{Platform: "chzzk", Kind: platform.KindLive, ID: strings.Repeat("a", 32)}

// recorder is a LiveSink that keeps what it received.
type recorder struct {
	mu        sync.Mutex
	connected int
	chats     []platform.LiveChat
	beats     int
	fail      error
}

func (r *recorder) Connected(time.Time) { r.mu.Lock(); r.connected++; r.mu.Unlock() }
func (r *recorder) Heartbeat(time.Time) { r.mu.Lock(); r.beats++; r.mu.Unlock() }
func (r *recorder) Chat(c platform.LiveChat) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.chats = append(r.chats, c)
	return r.fail
}

// chatServer plays the CHZZK chat server; script runs after the connect frame.
type chatServer struct {
	routingHosts []any
	connectRet   int
	script       func(ctx context.Context, c *websocket.Conn)
	gotPong      chan struct{}
}

func (cs *chatServer) start(t *testing.T) *Chzzk {
	t.Helper()
	cs.gotPong = make(chan struct{}, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/nng_main/v1/chats/access-token", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "content": map[string]any{"accessToken": "tok-" + r.URL.Query().Get("channelId")}})
	})
	mux.HandleFunc("/routing/getRouting", func(w http.ResponseWriter, r *http.Request) {
		hosts := cs.routingHosts
		if hosts == nil {
			hosts = []any{"kr-ss1.chat.naver.com"}
		}
		json.NewEncoder(w).Encode(map[string]any{"result": map[string]any{"sessionServerList": hosts}})
	})
	mux.HandleFunc("/chat", func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()
		ctx := r.Context()
		_, b, err := c.Read(ctx)
		var hello map[string]any
		if err != nil || json.Unmarshal(b, &hello) != nil || hello["cmd"].(float64) != cmdConnect ||
			hello["bdy"].(map[string]any)["auth"] != "READ" || hello["bdy"].(map[string]any)["accTkn"] != "tok-room1" {
			return
		}
		write(ctx, c, map[string]any{"cmd": cmdConnected, "retCode": cs.connectRet})
		go func() { // collect client frames (pongs)
			for {
				_, b, err := c.Read(ctx)
				if err != nil {
					return
				}
				var f map[string]any
				json.Unmarshal(b, &f)
				if f["cmd"] == float64(cmdPong) {
					select {
					case cs.gotPong <- struct{}{}:
					default:
					}
				}
			}
		}()
		if cs.script != nil {
			cs.script(ctx, c)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return New(WithAPIBase(srv.URL, u.Hostname()), WithInterval(0),
		WithLiveEndpoints(srv.URL, srv.URL, func(string) string { return "ws://" + u.Host + "/chat" }, 50*time.Millisecond))
}

func write(ctx context.Context, c *websocket.Conn, v any) {
	b, _ := json.Marshal(v)
	c.Write(ctx, websocket.MessageText, b)
}

func code(err error) string {
	if ue, ok := domain.AsUserError(err); ok {
		return ue.Code
	}
	return ""
}

func TestLiveStreamDeliversChatsAndAnswersPing(t *testing.T) {
	cs := &chatServer{script: func(ctx context.Context, c *websocket.Conn) {
		now := time.Now().UnixMilli()
		write(ctx, c, map[string]any{"cmd": cmdChat, "bdy": []any{
			map[string]any{"uid": "u1", "msg": "ㅋㅋ", "msgTime": now, "msgTid": 7, "profile": "{\"nickname\":\"가짜\"}"},
			map[string]any{"uid": nil, "msg": "익명", "msgTime": now},
		}})
		write(ctx, c, map[string]any{"cmd": cmdPing})
		time.Sleep(100 * time.Millisecond)
		write(ctx, c, map[string]any{"cmd": cmdClosed})
	}}
	c := cs.start(t)
	rec := &recorder{}
	err := c.LiveStream(context.Background(), liveTarget, "room1", rec)
	if code(err) != "LIVE_DISCONNECTED" {
		t.Fatalf("err = %v, want LIVE_DISCONNECTED", err)
	}
	select {
	case <-cs.gotPong:
	default:
		t.Fatal("client did not answer the server ping")
	}
	if rec.connected != 1 || len(rec.chats) != 2 || rec.chats[0].AuthorID != "u1" || rec.chats[0].MessageID != "7" ||
		rec.chats[1].AuthorID != "" || rec.chats[0].EventTime.IsZero() || rec.beats == 0 {
		t.Fatalf("recorded = %+v", rec)
	}
}

func TestLiveStreamErrors(t *testing.T) {
	cases := map[string]struct {
		cs   chatServer
		sink *recorder
		want string
	}{
		"refused":  {chatServer{connectRet: 1}, &recorder{}, "LIVE_ACCESS"},
		"bad host": {chatServer{routingHosts: []any{"evil.example"}}, &recorder{}, "LIVE_SCHEMA"},
		"missing body": {chatServer{script: func(ctx context.Context, c *websocket.Conn) {
			write(ctx, c, map[string]any{"cmd": cmdChat, "bdy": []any{map[string]any{"uid": "u"}}})
		}}, &recorder{}, "LIVE_SCHEMA"},
		"no pong": {chatServer{script: func(ctx context.Context, c *websocket.Conn) { <-ctx.Done() }}, &recorder{}, "LIVE_TIMEOUT"},
		"queue overflow": {chatServer{script: func(ctx context.Context, c *websocket.Conn) {
			write(ctx, c, map[string]any{"cmd": cmdChat, "bdy": []any{map[string]any{"uid": "u", "msg": "x"}}})
			<-ctx.Done()
		}}, &recorder{fail: domain.NewUserError("LIVE_OVERLOAD", "가득 참")}, "LIVE_OVERLOAD"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := tc.cs.start(t)
			err := c.LiveStream(context.Background(), liveTarget, "room1", tc.sink)
			if code(err) != tc.want {
				t.Fatalf("err = %v, want %s", err, tc.want)
			}
		})
	}
}

func TestLiveStreamCancel(t *testing.T) {
	cs := &chatServer{script: func(ctx context.Context, c *websocket.Conn) {
		for ctx.Err() == nil { // keep answering pings so only cancel can end it
			time.Sleep(20 * time.Millisecond)
			write(ctx, c, map[string]any{"cmd": cmdPong})
		}
	}}
	c := cs.start(t)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	if err := c.LiveStream(ctx, liveTarget, "room1", &recorder{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestParseLiveMeta(t *testing.T) {
	m := parseLiveMeta(map[string]any{"liveId": 12345.0, "liveTitle": "방송", "status": "OPEN", "chatChannelId": "room1",
		"openDate": "2026-10-16 20:00:00", "concurrentUserCount": 321.0}, time.Unix(0, 0))
	want := time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC) // 20:00 KST
	if m.LiveID != "12345" || !m.Open || m.ChatRoom != "room1" || m.Viewers != 321 || !m.StartedAt.Equal(want) {
		t.Fatalf("meta = %+v", m)
	}
	closed := parseLiveMeta(map[string]any{"status": "CLOSE"}, time.Unix(0, 0))
	if closed.Open || closed.Viewers != -1 || !closed.StartedAt.IsZero() || closed.Title != "라이브" {
		t.Fatalf("closed meta = %+v", closed)
	}
}

func TestMatchLive(t *testing.T) {
	id := strings.Repeat("AB", 16)
	got, ok, err := New().Match("https://chzzk.naver.com/live/" + id)
	if err != nil || !ok || got.Kind != platform.KindLive || got.ID != strings.ToLower(id) {
		t.Fatalf("Match live = %+v, %v, %v", got, ok, err)
	}
}
