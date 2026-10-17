package chzzk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

var vod = platform.Target{Platform: "chzzk", Kind: platform.KindVOD, ID: "1234"}

// fake serves CHZZK-shaped responses. All data is synthetic.
func fake(t *testing.T, handler http.HandlerFunc) *Chzzk {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return New(WithAPIBase(srv.URL, u.Hostname()), WithInterval(0),
		WithSleep(func(ctx context.Context, d time.Duration) error { return ctx.Err() }))
}

func content(v any) []byte {
	b, _ := json.Marshal(map[string]any{"code": 200, "message": nil, "content": v})
	return b
}

func chat(t int64, author any, text string) map[string]any {
	return map[string]any{"playerMessageTime": t, "userIdHash": author, "content": text, "profile": "{\"nickname\":\"가짜\"}"}
}

func TestMatch(t *testing.T) {
	c := New()
	cases := []struct {
		in     string
		ok     bool
		id     string
		errOut bool
	}{
		{"https://chzzk.naver.com/video/1234", true, "1234", false},
		{"https://chzzk.naver.com/video/1234/", true, "1234", false},
		{"1234", true, "1234", false},
		{"https://chzzk.naver.com/live/abc", false, "", true},
		{"http://chzzk.naver.com/video/1234", false, "", true},
		{"https://user@chzzk.naver.com/video/1234", false, "", true},
		{"https://example.com/video/1234", false, "", false},
	}
	for _, tc := range cases {
		got, ok, err := c.Match(tc.in)
		if ok != tc.ok || (err != nil) != tc.errOut || (ok && got.ID != tc.id) {
			t.Errorf("Match(%q) = %+v, %v, %v", tc.in, got, ok, err)
		}
	}
}

func TestVODMeta(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/service/v3/videos/1234" || r.Header.Get("Referer") == "" {
			http.NotFound(w, r)
			return
		}
		w.Write(content(map[string]any{"videoNo": 1234, "videoTitle": "테스트 방송", "duration": 600.0005, "videoChatEnabled": true}))
	})
	meta, err := c.VODMeta(context.Background(), vod)
	if err != nil || meta.Title != "테스트 방송" || meta.DurationMs != 600000 {
		t.Fatalf("meta = %+v, %v", meta, err)
	}
}

func TestVODMetaErrors(t *testing.T) {
	cases := map[string]struct {
		status int
		body   any
		code   string
	}{
		"chat off":    {200, map[string]any{"videoNo": 1234, "videoTitle": "t", "duration": 10, "videoChatEnabled": false}, "CHAT_UNAVAILABLE"},
		"other video": {200, map[string]any{"videoNo": 99, "videoTitle": "t", "duration": 10}, "SOURCE_CHANGED"},
		"no duration": {200, map[string]any{"videoTitle": "t"}, "SCHEMA_CHANGED"},
		"login":       {401, nil, "LOGIN_REQUIRED"},
		"deleted":     {404, nil, "ACCESS_DENIED"},
		"teapot":      {418, nil, "HTTP_ERROR"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				if tc.body != nil {
					w.Write(content(tc.body))
				}
			})
			_, err := c.VODMeta(context.Background(), vod)
			if ue, ok := domain.AsUserError(err); !ok || ue.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestVODChatsPagesAndValidation(t *testing.T) {
	type pageCase struct {
		name   string
		cursor int64
		body   map[string]any
		code   string // expected error code; "" = ok
	}
	ok := map[string]any{
		"previousVideoChats":    []any{chat(900, "u1", "앞")},
		"videoChats":            []any{chat(1001, "u2", "ㅋㅋ"), chat(1500, nil, "작성자 없음")},
		"nextPlayerMessageTime": 1500,
	}
	cases := []pageCase{
		{"ok", 1000, ok, ""},
		{"previous after cursor", 1000, map[string]any{"previousVideoChats": []any{chat(1001, "u", "x")}, "videoChats": []any{}, "nextPlayerMessageTime": nil}, "BOUNDARY_CHANGED"},
		{"following at cursor", 1000, map[string]any{"previousVideoChats": []any{}, "videoChats": []any{chat(1000, "u", "x")}, "nextPlayerMessageTime": nil}, "BOUNDARY_CHANGED"},
		{"float time", 1000, map[string]any{"previousVideoChats": []any{}, "videoChats": []any{chat(0, "u", "x")}, "nextPlayerMessageTime": nil}, ""},
		{"empty with next", 1000, map[string]any{"previousVideoChats": []any{}, "videoChats": []any{}, "nextPlayerMessageTime": 2000}, "PAGE_STALLED"},
		{"next mismatch", 1000, map[string]any{"previousVideoChats": []any{}, "videoChats": []any{chat(1100, "u", "x")}, "nextPlayerMessageTime": 1200}, "PAGE_STALLED"},
		{"missing field", 1000, map[string]any{"videoChats": []any{}}, "SCHEMA_CHANGED"},
		{"time past end", 1000, map[string]any{"previousVideoChats": []any{}, "videoChats": []any{chat(9_999_999, "u", "x")}, "nextPlayerMessageTime": nil}, "SCHEMA_CHANGED"},
	}
	// "float time": following at 0 <= cursor 1000 is a boundary violation in
	// CzzEdit too; fix the case to test a real float value instead.
	cases[3] = pageCase{"float time", 1000, map[string]any{"previousVideoChats": []any{}, "videoChats": []any{map[string]any{"playerMessageTime": 1001.5, "userIdHash": "u", "content": "x"}}, "nextPlayerMessageTime": nil}, "SCHEMA_CHANGED"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := fake(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("playerMessageTime") != strconv.FormatInt(tc.cursor, 10) ||
					r.URL.Query().Get("previousVideoChatSize") != "50" {
					http.NotFound(w, r)
					return
				}
				w.Write(content(tc.body))
			})
			page, err := c.VODChats(context.Background(), vod, tc.cursor, 600000)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				if tc.name == "ok" && (len(page.Messages) != 3 || page.Next != 1500 || page.Finished ||
					page.Messages[2].AuthorID != "" || len(page.Raw) == 0) {
					t.Fatalf("page = %+v", page)
				}
				return
			}
			var pe *platform.PageError
			ue, isUser := domain.AsUserError(err)
			if !errors.As(err, &pe) || len(pe.Raw) == 0 || !isUser || ue.Code != tc.code {
				t.Fatalf("err = %v, want PageError with %s", err, tc.code)
			}
		})
	}
}

func TestAmbiguousBoundaryBecomesGap(t *testing.T) {
	rows := make([]any, 200)
	for i := range rows {
		rows[i] = chat(5000, fmt.Sprintf("u%d", i), "같은 시각")
	}
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(content(map[string]any{"previousVideoChats": []any{}, "videoChats": rows, "nextPlayerMessageTime": 5000}))
	})
	page, err := c.VODChats(context.Background(), vod, 4000, 600000)
	if err != nil || len(page.Gaps) != 1 || page.Gaps[0] != (domain.Gap{StartMs: 5000, EndMs: 5001}) {
		t.Fatalf("page gaps = %+v, err %v", page.Gaps, err)
	}
}

func TestRetriesHonourRetryAfterThenGiveUp(t *testing.T) {
	var calls atomic.Int32
	var waits []time.Duration
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(503)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := New(WithAPIBase(srv.URL, u.Hostname()), WithInterval(0),
		WithSleep(func(ctx context.Context, d time.Duration) error { waits = append(waits, d); return nil }))
	_, err := c.VODMeta(context.Background(), vod)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "RETRY_EXHAUSTED" {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 5 {
		t.Fatalf("calls = %d, want 5", calls.Load())
	}
	for _, w := range waits {
		if w != 7*time.Second {
			t.Fatalf("waits = %v, want Retry-After 7s", waits)
		}
	}
}

func TestRedirectToOtherHostIsRefused(t *testing.T) {
	c := fake(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://evil.example/service/v3/videos/1234", http.StatusFound)
	})
	_, err := c.VODMeta(context.Background(), vod)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "REDIRECT" {
		t.Fatalf("err = %v, want REDIRECT", err)
	}
}

func TestCancelStopsRetries(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := fake(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	if _, err := c.VODMeta(ctx, vod); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestPlayback(t *testing.T) {
	pb := func(path, auth string) string {
		b, _ := json.Marshal(map[string]any{"meta": map[string]any{"playbackAuthType": auth},
			"media": []any{map[string]any{"protocol": "HLS", "path": path}}})
		return string(b)
	}
	cases := map[string]struct {
		data map[string]any
		url  string
		code string
	}{
		"public":    {map[string]any{"liveRewindPlaybackJson": pb("https://a.b.navercdn.com/x.m3u8", "NONE")}, "https://a.b.navercdn.com/x.m3u8", ""},
		"adult":     {map[string]any{"adult": true}, "", "PLAYBACK_RESTRICTED"},
		"paid":      {map[string]any{"paidProductId": "p1"}, "", "PLAYBACK_RESTRICTED"},
		"other cdn": {map[string]any{"liveRewindPlaybackJson": pb("https://evil.example/x.m3u8", "NONE")}, "", "PLAYBACK_UNAVAILABLE"},
		"live cdn":  {map[string]any{"liveRewindPlaybackJson": pb("https://livecloud.pstatic.net/x.m3u8", "NONE")}, "", "PLAYBACK_UNAVAILABLE"},
		"auth":      {map[string]any{"liveRewindPlaybackJson": pb("https://a.navercdn.com/x.m3u8", "TOKEN")}, "", "PLAYBACK_UNAVAILABLE"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := fake(t, func(w http.ResponseWriter, r *http.Request) { w.Write(content(tc.data)) })
			got, err := c.Playback(context.Background(), vod)
			if tc.code == "" {
				if err != nil || got.URL != tc.url || got.Type != "hls" {
					t.Fatalf("got %+v, %v", got, err)
				}
				return
			}
			if ue, ok := domain.AsUserError(err); !ok || ue.Code != tc.code {
				t.Fatalf("err = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestWatchURL(t *testing.T) {
	if got := New().WatchURL(vod, 125_900); got != "https://chzzk.naver.com/video/1234?currentTime=125" {
		t.Fatal(got)
	}
}
