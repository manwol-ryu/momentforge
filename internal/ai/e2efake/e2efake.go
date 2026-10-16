//go:build e2efake

// Package e2efake is a synthetic AI provider for end-to-end tests: it never
// leaves the machine and recommends the first cited second of each chunk.
// Compiled only with the e2efake build tag (make e2e).
package e2efake

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/ai"
)

type Fake struct{}

func New() Fake { return Fake{} }

func (Fake) ID() string     { return "e2efake" }
func (Fake) Name() string   { return "E2E 가짜 AI" }
func (Fake) KeyEnv() string { return "" }

func (Fake) Review(ctx context.Context, key, model string, c ai.Chunk) (json.RawMessage, ai.Usage, error) {
	recs := []any{}
	for _, e := range c.Evidence {
		if strings.HasPrefix(e.Ref, "chat:") {
			recs = append(recs, map[string]any{"source_candidate_ids": []string{}, "start_ms": e.TimeMs,
				"end_ms": min(e.TimeMs+10000, c.EndMs), "title": "가짜 AI 추천", "rationale": "채팅이 시작된 지점",
				"evidence_refs": []string{e.Ref}, "uncertainty": "E2E용 가짜 응답입니다", "priority": "low"})
			break
		}
	}
	out, _ := json.Marshal(map[string]any{"status": "ok", "recommendations": recs})
	return out, ai.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, nil
}
