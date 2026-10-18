package store

import (
	"context"
	"time"
)

// MediaRef points at a session's media file where it already is (a NAS
// share, a local folder); nothing is copied.
type MediaRef struct {
	Kind     string // audio | video
	Path     string
	Bytes    int64
	Modified time.Time
}

// SetMediaRef records (or replaces) a session's media file of one kind.
func (s *Store) SetMediaRef(ctx context.Context, sid string, m MediaRef) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO media_refs (session_id, kind, path, bytes, modified, created_at) VALUES (?,?,?,?,?,?)
		ON CONFLICT(session_id, kind) DO UPDATE SET path=excluded.path, bytes=excluded.bytes, modified=excluded.modified`,
		sid, m.Kind, m.Path, m.Bytes, m.Modified.UTC().Format(time.RFC3339Nano), now())
	return err
}

// MediaRefs lists a session's media files.
func (s *Store) MediaRefs(ctx context.Context, sid string) ([]MediaRef, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT kind, path, bytes, modified FROM media_refs WHERE session_id=? ORDER BY kind", sid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MediaRef
	for rows.Next() {
		var m MediaRef
		var modified string
		if err := rows.Scan(&m.Kind, &m.Path, &m.Bytes, &modified); err != nil {
			return nil, err
		}
		m.Modified, _ = time.Parse(time.RFC3339Nano, modified)
		out = append(out, m)
	}
	return out, rows.Err()
}
