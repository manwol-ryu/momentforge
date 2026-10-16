// Package app holds MomentForge use cases. The HTTP API and the MCP server
// both call this package; it talks to storage, platforms, detection and AI
// only through their root-package interfaces.
package app

import (
	"context"
	"time"

	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// Config wires an App. cmd/momentforge builds it.
type Config struct {
	Version string
	DataDir string
	Store   *store.Store
	Bus     *events.Bus
}

// App is the application core.
type App struct {
	cfg       Config
	startedAt time.Time
}

// New returns an App ready to serve requests.
func New(cfg Config) *App {
	return &App{cfg: cfg, startedAt: time.Now().UTC()}
}

// Status describes the running app.
type Status struct {
	Name          string
	Version       string
	SchemaVersion int
	StartedAt     time.Time
	DataDir       string
}

// Status reports version and storage state.
func (a *App) Status(ctx context.Context) (Status, error) {
	v, err := a.cfg.Store.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{
		Name:          "MomentForge",
		Version:       a.cfg.Version,
		SchemaVersion: v,
		StartedAt:     a.startedAt,
		DataDir:       a.cfg.DataDir,
	}, nil
}
