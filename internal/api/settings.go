package api

import (
	"net/http"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

type settingsJSON struct {
	AIProvider      string            `json:"aiProvider"`
	AIModels        map[string]string `json:"aiModels"`
	AIEndpoints     map[string]string `json:"aiEndpoints"`
	Sensitive       bool              `json:"sensitive"`
	IncludeComments bool              `json:"includeComments"`
	ViewerAdaptive  bool              `json:"viewerAdaptive"`
	File            string            `json:"file"`
	KeyFile         string            `json:"keyFile"`
}

func toSettingsJSON(s app.SettingsInfo) settingsJSON {
	models, endpoints := s.AIModels, s.AIEndpoints
	if models == nil {
		models = map[string]string{}
	}
	if endpoints == nil {
		endpoints = map[string]string{}
	}
	return settingsJSON{AIProvider: s.AIProvider, AIModels: models, AIEndpoints: endpoints, Sensitive: s.Sensitive,
		IncludeComments: s.IncludeComments, ViewerAdaptive: s.ViewerAdaptive, File: s.File, KeyFile: s.KeyFile}
}

// settings returns the defaults the screens start from (nothing secret).
func (h *handler) settings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, toSettingsJSON(h.cfg.Core.Settings()))
}

// saveSettings replaces every default at once, so each one must be given.
func (h *handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AIProvider      *string           `json:"aiProvider"`
		AIModels        map[string]string `json:"aiModels"`
		AIEndpoints     map[string]string `json:"aiEndpoints"`
		Sensitive       *bool             `json:"sensitive"`
		IncludeComments *bool             `json:"includeComments"`
		ViewerAdaptive  *bool             `json:"viewerAdaptive"`
	}
	if !readBody(w, r, &body) {
		return
	}
	if body.AIProvider == nil || body.Sensitive == nil || body.IncludeComments == nil || body.ViewerAdaptive == nil {
		writeAppError(w, domain.NewUserError("INPUT", "설정 값이 빠졌습니다. 화면을 새로 고친 뒤 다시 저장하세요."))
		return
	}
	s, err := h.cfg.Core.SaveSettings(app.Settings{AIProvider: *body.AIProvider, AIModels: body.AIModels, AIEndpoints: body.AIEndpoints,
		Sensitive: *body.Sensitive, IncludeComments: *body.IncludeComments, ViewerAdaptive: *body.ViewerAdaptive})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSettingsJSON(s))
}
