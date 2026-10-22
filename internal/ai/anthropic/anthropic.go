// Package anthropic sends review chunks to Claude with the official Go SDK:
// structured output for the answer schema, an explicit effort, and the fixed
// instruction marked for prompt caching. It also counts tokens, lists models
// and runs Message Batches. It is MomentForge's default AI provider.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// DefaultModel is the model filled in for the user.
const DefaultModel = "claude-opus-5-5"

// maxOutput is the answer limit of one request. Thinking counts against it,
// so it is larger than the JSON answer needs.
const maxOutput = 8000

// priceDate is when the list prices below were taken; the screen shows it.
const priceDate = "2026-10"

// prices are list prices in US dollars per million tokens. Cache writes
// (5-minute TTL) cost 1.25 times the input price and batches half the price.
// Haiku's price is for prompts of 100K tokens or fewer, as every request here is.
var prices = map[string]ai.Price{
	"claude-opus-5-5":   {Input: 4, Output: 20, CacheRead: 0.20, CacheWrite: 5, BatchShare: 0.5, AsOf: priceDate},
	"claude-sonnet-5-5": {Input: 2, Output: 10, CacheRead: 0.20, CacheWrite: 2.5, BatchShare: 0.5, AsOf: priceDate},
	"claude-haiku-5-5":  {Input: 0.10, Output: 0.50, CacheRead: 0.01, CacheWrite: 0.125, BatchShare: 0.5, AsOf: priceDate},
	"claude-fable-5-1":  {Input: 10, Output: 50, CacheRead: 0.25, CacheWrite: 12.5, BatchShare: 0.5, AsOf: priceDate},
}

// Claude is an ai.Provider with every optional feature.
type Claude struct {
	baseURL string // "" for the SDK's production endpoint
	http    *http.Client
}

// Option configures the provider (tests point it at a fake server).
type Option func(*Claude)

// WithBaseURL replaces the API base URL and HTTP client. Tests only.
func WithBaseURL(url string, client *http.Client) Option {
	return func(c *Claude) { c.baseURL, c.http = url, client }
}

// New returns the provider. Redirects are refused.
func New(opts ...Option) *Claude {
	c := &Claude{http: &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	for _, f := range opts {
		f(c)
	}
	return c
}

func (*Claude) ID() string           { return "anthropic" }
func (*Claude) Name() string         { return "Claude (Anthropic)" }
func (*Claude) KeyEnv() string       { return "ANTHROPIC_API_KEY" }
func (*Claude) DefaultModel() string { return DefaultModel }
func (*Claude) MaxOutputTokens() int { return maxOutput }

// taskModels are the models each task starts with: fixing a transcript is
// many short, simple requests (the fastest, cheapest model), a scene
// explanation weighs chat and speech (a balanced one), and the cut analysis
// reasons over steps and evidence (the strongest).
var taskModels = map[string]string{
	ai.TaskTranscript: "claude-haiku-5-5",
	ai.TaskExplain:    "claude-sonnet-5-5",
	ai.TaskCut:        DefaultModel,
}

// TaskModel recommends a model for a task ("" for an unknown task).
func (*Claude) TaskModel(task string) string { return taskModels[task] }

// Price returns a model's list price.
func (*Claude) Price(model string) (ai.Price, bool) {
	p, ok := prices[model]
	return p, ok
}

// client builds an SDK client for one call. Environment defaults are off so
// that only the key the user gave is used and nothing (ANTHROPIC_BASE_URL,
// profiles) can send it elsewhere; the app retries by itself.
func (c *Claude) client(key string) *sdk.Client {
	opts := []option.RequestOption{option.WithoutEnvironmentDefaults(), option.WithAPIKey(key),
		option.WithHTTPClient(c.http), option.WithMaxRetries(0)}
	if c.baseURL != "" {
		opts = append(opts, option.WithBaseURL(c.baseURL))
	}
	cl := sdk.NewClient(opts...)
	return &cl
}

// system is the cached prefix: the step's fixed instruction, identical in
// every request of that step.
func system(prompt string) []sdk.TextBlockParam {
	return []sdk.TextBlockParam{{Text: prompt, CacheControl: sdk.NewCacheControlEphemeralParam()}}
}

// outputConfig asks for the answer schema at high effort. Thinking is left
// at the model's default, adaptive on current models.
func outputConfig(schema map[string]any) sdk.OutputConfigParam {
	return sdk.OutputConfigParam{Effort: sdk.OutputConfigEffortHigh, Format: sdk.JSONOutputFormatParam{Schema: schema}}
}

func params(model string, step ai.StepRequest) sdk.MessageNewParams {
	return sdk.MessageNewParams{Model: model, MaxTokens: int64(max(maxOutput, step.OutputLimit())), System: system(step.Prompt),
		Messages:     []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(string(step.InputJSON())))},
		OutputConfig: outputConfig(step.Schema)}
}

// Review sends one chunk and returns Claude's JSON answer.
func (c *Claude) Review(ctx context.Context, key, model string, chunk ai.Chunk) (json.RawMessage, ai.Usage, error) {
	return c.Step(ctx, key, model, chunk.Step())
}

// Step sends one step of a multi-step analysis.
func (c *Claude) Step(ctx context.Context, key, model string, step ai.StepRequest) (json.RawMessage, ai.Usage, error) {
	msg, err := c.client(key).Messages.New(ctx, params(model, step))
	if err != nil {
		return nil, ai.Usage{}, mapError(ctx, err)
	}
	return parse(msg)
}

func usage(u sdk.Usage) ai.Usage {
	out := ai.Usage{InputTokens: int(u.InputTokens), CacheReadTokens: int(u.CacheReadInputTokens),
		CacheWriteTokens: int(u.CacheCreationInputTokens), OutputTokens: int(u.OutputTokens)}
	out.TotalTokens = out.AllInput() + out.OutputTokens
	return out
}

// parse reads a finished message: the joined text blocks are the JSON answer.
func parse(m *sdk.Message) (json.RawMessage, ai.Usage, error) {
	u := usage(m.Usage)
	switch m.StopReason {
	case sdk.StopReasonRefusal:
		return nil, u, domain.NewUserError("AI_REFUSAL", "Claude가 이 구간의 분석을 거절했습니다. 다른 구간이나 모델로 시도해 주세요.")
	case sdk.StopReasonMaxTokens, sdk.StopReasonModelContextWindowExceeded:
		return nil, u, domain.NewUserError("AI_INCOMPLETE", "AI 응답이 출력 한도에서 끊겼습니다. 추천으로 반영하지 않습니다.")
	}
	var text strings.Builder
	for _, b := range m.Content {
		if t, ok := b.AsAny().(sdk.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	out := json.RawMessage(text.String())
	if !json.Valid(out) {
		return nil, u, domain.NewUserError("AI_FORMAT", "AI 추천 JSON을 읽지 못했습니다.")
	}
	return out, u, nil
}

// CountTokens counts a chunk's input tokens exactly, as Review would send it.
func (c *Claude) CountTokens(ctx context.Context, key, model string, chunk ai.Chunk) (int, error) {
	res, err := c.client(key).Messages.CountTokens(ctx, sdk.MessageCountTokensParams{Model: model,
		System:       sdk.MessageCountTokensParamsSystemUnion{OfTextBlockArray: system(ai.Prompt)},
		Messages:     []sdk.MessageParam{sdk.NewUserMessage(sdk.NewTextBlock(string(ai.Encode(chunk))))},
		OutputConfig: outputConfig(ai.Schema())})
	if err != nil {
		return 0, mapError(ctx, err)
	}
	return int(res.InputTokens), nil
}

// Models lists the models the key can use (Models API, first 100).
func (c *Claude) Models(ctx context.Context, key string) ([]ai.Model, error) {
	page, err := c.client(key).Models.List(ctx, sdk.ModelListParams{Limit: sdk.Int(100)})
	if err != nil {
		return nil, mapError(ctx, err)
	}
	out := []ai.Model{}
	for _, m := range page.Data {
		out = append(out, ai.Model{ID: m.ID, Name: m.DisplayName})
	}
	return out, nil
}

// SubmitBatch sends every chunk as one Message Batch (half price; results
// within 24 hours). Request IDs come back as custom_id.
func (c *Claude) SubmitBatch(ctx context.Context, key, model string, reqs []ai.BatchRequest) (string, error) {
	items := make([]sdk.MessageBatchNewParamsRequest, 0, len(reqs))
	for _, r := range reqs {
		p := params(model, r.Chunk.Step())
		items = append(items, sdk.MessageBatchNewParamsRequest{CustomID: r.ID, Params: sdk.MessageBatchNewParamsRequestParams{
			Model: p.Model, MaxTokens: p.MaxTokens, System: p.System, Messages: p.Messages, OutputConfig: p.OutputConfig}})
	}
	b, err := c.client(key).Messages.Batches.New(ctx, sdk.MessageBatchNewParams{Requests: items})
	if err != nil {
		return "", mapError(ctx, err)
	}
	return b.ID, nil
}

// BatchStatus reports a batch's progress.
func (c *Claude) BatchStatus(ctx context.Context, key, id string) (ai.BatchStatus, error) {
	b, err := c.client(key).Messages.Batches.Get(ctx, id, sdk.MessageBatchGetParams{})
	if err != nil {
		return ai.BatchStatus{}, mapError(ctx, err)
	}
	n := b.RequestCounts
	return ai.BatchStatus{Ended: b.ProcessingStatus == sdk.MessageBatchProcessingStatusEnded, Processing: int(n.Processing),
		Succeeded: int(n.Succeeded), Errored: int(n.Errored), Canceled: int(n.Canceled), Expired: int(n.Expired)}, nil
}

// BatchResults reads an ended batch's results (in any order).
func (c *Claude) BatchResults(ctx context.Context, key, id string) ([]ai.BatchResult, error) {
	stream := c.client(key).Messages.Batches.ResultsStreaming(ctx, id, sdk.MessageBatchResultsParams{})
	defer stream.Close()
	var out []ai.BatchResult
	for stream.Next() {
		r := stream.Current()
		res := ai.BatchResult{ID: r.CustomID}
		switch v := r.Result.AsAny().(type) {
		case sdk.MessageBatchSucceededResult:
			res.Output, res.Usage, res.Err = parse(&v.Message)
		case sdk.MessageBatchErroredResult:
			res.Err = domain.NewUserError("AI_BATCH_ERROR", fmt.Sprintf("배치 요청이 실패했습니다 (%s).", v.Error.Error.Type))
		case sdk.MessageBatchCanceledResult:
			res.Err = domain.NewUserError("CANCELLED", "배치 요청이 취소되었습니다.")
		case sdk.MessageBatchExpiredResult:
			res.Err = domain.NewUserError("AI_BATCH_EXPIRED", "배치 요청이 24시간 안에 처리되지 않아 만료되었습니다.")
		default:
			res.Err = domain.NewUserError("AI_FORMAT", "배치 결과 형식을 확인하지 못했습니다.")
		}
		out = append(out, res)
	}
	if err := stream.Err(); err != nil {
		return nil, mapError(ctx, err)
	}
	return out, nil
}

// CancelBatch asks Anthropic to stop a batch; finished requests stay billed.
func (c *Claude) CancelBatch(ctx context.Context, key, id string) error {
	if _, err := c.client(key).Messages.Batches.Cancel(ctx, id, sdk.MessageBatchCancelParams{}); err != nil {
		return mapError(ctx, err)
	}
	return nil
}

var outcomeUnknown = "전송 결과를 확인할 수 없습니다. 중복 비용을 막기 위해 자동 재전송하지 않습니다."

// mapError turns SDK errors into ProviderErrors: rate limits, overload and
// server errors may be retried; a refused connection was never sent and may
// be retried; anything else after sending has an unknown outcome.
func mapError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var apierr *sdk.Error
	if errors.As(err, &apierr) {
		code := apierr.StatusCode
		var msg string
		switch {
		case code == 400:
			msg = "Claude가 요청 형식을 거부했습니다 (HTTP 400). 모델 ID와 입력을 확인해 주세요."
		case code == 401:
			msg = "API 키가 맞지 않습니다 (HTTP 401). 키를 다시 확인해 주세요."
		case code == 402:
			msg = "결제·크레딧을 확인해 주세요 (HTTP 402)."
		case code == 403:
			msg = "이 키로 해당 모델이나 기능을 쓸 권한이 없습니다 (HTTP 403)."
		case code == 404:
			msg = "모델 ID나 배치를 찾을 수 없습니다 (HTTP 404). 모델 목록에서 고르거나 ID를 확인해 주세요."
		case code == 413:
			msg = "요청이 너무 큽니다 (HTTP 413)."
		case code == 429:
			msg = "사용 한도에 닿았습니다 (HTTP 429). 잠시 기다린 뒤 다시 보냅니다."
		case code == 529:
			msg = "Claude가 과부하 상태입니다 (HTTP 529). 잠시 기다린 뒤 다시 보냅니다."
		case code >= 500:
			msg = fmt.Sprintf("Claude 서버 오류입니다 (HTTP %d).", code)
		default:
			msg = fmt.Sprintf("AI 요청이 거부되었습니다 (HTTP %d).", code)
		}
		return &ai.ProviderError{UserError: domain.NewUserError(fmt.Sprintf("AI_HTTP_%d", code), msg),
			Retryable: code == 429 || code >= 500, RetryAfter: retryAfter(apierr.Response)}
	}
	var op *net.OpError
	if errors.As(err, &op) && op.Op == "dial" {
		return &ai.ProviderError{UserError: domain.WrapUserError("AI_CONNECT",
			"Claude API에 연결하지 못했습니다. 네트워크를 확인해 주세요.", err), Retryable: true, RetryAfter: 2 * time.Second}
	}
	return domain.WrapUserError("AI_OUTCOME_UNKNOWN", outcomeUnknown, err)
}

func retryAfter(res *http.Response) time.Duration {
	wait := 2 * time.Second
	if res == nil {
		return wait
	}
	if v := strings.TrimSpace(res.Header.Get("Retry-After")); v != "" {
		if s, err := strconv.Atoi(v); err == nil {
			return time.Duration(max(0, s)) * time.Second
		}
		if t, err := http.ParseTime(v); err == nil {
			return max(0, time.Until(t))
		}
	}
	return wait
}

var (
	_ ai.Provider      = (*Claude)(nil)
	_ ai.Priced        = (*Claude)(nil)
	_ ai.TokenCounter  = (*Claude)(nil)
	_ ai.ModelLister   = (*Claude)(nil)
	_ ai.BatchProvider = (*Claude)(nil)
)
