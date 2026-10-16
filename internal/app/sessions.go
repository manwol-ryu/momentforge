package app

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// SessionInfo is a session as the screens see it (no salt, no raw pages).
type SessionInfo struct {
	ID                 string
	Platform           string
	PlatformName       string
	Kind               string
	TargetID           string
	Title              string
	DurationMs         int64
	ReportedDurationMs int64
	State              string // collecting | paused | finished
	Cursor             int64
	Pages              int
	ChatCount          int
	ErrorCode          string
	ErrorMessage       string
	LatestAnalysisID   string
	CreatedAt          time.Time
	UpdatedAt          time.Time
	IsDemo             bool
	CanResume          bool // paused and the platform can collect it
	CanPlay            bool // the platform can stream it in the app
	CanReadComments    bool // the platform can read the video's comments
	WatchURL           string
}

func (a *App) info(s store.Session) SessionInfo {
	out := SessionInfo{
		ID: s.ID, Platform: s.Platform, Kind: s.Kind, TargetID: s.TargetID, Title: s.Title,
		DurationMs: s.DurationMs, ReportedDurationMs: s.ReportedDurationMs, State: s.State, Cursor: s.Cursor,
		Pages: s.Pages, ChatCount: s.ChatCount, ErrorCode: s.ErrorCode, ErrorMessage: s.ErrorMessage,
		LatestAnalysisID: s.LatestAnalysisID, CreatedAt: s.CreatedAt, UpdatedAt: s.UpdatedAt, IsDemo: IsDemo(s),
	}
	if p, ok := a.cfg.Platforms.Get(s.Platform); ok {
		out.PlatformName = p.Name()
		_, vod := p.(platform.VODSource)
		_, live := p.(platform.LiveSource)
		_, play := p.(platform.PlaybackSource)
		if s.Kind == string(platform.KindLive) {
			out.CanResume = live && (s.State == store.LiveStopped || s.State == store.LiveError || s.State == store.LiveIncomplete)
		} else {
			_, comments := p.(platform.CommentSource)
			out.CanResume = vod && (s.State == store.StatePaused || s.State == store.StateCommentsOnly)
			out.CanPlay, out.CanReadComments = play, comments
		}
		if link := a.linker(s); link != nil {
			out.WatchURL = link(0)
		}
	} else if out.IsDemo {
		out.PlatformName = "샘플"
	}
	return out
}

// Sessions lists visible sessions, newest first.
func (a *App) Sessions(ctx context.Context) ([]SessionInfo, error) {
	list, err := a.cfg.Store.Sessions(ctx, false)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, len(list))
	for i, s := range list {
		out[i] = a.info(s)
	}
	return out, nil
}

// HideSessions hides every session from the list; nothing is deleted.
func (a *App) HideSessions(ctx context.Context) (int, error) {
	return a.cfg.Store.SetAllHidden(ctx, true)
}

// RestoreSessions shows every hidden session again.
func (a *App) RestoreSessions(ctx context.Context) (int, error) {
	return a.cfg.Store.SetAllHidden(ctx, false)
}

// Session returns one session.
func (a *App) Session(ctx context.Context, id string) (SessionInfo, error) {
	s, err := a.cfg.Store.Session(ctx, id)
	if err != nil {
		return SessionInfo{}, err
	}
	return a.info(s), nil
}

// ContextChat is a chat in a candidate's clip range.
type ContextChat struct {
	domain.ChatEvent
	IsEvidence bool
}

// CandidateChats pages through the chat of a candidate's clip range, as
// captured when the analysis ran.
func (a *App) CandidateChats(ctx context.Context, analysisID, candidateID string, offset, limit int) ([]ContextChat, int, error) {
	if offset < 0 || limit < 1 || limit > 500 {
		return nil, 0, domain.NewUserError("INPUT", "채팅 조회 범위가 올바르지 않습니다.")
	}
	an, err := a.cfg.Store.Analysis(ctx, analysisID)
	if err != nil {
		return nil, 0, err
	}
	var cand *domain.Candidate
	for i := range an.Candidates {
		if an.Candidates[i].ID == candidateID {
			cand = &an.Candidates[i]
		}
	}
	if cand == nil {
		return nil, 0, domain.NewUserError("NOT_FOUND", "편집 후보를 찾을 수 없습니다.")
	}
	return a.contextChats(ctx, an.SessionID, *cand, an.MaxSeq, offset, limit)
}

func (a *App) contextChats(ctx context.Context, sid string, cand domain.Candidate, maxSeq int64, offset, limit int) ([]ContextChat, int, error) {
	chats, total, err := a.cfg.Store.ChatsBetween(ctx, sid, cand.ClipStartMs, cand.ClipEndMs, maxSeq, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	evidence := make(map[string]bool, len(cand.EvidenceIDs))
	for _, id := range cand.EvidenceIDs {
		evidence[id] = true
	}
	out := make([]ContextChat, len(chats))
	for i, c := range chats {
		out[i] = ContextChat{ChatEvent: c, IsEvidence: evidence[c.ID]}
	}
	return out, total, nil
}

// PlayheadChats is the chat around a playback position.
type PlayheadChats struct {
	Chats     []domain.ChatEvent
	StartMs   int64
	EndMs     int64
	AtMs      int64
	Truncated bool
}

const (
	playheadSpanMs  = 30000
	playheadPerSide = 150
)

// ChatsAt returns up to 150 messages on each side of atMs within ±30 s,
// limited to the analysis snapshot when analysisID is given.
func (a *App) ChatsAt(ctx context.Context, sid, analysisID string, atMs int64) (*PlayheadChats, error) {
	if atMs < 0 || atMs > 31*24*3600*1000+1000 {
		return nil, domain.NewUserError("INPUT", "재생 시간이 올바르지 않습니다.")
	}
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return nil, err
	}
	end, maxSeq := s.DurationMs, int64(store.AllSeq)
	if analysisID != "" {
		an, err := a.cfg.Store.Analysis(ctx, analysisID)
		if err != nil {
			return nil, err
		}
		if an.SessionID != sid {
			return nil, domain.NewUserError("INPUT", "분석과 수집 작업이 맞지 않습니다.")
		}
		end, maxSeq = an.DurationMs, an.MaxSeq
	}
	if atMs >= end {
		return &PlayheadChats{Chats: []domain.ChatEvent{}, StartMs: end, EndMs: end, AtMs: atMs}, nil
	}
	chats, truncated, err := a.cfg.Store.ChatsAround(ctx, sid, atMs, playheadSpanMs, end, maxSeq, playheadPerSide)
	if err != nil {
		return nil, err
	}
	return &PlayheadChats{Chats: chats, StartMs: max(0, atMs-playheadSpanMs), EndMs: min(end, atMs+playheadSpanMs),
		AtMs: atMs, Truncated: truncated}, nil
}

// PlaybackInfo is a public stream the browser can play.
type PlaybackInfo struct {
	URL  string
	Type string // "hls"
}

// Playback resolves a public stream for a session's video. The URL is
// signed and short-lived; it is returned, never stored.
func (a *App) Playback(ctx context.Context, sid string) (PlaybackInfo, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return PlaybackInfo{}, err
	}
	p, ok := a.cfg.Platforms.Get(s.Platform)
	src, can := p.(platform.PlaybackSource)
	if !ok || !can {
		if IsDemo(s) {
			return PlaybackInfo{}, domain.NewUserError("DEMO", "샘플 채팅에는 연결된 영상이 없습니다.")
		}
		return PlaybackInfo{}, domain.NewUserError("PLAYBACK_UNAVAILABLE", "이 작업은 앱 안에서 재생할 수 없습니다.")
	}
	pb, err := src.Playback(ctx, platform.Target{Platform: s.Platform, Kind: platform.Kind(s.Kind), ID: s.TargetID})
	if err != nil {
		return PlaybackInfo{}, err
	}
	return PlaybackInfo{URL: pb.URL, Type: pb.Type}, nil
}

// linker returns a function that links to the platform player, or nil.
func (a *App) linker(s store.Session) func(offsetMs int64) string {
	p, ok := a.cfg.Platforms.Get(s.Platform)
	link, can := p.(LinkSource)
	if !ok || !can {
		return nil
	}
	t := platform.Target{Platform: s.Platform, Kind: platform.Kind(s.Kind), ID: s.TargetID}
	return func(ms int64) string { return link.WatchURL(t, ms) }
}

// LinkSource is platform.LinkSource (aliased for readability in this package).
type LinkSource = platform.LinkSource

// ExportFile is a downloadable file of a session.
type ExportFile struct {
	Path string // relative to the session's export folder, "/" separated
	Size int64
}

var exportExt = map[string]bool{".txt": true, ".json": true, ".jsonl": true, ".csv": true}

// Files lists a session's export files.
func (a *App) Files(ctx context.Context, sid string) ([]ExportFile, error) {
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, err
	}
	root := a.exportDir(sid)
	var out []ExportFile
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return fs.SkipAll
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") || !exportExt[filepath.Ext(p)] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out = append(out, ExportFile{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, err
}

// OpenFile opens one export file for download. rel must stay inside the
// session's export folder and have an allowed extension.
func (a *App) OpenFile(ctx context.Context, sid, rel string) (*os.File, error) {
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, err
	}
	notFound := domain.NewUserError("NOT_FOUND", "파일을 찾을 수 없습니다.")
	root, err := filepath.Abs(a.exportDir(sid))
	if err != nil {
		return nil, err
	}
	full, err := filepath.Abs(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil || !strings.HasPrefix(full, root+string(filepath.Separator)) || !exportExt[filepath.Ext(full)] {
		return nil, notFound
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, notFound
	}
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, notFound
	}
	return f, nil
}
