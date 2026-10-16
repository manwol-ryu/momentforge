package app

import (
	"context"
	"encoding/json"
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

func (f *fakeAI) ID() string     { return "fakeai" }
func (f *fakeAI) Name() string   { return "가짜 AI" }
func (f *fakeAI) KeyEnv() string { return "" }
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
	if v.State != store.AICompleted || v.Recommendations == 0 || v.Attempts != v.RequestCount+1 || v.InputTokens == 0 || f.keys[0] != "key-1" {
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
