-- Phase 6-B: chat imported from NAS backups carries donations and
-- subscriptions (docs/AI-CUT-ANALYSIS.md 2.5a), and a session can point at
-- media files on this computer or a mounted share (never copied).
ALTER TABLE chat_events ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat';   -- chat | donation | subscription | system
ALTER TABLE chat_events ADD COLUMN amount INTEGER;                      -- donation: won; subscription: months; NULL otherwise
ALTER TABLE chat_events ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;   -- 1: the platform hid it (bot filter)

CREATE TABLE media_refs (
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,                -- audio | video
    path       TEXT NOT NULL,                -- absolute path as the user gave it (the file stays where it is)
    bytes      INTEGER NOT NULL,
    modified   TEXT NOT NULL,                -- the file's mtime when referenced
    created_at TEXT NOT NULL,
    PRIMARY KEY (session_id, kind)
) STRICT;
