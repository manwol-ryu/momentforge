package app

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// Timeline item sources.
const (
	SourceRule   = "rule"
	SourceViewer = "viewer_comment"
	SourceAI     = "ai"
)

// TimelineItem is one moment of the unified timeline, from any source.
type TimelineItem struct {
	ID          string // source:origin
	Source      string // rule | viewer_comment | ai
	OriginID    string // candidate, recommendation or AI recommendation ID
	AnchorMs    int64  // where to start watching
	ClipStartMs int64
	ClipEndMs   int64
	Title       string
	CommentText string // viewer recommendations: the comment
	Rationale   string // AI recommendations
	Uncertainty string // AI recommendations
	Priority    string // AI recommendations: high | medium | low
	WatchURL    string
}

// UnifiedTimeline merges a video's latest rule analysis, comment capture and
// AI run in time order (CzzEdit's 통합 타임라인); each item keeps its source.
type UnifiedTimeline struct {
	SessionID  string
	Title      string
	DurationMs int64
	AnalysisID string
	CaptureID  string
	AIRunID    string
	Items      []TimelineItem
}

// Timeline builds the unified timeline of a VOD session.
func (a *App) Timeline(ctx context.Context, sid string) (*UnifiedTimeline, error) {
	st := a.cfg.Store
	s, err := st.Session(ctx, sid)
	if err != nil {
		return nil, err
	}
	t := &UnifiedTimeline{SessionID: s.ID, Title: s.Title, DurationMs: s.DurationMs, Items: []TimelineItem{}}
	if s.Kind != string(platform.KindVOD) {
		return t, nil
	}
	link := a.linker(s)
	watch := func(ms int64) string {
		if link == nil {
			return ""
		}
		return link(ms)
	}
	if s.LatestAnalysisID != "" {
		an, err := st.Analysis(ctx, s.LatestAnalysisID)
		if err != nil {
			return nil, err
		}
		t.AnalysisID = an.ID
		for _, c := range an.Candidates {
			t.Items = append(t.Items, TimelineItem{ID: SourceRule + ":" + c.ID, Source: SourceRule, OriginID: c.ID,
				AnchorMs: c.ClipStartMs, ClipStartMs: c.ClipStartMs, ClipEndMs: c.ClipEndMs,
				Title: strings.Join(c.Reasons, " · "), WatchURL: watch(c.ClipStartMs)})
		}
	}
	if v, err := a.Comments(ctx, sid); err != nil {
		return nil, err
	} else if v != nil {
		t.CaptureID = v.CaptureID
		for _, r := range v.Recommendations {
			end := r.EndMs
			if end == 0 {
				end = min(s.DurationMs, r.StartMs+30000)
			}
			title := r.Description
			if title == "" {
				title = "시청자 추천 시각"
			}
			t.Items = append(t.Items, TimelineItem{ID: SourceViewer + ":" + r.ID, Source: SourceViewer, OriginID: r.ID,
				AnchorMs: r.StartMs, ClipStartMs: r.StartMs, ClipEndMs: end, Title: title, CommentText: r.CommentText,
				WatchURL: r.WatchURL})
		}
	}
	ai, err := a.aiTimelineItems(ctx, s.ID, watch)
	if err != nil {
		return nil, err
	}
	t.AIRunID = ai.runID
	t.Items = append(t.Items, ai.items...)
	sort.SliceStable(t.Items, func(i, j int) bool {
		x, y := t.Items[i], t.Items[j]
		if x.AnchorMs != y.AnchorMs {
			return x.AnchorMs < y.AnchorMs
		}
		if x.Source != y.Source {
			return x.Source < y.Source
		}
		return x.ID < y.ID
	})
	return t, nil
}

// writeUnified rewrites exports/<session>/timeline/ after a source changed.
func (a *App) writeUnified(ctx context.Context, sid string) {
	t, err := a.Timeline(ctx, sid)
	if err != nil {
		a.logError("build unified timeline", err)
		return
	}
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil || s.Kind != string(platform.KindVOD) {
		return
	}
	f := export.TimelineFile{SessionID: sid, VideoID: s.TargetID, Title: t.Title, DurationMs: t.DurationMs,
		AnalysisID: t.AnalysisID, CaptureID: t.CaptureID, AIRunID: t.AIRunID}
	for _, it := range t.Items {
		f.Items = append(f.Items, export.TimelineItem{ID: it.ID, Source: it.Source, OriginID: it.OriginID, AnchorMs: it.AnchorMs,
			ClipStartMs: it.ClipStartMs, ClipEndMs: it.ClipEndMs, Title: it.Title, CommentText: it.CommentText,
			Rationale: it.Rationale, Uncertainty: it.Uncertainty, WatchURL: it.WatchURL})
	}
	if err := export.WriteTimeline(filepath.Join(a.exportDir(sid), "timeline"), f); err != nil {
		a.logError("write unified timeline", err)
	}
}

// aiItems are the AI part of the unified timeline.
type aiItems struct {
	runID string
	items []TimelineItem
}
