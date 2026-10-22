package app

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// While a LIVE capture runs with 일반 설정's "생방송 음성도 받아 후보 구간
// 인식" on, the broadcast's lightest rendition is recorded to the runtime
// folder (docs/REDESIGN.md 15, 2026-10-22), so a judged candidate's range
// can be transcribed on this computer minutes after it happened. Only the
// last liveAudioKeepMs stay; the folder goes when the capture ends (and at
// start, after a crash). No audio leaves the computer.

// liveAudioKeepMs is how much recorded audio stays: a candidate is judged
// 5 minutes after its reaction (a minute more between judgments, two while
// a batch gathers), and its range reaches a minute and a half before it.
const liveAudioKeepMs = 25 * 60_000

// Live audio states, as the LIVE screen shows them.
const (
	audioRecording = "recording" // segments arrive
	audioEnded     = "ended"     // the broadcast's stream ended
	audioFailed    = "failed"    // recording stopped with an error (note says why)
)

// liveAudio records one capture's audio and transcribes ranges of it.
type liveAudio struct {
	a        *App
	src      platform.LiveAudioSource
	target   platform.Target
	originMs int64 // the session's time origin (unix ms)
	dir      string

	mu    sync.Mutex
	segs  []liveSeg // in time order
	state string
	note  string
	done  chan struct{} // closed when recording stops
}

type liveSeg struct {
	platform.LiveAudioSegment
	startMs int64 // session time
}

// newLiveAudio prepares a recording for the capture, or returns nil when
// the platform cannot give its live audio.
func (a *App) newLiveAudio(sid string, p platform.Platform, t platform.Target, originMs int64) *liveAudio {
	src, ok := p.(platform.LiveAudioSource)
	if !ok || a.cfg.DataDir == "" {
		return nil
	}
	return &liveAudio{a: a, src: src, target: t, originMs: originMs,
		dir: filepath.Join(a.runtimeAudioDir(), "live-"+sid+"-"+randomID()[:6]), done: make(chan struct{})}
}

// record runs until ctx ends or the stream does, then deletes what it kept.
func (l *liveAudio) record(ctx context.Context) {
	defer close(l.done)
	l.set(audioRecording, "")
	err := l.src.RecordLiveAudio(ctx, l.target, l.dir, l.add)
	switch {
	case err != nil && ctx.Err() == nil:
		msg := "방송 음성을 받지 못했습니다."
		if ue, ok := domain.AsUserError(err); ok {
			msg = ue.Message
		} else {
			l.a.logError("record live audio", err)
		}
		l.set(audioFailed, msg)
	case ctx.Err() == nil:
		l.set(audioEnded, "")
	}
}

// cleanup deletes the recording (after the capture's last transcription).
func (l *liveAudio) cleanup() {
	l.mu.Lock()
	l.segs = nil
	l.mu.Unlock()
	if err := os.RemoveAll(l.dir); err != nil {
		l.a.logError("remove live audio", err)
	}
}

func (l *liveAudio) set(state, note string) {
	l.mu.Lock()
	l.state, l.note = state, note
	l.mu.Unlock()
}

// status is the recording's state and why it stopped, if it did.
func (l *liveAudio) status() (state, note string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state, l.note
}

// add keeps a new segment and deletes those older than liveAudioKeepMs.
func (l *liveAudio) add(s platform.LiveAudioSegment) {
	if s.At.IsZero() {
		os.Remove(s.Path)
		return
	}
	seg := liveSeg{LiveAudioSegment: s, startMs: s.At.UnixMilli() - l.originMs}
	l.mu.Lock()
	defer l.mu.Unlock()
	i, _ := slices.BinarySearchFunc(l.segs, seg.startMs, func(x liveSeg, t int64) int { return cmp.Compare(x.startMs, t) })
	l.segs = slices.Insert(l.segs, i, seg)
	keepFrom := l.segs[len(l.segs)-1].startMs - liveAudioKeepMs
	n := 0
	for n < len(l.segs) && l.segs[n].startMs+l.segs[n].DurMs < keepFrom {
		os.Remove(l.segs[n].Path)
		n++
	}
	l.segs = l.segs[n:]
}

// runs are the recorded segments overlapping [from, to), split where a
// segment is missing (a joined file must not skip time).
func (l *liveAudio) runs(from, to int64) [][]liveSeg {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out [][]liveSeg
	var cur []liveSeg
	for _, s := range l.segs {
		if s.startMs+s.DurMs <= from || s.startMs >= to {
			continue
		}
		if n := len(cur); n > 0 {
			prev := cur[n-1]
			if s.startMs-(prev.startMs+prev.DurMs) > 1000 || s.Init != prev.Init {
				out = append(out, cur)
				cur = nil
			}
		}
		cur = append(cur, s)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// behind says whether the recording still runs and has not reached to
// yet (live audio arrives seconds late, and nothing comes before it starts).
func (l *liveAudio) behind(to int64) bool {
	select {
	case <-l.done:
		return false
	default:
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.segs)
	return n == 0 || l.segs[n-1].startMs+l.segs[n-1].DurMs < to
}

// errNoLiveAudio says a range's audio was not recorded (or no longer kept).
var errNoLiveAudio = errors.New("no recorded audio for the range")

// transcribe recognises [from, to) of the recording with engine and
// returns its utterances in session time, those starting in the range.
// Each run of segments is joined to a file of its own, read and deleted.
func (l *liveAudio) transcribe(ctx context.Context, engine asr.Engine, from, to int64) ([]domain.Utterance, error) {
	runs := l.runs(from, to)
	if len(runs) == 0 {
		return nil, errNoLiveAudio
	}
	var out []domain.Utterance
	for _, run := range runs {
		files := make([]platform.LiveAudioSegment, len(run))
		for i, s := range run {
			files[i] = s.LiveAudioSegment
		}
		af, err := l.src.AssembleAudio(files, filepath.Join(l.dir, "range-"+randomID()[:8]))
		if err != nil {
			return out, err
		}
		utts, err := engine.Transcribe(ctx, af.Path, asr.Options{Locale: "ko-KR"}, nil)
		os.Remove(af.Path)
		if err != nil {
			return out, err
		}
		base := run[0].startMs
		for _, u := range utts {
			if u.StartMs > af.DurationMs+1000 {
				continue // past the joined audio: not what was heard
			}
			u.StartMs += base
			u.EndMs += base
			if u.StartMs >= from && u.StartMs < to {
				out = append(out, u)
			}
		}
	}
	return asr.Sorted(out), nil
}
