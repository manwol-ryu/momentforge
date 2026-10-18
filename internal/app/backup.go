package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/backup"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/privacy"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// backupPlatform marks sessions imported from a NAS backup folder
// (docs/AI-CUT-ANALYSIS.md 2.5a). They have no platform adapter: the chat
// came from the recorder's file and the video plays from the file itself.
const backupPlatform = "backup"

// BackupView is one broadcast found in a backup folder.
type BackupView struct {
	Name       string    `json:"name"`
	Title      string    `json:"title"`
	Date       string    `json:"date,omitempty"` // YYYY-MM-DD
	ChatPath   string    `json:"chatPath"`
	HasAudio   bool      `json:"hasAudio"`
	HasVideo   bool      `json:"hasVideo"`
	ChatBytes  int64     `json:"chatBytes"`
	AudioBytes int64     `json:"audioBytes"`
	VideoBytes int64     `json:"videoBytes"`
	SessionID  string    `json:"sessionId,omitempty"` // already imported
	ImportedAt time.Time `json:"importedAt,omitzero"`
}

// ScanBackups lists the broadcasts in a folder and which are already imported.
func (a *App) ScanBackups(ctx context.Context, dir string) ([]BackupView, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil, domain.NewUserError("INPUT", "백업 폴더 경로를 입력해 주세요.")
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil, domain.NewUserError("INPUT", "그 경로에 폴더가 없습니다. NAS가 연결되어 있는지(Finder에서 열리는지) 확인하세요.")
	}
	list, err := backup.Scan(dir)
	if err != nil {
		return nil, domain.WrapUserError("INPUT", "폴더를 읽지 못했습니다.", err)
	}
	sessions, err := a.cfg.Store.Sessions(ctx, true)
	if err != nil {
		return nil, err
	}
	imported := map[string]store.Session{}
	for _, s := range sessions {
		if s.Platform == backupPlatform {
			imported[s.TargetID] = s
		}
	}
	out := []BackupView{}
	for _, b := range list {
		v := BackupView{Name: b.Name, Title: b.Title, ChatPath: b.ChatPath, HasAudio: b.AudioPath != "", HasVideo: b.VideoPath != "",
			ChatBytes: b.ChatBytes, AudioBytes: b.AudioBytes, VideoBytes: b.VideoBytes}
		if !b.Date.IsZero() {
			v.Date = b.Date.Format("2006-01-02")
		}
		if s, ok := imported[backupTarget(b)]; ok {
			v.SessionID, v.ImportedAt = s.ID, s.CreatedAt
		}
		out = append(out, v)
	}
	return out, nil
}

// backupTarget identifies a broadcast across imports: its folder and name.
func backupTarget(b backup.Broadcast) string {
	return filepath.Join(filepath.Dir(b.ChatPath), b.Name)
}

// ImportRequest asks to import one broadcast (any of its files names it).
type ImportRequest struct {
	Path       string
	Transcribe bool // run speech recognition on the audio after the chat
	Sensitive  bool
}

// StartImportBackup imports a broadcast from a backup folder in the
// background: its chat as a new session (pseudonymised), then analysis,
// then, when asked, the transcript.
func (a *App) StartImportBackup(req ImportRequest) (JobStatus, error) {
	b, err := backup.Locate(strings.TrimSpace(req.Path))
	if err != nil {
		if _, ok := domain.AsUserError(err); ok {
			return JobStatus{}, err
		}
		return JobStatus{}, domain.WrapUserError("INPUT", "그 파일을 읽지 못했습니다. 경로를 확인하세요.", err)
	}
	if req.Transcribe && b.AudioPath == "" {
		return JobStatus{}, domain.NewUserError("INPUT", "이 방송에는 음성 파일(.m4a)이 없어 전사할 수 없습니다. 채팅만 불러오려면 전사를 끄세요.")
	}
	return a.jobs.start(a.ctx, "import", func(ctx context.Context, h job) error {
		return a.importBackup(ctx, h, b, req)
	})
}

func (a *App) importBackup(ctx context.Context, h job, b backup.Broadcast, req ImportRequest) error {
	st := a.cfg.Store
	h.update(func(s *JobStatus) {
		s.Phase, s.Message, s.Title = "checking", "백업 파일을 확인하는 중입니다.", b.Title
	})
	// Already imported: open it, and transcribe it if that was asked and not done.
	all, err := st.Sessions(ctx, true)
	if err != nil {
		return err
	}
	for _, s := range all {
		if s.Platform == backupPlatform && s.TargetID == backupTarget(b) {
			if err := st.SetHidden(ctx, s.ID, false); err != nil {
				return err
			}
			h.update(func(js *JobStatus) { js.SessionID, js.DurationMs = s.ID, s.DurationMs })
			if _, has, err := st.LatestTranscript(ctx, s.ID); err != nil {
				return err
			} else if req.Transcribe && !has {
				return a.transcribe(ctx, h, s.ID, b.AudioPath)
			}
			h.update(func(js *JobStatus) { js.Message = "이미 불러온 방송입니다." })
			return nil
		}
	}

	f, err := os.Open(b.ChatPath)
	if err != nil {
		return domain.WrapUserError("INPUT", "채팅 파일을 열지 못했습니다.", err)
	}
	records, skipped, err := backup.ParseChat(f)
	f.Close()
	if errors.Is(err, backup.ErrFormat) {
		return domain.NewUserError("INPUT", "chzzk-automation의 채팅 파일(.chat.jsonl) 형식이 아닙니다.")
	}
	if err != nil {
		return domain.WrapUserError("INPUT", "채팅 파일을 읽지 못했습니다.", err)
	}
	// The length comes from the audio when there is one, else from the last chat.
	var durationMs int64
	if b.AudioPath != "" {
		if engine := a.defaultASR(ctx); engine != nil {
			if d, err := engine.Probe(ctx, b.AudioPath); err == nil {
				durationMs = d
			}
		}
	}
	if durationMs == 0 && len(records) > 0 {
		durationMs = records[len(records)-1].OffsetMs + 1000
	}
	salt, err := privacy.NewSalt()
	if err != nil {
		return err
	}
	sess, err := st.CreateSession(ctx, store.NewSession{Platform: backupPlatform, Kind: "vod", TargetID: backupTarget(b),
		Title: b.Title, DurationMs: durationMs, Salt: salt})
	if err != nil {
		return err
	}
	h.update(func(s *JobStatus) {
		s.SessionID, s.DurationMs, s.Phase, s.Message = sess.ID, durationMs, "collecting", "채팅을 불러오는 중입니다."
	})
	events := backupEvents(salt, records)
	// The raw page says only where the chat came from; the recorder's file (with original ids) is not copied.
	raw := fmt.Sprintf(`{"source":"backup","file":%q,"records":%d,"skipped":%d}`, filepath.Base(b.ChatPath), len(records), skipped)
	if _, err := st.SavePage(ctx, sess.ID, store.Page{Raw: []byte(raw), Events: events, Next: durationMs, Finished: true}); err != nil {
		return err
	}
	for _, m := range []struct{ kind, path string }{{"audio", b.AudioPath}, {"video", b.VideoPath}} {
		if m.path == "" {
			continue
		}
		if info, err := os.Stat(m.path); err == nil {
			if err := st.SetMediaRef(ctx, sess.ID, store.MediaRef{Kind: m.kind, Path: m.path, Bytes: info.Size(), Modified: info.ModTime()}); err != nil {
				return err
			}
		}
	}
	dir := a.exportDir(sess.ID)
	warning := ""
	if received, rerr := st.Chats(ctx, sess.ID, store.AllSeq, true); rerr != nil || export.WriteReceived(dir, received) != nil {
		warning = "수신 TXT를 쓰지 못했습니다. DB 저장은 끝났습니다."
	}
	if err := a.writeChat(ctx, sess.ID); err != nil {
		warning = "채팅 파일을 쓰지 못했습니다. 저장된 채팅으로 다시 분석할 수 있습니다."
	}
	h.update(func(s *JobStatus) {
		s.Phase, s.Message, s.Warning, s.PositionMs, s.Pages = "analyzing", "편집 후보를 찾는 중입니다.", warning, durationMs, 1
	})
	an, err := a.analyzeSession(ctx, sess.ID, a.params(req.Sensitive), h)
	if err != nil {
		return err
	}
	h.update(func(s *JobStatus) { s.AnalysisID, s.Candidates = an.ID, len(an.Candidates) })
	if req.Transcribe {
		return a.transcribe(ctx, h, sess.ID, b.AudioPath)
	}
	h.update(func(s *JobStatus) { s.Message = "불러오기와 분석이 끝났습니다." })
	return nil
}

// backupEvents pseudonymises the recorder's records: authors become
// per-session keys, event ids are derived, and nothing original is kept.
func backupEvents(salt []byte, records []backup.Record) []domain.ChatEvent {
	type key struct {
		t      int64
		author string
		text   string
	}
	seen := map[key]int{}
	out := make([]domain.ChatEvent, 0, len(records))
	for _, r := range records {
		k := key{r.OffsetMs, r.AuthorID, r.Text}
		seen[k]++
		e := domain.ChatEvent{ID: privacy.EventID(salt, r.OffsetMs, r.AuthorID, r.Text, seen[k]), OffsetMs: r.OffsetMs,
			Text: r.Text, Amount: r.Amount, Hidden: r.Status == "CBOTBLIND"}
		if r.AuthorID != "" {
			e.AuthorKey = privacy.AuthorKey(salt, r.AuthorID)
		}
		if r.Kind != backup.KindChat {
			e.Kind = r.Kind
		}
		out = append(out, e)
	}
	return out
}

// defaultASR is the engine the settings name when it is ready, else the
// first registered engine that is ready, if any.
func (a *App) defaultASR(ctx context.Context) asr.Engine {
	if id := a.Settings().ASREngine; id != "" {
		if e, ok := a.cfg.ASR.Get(id); ok {
			if ready, _ := e.Ready(ctx); ready {
				return e
			}
		}
	}
	for _, e := range a.cfg.ASR.All() {
		if ok, _ := e.Ready(ctx); ok {
			return e
		}
	}
	return nil
}

// ASREngineView is one speech recognition engine's state for the screen.
type ASREngineView struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Ready    bool          `json:"ready"`
	How      string        `json:"how,omitempty"` // what to do when it is not ready, or a note when it is
	Default  bool          `json:"default"`       // the engine a transcription would use now
	Settings []asr.Setting `json:"settings,omitempty"`
}

// ASREngines lists the engines in registration order.
func (a *App) ASREngines(ctx context.Context) []ASREngineView {
	out := []ASREngineView{}
	def := a.defaultASR(ctx)
	for _, e := range a.cfg.ASR.All() {
		ok, how := e.Ready(ctx)
		v := ASREngineView{ID: e.ID(), Name: e.Name(), Ready: ok, How: how, Default: def != nil && def.ID() == e.ID()}
		if c, can := e.(asr.Configurable); can {
			v.Settings = c.Settings()
		}
		out = append(out, v)
	}
	return out
}

// StartTranscribe runs speech recognition on a session's audio in the
// background, replacing an earlier transcript. The audio is the session's
// media file (an imported backup) or, for a VOD whose platform can serve
// its stream, the lightest rendition downloaded to a temporary file that is
// deleted afterwards (docs/AI-CUT-ANALYSIS.md 2.3).
func (a *App) StartTranscribe(ctx context.Context, sid string) (JobStatus, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return JobStatus{}, err
	}
	path, stream, err := a.transcribeSource(ctx, s)
	if err != nil {
		return JobStatus{}, err
	}
	if a.defaultASR(ctx) == nil {
		return JobStatus{}, domain.NewUserError("ASR", "쓸 수 있는 음성 인식 엔진이 없습니다. 설정의 음성 인식 상태를 확인하세요.")
	}
	return a.jobs.start(a.ctx, "transcribe", func(ctx context.Context, h job) error {
		h.update(func(js *JobStatus) { js.SessionID, js.Title, js.DurationMs = s.ID, s.Title, s.DurationMs })
		if stream != nil {
			return a.transcribeStream(ctx, h, s, stream)
		}
		return a.transcribe(ctx, h, s.ID, path)
	})
}

// transcribeSource is where a session's audio comes from: a media file
// path, or the platform that streams it. Neither is a user error.
func (a *App) transcribeSource(ctx context.Context, s store.Session) (path string, stream platform.AudioSource, err error) {
	refs, err := a.cfg.Store.MediaRefs(ctx, s.ID)
	if err != nil {
		return "", nil, err
	}
	for _, m := range refs {
		if m.Kind == "audio" || (m.Kind == "video" && path == "") {
			path = m.Path
		}
	}
	if path != "" {
		return path, nil, nil
	}
	if src := a.audioSource(s); src != nil {
		return "", src, nil
	}
	return "", nil, domain.NewUserError("INPUT", "이 작업에는 연결된 음성·영상 파일이 없고, 플랫폼에서 음성을 받을 수도 없어 전사할 수 없습니다. 자막 파일(SRT·VTT)을 넣을 수는 있습니다.")
}

// audioSource is the platform that can stream this VOD's audio, or nil.
func (a *App) audioSource(s store.Session) platform.AudioSource {
	if s.Kind == string(platform.KindLive) || IsDemo(s) {
		return nil
	}
	p, ok := a.cfg.Platforms.Get(s.Platform)
	if !ok {
		return nil
	}
	src, can := p.(platform.AudioSource)
	if !can {
		return nil
	}
	return src
}

// transcribe runs the default engine on the file and stores the result.
func (a *App) transcribe(ctx context.Context, h job, sid, path string) error {
	engine := a.defaultASR(ctx)
	if engine == nil {
		return domain.NewUserError("ASR", "쓸 수 있는 음성 인식 엔진이 없습니다. 설정의 음성 인식 상태를 확인하세요.")
	}
	if _, err := os.Stat(path); err != nil {
		return domain.NewUserError("INPUT", "음성 파일을 찾지 못했습니다. NAS가 연결되어 있는지 확인하세요: "+path)
	}
	h.update(func(s *JobStatus) {
		s.Phase, s.Message = "transcribing", "방송 내용을 인식하는 중입니다 (이 컴퓨터에서만)."
	})
	utts, err := engine.Transcribe(ctx, path, asr.Options{Locale: "ko-KR"}, func(p asr.Progress) {
		h.update(func(s *JobStatus) {
			s.PositionMs, s.Utterances = p.DoneMs, p.Utterances
			if p.Phase == "installing" {
				s.Message = "macOS가 한국어 음성 모델을 내려받는 중입니다."
			}
		})
	})
	if err != nil {
		return err
	}
	if len(utts) == 0 {
		return domain.NewUserError("ASR", "인식된 발언이 없습니다. 음성 파일에 소리가 있는지 확인하세요.")
	}
	model := "system"
	if m, ok := engine.(asr.ModelNamer); ok && m.Model() != "" {
		model = m.Model()
	}
	if err := a.saveTranscript(ctx, sid, store.Transcript{SessionID: sid, Source: "asr", Engine: engine.ID(), Model: model}, utts); err != nil {
		return err
	}
	h.update(func(s *JobStatus) {
		s.Utterances, s.Message = len(utts), fmt.Sprintf("발언 %d건을 인식했습니다.", len(utts))
	})
	return nil
}

// saveTranscript stores a transcript, writes its caption files and refreshes the unified timeline.
func (a *App) saveTranscript(ctx context.Context, sid string, t store.Transcript, utts []domain.Utterance) error {
	t.Coverage = asr.Coverage(utts, 30000)
	saved, err := a.cfg.Store.SaveTranscript(ctx, t, utts)
	if err != nil {
		return err
	}
	if err := export.WriteTranscript(filepath.Join(a.exportDir(sid), "transcript"), saved.ID, utts); err != nil {
		a.logError("write transcript", err)
	}
	a.writeUnified(context.WithoutCancel(ctx), sid)
	return nil
}

// TranscriptView summarises a session's newest transcript.
type TranscriptView struct {
	ID         string       `json:"id"`
	Source     string       `json:"source"`
	Engine     string       `json:"engine"`
	Utterances int          `json:"utterances"`
	Coverage   []domain.Gap `json:"coverage"`
	CreatedAt  time.Time    `json:"createdAt"`
}

// Transcript returns the newest transcript, ok false when there is none.
func (a *App) Transcript(ctx context.Context, sid string) (TranscriptView, bool, error) {
	t, ok, err := a.cfg.Store.LatestTranscript(ctx, sid)
	if err != nil || !ok {
		return TranscriptView{}, false, err
	}
	n, err := a.cfg.Store.UtteranceCount(ctx, t.ID)
	if err != nil {
		return TranscriptView{}, false, err
	}
	return TranscriptView{ID: t.ID, Source: t.Source, Engine: t.Engine, Utterances: n, Coverage: t.Coverage, CreatedAt: t.CreatedAt}, true, nil
}

// Utterances returns the newest transcript's utterances in [from, to).
func (a *App) Utterances(ctx context.Context, sid string, from, to int64, limit int) ([]domain.Utterance, error) {
	t, ok, err := a.cfg.Store.LatestTranscript(ctx, sid)
	if err != nil || !ok {
		return []domain.Utterance{}, err
	}
	if to <= from {
		to = from + 600_000
	}
	utts, err := a.cfg.Store.UtterancesBetween(ctx, t.ID, from, to, max(1, min(limit, 2000)))
	if utts == nil {
		utts = []domain.Utterance{}
	}
	return utts, err
}

// MediaFile is the path of a session's media file of one kind (audio|video), for streaming.
func (a *App) MediaFile(ctx context.Context, sid, kind string) (string, error) {
	refs, err := a.cfg.Store.MediaRefs(ctx, sid)
	if err != nil {
		return "", err
	}
	for _, m := range refs {
		if m.Kind == kind {
			if _, err := os.Stat(m.Path); err != nil {
				return "", domain.NewUserError("MEDIA", "미디어 파일을 찾지 못했습니다. NAS가 연결되어 있는지 확인하세요.")
			}
			return m.Path, nil
		}
	}
	return "", domain.NewUserError("NOT_FOUND", "이 작업에는 연결된 미디어 파일이 없습니다.")
}

// hasVideoFile says whether a session has a video file to play from.
func (a *App) hasVideoFile(sid string) bool { return a.hasMediaKind(sid, "video") }

// hasMediaFile says whether a session has any media file to transcribe.
func (a *App) hasMediaFile(sid string) bool { return a.hasMediaKind(sid, "") }

func (a *App) hasMediaKind(sid, kind string) bool {
	refs, err := a.cfg.Store.MediaRefs(context.Background(), sid)
	if err != nil {
		return false
	}
	for _, m := range refs {
		if kind == "" || m.Kind == kind {
			return true
		}
	}
	return false
}
