package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
)

// fakeEngine answers with fixed lines; ready can be switched off.
type fakeEngine struct {
	id     string
	ready  bool
	audio  []string // files Transcribe was given
	paths  map[string]string
	config bool
}

func (f *fakeEngine) ID() string                                   { return f.id }
func (f *fakeEngine) Name() string                                 { return "가짜 " + f.id }
func (f *fakeEngine) Ready(context.Context) (bool, string)         { return f.ready, "" }
func (f *fakeEngine) Probe(context.Context, string) (int64, error) { return 60_000, nil }
func (f *fakeEngine) Model() string                                { return "fake-model" }
func (f *fakeEngine) Transcribe(ctx context.Context, audio string, opts asr.Options, progress func(asr.Progress)) ([]domain.Utterance, error) {
	if _, err := os.Stat(audio); err != nil {
		return nil, err
	}
	f.audio = append(f.audio, audio)
	return []domain.Utterance{{StartMs: 1000, EndMs: 2000, Speaker: "방송자", Text: "가짜 발언"}}, nil
}
func (f *fakeEngine) Settings() []asr.Setting {
	return []asr.Setting{{Key: "bin", Label: "프로그램", Value: f.paths["bin"]}}
}
func (f *fakeEngine) Configure(values map[string]string) error {
	f.config = true
	f.paths = values
	return nil
}

// fakeAudio is a VOD platform that can also stream its audio.
type fakeAudio struct {
	fakeVOD
	fail bool
}

func (f *fakeAudio) VODAudio(ctx context.Context, t platform.Target, dst string, progress func(platform.AudioProgress)) (platform.AudioFile, error) {
	if f.fail {
		return platform.AudioFile{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "없음")
	}
	os.MkdirAll(filepath.Dir(dst), 0o700)
	if err := os.WriteFile(dst+".aac", []byte("aac"), 0o600); err != nil {
		return platform.AudioFile{}, err
	}
	progress(platform.AudioProgress{Segments: 1, TotalSegments: 1, Bytes: 3, DoneMs: f.duration})
	return platform.AudioFile{Path: dst + ".aac", DurationMs: f.duration}, nil
}

func collected(t *testing.T, a *App, url string) SessionInfo {
	t.Helper()
	if _, err := a.StartCollect(CollectRequest{URL: url}); err != nil {
		t.Fatal(err)
	}
	st := wait(t, a)
	if st.Phase != "done" {
		t.Fatalf("collect: %+v", st)
	}
	s, err := a.Session(context.Background(), st.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const captionSRT = "1\n00:00:05,000 --> 00:00:08,000\n안녕하세요\n\n2\n00:00:10,000 --> 00:00:12,000\n벌칙 <i>약속</i>\n\n3\n02:00:00,000 --> 02:00:01,000\n방송 밖\n"

func TestImportCaptionBecomesTheTranscript(t *testing.T) {
	src := &fakeVOD{duration: 60_000, pages: 1, failAt: -1}
	a, _ := newTestApp(t, src)
	s := collected(t, a, "fake:1")
	if s.TranscribeSource != "" {
		t.Fatalf("a plain VOD platform cannot transcribe: %q", s.TranscribeSource)
	}

	v, err := a.ImportCaption(context.Background(), s.ID, "방송.SRT", strings.NewReader(captionSRT), 2000)
	if err != nil {
		t.Fatal(err)
	}
	if v.Source != "file" || v.Utterances != 2 || v.Engine != "" {
		t.Fatalf("transcript = %+v", v)
	}
	utts, err := a.Utterances(context.Background(), s.ID, 0, 600_000, 100)
	if err != nil || len(utts) != 2 || utts[0].StartMs != 7000 || utts[0].Text != "안녕하세요" || utts[1].Text != "벌칙 약속" {
		t.Fatalf("utterances = %+v, %v", utts, err)
	}
	tr, _, _ := a.cfg.Store.LatestTranscript(context.Background(), s.ID)
	if tr.Model != "srt" {
		t.Errorf("model = %q", tr.Model)
	}
	files, err := os.ReadDir(filepath.Join(a.exportDir(s.ID), "transcript"))
	if err != nil || len(files) != 3 {
		t.Fatalf("caption files = %v, %v", files, err)
	}
	srt, _ := os.ReadFile(filepath.Join(a.exportDir(s.ID), "transcript", tr.ID+".srt"))
	if !strings.Contains(string(srt), "00:00:07,000 --> 00:00:10,000\n안녕하세요") {
		t.Errorf("srt = %s", srt)
	}

	// Rejections are user errors with a reason.
	for name, in := range map[string]struct {
		body   string
		offset int64
	}{"not a caption": {"그냥 글", 0}, "all outside": {captionSRT, 20 * 3600 * 1000}, "offset too big": {captionSRT, 48 * 3600 * 1000}} {
		_, err := a.ImportCaption(context.Background(), s.ID, "x.srt", strings.NewReader(in.body), in.offset)
		var ue *domain.UserError
		if !errors.As(err, &ue) || ue.Code != "INPUT" {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A second file replaces the first as the newest transcript.
	v2, err := a.ImportCaption(context.Background(), s.ID, "b.vtt", strings.NewReader("WEBVTT\n\n00:01.000 --> 00:02.000\n한 줄\n"), 0)
	if err != nil || v2.Utterances != 1 || v2.ID == v.ID {
		t.Fatalf("second import = %+v, %v", v2, err)
	}
}

func TestTranscribeStreamDownloadsThenDeletes(t *testing.T) {
	src := &fakeAudio{fakeVOD: fakeVOD{duration: 60_000, pages: 1, failAt: -1}}
	a, _ := newTestApp(t, src)
	engine := &fakeEngine{id: "fake", ready: true}
	a.cfg.ASR.Register(engine)
	// Collected without transcribing by itself (일반 설정 off), so the button's job does it.
	if _, err := a.SaveSettings(Settings{}); err != nil {
		t.Fatal(err)
	}
	s := collected(t, a, "fake:2")
	if s.TranscribeSource != "stream" {
		t.Fatalf("TranscribeSource = %q", s.TranscribeSource)
	}
	if _, err := a.StartTranscribe(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	st := wait(t, a)
	if st.Phase != "done" || st.Utterances != 1 || !strings.Contains(st.Message, "발언 1건") {
		t.Fatalf("job = %+v", st)
	}
	if len(engine.audio) != 1 || !strings.HasPrefix(engine.audio[0], filepath.Join(a.cfg.DataDir, "runtime", "audio")) {
		t.Fatalf("engine read %v", engine.audio)
	}
	if _, err := os.Stat(engine.audio[0]); !os.IsNotExist(err) {
		t.Error("downloaded audio was not deleted")
	}
	v, ok, _ := a.Transcript(context.Background(), s.ID)
	if !ok || v.Source != "asr" || v.Engine != "fake" {
		t.Fatalf("transcript = %+v", v)
	}
	tr, _, _ := a.cfg.Store.LatestTranscript(context.Background(), s.ID)
	if tr.Model != "fake-model" {
		t.Errorf("model = %q", tr.Model)
	}

	// A download failure is reported and leaves no file.
	src.fail = true
	if _, err := a.StartTranscribe(context.Background(), s.ID); err != nil {
		t.Fatal(err)
	}
	if st := wait(t, a); st.Phase != "error" || st.Code != "PLAYBACK_UNAVAILABLE" {
		t.Fatalf("job = %+v", st)
	}
	left, _ := os.ReadDir(filepath.Join(a.cfg.DataDir, "runtime", "audio"))
	if len(left) != 0 {
		t.Errorf("leftovers %v", left)
	}

	// Without an engine the request is refused before any download.
	engine.ready = false
	_, err := a.StartTranscribe(context.Background(), s.ID)
	var ue *domain.UserError
	if !errors.As(err, &ue) || ue.Code != "ASR" {
		t.Errorf("no engine: %v", err)
	}
}

func TestTranscribeNeedsASource(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 60_000, pages: 1, failAt: -1})
	a.cfg.ASR.Register(&fakeEngine{id: "fake", ready: true})
	s := collected(t, a, "fake:3")
	_, err := a.StartTranscribe(context.Background(), s.ID)
	var ue *domain.UserError
	if !errors.As(err, &ue) || ue.Code != "INPUT" || !strings.Contains(ue.Message, "자막 파일") {
		t.Fatalf("err = %v", err)
	}
}

// The saved engine is used when ready; otherwise the first ready one. Paths
// reach Configurable engines at start and after a save.
func TestASRSettingsChooseTheEngineAndPassPaths(t *testing.T) {
	a, _ := newTestApp(t, &fakeVOD{duration: 60_000, pages: 1, failAt: -1})
	first := &fakeEngine{id: "first", ready: true}
	second := &fakeEngine{id: "second", ready: true}
	a.cfg.ASR.Register(first, second)
	ctx := context.Background()
	if e := a.defaultASR(ctx); e == nil || e.ID() != "first" {
		t.Fatalf("default = %v", e)
	}
	views := a.ASREngines(ctx)
	if len(views) != 2 || !views[0].Default || views[1].Default || len(views[1].Settings) != 1 {
		t.Fatalf("views = %+v", views)
	}

	s := a.Settings().Settings
	s.ASREngine = "second"
	s.ASRPaths = map[string]map[string]string{"second": {"bin": "/opt/whisper-cli"}}
	if _, err := a.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if e := a.defaultASR(ctx); e.ID() != "second" {
		t.Errorf("default after save = %s", e.ID())
	}
	if !second.config || second.paths["bin"] != "/opt/whisper-cli" {
		t.Errorf("second not configured: %+v", second.paths)
	}
	env := readFile(t, a.settingsPath())
	for _, line := range []string{"ASR_ENGINE=second\n", "ASR_SECOND_BIN=/opt/whisper-cli\n"} {
		if !strings.Contains(env, line) {
			t.Errorf("settings.env lacks %q:\n%s", line, env)
		}
	}
	got := a.Settings()
	if got.ASREngine != "second" || got.ASRPaths["second"]["bin"] != "/opt/whisper-cli" {
		t.Errorf("Settings = %+v", got.Settings)
	}

	// The saved engine not ready: the first ready one steps in.
	second.ready = false
	if e := a.defaultASR(ctx); e.ID() != "first" {
		t.Errorf("fallback = %s", e.ID())
	}

	// Bad input is refused.
	for name, bad := range map[string]Settings{
		"unknown engine": {ASREngine: "nope"},
		"unknown key":    {ASRPaths: map[string]map[string]string{"second": {"model": "/m"}}},
		"not configurable engine paths with control chars": {ASRPaths: map[string]map[string]string{"second": {"bin": "/a\nb"}}},
	} {
		if _, err := a.SaveSettings(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// The next run applies the saved paths at start.
	third := &fakeEngine{id: "second"}
	b := reopen(t, a)
	b.cfg.ASR.Register(third)
	b.applyASRSettings()
	if third.paths["bin"] != "/opt/whisper-cli" {
		t.Errorf("paths not applied at start: %+v", third.paths)
	}
}
