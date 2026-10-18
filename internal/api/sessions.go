package api

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// readBody decodes a small JSON body (≤16 KB, like CzzEdit).
func readBody(w http.ResponseWriter, r *http.Request, v any) bool {
	data, err := io.ReadAll(io.LimitReader(r.Body, 16<<10+1))
	if err != nil || len(data) > 16<<10 {
		writeError(w, http.StatusBadRequest, "INPUT", "입력 형식이 잘못되었습니다.")
		return false
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	if err := json.Unmarshal(data, v); err != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "입력 형식이 잘못되었습니다.")
		return false
	}
	return true
}

type sensitiveBody struct {
	Sensitive bool `json:"sensitive"`
}

// --- jobs ---

func (h *handler) job(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.cfg.Core.Job())
}

func (h *handler) cancelJob(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, h.cfg.Core.CancelJob())
}

func (h *handler) collect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL             string `json:"url"`
		Sensitive       bool   `json:"sensitive"`
		IncludeComments *bool  `json:"includeComments"` // default true
	}
	if !readBody(w, r, &body) {
		return
	}
	st, err := h.cfg.Core.StartCollect(app.CollectRequest{URL: body.URL, Sensitive: body.Sensitive,
		IncludeComments: body.IncludeComments == nil || *body.IncludeComments})
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (h *handler) resume(w http.ResponseWriter, r *http.Request) {
	var body sensitiveBody
	if !readBody(w, r, &body) {
		return
	}
	st, err := h.cfg.Core.ResumeSession(r.Context(), chi.URLParam(r, "id"), body.Sensitive)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (h *handler) reanalyze(w http.ResponseWriter, r *http.Request) {
	var body sensitiveBody
	if !readBody(w, r, &body) {
		return
	}
	st, err := h.cfg.Core.StartAnalyze(chi.URLParam(r, "id"), body.Sensitive)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, st)
}

func (h *handler) demo(w http.ResponseWriter, r *http.Request) {
	var body sensitiveBody
	if !readBody(w, r, &body) {
		return
	}
	s, err := h.cfg.Core.CreateDemo(r.Context(), body.Sensitive)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSessionJSON(s))
}

// --- sessions ---

type sessionJSON struct {
	ID                 string    `json:"id"`
	Platform           string    `json:"platform"`
	PlatformName       string    `json:"platformName"`
	Kind               string    `json:"kind"`
	TargetID           string    `json:"targetId"`
	Title              string    `json:"title"`
	DurationMs         int64     `json:"durationMs"`
	ReportedDurationMs int64     `json:"reportedDurationMs"`
	State              string    `json:"state"`
	Cursor             int64     `json:"cursor"`
	Pages              int       `json:"pages"`
	ChatCount          int       `json:"chatCount"`
	ErrorCode          string    `json:"errorCode,omitempty"`
	ErrorMessage       string    `json:"errorMessage,omitempty"`
	LatestAnalysisID   string    `json:"latestAnalysisId,omitempty"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
	IsDemo             bool      `json:"isDemo"`
	CanResume          bool      `json:"canResume"`
	CanPlay            bool      `json:"canPlay"`
	CanReadComments    bool      `json:"canReadComments"`
	WatchURL           string    `json:"watchUrl,omitempty"`
}

func toSessionJSON(s app.SessionInfo) sessionJSON {
	return sessionJSON{
		ID: s.ID, Platform: s.Platform, PlatformName: s.PlatformName, Kind: s.Kind, TargetID: s.TargetID,
		Title: s.Title, DurationMs: s.DurationMs, ReportedDurationMs: s.ReportedDurationMs, State: s.State,
		Cursor: s.Cursor, Pages: s.Pages, ChatCount: s.ChatCount, ErrorCode: s.ErrorCode,
		ErrorMessage: s.ErrorMessage, LatestAnalysisID: s.LatestAnalysisID, CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt, IsDemo: s.IsDemo, CanResume: s.CanResume, CanPlay: s.CanPlay, CanReadComments: s.CanReadComments,
		WatchURL: s.WatchURL,
	}
}

func (h *handler) sessions(w http.ResponseWriter, r *http.Request) {
	list, err := h.cfg.Core.Sessions(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	out := make([]sessionJSON, len(list))
	for i, s := range list {
		out[i] = toSessionJSON(s)
	}
	writeJSON(w, http.StatusOK, out)
}

func (h *handler) hideSessions(w http.ResponseWriter, r *http.Request) {
	n, err := h.cfg.Core.HideSessions(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"changed": n})
}

func (h *handler) restoreSessions(w http.ResponseWriter, r *http.Request) {
	n, err := h.cfg.Core.RestoreSessions(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"changed": n})
}

func (h *handler) session(w http.ResponseWriter, r *http.Request) {
	s, err := h.cfg.Core.Session(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, toSessionJSON(s))
}

type chatJSON struct {
	ID       string `json:"id"`
	OffsetMs int64  `json:"offsetMs"`
	Author   string `json:"author"` // pseudonym, never the original id
	Text     string `json:"text"`
	Kind     string `json:"kind,omitempty"`   // donation | subscription | system; absent for a message
	Amount   int64  `json:"amount,omitempty"` // won, or months subscribed
	Hidden   bool   `json:"hidden,omitempty"`
}

func toChatJSON(e domain.ChatEvent) chatJSON {
	return chatJSON{ID: e.ID, OffsetMs: e.OffsetMs, Author: e.AuthorKey, Text: e.Text, Kind: e.Kind, Amount: e.Amount, Hidden: e.Hidden}
}

func (h *handler) chatsAt(w http.ResponseWriter, r *http.Request) {
	at, err := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "재생 시간이 잘못되었습니다.")
		return
	}
	pc, err := h.cfg.Core.ChatsAt(r.Context(), chi.URLParam(r, "id"), r.URL.Query().Get("analysis"), at)
	if err != nil {
		writeAppError(w, err)
		return
	}
	chats := make([]chatJSON, len(pc.Chats))
	for i, c := range pc.Chats {
		chats[i] = toChatJSON(c)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"chats": chats, "startMs": pc.StartMs, "endMs": pc.EndMs, "atMs": pc.AtMs, "truncated": pc.Truncated,
	})
}

func (h *handler) playback(w http.ResponseWriter, r *http.Request) {
	pb, err := h.cfg.Core.Playback(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": pb.URL, "type": pb.Type})
}

// --- files ---

func (h *handler) files(w http.ResponseWriter, r *http.Request) {
	list, err := h.cfg.Core.Files(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	type fileJSON struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	out := make([]fileJSON, len(list))
	for i, f := range list {
		out[i] = fileJSON{Path: f.Path, Size: f.Size}
	}
	writeJSON(w, http.StatusOK, out)
}

// download streams a fixed length: a file still growing during collection
// is sent as it was when opened.
func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	f, err := h.cfg.Core.OpenFile(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "*"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "STORAGE", "파일을 읽지 못했습니다.")
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "application/octet-stream")
	hd.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	hd.Set("Content-Disposition", `attachment; filename="`+path.Base(chi.URLParam(r, "*"))+`"`)
	w.WriteHeader(http.StatusOK)
	io.CopyN(w, f, info.Size())
}

// --- analyses ---

type candidateJSON struct {
	domain.Candidate
	Evidence      []evidenceJSON `json:"evidence"`
	EvidenceTotal int            `json:"evidenceTotal"`
	WatchURL      string         `json:"watchUrl,omitempty"`
}

type evidenceJSON struct {
	chatJSON
	Count int `json:"count"` // evidence messages with the same text
}

type analysisJSON struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"sessionId"`
	Title        string          `json:"title"`
	DurationMs   int64           `json:"durationMs"`
	RulesVersion string          `json:"rulesVersion"`
	Sensitive    bool            `json:"sensitive"`
	Windows      []domain.Window `json:"windows"`
	Candidates   []candidateJSON `json:"candidates"`
}

func (h *handler) analysis(w http.ResponseWriter, r *http.Request) {
	a, err := h.cfg.Core.Analysis(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeAppError(w, err)
		return
	}
	out := analysisJSON{ID: a.ID, SessionID: a.SessionID, Title: a.Title, DurationMs: a.DurationMs,
		RulesVersion: a.RulesVersion, Sensitive: a.Sensitive, Windows: nonNil(a.Windows),
		Candidates: make([]candidateJSON, 0, len(a.Candidates))}
	for _, c := range a.Candidates {
		out.Candidates = append(out.Candidates, toCandidateJSON(c))
	}
	writeJSON(w, http.StatusOK, out)
}

func toCandidateJSON(c app.CandidateView) candidateJSON {
	cj := candidateJSON{Candidate: c.Candidate, EvidenceTotal: c.EvidenceTotal, WatchURL: c.WatchURL, Evidence: []evidenceJSON{}}
	for _, e := range c.Evidence {
		cj.Evidence = append(cj.Evidence, evidenceJSON{chatJSON: toChatJSON(e.ChatEvent), Count: e.Count})
	}
	return cj
}

func (h *handler) candidateChats(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	offset, err1 := atoiDefault(q.Get("offset"), 0)
	limit, err2 := atoiDefault(q.Get("limit"), 200)
	if err1 != nil || err2 != nil {
		writeError(w, http.StatusBadRequest, "INPUT", "채팅 조회 위치가 잘못되었습니다.")
		return
	}
	chats, total, err := h.cfg.Core.CandidateChats(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "cid"), offset, limit)
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeContextChats(w, chats, total, offset)
}

func writeContextChats(w http.ResponseWriter, chats []app.ContextChat, total, offset int) {
	type contextJSON struct {
		chatJSON
		IsEvidence bool `json:"isEvidence"`
	}
	out := make([]contextJSON, len(chats))
	for i, c := range chats {
		out[i] = contextJSON{chatJSON: toChatJSON(c.ChatEvent), IsEvidence: c.IsEvidence}
	}
	var next *int
	if offset+len(chats) < total {
		n := offset + len(chats)
		next = &n
	}
	writeJSON(w, http.StatusOK, map[string]any{"chats": out, "total": total, "nextOffset": next})
}

func atoiDefault(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}
