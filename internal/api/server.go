// Package api serves the local HTTP API, the SSE event stream, and the
// embedded web app. Handlers only validate input, call app, and encode output.
package api

import (
	"context"
	"io/fs"
	"net/http"
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
}

// Config wires the HTTP handler.
type Config struct {
	Core      Core
	Bus       *events.Bus
	Port      int    // listening port, used for Host and Origin checks
	AccessKey string // random per run; injected into index.html
	Assets    fs.FS  // built web app (web/dist); may lack index.html in dev
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
	r.Use(middleware.Recoverer, securityHeaders, h.guard.localHost)
	r.Route("/api", func(r chi.Router) {
		r.Use(h.guard.sameOrigin, h.guard.accessKey)
		r.Get("/status", h.status)
		r.Get("/events", h.events)
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
	})
}
