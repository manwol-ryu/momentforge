package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// StateCommentsOnly is a VOD session that has its comments but no chat yet.
const StateCommentsOnly = "comments_only"

// CommentCapture is one fetch (or pasted text) of a video's comments.
type CommentCapture struct {
	ID            string
	SessionID     string
	Source        string // website | manual_paste
	State         string // finished | partial
	Pages         int
	ReportedCount int // -1 unknown
	Issues        []string
	ParserVersion int
	CreatedAt     time.Time
}

// StoredComment is a comment as kept: pseudonymous, blank when hidden.
type StoredComment struct {
	CommentID  string
	ParentID   string
	AuthorKey  string
	Content    string
	Status     string // parsed | needs_review | no_timestamp | unavailable
	IssuesJSON []byte // the parser's issues, owned by the caller
}

// ViewerRecommendation is a time a viewer wrote in a comment.
type ViewerRecommendation struct {
	ID          string
	CommentID   string
	StartMs     int64
	EndMs       int64 // 0 for a single time
	Description string
}

// SaveCommentCapture stores a capture with its comments and recommendations
// in one transaction and returns it with its ID and time.
func (s *Store) SaveCommentCapture(ctx context.Context, c CommentCapture, comments []StoredComment, recs []ViewerRecommendation) (CommentCapture, error) {
	c.ID, c.CreatedAt = newID(), time.Now().UTC()
	issues, _ := json.Marshal(nonNilStrings(c.Issues))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return c, err
	}
	defer tx.Rollback()
	var reported any
	if c.ReportedCount >= 0 {
		reported = c.ReportedCount
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO comment_captures (id, session_id, source, state, pages, reported_count,
		issues_json, parser_version, created_at) VALUES (?,?,?,?,?,?,?,?,?)`, c.ID, c.SessionID, c.Source, c.State, c.Pages,
		reported, string(issues), c.ParserVersion, c.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return c, fmt.Errorf("save comment capture: %w", err)
	}
	for i, cm := range comments {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO comments (capture_id, comment_id, seq, parent_id, author_key,
			content, status, issues_json) VALUES (?,?,?,NULLIF(?,''),NULLIF(?,''),?,?,?)`, c.ID, cm.CommentID, i+1, cm.ParentID,
			cm.AuthorKey, cm.Content, cm.Status, string(orEmptyArray(cm.IssuesJSON))); err != nil {
			return c, fmt.Errorf("save comment: %w", err)
		}
	}
	for _, r := range recs {
		var end any
		if r.EndMs > 0 {
			end = r.EndMs
		}
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO viewer_recommendations (capture_id, id, comment_id, start_ms,
			end_ms, description) VALUES (?,?,?,?,?,?)`, c.ID, r.ID, r.CommentID, r.StartMs, end, r.Description); err != nil {
			return c, fmt.Errorf("save recommendation: %w", err)
		}
	}
	return c, tx.Commit()
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func orEmptyArray(b []byte) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

// LatestCommentCapture returns a session's newest capture; ok is false when it has none.
func (s *Store) LatestCommentCapture(ctx context.Context, sid string) (CommentCapture, bool, error) {
	var c CommentCapture
	var reported sql.NullInt64
	var issues, created string
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, source, state, pages, reported_count, issues_json,
		parser_version, created_at FROM comment_captures WHERE session_id=? ORDER BY created_at DESC LIMIT 1`, sid).
		Scan(&c.ID, &c.SessionID, &c.Source, &c.State, &c.Pages, &reported, &issues, &c.ParserVersion, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, fmt.Errorf("read comment capture: %w", err)
	}
	c.ReportedCount = -1
	if reported.Valid {
		c.ReportedCount = int(reported.Int64)
	}
	json.Unmarshal([]byte(issues), &c.Issues)
	c.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return c, true, nil
}

// CaptureComments returns a capture's comments in the order received.
func (s *Store) CaptureComments(ctx context.Context, captureID string) ([]StoredComment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT comment_id, COALESCE(parent_id,''), COALESCE(author_key,''), content, status,
		issues_json FROM comments WHERE capture_id=? ORDER BY seq`, captureID)
	if err != nil {
		return nil, fmt.Errorf("read comments: %w", err)
	}
	defer rows.Close()
	var out []StoredComment
	for rows.Next() {
		var c StoredComment
		var issues string
		if err := rows.Scan(&c.CommentID, &c.ParentID, &c.AuthorKey, &c.Content, &c.Status, &issues); err != nil {
			return nil, fmt.Errorf("read comments: %w", err)
		}
		c.IssuesJSON = []byte(issues)
		out = append(out, c)
	}
	return out, rows.Err()
}

// ViewerRecommendations returns a capture's recommendations in time order.
func (s *Store) ViewerRecommendations(ctx context.Context, captureID string) ([]ViewerRecommendation, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, comment_id, start_ms, COALESCE(end_ms,0), description
		FROM viewer_recommendations WHERE capture_id=? ORDER BY start_ms, COALESCE(end_ms,0), id`, captureID)
	if err != nil {
		return nil, fmt.Errorf("read recommendations: %w", err)
	}
	defer rows.Close()
	var out []ViewerRecommendation
	for rows.Next() {
		var r ViewerRecommendation
		if err := rows.Scan(&r.ID, &r.CommentID, &r.StartMs, &r.EndMs, &r.Description); err != nil {
			return nil, fmt.Errorf("read recommendations: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
