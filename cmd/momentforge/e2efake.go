//go:build e2efake

package main

import (
	aifake "github.com/manwol-ryu/momentforge/internal/ai/e2efake"
	"github.com/manwol-ryu/momentforge/internal/platform/e2efake"
)

// The E2E binary (make e2e) also serves a synthetic LIVE platform and AI provider.
func init() {
	extraPlatforms = append(extraPlatforms, e2efake.New())
	extraAI = append(extraAI, aifake.New())
}
