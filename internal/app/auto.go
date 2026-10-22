package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// What runs by itself once candidates are found (일반 설정, docs/REDESIGN.md
// 15, 2026-10-22): a collected VOD is transcribed, AI fixes what speech
// recognition misheard, and AI explains every candidate from its chat and
// speech. AI work runs without a preview only when the user turned it on in
// the settings (agreeing there to what it sends and to its limits) or
// pressed "AI 장면 설명 받기" for one session; it stays within the requests
// and cost the settings allow one session. The sample's synthetic chat goes
// only when the user presses for it.

var errAutoBudget = domain.NewUserError("AUTO_LIMIT",
	"이 작업의 자동 AI 검토 한도에 닿아 나머지는 보내지 않았습니다. 설정 > 일반 설정에서 한도를 늘릴 수 있습니다.")

// autoBudget is what one session's automatic AI work may still send.
type autoBudget struct {
	requests int     // requests left
	costUSD  float64 // cost left, for models with a known price
}

// spend takes what a run used out of the budget.
func (b *autoBudget) spend(run store.AIRun) {
	b.requests -= run.Attempts
	if run.CostUSD != nil {
		b.costUSD -= *run.CostUSD
	}
}

// autoBudgetFor is what the session may still send: the settings' limits
// less what its automatic runs used.
func (a *App) autoBudgetFor(ctx context.Context, sid string) (autoBudget, error) {
	s := a.Settings()
	used, err := a.cfg.Store.AIUsageOf(ctx, sid, store.AIOriginAuto)
	if err != nil {
		return autoBudget{}, err
	}
	return autoBudget{requests: s.AutoAIMaxRequests - used.Requests, costUSD: s.AutoAICostCapUSD - used.CostUSD}, nil
}

// autoProvider is the provider automatic work sends to (the default one in
// the settings) with its key, or why it cannot send now.
func (a *App) autoProvider() (ai.Provider, string, error) {
	p, ok := a.cfg.AI.Get(a.Settings().AIProvider)
	if !ok {
		all := a.cfg.AI.All()
		if len(all) == 0 {
			return nil, "", domain.NewUserError("AI_SETUP", "이 실행파일에는 AI 제공자가 없습니다.")
		}
		p = all[0]
	}
	key, err := a.providerKey(p)
	if err != nil {
		return nil, "", domain.NewUserError("AI_SETUP",
			p.Name()+"을(를) 쓸 준비가 되지 않았습니다. 설정 > AI 검토에서 키나 로그인을 확인하세요.")
	}
	return p, key, nil
}

// autoSteps says which steps afterAnalysis takes.
type autoSteps struct {
	transcribe bool // a VOD without a transcript is transcribed
	ai         bool // a fresh transcript is fixed (AutoTranscriptFix) and candidates are explained
	asked      bool // the user pressed for it: the sample goes too
}

// autoStepsFor are the steps the settings turn on.
func (a *App) autoStepsFor() autoSteps {
	s := a.Settings()
	return autoSteps{transcribe: s.AutoTranscribe, ai: s.AutoAI}
}

// afterAnalysis takes, in the job that analysed (or transcribed) a session,
// the steps that run by itself: transcribing a VOD that has none, fixing
// the transcript and explaining the candidates that have no explanation
// yet. It returns what was done and, as notes, the steps that could not
// run and why; only cancellation fails the job. The sample is sent only
// when asked.
func (a *App) afterAnalysis(ctx context.Context, h job, sid string, steps autoSteps) (done, notes []string, err error) {
	st := a.cfg.Store
	s, err := st.Session(ctx, sid)
	if err != nil {
		return nil, nil, err
	}
	note := func(what string, err error) {
		msg := "완료하지 못했습니다"
		if ue, ok := domain.AsUserError(err); ok {
			msg = ue.Message
		} else {
			a.logError(what, err)
		}
		notes = append(notes, what+": "+msg)
	}
	if steps.transcribe && s.Kind == string(platform.KindVOD) && !IsDemo(s) {
		if _, has, err := st.LatestTranscript(ctx, sid); err != nil {
			return nil, nil, err
		} else if !has {
			if err := a.transcribeNow(ctx, h, s); errors.Is(err, context.Canceled) {
				return done, notes, err
			} else if err != nil {
				note("방송 내용 인식", err)
			} else {
				done = append(done, "방송 내용 인식")
			}
		}
	}
	if !steps.ai || (IsDemo(s) && !steps.asked) {
		return done, notes, nil
	}
	p, key, err := a.autoProvider()
	if err != nil {
		note("AI 검토", err)
		return done, notes, nil
	}
	b, err := a.autoBudgetFor(ctx, sid)
	if err != nil {
		return done, notes, err
	}
	if a.Settings().AutoTranscriptFix {
		h.update(func(js *JobStatus) { js.Phase, js.Message = "fixing", "AI가 대본을 다듬는 중입니다." })
		n, err := a.fixTranscript(ctx, sid, p, key, &b, func(i, total int) {
			h.update(func(js *JobStatus) {
				js.Message = fmt.Sprintf("AI가 대본을 다듬는 중입니다 (요청 %d/%d).", i, total)
			})
		})
		switch {
		case errors.Is(err, context.Canceled):
			return done, notes, err
		case err != nil:
			note("대본 다듬기", err)
		case n > 0:
			done = append(done, fmt.Sprintf("AI 대본 다듬기 %d줄", n))
		}
	}
	h.update(func(js *JobStatus) {
		js.Phase, js.Message = "reviewing", "AI가 편집 후보의 장면을 설명하는 중입니다."
	})
	n, err := a.explainAuto(ctx, sid, p, &b, nil, func(i, total int) {
		h.update(func(js *JobStatus) {
			js.Message = fmt.Sprintf("AI가 편집 후보의 장면을 설명하는 중입니다 (%d/%d개).", i, total)
		})
	})
	switch {
	case errors.Is(err, context.Canceled):
		return done, notes, err
	case err != nil:
		note("AI 장면 설명", err)
	case n > 0:
		done = append(done, fmt.Sprintf("AI 장면 설명 %d개", n))
	}
	return done, notes, nil
}

// transcribeNow transcribes a session in the running job, from its media
// file or its platform's stream.
func (a *App) transcribeNow(ctx context.Context, h job, s store.Session) error {
	path, stream, err := a.transcribeSource(ctx, s)
	if err != nil {
		return err
	}
	if a.defaultASR(ctx) == nil {
		return domain.NewUserError("ASR", "쓸 수 있는 음성 인식 엔진이 없어 건너뛰었습니다. 설정의 방송 내용 인식에서 준비 상태를 확인하세요.")
	}
	if stream != nil {
		return a.transcribeStream(ctx, h, s, stream)
	}
	return a.transcribe(ctx, h, s.ID, path)
}

// explainTier orders what to explain first when the limits cannot cover
// everything: strong candidates, then medium ones, viewers' picks, the rest.
func explainTier(it TimelineItem) int {
	if it.Source == SourceViewer {
		return 2
	}
	if it.rule != nil {
		switch it.rule.Strength {
		case "strong":
			return 0
		case "medium", "":
			return 1 // an analysis without strengths (LIVE, older ones) counts each candidate as medium
		}
	}
	return 3
}

// explainAuto explains, in as few runs as the request budget of a plan
// allows, every candidate the session offers that no review explained yet
// (strong ones first) but those in later (timeline item IDs), with the
// explain task's model. It returns how many candidates were sent.
func (a *App) explainAuto(ctx context.Context, sid string, p ai.Provider, b *autoBudget, later map[string]bool,
	report func(done, total int)) (int, error) {
	src, err := a.aiSource(ctx, sid)
	if err != nil {
		return 0, err
	}
	tl, err := a.Timeline(ctx, sid)
	if err != nil {
		return 0, err
	}
	explained := map[string]bool{}
	for _, it := range tl.Items {
		if it.Source == SourceAI && it.Kind != "cut" {
			for _, id := range it.SourceCandidateIDs {
				explained[id] = true
			}
		}
	}
	var todo []TimelineItem
	for _, it := range src.items {
		if !explained[it.ID] && !later[it.ID] {
			todo = append(todo, it)
		}
	}
	if len(todo) == 0 {
		return 0, nil
	}
	sort.SliceStable(todo, func(i, j int) bool {
		if ti, tj := explainTier(todo[i]), explainTier(todo[j]); ti != tj {
			return ti < tj
		}
		return todo[i].AnchorMs < todo[j].AnchorMs
	})
	model := a.TaskModel(p, ai.TaskExplain)
	_, priced := p.(ai.Priced)
	started := time.Now()
	done := 0
	for len(todo) > 0 {
		if b.requests <= 0 || (priced && b.costUSD <= 0) {
			return done, errAutoBudget
		}
		// The candidates of one tier go together; a plan holds as many as its requests allow.
		tier := explainTier(todo[0])
		var ids []string
		for _, it := range todo {
			if explainTier(it) == tier {
				ids = append(ids, it.ID)
			}
		}
		pv, err := a.autoPreview(ctx, sid, p, model, ids, b, priced)
		if err != nil {
			return done, err
		}
		covered := map[string]bool{}
		for _, c := range pv.Plan.Chunks {
			for _, cand := range c.Candidates {
				covered[cand.ID] = true
			}
		}
		if len(covered) == 0 {
			a.dropPreview(pv.ID)
			return done, errAutoBudget
		}
		runID, err := a.startAuto(ctx, pv)
		a.dropPreview(pv.ID)
		if err != nil {
			return done, err
		}
		if err := a.awaitRun(ctx, runID); err != nil {
			return done, err
		}
		run, err := a.cfg.Store.AIRun(context.WithoutCancel(ctx), runID)
		if err != nil {
			return done, err
		}
		if !run.CreatedAt.Before(started) { // a completed run of the same plan was reused: nothing was sent
			b.spend(run)
		}
		if run.State == store.AIFailed || run.State == store.AICancelled {
			return done, domain.NewUserError(run.ErrorCode, run.ErrorMessage)
		}
		rest := todo[:0]
		for _, it := range todo {
			if !covered[it.ID] {
				rest = append(rest, it)
			}
		}
		done += len(todo) - len(rest)
		todo = rest
		if report != nil {
			report(done, done+len(todo))
		}
	}
	return done, nil
}

// autoPreview prepares the review of ids within the budget: with the cost
// left as its cap (a priced model), and halved until its requests fit.
func (a *App) autoPreview(ctx context.Context, sid string, p ai.Provider, model string, ids []string, b *autoBudget, priced bool) (AIPreview, error) {
	req := AIPreviewRequest{SessionID: sid, Provider: p.ID(), Model: model, ItemIDs: ids, Delivery: ai.DeliverySingle}
	for {
		req.CostCapUSD = 0
		if priced {
			if pr, ok := p.(ai.Priced); ok {
				if _, known := pr.Price(model); known {
					req.CostCapUSD = min(b.costUSD, aiMaxCostCapUSD)
				}
			}
		}
		pv, err := a.PreviewAI(ctx, req)
		if ue, ok := domain.AsUserError(err); ok && ue.Code == "COST_CAP" {
			return AIPreview{}, errAutoBudget
		}
		if err != nil {
			return AIPreview{}, err
		}
		if pv.Plan.RequestCount <= b.requests {
			return pv, nil
		}
		a.dropPreview(pv.ID)
		if len(req.ItemIDs) == 1 {
			return AIPreview{}, errAutoBudget
		}
		req.ItemIDs = req.ItemIDs[:len(req.ItemIDs)/2]
	}
}

// startAuto starts an automatic run of a preview, after a run of the
// user's that sends one request at a time (only one goes at once).
func (a *App) startAuto(ctx context.Context, pv AIPreview) (string, error) {
	for {
		id, err := a.startAI(ctx, pv.ID, pv.Plan.Fingerprint, false, store.AIOriginAuto)
		if ue, ok := domain.AsUserError(err); ok && ue.Code == "BUSY" && a.singleRunActive() {
			if err := sleepCtx(ctx, 2*time.Second); err != nil {
				return "", err
			}
			continue
		}
		return id, err
	}
}

// dropPreview forgets a preview nobody will look at (automatic work makes
// its own and must not crowd out the user's).
func (a *App) dropPreview(id string) {
	a.ai.mu.Lock()
	delete(a.ai.previews, id)
	a.ai.mu.Unlock()
}

// StartAIReview runs, for one session the user picked, what the automatic
// AI work does (the user pressed "AI 장면 설명 받기", agreeing to send its
// chat and transcript within the settings' limits): a missing transcript
// is made first when the settings transcribe by themselves.
func (a *App) StartAIReview(ctx context.Context, sid string) (JobStatus, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return JobStatus{}, err
	}
	if _, _, err := a.autoProvider(); err != nil {
		return JobStatus{}, err
	}
	return a.jobs.start(a.ctx, "review", func(ctx context.Context, h job) error {
		h.update(func(js *JobStatus) { js.SessionID, js.Title, js.DurationMs = s.ID, s.Title, s.DurationMs })
		return a.runAfter(ctx, h, sid, autoSteps{transcribe: a.Settings().AutoTranscribe, ai: true, asked: true}, "AI 검토가 끝났습니다.")
	})
}

// runAfter takes the automatic steps at the end of a job and sets its last
// message from what the job and the steps did.
func (a *App) runAfter(ctx context.Context, h job, sid string, steps autoSteps, message string) error {
	done, notes, err := a.afterAnalysis(ctx, h, sid, steps)
	if err != nil {
		return err
	}
	finish(h, message, done, notes)
	return nil
}

// finish sets a job's last message: what the job did, the automatic steps
// that ran ("…까지 마쳤습니다"), and the steps that were skipped, with
// why, as its warning.
func finish(h job, message string, done, notes []string) {
	h.update(func(js *JobStatus) {
		js.Message = message
		if len(done) > 0 {
			js.Message += " " + strings.Join(done, ", ") + "까지 마쳤습니다."
		}
		for _, n := range notes {
			if js.Warning != "" {
				js.Warning += " "
			}
			js.Warning += n + "."
		}
	})
}

// StartCut runs a cut analysis of one candidate now: the user pressed its
// "AI 컷 분석" (or a result box's "다시 분석", continuing planID with the
// notes left on its versions), so it is sent without a preview, with the
// default provider's model for the cut task. A re-analysis is always sent
// again, even when nothing about the candidate changed.
func (a *App) StartCut(ctx context.Context, sid, itemID, planID string) (string, error) {
	p, _, err := a.autoProvider()
	if err != nil {
		return "", err
	}
	req := AIPreviewRequest{SessionID: sid, Provider: p.ID(), Model: a.TaskModel(p, ai.TaskCut), Kind: store.AIKindCut,
		ItemIDs: []string{itemID}, Delivery: ai.DeliverySingle, ConfirmSearches: a.Settings().CutConfirmSearches}
	if planID != "" {
		req.ContinuePlans = map[string]string{itemID: planID}
	}
	pv, err := a.PreviewAI(ctx, req)
	if err != nil {
		return "", err
	}
	defer a.dropPreview(pv.ID)
	return a.startAI(ctx, pv.ID, pv.Plan.Fingerprint, planID != "", store.AIOriginUser)
}
