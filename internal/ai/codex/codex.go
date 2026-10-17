// Package codex reviews chunks through the Codex CLI (`codex exec`), so the
// requests use the user's own Codex login (a ChatGPT plan). MomentForge
// never sees a key.
//
// Chat is untrusted input. Each request runs in a fresh, empty folder with
// every tool turned off (shell, browser, computer use, apps, plugins, image
// generation, web search), the user's Codex config and rules not loaded, a
// read-only sandbox, no session files kept (--ephemeral), and the answer's
// shape fixed by --output-schema. The message goes through stdin. A tool
// that ran anyway fails the request. (Checked 2026-10-17 with Codex CLI
// 0.154.0: asked to use the shell, it answered that it had no shell tool.)
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// DefaultModel leaves the model to Codex (no -m).
const DefaultModel = "default"

const (
	requestTimeout = 10 * time.Minute
	statusTimeout  = 15 * time.Second
	statusTTL      = 30 * time.Second
	maxStdout      = 8 << 20
)

// disabled are the Codex features that are tools; all are turned off.
var disabled = []string{"shell_tool", "browser_use", "browser_use_external", "computer_use", "in_app_browser",
	"apps", "plugins", "image_generation"}

// Codex is an ai.Provider that runs the Codex CLI.
type Codex struct {
	command string // executable; found on PATH or in the usual install folders when ""

	mu      sync.Mutex
	checked time.Time
	ready   error
}

// Option configures the provider.
type Option func(*Codex)

// WithCommand runs this executable instead of looking for codex. Tests and the E2E binary.
func WithCommand(path string) Option { return func(c *Codex) { c.command = path } }

// New returns the provider.
func New(opts ...Option) *Codex {
	c := &Codex{}
	for _, f := range opts {
		f(c)
	}
	return c
}

func (*Codex) ID() string           { return "codex" }
func (*Codex) Name() string         { return "Codex" }
func (*Codex) KeyEnv() string       { return "" }
func (*Codex) DefaultModel() string { return DefaultModel }
func (*Codex) MaxOutputTokens() int { return ai.MaxOutputTokens }

// AuthHelp tells the user how Codex signs in and what it may do.
func (*Codex) AuthHelp() string {
	return "Codex CLI에 ChatGPT로 로그인한 계정을 씁니다. 키는 MomentForge에 넣지 않으며, 로그인은 터미널에서 codex login으로 합니다. " +
		"셸·브라우저·컴퓨터 사용·앱·플러그인·이미지 생성·웹 검색 도구를 모두 끄고 읽기 전용으로 실행하며, Codex 기록을 남기지 않습니다. " +
		"사용량은 ChatGPT 요금제 한도에서 쓰입니다. 모델 ID를 default로 두면 Codex의 기본 모델을 씁니다."
}

var errNotInstalled = domain.NewUserError("AI_UNAVAILABLE",
	"Codex CLI(codex)를 찾지 못했습니다. Codex CLI를 설치하고 codex login으로 로그인한 뒤 앱을 다시 실행하세요.")

var errNotLoggedIn = domain.NewUserError("AI_UNAVAILABLE",
	"Codex CLI에 로그인되어 있지 않습니다. 터미널에서 codex login을 실행해 ChatGPT로 로그인하세요.")

// Ready reports whether codex is installed and signed in (checked at most
// every 30 seconds).
func (c *Codex) Ready() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.checked.IsZero() && time.Since(c.checked) < statusTTL {
		return c.ready
	}
	c.ready = c.loginStatus()
	c.checked = time.Now()
	return c.ready
}

func (c *Codex) loginStatus() error {
	bin, err := c.executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "login", "status")
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(strings.ToLower(string(out)), "logged in") ||
		strings.Contains(strings.ToLower(string(out)), "not logged in") {
		return errNotLoggedIn
	}
	return nil
}

// executable finds codex on PATH, then where its installers put it (an app
// started from Finder may not have the shell's PATH).
func (c *Codex) executable() (string, error) {
	if c.command != "" {
		if _, err := os.Stat(c.command); err != nil {
			return "", errNotInstalled
		}
		return c.command, nil
	}
	if p, err := exec.LookPath("codex"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "codex"), "/opt/homebrew/bin/codex", "/usr/local/bin/codex"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errNotInstalled
}

// message is what Codex receives: the instruction, the answer format and
// the input. Codex has its own system prompt, so the instruction comes first.
func message(chunk ai.Chunk) string {
	return "이 작업에는 도구를 쓰지 않습니다. 파일·명령·웹 도구를 부르지 말고 바로 답하세요.\n\n지시:\n" + ai.Prompt +
		"\n\n아래 입력 JSON을 지시대로 검토하고, 정해진 JSON 스키마를 따르는 JSON 객체 하나로만 답하세요.\n\n입력:\n" +
		string(ai.Encode(chunk)) + "\n"
}

// args are the `codex exec` arguments for one request.
func args(dir, model string) []string {
	a := []string{"exec", "--ephemeral", "--ignore-user-config", "--ignore-rules", "--skip-git-repo-check",
		"--sandbox", "read-only", "--color", "never", "--json",
		"-C", filepath.Join(dir, "work"),
		"--output-schema", filepath.Join(dir, "schema.json"),
		"-o", filepath.Join(dir, "last.txt"),
		"-c", `web_search="disabled"`}
	for _, f := range disabled {
		a = append(a, "--disable", f)
	}
	if model != "" && model != DefaultModel {
		a = append(a, "-m", model)
	}
	return append(a, "-")
}

// Review runs one chunk through `codex exec` and returns the JSON answer.
func (c *Codex) Review(ctx context.Context, key, model string, chunk ai.Chunk) (json.RawMessage, ai.Usage, error) {
	if strings.ContainsAny(model, " \t\r\n") {
		return nil, ai.Usage{}, domain.NewUserError("INPUT", "Codex 모델 ID를 확인해 주세요. 비우거나 default로 두면 Codex의 기본 모델을 씁니다.")
	}
	bin, err := c.executable()
	if err != nil {
		return nil, ai.Usage{}, err
	}
	dir, err := os.MkdirTemp("", "momentforge-codex-*")
	if err != nil {
		return nil, ai.Usage{}, err
	}
	defer os.RemoveAll(dir)
	if err := os.Mkdir(filepath.Join(dir, "work"), 0o700); err != nil {
		return nil, ai.Usage{}, err
	}
	if err := os.WriteFile(filepath.Join(dir, "schema.json"), ai.Encode(ai.Schema()), 0o600); err != nil {
		return nil, ai.Usage{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, bin, args(dir, model)...)
	cmd.Dir = filepath.Join(dir, "work")
	cmd.Stdin = strings.NewReader(message(chunk))
	var stdout limited
	stdout.max = maxStdout
	cmd.Stdout = &stdout
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()
	res := parse(stdout.Bytes())
	if res.text == "" {
		if b, err := os.ReadFile(filepath.Join(dir, "last.txt")); err == nil {
			res.text = string(b)
		}
	}
	switch {
	case ctx.Err() != nil:
		return nil, res.usage, ctx.Err()
	case res.toolUsed:
		return nil, res.usage, domain.NewUserError("AI_TOOL_USE", "AI가 도구를 실행해서 결과를 쓰지 않습니다.")
	case res.err != nil:
		return nil, res.usage, res.err
	case runCtx.Err() != nil:
		return nil, res.usage, domain.NewUserError("AI_OUTCOME_UNKNOWN", "Codex 응답이 10분 안에 끝나지 않았습니다. 중복 사용을 막기 위해 자동 재전송하지 않습니다.")
	case runErr != nil && res.text == "":
		return nil, res.usage, domain.WrapUserError("AI_OUTCOME_UNKNOWN",
			"Codex 실행 결과를 확인할 수 없습니다. 중복 사용을 막기 위해 자동 재전송하지 않습니다.", runErr)
	}
	out := extractJSON(res.text)
	if out == nil {
		return nil, res.usage, domain.NewUserError("AI_FORMAT", "AI 추천 JSON을 읽지 못했습니다.")
	}
	return out, res.usage, nil
}

// result is what one `codex exec --json` printed.
type result struct {
	text     string
	usage    ai.Usage
	err      error
	toolUsed bool
}

// parse reads the run's JSON lines: the last agent message is the answer,
// turn.completed carries the usage, and any other completed item is a tool.
func parse(out []byte) result {
	var r result
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), maxStdout)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message string `json:"message"`
			Item    struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"item"`
			Usage struct {
				Input      int `json:"input_tokens"`
				Cached     int `json:"cached_input_tokens"`
				CacheWrite int `json:"cache_write_input_tokens"`
				Output     int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "item.completed":
			switch ev.Item.Type {
			case "agent_message":
				r.text = ev.Item.Text
			case "reasoning", "todo_list":
			default:
				r.toolUsed = true
			}
		case "turn.completed":
			u := ev.Usage
			cached := min(max(u.Cached, 0), u.Input)
			usage := ai.Usage{InputTokens: u.Input - cached, CacheReadTokens: cached, CacheWriteTokens: u.CacheWrite, OutputTokens: u.Output}
			usage.TotalTokens = usage.AllInput() + usage.OutputTokens
			r.usage = r.usage.Add(usage)
		case "turn.failed", "error":
			if r.err == nil {
				r.err = runError(firstNonEmpty(ev.Error.Message, ev.Message))
			}
		}
	}
	return r
}

// runError explains a failed turn. Plan limits and sign-in problems are not
// retried; MomentForge shows what Codex said.
func runError(msg string) error {
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	low := strings.ToLower(msg)
	text := fmt.Sprintf("Codex가 요청을 처리하지 못했습니다: %s", firstNonEmpty(msg, "알 수 없는 오류"))
	switch {
	case strings.Contains(low, "log in") || strings.Contains(low, "login") || strings.Contains(low, "unauthorized") || strings.Contains(low, "401"):
		text = "Codex 로그인을 확인해 주세요 (터미널에서 codex login): " + msg
	case strings.Contains(low, "usage limit") || strings.Contains(low, "rate limit") || strings.Contains(low, "429"):
		text = "ChatGPT 요금제의 Codex 사용 한도에 닿았습니다. 한도가 풀린 뒤 다시 실행하세요: " + msg
	}
	return &ai.ProviderError{UserError: domain.NewUserError("AI_ERROR", text)}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// extractJSON returns the answer's JSON object, allowing text around it.
func extractJSON(text string) json.RawMessage {
	t := strings.TrimSpace(text)
	if json.Valid([]byte(t)) && strings.HasPrefix(t, "{") {
		return json.RawMessage(t)
	}
	start, end := strings.Index(t, "{"), strings.LastIndex(t, "}")
	if start < 0 || end <= start {
		return nil
	}
	if c := t[start : end+1]; json.Valid([]byte(c)) {
		return json.RawMessage(c)
	}
	return nil
}

// limited keeps at most max bytes of output.
type limited struct {
	bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.Len(); room > 0 {
		if len(p) > room {
			l.Buffer.Write(p[:room])
		} else {
			l.Buffer.Write(p)
		}
	}
	return len(p), nil
}

var (
	_ ai.Provider          = (*Codex)(nil)
	_ ai.SelfAuthenticated = (*Codex)(nil)
)
