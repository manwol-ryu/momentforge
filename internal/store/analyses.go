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

var errNoAnalysis = domain.NewUserError("NOT_FOUND", "분석 결과를 찾을 수 없습니다.")

const analysisColumns = `id, session_id, created_at, rules_version, params_json, duration_ms,
	max_seq, event_count, data_sha256, source_state`

// AnalysisMeta loads a run's metadata without its windows and candidates
// (the windows of a long broadcast are close to a megabyte of JSON).
func (s *Store) AnalysisMeta(ctx context.Context, id string) (*Analysis, error) {
	a, _, err := s.analysisRow(ctx, id, false)
	return a, err
}

// Analysis loads a run with its windows and candidates.
func (s *Store) Analysis(ctx context.Context, id string) (*Analysis, error) {
	a, windows, err := s.analysisRow(ctx, id, true)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(windows), &a.Windows); err != nil {
		return nil, fmt.Errorf("read windows: %w", err)
	}
	if a.Candidates, err = s.analysisCandidates(ctx, id); err != nil {
		return nil, err
	}
	return a, nil
}

func (s *Store) analysisRow(ctx context.Context, id string, withWindows bool) (*Analysis, string, error) {
	var a Analysis
	var created, params, windows string
	q := "SELECT " + analysisColumns
	dest := []any{&a.ID, &a.SessionID, &created, &a.RulesVersion, &params, &a.DurationMs, &a.MaxSeq, &a.EventCount,
		&a.DataSHA256, &a.SourceState}
	if withWindows {
		q += ", windows_json"
		dest = append(dest, &windows)
	}
	err := s.db.QueryRowContext(ctx, q+" FROM analyses WHERE id=?", id).Scan(dest...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", errNoAnalysis
	}
	if err != nil {
		return nil, "", fmt.Errorf("read analysis: %w", err)
	}
	a.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	a.ParamsJSON = []byte(params)
	return &a, windows, nil
}

// analysisCandidates returns a run's candidates in time order.
func (s *Store) analysisCandidates(ctx context.Context, id string) ([]domain.Candidate, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT body_json FROM candidates WHERE analysis_id=? ORDER BY reaction_start_ms, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.Candidate
	for rows.Next() {
		var body string
		var c domain.Candidate
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			return nil, fmt.Errorf("read candidate: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AnalysisCandidate returns one candidate of a run; ok is false when the
// run has no candidate with that ID.
func (s *Store) AnalysisCandidate(ctx context.Context, analysisID, candidateID string) (domain.Candidate, bool, error) {
	var c domain.Candidate
	var body string
	err := s.db.QueryRowContext(ctx, `SELECT body_json FROM candidates WHERE analysis_id=? AND id=?`, analysisID, candidateID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	if err != nil {
		return c, false, fmt.Errorf("read candidate: %w", err)
	}
	if err := json.Unmarshal([]byte(body), &c); err != nil {
		return c, false, fmt.Errorf("read candidate: %w", err)
	}
	return c, true, nil
}
