package app

import (
	"context"
	"sort"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// evidencePreview is how many distinct evidence messages a candidate shows.
const evidencePreview = 5

// Analysis is an analyzed timeline ready for review.
type Analysis struct {
	Title        string
	DurationMs   int64
	RulesVersion string
	Windows      []domain.Window
	Candidates   []CandidateView
}

// CandidateView is a candidate plus a short summary of its evidence chat.
type CandidateView struct {
	domain.Candidate
	Evidence      []EvidenceLine // most repeated texts first
	EvidenceTotal int
}

// EvidenceLine is the first message with a given text and how often that
// text appears in the candidate's evidence.
type EvidenceLine struct {
	domain.ChatEvent
	Count int
}

// Demo analyzes the built-in synthetic chat. Nothing is stored.
func (a *App) Demo(ctx context.Context) (*Analysis, error) {
	events := domain.DemoChat()
	res, err := detect.Analyze(ctx, events, domain.DemoDurationMs, nil, a.cfg.Params, a.cfg.Rules, nil)
	if err != nil {
		return nil, err
	}
	return &Analysis{
		Title:        "샘플 채팅 (실제 방송 아님)",
		DurationMs:   res.DurationMs,
		RulesVersion: res.RulesVersion,
		Windows:      res.Windows,
		Candidates:   candidateViews(res.Candidates, events),
	}, nil
}

func candidateViews(cands []domain.Candidate, events []domain.ChatEvent) []CandidateView {
	byID := make(map[string]domain.ChatEvent, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	out := make([]CandidateView, 0, len(cands))
	for _, c := range cands {
		out = append(out, CandidateView{Candidate: c, EvidenceTotal: len(c.EvidenceIDs), Evidence: summarize(c.EvidenceIDs, byID)})
	}
	return out
}

// summarize groups evidence by normalized text, so a surge's background chat
// does not hide the reaction everyone shared.
func summarize(ids []string, byID map[string]domain.ChatEvent) []EvidenceLine {
	index := map[string]int{}
	var lines []EvidenceLine
	for _, id := range ids { // ids are in time order
		e, ok := byID[id]
		if !ok {
			continue
		}
		key := detect.Normalize(e.Text)
		if i, seen := index[key]; seen {
			lines[i].Count++
			continue
		}
		index[key] = len(lines)
		lines = append(lines, EvidenceLine{ChatEvent: e, Count: 1})
	}
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Count > lines[j].Count })
	return lines[:min(evidencePreview, len(lines))]
}
