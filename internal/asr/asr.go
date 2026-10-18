// Package asr turns a session's broadcast audio into utterances with local
// speech recognition (docs/AI-CUT-ANALYSIS.md 2.2). Engines live in
// subpackages and are registered by cmd/momentforge; nothing here sends
// audio anywhere. The app chooses an engine, feeds it an audio file and
// stores what comes back as the session's transcript.
package asr

import (
	"context"
	"sort"
	"sync"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Engine is one way to transcribe audio on this computer.
type Engine interface {
	ID() string   // "applespeech", "whispercpp"
	Name() string // shown to users
	// Ready says whether the engine can run now; when it cannot, how says
	// what the user must do (install a program, a newer macOS, a model file).
	Ready(ctx context.Context) (ok bool, how string)
	// Probe reads a media file's length without transcribing it.
	Probe(ctx context.Context, media string) (durationMs int64, err error)
	// Transcribe reads the audio file (any container AVFoundation or the
	// engine reads: m4a, mp4, wav) and returns utterances in time order with
	// times relative to the file's start. progress is called as parts finish.
	Transcribe(ctx context.Context, audio string, opts Options, progress func(Progress)) ([]domain.Utterance, error)
}

// Options of one transcription.
type Options struct {
	Locale string // BCP 47, ko-KR by default
	// FromMs and DurationMs select a part of the file (0: the whole file).
	FromMs     int64
	DurationMs int64
}

// Progress is how far a transcription has come.
type Progress struct {
	DoneMs     int64  // audio transcribed so far
	TotalMs    int64  // audio to transcribe (0 when unknown)
	Utterances int    // utterances so far
	Phase      string // preparing | installing | transcribing
}

// Registry holds the engines in registration order (the first ready one is the default).
type Registry struct {
	mu      sync.Mutex
	engines []Engine
}

// Register adds engines.
func (r *Registry) Register(engines ...Engine) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.engines = append(r.engines, engines...)
}

// Get returns an engine by ID.
func (r *Registry) Get(id string) (Engine, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.engines {
		if e.ID() == id {
			return e, true
		}
	}
	return nil, false
}

// All returns the engines in registration order.
func (r *Registry) All() []Engine {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Engine(nil), r.engines...)
}

// Sorted orders utterances by start time, then end time.
func Sorted(utts []domain.Utterance) []domain.Utterance {
	out := append([]domain.Utterance(nil), utts...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StartMs != out[j].StartMs {
			return out[i].StartMs < out[j].StartMs
		}
		return out[i].EndMs < out[j].EndMs
	})
	return out
}

// Coverage is the spans the utterances cover, merged with a small gap,
// so a transcript can say which parts of the broadcast it covers.
func Coverage(utts []domain.Utterance, gapMs int64) []domain.Gap {
	var out []domain.Gap
	for _, u := range Sorted(utts) {
		if n := len(out); n > 0 && u.StartMs <= out[n-1].EndMs+gapMs {
			if u.EndMs > out[n-1].EndMs {
				out[n-1].EndMs = u.EndMs
			}
			continue
		}
		out = append(out, domain.Gap{StartMs: u.StartMs, EndMs: u.EndMs})
	}
	return out
}
