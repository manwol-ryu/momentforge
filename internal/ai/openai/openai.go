// Package openai sends review chunks to the OpenAI Responses API with strict
// structured output, the way CzzEdit 0.3.2 did (standard library only).
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

const endpoint = "https://api.openai.com/v1/responses"

// OpenAI is an ai.Provider.
type OpenAI struct {
	endpoint string
	client   *http.Client
	now      func() time.Time
}

// Option configures the provider (tests point it at a fake server).
type Option func(*OpenAI)

// WithEndpoint replaces the Responses endpoint and HTTP client. Tests only.
func WithEndpoint(url string, client *http.Client) Option {
	return func(o *OpenAI) { o.endpoint, o.client = url, client }
}

// New returns the provider. Redirects are refused and a request may take 90 s.
func New(opts ...Option) *OpenAI {
	o := &OpenAI{endpoint: endpoint, now: time.Now, client: &http.Client{
		Timeout:       90 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for _, f := range opts {
		f(o)
	}
	return o
}

func (*OpenAI) ID() string     { return "openai" }
func (*OpenAI) Name() string   { return "OpenAI" }
func (*OpenAI) KeyEnv() string { return "OPENAI_API_KEY" }

const maxResponse = 4 << 20

// Review sends one chunk. store=false asks OpenAI not to keep the request.
func (o *OpenAI) Review(ctx context.Context, key, model string, chunk ai.Chunk) (json.RawMessage, ai.Usage, error) {
	payload := map[string]any{
		"model": model, "store": false, "instructions": ai.Prompt, "input": string(ai.Encode(chunk)),
		"max_output_tokens": ai.MaxOutputTokens,
		"text": map[string]any{"format": map[string]any{"type": "json_schema", "name": "edit_recommendations",
			"strict": true, "schema": ai.Schema()}},
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, ai.Usage{}, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	res, err := o.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ai.Usage{}, ctx.Err()
		}
		return nil, ai.Usage{}, domain.WrapUserError("AI_OUTCOME_UNKNOWN",
			"전송 결과를 확인할 수 없습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다.", err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, maxResponse+1))
	if err != nil {
		return nil, ai.Usage{}, domain.WrapUserError("AI_OUTCOME_UNKNOWN",
			"전송 결과를 확인할 수 없습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다.", err)
	}
	if res.StatusCode != http.StatusOK {
		return nil, ai.Usage{}, o.httpError(res, raw)
	}
	if len(raw) > maxResponse {
		return nil, ai.Usage{}, domain.NewUserError("AI_FORMAT", "AI 응답이 너무 큽니다.")
	}
	return parse(raw)
}

func (o *OpenAI) httpError(res *http.Response, raw []byte) error {
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	json.Unmarshal(raw, &body)
	retryable := false
	switch res.StatusCode {
	case 429, 500, 502, 503, 504:
		retryable = body.Error.Code != "insufficient_quota" && body.Error.Code != "billing_hard_limit_reached"
	}
	wait := 2 * time.Second
	if v := strings.TrimSpace(res.Header.Get("Retry-After")); v != "" {
		if s, err := strconv.Atoi(v); err == nil {
			wait = time.Duration(max(0, s)) * time.Second
		} else if t, err := http.ParseTime(v); err == nil {
			wait = max(0, t.Sub(o.now()))
		}
	}
	return &ai.ProviderError{
		UserError: domain.NewUserError(fmt.Sprintf("AI_HTTP_%d", res.StatusCode),
			fmt.Sprintf("AI 요청이 거부되었습니다 (HTTP %d). 인증·모델·사용 한도를 확인해 주세요.", res.StatusCode)),
		Retryable: retryable, RetryAfter: wait,
	}
}

// parse reads a Responses API result: the joined output_text is the model's JSON.
func parse(raw []byte) (json.RawMessage, ai.Usage, error) {
	var data struct {
		Status string `json:"status"`
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage ai.Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil, ai.Usage{}, domain.NewUserError("AI_FORMAT", "AI 응답을 읽지 못했습니다.")
	}
	if data.Status != "completed" {
		return nil, data.Usage, domain.NewUserError("AI_INCOMPLETE", "AI 응답이 끝나지 않았습니다. 추천으로 반영하지 않습니다.")
	}
	var text strings.Builder
	for _, o := range data.Output {
		if o.Type != "message" {
			continue
		}
		for _, p := range o.Content {
			switch p.Type {
			case "refusal":
				return nil, data.Usage, domain.NewUserError("AI_REFUSAL", "AI가 분석을 거절했습니다.")
			case "output_text":
				text.WriteString(p.Text)
			}
		}
	}
	out := json.RawMessage(text.String())
	if !json.Valid(out) {
		return nil, data.Usage, domain.NewUserError("AI_FORMAT", "AI 추천 JSON을 읽지 못했습니다.")
	}
	return out, data.Usage, nil
}

var _ ai.Provider = (*OpenAI)(nil)
