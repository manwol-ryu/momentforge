-- Phase 6-D: session backup bundles (docs/AI-CUT-ANALYSIS.md 2.5, 2.6).
-- One bundle per session; the files live in the folder at path, and
-- files_json lists what this app wrote there (so an update replaces or
-- removes only its own files).
CREATE TABLE backups (
    session_id       TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
    path             TEXT NOT NULL,                -- the bundle folder (absolute)
    manifest_version INTEGER NOT NULL,
    video_state      TEXT NOT NULL,                -- complete | partial | ref | none
    files_json       TEXT NOT NULL DEFAULT '[]',   -- [{path, bytes, sha256}]
    bytes            INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
) STRICT;
