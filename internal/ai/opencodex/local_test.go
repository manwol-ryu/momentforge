package opencodex

import (
	"context"
	"encoding/json"
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

// The test binary stands in for ocx: run with MF_FAKE_OCX=1, "start" serves
// /healthz like OpenCodex on its config's port (or --port's) and records its
// pid and port in the folder it runs in; "stop" stops the recorded process.
func TestMain(m *testing.M) {
	if os.Getenv("MF_FAKE_OCX") == "1" && len(os.Args) > 1 {
		args := os.Args[1:]
		for len(args) > 0 && strings.HasPrefix(args[0], "-test.") {
			args = args[1:]
		}
		if len(args) == 0 {
			os.Exit(2)
		}
		switch args[0] {
		case "start":
			c, err := readLocalConfig(configName)
			if err != nil {
				os.Exit(3)
			}
			if len(args) == 3 && args[1] == "--port" {
				c.port, _ = strconv.Atoi(args[2])
			}
			l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(c.port)))
			if err != nil {
				os.Exit(4)
			}
			b, _ := json.Marshal(map[string]any{"pid": os.Getpid(), "port": c.port, "attestationSecret": "never-read"})
			os.WriteFile(runtimeName, b, 0o600)
			fmt.Println("opencodex started")
			http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, `{"service":"opencodex","status":"ok","version":"test","uptime":1}`)
			}))
			os.Exit(5)
		case "stop":
			var r struct {
				PID int `json:"pid"`
			}
			if readJSON(runtimeName, &r) == nil && r.PID > 1 {
				if proc, err := os.FindProcess(r.PID); err == nil {
					proc.Kill()
				}
				os.Remove(runtimeName)
			}
			fmt.Println("stopped; Codex restored")
			os.Exit(0)
		}
		os.Exit(2)
	}
	os.Exit(m.Run())
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
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

func TestReadsOpenCodexConfigAndRuntimePort(t *testing.T) {
	home := t.TempDir()
	writeFile(t, home, configName, `{"port": 10123, "hostname": null, "providers": {"openai": {"apiKey": "sk-never-read"}}}`)
	p := New(WithHome(home))
	if p.DefaultEndpoint() != "http://127.0.0.1:10123/v1" || p.Endpoint() != "http://127.0.0.1:10123/v1" {
		t.Fatalf("endpoint %s", p.Endpoint())
	}
	// A running OpenCodex that moved to another port: its record wins while its process lives.
	writeFile(t, home, runtimeName, fmt.Sprintf(`{"pid": %d, "port": 10999, "attestationSecret": "x"}`, os.Getpid()))
	if p.Endpoint() != "http://127.0.0.1:10999/v1" {
		t.Fatalf("runtime port %s", p.Endpoint())
	}
	writeFile(t, home, runtimeName, `{"pid": 1, "port": 10999}`)
	if p.Endpoint() != "http://127.0.0.1:10123/v1" {
		t.Fatalf("a record without a living process %s", p.Endpoint())
	}

	// Listening beyond this computer: OpenCodex then wants a key MomentForge does not send.
	writeFile(t, home, configName, `{"port": 10124, "hostname": "::"}`)
	p = New(WithHome(home), WithProgram(os.Args[0]))
	if p.DefaultEndpoint() != "http://127.0.0.1:10124/v1" {
		t.Fatal(p.DefaultEndpoint())
	}
	if !strings.Contains(p.ServiceStatus(context.Background()).Note, "키를 요구") {
		t.Fatal("a config that needs a key is not pointed out")
	}

	// No config: the usual address.
	if p := New(WithHome(t.TempDir())); p.DefaultEndpoint() != DefaultEndpoint {
		t.Fatal(p.DefaultEndpoint())
	}
}

func TestReadyNeedsTheProgram(t *testing.T) {
	if err := New(WithHome(t.TempDir()), WithProgram("/nonexistent/ocx")).Ready(); !isCode(err, "AI_UNAVAILABLE") ||
		!strings.Contains(err.Error(), "찾지 못했습니다") {
		t.Fatalf("missing: %v", err)
	}
	if err := New(WithHome(t.TempDir()), WithProgram(os.Args[0])).Ready(); err != nil {
		t.Fatal(err)
	}
}

func TestStartAndStopOpenCodex(t *testing.T) {
	home := t.TempDir()
	port := freePort(t)
	writeFile(t, home, configName, fmt.Sprintf(`{"port": %d}`, port))
	t.Setenv("MF_FAKE_OCX", "1") // the child inherits it
	ctx := context.Background()
	p := New(WithHome(home), WithProgram(os.Args[0]))
	st := p.ServiceStatus(ctx)
	if !st.Installed || st.Running || st.Managed || st.Dashboard != "" || st.Config != filepath.Join(home, configName) ||
		!strings.Contains(st.Note, "ocx start") {
		t.Fatalf("before %+v", st)
	}
	if err := p.StartService(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.StopService(context.Background()) })
	st = p.ServiceStatus(ctx)
	if !st.Running || !st.Managed || st.Log != filepath.Join(home, logName) {
		t.Fatalf("after start %+v", st)
	}
	if st.Dashboard != fmt.Sprintf("http://localhost:%d/", port) || st.DashboardOff || !strings.Contains(st.DashboardHelp, "로그인 없이") {
		t.Fatalf("dashboard %+v", st)
	}
	if b, err := os.ReadFile(filepath.Join(home, logName)); err != nil || !strings.Contains(string(b), "opencodex started") {
		t.Fatalf("log %q %v", b, err)
	}
	if err := p.StartService(ctx); err != nil {
		t.Fatalf("starting it again is a no-op: %v", err)
	}

	// The next run of MomentForge stops it too: ocx stop finds it however it was started.
	q := New(WithHome(home), WithProgram(os.Args[0]))
	if st := q.ServiceStatus(ctx); !st.Running || !st.Managed || st.Log != "" {
		t.Fatalf("next run %+v", st)
	}
	if err := q.StopService(ctx); err != nil {
		t.Fatal(err)
	}
	if st := q.ServiceStatus(ctx); st.Running || st.Managed || st.Dashboard != "" {
		t.Fatalf("after stop %+v", st)
	}
	if err := q.StopService(ctx); err != nil {
		t.Fatalf("stopping it again is a no-op: %v", err)
	}
}

func TestOnlyOpenCodexCountsAsRunning(t *testing.T) {
	ctx := context.Background()
	for body, running := range map[string]bool{
		`{"service":"opencodex","status":"ok"}`:           true,
		`{"status":"ok","version":"2.0.0","uptime":12.5}`: true, // an OpenCodex from before /healthz named the service
		`{"service":"something-else","status":"ok"}`:      false,
		`{"status":"ok"}`: false,
		`not json`:        false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/healthz" {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		}))
		p := New(WithHome(t.TempDir()), WithProgram(os.Args[0]))
		p.SetEndpoint(srv.URL + "/v1")
		if st := p.ServiceStatus(ctx); st.Running != running {
			t.Fatalf("%s: %+v", body, st)
		}
		srv.Close()
	}
}

func TestStartAndStopNeedALocalAddressAndTheProgram(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	closed := fmt.Sprintf("http://127.0.0.1:%d/v1", freePort(t)) // nothing answers there
	p := New(WithHome(home), WithProgram(filepath.Join(home, "missing")))
	p.SetEndpoint(closed)
	if err := p.StartService(ctx); !isCode(err, "AI_UNAVAILABLE") || !strings.Contains(err.Error(), "찾지 못했습니다") {
		t.Fatalf("not installed: %v", err)
	}
	if err := p.StopService(ctx); err != nil {
		t.Fatalf("nothing to stop: %v", err)
	}
	if err := p.SetEndpoint("https://hub.example.test/v1"); err != nil {
		t.Fatal(err)
	}
	if err := p.StartService(ctx); !isCode(err, "INPUT") {
		t.Fatalf("remote start: %v", err)
	}
	if err := p.StopService(ctx); !isCode(err, "INPUT") {
		t.Fatalf("remote stop: %v", err)
	}
	if st := p.ServiceStatus(ctx); !strings.Contains(st.Note, "이 컴퓨터가 아니라서") || st.Managed {
		t.Fatalf("remote status %+v", st)
	}
}

func TestStartOnTheAddressSetInSettings(t *testing.T) {
	home := t.TempDir()
	// The config says one port, 설정 another: OpenCodex is started on 설정's.
	writeFile(t, home, configName, fmt.Sprintf(`{"port": %d}`, freePort(t)))
	t.Setenv("MF_FAKE_OCX", "1")
	p := New(WithHome(home), WithProgram(os.Args[0]))
	if err := p.SetEndpoint(fmt.Sprintf("http://127.0.0.1:%d/v1", freePort(t))); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := p.StartService(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { p.StopService(context.Background()) })
	if st := p.ServiceStatus(ctx); !st.Running {
		t.Fatalf("%+v", st)
	}
}
