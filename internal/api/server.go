// Package api serves the local HTTP API, the SSE event stream, and the
// embedded web app. Handlers only validate input, call app, and encode output.
package api

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
)

// KeyHeader carries the per-run local access key on every API request.
const KeyHeader = "X-MomentForge-Key"

// Core is the part of app.App the API uses.
type Core interface {
	Status(ctx context.Context) (app.Status, error)
	Job() app.JobStatus
	CancelJob() app.JobStatus
	StartCollect(req app.CollectRequest) (app.JobStatus, error)
	ResumeSession(ctx context.Context, sid string, sensitive bool) (app.JobStatus, error)
	StartAnalyze(sid string, sensitive bool) (app.JobStatus, error)
	CreateDemo(ctx context.Context, sensitive bool) (app.SessionInfo, error)
	Sessions(ctx context.Context) ([]app.SessionInfo, error)
	Session(ctx context.Context, id string) (app.SessionInfo, error)
	Analysis(ctx context.Context, id string) (*app.AnalysisView, error)
	CandidateChats(ctx context.Context, analysisID, candidateID string, offset, limit int) ([]app.ContextChat, int, error)
	ChatsAt(ctx context.Context, sid, analysisID string, atMs int64) (*app.PlayheadChats, error)
	Playback(ctx context.Context, sid string) (app.PlaybackInfo, error)
	Files(ctx context.Context, sid string) ([]app.ExportFile, error)
	OpenFile(ctx context.Context, sid, rel string) (*os.File, error)
	StartComments(ctx context.Context, req app.CommentRequest) (app.JobStatus, error)
	Comments(ctx context.Context, sid string) (*app.CommentsView, error)
	Timeline(ctx context.Context, sid string) (*app.UnifiedTimeline, error)
	AIProviders() []app.AIProvider
	SetAIKey(provider, key string, persist bool) (string, error)
	AIServiceStatus(ctx context.Context, provider string) (app.AIService, error)
	StartAIService(ctx context.Context, provider string) (app.AIService, error)
	StopAIService(ctx context.Context, provider string) (app.AIService, error)
	Settings() app.SettingsInfo
	SaveSettings(s app.Settings) (app.SettingsInfo, error)
	PreviewAI(ctx context.Context, req app.AIPreviewRequest) (app.AIPreview, error)
	StartAI(ctx context.Context, previewID, fingerprint string, regenerate bool) (string, error)
	AIRun(ctx context.Context, id string) (app.AIRunView, error)
	CancelAI(id string)
	AIModels(ctx context.Context, provider string) ([]app.AIModel, error)
	CountAIPreview(ctx context.Context, previewID, fingerprint string) (app.AITokenCount, error)
	AIRuns(ctx context.Context, sid string) ([]app.AIRunView, error)
	CutPlans(ctx context.Context, sid string) ([]app.CutPlanView, error)
	ScanBackups(ctx context.Context, dir string) ([]app.BackupView, error)
	StartImportBackup(req app.ImportRequest) (app.JobStatus, error)
	StartTranscribe(ctx context.Context, sid string) (app.JobStatus, error)
	ASREngines(ctx context.Context) []app.ASREngineView
	Transcript(ctx context.Context, sid string) (app.TranscriptView, bool, error)
	Utterances(ctx context.Context, sid string, from, to int64, limit int) ([]domain.Utterance, error)
	ImportCaption(ctx context.Context, sid, name string, r io.Reader, offsetMs int64) (app.TranscriptView, error)
	MediaFile(ctx context.Context, sid, kind string) (string, error)
	CutPlan(ctx context.Context, id string) (app.CutPlanView, json.RawMessage, error)
	CompareAI(ctx context.Context, idA, idB string) (*app.AIComparison, error)
	HideSessions(ctx context.Context) (int, error)
	RestoreSessions(ctx context.Context) (int, error)

	InspectLive(ctx context.Context, url string) (app.LiveInfo, error)
	StartLive(ctx context.Context, req app.LiveRequest) (app.SessionInfo, error)
	ResumeLive(ctx context.Context, sid string) (app.SessionInfo, error)
	StopLive(sid string)
	LiveStatus(ctx context.Context, sid string) (app.LiveView, error)
	LiveChats(ctx context.Context, sid string, afterSeq int64) ([]app.LiveChat, error)
	LiveContext(ctx context.Context, sid string, atMs int64) ([]app.LiveChat, error)
	LiveCandidates(ctx context.Context, sid string, afterRev int64) ([]app.CandidateView, int64, error)
	LiveCandidateChats(ctx context.Context, sid, candidateID string, offset, limit int) ([]app.ContextChat, int, error)
	LiveCheckpoint(ctx context.Context, sid string) (app.Checkpoint, error)
	LiveDensity(ctx context.Context, sid string) (app.LiveDensity, error)
}

// Config wires the HTTP handler.
type Config struct {
	Core      Core
	Bus       *events.Bus
	Port      int    // listening port, used for Host and Origin checks
	AccessKey string // random per run; injected into index.html
	Assets    fs.FS  // built web app (web/dist); may lack index.html in dev
	// MediaOrigins are https origins the page may stream video from
	// (from the registered platform adapters).
	MediaOrigins []string
	// Heartbeat is the SSE keep-alive interval (default 25s).
	Heartbeat time.Duration
}

type handler struct {
	cfg   Config
	guard guard
}

// NewHandler returns the full HTTP handler.
func NewHandler(cfg Config) http.Handler {
	if cfg.Heartbeat <= 0 {
		cfg.Heartbeat = 25 * time.Second
	}
	h := &handler{cfg: cfg, guard: newGuard(cfg.Port, cfg.AccessKey)}

	r := chi.NewRouter()
	r.Use(middleware.Recoverer, securityHeaders(cfg.MediaOrigins), h.guard.localHost)
	r.Route("/api", func(r chi.Router) {
		r.Use(h.guard.sameOrigin)
		// The player's media requests cannot carry the access key header (api.media).
		r.Get("/sessions/{id}/media/{kind}", h.media)
		r.Group(func(r chi.Router) {
			r.Use(h.guard.accessKey)
			h.apiRoutes(r)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "없는 API 경로입니다.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "이 API 경로에서 지원하지 않는 요청 방식입니다.")
		})
	})
	h.pageRoutes(r)
	return r
}

// apiRoutes are the JSON endpoints behind the access key.
func (h *handler) apiRoutes(r chi.Router) {
	{ // kept as one block so the route list reads as before
		r.Get("/status", h.status)
		r.Get("/events", h.events)
		r.Get("/job", h.job)
		r.Post("/job/cancel", h.cancelJob)
		r.Post("/collect", h.collect)
		r.Post("/comments", h.startComments)
		r.Post("/demo", h.demo)
		r.Post("/import/scan", h.scanBackups)
		r.Post("/import/backup", h.importBackup)
		r.Get("/asr", h.asrEngines)
		r.Get("/sessions", h.sessions)
		r.Post("/sessions/hide", h.hideSessions)
		r.Post("/sessions/restore", h.restoreSessions)
		r.Route("/sessions/{id}", func(r chi.Router) {
			r.Get("/", h.session)
			r.Post("/resume", h.resume)
			r.Post("/analyses", h.reanalyze)
			r.Get("/chats", h.chatsAt)
			r.Get("/playback", h.playback)
			r.Get("/files", h.files)
			r.Get("/comments", h.comments)
			r.Get("/timeline", h.timeline)
			r.Get("/ai/runs", h.aiRuns)
			r.Get("/cuts", h.cutPlans)
			r.Post("/transcribe", h.transcribe)
			r.Get("/transcript", h.transcript)
			r.Post("/transcript/file", h.importCaption)
			r.Get("/utterances", h.utterances)
			r.Get("/files/*", h.download)
		})
		r.Post("/live/inspect", h.inspectLive)
		r.Post("/live", h.startLive)
		r.Route("/live/{id}", func(r chi.Router) {
			r.Get("/", h.liveStatus)
			r.Post("/stop", h.stopLive)
			r.Post("/resume", h.resumeLive)
			r.Get("/chats", h.liveChats)
			r.Get("/context", h.liveContext)
			r.Get("/density", h.liveDensity)
			r.Get("/candidates", h.liveCandidates)
			r.Get("/candidates/{cid}/chats", h.liveCandidateChats)
			r.Post("/checkpoint", h.liveCheckpoint)
		})
		r.Get("/settings", h.settings)
		r.Post("/settings", h.saveSettings)
		r.Get("/ai", h.aiProviders)
		r.Post("/ai/key", h.aiKey)
		r.Post("/ai/models", h.aiModels)
		r.Get("/ai/service", h.aiService)
		r.Post("/ai/service", h.aiServiceControl)
		r.Post("/ai/preview", h.aiPreview)
		r.Post("/ai/preview/count", h.aiCount)
		r.Get("/ai/compare", h.aiCompare)
		r.Post("/ai/runs", h.aiStart)
		r.Get("/ai/runs/{id}", h.aiRun)
		r.Get("/cuts/{id}", h.cutPlan)
		r.Post("/ai/runs/{id}/cancel", h.aiCancel)
		r.Get("/analyses/{id}", h.analysis)
		r.Get("/analyses/{id}/candidates/{cid}/chats", h.candidateChats)
	}
}

// pageRoutes serve the web app itself.
func (h *handler) pageRoutes(r chi.Router) {
	r.Get("/*", h.static)
}

type statusResponse struct {
	Name          string    `json:"name"`
	Version       string    `json:"version"`
	SchemaVersion int       `json:"schemaVersion"`
	StartedAt     time.Time `json:"startedAt"`
	DataDir       string    `json:"dataDir"`
	Platforms     []string  `json:"platforms"`
}

func (h *handler) status(w http.ResponseWriter, r *http.Request) {
	st, err := h.cfg.Core.Status(r.Context())
	if err != nil {
		writeAppError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, statusResponse{
		Name:          st.Name,
		Version:       st.Version,
		SchemaVersion: st.SchemaVersion,
		StartedAt:     st.StartedAt,
		DataDir:       st.DataDir,
		Platforms:     nonNil(st.Platforms),
	})
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func cspFor(media []string) string {
	extra := ""
	if len(media) > 0 {
		extra = " " + strings.Join(media, " ")
	}
	return "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; " +
		"connect-src 'self'" + extra + "; media-src 'self' blob:" + extra + "; worker-src 'self' blob:; " +
		"frame-ancestors 'none'; base-uri 'none'; form-action 'self'"
}
