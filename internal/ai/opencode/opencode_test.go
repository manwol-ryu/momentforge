package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// fakeScript stands in for the opencode CLI: it records its arguments,
// stdin, folder and config into $FAKE_LOG and prints events for $FAKE_MODE.
const fakeScript = `#!/bin/sh
case "$1" in
run)
  printf '%s\n' "$@" > "$FAKE_LOG/args"
  cat > "$FAKE_LOG/stdin"
  pwd > "$FAKE_LOG/cwd"
  cp opencode.json "$FAKE_LOG/config.json"
  case "$FAKE_MODE" in
  ok)
    printf '%s\n' '{"type":"step_start","timestamp":1,"sessionID":"ses_1","part":{"type":"step-start"}}'
    printf '%s\n' '{"type":"text","timestamp":2,"sessionID":"ses_1","part":{"type":"text","text":"` + "```" + `json\n{\"status\":\"ok\",\"recommendations\":[]}\n` + "```" + `"}}'
    printf '%s\n' '{"type":"step_finish","timestamp":3,"sessionID":"ses_1","part":{"reason":"stop","cost":0.002,"tokens":{"input":100,"output":20,"reasoning":5,"cache":{"read":900,"write":0}}}}'
    ;;
  subscription)
    printf '%s\n' '{"type":"error","timestamp":1,"sessionID":"ses_2","error":{"type":"provider.auth","message":"An active OpenCode Go subscription is required to use Go models.","status":403}}'
    exit 1 ;;
  rate)
    printf '%s\n' '{"type":"error","timestamp":1,"sessionID":"ses_3","error":{"name":"APIError","data":{"message":"Rate limit exceeded","statusCode":429,"isRetryable":true}}}'
    exit 1 ;;
  tool)
    printf '%s\n' '{"type":"tool_use","timestamp":1,"sessionID":"ses_4","part":{"tool":"bash","state":{"status":"completed"}}}'
    printf '%s\n' '{"type":"text","timestamp":2,"sessionID":"ses_4","part":{"text":"{\"status\":\"ok\",\"recommendations\":[]}"}}'
    ;;
  crash)
    exit 2 ;;
  freetier)
    printf '%s\n' '{"type":"error","timestamp":1,"sessionID":"ses_5","error":{"type":"provider.auth","message":"OpenCode free tier can only be used from within OpenCode.","status":403,"response":{"body":"{\"type\":\"error\",\"error\":{\"type\":\"FreeTierError\"}}"}}}'
    exit 1 ;;
  esac ;;
session)
  printf '%s\n' "$@" >> "$FAKE_LOG/deleted" ;;
models)
  printf 'opencode/beam-free\nopencode-go/kimi-k3\nopencode-go/deepseek-v4.1-flash\n\n' ;;
esac
`

func fake(t *testing.T, mode string) (*OpenCode, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "opencode")
	if err := os.WriteFile(bin, []byte(fakeScript), 0o700); err != nil {
		t.Fatal(err)
	}
	logDir := filepath.Join(dir, "log")
	os.Mkdir(logDir, 0o700)
	t.Setenv("FAKE_LOG", logDir)
	t.Setenv("FAKE_MODE", mode)
	return New(WithCommand(bin)), logDir
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var chunk = ai.Chunk{StartMs: 0, EndMs: 60000, Candidates: []ai.Candidate{},
	Evidence: []ai.Evidence{{Ref: "chat:e1", TimeMs: 1000, Content: "비밀 채팅 문구"}}}

func TestReviewRunsIsolatedWithoutToolsAndCleansUp(t *testing.T) {
	o, logDir := fake(t, "ok")
	out, u, err := o.Review(context.Background(), "", DefaultModel, chunk)
	if err != nil || string(out) != `{"status":"ok","recommendations":[]}` {
		t.Fatalf("out %s err %v", out, err)
	}
	if u.InputTokens != 100 || u.CacheReadTokens != 900 || u.OutputTokens != 25 || u.CostUSD != 0.002 || u.TotalTokens != 1025 {
		t.Fatalf("usage %+v", u)
	}
	args := read(t, filepath.Join(logDir, "args"))
	for _, want := range []string{"run\n", "--standalone\n", "--format\njson\n", "--agent\n" + agentName + "\n", "-m\n" + DefaultModel + "\n"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q lack %q", args, want)
		}
	}
	if strings.Contains(args, "비밀") {
		t.Fatal("chat must go through stdin, not the command line")
	}
	stdin := read(t, filepath.Join(logDir, "stdin"))
	if !strings.Contains(stdin, string(ai.Encode(chunk))) || !strings.Contains(stdin, `"recommendations"`) {
		t.Fatalf("stdin %q", stdin)
	}
	var cfg struct {
		Permissions []map[string]string `json:"permissions"`
		Agents      map[string]struct {
			System      string              `json:"system"`
			Steps       int                 `json:"steps"`
			Permissions []map[string]string `json:"permissions"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(read(t, filepath.Join(logDir, "config.json"))), &cfg); err != nil {
		t.Fatal(err)
	}
	agent := cfg.Agents[agentName]
	denyAll := map[string]string{"action": "*", "resource": "*", "effect": "deny"}
	if len(cfg.Permissions) != 1 || len(agent.Permissions) != 1 || agent.Permissions[0]["effect"] != denyAll["effect"] ||
		agent.Permissions[0]["action"] != "*" || agent.Steps != 1 || agent.System != ai.Prompt {
		t.Fatalf("config %+v", cfg)
	}
	cwd := strings.TrimSpace(read(t, filepath.Join(logDir, "cwd")))
	if _, err := os.Stat(cwd); !os.IsNotExist(err) {
		t.Fatalf("the request folder %s was not removed", cwd)
	}
	if deleted := read(t, filepath.Join(logDir, "deleted")); !strings.Contains(deleted, "delete\n--standalone\nses_1") {
		t.Fatalf("session not deleted: %q", deleted)
	}
}

func TestReviewErrors(t *testing.T) {
	cases := []struct {
		mode, code string
		retryable  bool
	}{
		{"subscription", "AI_HTTP_403", false},
		{"rate", "AI_HTTP_429", true},
	}
	for _, c := range cases {
		o, logDir := fake(t, c.mode)
		_, _, err := o.Review(context.Background(), "", DefaultModel, chunk)
		var pe *ai.ProviderError
		if !errors.As(err, &pe) || pe.Code != c.code || pe.Retryable != c.retryable {
			t.Fatalf("%s: %v", c.mode, err)
		}
		if _, statErr := os.Stat(filepath.Join(logDir, "deleted")); statErr != nil {
			t.Fatalf("%s: the failed request's session was not deleted", c.mode)
		}
	}
	o, _ := fake(t, "subscription")
	_, _, err := o.Review(context.Background(), "", DefaultModel, chunk)
	if ue, _ := domain.AsUserError(err); ue == nil || !strings.Contains(ue.Message, "Go 구독") || !strings.Contains(ue.Message, "subscription is required") {
		t.Fatalf("subscription message: %v", err)
	}
	for mode, code := range map[string]string{"tool": "AI_TOOL_USE", "crash": "AI_OUTCOME_UNKNOWN"} {
		o, _ := fake(t, mode)
		if _, _, err := o.Review(context.Background(), "", DefaultModel, chunk); !isCode(err, code) {
			t.Fatalf("%s: %v", mode, err)
		}
	}
	if _, _, err := o.Review(context.Background(), "", "kimi-k3", chunk); !isCode(err, "INPUT") {
		t.Fatalf("model without the Go prefix: %v", err)
	}
}

// OpenCode's free models answer only OpenCode's own agent, so they are
// explained before a run and when OpenCode refuses one anyway.
func TestFreeTierModelsAreExplained(t *testing.T) {
	o, logDir := fake(t, "ok")
	for model, code := range map[string]string{
		"opencode/deepseek-v4.1-flash-free": "AI_FREE_TIER",
		"opencode/glm-5.3-flash-free":       "AI_FREE_TIER",
		"kimi-k3":                           "INPUT",
		"opencode-go/":                      "INPUT",
	} {
		if err := o.CheckModel(model); !isCode(err, code) {
			t.Fatalf("%s: %v", model, err)
		}
	}
	if err := o.CheckModel(DefaultModel); err != nil {
		t.Fatal(err)
	}
	if _, _, err := o.Review(context.Background(), "", "opencode/deepseek-v4.1-flash-free", chunk); !isCode(err, "AI_FREE_TIER") {
		t.Fatalf("free model: %v", err)
	}
	if _, err := os.Stat(filepath.Join(logDir, "args")); !os.IsNotExist(err) {
		t.Fatal("the CLI ran for a free model")
	}
	if !strings.Contains(o.AuthHelp(), "무료 모델") {
		t.Fatal("the sign-in help does not mention the free models")
	}

	o, _ = fake(t, "freetier")
	_, _, err := o.Review(context.Background(), "", DefaultModel, chunk)
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Code != "AI_HTTP_403" || pe.Retryable || !strings.Contains(pe.Message, "무료 모델") {
		t.Fatalf("free-tier refusal: %v", err)
	}
}

func TestModelsAndReadiness(t *testing.T) {
	o, _ := fake(t, "ok")
	models, err := o.Models(context.Background(), "")
	if err != nil || len(models) != 2 || models[0].ID != "opencode-go/kimi-k3" || models[0].Name != "kimi-k3" {
		t.Fatalf("models %v err %v", models, err)
	}
	if err := o.Ready(); err != nil {
		t.Fatal(err)
	}
	if err := New(WithCommand(filepath.Join(t.TempDir(), "missing"))).Ready(); !isCode(err, "AI_UNAVAILABLE") {
		t.Fatalf("missing CLI: %v", err)
	}
}

func TestExtractJSON(t *testing.T) {
	for in, want := range map[string]string{
		`{"a":1}`:                   `{"a":1}`,
		"```json\n{\"a\":1}\n```":   `{"a":1}`,
		"답입니다: {\"a\":{\"b\":2}} 끝": `{"a":{"b":2}}`,
		"JSON 없음":                   "",
	} {
		if got := string(extractJSON(in)); got != want {
			t.Fatalf("%q → %q, want %q", in, got, want)
		}
	}
}

func isCode(err error, code string) bool {
	ue, ok := domain.AsUserError(err)
	return ok && ue.Code == code
}
