-- Phase 6-A: what the broadcaster said, as evidence for the cut analysis.
-- Filled by local speech recognition or a caption file (6-B); the sample
-- session seeds synthetic utterances.
CREATE TABLE transcripts (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    source        TEXT NOT NULL,              -- asr | file | demo
    engine        TEXT NOT NULL,              -- applespeech | whispercpp | '' for files
    model         TEXT NOT NULL,
    coverage_json TEXT NOT NULL,              -- [{start_ms,end_ms}] transcribed spans
    created_at    TEXT NOT NULL
) STRICT;
CREATE INDEX transcripts_session ON transcripts (session_id, created_at);

CREATE TABLE utterances (
    id            TEXT PRIMARY KEY,           -- stable per session: <transcript id>-<seq>
    transcript_id TEXT NOT NULL REFERENCES transcripts(id) ON DELETE CASCADE,
    session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    seq           INTEGER NOT NULL,
    start_ms      INTEGER NOT NULL,
    end_ms        INTEGER NOT NULL,
    speaker_role  TEXT NOT NULL,              -- 방송자, 게스트 1, …; never a name
    text          TEXT NOT NULL,
    confidence    REAL                        -- NULL when the source gives none
) STRICT;
CREATE INDEX utterances_time ON utterances (session_id, start_ms);
