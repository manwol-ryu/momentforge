package cliproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The test binary stands in for cli-proxy-api: run with MF_FAKE_CLIPROXY=1
// and "-config <file>", it answers on the config's port like CLIProxyAPI
// without a key (401) until it is stopped.
func TestMain(m *testing.M) {
	if os.Getenv("MF_FAKE_CLIPROXY") == "1" {
		for i, a := range os.Args {
			if a == "-config" && i+1 < len(os.Args) {
				c, err := readLocalConfig(os.Args[i+1])
				if err != nil {
					os.Exit(3)
				}
				http.ListenAndServe(net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port)),
					http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprint(w, `{"error":"Invalid API key"}`)
					}))
				os.Exit(4)
			}
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, configName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestReadsCLIProxyAPIConfig(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `# CLIProxyAPI v8
config-version: 8
server:
  # this computer only
  host: "127.0.0.1"   # inline comment
  port: 9123
  tls:
    enable: false
management:
  secret-key: ""
access:
  api-keys:
    - "your-api-key-1"
    - "mf-real#key"
    - 'second'
api-keys:
  - provider-group-key
`)
	p := New(WithHome(home))
	if p.DefaultEndpoint() != "http://127.0.0.1:9123/v1" || p.Endpoint() != "http://127.0.0.1:9123/v1" {
		t.Fatalf("endpoint %s", p.Endpoint())
	}
	if key, where := p.FindKey(); key != "mf-real#key" || where != filepath.Join(home, configName) {
		t.Fatalf("key %q from %q", key, where)
	}

	// The v7 layout, an empty host (every interface) and TLS.
	writeConfig(t, home, "host: \"\"\nport: 8443\ntls:\n  enable: true\napi-keys:\n  - not-a-client-key\n")
	p = New(WithHome(home))
	if p.DefaultEndpoint() != "https://127.0.0.1:8443/v1" {
		t.Fatal(p.DefaultEndpoint())
	}
	if key, _ := p.FindKey(); key != "" {
		t.Fatalf("a v7 top-level api-keys entry is not a client key here: %q", key)
	}
	if !strings.Contains(p.ServiceStatus(context.Background()).Note, "다른 기기") {
		t.Fatal("listening on every interface is not pointed out")
	}

	// As CLIProxyAPI rewrites it: comments at column 0 inside sections, the key hashed.
	writeConfig(t, home, "config-version: 8\nserver:\n# this computer only\n  host: \"127.0.0.1\"\n  port: 9124\nmanagement:\n"+
		"  allow-remote: false\n# key\n  secret-key: \"$2a$10$abc\"\n  disable-control-panel: false\naccess:\n  api-keys:\n    - \"mf-k\"\n")
	c, err := readLocalConfig(filepath.Join(home, configName))
	if err != nil || c.port != 9124 || !c.dashboard || len(c.keys) != 1 || c.keys[0] != "mf-k" {
		t.Fatalf("rewritten config %+v %v", c, err)
	}
	writeConfig(t, home, "management:\n  secret-key: \"\"\n  disable-control-panel: false\n")
	if c, _ := readLocalConfig(filepath.Join(home, configName)); c.dashboard {
		t.Fatal("no management key: no dashboard")
	}
	writeConfig(t, home, "management:\n  secret-key: \"x\"\n  disable-control-panel: true\n")
	if c, _ := readLocalConfig(filepath.Join(home, configName)); c.dashboard {
		t.Fatal("control panel turned off: no dashboard")
	}

	// No config: the usual address and no key.
	p = New(WithHome(t.TempDir()))
	if p.DefaultEndpoint() != DefaultEndpoint {
		t.Fatal(p.DefaultEndpoint())
	}
	if key, _ := p.FindKey(); key != "" {
		t.Fatal(key)
	}
}

func TestStartAndStopCLIProxyAPI(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	writeConfig(t, home, fmt.Sprintf("server:\n  host: \"127.0.0.1\"\n  port: %d\naccess:\n  api-keys:\n    - \"k\"\n", port))
	t.Setenv("MF_FAKE_CLIPROXY", "1") // the child inherits it
	ctx := context.Background()
	p := New(WithHome(home), WithProgram(os.Args[0]))
	st := p.ServiceStatus(ctx)
	if !st.Installed || st.Running || st.Managed || st.Config != filepath.Join(home, configName) {
		t.Fatalf("before %+v", st)
	}
	if err := p.StartService(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.StopService(context.Background()) })
	st = p.ServiceStatus(ctx)
	if !st.Running || !st.Managed {
		t.Fatalf("after start %+v", st)
	}
	if st.Dashboard != "" || !st.DashboardOff {
		t.Fatalf("no management key in this config: %+v", st)
	}
	// Turned on in the config (read fresh), with the login key's file next to it.
	writeConfig(t, home, fmt.Sprintf("server:\n  host: \"127.0.0.1\"\n  port: %d\nmanagement:\n  secret-key: \"k\"\n"+
		"  disable-control-panel: false\naccess:\n  api-keys:\n    - \"k\"\n", port))
	os.WriteFile(filepath.Join(home, mgmtKeyName), []byte("k\n"), 0o600)
	st = p.ServiceStatus(ctx)
	if st.Dashboard != fmt.Sprintf("http://127.0.0.1:%d/management.html", port) || st.DashboardOff ||
		st.DashboardKey != filepath.Join(home, mgmtKeyName) {
		t.Fatalf("dashboard %+v", st)
	}
	b, err := os.ReadFile(filepath.Join(home, pidName))
	if err != nil || strings.TrimSpace(string(b)) == "" {
		t.Fatalf("pid file: %q %v", b, err)
	}
	if err := p.StartService(ctx); err != nil {
		t.Fatalf("starting it again is a no-op: %v", err)
	}

	// The next run of MomentForge stops it through the pid file.
	q := New(WithHome(home), WithProgram(os.Args[0]))
	if st := q.ServiceStatus(ctx); !st.Managed {
		t.Fatalf("next run %+v", st)
	}
	if err := q.StopService(ctx); err != nil {
		t.Fatal(err)
	}
	if st := q.ServiceStatus(ctx); st.Running || st.Managed {
		t.Fatalf("after stop %+v", st)
	}
	if _, err := os.Stat(filepath.Join(home, pidName)); !os.IsNotExist(err) {
		t.Fatal("the pid file stays")
	}
	if err := q.StopService(ctx); err != nil {
		t.Fatalf("stopping it again is a no-op: %v", err)
	}
}

func TestStopLeavesOtherProgramsAlone(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.NotFoundHandler()) // a CLIProxyAPI started some other way
	t.Cleanup(srv.Close)
	home := t.TempDir()
	fakeProgram := filepath.Join(home, "cli-proxy-api")
	os.WriteFile(fakeProgram, []byte("#!/bin/sh\n"), 0o700)
	// The pid file names this test process, which is not cli-proxy-api.
	os.WriteFile(filepath.Join(home, pidName), []byte(strconv.Itoa(os.Getpid())), 0o600)
	p := New(WithHome(home), WithProgram(fakeProgram))
	if err := p.SetEndpoint(srv.URL + "/v1"); err != nil {
		t.Fatal(err)
	}
	if st := p.ServiceStatus(ctx); !st.Running || st.Managed {
		t.Fatalf("status %+v", st)
	}
	if err := p.StopService(ctx); !isCode(err, "AI_UNAVAILABLE") {
		t.Fatalf("stop: %v", err)
	}
}

func TestStartNeedsALocalAddressTheProgramAndItsConfig(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	closed := fmt.Sprintf("http://127.0.0.1:%d/v1", freePort(t)) // nothing answers there
	p := New(WithHome(home), WithProgram(filepath.Join(home, "missing")))
	p.SetEndpoint(closed)
	if err := p.StartService(ctx); !isCode(err, "AI_UNAVAILABLE") || !strings.Contains(err.Error(), "찾지 못했습니다") {
		t.Fatalf("not installed: %v", err)
	}
	p = New(WithHome(home), WithProgram(os.Args[0]))
	p.SetEndpoint(closed)
	if err := p.StartService(ctx); !isCode(err, "AI_UNAVAILABLE") || !strings.Contains(err.Error(), "설정 파일이 없습니다") {
		t.Fatalf("no config: %v", err)
	}
	if err := p.SetEndpoint("https://proxy.example.test/v1"); err != nil {
		t.Fatal(err)
	}
	if err := p.StartService(ctx); !isCode(err, "INPUT") {
		t.Fatalf("remote address: %v", err)
	}
}
