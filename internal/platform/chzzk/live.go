package chzzk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/coder/websocket"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Anonymous public-web READ protocol (no account cookies, no official OAuth).
const (
	commBase    = "https://comm-api.game.naver.com"
	routingBase = "https://routing.chat.naver.com"
	liveMaxBody = 4 << 20

	cmdPing      = 0
	cmdPong      = 10000
	cmdConnect   = 100
	cmdConnected = 10100
	cmdChat      = 93101
	cmdClosed    = 90102
)

var (
	livePath    = regexp.MustCompile(`^/live/([a-fA-F0-9]{32})/?$`)
	chatHostPat = regexp.MustCompile(`^[a-zA-Z0-9-]+\.chat\.naver\.com$`)
	kst         = time.FixedZone("KST", 9*3600)
)

// liveEndpoints can be replaced in tests.
type liveEndpoints struct {
	comm, routing string
	ws            func(host string) string // chat server host → websocket URL
	client        *http.Client
	pingEvery     time.Duration
	pongWait      time.Duration
}

func defaultLive() liveEndpoints {
	return liveEndpoints{
		comm: commBase, routing: routingBase,
		ws: func(host string) string { return "wss://" + host + "/chat" },
		client: &http.Client{Timeout: 8 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }},
		pingEvery: 10 * time.Second, pongWait: 10 * time.Second,
	}
}

// WithLiveEndpoints points the LIVE calls at test servers. Tests only.
func WithLiveEndpoints(comm, routing string, ws func(host string) string, pingEvery time.Duration) Option {
	return func(c *Chzzk) {
		c.live.comm, c.live.routing, c.live.ws = comm, routing, ws
		if pingEvery > 0 {
			c.live.pingEvery, c.live.pongWait = pingEvery, pingEvery
		}
	}
}

func liveErr(code, msg string) error { return domain.NewUserError(code, msg) }

// liveJSON fetches a fixed endpoint once; the app's reconnect loop retries.
func (c *Chzzk) liveJSON(ctx context.Context, u string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	req.Header.Set("Referer", webOrigin+"/")
	res, err := c.live.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, liveErr("LIVE_NETWORK", "라이브 서버에 연결하지 못했습니다.")
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 401 || res.StatusCode == 403:
		return nil, liveErr("LIVE_ACCESS", "공개 라이브에 접근할 수 없습니다. 로그인·시청 제한 또는 연결 상태를 확인하세요.")
	case res.StatusCode == 429:
		e := &domain.UserError{Code: "LIVE_RATE", Message: "공개 라이브 요청이 많아 잠시 기다립니다."}
		return nil, &RetryAfterError{UserError: e, After: retryAfter(res.Header.Get("Retry-After"))}
	case res.StatusCode != 200:
		return nil, liveErr("LIVE_NETWORK", "공개 라이브에 접근할 수 없습니다. 로그인·시청 제한 또는 연결 상태를 확인하세요.")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, liveMaxBody+1))
	if err != nil {
		return nil, liveErr("LIVE_NETWORK", "라이브 서버에 연결하지 못했습니다.")
	}
	if len(body) > liveMaxBody {
		return nil, liveErr("LIVE_SCHEMA", "라이브 응답 크기가 예상 범위를 넘었습니다.")
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return nil, liveErr("LIVE_NETWORK", "라이브 서버에 연결하지 못했습니다.")
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, liveErr("LIVE_SCHEMA", "라이브 응답 형식이 변경되었습니다.")
	}
	if code := fmt.Sprint(m["code"]); code == "401" || code == "403" {
		return nil, liveErr("LIVE_ACCESS", "무로그인으로 볼 수 없는 방송입니다. 접근 제한을 확인하세요.")
	}
	return m, nil
}

// RetryAfterError is a rate limit with the server's requested wait.
type RetryAfterError struct {
	*domain.UserError
	After time.Duration
}

func (e *RetryAfterError) Unwrap() error { return e.UserError }

func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil {
		return time.Duration(min(600, max(1, s))) * time.Second
	}
	return 60 * time.Second
}

// LiveMeta reads the channel's current broadcast.
func (c *Chzzk) LiveMeta(ctx context.Context, t platform.Target) (platform.LiveMeta, error) {
	m, err := c.liveJSON(ctx, c.base+"/service/v2/channels/"+t.ID+"/live-detail")
	if err != nil {
		return platform.LiveMeta{}, err
	}
	content, ok := m["content"].(map[string]any)
	if fmt.Sprint(m["code"]) != "200" || !ok {
		return platform.LiveMeta{}, liveErr("LIVE_MISSING", "방송 정보를 찾지 못했습니다. 방송이 종료되었는지 확인하세요.")
	}
	return parseLiveMeta(content, time.Now()), nil
}

func parseLiveMeta(c map[string]any, now time.Time) platform.LiveMeta {
	meta := platform.LiveMeta{Title: "라이브", Viewers: -1, ObservedAt: now}
	if id, ok := c["liveId"].(float64); ok {
		meta.LiveID = strconv.FormatFloat(id, 'f', -1, 64)
	} else if s, ok := c["liveId"].(string); ok {
		meta.LiveID = s
	}
	if s, _ := c["liveTitle"].(string); s != "" {
		meta.Title = s
	}
	meta.Open = c["status"] == "OPEN"
	meta.ChatRoom, _ = c["chatChannelId"].(string)
	if s, ok := c["openDate"].(string); ok {
		for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05"} {
			if at, err := time.ParseInLocation(layout, s, kst); err == nil {
				meta.StartedAt = at
				break
			}
		}
	}
	if v, ok := c["concurrentUserCount"].(float64); ok && v >= 0 && v == float64(int(v)) {
		meta.Viewers = int(v)
	}
	return meta
}

type frame struct {
	Cmd     int             `json:"cmd"`
	RetCode *int            `json:"retCode"`
	Bdy     json.RawMessage `json:"bdy"`
}

type chatRow struct {
	UID     *string `json:"uid"`
	Msg     *string `json:"msg"`
	MsgTime *int64  `json:"msgTime"`
	MsgTid  any     `json:"msgTid"`
}

// LiveStream runs one read-only chat connection.
func (c *Chzzk) LiveStream(ctx context.Context, t platform.Target, room string, sink platform.LiveSink) error {
	if room == "" {
		return liveErr("LIVE_ACCESS", "공개 채팅방이 없습니다.")
	}
	tok, err := c.liveJSON(ctx, platform.QueryURL(c.live.comm, "/nng_main/v1/chats/access-token",
		url.Values{"channelId": {room}, "chatType": {"STREAMING"}}))
	if err != nil {
		return err
	}
	content, _ := tok["content"].(map[string]any)
	token, _ := content["accessToken"].(string)
	if fmt.Sprint(tok["code"]) != "200" || token == "" {
		return liveErr("LIVE_ACCESS", "익명 채팅 열람 권한을 받지 못했습니다.")
	}
	route, err := c.liveJSON(ctx, platform.QueryURL(c.live.routing, "/routing/getRouting", url.Values{"serviceId": {"game"}}))
	if err != nil {
		return err
	}
	result, _ := route["result"].(map[string]any)
	list, _ := result["sessionServerList"].([]any)
	var hosts []string
	for _, h := range list {
		s, ok := h.(string)
		if !ok || !chatHostPat.MatchString(s) {
			return liveErr("LIVE_SCHEMA", "채팅 서버 안내 형식이 변경되었습니다.")
		}
		hosts = append(hosts, s)
	}
	if len(hosts) == 0 {
		return liveErr("LIVE_SCHEMA", "채팅 서버 안내 형식이 변경되었습니다.")
	}

	dialCtx, cancelDial := context.WithTimeout(ctx, 8*time.Second)
	conn, _, err := websocket.Dial(dialCtx, c.live.ws(hosts[rand.IntN(len(hosts))]), &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {webOrigin}},
	})
	cancelDial()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return liveErr("LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다.")
	}
	defer conn.CloseNow()
	conn.SetReadLimit(liveMaxBody)

	send := func(v any) error {
		b, _ := json.Marshal(v)
		wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return conn.Write(wctx, websocket.MessageText, b)
	}
	if err := send(map[string]any{"ver": "3", "cmd": cmdConnect, "svcid": "game", "cid": room, "sid": nil, "tid": 1,
		"bdy": map[string]any{"uid": nil, "devType": 2001, "accTkn": token, "auth": "READ", "libVer": "4.11.0"}}); err != nil {
		return liveErr("LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다.")
	}
	token = ""

	frames := make(chan []byte, 64)
	readErr := make(chan error, 1)
	go func() {
		for {
			_, b, err := conn.Read(ctx)
			if err != nil {
				readErr <- err
				return
			}
			frames <- b
		}
	}()
	next := func(wait time.Duration) ([]byte, error) {
		select {
		case b := <-frames:
			return b, nil
		case err := <-readErr:
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			var ce websocket.CloseError
			if errors.As(err, &ce) || errors.Is(err, io.EOF) {
				return nil, liveErr("LIVE_DISCONNECTED", "채팅 연결이 끊겼습니다. 다시 연결합니다.")
			}
			if errors.Is(err, websocket.ErrMessageTooBig) {
				return nil, liveErr("LIVE_SCHEMA", "채팅 패킷 크기가 예상 범위를 넘었습니다.")
			}
			return nil, liveErr("LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다.")
		case <-time.After(wait):
			return nil, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	first, err := next(8 * time.Second)
	if err != nil {
		return err
	}
	var f frame
	if first == nil || json.Unmarshal(first, &f) != nil || f.Cmd != cmdConnected || f.RetCode == nil || *f.RetCode != 0 {
		return liveErr("LIVE_ACCESS", "채팅 읽기 연결이 거부되었습니다.")
	}
	sink.Connected(time.Now())

	startWall, startMono := time.Now().Round(0), time.Now()
	lastPing := time.Now()
	var pingPending time.Time
	for {
		now := time.Now()
		if drift := now.Round(0).Sub(startWall) - now.Sub(startMono); drift > 2*time.Second || drift < -2*time.Second {
			return liveErr("LIVE_CLOCK", "컴퓨터 시각이 변경되었습니다. 시간 기준을 확인한 뒤 이어 기록하세요.")
		}
		if !pingPending.IsZero() && now.Sub(pingPending) > c.live.pongWait {
			return liveErr("LIVE_TIMEOUT", "채팅 연결 확인 응답이 없어 재연결합니다.")
		}
		if pingPending.IsZero() && now.Sub(lastPing) >= c.live.pingEvery {
			if err := send(map[string]any{"ver": "3", "cmd": cmdPing}); err != nil {
				return liveErr("LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다.")
			}
			lastPing, pingPending = now, now
		}
		b, err := next(time.Second)
		if err != nil {
			return err
		}
		if b == nil {
			continue
		}
		if err := json.Unmarshal(b, &f); err != nil {
			return liveErr("LIVE_SCHEMA", "채팅 메시지 형식이 변경되었습니다.")
		}
		switch f.Cmd {
		case cmdPing:
			if err := send(map[string]any{"ver": "3", "cmd": cmdPong}); err != nil {
				return liveErr("LIVE_NETWORK", "채팅 연결이 끊겨 다시 연결합니다.")
			}
			sink.Heartbeat(time.Now())
		case cmdPong:
			pingPending = time.Time{}
			sink.Heartbeat(time.Now())
		case cmdClosed:
			return liveErr("LIVE_DISCONNECTED", "서버가 연결을 종료해 다시 연결합니다.")
		case cmdChat:
			var rows []chatRow
			if err := json.Unmarshal(f.Bdy, &rows); err != nil {
				return liveErr("LIVE_SCHEMA", "채팅 메시지 형식이 변경되었습니다.")
			}
			for _, r := range rows {
				if r.Msg == nil {
					return liveErr("LIVE_SCHEMA", "채팅 본문 형식이 변경되었습니다.")
				}
				lc := platform.LiveChat{Text: *r.Msg, ReceivedAt: time.Now()}
				if r.UID != nil {
					lc.AuthorID = *r.UID
				}
				if r.MsgTime != nil {
					lc.EventTime = time.UnixMilli(*r.MsgTime)
				}
				if r.MsgTid != nil {
					lc.MessageID = fmt.Sprint(r.MsgTid)
				}
				if err := sink.Chat(lc); err != nil {
					return err
				}
			}
		}
	}
}
