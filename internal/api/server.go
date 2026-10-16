// Package api serves the local HTTP API, the SSE event stream, and the
// embedded web app. Handlers only validate input, call app, and encode output.
package api

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/manwol-ryu/momentforge/internal/app"
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
		r.Use(h.guard.sameOrigin, h.guard.accessKey)
		r.Get("/status", h.status)
		r.Get("/events", h.events)
		r.Get("/job", h.job)
		r.Post("/job/cancel", h.cancelJob)
		r.Post("/collect", h.collect)
		r.Post("/demo", h.demo)
		r.Get("/sessions", h.sessions)
		r.Route("/sessions/{id}", func(r chi.Router) {
			r.Get("/", h.session)
			r.Post("/resume", h.resume)
			r.Post("/analyses", h.reanalyze)
			r.Get("/chats", h.chatsAt)
			r.Get("/playback", h.playback)
			r.Get("/files", h.files)
			r.Get("/files/*", h.download)
		})
		r.Get("/analyses/{id}", h.analysis)
		r.Get("/analyses/{id}/candidates/{cid}/chats", h.candidateChats)
		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "NOT_FOUND", "없는 API 경로입니다.")
		})
		r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "이 API 경로에서 지원하지 않는 요청 방식입니다.")
		})
	})
	r.Get("/*", h.static)
	return r
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
