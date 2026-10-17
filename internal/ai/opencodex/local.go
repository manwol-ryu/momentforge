package opencodex

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// OpenCodex on this computer: its folder holds its config and the record of
// the port a running OpenCodex listens on. MomentForge reads only the
// address from them (both files also hold secrets, which it never keeps)
// and writes one file there: the output of the OpenCodex it starts.
const (
	homeDirName = ".opencodex"
	configName  = "config.json"
	runtimeName = "runtime-port.json"
	logName     = "momentforge-start.log"
	defaultPort = 10100
	maxFile     = 16 << 20
)

const (
	probeTimeout = 1500 * time.Millisecond
	// ocx start checks its providers and Codex before it listens.
	startWait = 30 * time.Second
	// ocx stop also puts Codex back on its own settings.
	stopTimeout = time.Minute
	stopWait    = 10 * time.Second
)

// localConfig is the address MomentForge reads from OpenCodex's files.
type localConfig struct {
	host string
	port int
}

// endpoint is the address OpenCodex listens on, as this computer reaches it.
func (c localConfig) endpoint() string {
	host := c.host
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1" // listening everywhere includes this computer
	}
	port := c.port
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/v1"
}

// needsKey reports a config that makes OpenCodex listen beyond this
// computer, where it requires a key on every request.
func (c localConfig) needsKey() bool {
	return c.host != "" && !loopback(c.host)
}

func loopback(host string) bool {
	ip := net.ParseIP(host)
	return host == "localhost" || (ip != nil && ip.IsLoopback())
}

// readJSON reads one of OpenCodex's JSON files into v, which names only the
// fields MomentForge needs.
func readJSON(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewDecoder(io.LimitReader(f, maxFile)).Decode(v)
}

// readLocalConfig reads port and hostname from OpenCodex's config.json.
func readLocalConfig(path string) (localConfig, error) {
	var c struct {
		Port     int    `json:"port"`
		Hostname string `json:"hostname"`
	}
	if err := readJSON(path, &c); err != nil {
		return localConfig{}, err
	}
	return localConfig{host: c.Hostname, port: c.Port}, nil
}

func (p *OpenCodex) configPath() string { return filepath.Join(p.home, configName) }
func (p *OpenCodex) logPath() string    { return filepath.Join(p.home, logName) }

// detect reads the config once when the app starts: its port becomes the
// default address while OpenCodex is not running.
func (p *OpenCodex) detect() {
	if p.home == "" {
		return
	}
	if c, err := readLocalConfig(p.configPath()); err == nil {
		p.found = &c
	}
}

// runtime is the address the running OpenCodex recorded for itself, when
// the process that recorded it is still alive.
func (p *OpenCodex) runtime() (localConfig, bool) {
	if p.home == "" {
		return localConfig{}, false
	}
	var r struct {
		PID      int    `json:"pid"`
		Port     int    `json:"port"`
		Hostname string `json:"hostname"`
	}
	if readJSON(filepath.Join(p.home, runtimeName), &r) != nil || r.PID <= 1 || r.Port <= 0 || r.Port > 65535 || !alive(r.PID) {
		return localConfig{}, false
	}
	return localConfig{host: r.Hostname, port: r.Port}, true
}

// program finds OpenCodex's command: on PATH, then where npm and Homebrew put it.
func (p *OpenCodex) program() (string, error) {
	if p.programPath != "" {
		if st, err := os.Stat(p.programPath); err == nil && !st.IsDir() {
			return p.programPath, nil
		}
		return "", errNotInstalled
	}
	for _, name := range []string{"ocx", "opencodex"} {
		if found, err := exec.LookPath(name); err == nil {
			return found, nil
		}
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{filepath.Join(home, ".local", "bin", "ocx"), filepath.Join(home, ".local", "bin", "opencodex"),
		"/opt/homebrew/bin/ocx", "/usr/local/bin/ocx"} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errNotInstalled
}

var errNotInstalled = domain.NewUserError("AI_UNAVAILABLE",
	"OpenCodex(ocx)를 찾지 못했습니다. README의 OpenCodex 준비 방법대로 설치하세요.")

// command runs one of OpenCodex's commands in its folder. ocx is a Node
// script, so the folder it is in (where npm also put node) leads PATH: an
// app started from Finder may not have the shell's PATH.
func (p *OpenCodex) command(ctx context.Context, prog string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, prog, args...)
	if st, err := os.Stat(p.home); err == nil && st.IsDir() {
		cmd.Dir = p.home
	}
	cmd.Env = withPath(os.Environ(), filepath.Dir(prog))
	return cmd
}

func withPath(env []string, dir string) []string {
	out := make([]string, 0, len(env)+1)
	path := dir
	for _, e := range env {
		if k, v, ok := strings.Cut(e, "="); ok && strings.EqualFold(k, "PATH") {
			if v != "" {
				path = dir + string(os.PathListSeparator) + v
			}
			continue
		}
		out = append(out, e)
	}
	return append(out, "PATH="+path)
}

// base is the address without /v1: where OpenCodex's own pages are.
func (p *OpenCodex) base() (*url.URL, error) {
	u, err := url.Parse(p.Endpoint())
	if err != nil {
		return nil, err
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/v1")
	return u, nil
}

// answers reports whether OpenCodex itself answers at the current address:
// its /healthz names the service, so another program on the port is not
// taken for it.
func (p *OpenCodex) answers(ctx context.Context) bool {
	u, err := p.base()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String()+"/healthz", nil)
	if err != nil {
		return false
	}
	res, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer res.Body.Close()
	var id struct {
		Service *string `json:"service"`
		Status  string  `json:"status"`
		Version *string `json:"version"`
		Uptime  *any    `json:"uptime"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&id) != nil {
		return false
	}
	if id.Service != nil {
		return *id.Service == "opencodex"
	}
	return id.Status == "ok" && id.Version != nil && id.Uptime != nil // before /healthz named the service
}

// dashboardURL is OpenCodex's dashboard: the address's root, by the name
// ocx gui opens it with.
func (p *OpenCodex) dashboardURL() string {
	u, err := p.base()
	if err != nil {
		return ""
	}
	if u.Hostname() == "127.0.0.1" {
		u.Host = net.JoinHostPort("localhost", u.Port())
	}
	u.Path = "/"
	return u.String()
}

// local reports whether the address is this computer, the only place
// MomentForge can start and stop OpenCodex for.
func (p *OpenCodex) local() bool {
	u, err := url.Parse(p.Endpoint())
	return err == nil && loopback(u.Hostname())
}

func (p *OpenCodex) startedHere() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.child != nil && alive(p.child.Pid)
}

// ServiceStatus says whether OpenCodex is installed and running. ocx stop
// stops OpenCodex however it was started, so MomentForge can stop any
// OpenCodex on this computer.
func (p *OpenCodex) ServiceStatus(ctx context.Context) ai.ServiceStatus {
	var st ai.ServiceStatus
	if prog, err := p.program(); err == nil {
		st.Installed, st.Program = true, prog
	}
	if _, err := os.Stat(p.configPath()); err == nil {
		st.Config = p.configPath()
	}
	st.Running = p.answers(ctx)
	local := p.local()
	st.Managed = st.Running && st.Installed && local
	if st.Running && local {
		st.Dashboard = p.dashboardURL()
		st.DashboardHelp = "대시보드는 새 탭에서 열리며, 이 컴퓨터에서는 로그인 없이 들어갑니다. 제공자 연결과 모델 선택은 대시보드에서 합니다."
	}
	if p.startedHere() {
		st.Log = p.logPath()
	}
	switch {
	case p.found != nil && p.found.needsKey():
		st.Note = "OpenCodex 설정의 hostname이 이 컴퓨터(127.0.0.1)가 아니라서 OpenCodex가 키를 요구합니다. " +
			"MomentForge는 키 없이 받는 이 컴퓨터의 OpenCodex만 씁니다."
	case !local:
		st.Note = "주소가 이 컴퓨터가 아니라서 MomentForge에서 켜고 끌 수 없습니다."
	case st.Installed:
		st.Note = "켜기는 ocx start, 끄기는 ocx stop과 같습니다. OpenCodex 설정에 따라 켜져 있는 동안 Codex CLI도 OpenCodex를 거치며, " +
			"끄면 Codex가 원래 설정으로 돌아갑니다."
	}
	return st
}

// StartService runs ocx start in the background, its output going to the
// log. It keeps running when MomentForge quits. With an address set in
// 설정, OpenCodex is started on that address's port.
func (p *OpenCodex) StartService(ctx context.Context) error {
	if !p.local() {
		return domain.NewUserError("INPUT", "OpenCodex 주소가 이 컴퓨터가 아니라서 여기서 켤 수 없습니다.")
	}
	if p.answers(ctx) {
		return nil // already running
	}
	prog, err := p.program()
	if err != nil {
		return err
	}
	args := []string{"start"}
	p.mu.RLock()
	custom := p.endpoint
	p.mu.RUnlock()
	if u, err := url.Parse(custom); custom != "" && err == nil && u.Port() != "" {
		args = append(args, "--port", u.Port())
	}
	if err := os.MkdirAll(p.home, 0o700); err != nil {
		return domain.WrapUserError("AI_UNAVAILABLE", "OpenCodex 폴더를 만들 수 없습니다: "+p.home, err)
	}
	logFile, err := os.OpenFile(p.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return domain.WrapUserError("AI_UNAVAILABLE", "OpenCodex 기록 파일을 열 수 없습니다: "+p.logPath(), err)
	}
	cmd := p.command(context.Background(), prog, args...) // it outlives this request
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return domain.WrapUserError("AI_UNAVAILABLE", "OpenCodex를 실행하지 못했습니다: "+prog, err)
	}
	proc := cmd.Process
	exited := make(chan struct{})
	p.mu.Lock()
	p.child = proc
	p.mu.Unlock()
	go func() {
		cmd.Wait()
		logFile.Close()
		p.mu.Lock()
		if p.child == proc {
			p.child = nil
		}
		p.mu.Unlock()
		close(exited)
	}()

	deadline := time.NewTimer(startWait)
	defer deadline.Stop()
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-exited:
			if p.answers(ctx) {
				return nil
			}
			return domain.NewUserError("AI_UNAVAILABLE", "OpenCodex가 켜지자마자 멈췄습니다. "+lastLine(p.logPath())+"기록을 확인하세요: "+p.logPath())
		case <-deadline.C:
			return domain.NewUserError("AI_UNAVAILABLE", "OpenCodex가 켜졌지만 "+p.Endpoint()+
				"에서 30초 안에 응답하지 않습니다. 설정의 주소와 OpenCodex의 포트가 같은지, 기록을 확인하세요: "+p.logPath())
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if p.answers(ctx) {
				return nil
			}
		}
	}
}

// StopService runs ocx stop, which stops OpenCodex however it was started
// and puts Codex back on its own settings.
func (p *OpenCodex) StopService(ctx context.Context) error {
	if !p.local() {
		return domain.NewUserError("INPUT", "OpenCodex 주소가 이 컴퓨터가 아니라서 여기서 끌 수 없습니다.")
	}
	if !p.answers(ctx) && !p.startedHere() {
		return nil // already off
	}
	prog, err := p.program()
	if err != nil {
		return err
	}
	stopCtx, cancel := context.WithTimeout(ctx, stopTimeout)
	defer cancel()
	out, err := p.command(stopCtx, prog, "stop").CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return domain.WrapUserError("AI_UNAVAILABLE", "OpenCodex를 끄지 못했습니다. "+lastOutputLine(string(out)), err)
	}
	deadline := time.Now().Add(stopWait)
	for p.answers(ctx) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if p.answers(ctx) {
		return domain.NewUserError("AI_UNAVAILABLE", "OpenCodex가 10초 안에 꺼지지 않았습니다. 잠시 뒤 상태를 다시 확인하세요.")
	}
	return nil
}

// lastLine is the last line of a file, as "OpenCodex의 마지막 출력: …. ".
func lastLine(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if t := strings.TrimSpace(sc.Text()); t != "" {
			last = t
		}
	}
	return lastOutputLine(last)
}

func lastOutputLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return ""
	}
	if r := []rune(last); len(r) > 200 {
		last = string(r[:200]) + "…"
	}
	return "OpenCodex의 마지막 출력: " + last + ". "
}
