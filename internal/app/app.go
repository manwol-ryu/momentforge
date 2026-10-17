// Package app holds MomentForge use cases. The HTTP API and the MCP server
// both call this package; it talks to storage, platforms, detection and AI
// only through their root-package interfaces.
package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// Config wires an App. cmd/momentforge builds it.
type Config struct {
	Version   string
	DataDir   string
	Store     *store.Store
	Bus       *events.Bus
	Params    detect.Params      // detection thresholds
	Rules     []detect.Rule      // registered detection rules
	Platforms *platform.Registry // registered platform adapters
	AI        *ai.Registry       // registered AI providers
	// AIBatchPoll is the first wait before checking a submitted batch
	// (30 s when 0; the E2E binary shortens it).
	AIBatchPoll time.Duration
}

// App is the application core.
type App struct {
	cfg       Config
	startedAt time.Time
	ctx       context.Context // lives until Shutdown; background jobs derive from it
	stop      context.CancelFunc
	jobs      *jobs

	live       liveJobs
	liveTiming liveTiming
	ai         aiState
}

// New returns an App ready to serve requests.
func New(cfg Config) *App {
	if cfg.Platforms == nil {
		cfg.Platforms = &platform.Registry{}
	}
	if cfg.AI == nil {
		cfg.AI = &ai.Registry{}
	}
	ctx, stop := context.WithCancel(context.Background())
	a := &App{cfg: cfg, startedAt: time.Now().UTC(), ctx: ctx, stop: stop, jobs: &jobs{bus: cfg.Bus},
		liveTiming: defaultLiveTiming()}
	a.initAI()
	return a
}

// Shutdown cancels background work and waits for it (bounded by ctx).
func (a *App) Shutdown(ctx context.Context) {
	a.stop()
	a.jobs.stop(ctx)
	a.stopLive(ctx)
	a.stopAI(ctx)
}

// Status describes the running app.
type Status struct {
	Name          string
	Version       string
	SchemaVersion int
	StartedAt     time.Time
	DataDir       string
	Platforms     []string
}

// Status reports version and storage state.
func (a *App) Status(ctx context.Context) (Status, error) {
	v, err := a.cfg.Store.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	var names []string
	for _, p := range a.cfg.Platforms.All() {
		names = append(names, p.Name())
	}
	return Status{
		Name:          "MomentForge",
		Version:       a.cfg.Version,
		SchemaVersion: v,
		StartedAt:     a.startedAt,
		DataDir:       a.cfg.DataDir,
		Platforms:     names,
	}, nil
}

// Job returns the current or last background job.
func (a *App) Job() JobStatus { return a.jobs.current() }

// CancelJob asks the running job to stop; it pauses at a safe point.
func (a *App) CancelJob() JobStatus {
	a.jobs.mu.Lock()
	cancel, busy := a.jobs.cancel, a.jobs.status.Busy
	a.jobs.mu.Unlock()
	if busy && cancel != nil {
		cancel()
	}
	return a.jobs.current()
}

// MediaOrigins lists where registered platforms serve video from.
func (a *App) MediaOrigins() []string { return a.cfg.Platforms.MediaOrigins() }

func (a *App) params(sensitive bool) detect.Params {
	p := a.cfg.Params
	p.Sensitive = sensitive
	return p
}

// exportDir is <data>/exports/<session id>. sid always comes from the database.
func (a *App) exportDir(sid string) string {
	return filepath.Join(a.cfg.DataDir, "exports", sid)
}

func (a *App) logError(what string, err error) { slog.Error(what, "err", err) }
