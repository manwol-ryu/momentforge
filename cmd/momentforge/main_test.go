package main

import (
	"os"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai/cliproxy"
	"github.com/manwol-ryu/momentforge/internal/ai/codex"
	"github.com/manwol-ryu/momentforge/internal/ai/opencode"
)

// Tests never use the AI programs on this computer (Codex, OpenCode,
// CLIProxyAPI) or read their config, so the results do not depend on what
// is installed or signed in here.
func TestMain(m *testing.M) {
	const missing = "/nonexistent/momentforge-test"
	opencodeOptions = append(opencodeOptions, opencode.WithCommand(missing+"/opencode"))
	codexOptions = append(codexOptions, codex.WithCommand(missing+"/codex"))
	cliproxyOptions = append(cliproxyOptions, cliproxy.WithHome(missing+"/cli-proxy-api"), cliproxy.WithProgram(missing+"/cli-proxy-api"),
		cliproxy.WithFallbackEndpoint("http://127.0.0.1:1/v1"))
	os.Exit(m.Run())
}
