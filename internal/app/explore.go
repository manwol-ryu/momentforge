package app

import (
	"context"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/platform"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// Read-only use cases for the MCP server: they only read the database, so
// they work on a store opened read-only beside a running server.

// Explore limits.
const (
	exploreMaxPage  = 200
	exploreMaxQuery = 100
)

// ExploreSessions lists sessions newest first; hidden ones only when asked.
func (a *App) ExploreSessions(ctx context.Context, includeHidden bool) ([]SessionInfo, error) {
	list, err := a.cfg.Store.Sessions(ctx, includeHidden)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, a.info(s))
	}
	return out, nil
}

func checkPage(offset, limit int) error {
	if offset < 0 || limit < 1 || limit > exploreMaxPage {
		return domain.NewUserError("INPUT", "limit은 1~200, cursor는 이전 응답의 next_cursor를 쓰세요.")
	}
	return nil
}

// SearchChats finds messages in [fromMs, toMs) containing query and/or
// sent by one pseudonymous author, in time order.
func (a *App) SearchChats(ctx context.Context, sid, query, author string, fromMs, toMs int64, offset, limit int) ([]domain.ChatEvent, int, error) {
	query, author = strings.TrimSpace(query), strings.TrimSpace(author)
	if (query == "" && author == "") || utf8.RuneCountInString(query) > exploreMaxQuery || len(author) > exploreMaxQuery {
		return nil, 0, domain.NewUserError("INPUT", "검색어(1~100자)나 작성자 가명 중 하나 이상을 입력하세요.")
	}
	if err := checkPage(offset, limit); err != nil {
		return nil, 0, err
	}
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, 0, err
	}
	return a.cfg.Store.SearchChats(ctx, sid, query, author, fromMs, toMs, limit, offset)
}

// ChatsInRange pages through the messages in [fromMs, toMs).
func (a *App) ChatsInRange(ctx context.Context, sid string, fromMs, toMs int64, offset, limit int) ([]domain.ChatEvent, int, error) {
	if err := checkPage(offset, limit); err != nil {
		return nil, 0, err
	}
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, 0, err
	}
	return a.cfg.Store.ChatsBetween(ctx, sid, fromMs, toMs, store.AllSeq, limit, offset)
}

// Moment is a candidate from any source: a rule candidate (VOD or LIVE), a
// viewer's comment, or an AI recommendation.
type Moment struct {
	ID          string // as in the unified timeline: source:origin
	Source      string // rule | viewer_comment | ai
	StartMs     int64
	EndMs       int64
	AnchorMs    int64
	Title       string
	Reasons     []string          // rule candidates
	Peak        *domain.PeakStats // rule candidates
	EvidenceIDs []string          // rule candidates: the chats that triggered it
	RunID       string            // AI recommendations: the review that made it
	CommentText string            // viewer recommendations
	Rationale   string            // AI recommendations
	Uncertainty string            // AI recommendations
	Priority    string            // AI recommendations
}

// Moments returns a session's candidates in time order: the unified
// timeline of a VOD, or the rule candidates of a LIVE capture.
func (a *App) Moments(ctx context.Context, sid string) ([]Moment, error) {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return nil, err
	}
	out := []Moment{}
	rule := func(c domain.Candidate) Moment {
		peak := c.Peak
		return Moment{ID: SourceRule + ":" + c.ID, Source: SourceRule, StartMs: c.ClipStartMs, EndMs: c.ClipEndMs,
			AnchorMs: c.ClipStartMs, Title: strings.Join(c.Reasons, ", "), Reasons: c.Reasons, Peak: &peak, EvidenceIDs: c.EvidenceIDs}
	}
	if s.Kind == string(platform.KindLive) {
		cands, _, err := a.cfg.Store.LiveCandidates(ctx, sid, 0)
		if err != nil {
			return nil, err
		}
		for _, c := range cands {
			out = append(out, rule(c))
		}
	} else {
		tl, err := a.Timeline(ctx, sid)
		if err != nil {
			return nil, err
		}
		rules := map[string]domain.Candidate{}
		if tl.AnalysisID != "" {
			an, err := a.cfg.Store.Analysis(ctx, tl.AnalysisID)
			if err != nil {
				return nil, err
			}
			for _, c := range an.Candidates {
				rules[SourceRule+":"+c.ID] = c
			}
		}
		for _, it := range tl.Items {
			if c, ok := rules[it.ID]; ok {
				out = append(out, rule(c))
				continue
			}
			m := Moment{ID: it.ID, Source: it.Source, StartMs: it.ClipStartMs, EndMs: it.ClipEndMs,
				AnchorMs: it.AnchorMs, Title: it.Title, CommentText: it.CommentText, Rationale: it.Rationale,
				Uncertainty: it.Uncertainty, Priority: it.Priority}
			if it.Source == SourceAI {
				m.RunID = tl.AIRunID
			}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out, nil
}

// Activity is a session's chat per time bucket, and the spans where
// collection was interrupted.
type Activity struct {
	BucketMs int64
	Buckets  []store.ActivityBucket // only buckets with messages
	Gaps     []domain.Gap
}

// ChatActivity counts messages and authors per bucket in [fromMs, toMs).
func (a *App) ChatActivity(ctx context.Context, sid string, bucketMs, fromMs, toMs int64) (Activity, error) {
	if bucketMs < 1000 || bucketMs > 3600_000 {
		return Activity{}, domain.NewUserError("INPUT", "bucket_seconds는 1~3600초로 지정하세요.")
	}
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return Activity{}, err
	}
	b, err := a.cfg.Store.ChatActivity(ctx, sid, bucketMs, fromMs, toMs)
	if err != nil {
		return Activity{}, err
	}
	gaps, err := a.cfg.Store.Gaps(ctx, sid)
	if err != nil {
		return Activity{}, err
	}
	return Activity{BucketMs: bucketMs, Buckets: b, Gaps: gaps}, nil
}
