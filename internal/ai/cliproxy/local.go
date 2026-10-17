package cliproxy

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// CLIProxyAPI on this computer: its folder holds the config the user (or
// MomentForge's README) set up, the pid file and the log, the same names
// the README's commands use.
const (
	homeDirName = ".cli-proxy-api"
	configName  = "config.yaml"
	pidName     = "cli-proxy-api.pid"
	logName     = "cli-proxy-api.log"
	defaultPort = 8317
	// mgmtKeyName holds the dashboard's login key: CLIProxyAPI keeps only a
	// hash of it in its config (MomentForge's setup writes it, README).
	mgmtKeyName   = "management-key.txt"
	dashboardPath = "/management.html"
)

const (
	probeTimeout = 1500 * time.Millisecond
	startWait    = 8 * time.Second
	stopWait     = 5 * time.Second
)

// localConfig is what MomentForge reads from CLIProxyAPI's config.yaml.
type localConfig struct {
	host string
	port int
	tls  bool
	keys []string // access.api-keys: the client keys
	// dashboard: a management key is set and the control panel is not turned off.
	dashboard bool
}

// endpoint is the address the config makes CLIProxyAPI listen on, as this
// computer reaches it.
func (c localConfig) endpoint() string {
	host := c.host
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "127.0.0.1" // listening everywhere includes this computer
	}
	port := c.port
	if port <= 0 || port > 65535 {
		port = defaultPort
	}
	scheme := "http"
	if c.tls {
		scheme = "https"
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/v1"
}

// listensEverywhere reports a config that accepts other computers too.
func (c localConfig) listensEverywhere() bool {
	ip := net.ParseIP(c.host)
	return c.host == "" || (ip != nil && ip.IsUnspecified())
}

// readLocalConfig reads the few values MomentForge needs from CLIProxyAPI's
// YAML config: server.host, server.port, server.tls.enable and
// access.api-keys (the v8 layout; v7's top-level host and port too). It is
// a small line reader for that file, not a general YAML parser.
func readLocalConfig(path string) (localConfig, error) {
	f, err := os.Open(path)
	if err != nil {
		return localConfig{}, err
	}
	defer f.Close()
	scalars := map[string]string{}
	lists := map[string][]string{}
	var stack []level
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		raw := strings.TrimRightFunc(stripComment(sc.Text()), unicode.IsSpace)
		body := strings.TrimLeft(raw, " ")
		if body == "" {
			continue
		}
		indent := len(raw) - len(body)
		if strings.HasPrefix(body, "- ") || body == "-" {
			// A list item belongs to the nearest key above it with less indent.
			for len(stack) > 0 && stack[len(stack)-1].indent > indent {
				stack = stack[:len(stack)-1]
			}
			if len(stack) > 0 {
				p := joinPath(stack)
				lists[p] = append(lists[p], unquote(strings.TrimSpace(strings.TrimPrefix(body, "-"))))
			}
			continue
		}
		key, value, ok := strings.Cut(body, ":")
		if !ok {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, level{indent, strings.TrimSpace(key)})
		if v := strings.TrimSpace(value); v != "" {
			scalars[joinPath(stack)] = unquote(v)
		}
	}
	if err := sc.Err(); err != nil {
		return localConfig{}, err
	}
	c := localConfig{host: firstOf(scalars, "server.host", "host"), keys: lists["access.api-keys"]}
	if p, err := strconv.Atoi(firstOf(scalars, "server.port", "port")); err == nil {
		c.port = p
	}
	c.tls, _ = strconv.ParseBool(firstOf(scalars, "server.tls.enable", "tls.enable"))
	// The management key (v8 management, v7 remote-management) turns the
	// management API on; without it the dashboard is a 404.
	panelOff, _ := strconv.ParseBool(firstOf(scalars, "management.disable-control-panel", "remote-management.disable-control-panel"))
	c.dashboard = firstOf(scalars, "management.secret-key", "remote-management.secret-key") != "" && !panelOff
	return c, nil
}

// level is one key on the way down to a line, by its indentation.
type level struct {
	indent int
	key    string
}

func joinPath(stack []level) string {
	keys := make([]string, len(stack))
	for i, l := range stack {
		keys[i] = l.key
	}
	return strings.Join(keys, ".")
}

func firstOf(m map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return ""
}

// stripComment drops a # comment that is not inside quotes.
func stripComment(line string) string {
	var quote rune
	for i, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#' && (i == 0 || line[i-1] == ' ' || line[i-1] == '\t'):
			return line[:i]
		}
	}
	return line
}

func unquote(v string) string {
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

func (p *CLIProxy) configPath() string { return filepath.Join(p.home, configName) }
func (p *CLIProxy) pidPath() string    { return filepath.Join(p.home, pidName) }
func (p *CLIProxy) logPath() string    { return filepath.Join(p.home, logName) }

// detect reads the config once when the app starts: its address becomes
// the default one, its first client key the key MomentForge finds.
func (p *CLIProxy) detect() {
	if p.home == "" {
		return
	}
	c, err := readLocalConfig(p.configPath())
	if err != nil {
		return
	}
	p.found = &c
}

// FindKey returns CLIProxyAPI's first client key from its config.
func (p *CLIProxy) FindKey() (string, string) {
	if p.found == nil {
		return "", ""
	}
	for _, k := range p.found.keys {
		if k != "" && len(k) <= 1024 && !strings.ContainsFunc(k, unicode.IsSpace) && !strings.HasPrefix(k, "your-api-key") {
			return k, p.configPath()
		}
	}
	return "", ""
}

// program finds the CLIProxyAPI executable: on PATH, then where its
// release and Homebrew put it.
func (p *CLIProxy) program() (string, error) {
	if p.programPath != "" {
		if st, err := os.Stat(p.programPath); err == nil && !st.IsDir() {
			return p.programPath, nil
		}
		return "", errNotInstalled
	}
	for _, name := range []string{"cli-proxy-api", "cliproxyapi"} {
		if found, err := exec.LookPath(name); err == nil {
			return found, nil
		}
	}
	home, _ := os.UserHomeDir()
	for _, c := range []string{filepath.Join(home, ".local", "bin", "cli-proxy-api"), "/opt/homebrew/bin/cliproxyapi",
		"/usr/local/bin/cliproxyapi", "/usr/local/bin/cli-proxy-api"} {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c, nil
		}
	}
	return "", errNotInstalled
}

var errNotInstalled = domain.NewUserError("AI_UNAVAILABLE",
	"CLIProxyAPI(cli-proxy-api)를 찾지 못했습니다. README의 CLIProxyAPI 준비 방법대로 설치하세요.")

// answers reports whether anything answers HTTP at the current address.
func (p *CLIProxy) answers(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.Endpoint()+"/models", nil)
	if err != nil {
		return false
	}
	res, err := p.client.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return true
}

// dashboardURL is the dashboard next to the API: the address without /v1.
func (p *CLIProxy) dashboardURL() string {
	u, err := url.Parse(p.Endpoint())
	if err != nil {
		return ""
	}
	u.Path = strings.TrimSuffix(strings.TrimSuffix(u.Path, "/"), "/v1") + dashboardPath
	return u.String()
}

// local reports whether the address is this computer, the only place
// MomentForge can start the program for.
func (p *CLIProxy) local() bool {
	u, err := url.Parse(p.Endpoint())
	if err != nil {
		return false
	}
	h := u.Hostname()
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

// ServiceStatus says whether CLIProxyAPI is installed and running, and
// whether MomentForge can stop it.
func (p *CLIProxy) ServiceStatus(ctx context.Context) ai.ServiceStatus {
	st := ai.ServiceStatus{Log: p.logPath()}
	if prog, err := p.program(); err == nil {
		st.Installed, st.Program = true, prog
	}
	if _, err := os.Stat(p.configPath()); err == nil {
		st.Config = p.configPath()
	}
	st.Running = p.answers(ctx)
	st.Managed = st.Running && p.managedPID() > 0
	// The dashboard, read fresh from the config (CLIProxyAPI rewrites it).
	if st.Running && p.local() {
		if c, err := readLocalConfig(p.configPath()); err == nil {
			if c.dashboard {
				st.Dashboard = p.dashboardURL()
				st.DashboardHelp = "대시보드는 새 탭에서 열리며 로그인에 관리 키가 필요합니다. "
				if _, err := os.Stat(filepath.Join(p.home, mgmtKeyName)); err == nil {
					st.DashboardHelp += "관리 키는 " + filepath.Join(p.home, mgmtKeyName) + " 파일에 있습니다."
				} else {
					st.DashboardHelp += "관리 키는 CLIProxyAPI 설정의 management.secret-key로 정한 값입니다."
				}
			} else {
				st.DashboardOff = true
				st.DashboardHelp = "대시보드가 꺼져 있습니다. " + p.configPath() + "에서 management.secret-key(관리 키)를 정하고 " +
					"disable-control-panel을 false로 둔 뒤 CLIProxyAPI를 다시 켜면 열 수 있습니다."
			}
		}
	}
	switch {
	case st.Installed && st.Config == "":
		st.Note = "CLIProxyAPI 설정 파일이 없습니다: " + p.configPath() + ". README의 CLIProxyAPI 준비 방법대로 만드세요."
	case p.found != nil && p.found.listensEverywhere():
		st.Note = "설정 파일의 server.host가 비어 있어 CLIProxyAPI가 다른 기기의 접속도 받습니다. 127.0.0.1로 두기를 권합니다."
	case !p.local():
		st.Note = "주소가 이 컴퓨터가 아니라서 MomentForge에서 켜고 끌 수 없습니다."
	}
	return st
}

// StartService runs CLIProxyAPI in the background with its config, its
// output going to the log and its process ID to the pid file (as the
// README's nohup command does). It keeps running when MomentForge quits.
func (p *CLIProxy) StartService(ctx context.Context) error {
	if !p.local() {
		return domain.NewUserError("INPUT", "CLIProxyAPI 주소가 이 컴퓨터가 아니라서 여기서 켤 수 없습니다.")
	}
	if p.answers(ctx) {
		return nil // already running
	}
	prog, err := p.program()
	if err != nil {
		return err
	}
	if _, err := os.Stat(p.configPath()); err != nil {
		return domain.NewUserError("AI_UNAVAILABLE", "CLIProxyAPI 설정 파일이 없습니다: "+p.configPath()+". README의 CLIProxyAPI 준비 방법대로 만드세요.")
	}
	logFile, err := os.OpenFile(p.logPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return domain.WrapUserError("AI_UNAVAILABLE", "CLIProxyAPI 로그 파일을 열 수 없습니다: "+p.logPath(), err)
	}
	cmd := exec.Command(prog, "-config", p.configPath())
	cmd.Dir = p.home
	cmd.Stdout, cmd.Stderr = logFile, logFile
	detach(cmd)
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return domain.WrapUserError("AI_UNAVAILABLE", "CLIProxyAPI를 실행하지 못했습니다: "+prog, err)
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
	os.WriteFile(p.pidPath(), []byte(strconv.Itoa(proc.Pid)+"\n"), 0o600)

	deadline := time.NewTimer(startWait)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-exited:
			return domain.NewUserError("AI_UNAVAILABLE", "CLIProxyAPI가 켜지자마자 멈췄습니다. 로그를 확인하세요: "+p.logPath())
		case <-deadline.C:
			return domain.NewUserError("AI_UNAVAILABLE", "CLIProxyAPI가 켜졌지만 "+p.Endpoint()+
				"에서 응답하지 않습니다. 설정의 주소와 CLIProxyAPI 설정 파일의 host·port가 같은지 확인하세요.")
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if p.answers(ctx) {
				return nil
			}
		}
	}
}

// StopService stops the CLIProxyAPI MomentForge started, or the one its pid
// file names when that process really is CLIProxyAPI.
func (p *CLIProxy) StopService(ctx context.Context) error {
	pid := p.managedPID()
	if pid == 0 {
		if p.answers(ctx) {
			return domain.NewUserError("AI_UNAVAILABLE",
				"이 CLIProxyAPI는 MomentForge가 알 수 없는 방법으로 실행되어 여기서 끌 수 없습니다. 실행한 터미널에서 끄세요.")
		}
		return nil // already off
	}
	if err := terminate(pid); err != nil {
		return domain.WrapUserError("AI_UNAVAILABLE", "CLIProxyAPI를 끄지 못했습니다.", err)
	}
	deadline := time.Now().Add(stopWait)
	for alive(pid) && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	if alive(pid) {
		return domain.NewUserError("AI_UNAVAILABLE", "CLIProxyAPI가 5초 안에 꺼지지 않았습니다. 잠시 뒤 상태를 다시 확인하세요.")
	}
	if b, err := os.ReadFile(p.pidPath()); err == nil && strings.TrimSpace(string(b)) == strconv.Itoa(pid) {
		os.Remove(p.pidPath())
	}
	return nil
}

// managedPID is the CLIProxyAPI process MomentForge may stop: the one it
// started, else the pid file's when that process runs CLIProxyAPI.
func (p *CLIProxy) managedPID() int {
	p.mu.RLock()
	child := p.child
	p.mu.RUnlock()
	if child != nil && alive(child.Pid) {
		return child.Pid
	}
	b, err := os.ReadFile(p.pidPath())
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pid <= 1 || !alive(pid) {
		return 0
	}
	names := []string{"cli-proxy-api", "cliproxyapi"}
	if prog, err := p.program(); err == nil {
		names = append(names, filepath.Base(prog))
	}
	if !runs(pid, names) {
		return 0
	}
	return pid
}
