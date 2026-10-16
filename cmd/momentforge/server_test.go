package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manwol-ryu/momentforge/internal/api"
	"github.com/manwol-ryu/momentforge/internal/domain"
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
	if code, body := get(t, s.URL+"/api/status", string(key)); code != 200 || !strings.Contains(body, `"schemaVersion":1`) {
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

func TestDemoEndpointUsesRegisteredRules(t *testing.T) {
	dir := t.TempDir()
	s := startTest(t, dir)
	defer shutdown(t, s)
	key, _ := os.ReadFile(filepath.Join(dir, "runtime", "access-key"))
	code, body := get(t, s.URL+"/api/demo", string(key))
	if code != 200 {
		t.Fatalf("demo: %d %s", code, body)
	}
	for _, want := range []string{`"id":"m0003"`, "웃음 증가", "집단 반복", "감정 반응", `"rulesVersion":"1.0.0"`} {
		if !strings.Contains(body, want) {
			t.Errorf("demo response lacks %s", want)
		}
	}
}
