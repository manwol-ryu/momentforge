package app

import (
	"context"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// aiSource is what a session offers to an AI review: its candidates as
// timeline items (never AI ones; rule items carry their detector
// candidate), the chat to read (up to maxSeq), how far a chat range may
// reach and the analysis the rule items belong to.
type aiSource struct {
	items      []TimelineItem
	pending    map[string]bool // LIVE candidates still forming, not offered yet
	maxSeq     int64
	endMs      int64
	analysisID string // "" without one (LIVE, comments only)
	// What to tell the user when nothing can be picked, or a range is off.
	none, badRange string
}

// peakStats are a rule candidate's peak window numbers (nil for other items).
func peakStats(it TimelineItem) map[string]any {
	c := it.rule
	if c == nil {
		return nil
	}
	return map[string]any{"n": c.Peak.N, "u": c.Peak.U, "ratio": c.Peak.Ratio, "z": c.Peak.Z, "baseline_n": c.Peak.BaselineN}
}

// explainStats are the review's statistics of a rule candidate: the peak
// numbers, where the chat reacted and peaked and, from a whole-broadcast
// analysis, how strong the reaction was for the broadcast.
func explainStats(it TimelineItem) map[string]any {
	st := peakStats(it)
	if st == nil {
		return nil
	}
	c := it.rule
	st["reaction_start_ms"], st["reaction_end_ms"], st["peak_ms"] = c.ReactionStartMs, c.ReactionEndMs, c.PeakMs
	if c.Strength != "" {
		st["strength"], st["score"] = c.Strength, c.Score
	}
	return st
}

func (a *App) aiSource(ctx context.Context, sid string) (aiSource, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return aiSource{}, err
	}
	if s.Kind == string(platform.KindLive) {
		return a.liveAISource(ctx, s)
	}
	return a.vodAISource(ctx, sid)
}

// vodAISource offers a video's unified timeline, read against its latest analysis.
func (a *App) vodAISource(ctx context.Context, sid string) (aiSource, error) {
	tl, err := a.Timeline(ctx, sid)
	if err != nil {
		return aiSource{}, err
	}
	src := aiSource{maxSeq: store.AllSeq, endMs: tl.DurationMs, analysisID: tl.AnalysisID,
		none: "검토할 후보가 없습니다. 먼저 분석하거나 영상 댓글을 읽어 주세요.", badRange: "채팅 탐색 범위를 확인해 주세요."}
	if tl.AnalysisID != "" {
		src.maxSeq = tl.maxSeq
	}
	for _, it := range tl.Items {
		if it.Source != SourceAI {
			src.items = append(src.items, it)
		}
	}
	return src, nil
}

// liveAISource offers a LIVE capture as stored now, while it runs or after:
// its settled candidates, and its chat up to the newest stored message, so
// chat arriving after the preview is never part of the plan. A candidate
// still forming grows its clip range, so it is offered once confirmed (or
// closed with partial context). A range may reach as far as the analysis.
// LIVE candidate IDs do not change, so results keep them while the
// session's analysis (normally none) stays the same.
func (a *App) liveAISource(ctx context.Context, s store.Session) (aiSource, error) {
	st, sid := a.cfg.Store, s.ID
	ls, err := st.LiveState(ctx, sid)
	if err != nil {
		return aiSource{}, err
	}
	// Candidates first: chat is stored before the candidates it makes, so
	// everything they were built from is at or below maxSeq.
	cands, _, err := st.LiveCandidates(ctx, sid, 0)
	if err != nil {
		return aiSource{}, err
	}
	maxSeq, _, err := st.MaxSeq(ctx, sid)
	if err != nil {
		return aiSource{}, err
	}
	src := aiSource{pending: map[string]bool{}, maxSeq: maxSeq, endMs: ls.WatermarkMs, analysisID: s.LatestAnalysisID,
		none:     "검토할 확정 후보가 없습니다. 반응이 끝나고 뒤 채팅까지 받으면 후보가 확정됩니다.",
		badRange: "채팅 탐색 범위를 확인해 주세요. 분석을 마친 " + export.Stamp(ls.WatermarkMs)[:8] + "까지 지정할 수 있습니다."}
	for _, c := range cands {
		it := ruleItem(c, func(int64) string { return "" })
		if c.State != domain.CandidateConfirmed && c.State != domain.CandidatePartial {
			src.pending[it.ID] = true
			continue
		}
		src.items = append(src.items, it)
	}
	return src, nil
}
