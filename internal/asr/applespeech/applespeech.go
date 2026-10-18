// Package applespeech transcribes with macOS's on-device speech recognition
// (the Speech framework's SpeechAnalyzer, macOS 26 and later) through a
// small helper executable built from tools/speech-helper and embedded in
// the app. The Korean model is a system asset that macOS downloads and
// keeps; the app never fetches a model itself and no audio leaves the Mac.
package applespeech

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// ID of the engine.
const ID = "applespeech"

// Engine runs the helper.
type Engine struct {
	helper  []byte // the embedded executable (empty on other systems)
	dir     string // where the helper is written (the data folder's runtime/)
	version string // part of the helper file name, so a new build replaces the old file

	mu   sync.Mutex
	path string // the extracted helper
}

// New returns the engine. helper is the embedded executable; dir is where
// it is extracted on first use.
func New(helper []byte, dir, version string) *Engine {
	return &Engine{helper: helper, dir: dir, version: version}
}

func (e *Engine) ID() string   { return ID }
func (e *Engine) Name() string { return "Apple 음성 인식 (macOS 내장)" }

// Ready checks the system, the helper and the Korean asset.
func (e *Engine) Ready(ctx context.Context) (bool, string) {
	if runtime.GOOS != "darwin" {
		return false, "macOS 26 이상에서만 쓸 수 있습니다. 다른 컴퓨터에서는 whisper.cpp를 설치해 고르세요."
	}
	if len(e.helper) == 0 {
		return false, "이 실행파일에는 음성 인식 도우미가 들어 있지 않습니다. make build로 다시 빌드하세요."
	}
	bin, err := e.executable()
	if err != nil {
		return false, "음성 인식 도우미를 준비하지 못했습니다: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "locales").Output()
	if err != nil {
		return false, "macOS 음성 인식을 확인하지 못했습니다. macOS 26 이상인지 확인하세요."
	}
	var info struct {
		Korean struct {
			Supported bool `json:"supported"`
			Installed bool `json:"installed"`
		} `json:"korean"`
	}
	if json.Unmarshal(lastLine(out), &info) != nil {
		return false, "음성 인식 도우미의 응답을 읽지 못했습니다."
	}
	switch {
	case !info.Korean.Supported:
		return false, "이 macOS의 음성 인식은 한국어를 지원하지 않습니다. whisper.cpp를 설치해 고르세요."
	case !info.Korean.Installed:
		return true, "한국어 모델을 macOS가 처음 한 번 내려받습니다(수백 MB, 인터넷 필요)."
	}
	return true, ""
}

// Probe reads the media's length with the helper (AVFoundation reads only the headers).
func (e *Engine) Probe(ctx context.Context, media string) (int64, error) {
	bin, err := e.executable()
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "probe", media).Output()
	if err != nil {
		return 0, domain.WrapUserError("MEDIA", "미디어 파일을 읽지 못했습니다. 경로와 형식을 확인하세요.", err)
	}
	var info struct {
		DurationMs int64  `json:"duration_ms"`
		Error      string `json:"error"`
	}
	if json.Unmarshal(lastLine(out), &info) != nil || info.Error != "" {
		return 0, domain.NewUserError("MEDIA", "미디어 파일을 읽지 못했습니다: "+info.Error)
	}
	return info.DurationMs, nil
}

// Transcribe runs the helper on the file and reads its JSON lines.
func (e *Engine) Transcribe(ctx context.Context, audio string, opts asr.Options, progress func(asr.Progress)) ([]domain.Utterance, error) {
	bin, err := e.executable()
	if err != nil {
		return nil, err
	}
	locale := opts.Locale
	if locale == "" {
		locale = "ko-KR"
	}
	args := []string{"transcribe", audio, "--locale", locale, "--install"}
	if opts.FromMs > 0 {
		args = append(args, "--from", strconv.FormatFloat(float64(opts.FromMs)/1000, 'f', 3, 64))
	}
	if opts.DurationMs > 0 {
		args = append(args, "--seconds", strconv.FormatFloat(float64(opts.DurationMs)/1000, 'f', 3, 64))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 5 * time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start speech helper: %w", err)
	}
	var utts []domain.Utterance
	var runErr error
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for sc.Scan() {
		var line struct {
			StartMs *int64 `json:"start_ms"`
			EndMs   int64  `json:"end_ms"`
			Text    string `json:"text"`
			Status  string `json:"status"`
			Error   string `json:"error"`
			Done    bool   `json:"done"`
		}
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		switch {
		case line.Error != "":
			runErr = domain.NewUserError("ASR", "음성 인식에 실패했습니다: "+line.Error)
		case line.Status != "":
			if progress != nil {
				progress(asr.Progress{Phase: line.Status, Utterances: len(utts)})
			}
		case line.StartMs != nil && line.Text != "":
			utts = append(utts, domain.Utterance{StartMs: *line.StartMs, EndMs: line.EndMs, Speaker: "방송자", Text: line.Text})
			if progress != nil {
				progress(asr.Progress{Phase: "transcribing", DoneMs: line.EndMs, Utterances: len(utts)})
			}
		}
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return utts, ctx.Err()
	}
	if runErr != nil {
		return utts, runErr
	}
	if waitErr != nil {
		return utts, domain.WrapUserError("ASR", "음성 인식 도우미가 비정상 종료했습니다: "+strings.TrimSpace(stderr.String()), waitErr)
	}
	return asr.Sorted(utts), nil
}

// executable writes the embedded helper next to the data (once per
// version) and returns its path.
func (e *Engine) executable() (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.path != "" {
		return e.path, nil
	}
	if len(e.helper) == 0 {
		return "", errors.New("no embedded helper")
	}
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(e.dir, "speech-helper-"+e.version)
	if st, err := os.Stat(path); err != nil || st.Size() != int64(len(e.helper)) {
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, e.helper, 0o700); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, path); err != nil {
			return "", err
		}
	}
	e.path = path
	return path, nil
}

func lastLine(out []byte) []byte {
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	return lines[len(lines)-1]
}
