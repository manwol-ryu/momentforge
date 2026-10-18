-- Phase 6-A: the second-generation AI analysis (docs/AI-CUT-ANALYSIS.md).
-- A run now says what kind of analysis it was and keeps a log of what each
-- step sent; a cut plan is one candidate's result.
ALTER TABLE ai_runs ADD COLUMN kind TEXT NOT NULL DEFAULT 'explain';  -- explain (recommendations) | cut (cut plans)
ALTER TABLE ai_runs ADD COLUMN sent_log_json TEXT NOT NULL DEFAULT '[]'; -- per step: candidate, step, evidence refs, bytes

CREATE TABLE ai_cut_plans (
    id                 TEXT PRIMARY KEY,
    run_id             TEXT NOT NULL REFERENCES ai_runs(id) ON DELETE CASCADE,
    session_id         TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    candidate_ids_json TEXT NOT NULL,          -- unified timeline item IDs the plan is about
    version            INTEGER NOT NULL,       -- nth plan for these candidates
    supersedes         TEXT,                   -- the previous plan's id
    state              TEXT NOT NULL,          -- ok | verify_failed | failed
    start_ms           INTEGER NOT NULL,       -- first span start (listing without the plan)
    end_ms             INTEGER NOT NULL,       -- last span end
    plan_json          TEXT NOT NULL,          -- the whole cut.Plan
    confirmed          INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
) STRICT;
CREATE INDEX ai_cut_plans_session ON ai_cut_plans (session_id, created_at);
CREATE INDEX ai_cut_plans_run ON ai_cut_plans (run_id);
