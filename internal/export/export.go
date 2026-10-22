// Package export writes chat and analysis files (TXT, JSONL, CSV, JSON).
// Formats follow CzzEdit 0.3.2 so existing workflows (e.g. pasting TXT into
// a chat assistant) keep working. It never touches the database.
package export

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Stamp formats a timeline position as HH:MM:SS.mmm.
func Stamp(ms int64) string {
	ms = max(0, ms)
	s, milli := ms/1000, ms%1000
	return fmt.Sprintf("%02d:%02d:%02d.%03d", s/3600, s%3600/60, s%60, milli)
}

// Escape keeps one message on one line.
func Escape(s string) string {
	return strings.NewReplacer(`\`, `\\`, "\r", `\r`, "\n", `\n`, "\t", `\t`).Replace(s)
}

// ChatLine is "[HH:MM:SS.mmm][author|unknown][event id] text\n"; a donation
// or subscription adds its kind and amount after the id ("[donation 1000]").
func ChatLine(e domain.ChatEvent) string {
	author := e.AuthorKey
	if author == "" {
		author = "unknown"
	}
	kind := ""
	switch e.Kind {
	case domain.ChatKindDonation, domain.ChatKindSubscription:
		kind = fmt.Sprintf("[%s %d]", e.Kind, e.Amount)
	case domain.ChatKindSystem:
		kind = "[system]"
	}
	return "[" + Stamp(e.OffsetMs) + "][" + author + "][" + e.ID + "]" + kind + " " + Escape(e.Text) + "\n"
}

// writeAtomic replaces path with data via a temporary file in the same folder.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".writing-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func jsonBytes(v any) ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ChatManifest describes a session's chat files.
type ChatManifest struct {
	ID                 string `json:"id"`
	Platform           string `json:"platform"`
	TargetID           string `json:"target_id"`
	Title              string `json:"title"`
	DurationMs         int64  `json:"duration_ms"`
	ReportedDurationMs int64  `json:"reported_duration_ms"`
	State              string `json:"state"`
	Cursor             int64  `json:"cursor"`
	Pages              int    `json:"pages"`
	Gaps               Issues `json:"issues"`
	Error              string `json:"error"`
	SchemaVersion      int    `json:"schema_version"`
	TimeBasis          string `json:"time_basis"`
	EventCount         int    `json:"event_count"`
	AvailableEndMs     int64  `json:"available_end_ms"`
	Completeness       string `json:"completeness"`
	RetrievalFinished  bool   `json:"retrieval_finished"`
	Note               string `json:"note"`
}

// Issues are unobserved spans, written as CzzEdit wrote them ({"start_ms", "end_ms"}).
type Issues []domain.Gap

func (is Issues) MarshalJSON() ([]byte, error) {
	type issue struct {
		StartMs int64 `json:"start_ms"`
		EndMs   int64 `json:"end_ms"`
	}
	out := make([]issue, len(is))
	for i, g := range is {
		out[i] = issue{g.StartMs, g.EndMs}
	}
	return json.Marshal(out)
}

// Completeness is "unverified" for a finished session without gaps, else "partial".
func Completeness(finished bool, gaps int) string {
	if finished && gaps == 0 {
		return "unverified"
	}
	return "partial"
}

type jsonlChat struct {
	EventID   string  `json:"event_id"`
	TimeMs    int64   `json:"time_ms"`
	AuthorKey *string `json:"author_key"`
	Content   string  `json:"content"`
	Seq       int64   `json:"seq"`
	Kind      string  `json:"kind,omitempty"`   // donation | subscription | system (absent for a message)
	Amount    int64   `json:"amount,omitempty"` // won, or months subscribed
	Hidden    bool    `json:"hidden,omitempty"`
}

// WriteChat writes manifest.json, chat.txt (time order) and chat.jsonl.
func WriteChat(dir string, m ChatManifest, events []domain.ChatEvent) error {
	m.SchemaVersion, m.TimeBasis, m.EventCount = 1, "video_position", len(events)
	m.Note = "API 조회 종료는 플랫폼의 모든 원문 채팅을 받았다는 보장이 아닙니다."
	if m.Gaps == nil {
		m.Gaps = Issues{}
	}
	manifest, err := jsonBytes(m)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return err
	}
	var txt strings.Builder
	fmt.Fprintf(&txt, "# MomentForge chat v1 | mode=VOD | time_basis=video_position | completeness=%s\n", m.Completeness)
	var jsonl bytes.Buffer
	enc := json.NewEncoder(&jsonl)
	enc.SetEscapeHTML(false)
	for _, e := range events {
		txt.WriteString(ChatLine(e))
		row := jsonlChat{EventID: e.ID, TimeMs: e.OffsetMs, Content: e.Text, Seq: e.Seq, Kind: e.Kind, Amount: e.Amount, Hidden: e.Hidden}
		if e.AuthorKey != "" {
			row.AuthorKey = &e.AuthorKey
		}
		if err := enc.Encode(row); err != nil {
			return err
		}
	}
	if err := writeAtomic(filepath.Join(dir, "chat.txt"), []byte(txt.String())); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "chat.jsonl"), jsonl.Bytes())
}

const receivedHeader = "# MomentForge chat v1 | mode=VOD | ordering=received | time_basis=video_position\n"

// WriteReceived rewrites a VOD's chat.received.txt (arrival order) from scratch.
func WriteReceived(dir string, events []domain.ChatEvent) error {
	return writeReceived(dir, receivedHeader, events)
}

// LiveHeader is the first line of LIVE chat files.
func LiveHeader(timeBasis, liveID string) string {
	return "# MomentForge chat v1 | mode=LIVE | time_basis=" + timeBasis + " | live_id=" + Escape(liveID) + "\n"
}

// WriteLiveReceived rewrites a LIVE capture's chat.received.txt from scratch.
func WriteLiveReceived(dir, timeBasis, liveID string, events []domain.ChatEvent) error {
	return writeReceived(dir, LiveHeader(timeBasis, liveID), events)
}

func writeReceived(dir, header string, events []domain.ChatEvent) error {
	var b strings.Builder
	b.WriteString(header)
	for _, e := range events {
		b.WriteString(ChatLine(e))
	}
	return writeAtomic(filepath.Join(dir, "chat.received.txt"), []byte(b.String()))
}

// AppendReceived adds newly stored messages to chat.received.txt.
func AppendReceived(dir string, events []domain.ChatEvent) error {
	f, err := os.OpenFile(filepath.Join(dir, "chat.received.txt"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	for _, e := range events {
		if _, err := f.WriteString(ChatLine(e)); err != nil {
			return err
		}
	}
	return f.Sync()
}

// AnalysisManifest describes one analysis folder.
type AnalysisManifest struct {
	SchemaVersion   int             `json:"schema_version"`
	AnalysisID      string          `json:"analysis_id"`
	SourceSessionID string          `json:"source_session_id"`
	Params          json.RawMessage `json:"params"`
	DataSHA256      string          `json:"data_sha256"`
	EventCount      int             `json:"event_count"`
	MaxSeq          int64           `json:"max_seq"`
	RulesVersion    string          `json:"rules_version"`
	TimeBasis       string          `json:"time_basis"`
	DurationMs      int64           `json:"duration_ms"`
	SourceState     string          `json:"source_state"`
	Completeness    string          `json:"completeness"`
	Gaps            Issues          `json:"issues"`
}

// WriteAnalysis writes analysis-manifest.json, timeline.json, timeline.txt,
// timeline.csv and markers/<id>.txt (clip-range chat, evidence marked "*").
// events must be the analysis snapshot in time order.
func WriteAnalysis(dir string, m AnalysisManifest, title string, cands []domain.Candidate, windows []domain.Window, events []domain.ChatEvent) error {
	m.SchemaVersion = 1
	if m.TimeBasis == "" {
		m.TimeBasis = "video_position"
	}
	if m.Gaps == nil {
		m.Gaps = Issues{}
	}
	manifest, err := jsonBytes(m)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "analysis-manifest.json"), manifest); err != nil {
		return err
	}
	timeline, err := jsonBytes(map[string]any{
		"candidates": cands, "windows": windows, "duration_ms": m.DurationMs, "rules_version": m.RulesVersion,
	})
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "timeline.json"), timeline); err != nil {
		return err
	}

	var txt strings.Builder
	fmt.Fprintf(&txt, "MomentForge 편집 후보 | %s\n시간 기준: %s | 후보 %d개\n", Escape(title), basisLabel[m.TimeBasis], len(cands))
	txt.WriteString("수집 완전성: 미확인. manifest.json의 범위·누락 표시를 함께 확인하세요.\n\n")
	var csvBuf bytes.Buffer
	csvBuf.WriteString("\ufeff")
	w := csv.NewWriter(&csvBuf)
	w.Write([]string{"id", "time_basis", "start", "peak", "end", "clip_start", "clip_end", "reasons", "N", "U",
		"ratio", "baseline_ready", "partial_context", "review_state", "evidence_ids"})
	for _, c := range cands {
		ready, context := "완료", "확보"
		if !c.BaselineReady {
			ready = "임시 후보"
		}
		if c.PartialContext {
			context = "부족"
		}
		fmt.Fprintf(&txt, "%s [%s ~ %s]\n  이유: %s\n  반응: %s ~ %s / 정점: %s\n  정점 유효 채팅 %d건 / 작성자 %d명 / 기준 학습: %s\n  앞뒤 문맥: %s / 검토: 미검토\n",
			c.ID, Stamp(c.ClipStartMs), Stamp(c.ClipEndMs), strings.Join(c.Reasons, ", "),
			Stamp(c.ReactionStartMs), Stamp(c.ReactionEndMs), Stamp(c.PeakMs), c.Peak.N, c.Peak.U, ready, context)
		if c.State != "" {
			fmt.Fprintf(&txt, "  LIVE 상태: %s\n", stateLabel[c.State])
		}
		txt.WriteString("\n")
		ratio := ""
		if c.Peak.Ratio != nil {
			ratio = strconv.FormatFloat(*c.Peak.Ratio, 'g', -1, 64)
		}
		row := []string{c.ID, m.TimeBasis, Stamp(c.ReactionStartMs), Stamp(c.PeakMs), Stamp(c.ReactionEndMs),
			Stamp(c.ClipStartMs), Stamp(c.ClipEndMs), strings.Join(c.Reasons, ", "), strconv.Itoa(c.Peak.N),
			strconv.Itoa(c.Peak.U), ratio, pyBool(c.BaselineReady), pyBool(c.PartialContext), "미검토",
			strings.Join(c.EvidenceIDs, " ")}
		for i := range row {
			row[i] = csvSafe(row[i])
		}
		w.Write(row)

		evidence := make(map[string]bool, len(c.EvidenceIDs))
		for _, id := range c.EvidenceIDs {
			evidence[id] = true
		}
		var marker strings.Builder
		marker.WriteString("# * = 판정 근거에 포함된 채팅. 나머지는 앞뒤 문맥입니다.\n")
		from := sort.Search(len(events), func(i int) bool { return events[i].OffsetMs >= c.ClipStartMs })
		for _, e := range events[from:] {
			if e.OffsetMs >= c.ClipEndMs {
				break
			}
			if evidence[e.ID] {
				marker.WriteString("* ")
			} else {
				marker.WriteString("  ")
			}
			marker.WriteString(ChatLine(e))
		}
		if err := writeAtomic(filepath.Join(dir, "markers", c.ID+".txt"), []byte(marker.String())); err != nil {
			return err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "timeline.txt"), []byte(txt.String())); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "timeline.csv"), csvBuf.Bytes())
}

var basisLabel = map[string]string{
	"video_position":    "다시보기 재생 위치",
	"broadcast_elapsed": "방송 시작 후 경과",
	"capture_elapsed":   "수집 시작 후 경과",
}

var stateLabel = map[string]string{
	domain.CandidateGenerating:     "반응 진행 중",
	domain.CandidateWaitingContext: "뒤 문맥 대기",
	domain.CandidateConfirmed:      "확정",
	domain.CandidatePartial:        "문맥 부족",
}

// LiveManifest describes a LIVE checkpoint's chat files.
type LiveManifest struct {
	ID               string            `json:"id"`
	CheckpointID     string            `json:"checkpoint_id"`
	Platform         string            `json:"platform"`
	ChannelID        string            `json:"channel_id"`
	LiveID           string            `json:"live_id"`
	Title            string            `json:"title"`
	State            string            `json:"state"`
	TimeBasis        string            `json:"time_basis"`
	OriginMs         int64             `json:"origin_ms"`
	WatermarkMs      int64             `json:"watermark_ms"`
	Generation       int               `json:"generation"`
	Gaps             Issues            `json:"issues"`
	MaxSeq           int64             `json:"max_seq"`
	LateCount        int               `json:"late_count"`
	DuplicateCount   int               `json:"duplicate_count"`
	InvalidTimeCount int               `json:"invalid_time_count"`
	ViewerSamples    []json.RawMessage `json:"viewer_samples"`
	Error            string            `json:"error"`
	SchemaVersion    int               `json:"schema_version"`
	EventCount       int               `json:"event_count"`
	DataSHA256       string            `json:"data_sha256"`
	LatePolicy       string            `json:"late_policy"`
	Completeness     string            `json:"completeness"`
	Note             string            `json:"note"`
}

// LiveRow is a stored LIVE message with the facts exported alongside it.
type LiveRow struct {
	domain.ChatEvent
	Late        bool
	TimeQuality string
}

type jsonlLive struct {
	jsonlChat
	Late        bool   `json:"late"`
	TimeQuality string `json:"time_quality"`
}

// WriteLiveChat writes a LIVE checkpoint's manifest.json, chat.txt and
// chat.jsonl. rows must be in time order. It returns the SHA-256 of chat.jsonl.
func WriteLiveChat(dir string, m LiveManifest, rows []LiveRow) (string, error) {
	var jsonl bytes.Buffer
	enc := json.NewEncoder(&jsonl)
	enc.SetEscapeHTML(false)
	var txt strings.Builder
	txt.WriteString(LiveHeader(m.TimeBasis, m.LiveID))
	for _, r := range rows {
		txt.WriteString(ChatLine(r.ChatEvent))
		row := jsonlLive{jsonlChat{EventID: r.ID, TimeMs: r.OffsetMs, Content: r.Text, Seq: r.Seq}, r.Late, r.TimeQuality}
		if r.AuthorKey != "" {
			row.AuthorKey = &r.AuthorKey
		}
		if err := enc.Encode(row); err != nil {
			return "", err
		}
	}
	sum := sha256.Sum256(jsonl.Bytes())
	m.SchemaVersion, m.EventCount, m.DataSHA256 = 1, len(rows), hex.EncodeToString(sum[:])
	m.LatePolicy = "retain_raw_exclude_finalized_windows"
	m.Note = "늦게 도착한 채팅(late)은 저장하지만 이미 확정한 분석 구간에는 넣지 않습니다. issues는 연결이 끊겨 관측하지 못한 구간입니다."
	if m.Gaps == nil {
		m.Gaps = Issues{}
	}
	if m.ViewerSamples == nil {
		m.ViewerSamples = []json.RawMessage{}
	}
	manifest, err := jsonBytes(m)
	if err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, "manifest.json"), manifest); err != nil {
		return "", err
	}
	if err := writeAtomic(filepath.Join(dir, "chat.txt"), []byte(txt.String())); err != nil {
		return "", err
	}
	return m.DataSHA256, writeAtomic(filepath.Join(dir, "chat.jsonl"), jsonl.Bytes())
}

// csvSafe stops spreadsheet formula injection.
func csvSafe(v string) string {
	if t := strings.TrimLeft(v, " \t\r\n"); t != "" && strings.ContainsRune("=+-@", rune(t[0])) {
		return "'" + v
	}
	return v
}

func pyBool(b bool) string {
	if b {
		return "True"
	}
	return "False"
}

// CommentManifest describes a comment capture's files.
type CommentManifest struct {
	CaptureID           string   `json:"capture_id"`
	SessionID           string   `json:"session_id"`
	VideoID             string   `json:"video_id"`
	Source              string   `json:"source"` // website | manual_paste
	State               string   `json:"state"`  // finished | partial
	Pages               int      `json:"pages"`
	ReportedCount       *int     `json:"reported_count"`
	Issues              []string `json:"issues"`
	ParserVersion       int      `json:"parser_version"`
	CommentCount        int      `json:"comment_count"`
	RecommendationCount int      `json:"recommendation_count"`
	Completeness        string   `json:"completeness"`
	Note                string   `json:"note"`
}

// CommentRow is one stored comment (pseudonymous; blank when hidden).
type CommentRow struct {
	CommentID string          `json:"comment_id"`
	ParentID  *string         `json:"parent_comment_id"`
	AuthorKey *string         `json:"author_key"`
	Content   string          `json:"content"`
	Status    string          `json:"status"`
	Issues    json.RawMessage `json:"issues"`
}

// RecommendationRow is one viewer recommendation (CzzEdit's fields).
type RecommendationRow struct {
	ID          string `json:"id"`
	CommentID   string `json:"comment_id"`
	StartMs     int64  `json:"start_ms"`
	EndMs       *int64 `json:"end_ms"`
	Description string `json:"description"`
	SourceType  string `json:"source_type"`
	ReviewState string `json:"review_state"`
	Source      string `json:"source"`
	ParseKind   string `json:"parse_kind"`
}

// WriteComments writes comments.jsonl, recommendations.json and manifest.json.
func WriteComments(dir string, m CommentManifest, comments []CommentRow, recs []RecommendationRow) error {
	m.Completeness = "unverified"
	m.Note = "영상 댓글의 시각은 시청자가 쓴 내용입니다. 채팅 TXT에는 섞지 않습니다."
	m.CommentCount, m.RecommendationCount = len(comments), len(recs)
	if m.Issues == nil {
		m.Issues = []string{}
	}
	var jsonl bytes.Buffer
	enc := json.NewEncoder(&jsonl)
	enc.SetEscapeHTML(false)
	for _, c := range comments {
		if len(c.Issues) == 0 {
			c.Issues = json.RawMessage("[]")
		}
		if err := enc.Encode(c); err != nil {
			return err
		}
	}
	if recs == nil {
		recs = []RecommendationRow{}
	}
	for _, f := range []struct {
		name string
		v    any
	}{{"recommendations.json", recs}, {"manifest.json", m}} {
		b, err := jsonBytes(f.v)
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, f.name), b); err != nil {
			return err
		}
	}
	return writeAtomic(filepath.Join(dir, "comments.jsonl"), jsonl.Bytes())
}

// TimelineFile is the unified timeline of one video: rule candidates,
// viewer recommendations and AI recommendations, each labelled by source.
type TimelineFile struct {
	SessionID  string         `json:"session_id"`
	VideoID    string         `json:"video_id"`
	Title      string         `json:"title"`
	DurationMs int64          `json:"duration_ms"`
	AnalysisID string         `json:"run_id"`
	CaptureID  string         `json:"comment_id"`
	AIRunID    string         `json:"ai_id"`
	TimeBasis  string         `json:"time_basis"`
	Items      []TimelineItem `json:"items"`
}

// TimelineItem is one entry; Source is rule, viewer_comment or ai.
type TimelineItem struct {
	ID          string `json:"id"`
	Source      string `json:"source_type"`
	OriginID    string `json:"origin_id"`
	AnchorMs    int64  `json:"anchor_ms"`
	ClipStartMs int64  `json:"clip_start_ms"`
	ClipEndMs   int64  `json:"clip_end_ms"`
	Title       string `json:"title"`
	CommentText string `json:"comment_text,omitempty"`
	Rationale   string `json:"rationale,omitempty"`
	Uncertainty string `json:"uncertainty,omitempty"`
	WatchURL    string `json:"video_url,omitempty"`
}

var sourceLabel = map[string]string{"rule": "자동 탐지", "viewer_comment": "시청자 추천", "ai": "AI 추천"}

// WriteTimeline writes timeline.json, timeline.txt and timeline.csv (CzzEdit's unified timeline).
func WriteTimeline(dir string, t TimelineFile) error {
	t.TimeBasis = "video_position"
	if t.Items == nil {
		t.Items = []TimelineItem{}
	}
	b, err := jsonBytes(t)
	if err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "timeline.json"), b); err != nil {
		return err
	}
	var txt strings.Builder
	fmt.Fprintf(&txt, "MomentForge 통합 타임라인 | %s\n자동 탐지 %s / 시청자 추천 %s / AI %s\n", Escape(t.Title),
		orNone(t.AnalysisID), orNone(t.CaptureID), orNone(t.AIRunID))
	var csvBuf bytes.Buffer
	csvBuf.WriteString("\ufeff")
	w := csv.NewWriter(&csvBuf)
	w.Write([]string{"time_ms", "time", "source", "id", "title"})
	for _, it := range t.Items {
		s := it.AnchorMs / 1000
		fmt.Fprintf(&txt, "%02d:%02d:%02d [%s] %s (%s)\n", s/3600, s/60%60, s%60, sourceLabel[it.Source], Escape(it.Title), it.ID)
		w.Write([]string{strconv.FormatInt(it.AnchorMs, 10), Stamp(it.AnchorMs), sourceLabel[it.Source], it.ID, csvSafe(it.Title)})
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return err
	}
	if err := writeAtomic(filepath.Join(dir, "timeline.txt"), []byte(txt.String())); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, "timeline.csv"), csvBuf.Bytes())
}

func orNone(id string) string {
	if id == "" {
		return "없음"
	}
	return id
}

// AIManifest describes an AI run's files.
type AIManifest struct {
	RunID             string   `json:"run_id"`
	SessionID         string   `json:"session_id"`
	Provider          string   `json:"provider"`
	Model             string   `json:"model"`
	Delivery          string   `json:"delivery"` // single | batch
	BatchID           string   `json:"batch_id,omitempty"`
	PromptVersion     int      `json:"prompt_version"`
	Fingerprint       string   `json:"fingerprint"`
	State             string   `json:"state"`
	RequestCount      int      `json:"request_count"`
	CompletedRequests int      `json:"completed_requests"`
	Attempts          int      `json:"attempts"`
	Rejected          int      `json:"rejected"`
	InputTokens       int      `json:"input_tokens"` // uncached
	CacheReadTokens   int      `json:"cache_read_input_tokens"`
	CacheWriteTokens  int      `json:"cache_creation_input_tokens"`
	OutputTokens      int      `json:"output_tokens"`
	CostUSD           *float64 `json:"cost_usd"`     // at list price; null when unknown
	CostCapUSD        *float64 `json:"cost_cap_usd"` // the user's limit; null when none
	Error             string   `json:"error,omitempty"`
	Ranges            any      `json:"ranges"`
	Omitted           any      `json:"omitted"`
	Unprocessed       []Range  `json:"unprocessed_ranges"`
	Note              string   `json:"note"`
}

// Range is a span of the video in milliseconds.
type Range struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
}

// AIRecommendation is one validated AI recommendation.
type AIRecommendation struct {
	ID                 string   `json:"id"`
	SourceCandidateIDs []string `json:"source_candidate_ids"`
	StartMs            int64    `json:"start_ms"`
	EndMs              int64    `json:"end_ms"`
	Title              string   `json:"title"`
	Rationale          string   `json:"rationale"`
	EvidenceRefs       []string `json:"evidence_refs"`
	Uncertainty        string   `json:"uncertainty"`
	Priority           string   `json:"priority"`
}

var priorityLabel = map[string]string{"high": "높음", "medium": "보통", "low": "낮음"}

// WriteAIRun writes recommendations.json, recommendations.txt and manifest.json.
func WriteAIRun(dir string, m AIManifest, recs []AIRecommendation) error {
	m.Note = "AI는 영상과 음성을 보지 않았습니다. 채팅 근거로 만든 추천이며 장면을 직접 확인해야 합니다."
	if recs == nil {
		recs = []AIRecommendation{}
	}
	if m.Unprocessed == nil {
		m.Unprocessed = []Range{}
	}
	var txt strings.Builder
	fmt.Fprintf(&txt, "MomentForge AI 추천 | %s %s | %s\n\n", m.Provider, Escape(m.Model), m.State)
	for _, r := range recs {
		fmt.Fprintf(&txt, "%s ~ %s [%s] %s\n  근거: %s\n  판단 한계: %s\n\n", Stamp(r.StartMs), Stamp(r.EndMs),
			priorityLabel[r.Priority], Escape(r.Title), Escape(r.Rationale), Escape(r.Uncertainty))
	}
	for _, f := range []struct {
		name string
		v    any
	}{{"recommendations.json", recs}, {"manifest.json", m}} {
		b, err := jsonBytes(f.v)
		if err != nil {
			return err
		}
		if err := writeAtomic(filepath.Join(dir, f.name), b); err != nil {
			return err
		}
	}
	return writeAtomic(filepath.Join(dir, "recommendations.txt"), []byte(txt.String()))
}

// CutSpan is one span of a cut plan in the files.
type CutSpan struct {
	Order    int    `json:"order"`
	StartMs  int64  `json:"start_ms"`
	EndMs    int64  `json:"end_ms"`
	Role     string `json:"role"`
	StartRef string `json:"start_ref"`
	EndRef   string `json:"end_ref"`
}

// CutPlanFile is what WriteCutPlan writes: the plan as JSON, and its spans
// as CSV and EDL for an editor (editnest, Premiere, Resolve).
type CutPlanFile struct {
	ID        string
	SessionID string
	State     string
	Target    string
	Reason    string
	Spans     []CutSpan
	Plan      []byte // the plan's JSON as is
}

// edlTimecode is HH:MM:SS:FF at 30 fps (non-drop), which editors accept as
// a plain time; the video's own frame rate is applied when it is placed.
func edlTimecode(ms int64) string {
	frames := (ms % 1000) * 30 / 1000
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d:%02d", s/3600, s%3600/60, s%60, frames)
}

// WriteCutPlan writes plan.json, cuts.csv and cuts.edl under dir.
func WriteCutPlan(dir string, f CutPlanFile) error {
	if f.Spans == nil {
		f.Spans = []CutSpan{}
	}
	var csvb strings.Builder
	csvb.WriteString("plan_id,order,role,start_ms,end_ms,start,end,start_ref,end_ref,target,state\n")
	for _, s := range f.Spans {
		fmt.Fprintf(&csvb, "%s,%d,%s,%d,%d,%s,%s,%s,%s,%s,%s\n", f.ID, s.Order, s.Role, s.StartMs, s.EndMs, Stamp(s.StartMs), Stamp(s.EndMs),
			s.StartRef, s.EndRef, csvField(f.Target), f.State)
	}
	var edl strings.Builder
	fmt.Fprintf(&edl, "TITLE: MomentForge %s\nFCM: NON-DROP FRAME\n\n", f.ID)
	var record int64
	for _, s := range f.Spans {
		length := s.EndMs - s.StartMs
		fmt.Fprintf(&edl, "%03d  AX       V     C        %s %s %s %s\n* FROM CLIP NAME: %s\n* %s\n\n", s.Order,
			edlTimecode(s.StartMs), edlTimecode(s.EndMs), edlTimecode(record), edlTimecode(record+length), f.SessionID, s.Role)
		record += length
	}
	for _, w := range []struct {
		name string
		data []byte
	}{{"plan.json", f.Plan}, {"cuts.csv", []byte(csvb.String())}, {"cuts.edl", []byte(edl.String())}} {
		if err := writeAtomic(filepath.Join(dir, w.name), w.data); err != nil {
			return err
		}
	}
	return nil
}

// ConfirmedCut is one confirmed cut plan in the confirmed exports.
type ConfirmedCut struct {
	PlanID   string
	Version  int
	Boundary string // short | context | full_event
	Target   string
	State    string
	Spans    []CutSpan
}

// ConfirmedCutsFile is every confirmed cut of a session, for an editor:
// ClipName names the source in the EDL (the video file's name when the
// session has one), VideoName is set only from a real file name so
// editnest's 타임라인 메모 can match it.
type ConfirmedCutsFile struct {
	SessionID  string
	ClipName   string
	VideoName  string
	DurationMs int64
	Cuts       []ConfirmedCut
}

var confirmedNames = []string{"confirmed.edl", "confirmed.csv", "confirmed.editnest.json"}

var roleLabel = map[string]string{"setup": "앞선 맥락", "main": "본 장면", "payoff": "마무리"}

// WriteConfirmedCuts writes confirmed.edl (CMX 3600, one event per span,
// laid end to end), confirmed.csv and confirmed.editnest.json (editnest's
// 타임라인 메모 JSON: segments in seconds) under dir. With no confirmed cut
// the files are removed, so a stale list is never left behind.
func WriteConfirmedCuts(dir string, f ConfirmedCutsFile) error {
	if len(f.Cuts) == 0 {
		for _, name := range confirmedNames {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		return nil
	}
	clip := strings.Join(strings.Fields(f.ClipName), " ")
	if clip == "" {
		clip = f.SessionID
	}
	var csvb, edl strings.Builder
	csvb.WriteString("plan_id,version,boundary,order,role,start_ms,end_ms,start,end,target,state\n")
	fmt.Fprintf(&edl, "TITLE: MomentForge confirmed cuts\nFCM: NON-DROP FRAME\n\n")
	type segment struct {
		ID    string  `json:"id"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
		Title string  `json:"title"`
		Note  string  `json:"note"`
		Tag   string  `json:"tag"`
	}
	segments := []segment{}
	var record int64
	event := 0
	for _, c := range f.Cuts {
		target := strings.Join(strings.Fields(c.Target), " ")
		for i, s := range c.Spans {
			event++
			fmt.Fprintf(&csvb, "%s,%d,%s,%d,%s,%d,%d,%s,%s,%s,%s\n", c.PlanID, c.Version, c.Boundary, i+1, s.Role, s.StartMs, s.EndMs,
				Stamp(s.StartMs), Stamp(s.EndMs), csvField(target), c.State)
			length := s.EndMs - s.StartMs
			fmt.Fprintf(&edl, "%03d  AX       V     C        %s %s %s %s\n* FROM CLIP NAME: %s\n* COMMENT: %s | %s\n\n", event,
				edlTimecode(s.StartMs), edlTimecode(s.EndMs), edlTimecode(record), edlTimecode(record+length), clip,
				truncate(target, 60), roleLabel[s.Role])
			record += length
			title := []rune(target)
			if len(title) > 80 {
				title = title[:80]
			}
			segments = append(segments, segment{ID: fmt.Sprintf("mf-%s-%d", c.PlanID, i+1), Start: float64(s.StartMs) / 1000,
				End: float64(s.EndMs) / 1000, Title: string(title), Tag: "MomentForge",
				Note: fmt.Sprintf("%s · %s · v%d", roleLabel[s.Role], boundaryLabel[c.Boundary], c.Version)})
		}
	}
	memo := map[string]any{"exportedAt": time.Now().UTC().Format(time.RFC3339), "duration": float64(f.DurationMs) / 1000, "segments": segments}
	if f.VideoName != "" {
		memo["videoName"] = f.VideoName
	}
	js, err := jsonBytes(memo)
	if err != nil {
		return err
	}
	for _, w := range []struct {
		name string
		data []byte
	}{{"confirmed.edl", []byte(edl.String())}, {"confirmed.csv", []byte(csvb.String())}, {"confirmed.editnest.json", js}} {
		if err := writeAtomic(filepath.Join(dir, w.name), w.data); err != nil {
			return err
		}
	}
	return nil
}

var boundaryLabel = map[string]string{"short": "짧게", "context": "맥락", "full_event": "전체"}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func csvField(s string) string {
	if strings.ContainsAny(s, ",\"\n") {
		return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
	}
	return s
}

// srtStamp is HH:MM:SS,mmm.
func srtStamp(ms int64) string {
	ms = max(0, ms)
	s := ms / 1000
	return fmt.Sprintf("%02d:%02d:%02d,%03d", s/3600, s%3600/60, s%60, ms%1000)
}

// WriteTranscript writes <id>.srt, <id>.vtt and <id>.json under dir (a
// caption file an editor such as editnest reads, and the utterances with
// their ids for the cut analysis' refs).
func WriteTranscript(dir, id string, utts []domain.Utterance) error {
	var srt, vtt strings.Builder
	vtt.WriteString("WEBVTT\n\n")
	type row struct {
		ID      string `json:"id"`
		StartMs int64  `json:"start_ms"`
		EndMs   int64  `json:"end_ms"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	}
	rows := make([]row, 0, len(utts))
	for i, u := range utts {
		end := u.EndMs
		if end <= u.StartMs {
			end = u.StartMs + 1000
		}
		fmt.Fprintf(&srt, "%d\n%s --> %s\n%s\n\n", i+1, srtStamp(u.StartMs), srtStamp(end), u.Text)
		fmt.Fprintf(&vtt, "%s --> %s\n%s\n\n", strings.Replace(srtStamp(u.StartMs), ",", ".", 1), strings.Replace(srtStamp(end), ",", ".", 1), u.Text)
		uid := u.ID
		if uid == "" {
			uid = fmt.Sprintf("%s-%d", id, i+1)
		}
		rows = append(rows, row{ID: uid, StartMs: u.StartMs, EndMs: end, Speaker: u.Speaker, Text: u.Text})
	}
	b, err := jsonBytes(rows)
	if err != nil {
		return err
	}
	for _, w := range []struct {
		name string
		data []byte
	}{{id + ".srt", []byte(srt.String())}, {id + ".vtt", []byte(vtt.String())}, {id + ".json", b}} {
		if err := writeAtomic(filepath.Join(dir, w.name), w.data); err != nil {
			return err
		}
	}
	return nil
}
