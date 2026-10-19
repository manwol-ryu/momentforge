package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
)

// bundle reports the session's backup bundle and what a new one would include.
func (h *handler) bundle(w http.ResponseWriter, r *http.Request) {
	v, err := h.cfg.Core.Bundle(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// startBundle writes (or refreshes) the bundle in the background: {video}.
func (h *handler) startBundle(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Video bool `json:"video"`
	}
	if !readBody(w, r, &body) {
		return
	}
	st, err := h.cfg.Core.StartBundle(r.Context(), app.BundleRequest{SessionID: chi.URLParam(r, "id"), Video: body.Video})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}
