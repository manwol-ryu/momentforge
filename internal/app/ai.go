package app

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// AI limits (CzzEdit's).
const (
	aiPreviewTTL     = 10 * time.Minute
	aiMaxPreviews    = 20
	aiTriesPerChunk  = 3
	aiMaxRetryWait   = 120 * time.Second
	aiMaxModelLength = 200
)

// aiState keeps API keys and previews in memory only; keys are never
// written to the database, files or logs.
type aiState struct {
	mu       sync.Mutex
	keys     map[string]string // provider ID → key
	previews map[string]*aiPreview
	active   map[string]context.CancelFunc // run ID → cancel; at most one
	wg       sync.WaitGroup
}

type aiPreview struct {
	plan    ai.Plan
	expires time.Time
	runID   string
}

func (a *App) initAI() {
	a.ai.keys, a.ai.previews, a.ai.active = map[string]string{}, map[string]*aiPreview{}, map[string]context.CancelFunc{}
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
	ID         string
	Name       string
	Configured bool // a key is in memory
}

// AIProviders lists providers and whether each has a key.
func (a *App) AIProviders() []AIProvider {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	var out []AIProvider
	for _, p := range a.cfg.AI.All() {
		out = append(out, AIProvider{ID: p.ID(), Name: p.Name(), Configured: a.ai.keys[p.ID()] != ""})
	}
	return out
}

// SetAIKey keeps (or, when empty, forgets) a provider's key in memory.
func (a *App) SetAIKey(provider, key string) (bool, error) {
	if _, ok := a.cfg.AI.Get(provider); !ok {
		return false, domain.NewUserError("INPUT", "AI 제공자를 선택해 주세요.")
	}
	if len(key) > 1024 || strings.ContainsAny(key, "\r\n") {
		return false, domain.NewUserError("INPUT", "API 키 형식을 확인해 주세요.")
	}
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	if key = strings.TrimSpace(key); key == "" {
		delete(a.ai.keys, provider)
	} else {
		a.ai.keys[provider] = key
	}
	return key != "", nil
}

// AIPreviewRequest says what to review: timeline items, or a chat range.
type AIPreviewRequest struct {
	SessionID string
	Provider  string
	Model     string
	ItemIDs   []string // unified timeline item IDs (not AI ones); nil for a range
	StartMs   int64    // range mode
	EndMs     int64
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
	if _, ok := a.cfg.AI.Get(req.Provider); !ok {
		return AIPreview{}, domain.NewUserError("INPUT", "AI 제공자를 선택해 주세요.")
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
	in := ai.PlanInput{SessionID: req.SessionID, Provider: req.Provider, Model: req.Model, Comments: map[string]ai.Evidence{},
		Chats: func(from, to int64) ([]domain.ChatEvent, error) {
			chats, _, err := a.cfg.Store.ChatsBetween(ctx, req.SessionID, from, to, maxSeq, 1_000_000, 0)
			return chats, err
		}}
	if req.ItemIDs != nil {
		if len(req.ItemIDs) == 0 {
			return AIPreview{}, domain.NewUserError("INPUT", "검토할 후보를 선택해 주세요.")
		}
		want := map[string]bool{}
		for _, id := range req.ItemIDs {
			want[id] = true
		}
		for _, it := range tl.Items {
			if !want[it.ID] || it.Source == SourceAI {
				continue
			}
			delete(want, it.ID)
			c := ai.Candidate{ID: it.ID, SourceType: it.Source, AnchorMs: it.AnchorMs, Title: it.Title, Statistics: stats[it.ID]}
			in.Candidates = append(in.Candidates, c)
			start := it.ClipStartMs
			if it.Source == SourceViewer {
				start = max(0, it.AnchorMs-30000)
				in.Comments[it.ID] = ai.Evidence{Ref: "comment:" + it.OriginID, TimeMs: it.AnchorMs, Content: it.CommentText}
			}
			in.Ranges = append(in.Ranges, ai.Range{StartMs: start, EndMs: it.ClipEndMs})
		}
		if len(want) > 0 {
			return AIPreview{}, domain.NewUserError("INPUT", "선택한 원본 후보를 찾을 수 없습니다.")
		}
	} else {
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

// StartAI sends a confirmed preview. The fingerprint must match what the
// user saw; the same completed plan is reused unless regenerate is set.
func (a *App) StartAI(ctx context.Context, previewID, fingerprint string, regenerate bool) (string, error) {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	pv := a.ai.previews[previewID]
	if pv == nil || pv.expires.Before(time.Now()) || pv.plan.Fingerprint != fingerprint {
		return "", domain.NewUserError("PREVIEW", "미리보기가 만료되었거나 입력이 변경되었습니다.")
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
	if len(a.ai.active) > 0 {
		return "", domain.NewUserError("BUSY", "진행 중인 AI 작업을 먼저 마치거나 중단해 주세요.")
	}
	provider, _ := a.cfg.AI.Get(plan.Provider)
	run, err := a.cfg.Store.CreateAIRun(ctx, store.AIRun{SessionID: plan.SessionID, Provider: plan.Provider,
		Model: plan.Model, Fingerprint: plan.Fingerprint, PlanJSON: ai.Encode(plan)})
	if err != nil {
		return "", err
	}
	pv.runID = run.ID
	runCtx, cancel := context.WithCancel(a.ctx)
	a.ai.active[run.ID] = cancel
	a.ai.wg.Add(1)
	go func() {
		defer a.ai.wg.Done()
		defer cancel()
		a.runAI(runCtx, run, plan, provider, key)
		a.ai.mu.Lock()
		delete(a.ai.active, run.ID)
		a.ai.mu.Unlock()
	}()
	a.publishAI(run, plan, nil)
	return run.ID, nil
}

// CancelAI asks a running review to stop after the current request.
func (a *App) CancelAI(id string) {
	a.ai.mu.Lock()
	defer a.ai.mu.Unlock()
	if cancel := a.ai.active[id]; cancel != nil {
		cancel()
	}
}

func (a *App) stopAI(ctx context.Context) {
	a.ai.mu.Lock()
	for _, cancel := range a.ai.active {
		cancel()
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
// and they are never re-sent automatically.
func (a *App) RecoverAI(ctx context.Context) error {
	_, err := a.cfg.Store.MarkAIRunsInterrupted(ctx, "앱이 종료되어 전송 결과가 불명확합니다. 자동 재실행하지 않습니다.")
	return err
}

var errAICancelled = domain.NewUserError("CANCELLED", "AI 작업을 중단했습니다.")

// runAI sends the chunks in order (CzzEdit ai.Jobs.run): up to 3 tries per
// chunk for rate limits and server errors, 20 requests in all.
func (a *App) runAI(ctx context.Context, run store.AIRun, plan ai.Plan, provider ai.Provider, key string) {
	bg := context.WithoutCancel(ctx)
	var recs []ai.Recommendation
	var usage []ai.Usage
	var extraOmitted []ai.Omitted
	save := func() {
		run.RecommendationsJSON, _ = json.Marshal(nonNilRecs(recs))
		run.UsageJSON, _ = json.Marshal(usage)
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
			var out json.RawMessage
			var u ai.Usage
			for try := 0; ; try++ {
				run.Attempts++
				var err error
				if out, u, err = provider.Review(ctx, key, plan.Model, chunk); err == nil {
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
			usage = append(usage, u)
			run.CompletedRequests++
			save()
			a.publishAI(run, plan, recs)
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
	// Files first: once the end state is visible, the exports are complete.
	a.writeAIRun(run, plan, recs, usage, extraOmitted)
	save()
	a.writeUnified(bg, run.SessionID)
	a.publishAI(run, plan, recs)
}

func nonNilRecs(r []ai.Recommendation) []ai.Recommendation {
	if r == nil {
		return []ai.Recommendation{}
	}
	return r
}

func dedupeRecs(recs []ai.Recommendation) []ai.Recommendation {
	seen := map[string]bool{}
	var out []ai.Recommendation
	for _, r := range recs {
		if !seen[r.ID] {
			seen[r.ID] = true
			out = append(out, r)
		}
	}
	return out
}

func (a *App) writeAIRun(run store.AIRun, plan ai.Plan, recs []ai.Recommendation, usage []ai.Usage, extra []ai.Omitted) {
	m := export.AIManifest{RunID: run.ID, SessionID: run.SessionID, Provider: run.Provider, Model: run.Model,
		PromptVersion: plan.PromptVersion, Fingerprint: plan.Fingerprint, State: run.State, RequestCount: plan.RequestCount,
		CompletedRequests: run.CompletedRequests, Attempts: run.Attempts, Rejected: run.Rejected, Error: run.ErrorMessage,
		Ranges: plan.Ranges, Omitted: append(append([]ai.Omitted{}, plan.Omitted...), extra...)}
	for _, u := range usage {
		m.InputTokens += u.InputTokens
		m.OutputTokens += u.OutputTokens
	}
	for _, c := range plan.Chunks[min(run.CompletedRequests, len(plan.Chunks)):] {
		m.Unprocessed = append(m.Unprocessed, export.Range{StartMs: c.StartMs, EndMs: c.EndMs})
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
	ID                string `json:"id"`
	SessionID         string `json:"sessionId"`
	Provider          string `json:"provider"`
	Model             string `json:"model"`
	State             string `json:"state"`
	RequestCount      int    `json:"requestCount"`
	CompletedRequests int    `json:"completedRequests"`
	Attempts          int    `json:"attempts"`
	Rejected          int    `json:"rejected"`
	Recommendations   int    `json:"recommendations"`
	InputTokens       int    `json:"inputTokens"`
	OutputTokens      int    `json:"outputTokens"`
	ErrorCode         string `json:"errorCode,omitempty"`
	ErrorMessage      string `json:"errorMessage,omitempty"`
}

func (a *App) publishAI(run store.AIRun, plan ai.Plan, recs []ai.Recommendation) {
	if a.cfg.Bus == nil {
		return
	}
	a.cfg.Bus.Publish(events.Event{Type: "ai", Data: aiRunView(run, plan.RequestCount, len(dedupeRecs(recs)), nil)})
}

func aiRunView(run store.AIRun, requests, recs int, usage []ai.Usage) AIRunView {
	v := AIRunView{ID: run.ID, SessionID: run.SessionID, Provider: run.Provider, Model: run.Model, State: run.State,
		RequestCount: requests, CompletedRequests: run.CompletedRequests, Attempts: run.Attempts, Rejected: run.Rejected,
		Recommendations: recs, ErrorCode: run.ErrorCode, ErrorMessage: run.ErrorMessage}
	for _, u := range usage {
		v.InputTokens += u.InputTokens
		v.OutputTokens += u.OutputTokens
	}
	return v
}

// AIRun returns a review's progress and result.
func (a *App) AIRun(ctx context.Context, id string) (AIRunView, error) {
	run, err := a.cfg.Store.AIRun(ctx, id)
	if err != nil {
		return AIRunView{}, err
	}
	var plan ai.Plan
	var recs []ai.Recommendation
	var usage []ai.Usage
	json.Unmarshal(run.PlanJSON, &plan)
	json.Unmarshal(run.RecommendationsJSON, &recs)
	json.Unmarshal(run.UsageJSON, &usage)
	return aiRunView(run, plan.RequestCount, len(recs), usage), nil
}

// aiTimelineItems are the newest review's recommendations.
func (a *App) aiTimelineItems(ctx context.Context, sid string, watch func(int64) string) (aiItems, error) {
	run, ok, err := a.cfg.Store.LatestAIRun(ctx, sid)
	if err != nil || !ok {
		return aiItems{}, err
	}
	var recs []ai.Recommendation
	json.Unmarshal(run.RecommendationsJSON, &recs)
	out := aiItems{runID: run.ID}
	for _, r := range recs {
		out.items = append(out.items, TimelineItem{ID: SourceAI + ":" + r.ID, Source: SourceAI, OriginID: r.ID,
			AnchorMs: r.StartMs, ClipStartMs: r.StartMs, ClipEndMs: r.EndMs, Title: r.Title, Rationale: r.Rationale,
			Uncertainty: r.Uncertainty, Priority: r.Priority, WatchURL: watch(r.StartMs)})
	}
	return out, nil
}
