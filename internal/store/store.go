// Package store owns the SQLite database: connection settings, schema
// migrations, and queries. It does not analyze data or call external services.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" driver (pure Go, no CGO)
)

const dbFileName = "momentforge.db"

// Store is the application database inside <data dir>/data/.
type Store struct {
	db      *sql.DB
	dataDir string
	counts  *chatCounts // nil for a read-only store
}

// chatCounts caches each session's message count, which Session and
// Sessions would otherwise count on every call (tens of milliseconds for a
// long broadcast). Only the process holding the server lock writes chat,
// and every write goes through SavePage or AppendLive, which update the
// cache as they commit. A read-only store (the MCP server, beside a running
// server) has no cache and always counts.
type chatCounts struct {
	mu  sync.Mutex
	n   map[string]int
	gen map[string]uint64 // per session, bumped by every chat write
}

func newChatCounts() *chatCounts {
	return &chatCounts{n: map[string]int{}, gen: map[string]uint64{}}
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
	s := &Store{db: db, dataDir: dataDir, counts: newChatCounts()}
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

// chatCount returns a session's stored message count.
func (s *Store) chatCount(ctx context.Context, sid string) (int, error) {
	c := s.counts
	var gen uint64
	if c != nil {
		c.mu.Lock()
		n, ok := c.n[sid]
		gen = c.gen[sid]
		c.mu.Unlock()
		if ok {
			return n, nil
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM chat_events WHERE session_id=?`, sid).Scan(&n); err != nil {
		return 0, fmt.Errorf("count chat: %w", err)
	}
	if c != nil {
		c.mu.Lock()
		if c.gen[sid] == gen { // no write committed since gen was read
			c.n[sid] = n
		}
		c.mu.Unlock()
	}
	return n, nil
}

// commitChat commits a transaction that added messages to sid. The commit
// and the cache update happen under the cache lock, so a count read before
// the commit is either cached before it (and then grown by added) or not
// cached at all.
func (s *Store) commitChat(tx *sql.Tx, sid string, added int) error {
	c := s.counts
	if c == nil || added == 0 {
		return tx.Commit()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.gen[sid]++
	err := tx.Commit()
	if n, ok := c.n[sid]; ok && err == nil {
		c.n[sid] = n + added
	} else {
		delete(c.n, sid)
	}
	return err
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
