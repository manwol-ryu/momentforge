-- Phase 4: opt-in AI reviews. A run records exactly what was shown in the
-- preview and sent (plan_json), and what came back after validation.
CREATE TABLE ai_runs (
    id                   TEXT PRIMARY KEY,
    session_id           TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    provider             TEXT NOT NULL,
    model                TEXT NOT NULL,
    fingerprint          TEXT NOT NULL,      -- of the plan the user confirmed
    state                TEXT NOT NULL,      -- running | completed | partial | failed | cancelled
    plan_json            TEXT NOT NULL,      -- prompt, ranges, chunks (pseudonymous chat), omitted
    recommendations_json TEXT NOT NULL,      -- validated recommendations
    usage_json           TEXT NOT NULL,      -- token usage per completed request
    completed_requests   INTEGER NOT NULL DEFAULT 0,
    attempts             INTEGER NOT NULL DEFAULT 0,
    rejected             INTEGER NOT NULL DEFAULT 0,
    error_code           TEXT,
    error_message        TEXT,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
) STRICT;
CREATE INDEX ai_runs_session ON ai_runs (session_id, created_at);
