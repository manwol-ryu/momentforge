package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"slices"
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
	// How the windows were cut and judged, for drawing them: window length
	// and step, and for a whole-broadcast analysis the score a candidate's
	// peak needs (0 for the incremental engine).
	WindowMs, StepMs int64
	ScoreThreshold   float64
	Windows          []domain.Window
	Candidates       []CandidateView
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
		// New candidates get their explanation when 일반 설정 says so (explained moments keep theirs).
		return a.runAfter(ctx, h, sid, autoSteps{ai: a.Settings().AutoAI}, "분석이 끝났습니다.")
	})
}

// storedChat is a session as stored with all its chat (in time order) and
// gaps, read once when a job both writes the chat files and analyzes.
type storedChat struct {
	s      store.Session
	events []domain.ChatEvent
	gaps   []domain.Gap
}

func (a *App) loadChat(ctx context.Context, sid string) (storedChat, error) {
	st := a.cfg.Store
	s, err := st.Session(ctx, sid)
	if err != nil {
		return storedChat{}, err
	}
	events, err := st.Chats(ctx, sid, store.AllSeq, false)
	if err != nil {
		return storedChat{}, err
	}
	gaps, err := st.Gaps(ctx, sid)
	if err != nil {
		return storedChat{}, err
	}
	return storedChat{s: s, events: events, gaps: gaps}, nil
}

// byArrival returns the chat in arrival order (chat.received.txt).
func (c storedChat) byArrival() []domain.ChatEvent {
	out := slices.Clone(c.events)
	slices.SortFunc(out, func(x, y domain.ChatEvent) int { return cmp.Compare(x.Seq, y.Seq) })
	return out
}

// upTo returns the chat of an analysis snapshot (seq <= maxSeq), in time order.
func upTo(events []domain.ChatEvent, maxSeq int64) []domain.ChatEvent {
	if !slices.ContainsFunc(events, func(e domain.ChatEvent) bool { return e.Seq > maxSeq }) {
		return events
	}
	out := make([]domain.ChatEvent, 0, len(events))
	for _, e := range events {
		if e.Seq <= maxSeq {
			out = append(out, e)
		}
	}
	return out
}

// analyzeSession runs detection over everything collected so far, stores
// the result with its chat snapshot, and writes the analysis files.
func (a *App) analyzeSession(ctx context.Context, sid string, p detect.Params, h job) (*store.Analysis, error) {
	c, err := a.loadChat(ctx, sid)
	if err != nil {
		return nil, err
	}
	return a.analyzeChat(ctx, c, p, h)
}

// analyzeChat is analyzeSession on chat already read.
func (a *App) analyzeChat(ctx context.Context, c storedChat, p detect.Params, h job) (*store.Analysis, error) {
	st := a.cfg.Store
	s, sid, events, gaps := c.s, c.s.ID, c.events, c.gaps
	end := s.Cursor
	if s.State == store.StateFinished {
		end = s.DurationMs
	}
	// A stored timeline is analyzed as a whole (REDESIGN 6.2.2); params
	// without whole-broadcast settings keep the incremental engine.
	analyze := detect.Analyze
	if p.Broadcast != nil {
		analyze = detect.AnalyzeBroadcast
	}
	res, err := analyze(ctx, events, end, gaps, p, a.cfg.Rules, func(pr detect.Progress) {
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
	a.writeUnified(context.WithoutCancel(ctx), sid)
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
	c, err := a.loadChat(ctx, sid)
	if err != nil {
		return err
	}
	return writeChatFiles(a.exportDir(sid), c)
}

// writeChatFiles writes the session's chat files (manifest.json, chat.txt, chat.jsonl) into dir.
func writeChatFiles(dir string, c storedChat) error {
	s := c.s
	finished := s.State == store.StateFinished
	avail := s.Cursor
	if finished {
		avail = s.DurationMs
	}
	return export.WriteChat(dir, export.ChatManifest{
		ID: s.ID, Platform: s.Platform, TargetID: s.TargetID, Title: s.Title, DurationMs: s.DurationMs,
		ReportedDurationMs: s.ReportedDurationMs, State: s.State, Cursor: s.Cursor, Pages: s.Pages,
		Gaps: c.gaps, Error: s.ErrorCode, AvailableEndMs: avail,
		Completeness: export.Completeness(finished, len(c.gaps)), RetrievalFinished: finished,
	}, c.events)
}

// evidenceChats reads the evidence messages of cands (those up to maxSeq)
// by event ID, for summarize.
func (a *App) evidenceChats(ctx context.Context, sid string, cands []domain.Candidate, maxSeq int64) (map[string]domain.ChatEvent, error) {
	var ids []string
	for _, c := range cands {
		ids = append(ids, c.EvidenceIDs...)
	}
	chats, err := a.cfg.Store.ChatsByID(ctx, sid, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.ChatEvent, len(chats))
	for _, e := range chats {
		if e.Seq <= maxSeq {
			byID[e.ID] = e
		}
	}
	return byID, nil
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
	// Only the evidence is read: a long broadcast has hundreds of thousands of messages.
	byID, err := a.evidenceChats(ctx, an.SessionID, an.Candidates, an.MaxSeq)
	if err != nil {
		return nil, err
	}
	view := &AnalysisView{ID: an.ID, SessionID: s.ID, Title: s.Title, DurationMs: an.DurationMs,
		RulesVersion: an.RulesVersion, Sensitive: p.Sensitive, WindowMs: p.WindowMs, StepMs: p.StepMs, Windows: an.Windows}
	if p.Broadcast != nil {
		view.ScoreThreshold = p.Broadcast.OnZ
		if p.Sensitive {
			view.ScoreThreshold = p.Broadcast.SensitiveOnZ
		}
	}
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
