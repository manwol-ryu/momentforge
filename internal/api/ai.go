package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
)

func (h *handler) aiProviders(w http.ResponseWriter, r *http.Request) {
	type providerJSON struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Configured bool   `json:"configured"`
	}
	out := []providerJSON{}
	for _, p := range h.cfg.Core.AIProviders() {
		out = append(out, providerJSON{p.ID, p.Name, p.Configured})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out, "keyStorage": "memory"})
}

// aiKey keeps a key in server memory; it is never echoed back or stored.
func (h *handler) aiKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
	}
	if !readBody(w, r, &body) {
		return
	}
	ok, err := h.cfg.Core.SetAIKey(body.Provider, body.Key)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": ok, "storage": "memory"})
}

func (h *handler) aiPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID string   `json:"sessionId"`
		Provider  string   `json:"provider"`
		Model     string   `json:"model"`
		Mode      string   `json:"mode"` // candidates | range
		ItemIDs   []string `json:"itemIds"`
		StartMs   int64    `json:"startMs"`
		EndMs     int64    `json:"endMs"`
	}
	if !readBody(w, r, &body) {
		return
	}
	req := app.AIPreviewRequest{SessionID: body.SessionID, Provider: body.Provider, Model: body.Model,
		StartMs: body.StartMs, EndMs: body.EndMs}
	if body.Mode != "range" {
		req.ItemIDs = nonNil(body.ItemIDs)
	}
	pv, err := h.cfg.Core.PreviewAI(r.Context(), req)
	if err != nil {
		writeAppError(w, err)
		return
	}
	// The plan is shown as is: it is exactly what will be sent.
	writeJSON(w, http.StatusOK, map[string]any{"previewId": pv.ID, "expiresAt": pv.ExpiresAt.UTC().Format(time.RFC3339), "plan": pv.Plan})
}

func (h *handler) aiStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PreviewID   string `json:"previewId"`
		Fingerprint string `json:"fingerprint"`
		Regenerate  bool   `json:"regenerate"`
	}
	if !readBody(w, r, &body) {
		return
	}
	id, err := h.cfg.Core.StartAI(r.Context(), body.PreviewID, body.Fingerprint, body.Regenerate)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"id": id})
}

type aiRunJSON struct {
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

func (h *handler) aiRun(w http.ResponseWriter, r *http.Request) {
	v, err := h.cfg.Core.AIRun(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, aiRunJSON(v))
}

func (h *handler) aiCancel(w http.ResponseWriter, r *http.Request) {
	h.cfg.Core.CancelAI(chi.URLParam(r, "id"))
	writeJSON(w, http.StatusAccepted, map[string]bool{"cancelling": true})
}
