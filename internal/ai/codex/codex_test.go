package codex

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

// fakeScript stands in for the Codex CLI: it logs what it was given and
// prints events like `codex exec --json` for FAKE_MODE.
const fakeScript = `#!/bin/sh
case "$1" in
login)
  if [ "$FAKE_MODE" = "loggedout" ]; then echo "Not logged in"; exit 1; fi
  echo "Logged in using ChatGPT" ;;
exec)
  printf '%s\n' "$@" > "$FAKE_LOG/args"
  cat > "$FAKE_LOG/stdin"
  pwd > "$FAKE_LOG/cwd"
  prev=""
  for a in "$@"; do
    if [ "$prev" = "--output-schema" ]; then cp "$a" "$FAKE_LOG/schema.json"; fi
    prev="$a"
  done
  printf '%s\n' '{"type":"thread.started","thread_id":"t1"}'
  printf '%s\n' '{"type":"turn.started"}'
  case "$FAKE_MODE" in
  ok)
    printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"생각"}}'
    printf '%s\n' '{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"{\"status\":\"ok\",\"recommendations\":[]}"}}'
    printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":1000,"cached_input_tokens":400,"cache_write_input_tokens":0,"output_tokens":90,"reasoning_output_tokens":40}}'
    ;;
  tool)
    printf '%s\n' '{"type":"item.completed","item":{"id":"item_0","type":"command_execution","command":"ls","status":"completed"}}'
    printf '%s\n' '{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"{\"status\":\"ok\",\"recommendations\":[]}"}}'
    printf '%s\n' '{"type":"turn.completed","usage":{"input_tokens":10,"cached_input_tokens":0,"output_tokens":5}}'
    ;;
  limit)
    printf '%s\n' '{"type":"turn.failed","error":{"message":"You have hit your usage limit."}}'
    exit 1 ;;
  esac ;;
esac
`

func fake(t *testing.T, mode string) (*Codex, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake CLI is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "codex")
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

func isCode(err error, code string) bool {
	ue, ok := domain.AsUserError(err)
	return ok && ue.Code == code
}

var chunk = ai.Chunk{StartMs: 0, EndMs: 60000, Candidates: []ai.Candidate{},
	Evidence: []ai.Evidence{{Ref: "chat:e1", TimeMs: 1000, Content: "비밀 채팅 문구"}}}

func TestReviewRunsWithoutToolsOrHistory(t *testing.T) {
	c, logDir := fake(t, "ok")
	out, u, err := c.Review(context.Background(), "", DefaultModel, chunk)
	if err != nil || string(out) != `{"status":"ok","recommendations":[]}` {
		t.Fatalf("out %s err %v", out, err)
	}
	if u.InputTokens != 600 || u.CacheReadTokens != 400 || u.OutputTokens != 90 || u.TotalTokens != 1090 {
		t.Fatalf("usage %+v", u)
	}
	args := read(t, filepath.Join(logDir, "args"))
	for _, want := range []string{"exec\n", "--ephemeral\n", "--ignore-user-config\n", "--ignore-rules\n", "--sandbox\nread-only\n",
		"--json\n", "--output-schema\n", "web_search=\"disabled\"\n", "--disable\nshell_tool\n", "--disable\ncomputer_use\n",
		"--disable\nbrowser_use\n", "--disable\nplugins\n", "--disable\napps\n"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %q lack %q", args, want)
		}
	}
	if strings.Contains(args, "-m\n") {
		t.Fatal("the default model leaves the choice to Codex")
	}
	if strings.Contains(args, "비밀") {
		t.Fatal("chat must go through stdin, not the command line")
	}
	stdin := read(t, filepath.Join(logDir, "stdin"))
	if !strings.HasPrefix(stdin, "이 작업에는 도구를 쓰지 않습니다") || !strings.Contains(stdin, ai.Prompt) || !strings.Contains(stdin, string(ai.Encode(chunk))) {
		t.Fatalf("stdin %q", stdin)
	}
	var schema map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(logDir, "schema.json"))), &schema); err != nil || schema["type"] != "object" {
		t.Fatalf("schema %v %v", schema, err)
	}
	cwd := strings.TrimSpace(read(t, filepath.Join(logDir, "cwd")))
	if _, err := os.Stat(cwd); !os.IsNotExist(err) {
		t.Fatalf("the request folder %s was not removed", cwd)
	}

	if _, _, err := c.Review(context.Background(), "", "some-model", chunk); err != nil {
		t.Fatal(err)
	}
	if args := read(t, filepath.Join(logDir, "args")); !strings.Contains(args, "-m\nsome-model\n") {
		t.Fatalf("a chosen model is passed: %q", args)
	}
}

func TestReviewErrors(t *testing.T) {
	c, _ := fake(t, "tool")
	if _, _, err := c.Review(context.Background(), "", DefaultModel, chunk); !isCode(err, "AI_TOOL_USE") {
		t.Fatalf("tool: %v", err)
	}
	c, _ = fake(t, "limit")
	_, _, err := c.Review(context.Background(), "", DefaultModel, chunk)
	var pe *ai.ProviderError
	if !errors.As(err, &pe) || pe.Retryable || !strings.Contains(pe.Message, "사용 한도") {
		t.Fatalf("limit: %v", err)
	}
	if _, _, err := c.Review(context.Background(), "", "two words", chunk); !isCode(err, "INPUT") {
		t.Fatalf("bad model: %v", err)
	}
}

func TestReadiness(t *testing.T) {
	c, _ := fake(t, "ok")
	if err := c.Ready(); err != nil {
		t.Fatal(err)
	}
	c, _ = fake(t, "loggedout")
	if err := c.Ready(); !isCode(err, "AI_UNAVAILABLE") || !strings.Contains(err.Error(), "codex login") {
		t.Fatalf("logged out: %v", err)
	}
	if err := New(WithCommand(filepath.Join(t.TempDir(), "missing"))).Ready(); !isCode(err, "AI_UNAVAILABLE") {
		t.Fatalf("missing CLI: %v", err)
	}
	if !strings.Contains(New().AuthHelp(), "codex login") {
		t.Fatal("auth help")
	}
}
