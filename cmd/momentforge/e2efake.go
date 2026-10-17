//go:build e2efake

package main

import (
	"time"

	aifake "github.com/manwol-ryu/momentforge/internal/ai/e2efake"
	"github.com/manwol-ryu/momentforge/internal/platform/e2efake"
)

// The E2E binary (make e2e) also serves a synthetic LIVE platform and AI provider.
func init() {
	extraPlatforms = append(extraPlatforms, e2efake.New())
	extraAI = append(extraAI, aifake.New(), aifake.NewSecond())
	extraBatchPoll = 200 * time.Millisecond // the fake batch ends at once
}
