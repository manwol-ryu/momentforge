package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
)

func (h *handler) startComments(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string `json:"sessionId"`
		URL       string `json:"url"`
		Text      string `json:"text"`
	}
	if !readBody(w, r, &body) {
		return
	}
	st, err := h.cfg.Core.StartComments(r.Context(), app.CommentRequest{SessionID: body.SessionID, URL: body.URL, Text: body.Text})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

type viewerRecJSON struct {
	ID          string `json:"id"`
	CommentID   string `json:"commentId"`
	StartMs     int64  `json:"startMs"`
	EndMs       *int64 `json:"endMs"`
	Description string `json:"description"`
	CommentText string `json:"commentText"`
	WatchURL    string `json:"watchUrl,omitempty"`
}

type commentLineJSON struct {
	CommentID string   `json:"commentId"`
	Text      string   `json:"text"`
	Reasons   []string `json:"reasons"`
}

type commentsJSON struct {
	CaptureID       string            `json:"captureId"`
	Source          string            `json:"source"`
	State           string            `json:"state"`
	Pages           int               `json:"pages"`
	ReportedCount   *int              `json:"reportedCount"`
	CommentCount    int               `json:"commentCount"`
	Issues          []string          `json:"issues"`
	CreatedAt       time.Time         `json:"createdAt"`
	Recommendations []viewerRecJSON   `json:"recommendations"`
	NeedsReview     []commentLineJSON `json:"needsReview"`
}

func toViewerRecJSON(r app.ViewerRecView) viewerRecJSON {
	out := viewerRecJSON{ID: r.ID, CommentID: r.CommentID, StartMs: r.StartMs, Description: r.Description,
		CommentText: r.CommentText, WatchURL: r.WatchURL}
	if r.EndMs > 0 {
		end := r.EndMs
		out.EndMs = &end
	}
	return out
}

// comments returns the newest comment capture, or null when there is none.
func (h *handler) comments(w http.ResponseWriter, r *http.Request) {
	v, err := h.cfg.Core.Comments(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	if v == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	out := commentsJSON{CaptureID: v.CaptureID, Source: v.Source, State: v.State, Pages: v.Pages, CommentCount: v.CommentCount,
		Issues: nonNil(v.Issues), CreatedAt: v.CreatedAt, Recommendations: []viewerRecJSON{}, NeedsReview: []commentLineJSON{}}
	if v.ReportedCount >= 0 {
		out.ReportedCount = &v.ReportedCount
	}
	for _, rec := range v.Recommendations {
		out.Recommendations = append(out.Recommendations, toViewerRecJSON(rec))
	}
	for _, l := range v.NeedsReview {
		out.NeedsReview = append(out.NeedsReview, commentLineJSON{CommentID: l.CommentID, Text: l.Text, Reasons: nonNil(l.Reasons)})
	}
	writeJSON(w, http.StatusOK, out)
}

type timelineItemJSON struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	OriginID    string `json:"originId"`
	AnchorMs    int64  `json:"anchorMs"`
	ClipStartMs int64  `json:"clipStartMs"`
	ClipEndMs   int64  `json:"clipEndMs"`
	Title       string `json:"title"`
	CommentText string `json:"commentText,omitempty"`
	Rationale   string `json:"rationale,omitempty"`
	Uncertainty string `json:"uncertainty,omitempty"`
	Priority    string `json:"priority,omitempty"`
	// AI recommendations: the items the recommendation explains (empty: a new moment).
	SourceCandidateIDs []string `json:"sourceCandidateIds,omitempty"`
	WatchURL           string   `json:"watchUrl,omitempty"`
	// Cut plans: the spans to keep, the plan to open, whether both checks passed.
	Kind      string             `json:"kind,omitempty"`
	Spans     []app.TimelineSpan `json:"spans,omitempty"`
	CutPlanID string             `json:"cutPlanId,omitempty"`
	Verified  bool               `json:"verified,omitempty"`
	Version   int                `json:"version,omitempty"`
}

// timeline is the unified timeline: rule candidates, viewer and AI recommendations.
func (h *handler) timeline(w http.ResponseWriter, r *http.Request) {
	t, err := h.cfg.Core.Timeline(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	items := make([]timelineItemJSON, len(t.Items))
	for i, it := range t.Items {
		items[i] = timelineItemJSON{ID: it.ID, Source: it.Source, OriginID: it.OriginID, AnchorMs: it.AnchorMs,
			ClipStartMs: it.ClipStartMs, ClipEndMs: it.ClipEndMs, Title: it.Title, CommentText: it.CommentText,
			Rationale: it.Rationale, Uncertainty: it.Uncertainty, Priority: it.Priority, SourceCandidateIDs: it.SourceCandidateIDs,
			WatchURL: it.WatchURL, Kind: it.Kind, Spans: it.Spans, CutPlanID: it.CutPlanID, Verified: it.Verified, Version: it.Version}
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessionId": t.SessionID, "title": t.Title, "durationMs": t.DurationMs,
		"analysisId": t.AnalysisID, "captureId": t.CaptureID, "aiRunId": t.AIRunID, "items": items})
}
