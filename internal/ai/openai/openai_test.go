package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

func fake(t *testing.T, h http.HandlerFunc) *OpenAI {
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(WithEndpoint(srv.URL+"/v1/responses", srv.Client()))
}

var chunk = ai.Chunk{StartMs: 0, EndMs: 60000, Candidates: []ai.Candidate{}, Evidence: []ai.Evidence{{Ref: "chat:e1", TimeMs: 1000, Content: "ㅋㅋ"}}}

func TestReviewSendsStrictSchemaAndReadsOutput(t *testing.T) {
	var got map[string]any
	o := fake(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-test" || r.URL.Path != "/v1/responses" {
			t.Errorf("auth %q path %s", r.Header.Get("Authorization"), r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &got)
		w.Write([]byte(`{"status":"completed","output":[{"type":"reasoning"},{"type":"message","content":[` +
			`{"type":"output_text","text":"{\"status\":\"ok\","},{"type":"output_text","text":"\"recommendations\":[]}"}]}],` +
			`"usage":{"input_tokens":120,"output_tokens":30,"total_tokens":150}}`))
	})
	out, usage, err := o.Review(context.Background(), "sk-test", "test-model", chunk)
	if err != nil || string(out) != `{"status":"ok","recommendations":[]}` || usage.TotalTokens != 150 {
		t.Fatalf("out %s usage %+v err %v", out, usage, err)
	}
	format := got["text"].(map[string]any)["format"].(map[string]any)
	if got["store"] != false || got["model"] != "test-model" || format["strict"] != true || format["type"] != "json_schema" ||
		!strings.Contains(got["input"].(string), `"ref":"chat:e1"`) || got["instructions"] != ai.Prompt {
		t.Fatalf("request %v", got)
	}
}

func TestReviewErrors(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		header    string
		body      string
		code      string
		retryable bool
	}{
		{"rate limit", 429, "7", `{"error":{"code":"rate_limit_exceeded"}}`, "AI_HTTP_429", true},
		{"no quota", 429, "", `{"error":{"code":"insufficient_quota"}}`, "AI_HTTP_429", false},
		{"bad key", 401, "", `{"error":{"code":"invalid_api_key"}}`, "AI_HTTP_401", false},
		{"incomplete", 200, "", `{"status":"incomplete","output":[]}`, "AI_INCOMPLETE", false},
		{"refusal", 200, "", `{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"no"}]}]}`, "AI_REFUSAL", false},
		{"not json", 200, "", `{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello"}]}]}`, "AI_FORMAT", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := fake(t, func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.status)
				w.Write([]byte(tc.body))
			})
			_, _, err := o.Review(context.Background(), "k", "m", chunk)
			ue, ok := domain.AsUserError(err)
			if !ok || ue.Code != tc.code {
				t.Fatalf("err %v, want %s", err, tc.code)
			}
			var pe *ai.ProviderError
			isPE := errors.As(err, &pe)
			if (isPE && pe.Retryable) != tc.retryable {
				t.Fatalf("retryable = %v", isPE && pe.Retryable)
			}
			if tc.header == "7" && pe.RetryAfter.Seconds() != 7 {
				t.Fatalf("retry after %v", pe.RetryAfter)
			}
		})
	}
}

func TestReviewUnknownOutcomeIsNotRetryable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close() // the request was sent; no answer comes back
	}))
	defer srv.Close()
	_, _, err := New(WithEndpoint(srv.URL+"/v1/responses", srv.Client())).Review(context.Background(), "k", "m", chunk)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "AI_OUTCOME_UNKNOWN" {
		t.Fatalf("err %v", err)
	}
}
