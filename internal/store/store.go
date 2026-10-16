// Package store owns the SQLite database: connection settings, schema
// migrations, and queries. It does not analyze data or call external services.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

const dbFileName = "momentforge.db"

// Store is the application database inside <data dir>/data/.
type Store struct {
	db      *sql.DB
	dataDir string
}

// Open opens (creating if needed) the database under dataDir and applies
// pending migrations. An existing database is backed up before upgrading.
func Open(ctx context.Context, dataDir string, appVersion string) (*Store, error) {
	dir := filepath.Join(dataDir, "data")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data folder: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(filepath.Join(dir, dbFileName),
		"journal_mode(WAL)", "busy_timeout(5000)", "foreign_keys(1)", "synchronous(NORMAL)"))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db, dataDir: dataDir}
	if err := s.migrate(ctx, migrationFiles, filepath.Join(dir, "backup")); err != nil {
		db.Close()
		return nil, err
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO app_meta(key, value) VALUES ('created_at', ?), ('created_by_version', ?)
		 ON CONFLICT(key) DO NOTHING`,
		time.Now().UTC().Format(time.RFC3339), appVersion); err != nil {
		db.Close()
		return nil, fmt.Errorf("record app meta: %w", err)
	}
	return s, nil
}

// SchemaVersion reports the applied migration number.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	if err := s.db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// dsn builds a modernc.org/sqlite data source name with _pragma parameters.
func dsn(path string, pragmas ...string) string {
	out := path
	for i, p := range pragmas {
		if i == 0 {
			out += "?"
		} else {
			out += "&"
		}
		out += "_pragma=" + p
	}
	return out
}
