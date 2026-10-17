// Package opencode reviews chunks through the OpenCode CLI (`opencode run`),
// so the requests come from OpenCode itself and use the user's own OpenCode
// login: OpenCode Go models with the Go subscription, and OpenCode's free
// models. MomentForge never sees a key.
//
// Chat is untrusted input. Each request runs in a fresh, empty folder, the
// message goes through stdin (not the command line), and the OpenCode
// session is deleted afterwards so the chat is not kept in the user's
// OpenCode history. Tools never run:
//   - Go models get a review agent with the fixed instruction, one step and
//     every tool denied.
//   - Free models answer only OpenCode's default agent (the free tier
//     refuses a custom agent, and also the default agent with its tools
//     denied: 403 FreeTierError, checked 2026-10-17 with CLI v2.0.32). They
//     run on that agent with every tool set to "ask"; `opencode run` has no
//     one to ask and rejects each request, so nothing runs. The instruction
//     goes in the message. A tool that completed anyway fails the request.
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Model IDs are OpenCode's provider/model form: Go models, and OpenCode's
// own models (OpenCode Zen), whose "-free" ones cost nothing.
const (
	modelPrefix = "opencode-go/"
	// DefaultModel is a low-cost Go model; the screen lists the others.
	DefaultModel = modelPrefix + "deepseek-v4.1-flash"
	zenPrefix    = "opencode/"
	freeSuffix   = "-free"
)

// isFree reports whether a model is one of OpenCode's free models, which
// run on OpenCode's default agent (see the package comment).
func isFree(model string) bool {
	return strings.HasPrefix(model, zenPrefix) && strings.HasSuffix(model, freeSuffix)
}

// errFreeTier explains OpenCode refusing a free model even so (its rule
// may change); the run is not retried.
var errFreeTier = domain.NewUserError("AI_FREE_TIER",
	"OpenCode가 무료 모델 요청을 거절했습니다. 무료 모델은 OpenCode 기본 에이전트로만 쓸 수 있는데, "+
		"설치된 OpenCode가 그 방식을 받아 주지 않습니다. opencode upgrade로 OpenCode를 최신으로 바꾸거나 "+
		"OpenCode Go 모델·다른 AI 제공자를 고르세요.")

const (
	agentName      = "momentforge-review"
	maxOutput      = 8000 // not enforced by the CLI; the plan's upper estimate
	requestTimeout = 10 * time.Minute
	listTimeout    = 2 * time.Minute
	maxStdout      = 8 << 20
)

// OpenCode is an ai.Provider that runs the OpenCode CLI.
type OpenCode struct {
	command string // executable; found on PATH or in the usual install folders when ""
}

// Option configures the provider (tests use a fake executable).
type Option func(*OpenCode)

// WithCommand runs this executable instead of looking for opencode. Tests only.
func WithCommand(path string) Option { return func(o *OpenCode) { o.command = path } }

// New returns the provider.
func New(opts ...Option) *OpenCode {
	o := &OpenCode{}
	for _, f := range opts {
		f(o)
	}
	return o
}

func (*OpenCode) ID() string           { return "opencode" }
func (*OpenCode) Name() string         { return "OpenCode" }
func (*OpenCode) KeyEnv() string       { return "" }
func (*OpenCode) DefaultModel() string { return DefaultModel }
func (*OpenCode) MaxOutputTokens() int { return maxOutput }

// AuthHelp tells the user how OpenCode signs in.
func (*OpenCode) AuthHelp() string {
	return "OpenCode CLI의 로그인을 씁니다. 키는 MomentForge에 넣지 않습니다. Go 모델(opencode-go/…)은 OpenCode Go 구독이 " +
		"필요하며, 터미널에서 opencode를 실행해 /connect로 연결하세요. 무료 모델(opencode/…-free)은 OpenCode 기본 " +
		"에이전트로 보내고 도구는 모두 자동 거부됩니다. 무료 모델은 모델마다 데이터 정책이 달라, 일부는 무료 기간에 " +
		"받은 내용을 모델 개선에 쓰거나 30일 보관합니다(opencode.ai/docs/zen)."
}

// CheckModel accepts OpenCode's provider/model IDs: Go models and OpenCode's own.
func (*OpenCode) CheckModel(model string) error {
	for _, prefix := range []string{modelPrefix, zenPrefix} {
		if strings.HasPrefix(model, prefix) && len(model) > len(prefix) {
			return nil
		}
	}
	return domain.NewUserError("INPUT",
		"OpenCode 모델 ID는 opencode-go/ 또는 opencode/로 시작합니다 (예: "+DefaultModel+"). 모델 목록에서 고르세요.")
}

// Ready reports whether the opencode executable can be found.
func (o *OpenCode) Ready() error {
	_, err := o.executable()
	return err
}

var errNotInstalled = domain.NewUserError("AI_UNAVAILABLE",
	"OpenCode CLI(opencode)를 찾지 못했습니다. https://opencode.ai 에서 설치한 뒤 앱을 다시 실행하세요.")

// executable finds opencode on PATH, then where its installers put it (an
// app started from Finder may not have the shell's PATH).
func (o *OpenCode) executable() (string, error) {
	if o.command != "" {
		if _, err := os.Stat(o.command); err != nil {
			return "", errNotInstalled
		}
		return o.command, nil
	}
	if p, err := exec.LookPath("opencode"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, ".local", "bin", "opencode"), filepath.Join(home, ".opencode", "bin", "opencode"),
		"/opt/homebrew/bin/opencode", "/usr/local/bin/opencode"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", errNotInstalled
}

// config is the OpenCode config written into each request's empty folder.
// Go models: the review agent has the fixed instruction, one step, and no
// tools. Free models: OpenCode's default agent, every tool set to "ask",
// which `opencode run` rejects (nobody is there to approve).
func config(free bool) []byte {
	if free {
		b, _ := json.MarshalIndent(map[string]any{
			"$schema":     "https://opencode.ai/config.json",
			"permissions": []map[string]string{{"action": "*", "resource": "*", "effect": "ask"}},
		}, "", "  ")
		return b
	}
	deny := []map[string]string{{"action": "*", "resource": "*", "effect": "deny"}}
	b, _ := json.MarshalIndent(map[string]any{
		"$schema":     "https://opencode.ai/config.json",
		"permissions": deny,
		"agents": map[string]any{agentName: map[string]any{
			"description": "MomentForge 채팅 검토 (도구 없음)",
			"mode":        "primary",
			"system":      ai.Prompt,
			"steps":       1,
			"permissions": deny,
		}},
	}, "", "  ")
	return b
}

// message is what the agent receives: the answer format and the input.
// The CLI cannot enforce a JSON schema, so Validate checks the answer. The
// default agent (free models) has its own system prompt, so the
// instruction comes first in the message, with tools ruled out.
func message(chunk ai.Chunk, free bool) string {
	m := "아래 입력 JSON을 지시대로 검토하세요. 답은 다음 JSON 스키마를 따르는 JSON 객체 하나로만 쓰고, " +
		"코드 블록이나 설명을 붙이지 마세요.\n\n스키마:\n" + string(ai.Encode(ai.Schema())) +
		"\n\n입력:\n" + string(ai.Encode(chunk)) + "\n"
	if free {
		m = "이 작업에는 도구를 쓰지 않습니다. 파일·명령·웹 도구를 부르지 말고 바로 답하세요.\n\n지시:\n" + ai.Prompt + "\n\n" + m
	}
	return m
}

// Review runs one chunk through `opencode run` and returns the JSON answer.
func (o *OpenCode) Review(ctx context.Context, key, model string, chunk ai.Chunk) (json.RawMessage, ai.Usage, error) {
	if err := o.CheckModel(model); err != nil {
		return nil, ai.Usage{}, err
	}
	bin, err := o.executable()
	if err != nil {
		return nil, ai.Usage{}, err
	}
	dir, err := os.MkdirTemp("", "momentforge-opencode-*")
	if err != nil {
		return nil, ai.Usage{}, err
	}
	defer os.RemoveAll(dir)
	free := isFree(model)
	if err := os.WriteFile(filepath.Join(dir, "opencode.json"), config(free), 0o600); err != nil {
		return nil, ai.Usage{}, err
	}
	runCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	args := []string{"run", "--standalone", "--format", "json", "--title", "MomentForge 채팅 검토", "-m", model}
	if !free {
		args = append(args, "--agent", agentName)
	}
	cmd := exec.CommandContext(runCtx, bin, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OPENCODE_DISABLE_AUTOUPDATE=1")
	cmd.Stdin = strings.NewReader(message(chunk, free))
	var stdout limited
	stdout.max = maxStdout
	cmd.Stdout = &stdout
	cmd.WaitDelay = 5 * time.Second
	runErr := cmd.Run()
	res := parse(stdout.Bytes())
	if res.sessionID != "" {
		o.deleteSession(bin, dir, res.sessionID)
	}
	switch {
	case ctx.Err() != nil:
		return nil, res.usage, ctx.Err()
	case res.err != nil:
		return nil, res.usage, res.err
	case res.toolUsed:
		return nil, res.usage, domain.NewUserError("AI_TOOL_USE", "AI가 도구를 실행하려 해서 결과를 쓰지 않습니다.")
	case runCtx.Err() != nil:
		return nil, res.usage, domain.NewUserError("AI_OUTCOME_UNKNOWN", "OpenCode 응답이 10분 안에 끝나지 않았습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다.")
	case runErr != nil && res.text == "":
		return nil, res.usage, domain.WrapUserError("AI_OUTCOME_UNKNOWN",
			"OpenCode 실행 결과를 확인할 수 없습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다.", runErr)
	}
	out := extractJSON(res.text)
	if out == nil {
		return nil, res.usage, domain.NewUserError("AI_FORMAT", "AI 추천 JSON을 읽지 못했습니다.")
	}
	return out, res.usage, nil
}

// deleteSession removes the request's OpenCode session so the chat is not
// kept in the user's OpenCode history (best effort).
func (o *OpenCode) deleteSession(bin, dir, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "session", "delete", "--standalone", id)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "OPENCODE_DISABLE_AUTOUPDATE=1")
	if err := cmd.Run(); err != nil {
		slog.Warn("OpenCode session not deleted", "session", id, "err", err)
	}
}

// Models lists the models OpenCode can use (`opencode models`): Go models
// and OpenCode's own, the free ones marked.
func (o *OpenCode) Models(ctx context.Context, key string) ([]ai.Model, error) {
	bin, err := o.executable()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "models", "--standalone")
	cmd.Env = append(os.Environ(), "OPENCODE_DISABLE_AUTOUPDATE=1")
	cmd.Dir = os.TempDir()
	var stdout limited
	stdout.max = 1 << 20
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, domain.WrapUserError("AI_UNAVAILABLE", "OpenCode 모델 목록을 읽지 못했습니다. 터미널에서 opencode models를 실행해 보세요.", err)
	}
	out := []ai.Model{}
	for _, line := range strings.Split(stdout.String(), "\n") {
		id := strings.TrimSpace(line)
		if strings.ContainsAny(id, " \t") {
			continue
		}
		switch {
		case strings.HasPrefix(id, modelPrefix) && len(id) > len(modelPrefix):
			out = append(out, ai.Model{ID: id, Name: "Go " + strings.TrimPrefix(id, modelPrefix)})
		case isFree(id) && len(id) > len(zenPrefix)+len(freeSuffix):
			out = append(out, ai.Model{ID: id, Name: "무료 " + strings.TrimSuffix(strings.TrimPrefix(id, zenPrefix), freeSuffix)})
		}
	}
	if len(out) == 0 {
		return nil, domain.NewUserError("AI_UNAVAILABLE", "OpenCode가 쓸 수 있는 모델을 알려 주지 않았습니다. 터미널에서 opencode models를 실행해 보세요.")
	}
	return out, nil
}

// result is what one `opencode run --format json` printed.
type result struct {
	sessionID string
	text      string
	usage     ai.Usage
	err       error
	toolUsed  bool
}

// parse reads the run's JSON lines. It accepts the event shapes of both
// OpenCode CLI generations: errors as {type, message, status} or
// {name, data: {message, statusCode, isRetryable}}, text in part.text or
// text, usage in a step_finish part's tokens and cost.
func parse(out []byte) result {
	var r result
	var text strings.Builder
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), maxStdout)
	for sc.Scan() {
		var ev struct {
			Type      string          `json:"type"`
			SessionID string          `json:"sessionID"`
			Text      string          `json:"text"`
			Message   string          `json:"message"`
			Part      json.RawMessage `json:"part"`
			Error     json.RawMessage `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		if r.sessionID == "" {
			r.sessionID = ev.SessionID
		}
		var part struct {
			Text   string  `json:"text"`
			Cost   float64 `json:"cost"`
			Tokens *struct {
				Input     int `json:"input"`
				Output    int `json:"output"`
				Reasoning int `json:"reasoning"`
				Cache     struct {
					Read  int `json:"read"`
					Write int `json:"write"`
				} `json:"cache"`
			} `json:"tokens"`
			State struct {
				Status string `json:"status"`
			} `json:"state"`
		}
		json.Unmarshal(ev.Part, &part)
		switch strings.ReplaceAll(ev.Type, "-", "_") {
		case "step_start":
			// Each step starts over: only the last step's text is the answer.
			text.Reset()
		case "text":
			text.WriteString(part.Text)
			if part.Text == "" {
				text.WriteString(ev.Text)
			}
		case "step_finish":
			if t := part.Tokens; t != nil {
				u := ai.Usage{InputTokens: t.Input, CacheReadTokens: t.Cache.Read, CacheWriteTokens: t.Cache.Write,
					OutputTokens: t.Output + t.Reasoning, CostUSD: part.Cost}
				u.TotalTokens = u.AllInput() + u.OutputTokens
				r.usage = r.usage.Add(u)
			}
		case "tool_use":
			if part.State.Status == "completed" {
				r.toolUsed = true
			}
		case "error":
			if r.err == nil {
				r.err = runError(ev.Error, ev.Message)
			}
		}
	}
	r.text = text.String()
	return r
}

// runError maps an error event to a ProviderError: rate limits and server
// errors may be retried, everything else (auth, subscription, unknown
// model) is shown as OpenCode said it.
func runError(raw json.RawMessage, message string) error {
	var e struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Message string `json:"message"`
		Status  int    `json:"status"`
		Data    struct {
			Message     string `json:"message"`
			StatusCode  int    `json:"statusCode"`
			IsRetryable bool   `json:"isRetryable"`
		} `json:"data"`
	}
	json.Unmarshal(raw, &e)
	status := max(e.Status, e.Data.StatusCode)
	msg := firstNonEmpty(e.Message, e.Data.Message, message, "알 수 없는 오류")
	if r := []rune(msg); len(r) > 300 {
		msg = string(r[:300]) + "…"
	}
	kind := firstNonEmpty(e.Type, e.Name)
	code := "AI_ERROR"
	if status > 0 {
		code = fmt.Sprintf("AI_HTTP_%d", status)
	}
	retryable := status == 429 || status >= 500 || e.Data.IsRetryable
	text := fmt.Sprintf("OpenCode가 요청을 처리하지 못했습니다: %s", msg)
	switch {
	case strings.Contains(string(raw), "FreeTierError"):
		text = errFreeTier.Message
	case status == 401 || status == 403 || strings.Contains(kind, "auth"):
		text = fmt.Sprintf("OpenCode 계정이나 Go 구독을 확인해 주세요: %s", msg)
	case strings.Contains(kind, "no-route") || strings.Contains(kind, "model"):
		text = fmt.Sprintf("이 모델을 쓸 수 없습니다: %s", msg)
	}
	return &ai.ProviderError{UserError: domain.NewUserError(code, text), Retryable: retryable, RetryAfter: 5 * time.Second}
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// extractJSON returns the answer's JSON object, allowing a code fence or
// text around it; nil when there is none.
func extractJSON(text string) json.RawMessage {
	t := strings.TrimSpace(text)
	if json.Valid([]byte(t)) {
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

// limited is a buffer that stops growing at max bytes.
type limited struct {
	bytes.Buffer
	max int
}

func (l *limited) Write(p []byte) (int, error) {
	if room := l.max - l.Len(); room > 0 {
		l.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

var (
	_ ai.Provider          = (*OpenCode)(nil)
	_ ai.ModelLister       = (*OpenCode)(nil)
	_ ai.SelfAuthenticated = (*OpenCode)(nil)
)
