-- Phase 5: Claude, batches and cost. A batch run keeps its provider batch
-- ID so results can be fetched after a restart; totals are kept as columns
-- so runs can be compared without reading every usage entry.
ALTER TABLE ai_runs ADD COLUMN delivery TEXT NOT NULL DEFAULT 'single';  -- single | batch
ALTER TABLE ai_runs ADD COLUMN batch_id TEXT;                            -- provider batch ID once submitted
ALTER TABLE ai_runs ADD COLUMN request_count INTEGER NOT NULL DEFAULT 0; -- requests in the confirmed plan
ALTER TABLE ai_runs ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;      -- uncached input
ALTER TABLE ai_runs ADD COLUMN cache_read_tokens INTEGER NOT NULL DEFAULT 0; -- prompt cache hits
ALTER TABLE ai_runs ADD COLUMN cache_write_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_runs ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE ai_runs ADD COLUMN cost_usd REAL;                            -- at list price; NULL when the price is unknown
ALTER TABLE ai_runs ADD COLUMN cost_cap_usd REAL;                        -- the user's limit, if any
