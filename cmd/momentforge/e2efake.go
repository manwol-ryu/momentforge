//go:build e2efake

package main

import "github.com/manwol-ryu/momentforge/internal/platform/e2efake"

// The E2E binary (make e2e) also serves the synthetic LIVE platform.
func init() { extraPlatforms = append(extraPlatforms, e2efake.New()) }
