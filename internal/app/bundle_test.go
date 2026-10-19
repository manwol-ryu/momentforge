package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// fakeVideo is a VOD whose video is three "segments"; failAfter stops a
// download with err after that many segments (once), as a dropped
// connection or a refused video would.
type fakeVideo struct {
	fakeVOD
	vmu       sync.Mutex
	failAfter int
	err       error
	resumed   []*platform.VideoResume
}

func (f *fakeVideo) VODVideo(ctx context.Context, t platform.Target, path string, req platform.VideoRequest,
	progress func(platform.VideoProgress)) (platform.VideoFile, error) {
	f.vmu.Lock()
	f.resumed = append(f.resumed, req.Resume)
	failAfter, ferr := f.failAfter, f.err
	f.failAfter = -1
	f.vmu.Unlock()
	rend := platform.VideoRendition{Width: 1280, Height: req.MaxHeight}
	st := platform.VideoResume{Height: req.MaxHeight, TotalSegments: 3, Shift: []platform.TrackShift{{TrackID: 1, Timescale: 1000, Ticks: 2050}}}
	flags := os.O_CREATE | os.O_TRUNC | os.O_WRONLY
	if req.Resume != nil {
		st, flags = *req.Resume, os.O_WRONLY
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return platform.VideoFile{}, err
	}
	defer file.Close()
	file.Truncate(st.Bytes)
	file.Seek(st.Bytes, 0)
	for i := st.Segments; i < 3; i++ {
		if i == failAfter {
			return platform.VideoFile{}, ferr
		}
		b := bytes.Repeat([]byte{byte('a' + i)}, 10)
		file.Write(b)
		st.Segments, st.Bytes, st.DoneMs = i+1, st.Bytes+10, int64(i+1)*f.duration/3
		progress(platform.VideoProgress{Rendition: rend, State: st})
	}
	return platform.VideoFile{Path: path, DurationMs: f.duration, Container: "fmp4", Rendition: rend, FirstPTSMs: 2050, State: st}, nil
}

func bundleOf(t *testing.T, a *App, sid string, video bool) JobStatus {
	t.Helper()
	if _, err := a.StartBundle(context.Background(), BundleRequest{SessionID: sid, Video: video}); err != nil {
		t.Fatal(err)
	}
	return wait(t, a)
}

func readManifest(t *testing.T, dir string) export.BundleManifest {
	t.Helper()
	var m export.BundleManifest
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestBundleHoldsEverythingAndUpdatesOnlyWhatChanged(t *testing.T) {
	src := &fakeVideo{fakeVOD: fakeVOD{duration: 600_000, pages: 2, failAt: -1}, failAfter: -1}
	a, _ := newTestApp(t, src)
	s := collected(t, a, "fake:7")
	ctx := context.Background()

	st := bundleOf(t, a, s.ID, true)
	if st.Phase != "done" || st.Kind != "bundle" || !strings.Contains(st.Message, "영상 포함") {
		t.Fatalf("job = %+v", st)
	}
	dir := filepath.Join(a.cfg.DataDir, "backups", s.ID)
	m := readManifest(t, dir)
	if m.Format != export.BundleFormat || m.Version != export.BundleVersion || m.Session.ID != s.ID || m.Session.ChatCount != s.ChatCount {
		t.Fatalf("manifest = %+v", m)
	}
	if m.Video.State != "complete" || m.TimeAxis.VideoOffsetMs != 0 || !strings.Contains(m.TimeAxis.Method, "0초") {
		t.Fatalf("video %+v, time axis %+v", m.Video, m.TimeAxis)
	}
	if m.Contents.AnalysisID != s.LatestAnalysisID || !m.Contents.Chat || !m.Contents.Timeline {
		t.Fatalf("contents = %+v", m.Contents)
	}
	listed := map[string]export.BundleFile{}
	for _, f := range m.Files {
		listed[f.Path] = f
	}
	for _, want := range []string{"chat.jsonl", "chat.txt", "chat-manifest.json", "timeline/timeline.json", "notes.md",
		"analysis/" + s.LatestAnalysisID + "/analysis-manifest.json", "video.mp4", "video.json"} {
		f, ok := listed[want]
		if !ok {
			t.Errorf("%s not listed", want)
			continue
		}
		size, sum, err := export.HashFile(filepath.Join(dir, filepath.FromSlash(want)))
		if err != nil || size != f.Bytes || sum != f.SHA256 {
			t.Errorf("%s: listed %d %s, on disk %d %s (%v)", want, f.Bytes, f.SHA256, size, sum, err)
		}
	}
	for _, name := range []string{"README.txt", "video.mp4"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Error(err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "video.mp4.part")); !os.IsNotExist(err) {
		t.Error("partial video left behind")
	}
	var vdoc export.BundleVideoFile
	raw, _ := os.ReadFile(filepath.Join(dir, "video.json"))
	json.Unmarshal(raw, &vdoc)
	if vdoc.State != "complete" || vdoc.FirstPTSMs != 2050 || !vdoc.Rebased || vdoc.Height != 720 || vdoc.Segments != 3 || vdoc.SHA256 == "" {
		t.Fatalf("video.json = %+v", vdoc)
	}

	// Nothing that identifies a viewer or unlocks the pseudonyms is in the bundle.
	salt, err := a.cfg.Store.SessionSalt(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	secrets := []string{hex.EncodeToString(salt), base64.StdEncoding.EncodeToString(salt), base64.RawURLEncoding.EncodeToString(salt)}
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, _ := os.ReadFile(path)
		for _, sec := range secrets {
			if bytes.Contains(b, []byte(sec)) {
				t.Errorf("%s contains the session salt", path)
			}
		}
		return nil
	})
	f, _ := os.Open(filepath.Join(dir, "chat.jsonl"))
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var row struct {
			AuthorKey string `json:"author_key"`
		}
		json.Unmarshal(sc.Bytes(), &row)
		if strings.HasPrefix(row.AuthorKey, "u") && len(row.AuthorKey) < 4 {
			t.Fatalf("original author id in chat.jsonl: %q", row.AuthorKey)
		}
	}
	f.Close()

	// The second run rewrites nothing; a finished video stays even without the video choice.
	st = bundleOf(t, a, s.ID, false)
	if st.Phase != "done" || !strings.Contains(st.Message, "바뀐 파일 0개") {
		t.Fatalf("second run = %+v", st)
	}
	if m := readManifest(t, dir); m.Video.State != "complete" {
		t.Fatalf("video after rerun = %+v", m.Video)
	}

	// A file the bundle listed before and no longer has is removed; one the user put there stays.
	rec, _, _ := a.cfg.Store.Backup(ctx, s.ID)
	rec.Files = append(rec.Files, store.BackupFile{Path: "cuts/old/plan.json"}, store.BackupFile{Path: "../outside.txt"})
	a.cfg.Store.SaveBackup(ctx, rec)
	os.MkdirAll(filepath.Join(dir, "cuts", "old"), 0o700)
	os.WriteFile(filepath.Join(dir, "cuts", "old", "plan.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("mine"), 0o600)
	outside := filepath.Join(filepath.Dir(dir), "outside.txt")
	os.WriteFile(outside, []byte("keep"), 0o600)
	if st := bundleOf(t, a, s.ID, false); st.Phase != "done" {
		t.Fatalf("third run = %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, "cuts", "old")); !os.IsNotExist(err) {
		t.Error("stale cut plan folder was not removed")
	}
	for _, keep := range []string{filepath.Join(dir, "mine.txt"), outside} {
		if _, err := os.Stat(keep); err != nil {
			t.Errorf("%s was removed", keep)
		}
	}
	v, err := a.Bundle(ctx, s.ID)
	if err != nil || !v.Exists || v.Dir != dir || v.VideoState != "complete" || v.VideoSource != "download" || v.Files == 0 {
		t.Fatalf("view = %+v, %v", v, err)
	}
}

func TestBundleVideoResumesAfterAStopAndSkipsARefusedVideo(t *testing.T) {
	src := &fakeVideo{fakeVOD: fakeVOD{duration: 600_000, pages: 1, failAt: -1}, failAfter: 2,
		err: domain.NewUserError("HTTP_ERROR", "영상 조각을 받지 못했습니다 (HTTP 503).")}
	a, _ := newTestApp(t, src)
	s := collected(t, a, "fake:8")
	dir := filepath.Join(a.cfg.DataDir, "backups", s.ID)

	st := bundleOf(t, a, s.ID, true)
	if st.Phase != "error" || st.Code != "HTTP_ERROR" {
		t.Fatalf("first run = %+v", st)
	}
	if m := readManifest(t, dir); m.Video.State != "partial" || !m.Contents.Chat {
		t.Fatalf("after a stop the text files are written and the video is partial: %+v", m.Video)
	}
	if v, _ := a.Bundle(context.Background(), s.ID); v.VideoState != "partial" {
		t.Fatalf("view = %+v", v)
	}
	st = bundleOf(t, a, s.ID, true)
	if st.Phase != "done" {
		t.Fatalf("resume = %+v", st)
	}
	if len(src.resumed) != 2 || src.resumed[1] == nil || src.resumed[1].Segments != 2 || src.resumed[1].Bytes != 20 {
		t.Fatalf("resume states = %+v", src.resumed)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "video.mp4"))
	if string(data) != strings.Repeat("a", 10)+strings.Repeat("b", 10)+strings.Repeat("c", 10) {
		t.Fatalf("video = %q", data)
	}

	// A video the platform refuses leaves the bundle without it, with the reason.
	src2 := &fakeVideo{fakeVOD: fakeVOD{duration: 600_000, pages: 1, failAt: -1}, failAfter: 0,
		err: domain.NewUserError("PLAYBACK_RESTRICTED", "이 영상은 앱 내부 공개 재생을 지원하지 않습니다.")}
	b, _ := newTestApp(t, src2)
	s2 := collected(t, b, "fake:9")
	st = bundleOf(t, b, s2.ID, true)
	if st.Phase != "done" || !strings.Contains(st.Warning, "공개 재생") {
		t.Fatalf("refused = %+v", st)
	}
	m := readManifest(t, filepath.Join(b.cfg.DataDir, "backups", s2.ID))
	if m.Video.State != "none" || !strings.Contains(m.Video.Reason, "공개 재생") {
		t.Fatalf("video = %+v", m.Video)
	}
	if _, err := os.Stat(filepath.Join(b.cfg.DataDir, "backups", s2.ID, "video.mp4.part")); !os.IsNotExist(err) {
		t.Error("partial video left behind")
	}
}

func TestBundleFolderComesFromSettings(t *testing.T) {
	src := &fakeVideo{fakeVOD: fakeVOD{duration: 60_000, pages: 1, failAt: -1}, failAfter: -1}
	a, _ := newTestApp(t, src)
	s := collected(t, a, "fake:3")
	set := a.Settings().Settings
	if !set.BackupVideo || set.BackupVideoHeight != 720 {
		t.Fatalf("defaults = %+v", set)
	}
	set.BackupDir = "relative/path"
	if _, err := a.SaveSettings(set); err == nil {
		t.Fatal("relative folder accepted")
	}
	set.BackupDir, set.BackupVideoHeight = filepath.Join(t.TempDir(), "nas"), 1080
	if _, err := a.SaveSettings(set); err != nil {
		t.Fatal(err)
	}
	if st := bundleOf(t, a, s.ID, true); st.Phase != "done" {
		t.Fatalf("job = %+v", st)
	}
	dir := filepath.Join(set.BackupDir, s.ID)
	if m := readManifest(t, dir); m.Video.State != "complete" {
		t.Fatalf("manifest video = %+v", m.Video)
	}
	if src.resumed[0] != nil {
		t.Error("fresh bundle resumed")
	}
	if _, err := a.StartBundle(context.Background(), BundleRequest{SessionID: "nope"}); err == nil {
		t.Error("unknown session accepted")
	}
}
