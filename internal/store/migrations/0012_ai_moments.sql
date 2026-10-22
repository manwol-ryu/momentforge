-- AI results follow their moment when a session is analysed again
-- (docs/AI-CUT-ANALYSIS.md 4.5). Rule candidate IDs are serial (m0001… in
-- time order) and change with every analysis, so a run and a cut plan
-- record which analysis their candidate IDs belong to; the timeline keeps
-- them where they are while that analysis is current and finds their
-- candidate again by time otherwise. The versions of one moment's cut plan
-- share a lineage instead of the candidate ID string.
ALTER TABLE ai_runs ADD COLUMN analysis_id TEXT NOT NULL DEFAULT '';      -- the analysis the inputs' rule IDs name ('' none: LIVE, comments only)
ALTER TABLE ai_cut_plans ADD COLUMN analysis_id TEXT NOT NULL DEFAULT ''; -- the run's
ALTER TABLE ai_cut_plans ADD COLUMN lineage TEXT NOT NULL DEFAULT '';     -- versions of one moment's plan share it

-- Earlier runs used the session's newest analysis when they were started;
-- earlier plans chained versions by their candidate IDs.
UPDATE ai_runs SET analysis_id = COALESCE((SELECT a.id FROM analyses a WHERE a.session_id = ai_runs.session_id
    AND a.created_at <= ai_runs.created_at ORDER BY a.created_at DESC LIMIT 1), '');
UPDATE ai_cut_plans SET analysis_id = COALESCE((SELECT r.analysis_id FROM ai_runs r WHERE r.id = ai_cut_plans.run_id), ''),
    lineage = candidate_ids_json;
CREATE INDEX ai_cut_plans_lineage ON ai_cut_plans (session_id, lineage, version);
DROP INDEX ai_cut_plans_set; -- (session_id, candidate_ids_json, version): no query groups by the IDs any more

-- Utterance reads filter by transcript (the cut analysis, the transcript tab).
CREATE INDEX utterances_transcript ON utterances (transcript_id, start_ms);
