package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// The cut analysis (docs/AI-CUT-ANALYSIS.md): one candidate at a time, read
// from a window around its clip; the model's search requests run within a
// budget (ai_tools.go) and the analyze step repeats with what they found.
const (
	cutWindowPadMs    = 60_000 // read this much before and after the clip
	cutMaxChats       = 400    // per window
	cutMaxUtterances  = 200
	cutMaxReplans     = 1 // a plan that fails a check is redone once with the reasons
	cutMaxOutputStep  = 4000
	cutPlannedPerCand = 4 // steps without a re-plan or a search
	// The chat kept first: the reaction with this much before and after it
	// (chat comes seconds after what it reacts to, and the cause before that).
	cutFocusBeforeMs = 60_000
	cutFocusAfterMs  = 30_000
	cutLoadMaxChats  = 20_000 // chat read for one window (the curve counts it; the evidence picks from it)
	cutCurveBinMs    = 5000
	cutCurveMaxBins  = 120
	cutUsualAroundMs = 10 * 60_000 // the curve's usual level comes from this much on each side
	cutMaxMeasures   = 4
	cutMaxKeyRunes   = 20 // a repeat measure's words, cut to this
)

// CutPreview is the preview's cut part for the API (inputs and the budget)
// and what the run starts from.
type CutPreview struct {
	Inputs               []cut.Input `json:"inputs"`
	RequestsPerCandidate int         `json:"requestsPerCandidate"`
	PlannedPerCandidate  int         `json:"plannedPerCandidate"`
	SearchExecuted       bool        `json:"searchExecuted"` // searches run (the budget allows at least one round)
	SearchRounds         int         `json:"searchRounds"`
	SearchMaxKB          int         `json:"searchMaxKb"`
	ConfirmSearches      bool        `json:"confirmSearches"` // each round's results wait for the user before they are sent
	HasTranscript        bool        `json:"hasTranscript"`
	UtteranceCount       int         `json:"utteranceCount"`
	ChatCount            int         `json:"chatCount"`
	EvidenceBytes        int         `json:"evidenceBytes"`
	// Notes the user left on earlier versions of each candidate's plan
	// (by timeline item ID); the plan step reads them (6-D).
	Notes     map[string][]cut.UserNote `json:"notes"`
	NoteCount int                       `json:"noteCount"`
	// What the searches read from, fixed at preview time: the transcript
	// and, for a LIVE capture, the chat saved so far (nothing newer is sent).
	TranscriptID string `json:"-"`
	MaxSeq       int64  `json:"-"`
	EndMs        int64  `json:"-"`
	// The analysis the items' rule IDs belong to, and the plan lineage each
	// item's new plan continues (by item ID; none: a new lineage).
	AnalysisID string            `json:"-"`
	Lineages   map[string]string `json:"-"`
}

// cutBudget is the search budget from the settings.
func (a *App) cutBudget() cut.Budget {
	b := cut.DefaultBudget()
	st := a.Settings()
	b.MaxRounds, b.MaxBytes = st.CutSearchRounds, st.CutSearchKB*1024
	return b
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
	budget := a.cutBudget()
	perCand := cut.MaxRequests(budget)
	lineages, err := a.cutLineages(ctx, req.SessionID, newMomentHosts(src.analysisID, src.items))
	if err != nil {
		return ai.Plan{}, nil, err
	}
	pv := &CutPreview{RequestsPerCandidate: perCand, PlannedPerCandidate: cutPlannedPerCand, HasTranscript: hasTranscript,
		SearchExecuted: budget.MaxRounds > 0, SearchRounds: budget.MaxRounds, SearchMaxKB: budget.MaxBytes / 1024,
		ConfirmSearches: req.ConfirmSearches && budget.MaxRounds > 0, TranscriptID: transcript.ID, MaxSeq: src.maxSeq, EndMs: src.endMs,
		Notes: map[string][]cut.UserNote{}, AnalysisID: src.analysisID, Lineages: map[string]string{}}
	note := "입력 토큰은 UTF-8 바이트 수로 잡은 상한이며 실제 토큰 수는 보통 더 적습니다. 후보마다 분석·계획·검수 2회의 네 단계를 보내고, " +
		"검수에 걸리면 계획과 검수를 한 번 더 보냅니다. "
	if budget.MaxRounds > 0 {
		note += fmt.Sprintf("분석이 '아까'·약속 같은 앞선 맥락을 물으면 이 방송 안의 발언·채팅을 검색해 최대 %d단계·%dKB까지 더 보내고 분석을 다시 합니다(후보당 최대 %d회).",
			budget.MaxRounds, budget.MaxBytes/1024, perCand)
		if pv.ConfirmSearches {
			note += " 검색 결과는 보내기 전에 매번 확인을 받습니다."
		}
	} else {
		note += "설정에서 검색 단계를 0으로 두어 읽은 범위 밖은 검색하지 않습니다."
	}
	plan := ai.Plan{SessionID: req.SessionID, Kind: store.AIKindCut, Provider: req.Provider, Model: req.Model, Delivery: ai.DeliverySingle,
		PromptVersion: cut.PipelineVersion, Prompt: cut.PromptAnalyze, Chunks: []ai.Chunk{}, Omitted: []ai.Omitted{},
		Price: price, CostCapUSD: req.CostCapUSD, EstimateNote: note,
		SearchRounds: budget.MaxRounds, SearchMaxBytes: budget.MaxBytes, ConfirmSearches: pv.ConfirmSearches}
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
		lineage := lineages[id]
		if planID := req.ContinuePlans[id]; planID != "" {
			rec, err := a.cfg.Store.CutPlan(ctx, planID)
			if err != nil || rec.SessionID != req.SessionID {
				return ai.Plan{}, nil, domain.NewUserError("INPUT", "이어서 분석할 컷 분석 결과를 찾을 수 없습니다. 화면을 새로 고친 뒤 다시 시도해 주세요.")
			}
			lineage = rec.Lineage
		}
		if lineage != "" {
			pv.Lineages[id] = lineage
		}
		notes, err := a.cutNotes(ctx, req.SessionID, lineage)
		if err != nil {
			return ai.Plan{}, nil, err
		}
		if len(notes) > 0 {
			pv.Notes[id] = notes
			pv.NoteCount += len(notes)
		}
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
		// The plan step carries the analysis too, and a re-plan repeats plan and
		// checks; every search round sends the input again with up to the
		// budget's bytes added.
		searchEst := est + budget.MaxBytes/3
		plan.PlannedInputEstimate += est * cutPlannedPerCand
		plan.InputUpperEstimate += est*cut.BaseRequestsPerCandidate + searchEst*budget.MaxRounds
		plan.RequestCount += perCand
		if price != nil {
			plan.CostUpperUSD += price.UpperCost(est, cutMaxOutputStep, false)*cut.BaseRequestsPerCandidate +
				price.UpperCost(searchEst, cutMaxOutputStep, false)*float64(budget.MaxRounds)
		}
	}
	if len(pv.Inputs) == 0 {
		return ai.Plan{}, nil, domain.NewUserError("INPUT", "분석할 후보를 선택해 주세요.")
	}
	if pv.NoteCount > 0 {
		plan.EstimateNote += fmt.Sprintf(" 앞선 결과에 남긴 메모 %d개를 계획 단계에 함께 보내고, 결과는 새 버전으로 저장합니다(확정한 버전은 그대로 둠).", pv.NoteCount)
	}
	plan.OutputTokenLimit = plan.RequestCount * cutMaxOutputStep
	plan.AttemptLimit = plan.RequestCount + len(pv.Inputs)*2
	if req.CostCapUSD > 0 && plan.CostUpperUSD > req.CostCapUSD {
		return ai.Plan{}, nil, domain.NewUserError("COST_CAP",
			fmt.Sprintf("컷 분석의 최대 비용(%.2f달러)이 한도를 넘습니다. 후보를 줄이거나 한도를 올려 주세요.", plan.CostUpperUSD))
	}
	plan.Fingerprint = ai.Digest(struct {
		Plan   ai.Plan                   `json:"plan"`
		Inputs []cut.Input               `json:"inputs"`
		Notes  map[string][]cut.UserNote `json:"notes"`
	}{plan, pv.Inputs, pv.Notes})
	return plan, pv, nil
}

// cutInput reads the window around one timeline item: its utterances (when
// the session has a transcript), its chat with the reaction kept first, the
// detector's account of the reaction with a chat curve over the clip and,
// for a viewer item, the comment. Messages the platform hid are left out.
func (a *App) cutInput(ctx context.Context, s store.Session, it TimelineItem, src aiSource, transcriptID string, hasTranscript bool) (cut.Input, error) {
	from := max(0, it.ClipStartMs-cutWindowPadMs)
	to := min(src.endMs, it.ClipEndMs+cutWindowPadMs)
	if to <= from {
		to = min(src.endMs, from+cutWindowPadMs)
	}
	in := cut.Input{WindowStartMs: from, WindowEndMs: to, DurationMs: s.DurationMs, Sources: []string{"chat"}}
	in.Candidates = []cut.Candidate{{ID: it.ID, SourceType: it.Source, AnchorMs: it.AnchorMs, ClipStartMs: it.ClipStartMs,
		ClipEndMs: it.ClipEndMs, Title: it.Title, Statistics: peakStats(it), Reaction: cutReaction(it.rule)}}
	if hasTranscript {
		in.Sources = append([]string{"utterances"}, in.Sources...)
		utts, err := a.cfg.Store.UtterancesBetween(ctx, transcriptID, from, to, cutMaxUtterances)
		if err != nil {
			return in, err
		}
		in.Evidence = append(in.Evidence, utteranceEvidence(utts)...)
	}
	loaded, total, err := a.cfg.Store.ChatsBetween(ctx, s.ID, from, to, src.maxSeq, cutLoadMaxChats, 0)
	if err != nil {
		return in, err
	}
	chats := make([]domain.ChatEvent, 0, len(loaded))
	for _, c := range loaded {
		if !c.Hidden {
			chats = append(chats, c)
		}
	}
	// The reaction (a viewer item: its clip) with what led to it comes first.
	focusFrom, focusTo := it.ClipStartMs, it.ClipEndMs
	if r := it.rule; r != nil {
		focusFrom, focusTo = r.ReactionStartMs, r.ReactionEndMs
	}
	picked := pickChats(chats, focusFrom-cutFocusBeforeMs, focusTo+cutFocusAfterMs, cutMaxChats)
	in.ChatsLeftOut = total - (len(loaded) - len(chats)) - len(picked)
	in.Evidence = append(in.Evidence, chatEvidence(picked)...)
	curve, err := a.chatCurve(ctx, s.ID, max(from, min(it.ClipStartMs, focusFrom)), min(to, max(it.ClipEndMs, focusTo)), src.endMs,
		src.maxSeq, chats, len(loaded) < total)
	if err != nil {
		return in, err
	}
	in.Curve = curve
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

// cutReaction is the detector's account of a rule candidate for the cut
// input: where the chat reacted and peaked, how strong it was for the
// broadcast and the few counts that stood out most (nil for other items).
func cutReaction(c *domain.Candidate) *cut.Reaction {
	if c == nil {
		return nil
	}
	r := &cut.Reaction{StartMs: c.ReactionStartMs, EndMs: c.ReactionEndMs, PeakMs: c.PeakMs, Strength: c.Strength, Score: c.Score}
	for _, m := range c.Measures[:min(len(c.Measures), cutMaxMeasures)] {
		key := m.Key
		if words, ok := strings.CutPrefix(key, "repeat:"); ok && utf8.RuneCountInString(words) > cutMaxKeyRunes {
			key = "repeat:" + string([]rune(words)[:cutMaxKeyRunes]) + "…"
		}
		r.Measures = append(r.Measures, cut.Measure{Key: key, Observed: m.Observed, Usual: m.Usual, WindowS: m.WindowMs / 1000})
	}
	return r
}

// pickChats chooses at most n of a window's messages (time order) for the
// evidence: donations and subscriptions, then every message of the focus
// span [focusFrom, focusTo), then the rest nearest to it first, so the
// reaction is never what is cut off. A focus span with more than room
// takes messages from each 5 s of it in turn, so its rise and fall stay
// visible. The result is in time order.
func pickChats(chats []domain.ChatEvent, focusFrom, focusTo int64, n int) []domain.ChatEvent {
	if len(chats) <= n {
		return chats
	}
	keep := make([]bool, len(chats))
	room := n
	take := func(i int) {
		if room > 0 && !keep[i] {
			keep[i] = true
			room--
		}
	}
	for i, c := range chats {
		if k := c.KindOrChat(); k == domain.ChatKindDonation || k == domain.ChatKindSubscription {
			take(i)
		}
	}
	lo := sort.Search(len(chats), func(i int) bool { return chats[i].OffsetMs >= focusFrom })
	hi := sort.Search(len(chats), func(i int) bool { return chats[i].OffsetMs >= focusTo })
	var bins [][]int
	for i := lo; i < hi; i++ {
		if keep[i] {
			continue
		}
		b := int((chats[i].OffsetMs - focusFrom) / cutCurveBinMs)
		for len(bins) <= b {
			bins = append(bins, nil)
		}
		bins[b] = append(bins[b], i)
	}
	for k, more := 0, true; room > 0 && more; k++ {
		more = false
		for _, b := range bins {
			if k < len(b) {
				take(b[k])
				more = true
			}
		}
	}
	// Outward from the focus span: whichever side is nearer next.
	before, after := lo-1, hi
	for room > 0 && (before >= 0 || after < len(chats)) {
		if after >= len(chats) || (before >= 0 && focusFrom-chats[before].OffsetMs <= chats[after].OffsetMs-focusTo) {
			take(before)
			before--
		} else {
			take(after)
			after++
		}
	}
	out := make([]domain.ChatEvent, 0, n)
	for i, c := range chats {
		if keep[i] {
			out = append(out, c)
		}
	}
	return out
}

// chatCurve counts the chat of [from, to) in bins (5 s, longer for a long
// span): messages, authors, and authors who share a reaction key with
// another author in the bin, as the detector counts (system notices are
// not counted; chats holds no hidden message and covers the span unless
// truncated, when the curve ends where the reading did). The usual level is
// the median bin of the ten minutes on each side, up to endMs, counted the
// same way (viewers' messages stored up to maxSeq).
func (a *App) chatCurve(ctx context.Context, sid string, from, to, endMs, maxSeq int64, chats []domain.ChatEvent, truncated bool) (*cut.Curve, error) {
	if to <= from {
		return nil, nil
	}
	bin := int64(cutCurveBinMs)
	if span := to - from; span > cutCurveMaxBins*bin {
		bin = (span + cutCurveMaxBins*cutCurveBinMs - 1) / (cutCurveMaxBins * cutCurveBinMs) * cutCurveBinMs
	}
	start := from / bin * bin
	curve := &cut.Curve{BinMs: bin, Bins: []cut.CurveBin{}}
	if n := len(chats); truncated && n > 0 && chats[n-1].OffsetMs < to {
		to, curve.Partial = chats[n-1].OffsetMs+1, true
	}
	var rules []detect.MessageRule
	for _, r := range a.cfg.Rules {
		if mr, ok := r.(detect.MessageRule); ok {
			rules = append(rules, mr)
		}
	}
	i := sort.Search(len(chats), func(i int) bool { return chats[i].OffsetMs >= start })
	for t := start; t < to; t += bin {
		var in []domain.ChatEvent
		for ; i < len(chats) && chats[i].OffsetMs < t+bin; i++ {
			if k := chats[i].KindOrChat(); k == domain.ChatKindChat || k == domain.ChatKindDonation {
				in = append(in, chats[i])
			}
		}
		curve.Bins = append(curve.Bins, curveBin(t, in, rules))
	}
	aroundFrom, aroundTo := max(0, start-cutUsualAroundMs), min(endMs, to+cutUsualAroundMs)
	buckets, err := a.cfg.Store.CountedActivity(ctx, sid, bin, aroundFrom, aroundTo, maxSeq)
	if err != nil {
		return nil, err
	}
	byIndex := make(map[int64]store.ActivityBucket, len(buckets))
	for _, b := range buckets {
		byIndex[b.Index] = b
	}
	var chatsAround, authorsAround []float64
	for idx := aroundFrom / bin; idx*bin < aroundTo; idx++ {
		if idx*bin+bin > start && idx*bin < to {
			continue // the curve itself
		}
		chatsAround = append(chatsAround, float64(byIndex[idx].Chats))
		authorsAround = append(authorsAround, float64(byIndex[idx].Authors))
	}
	curve.UsualChats, curve.UsualAuthors = median(chatsAround), median(authorsAround)
	return curve, nil
}

// curveBin counts one bin's messages for the curve.
func curveBin(start int64, chats []domain.ChatEvent, rules []detect.MessageRule) cut.CurveBin {
	b := cut.CurveBin{StartMs: start, Chats: len(chats)}
	authors := map[string]bool{}
	byKey := map[string]map[string]bool{} // reaction key → its authors
	for _, c := range chats {
		if c.AuthorKey == "" {
			continue
		}
		authors[c.AuthorKey] = true
		text := detect.Normalize(c.Text)
		if text == "" || strings.HasPrefix(text, "!") || strings.HasPrefix(text, "/") {
			continue // empty or a bot command, as the detector skips
		}
		for _, r := range rules {
			for _, k := range r.Keys(text) {
				if byKey[k] == nil {
					byKey[k] = map[string]bool{}
				}
				byKey[k][c.AuthorKey] = true
			}
		}
	}
	reacting := map[string]bool{}
	for _, by := range byKey {
		if len(by) >= 2 {
			for author := range by {
				reacting[author] = true
			}
		}
	}
	b.Authors, b.Reacting = len(authors), len(reacting)
	return b
}

// median is the middle value, to one decimal (0 for none).
func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	xs = slices.Clone(xs)
	slices.Sort(xs)
	m := xs[len(xs)/2]
	if len(xs)%2 == 0 {
		m = (xs[len(xs)/2-1] + m) / 2
	}
	return math.Round(m*10) / 10
}

// CutClipEvidence is a stored plan with the evidence inside its chosen
// spans, read again from the session (its latest transcript and the saved
// chat), for development checks that send the clip-only check again
// (cmd/momentforge's strictness test). It only reads and sends nothing.
func (a *App) CutClipEvidence(ctx context.Context, planID string) (cut.Plan, []cut.Evidence, error) {
	rec, err := a.cfg.Store.CutPlan(ctx, planID)
	if err != nil {
		return cut.Plan{}, nil, err
	}
	p, ok := decodePlan(rec)
	if !ok {
		return cut.Plan{}, nil, domain.NewUserError("INPUT", "이 컷 분석 결과를 읽지 못했습니다.")
	}
	transcript, hasTranscript, err := a.cfg.Store.LatestTranscript(ctx, rec.SessionID)
	if err != nil {
		return p, nil, err
	}
	seen := map[string]bool{}
	ev := []cut.Evidence{}
	for _, s := range p.SelectedSpans {
		var found []cut.Evidence
		if hasTranscript {
			utts, err := a.cfg.Store.UtterancesBetween(ctx, transcript.ID, s.StartMs, s.EndMs+1, cutMaxUtterances)
			if err != nil {
				return p, nil, err
			}
			found = utteranceEvidence(utts)
		}
		chats, _, err := a.cfg.Store.ChatsBetween(ctx, rec.SessionID, s.StartMs, s.EndMs+1, math.MaxInt64, cutMaxChats, 0)
		if err != nil {
			return p, nil, err
		}
		for _, e := range append(found, chatEvidence(chats)...) {
			if !seen[e.Ref] {
				seen[e.Ref] = true
				ev = append(ev, e)
			}
		}
	}
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].StartMs < ev[j].StartMs })
	return p, ev, nil
}

// sentLogEntry records what one step sent (the run's sent_log): how many
// refs and bytes, and which refs the user had not seen in the preview
// (what searches added; the user reads them here afterwards).
type sentLogEntry struct {
	Candidate string   `json:"candidate"`
	Step      string   `json:"step"`
	Round     int      `json:"round,omitempty"` // analyze steps after the first
	Refs      int      `json:"refs"`
	Bytes     int      `json:"bytes"`
	NewRefs   []string `json:"new_refs,omitempty"`
	At        string   `json:"at"`
}

// AIPending is a search round's results waiting for the user's go-ahead
// (the preview's "검색마다 확인" option): what would be sent next.
type AIPending struct {
	Candidate  string         `json:"candidate"`
	Round      int            `json:"round"`
	Utterances int            `json:"utterances"`
	Chats      int            `json:"chats"`
	Bytes      int            `json:"bytes"`
	Evidence   []cut.Evidence `json:"evidence"`
}

// runCut analyses each candidate in turn: analyze (with search rounds) →
// plan → check → the two verifications, with one re-plan when a check
// fails. Every plan is saved, trusted or not, with the reasons.
func (a *App) runCut(act *aiActive, run store.AIRun, plan ai.Plan, cp *CutPreview, provider ai.StepProvider, key string) {
	ctx := act.ctx
	bg := context.WithoutCancel(ctx)
	var usage []ai.Usage
	var total ai.Usage
	var sentLog []sentLogEntry
	var planIDs []string
	inputs := cp.Inputs
	sentRefs := map[string]bool{} // refs already sent for the current candidate
	round := 0
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
		entry := sentLogEntry{Candidate: candidate, Step: step.Name, Refs: 0, Bytes: len(step.InputJSON()), At: time.Now().UTC().Format(time.RFC3339)}
		if step.Name == "analyze" {
			entry.Round = round
		}
		for _, ref := range stepRefs(step) {
			entry.Refs++
			if !sentRefs[ref] {
				sentRefs[ref] = true
				entry.NewRefs = append(entry.NewRefs, ref)
			}
		}
		sentLog = append(sentLog, entry)
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

	var failure error    // ends the run
	var skipped []string // candidates whose steps failed, with why
	skipCode := ""
	done := 0
	for _, in := range inputs {
		cand := in.Candidates[0]
		sentRefs, round = map[string]bool{}, 0
		// The preview's refs are what the user saw; the first step is not "new".
		for _, e := range in.Evidence {
			sentRefs[e.Ref] = true
		}
		searcher := &cutSearcher{a: a, sid: run.SessionID, transcriptID: cp.TranscriptID, maxSeq: cp.MaxSeq, endMs: cp.EndMs,
			budget: cut.Budget{MaxRounds: plan.SearchRounds, MaxRequestsPerRound: cut.DefaultSearchRequestsPerRound, MaxBytes: plan.SearchMaxBytes}}
		cr := cutRun{act: act, run: run, plan: plan, searcher: searcher, round: &round, notes: cp.Notes[cand.ID]}
		p, err := a.analyzeCandidate(ctx, cr, in, send)
		if err != nil {
			if cutRunStops(err) {
				failure = err
				break
			}
			// One candidate's answer failed: say so and go on with the others.
			code, msg := errorText(err)
			if skipCode == "" {
				skipCode = code
			}
			skipped = append(skipped, export.Stamp(cand.ClipStartMs)[:8]+" 후보: "+msg)
			continue
		}
		rec, err := a.saveCutPlan(bg, run, p, cp.Lineages[cand.ID])
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
		run.ErrorCode, run.ErrorMessage = errorText(failure)
		run.State = store.AIFailed
		if run.ErrorCode == "CANCELLED" {
			run.State = store.AICancelled
		} else if done > 0 {
			run.State = store.AIPartial
		}
		if len(skipped) > 0 {
			run.ErrorMessage += " 그 전에 분석하지 못한 후보: " + strings.Join(skipped, " · ")
		}
	case len(skipped) > 0:
		run.State = store.AIFailed
		if done > 0 {
			run.State = store.AIPartial
		}
		run.ErrorCode = skipCode
		run.ErrorMessage = fmt.Sprintf("후보 %d개 중 %d개를 분석하지 못했습니다. %s", len(inputs), len(skipped), strings.Join(skipped, " · "))
	default:
		run.State = store.AICompleted
	}
	setUsage(&run, total, plan.Price, false)
	save()
	a.writeUnified(bg, run.SessionID)
	a.publishAI(run, nil)
}

// cutRunStops says whether a step error ends the whole cut run rather than
// one candidate: the user cancelled, the request or cost limit was reached,
// or the provider cannot answer anyone (key, permission, model, program).
// Other answers (a malformed one, a refusal, a server error after the
// retries) fail their candidate alone.
func cutRunStops(err error) bool {
	ue, ok := domain.AsUserError(err)
	if !ok {
		return true // not from the provider: the store or a bug
	}
	switch ue.Code {
	case "CANCELLED", "REQUEST_BUDGET", "COST_CAP", "CREDENTIALS", "AI_UNAVAILABLE", "AI_FREE_TIER",
		"AI_HTTP_401", "AI_HTTP_403", "AI_HTTP_404":
		return true
	}
	return false
}

// errorText is an error's code and message for the run record.
func errorText(err error) (code, message string) {
	if ue, ok := domain.AsUserError(err); ok {
		return ue.Code, ue.Message
	}
	return "AI_ERROR", "AI 작업을 완료하지 못했습니다."
}

// stepRefs lists the evidence refs a step sends.
func stepRefs(step ai.StepRequest) []string {
	var out []string
	add := func(ev []cut.Evidence) {
		for _, e := range ev {
			out = append(out, e.Ref)
		}
	}
	switch in := step.Input.(type) {
	case cut.Input:
		add(in.Evidence)
	case map[string]any:
		if i, ok := in["input"].(cut.Input); ok {
			add(i.Evidence)
		}
		for _, k := range []string{"selected", "context"} {
			if ev, ok := in[k].([]cut.Evidence); ok {
				add(ev)
			}
		}
		if spans, ok := in["spans"].([]cut.ClipSpan); ok {
			for _, s := range spans {
				for _, e := range s.Evidence {
					out = append(out, e.Ref)
				}
			}
		}
	}
	return out
}

// cutRun is what one candidate's analysis runs with.
type cutRun struct {
	act      *aiActive
	run      store.AIRun
	plan     ai.Plan
	searcher *cutSearcher
	round    *int           // the current analyze round, for the sent log
	notes    []cut.UserNote // the user's notes on earlier versions, for the plan step
}

// analyzeCandidate runs the steps for one input and builds its plan: the
// analyze step, repeated with search results while the model asks and the
// budget allows; then plan, checks and the verifications.
func (a *App) analyzeCandidate(ctx context.Context, cr cutRun, in cut.Input,
	send func(string, ai.StepRequest) (json.RawMessage, error)) (cut.Plan, error) {
	run, plan := cr.run, cr.plan
	cand := in.Candidates[0].ID
	// in shares its lists with the preview the user saw: searches replace
	// them (cut.Merge, withoutRefs, cutSearcher.run), never write into them.
	known := cut.Known(in.Evidence)
	out := cut.Plan{RunID: run.ID, SessionID: run.SessionID, State: cut.StateFailed, Sources: in.Sources,
		SearchedRanges: []cut.SearchRequest{}, Searches: []cut.SearchResult{}, UnresolvedQuestions: []string{}, UserNotes: []cut.UserNote{},
		AnalysisEvidenceRefs: []string{}, RequiredContextRefs: []string{}, OptionalContextRefs: []string{}, SelectedSpans: []cut.Span{},
		NotesApplied: append([]cut.UserNote{}, cr.notes...), Refs: map[string]cut.RefInfo{}}
	for _, c := range in.Candidates {
		out.CandidateIDs = append(out.CandidateIDs, c.ID)
	}
	c0 := in.Candidates[0]
	out.Reaction.PeakMs = c0.AnchorMs // a viewer item: the comment's time
	if c0.Reaction != nil {
		out.Reaction.PeakMs = c0.Reaction.PeakMs
	}
	out.CandidateClip = &cut.ClipRange{StartMs: c0.ClipStartMs, EndMs: c0.ClipEndMs}
	out.AnalysisVersion = cut.Version{Pipeline: cut.PipelineVersion, Prompts: cut.PromptVersions, Provider: plan.Provider,
		Model: plan.Model, Sources: in.Sources}
	out.Verification = cut.Verification{EvidenceMatch: "all_read", Issues: []string{}}

	var an cut.Analysis
	budget := cr.searcher.budget
	for round := 0; ; round++ {
		*cr.round = round
		raw, err := send(cand, cut.AnalyzeStep(in))
		if err != nil {
			return out, err
		}
		var dropped int
		an, dropped, err = cut.ParseAnalysis(raw, known)
		if err != nil {
			return out, err
		}
		if dropped > 0 {
			out.Verification.EvidenceMatch = "some_unread"
		}
		if len(an.SearchRequests) == 0 {
			break
		}
		if round >= budget.MaxRounds {
			// The model still wanted to read more; the budget stops here.
			cr.searcher.use.Exhausted = true
			break
		}
		results, added, err := cr.searcher.run(ctx, round+1, an.SearchRequests, &in, known)
		if err != nil {
			return out, err
		}
		out.Searches = append(out.Searches, results...)
		for _, r := range results {
			if r.Ran {
				out.SearchedRanges = append(out.SearchedRanges, r.Request)
			}
		}
		if len(added) == 0 && !activityIn(results) {
			break // nothing new to analyse with
		}
		if plan.ConfirmSearches && len(added) > 0 {
			ok, err := a.awaitSearchConsent(cr.act, run, pendingOf(cand, round+1, added, known))
			if err != nil {
				return out, err
			}
			if !ok {
				// The user kept the results on this computer: forget them and plan with what was previewed.
				cr.searcher.use.Declined = true
				in.Evidence, known = withoutRefs(in.Evidence, added), cut.Known(nil)
				for _, e := range in.Evidence {
					known[e.Ref] = e
				}
				break
			}
		}
	}
	out.SearchBudget = cr.searcher.use
	out.Reaction.Target, out.Causes, out.CauseStatus = an.Target, an.Causes, an.CauseStatus
	out.UnresolvedQuestions = append(out.UnresolvedQuestions, an.Questions...)
	if out.SearchBudget.Exhausted && len(an.Questions) > 0 {
		out.UnresolvedQuestions = append(out.UnresolvedQuestions, "검색 범위 안에서 확인하지 못함 (검색 예산을 다 씀)")
	}
	if out.SearchBudget.Declined {
		out.UnresolvedQuestions = append(out.UnresolvedQuestions, "검색 결과를 보내지 않아 그 근거 없이 계획함")
	}

	// Each attempt keeps its own plan, checks and verdicts; the plan saved is
	// the last one that passed the structural checks (verified, with what
	// the checks found), or the last attempt when none did.
	var best *cutAttempt
	var issues []string
	for i := 0; i <= cutMaxReplans; i++ {
		raw, err := send(cand, cut.PlanStep(in, an, issues, cr.notes))
		if err != nil {
			return out, err
		}
		answer, err := cut.ParsePlan(raw)
		if err != nil {
			return out, err
		}
		at := &cutAttempt{answer: answer}
		at.checked, at.issues = cut.CheckPlan(&at.answer, known, in.DurationMs)
		if len(at.issues) == 0 && len(at.checked.Spans) == 0 {
			at.issues = []string{"고른 후보의 span을 시각으로 바꾸지 못했습니다"}
		}
		if len(at.issues) == 0 {
			// The structure holds: verify the clip alone, then against the source.
			at.usable = true
			clipSpans := cut.ClipSpans(at.checked.Spans, in.Evidence, cut.KeyRefs(an, at.answer))
			clipRaw, err := send(cand, cut.VerifyClipStep(an.Target, clipSpans))
			if err != nil {
				return out, err
			}
			clip, err := cut.ParseClipVerification(clipRaw, clipSpans)
			if err != nil {
				return out, err
			}
			srcRaw, err := send(cand, cut.VerifySourceStep(an.Target, at.answer.Chosen, at.checked.Spans, at.answer.RequiredContextRefs,
				at.answer.Ending, in.Evidence))
			if err != nil {
				return out, err
			}
			src, err := cut.ParseSourceVerification(srcRaw)
			if err != nil {
				return out, err
			}
			at.clip, at.src = &clip, &src
			at.issues = cut.Issues(&clip, &src)
		}
		if best == nil || at.usable || !best.usable {
			best = at
		}
		if issues = at.issues; len(issues) == 0 {
			break
		}
		// Re-plan with the reasons, or give up after the retries.
	}
	if best.checked.Dropped > 0 {
		out.Verification.EvidenceMatch = "some_unread"
	}
	out.Verification.ClipOnly, out.Verification.AgainstSource = best.clip, best.src
	fillPlan(&out, best.answer, best.checked, known, best.issues, in.DurationMs)
	return out, nil
}

// cutAttempt is one plan step's answer with what checked it.
type cutAttempt struct {
	answer  cut.PlanAnswer
	checked cut.Checked
	clip    *cut.ClipVerification // nil when the structure failed (not verified)
	src     *cut.SourceVerification
	issues  []string
	usable  bool // passed the structural checks
}

func activityIn(results []cut.SearchResult) bool {
	for _, r := range results {
		if len(r.Activity) > 0 {
			return true
		}
	}
	return false
}

func pendingOf(cand string, round int, refs []string, known map[string]cut.Evidence) AIPending {
	p := AIPending{Candidate: cand, Round: round, Evidence: []cut.Evidence{}}
	for _, ref := range refs {
		e := known[ref]
		p.Evidence = append(p.Evidence, e)
		p.Bytes += len(e.Text)
		if e.Kind == cut.KindUtterance {
			p.Utterances++
		} else {
			p.Chats++
		}
	}
	return p
}

// withoutRefs is ev without refs, as a new list (ev may be shared).
func withoutRefs(ev []cut.Evidence, refs []string) []cut.Evidence {
	drop := map[string]bool{}
	for _, r := range refs {
		drop[r] = true
	}
	out := make([]cut.Evidence, 0, len(ev))
	for _, e := range ev {
		if !drop[e.Ref] {
			out = append(out, e)
		}
	}
	return out
}

// awaitSearchConsent shows the user what a search round found and waits
// for "보내기" or "보내지 않기" (ContinueAI), or for the run to be cancelled.
func (a *App) awaitSearchConsent(act *aiActive, run store.AIRun, p AIPending) (bool, error) {
	decision := make(chan bool, 1)
	a.ai.pendMu.Lock()
	a.ai.pending[run.ID], a.ai.decisions[run.ID] = &p, decision
	a.ai.pendMu.Unlock()
	a.publishAI(run, nil)
	defer func() {
		a.ai.pendMu.Lock()
		delete(a.ai.pending, run.ID)
		delete(a.ai.decisions, run.ID)
		a.ai.pendMu.Unlock()
		a.publishAI(run, nil)
	}()
	select {
	case ok := <-decision:
		return ok, nil
	case <-act.ctx.Done():
		return false, errAICancelled
	}
}

// ContinueAI answers a run waiting at a search round: send what it found
// (true) or go on without it (false).
func (a *App) ContinueAI(id string, send bool) error {
	a.ai.pendMu.Lock()
	defer a.ai.pendMu.Unlock()
	decision := a.ai.decisions[id]
	if decision == nil {
		return domain.NewUserError("NOT_WAITING", "이 AI 작업은 지금 확인을 기다리고 있지 않습니다.")
	}
	select {
	case decision <- send:
	default: // already answered
	}
	return nil
}

// fillPlan copies the (last) answer into the plan and resolves its times.
func fillPlan(out *cut.Plan, p cut.PlanAnswer, checked cut.Checked, known map[string]cut.Evidence, issues []string, durationMs int64) {
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
	// Every candidate gets its times, so the user can switch between them;
	// the chosen one is exactly the checked spans.
	out.Boundaries.Resolved = cut.ResolveBoundaries(out.Boundaries.Candidates, known, durationMs)
	for i := range out.Boundaries.Resolved {
		if out.Boundaries.Resolved[i].Name == p.Chosen && len(out.SelectedSpans) > 0 {
			out.Boundaries.Resolved[i].Spans = out.SelectedSpans
		}
	}
	out.Ending = p.Ending
	if out.Ending.Checks == nil {
		out.Ending.Checks = []string{}
	}
	out.UnresolvedQuestions = append(out.UnresolvedQuestions, p.UnresolvedQuestions...)
	out.Times.StoryOriginRef, out.Times.OriginConfidence = p.StoryOriginRef, p.OriginConfidence
	if e, ok := known[p.StoryOriginRef]; ok {
		out.Times.StoryOriginMs = cut.SeekOf(e)
	}
	out.Times.EventStartRef = p.EventStartRef
	if e, ok := known[p.EventStartRef]; ok {
		out.Times.EventStartMs = cut.SeekOf(e)
	}
	if len(out.SelectedSpans) > 0 {
		out.Times.CutStartRef = out.SelectedSpans[0].StartRef
	}
	cut.IndexRefs(out, known)
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

// saveCutPlan stores the plan as the next version of lineage ("" a new
// one) and writes its files.
func (a *App) saveCutPlan(ctx context.Context, run store.AIRun, p cut.Plan, lineage string) (store.CutPlan, error) {
	rec := store.CutPlan{RunID: run.ID, SessionID: run.SessionID, CandidateIDs: p.CandidateIDs, AnalysisID: run.AnalysisID,
		Lineage: lineage, State: p.State}
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
	if err := writeCutPlanTo(filepath.Join(a.exportDir(sid), "cuts", p.ID), sid, p); err != nil {
		a.logError("write cut plan", err)
	}
}

// writeCutPlanTo writes a plan's plan.json, cuts.csv and cuts.edl into dir.
func writeCutPlanTo(dir, sid string, p cut.Plan) error {
	eff := p.EffectiveSpans()
	spans := make([]export.CutSpan, len(eff))
	for i, s := range eff {
		spans[i] = export.CutSpan{Order: i + 1, StartMs: s.StartMs, EndMs: s.EndMs, Role: s.Role, StartRef: s.StartRef, EndRef: s.EndRef}
	}
	reasons := strings.TrimSpace(p.Boundaries.StartReason + " / " + p.Boundaries.EndReason)
	return export.WriteCutPlan(dir, export.CutPlanFile{ID: p.ID, SessionID: sid,
		State: p.State, Target: p.Reaction.Target, Reason: reasons, Spans: spans, Plan: ai.Encode(p)})
}

// CutPlanView is a plan in lists.
type CutPlanView struct {
	ID           string   `json:"id"`
	RunID        string   `json:"runId"`
	SessionID    string   `json:"sessionId"`
	CandidateIDs []string `json:"candidateIds"`
	Version      int      `json:"version"`
	Supersedes   string   `json:"supersedes,omitempty"`
	State        string   `json:"state"`
	StartMs      int64    `json:"startMs"`
	EndMs        int64    `json:"endMs"`
	Confirmed    bool     `json:"confirmed"`
	// The confirmed boundary candidate (short | context | full_event).
	ConfirmedBoundary string    `json:"confirmedBoundary,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
}

func cutView(p store.CutPlan) CutPlanView {
	ids := p.CandidateIDs
	if ids == nil {
		ids = []string{}
	}
	return CutPlanView{ID: p.ID, RunID: p.RunID, SessionID: p.SessionID, CandidateIDs: ids, Version: p.Version, Supersedes: p.Supersedes,
		State: p.State, StartMs: p.StartMs, EndMs: p.EndMs, Confirmed: p.Confirmed, ConfirmedBoundary: p.ConfirmedBoundary, CreatedAt: p.CreatedAt}
}

// CutPlans lists a session's plans, newest first.
func (a *App) CutPlans(ctx context.Context, sid string) ([]CutPlanView, error) {
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, err
	}
	plans, err := a.cfg.Store.CutPlans(ctx, sid, false)
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

// cutTimelineItems are the current plans as timeline items, folded under
// their candidates by the screen: the confirmed version and, when there is
// a later one, the newest too (CurrentCutPlans). A plan made against an
// earlier analysis is folded under the candidate it is about now (hosts).
// A failed plan has no spans and is left out.
func (a *App) cutTimelineItems(ctx context.Context, sid string, hosts momentHosts) ([]TimelineItem, error) {
	plans, err := a.cfg.Store.CurrentCutPlans(ctx, sid)
	if err != nil {
		return nil, err
	}
	kept := keptVersions(plans)
	var out []TimelineItem
	for _, rec := range plans {
		p, ok := decodePlan(rec)
		if !ok || len(p.SelectedSpans) == 0 {
			continue
		}
		d := cutDetail(rec, p)
		d.KeptVersion = kept.of(rec)
		d.placeUnder(hosts.planHosts(rec, p))
		spans := p.EffectiveSpans()
		it := TimelineItem{ID: "cut:" + rec.ID, Source: SourceAI, Kind: "cut", OriginID: rec.ID, AnchorMs: spans[0].StartMs,
			ClipStartMs: spans[0].StartMs, ClipEndMs: spans[len(spans)-1].EndMs, Title: p.Reaction.Target, SourceCandidateIDs: d.CandidateIDs,
			CutPlanID: rec.ID, Verified: rec.State == cut.StateOK, Version: rec.Version, Cut: &d}
		if len(p.Causes) > 0 {
			it.Rationale = p.Causes[0].Summary
		}
		it.SearchRounds, it.SearchAdded = d.SearchRounds, d.SearchAdded
		if p.Times.StoryOriginRef != "" {
			it.StoryOriginMs, it.HasOrigin = p.Times.StoryOriginMs, true
		}
		if len(p.Verification.Issues) > 0 {
			it.Uncertainty = strings.Join(p.Verification.Issues, " · ")
		} else if len(p.UnresolvedQuestions) > 0 {
			it.Uncertainty = strings.Join(p.UnresolvedQuestions, " · ")
		}
		for _, s := range spans {
			it.Spans = append(it.Spans, TimelineSpan{StartMs: s.StartMs, EndMs: s.EndMs, Role: s.Role})
		}
		out = append(out, it)
	}
	return out, nil
}

// cutLineages says, for each current timeline item, which plan lineage its
// next cut plan continues: that of the newest current plan shown under it
// (hosts), so "다시 분석" makes a new version of the plan the user sees even
// after the session was analysed again and the candidate was renumbered.
func (a *App) cutLineages(ctx context.Context, sid string, hosts momentHosts) (map[string]string, error) {
	plans, err := a.cfg.Store.CurrentCutPlans(ctx, sid)
	if err != nil {
		return nil, err
	}
	newest := map[string]store.CutPlan{}
	for _, rec := range plans {
		p, ok := decodePlan(rec)
		if !ok {
			continue
		}
		for _, host := range hosts.planHosts(rec, p) {
			if cur, seen := newest[host]; !seen || rec.CreatedAt.After(cur.CreatedAt) {
				newest[host] = rec
			}
		}
	}
	out := make(map[string]string, len(newest))
	for host, rec := range newest {
		out[host] = rec.Lineage
	}
	return out, nil
}
