-- Phase 3: LIVE sessions share sessions/chat_events with VOD and add these.

-- LIVE-only message facts (NULL for VOD).
ALTER TABLE chat_events ADD COLUMN received_at_ms INTEGER;              -- when the app received it
ALTER TABLE chat_events ADD COLUMN event_time_ms INTEGER;               -- platform's message time
ALTER TABLE chat_events ADD COLUMN generation INTEGER;                  -- connection number
ALTER TABLE chat_events ADD COLUMN late INTEGER NOT NULL DEFAULT 0;     -- stored but not analyzed
ALTER TABLE chat_events ADD COLUMN time_quality TEXT;                   -- 'provider' | 'invalid'
ALTER TABLE chat_events ADD COLUMN replay_key TEXT;                     -- HMAC of a message, to drop reconnect replays
CREATE INDEX chat_events_replay ON chat_events (session_id, replay_key, generation) WHERE replay_key IS NOT NULL;

CREATE TABLE live_state (
    session_id         TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    channel_id         TEXT NOT NULL,
    live_id            TEXT NOT NULL,           -- broadcast (episode); resume requires the same one
    origin_ms          INTEGER NOT NULL,        -- wall-clock ms of timeline 0
    time_basis         TEXT NOT NULL,           -- 'broadcast_elapsed' | 'capture_elapsed'
    params_json        TEXT NOT NULL,
    viewer_adaptive    INTEGER NOT NULL,
    generation         INTEGER NOT NULL DEFAULT 0,
    watermark_ms       INTEGER NOT NULL DEFAULT 0,
    last_healthy_at_ms INTEGER,
    closed_reason      TEXT,
    viewer_status      TEXT,
    viewer_count       INTEGER,
    late_count         INTEGER NOT NULL DEFAULT 0,
    duplicate_count    INTEGER NOT NULL DEFAULT 0,
    invalid_time_count INTEGER NOT NULL DEFAULT 0,
    revision           INTEGER NOT NULL DEFAULT 0, -- last candidate change
    last_window_json   TEXT
) STRICT;

-- Candidates change state while a broadcast runs; revision orders the changes.
CREATE TABLE live_candidates (
    session_id    TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    id            TEXT NOT NULL,
    revision      INTEGER NOT NULL,
    state         TEXT NOT NULL,
    clip_start_ms INTEGER NOT NULL,
    body_json     TEXT NOT NULL,
    PRIMARY KEY (session_id, id)
) STRICT;

CREATE TABLE viewer_samples (
    seq        INTEGER PRIMARY KEY,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    body_json  TEXT NOT NULL
) STRICT;
