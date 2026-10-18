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

// CutPlan is one candidate's cut analysis (docs/AI-CUT-ANALYSIS.md 4.1).
// The plan itself is JSON owned by the caller (internal/ai/cut.Plan); the
// columns are what lists and the timeline need without reading it.
type CutPlan struct {
	ID           string
	RunID        string
	SessionID    string
	CandidateIDs []string
	Version      int
	Supersedes   string
	State        string // ok | verify_failed | failed
	StartMs      int64
	EndMs        int64
	PlanJSON     []byte // empty when listed
	Confirmed    bool
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateCutPlan stores a plan as the next version for its candidates and
// returns it with its ID, version and the plan it supersedes.
func (s *Store) CreateCutPlan(ctx context.Context, p CutPlan) (CutPlan, error) {
	ids, _ := json.Marshal(p.CandidateIDs)
	var version int
	var prev sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0), (SELECT id FROM ai_cut_plans WHERE session_id=? AND candidate_ids_json=?
		ORDER BY version DESC LIMIT 1) FROM ai_cut_plans WHERE session_id=? AND candidate_ids_json=?`,
		p.SessionID, string(ids), p.SessionID, string(ids)).Scan(&version, &prev)
	if err != nil {
		return p, fmt.Errorf("cut plan version: %w", err)
	}
	p.ID, p.Version, p.Supersedes = newID(), version+1, prev.String
	p.CreatedAt = time.Now().UTC()
	p.UpdatedAt = p.CreatedAt
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_cut_plans (id, run_id, session_id, candidate_ids_json, version, supersedes,
		state, start_ms, end_ms, plan_json, confirmed, created_at, updated_at) VALUES (?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?)`,
		p.ID, p.RunID, p.SessionID, string(ids), p.Version, p.Supersedes, p.State, p.StartMs, p.EndMs, string(p.PlanJSON),
		p.Confirmed, p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return p, fmt.Errorf("create cut plan: %w", err)
	}
	return p, nil
}

// SaveCutPlan rewrites a plan's JSON and state (a user note, a confirmation).
func (s *Store) SaveCutPlan(ctx context.Context, p CutPlan) error {
	_, err := s.db.ExecContext(ctx, `UPDATE ai_cut_plans SET state=?, start_ms=?, end_ms=?, plan_json=?, confirmed=?, updated_at=? WHERE id=?`,
		p.State, p.StartMs, p.EndMs, string(p.PlanJSON), p.Confirmed, now(), p.ID)
	return err
}

const cutColumns = `id, run_id, session_id, candidate_ids_json, version, COALESCE(supersedes,''), state, start_ms, end_ms,
	plan_json, confirmed, created_at, updated_at`

var cutListColumns = replaceOnce(cutColumns, "plan_json", "''")

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

func scanCutPlan(row interface{ Scan(...any) error }) (CutPlan, error) {
	var p CutPlan
	var ids, plan, created, updated string
	var confirmed int
	err := row.Scan(&p.ID, &p.RunID, &p.SessionID, &ids, &p.Version, &p.Supersedes, &p.State, &p.StartMs, &p.EndMs,
		&plan, &confirmed, &created, &updated)
	json.Unmarshal([]byte(ids), &p.CandidateIDs)
	p.PlanJSON = []byte(plan)
	p.Confirmed = confirmed != 0
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return p, err
}

// CutPlan returns one plan with its JSON.
func (s *Store) CutPlan(ctx context.Context, id string) (CutPlan, error) {
	p, err := scanCutPlan(s.db.QueryRowContext(ctx, "SELECT "+cutColumns+" FROM ai_cut_plans WHERE id=?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.NewUserError("NOT_FOUND", "컷 분석 결과를 찾을 수 없습니다.")
	}
	return p, err
}

// CutPlans lists a session's plans, newest first, without their JSON.
func (s *Store) CutPlans(ctx context.Context, sid string) ([]CutPlan, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+cutListColumns+` FROM ai_cut_plans WHERE session_id=?
		ORDER BY created_at DESC LIMIT 500`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CutPlan
	for rows.Next() {
		p, err := scanCutPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// CurrentCutPlans are the newest plan of each candidate set, with JSON, in time order.
func (s *Store) CurrentCutPlans(ctx context.Context, sid string) ([]CutPlan, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+cutColumns+` FROM ai_cut_plans p WHERE session_id=? AND version =
		(SELECT MAX(version) FROM ai_cut_plans q WHERE q.session_id=p.session_id AND q.candidate_ids_json=p.candidate_ids_json)
		ORDER BY start_ms, created_at`, sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CutPlan
	for rows.Next() {
		p, err := scanCutPlan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
