package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Analysis is a stored detection run and the chat snapshot it used.
type Analysis struct {
	ID           string
	SessionID    string
	CreatedAt    time.Time
	RulesVersion string
	ParamsJSON   []byte
	DurationMs   int64
	MaxSeq       int64
	EventCount   int
	DataSHA256   string
	SourceState  string
	Windows      []domain.Window
	Candidates   []domain.Candidate
}

// SaveAnalysis stores a run; it assigns ID and CreatedAt when empty.
func (s *Store) SaveAnalysis(ctx context.Context, a *Analysis) error {
	if a.ID == "" {
		a.ID = newID()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	windows, err := json.Marshal(a.Windows)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO analyses (id, session_id, created_at, rules_version, params_json,
		duration_ms, max_seq, event_count, data_sha256, source_state, windows_json) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.SessionID, a.CreatedAt.Format(time.RFC3339Nano), a.RulesVersion, string(a.ParamsJSON),
		a.DurationMs, a.MaxSeq, a.EventCount, a.DataSHA256, a.SourceState, string(windows)); err != nil {
		return fmt.Errorf("save analysis: %w", err)
	}
	for _, c := range a.Candidates {
		body, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO candidates (analysis_id, id, reaction_start_ms, reaction_end_ms, body_json)
			VALUES (?,?,?,?,?)`, a.ID, c.ID, c.ReactionStartMs, c.ReactionEndMs, string(body)); err != nil {
			return fmt.Errorf("save candidate: %w", err)
		}
	}
	return tx.Commit()
}

// Analysis loads a run with its windows and candidates.
func (s *Store) Analysis(ctx context.Context, id string) (*Analysis, error) {
	var a Analysis
	var created, params, windows string
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, created_at, rules_version, params_json, duration_ms,
		max_seq, event_count, data_sha256, source_state, windows_json FROM analyses WHERE id=?`, id).
		Scan(&a.ID, &a.SessionID, &created, &a.RulesVersion, &params, &a.DurationMs, &a.MaxSeq, &a.EventCount,
			&a.DataSHA256, &a.SourceState, &windows)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.NewUserError("NOT_FOUND", "분석 결과를 찾을 수 없습니다.")
	}
	if err != nil {
		return nil, fmt.Errorf("read analysis: %w", err)
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	a.ParamsJSON = []byte(params)
	if err := json.Unmarshal([]byte(windows), &a.Windows); err != nil {
		return nil, fmt.Errorf("read windows: %w", err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT body_json FROM candidates WHERE analysis_id=? ORDER BY reaction_start_ms, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		var c domain.Candidate
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, fmt.Errorf("read candidate: %w", err)
		}
		a.Candidates = append(a.Candidates, c)
	}
	return &a, rows.Err()
}
