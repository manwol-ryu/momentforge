package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Transcript is one transcription of a session's broadcast audio (or a
// caption file the user added).
type Transcript struct {
	ID        string
	SessionID string
	Source    string // asr | file | demo
	Engine    string
	Model     string
	Coverage  []domain.Gap // transcribed spans
	CreatedAt time.Time
}

// SaveTranscript stores a transcript and its utterances in one transaction
// and returns the transcript with its ID. Utterance IDs are
// "<transcript id>-<seq>", so a ref names one utterance for good.
func (s *Store) SaveTranscript(ctx context.Context, t Transcript, utts []domain.Utterance) (Transcript, error) {
	t.ID = newID()
	t.CreatedAt = time.Now().UTC()
	if t.Coverage == nil {
		t.Coverage = []domain.Gap{}
	}
	cov, _ := json.Marshal(t.Coverage)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return t, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO transcripts (id, session_id, source, engine, model, coverage_json, created_at)
		VALUES (?,?,?,?,?,?,?)`, t.ID, t.SessionID, t.Source, t.Engine, t.Model, string(cov), t.CreatedAt.Format(time.RFC3339Nano)); err != nil {
		return t, fmt.Errorf("create transcript: %w", err)
	}
	for i, u := range utts {
		id := fmt.Sprintf("%s-%d", t.ID, i+1)
		var conf any
		if u.Confidence > 0 {
			conf = u.Confidence
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO utterances (id, transcript_id, session_id, seq, start_ms, end_ms, speaker_role, text, confidence)
			VALUES (?,?,?,?,?,?,?,?,?)`, id, t.ID, t.SessionID, i+1, u.StartMs, u.EndMs, u.Speaker, u.Text, conf); err != nil {
			return t, fmt.Errorf("insert utterance: %w", err)
		}
	}
	return t, tx.Commit()
}

// LatestTranscript is the session's newest transcript, if any.
func (s *Store) LatestTranscript(ctx context.Context, sid string) (Transcript, bool, error) {
	var t Transcript
	var cov, created string
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, source, engine, model, coverage_json, created_at FROM transcripts
		WHERE session_id=? ORDER BY created_at DESC LIMIT 1`, sid).Scan(&t.ID, &t.SessionID, &t.Source, &t.Engine, &t.Model, &cov, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return t, false, nil
	}
	if err != nil {
		return t, false, err
	}
	json.Unmarshal([]byte(cov), &t.Coverage)
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	return t, true, nil
}

const utteranceColumns = `id, start_ms, end_ms, speaker_role, text, COALESCE(confidence, 0)`

func scanUtterances(rows *sql.Rows) ([]domain.Utterance, error) {
	var out []domain.Utterance
	for rows.Next() {
		var u domain.Utterance
		if err := rows.Scan(&u.ID, &u.StartMs, &u.EndMs, &u.Speaker, &u.Text, &u.Confidence); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UtterancesBetween returns a transcript's utterances starting in [from, to), in time order.
func (s *Store) UtterancesBetween(ctx context.Context, transcriptID string, from, to int64, limit int) ([]domain.Utterance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+utteranceColumns+` FROM utterances WHERE transcript_id=? AND start_ms>=? AND start_ms<?
		ORDER BY start_ms, seq LIMIT ?`, transcriptID, from, to, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUtterances(rows)
}

// UtteranceCount is how many utterances a transcript holds.
func (s *Store) UtteranceCount(ctx context.Context, transcriptID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM utterances WHERE transcript_id=?", transcriptID).Scan(&n)
	return n, err
}

// SearchUtterances returns a transcript's utterances starting in [from, to)
// whose text contains query (letters A–Z match either case), in time order.
func (s *Store) SearchUtterances(ctx context.Context, transcriptID, query string, from, to int64, limit int) ([]domain.Utterance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+utteranceColumns+` FROM utterances
		WHERE transcript_id=? AND start_ms>=? AND start_ms<? AND text LIKE ? ESCAPE '\'
		ORDER BY start_ms, seq LIMIT ?`, transcriptID, from, to, "%"+escapeLike(query)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUtterances(rows)
}
