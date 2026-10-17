// Package opencodex reviews chunks through OpenCodex
// (github.com/lidge-jun/opencodex), a proxy the user runs that routes
// requests to the providers and accounts connected in its own dashboard.
// MomentForge sends Chat Completions to it on this computer, where it takes
// requests without a key. Which service and account answers is decided by
// OpenCodex and the model ID; MomentForge does not see it.
package opencodex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// DefaultEndpoint is OpenCodex's default port on this computer.
const DefaultEndpoint = "http://127.0.0.1:10100/v1"

const (
	maxResponse = 4 << 20
	// A proxied account can be slow to answer one chunk.
	requestTimeout = 5 * time.Minute
)

// OpenCodex is an ai.Provider that sends to OpenCodex without a key, and
// starts and stops OpenCodex on this computer with its own commands.
type OpenCodex struct {
	mu          sync.RWMutex
	endpoint    string // "" for the default address
	client      *http.Client
	now         func() time.Time
	home        string       // OpenCodex's folder (config, runtime port, MomentForge's start log); "" to look nowhere
	programPath string       // the ocx executable; found on PATH and in install folders when ""
	found       *localConfig // the config read when the app started
	fallback    string       // the address without a config; DefaultEndpoint unless set
	child       *os.Process  // the OpenCodex this run started
}

// Option configures the provider.
type Option func(*OpenCodex)

// WithClient replaces the HTTP client. Tests only.
func WithClient(c *http.Client) Option { return func(p *OpenCodex) { p.client = c } }

// WithHome reads OpenCodex's files in dir instead of ~/.opencodex ("" turns
// the detection off). Tests and the E2E binary.
func WithHome(dir string) Option { return func(p *OpenCodex) { p.home = dir } }

// WithProgram runs this executable instead of finding ocx. Tests and the E2E binary.
func WithProgram(path string) Option { return func(p *OpenCodex) { p.programPath = path } }

// WithFallbackEndpoint replaces the usual address used without a config, so
// tests never reach an OpenCodex running on this computer. Tests and the E2E binary.
func WithFallbackEndpoint(url string) Option { return func(p *OpenCodex) { p.fallback = url } }

// New returns the provider and reads OpenCodex's config if there is one.
// Redirects are refused.
func New(opts ...Option) *OpenCodex {
	p := &OpenCodex{now: time.Now, client: &http.Client{
		Timeout:       requestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	if home, err := os.UserHomeDir(); err == nil {
		p.home = filepath.Join(home, homeDirName)
	}
	for _, f := range opts {
		f(p)
	}
	p.detect()
	return p
}

func (*OpenCodex) ID() string     { return "opencodex" }
func (*OpenCodex) Name() string   { return "OpenCodex" }
func (*OpenCodex) KeyEnv() string { return "" }

// DefaultModel is empty: the models depend on the providers OpenCodex has.
func (*OpenCodex) DefaultModel() string { return "" }
func (*OpenCodex) MaxOutputTokens() int { return ai.MaxOutputTokens }

// AuthHelp says why there is no key and who decides where requests go.
func (*OpenCodex) AuthHelp() string {
	return "OpenCodex는 이 컴퓨터(127.0.0.1)에서 키 없이 요청을 받으므로 MomentForge에 키를 넣지 않습니다. " +
		"어느 서비스·계정으로 보낼지는 OpenCodex 대시보드에서 연결한 제공자와 모델 ID가 정하며, " +
		"구독 계정을 API처럼 쓰는 것은 그 서비스의 약관에 어긋날 수 있습니다."
}

// Ready reports whether OpenCodex is installed; whether it runs is shown
// (and switched) in 설정, and a send to a stopped one says so.
func (p *OpenCodex) Ready() error {
	_, err := p.program()
	return err
}

// DefaultEndpoint is where the running OpenCodex listens (it may pick
// another port when its own is busy), else its config's port, else its
// usual port on this computer.
func (p *OpenCodex) DefaultEndpoint() string {
	if c, ok := p.runtime(); ok {
		return c.endpoint()
	}
	if p.found != nil {
		return p.found.endpoint()
	}
	if p.fallback != "" {
		return p.fallback
	}
	return DefaultEndpoint
}

// Endpoint is the address requests go to.
func (p *OpenCodex) Endpoint() string {
	p.mu.RLock()
	custom := p.endpoint
	p.mu.RUnlock()
	if custom == "" {
		return p.DefaultEndpoint()
	}
	return custom
}

// SetEndpoint changes the address; "" goes back to the default.
func (p *OpenCodex) SetEndpoint(raw string) error {
	u, err := ai.NormalizeEndpoint(raw)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.endpoint = u
	p.mu.Unlock()
	return nil
}

// EndpointHelp explains the address field in 설정.
func (p *OpenCodex) EndpointHelp() string {
	return "OpenCodex가 요청을 받는 이 컴퓨터의 OpenAI 호환 주소입니다. 비우면 " + p.DefaultEndpoint() +
		"(실행 중인 OpenCodex의 포트, 없으면 OpenCodex 설정의 포트)로 보냅니다. 키는 필요 없습니다."
}

// message is the user turn: the answer format and the input. The proxy may
// translate to services without structured output, so the JSON is asked
// for in words and Validate checks it.
func message(chunk ai.Chunk) string {
	return "아래 입력 JSON을 지시대로 검토하세요. 답은 다음 JSON 스키마를 따르는 JSON 객체 하나로만 쓰고, " +
		"코드 블록이나 설명을 붙이지 마세요.\n\n스키마:\n" + string(ai.Encode(ai.Schema())) +
		"\n\n입력:\n" + string(ai.Encode(chunk)) + "\n"
}

// Review sends one chunk as a Chat Completion. There is no key: OpenCodex
// on this computer takes requests without one.
func (p *OpenCodex) Review(ctx context.Context, _, model string, chunk ai.Chunk) (json.RawMessage, ai.Usage, error) {
	body, _ := json.Marshal(map[string]any{
		"model":  model,
		"stream": false,
		"messages": []map[string]string{
			{"role": "system", "content": ai.Prompt},
			{"role": "user", "content": message(chunk)},
		},
	})
	raw, err := p.do(ctx, http.MethodPost, "/chat/completions", body)
	if err != nil {
		return nil, ai.Usage{}, err
	}
	return parse(raw)
}

// Models lists the models OpenCodex offers.
func (p *OpenCodex) Models(ctx context.Context, _ string) ([]ai.Model, error) {
	raw, err := p.do(ctx, http.MethodGet, "/models", nil)
	if err != nil {
		return nil, err
	}
	var list struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &list) != nil {
		return nil, domain.NewUserError("AI_FORMAT", "OpenCodex 모델 목록을 읽지 못했습니다.")
	}
	out := []ai.Model{}
	for _, m := range list.Data {
		if m.ID == "" || len(m.ID) > 200 {
			continue
		}
		name := m.ID
		if m.OwnedBy != "" {
			name += " (" + m.OwnedBy + ")"
		}
		out = append(out, ai.Model{ID: m.ID, Name: name})
	}
	if len(out) == 0 {
		return nil, domain.NewUserError("AI_UNAVAILABLE", "OpenCodex가 알려 준 모델이 없습니다. OpenCodex 대시보드에서 제공자를 연결하세요.")
	}
	return out, nil
}

// do sends one request and returns a 200 response's body.
func (p *OpenCodex) do(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	endpoint := p.Endpoint()
	req, err := http.NewRequestWithContext(ctx, method, endpoint+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Not connected: nothing was sent, so this is safe to say plainly.
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, domain.WrapUserError("AI_UNAVAILABLE",
				"OpenCodex("+endpoint+")에 연결하지 못했습니다. 설정에서 OpenCodex를 켜거나, 설정의 주소가 맞는지 확인하세요.", err)
		}
		return nil, domain.WrapUserError("AI_OUTCOME_UNKNOWN",
			"전송 결과를 확인할 수 없습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다.", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, domain.WrapUserError("AI_OUTCOME_UNKNOWN",
			"전송 결과를 확인할 수 없습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다.", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, p.httpError(res, raw)
	}
	if len(raw) > maxResponse {
		return nil, domain.NewUserError("AI_FORMAT", "AI 응답이 너무 큽니다.")
	}
	return raw, nil
}

func (p *OpenCodex) httpError(res *http.Response, raw []byte) error {
	detail := strings.TrimSpace(errorText(raw))
	if r := []rune(detail); len(r) > 200 {
		detail = string(r[:200]) + "…"
	}
	text := fmt.Sprintf("OpenCodex가 요청을 거절했습니다 (HTTP %d). 모델 ID와 OpenCodex 대시보드에 연결한 제공자·계정을 확인해 주세요.", res.StatusCode)
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		text = fmt.Sprintf("OpenCodex가 키를 요구했습니다 (HTTP %d). OpenCodex가 127.0.0.1이 아닌 주소에서 받도록 설정되면 키가 필요한데, "+
			"MomentForge는 키 없이 받는 이 컴퓨터의 OpenCodex만 씁니다.", res.StatusCode)
	}
	if detail != "" {
		text += " OpenCodex의 설명: " + detail
	}
	retryable := false
	switch res.StatusCode {
	case 429, 500, 502, 503, 504:
		retryable = true
	}
	wait := 2 * time.Second
	if v := strings.TrimSpace(res.Header.Get("Retry-After")); v != "" {
		if s, err := strconv.Atoi(v); err == nil {
			wait = time.Duration(max(0, s)) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			wait = max(0, t.Sub(p.now()))
		}
	}
	return &ai.ProviderError{UserError: domain.NewUserError(fmt.Sprintf("AI_HTTP_%d", res.StatusCode), text),
		Retryable: retryable, RetryAfter: wait}
}

// errorText is an error body's message: {"error": {"message": "text"}}, or
// {"error": "text"}.
func errorText(raw []byte) string {
	var body struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(body.Error, &text) == nil {
		return text
	}
	var obj struct {
		Message string `json:"message"`
	}
	json.Unmarshal(body.Error, &obj)
	return obj.Message
}

// parse reads a Chat Completion: the first choice's text is the model's JSON.
func parse(raw []byte) (json.RawMessage, ai.Usage, error) {
	var data struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content json.RawMessage `json:"content"`
				Refusal string          `json:"refusal"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens        int `json:"prompt_tokens"`
			CompletionTokens    int `json:"completion_tokens"`
			TotalTokens         int `json:"total_tokens"`
			PromptTokensDetails struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, ai.Usage{}, domain.NewUserError("AI_FORMAT", "AI 응답을 읽지 못했습니다.")
	}
	u := data.Usage
	cached := min(max(u.PromptTokensDetails.CachedTokens, 0), u.PromptTokens)
	usage := ai.Usage{InputTokens: u.PromptTokens - cached, CacheReadTokens: cached, OutputTokens: u.CompletionTokens,
		TotalTokens: u.TotalTokens}
	if len(data.Choices) == 0 {
		return nil, usage, domain.NewUserError("AI_FORMAT", "AI 응답에 답이 없습니다.")
	}
	c := data.Choices[0]
	switch {
	case c.Message.Refusal != "" || c.FinishReason == "content_filter":
		return nil, usage, domain.NewUserError("AI_REFUSAL", "AI가 분석을 거절했습니다.")
	case c.FinishReason == "length":
		return nil, usage, domain.NewUserError("AI_INCOMPLETE", "AI 응답이 끝나지 않았습니다. 추천으로 반영하지 않습니다.")
	}
	out := extractJSON(content(c.Message.Content))
	if out == nil {
		return nil, usage, domain.NewUserError("AI_FORMAT", "AI 추천 JSON을 읽지 못했습니다.")
	}
	return out, usage, nil
}

// content is a message's text: a string, or the text parts of a list.
func content(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	json.Unmarshal(raw, &parts)
	var b strings.Builder
	for _, p := range parts {
		if p.Type == "text" || p.Type == "output_text" {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

// extractJSON returns the answer's JSON object, allowing a code fence or
// text around it; nil when there is none.
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

var (
	_ ai.Provider          = (*OpenCodex)(nil)
	_ ai.ModelLister       = (*OpenCodex)(nil)
	_ ai.Endpointed        = (*OpenCodex)(nil)
	_ ai.SelfAuthenticated = (*OpenCodex)(nil)
	_ ai.Service           = (*OpenCodex)(nil)
)
