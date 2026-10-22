package store

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// LIVE session states (VOD uses collecting/paused/finished).
const (
	LiveStarting     = "starting"
	LiveCollecting   = "collecting"
	LiveReconnecting = "reconnecting"
	LiveStopping     = "stopping"
	LiveStopped      = "stopped"
	LiveEnded        = "ended"
	LiveError        = "error"
	LiveIncomplete   = "incomplete"
)

// LiveTerminal reports whether a LIVE state is final (resumable by the user).
func LiveTerminal(state string) bool {
	switch state {
	case LiveStopped, LiveEnded, LiveError, LiveIncomplete:
		return true
	}
	return false
}

// LiveState is the mutable LIVE part of a session.
type LiveState struct {
	ChannelID        string
	LiveID           string
	OriginMs         int64
	TimeBasis        string
	ParamsJSON       []byte
	ViewerAdaptive   bool
	Generation       int
	WatermarkMs      int64
	LastHealthyAtMs  int64 // 0 when never healthy
	ClosedReason     string
	ViewerStatus     string
	ViewerCount      int // -1 when unknown
	LateCount        int
	DuplicateCount   int
	InvalidTimeCount int
	Revision         int64
	LastWindowJSON   []byte
}

// CreateLive stores a new LIVE session and its state.
func (s *Store) CreateLive(ctx context.Context, n NewSession, ls LiveState) (Session, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer tx.Rollback()
	id, ts := newID(), now()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sessions
		(id, platform, kind, target_id, title, duration_ms, reported_duration_ms, salt, state, created_at, updated_at)
		VALUES (?,?,?,?,?,0,0,?,?,?,?)`, id, n.Platform, "live", n.TargetID, n.Title, n.Salt, LiveStarting, ts, ts); err != nil {
		return Session{}, fmt.Errorf("create live session: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO live_state (session_id, channel_id, live_id, origin_ms, time_basis,
		params_json, viewer_adaptive, viewer_count) VALUES (?,?,?,?,?,?,?,-1)`,
		id, ls.ChannelID, ls.LiveID, ls.OriginMs, ls.TimeBasis, string(ls.ParamsJSON), boolInt(ls.ViewerAdaptive)); err != nil {
		return Session{}, fmt.Errorf("create live state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Session{}, err
	}
	return s.Session(ctx, id)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// LiveState loads a session's LIVE state.
func (s *Store) LiveState(ctx context.Context, sid string) (LiveState, error) {
	var ls LiveState
	var params string
	var adaptive int
	var healthy, viewerCount sql.NullInt64
	var closed, vstatus, window sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT channel_id, live_id, origin_ms, time_basis, params_json, viewer_adaptive,
		generation, watermark_ms, last_healthy_at_ms, closed_reason, viewer_status, viewer_count, late_count,
		duplicate_count, invalid_time_count, revision, last_window_json FROM live_state WHERE session_id=?`, sid).
		Scan(&ls.ChannelID, &ls.LiveID, &ls.OriginMs, &ls.TimeBasis, &params, &adaptive, &ls.Generation, &ls.WatermarkMs,
			&healthy, &closed, &vstatus, &viewerCount, &ls.LateCount, &ls.DuplicateCount, &ls.InvalidTimeCount,
			&ls.Revision, &window)
	if errors.Is(err, sql.ErrNoRows) {
		return ls, domain.NewUserError("NOT_FOUND", "라이브 기록을 찾을 수 없습니다.")
	}
	if err != nil {
		return ls, fmt.Errorf("read live state: %w", err)
	}
	ls.ParamsJSON, ls.ViewerAdaptive = []byte(params), adaptive != 0
	ls.LastHealthyAtMs, ls.ClosedReason, ls.ViewerStatus = healthy.Int64, closed.String, vstatus.String
	ls.ViewerCount = -1
	if viewerCount.Valid {
		ls.ViewerCount = int(viewerCount.Int64)
	}
	if window.Valid {
		ls.LastWindowJSON = []byte(window.String)
	}
	return ls, nil
}

// SaveLiveState writes the mutable LIVE fields and the session's state and
// timeline length in one transaction.
func (s *Store) SaveLiveState(ctx context.Context, sid, state, errCode, errMsg string, ls LiveState) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE live_state SET generation=?, watermark_ms=?, last_healthy_at_ms=NULLIF(?,0),
		closed_reason=NULLIF(?,''), viewer_status=NULLIF(?,''), viewer_count=?, late_count=?, duplicate_count=?,
		invalid_time_count=?, last_window_json=? WHERE session_id=?`,
		ls.Generation, ls.WatermarkMs, ls.LastHealthyAtMs, ls.ClosedReason, ls.ViewerStatus, ls.ViewerCount,
		ls.LateCount, ls.DuplicateCount, ls.InvalidTimeCount, nullBytes(ls.LastWindowJSON), sid); err != nil {
		return fmt.Errorf("save live state: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET state=?, error_code=NULLIF(?,''), error_message=NULLIF(?,''),
		duration_ms=max(duration_ms, ?), cursor=?, updated_at=? WHERE id=?`,
		state, errCode, errMsg, ls.WatermarkMs, ls.WatermarkMs, now(), sid); err != nil {
		return fmt.Errorf("save live session: %w", err)
	}
	return tx.Commit()
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

// LiveEvent is a received LIVE message ready to store.
type LiveEvent struct {
	domain.ChatEvent // ID, OffsetMs, AuthorKey, Text
	ReceivedAtMs     int64
	EventTimeMs      int64 // 0 when the platform gave none
	Generation       int
	Late             bool
	TimeQuality      string // "provider" | "invalid"
	ReplayKey        string // set when the platform gave a message id
	History          bool   // sent at connection time, from before the connection
}

// AppendLive stores messages and returns the stored ones with Seq. History
// messages already stored by an earlier connection are dropped (replays);
// seen counts this connection's occurrences per replay key and must be kept
// by the caller for the connection's lifetime.
func (s *Store) AppendLive(ctx context.Context, sid string, evs []LiveEvent, seen map[string]int) (saved []LiveEvent, duplicates int, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	defer tx.Rollback()
	for _, e := range evs {
		if e.ReplayKey != "" && e.History {
			var old int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM chat_events WHERE session_id=? AND replay_key=? AND generation<?`,
				sid, e.ReplayKey, e.Generation).Scan(&old); err != nil {
				return nil, 0, err
			}
			k := fmt.Sprintf("%d/%s", e.Generation, e.ReplayKey)
			seen[k]++
			if seen[k] <= old {
				duplicates++
				continue
			}
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO chat_events (session_id, event_id, offset_ms, author_key, text,
			received_at_ms, event_time_ms, generation, late, time_quality, replay_key)
			VALUES (?,?,?,NULLIF(?,''),?,?,NULLIF(?,0),?,?,?,NULLIF(?,''))`,
			sid, e.ID, e.OffsetMs, e.AuthorKey, e.Text, e.ReceivedAtMs, e.EventTimeMs, e.Generation, boolInt(e.Late),
			e.TimeQuality, e.ReplayKey)
		if err != nil {
			return nil, 0, fmt.Errorf("insert live chat: %w", err)
		}
		e.Seq, _ = res.LastInsertId()
		saved = append(saved, e)
	}
	return saved, duplicates, s.commitChat(tx, sid, len(saved))
}

// AddGap records an unobserved span.
func (s *Store) AddGap(ctx context.Context, sid string, g domain.Gap, reason string) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO gaps (session_id, start_ms, end_ms, reason) VALUES (?,?,?,?)`,
		sid, g.StartMs, g.EndMs, reason)
	return err
}

// UpsertLiveCandidates stores changed candidates under new revisions and
// returns the latest revision. Unchanged candidates are skipped.
func (s *Store) UpsertLiveCandidates(ctx context.Context, sid string, cands []domain.Candidate) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var rev int64
	if err := tx.QueryRowContext(ctx, `SELECT revision FROM live_state WHERE session_id=?`, sid).Scan(&rev); err != nil {
		return 0, err
	}
	for _, c := range cands {
		body, err := json.Marshal(c)
		if err != nil {
			return 0, err
		}
		var old string
		err = tx.QueryRowContext(ctx, `SELECT body_json FROM live_candidates WHERE session_id=? AND id=?`, sid, c.ID).Scan(&old)
		if err == nil && old == string(body) {
			continue
		}
		rev++
		if _, err := tx.ExecContext(ctx, `INSERT INTO live_candidates (session_id, id, revision, state, clip_start_ms, body_json)
			VALUES (?,?,?,?,?,?) ON CONFLICT (session_id, id) DO UPDATE SET revision=excluded.revision,
			state=excluded.state, clip_start_ms=excluded.clip_start_ms, body_json=excluded.body_json`,
			sid, c.ID, rev, c.State, c.ClipStartMs, string(body)); err != nil {
			return 0, fmt.Errorf("save live candidate: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE live_state SET revision=? WHERE session_id=?`, rev, sid); err != nil {
		return 0, err
	}
	return rev, tx.Commit()
}

// LiveCandidates returns candidates changed after afterRevision (0: all),
// ordered by clip start, and the latest revision.
func (s *Store) LiveCandidates(ctx context.Context, sid string, afterRevision int64) ([]domain.Candidate, int64, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body_json FROM live_candidates WHERE session_id=? AND revision>?
		ORDER BY clip_start_ms, id`, sid, afterRevision)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []domain.Candidate
	for rows.Next() {
		var body string
		var c domain.Candidate
		if err := rows.Scan(&body); err != nil {
			return nil, 0, err
		}
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, 0, err
		}
		out = append(out, c)
	}
	var rev int64
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM live_state WHERE session_id=?`, sid).Scan(&rev); err != nil {
		return nil, 0, err
	}
	return out, rev, rows.Err()
}

// AddViewerSample keeps one viewer-count observation (JSON).
func (s *Store) AddViewerSample(ctx context.Context, sid string, body []byte) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO viewer_samples (session_id, body_json) VALUES (?,?)`, sid, string(body))
	return err
}

// ViewerSamples returns a session's samples in arrival order.
func (s *Store) ViewerSamples(ctx context.Context, sid string) ([]json.RawMessage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body_json FROM viewer_samples WHERE session_id=? ORDER BY seq`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []json.RawMessage
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return nil, err
		}
		out = append(out, json.RawMessage(b))
	}
	return out, rows.Err()
}

// LiveChat is a stored LIVE message with its LIVE facts.
type LiveChat struct {
	domain.ChatEvent
	Late        bool
	TimeQuality string
}

func (s *Store) queryLive(ctx context.Context, q string, args ...any) ([]LiveChat, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveChat
	for rows.Next() {
		var c LiveChat
		var late int
		var tq sql.NullString
		if err := rows.Scan(&c.ID, &c.Seq, &c.OffsetMs, &c.AuthorKey, &c.Text, &late, &tq); err != nil {
			return nil, err
		}
		c.Late, c.TimeQuality = late != 0, tq.String
		out = append(out, c)
	}
	return out, rows.Err()
}

const liveColumns = "event_id, seq, offset_ms, COALESCE(author_key,''), text, late, time_quality"

// RecentLiveChats returns the last limit messages, or those after afterSeq, in arrival order.
func (s *Store) RecentLiveChats(ctx context.Context, sid string, afterSeq int64, limit int) ([]LiveChat, error) {
	if afterSeq > 0 {
		return s.queryLive(ctx, "SELECT "+liveColumns+" FROM chat_events WHERE session_id=? AND seq>? ORDER BY seq LIMIT ?", sid, afterSeq, limit)
	}
	rows, err := s.queryLive(ctx, "SELECT "+liveColumns+" FROM chat_events WHERE session_id=? ORDER BY seq DESC LIMIT ?", sid, limit)
	for i, j := 0, len(rows)-1; i < j; i, j = i+1, j-1 {
		rows[i], rows[j] = rows[j], rows[i]
	}
	return rows, err
}

// LiveChatsAround returns up to limit messages within ±span of at, nearest first, then in time order.
func (s *Store) LiveChatsAround(ctx context.Context, sid string, at, span int64, limit int) ([]LiveChat, error) {
	rows, err := s.queryLive(ctx, "SELECT "+liveColumns+` FROM chat_events WHERE session_id=? AND offset_ms>=? AND offset_ms<=?
		ORDER BY abs(offset_ms-?), seq LIMIT ?`, sid, max(0, at-span), at+span, at, limit)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(rows, func(a, b LiveChat) int {
		return cmp.Or(cmp.Compare(a.OffsetMs, b.OffsetMs), cmp.Compare(a.Seq, b.Seq))
	})
	return rows, nil
}

// MaxSeq returns the session's last stored message seq.
func (s *Store) MaxSeq(ctx context.Context, sid string) (int64, int, error) {
	var maxSeq int64
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(max(seq),0), count(*) FROM chat_events WHERE session_id=?`, sid).Scan(&maxSeq, &count)
	return maxSeq, count, err
}

// MarkLiveIncomplete closes LIVE sessions left running by a crash or quit:
// they become incomplete and their open candidates partial.
func (s *Store) MarkLiveIncomplete(ctx context.Context, message string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM sessions WHERE kind='live' AND state NOT IN (?,?,?,?)`,
		LiveStopped, LiveEnded, LiveError, LiveIncomplete)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		cands, _, err := s.LiveCandidates(ctx, id, 0)
		if err != nil {
			return 0, err
		}
		var open []domain.Candidate
		for _, c := range cands {
			if c.State == domain.CandidateGenerating || c.State == domain.CandidateWaitingContext {
				c.State, c.PartialContext = domain.CandidatePartial, true
				open = append(open, c)
			}
		}
		if _, err := s.UpsertLiveCandidates(ctx, id, open); err != nil {
			return 0, err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE sessions SET state=?, error_code='PROCESS_RESTART', error_message=?, updated_at=? WHERE id=?`,
			LiveIncomplete, message, now(), id); err != nil {
			return 0, err
		}
		s.db.ExecContext(ctx, `UPDATE live_state SET closed_reason='process_restart' WHERE session_id=?`, id)
	}
	return len(ids), nil
}

// LiveChatsUpTo returns a LIVE session's messages up to maxSeq in time order.
func (s *Store) LiveChatsUpTo(ctx context.Context, sid string, maxSeq int64) ([]LiveChat, error) {
	return s.queryLive(ctx, "SELECT "+liveColumns+" FROM chat_events WHERE session_id=? AND seq<=? ORDER BY offset_ms, seq", sid, maxSeq)
}

// ChatsByID returns the session's messages with the given event IDs (each
// once), in time order.
func (s *Store) ChatsByID(ctx context.Context, sid string, ids []string) ([]domain.ChatEvent, error) {
	seen := make(map[string]bool, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if !seen[id] {
			seen[id] = true
			unique = append(unique, id)
		}
	}
	var out []domain.ChatEvent
	for ids := unique; len(ids) > 0; {
		n := min(len(ids), 500)
		args := []any{sid}
		for _, id := range ids[:n] {
			args = append(args, id)
		}
		// No ORDER BY: it would make SQLite walk the session's whole time
		// index instead of looking each id up; the result is sorted below.
		rows, err := s.queryChats(ctx, "SELECT "+chatColumns+" FROM chat_events WHERE session_id=? AND event_id IN (?"+
			strings.Repeat(",?", n-1)+")", args...)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		ids = ids[n:]
	}
	slices.SortFunc(out, func(a, b domain.ChatEvent) int {
		return cmp.Or(cmp.Compare(a.OffsetMs, b.OffsetMs), cmp.Compare(a.Seq, b.Seq))
	})
	return out, nil
}

// ChatDensity counts a session's messages per bucketMs-long span, from 0.
// Spans without messages are left out.
func (s *Store) ChatDensity(ctx context.Context, sid string, bucketMs int64) (map[int64]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT offset_ms/?, count(*) FROM chat_events WHERE session_id=? GROUP BY 1`, bucketMs, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int{}
	for rows.Next() {
		var b int64
		var n int
		if err := rows.Scan(&b, &n); err != nil {
			return nil, err
		}
		out[b] = n
	}
	return out, rows.Err()
}
