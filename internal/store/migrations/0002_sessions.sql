-- Phase 2: collected sessions (VOD now, LIVE later), their chat, and analyses.

CREATE TABLE sessions (
    id                   TEXT PRIMARY KEY,
    platform             TEXT NOT NULL,              -- adapter id, or 'demo'
    kind                 TEXT NOT NULL,              -- 'vod' | 'live'
    target_id            TEXT NOT NULL,              -- platform video/channel id
    title                TEXT NOT NULL,
    duration_ms          INTEGER NOT NULL,           -- grows if chat runs past the reported length
    reported_duration_ms INTEGER NOT NULL,
    salt                 BLOB NOT NULL,              -- pseudonym secret; never sent to clients
    state                TEXT NOT NULL,              -- 'collecting' | 'paused' | 'finished'
    cursor               INTEGER NOT NULL DEFAULT 0, -- resume position
    pages                INTEGER NOT NULL DEFAULT 0,
    error_code           TEXT,
    error_message        TEXT,
    hidden               INTEGER NOT NULL DEFAULT 0,
    created_at           TEXT NOT NULL,
    updated_at           TEXT NOT NULL
) STRICT;

-- Original platform responses, for resume and diagnosis. Never sent to clients.
CREATE TABLE raw_pages (
    id          INTEGER PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    cursor      INTEGER NOT NULL,
    digest      TEXT NOT NULL,
    body        BLOB NOT NULL,
    quarantined INTEGER NOT NULL DEFAULT 0,
    received_at TEXT NOT NULL,
    UNIQUE (session_id, cursor, digest)
) STRICT;

CREATE TABLE chat_events (
    seq        INTEGER PRIMARY KEY,                  -- arrival order
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    event_id   TEXT NOT NULL,
    offset_ms  INTEGER NOT NULL,
    author_key TEXT,                                 -- pseudonym; NULL when unknown
    text       TEXT NOT NULL,
    page_id    INTEGER REFERENCES raw_pages(id),
    UNIQUE (session_id, event_id)
) STRICT;
CREATE INDEX chat_events_time ON chat_events (session_id, offset_ms, seq);

-- Spans analysis must not learn from (ambiguous boundaries, disconnects).
CREATE TABLE gaps (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    start_ms   INTEGER NOT NULL,
    end_ms     INTEGER NOT NULL,
    reason     TEXT NOT NULL,
    UNIQUE (session_id, start_ms, end_ms, reason)
) STRICT;

CREATE TABLE analyses (
    id            TEXT PRIMARY KEY,
    session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    created_at    TEXT NOT NULL,
    rules_version TEXT NOT NULL,
    params_json   TEXT NOT NULL,
    duration_ms   INTEGER NOT NULL,                  -- analyzed range [0, duration)
    max_seq       INTEGER NOT NULL,                  -- chat snapshot: events with seq <= max_seq
    event_count   INTEGER NOT NULL,
    data_sha256   TEXT NOT NULL,
    source_state  TEXT NOT NULL,
    windows_json  TEXT NOT NULL
) STRICT;
CREATE INDEX analyses_session ON analyses (session_id, created_at);

CREATE TABLE candidates (
    analysis_id       TEXT NOT NULL REFERENCES analyses(id) ON DELETE CASCADE,
    id                TEXT NOT NULL,
    reaction_start_ms INTEGER NOT NULL,
    reaction_end_ms   INTEGER NOT NULL,
    body_json         TEXT NOT NULL,
    PRIMARY KEY (analysis_id, id)
) STRICT;
