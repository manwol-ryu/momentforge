//go:build e2efake

package main

import (
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai/cliproxy"
	"github.com/manwol-ryu/momentforge/internal/ai/codex"
	aifake "github.com/manwol-ryu/momentforge/internal/ai/e2efake"
	"github.com/manwol-ryu/momentforge/internal/ai/opencode"
	"github.com/manwol-ryu/momentforge/internal/ai/opencodex"
	"github.com/manwol-ryu/momentforge/internal/platform/e2efake"
)

// The E2E binary (make e2e) also serves a synthetic LIVE platform and AI provider.
func init() {
	extraPlatforms = append(extraPlatforms, e2efake.New())
	extraAI = append(extraAI, aifake.New(), aifake.NewSecond())
	extraBatchPoll = 200 * time.Millisecond // the fake batch ends at once
	opencodeOptions = append(opencodeOptions, opencode.WithCommand("/nonexistent/momentforge-e2e/opencode"))
	codexOptions = append(codexOptions, codex.WithCommand("/nonexistent/momentforge-e2e/codex"))
	cliproxyOptions = append(cliproxyOptions, cliproxy.WithHome("/nonexistent/momentforge-e2e/cli-proxy-api"),
		cliproxy.WithProgram("/nonexistent/momentforge-e2e/cli-proxy-api"), cliproxy.WithFallbackEndpoint("http://127.0.0.1:1/v1"))
	opencodexOptions = append(opencodexOptions, opencodex.WithHome("/nonexistent/momentforge-e2e/opencodex"),
		opencodex.WithProgram("/nonexistent/momentforge-e2e/ocx"), opencodex.WithFallbackEndpoint("http://127.0.0.1:1/v1"))
}
