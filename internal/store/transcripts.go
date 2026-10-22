package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	FixedBy   string       // the AI run that fixed its text ("" none)
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

// AppendUtterances adds utterances to a transcript (a LIVE capture's grows
// as its candidates' ranges are transcribed), their IDs continuing its
// numbering, and sets its coverage. It returns them with their IDs.
func (s *Store) AppendUtterances(ctx context.Context, t Transcript, utts []domain.Utterance, coverage []domain.Gap) ([]domain.Utterance, error) {
	if coverage == nil {
		coverage = []domain.Gap{}
	}
	cov, _ := json.Marshal(coverage)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var last int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM utterances WHERE transcript_id=?`, t.ID).Scan(&last); err != nil {
		return nil, err
	}
	out := slices.Clone(utts)
	for i, u := range out {
		seq := last + i + 1
		out[i].ID = fmt.Sprintf("%s-%d", t.ID, seq)
		var conf any
		if u.Confidence > 0 {
			conf = u.Confidence
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO utterances (id, transcript_id, session_id, seq, start_ms, end_ms, speaker_role, text, confidence)
			VALUES (?,?,?,?,?,?,?,?,?)`, out[i].ID, t.ID, t.SessionID, seq, u.StartMs, u.EndMs, u.Speaker, u.Text, conf); err != nil {
			return nil, fmt.Errorf("insert utterance: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transcripts SET coverage_json=? WHERE id=?`, string(cov), t.ID); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}

// LatestTranscript is the session's newest transcript, if any.
func (s *Store) LatestTranscript(ctx context.Context, sid string) (Transcript, bool, error) {
	var t Transcript
	var cov, created string
	err := s.db.QueryRowContext(ctx, `SELECT id, session_id, source, engine, model, coverage_json, fixed_by, created_at FROM transcripts
		WHERE session_id=? ORDER BY created_at DESC LIMIT 1`, sid).Scan(&t.ID, &t.SessionID, &t.Source, &t.Engine, &t.Model, &cov,
		&t.FixedBy, &created)
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

const utteranceColumns = `id, start_ms, end_ms, speaker_role, text, COALESCE(confidence, 0), COALESCE(raw_text, '')`

func scanUtterances(rows *sql.Rows) ([]domain.Utterance, error) {
	var out []domain.Utterance
	for rows.Next() {
		var u domain.Utterance
		if err := rows.Scan(&u.ID, &u.StartMs, &u.EndMs, &u.Speaker, &u.Text, &u.Confidence, &u.RawText); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// FixUtterances replaces the text of a transcript's utterances (ID → text)
// with an AI run's corrections, keeping what was first recognised, and
// records the run on the transcript. It returns how many lines changed.
func (s *Store) FixUtterances(ctx context.Context, transcriptID, runID string, fixes map[string]string) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	n := 0
	for id, text := range fixes {
		res, err := tx.ExecContext(ctx, `UPDATE utterances SET raw_text=COALESCE(raw_text, text), text=?
			WHERE id=? AND transcript_id=? AND text<>?`, text, id, transcriptID, text)
		if err != nil {
			return 0, fmt.Errorf("fix utterance: %w", err)
		}
		if k, _ := res.RowsAffected(); k > 0 {
			n++
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE transcripts SET fixed_by=? WHERE id=?`, runID, transcriptID); err != nil {
		return 0, err
	}
	return n, tx.Commit()
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

// FixedUtteranceCount is how many of a transcript's utterances AI fixed.
func (s *Store) FixedUtteranceCount(ctx context.Context, transcriptID string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM utterances WHERE transcript_id=? AND raw_text IS NOT NULL", transcriptID).Scan(&n)
	return n, err
}

// SearchUtterances returns, of a transcript's utterances starting in
// [from, to) whose text contains query (letters A–Z match either case), the
// limit latest ones (nearest to to) in time order.
func (s *Store) SearchUtterances(ctx context.Context, transcriptID, query string, from, to int64, limit int) ([]domain.Utterance, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+utteranceColumns+` FROM utterances
		WHERE transcript_id=? AND start_ms>=? AND start_ms<? AND text LIKE ? ESCAPE '\'
		ORDER BY start_ms DESC, seq DESC LIMIT ?`, transcriptID, from, to, "%"+escapeLike(query)+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out, err := scanUtterances(rows)
	slices.Reverse(out)
	return out, err
}

// UtterancePage pages through a transcript's utterances starting in
// [from, to), in time order, keeping those whose text contains query when
// it is not empty. total counts every match.
func (s *Store) UtterancePage(ctx context.Context, transcriptID, query string, from, to int64, offset, limit int) ([]domain.Utterance, int, error) {
	where := "transcript_id=? AND start_ms>=? AND start_ms<?"
	args := []any{transcriptID, from, to}
	if query != "" {
		where += ` AND text LIKE ? ESCAPE '\'`
		args = append(args, "%"+escapeLike(query)+"%")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM utterances WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+utteranceColumns+" FROM utterances WHERE "+where+" ORDER BY start_ms, seq LIMIT ? OFFSET ?",
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	list, err := scanUtterances(rows)
	return list, total, err
}
