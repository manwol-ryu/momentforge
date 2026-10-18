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
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
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
	// The full fake explains the chunk's first candidate (the screen shows
	// that under the candidate) and says nothing about chat without one; the
	// plain one adds a moment of its own to every chunk.
	sources := []string{}
	if !f.last {
		if len(c.Candidates) == 0 {
			pick = nil
		} else {
			sources = []string{c.Candidates[0].ID}
		}
	}
	if pick != nil {
		recs = append(recs, map[string]any{"source_candidate_ids": sources, "start_ms": pick.TimeMs,
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

// Step answers the cut analysis deterministically. The full fake reads the
// window, names the first utterance as the cause, keeps the earliest
// "약속" utterance as required context with its own setup span, and passes
// both checks. The plain fake chooses a span that leaves its required
// context out, so the plan fails the structural check twice and is saved
// as verify_failed with the reasons.
func (f *Fake) Step(ctx context.Context, key, model string, step ai.StepRequest) (json.RawMessage, ai.Usage, error) {
	f.mu.Lock()
	f.sent++
	first := f.sent == 1
	f.mu.Unlock()
	u := ai.Usage{InputTokens: 10, CacheReadTokens: 50, OutputTokens: 5}
	if first {
		u = ai.Usage{InputTokens: 10, CacheWriteTokens: 50, OutputTokens: 5}
	}
	u.TotalTokens = u.AllInput() + u.OutputTokens
	var out any
	switch step.Name {
	case "analyze":
		in := step.Input.(cut.Input)
		utt, chat := firstOf(in.Evidence, cut.KindUtterance), firstOf(in.Evidence, cut.KindChat)
		support := refsOf(utt, chat)
		out = map[string]any{
			"target": f.name + "가 짐작한 장면: 방송자의 말에 시청자가 웃음으로 반응",
			"causes": []any{
				map[string]any{"kind": "broadcast_event", "summary": "첫 발언 뒤 채팅이 몰림", "support": support,
					"against": []string{}, "direction": "broadcast_to_viewers"},
				map[string]any{"kind": "viewer_joke", "summary": "채팅끼리의 농담일 가능성", "support": refsOf(chat),
					"against": refsOf(utt), "direction": "unknown"},
			},
			"cause_status": "likely", "questions": []string{"'아까'가 무엇을 가리키는가"},
			"search_requests": []any{map[string]any{"tool": "search", "query": "벌칙", "in": "both", "before_ms": in.WindowStartMs,
				"at_ms": 0, "from_ms": 0, "to_ms": 0, "window_s": 0}},
		}
	case "plan":
		in := step.Input.(map[string]any)["input"].(cut.Input)
		utts := ofKind(in.Evidence, cut.KindUtterance)
		if len(utts) == 0 {
			utts = ofKind(in.Evidence, cut.KindChat)
		}
		promise := promiseOf(in.Evidence)
		mainStart, mainEnd := utts[0], utts[len(utts)-1]
		for _, e := range utts {
			if e.StartMs >= in.Candidates[0].ClipStartMs && (mainStart.StartMs < in.Candidates[0].ClipStartMs || e.StartMs < mainStart.StartMs) {
				mainStart = e
			}
			if e.StartMs <= in.Candidates[0].ClipEndMs && e.StartMs > mainEnd.StartMs {
				mainEnd = e
			}
		}
		spans := []any{map[string]any{"start_ref": mainStart.Ref, "end_ref": mainEnd.Ref, "role": "main"}}
		required := []string{mainStart.Ref}
		origin := ""
		if promise != nil {
			required = append([]string{promise.Ref}, required...)
			origin = promise.Ref
			if !f.last {
				spans = append([]any{map[string]any{"start_ref": promise.Ref, "end_ref": promise.Ref, "role": "setup"}}, spans...)
			}
		}
		analysis := append([]string{}, required...)
		for _, e := range in.Evidence {
			analysis = append(analysis, e.Ref)
		}
		out = map[string]any{
			"story_origin_ref": origin, "origin_confidence": map[bool]string{true: "found_in_search_range", false: "not_found"}[origin != ""],
			"event_start_ref": mainStart.Ref, "analysis_evidence_refs": analysis, "required_context_refs": required,
			"optional_context_refs": []string{},
			"boundary_candidates": []any{
				map[string]any{"name": "short", "spans": spans[len(spans)-1:]},
				map[string]any{"name": "context", "spans": spans},
				map[string]any{"name": "full_event", "spans": spans},
			},
			"chosen": "context", "start_reason": "약속이 처음 나온 발언부터", "end_reason": "반응이 끝나는 마지막 발언까지",
			"ending":               map[string]any{"state": "confirmed", "event_kind": "promise_penalty", "checks": []string{"결과 확인됨"}},
			"unresolved_questions": []string{},
		}
	case "verify_clip":
		out = map[string]any{"pass": true, "answers": map[string]string{"what": "벌칙 약속 뒤의 도전", "condition": "지면 벌칙",
			"result": "실패", "references": "'아까'는 setup의 약속", "why_reaction": "약속과 결과의 대비"}, "gaps": []string{}}
	case "verify_source":
		out = map[string]any{"pass": true, "mixed_attempt": false, "cut_condition": false, "missed_correction": false,
			"ended_early": false, "notes": []string{}}
	default:
		return f.Review(ctx, key, model, step.Input.(ai.Chunk))
	}
	b, _ := json.Marshal(out)
	return b, u, nil
}

func firstOf(ev []cut.Evidence, kind string) *cut.Evidence {
	for i := range ev {
		if ev[i].Kind == kind {
			return &ev[i]
		}
	}
	return nil
}

func ofKind(ev []cut.Evidence, kind string) []cut.Evidence {
	var out []cut.Evidence
	for _, e := range ev {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// promiseOf is the earliest utterance that mentions a 벌칙 promise (the
// sample's planted case), nil when the window has none.
func promiseOf(ev []cut.Evidence) *cut.Evidence {
	for i := range ev {
		if ev[i].Kind == cut.KindUtterance && strings.Contains(ev[i].Text, "벌칙") {
			return &ev[i]
		}
	}
	return nil
}

func refsOf(es ...*cut.Evidence) []string {
	out := []string{}
	for _, e := range es {
		if e != nil {
			out = append(out, e.Ref)
		}
	}
	return out
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
