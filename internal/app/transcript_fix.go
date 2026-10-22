package app

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// fixWorkers is how many transcript fix requests are out at a time.
const fixWorkers = 3

// fixTranscript sends a session's newest recognised transcript to AI to
// correct what speech recognition misheard (the transcript task's model),
// a chunk of about 8 KB at a time with the lines around it, the title and
// the words viewers used meanwhile. Lines keep their times and IDs; what
// was heard stays beside the fix. A caption file, a transcript fixed
// before and the sample's are left alone. It returns the lines changed.
func (a *App) fixTranscript(ctx context.Context, sid string, p ai.Provider, key string, b *autoBudget,
	report func(done, total int)) (int, error) {
	st := a.cfg.Store
	t, ok, err := st.LatestTranscript(ctx, sid)
	if err != nil || !ok || t.Source != "asr" || t.FixedBy != "" {
		return 0, err
	}
	utts, err := st.UtterancesBetween(ctx, t.ID, 0, math.MaxInt64, 1_000_000)
	if err != nil || len(utts) == 0 {
		return 0, err
	}
	return a.fixLines(ctx, sid, t, utts, p, key, b, report)
}

// fixLines is fixTranscript for some lines of transcript t (a LIVE
// capture's, as each candidate's range is transcribed).
func (a *App) fixLines(ctx context.Context, sid string, t store.Transcript, utts []domain.Utterance, p ai.Provider, key string,
	b *autoBudget, report func(done, total int)) (int, error) {
	st := a.cfg.Store
	stepper, ok := p.(ai.StepProvider)
	if !ok {
		return 0, domain.NewUserError("UNSUPPORTED", p.Name()+"은(는) 대본 다듬기를 지원하지 않습니다.")
	}
	s, err := st.Session(ctx, sid)
	if err != nil {
		return 0, err
	}
	words := func(from, to int64) []string {
		chats, _, err := st.ChatsBetween(ctx, sid, from, to, store.AllSeq, 20_000, 0)
		if err != nil {
			return nil
		}
		return ai.ChatWords(chats)
	}
	chunks := ai.PrepareTranscript(s.Title, utts, words)
	model := a.TaskModel(p, ai.TaskTranscript)
	var price *ai.Price
	if pr, ok := p.(ai.Priced); ok {
		if v, ok := pr.Price(model); ok {
			price = &v
		}
	}
	// Within what the session may still send: the earliest stretches first.
	n, upper := 0, 0.0
	for _, c := range chunks {
		step := c.Step()
		cost := 0.0
		if price != nil {
			cost = price.UpperCost(ai.StepInputEstimate(step), step.OutputLimit(), false)
		}
		if n >= b.requests || (price != nil && upper+cost > b.costUSD) {
			break
		}
		n, upper = n+1, upper+cost
	}
	if n == 0 {
		return 0, errAutoBudget
	}
	chunks = chunks[:n]

	run := store.AIRun{SessionID: sid, Provider: p.ID(), Model: model, Kind: store.AIKindTranscript, Origin: store.AIOriginAuto,
		Fingerprint: ai.Digest(chunks), RequestCount: len(chunks), PlanJSON: ai.Encode(chunks)}
	if price != nil {
		limit := b.costUSD
		run.CostCapUSD = &limit
	}
	if run, err = st.CreateAIRun(ctx, run); err != nil {
		return 0, err
	}
	a.ai.mu.Lock()
	act := a.startActive(run.ID, false)
	a.ai.mu.Unlock()
	defer a.endActive(run.ID, act)
	a.publishAI(run, nil)

	bg := context.WithoutCancel(ctx)
	var mu sync.Mutex // guards run, total, fixed and done below
	var total ai.Usage
	fixed, done := 0, 0
	save := func() {
		setUsage(&run, total, price, false)
		run.RecommendationsJSON, _ = json.Marshal(map[string]int{"fixed": fixed})
		if err := st.SaveAIRun(bg, run); err != nil {
			a.logError("save AI run", err)
		}
	}
	work := make(chan ai.TranscriptChunk)
	errs := make(chan error, len(chunks))
	var wg sync.WaitGroup
	for range min(fixWorkers, len(chunks)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				out, u, attempts, err := sendStep(act.ctx, stepper, key, model, c.Step())
				var fixes map[string]string
				rejected := 0
				if err == nil {
					fixes, rejected, err = ai.ValidateFixes(out, c)
				}
				changed := 0
				if err == nil && len(fixes) > 0 {
					changed, err = st.FixUtterances(bg, t.ID, run.ID, fixes)
				}
				mu.Lock()
				total = total.Add(u)
				run.Attempts += attempts
				run.Rejected += rejected
				if err == nil {
					run.CompletedRequests++
					fixed += changed
				}
				done++
				d := done
				save()
				mu.Unlock()
				a.publishAI(run, nil)
				if report != nil {
					report(d, len(chunks))
				}
				if err != nil {
					errs <- err
				}
			}
		}()
	}
	for _, c := range chunks {
		if act.ctx.Err() != nil {
			break
		}
		work <- c
	}
	close(work)
	wg.Wait()
	close(errs)
	var failure error
	for err := range errs {
		if failure == nil || errors.Is(err, context.Canceled) {
			failure = err
		}
	}
	mu.Lock()
	switch {
	case act.ctx.Err() != nil:
		run.State, run.ErrorCode, run.ErrorMessage = store.AICancelled, "CANCELLED", "대본 다듬기를 중단했습니다."
	case failure != nil && run.CompletedRequests == 0:
		run.State, run.ErrorCode, run.ErrorMessage = store.AIFailed, "AI_ERROR", "대본을 다듬지 못했습니다."
		if ue, ok := domain.AsUserError(failure); ok {
			run.ErrorCode, run.ErrorMessage = ue.Code, ue.Message
		}
	case failure != nil:
		run.State, run.ErrorCode, run.ErrorMessage = store.AIPartial, "AI_ERROR", "일부 구간의 대본을 다듬지 못했습니다."
	default:
		run.State = store.AICompleted
	}
	save()
	b.spend(run)
	mu.Unlock()
	a.publishAI(run, nil)
	if fixed > 0 {
		if fresh, err := st.UtterancesBetween(bg, t.ID, 0, math.MaxInt64, 1_000_000); err == nil {
			if err := export.WriteTranscript(filepath.Join(a.exportDir(sid), "transcript"), t.ID, fresh); err != nil {
				a.logError("write transcript", err)
			}
		}
	}
	if run.State == store.AICancelled {
		return fixed, context.Canceled
	}
	if run.State == store.AIFailed {
		return fixed, failure
	}
	return fixed, nil
}

// sendStep sends one step, again after a rate limit or a server error (up
// to three tries, as a review's chunk). It returns the tries it took.
func sendStep(ctx context.Context, p ai.StepProvider, key, model string, step ai.StepRequest) (json.RawMessage, ai.Usage, int, error) {
	var total ai.Usage
	for try := 0; ; try++ {
		out, u, err := p.Step(ctx, key, model, step)
		total = total.Add(u) // a failed answer may still be billed
		if err == nil {
			return out, total, try + 1, nil
		}
		if ctx.Err() != nil {
			return nil, total, try + 1, context.Canceled
		}
		var pe *ai.ProviderError
		if !errors.As(err, &pe) || !pe.Retryable || try == aiTriesPerChunk-1 || pe.RetryAfter > aiMaxRetryWait {
			return nil, total, try + 1, err
		}
		if sleepCtx(ctx, max(pe.RetryAfter, time.Duration(1<<try)*time.Second)) != nil {
			return nil, total, try + 1, context.Canceled
		}
	}
}
