package cliproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

var chunk = ai.Chunk{StartMs: 0, EndMs: 60000, Candidates: []ai.Candidate{},
	Evidence: []ai.Evidence{{Ref: "chat:e1", TimeMs: 1000, Content: "채팅 문구"}}}

func isCode(err error, code string) bool {
	ue, ok := domain.AsUserError(err)
	return ok && ue.Code == code
}

// server answers like CLIProxyAPI's OpenAI-compatible routes and records the last request.
func server(t *testing.T, status int, reply string) (*CLIProxy, *http.Request, *[]byte) {
	t.Helper()
	var got http.Request
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = *r
		body, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusTooManyRequests {
			w.Header().Set("Retry-After", "7")
		}
		w.WriteHeader(status)
		io.WriteString(w, reply)
	}))
	t.Cleanup(srv.Close)
	p := New(WithClient(srv.Client()), WithHome(t.TempDir()))
	if err := p.SetEndpoint(srv.URL + "/v1/"); err != nil {
		t.Fatal(err)
	}
	return p, &got, &body
}

func TestReviewSendsAChatCompletion(t *testing.T) {
	reply := `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"` +
		"```json\\n{\\\"status\\\":\\\"ok\\\",\\\"recommendations\\\":[]}\\n```" +
		`"}}],"usage":{"prompt_tokens":120,"completion_tokens":30,"total_tokens":150,"prompt_tokens_details":{"cached_tokens":20}}}`
	p, req, body := server(t, http.StatusOK, reply)
	out, u, err := p.Review(context.Background(), "client-key", "test-model", chunk)
	if err != nil || string(out) != `{"status":"ok","recommendations":[]}` {
		t.Fatalf("out %s err %v", out, err)
	}
	if u.InputTokens != 100 || u.CacheReadTokens != 20 || u.OutputTokens != 30 || u.TotalTokens != 150 {
		t.Fatalf("usage %+v", u)
	}
	if req.Method != http.MethodPost || req.URL.Path != "/v1/chat/completions" || req.Header.Get("Authorization") != "Bearer client-key" {
		t.Fatalf("request %s %s %q", req.Method, req.URL.Path, req.Header.Get("Authorization"))
	}
	var sent struct {
		Model    string              `json:"model"`
		Stream   bool                `json:"stream"`
		Messages []map[string]string `json:"messages"`
	}
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatal(err)
	}
	if sent.Model != "test-model" || sent.Stream || len(sent.Messages) != 2 || sent.Messages[0]["role"] != "system" ||
		sent.Messages[0]["content"] != ai.Prompt || !strings.Contains(sent.Messages[1]["content"], string(ai.Encode(chunk))) {
		t.Fatalf("sent %+v", sent)
	}
}

func TestReviewReadsTextPartsAndRefusals(t *testing.T) {
	p, _, _ := server(t, http.StatusOK, `{"choices":[{"finish_reason":"stop","message":{"content":[{"type":"text","text":"{\"status\":\"ok\",\"recommendations\":[]}"}]}}]}`)
	if out, _, err := p.Review(context.Background(), "k", "m", chunk); err != nil || !json.Valid(out) {
		t.Fatalf("parts: %s %v", out, err)
	}
	for reply, code := range map[string]string{
		`{"choices":[{"finish_reason":"stop","message":{"content":"","refusal":"no"}}]}`: "AI_REFUSAL",
		`{"choices":[{"finish_reason":"length","message":{"content":"{\"status\":"}}]}`:  "AI_INCOMPLETE",
		`{"choices":[{"finish_reason":"stop","message":{"content":"추천할 구간이 없습니다"}}]}`:    "AI_FORMAT",
		`{"choices":[]}`: "AI_FORMAT",
	} {
		p, _, _ := server(t, http.StatusOK, reply)
		if _, _, err := p.Review(context.Background(), "k", "m", chunk); !isCode(err, code) {
			t.Fatalf("%s: %v", reply, err)
		}
	}
}

func TestHTTPErrors(t *testing.T) {
	p, _, _ := server(t, http.StatusUnauthorized, `{"error":{"message":"Invalid API key"}}`)
	_, _, err := p.Review(context.Background(), "wrong", "m", chunk)
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Code != "AI_HTTP_401" || pe.Retryable || !strings.Contains(pe.Message, "access.api-keys") ||
		!strings.Contains(pe.Message, "Invalid API key") {
		t.Fatalf("401: %v", err)
	}
	// CLIProxyAPI's own errors carry the text directly (seen from v8.0.32).
	p, _, _ = server(t, http.StatusUnauthorized, `{"error":"Invalid API key"}`)
	if _, _, err = p.Review(context.Background(), "wrong", "m", chunk); !errors.As(err, &pe) || !strings.Contains(pe.Message, "Invalid API key") {
		t.Fatalf("plain error text: %v", err)
	}
	p, _, _ = server(t, http.StatusTooManyRequests, `{"error":{"message":"quota"}}`)
	_, _, err = p.Review(context.Background(), "k", "m", chunk)
	if !errors.As(err, &pe) || !pe.Retryable || pe.RetryAfter.Seconds() != 7 {
		t.Fatalf("429: %v", err)
	}
}

func TestNotRunningSaysWhereItLooked(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close() // nothing listens there now
	p := New(WithHome(t.TempDir()))
	if err := p.SetEndpoint("http://" + addr + "/v1"); err != nil {
		t.Fatal(err)
	}
	_, _, err = p.Review(context.Background(), "k", "m", chunk)
	if ue, _ := domain.AsUserError(err); ue == nil || ue.Code != "AI_UNAVAILABLE" || !strings.Contains(ue.Message, addr) {
		t.Fatalf("not running: %v", err)
	}
}

func TestModels(t *testing.T) {
	p, req, _ := server(t, http.StatusOK, `{"object":"list","data":[{"id":"test-model","owned_by":"proxy"},{"id":""}]}`)
	models, err := p.Models(context.Background(), "k")
	if err != nil || len(models) != 1 || models[0].ID != "test-model" || models[0].Name != "test-model (proxy)" {
		t.Fatalf("models %v err %v", models, err)
	}
	if req.Method != http.MethodGet || req.URL.Path != "/v1/models" || req.Header.Get("Authorization") != "Bearer k" {
		t.Fatalf("request %s %s", req.Method, req.URL.Path)
	}
	p, _, _ = server(t, http.StatusOK, `{"data":[]}`)
	if _, err := p.Models(context.Background(), "k"); !isCode(err, "AI_UNAVAILABLE") {
		t.Fatalf("empty: %v", err)
	}
}

func TestEndpoint(t *testing.T) {
	p := New(WithHome(t.TempDir()))
	if p.Endpoint() != DefaultEndpoint || p.DefaultEndpoint() != DefaultEndpoint {
		t.Fatal(p.Endpoint())
	}
	if err := p.SetEndpoint(" https://proxy.example.test:9000/v1/ "); err != nil || p.Endpoint() != "https://proxy.example.test:9000/v1" {
		t.Fatalf("%v %s", err, p.Endpoint())
	}
	for _, bad := range []string{"ftp://x/v1", "127.0.0.1:8317", "http://", "http://user:pw@host/v1", "http://h/v1?x=1", "http://h/v 1"} {
		if err := p.SetEndpoint(bad); !isCode(err, "INPUT") {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	if p.Endpoint() != "https://proxy.example.test:9000/v1" {
		t.Fatal("a rejected address changed the endpoint")
	}
	if err := p.SetEndpoint(""); err != nil || p.Endpoint() != DefaultEndpoint {
		t.Fatalf("reset: %v %s", err, p.Endpoint())
	}
}
