package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

func TestCutPlanVersionsFollowTheirLineage(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sess, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "x", Title: "t", DurationMs: 600_000, Salt: []byte("salt")})
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.CreateAIRun(ctx, AIRun{SessionID: sess.ID, Provider: "p", Model: "m", Kind: AIKindCut, AnalysisID: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AIRun(ctx, run.ID); got.AnalysisID != "a1" {
		t.Fatalf("run analysis %q", got.AnalysisID)
	}
	create := func(lineage string, startMs int64) CutPlan {
		t.Helper()
		p, err := s.CreateCutPlan(ctx, CutPlan{RunID: run.ID, SessionID: sess.ID, CandidateIDs: []string{"rule:m0001"}, AnalysisID: "a1",
			Lineage: lineage, State: "ok", StartMs: startMs, PlanJSON: []byte("{}")})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	// The same candidate ID twice is two moments unless the second continues the first.
	v1 := create("", 100_000)
	other := create("", 300_000)
	v2 := create(v1.Lineage, 100_000)
	if v1.Lineage != v1.ID || v1.Version != 1 || other.Version != 1 || other.Lineage == v1.Lineage ||
		v2.Version != 2 || v2.Supersedes != v1.ID || v2.Lineage != v1.Lineage {
		t.Fatalf("v1 %+v other %+v v2 %+v", v1, other, v2)
	}
	if got, _ := s.CutPlan(ctx, v2.ID); got.AnalysisID != "a1" || got.Lineage != v1.ID {
		t.Fatalf("stored %+v", got)
	}
	current, err := s.CurrentCutPlans(ctx, sess.ID)
	if err != nil || len(current) != 2 || current[0].ID != v2.ID || current[1].ID != other.ID {
		t.Fatalf("current %+v %v", current, err)
	}
	if versions, _ := s.CutPlanLineage(ctx, sess.ID, v1.Lineage); len(versions) != 2 || versions[0].ID != v1.ID || versions[1].ID != v2.ID {
		t.Fatalf("lineage %+v", versions)
	}
	v1.Confirmed = true
	if err := s.SaveCutPlan(ctx, v1); err != nil {
		t.Fatal(err)
	}
	if current, _ = s.CurrentCutPlans(ctx, sess.ID); len(current) != 3 {
		t.Fatalf("the confirmed v1 stays beside v2: %+v", current)
	}
	if confirmed, _ := s.ConfirmedCutPlans(ctx, sess.ID); len(confirmed) != 1 || confirmed[0].ID != v1.ID {
		t.Fatalf("confirmed %+v", confirmed)
	}
}

// Migration 0012 gives earlier runs the analysis that was newest when they
// started, their plans the run's, and keeps chaining their versions by the
// candidate IDs they were made for.
func TestMigrationGivesEarlierResultsTheirAnalysis(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, "m.db")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &Store{db: db, dataDir: dir}
	all, err := loadMigrations(migrationFiles)
	if err != nil {
		t.Fatal(err)
	}
	before := fstest.MapFS{}
	for _, m := range all[:11] {
		before[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	backups := filepath.Join(dir, "backup")
	if err := s.migrate(ctx, before, backups); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	at := func(h int) string { return t0.Add(time.Duration(h) * time.Hour).Format(time.RFC3339Nano) }
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO sessions (id, platform, kind, target_id, title, duration_ms, reported_duration_ms, salt, state, created_at, updated_at)
			VALUES ('s', 'demo', 'vod', 'x', 't', 1, 1, x'00', 'finished', ?, ?)`, []any{at(0), at(0)}},
		{`INSERT INTO analyses (id, session_id, created_at, rules_version, params_json, duration_ms, max_seq, event_count, data_sha256,
			source_state, windows_json) VALUES ('a1', 's', ?, '1', '{}', 1, 0, 0, '', 'finished', '[]'), ('a2', 's', ?, '1', '{}', 1, 0, 0, '', 'finished', '[]')`,
			[]any{at(1), at(3)}},
		{`INSERT INTO ai_runs (id, session_id, provider, model, fingerprint, state, plan_json, recommendations_json, usage_json, created_at, updated_at)
			VALUES ('r0', 's', 'p', 'm', '', 'completed', '{}', '[]', '[]', ?, ?), ('r1', 's', 'p', 'm', '', 'completed', '{}', '[]', '[]', ?, ?),
			('r2', 's', 'p', 'm', '', 'completed', '{}', '[]', '[]', ?, ?)`, []any{at(0), at(0), at(2), at(2), at(4), at(4)}},
		{`INSERT INTO ai_cut_plans (id, run_id, session_id, candidate_ids_json, version, supersedes, state, start_ms, end_ms, plan_json,
			created_at, updated_at) VALUES ('p1', 'r1', 's', '["rule:m0001"]', 1, NULL, 'ok', 0, 1, '{}', ?, ?),
			('p2', 'r2', 's', '["rule:m0001"]', 2, 'p1', 'ok', 0, 1, '{}', ?, ?)`, []any{at(2), at(2), at(4), at(4)}},
	} {
		if _, err := db.ExecContext(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
	if err := s.migrate(ctx, migrationFiles, backups); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]string{"r0": "", "r1": "a1", "r2": "a2"} {
		if r, err := s.AIRun(ctx, id); err != nil || r.AnalysisID != want {
			t.Errorf("run %s analysis %q (%v), want %q", id, r.AnalysisID, err, want)
		}
	}
	versions, err := s.CutPlanLineage(ctx, "s", `["rule:m0001"]`)
	if err != nil || len(versions) != 2 || versions[0].AnalysisID != "a1" || versions[1].AnalysisID != "a2" {
		t.Fatalf("plans %+v %v", versions, err)
	}
	// A new version of that lineage comes after them.
	p, err := s.CreateCutPlan(ctx, CutPlan{RunID: "r2", SessionID: "s", CandidateIDs: []string{"rule:m0002"}, Lineage: versions[0].Lineage, State: "ok"})
	if err != nil || p.Version != 3 || p.Supersedes != "p2" {
		t.Fatalf("next %+v %v", p, err)
	}
}

func TestSearchUtterancesReturnsTheLatestInTimeOrder(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	sess, err := s.CreateSession(ctx, NewSession{Platform: "demo", Kind: "vod", TargetID: "x", Title: "t", DurationMs: 600_000, Salt: []byte("salt")})
	if err != nil {
		t.Fatal(err)
	}
	tr, err := s.SaveTranscript(ctx, Transcript{SessionID: sess.ID, Source: "demo"}, []domain.Utterance{
		{StartMs: 1000, Text: "벌칙 하나"}, {StartMs: 2000, Text: "다른 말"}, {StartMs: 3000, Text: "벌칙 둘"}, {StartMs: 4000, Text: "벌칙 셋"},
		{StartMs: 9000, Text: "벌칙 넷 (이후)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.SearchUtterances(ctx, tr.ID, "벌칙", 0, 5000, 2)
	if err != nil || len(got) != 2 || got[0].Text != "벌칙 둘" || got[1].Text != "벌칙 셋" {
		t.Fatalf("search %+v %v", got, err)
	}
}
