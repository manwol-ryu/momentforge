package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// OpenReadOnly opens an existing database for reading only (the MCP
// server). It takes no server lock and never migrates: SQLite opens the
// file read-only and refuses writes, so it can run beside momentforge serve.
func OpenReadOnly(ctx context.Context, dataDir string) (*Store, error) {
	path := filepath.Join(dataDir, "data", dbFileName)
	if _, err := os.Stat(path); err != nil {
		return nil, domain.NewUserError("NO_DATA", fmt.Sprintf("%s에 MomentForge 데이터가 없습니다. --data로 데이터 폴더를 지정하세요.", dataDir))
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// A URI path: forward slashes, a leading slash before a drive letter,
	// and the characters that would end the path escaped.
	uri := filepath.ToSlash(abs)
	if !strings.HasPrefix(uri, "/") {
		uri = "/" + uri
	}
	uri = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(uri)
	db, err := sql.Open("sqlite", "file:"+uri+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	s := &Store{db: db, dataDir: dataDir}
	migs, err := loadMigrations(migrationFiles)
	if err != nil {
		db.Close()
		return nil, err
	}
	v, err := s.SchemaVersion(ctx)
	if err != nil {
		db.Close()
		return nil, err
	}
	switch {
	case v < len(migs):
		db.Close()
		return nil, domain.NewUserError("SCHEMA_OLD", "이전 버전의 데이터입니다. momentforge serve를 한 번 실행해 데이터를 갱신한 뒤 다시 시도하세요.")
	case v > len(migs):
		db.Close()
		return nil, domain.NewUserError("SCHEMA_NEW", "더 새로운 MomentForge가 만든 데이터입니다. 실행파일을 새 버전으로 바꿔 주세요.")
	}
	return s, nil
}

// escapeLike makes s match literally inside a LIKE pattern (ESCAPE '\').
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(s)
}

// SearchChats returns messages in [from, to) whose text contains query
// (letters A–Z match either case) and, when author is set, that this
// pseudonymous author sent; in time order with paging, and the total.
func (s *Store) SearchChats(ctx context.Context, sid, query, author string, from, to int64, limit, offset int) ([]domain.ChatEvent, int, error) {
	const where = `session_id=? AND offset_ms>=? AND offset_ms<? AND text LIKE ? ESCAPE '\' AND (?='' OR author_key=?)`
	pattern := "%" + escapeLike(query) + "%"
	args := []any{sid, from, to, pattern, author, author}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM chat_events WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	chats, err := s.queryChats(ctx, "SELECT "+chatColumns+" FROM chat_events WHERE "+where+
		" ORDER BY offset_ms, seq LIMIT ? OFFSET ?", append(args, limit, offset)...)
	return chats, total, err
}

// ActivityBucket is the chat of one bucketMs-long span.
type ActivityBucket struct {
	Index   int64 // span number from 0 (start = Index × bucket length)
	Chats   int
	Authors int // different pseudonymous authors
}

// ChatActivity counts messages and authors per bucketMs-long span in
// [from, to); spans without messages are left out.
func (s *Store) ChatActivity(ctx context.Context, sid string, bucketMs, from, to int64) ([]ActivityBucket, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT offset_ms/?, count(*), count(DISTINCT author_key) FROM chat_events
		WHERE session_id=? AND offset_ms>=? AND offset_ms<? GROUP BY 1 ORDER BY 1`, bucketMs, sid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActivityBucket
	for rows.Next() {
		var b ActivityBucket
		if err := rows.Scan(&b.Index, &b.Chats, &b.Authors); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
