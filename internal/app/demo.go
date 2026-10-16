package app

import (
	"context"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/privacy"
	"github.com/manwol-ryu/momentforge/internal/store"
)

const demoPlatform = "demo"

// CreateDemo stores the built-in synthetic chat as a finished session and
// analyzes it, so the sample uses the same review screen as real videos.
func (a *App) CreateDemo(ctx context.Context, sensitive bool) (SessionInfo, error) {
	st := a.cfg.Store
	salt, err := privacy.NewSalt()
	if err != nil {
		return SessionInfo{}, err
	}
	s, err := st.CreateSession(ctx, store.NewSession{Platform: demoPlatform, Kind: "vod", TargetID: "demo",
		Title: "샘플 채팅 (실제 방송 아님)", DurationMs: domain.DemoDurationMs, Salt: salt})
	if err != nil {
		return SessionInfo{}, err
	}
	events := domain.DemoChat()
	for i := range events {
		events[i].Seq = 0 // assigned by the store
	}
	if _, err := st.SavePage(ctx, s.ID, store.Page{Raw: []byte(`{"synthetic":true}`), Events: events,
		Next: domain.DemoDurationMs, Finished: true}); err != nil {
		return SessionInfo{}, err
	}
	if err := export.WriteReceived(a.exportDir(s.ID), events); err != nil {
		a.logError("write demo received", err)
	}
	if err := a.writeChat(ctx, s.ID); err != nil {
		a.logError("write demo chat", err)
	}
	if _, err := a.analyzeSession(ctx, s.ID, a.params(sensitive), job{}); err != nil {
		return SessionInfo{}, err
	}
	return a.Session(ctx, s.ID)
}

// IsDemo reports whether a session is the synthetic sample.
func IsDemo(s store.Session) bool { return strings.EqualFold(s.Platform, demoPlatform) }
