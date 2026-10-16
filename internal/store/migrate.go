package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

//go:embed migrations/*.sql
var migrationsEmbed embed.FS

var migrationFiles, _ = fs.Sub(migrationsEmbed, "migrations")

type migration struct {
	version int
	name    string
	sql     string
}

// loadMigrations reads NNNN_name.sql files and checks they are numbered 1..N
// without gaps, so a missing or renamed file is caught at startup.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		num, _, ok := strings.Cut(name, "_")
		v, err := strconv.Atoi(num)
		if !ok || err != nil || len(num) != 4 {
			return nil, fmt.Errorf("migration %q: name must be NNNN_description.sql", name)
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", name, err)
		}
		out = append(out, migration{version: v, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %q: expected number %04d", m.name, i+1)
		}
	}
	return out, nil
}

// migrate applies pending migrations, each in its own transaction together
// with the user_version bump. An existing database is copied to backupDir first.
func (s *Store) migrate(ctx context.Context, fsys fs.FS, backupDir string) error {
	all, err := loadMigrations(fsys)
	if err != nil {
		return err
	}
	current, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	if current > len(all) {
		return domain.NewUserError("SCHEMA_TOO_NEW",
			fmt.Sprintf("이 데이터 폴더는 더 새로운 MomentForge(스키마 %d)로 만들어졌습니다. 최신 버전으로 실행하거나 다른 데이터 폴더를 지정하세요.", current))
	}
	if current == len(all) {
		return nil
	}
	if current > 0 {
		if err := s.backup(ctx, backupDir, current); err != nil {
			return err
		}
	}
	for _, m := range all[current:] {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", m.name, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", m.version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("set schema version %d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", m.name, err)
		}
	}
	return nil
}

func (s *Store) backup(ctx context.Context, dir string, version int) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create backup folder: %w", err)
	}
	name := fmt.Sprintf("momentforge-v%d-%s.db", version, time.Now().UTC().Format("20060102T150405Z"))
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", filepath.Join(dir, name)); err != nil {
		return fmt.Errorf("back up database before migration: %w", err)
	}
	return nil
}
