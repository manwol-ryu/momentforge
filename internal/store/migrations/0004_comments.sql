-- Phase 4: viewer recommendations read from VOD comments.

-- One fetch (or one pasted text) of a video's comments.
CREATE TABLE comment_captures (
    id             TEXT PRIMARY KEY,
    session_id     TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    source         TEXT NOT NULL,          -- 'website' | 'manual_paste'
    state          TEXT NOT NULL,          -- 'finished' | 'partial'
    pages          INTEGER NOT NULL,
    reported_count INTEGER,                -- the platform's top-level count; NULL unknown
    issues_json    TEXT NOT NULL,          -- what is missing: page_budget, reply_incomplete, error codes
    parser_version INTEGER NOT NULL,
    created_at     TEXT NOT NULL
) STRICT;
CREATE INDEX comment_captures_session ON comment_captures (session_id, created_at);

CREATE TABLE comments (
    capture_id  TEXT NOT NULL REFERENCES comment_captures(id) ON DELETE CASCADE,
    comment_id  TEXT NOT NULL,
    seq         INTEGER NOT NULL,          -- order received
    parent_id   TEXT,
    author_key  TEXT,                      -- per-session HMAC pseudonym, never the platform id
    content     TEXT NOT NULL,             -- '' when deleted, secret or filtered on the platform
    status      TEXT NOT NULL,             -- parsed | needs_review | no_timestamp | unavailable
    issues_json TEXT NOT NULL,
    PRIMARY KEY (capture_id, comment_id)
) STRICT;

CREATE TABLE viewer_recommendations (
    capture_id  TEXT NOT NULL REFERENCES comment_captures(id) ON DELETE CASCADE,
    id          TEXT NOT NULL,
    comment_id  TEXT NOT NULL,
    start_ms    INTEGER NOT NULL,
    end_ms      INTEGER,                   -- NULL for a single time
    description TEXT NOT NULL,
    PRIMARY KEY (capture_id, id)
) STRICT;
