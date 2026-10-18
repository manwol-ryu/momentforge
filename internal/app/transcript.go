package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/asr/caption"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// CaptionMaxBytes is the largest caption file accepted.
const CaptionMaxBytes = caption.MaxBytes

// runtimeAudioDir holds audio downloaded for one transcription. Files are
// deleted when the transcription ends and the folder is emptied at start
// (a crash mid-download leaves nothing behind for long).
func (a *App) runtimeAudioDir() string { return filepath.Join(a.cfg.DataDir, "runtime", "audio") }

// cleanRuntimeAudio removes leftovers from an earlier run.
func (a *App) cleanRuntimeAudio() {
	if a.cfg.DataDir == "" {
		return
	}
	if err := os.RemoveAll(a.runtimeAudioDir()); err != nil {
		a.logError("clean runtime audio", err)
	}
}

// transcribeStream downloads the VOD's lightest audio rendition to the
// runtime folder, transcribes it and deletes it, whatever happened.
func (a *App) transcribeStream(ctx context.Context, h job, s store.Session, src platform.AudioSource) error {
	h.update(func(js *JobStatus) {
		js.Phase, js.Message = "downloading", "방송 음성을 받는 중입니다 (가장 낮은 화질, 전사가 끝나면 삭제)."
	})
	dst := filepath.Join(a.runtimeAudioDir(), s.ID)
	t := platform.Target{Platform: s.Platform, Kind: platform.Kind(s.Kind), ID: s.TargetID}
	af, err := src.VODAudio(ctx, t, dst, func(p platform.AudioProgress) {
		h.update(func(js *JobStatus) {
			js.PositionMs, js.Pages = p.DoneMs, p.Segments
			js.Message = fmt.Sprintf("방송 음성을 받는 중입니다 (%d/%d 조각, %.0f MB). 전사가 끝나면 삭제합니다.", p.Segments, p.TotalSegments, float64(p.Bytes)/1e6)
		})
	})
	if af.Path != "" {
		defer os.Remove(af.Path)
	}
	if err != nil {
		return err
	}
	if s.DurationMs == 0 && af.DurationMs > 0 {
		h.update(func(js *JobStatus) { js.DurationMs = af.DurationMs })
	}
	h.update(func(js *JobStatus) { js.PositionMs, js.Pages = 0, 0 })
	return a.transcribe(ctx, h, s.ID, af.Path)
}

// ImportCaption stores a caption file (SRT or WebVTT) the user added as the
// session's transcript, times shifted by offsetMs, and writes the caption
// files beside the chat like a recognised transcript. Speaker names in the
// file become roles; nothing else from the file is kept.
func (a *App) ImportCaption(ctx context.Context, sid, name string, r io.Reader, offsetMs int64) (TranscriptView, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return TranscriptView{}, err
	}
	if offsetMs < -24*3600*1000 || offsetMs > 24*3600*1000 {
		return TranscriptView{}, domain.NewUserError("INPUT", "시간 오프셋은 ±24시간 안에서 적어 주세요.")
	}
	utts, format, err := caption.Parse(r, caption.Options{OffsetMs: offsetMs})
	if errors.Is(err, caption.ErrFormat) {
		return TranscriptView{}, domain.NewUserError("INPUT", "SRT 또는 WebVTT 자막 파일이 아닙니다. 파일 내용이 '1'과 시각 줄, 또는 'WEBVTT'로 시작하는지 확인하세요.")
	}
	if err != nil {
		return TranscriptView{}, err
	}
	// Lines far past the broadcast's end are a wrong offset, not speech.
	if s.DurationMs > 0 {
		kept := utts[:0]
		for _, u := range utts {
			if u.StartMs <= s.DurationMs+60_000 {
				kept = append(kept, u)
			}
		}
		if len(kept) == 0 {
			return TranscriptView{}, domain.NewUserError("INPUT", "자막의 모든 줄이 방송 길이 밖에 있습니다. 시간 오프셋을 확인하세요.")
		}
		utts = kept
	}
	model := string(format)
	if ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")); ext != "" && len(ext) <= 8 {
		model = ext
	}
	if err := a.saveTranscript(ctx, sid, store.Transcript{SessionID: sid, Source: "file", Engine: "", Model: model}, utts); err != nil {
		return TranscriptView{}, err
	}
	v, _, err := a.Transcript(ctx, sid)
	return v, err
}
