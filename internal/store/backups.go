package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// BackupFile is one file the app wrote into a backup bundle.
type BackupFile struct {
	Path   string `json:"path"` // relative to the bundle, with forward slashes
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// Backup is a session's backup bundle (docs/AI-CUT-ANALYSIS.md 2.5).
type Backup struct {
	SessionID       string
	Path            string
	ManifestVersion int
	VideoState      string // complete | partial | ref | none
	Files           []BackupFile
	Bytes           int64
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SaveBackup records (or replaces) a session's bundle; created_at stays.
func (s *Store) SaveBackup(ctx context.Context, b Backup) error {
	if b.Files == nil {
		b.Files = []BackupFile{}
	}
	files, err := json.Marshal(b.Files)
	if err != nil {
		return err
	}
	t := now()
	_, err = s.db.ExecContext(ctx, `INSERT INTO backups (session_id, path, manifest_version, video_state, files_json, bytes, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET path=excluded.path, manifest_version=excluded.manifest_version,
			video_state=excluded.video_state, files_json=excluded.files_json, bytes=excluded.bytes, updated_at=excluded.updated_at`,
		b.SessionID, b.Path, b.ManifestVersion, b.VideoState, string(files), b.Bytes, t, t)
	return err
}

// Backup is the session's bundle, if one was made.
func (s *Store) Backup(ctx context.Context, sid string) (Backup, bool, error) {
	var b Backup
	var files, created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT session_id, path, manifest_version, video_state, files_json, bytes, created_at, updated_at
		FROM backups WHERE session_id=?`, sid).Scan(&b.SessionID, &b.Path, &b.ManifestVersion, &b.VideoState, &files, &b.Bytes, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return b, false, nil
	}
	if err != nil {
		return b, false, err
	}
	if err := json.Unmarshal([]byte(files), &b.Files); err != nil {
		return b, false, err
	}
	b.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	b.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return b, true, nil
}
