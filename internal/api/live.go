package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// --- LIVE capture ---

type liveURLBody struct {
	URL            string `json:"url"`
	Sensitive      bool   `json:"sensitive"`
	ViewerAdaptive *bool  `json:"viewerAdaptive"` // default true
}

func (h *handler) inspectLive(w http.ResponseWriter, r *http.Request) {
	var body liveURLBody
	if !readBody(w, r, &body) {
		return
	}
	li, err := h.cfg.Core.InspectLive(r.Context(), body.URL)
	if err != nil {
		writeAppError(w, err)
		return
	}
	var started *time.Time
	if !li.StartedAt.IsZero() {
		started = &li.StartedAt
	}
	var viewers *int
	if li.Viewers >= 0 {
		viewers = &li.Viewers
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"platformName": li.PlatformName, "channelId": li.ChannelID, "liveId": li.LiveID, "title": li.Title,
		"open": li.Open, "viewers": viewers, "startedAt": started,
	})
}

func (h *handler) startLive(w http.ResponseWriter, r *http.Request) {
	var body liveURLBody
	if !readBody(w, r, &body) {
		return
	}
	adaptive := body.ViewerAdaptive == nil || *body.ViewerAdaptive
	s, err := h.cfg.Core.StartLive(r.Context(), app.LiveRequest{URL: body.URL, Sensitive: body.Sensitive, ViewerAdaptive: adaptive})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSessionJSON(s))
}

type liveJSON struct {
	Session          sessionJSON    `json:"session"`
	ChannelID        string         `json:"channelId"`
	LiveID           string         `json:"liveId"`
	TimeBasis        string         `json:"timeBasis"`
	Running          bool           `json:"running"`
	Sensitive        bool           `json:"sensitive"`
	AdaptiveGate     bool           `json:"adaptiveGate"`
	ViewerAdaptive   bool           `json:"viewerAdaptive"`
	WatermarkMs      int64          `json:"watermarkMs"`
	Generation       int            `json:"generation"`
	ViewerCount      *int           `json:"viewerCount"`
	ViewerStatus     string         `json:"viewerStatus"`
	LateCount        int            `json:"lateCount"`
	DuplicateCount   int            `json:"duplicateCount"`
	InvalidTimeCount int            `json:"invalidTimeCount"`
	Revision         int64          `json:"revision"`
	ClosedReason     string         `json:"closedReason,omitempty"`
	Gaps             []gapJSON      `json:"gaps"`
	LastWindow       *domain.Window `json:"lastWindow"`
}

func (h *handler) liveStatus(w http.ResponseWriter, r *http.Request) {
	v, err := h.cfg.Core.LiveStatus(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	var viewers *int
	if v.ViewerCount >= 0 {
		viewers = &v.ViewerCount
	}
	writeJSON(w, http.StatusOK, liveJSON{Session: toSessionJSON(v.Session), ChannelID: v.ChannelID, LiveID: v.LiveID,
		TimeBasis: v.TimeBasis, Running: v.Running, Sensitive: v.Sensitive, AdaptiveGate: v.AdaptiveGate, ViewerAdaptive: v.ViewerAdaptive,
		WatermarkMs: v.WatermarkMs, Generation: v.Generation, ViewerCount: viewers, ViewerStatus: v.ViewerStatus,
		LateCount: v.LateCount, DuplicateCount: v.DuplicateCount, InvalidTimeCount: v.InvalidTimeCount,
		Revision: v.Revision, ClosedReason: v.ClosedReason, Gaps: toGapsJSON(v.Gaps), LastWindow: v.LastWindow})
}

type gapJSON struct {
	StartMs int64 `json:"startMs"`
	EndMs   int64 `json:"endMs"`
}

func toGapsJSON(gaps []domain.Gap) []gapJSON {
	out := make([]gapJSON, len(gaps))
	for i, g := range gaps {
		out[i] = gapJSON{g.StartMs, g.EndMs}
	}
	return out
}

func (h *handler) stopLive(w http.ResponseWriter, r *http.Request) {
	h.cfg.Core.StopLive(chi.URLParam(r, "id"))
	writeJSON(w, http.StatusAccepted, map[string]bool{"stopping": true})
}

func (h *handler) resumeLive(w http.ResponseWriter, r *http.Request) {
	s, err := h.cfg.Core.ResumeLive(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, toSessionJSON(s))
}

func (h *handler) liveChats(w http.ResponseWriter, r *http.Request) {
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after, err = 0, nil
	}
	if err != nil || after < 0 {
		writeError(w, http.StatusBadRequest, "INPUT", "채팅 조회 위치가 잘못되었습니다.")
		return
	}
	chats, err := h.cfg.Core.LiveChats(r.Context(), chi.URLParam(r, "id"), after)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": nonNil(chats)})
}

func (h *handler) liveContext(w http.ResponseWriter, r *http.Request) {
	at, err := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "시간이 잘못되었습니다.")
		return
	}
	chats, err := h.cfg.Core.LiveContext(r.Context(), chi.URLParam(r, "id"), at)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": nonNil(chats)})
}

func (h *handler) liveCandidates(w http.ResponseWriter, r *http.Request) {
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if r.URL.Query().Get("after") == "" {
		after, err = 0, nil
	}
	if err != nil || after < 0 {
		writeError(w, http.StatusBadRequest, "INPUT", "후보 조회 위치가 잘못되었습니다.")
		return
	}
	cands, rev, err := h.cfg.Core.LiveCandidates(r.Context(), chi.URLParam(r, "id"), after)
	if err != nil {
		writeAppError(w, err)
		return
	}
	out := make([]candidateJSON, 0, len(cands))
	for _, c := range cands {
		out = append(out, toCandidateJSON(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": out, "revision": rev})
}

func (h *handler) liveCandidateChats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, err1 := atoiDefault(q.Get("offset"), 0)
	limit, err2 := atoiDefault(q.Get("limit"), 200)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "채팅 조회 위치가 잘못되었습니다.")
		return
	}
	chats, total, err := h.cfg.Core.LiveCandidateChats(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "cid"), offset, limit)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeContextChats(w, chats, total, offset)
}

func (h *handler) liveCheckpoint(w http.ResponseWriter, r *http.Request) {
	cp, err := h.cfg.Core.LiveCheckpoint(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": cp.ID, "folder": cp.Folder, "eventCount": cp.EventCount})
}

func (h *handler) liveDensity(w http.ResponseWriter, r *http.Request) {
	d, err := h.cfg.Core.LiveDensity(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"bucketMs": d.BucketMs, "endMs": d.EndMs, "windows": nonNil(d.Windows)})
}
