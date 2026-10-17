package ai

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// chats makes one synthetic message every second in [from, to).
func chats(text string) func(from, to int64) ([]domain.ChatEvent, error) {
	return func(from, to int64) ([]domain.ChatEvent, error) {
		var out []domain.ChatEvent
		for t := (from + 999) / 1000 * 1000; t < to; t += 1000 {
			out = append(out, domain.ChatEvent{ID: fmt.Sprintf("e%d", t), OffsetMs: t, AuthorKey: "a1", Text: text})
		}
		return out, nil
	}
}

func TestPrepareSplitsByWindowAndSize(t *testing.T) {
	p, err := Prepare(PlanInput{SessionID: "s", Provider: "openai", Model: "m",
		Ranges: []Range{{0, 300000}, {200000, 900000}}, Chats: chats("ㅋㅋㅋ 웃기다")})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Ranges) != 1 || p.Ranges[0] != (Range{0, 900000}) {
		t.Fatalf("ranges %v (overlaps merge)", p.Ranges)
	}
	for _, c := range p.Chunks {
		if n := len(Encode(c)); n > maxChunkBytes {
			t.Fatalf("chunk of %d bytes", n)
		}
		if c.StartMs < 0 || c.EndMs > 900000 {
			t.Fatalf("chunk %d–%d outside the range", c.StartMs, c.EndMs)
		}
	}
	if p.RequestCount != len(p.Chunks) || p.RequestCount < 2 || p.Fingerprint == "" || p.PlannedInputEstimate == 0 {
		t.Fatalf("plan: %d requests, fingerprint %q", p.RequestCount, p.Fingerprint)
	}
	q, _ := Prepare(PlanInput{SessionID: "s", Provider: "openai", Model: "m", Ranges: []Range{{0, 900000}}, Chats: chats("ㅋㅋㅋ 웃기다")})
	if q.Fingerprint != p.Fingerprint {
		t.Fatal("the same input must give the same fingerprint")
	}
}

func TestPrepareBudgetsAndEmpty(t *testing.T) {
	p, err := Prepare(PlanInput{Model: "m", Ranges: []Range{{0, 4 * 3600000}}, Chats: chats(strings.Repeat("가", 200))})
	if err != nil {
		t.Fatal(err)
	}
	budget := 0
	for _, o := range p.Omitted {
		if o.Reason == "request_budget" {
			budget++
		}
	}
	if p.RequestCount != MaxRequests || budget == 0 {
		t.Fatalf("requests %d, budget omissions %d", p.RequestCount, budget)
	}
	huge, _ := Prepare(PlanInput{Model: "m", Ranges: []Range{{0, 3000}}, Chats: chats(strings.Repeat("가", 4000))})
	if len(huge.Omitted) == 0 || huge.Omitted[0].Reason != "message_too_large" {
		t.Fatalf("omitted %v", huge.Omitted)
	}
	_, err = Prepare(PlanInput{Model: "m", Ranges: []Range{{0, 1000}}, Chats: func(int64, int64) ([]domain.ChatEvent, error) { return nil, nil }})
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "NO_INPUT" {
		t.Fatalf("empty: %v", err)
	}
}

func TestPrepareAddsSelectedCandidatesAndComments(t *testing.T) {
	cands := []Candidate{{ID: "rule:m0001", SourceType: "rule", AnchorMs: 60000, Title: "웃음 증가"},
		{ID: "viewer_comment:r1", SourceType: "viewer_comment", AnchorMs: 90000, Title: "시청자"}}
	p, err := Prepare(PlanInput{Model: "m", Ranges: []Range{{30000, 120000}}, Candidates: cands,
		Comments: map[string]Evidence{"viewer_comment:r1": {Ref: "comment:r1", TimeMs: 90000, Content: "1:30 웃김"}},
		Chats:    chats("ㅋㅋ")})
	if err != nil {
		t.Fatal(err)
	}
	c := p.Chunks[0]
	if len(c.Candidates) != 2 || c.Evidence[0].Ref != "comment:r1" || c.Evidence[0].Author != nil {
		t.Fatalf("chunk %+v", c)
	}
}

func TestValidate(t *testing.T) {
	chunk := Chunk{StartMs: 0, EndMs: 60000, Candidates: []Candidate{{ID: "rule:m1"}},
		Evidence: []Evidence{{Ref: "chat:e1", TimeMs: 10000}, {Ref: "chat:e2", TimeMs: 50000}}}
	rec := func(edit func(m map[string]any)) map[string]any {
		m := map[string]any{"source_candidate_ids": []string{"rule:m1"}, "start_ms": 5000, "end_ms": 20000, "title": "웃음",
			"rationale": "근거", "evidence_refs": []string{"chat:e1"}, "uncertainty": "영상 미확인", "priority": "high"}
		if edit != nil {
			edit(m)
		}
		return m
	}
	answer := func(status string, recs ...map[string]any) json.RawMessage {
		b, _ := json.Marshal(map[string]any{"status": status, "recommendations": recs})
		return b
	}
	got, rejected, err := Validate(answer("ok",
		rec(nil),
		rec(func(m map[string]any) { m["evidence_refs"] = []string{"chat:e2"} }),       // cited message outside the span
		rec(func(m map[string]any) { m["evidence_refs"] = []string{"chat:nope"} }),     // made-up evidence
		rec(func(m map[string]any) { m["end_ms"] = 70000 }),                            // outside the chunk
		rec(func(m map[string]any) { m["source_candidate_ids"] = []string{"rule:x"} }), // unknown candidate
		rec(func(m map[string]any) { m["extra"] = 1 }),                                 // extra field
		rec(func(m map[string]any) { delete(m, "uncertainty") }),                       // missing field
		rec(func(m map[string]any) { m["priority"] = "urgent" }),                       // bad priority
		rec(func(m map[string]any) { m["evidence_refs"] = nil }),                       // null list
		rec(func(m map[string]any) { m["title"] = "" }),                                // empty text
	), chunk)
	if err != nil || len(got) != 1 || rejected != 9 || got[0].ID == "" || got[0].StartMs != 5000 {
		t.Fatalf("accepted %v rejected %d err %v", got, rejected, err)
	}
	if got, rejected, _ := Validate(answer("insufficient_evidence", rec(nil)), chunk); len(got) != 0 || rejected != 1 {
		t.Fatal("recommendations with insufficient_evidence are not used")
	}
	for _, bad := range []string{`not json`, `{"status":"maybe","recommendations":[]}`, `{"status":"ok"}`} {
		if _, _, err := Validate(json.RawMessage(bad), chunk); err == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
}

func TestPrepareBatchBudgetAndCostCap(t *testing.T) {
	long := []Range{{0, 4 * 3600000}}
	batch, err := Prepare(PlanInput{Model: "m", Delivery: DeliveryBatch, Ranges: long, Chats: chats(strings.Repeat("가", 200))})
	if err != nil || batch.Delivery != DeliveryBatch || batch.RequestCount <= MaxRequests || batch.CostUpperUSD != 0 {
		t.Fatalf("batch plan: %d requests, cost %v, err %v", batch.RequestCount, batch.CostUpperUSD, err)
	}
	price := &Price{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5, BatchShare: 0.5}
	full, _ := Prepare(PlanInput{Model: "m", Ranges: long, Price: price, MaxOutputTokens: 8000, Chats: chats("ㅋㅋ")})
	perRequest := price.UpperCost(ChunkInputEstimate(full.Chunks[0]), 8000, false)
	if full.CostUpperUSD < perRequest*float64(full.RequestCount)*0.9 || full.OutputTokenLimit != full.RequestCount*8000 {
		t.Fatalf("cost %v for %d requests (one ≈ %v)", full.CostUpperUSD, full.RequestCount, perRequest)
	}
	capped, err := Prepare(PlanInput{Model: "m", Ranges: long, Price: price, MaxOutputTokens: 8000, CostCapUSD: perRequest * 3.5, Chats: chats("ㅋㅋ")})
	if err != nil || capped.RequestCount != 3 || capped.CostUpperUSD > capped.CostCapUSD {
		t.Fatalf("capped plan: %d requests, cost %v, err %v", capped.RequestCount, capped.CostUpperUSD, err)
	}
	if o := capped.Omitted[len(capped.Omitted)-1]; o.Reason != "cost_cap" {
		t.Fatalf("omitted %v", capped.Omitted)
	}
	halfPrice, _ := Prepare(PlanInput{Model: "m", Delivery: DeliveryBatch, Ranges: long, Price: price, MaxOutputTokens: 8000,
		CostCapUSD: perRequest * 3.5, Chats: chats("ㅋㅋ")})
	if halfPrice.RequestCount != 7 {
		t.Fatalf("a batch costs half: %d requests", halfPrice.RequestCount)
	}
	if _, err := Prepare(PlanInput{Model: "m", Ranges: long, Price: price, CostCapUSD: 0.000001, Chats: chats("ㅋㅋ")}); err == nil {
		t.Fatal("a cap below one request must fail")
	}
	if _, err := Prepare(PlanInput{Model: "m", Ranges: long, CostCapUSD: 1, Chats: chats("ㅋㅋ")}); err == nil {
		t.Fatal("a cap needs a known price")
	}
}

func TestPriceCost(t *testing.T) {
	p := Price{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5, BatchShare: 0.5}
	u := Usage{InputTokens: 1_000_000, CacheReadTokens: 1_000_000, CacheWriteTokens: 1_000_000, OutputTokens: 1_000_000}
	if got := p.Cost(u, false); got != 4+0.2+5+20 {
		t.Fatalf("cost %v", got)
	}
	if got := p.Cost(u, true); got != (4+0.2+5+20)/2 {
		t.Fatalf("batch cost %v", got)
	}
	if got := p.UpperCost(1_000_000, 0, false); got != 5 {
		t.Fatalf("upper input cost %v (cache write price)", got)
	}
}

// Claude caches a prompt prefix only from 512 tokens; the fixed instruction
// alone must stay above that so repeated requests hit the cache. A token is
// at most a few Korean characters, so 1,200 characters is a safe floor.
func TestPromptIsCacheable(t *testing.T) {
	if n := len([]rune(Prompt)); n < 1200 {
		t.Fatalf("prompt has %d characters", n)
	}
	for _, varying := range []string{"%", "{{", "time.Now"} {
		if strings.Contains(Prompt, varying) {
			t.Fatalf("prompt contains %q: it must not vary between requests", varying)
		}
	}
}
