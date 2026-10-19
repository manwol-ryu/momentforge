package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// Session backup bundles (docs/AI-CUT-ANALYSIS.md 2.5): one folder per
// session with the video (or where it is), chat, transcript, timeline,
// analysis, comments, cut plans, AI run records and notes, enough to edit
// and re-analyze the broadcast without the app or its database.

// Bundle file names the app manages itself (outside the staged sync).
const (
	bundleManifest  = "manifest.json"
	bundleVideo     = "video.mp4"
	bundleVideoPart = "video.mp4.part"
	bundleVideoInfo = "video.json"
)

// Video states in the manifest and the backups table.
const (
	videoComplete = "complete"
	videoPartial  = "partial"
	videoRef      = "ref"
	videoNone     = "none"
)

// BundleRequest asks for (or refreshes) a session's backup bundle.
type BundleRequest struct {
	SessionID string
	// Video includes the video: downloaded when the platform can, resumed
	// when an earlier download stopped. A NAS backup's video is only
	// referenced, never copied.
	Video bool
}

// BundleView is a session's bundle state for the Files tab.
type BundleView struct {
	Dir         string `json:"dir"`    // where the bundle is (or would be) written
	Exists      bool   `json:"exists"` // a bundle was made
	UpdatedAt   string `json:"updatedAt,omitempty"`
	Files       int    `json:"files"`
	Bytes       int64  `json:"bytes"`
	VideoState  string `json:"videoState"`  // complete | partial | ref | none
	VideoSource string `json:"videoSource"` // download | ref | "" (no video for this session)
	VideoRef    string `json:"videoRef,omitempty"`
	VideoHeight int    `json:"videoHeight"` // the rendition a download asks for
	VideoOn     bool   `json:"videoOn"`     // the setting's default for the video choice
}

// bundleRoot is where bundles go: the settings' folder, else <data>/backups.
func (a *App) bundleRoot() string {
	set := a.Settings()
	if set.BackupDir != "" {
		return set.BackupDir
	}
	return set.BackupDefaultDir
}

// Bundle reports the session's bundle and what a new one would include.
func (a *App) Bundle(ctx context.Context, sid string) (BundleView, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return BundleView{}, err
	}
	set := a.Settings()
	v := BundleView{Dir: filepath.Join(a.bundleRoot(), s.ID), VideoState: videoNone, VideoHeight: set.BackupVideoHeight, VideoOn: set.BackupVideo}
	ref, err := a.videoRef(ctx, sid)
	if err != nil {
		return BundleView{}, err
	}
	switch {
	case ref != nil:
		v.VideoSource, v.VideoRef = "ref", ref.Path
	case a.videoSource(s) != nil:
		v.VideoSource = "download"
	}
	b, ok, err := a.cfg.Store.Backup(ctx, sid)
	if err != nil {
		return BundleView{}, err
	}
	if ok {
		v.Dir, v.Exists, v.UpdatedAt, v.Files, v.Bytes, v.VideoState =
			b.Path, true, b.UpdatedAt.UTC().Format(time.RFC3339), len(b.Files), b.Bytes, b.VideoState
	}
	return v, nil
}

func (a *App) videoRef(ctx context.Context, sid string) (*store.MediaRef, error) {
	refs, err := a.cfg.Store.MediaRefs(ctx, sid)
	if err != nil {
		return nil, err
	}
	for _, m := range refs {
		if m.Kind == "video" {
			return &m, nil
		}
	}
	return nil, nil
}

func (a *App) videoSource(s store.Session) platform.VideoSource {
	if p, ok := a.cfg.Platforms.Get(s.Platform); ok {
		if v, ok := p.(platform.VideoSource); ok {
			return v
		}
	}
	return nil
}

// StartBundle writes the session's bundle in the background: the text
// files first (only the changed ones are replaced), then the video.
func (a *App) StartBundle(ctx context.Context, req BundleRequest) (JobStatus, error) {
	s, err := a.cfg.Store.Session(ctx, req.SessionID)
	if err != nil {
		return JobStatus{}, err
	}
	if s.Kind != string(platform.KindVOD) {
		return JobStatus{}, domain.NewUserError("BUNDLE_UNSUPPORTED", "생방송 기록은 아직 백업 묶음을 만들 수 없습니다. 방송이 끝난 뒤 다시보기로 수집해 주세요.")
	}
	if s.State == store.StateCollecting {
		return JobStatus{}, domain.NewUserError("BUSY", "채팅 수집이 끝난 뒤 백업 묶음을 만들어 주세요.")
	}
	return a.jobs.start(a.ctx, "bundle", func(ctx context.Context, h job) error {
		h.update(func(js *JobStatus) {
			js.SessionID, js.Title, js.DurationMs = s.ID, s.Title, s.DurationMs
			js.Phase, js.Message = "bundling", "백업 묶음을 만드는 중입니다."
		})
		return a.writeBundle(ctx, h, s, req.Video)
	})
}

// bundleState is what one bundle job knows as it goes.
type bundleState struct {
	s        store.Session
	dir      string
	created  time.Time
	files    []export.BundleFile // staged files now in the bundle
	index    export.BundleIndex
	video    export.BundleVideo
	axis     export.BundleTime
	videoDoc *export.BundleVideoFile
	listed   int // files the manifest lists (the staged ones and the video's)
}

func (a *App) writeBundle(ctx context.Context, h job, s store.Session, withVideo bool) error {
	st := a.cfg.Store
	dir := filepath.Join(a.bundleRoot(), s.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "백업 묶음 폴더를 만들 수 없습니다: "+dir+". 설정의 백업 묶음 폴더와 쓰기 권한을 확인하세요.", err)
	}
	prev, had, err := st.Backup(ctx, s.ID)
	if err != nil {
		return err
	}
	b := &bundleState{s: s, dir: dir, created: time.Now().UTC()}
	var previous []export.BundleFile
	if had && prev.Path == dir {
		b.created = prev.CreatedAt
		for _, f := range prev.Files {
			previous = append(previous, export.BundleFile(f))
		}
	}

	staging, err := os.MkdirTemp(dir, ".staging-")
	if err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "백업 묶음 폴더에 쓸 수 없습니다: "+dir+". 쓰기 권한을 확인하세요.", err)
	}
	defer os.RemoveAll(staging)
	if err := a.stageBundle(ctx, b, staging); err != nil {
		return err
	}
	video := func(rel string) bool { return rel == bundleVideo || rel == bundleVideoInfo }
	files, changed, err := export.SyncBundle(staging, dir, previous, video)
	if err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "백업 묶음 파일을 쓰지 못했습니다: "+dir+".", err)
	}
	b.files = files
	if err := a.decideVideo(ctx, b, withVideo); err != nil {
		return err
	}
	if err := a.saveBundle(ctx, b); err != nil {
		return err
	}
	h.update(func(js *JobStatus) {
		js.Message = fmt.Sprintf("백업 묶음에서 영상 외 파일 %d개를 확인했습니다 (바뀐 파일 %d개).", len(files), changed)
	})
	if b.video.State != videoPartial {
		h.update(func(js *JobStatus) { js.Message = bundleDoneMessage(b, changed) })
		return nil
	}
	if err := a.downloadBundleVideo(ctx, h, b); err != nil {
		ue, ok := domain.AsUserError(err)
		if errors.Is(err, context.Canceled) || !ok || !permanentVideoError(ue.Code) {
			// The text files are written; the video resumes where it stopped.
			if save := a.saveBundle(context.WithoutCancel(ctx), b); save != nil {
				a.logError("save bundle", save)
			}
			return err
		}
		// The platform will not give this video; the bundle stays without it.
		os.Remove(filepath.Join(dir, bundleVideoPart))
		os.Remove(filepath.Join(dir, bundleVideoInfo))
		b.video, b.videoDoc = export.BundleVideo{State: videoNone, Reason: ue.Message}, nil
		b.axis = timeAxis(b, nil)
		if err := a.saveBundle(ctx, b); err != nil {
			return err
		}
		h.update(func(js *JobStatus) {
			js.Message, js.Warning = "백업 묶음을 만들었습니다. 영상은 넣지 못했습니다.", ue.Message
		})
		return nil
	}
	if err := a.saveBundle(ctx, b); err != nil {
		return err
	}
	changed += 2 // video.mp4 and video.json
	h.update(func(js *JobStatus) { js.Message = bundleDoneMessage(b, changed) })
	return nil
}

func permanentVideoError(code string) bool {
	switch code {
	case "PLAYBACK_RESTRICTED", "PLAYBACK_UNAVAILABLE", "MEDIA":
		return true
	}
	return false
}

func bundleDoneMessage(b *bundleState, changed int) string {
	msg := fmt.Sprintf("백업 묶음을 만들었습니다: %s (파일 %d개, 바뀐 파일 %d개).", b.dir, b.listed, changed)
	switch b.video.State {
	case videoComplete:
		msg += " 영상 포함."
	case videoRef:
		msg += " 영상은 원래 위치를 적었습니다."
	}
	return msg
}

// stageBundle writes every text part of the bundle into staging.
func (a *App) stageBundle(ctx context.Context, b *bundleState, staging string) error {
	st := a.cfg.Store
	s := b.s
	write := func(what string, err error) error {
		if err != nil {
			return fmt.Errorf("bundle %s: %w", what, err)
		}
		return nil
	}

	// Chat: chat.jsonl, chat.txt and the collection manifest as chat-manifest.json.
	chatDir := filepath.Join(staging, ".chat")
	if err := write("chat", a.writeChatTo(ctx, s.ID, chatDir)); err != nil {
		return err
	}
	for from, to := range map[string]string{"manifest.json": "chat-manifest.json", "chat.txt": "chat.txt", "chat.jsonl": "chat.jsonl"} {
		if err := os.Rename(filepath.Join(chatDir, from), filepath.Join(staging, to)); err != nil {
			return write("chat", err)
		}
	}
	os.Remove(chatDir)
	b.index.Chat = true

	// Transcript: the newest one as transcript.json, .srt and .vtt.
	if t, ok, err := st.LatestTranscript(ctx, s.ID); err != nil {
		return err
	} else if ok {
		utts, err := st.UtterancesBetween(ctx, t.ID, 0, 1<<50, 1<<30)
		if err != nil {
			return err
		}
		tdir := filepath.Join(staging, ".transcript")
		if err := write("transcript", export.WriteTranscript(tdir, t.ID, utts)); err != nil {
			return err
		}
		for _, ext := range []string{".json", ".srt", ".vtt"} {
			if err := os.Rename(filepath.Join(tdir, t.ID+ext), filepath.Join(staging, "transcript"+ext)); err != nil {
				return write("transcript", err)
			}
		}
		os.Remove(tdir)
		b.index.TranscriptID, b.index.Utterances = t.ID, len(utts)
	}

	// Unified timeline.
	if f, ok, err := a.unifiedFile(ctx, s.ID); err != nil {
		return err
	} else if ok {
		if err := write("timeline", export.WriteTimeline(filepath.Join(staging, "timeline"), f)); err != nil {
			return err
		}
		b.index.Timeline = true
	}

	// The latest rule analysis.
	if s.LatestAnalysisID != "" {
		an, err := st.Analysis(ctx, s.LatestAnalysisID)
		if err != nil {
			return err
		}
		events, err := st.Chats(ctx, s.ID, store.AllSeq, false)
		if err != nil {
			return err
		}
		gaps, err := st.Gaps(ctx, s.ID)
		if err != nil {
			return err
		}
		m := export.AnalysisManifest{AnalysisID: an.ID, SourceSessionID: s.ID, Params: an.ParamsJSON, DataSHA256: an.DataSHA256,
			EventCount: an.EventCount, MaxSeq: an.MaxSeq, RulesVersion: an.RulesVersion, DurationMs: an.DurationMs, SourceState: an.SourceState,
			Completeness: export.Completeness(an.SourceState == store.StateFinished, len(gaps)), Gaps: gaps}
		if err := write("analysis", export.WriteAnalysis(filepath.Join(staging, "analysis", an.ID), m, s.Title, an.Candidates, an.Windows, events)); err != nil {
			return err
		}
		b.index.AnalysisID = an.ID
	}

	// The latest comment capture.
	if c, ok, err := st.LatestCommentCapture(ctx, s.ID); err != nil {
		return err
	} else if ok {
		comments, err := st.CaptureComments(ctx, c.ID)
		if err != nil {
			return err
		}
		recs, err := st.ViewerRecommendations(ctx, c.ID)
		if err != nil {
			return err
		}
		if err := write("comments", writeCommentsTo(filepath.Join(staging, "comments"), c, s, comments, recs)); err != nil {
			return err
		}
		b.index.CommentsID = c.ID
	}

	// Every cut plan version, the confirmed cuts, and notes.md.
	plans, err := st.CutPlans(ctx, s.ID, true)
	if err != nil {
		return err
	}
	for _, rec := range plans {
		p, ok := decodePlan(rec)
		if !ok {
			continue
		}
		if err := write("cut plan", writeCutPlanTo(filepath.Join(staging, "cuts", rec.ID), s.ID, p)); err != nil {
			return err
		}
		b.index.CutPlans++
		if rec.Confirmed {
			b.index.Confirmed++
		}
	}
	if err := write("confirmed cuts", a.writeConfirmedTo(ctx, s.ID, filepath.Join(staging, "cuts"))); err != nil {
		return err
	}
	details, err := a.CutPlanDetails(ctx, s.ID, true)
	if err != nil {
		return err
	}
	if err := write("notes", export.WriteText(staging, "notes.md", export.NotesMarkdown(s.Title, bundleNotes(details)))); err != nil {
		return err
	}

	// AI runs: what was sent, with which model, and what it cost (keys are never stored).
	runs, err := st.AIRuns(ctx, s.ID)
	if err != nil {
		return err
	}
	for _, r := range runs {
		full, err := st.AIRun(ctx, r.ID)
		if err != nil {
			return err
		}
		if err := write("AI run", export.WriteJSON(staging, "ai-runs/"+full.ID+".json", bundleRun(full))); err != nil {
			return err
		}
		b.index.AIRuns++
	}
	return nil
}

// bundleNotes lists the plans for notes.md: each version's confirmed spans and notes.
func bundleNotes(details []CutDetail) []export.BundleNote {
	out := make([]export.BundleNote, 0, len(details))
	for _, d := range details {
		n := export.BundleNote{PlanID: d.PlanID, Version: d.Version, State: d.State, Target: d.Target, Confirmed: d.Confirmed,
			Boundary: d.ConfirmedBoundary}
		want := d.ConfirmedBoundary
		if want == "" {
			want = d.Chosen
		}
		for _, bd := range d.Boundaries {
			if bd.Name != want {
				continue
			}
			for i, sp := range bd.Spans {
				n.Spans = append(n.Spans, export.CutSpan{Order: i + 1, StartMs: sp.StartMs, EndMs: sp.EndMs, Role: sp.Role})
			}
		}
		for _, note := range d.Notes {
			n.Notes = append(n.Notes, note.Text)
		}
		out = append(out, n)
	}
	return out
}

// bundleAIRun is ai-runs/<id>.json.
type bundleAIRun struct {
	ID                string          `json:"id"`
	Kind              string          `json:"kind"`
	Provider          string          `json:"provider"`
	Model             string          `json:"model"`
	Delivery          string          `json:"delivery"`
	State             string          `json:"state"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
	RequestCount      int             `json:"request_count"`
	CompletedRequests int             `json:"completed_requests"`
	InputTokens       int             `json:"input_tokens"`
	CacheReadTokens   int             `json:"cache_read_tokens"`
	CacheWriteTokens  int             `json:"cache_write_tokens"`
	OutputTokens      int             `json:"output_tokens"`
	CostUSD           *float64        `json:"cost_usd"`
	CostCapUSD        *float64        `json:"cost_cap_usd"`
	ErrorCode         string          `json:"error_code,omitempty"`
	ErrorMessage      string          `json:"error_message,omitempty"`
	Plan              json.RawMessage `json:"plan"`     // the instructions and the pseudonymous chat and utterances sent
	SentLog           json.RawMessage `json:"sent_log"` // cut runs: what each step sent
	Recommendations   json.RawMessage `json:"recommendations"`
	Usage             json.RawMessage `json:"usage"`
}

func bundleRun(r store.AIRun) bundleAIRun {
	raw := func(b []byte) json.RawMessage {
		if len(b) == 0 || !json.Valid(b) {
			return json.RawMessage("null")
		}
		return b
	}
	return bundleAIRun{ID: r.ID, Kind: r.Kind, Provider: r.Provider, Model: r.Model, Delivery: r.Delivery, State: r.State,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, RequestCount: r.RequestCount, CompletedRequests: r.CompletedRequests,
		InputTokens: r.InputTokens, CacheReadTokens: r.CacheReadTokens, CacheWriteTokens: r.CacheWriteTokens, OutputTokens: r.OutputTokens,
		CostUSD: r.CostUSD, CostCapUSD: r.CostCapUSD, ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage,
		Plan: raw(r.PlanJSON), SentLog: raw(r.SentLogJSON), Recommendations: raw(r.RecommendationsJSON), Usage: raw(r.UsageJSON)}
}

// decideVideo sets what the bundle holds for the video: a reference to a
// NAS backup's file, a finished or partial download, or nothing.
func (a *App) decideVideo(ctx context.Context, b *bundleState, withVideo bool) error {
	ref, err := a.videoRef(ctx, b.s.ID)
	if err != nil {
		return err
	}
	doc := readVideoDoc(b.dir)
	_, statErr := os.Stat(filepath.Join(b.dir, bundleVideo))
	haveFile := statErr == nil && doc != nil && doc.State == videoComplete
	switch {
	case ref != nil:
		b.video = export.BundleVideo{State: videoRef, Ref: &export.BundleVideoRef{Path: ref.Path, Bytes: ref.Bytes, Modified: ref.Modified.UTC()}}
	case haveFile:
		// A finished download stays, with or without the video choice.
		b.video, b.videoDoc = export.BundleVideo{State: videoComplete, File: bundleVideo}, doc
	case !withVideo:
		b.video = export.BundleVideo{State: videoNone, Reason: "영상 없이 만들었습니다."}
	case a.videoSource(b.s) == nil:
		b.video = export.BundleVideo{State: videoNone, Reason: "이 작업은 받을 수 있는 영상이 없습니다."}
	default:
		b.video = export.BundleVideo{State: videoPartial, File: bundleVideo}
		if doc != nil && doc.State == videoPartial {
			b.videoDoc = doc
		}
	}
	b.axis = timeAxis(b, ref)
	return nil
}

func timeAxis(b *bundleState, ref *store.MediaRef) export.BundleTime {
	t := export.BundleTime{Basis: "video_position"} // saveBundle adds the gaps
	switch {
	case ref != nil:
		t.Method = "NAS 백업 영상: 영상·음성 트랙이 0초에서 시작하고 채팅 시각과 같은 시간축 (docs/AI-CUT-ANALYSIS.md 2.5a)"
	case b.video.State == videoComplete || b.video.State == videoPartial:
		t.Method = "HLS 첫 조각의 시작 시각(video.json의 original_first_pts_ms)을 0초로 옮겨 받음"
	default:
		t.Method = "영상 없음: 시각은 플랫폼 다시보기의 재생 위치"
	}
	return t
}

func readVideoDoc(dir string) *export.BundleVideoFile {
	raw, err := os.ReadFile(filepath.Join(dir, bundleVideoInfo))
	if err != nil {
		return nil
	}
	var d export.BundleVideoFile
	if json.Unmarshal(raw, &d) != nil {
		return nil
	}
	return &d
}

// saveBundle writes manifest.json (and README.txt) for what is in the
// bundle now and records it in the database.
func (a *App) saveBundle(ctx context.Context, b *bundleState) error {
	s, err := a.cfg.Store.Session(ctx, b.s.ID)
	if err != nil {
		return err
	}
	info := a.info(s)
	gaps, err := a.cfg.Store.Gaps(ctx, s.ID)
	if err != nil {
		return err
	}
	files := append([]export.BundleFile(nil), b.files...)
	if b.videoDoc != nil {
		if size, sum, err := export.HashFile(filepath.Join(b.dir, bundleVideoInfo)); err == nil {
			files = append(files, export.BundleFile{Path: bundleVideoInfo, Bytes: size, SHA256: sum})
		}
		if b.video.State == videoComplete {
			files = append(files, export.BundleFile{Path: bundleVideo, Bytes: b.videoDoc.Bytes, SHA256: b.videoDoc.SHA256})
		}
	}
	m := export.BundleManifest{Format: export.BundleFormat, Version: export.BundleVersion, AppVersion: a.cfg.Version,
		CreatedAt: b.created, UpdatedAt: time.Now().UTC(),
		Session: export.BundleSession{ID: s.ID, Title: s.Title, Platform: info.PlatformName, Kind: s.Kind, VideoID: s.TargetID,
			DurationMs: s.DurationMs, State: s.State, Completeness: export.Completeness(s.State == store.StateFinished, len(gaps)),
			ChatCount: s.ChatCount, WatchURL: info.WatchURL},
		TimeAxis: b.axis, Video: b.video, Contents: b.index, Files: files}
	m.TimeAxis.Gaps = export.GapsOf(gaps)
	if err := export.WriteJSON(b.dir, bundleManifest, m); err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "백업 묶음의 manifest.json을 쓰지 못했습니다: "+b.dir+".", err)
	}
	if err := export.WriteText(b.dir, "README.txt", export.BundleReadme(m)); err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "백업 묶음의 README.txt를 쓰지 못했습니다: "+b.dir+".", err)
	}
	var total int64
	rec := store.Backup{SessionID: s.ID, Path: b.dir, ManifestVersion: export.BundleVersion, VideoState: b.video.State}
	for _, f := range files {
		total += f.Bytes
		rec.Files = append(rec.Files, store.BackupFile(f))
	}
	rec.Bytes = total
	b.listed = len(files)
	return a.cfg.Store.SaveBackup(ctx, rec)
}

// downloadBundleVideo downloads (or resumes) video.mp4, saving video.json
// as it goes so a stopped download resumes where it was.
func (a *App) downloadBundleVideo(ctx context.Context, h job, b *bundleState) error {
	src := a.videoSource(b.s)
	part := filepath.Join(b.dir, bundleVideoPart)
	height := a.Settings().BackupVideoHeight
	req := platform.VideoRequest{MaxHeight: height}
	doc := b.videoDoc
	if doc != nil && doc.State == videoPartial {
		if _, err := os.Stat(part); err == nil {
			req.Resume = resumeOf(doc)
		}
	}
	if doc == nil || req.Resume == nil {
		doc = &export.BundleVideoFile{State: videoPartial, StartedAt: time.Now().UTC()}
	}
	b.videoDoc = doc
	doing := "영상을 받는 중입니다"
	if req.Resume != nil {
		doing = "멈춘 곳부터 영상을 이어받는 중입니다"
	}
	h.update(func(js *JobStatus) {
		js.Phase, js.PositionMs = "video", 0
		js.Message = fmt.Sprintf("%s (%dp 이하, 재인코딩 없음).", doing, height)
	})
	t := platform.Target{Platform: b.s.Platform, Kind: platform.Kind(b.s.Kind), ID: b.s.TargetID}
	var saved time.Time
	vf, err := src.VODVideo(ctx, t, part, req, func(p platform.VideoProgress) {
		fillVideoDoc(doc, p.Rendition, p.State)
		if time.Since(saved) > 3*time.Second {
			saved = time.Now()
			if err := export.WriteJSON(b.dir, bundleVideoInfo, doc); err != nil {
				a.logError("write video.json", err)
			}
		}
		h.update(func(js *JobStatus) {
			js.PositionMs, js.Pages, js.Bytes = p.State.DoneMs, p.State.Segments, p.State.Bytes
			js.Message = fmt.Sprintf("%s (%dp, %d/%d 조각, %.1f GB).", doing, p.Rendition.Height,
				p.State.Segments, p.State.TotalSegments, float64(p.State.Bytes)/1e9)
		})
	})
	if err != nil {
		if doc.Segments > 0 {
			if werr := export.WriteJSON(b.dir, bundleVideoInfo, doc); werr != nil {
				a.logError("write video.json", werr)
			}
		}
		return err
	}
	fillVideoDoc(doc, vf.Rendition, vf.State)
	doc.Container, doc.DurationMs, doc.FirstPTSMs, doc.Rebased = vf.Container, vf.DurationMs, vf.FirstPTSMs, true
	h.update(func(js *JobStatus) { js.Message = "받은 영상을 확인하는 중입니다." })
	size, sum, err := export.HashFile(part)
	if err != nil {
		return err
	}
	if err := os.Rename(part, filepath.Join(b.dir, bundleVideo)); err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "받은 영상을 video.mp4로 옮기지 못했습니다: "+b.dir+".", err)
	}
	done := time.Now().UTC()
	doc.State, doc.Bytes, doc.SHA256, doc.FinishedAt = videoComplete, size, sum, &done
	if err := export.WriteJSON(b.dir, bundleVideoInfo, doc); err != nil {
		return domain.WrapUserError("BUNDLE_WRITE", "백업 묶음의 video.json을 쓰지 못했습니다: "+b.dir+".", err)
	}
	b.video = export.BundleVideo{State: videoComplete, File: bundleVideo}
	return nil
}

func fillVideoDoc(doc *export.BundleVideoFile, r platform.VideoRendition, st platform.VideoResume) {
	doc.Width, doc.Height, doc.Bandwidth, doc.Codecs, doc.FrameRate = r.Width, r.Height, r.Bandwidth, r.Codecs, r.FrameRate
	doc.Segments, doc.TotalSegments, doc.Bytes, doc.DoneMs = st.Segments, st.TotalSegments, st.Bytes, st.DoneMs
	doc.Missing = export.GapsOf(st.Missing)
	doc.Shift = doc.Shift[:0]
	for _, s := range st.Shift {
		doc.Shift = append(doc.Shift, export.TrackShift(s))
	}
	if len(doc.Shift) > 0 {
		doc.FirstPTSMs, doc.Rebased = shiftMs(doc.Shift), true
	}
}

func shiftMs(shift []export.TrackShift) int64 {
	var out int64 = -1
	for _, s := range shift {
		if s.Timescale == 0 {
			continue
		}
		if ms := int64(s.Ticks * 1000 / uint64(s.Timescale)); out < 0 || ms < out {
			out = ms
		}
	}
	return max(out, 0)
}

func resumeOf(doc *export.BundleVideoFile) *platform.VideoResume {
	if doc.Segments == 0 || len(doc.Shift) == 0 {
		return nil
	}
	r := &platform.VideoResume{Height: doc.Height, TotalSegments: doc.TotalSegments, Segments: doc.Segments, Bytes: doc.Bytes,
		DoneMs: doc.DoneMs, Missing: append([]domain.Gap(nil), doc.Missing...)}
	for _, s := range doc.Shift {
		r.Shift = append(r.Shift, platform.TrackShift(s))
	}
	return r
}
