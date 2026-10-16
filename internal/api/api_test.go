package api

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
)

const (
	testPort = 18765
	testKey  = "test-key"
	testHost = "127.0.0.1:18765"
)

type fakeCore struct{ err error }

func (f fakeCore) Status(context.Context) (app.Status, error) {
	return app.Status{Name: "MomentForge", Version: "test", SchemaVersion: 1}, f.err
}

func (f fakeCore) Demo(context.Context) (*app.Analysis, error) {
	if f.err != nil {
		return nil, f.err
	}
	c := app.CandidateView{
		Candidate:     domain.Candidate{ID: "m0001", Signals: []string{"laugh"}, Reasons: []string{"웃음 증가"}, EvidenceIDs: []string{"e1"}},
		Evidence:      []app.EvidenceLine{{ChatEvent: domain.ChatEvent{ID: "e1", OffsetMs: 300000, AuthorKey: "user-1", Text: "ㅋㅋ"}, Count: 1}},
		EvidenceTotal: 1,
	}
	return &app.Analysis{Title: "샘플", DurationMs: 600000, Windows: []domain.Window{{T: 10000, N: 3, U: 2, Valid: true, Signals: []string{}}},
		Candidates: []app.CandidateView{c}}, nil
}

func newTestHandler(core Core, bus *events.Bus, assets fstest.MapFS) http.Handler {
	cfg := Config{Core: core, Bus: bus, Port: testPort, AccessKey: testKey, Heartbeat: time.Hour}
	if assets != nil {
		cfg.Assets = assets
	}
	return NewHandler(cfg)
}

func request(h http.Handler, method, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Host = testHost
	for k, v := range headers {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code
}

func TestStatusRequiresGuards(t *testing.T) {
	h := newTestHandler(fakeCore{}, events.NewBus(4), nil)
	cases := []struct {
		name     string
		headers  map[string]string
		wantCode int
		wantErr  string
	}{
		{"ok", map[string]string{KeyHeader: testKey}, 200, ""},
		{"localhost name", map[string]string{KeyHeader: testKey, "Host": "localhost:18765"}, 200, ""},
		{"same origin", map[string]string{KeyHeader: testKey, "Origin": "http://127.0.0.1:18765", "Sec-Fetch-Site": "same-origin"}, 200, ""},
		{"no key", nil, 401, "BAD_KEY"},
		{"wrong key", map[string]string{KeyHeader: "nope"}, 401, "BAD_KEY"},
		{"rebinding host", map[string]string{KeyHeader: testKey, "Host": "evil.example:18765"}, 403, "BAD_HOST"},
		{"other port origin", map[string]string{KeyHeader: testKey, "Origin": "http://127.0.0.1:9999"}, 403, "BAD_ORIGIN"},
		{"cross site fetch", map[string]string{KeyHeader: testKey, "Sec-Fetch-Site": "cross-site"}, 403, "BAD_ORIGIN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := request(h, "GET", "/api/status", c.headers)
			if rec.Code != c.wantCode {
				t.Fatalf("status = %d, want %d: %s", rec.Code, c.wantCode, rec.Body)
			}
			if c.wantErr != "" && errorCode(t, rec) != c.wantErr {
				t.Fatalf("error code = %q, want %q", errorCode(t, rec), c.wantErr)
			}
			if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("security headers missing")
			}
		})
	}
}

func TestStatusBody(t *testing.T) {
	h := newTestHandler(fakeCore{}, events.NewBus(4), nil)
	rec := request(h, "GET", "/api/status", map[string]string{KeyHeader: testKey})
	var got statusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "MomentForge" || got.SchemaVersion != 1 {
		t.Fatalf("got %+v", got)
	}
}

func TestUserErrorIsShownInternalIsHidden(t *testing.T) {
	ue := request(newTestHandler(fakeCore{err: domain.NewUserError("X", "보여줄 문구")}, nil, nil),
		"GET", "/api/status", map[string]string{KeyHeader: testKey})
	if ue.Code != 400 || !strings.Contains(ue.Body.String(), "보여줄 문구") {
		t.Fatalf("user error: %d %s", ue.Code, ue.Body)
	}
	internal := request(newTestHandler(fakeCore{err: context.DeadlineExceeded}, nil, nil),
		"GET", "/api/status", map[string]string{KeyHeader: testKey})
	if internal.Code != 500 || strings.Contains(internal.Body.String(), "deadline") {
		t.Fatalf("internal error leaked: %d %s", internal.Code, internal.Body)
	}
}

func TestUnknownAPIPathIsJSON404(t *testing.T) {
	rec := request(newTestHandler(fakeCore{}, nil, nil), "GET", "/api/nope", map[string]string{KeyHeader: testKey})
	if rec.Code != 404 || errorCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestIndexGetsKeyAndAssetsAreCached(t *testing.T) {
	assets := fstest.MapFS{
		"index.html":      {Data: []byte(`<meta name="mf-access-key" content="__MF_ACCESS_KEY__">`)},
		"assets/app-1.js": {Data: []byte("console.log(1)")},
		"favicon.svg":     {Data: []byte("<svg/>")},
	}
	h := newTestHandler(fakeCore{}, nil, assets)
	for _, p := range []string{"/", "/index.html", "/vod/123"} {
		rec := request(h, "GET", p, nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), `content="test-key"`) {
			t.Fatalf("%s: %d %s", p, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: index must not be cached", p)
		}
	}
	js := request(h, "GET", "/assets/app-1.js", nil)
	if js.Code != 200 || !strings.Contains(js.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset: %d %q", js.Code, js.Header().Get("Cache-Control"))
	}
	if rec := request(h, "GET", "/missing.js", nil); rec.Code != 404 {
		t.Fatalf("missing file: %d", rec.Code)
	}
	if rec := request(h, "GET", "/", map[string]string{"Host": "evil.example:18765"}); rec.Code != 403 {
		t.Fatalf("index with bad host: %d", rec.Code)
	}
}

func TestMissingUIExplainsHowToBuild(t *testing.T) {
	rec := request(newTestHandler(fakeCore{}, nil, fstest.MapFS{}), "GET", "/", nil)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "make build") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestEventsStream(t *testing.T) {
	bus := events.NewBus(8)
	srv := httptest.NewUnstartedServer(newTestHandler(fakeCore{}, bus, nil))
	srv.Start()
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/events", nil)
	req.Host = testHost
	req.Header.Set(KeyHeader, testKey)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type %q", ct)
	}
	lines := bufio.NewScanner(res.Body)
	next := func() string {
		for lines.Scan() {
			if l := lines.Text(); strings.HasPrefix(l, "event: ") {
				return strings.TrimPrefix(l, "event: ")
			}
		}
		return ""
	}
	if got := next(); got != "ready" {
		t.Fatalf("first event = %q, want ready", got)
	}
	bus.Publish(events.Event{Type: "job.progress", Data: map[string]int{"done": 1}})
	if got := next(); got != "job.progress" {
		t.Fatalf("event = %q, want job.progress", got)
	}
	bus.Close()
	if got := next(); got != "" {
		t.Fatalf("stream should end after bus close, got %q", got)
	}
}

func TestDemoResponseShape(t *testing.T) {
	rec := request(newTestHandler(fakeCore{}, nil, nil), "GET", "/api/demo", map[string]string{KeyHeader: testKey})
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	c := body["candidates"].([]any)[0].(map[string]any)
	ev := c["evidence"].([]any)[0].(map[string]any)
	if c["id"] != "m0001" || c["reasons"].([]any)[0] != "웃음 증가" || ev["author"] != "user-1" || ev["offsetMs"].(float64) != 300000 {
		t.Fatalf("unexpected body: %s", rec.Body)
	}
	if _, leaked := body["windows"].([]any)[0].(map[string]any)["Evidence"]; leaked {
		t.Fatal("window evidence ids must not be sent")
	}
}
