-- Phase 6-D: the user's decisions on cut plans (docs/AI-CUT-ANALYSIS.md 6.3).
-- Notes left on a version go into the next analysis of the same candidates;
-- a confirmed version stays the current one until another is confirmed, and
-- the confirmed boundary candidate (short | context | full_event) is what the
-- confirmed exports cut by.
ALTER TABLE ai_cut_plans ADD COLUMN user_notes_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE ai_cut_plans ADD COLUMN confirmed_boundary TEXT NOT NULL DEFAULT '';
ALTER TABLE ai_cut_plans ADD COLUMN confirmed_at TEXT;
CREATE INDEX ai_cut_plans_set ON ai_cut_plans (session_id, candidate_ids_json, version);
