//go:build e2efake

// Package e2efake is a synthetic speech recognition engine for end-to-end
// tests: it reads no audio and returns a fixed transcript, so an import can
// be tested without a real recording or macOS's speech model.
package e2efake

import (
	"context"
	"fmt"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Engine answers at once.
type Engine struct{}

func New() *Engine { return &Engine{} }

func (*Engine) ID() string                                   { return "e2efake" }
func (*Engine) Name() string                                 { return "E2E 가짜 음성 인식" }
func (*Engine) Ready(context.Context) (bool, string)         { return true, "" }
func (*Engine) Probe(context.Context, string) (int64, error) { return 20 * 60 * 1000, nil }

// Transcribe returns one line every 30 seconds for 20 minutes, with a
// penalty promise at 1:05: more than a minute before the fixture's first
// candidate, so the cut analysis has to search for it (phase 6-C).
func (*Engine) Transcribe(ctx context.Context, audio string, opts asr.Options, progress func(asr.Progress)) ([]domain.Utterance, error) {
	var out []domain.Utterance
	for t := int64(5000); t < 20*60*1000; t += 30000 {
		text := fmt.Sprintf("가짜 발언 %d분 %d초", t/60000, t%60000/1000)
		if t == 65000 {
			text = "이번 판 지면 벌칙으로 노래 부를게요. 약속"
		}
		out = append(out, domain.Utterance{StartMs: t, EndMs: t + 4000, Speaker: "방송자", Text: text, Confidence: 0.9})
	}
	if progress != nil {
		progress(asr.Progress{Phase: "transcribing", DoneMs: 20 * 60 * 1000, TotalMs: 20 * 60 * 1000, Utterances: len(out)})
	}
	return out, nil
}
