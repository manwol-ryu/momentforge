package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

func TestOpenAppliesMigrations(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, "test")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		t.Fatal(err)
	}
	all, _ := loadMigrations(migrationFiles)
	if v != len(all) {
		t.Fatalf("schema version = %d, want %d", v, len(all))
	}
	var mode string
	if err := s.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q (%v), want wal", mode, err)
	}
}

func TestReopenKeepsDataAndSkipsBackup(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, "v1")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(ctx, dir, "v2")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var by string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM app_meta WHERE key='created_by_version'").Scan(&by); err != nil || by != "v1" {
		t.Fatalf("created_by_version = %q (%v), want v1", by, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data", "backup")); !os.IsNotExist(err) {
		t.Fatalf("backup folder should not exist when nothing was migrated: %v", err)
	}
}

func TestMigrateBacksUpExistingDatabase(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, "v1")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The real migrations plus one more, as a future release would ship.
	all, _ := loadMigrations(migrationFiles)
	next := fstest.MapFS{}
	for _, m := range all {
		next[m.name] = &fstest.MapFile{Data: []byte(m.sql)}
	}
	next[fmt.Sprintf("%04d_extra.sql", len(all)+1)] = &fstest.MapFile{Data: []byte("CREATE TABLE extra (id INTEGER PRIMARY KEY);")}
	backups := filepath.Join(dir, "data", "backup")
	if err := s.migrate(ctx, next, backups); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if v, _ := s.SchemaVersion(ctx); v != len(all)+1 {
		t.Fatalf("schema version = %d, want %d", v, len(all)+1)
	}
	files, _ := os.ReadDir(backups)
	if len(files) != 1 {
		t.Fatalf("backups = %d, want 1", len(files))
	}
}

func TestLoadMigrationsRejectsGaps(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"gap":      {"0001_a.sql": {Data: []byte("")}, "0003_c.sql": {Data: []byte("")}},
		"bad name": {"1_a.sql": {Data: []byte("")}},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := loadMigrations(fsys); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSchemaTooNew(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "PRAGMA user_version = 99"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	_, err = Open(ctx, dir, "v1")
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "SCHEMA_TOO_NEW" {
		t.Fatalf("err = %v, want SCHEMA_TOO_NEW", err)
	}
}

func TestServerLockIsExclusive(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first, err := AcquireServerLock(ctx, dir)
	if err != nil {
		t.Fatalf("first lock: %v", err)
	}
	_, err = AcquireServerLock(ctx, dir)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "ALREADY_RUNNING" {
		t.Fatalf("second lock err = %v, want ALREADY_RUNNING", err)
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	again, err := AcquireServerLock(ctx, dir)
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	again.Release()
}
