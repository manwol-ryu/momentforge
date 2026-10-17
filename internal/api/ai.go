package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

func (h *handler) aiProviders(w http.ResponseWriter, r *http.Request) {
	type providerJSON struct {
		ID            string `json:"id"`
		Name          string `json:"name"`
		Configured    bool   `json:"configured"`
		SelfAuth      bool   `json:"selfAuth"`
		AuthHelp      string `json:"authHelp,omitempty"`
		NotReady      string `json:"notReady,omitempty"`
		DefaultModel  string `json:"defaultModel"`
		CanCount      bool   `json:"canCount"`
		CanListModels bool   `json:"canListModels"`
		CanBatch      bool   `json:"canBatch"`
		HasPrices     bool   `json:"hasPrices"`
		KeySource     string `json:"keySource,omitempty"` // env | file | memory
		KeyFoundIn    string `json:"keyFoundIn,omitempty"`
		KeyName       string `json:"keyName,omitempty"`
		// The address requests go to, for a provider sending to a proxy the user runs.
		Endpoint        string `json:"endpoint,omitempty"`
		DefaultEndpoint string `json:"defaultEndpoint,omitempty"`
		HasService      bool   `json:"hasService"`
	}
	out := []providerJSON{}
	for _, p := range h.cfg.Core.AIProviders() {
		out = append(out, providerJSON(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// aiKey keeps a key in server memory, and in the data folder's .env only
// with persist. The key is never echoed back or logged.
func (h *handler) aiKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Key      string `json:"key"`
		Persist  bool   `json:"persist"`
	}
	if !readBody(w, r, &body) {
		return
	}
	source, err := h.cfg.Core.SetAIKey(body.Provider, body.Key, body.Persist)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"configured": source != "", "keySource": source})
}

// aiModels asks the provider which models the key can use (only the key is sent).
func (h *handler) aiModels(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
	}
	if !readBody(w, r, &body) {
		return
	}
	models, err := h.cfg.Core.AIModels(r.Context(), body.Provider)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"models": nonNil(models)})
}

func (h *handler) aiPreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SessionID  string   `json:"sessionId"`
		Provider   string   `json:"provider"`
		Model      string   `json:"model"`
		Mode       string   `json:"mode"` // candidates | all | range
		ItemIDs    []string `json:"itemIds"`
		StartMs    int64    `json:"startMs"`
		EndMs      int64    `json:"endMs"`
		ToEnd      bool     `json:"toEnd"`
		Delivery   string   `json:"delivery"` // single | batch
		CostCapUSD float64  `json:"costCapUsd"`
	}
	if !readBody(w, r, &body) {
		return
	}
	req := app.AIPreviewRequest{SessionID: body.SessionID, Provider: body.Provider, Model: body.Model,
		StartMs: body.StartMs, EndMs: body.EndMs, ToEnd: body.ToEnd, Delivery: body.Delivery, CostCapUSD: body.CostCapUSD}
	switch body.Mode {
	case "all":
		req.All = true
	case "range":
	default:
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

// aiCount counts a preview's input tokens with the provider (sends the previewed chats).
func (h *handler) aiCount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PreviewID   string `json:"previewId"`
		Fingerprint string `json:"fingerprint"`
	}
	if !readBody(w, r, &body) {
		return
	}
	c, err := h.cfg.Core.CountAIPreview(r.Context(), body.PreviewID, body.Fingerprint)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"inputTokens": c.InputTokens, "costUpperUsd": c.CostUpperUSD, "hasPrice": c.HasPrice})
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

// AIRunView carries its own JSON names; it is also the "ai" event.
func (h *handler) aiRun(w http.ResponseWriter, r *http.Request) {
	v, err := h.cfg.Core.AIRun(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (h *handler) aiRuns(w http.ResponseWriter, r *http.Request) {
	runs, err := h.cfg.Core.AIRuns(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (h *handler) aiCancel(w http.ResponseWriter, r *http.Request) {
	h.cfg.Core.CancelAI(chi.URLParam(r, "id"))
	writeJSON(w, http.StatusAccepted, map[string]bool{"cancelling": true})
}

type aiRecJSON struct {
	ID                 string   `json:"id"`
	SourceCandidateIDs []string `json:"sourceCandidateIds"`
	StartMs            int64    `json:"startMs"`
	EndMs              int64    `json:"endMs"`
	Title              string   `json:"title"`
	Rationale          string   `json:"rationale"`
	Uncertainty        string   `json:"uncertainty"`
	Priority           string   `json:"priority"`
}

func (h *handler) aiCompare(w http.ResponseWriter, r *http.Request) {
	c, err := h.cfg.Core.CompareAI(r.Context(), r.URL.Query().Get("a"), r.URL.Query().Get("b"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	type matchJSON struct {
		A         aiRecJSON `json:"a"`
		B         aiRecJSON `json:"b"`
		OverlapMs int64     `json:"overlapMs"`
	}
	recs := func(list []app.AIRecommendation) []aiRecJSON {
		out := []aiRecJSON{}
		for _, r := range list {
			out = append(out, aiRecJSON{ID: r.ID, SourceCandidateIDs: nonNil(r.SourceCandidateIDs), StartMs: r.StartMs,
				EndMs: r.EndMs, Title: r.Title, Rationale: r.Rationale, Uncertainty: r.Uncertainty, Priority: r.Priority})
		}
		return out
	}
	matched := []matchJSON{}
	for _, m := range c.Matched {
		pair := recs([]app.AIRecommendation{m.A, m.B})
		matched = append(matched, matchJSON{A: pair[0], B: pair[1], OverlapMs: m.OverlapMs})
	}
	writeJSON(w, http.StatusOK, map[string]any{"a": c.A, "b": c.B, "sameInput": c.SameInput, "matched": matched,
		"onlyA": recs(c.OnlyA), "onlyB": recs(c.OnlyB)})
}

type aiServiceJSON struct {
	Installed bool   `json:"installed"`
	Running   bool   `json:"running"`
	Managed   bool   `json:"managed"`
	Program   string `json:"program,omitempty"`
	Config    string `json:"config,omitempty"`
	Log       string `json:"log,omitempty"`
	Note      string `json:"note,omitempty"`
}

// aiService reports whether a provider's local program (CLIProxyAPI) runs.
func (h *handler) aiService(w http.ResponseWriter, r *http.Request) {
	s, err := h.cfg.Core.AIServiceStatus(r.Context(), r.URL.Query().Get("provider"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, aiServiceJSON(s))
}

// aiServiceControl starts or stops a provider's local program.
func (h *handler) aiServiceControl(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Provider string `json:"provider"`
		Action   string `json:"action"` // start | stop
	}
	if !readBody(w, r, &body) {
		return
	}
	var (
		s   app.AIService
		err error
	)
	switch body.Action {
	case "start":
		s, err = h.cfg.Core.StartAIService(r.Context(), body.Provider)
	case "stop":
		s, err = h.cfg.Core.StopAIService(r.Context(), body.Provider)
	default:
		writeAppError(w, domain.NewUserError("INPUT", "켜기 또는 끄기를 골라 주세요."))
		return
	}
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, aiServiceJSON(s))
}
