package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// fakeAI recommends the first second of each chunk, citing its first chat.
type fakeAI struct {
	mu       sync.Mutex
	calls    int
	failOnce error         // returned by the first call
	block    chan struct{} // when set, Review waits on it (to test cancel)
	keys     []string
}

func (f *fakeAI) ID() string           { return "fakeai" }
func (f *fakeAI) Name() string         { return "가짜 AI" }
func (f *fakeAI) KeyEnv() string       { return "" }
func (f *fakeAI) DefaultModel() string { return "m" }
func (f *fakeAI) MaxOutputTokens() int { return ai.MaxOutputTokens }
func (f *fakeAI) Review(ctx context.Context, key, model string, c ai.Chunk) (json.RawMessage, ai.Usage, error) {
	f.mu.Lock()
	f.calls++
	f.keys = append(f.keys, key)
	first, block := f.calls == 1, f.block
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return nil, ai.Usage{}, ctx.Err()
		}
	}
	if first && f.failOnce != nil {
		return nil, ai.Usage{}, f.failOnce
	}
	var ref ai.Evidence
	for _, e := range c.Evidence {
		if len(e.Ref) > 5 && e.Ref[:5] == "chat:" {
			ref = e
			break
		}
	}
	out, _ := json.Marshal(map[string]any{"status": "ok", "recommendations": []any{map[string]any{
		"source_candidate_ids": []string{}, "start_ms": ref.TimeMs, "end_ms": ref.TimeMs + 1000, "title": "웃음 반응",
		"rationale": "같은 시각 채팅이 몰림", "evidence_refs": []string{ref.Ref}, "uncertainty": "영상은 확인하지 않음", "priority": "medium"}}})
	return out, ai.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120}, nil
}

func newAIApp(t *testing.T, f *fakeAI) *App {
	t.Helper()
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(f)
	a.initAI()
	return a
}

func waitAI(t *testing.T, a *App, id string) AIRunView {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		v, err := a.AIRun(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if v.State != store.AIRunning {
			return v
		}
	}
	t.Fatal("AI run did not finish")
	return AIRunView{}
}

func TestAIPreviewConfirmRunAndTimeline(t *testing.T) {
	f := &fakeAI{failOnce: &ai.ProviderError{UserError: domain.NewUserError("AI_HTTP_429", "느림"), Retryable: true}}
	a := newAIApp(t, f)
	ctx := context.Background()
	demo, err := a.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	tl, _ := a.Timeline(ctx, demo.ID)
	if len(tl.Items) == 0 || tl.Items[0].Source != SourceRule { // the test app has only the laugh rule
		t.Fatalf("timeline %+v", tl.Items)
	}
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m-1", ItemIDs: []string{tl.Items[0].ID}})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Plan.RequestCount == 0 || pv.Plan.Chunks[0].Candidates[0].Statistics == nil || f.calls != 0 {
		t.Fatalf("preview %+v (nothing is sent before the user confirms)", pv.Plan.Chunks)
	}
	if _, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false); !isCode(err, "CREDENTIALS") {
		t.Fatalf("no key: %v", err)
	}
	if _, err := a.StartAI(ctx, pv.ID, "other", false); !isCode(err, "PREVIEW") {
		t.Fatalf("changed plan: %v", err)
	}
	if ok, _ := a.SetAIKey("fakeai", " key-1 "); !ok || !a.AIProviders()[0].Configured {
		t.Fatal("key not kept")
	}
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, id)
	if v.State != store.AICompleted || v.Recommendations == 0 || v.Attempts != v.RequestCount+1 || v.InputTokens == 0 ||
		f.keys[0] != "key-1" || v.CostUSD != nil {
		t.Fatalf("run %+v (one retry after the rate limit)", v)
	}
	tl, _ = a.Timeline(ctx, demo.ID)
	aiItems := 0
	for _, it := range tl.Items {
		if it.Source == SourceAI {
			aiItems++
		}
	}
	if aiItems != v.Recommendations || tl.AIRunID != id {
		t.Fatalf("timeline has %d AI items for run %s", aiItems, tl.AIRunID)
	}
	if _, err := os.Stat(filepath.Join(a.exportDir(demo.ID), "ai", id, "recommendations.txt")); err != nil {
		t.Fatal(err)
	}
	// The same plan is not sent twice unless asked.
	pv2, _ := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m-1", ItemIDs: []string{tl.Items[0].ID}})
	calls := f.calls
	if again, _ := a.StartAI(ctx, pv2.ID, pv2.Plan.Fingerprint, false); again != id || f.calls != calls {
		t.Fatalf("reused %s, calls %d→%d", again, calls, f.calls)
	}

	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m-1", StartMs: 5, EndMs: 1}); !isCode(err, "INPUT") {
		t.Fatalf("bad range: %v", err)
	}
	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "", StartMs: 0, EndMs: 1000}); !isCode(err, "INPUT") {
		t.Fatalf("no model: %v", err)
	}
	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", ItemIDs: []string{"rule:none"}}); !isCode(err, "INPUT") {
		t.Fatalf("unknown item: %v", err)
	}
}

func TestAICancelAndRecovery(t *testing.T) {
	f := &fakeAI{block: make(chan struct{})}
	a := newAIApp(t, f)
	ctx := context.Background()
	demo, _ := a.CreateDemo(ctx, false)
	a.SetAIKey("fakeai", "k")
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", StartMs: 0, EndMs: 600000})
	if err != nil {
		t.Fatal(err)
	}
	id, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	pv2, _ := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m2", StartMs: 0, EndMs: 600000})
	if _, err := a.StartAI(ctx, pv2.ID, pv2.Plan.Fingerprint, false); !isCode(err, "BUSY") {
		t.Fatalf("second run: %v", err)
	}
	a.CancelAI(id)
	if v := waitAI(t, a, id); v.State != store.AICancelled {
		t.Fatalf("cancelled run %+v", v)
	}

	run, _ := a.cfg.Store.CreateAIRun(ctx, store.AIRun{SessionID: demo.ID, Provider: "fakeai", Model: "m", Fingerprint: "f", PlanJSON: []byte(`{}`)})
	if err := a.RecoverAI(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.AIRun(ctx, run.ID); v.State != store.AIPartial || v.ErrorCode != "PROCESS_RESTART" {
		t.Fatalf("after restart %+v", v)
	}
}

// pricedAI adds prices, token counting and batches to fakeAI.
type pricedAI struct {
	*fakeAI
	usage     ai.Usage // returned by every review
	counts    int
	submitted [][]ai.BatchRequest
	ended     bool // batches report ended
	cancelled int
}

var testPrice = ai.Price{Input: 4, Output: 20, CacheRead: 0.2, CacheWrite: 5, BatchShare: 0.5}

func (p *pricedAI) ID() string                    { return "priced" }
func (p *pricedAI) Price(string) (ai.Price, bool) { return testPrice, true }
func (p *pricedAI) Review(ctx context.Context, key, model string, c ai.Chunk) (json.RawMessage, ai.Usage, error) {
	out, _, err := p.fakeAI.Review(ctx, key, model, c)
	return out, p.usage, err
}
func (p *pricedAI) CountTokens(ctx context.Context, key, model string, c ai.Chunk) (int, error) {
	p.mu.Lock()
	p.counts++
	p.mu.Unlock()
	return 1000, nil
}
func (p *pricedAI) SubmitBatch(ctx context.Context, key, model string, reqs []ai.BatchRequest) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.submitted = append(p.submitted, reqs)
	return fmt.Sprintf("batch-%d", len(p.submitted)), nil
}
func (p *pricedAI) BatchStatus(ctx context.Context, key, id string) (ai.BatchStatus, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ai.BatchStatus{Ended: p.ended}, nil
}
func (p *pricedAI) BatchResults(ctx context.Context, key, id string) ([]ai.BatchResult, error) {
	p.mu.Lock()
	var n int
	fmt.Sscanf(id, "batch-%d", &n)
	reqs := p.submitted[n-1]
	p.mu.Unlock()
	var out []ai.BatchResult
	for i := len(reqs) - 1; i >= 0; i-- { // results come in any order
		o, u, err := p.Review(ctx, key, "m", reqs[i].Chunk)
		out = append(out, ai.BatchResult{ID: reqs[i].ID, Output: o, Usage: u, Err: err})
	}
	return out, nil
}
func (p *pricedAI) CancelBatch(ctx context.Context, key, id string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelled++
	p.ended = true
	return nil
}

func newPricedApp(t *testing.T) (*App, *pricedAI, *fakeAI) {
	t.Helper()
	single := &fakeAI{}
	p := &pricedAI{fakeAI: &fakeAI{}, usage: ai.Usage{InputTokens: 100, CacheReadTokens: 900, OutputTokens: 50}, ended: true}
	a, _ := newTestApp(t, &fakeVOD{duration: 1000, pages: 1})
	a.cfg.AI = &ai.Registry{}
	a.cfg.AI.Register(p, single)
	a.initAI()
	a.ai.timing = aiTiming{batchPoll: 5 * time.Millisecond, batchPollMax: 20 * time.Millisecond}
	return a, p, single
}

func TestAIPreviewPricesCountsAndCaps(t *testing.T) {
	a, p, _ := newPricedApp(t)
	ctx := context.Background()
	demo, _ := a.CreateDemo(ctx, false)
	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "priced", Model: "m", All: true}); !isCode(err, "INPUT") {
		t.Fatalf("automatic selection without a cap: %v", err)
	}
	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", StartMs: 0, EndMs: 600000, CostCapUSD: 1}); !isCode(err, "INPUT") {
		t.Fatalf("cap without a price: %v", err)
	}
	if _, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", StartMs: 0, EndMs: 600000, Delivery: "batch"}); !isCode(err, "INPUT") {
		t.Fatalf("batch without batch support: %v", err)
	}
	pv, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "priced", Model: "m", All: true, CostCapUSD: 5})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Plan.Price == nil || pv.Plan.CostUpperUSD <= 0 || pv.Plan.CostUpperUSD > 5 || len(pv.Plan.Chunks[0].Candidates) == 0 {
		t.Fatalf("plan cost %v of cap %v", pv.Plan.CostUpperUSD, pv.Plan.CostCapUSD)
	}
	if _, err := a.CountAIPreview(ctx, pv.ID, pv.Plan.Fingerprint); !isCode(err, "CREDENTIALS") || p.counts != 0 {
		t.Fatalf("count without a key: %v", err)
	}
	a.SetAIKey("priced", "k")
	n, err := a.CountAIPreview(ctx, pv.ID, pv.Plan.Fingerprint)
	if err != nil || n.InputTokens != 1000*pv.Plan.RequestCount || !n.HasPrice || n.CostUpperUSD <= 0 || n.CostUpperUSD > pv.Plan.CostUpperUSD {
		t.Fatalf("count %+v err %v", n, err)
	}

	// A run whose real cost passes the cap stops before the next request.
	p.usage = ai.Usage{InputTokens: 100, OutputTokens: 300_000} // $6 a request, past the $5 cap
	pv, err = a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "priced", Model: "m", StartMs: 0, EndMs: domain.DemoDurationMs, CostCapUSD: 5})
	if err != nil || pv.Plan.RequestCount < 2 {
		t.Fatalf("plan %d requests err %v", pv.Plan.RequestCount, err)
	}
	id, _ := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	v := waitAI(t, a, id)
	if v.State != store.AIPartial || v.ErrorCode != "COST_CAP" || v.CompletedRequests != 1 || v.CostUSD == nil || *v.CostUSD < 6 {
		t.Fatalf("capped run %+v", v)
	}
}

func TestAIBatchRunResumeCancelAndCompare(t *testing.T) {
	a, p, _ := newPricedApp(t)
	ctx := context.Background()
	demo, _ := a.CreateDemo(ctx, false)
	a.SetAIKey("priced", "k")
	a.SetAIKey("fakeai", "k")
	req := AIPreviewRequest{SessionID: demo.ID, Provider: "priced", Model: "m", All: true, CostCapUSD: 10, Delivery: "batch"}
	pv, err := a.PreviewAI(ctx, req)
	if err != nil || pv.Plan.Delivery != "batch" {
		t.Fatalf("batch plan %v", err)
	}
	batchID, err := a.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	v := waitAI(t, a, batchID)
	total := ai.Usage{}
	for range pv.Plan.Chunks {
		total = total.Add(p.usage)
	}
	if v.State != store.AICompleted || v.Delivery != "batch" || v.Recommendations == 0 || v.CacheReadTokens != total.CacheReadTokens ||
		v.CostUSD == nil || *v.CostUSD != testPrice.Cost(total, true) {
		t.Fatalf("batch run %+v (want cost %v)", v, testPrice.Cost(total, true))
	}
	if ids := p.submitted[0]; ids[0].ID != "chunk-000" || len(ids) != pv.Plan.RequestCount {
		t.Fatalf("batch requests %v", ids)
	}

	// A batch still running when the app stops is resumed once the key is back.
	p.ended = false
	pv2, _ := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "priced", Model: "m2", All: true, CostCapUSD: 10, Delivery: "batch"})
	waiting, _ := a.StartAI(ctx, pv2.ID, pv2.Plan.Fingerprint, false)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if r, _ := a.cfg.Store.AIRun(ctx, waiting); r.BatchID != "" {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("batch not submitted")
		}
	}
	a.stopAI(ctx) // shutdown: stop waiting, keep the batch
	a.SetAIKey("priced", "")
	if err := a.RecoverAI(ctx); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.AIRun(ctx, waiting); v.State != store.AIRunning || !v.WaitingForKey {
		t.Fatalf("after restart without a key %+v", v)
	}
	p.mu.Lock()
	p.ended = true
	p.mu.Unlock()
	a.SetAIKey("priced", "k")
	if v := waitAI(t, a, waiting); v.State != store.AICompleted || len(p.submitted) != 2 {
		t.Fatalf("resumed batch %+v, %d submissions (never re-sent)", v, len(p.submitted))
	}

	// Cancelling a batch cancels it at the provider and keeps finished results.
	p.mu.Lock()
	p.ended = false
	p.mu.Unlock()
	pv3, _ := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "priced", Model: "m3", All: true, CostCapUSD: 10, Delivery: "batch"})
	cancelled, _ := a.StartAI(ctx, pv3.ID, pv3.Plan.Fingerprint, false)
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if r, _ := a.cfg.Store.AIRun(ctx, cancelled); r.BatchID != "" {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("batch not submitted")
		}
	}
	a.CancelAI(cancelled)
	if v := waitAI(t, a, cancelled); v.State != store.AICancelled || p.cancelled != 1 {
		t.Fatalf("cancelled batch %+v", v)
	}

	// Two providers reviewing the same candidates can be compared.
	pvSingle, err := a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", All: true, CostCapUSD: 0.0001})
	if !isCode(err, "INPUT") {
		t.Fatalf("cap on an unpriced provider: %v", err)
	}
	tl, _ := a.Timeline(ctx, demo.ID)
	var ids []string
	for _, it := range tl.Items {
		if it.Source != SourceAI {
			ids = append(ids, it.ID)
		}
	}
	pvSingle, _ = a.PreviewAI(ctx, AIPreviewRequest{SessionID: demo.ID, Provider: "fakeai", Model: "m", ItemIDs: ids})
	singleID, _ := a.StartAI(ctx, pvSingle.ID, pvSingle.Plan.Fingerprint, false)
	waitAI(t, a, singleID)
	cmp, err := a.CompareAI(ctx, batchID, singleID)
	if err != nil || len(cmp.Matched) == 0 || !cmp.SameInput || cmp.A.Provider != "priced" || cmp.B.Provider != "fakeai" {
		t.Fatalf("comparison %+v err %v", cmp, err)
	}
	runs, _ := a.AIRuns(ctx, demo.ID)
	if len(runs) != 4 || runs[0].ID != singleID {
		t.Fatalf("runs %d, newest %s", len(runs), runs[0].ID)
	}
	if _, err := a.CompareAI(ctx, batchID, batchID); !isCode(err, "INPUT") {
		t.Fatalf("same run: %v", err)
	}
}

func TestMatchRecs(t *testing.T) {
	r := func(id string, s, e int64) ai.Recommendation { return ai.Recommendation{ID: id, StartMs: s, EndMs: e} }
	m, onlyA, onlyB := matchRecs([]ai.Recommendation{r("a1", 0, 10), r("a2", 100, 200), r("a3", 500, 600)},
		[]ai.Recommendation{r("b1", 150, 260), r("b2", 5, 8), r("b3", 900, 950)})
	if len(m) != 2 || m[0].B.ID != "b2" || m[1].B.ID != "b1" || m[1].OverlapMs != 50 || len(onlyA) != 1 || onlyA[0].ID != "a3" ||
		len(onlyB) != 1 || onlyB[0].ID != "b3" {
		t.Fatalf("matched %+v onlyA %v onlyB %v", m, onlyA, onlyB)
	}
}
