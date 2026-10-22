-- AI work that runs by itself once candidates are found (일반 설정,
-- docs/REDESIGN.md 15, 2026-10-22). A run says who started it, so one
-- session's automatic work is held to the limits the user set; a transcript
-- fixed by AI keeps what speech recognition heard.
ALTER TABLE ai_runs ADD COLUMN origin TEXT NOT NULL DEFAULT 'user'; -- user | auto
ALTER TABLE utterances ADD COLUMN raw_text TEXT;                     -- the recognised text when AI changed it (NULL: unchanged)
ALTER TABLE transcripts ADD COLUMN fixed_by TEXT NOT NULL DEFAULT ''; -- the AI run that fixed it ('' none)
