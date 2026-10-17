package app

import (
	"context"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// aiSource is what a session offers to an AI review: its candidates as
// timeline items (never AI ones), their peak statistics, the chat to read
// (up to maxSeq) and how far a chat range may reach.
type aiSource struct {
	items   []TimelineItem
	stats   map[string]map[string]any // by item ID; rule candidates only
	pending map[string]bool           // LIVE candidates still forming, not offered yet
	maxSeq  int64
	endMs   int64
	// What to tell the user when nothing can be picked, or a range is off.
	none, badRange string
}

func peakStats(c domain.Candidate) map[string]any {
	return map[string]any{"n": c.Peak.N, "u": c.Peak.U, "ratio": c.Peak.Ratio, "z": c.Peak.Z, "baseline_n": c.Peak.BaselineN}
}

func (a *App) aiSource(ctx context.Context, sid string) (aiSource, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return aiSource{}, err
	}
	if s.Kind == string(platform.KindLive) {
		return a.liveAISource(ctx, sid)
	}
	return a.vodAISource(ctx, sid)
}

// vodAISource offers a video's unified timeline, read against its latest analysis.
func (a *App) vodAISource(ctx context.Context, sid string) (aiSource, error) {
	tl, err := a.Timeline(ctx, sid)
	if err != nil {
		return aiSource{}, err
	}
	src := aiSource{stats: map[string]map[string]any{}, maxSeq: store.AllSeq, endMs: tl.DurationMs,
		none: "검토할 후보가 없습니다. 먼저 분석하거나 영상 댓글을 읽어 주세요.", badRange: "채팅 탐색 범위를 확인해 주세요."}
	if tl.AnalysisID != "" {
		an, err := a.cfg.Store.Analysis(ctx, tl.AnalysisID)
		if err != nil {
			return aiSource{}, err
		}
		src.maxSeq = an.MaxSeq
		for _, c := range an.Candidates {
			src.stats[SourceRule+":"+c.ID] = peakStats(c)
		}
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
func (a *App) liveAISource(ctx context.Context, sid string) (aiSource, error) {
	st := a.cfg.Store
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
	src := aiSource{stats: map[string]map[string]any{}, pending: map[string]bool{}, maxSeq: maxSeq, endMs: ls.WatermarkMs,
		none:     "검토할 확정 후보가 없습니다. 반응이 끝나고 뒤 채팅까지 받으면 후보가 확정됩니다.",
		badRange: "채팅 탐색 범위를 확인해 주세요. 분석을 마친 " + export.Stamp(ls.WatermarkMs)[:8] + "까지 지정할 수 있습니다."}
	for _, c := range cands {
		it := ruleItem(c, func(int64) string { return "" })
		if c.State != domain.CandidateConfirmed && c.State != domain.CandidatePartial {
			src.pending[it.ID] = true
			continue
		}
		src.items = append(src.items, it)
		src.stats[it.ID] = peakStats(c)
	}
	return src, nil
}
