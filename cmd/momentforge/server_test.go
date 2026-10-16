package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manwol-ryu/momentforge/internal/api"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/platform/chzzk"
)

var testAssets = fstest.MapFS{
	"index.html": {Data: []byte(`<meta name="mf-access-key" content="__MF_ACCESS_KEY__">`)},
}

func startTest(t *testing.T, dir string) *server {
	t.Helper()
	s, err := start(context.Background(), options{Port: 0, DataDir: dir, Version: "test", Assets: testAssets})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	return s
}

func shutdown(t *testing.T, s *server) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func get(t *testing.T, url, key string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	if key != "" {
		req.Header.Set(api.KeyHeader, key)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func TestServerEndToEnd(t *testing.T) {
	dir := t.TempDir()
	s := startTest(t, dir)

	key, err := os.ReadFile(filepath.Join(dir, "runtime", "access-key"))
	if err != nil {
		t.Fatalf("access key file: %v", err)
	}
	if info, _ := os.Stat(filepath.Join(dir, "runtime", "access-key")); info.Mode().Perm() != 0o600 {
		t.Fatalf("access key permissions = %v, want 0600", info.Mode().Perm())
	}
	if code, body := get(t, s.URL+"/", ""); code != 200 || !strings.Contains(body, string(key)) {
		t.Fatalf("index: %d %s", code, body)
	}
	schema, _ := s.store.SchemaVersion(context.Background())
	if code, body := get(t, s.URL+"/api/status", string(key)); code != 200 || schema < 3 || !strings.Contains(body, fmt.Sprintf(`"schemaVersion":%d`, schema)) {
		t.Fatalf("status: %d %s", code, body)
	}
	if code, _ := get(t, s.URL+"/api/status", "wrong"); code != 401 {
		t.Fatalf("wrong key: %d", code)
	}

	// A second server on the same data folder must refuse to start.
	_, err = start(context.Background(), options{Port: 0, DataDir: dir, Version: "test", Assets: testAssets})
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "ALREADY_RUNNING" {
		t.Fatalf("second start err = %v, want ALREADY_RUNNING", err)
	}

	shutdown(t, s)
	if _, err := os.Stat(filepath.Join(dir, "runtime", "access-key")); !os.IsNotExist(err) {
		t.Fatalf("access key should be removed on shutdown: %v", err)
	}
	shutdown(t, startTest(t, dir)) // lock was released
}

func TestPortInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	_, err = start(context.Background(), options{Port: port, DataDir: t.TempDir(), Version: "test", Assets: testAssets})
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "PORT_IN_USE" {
		t.Fatalf("err = %v, want PORT_IN_USE", err)
	}
}

func TestRunVersionAndUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "momentforge") {
		t.Fatalf("version: %d %q", code, out.String())
	}
	if code := run([]string{"nope"}, &out, &errOut); code != 2 || !strings.Contains(errOut.String(), "알 수 없는 명령") {
		t.Fatalf("unknown: %d %q", code, errOut.String())
	}
}

func post(t *testing.T, url, key, body string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set(api.KeyHeader, key)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(b)
}

func TestDemoSessionUsesRegisteredRules(t *testing.T) {
	dir := t.TempDir()
	s := startTest(t, dir)
	defer shutdown(t, s)
	key, _ := os.ReadFile(filepath.Join(dir, "runtime", "access-key"))
	code, body := post(t, s.URL+"/api/demo", string(key), "{}")
	if code != 201 {
		t.Fatalf("demo: %d %s", code, body)
	}
	var sess struct {
		ID               string `json:"id"`
		LatestAnalysisID string `json:"latestAnalysisId"`
	}
	json.Unmarshal([]byte(body), &sess)
	code, body = get(t, s.URL+"/api/analyses/"+sess.LatestAnalysisID, string(key))
	if code != 200 {
		t.Fatalf("analysis: %d %s", code, body)
	}
	for _, want := range []string{`"id":"m0003"`, "웃음 증가", "집단 반복", "감정 반응", `"rulesVersion":"` + detect.RulesVersion + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("analysis lacks %s", want)
		}
	}
	code, body = get(t, s.URL+"/api/sessions/"+sess.ID+"/files/chat.txt", string(key))
	if code != 200 || !strings.HasPrefix(body, "# MomentForge chat v1") {
		t.Fatalf("download: %d %.80s", code, body)
	}
}

// fakeChzzk serves a 2-minute VOD with two chat pages. All data is synthetic.
func fakeChzzk(t *testing.T) *httptest.Server {
	t.Helper()
	content := func(w http.ResponseWriter, v any) {
		json.NewEncoder(w).Encode(map[string]any{"code": 200, "content": v})
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/service/v3/videos/1234":
			pb, _ := json.Marshal(map[string]any{"meta": map[string]any{"playbackAuthType": "NONE"},
				"media": []any{map[string]any{"protocol": "HLS", "path": "https://v.navercdn.com/1234.m3u8"}}})
			content(w, map[string]any{"videoNo": 1234, "videoTitle": "가짜 방송", "duration": 120,
				"videoChatEnabled": true, "liveRewindPlaybackJson": string(pb)})
		case "/service/v1/videos/1234/chats":
			cursor := r.URL.Query().Get("playerMessageTime")
			row := func(ms int, author, text string) map[string]any {
				return map[string]any{"playerMessageTime": ms, "userIdHash": author, "content": text}
			}
			if cursor == "0" {
				content(w, map[string]any{"previousVideoChats": []any{}, "nextPlayerMessageTime": 60000,
					"videoChats": []any{row(1000, "a", "안녕"), row(60000, "b", "ㅋㅋ")}})
			} else {
				content(w, map[string]any{"previousVideoChats": []any{row(60000, "b", "ㅋㅋ")}, "nextPlayerMessageTime": nil,
					"videoChats": []any{row(90000, "c", "끝")}})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestCollectThroughAPIWithFakeChzzk(t *testing.T) {
	ts := fakeChzzk(t)
	u, _ := url.Parse(ts.URL)
	dir := t.TempDir()
	s, err := start(context.Background(), options{Port: 0, DataDir: dir, Version: "test", Assets: testAssets,
		Platforms: []platform.Platform{chzzk.New(chzzk.WithAPIBase(ts.URL, u.Hostname()), chzzk.WithInterval(0))}})
	if err != nil {
		t.Fatal(err)
	}
	defer shutdown(t, s)
	key, _ := os.ReadFile(filepath.Join(dir, "runtime", "access-key"))

	if code, body := post(t, s.URL+"/api/collect", string(key), `{"url":"1234"}`); code != 202 {
		t.Fatalf("collect: %d %s", code, body)
	}
	var job struct {
		Busy      bool   `json:"busy"`
		Phase     string `json:"phase"`
		SessionID string `json:"sessionId"`
		Message   string `json:"message"`
	}
	for deadline := time.Now().Add(10 * time.Second); ; {
		_, body := get(t, s.URL+"/api/job", string(key))
		json.Unmarshal([]byte(body), &job)
		if !job.Busy || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if job.Phase != "done" {
		t.Fatalf("job = %+v", job)
	}
	_, body := get(t, s.URL+"/api/sessions/"+job.SessionID, string(key))
	watch := chzzk.New().WatchURL(platform.Target{Platform: "chzzk", Kind: platform.KindVOD, ID: "1234"}, 0)
	if !strings.Contains(body, `"state":"finished"`) || !strings.Contains(body, `"chatCount":3`) ||
		!strings.Contains(body, `"watchUrl":"`+watch+`"`) {
		t.Fatalf("session: %s", body)
	}
	code, body := get(t, s.URL+"/api/sessions/"+job.SessionID+"/playback", string(key))
	if code != 200 || !strings.Contains(body, "navercdn.com/1234.m3u8") {
		t.Fatalf("playback: %d %s", code, body)
	}
	req, _ := http.NewRequest("GET", s.URL+"/", nil)
	res, _ := http.DefaultClient.Do(req)
	res.Body.Close()
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "media-src 'self' blob: https://*.navercdn.com") {
		t.Fatalf("csp = %s", csp)
	}
}
