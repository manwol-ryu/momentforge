//go:build e2efake

// Package e2efake has synthetic AI providers for end-to-end tests: they
// never leave the machine. The first supports every optional feature
// (prices, token counting, model list, batches) and recommends the first
// cited second of each chunk; the second sends one by one and recommends
// the last, so the two can be compared. Compiled only with the e2efake build
// tag (make e2e).
package e2efake

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Fake answers like a provider; Full enables the optional features.
type Fake struct {
	id, name string
	last     bool // recommend the last chat instead of the first

	mu      sync.Mutex
	sent    int                         // requests answered, for the simulated prompt cache
	batches map[string][]ai.BatchResult // batch ID → results
}

// New returns the full-featured fake ("E2E 가짜 AI").
func New() *Full {
	return &Full{Fake{id: "e2efake", name: "E2E 가짜 AI", batches: map[string][]ai.BatchResult{}}}
}

// NewSecond returns the plain fake ("E2E 가짜 AI 2").
func NewSecond() *Fake { return &Fake{id: "e2efake2", name: "E2E 가짜 AI 2", last: true} }

func (f *Fake) ID() string           { return f.id }
func (f *Fake) Name() string         { return f.name }
func (f *Fake) KeyEnv() string       { return "" }
func (f *Fake) DefaultModel() string { return "e2e-model" }
func (f *Fake) MaxOutputTokens() int { return ai.MaxOutputTokens }

func (f *Fake) Review(ctx context.Context, key, model string, c ai.Chunk) (json.RawMessage, ai.Usage, error) {
	recs := []any{}
	var pick *ai.Evidence
	for i := range c.Evidence {
		if strings.HasPrefix(c.Evidence[i].Ref, "chat:") {
			pick = &c.Evidence[i]
			if !f.last {
				break
			}
		}
	}
	if pick != nil {
		recs = append(recs, map[string]any{"source_candidate_ids": []string{}, "start_ms": pick.TimeMs,
			"end_ms": min(pick.TimeMs+10000, c.EndMs), "title": f.name + " 추천", "rationale": "채팅이 시작된 지점",
			"evidence_refs": []string{pick.Ref}, "uncertainty": "E2E용 가짜 응답입니다", "priority": "low"})
	}
	out, _ := json.Marshal(map[string]any{"status": "ok", "recommendations": recs})
	// The first request writes the simulated cache; later ones read it.
	f.mu.Lock()
	f.sent++
	first := f.sent == 1
	f.mu.Unlock()
	u := ai.Usage{InputTokens: 10, CacheReadTokens: 50, OutputTokens: 5}
	if first {
		u = ai.Usage{InputTokens: 10, CacheWriteTokens: 50, OutputTokens: 5}
	}
	u.TotalTokens = u.AllInput() + u.OutputTokens
	return out, u, nil
}

// Full adds the optional features to Fake.
type Full struct{ Fake }

func (f *Full) Price(model string) (ai.Price, bool) {
	return ai.Price{Input: 1, Output: 2, CacheRead: 0.1, CacheWrite: 1.25, BatchShare: 0.5, AsOf: "E2E"}, true
}

func (f *Full) CountTokens(ctx context.Context, key, model string, c ai.Chunk) (int, error) {
	return len(ai.Encode(c))/3 + 400, nil
}

func (f *Full) Models(ctx context.Context, key string) ([]ai.Model, error) {
	return []ai.Model{{ID: "e2e-model", Name: "E2E 모델"}, {ID: "e2e-model-small", Name: "E2E 작은 모델"}}, nil
}

func (f *Full) SubmitBatch(ctx context.Context, key, model string, reqs []ai.BatchRequest) (string, error) {
	var results []ai.BatchResult
	for _, r := range reqs {
		out, u, err := f.Review(ctx, key, model, r.Chunk)
		results = append(results, ai.BatchResult{ID: r.ID, Output: out, Usage: u, Err: err})
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("e2e-batch-%d", len(f.batches)+1)
	f.batches[id] = results
	return id, nil
}

func (f *Full) BatchStatus(ctx context.Context, key, id string) (ai.BatchStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.batches[id]
	if !ok {
		return ai.BatchStatus{}, domain.NewUserError("AI_HTTP_404", "배치를 찾지 못했습니다.")
	}
	return ai.BatchStatus{Ended: true, Succeeded: len(r)}, nil
}

func (f *Full) BatchResults(ctx context.Context, key, id string) ([]ai.BatchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.batches[id], nil
}

func (f *Full) CancelBatch(ctx context.Context, key, id string) error { return nil }

var (
	_ ai.Provider      = (*Fake)(nil)
	_ ai.Priced        = (*Full)(nil)
	_ ai.TokenCounter  = (*Full)(nil)
	_ ai.ModelLister   = (*Full)(nil)
	_ ai.BatchProvider = (*Full)(nil)
)
