package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Benchmarks over a synthetic long broadcast: 6 hours, 300,000 messages
// (about 14 a second), with an analysis of 2,160 windows and 120
// candidates. Run with: go test ./internal/store -run '^$' -bench .
const (
	longDurationMs = 6 * 3600 * 1000
	longMessages   = 300_000
)

type longFixture struct {
	s          *Store
	sid        string
	analysisID string
	evidence   []string // every candidate's evidence ids
}

func seedLong(b *testing.B) longFixture {
	b.Helper()
	ctx := context.Background()
	s, err := Open(ctx, b.TempDir(), "bench")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { s.Close() })
	sess, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "long", Title: "긴 방송",
		DurationMs: longDurationMs, Salt: []byte("salt")})
	if err != nil {
		b.Fatal(err)
	}
	events := make([]domain.ChatEvent, longMessages)
	for i := range events {
		events[i] = domain.ChatEvent{ID: fmt.Sprintf("e%07d", i), OffsetMs: int64(i) * longDurationMs / longMessages,
			AuthorKey: fmt.Sprintf("a%04d", i%3000), Text: "ㅋㅋㅋ 그게 되네"}
	}
	if _, err := s.SavePage(ctx, sess.ID, Page{Raw: []byte(`{}`), Events: events, Next: longDurationMs, Finished: true}); err != nil {
		b.Fatal(err)
	}
	an := &Analysis{SessionID: sess.ID, RulesVersion: "bench", ParamsJSON: []byte(`{}`), DurationMs: longDurationMs,
		MaxSeq: longMessages, EventCount: longMessages, SourceState: StateFinished}
	for t := int64(10000); t <= longDurationMs; t += 10000 {
		score := 1.5
		an.Windows = append(an.Windows, domain.Window{T: t, N: 140, U: 120, Valid: true, BaselineReady: true,
			Signals: []string{}, Score: &score, Ratio: &score, Z: &score, BaselineN: &score})
	}
	var evidence []string
	for c := 0; c < 120; c++ {
		start := int64(c) * 180_000
		cand := domain.Candidate{ID: fmt.Sprintf("m%04d", c+1), ReactionStartMs: start, ReactionEndMs: start + 30000,
			ClipStartMs: start, ClipEndMs: start + 60000, Signals: []string{"laugh"}, Reasons: []string{"웃음 증가"}}
		first := int(start * longMessages / longDurationMs)
		for i := first; i < first+400; i++ { // 30 s of chat
			cand.EvidenceIDs = append(cand.EvidenceIDs, events[i].ID)
		}
		evidence = append(evidence, cand.EvidenceIDs...)
		an.Candidates = append(an.Candidates, cand)
	}
	if err := s.SaveAnalysis(ctx, an); err != nil {
		b.Fatal(err)
	}
	return longFixture{s: s, sid: sess.ID, analysisID: an.ID, evidence: evidence}
}

func BenchmarkLongSession(b *testing.B) {
	f := seedLong(b)
	ctx := context.Background()
	b.Run("Session", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.Session(ctx, f.sid); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Sessions", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.Sessions(ctx, true); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Analysis", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.Analysis(ctx, f.analysisID); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("AnalysisMeta", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.AnalysisMeta(ctx, f.analysisID); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("AnalysisCandidate", func(b *testing.B) {
		for b.Loop() {
			if _, ok, err := f.s.AnalysisCandidate(ctx, f.analysisID, "m0060"); err != nil || !ok {
				b.Fatal(ok, err)
			}
		}
	})
	b.Run("ChatsAll", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.Chats(ctx, f.sid, AllSeq, false); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ChatsByIDEvidence", func(b *testing.B) {
		for b.Loop() {
			if _, err := f.s.ChatsByID(ctx, f.sid, f.evidence); err != nil {
				b.Fatal(err)
			}
		}
	})
}
