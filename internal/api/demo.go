package api

import (
	"net/http"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

type analysisResponse struct {
	Title        string          `json:"title"`
	DurationMs   int64           `json:"durationMs"`
	RulesVersion string          `json:"rulesVersion"`
	Windows      []domain.Window `json:"windows"`
	Candidates   []candidateJSON `json:"candidates"`
}

type candidateJSON struct {
	domain.Candidate
	Evidence      []chatJSON `json:"evidence"`
	EvidenceTotal int        `json:"evidenceTotal"`
}

type chatJSON struct {
	OffsetMs int64  `json:"offsetMs"`
	Author   string `json:"author"` // pseudonym, never the original id
	Text     string `json:"text"`
	Count    int    `json:"count"` // evidence messages with the same text
}

func (h *handler) demo(w http.ResponseWriter, r *http.Request) {
	a, err := h.cfg.Core.Demo(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toAnalysisResponse(a))
}

func toAnalysisResponse(a *app.Analysis) analysisResponse {
	out := analysisResponse{
		Title: a.Title, DurationMs: a.DurationMs, RulesVersion: a.RulesVersion,
		Windows: a.Windows, Candidates: make([]candidateJSON, 0, len(a.Candidates)),
	}
	for _, c := range a.Candidates {
		cj := candidateJSON{Candidate: c.Candidate, EvidenceTotal: c.EvidenceTotal, Evidence: []chatJSON{}}
		for _, e := range c.Evidence {
			cj.Evidence = append(cj.Evidence, chatJSON{OffsetMs: e.OffsetMs, Author: e.AuthorKey, Text: e.Text, Count: e.Count})
		}
		out.Candidates = append(out.Candidates, cj)
	}
	return out
}
