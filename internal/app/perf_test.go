package app

import (
	"context"
	"fmt"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// BenchmarkLongBroadcastReview measures the review queries on a synthetic
// 6-hour, 300,000-message VOD (a laugh burst every 3 minutes), analyzed
// once. Run with: go test ./internal/app -run '^$' -bench LongBroadcast
func BenchmarkLongBroadcastReview(b *testing.B) {
	const durationMs, messages = 6 * 3600 * 1000, 300_000
	a, _ := newTestApp(b, &fakeVOD{duration: 1000, pages: 1})
	ctx := context.Background()
	s, err := a.cfg.Store.CreateSession(ctx, store.NewSession{Platform: "demo", Kind: "vod", TargetID: "long", Title: "긴 방송",
		DurationMs: durationMs, Salt: []byte("salt")})
	if err != nil {
		b.Fatal(err)
	}
	events := make([]domain.ChatEvent, messages)
	for i := range events {
		at := int64(i) * durationMs / messages
		text := fmt.Sprintf("잡담 %d", i%500)
		if at%180_000 < 15_000 && i%3 != 0 {
			text = "ㅋㅋㅋㅋ"
		}
		events[i] = domain.ChatEvent{ID: fmt.Sprintf("e%07d", i), OffsetMs: at, AuthorKey: fmt.Sprintf("a%04d", (i*7919)%2500), Text: text}
	}
	if _, err := a.cfg.Store.SavePage(ctx, s.ID, store.Page{Raw: []byte(`{}`), Events: events, Next: durationMs, Finished: true}); err != nil {
		b.Fatal(err)
	}
	an, err := a.analyzeSession(ctx, s.ID, a.params(false), job{})
	if err != nil {
		b.Fatal(err)
	}
	if len(an.Candidates) == 0 {
		b.Fatal("no candidates")
	}
	cand := an.Candidates[len(an.Candidates)/2]
	b.Logf("%d candidates, %d windows", len(an.Candidates), len(an.Windows))

	b.Run("Analysis", func(b *testing.B) {
		for b.Loop() {
			if _, err := a.Analysis(ctx, an.ID); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ChatsAt", func(b *testing.B) {
		for b.Loop() {
			if _, err := a.ChatsAt(ctx, s.ID, an.ID, cand.PeakMs); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("CandidateChats", func(b *testing.B) {
		for b.Loop() {
			if _, _, err := a.CandidateChats(ctx, an.ID, cand.ID, 0, 200); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Sessions", func(b *testing.B) {
		for b.Loop() {
			if _, err := a.Sessions(ctx); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Reanalyze", func(b *testing.B) {
		for b.Loop() {
			if _, err := a.analyzeSession(ctx, s.ID, a.params(false), job{}); err != nil {
				b.Fatal(err)
			}
		}
	})
}
