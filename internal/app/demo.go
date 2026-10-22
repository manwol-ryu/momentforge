package app

import (
	"context"
	"sort"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/privacy"
	"github.com/manwol-ryu/momentforge/internal/store"
)

const demoPlatform = "demo"

// CreateDemo stores the built-in synthetic chat as a finished session and
// analyzes it, so the sample uses the same review screen as real videos.
// A sample saved before (even one hidden from the list) is shown again.
func (a *App) CreateDemo(ctx context.Context, sensitive bool) (SessionInfo, error) {
	st := a.cfg.Store
	all, err := st.Sessions(ctx, true)
	if err != nil {
		return SessionInfo{}, err
	}
	for _, s := range all {
		if IsDemo(s) && s.LatestAnalysisID != "" {
			if err := st.SetHidden(ctx, s.ID, false); err != nil {
				return SessionInfo{}, err
			}
			return a.Session(ctx, s.ID)
		}
	}
	salt, err := privacy.NewSalt()
	if err != nil {
		return SessionInfo{}, err
	}
	s, err := st.CreateSession(ctx, store.NewSession{Platform: demoPlatform, Kind: "vod", TargetID: "demo",
		Title: "샘플 채팅 (실제 방송 아님)", DurationMs: domain.DemoDurationMs, Salt: salt})
	if err != nil {
		return SessionInfo{}, err
	}
	events := append(domain.DemoChat(), domain.DemoCallbackChat()...)
	sort.SliceStable(events, func(i, j int) bool { return events[i].OffsetMs < events[j].OffsetMs })
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
	chat, err := a.loadChat(ctx, s.ID)
	if err != nil {
		return SessionInfo{}, err
	}
	if err := writeChatFiles(a.exportDir(s.ID), chat); err != nil {
		a.logError("write demo chat", err)
	}
	// The sample has a synthetic transcript too, so the cut analysis can be tried without speech recognition.
	if _, err := st.SaveTranscript(ctx, store.Transcript{SessionID: s.ID, Source: "demo", Engine: "", Model: "",
		Coverage: []domain.Gap{{StartMs: 0, EndMs: domain.DemoDurationMs}}}, domain.DemoUtterances()); err != nil {
		return SessionInfo{}, err
	}
	if _, err := a.analyzeChat(ctx, chat, a.params(sensitive), job{}); err != nil {
		return SessionInfo{}, err
	}
	return a.Session(ctx, s.ID)
}

// IsDemo reports whether a session is the synthetic sample.
func IsDemo(s store.Session) bool { return strings.EqualFold(s.Platform, demoPlatform) }
