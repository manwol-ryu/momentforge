package app

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// AI limits (CzzEdit's, plus cost and batch limits).
const (
	aiPreviewTTL     = 10 * time.Minute
	aiMaxPreviews    = 20
	aiTriesPerChunk  = 3
	aiMaxRetryWait   = 120 * time.Second
	aiMaxModelLength = 200
	aiMaxCostCapUSD  = 1000
	aiCountWorkers   = 4 // token-counting requests at a time
	aiMaxBatches     = 5 // batches waiting for results at a time
)

// aiTiming is how often submitted batches are checked (tests shorten it).
type aiTiming struct {
	batchPoll    time.Duration // first check after submitting; doubles up to batchPollMax
	batchPollMax time.Duration
}

func defaultAITiming() aiTiming {
	return aiTiming{batchPoll: 30 * time.Second, batchPollMax: 5 * time.Minute}
}

// aiState keeps API keys and previews in memory only; keys are never
// written to the database, files or logs.
type aiState struct {
	mu       sync.Mutex
	keys     map[string]string // provider ID → key
	previews map[string]*aiPreview
	active   map[string]*aiActive // run ID → work in progress
	wg       sync.WaitGroup
	timing   aiTiming
}

// aiActive is a run being sent or waited for.
type aiActive struct {
	batch      bool
	ctx        context.Context
	cancel     context.CancelFunc // stops the work: sending (single) or waiting (batch; on shutdown)
	userCancel chan struct{}      // batch: closed when the user cancels the batch
	once       sync.Once
}

type aiPreview struct {
	plan    ai.Plan
	expires time.Time
	runID   string
	counted []int // exact input tokens per chunk, once the user asked for them
}

func (a *App) initAI() {
	a.ai.keys, a.ai.previews, a.ai.active = map[string]string{}, map[string]*aiPreview{}, map[string]*aiActive{}
	a.ai.timing = defaultAITiming()
	if a.cfg.AIBatchPoll > 0 {
		a.ai.timing.batchPoll = a.cfg.AIBatchPoll
		a.ai.timing.batchPollMax = 4 * a.cfg.AIBatchPoll
	}
	for _, p := range a.cfg.AI.All() {
		if env := p.KeyEnv(); env != "" {
			if k := strings.TrimSpace(os.Getenv(env)); k != "" {
				a.ai.keys[p.ID()] = k
			}
		}
	}
}

// AIProvider is a registered provider as screens show it.
type AIProvider struct {
	ID            string
	Name          string
	Configured    bool // a key is in memory
	DefaultModel  string
	CanCount      bool // exact token counts
	CanListModels bool
	CanBatch      bool
	HasPrices     bool
}

// AIProviders lists providers (the default first) and whether each has a key.
func (a *App) AIProviders() []AIProvider {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	var out []AIProvider
	for _, p := range a.cfg.AI.All() {
		_, count := p.(ai.TokenCounter)
		_, models := p.(ai.ModelLister)
		_, batch := p.(ai.BatchProvider)
		_, priced := p.(ai.Priced)
		out = append(out, AIProvider{ID: p.ID(), Name: p.Name(), Configured: a.ai.keys[p.ID()] != "",
			DefaultModel: p.DefaultModel(), CanCount: count, CanListModels: models, CanBatch: batch, HasPrices: priced})
	}
	return out
}

// SetAIKey keeps (or, when empty, forgets) a provider's key in memory. A
// new key also resumes batches of that provider waiting for their results.
func (a *App) SetAIKey(provider, key string) (bool, error) {
	if _, ok := a.cfg.AI.Get(provider); !ok {
		return false, domain.NewUserError("INPUT", "AI 제공자를 선택해 주세요.")
	}
	if len(key) > 1024 || strings.ContainsAny(key, "\r\n") {
		return false, domain.NewUserError("INPUT", "API 키 형식을 확인해 주세요.")
	}
	a.ai.mu.Lock()
	if key = strings.TrimSpace(key); key == "" {
		delete(a.ai.keys, provider)
	} else {
		a.ai.keys[provider] = key
	}
	a.ai.mu.Unlock()
	if key != "" {
		a.resumeBatches(a.ctx)
	}
	return key != "", nil
}

func (a *App) aiKey(provider string) string {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	return a.ai.keys[provider]
}

// AIModel is a model a key can use.
type AIModel = ai.Model

// AIModels lists the models the provider's key can use. Only the key is
// sent, no collected data.
func (a *App) AIModels(ctx context.Context, provider string) ([]AIModel, error) {
	p, ok := a.cfg.AI.Get(provider)
	if !ok {
		return nil, domain.NewUserError("INPUT", "AI 제공자를 선택해 주세요.")
	}
	lister, ok := p.(ai.ModelLister)
	if !ok {
		return nil, domain.NewUserError("UNSUPPORTED", "이 제공자는 모델 목록을 제공하지 않습니다. 모델 ID를 직접 입력해 주세요.")
	}
	key := a.aiKey(provider)
	if key == "" {
		return nil, domain.NewUserError("CREDENTIALS", "API 키를 설정해 주세요.")
	}
	return lister.Models(ctx, key)
}

// AIPreviewRequest says what to review: timeline items, every candidate,
// or a chat range.
type AIPreviewRequest struct {
	SessionID  string
	Provider   string
	Model      string
	ItemIDs    []string // unified timeline item IDs (not AI ones); nil for All or a range
	All        bool     // every rule and viewer candidate (automatic selection; needs a cost cap)
	StartMs    int64    // range mode
	EndMs      int64
	Delivery   string  // single (default) | batch
	CostCapUSD float64 // 0: no limit
}

// AIPreview is a plan the user must confirm before anything is sent.
type AIPreview struct {
	ID        string
	ExpiresAt time.Time
	Plan      ai.Plan
}

// PreviewAI prepares (but does not send) a review.
func (a *App) PreviewAI(ctx context.Context, req AIPreviewRequest) (AIPreview, error) {
	req.Model = strings.TrimSpace(req.Model)
	if req.Model == "" || len(req.Model) > aiMaxModelLength {
		return AIPreview{}, domain.NewUserError("INPUT", "사용 가능한 API 모델 ID를 입력해 주세요.")
	}
	provider, ok := a.cfg.AI.Get(req.Provider)
	if !ok {
		return AIPreview{}, domain.NewUserError("INPUT", "AI 제공자를 선택해 주세요.")
	}
	switch req.Delivery {
	case "", ai.DeliverySingle:
		req.Delivery = ai.DeliverySingle
	case ai.DeliveryBatch:
		if _, ok := provider.(ai.BatchProvider); !ok {
			return AIPreview{}, domain.NewUserError("INPUT", "이 제공자는 배치 전송을 지원하지 않습니다.")
		}
	default:
		return AIPreview{}, domain.NewUserError("INPUT", "전송 방식을 확인해 주세요.")
	}
	if req.CostCapUSD < 0 || req.CostCapUSD > aiMaxCostCapUSD {
		return AIPreview{}, domain.NewUserError("INPUT", "비용 한도는 0~1,000달러로 입력해 주세요.")
	}
	if req.All && req.CostCapUSD == 0 {
		return AIPreview{}, domain.NewUserError("INPUT", "모든 후보를 자동으로 고를 때는 비용 한도를 정해 주세요.")
	}
	in := ai.PlanInput{SessionID: req.SessionID, Provider: req.Provider, Model: req.Model, Delivery: req.Delivery,
		MaxOutputTokens: provider.MaxOutputTokens(), CostCapUSD: req.CostCapUSD, Comments: map[string]ai.Evidence{}}
	if priced, ok := provider.(ai.Priced); ok {
		if price, ok := priced.Price(req.Model); ok {
			in.Price = &price
		}
	}
	if req.CostCapUSD > 0 && in.Price == nil {
		return AIPreview{}, domain.NewUserError("INPUT", "가격을 알 수 없는 모델에는 비용 한도를 적용할 수 없습니다.")
	}
	tl, err := a.Timeline(ctx, req.SessionID)
	if err != nil {
		return AIPreview{}, err
	}
	maxSeq := int64(store.AllSeq)
	stats := map[string]map[string]any{}
	if tl.AnalysisID != "" {
		an, err := a.cfg.Store.Analysis(ctx, tl.AnalysisID)
		if err != nil {
			return AIPreview{}, err
		}
		maxSeq = an.MaxSeq
		for _, c := range an.Candidates {
			stats[SourceRule+":"+c.ID] = map[string]any{"n": c.Peak.N, "u": c.Peak.U, "ratio": c.Peak.Ratio, "z": c.Peak.Z,
				"baseline_n": c.Peak.BaselineN}
		}
	}
	in.Chats = func(from, to int64) ([]domain.ChatEvent, error) {
		chats, _, err := a.cfg.Store.ChatsBetween(ctx, req.SessionID, from, to, maxSeq, 1_000_000, 0)
		return chats, err
	}
	add := func(it TimelineItem) {
		in.Candidates = append(in.Candidates, ai.Candidate{ID: it.ID, SourceType: it.Source, AnchorMs: it.AnchorMs,
			Title: it.Title, Statistics: stats[it.ID]})
		start := it.ClipStartMs
		if it.Source == SourceViewer {
			start = max(0, it.AnchorMs-30000)
			in.Comments[it.ID] = ai.Evidence{Ref: "comment:" + it.OriginID, TimeMs: it.AnchorMs, Content: it.CommentText}
		}
		in.Ranges = append(in.Ranges, ai.Range{StartMs: start, EndMs: it.ClipEndMs})
	}
	switch {
	case req.All:
		for _, it := range tl.Items {
			if it.Source != SourceAI {
				add(it)
			}
		}
		if len(in.Candidates) == 0 {
			return AIPreview{}, domain.NewUserError("INPUT", "검토할 후보가 없습니다. 먼저 분석하거나 영상 댓글을 읽어 주세요.")
		}
	case req.ItemIDs != nil:
		if len(req.ItemIDs) == 0 {
			return AIPreview{}, domain.NewUserError("INPUT", "검토할 후보를 선택해 주세요.")
		}
		want := map[string]bool{}
		for _, id := range req.ItemIDs {
			want[id] = true
		}
		for _, it := range tl.Items {
			if want[it.ID] && it.Source != SourceAI {
				delete(want, it.ID)
				add(it)
			}
		}
		if len(want) > 0 {
			return AIPreview{}, domain.NewUserError("INPUT", "선택한 원본 후보를 찾을 수 없습니다.")
		}
	default:
		if req.StartMs < 0 || req.EndMs <= req.StartMs || req.EndMs > tl.DurationMs {
			return AIPreview{}, domain.NewUserError("INPUT", "채팅 탐색 범위를 확인해 주세요.")
		}
		in.Ranges = []ai.Range{{StartMs: req.StartMs, EndMs: req.EndMs}}
	}
	plan, err := ai.Prepare(in)
	if err != nil {
		return AIPreview{}, err
	}
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	now := time.Now()
	for id, p := range a.ai.previews {
		if p.expires.Before(now) {
			delete(a.ai.previews, id)
		}
	}
	if len(a.ai.previews) >= aiMaxPreviews {
		return AIPreview{}, domain.NewUserError("BUSY", "미리보기가 너무 많습니다. 잠시 후 다시 시도해 주세요.")
	}
	id := randomHex()
	a.ai.previews[id] = &aiPreview{plan: plan, expires: now.Add(aiPreviewTTL)}
	return AIPreview{ID: id, ExpiresAt: now.Add(aiPreviewTTL), Plan: plan}, nil
}

func randomHex() string {
	b := make([]byte, 16)
	crand.Read(b)
	return hex.EncodeToString(b)
}

// confirmedPreview returns the preview the user saw; the caller holds a.ai.mu.
func (a *App) confirmedPreview(previewID, fingerprint string) (*aiPreview, error) {
	pv := a.ai.previews[previewID]
	if pv == nil || pv.expires.Before(time.Now()) || pv.plan.Fingerprint != fingerprint {
		return nil, domain.NewUserError("PREVIEW", "미리보기가 만료되었거나 입력이 변경되었습니다.")
	}
	return pv, nil
}

// AITokenCount is a preview's input counted by the provider.
type AITokenCount struct {
	InputTokens  int
	CostUpperUSD float64 // with the exact input and the full answer limit; 0 without a price
	HasPrice     bool
}

// CountAIPreview counts a previewed plan's input tokens with the provider's
// counting API. That sends the previewed chats (nothing is generated or
// billed), so it runs only when the user asks after seeing the preview.
func (a *App) CountAIPreview(ctx context.Context, previewID, fingerprint string) (AITokenCount, error) {
	a.ai.mu.Lock()
	pv, err := a.confirmedPreview(previewID, fingerprint)
	if err != nil {
		a.ai.mu.Unlock()
		return AITokenCount{}, err
	}
	plan, key := pv.plan, a.ai.keys[pv.plan.Provider]
	a.ai.mu.Unlock()
	p, _ := a.cfg.AI.Get(plan.Provider)
	counter, ok := p.(ai.TokenCounter)
	if !ok {
		return AITokenCount{}, domain.NewUserError("UNSUPPORTED", "이 제공자는 토큰 계산을 지원하지 않습니다.")
	}
	if key == "" {
		return AITokenCount{}, domain.NewUserError("CREDENTIALS", "API 키를 설정해 주세요.")
	}
	counted := make([]int, len(plan.Chunks))
	errs := make([]error, len(plan.Chunks))
	sem := make(chan struct{}, aiCountWorkers)
	var wg sync.WaitGroup
	for i, c := range plan.Chunks {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			counted[i], errs[i] = counter.CountTokens(ctx, key, plan.Model, c)
		}()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		for _, e := range errs {
			if e != nil {
				return AITokenCount{}, e
			}
		}
	}
	out := AITokenCount{HasPrice: plan.Price != nil}
	outPerRequest := plan.OutputTokenLimit / max(1, plan.RequestCount)
	for _, n := range counted {
		out.InputTokens += n
		if plan.Price != nil {
			out.CostUpperUSD += plan.Price.UpperCost(n, outPerRequest, plan.Delivery == ai.DeliveryBatch)
		}
	}
	a.ai.mu.Lock()
	pv.counted = counted
	a.ai.mu.Unlock()
	return out, nil
}

// StartAI sends a confirmed preview. The fingerprint must match what the
// user saw; the same completed plan is reused unless regenerate is set.
func (a *App) StartAI(ctx context.Context, previewID, fingerprint string, regenerate bool) (string, error) {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	pv, err := a.confirmedPreview(previewID, fingerprint)
	if err != nil {
		return "", err
	}
	if pv.runID != "" && !regenerate {
		return pv.runID, nil
	}
	plan := pv.plan
	key := a.ai.keys[plan.Provider]
	if key == "" {
		return "", domain.NewUserError("CREDENTIALS", "API 키를 설정해 주세요.")
	}
	// Reusing a completed run of the same plan sends nothing, so it never waits.
	if !regenerate {
		if done, ok, err := a.cfg.Store.CompletedAIRun(ctx, plan.SessionID, plan.Fingerprint); err != nil {
			return "", err
		} else if ok {
			pv.runID = done.ID
			return done.ID, nil
		}
	}
	batch := plan.Delivery == ai.DeliveryBatch
	singles, batches := 0, 0
	for _, act := range a.ai.active {
		if act.batch {
			batches++
		} else {
			singles++
		}
	}
	if !batch && singles > 0 {
		return "", domain.NewUserError("BUSY", "진행 중인 AI 작업을 먼저 마치거나 중단해 주세요.")
	}
	if batch && batches >= aiMaxBatches {
		return "", domain.NewUserError("BUSY", "결과를 기다리는 배치가 너무 많습니다. 끝난 뒤 다시 시도해 주세요.")
	}
	provider, _ := a.cfg.AI.Get(plan.Provider)
	run := store.AIRun{SessionID: plan.SessionID, Provider: plan.Provider, Model: plan.Model, Fingerprint: plan.Fingerprint,
		Delivery: plan.Delivery, RequestCount: plan.RequestCount, PlanJSON: ai.Encode(plan)}
	if plan.CostCapUSD > 0 {
		run.CostCapUSD = &plan.CostCapUSD
	}
	run, err = a.cfg.Store.CreateAIRun(ctx, run)
	if err != nil {
		return "", err
	}
	pv.runID = run.ID
	act := a.startActive(run.ID, batch)
	go func() {
		defer a.endActive(run.ID, act)
		if batch {
			a.runBatch(act, run, plan, provider.(ai.BatchProvider), key)
		} else {
			a.runAI(act, run, plan, provider, key)
		}
	}()
	a.publishAI(run, nil)
	return run.ID, nil
}

// startActive registers work for a run; the caller holds a.ai.mu.
func (a *App) startActive(runID string, batch bool) *aiActive {
	ctx, cancel := context.WithCancel(a.ctx)
	act := &aiActive{batch: batch, ctx: ctx, cancel: cancel, userCancel: make(chan struct{})}
	a.ai.active[runID] = act
	a.ai.wg.Add(1)
	return act
}

func (a *App) endActive(runID string, act *aiActive) {
	act.cancel()
	a.ai.mu.Lock()
	if a.ai.active[runID] == act {
		delete(a.ai.active, runID)
	}
	a.ai.mu.Unlock()
	a.ai.wg.Done()
}

// CancelAI asks a review to stop: a single run after the current request,
// a batch by cancelling it at the provider (finished requests are kept).
func (a *App) CancelAI(id string) {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	act := a.ai.active[id]
	switch {
	case act == nil:
	case act.batch:
		act.once.Do(func() { close(act.userCancel) })
	default:
		act.cancel()
	}
}

// stopAI ends all AI work for shutdown. Submitted batches keep running at
// the provider and are picked up again on the next start.
func (a *App) stopAI(ctx context.Context) {
	a.ai.mu.Lock()
	for _, act := range a.ai.active {
		act.cancel()
	}
	a.ai.mu.Unlock()
	done := make(chan struct{})
	go func() { a.ai.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// RecoverAI marks reviews left running by a crash: their outcome is unknown
// and they are never re-sent automatically. Submitted batches are resumed
// (their results are fetched) once the provider's key is in memory.
func (a *App) RecoverAI(ctx context.Context) error {
	if _, err := a.cfg.Store.MarkAIRunsInterrupted(ctx, "앱이 종료되어 전송 결과가 불명확합니다. 자동 재실행하지 않습니다."); err != nil {
		return err
	}
	a.resumeBatches(ctx)
	return nil
}

var (
	errAICancelled = domain.NewUserError("CANCELLED", "AI 작업을 중단했습니다.")
	errAICostCap   = domain.NewUserError("COST_CAP", "비용 한도에 닿아 남은 요청을 보내지 않았습니다.")
)

// runAI sends the chunks in order (CzzEdit ai.Jobs.run): up to 3 tries per
// chunk for rate limits and server errors, 20 requests in all. With a cost
// cap it stops before a request that could pass the cap.
func (a *App) runAI(act *aiActive, run store.AIRun, plan ai.Plan, provider ai.Provider, key string) {
	ctx := act.ctx
	bg := context.WithoutCancel(ctx)
	var recs []ai.Recommendation
	var usage []ai.Usage
	var total ai.Usage
	var extraOmitted []ai.Omitted
	outPerRequest := plan.OutputTokenLimit / max(1, plan.RequestCount)
	save := func() {
		run.RecommendationsJSON, _ = json.Marshal(nonNilRecs(recs))
		run.UsageJSON, _ = json.Marshal(usage)
		setUsage(&run, total, plan.Price, false)
		if err := a.cfg.Store.SaveAIRun(bg, run); err != nil {
			a.logError("save AI run", err)
		}
	}
	failure := func() error {
		for _, chunk := range plan.Chunks {
			if ctx.Err() != nil {
				return errAICancelled
			}
			if run.Attempts >= plan.AttemptLimit {
				extraOmitted = append(extraOmitted, ai.Omitted{StartMs: chunk.StartMs, EndMs: chunk.EndMs, Reason: "retry_request_budget"})
				break
			}
			if plan.CostCapUSD > 0 && plan.Price != nil &&
				plan.Price.Cost(total, false)+plan.Price.UpperCost(ai.ChunkInputEstimate(chunk), outPerRequest, false) > plan.CostCapUSD {
				return errAICostCap
			}
			var out json.RawMessage
			for try := 0; ; try++ {
				run.Attempts++
				var u ai.Usage
				var err error
				out, u, err = provider.Review(ctx, key, plan.Model, chunk)
				total = total.Add(u) // a failed answer may still be billed
				if err == nil {
					usage = append(usage, u)
					break
				}
				if ctx.Err() != nil {
					return errAICancelled
				}
				var pe *ai.ProviderError
				if !errors.As(err, &pe) || !pe.Retryable || try == aiTriesPerChunk-1 || run.Attempts >= plan.AttemptLimit || pe.RetryAfter > aiMaxRetryWait {
					return err
				}
				if sleepCtx(ctx, max(pe.RetryAfter, time.Duration(1<<try)*time.Second)) != nil {
					return errAICancelled
				}
			}
			accepted, rejected, err := ai.Validate(out, chunk)
			if err != nil {
				return err
			}
			recs = append(recs, accepted...)
			run.Rejected += rejected
			run.CompletedRequests++
			save()
			a.publishAI(run, recs)
		}
		return nil
	}()

	switch {
	case failure != nil:
		run.ErrorCode, run.ErrorMessage = "AI_ERROR", "AI 작업을 완료하지 못했습니다."
		if ue, ok := domain.AsUserError(failure); ok {
			run.ErrorCode, run.ErrorMessage = ue.Code, ue.Message
		}
		run.State = store.AIFailed
		if run.ErrorCode == "CANCELLED" {
			run.State = store.AICancelled
		} else if run.CompletedRequests > 0 {
			run.State = store.AIPartial
		}
	case len(plan.Omitted) > 0 || len(extraOmitted) > 0 || run.CompletedRequests < len(plan.Chunks):
		run.State = store.AIPartial
	default:
		run.State = store.AICompleted
	}
	recs = dedupeRecs(recs)
	var unprocessed []ai.Range
	for _, c := range plan.Chunks[min(run.CompletedRequests, len(plan.Chunks)):] {
		unprocessed = append(unprocessed, ai.Range{StartMs: c.StartMs, EndMs: c.EndMs})
	}
	setUsage(&run, total, plan.Price, false)
	// Files first: once the end state is visible, the exports are complete.
	a.writeAIRun(run, plan, recs, extraOmitted, unprocessed)
	save()
	a.writeUnified(bg, run.SessionID)
	a.publishAI(run, recs)
}

// setUsage records the token totals and their cost at list price.
func setUsage(run *store.AIRun, total ai.Usage, price *ai.Price, batch bool) {
	run.InputTokens, run.CacheReadTokens, run.CacheWriteTokens = total.InputTokens, total.CacheReadTokens, total.CacheWriteTokens
	run.OutputTokens = total.OutputTokens
	if price != nil {
		c := price.Cost(total, batch)
		run.CostUSD = &c
	}
}

func nonNilRecs(r []ai.Recommendation) []ai.Recommendation {
	if r == nil {
		return []ai.Recommendation{}
	}
	return r
}

// dedupeRecs drops repeated recommendations (overlapping chunks) and orders
// the rest by time.
func dedupeRecs(recs []ai.Recommendation) []ai.Recommendation {
	seen := map[string]bool{}
	var out []ai.Recommendation
	for _, r := range recs {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

func (a *App) writeAIRun(run store.AIRun, plan ai.Plan, recs []ai.Recommendation, extra []ai.Omitted, unprocessed []ai.Range) {
	m := export.AIManifest{RunID: run.ID, SessionID: run.SessionID, Provider: run.Provider, Model: run.Model,
		Delivery: run.Delivery, BatchID: run.BatchID, PromptVersion: plan.PromptVersion, Fingerprint: plan.Fingerprint,
		State: run.State, RequestCount: plan.RequestCount, CompletedRequests: run.CompletedRequests, Attempts: run.Attempts,
		Rejected: run.Rejected, InputTokens: run.InputTokens, CacheReadTokens: run.CacheReadTokens,
		CacheWriteTokens: run.CacheWriteTokens, OutputTokens: run.OutputTokens, CostUSD: run.CostUSD,
		CostCapUSD: run.CostCapUSD, Error: run.ErrorMessage, Ranges: plan.Ranges,
		Omitted: append(append([]ai.Omitted{}, plan.Omitted...), extra...)}
	for _, r := range unprocessed {
		m.Unprocessed = append(m.Unprocessed, export.Range{StartMs: r.StartMs, EndMs: r.EndMs})
	}
	out := make([]export.AIRecommendation, len(recs))
	for i, r := range recs {
		out[i] = export.AIRecommendation(r)
	}
	if err := export.WriteAIRun(filepath.Join(a.exportDir(run.SessionID), "ai", run.ID), m, out); err != nil {
		a.logError("write AI run", err)
	}
}

// AIRunView is a review's progress and result. It is also published on the
// event bus as "ai".
type AIRunView struct {
	ID                string    `json:"id"`
	SessionID         string    `json:"sessionId"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	Delivery          string    `json:"delivery"` // single | batch
	State             string    `json:"state"`
	RequestCount      int       `json:"requestCount"`
	CompletedRequests int       `json:"completedRequests"`
	Attempts          int       `json:"attempts"`
	Rejected          int       `json:"rejected"`
	Recommendations   int       `json:"recommendations"`
	InputTokens       int       `json:"inputTokens"` // all input, cached or not
	CacheReadTokens   int       `json:"cacheReadTokens"`
	CacheWriteTokens  int       `json:"cacheWriteTokens"`
	OutputTokens      int       `json:"outputTokens"`
	CostUSD           *float64  `json:"costUsd"`
	CostCapUSD        *float64  `json:"costCapUsd"`
	WaitingForKey     bool      `json:"waitingForKey"` // a submitted batch needs the key to fetch its results
	ErrorCode         string    `json:"errorCode,omitempty"`
	ErrorMessage      string    `json:"errorMessage,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

func (a *App) publishAI(run store.AIRun, recs []ai.Recommendation) {
	if a.cfg.Bus == nil {
		return
	}
	a.cfg.Bus.Publish(events.Event{Type: "ai", Data: a.aiRunView(run, len(dedupeRecs(recs)))})
}

func (a *App) aiRunView(run store.AIRun, recs int) AIRunView {
	v := AIRunView{ID: run.ID, SessionID: run.SessionID, Provider: run.Provider, Model: run.Model, Delivery: run.Delivery,
		State: run.State, RequestCount: run.RequestCount, CompletedRequests: run.CompletedRequests, Attempts: run.Attempts,
		Rejected: run.Rejected, Recommendations: recs, InputTokens: run.InputTokens + run.CacheReadTokens + run.CacheWriteTokens,
		CacheReadTokens: run.CacheReadTokens, CacheWriteTokens: run.CacheWriteTokens, OutputTokens: run.OutputTokens,
		CostUSD: run.CostUSD, CostCapUSD: run.CostCapUSD, ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage,
		CreatedAt: run.CreatedAt}
	if run.State == store.AIRunning && run.Delivery == store.AIBatch && run.BatchID != "" {
		a.ai.mu.Lock()
		v.WaitingForKey = a.ai.active[run.ID] == nil && a.ai.keys[run.Provider] == ""
		a.ai.mu.Unlock()
	}
	return v
}

func runRecs(run store.AIRun) []ai.Recommendation {
	var recs []ai.Recommendation
	json.Unmarshal(run.RecommendationsJSON, &recs)
	return recs
}

// AIRun returns a review's progress and result.
func (a *App) AIRun(ctx context.Context, id string) (AIRunView, error) {
	run, err := a.cfg.Store.AIRun(ctx, id)
	if err != nil {
		return AIRunView{}, err
	}
	return a.aiRunView(run, len(runRecs(run))), nil
}

// AIRuns lists a session's reviews, newest first.
func (a *App) AIRuns(ctx context.Context, sid string) ([]AIRunView, error) {
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, err
	}
	runs, err := a.cfg.Store.AIRuns(ctx, sid)
	if err != nil {
		return nil, err
	}
	out := []AIRunView{}
	for _, r := range runs {
		out = append(out, a.aiRunView(r, len(runRecs(r))))
	}
	return out, nil
}

// aiTimelineItems are the newest review's recommendations.
func (a *App) aiTimelineItems(ctx context.Context, sid string, watch func(int64) string) (aiItems, error) {
	run, ok, err := a.cfg.Store.LatestAIRun(ctx, sid)
	if err != nil || !ok {
		return aiItems{}, err
	}
	out := aiItems{runID: run.ID}
	for _, r := range runRecs(run) {
		out.items = append(out.items, TimelineItem{ID: SourceAI + ":" + r.ID, Source: SourceAI, OriginID: r.ID,
			AnchorMs: r.StartMs, ClipStartMs: r.StartMs, ClipEndMs: r.EndMs, Title: r.Title, Rationale: r.Rationale,
			Uncertainty: r.Uncertainty, Priority: r.Priority, WatchURL: watch(r.StartMs)})
	}
	return out, nil
}
