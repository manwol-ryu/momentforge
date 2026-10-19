package export

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// A session backup bundle (docs/AI-CUT-ANALYSIS.md 2.5) is a folder that
// holds everything needed to edit and re-analyze one broadcast without the
// app or its database: the video (or where it is), chat, transcript,
// timeline, rule analysis, comments, cut plans, AI run records and notes.
// Every file is readable as is and carries no original IDs, nicknames, API
// keys or salts.

// BundleFormat and BundleVersion identify the manifest's schema; an import
// reads them first.
const (
	BundleFormat  = "momentforge-backup"
	BundleVersion = 1
)

// BundleFile is one file in a bundle with its size and SHA-256.
type BundleFile struct {
	Path   string `json:"path"` // relative, forward slashes
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// BundleManifest is manifest.json.
type BundleManifest struct {
	Format     string        `json:"format"`
	Version    int           `json:"version"`
	AppVersion string        `json:"app_version"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
	Session    BundleSession `json:"session"`
	TimeAxis   BundleTime    `json:"time_axis"`
	Video      BundleVideo   `json:"video"`
	Contents   BundleIndex   `json:"contents"`
	Files      []BundleFile  `json:"files"`
}

// BundleSession is what the bundle is a copy of.
type BundleSession struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Platform     string `json:"platform"`
	Kind         string `json:"kind"`
	VideoID      string `json:"video_id"`
	DurationMs   int64  `json:"duration_ms"`
	State        string `json:"state"`
	Completeness string `json:"completeness"`
	ChatCount    int    `json:"chat_count"`
	WatchURL     string `json:"watch_url,omitempty"`
}

// BundleTime says how the files' times relate to the video file. Chat,
// utterance and cut times are milliseconds from the start of the video;
// video_offset_ms is what the video file's 0 s is on that axis.
type BundleTime struct {
	Basis         string `json:"basis"` // video_position
	VideoOffsetMs int64  `json:"video_offset_ms"`
	Method        string `json:"method"`
	Gaps          Issues `json:"gaps"` // spans whose chat was not collected
}

// BundleVideo describes video.mp4, or where the video is when it was not copied.
type BundleVideo struct {
	State string `json:"state"` // complete | partial | ref | none
	File  string `json:"file,omitempty"`
	// Ref is a video file that stays where it is (a NAS backup); editnest cuts it from there.
	Ref    *BundleVideoRef `json:"ref,omitempty"`
	Reason string          `json:"reason,omitempty"` // why there is no video
}

// BundleVideoRef is a video file outside the bundle.
type BundleVideoRef struct {
	Path     string    `json:"path"`
	Bytes    int64     `json:"bytes"`
	Modified time.Time `json:"modified"`
}

// BundleIndex says which parts are in the bundle.
type BundleIndex struct {
	Chat         bool   `json:"chat"`
	TranscriptID string `json:"transcript_id,omitempty"`
	Utterances   int    `json:"utterances"`
	Observations bool   `json:"observations"` // sound and on-screen text (phase 6-E)
	Timeline     bool   `json:"timeline"`
	AnalysisID   string `json:"analysis_id,omitempty"`
	CommentsID   string `json:"comments_id,omitempty"`
	CutPlans     int    `json:"cut_plans"`
	Confirmed    int    `json:"confirmed_cuts"`
	AIRuns       int    `json:"ai_runs"`
}

// BundleVideoFile is video.json: how video.mp4 was made and whether it is whole.
type BundleVideoFile struct {
	State         string       `json:"state"` // complete | partial
	Container     string       `json:"container"`
	Width         int          `json:"width"`
	Height        int          `json:"height"`
	Bandwidth     int64        `json:"bandwidth"`
	Codecs        string       `json:"codecs"`
	FrameRate     float64      `json:"frame_rate"`
	DurationMs    int64        `json:"duration_ms"`
	Segments      int          `json:"segments"`
	TotalSegments int          `json:"total_segments"`
	Bytes         int64        `json:"bytes"`
	Fragments     uint32       `json:"fragments"`        // movie fragments written, renumbered from 1
	SHA256        string       `json:"sha256,omitempty"` // of the complete file
	FirstPTSMs    int64        `json:"original_first_pts_ms"`
	Rebased       bool         `json:"rebased"`
	Missing       Issues       `json:"missing"` // spans whose segments the CDN no longer served
	StartedAt     time.Time    `json:"started_at"`
	FinishedAt    *time.Time   `json:"finished_at,omitempty"`
	DoneMs        int64        `json:"done_ms"` // video time the written segments cover
	Shift         []TrackShift `json:"shift"`
}

// TrackShift is how far one track's timestamps were moved back.
type TrackShift struct {
	TrackID   uint32 `json:"track_id"`
	Timescale uint32 `json:"timescale"`
	Ticks     uint64 `json:"ticks"`
}

// WriteJSON writes v as indented JSON at dir/name.
func WriteJSON(dir, name string, v any) error {
	b, err := jsonBytes(v)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, filepath.FromSlash(name)), b)
}

// WriteText writes s at dir/name.
func WriteText(dir, name, s string) error {
	return writeAtomic(filepath.Join(dir, filepath.FromSlash(name)), []byte(s))
}

// HashFile returns a file's size and SHA-256.
func HashFile(path string) (int64, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return n, hex.EncodeToString(h.Sum(nil)), nil
}

// SyncBundle moves the files written into staging over the bundle at
// dest, replacing only those whose content changed, and removes files the
// previous sync listed that are gone now. Files outside previous (and the
// ones keep names) are never removed, so a bundle in a shared folder only
// loses what this app wrote. It returns every file now in the bundle that
// came from staging, and how many were written.
func SyncBundle(staging, dest string, previous []BundleFile, keep func(rel string) bool) ([]BundleFile, int, error) {
	var out []BundleFile
	written := 0
	err := filepath.WalkDir(staging, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}
		size, sum, err := HashFile(path)
		if err != nil {
			return err
		}
		f := BundleFile{Path: filepath.ToSlash(rel), Bytes: size, SHA256: sum}
		out = append(out, f)
		target := filepath.Join(dest, rel)
		if n, s, err := HashFile(target); err == nil && n == size && s == sum {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		if err := os.Rename(path, target); err != nil {
			return fmt.Errorf("move %s into the bundle: %w", f.Path, err)
		}
		written++
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	now := map[string]bool{}
	for _, f := range out {
		now[f.Path] = true
	}
	var dirs []string
	for _, f := range previous {
		if now[f.Path] || (keep != nil && keep(f.Path)) || !safeRel(f.Path) {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(f.Path))
		if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, 0, fmt.Errorf("remove %s from the bundle: %w", f.Path, err)
		}
		written++
		for d := filepath.Dir(target); d != dest && strings.HasPrefix(d, dest); d = filepath.Dir(d) {
			dirs = append(dirs, d)
		}
	}
	slices.SortFunc(dirs, func(a, b string) int { return len(b) - len(a) }) // deepest first
	for _, d := range dirs {
		os.Remove(d) // only succeeds when empty
	}
	slices.SortFunc(out, func(a, b BundleFile) int { return strings.Compare(a.Path, b.Path) })
	return out, written, nil
}

// safeRel is a relative path inside the bundle.
func safeRel(p string) bool {
	c := filepath.Clean(filepath.FromSlash(p))
	return p != "" && !filepath.IsAbs(c) && c != ".." && !strings.HasPrefix(c, ".."+string(filepath.Separator))
}

// BundleNote is one cut plan as notes.md lists it.
type BundleNote struct {
	PlanID    string
	Version   int
	State     string
	Target    string
	Confirmed bool
	Boundary  string
	Spans     []CutSpan
	Notes     []string // the user's notes on this version
}

// boundaryNames are the boundary candidates as the screen names them.
var boundaryNames = map[string]string{"short": "짧게", "context": "맥락", "full_event": "전체"}

// NotesMarkdown is notes.md: the confirmed cuts and every note the user
// left, as plain text a person reads first.
func NotesMarkdown(title string, plans []BundleNote) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", oneLine(title))
	b.WriteString("## 확정한 컷\n\n")
	n := 0
	for _, p := range plans {
		if !p.Confirmed || len(p.Spans) == 0 {
			continue
		}
		n++
		name := boundaryNames[p.Boundary]
		if name == "" {
			name = "AI 선택"
		}
		fmt.Fprintf(&b, "%d. %s (v%d, %s, 계획 %s)\n", n, oneLine(p.Target), p.Version, name, p.PlanID)
		for _, s := range p.Spans {
			fmt.Fprintf(&b, "   - %s ~ %s %s\n", Stamp(s.StartMs), Stamp(s.EndMs), roleName(s.Role))
		}
	}
	if n == 0 {
		b.WriteString("아직 확정한 컷이 없습니다.\n")
	}
	b.WriteString("\n## 메모\n\n")
	m := 0
	for _, p := range plans {
		for _, note := range p.Notes {
			m++
			fmt.Fprintf(&b, "- %s (v%d): %s\n", oneLine(p.Target), p.Version, oneLine(note))
		}
	}
	if m == 0 {
		b.WriteString("남긴 메모가 없습니다.\n")
	}
	return b.String()
}

func roleName(role string) string {
	switch role {
	case "setup":
		return "앞선 맥락"
	case "main":
		return "본 사건"
	case "payoff":
		return "결말"
	case "":
		return ""
	}
	return role
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// BundleReadme is README.txt: what the folder is and how to use it again.
func BundleReadme(m BundleManifest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "MomentForge 백업 묶음 — %s\n\n", oneLine(m.Session.Title))
	b.WriteString(`이 폴더만으로 방송 한 편을 다시 편집하고 다시 분석할 수 있도록 MomentForge가 만든 묶음입니다.
모든 파일은 그대로 열어 볼 수 있습니다. 채팅 작성자는 세션별 가명이고, 원래 ID·닉네임·API 키는 들어 있지 않습니다.

시간: 채팅·발언·컷의 시각은 모두 "영상 시작부터의 밀리초"입니다. manifest.json의
time_axis.video_offset_ms가 영상 파일의 0초가 그 시간축의 몇 ms인지 적습니다(0이면 그대로 맞음).

파일
- manifest.json            무엇이 들어 있는지, 파일마다 크기와 SHA-256
- video.mp4 · video.json   재인코딩 없이 받은 다시보기 영상과 그 정보(화질, 조각 수, 빠진 구간)
                           NAS 백업에서 불러온 작업은 영상을 복사하지 않고 manifest.json의 video.ref에 경로만 적습니다
- chat.jsonl · chat.txt    전체 채팅 (chat-manifest.json에 수집 상태와 공백)
- transcript.json · .srt · .vtt   방송 음성을 이 컴퓨터에서 글로 바꾼 발언 (자막 파일은 editnest 대본 탭용)
- timeline/                통합 타임라인 (자동 탐지·시청자 추천·AI 추천·컷)
- analysis/<분석 ID>/      규칙 분석 결과 (규칙 버전·파라미터·후보·근거)
- comments/                영상 댓글에서 찾은 시각 추천
- cuts/<계획 ID>/          AI 컷 분석 결과(plan.json)와 구간(cuts.csv, cuts.edl)
- cuts/confirmed.*         확정한 컷: confirmed.edl(편집 프로그램), confirmed.csv, confirmed.editnest.json(editnest 구간 받기)
- ai-runs/<실행 ID>.json   AI에 보낸 내용과 모델·토큰·비용 기록 (키 없음)
- notes.md                 확정한 컷과 남긴 메모

editnest에서 자르기: editnest 타임라인 메모에서 이 영상을 열고 cuts/confirmed.editnest.json을
JSON 불러오기로 넣은 뒤, 구간 받기의 "타임라인 메모에서 가져오기"를 누릅니다.

묶음을 다시 만들면 바뀐 파일만 바꿔 씁니다. 이 폴더에 직접 넣은 다른 파일은 지우지 않습니다.
`)
	return b.String()
}

// GapsOf keeps a nil gap list out of the JSON.
func GapsOf(gaps []domain.Gap) Issues {
	if gaps == nil {
		return Issues{}
	}
	return Issues(gaps)
}
