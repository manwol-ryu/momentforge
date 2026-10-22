package app

import (
	"cmp"
	"context"
	"errors"
	"math"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/events"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// What a LIVE capture does by itself with its judged candidates (일반 설정,
// docs/REDESIGN.md 15, 2026-10-22): each one's range is transcribed from
// the recorded audio ("생방송 음성도 받아 후보 구간 인식"), then AI fixes
// the new lines and explains the candidates ("AI 검토 자동 실행"), within
// the session's limits. It runs beside the capture, a batch at a time, and
// finishes its queue after the capture ends.

// liveRangePadMs is how much speech around a candidate's clip is
// transcribed with it: what was said just before often explains a reaction.
const liveRangePadMs = 60_000

// liveAudioWait is how long a candidate waits for its range's audio to
// arrive before it is explained without it.
const liveAudioWait = 3 * time.Minute

// liveAuto is one capture's automatic work.
type liveAuto struct {
	a     *App
	sid   string
	audio *liveAudio    // nil: no recording
	every time.Duration // judged candidates are gathered this long, so one request explains several
	wake  chan struct{}

	mu    sync.Mutex
	queue []domain.Candidate
	taken map[string]bool // candidate IDs queued once already
	note  string          // the last step that could not run, and why ("" none)

	waited map[string]time.Time // when a candidate first waited for its audio (the run goroutine's)
}

func newLiveAuto(a *App, sid string, audio *liveAudio, every time.Duration) *liveAuto {
	return &liveAuto{a: a, sid: sid, audio: audio, every: every, wake: make(chan struct{}, 1), taken: map[string]bool{},
		waited: map[string]time.Time{}}
}

// offer queues judged candidates the capture has not handed over before.
func (l *liveAuto) offer(cands []domain.Candidate) {
	l.mu.Lock()
	for _, c := range cands {
		if c.State == domain.CandidateDropped || (c.Tier != domain.TierJudged && c.Tier != domain.TierFinal) || l.taken[c.ID] {
			continue
		}
		l.taken[c.ID] = true
		l.queue = append(l.queue, c)
	}
	queued := len(l.queue) > 0
	l.mu.Unlock()
	if queued {
		l.poke()
	}
}

func (l *liveAuto) poke() {
	select {
	case l.wake <- struct{}{}:
	default:
	}
}

// status is the last step that could not run, and why.
func (l *liveAuto) status() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.note
}

func (l *liveAuto) fail(what string, err error) {
	msg := "완료하지 못했습니다"
	if ue, ok := domain.AsUserError(err); ok {
		msg = ue.Message
	} else {
		l.a.logError("live "+what, err)
	}
	l.mu.Lock()
	l.note = what + ": " + msg
	l.mu.Unlock()
}

// run takes batches until the capture ends (ended closes) and the queue is
// done, or the app stops. The recording is deleted when it returns.
func (l *liveAuto) run(ctx context.Context, ended <-chan struct{}) {
	defer func() {
		if l.audio != nil {
			<-l.audio.done
			l.audio.cleanup()
		}
	}()
	// The last batch takes what the last judgment settled, with all the audio there will be.
	finish := func() {
		if l.audio != nil {
			<-l.audio.done
		}
		l.batch(ctx)
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ended:
			finish()
			return
		case <-l.wake:
		}
		select {
		case <-ctx.Done():
			return
		case <-ended:
			finish()
			return
		case <-time.After(l.every):
		}
		if l.batch(ctx) {
			l.poke() // some wait for their audio
		}
	}
}

// batch takes the queued candidates through the steps the settings turn
// on. Those whose audio has not arrived yet go back to the queue (and wait
// for their explanation too); it reports whether any did.
func (l *liveAuto) batch(ctx context.Context) (waiting bool) {
	l.mu.Lock()
	cands := l.queue
	l.queue = nil
	l.mu.Unlock()
	if len(cands) == 0 {
		return false
	}
	var fresh []domain.Utterance
	var t store.Transcript
	later := map[string]bool{} // timeline item IDs not to explain yet
	if l.audio != nil {
		var ready []domain.Candidate
		for _, c := range cands {
			if l.audio.behind(c.ClipEndMs + liveRangePadMs) {
				first, ok := l.waited[c.ID]
				if !ok {
					first = time.Now()
					l.waited[c.ID] = first
				}
				if time.Since(first) < liveAudioWait {
					later[SourceRule+":"+c.ID] = true
					l.mu.Lock()
					l.queue = append(l.queue, c)
					l.mu.Unlock()
					continue
				}
			}
			ready = append(ready, c)
		}
		var err error
		if t, fresh, err = l.transcribe(ctx, ready); err != nil {
			if ctx.Err() != nil {
				return false
			}
			l.fail("방송 내용 인식", err)
		}
	}
	set := l.a.Settings()
	if !set.AutoAI {
		return len(later) > 0
	}
	p, key, err := l.a.autoProvider()
	if err != nil {
		l.fail("AI 검토", err)
		return len(later) > 0
	}
	b, err := l.a.autoBudgetFor(ctx, l.sid)
	if err != nil {
		l.fail("AI 검토", err)
		return len(later) > 0
	}
	if set.AutoTranscriptFix && len(fresh) > 0 {
		if _, err := l.a.fixLines(ctx, l.sid, t, fresh, p, key, &b, nil); err != nil {
			if ctx.Err() != nil {
				return false
			}
			l.fail("대본 다듬기", err)
		}
	}
	if _, err := l.a.explainAuto(ctx, l.sid, p, &b, later, nil); err != nil && ctx.Err() == nil {
		l.fail("AI 장면 설명", err)
	}
	return len(later) > 0
}

// transcribe recognises the parts of the candidates' ranges (their clips
// and a minute around) that the capture's transcript does not cover yet,
// adds the lines to it (making it on the first lines) and returns them.
func (l *liveAuto) transcribe(ctx context.Context, cands []domain.Candidate) (store.Transcript, []domain.Utterance, error) {
	st := l.a.cfg.Store
	engine := l.a.defaultASR(ctx)
	if engine == nil {
		return store.Transcript{}, nil, domain.NewUserError("ASR", "쓸 수 있는 음성 인식 엔진이 없어 건너뛰었습니다. 설정의 방송 내용 인식에서 준비 상태를 확인하세요.")
	}
	t, has, err := st.LatestTranscript(ctx, l.sid)
	if err != nil || len(cands) == 0 {
		return t, nil, err
	}
	if has && t.Source != "asr" {
		return t, nil, domain.NewUserError("INPUT", "자막 파일을 넣은 기록이라 음성 인식 결과를 더하지 않았습니다.")
	}
	slices.SortFunc(cands, func(x, y domain.Candidate) int { return cmp.Compare(x.ClipStartMs, y.ClipStartMs) })
	coverage := slices.Clone(t.Coverage)
	var fresh []domain.Utterance
	for _, c := range cands {
		for _, part := range uncovered(coverage, max(0, c.ClipStartMs-liveRangePadMs), c.ClipEndMs+liveRangePadMs) {
			utts, err := l.audio.transcribe(ctx, engine, part.StartMs, part.EndMs)
			if errors.Is(err, errNoLiveAudio) {
				continue // recorded before the recording started, or no longer kept
			}
			if err != nil {
				return t, fresh, err
			}
			if !has {
				model := "system"
				if m, ok := engine.(asr.ModelNamer); ok && m.Model() != "" {
					model = m.Model()
				}
				if t, err = st.SaveTranscript(ctx, store.Transcript{SessionID: l.sid, Source: "asr", Engine: engine.ID(), Model: model}, nil); err != nil {
					return t, fresh, err
				}
				has = true
			}
			coverage = addSpan(coverage, part)
			saved, err := st.AppendUtterances(ctx, t, utts, coverage)
			if err != nil {
				return t, fresh, err
			}
			t.Coverage = coverage
			fresh = append(fresh, saved...)
		}
	}
	if has && len(fresh) > 0 {
		bg := context.WithoutCancel(ctx)
		if all, err := st.UtterancesBetween(bg, t.ID, 0, math.MaxInt64, 1_000_000); err == nil {
			if err := export.WriteTranscript(filepath.Join(l.a.exportDir(l.sid), "transcript"), t.ID, all); err != nil {
				l.a.logError("write transcript", err)
			}
		}
		if l.a.cfg.Bus != nil {
			l.a.cfg.Bus.Publish(events.Event{Type: "transcript", Data: map[string]any{"sessionId": l.sid, "utterances": len(fresh)}})
		}
	}
	return t, fresh, nil
}

// uncovered is [from, to) less the spans in cover.
func uncovered(cover []domain.Gap, from, to int64) []domain.Gap {
	spans := slices.Clone(cover)
	slices.SortFunc(spans, func(x, y domain.Gap) int { return cmp.Compare(x.StartMs, y.StartMs) })
	var out []domain.Gap
	at := from
	for _, s := range spans {
		if s.EndMs <= at || s.StartMs >= to {
			continue
		}
		if s.StartMs > at {
			out = append(out, domain.Gap{StartMs: at, EndMs: s.StartMs})
		}
		at = max(at, s.EndMs)
	}
	if at < to {
		out = append(out, domain.Gap{StartMs: at, EndMs: to})
	}
	return out
}

// addSpan adds span to cover, merging what touches.
func addSpan(cover []domain.Gap, span domain.Gap) []domain.Gap {
	all := append(slices.Clone(cover), span)
	slices.SortFunc(all, func(x, y domain.Gap) int { return cmp.Compare(x.StartMs, y.StartMs) })
	var out []domain.Gap
	for _, g := range all {
		if n := len(out); n > 0 && g.StartMs <= out[n-1].EndMs {
			out[n-1].EndMs = max(out[n-1].EndMs, g.EndMs)
			continue
		}
		out = append(out, g)
	}
	return out
}
