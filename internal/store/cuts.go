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
	// AnalysisID is the rule analysis the candidate IDs belong to ("" none);
	// Lineage groups the versions of one moment's plan (a new lineage is the
	// plan's own ID; plans from before migration 0012 use their candidate IDs).
	AnalysisID string
	Lineage    string
	Version    int
	Supersedes string
	State      string // ok | verify_failed | failed
	StartMs    int64
	EndMs      int64
	PlanJSON   []byte // empty when listed
	Confirmed  bool
	// The user's decisions: notes (JSON list, owned by the caller), the
	// confirmed boundary candidate and when the plan was confirmed.
	UserNotesJSON     []byte
	ConfirmedBoundary string
	ConfirmedAt       time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// CreateCutPlan stores a plan as the next version of its lineage (a new
// lineage when Lineage is empty) and returns it with its ID, lineage,
// version and the plan it supersedes.
func (s *Store) CreateCutPlan(ctx context.Context, p CutPlan) (CutPlan, error) {
	ids, _ := json.Marshal(p.CandidateIDs)
	p.ID = newID()
	if p.Lineage == "" {
		p.Lineage = p.ID
	}
	var version int
	var prev sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0), (SELECT id FROM ai_cut_plans WHERE session_id=? AND lineage=?
		ORDER BY version DESC LIMIT 1) FROM ai_cut_plans WHERE session_id=? AND lineage=?`,
		p.SessionID, p.Lineage, p.SessionID, p.Lineage).Scan(&version, &prev)
	if err != nil {
		return p, fmt.Errorf("cut plan version: %w", err)
	}
	p.Version, p.Supersedes = version+1, prev.String
	p.CreatedAt = time.Now().UTC()
	p.UpdatedAt = p.CreatedAt
	_, err = s.db.ExecContext(ctx, `INSERT INTO ai_cut_plans (id, run_id, session_id, candidate_ids_json, analysis_id, lineage, version,
		supersedes, state, start_ms, end_ms, plan_json, confirmed, created_at, updated_at) VALUES (?,?,?,?,?,?,?,NULLIF(?,''),?,?,?,?,?,?,?)`,
		p.ID, p.RunID, p.SessionID, string(ids), p.AnalysisID, p.Lineage, p.Version, p.Supersedes, p.State, p.StartMs, p.EndMs,
		string(p.PlanJSON), p.Confirmed, p.CreatedAt.Format(time.RFC3339Nano), p.UpdatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return p, fmt.Errorf("create cut plan: %w", err)
	}
	return p, nil
}

// SaveCutPlan rewrites a plan's JSON, state and the user's decisions (a
// note, a confirmation).
func (s *Store) SaveCutPlan(ctx context.Context, p CutPlan) error {
	notes := string(p.UserNotesJSON)
	if notes == "" {
		notes = "[]"
	}
	var confirmedAt any
	if !p.ConfirmedAt.IsZero() {
		confirmedAt = p.ConfirmedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE ai_cut_plans SET state=?, start_ms=?, end_ms=?, plan_json=?, confirmed=?, user_notes_json=?,
		confirmed_boundary=?, confirmed_at=?, updated_at=? WHERE id=?`,
		p.State, p.StartMs, p.EndMs, string(p.PlanJSON), p.Confirmed, notes, p.ConfirmedBoundary, confirmedAt, now(), p.ID)
	return err
}

const cutColumns = `id, run_id, session_id, candidate_ids_json, analysis_id, lineage, version, COALESCE(supersedes,''), state, start_ms, end_ms,
	plan_json, confirmed, user_notes_json, confirmed_boundary, COALESCE(confirmed_at,''), created_at, updated_at`

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
	var ids, plan, notes, confirmedAt, created, updated string
	var confirmed int
	err := row.Scan(&p.ID, &p.RunID, &p.SessionID, &ids, &p.AnalysisID, &p.Lineage, &p.Version, &p.Supersedes, &p.State, &p.StartMs, &p.EndMs,
		&plan, &confirmed, &notes, &p.ConfirmedBoundary, &confirmedAt, &created, &updated)
	json.Unmarshal([]byte(ids), &p.CandidateIDs)
	p.PlanJSON = []byte(plan)
	p.UserNotesJSON = []byte(notes)
	p.Confirmed = confirmed != 0
	p.ConfirmedAt, _ = time.Parse(time.RFC3339Nano, confirmedAt)
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

// CutPlans lists a session's plans, newest first, with their JSON only
// when asked (listing screens do not need it).
func (s *Store) CutPlans(ctx context.Context, sid string, withJSON bool) ([]CutPlan, error) {
	cols := cutListColumns
	if withJSON {
		cols = cutColumns
	}
	return s.cutPlanRows(ctx, "SELECT "+cols+` FROM ai_cut_plans WHERE session_id=? ORDER BY created_at DESC LIMIT 500`, sid)
}

// CurrentCutPlans are, for each lineage, the confirmed plan (the newest
// confirmed version) and the newest plan when it is a later version, with
// JSON, in time order. Without a confirmation it is the newest alone.
func (s *Store) CurrentCutPlans(ctx context.Context, sid string) ([]CutPlan, error) {
	return s.cutPlanRows(ctx, "SELECT "+cutColumns+` FROM ai_cut_plans p WHERE session_id=? AND (
		version = (SELECT MAX(version) FROM ai_cut_plans q WHERE q.session_id=p.session_id AND q.lineage=p.lineage)
		OR id = (SELECT id FROM ai_cut_plans q WHERE q.session_id=p.session_id AND q.lineage=p.lineage
			AND q.confirmed=1 ORDER BY version DESC LIMIT 1))
		ORDER BY start_ms, version`, sid)
}

// ConfirmedCutPlans are the session's confirmed plans, one per lineage (its
// newest confirmed version), with JSON, in time order.
func (s *Store) ConfirmedCutPlans(ctx context.Context, sid string) ([]CutPlan, error) {
	return s.cutPlanRows(ctx, "SELECT "+cutColumns+` FROM ai_cut_plans p WHERE session_id=? AND confirmed=1 AND version =
		(SELECT MAX(version) FROM ai_cut_plans q WHERE q.session_id=p.session_id AND q.lineage=p.lineage AND q.confirmed=1)
		ORDER BY start_ms, created_at`, sid)
}

// CutPlanLineage is every version of one lineage's plan, oldest first,
// with JSON.
func (s *Store) CutPlanLineage(ctx context.Context, sid, lineage string) ([]CutPlan, error) {
	return s.cutPlanRows(ctx, "SELECT "+cutColumns+` FROM ai_cut_plans WHERE session_id=? AND lineage=? ORDER BY version`, sid, lineage)
}

func (s *Store) cutPlanRows(ctx context.Context, query string, args ...any) ([]CutPlan, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
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
