package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// AI run states.
const (
	AIRunning   = "running"
	AICompleted = "completed"
	AIPartial   = "partial"
	AIFailed    = "failed"
	AICancelled = "cancelled"
)

// AIRun is one confirmed AI review. JSON fields are owned by the caller.
type AIRun struct {
	ID                  string
	SessionID           string
	Provider            string
	Model               string
	Fingerprint         string
	State               string
	PlanJSON            []byte
	RecommendationsJSON []byte
	UsageJSON           []byte
	CompletedRequests   int
	Attempts            int
	Rejected            int
	ErrorCode           string
	ErrorMessage        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CreateAIRun stores a new running run and returns it with its ID.
func (s *Store) CreateAIRun(ctx context.Context, r AIRun) (AIRun, error) {
	r.ID, r.State = newID(), AIRunning
	r.CreatedAt = time.Now().UTC()
	r.UpdatedAt = r.CreatedAt
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_runs (id, session_id, provider, model, fingerprint, state, plan_json,
		recommendations_json, usage_json, created_at, updated_at) VALUES (?,?,?,?,?,?,?,'[]','[]',?,?)`,
		r.ID, r.SessionID, r.Provider, r.Model, r.Fingerprint, r.State, string(r.PlanJSON),
		r.CreatedAt.Format(time.RFC3339Nano), r.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return r, fmt.Errorf("create AI run: %w", err)
	}
	return r, nil
}

// SaveAIRun records a run's progress or result.
func (s *Store) SaveAIRun(ctx context.Context, r AIRun) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ai_runs SET state=?, recommendations_json=?, usage_json=?, completed_requests=?,
		attempts=?, rejected=?, error_code=NULLIF(?,''), error_message=NULLIF(?,''), updated_at=? WHERE id=?`,
		r.State, string(orEmptyArray(r.RecommendationsJSON)), string(orEmptyArray(r.UsageJSON)), r.CompletedRequests,
		r.Attempts, r.Rejected, r.ErrorCode, r.ErrorMessage, now(), r.ID)
	return err
}

const aiColumns = `id, session_id, provider, model, fingerprint, state, plan_json, recommendations_json, usage_json,
	completed_requests, attempts, rejected, COALESCE(error_code,''), COALESCE(error_message,''), created_at, updated_at`

func scanAIRun(row interface{ Scan(...any) error }) (AIRun, error) {
	var r AIRun
	var plan, recs, usage, created, updated string
	err := row.Scan(&r.ID, &r.SessionID, &r.Provider, &r.Model, &r.Fingerprint, &r.State, &plan, &recs, &usage,
		&r.CompletedRequests, &r.Attempts, &r.Rejected, &r.ErrorCode, &r.ErrorMessage, &created, &updated)
	r.PlanJSON, r.RecommendationsJSON, r.UsageJSON = []byte(plan), []byte(recs), []byte(usage)
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, err
}

// AIRun returns one run.
func (s *Store) AIRun(ctx context.Context, id string) (AIRun, error) {
	r, err := scanAIRun(s.db.QueryRowContext(ctx, "SELECT "+aiColumns+" FROM ai_runs WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, errNotFound
	}
	return r, err
}

// LatestAIRun returns a session's newest run that has a result
// (completed or partial); ok is false when there is none.
func (s *Store) LatestAIRun(ctx context.Context, sid string) (AIRun, bool, error) {
	r, err := scanAIRun(s.db.QueryRowContext(ctx, "SELECT "+aiColumns+` FROM ai_runs WHERE session_id=? AND state IN (?,?)
		ORDER BY created_at DESC LIMIT 1`, sid, AICompleted, AIPartial))
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// CompletedAIRun returns a completed run of the same plan, if any.
func (s *Store) CompletedAIRun(ctx context.Context, sid, fingerprint string) (AIRun, bool, error) {
	r, err := scanAIRun(s.db.QueryRowContext(ctx, "SELECT "+aiColumns+` FROM ai_runs WHERE session_id=? AND fingerprint=?
		AND state=? ORDER BY created_at DESC LIMIT 1`, sid, fingerprint, AICompleted))
	if errors.Is(err, sql.ErrNoRows) {
		return r, false, nil
	}
	return r, err == nil, err
}

// MarkAIRunsInterrupted closes runs left running by a crash or quit as partial.
func (s *Store) MarkAIRunsInterrupted(ctx context.Context, message string) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE ai_runs SET state=?, error_code='PROCESS_RESTART', error_message=?, updated_at=?
		WHERE state=?`, AIPartial, message, now(), AIRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
