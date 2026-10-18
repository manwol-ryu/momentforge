package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// The cut analysis (docs/AI-CUT-ANALYSIS.md). Phase 6-A: one candidate at a
// time, read from a window around its clip, no searching yet (the search
// requests a model makes are recorded, not executed).
const (
	cutWindowPadMs    = 60_000 // read this much before and after the clip
	cutMaxChats       = 400    // per window
	cutMaxUtterances  = 200
	cutMaxReplans     = 1 // a plan that fails a check is redone once with the reasons
	cutMaxOutputStep  = 4000
	cutPlannedPerCand = 4 // steps without a re-plan
)

// cutPreview is what a cut-analysis preview will send: one input per candidate.
type cutPreview struct {
	Inputs []cut.Input `json:"inputs"`
}

// CutPreview is the preview's cut part for the API (inputs and the budget).
type CutPreview struct {
	Inputs               []cut.Input `json:"inputs"`
	RequestsPerCandidate int         `json:"requestsPerCandidate"`
	PlannedPerCandidate  int         `json:"plannedPerCandidate"`
	SearchExecuted       bool        `json:"searchExecuted"` // false until phase 6-C
	HasTranscript        bool        `json:"hasTranscript"`
	UtteranceCount       int         `json:"utteranceCount"`
	ChatCount            int         `json:"chatCount"`
	EvidenceBytes        int         `json:"evidenceBytes"`
}

// previewCut builds the preview of a cut analysis of the chosen timeline
// items. The provider must send steps; every item is analysed on its own.
func (a *App) previewCut(ctx context.Context, req AIPreviewRequest, provider ai.Provider, price *ai.Price) (ai.Plan, *CutPreview, error) {
	if _, ok := provider.(ai.StepProvider); !ok {
		return ai.Plan{}, nil, domain.NewUserError("INPUT", provider.Name()+"은(는) 아직 컷 분석을 지원하지 않습니다. 다른 제공자를 고르세요.")
	}
	if req.All || req.ItemIDs == nil {
		return ai.Plan{}, nil, domain.NewUserError("INPUT", "컷 분석은 후보 목록에서 고른 후보 단위로 합니다. 후보를 고른 뒤 다시 미리보기를 만드세요.")
	}
	if len(req.ItemIDs) == 0 {
		return ai.Plan{}, nil, domain.NewUserError("INPUT", "분석할 후보를 선택해 주세요.")
	}
	if req.Delivery == ai.DeliveryBatch {
		return ai.Plan{}, nil, domain.NewUserError("INPUT", "컷 분석은 단계가 앞 단계 결과에 의존해 배치로 보낼 수 없습니다.")
	}
	src, err := a.aiSource(ctx, req.SessionID)
	if err != nil {
		return ai.Plan{}, nil, err
	}
	s, err := a.cfg.Store.Session(ctx, req.SessionID)
	if err != nil {
		return ai.Plan{}, nil, err
	}
	transcript, hasTranscript, err := a.cfg.Store.LatestTranscript(ctx, req.SessionID)
	if err != nil {
		return ai.Plan{}, nil, err
	}
	byID := map[string]TimelineItem{}
	for _, it := range src.items {
		byID[it.ID] = it
	}
	pv := &CutPreview{RequestsPerCandidate: cut.RequestsPerCandidate, PlannedPerCandidate: cutPlannedPerCand, HasTranscript: hasTranscript}
	plan := ai.Plan{SessionID: req.SessionID, Kind: store.AIKindCut, Provider: req.Provider, Model: req.Model, Delivery: ai.DeliverySingle,
		PromptVersion: cut.PipelineVersion, Prompt: cut.PromptAnalyze, Chunks: []ai.Chunk{}, Omitted: []ai.Omitted{},
		Price: price, CostCapUSD: req.CostCapUSD,
		EstimateNote: "입력 토큰은 UTF-8 바이트 수로 잡은 상한이며 실제 토큰 수는 보통 더 적습니다. 후보마다 분석·계획·검수 2회의 네 단계를 보내고, " +
			"검수에 걸리면 계획과 검수를 한 번 더 보냅니다(최대 7회). 이 단계에서는 읽은 범위 밖을 검색하지 않습니다."}
	seen := map[string]bool{}
	for _, id := range req.ItemIDs {
		it, ok := byID[id]
		if !ok {
			if src.pending[id] {
				return ai.Plan{}, nil, domain.NewUserError("INPUT", "아직 반응이 이어지는 후보는 보낼 수 없습니다. 후보가 확정되면 다시 미리보기를 만들어 주세요.")
			}
			return ai.Plan{}, nil, domain.NewUserError("INPUT", "선택한 원본 후보를 찾을 수 없습니다.")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		in, err := a.cutInput(ctx, s, it, src, transcript.ID, hasTranscript)
		if err != nil {
			return ai.Plan{}, nil, err
		}
		pv.Inputs = append(pv.Inputs, in)
		plan.Ranges = append(plan.Ranges, ai.Range{StartMs: in.WindowStartMs, EndMs: in.WindowEndMs})
		est := ai.StepInputEstimate(cut.AnalyzeStep(in))
		for _, e := range in.Evidence {
			pv.EvidenceBytes += len(e.Text)
			if e.Kind == cut.KindUtterance {
				pv.UtteranceCount++
			} else if e.Kind == cut.KindChat {
				pv.ChatCount++
			}
		}
		// The plan step carries the analysis too, and a re-plan repeats plan and checks.
		plan.PlannedInputEstimate += est * cutPlannedPerCand
		plan.InputUpperEstimate += est * cut.RequestsPerCandidate
		plan.RequestCount += cut.RequestsPerCandidate
		if price != nil {
			plan.CostUpperUSD += price.UpperCost(est, cutMaxOutputStep, false) * cut.RequestsPerCandidate
		}
	}
	if len(pv.Inputs) == 0 {
		return ai.Plan{}, nil, domain.NewUserError("INPUT", "분석할 후보를 선택해 주세요.")
	}
	plan.OutputTokenLimit = plan.RequestCount * cutMaxOutputStep
	plan.AttemptLimit = plan.RequestCount + len(pv.Inputs)*2
	if req.CostCapUSD > 0 && plan.CostUpperUSD > req.CostCapUSD {
		return ai.Plan{}, nil, domain.NewUserError("COST_CAP",
			fmt.Sprintf("컷 분석의 최대 비용(%.2f달러)이 한도를 넘습니다. 후보를 줄이거나 한도를 올려 주세요.", plan.CostUpperUSD))
	}
	plan.Fingerprint = ai.Digest(struct {
		Plan   ai.Plan     `json:"plan"`
		Inputs []cut.Input `json:"inputs"`
	}{plan, pv.Inputs})
	return plan, pv, nil
}

// cutInput reads the window around one timeline item: its utterances (when
// the session has a transcript), its chat and, for a viewer item, the comment.
func (a *App) cutInput(ctx context.Context, s store.Session, it TimelineItem, src aiSource, transcriptID string, hasTranscript bool) (cut.Input, error) {
	from := max(0, it.ClipStartMs-cutWindowPadMs)
	to := min(src.endMs, it.ClipEndMs+cutWindowPadMs)
	if to <= from {
		to = min(src.endMs, from+cutWindowPadMs)
	}
	in := cut.Input{WindowStartMs: from, WindowEndMs: to, DurationMs: s.DurationMs, Sources: []string{"chat"}}
	in.Candidates = []cut.Candidate{{ID: it.ID, SourceType: it.Source, AnchorMs: it.AnchorMs, ClipStartMs: it.ClipStartMs,
		ClipEndMs: it.ClipEndMs, Title: it.Title, Statistics: src.stats[it.ID]}}
	if hasTranscript {
		in.Sources = append([]string{"utterances"}, in.Sources...)
		utts, err := a.cfg.Store.UtterancesBetween(ctx, transcriptID, from, to, cutMaxUtterances)
		if err != nil {
			return in, err
		}
		for _, u := range utts {
			in.Evidence = append(in.Evidence, cut.Evidence{Ref: "utt:" + u.ID, Kind: cut.KindUtterance, StartMs: u.StartMs, EndMs: u.EndMs,
				Speaker: u.Speaker, Text: u.Text})
		}
	}
	chats, _, err := a.cfg.Store.ChatsBetween(ctx, s.ID, from, to, src.maxSeq, cutMaxChats, 0)
	if err != nil {
		return in, err
	}
	for _, c := range chats {
		e := cut.Evidence{Ref: "chat:" + c.ID, Kind: cut.KindChat, StartMs: c.OffsetMs, Text: c.Text}
		if c.AuthorKey != "" {
			author := c.AuthorKey
			e.Author = &author
		}
		in.Evidence = append(in.Evidence, e)
	}
	if it.Source == SourceViewer && it.CommentText != "" {
		in.Sources = append(in.Sources, "comment")
		in.Evidence = append(in.Evidence, cut.Evidence{Ref: "comment:" + it.OriginID, Kind: cut.KindComment, StartMs: it.AnchorMs, Text: it.CommentText})
	}
	sort.SliceStable(in.Evidence, func(i, j int) bool { return in.Evidence[i].StartMs < in.Evidence[j].StartMs })
	if in.Evidence == nil {
		in.Evidence = []cut.Evidence{}
	}
	return in, nil
}

// sentLogEntry records what one step sent (the run's sent_log).
type sentLogEntry struct {
	Candidate string   `json:"candidate"`
	Step      string   `json:"step"`
	Refs      int      `json:"refs"`
	Bytes     int      `json:"bytes"`
	NewRefs   []string `json:"new_refs,omitempty"` // refs first sent in this step (searches, from 6-C)
	At        string   `json:"at"`
}

// runCut analyses each candidate in turn: analyze → plan → check → the two
// verifications, with one re-plan when a check fails. Every plan is saved,
// trusted or not, with the reasons.
func (a *App) runCut(act *aiActive, run store.AIRun, plan ai.Plan, inputs []cut.Input, provider ai.StepProvider, key string) {
	ctx := act.ctx
	bg := context.WithoutCancel(ctx)
	var usage []ai.Usage
	var total ai.Usage
	var sentLog []sentLogEntry
	var planIDs []string
	save := func() {
		run.RecommendationsJSON, _ = json.Marshal(planIDs)
		run.UsageJSON, _ = json.Marshal(usage)
		run.SentLogJSON, _ = json.Marshal(sentLog)
		setUsage(&run, total, plan.Price, false)
		if err := a.cfg.Store.SaveAIRun(bg, run); err != nil {
			a.logError("save AI run", err)
		}
	}
	send := func(candidate string, step ai.StepRequest) (json.RawMessage, error) {
		if ctx.Err() != nil {
			return nil, errAICancelled
		}
		if run.Attempts >= plan.AttemptLimit {
			return nil, domain.NewUserError("REQUEST_BUDGET", "요청 횟수 한도에 닿아 남은 단계를 보내지 않았습니다.")
		}
		est := ai.StepInputEstimate(step)
		if plan.CostCapUSD > 0 && plan.Price != nil &&
			plan.Price.Cost(total, false)+plan.Price.UpperCost(est, step.OutputLimit(), false) > plan.CostCapUSD {
			return nil, errAICostCap
		}
		sentLog = append(sentLog, sentLogEntry{Candidate: candidate, Step: step.Name, Refs: countRefs(step), Bytes: len(step.InputJSON()),
			At: time.Now().UTC().Format(time.RFC3339)})
		for try := 0; ; try++ {
			run.Attempts++
			out, u, err := provider.Step(ctx, key, plan.Model, step)
			total = total.Add(u)
			if err == nil {
				usage = append(usage, u)
				run.CompletedRequests++
				save()
				a.publishAI(run, nil)
				return out, nil
			}
			if ctx.Err() != nil {
				return nil, errAICancelled
			}
			var pe *ai.ProviderError
			if !errors.As(err, &pe) || !pe.Retryable || try == aiTriesPerChunk-1 || run.Attempts >= plan.AttemptLimit || pe.RetryAfter > aiMaxRetryWait {
				return nil, err
			}
			if sleepCtx(ctx, max(pe.RetryAfter, time.Duration(1<<try)*time.Second)) != nil {
				return nil, errAICancelled
			}
		}
	}

	var failure error
	done := 0
	for _, in := range inputs {
		p, err := a.analyzeCandidate(ctx, run, plan, in, send)
		if err != nil {
			failure = err
			break
		}
		rec, err := a.saveCutPlan(bg, run, p)
		if err != nil {
			a.logError("save cut plan", err)
			failure = err
			break
		}
		planIDs = append(planIDs, rec.ID)
		done++
		save()
		a.writeUnified(bg, run.SessionID)
		a.publishAI(run, nil)
	}
	switch {
	case failure != nil:
		run.ErrorCode, run.ErrorMessage = "AI_ERROR", "AI 작업을 완료하지 못했습니다."
		if ue, ok := domain.AsUserError(failure); ok {
			run.ErrorCode, run.ErrorMessage = ue.Code, ue.Message
		}
		run.State = store.AIFailed
		if run.ErrorCode == "CANCELLED" {
			run.State = store.AICancelled
		} else if done > 0 {
			run.State = store.AIPartial
		}
	default:
		run.State = store.AICompleted
	}
	setUsage(&run, total, plan.Price, false)
	save()
	a.writeUnified(bg, run.SessionID)
	a.publishAI(run, nil)
}

func countRefs(step ai.StepRequest) int {
	switch in := step.Input.(type) {
	case cut.Input:
		return len(in.Evidence)
	case map[string]any:
		if i, ok := in["input"].(cut.Input); ok {
			return len(i.Evidence)
		}
		n := 0
		for _, k := range []string{"selected", "context"} {
			if ev, ok := in[k].([]cut.Evidence); ok {
				n += len(ev)
			}
		}
		if spans, ok := in["spans"].([]cut.ClipSpan); ok {
			for _, s := range spans {
				n += len(s.Evidence)
			}
		}
		return n
	}
	return 0
}

// analyzeCandidate runs the steps for one input and builds its plan.
func (a *App) analyzeCandidate(ctx context.Context, run store.AIRun, plan ai.Plan, in cut.Input,
	send func(string, ai.StepRequest) (json.RawMessage, error)) (cut.Plan, error) {
	cand := in.Candidates[0].ID
	known := cut.Known(in.Evidence)
	out := cut.Plan{RunID: run.ID, SessionID: run.SessionID, State: cut.StateFailed, Sources: in.Sources,
		SearchedRanges: []cut.SearchRequest{}, UnresolvedQuestions: []string{}, UserNotes: []cut.UserNote{},
		AnalysisEvidenceRefs: []string{}, RequiredContextRefs: []string{}, OptionalContextRefs: []string{}, SelectedSpans: []cut.Span{}}
	for _, c := range in.Candidates {
		out.CandidateIDs = append(out.CandidateIDs, c.ID)
	}
	out.Reaction.PeakMs = in.Candidates[0].AnchorMs
	out.AnalysisVersion = cut.Version{Pipeline: cut.PipelineVersion, Prompts: cut.PromptVersions, Provider: plan.Provider,
		Model: plan.Model, Sources: in.Sources}
	out.Verification = cut.Verification{EvidenceMatch: "all_read", Issues: []string{}}

	raw, err := send(cand, cut.AnalyzeStep(in))
	if err != nil {
		return out, err
	}
	an, dropped, err := cut.ParseAnalysis(raw, known)
	if err != nil {
		return out, err
	}
	out.Reaction.Target, out.Causes, out.CauseStatus = an.Target, an.Causes, an.CauseStatus
	out.UnresolvedQuestions = append(out.UnresolvedQuestions, an.Questions...)
	// Searches are not executed in this phase; what the model wanted is kept for the record.
	out.SearchedRanges = an.SearchRequests
	if dropped > 0 {
		out.Verification.EvidenceMatch = "some_unread"
	}

	var issues []string
	var answer cut.PlanAnswer
	var checked cut.Checked
	for attempt := 0; attempt <= cutMaxReplans; attempt++ {
		raw, err = send(cand, cut.PlanStep(in, an, issues, nil))
		if err != nil {
			return out, err
		}
		answer, err = cut.ParsePlan(raw)
		if err != nil {
			return out, err
		}
		checked, issues = cut.CheckPlan(&answer, known, in.DurationMs)
		if checked.Dropped > 0 {
			out.Verification.EvidenceMatch = "some_unread"
		}
		if len(issues) > 0 || len(checked.Spans) == 0 {
			if len(checked.Spans) == 0 && len(issues) == 0 {
				issues = []string{"고른 후보의 span을 시각으로 바꾸지 못했습니다"}
			}
			continue // a structural problem: re-plan with the reasons, or give up after the retries
		}
		clipRaw, err := send(cand, cut.VerifyClipStep(an.Target, checked.Spans, in.Evidence))
		if err != nil {
			return out, err
		}
		clip, err := cut.ParseClipVerification(clipRaw)
		if err != nil {
			return out, err
		}
		srcRaw, err := send(cand, cut.VerifySourceStep(an.Target, answer.Chosen, checked.Spans, answer.RequiredContextRefs, answer.Ending, in.Evidence))
		if err != nil {
			return out, err
		}
		src, err := cut.ParseSourceVerification(srcRaw)
		if err != nil {
			return out, err
		}
		out.Verification.ClipOnly, out.Verification.AgainstSource = &clip, &src
		issues = cut.Issues(&clip, &src)
		if len(issues) == 0 {
			break
		}
	}
	fillPlan(&out, answer, checked, known, issues)
	return out, nil
}

// fillPlan copies the (last) answer into the plan and resolves its times.
func fillPlan(out *cut.Plan, p cut.PlanAnswer, checked cut.Checked, known map[string]cut.Evidence, issues []string) {
	out.AnalysisEvidenceRefs = append(out.AnalysisEvidenceRefs, p.AnalysisEvidenceRefs...)
	out.RequiredContextRefs = append(out.RequiredContextRefs, p.RequiredContextRefs...)
	out.OptionalContextRefs = append(out.OptionalContextRefs, p.OptionalContextRefs...)
	out.SelectedSpans = checked.Spans
	if out.SelectedSpans == nil {
		out.SelectedSpans = []cut.Span{}
	}
	out.Boundaries.Candidates, out.Boundaries.Chosen = p.Candidates, p.Chosen
	out.Boundaries.StartReason, out.Boundaries.EndReason = p.StartReason, p.EndReason
	out.Boundaries.Adjustments = checked.Adjustments
	if out.Boundaries.Adjustments == nil {
		out.Boundaries.Adjustments = []cut.Adjustment{}
	}
	if out.Boundaries.Candidates == nil {
		out.Boundaries.Candidates = []cut.BoundaryCandidate{}
	}
	out.Ending = p.Ending
	if out.Ending.Checks == nil {
		out.Ending.Checks = []string{}
	}
	out.UnresolvedQuestions = append(out.UnresolvedQuestions, p.UnresolvedQuestions...)
	out.Times.StoryOriginRef, out.Times.OriginConfidence = p.StoryOriginRef, p.OriginConfidence
	if e, ok := known[p.StoryOriginRef]; ok {
		out.Times.StoryOriginMs = cut.StartOf(e)
	}
	out.Times.EventStartRef = p.EventStartRef
	if e, ok := known[p.EventStartRef]; ok {
		out.Times.EventStartMs = cut.StartOf(e)
	}
	if len(out.SelectedSpans) > 0 {
		out.Times.CutStartRef = out.SelectedSpans[0].StartRef
	}
	out.Verification.Issues = issues
	if out.Verification.Issues == nil {
		out.Verification.Issues = []string{}
	}
	out.Verification.BoundaryOK = len(out.SelectedSpans) > 0 && len(issues) == 0
	switch {
	case len(out.SelectedSpans) == 0:
		out.State = cut.StateFailed
	case len(issues) > 0:
		out.State = cut.StateVerifyFailed
	default:
		out.State = cut.StateOK
	}
}

// saveCutPlan stores the plan as the next version and writes its files.
func (a *App) saveCutPlan(ctx context.Context, run store.AIRun, p cut.Plan) (store.CutPlan, error) {
	rec := store.CutPlan{RunID: run.ID, SessionID: run.SessionID, CandidateIDs: p.CandidateIDs, State: p.State}
	if len(p.SelectedSpans) > 0 {
		rec.StartMs, rec.EndMs = p.SelectedSpans[0].StartMs, p.SelectedSpans[len(p.SelectedSpans)-1].EndMs
	}
	// The ID and version come from the store; the JSON must carry them too.
	rec.PlanJSON = []byte("{}")
	rec, err := a.cfg.Store.CreateCutPlan(ctx, rec)
	if err != nil {
		return rec, err
	}
	p.ID, p.Version, p.Supersedes = rec.ID, rec.Version, rec.Supersedes
	rec.PlanJSON = ai.Encode(p)
	if err := a.cfg.Store.SaveCutPlan(ctx, rec); err != nil {
		return rec, err
	}
	a.writeCutPlan(run.SessionID, p)
	return rec, nil
}

func (a *App) writeCutPlan(sid string, p cut.Plan) {
	spans := make([]export.CutSpan, len(p.SelectedSpans))
	for i, s := range p.SelectedSpans {
		spans[i] = export.CutSpan{Order: i + 1, StartMs: s.StartMs, EndMs: s.EndMs, Role: s.Role, StartRef: s.StartRef, EndRef: s.EndRef}
	}
	reasons := strings.TrimSpace(p.Boundaries.StartReason + " / " + p.Boundaries.EndReason)
	if err := export.WriteCutPlan(filepath.Join(a.exportDir(sid), "cuts", p.ID), export.CutPlanFile{ID: p.ID, SessionID: sid,
		State: p.State, Target: p.Reaction.Target, Reason: reasons, Spans: spans, Plan: ai.Encode(p)}); err != nil {
		a.logError("write cut plan", err)
	}
}

// CutPlanView is a plan in lists.
type CutPlanView struct {
	ID           string    `json:"id"`
	RunID        string    `json:"runId"`
	SessionID    string    `json:"sessionId"`
	CandidateIDs []string  `json:"candidateIds"`
	Version      int       `json:"version"`
	Supersedes   string    `json:"supersedes,omitempty"`
	State        string    `json:"state"`
	StartMs      int64     `json:"startMs"`
	EndMs        int64     `json:"endMs"`
	Confirmed    bool      `json:"confirmed"`
	CreatedAt    time.Time `json:"createdAt"`
}

func cutView(p store.CutPlan) CutPlanView {
	ids := p.CandidateIDs
	if ids == nil {
		ids = []string{}
	}
	return CutPlanView{ID: p.ID, RunID: p.RunID, SessionID: p.SessionID, CandidateIDs: ids, Version: p.Version, Supersedes: p.Supersedes,
		State: p.State, StartMs: p.StartMs, EndMs: p.EndMs, Confirmed: p.Confirmed, CreatedAt: p.CreatedAt}
}

// CutPlans lists a session's plans, newest first.
func (a *App) CutPlans(ctx context.Context, sid string) ([]CutPlanView, error) {
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, err
	}
	plans, err := a.cfg.Store.CutPlans(ctx, sid)
	if err != nil {
		return nil, err
	}
	out := []CutPlanView{}
	for _, p := range plans {
		out = append(out, cutView(p))
	}
	return out, nil
}

// CutPlan returns one plan in full.
func (a *App) CutPlan(ctx context.Context, id string) (CutPlanView, json.RawMessage, error) {
	p, err := a.cfg.Store.CutPlan(ctx, id)
	if err != nil {
		return CutPlanView{}, nil, err
	}
	return cutView(p), json.RawMessage(p.PlanJSON), nil
}

// cutTimelineItems are the current (newest) plans as timeline items, folded
// under their candidates by the screen. A failed plan has no spans and is left out.
func (a *App) cutTimelineItems(ctx context.Context, sid string) ([]TimelineItem, error) {
	plans, err := a.cfg.Store.CurrentCutPlans(ctx, sid)
	if err != nil {
		return nil, err
	}
	var out []TimelineItem
	for _, rec := range plans {
		var p cut.Plan
		if json.Unmarshal(rec.PlanJSON, &p) != nil || len(p.SelectedSpans) == 0 {
			continue
		}
		it := TimelineItem{ID: "cut:" + rec.ID, Source: SourceAI, Kind: "cut", OriginID: rec.ID, AnchorMs: rec.StartMs,
			ClipStartMs: rec.StartMs, ClipEndMs: rec.EndMs, Title: p.Reaction.Target, SourceCandidateIDs: rec.CandidateIDs,
			CutPlanID: rec.ID, Verified: rec.State == cut.StateOK, Version: rec.Version}
		if len(p.Causes) > 0 {
			it.Rationale = p.Causes[0].Summary
		}
		if len(p.Verification.Issues) > 0 {
			it.Uncertainty = strings.Join(p.Verification.Issues, " · ")
		} else if len(p.UnresolvedQuestions) > 0 {
			it.Uncertainty = strings.Join(p.UnresolvedQuestions, " · ")
		}
		for _, s := range p.SelectedSpans {
			it.Spans = append(it.Spans, TimelineSpan{StartMs: s.StartMs, EndMs: s.EndMs, Role: s.Role})
		}
		out = append(out, it)
	}
	return out, nil
}
