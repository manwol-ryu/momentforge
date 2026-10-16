package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// evidencePreview is how many distinct evidence messages a candidate shows.
const evidencePreview = 5

// AnalysisView is a stored analysis ready for review.
type AnalysisView struct {
	ID           string
	SessionID    string
	Title        string
	DurationMs   int64
	RulesVersion string
	Sensitive    bool
	Windows      []domain.Window
	Candidates   []CandidateView
}

// CandidateView is a candidate plus a short summary of its evidence chat.
type CandidateView struct {
	domain.Candidate
	Evidence      []EvidenceLine // most repeated texts first
	EvidenceTotal int
	WatchURL      string // the platform's own player at the clip start; empty for demo
}

// EvidenceLine is the first message with a given text and how often that
// text appears in the candidate's evidence.
type EvidenceLine struct {
	domain.ChatEvent
	Count int
}

// StartAnalyze re-analyzes a stored session in the background.
func (a *App) StartAnalyze(sid string, sensitive bool) (JobStatus, error) {
	if _, err := a.cfg.Store.Session(a.ctx, sid); err != nil {
		return JobStatus{}, err
	}
	params := a.params(sensitive)
	return a.jobs.start(a.ctx, "analyze", func(ctx context.Context, h job) error {
		s, _ := a.cfg.Store.Session(ctx, sid)
		h.update(func(js *JobStatus) {
			js.SessionID, js.Title, js.DurationMs = s.ID, s.Title, s.DurationMs
			js.Phase, js.Message = "analyzing", "편집 후보를 찾는 중입니다."
		})
		an, err := a.analyzeSession(ctx, sid, params, h)
		if err != nil {
			return err
		}
		h.update(func(js *JobStatus) {
			js.AnalysisID, js.Candidates, js.Message = an.ID, len(an.Candidates), "분석이 끝났습니다."
		})
		return nil
	})
}

// analyzeSession runs detection over everything collected so far, stores
// the result with its chat snapshot, and writes the analysis files.
func (a *App) analyzeSession(ctx context.Context, sid string, p detect.Params, h job) (*store.Analysis, error) {
	st := a.cfg.Store
	s, err := st.Session(ctx, sid)
	if err != nil {
		return nil, err
	}
	end := s.Cursor
	if s.State == store.StateFinished {
		end = s.DurationMs
	}
	events, err := st.Chats(ctx, sid, store.AllSeq, false)
	if err != nil {
		return nil, err
	}
	gaps, err := st.Gaps(ctx, sid)
	if err != nil {
		return nil, err
	}
	res, err := detect.Analyze(ctx, events, end, gaps, p, a.cfg.Rules, func(pr detect.Progress) {
		if h.j != nil {
			h.update(func(js *JobStatus) { js.PositionMs, js.Candidates = pr.PositionMs, pr.Candidates })
		}
	})
	if err != nil {
		return nil, err
	}
	params, _ := json.Marshal(p)
	maxSeq, digest := snapshot(events)
	an := &store.Analysis{
		SessionID: sid, RulesVersion: res.RulesVersion, ParamsJSON: params, DurationMs: end,
		MaxSeq: maxSeq, EventCount: len(events), DataSHA256: digest, SourceState: s.State,
		Windows: res.Windows, Candidates: res.Candidates,
	}
	if err := st.SaveAnalysis(ctx, an); err != nil {
		return nil, err
	}
	m := export.AnalysisManifest{
		AnalysisID: an.ID, SourceSessionID: sid, Params: params, DataSHA256: digest, EventCount: len(events),
		MaxSeq: maxSeq, RulesVersion: res.RulesVersion, DurationMs: end, SourceState: s.State,
		Completeness: export.Completeness(s.State == store.StateFinished, len(gaps)), Gaps: gaps,
	}
	if err := export.WriteAnalysis(filepath.Join(a.exportDir(sid), an.ID), m, s.Title, res.Candidates, res.Windows, events); err != nil {
		a.logError("write analysis files", err)
		if h.j != nil {
			h.update(func(js *JobStatus) {
				js.Warning = "분석 결과 파일을 쓰지 못했습니다. 화면의 결과는 저장되어 있습니다."
			})
		}
	}
	return an, nil
}

// snapshot fingerprints the analyzed chat (max seq and SHA-256 of all events).
func snapshot(events []domain.ChatEvent) (int64, string) {
	h := sha256.New()
	var maxSeq int64
	enc := json.NewEncoder(h)
	for _, e := range events {
		maxSeq = max(maxSeq, e.Seq)
		enc.Encode([]any{e.ID, e.Seq, e.OffsetMs, e.AuthorKey, e.Text})
	}
	return maxSeq, hex.EncodeToString(h.Sum(nil))
}

// writeChat refreshes the session's chat files from the database.
func (a *App) writeChat(ctx context.Context, sid string) error {
	st := a.cfg.Store
	s, err := st.Session(ctx, sid)
	if err != nil {
		return err
	}
	events, err := st.Chats(ctx, sid, store.AllSeq, false)
	if err != nil {
		return err
	}
	gaps, err := st.Gaps(ctx, sid)
	if err != nil {
		return err
	}
	finished := s.State == store.StateFinished
	avail := s.Cursor
	if finished {
		avail = s.DurationMs
	}
	return export.WriteChat(a.exportDir(sid), export.ChatManifest{
		ID: s.ID, Platform: s.Platform, TargetID: s.TargetID, Title: s.Title, DurationMs: s.DurationMs,
		ReportedDurationMs: s.ReportedDurationMs, State: s.State, Cursor: s.Cursor, Pages: s.Pages,
		Gaps: gaps, Error: s.ErrorCode, AvailableEndMs: avail,
		Completeness: export.Completeness(finished, len(gaps)), RetrievalFinished: finished,
	}, events)
}

// Analysis loads a stored analysis for review.
func (a *App) Analysis(ctx context.Context, id string) (*AnalysisView, error) {
	st := a.cfg.Store
	an, err := st.Analysis(ctx, id)
	if err != nil {
		return nil, err
	}
	s, err := st.Session(ctx, an.SessionID)
	if err != nil {
		return nil, err
	}
	var p detect.Params
	json.Unmarshal(an.ParamsJSON, &p)
	events, err := st.Chats(ctx, an.SessionID, an.MaxSeq, false)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.ChatEvent, len(events))
	for _, e := range events {
		byID[e.ID] = e
	}
	view := &AnalysisView{ID: an.ID, SessionID: s.ID, Title: s.Title, DurationMs: an.DurationMs,
		RulesVersion: an.RulesVersion, Sensitive: p.Sensitive, Windows: an.Windows}
	link := a.linker(s)
	for _, c := range an.Candidates {
		cv := CandidateView{Candidate: c, EvidenceTotal: len(c.EvidenceIDs), Evidence: summarize(c.EvidenceIDs, byID)}
		if link != nil {
			cv.WatchURL = link(c.ClipStartMs)
		}
		view.Candidates = append(view.Candidates, cv)
	}
	return view, nil
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
