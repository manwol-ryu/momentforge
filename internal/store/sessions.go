package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Session states.
const (
	StateCollecting = "collecting"
	StatePaused     = "paused"
	StateFinished   = "finished"
)

// Session is a collected (or collecting) timeline.
type Session struct {
	ID                 string
	Platform           string
	Kind               string
	TargetID           string
	Title              string
	DurationMs         int64
	ReportedDurationMs int64
	State              string
	Cursor             int64
	Pages              int
	ErrorCode          string
	ErrorMessage       string
	Hidden             bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	ChatCount          int    // filled by Sessions and Session
	LatestAnalysisID   string // filled by Sessions and Session
}

// NewSession is what a collection starts with.
type NewSession struct {
	Platform, Kind, TargetID, Title string
	DurationMs                      int64
	Salt                            []byte
}

var errNotFound = domain.NewUserError("NOT_FOUND", "저장된 수집 작업을 찾을 수 없습니다.")

func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func newID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// CreateSession stores a new session in the collecting state.
func (s *Store) CreateSession(ctx context.Context, n NewSession) (Session, error) {
	id, ts := newID(), now()
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions
		(id, platform, kind, target_id, title, duration_ms, reported_duration_ms, salt, state, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, n.Platform, n.Kind, n.TargetID, n.Title, n.DurationMs, n.DurationMs, n.Salt, StateCollecting, ts, ts)
	if err != nil {
		return Session{}, fmt.Errorf("create session: %w", err)
	}
	return s.Session(ctx, id)
}

const sessionColumns = `s.id, s.platform, s.kind, s.target_id, s.title, s.duration_ms, s.reported_duration_ms,
	s.state, s.cursor, s.pages, COALESCE(s.error_code,''), COALESCE(s.error_message,''), s.hidden,
	s.created_at, s.updated_at,
	(SELECT count(*) FROM chat_events c WHERE c.session_id = s.id),
	COALESCE((SELECT a.id FROM analyses a WHERE a.session_id = s.id ORDER BY a.created_at DESC LIMIT 1), '')`

func scanSession(row interface{ Scan(...any) error }) (Session, error) {
	var x Session
	var hidden int
	var created, updated string
	err := row.Scan(&x.ID, &x.Platform, &x.Kind, &x.TargetID, &x.Title, &x.DurationMs, &x.ReportedDurationMs,
		&x.State, &x.Cursor, &x.Pages, &x.ErrorCode, &x.ErrorMessage, &hidden, &created, &updated,
		&x.ChatCount, &x.LatestAnalysisID)
	if err != nil {
		return x, err
	}
	x.Hidden = hidden != 0
	x.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	x.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return x, nil
}

// Session returns one session.
func (s *Store) Session(ctx context.Context, id string) (Session, error) {
	x, err := scanSession(s.db.QueryRowContext(ctx, "SELECT "+sessionColumns+" FROM sessions s WHERE s.id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return x, errNotFound
	}
	if err != nil {
		return x, fmt.Errorf("read session: %w", err)
	}
	return x, nil
}

// Sessions lists sessions, newest first.
func (s *Store) Sessions(ctx context.Context, includeHidden bool) ([]Session, error) {
	q := "SELECT " + sessionColumns + " FROM sessions s"
	if !includeHidden {
		q += " WHERE s.hidden = 0"
	}
	rows, err := s.db.QueryContext(ctx, q+" ORDER BY s.created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		x, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("list sessions: %w", err)
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// SessionSalt returns the pseudonym secret of a session.
func (s *Store) SessionSalt(ctx context.Context, id string) ([]byte, error) {
	var salt []byte
	err := s.db.QueryRowContext(ctx, "SELECT salt FROM sessions WHERE id = ?", id).Scan(&salt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errNotFound
	}
	return salt, err
}

// SetState records a state change and its error (empty code clears it).
func (s *Store) SetState(ctx context.Context, id, state, code, message string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET state=?, error_code=NULLIF(?,''), error_message=NULLIF(?,''), updated_at=? WHERE id=?`,
		state, code, message, now(), id)
	return err
}

// Page is one validated platform page ready to store.
type Page struct {
	Cursor   int64
	Raw      []byte
	Events   []domain.ChatEvent // ID, OffsetMs, AuthorKey, Text set; Seq assigned here
	Next     int64              // new resume cursor
	Finished bool
	Gaps     []domain.Gap
}

// SavePage stores a page, its new events, gaps and the checkpoint in one
// transaction. Events already stored (overlapping pages) are skipped; the
// newly inserted ones are returned with their Seq.
func (s *Store) SavePage(ctx context.Context, sid string, p Page) ([]domain.ChatEvent, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	pageID, err := insertRaw(ctx, tx, sid, p.Cursor, p.Raw, false)
	if err != nil {
		return nil, err
	}
	var added []domain.ChatEvent
	maxEnd := int64(0)
	for _, e := range p.Events {
		maxEnd = max(maxEnd, e.OffsetMs+1)
		res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chat_events (session_id, event_id, offset_ms, author_key, text, page_id)
			VALUES (?,?,?,NULLIF(?,''),?,?)`, sid, e.ID, e.OffsetMs, e.AuthorKey, e.Text, pageID)
		if err != nil {
			return nil, fmt.Errorf("insert chat: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 1 {
			e.Seq, _ = res.LastInsertId()
			added = append(added, e)
		}
	}
	for _, g := range p.Gaps {
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO gaps (session_id, start_ms, end_ms, reason) VALUES (?,?,?,?)`,
			sid, g.StartMs, g.EndMs, "boundary_ambiguous"); err != nil {
			return nil, fmt.Errorf("insert gap: %w", err)
		}
	}
	state := StateCollecting
	if p.Finished {
		state = StateFinished
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sessions SET cursor=?, pages=pages+1, state=?, duration_ms=max(duration_ms, ?),
		error_code=NULL, error_message=NULL, updated_at=? WHERE id=?`, p.Next, state, maxEnd, now(), sid); err != nil {
		return nil, fmt.Errorf("update checkpoint: %w", err)
	}
	return added, tx.Commit()
}

// Quarantine keeps a rejected page for diagnosis.
func (s *Store) Quarantine(ctx context.Context, sid string, cursor int64, raw []byte) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := insertRaw(ctx, tx, sid, cursor, raw, true); err != nil {
		return err
	}
	return tx.Commit()
}

func insertRaw(ctx context.Context, tx *sql.Tx, sid string, cursor int64, raw []byte, quarantined bool) (int64, error) {
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	q := 0
	if quarantined {
		q = 1
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO raw_pages (session_id, cursor, digest, body, quarantined, received_at)
		VALUES (?,?,?,?,?,?)`, sid, cursor, digest, raw, q, now()); err != nil {
		return 0, fmt.Errorf("store raw page: %w", err)
	}
	var id int64
	err := tx.QueryRowContext(ctx, `SELECT id FROM raw_pages WHERE session_id=? AND cursor=? AND digest=?`, sid, cursor, digest).Scan(&id)
	return id, err
}

// Gaps returns the spans analysis must skip.
func (s *Store) Gaps(ctx context.Context, sid string) ([]domain.Gap, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT start_ms, end_ms FROM gaps WHERE session_id=? ORDER BY start_ms`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Gap
	for rows.Next() {
		var g domain.Gap
		if err := rows.Scan(&g.StartMs, &g.EndMs); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}
