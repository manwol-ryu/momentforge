package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// ServerLock keeps one server per data folder. It holds an exclusive SQLite
// lock on runtime/server.lock, which works the same on every OS and is
// released by the OS if the process dies.
type ServerLock struct {
	db   *sql.DB
	conn *sql.Conn
}

// AcquireServerLock fails with a UserError if another server already uses dataDir.
func AcquireServerLock(ctx context.Context, dataDir string) (*ServerLock, error) {
	dir := filepath.Join(dataDir, "runtime")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create runtime folder: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(filepath.Join(dir, "server.lock"),
		"locking_mode(EXCLUSIVE)", "busy_timeout(0)"))
	if err != nil {
		return nil, fmt.Errorf("open server lock: %w", err)
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("open server lock: %w", err)
	}
	// In EXCLUSIVE locking mode the lock taken by this write is kept until the
	// connection closes.
	err = execAll(ctx, conn,
		stmt{"CREATE TABLE IF NOT EXISTS holder (pid INTEGER NOT NULL)", nil},
		stmt{"DELETE FROM holder", nil},
		stmt{"INSERT INTO holder(pid) VALUES (?)", []any{os.Getpid()}})
	if err != nil {
		conn.Close()
		db.Close()
		return nil, domain.WrapUserError("ALREADY_RUNNING",
			"같은 데이터 폴더를 쓰는 MomentForge가 이미 실행 중입니다. 열려 있는 창을 사용하거나, 먼저 실행한 터미널에서 Ctrl+C로 종료한 뒤 다시 실행하세요.", err)
	}
	return &ServerLock{db: db, conn: conn}, nil
}

type stmt struct {
	query string
	args  []any
}

func execAll(ctx context.Context, conn *sql.Conn, stmts ...stmt) error {
	for _, s := range stmts {
		if _, err := conn.ExecContext(ctx, s.query, s.args...); err != nil {
			return err
		}
	}
	return nil
}

// Release frees the lock.
func (l *ServerLock) Release() error {
	l.conn.Close()
	return l.db.Close()
}
