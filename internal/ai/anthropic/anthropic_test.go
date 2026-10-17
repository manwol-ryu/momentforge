package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// fakeAPI serves the Claude API paths the provider uses and records requests.
type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	requests []recorded
	reply    func(path string, body map[string]any) (int, http.Header, string)
}

type recorded struct {
	method, path string
	header       http.Header
	body         map[string]any
	raw          string
}

func newFake(t *testing.T, reply func(path string, body map[string]any) (int, http.Header, string)) (*Claude, *fakeAPI) {
	f := &fakeAPI{t: t, reply: reply}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.requests = append(f.requests, recorded{r.Method, r.URL.Path, r.Header.Clone(), body, string(raw)})
		f.mu.Unlock()
		code, h, out := f.reply(r.URL.Path, body)
		for k, v := range h {
			w.Header()[k] = v
		}
		if w.Header().Get("Content-Type") == "" {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(code)
		io.WriteString(w, out)
	}))
	t.Cleanup(srv.Close)
	return New(WithBaseURL(srv.URL, srv.Client())), f
}

var chunk = ai.Chunk{StartMs: 0, EndMs: 60000, Candidates: []ai.Candidate{},
	Evidence: []ai.Evidence{{Ref: "chat:e1", TimeMs: 1000, Content: "ㅋㅋㅋ"}}}

func message(stop, text string) string {
	b, _ := json.Marshal(map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": DefaultModel,
		"content": []any{map[string]any{"type": "text", "text": text}}, "stop_reason": stop,
		"usage": map[string]any{"input_tokens": 300, "output_tokens": 40, "cache_read_input_tokens": 1200,
			"cache_creation_input_tokens": 0}})
	return string(b)
}

const answer = `{"status":"ok","recommendations":[]}`

func TestReviewRequestShapeAndUsage(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "must-not-be-sent")
	c, f := newFake(t, func(string, map[string]any) (int, http.Header, string) { return 200, nil, message("end_turn", answer) })
	for range 2 {
		out, u, err := c.Review(context.Background(), "sk-ant-test", DefaultModel, chunk)
		if err != nil || string(out) != answer {
			t.Fatalf("out %s err %v", out, err)
		}
		if u.InputTokens != 300 || u.CacheReadTokens != 1200 || u.OutputTokens != 40 || u.TotalTokens != 1540 {
			t.Fatalf("usage %+v", u)
		}
	}
	if len(f.requests) != 2 {
		t.Fatalf("%d requests", len(f.requests))
	}
	r := f.requests[0]
	if r.path != "/v1/messages" || r.header.Get("X-Api-Key") != "sk-ant-test" || r.header.Get("Authorization") != "" {
		t.Fatalf("path %s key %q auth %q", r.path, r.header.Get("X-Api-Key"), r.header.Get("Authorization"))
	}
	b := r.body
	if b["model"] != DefaultModel || b["max_tokens"] != float64(maxOutput) {
		t.Fatalf("model %v max_tokens %v", b["model"], b["max_tokens"])
	}
	if _, ok := b["thinking"]; ok {
		t.Fatal("thinking must stay at the model default (adaptive)")
	}
	sys := b["system"].([]any)[0].(map[string]any)
	if sys["text"] != ai.Prompt || sys["cache_control"].(map[string]any)["type"] != "ephemeral" {
		t.Fatalf("system block %v", sys["cache_control"])
	}
	oc := b["output_config"].(map[string]any)
	format := oc["format"].(map[string]any)
	if oc["effort"] != "high" || format["type"] != "json_schema" || format["schema"] == nil {
		t.Fatalf("output_config %v", oc)
	}
	msg := b["messages"].([]any)[0].(map[string]any)
	text := msg["content"].([]any)[0].(map[string]any)["text"]
	if msg["role"] != "user" || text != string(ai.Encode(chunk)) {
		t.Fatalf("message %v", msg)
	}
	// The cached prefix (model, output config, system) must be byte-identical
	// between requests; only the messages differ.
	prefix := func(raw string) string {
		return raw[:strings.Index(raw, `"messages"`)] + raw[strings.Index(raw, `"output_config"`):]
	}
	if prefix(f.requests[0].raw) != prefix(f.requests[1].raw) {
		t.Fatalf("request prefix differs:\n%s\n%s", f.requests[0].raw, f.requests[1].raw)
	}
}

func TestReviewStopReasons(t *testing.T) {
	for stop, code := range map[string]string{"refusal": "AI_REFUSAL", "max_tokens": "AI_INCOMPLETE"} {
		c, _ := newFake(t, func(string, map[string]any) (int, http.Header, string) { return 200, nil, message(stop, "{") })
		_, u, err := c.Review(context.Background(), "k", DefaultModel, chunk)
		if ue, ok := domain.AsUserError(err); !ok || ue.Code != code || u.OutputTokens != 40 {
			t.Fatalf("%s: %v usage %+v", stop, err, u)
		}
	}
	c, _ := newFake(t, func(string, map[string]any) (int, http.Header, string) {
		return 200, nil, message("end_turn", "not json")
	})
	if _, _, err := c.Review(context.Background(), "k", DefaultModel, chunk); err == nil {
		t.Fatal("non-JSON answer accepted")
	}
}

func TestErrorsAreClassifiedAndNotRetriedBySDK(t *testing.T) {
	cases := []struct {
		status    int
		header    http.Header
		retryable bool
		wait      time.Duration
	}{
		{429, http.Header{"Retry-After": {"7"}}, true, 7 * time.Second},
		{529, nil, true, 2 * time.Second},
		{500, nil, true, 2 * time.Second},
		{400, nil, false, 0},
		{401, nil, false, 0},
		{404, nil, false, 0},
	}
	for _, tc := range cases {
		c, f := newFake(t, func(string, map[string]any) (int, http.Header, string) {
			return tc.status, tc.header, `{"type":"error","error":{"type":"x","message":"no"},"request_id":"req_1"}`
		})
		_, _, err := c.Review(context.Background(), "k", DefaultModel, chunk)
		var pe *ai.ProviderError
		if !errors.As(err, &pe) || pe.Retryable != tc.retryable || (tc.retryable && pe.RetryAfter != tc.wait) {
			t.Fatalf("HTTP %d: %v", tc.status, err)
		}
		if len(f.requests) != 1 {
			t.Fatalf("HTTP %d: the SDK sent %d requests; the app does the retrying", tc.status, len(f.requests))
		}
	}
	// A refused connection was never sent: retryable. (Port 1 is closed.)
	c := New(WithBaseURL("http://127.0.0.1:1", http.DefaultClient))
	_, _, err := c.Review(context.Background(), "k", DefaultModel, chunk)
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || !pe.Retryable || pe.Code != "AI_CONNECT" {
		t.Fatalf("connection refused: %v", err)
	}
}

func TestCountTokensAndModels(t *testing.T) {
	c, f := newFake(t, func(path string, _ map[string]any) (int, http.Header, string) {
		switch path {
		case "/v1/messages/count_tokens":
			return 200, nil, `{"input_tokens":1234}`
		case "/v1/models":
			return 200, nil, `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","type":"model","created_at":"2026-08-01T00:00:00Z"}],"has_more":false,"first_id":"claude-opus-5-5","last_id":"claude-opus-5-5"}`
		}
		return 404, nil, `{}`
	})
	n, err := c.CountTokens(context.Background(), "k", DefaultModel, chunk)
	if err != nil || n != 1234 {
		t.Fatalf("count %d err %v", n, err)
	}
	body := f.requests[0].body
	if body["system"] == nil || body["output_config"] == nil || body["model"] != DefaultModel {
		t.Fatalf("count request must match the review request: %v", body)
	}
	models, err := c.Models(context.Background(), "k")
	if err != nil || len(models) != 1 || models[0].ID != "claude-opus-5-5" || models[0].Name != "Claude Opus 5.5" {
		t.Fatalf("models %v err %v", models, err)
	}
	if p, ok := c.Price(DefaultModel); !ok || p.Input != 4 || p.Output != 20 || p.BatchShare != 0.5 {
		t.Fatalf("price %+v", p)
	}
	if _, ok := c.Price("unknown"); ok {
		t.Fatal("unknown models have no price")
	}
}

func TestBatchLifecycle(t *testing.T) {
	batch := func(status string) string {
		return `{"id":"msgbatch_1","type":"message_batch","processing_status":"` + status + `",` +
			`"request_counts":{"processing":0,"succeeded":1,"errored":1,"canceled":0,"expired":0},` +
			`"created_at":"2026-10-17T00:00:00Z","expires_at":"2026-10-18T00:00:00Z","results_url":null}`
	}
	c, f := newFake(t, func(path string, _ map[string]any) (int, http.Header, string) {
		switch path {
		case "/v1/messages/batches":
			return 200, nil, batch("in_progress")
		case "/v1/messages/batches/msgbatch_1":
			return 200, nil, batch("ended")
		case "/v1/messages/batches/msgbatch_1/cancel":
			return 200, nil, batch("canceling")
		case "/v1/messages/batches/msgbatch_1/results":
			ok := `{"custom_id":"c2","result":{"type":"succeeded","message":` + message("end_turn", answer) + `}}`
			bad := `{"custom_id":"c1","result":{"type":"errored","error":{"type":"error","error":{"type":"overloaded_error","message":"busy"},"request_id":"r"}}}`
			return 200, http.Header{"Content-Type": {"application/x-jsonl"}}, ok + "\n" + bad + "\n"
		}
		return 404, nil, `{}`
	})
	ctx := context.Background()
	id, err := c.SubmitBatch(ctx, "k", DefaultModel, []ai.BatchRequest{{ID: "c1", Chunk: chunk}, {ID: "c2", Chunk: chunk}})
	if err != nil || id != "msgbatch_1" {
		t.Fatalf("submit %q %v", id, err)
	}
	reqs := f.requests[0].body["requests"].([]any)
	first := reqs[0].(map[string]any)
	p := first["params"].(map[string]any)
	if len(reqs) != 2 || first["custom_id"] != "c1" || p["model"] != DefaultModel || p["output_config"] == nil || p["system"] == nil {
		t.Fatalf("batch request %v", first)
	}
	st, err := c.BatchStatus(ctx, "k", id)
	if err != nil || !st.Ended || st.Succeeded != 1 || st.Errored != 1 {
		t.Fatalf("status %+v %v", st, err)
	}
	res, err := c.BatchResults(ctx, "k", id)
	if err != nil || len(res) != 2 {
		t.Fatalf("results %v %v", res, err)
	}
	byID := map[string]ai.BatchResult{}
	for _, r := range res {
		byID[r.ID] = r
	}
	if string(byID["c2"].Output) != answer || byID["c2"].Err != nil || byID["c2"].Usage.CacheReadTokens != 1200 {
		t.Fatalf("succeeded result %+v", byID["c2"])
	}
	if ue, ok := domain.AsUserError(byID["c1"].Err); !ok || ue.Code != "AI_BATCH_ERROR" {
		t.Fatalf("errored result %+v", byID["c1"])
	}
	if err := c.CancelBatch(ctx, "k", id); err != nil {
		t.Fatal(err)
	}
}
