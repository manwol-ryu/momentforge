package app

import (
	"cmp"
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"slices"
	"time"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// LiveView is a LIVE capture as the LIVE screen shows it.
type LiveView struct {
	Session          SessionInfo
	ChannelID        string
	LiveID           string
	TimeBasis        string // broadcast_elapsed | capture_elapsed
	Running          bool
	Sensitive        bool
	AdaptiveGate     bool // the message gate follows the channel (REDESIGN 6.2.1)
	ViewerAdaptive   bool
	WatermarkMs      int64 // analysis is final up to here
	Generation       int   // connections made so far
	ViewerCount      int   // -1 unknown
	ViewerStatus     string
	LateCount        int
	DuplicateCount   int
	InvalidTimeCount int
	Revision         int64
	ClosedReason     string
	Gaps             []domain.Gap
	LastWindow       *domain.Window
	// Tiered captures judge their candidates in tiers (REDESIGN 6.2.3).
	// While one runs: its audio recording ("" none, recording, ended,
	// failed) and why it stopped, and the last automatic step that could
	// not run.
	Tiered    bool
	Audio     string
	AudioNote string
	AutoNote  string
}

func (a *App) liveSession(ctx context.Context, sid string) (store.Session, store.LiveState, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return s, store.LiveState{}, err
	}
	if s.Kind != string(platform.KindLive) {
		return s, store.LiveState{}, domain.NewUserError("NOT_FOUND", "생방송 기록을 찾을 수 없습니다.")
	}
	ls, err := a.cfg.Store.LiveState(ctx, sid)
	return s, ls, err
}

// LiveStatus returns a LIVE capture's state.
func (a *App) LiveStatus(ctx context.Context, sid string) (LiveView, error) {
	s, ls, err := a.liveSession(ctx, sid)
	if err != nil {
		return LiveView{}, err
	}
	gaps, err := a.cfg.Store.Gaps(ctx, sid)
	if err != nil {
		return LiveView{}, err
	}
	var p detect.Params
	json.Unmarshal(ls.ParamsJSON, &p)
	running, ok := a.runningLive()
	v := LiveView{Session: a.info(s), ChannelID: ls.ChannelID, LiveID: ls.LiveID, TimeBasis: ls.TimeBasis,
		Running: ok && running.sid == sid, Sensitive: p.Sensitive, AdaptiveGate: p.GateBusyRatio > 0, ViewerAdaptive: ls.ViewerAdaptive,
		WatermarkMs: ls.WatermarkMs, Generation: ls.Generation, ViewerCount: ls.ViewerCount, ViewerStatus: ls.ViewerStatus,
		LateCount: ls.LateCount, DuplicateCount: ls.DuplicateCount, InvalidTimeCount: ls.InvalidTimeCount,
		Revision: ls.Revision, ClosedReason: ls.ClosedReason, Gaps: gaps, Tiered: p.LiveTiers()}
	if v.Running {
		a.live.mu.Lock()
		audio, auto := running.audio, running.auto
		a.live.mu.Unlock()
		if audio != nil {
			v.Audio, v.AudioNote = audio.status()
		} else if auto != nil {
			v.Audio = "off" // 일반 설정 turned it off, or the platform cannot give its audio
		}
		if auto != nil {
			v.AutoNote = auto.status()
		}
	}
	if len(ls.LastWindowJSON) > 0 {
		var w domain.Window
		if json.Unmarshal(ls.LastWindowJSON, &w) == nil {
			v.LastWindow = &w
		}
	}
	return v, nil
}

// LiveChats returns the newest 300 messages, or up to 1,000 after afterSeq, in arrival order.
func (a *App) LiveChats(ctx context.Context, sid string, afterSeq int64) ([]LiveChat, error) {
	if _, _, err := a.liveSession(ctx, sid); err != nil {
		return nil, err
	}
	limit := 300
	if afterSeq > 0 {
		limit = 1000
	}
	rows, err := a.cfg.Store.RecentLiveChats(ctx, sid, afterSeq, limit)
	return liveChats(rows), err
}

// LiveContext returns up to 300 messages within ±30 s of atMs, in time order.
func (a *App) LiveContext(ctx context.Context, sid string, atMs int64) ([]LiveChat, error) {
	if atMs < 0 || atMs > 31*24*3600*1000 {
		return nil, domain.NewUserError("INPUT", "시간이 올바르지 않습니다.")
	}
	if _, _, err := a.liveSession(ctx, sid); err != nil {
		return nil, err
	}
	rows, err := a.cfg.Store.LiveChatsAround(ctx, sid, atMs, playheadSpanMs, 300)
	return liveChats(rows), err
}

func liveChats(rows []store.LiveChat) []LiveChat {
	out := make([]LiveChat, len(rows))
	for i, r := range rows {
		out[i] = LiveChat{ID: r.ID, Seq: r.Seq, OffsetMs: r.OffsetMs, Author: r.AuthorKey, Text: r.Text, Late: r.Late}
	}
	return out
}

// LiveCandidates returns candidates changed after revision afterRev, with an
// evidence summary, and the latest revision.
func (a *App) LiveCandidates(ctx context.Context, sid string, afterRev int64) ([]CandidateView, int64, error) {
	if _, _, err := a.liveSession(ctx, sid); err != nil {
		return nil, 0, err
	}
	cands, rev, err := a.cfg.Store.LiveCandidates(ctx, sid, afterRev)
	if err != nil {
		return nil, 0, err
	}
	byID, err := a.evidenceChats(ctx, sid, cands, store.AllSeq)
	if err != nil {
		return nil, 0, err
	}
	out := make([]CandidateView, len(cands))
	for i, c := range cands {
		out[i] = CandidateView{Candidate: c, EvidenceTotal: len(c.EvidenceIDs), Evidence: summarize(c.EvidenceIDs, byID)}
	}
	return out, rev, nil
}

// shownLive drops the candidates a judgment did not keep (REDESIGN 6.2.3).
func shownLive(cands []domain.Candidate) []domain.Candidate {
	return slices.DeleteFunc(cands, func(c domain.Candidate) bool { return c.State == domain.CandidateDropped })
}

// LiveCandidateChats pages through the chat of a LIVE candidate's clip range.
func (a *App) LiveCandidateChats(ctx context.Context, sid, candidateID string, offset, limit int) ([]ContextChat, int, error) {
	if offset < 0 || limit < 1 || limit > 500 {
		return nil, 0, domain.NewUserError("INPUT", "채팅 조회 범위가 올바르지 않습니다.")
	}
	if _, _, err := a.liveSession(ctx, sid); err != nil {
		return nil, 0, err
	}
	cands, _, err := a.cfg.Store.LiveCandidates(ctx, sid, 0)
	if err != nil {
		return nil, 0, err
	}
	for _, c := range cands {
		if c.ID == candidateID {
			return a.contextChats(ctx, sid, c, store.AllSeq, offset, limit)
		}
	}
	return nil, 0, domain.NewUserError("NOT_FOUND", "편집 후보를 찾을 수 없습니다.")
}

// LiveDensity is how many messages arrived per BucketMs, as a timeline of
// windows (T is a bucket's end; Valid is false inside unobserved gaps).
type LiveDensity struct {
	BucketMs int64
	EndMs    int64
	Windows  []domain.Window
}

// LiveDensity buckets the whole capture so far so a 5-hour broadcast stays
// around 1,800 bars.
func (a *App) LiveDensity(ctx context.Context, sid string) (LiveDensity, error) {
	s, _, err := a.liveSession(ctx, sid)
	if err != nil {
		return LiveDensity{}, err
	}
	gaps, err := a.cfg.Store.Gaps(ctx, sid)
	if err != nil {
		return LiveDensity{}, err
	}
	bucket := max(5000, (s.DurationMs/1800+4999)/5000*5000)
	counts, err := a.cfg.Store.ChatDensity(ctx, sid, bucket)
	if err != nil {
		return LiveDensity{}, err
	}
	end := s.DurationMs
	for b := range counts {
		end = max(end, (b+1)*bucket)
	}
	out := LiveDensity{BucketMs: bucket, EndMs: end, Windows: []domain.Window{}}
	for b, unobserved := range coveredBuckets(gaps, bucket, end) {
		if n := counts[int64(b)]; n > 0 || unobserved {
			out.Windows = append(out.Windows, domain.Window{T: int64(b+1) * bucket, N: n, Valid: !unobserved, Signals: []string{}})
		}
	}
	return out, nil
}

// coveredBuckets reports, for each bucket of [0, end), whether a single gap
// covers it. With the gaps in start order, that is when the furthest end of
// those starting at or before the bucket reaches the bucket's end.
func coveredBuckets(gaps []domain.Gap, bucket, end int64) []bool {
	gaps = slices.Clone(gaps)
	slices.SortFunc(gaps, func(x, y domain.Gap) int { return cmp.Compare(x.StartMs, y.StartMs) })
	out := make([]bool, (end+bucket-1)/bucket)
	next, reach := 0, int64(math.MinInt64)
	for b := range out {
		start := int64(b) * bucket
		for ; next < len(gaps) && gaps[next].StartMs <= start; next++ {
			reach = max(reach, gaps[next].EndMs)
		}
		out[b] = reach >= start+bucket
	}
	return out
}

// Checkpoint is a saved copy of a LIVE capture so far.
type Checkpoint struct {
	ID         string
	Folder     string // relative to the session's export folder, "/" separated
	EventCount int
}

// LiveCheckpoint writes the chat and candidates so far to
// checkpoints/<id>/ (chat.txt, chat.jsonl, manifest.json, timeline.*,
// markers/). It can run while the capture continues.
func (a *App) LiveCheckpoint(ctx context.Context, sid string) (Checkpoint, error) {
	return a.checkpoint(ctx, sid, "")
}

// checkpoint writes a checkpoint; state, when set, is recorded instead of the
// stored one (the final checkpoint is written before the end state is saved,
// so screens never list a half-written folder of a finished capture).
func (a *App) checkpoint(ctx context.Context, sid, state string) (Checkpoint, error) {
	a.live.export.Lock()
	defer a.live.export.Unlock()
	st := a.cfg.Store
	s, ls, err := a.liveSession(ctx, sid)
	if err != nil {
		return Checkpoint{}, err
	}
	if state != "" {
		s.State = state
	}
	maxSeq, _, err := st.MaxSeq(ctx, sid)
	if err != nil {
		return Checkpoint{}, err
	}
	rows, err := st.LiveChatsUpTo(ctx, sid, maxSeq)
	if err != nil {
		return Checkpoint{}, err
	}
	cands, _, err := st.LiveCandidates(ctx, sid, 0)
	if err != nil {
		return Checkpoint{}, err
	}
	cands = shownLive(cands)
	gaps, err := st.Gaps(ctx, sid)
	if err != nil {
		return Checkpoint{}, err
	}
	samples, err := st.ViewerSamples(ctx, sid)
	if err != nil {
		return Checkpoint{}, err
	}
	id := time.Now().UTC().Format("20060102-150405") + "-" + randomID()[:6]
	folder := "checkpoints/" + id
	dir := filepath.Join(a.exportDir(sid), "checkpoints", id)
	completeness := export.Completeness(s.State == store.LiveEnded, len(gaps))

	out := make([]export.LiveRow, len(rows))
	events := make([]domain.ChatEvent, len(rows))
	for i, r := range rows {
		out[i] = export.LiveRow{ChatEvent: r.ChatEvent, Late: r.Late, TimeQuality: r.TimeQuality}
		events[i] = r.ChatEvent
	}
	sum, err := export.WriteLiveChat(dir, export.LiveManifest{ID: sid, CheckpointID: id, Platform: s.Platform,
		ChannelID: ls.ChannelID, LiveID: ls.LiveID, Title: s.Title, State: s.State, TimeBasis: ls.TimeBasis,
		OriginMs: ls.OriginMs, WatermarkMs: ls.WatermarkMs, Generation: ls.Generation, Gaps: gaps, MaxSeq: maxSeq,
		LateCount: ls.LateCount, DuplicateCount: ls.DuplicateCount, InvalidTimeCount: ls.InvalidTimeCount,
		ViewerSamples: samples, Error: s.ErrorMessage, Completeness: completeness}, out)
	if err != nil {
		return Checkpoint{}, err
	}
	if err := export.WriteAnalysis(dir, export.AnalysisManifest{AnalysisID: id, SourceSessionID: sid,
		Params: ls.ParamsJSON, DataSHA256: sum, EventCount: len(rows), MaxSeq: maxSeq,
		RulesVersion: detect.RulesVersion, TimeBasis: ls.TimeBasis, DurationMs: ls.WatermarkMs,
		SourceState: s.State, Completeness: completeness, Gaps: gaps}, s.Title, cands, []domain.Window{}, events); err != nil {
		return Checkpoint{}, err
	}
	return Checkpoint{ID: id, Folder: folder, EventCount: len(rows)}, nil
}
