package app

import (
	"context"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// startAuto starts the capture's automatic work (live_auto.go) and, when
// 일반 설정 asks for it and the platform can, its audio recording. Both
// outlive the capture by what they still have to do; ended closes when it
// stops.
func (w *liveWorker) startAuto(ctx context.Context, ended <-chan struct{}) {
	var audio *liveAudio
	if w.a.Settings().LiveAudio {
		if p, ok := w.a.cfg.Platforms.Get(w.target.Platform); ok {
			audio = w.a.newLiveAudio(w.sid, p, w.target, w.ls.OriginMs)
		}
	}
	w.auto = newLiveAuto(w.a, w.sid, audio, w.timing.autoEvery)
	w.a.live.mu.Lock()
	w.liveRun.audio, w.liveRun.auto = audio, w.auto
	w.a.live.mu.Unlock()
	if audio != nil {
		go func() {
			if w.a.defaultASR(ctx) == nil {
				audio.set(audioFailed, "쓸 수 있는 음성 인식 엔진이 없어 방송 음성을 받지 않았습니다. 설정의 방송 내용 인식에서 준비 상태를 확인하세요.")
				close(audio.done)
				return
			}
			audio.record(ctx)
		}()
	}
	go w.auto.run(w.a.ctx, ended)
}

// judge runs the whole-broadcast analysis on the chat stored in the last
// LiveJudgeSpanMs and stores the capture's judged and settled candidates
// (REDESIGN 6.2.3); those go on to their automatic work.
func (w *liveWorker) judge(ctx context.Context, final bool) {
	st := w.a.cfg.Store
	end := w.analyzer.Watermark()
	w.judgedTo = &end
	from := max(0, end-w.params.LiveJudgeSpanMs())
	events, err := st.ChatsIn(ctx, w.sid, from, end)
	if err != nil {
		w.a.logError("live judge", err)
		return
	}
	gaps, err := st.Gaps(ctx, w.sid)
	if err != nil {
		w.a.logError("live judge", err)
		return
	}
	if from > 0 {
		gaps = append(gaps, domain.Gap{StartMs: 0, EndMs: from}) // not read: neither quiet nor busy
	}
	res, err := detect.AnalyzeBroadcast(ctx, events, end, gaps, w.params, w.a.cfg.Rules, nil)
	if err != nil {
		w.a.logError("live judge", err)
		return
	}
	stored, _, err := st.LiveCandidates(ctx, w.sid, 0)
	if err != nil {
		w.a.logError("live judge", err)
		return
	}
	changed := detect.JudgeLive(stored, res.Candidates, from, end, final, w.params)
	if len(changed) == 0 {
		return
	}
	rev, err := st.UpsertLiveCandidates(ctx, w.sid, changed)
	if err != nil {
		w.a.logError("live judge", err)
		return
	}
	w.ls.Revision = rev
	if w.auto != nil {
		w.auto.offer(changed)
	}
}
