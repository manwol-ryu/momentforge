package whisper

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/asr"
)

// fakeCLI is a shell script standing in for whisper-cli: it logs its
// arguments, prints progress on stderr and writes whisper's JSON output.
const fakeCLI = `#!/bin/sh
echo "$@" > "$(dirname "$0")/args.txt"
echo "whisper_print_progress_callback: progress =  50%" >&2
echo "whisper_print_progress_callback: progress = 100%" >&2
while [ $# -gt 0 ]; do
  if [ "$1" = "-of" ]; then out="$2"; fi
  shift
done
cat > "$out.json" <<'JSON'
{"transcription":[
 {"timestamps":{"from":"00:00:00,000","to":"00:00:02,000"},"offsets":{"from":0,"to":2000},"text":" [BLANK_AUDIO]"},
 {"timestamps":{"from":"00:00:02,000","to":"00:00:05,500"},"offsets":{"from":2000,"to":5500},"text":"  안녕하세요  방송   시작합니다"},
 {"timestamps":{"from":"00:00:05,500","to":"00:00:05,500"},"offsets":{"from":5500,"to":5500},"text":"(music)"},
 {"timestamps":{"from":"00:00:06,000","to":"00:00:06,000"},"offsets":{"from":6000,"to":6000},"text":"짧은 말"}
]}
JSON
`

func writeWAV(t *testing.T, path string, seconds int) {
	t.Helper()
	const rate, channels, bits = 16000, 1, 16
	byteRate := rate * channels * bits / 8
	data := make([]byte, seconds*byteRate)
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+len(data)))
	copy(hdr[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1)
	binary.LittleEndian.PutUint16(hdr[22:], channels)
	binary.LittleEndian.PutUint32(hdr[24:], rate)
	binary.LittleEndian.PutUint32(hdr[28:], uint32(byteRate))
	binary.LittleEndian.PutUint16(hdr[32:], channels*bits/8)
	binary.LittleEndian.PutUint16(hdr[34:], bits)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data)))
	if err := os.WriteFile(path, append(hdr, data...), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newTestEngine(t *testing.T) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	e := New(filepath.Join(dir, "runtime"))
	e.lookPath = func(string) (string, error) { return "", errors.New("not installed") }
	return e, dir
}

func TestReadySaysWhatIsMissing(t *testing.T) {
	e, dir := newTestEngine(t)
	bin := filepath.Join(dir, "whisper-cli")
	model := filepath.Join(dir, "ggml-large-v3-turbo-q5_0.bin")
	check := func(step string, wantOK bool, wantHow string) {
		t.Helper()
		ok, how := e.Ready(context.Background())
		if ok != wantOK || !strings.Contains(how, wantHow) {
			t.Errorf("%s: Ready = %v, %q; want %v, …%q…", step, ok, how, wantOK, wantHow)
		}
	}
	check("nothing set", false, "whisper-cli 실행파일 경로")
	if err := e.Configure(map[string]string{KeyBin: "relative/whisper-cli"}); err == nil {
		t.Error("a relative path was accepted")
	}
	if err := e.Configure(map[string]string{"other": "x"}); err == nil {
		t.Error("an unknown key was accepted")
	}
	e.Configure(map[string]string{KeyBin: bin})
	check("missing program", false, "찾지 못했습니다: "+bin)
	os.WriteFile(bin, []byte(fakeCLI), 0o600)
	check("not executable", false, "실행 권한")
	os.Chmod(bin, 0o700)
	check("no model", false, "모델 파일(.bin) 경로")
	e.Configure(map[string]string{KeyBin: bin, KeyModel: model})
	check("missing model", false, "모델 파일을 찾지 못했습니다")
	os.WriteFile(model, []byte("ggml"), 0o600)
	check("ready without converter", true, "ffmpeg")
	e.lookPath = func(name string) (string, error) { return "/usr/bin/" + name, nil }
	check("ready", true, "")
	if e.Model() != "ggml-large-v3-turbo-q5_0" {
		t.Errorf("Model = %q", e.Model())
	}
	s := e.Settings()
	if len(s) != 2 || s[0].Key != KeyBin || s[0].Value != bin || s[1].Key != KeyModel || s[1].Value != model {
		t.Errorf("Settings = %+v", s)
	}
}

func TestTranscribeRunsTheProgramAndReadsItsJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	e, dir := newTestEngine(t)
	bin := filepath.Join(dir, "whisper-cli")
	model := filepath.Join(dir, "m.bin")
	os.WriteFile(bin, []byte(fakeCLI), 0o700)
	os.WriteFile(model, []byte("ggml"), 0o600)
	e.Configure(map[string]string{KeyBin: bin, KeyModel: model})
	wav := filepath.Join(dir, "in.wav")
	writeWAV(t, wav, 7)

	var progress []asr.Progress
	utts, err := e.Transcribe(context.Background(), wav, asr.Options{Locale: "ko-KR", FromMs: 1000, DurationMs: 6000}, func(p asr.Progress) {
		progress = append(progress, p)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(utts) != 2 || utts[0].Text != "안녕하세요 방송 시작합니다" || utts[0].StartMs != 2000 || utts[0].EndMs != 5500 || utts[0].Speaker != "방송자" {
		t.Errorf("utterances = %+v", utts)
	}
	if utts[1].Text != "짧은 말" || utts[1].EndMs != 7000 { // a zero-length segment gets a second
		t.Errorf("utterance 2 = %+v", utts[1])
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args.txt"))
	for _, want := range []string{"-m " + model, "-f " + wav, "-l ko", "-oj", "-np", "-ot 1000", "-d 6000"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("args %q lack %q", args, want)
		}
	}
	if len(progress) < 3 || progress[0].DoneMs != 50 || progress[len(progress)-1].Utterances != 2 {
		t.Errorf("progress = %+v", progress)
	}
	left, _ := os.ReadDir(filepath.Join(dir, "runtime"))
	if len(left) != 0 {
		t.Errorf("work dir not cleaned: %v", left)
	}
}

func TestProbeReadsWAV(t *testing.T) {
	e, dir := newTestEngine(t)
	wav := filepath.Join(dir, "in.wav")
	writeWAV(t, wav, 3)
	d, err := e.Probe(context.Background(), wav)
	if err != nil || d != 3000 {
		t.Errorf("Probe = %d, %v", d, err)
	}
	m4a := filepath.Join(dir, "in.m4a")
	os.WriteFile(m4a, []byte("not wav"), 0o600)
	if _, err := e.Probe(context.Background(), m4a); err == nil {
		t.Error("a non-WAV file without ffprobe must fail")
	}
}

func TestTranscribeNeedsAConverterForOtherFiles(t *testing.T) {
	e, dir := newTestEngine(t)
	bin := filepath.Join(dir, "whisper-cli")
	model := filepath.Join(dir, "m.bin")
	os.WriteFile(bin, []byte(fakeCLI), 0o700)
	os.WriteFile(model, []byte("ggml"), 0o600)
	e.Configure(map[string]string{KeyBin: bin, KeyModel: model})
	m4a := filepath.Join(dir, "in.m4a")
	os.WriteFile(m4a, []byte("aac"), 0o600)
	_, err := e.Transcribe(context.Background(), m4a, asr.Options{}, nil)
	if err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Errorf("err = %v", err)
	}
}
