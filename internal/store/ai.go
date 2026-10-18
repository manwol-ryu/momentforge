package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// AI run deliveries.
const (
	AISingle = "single"
	AIBatch  = "batch"
)

// AI run kinds.
const (
	AIKindExplain = "explain" // recommendations from chunks (the first-generation review)
	AIKindCut     = "cut"     // cut plans, one per candidate (docs/AI-CUT-ANALYSIS.md)
)

// AIRun is one confirmed AI review. JSON fields are owned by the caller.
type AIRun struct {
	ID                  string
	SessionID           string
	Provider            string
	Model               string
	Fingerprint         string
	State               string
	Kind                string // explain | cut
	Delivery            string // single | batch
	BatchID             string // provider batch ID once submitted
	RequestCount        int
	PlanJSON            []byte // empty when listed with AIRuns
	RecommendationsJSON []byte
	UsageJSON           []byte
	SentLogJSON         []byte // cut runs: what each step sent (empty when listed)
	CompletedRequests   int
	Attempts            int
	Rejected            int
	InputTokens         int
	CacheReadTokens     int
	CacheWriteTokens    int
	OutputTokens        int
	CostUSD             *float64 // nil when the price is unknown
	CostCapUSD          *float64 // nil without a limit
	ErrorCode           string
	ErrorMessage        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// CreateAIRun stores a new running run and returns it with its ID.
func (s *Store) CreateAIRun(ctx context.Context, r AIRun) (AIRun, error) {
	r.ID, r.State = newID(), AIRunning
	if r.Delivery == "" {
		r.Delivery = AISingle
	}
	if r.Kind == "" {
		r.Kind = AIKindExplain
	}
	r.CreatedAt = time.Now().UTC()
	r.UpdatedAt = r.CreatedAt
	_, err := s.db.ExecContext(ctx, `INSERT INTO ai_runs (id, session_id, provider, model, fingerprint, state, kind, delivery,
		request_count, cost_cap_usd, plan_json, recommendations_json, usage_json, sent_log_json, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,'[]','[]','[]',?,?)`,
		r.ID, r.SessionID, r.Provider, r.Model, r.Fingerprint, r.State, r.Kind, r.Delivery, r.RequestCount, r.CostCapUSD,
		string(r.PlanJSON), r.CreatedAt.Format(time.RFC3339Nano), r.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return r, fmt.Errorf("create AI run: %w", err)
	}
	return r, nil
}

// SaveAIRun records a run's progress or result.
func (s *Store) SaveAIRun(ctx context.Context, r AIRun) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ai_runs SET state=?, batch_id=NULLIF(?,''), recommendations_json=?, usage_json=?,
		sent_log_json=?, completed_requests=?, attempts=?, rejected=?, input_tokens=?, cache_read_tokens=?, cache_write_tokens=?,
		output_tokens=?, cost_usd=?, error_code=NULLIF(?,''), error_message=NULLIF(?,''), updated_at=? WHERE id=?`,
		r.State, r.BatchID, string(orEmptyArray(r.RecommendationsJSON)), string(orEmptyArray(r.UsageJSON)),
		string(orEmptyArray(r.SentLogJSON)), r.CompletedRequests, r.Attempts, r.Rejected, r.InputTokens, r.CacheReadTokens,
		r.CacheWriteTokens, r.OutputTokens, r.CostUSD, r.ErrorCode, r.ErrorMessage, now(), r.ID)
	return err
}

const aiColumns = `id, session_id, provider, model, fingerprint, state, kind, delivery, COALESCE(batch_id,''), request_count,
	plan_json, recommendations_json, usage_json, sent_log_json, completed_requests, attempts, rejected, input_tokens, cache_read_tokens,
	cache_write_tokens, output_tokens, cost_usd, cost_cap_usd, COALESCE(error_code,''), COALESCE(error_message,''),
	created_at, updated_at`

// aiListColumns leaves out the plan and the sent log, which hold every chat sent.
var aiListColumns = strings.NewReplacer("plan_json", "''", "sent_log_json", "'[]'").Replace(aiColumns)

func scanAIRun(row interface{ Scan(...any) error }) (AIRun, error) {
	var r AIRun
	var plan, recs, usage, sent, created, updated string
	var cost, limit sql.NullFloat64
	err := row.Scan(&r.ID, &r.SessionID, &r.Provider, &r.Model, &r.Fingerprint, &r.State, &r.Kind, &r.Delivery, &r.BatchID,
		&r.RequestCount, &plan, &recs, &usage, &sent, &r.CompletedRequests, &r.Attempts, &r.Rejected, &r.InputTokens,
		&r.CacheReadTokens, &r.CacheWriteTokens, &r.OutputTokens, &cost, &limit, &r.ErrorCode, &r.ErrorMessage,
		&created, &updated)
	r.PlanJSON, r.RecommendationsJSON, r.UsageJSON, r.SentLogJSON = []byte(plan), []byte(recs), []byte(usage), []byte(sent)
	if cost.Valid {
		r.CostUSD = &cost.Float64
	}
	if limit.Valid {
		r.CostCapUSD = &limit.Float64
	}
	r.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	r.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return r, err
}

// AIRuns lists a session's runs, newest first, without their plans.
func (s *Store) AIRuns(ctx context.Context, sid string) ([]AIRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+aiListColumns+` FROM ai_runs WHERE session_id=?
		ORDER BY created_at DESC LIMIT 200`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIRun
	for rows.Next() {
		r, err := scanAIRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PendingBatchRuns are submitted batches whose results have not been read.
func (s *Store) PendingBatchRuns(ctx context.Context) ([]AIRun, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+aiColumns+` FROM ai_runs WHERE state=? AND delivery=?
		AND batch_id IS NOT NULL ORDER BY created_at`, AIRunning, AIBatch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AIRun
	for rows.Next() {
		r, err := scanAIRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AIRun returns one run.
func (s *Store) AIRun(ctx context.Context, id string) (AIRun, error) {
	r, err := scanAIRun(s.db.QueryRowContext(ctx, "SELECT "+aiColumns+" FROM ai_runs WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return r, errNotFound
	}
	return r, err
}

// LatestAIRun returns a session's newest review of recommendations that has
// a result (completed or partial); ok is false when there is none. Cut runs
// keep their results in ai_cut_plans.
func (s *Store) LatestAIRun(ctx context.Context, sid string) (AIRun, bool, error) {
	r, err := scanAIRun(s.db.QueryRowContext(ctx, "SELECT "+aiColumns+` FROM ai_runs WHERE session_id=? AND state IN (?,?)
		AND kind=? ORDER BY created_at DESC LIMIT 1`, sid, AICompleted, AIPartial, AIKindExplain))
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

// MarkAIRunsInterrupted closes runs left running by a crash or quit as
// partial. Submitted batches keep running at the provider and are not touched.
func (s *Store) MarkAIRunsInterrupted(ctx context.Context, message string) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE ai_runs SET state=?, error_code='PROCESS_RESTART', error_message=?, updated_at=?
		WHERE state=? AND (delivery<>? OR batch_id IS NULL)`, AIPartial, message, now(), AIRunning, AIBatch)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
