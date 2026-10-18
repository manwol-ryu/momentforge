// Package whisper transcribes with whisper.cpp's whisper-cli, a program the
// user installs and points the app at in the settings (docs/AI-CUT-ANALYSIS.md
// 2.2). The app never downloads the program or a model. whisper-cli reads
// 16 kHz WAV (and, in recent builds, MP3 and FLAC); other containers are
// converted first with ffmpeg when it is installed, or macOS's own afconvert.
// Everything runs on this computer.
package whisper

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// ID of the engine.
const ID = "whispercpp"

// Setting keys.
const (
	KeyBin   = "bin"
	KeyModel = "model"
)

// Engine runs whisper-cli.
type Engine struct {
	dir string // where converted WAV files are written (the data folder's runtime/)

	mu    sync.Mutex
	bin   string
	model string

	// lookPath finds a program on PATH; tests replace it.
	lookPath func(string) (string, error)
	goos     string
}

// New returns the engine with no paths set; Configure sets them.
func New(dir string) *Engine {
	return &Engine{dir: dir, lookPath: exec.LookPath, goos: runtime.GOOS}
}

func (e *Engine) ID() string   { return ID }
func (e *Engine) Name() string { return "whisper.cpp (whisper-cli)" }

// Model is the model file's name without its extension.
func (e *Engine) Model() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return strings.TrimSuffix(filepath.Base(e.model), filepath.Ext(e.model))
}

// Settings lists the two paths.
func (e *Engine) Settings() []asr.Setting {
	e.mu.Lock()
	defer e.mu.Unlock()
	return []asr.Setting{
		{Key: KeyBin, Label: "whisper-cli 실행파일", Value: e.bin, Placeholder: "/usr/local/bin/whisper-cli",
			Help: "whisper.cpp를 빌드하거나 받은 뒤 whisper-cli(옛 이름 main)의 전체 경로를 적습니다. MomentForge가 설치하지는 않습니다."},
		{Key: KeyModel, Label: "모델 파일(.bin)", Value: e.model, Placeholder: "/path/ggml-large-v3-turbo-q5_0.bin",
			Help: "whisper.cpp의 ggml 모델 파일 경로입니다. 한국어는 large-v3-turbo(약 1.5GB) 또는 large-v3-turbo-q5_0(약 550MB)을 권합니다."},
	}
}

// Configure sets the paths (missing keys clear them).
func (e *Engine) Configure(values map[string]string) error {
	for k := range values {
		if k != KeyBin && k != KeyModel {
			return domain.NewUserError("INPUT", "whisper.cpp에 없는 설정 항목입니다: "+k)
		}
	}
	for _, k := range []string{KeyBin, KeyModel} {
		if v := strings.TrimSpace(values[k]); v != "" && (!filepath.IsAbs(v) || strings.ContainsAny(v, "\n\r\x00")) {
			return domain.NewUserError("INPUT", "whisper.cpp 경로는 /로 시작하는 전체 경로로 적어 주세요.")
		}
	}
	e.mu.Lock()
	e.bin, e.model = strings.TrimSpace(values[KeyBin]), strings.TrimSpace(values[KeyModel])
	e.mu.Unlock()
	return nil
}

func (e *Engine) paths() (bin, model string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.bin, e.model
}

// Ready checks that both files exist and the program is executable; a
// missing converter is a note, not a failure.
func (e *Engine) Ready(ctx context.Context) (bool, string) {
	bin, model := e.paths()
	if bin == "" {
		return false, "설정의 '방송 내용 인식'에서 whisper-cli 실행파일 경로를 적어 주세요. 설치는 사용자가 합니다 (README 참고)."
	}
	st, err := os.Stat(bin)
	switch {
	case err != nil || st.IsDir():
		return false, "whisper-cli를 찾지 못했습니다: " + bin
	case st.Mode().Perm()&0o111 == 0:
		return false, "whisper-cli에 실행 권한이 없습니다 (chmod +x): " + bin
	}
	if model == "" {
		return false, "설정의 '방송 내용 인식'에서 whisper.cpp 모델 파일(.bin) 경로를 적어 주세요."
	}
	if st, err := os.Stat(model); err != nil || st.IsDir() {
		return false, "whisper.cpp 모델 파일을 찾지 못했습니다: " + model
	}
	if _, err := e.converter(); err != nil {
		return true, "16kHz WAV만 바로 읽습니다. m4a·mp4를 전사하려면 ffmpeg를 설치하세요 (macOS는 내장 afconvert를 씁니다)."
	}
	return true, ""
}

// converter names the program that turns other containers into 16 kHz WAV.
func (e *Engine) converter() (string, error) {
	if p, err := e.lookPath("ffmpeg"); err == nil {
		return p, nil
	}
	if e.goos == "darwin" {
		if p, err := e.lookPath("afconvert"); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no audio converter")
}

// Probe reads a WAV header, or asks ffprobe/afinfo when installed.
func (e *Engine) Probe(ctx context.Context, media string) (int64, error) {
	if d, err := wavDuration(media); err == nil {
		return d, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if p, err := e.lookPath("ffprobe"); err == nil {
		out, err := exec.CommandContext(ctx, p, "-v", "error", "-show_entries", "format=duration", "-of", "default=nw=1:nk=1", media).Output()
		if err == nil {
			if secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64); err == nil && secs > 0 {
				return int64(secs * 1000), nil
			}
		}
	}
	if e.goos == "darwin" {
		if p, err := e.lookPath("afinfo"); err == nil {
			out, err := exec.CommandContext(ctx, p, media).Output()
			if err == nil {
				if m := afinfoDuration.FindSubmatch(out); m != nil {
					if secs, err := strconv.ParseFloat(string(m[1]), 64); err == nil && secs > 0 {
						return int64(secs * 1000), nil
					}
				}
			}
		}
	}
	return 0, domain.NewUserError("MEDIA", "whisper.cpp 엔진은 이 파일의 길이를 읽지 못했습니다 (WAV가 아니고 ffprobe도 없음).")
}

var (
	afinfoDuration = regexp.MustCompile(`estimated duration:\s*([0-9.]+)\s*sec`)
	progressLine   = regexp.MustCompile(`progress\s*=\s*(\d+)%`)
	// [BLANK_AUDIO], (music), ♪ … ♪: whisper's non-speech markers.
	markerOnly = regexp.MustCompile(`^[\s\[\(（【♪♫]*[^\]\)）】]*[\]\)）】♪♫]+[\s♪♫]*$`)
)

// Transcribe converts the file when needed, runs whisper-cli with JSON
// output and reads the segments.
func (e *Engine) Transcribe(ctx context.Context, audio string, opts asr.Options, progress func(asr.Progress)) ([]domain.Utterance, error) {
	bin, model := e.paths()
	if ok, how := e.Ready(ctx); !ok {
		return nil, domain.NewUserError("ASR", how)
	}
	if err := os.MkdirAll(e.dir, 0o700); err != nil {
		return nil, err
	}
	work, err := os.MkdirTemp(e.dir, "whisper-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(work)

	wav := audio
	if strings.ToLower(filepath.Ext(audio)) != ".wav" {
		if progress != nil {
			progress(asr.Progress{Phase: "preparing"})
		}
		wav = filepath.Join(work, "input.wav")
		if err := e.convert(ctx, audio, wav); err != nil {
			return nil, err
		}
	}
	lang := "ko"
	if opts.Locale != "" {
		lang, _, _ = strings.Cut(opts.Locale, "-")
	}
	outBase := filepath.Join(work, "out")
	args := []string{"-m", model, "-f", wav, "-l", lang, "-oj", "-of", outBase, "-np", "-pp"}
	if opts.FromMs > 0 {
		args = append(args, "-ot", strconv.FormatInt(opts.FromMs, 10))
	}
	if opts.DurationMs > 0 {
		args = append(args, "-d", strconv.FormatInt(opts.DurationMs, 10))
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 5 * time.Second
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stdout = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, domain.WrapUserError("ASR", "whisper-cli를 실행하지 못했습니다: "+err.Error(), err)
	}
	var tail []string
	sc := bufio.NewScanner(stderr)
	for sc.Scan() {
		line := sc.Text()
		if m := progressLine.FindStringSubmatch(line); m != nil && progress != nil {
			pct, _ := strconv.Atoi(m[1])
			progress(asr.Progress{Phase: "transcribing", DoneMs: int64(pct), TotalMs: 100})
			continue
		}
		if tail = append(tail, line); len(tail) > 20 {
			tail = tail[1:]
		}
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if waitErr != nil {
		return nil, domain.WrapUserError("ASR", "whisper-cli가 비정상 종료했습니다: "+strings.TrimSpace(strings.Join(tail, "\n")), waitErr)
	}
	utts, err := readJSON(outBase + ".json")
	if err != nil {
		return nil, err
	}
	if progress != nil {
		progress(asr.Progress{Phase: "transcribing", DoneMs: 100, TotalMs: 100, Utterances: len(utts)})
	}
	return asr.Sorted(utts), nil
}

// convert makes a 16 kHz mono WAV with ffmpeg or afconvert.
func (e *Engine) convert(ctx context.Context, in, out string) error {
	conv, err := e.converter()
	if err != nil {
		return domain.NewUserError("ASR", "whisper.cpp는 16kHz WAV만 읽습니다. 이 파일을 변환할 ffmpeg가 없습니다. ffmpeg를 설치하거나 WAV로 바꿔 두세요.")
	}
	var cmd *exec.Cmd
	if strings.HasSuffix(conv, "afconvert") {
		cmd = exec.CommandContext(ctx, conv, "-f", "WAVE", "-d", "LEI16@16000", "-c", "1", in, out)
	} else {
		cmd = exec.CommandContext(ctx, conv, "-nostdin", "-hide_banner", "-loglevel", "error", "-y", "-i", in, "-vn", "-ac", "1", "-ar", "16000", "-f", "wav", out)
	}
	cmd.WaitDelay = 5 * time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return domain.WrapUserError("ASR", "음성을 WAV로 변환하지 못했습니다: "+strings.TrimSpace(string(out)), err)
	}
	return nil
}

// readJSON reads whisper-cli's --output-json file.
func readJSON(path string) ([]domain.Utterance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, domain.WrapUserError("ASR", "whisper-cli가 결과 파일을 만들지 않았습니다.", err)
	}
	var doc struct {
		Transcription []struct {
			Offsets struct {
				From int64 `json:"from"`
				To   int64 `json:"to"`
			} `json:"offsets"`
			Text string `json:"text"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, domain.WrapUserError("ASR", "whisper-cli의 결과 파일을 읽지 못했습니다.", err)
	}
	var out []domain.Utterance
	for _, seg := range doc.Transcription {
		text := strings.Join(strings.Fields(seg.Text), " ")
		if text == "" || markerOnly.MatchString(text) {
			continue
		}
		end := seg.Offsets.To
		if end <= seg.Offsets.From {
			end = seg.Offsets.From + 1000
		}
		out = append(out, domain.Utterance{StartMs: seg.Offsets.From, EndMs: end, Speaker: "방송자", Text: text})
	}
	return out, nil
}

// wavDuration reads a RIFF/WAVE header's byte rate and data size.
func wavDuration(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	var riff [12]byte
	if _, err := io.ReadFull(f, riff[:]); err != nil || string(riff[:4]) != "RIFF" || string(riff[8:]) != "WAVE" {
		return 0, errors.New("not a WAV file")
	}
	var byteRate uint32
	for {
		var hdr [8]byte
		if _, err := io.ReadFull(f, hdr[:]); err != nil {
			return 0, errors.New("wav: no data chunk")
		}
		size := binary.LittleEndian.Uint32(hdr[4:])
		switch string(hdr[:4]) {
		case "fmt ":
			var fm [16]byte
			if _, err := io.ReadFull(f, fm[:]); err != nil {
				return 0, err
			}
			byteRate = binary.LittleEndian.Uint32(fm[8:12])
			if _, err := f.Seek(int64(size)-16+int64(size%2), io.SeekCurrent); err != nil {
				return 0, err
			}
		case "data":
			if byteRate == 0 {
				return 0, errors.New("wav: no fmt chunk")
			}
			return int64(size) * 1000 / int64(byteRate), nil
		default:
			if _, err := f.Seek(int64(size)+int64(size%2), io.SeekCurrent); err != nil {
				return 0, err
			}
		}
	}
}

var (
	_ asr.Engine       = (*Engine)(nil)
	_ asr.Configurable = (*Engine)(nil)
	_ asr.ModelNamer   = (*Engine)(nil)
)
