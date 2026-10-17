package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// batchRequestID names a chunk inside a batch (custom_id: letters, digits,
// _ and -). A request covers a time window, not one candidate, so the ID is
// the chunk's position in the confirmed plan.
func batchRequestID(i int) string { return fmt.Sprintf("chunk-%03d", i) }

// runBatch submits a confirmed plan as one batch and waits for its results.
// Submission is retried only for rate limits and server errors; an unknown
// outcome is never re-sent.
func (a *App) runBatch(act *aiActive, run store.AIRun, plan ai.Plan, bp ai.BatchProvider, key string) {
	ctx := act.ctx
	reqs := make([]ai.BatchRequest, len(plan.Chunks))
	for i, c := range plan.Chunks {
		reqs[i] = ai.BatchRequest{ID: batchRequestID(i), Chunk: c}
	}
	err := func() error {
		for try := 0; ; try++ {
			run.Attempts++
			id, err := bp.SubmitBatch(ctx, key, plan.Model, reqs)
			if err == nil {
				run.BatchID = id
				return nil
			}
			if ctx.Err() != nil {
				return domain.NewUserError("AI_OUTCOME_UNKNOWN", "앱이 종료되어 배치 접수 여부를 확인하지 못했습니다. 제공자 콘솔에서 확인해 주세요.")
			}
			var pe *ai.ProviderError
			if !errors.As(err, &pe) || !pe.Retryable || try == aiTriesPerChunk-1 || pe.RetryAfter > aiMaxRetryWait {
				return err
			}
			if sleepCtx(ctx, max(pe.RetryAfter, time.Duration(1<<try)*time.Second)) != nil {
				return domain.NewUserError("CANCELLED", "배치를 보내기 전에 중단했습니다.")
			}
		}
	}()
	if err != nil {
		a.finishBatch(run, plan, nil, err, false)
		return
	}
	if err := a.cfg.Store.SaveAIRun(context.WithoutCancel(ctx), run); err != nil {
		a.logError("save AI batch", err)
	}
	a.publishAI(run, nil)
	a.pollBatch(act, run, plan, bp, key)
}

// pollBatch waits for a submitted batch. On shutdown it just returns: the
// batch keeps running at the provider and is resumed on the next start.
func (a *App) pollBatch(act *aiActive, run store.AIRun, plan ai.Plan, bp ai.BatchProvider, key string) {
	ctx := act.ctx
	wait := a.ai.timing.batchPoll
	userCancel := act.userCancel
	cancelled, failures := false, 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-userCancel:
			userCancel = nil // handled once
			cancelled = true
			if err := bp.CancelBatch(ctx, key, run.BatchID); err != nil && ctx.Err() == nil {
				a.logError("cancel AI batch", err)
			}
			wait = a.ai.timing.batchPoll
			continue
		case <-time.After(wait):
		}
		wait = min(wait*2, a.ai.timing.batchPollMax)
		st, err := bp.BatchStatus(ctx, key, run.BatchID)
		if err == nil && st.Ended {
			var results []ai.BatchResult
			if results, err = bp.BatchResults(ctx, key, run.BatchID); err == nil {
				a.finishBatch(run, plan, results, nil, cancelled)
				return
			}
		}
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			var pe *ai.ProviderError
			if failures++; errors.As(err, &pe) && pe.Retryable && failures < 10 {
				continue
			}
			a.finishBatch(run, plan, nil, err, cancelled)
			return
		}
		failures = 0
		if done := st.Succeeded + st.Errored + st.Canceled + st.Expired; done != run.CompletedRequests {
			run.CompletedRequests = done
			a.publishAI(run, nil)
		}
	}
}

// finishBatch records an ended batch: results are matched to chunks by ID,
// validated like single answers, and billed at the batch price.
func (a *App) finishBatch(run store.AIRun, plan ai.Plan, results []ai.BatchResult, failure error, cancelled bool) {
	bg := context.WithoutCancel(a.ctx)
	index := map[string]int{}
	for i := range plan.Chunks {
		index[batchRequestID(i)] = i
	}
	done := make([]bool, len(plan.Chunks))
	var recs []ai.Recommendation
	var usage []ai.Usage
	var total ai.Usage
	var extra []ai.Omitted
	var firstErr error
	for _, r := range results {
		i, ok := index[r.ID]
		if !ok || done[i] {
			continue
		}
		total = total.Add(r.Usage)
		usage = append(usage, r.Usage)
		chunk := plan.Chunks[i]
		err := r.Err
		if err == nil {
			var accepted []ai.Recommendation
			var rejected int
			if accepted, rejected, err = ai.Validate(r.Output, chunk); err == nil {
				recs = append(recs, accepted...)
				run.Rejected += rejected
				done[i] = true
				continue
			}
		}
		if firstErr == nil {
			firstErr = err
		}
		extra = append(extra, ai.Omitted{StartMs: chunk.StartMs, EndMs: chunk.EndMs, Reason: "batch_error"})
	}
	var unprocessed []ai.Range
	run.CompletedRequests = 0
	for i, ok := range done {
		if ok {
			run.CompletedRequests++
		} else {
			unprocessed = append(unprocessed, ai.Range{StartMs: plan.Chunks[i].StartMs, EndMs: plan.Chunks[i].EndMs})
		}
	}
	if failure == nil && firstErr != nil && run.CompletedRequests == 0 {
		failure = firstErr
	}
	switch {
	case cancelled:
		run.State, run.ErrorCode, run.ErrorMessage = store.AICancelled, "CANCELLED", "배치를 취소했습니다. 이미 끝난 요청의 결과는 남겼습니다."
	case failure != nil:
		run.State, run.ErrorCode, run.ErrorMessage = store.AIFailed, "AI_ERROR", "AI 작업을 완료하지 못했습니다."
		if ue, ok := domain.AsUserError(failure); ok {
			run.ErrorCode, run.ErrorMessage = ue.Code, ue.Message
		}
		if run.CompletedRequests > 0 {
			run.State = store.AIPartial
		}
	case run.CompletedRequests < len(plan.Chunks) || len(plan.Omitted) > 0:
		run.State = store.AIPartial
		if ue, ok := domain.AsUserError(firstErr); ok {
			run.ErrorCode, run.ErrorMessage = ue.Code, ue.Message
		}
	default:
		run.State = store.AICompleted
	}
	recs = dedupeRecs(recs)
	setUsage(&run, total, plan.Price, true)
	run.RecommendationsJSON, _ = json.Marshal(nonNilRecs(recs))
	run.UsageJSON, _ = json.Marshal(usage)
	a.writeAIRun(run, plan, recs, extra, unprocessed)
	if err := a.cfg.Store.SaveAIRun(bg, run); err != nil {
		a.logError("save AI batch", err)
	}
	a.writeUnified(bg, run.SessionID)
	a.publishAI(run, recs)
}

// resumeBatches waits again for submitted batches whose provider has a key
// in memory (after a restart, or once the user enters the key).
func (a *App) resumeBatches(ctx context.Context) {
	runs, err := a.cfg.Store.PendingBatchRuns(ctx)
	if err != nil {
		a.logError("list AI batches", err)
		return
	}
	for _, run := range runs {
		p, ok := a.cfg.AI.Get(run.Provider)
		bp, batch := p.(ai.BatchProvider)
		if !ok || !batch {
			continue
		}
		var plan ai.Plan
		if json.Unmarshal(run.PlanJSON, &plan) != nil {
			continue
		}
		a.ai.mu.Lock()
		key := a.ai.keys[run.Provider]
		if key == "" || a.ai.active[run.ID] != nil || a.ctx.Err() != nil {
			a.ai.mu.Unlock()
			continue
		}
		act := a.startActive(run.ID, true)
		a.ai.mu.Unlock()
		go func() {
			defer a.endActive(run.ID, act)
			a.pollBatch(act, run, plan, bp, key)
		}()
		a.publishAI(run, nil)
	}
}
