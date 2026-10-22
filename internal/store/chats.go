package store

import (
	"context"
	"fmt"
	"math"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// AllSeq means "no snapshot limit" for chat queries.
const AllSeq = math.MaxInt64

const chatColumns = "event_id, seq, offset_ms, COALESCE(author_key,''), text, kind, COALESCE(amount,0), hidden"

func (s *Store) queryChats(ctx context.Context, q string, args ...any) ([]domain.ChatEvent, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("read chat: %w", err)
	}
	defer rows.Close()
	var out []domain.ChatEvent
	for rows.Next() {
		var e domain.ChatEvent
		var hidden int
		if err := rows.Scan(&e.ID, &e.Seq, &e.OffsetMs, &e.AuthorKey, &e.Text, &e.Kind, &e.Amount, &hidden); err != nil {
			return nil, fmt.Errorf("read chat: %w", err)
		}
		e.Hidden = hidden != 0
		if e.Kind == domain.ChatKindChat {
			e.Kind = ""
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ChatsIn returns a session's chat with offsets in [from, to), in time order.
func (s *Store) ChatsIn(ctx context.Context, sid string, from, to int64) ([]domain.ChatEvent, error) {
	return s.queryChats(ctx, "SELECT "+chatColumns+" FROM chat_events WHERE session_id=? AND offset_ms>=? AND offset_ms<? ORDER BY offset_ms, seq",
		sid, from, to)
}

// Chats returns a session's chat with seq <= maxSeq, in time order
// (byArrival: in arrival order, as chat.received.txt).
func (s *Store) Chats(ctx context.Context, sid string, maxSeq int64, byArrival bool) ([]domain.ChatEvent, error) {
	order := "offset_ms, seq"
	if byArrival {
		order = "seq"
	}
	return s.queryChats(ctx, "SELECT "+chatColumns+" FROM chat_events WHERE session_id=? AND seq<=? ORDER BY "+order, sid, maxSeq)
}

// ChatsBetween returns [from, to) in time order with paging, and the total.
func (s *Store) ChatsBetween(ctx context.Context, sid string, from, to, maxSeq int64, limit, offset int) ([]domain.ChatEvent, int, error) {
	const where = "session_id=? AND offset_ms>=? AND offset_ms<? AND seq<=?"
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM chat_events WHERE "+where, sid, from, to, maxSeq).Scan(&total); err != nil {
		return nil, 0, err
	}
	chats, err := s.queryChats(ctx, "SELECT "+chatColumns+" FROM chat_events WHERE "+where+
		" ORDER BY offset_ms, seq LIMIT ? OFFSET ?", sid, from, to, maxSeq, limit, offset)
	return chats, total, err
}

// ChatsAround returns up to perSide messages on each side of at within
// ±span, so a burst on one side cannot hide the messages at the playhead.
// truncated reports that either side had more.
func (s *Store) ChatsAround(ctx context.Context, sid string, at, span, end, maxSeq int64, perSide int) (chats []domain.ChatEvent, truncated bool, err error) {
	before, err := s.queryChats(ctx, "SELECT "+chatColumns+` FROM chat_events
		WHERE session_id=? AND seq<=? AND offset_ms>=? AND offset_ms<=? AND offset_ms<?
		ORDER BY offset_ms DESC, seq DESC LIMIT ?`, sid, maxSeq, max(0, at-span), at, end, perSide+1)
	if err != nil {
		return nil, false, err
	}
	after, err := s.queryChats(ctx, "SELECT "+chatColumns+` FROM chat_events
		WHERE session_id=? AND seq<=? AND offset_ms>? AND offset_ms<?
		ORDER BY offset_ms, seq LIMIT ?`, sid, maxSeq, at, min(end, at+span), perSide+1)
	if err != nil {
		return nil, false, err
	}
	truncated = len(before) > perSide || len(after) > perSide
	before, after = before[:min(len(before), perSide)], after[:min(len(after), perSide)]
	for i := len(before) - 1; i >= 0; i-- {
		chats = append(chats, before[i])
	}
	return append(chats, after...), truncated, nil
}
