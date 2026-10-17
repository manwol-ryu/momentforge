package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

type tools struct{ r Reader }

// --- shared shapes ---

type sessionJSON struct {
	ID          string `json:"session_id"`
	Title       string `json:"title"`
	Platform    string `json:"platform" jsonschema:"플랫폼 이름 (치지직, 샘플 등)"`
	Kind        string `json:"kind" jsonschema:"vod(다시보기) 또는 live(생방송 기록)"`
	State       string `json:"state" jsonschema:"수집 상태"`
	DurationMs  int64  `json:"duration_ms"`
	Duration    string `json:"duration" jsonschema:"H:MM:SS"`
	ChatCount   int    `json:"chat_count"`
	CreatedAt   string `json:"created_at" jsonschema:"수집을 시작한 시각 (RFC 3339, UTC)"`
	IsDemo      bool   `json:"is_demo" jsonschema:"앱에 들어 있는 샘플(실제 방송 아님)"`
	HasAnalysis bool   `json:"has_analysis"`
	WatchURL    string `json:"watch_url,omitempty"`
}

func session(s app.SessionInfo) sessionJSON {
	return sessionJSON{ID: s.ID, Title: s.Title, Platform: s.PlatformName, Kind: s.Kind, State: s.State,
		DurationMs: s.DurationMs, Duration: tc(s.DurationMs), ChatCount: s.ChatCount,
		CreatedAt: s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), IsDemo: s.IsDemo, HasAnalysis: s.LatestAnalysisID != "",
		WatchURL: s.WatchURL}
}

type chatJSON struct {
	ID         string `json:"id"`
	TimeMs     int64  `json:"time_ms"`
	Time       string `json:"time" jsonschema:"H:MM:SS"`
	Author     string `json:"author" jsonschema:"세션별 가명"`
	Text       string `json:"text"`
	IsEvidence bool   `json:"is_evidence,omitempty" jsonschema:"규칙 후보의 판정 근거"`
}

func chats(list []domain.ChatEvent, evidence map[string]bool) []chatJSON {
	out := make([]chatJSON, 0, len(list))
	for _, c := range list {
		out = append(out, chatJSON{ID: c.ID, TimeMs: c.OffsetMs, Time: tc(c.OffsetMs), Author: c.AuthorKey, Text: c.Text,
			IsEvidence: evidence[c.ID]})
	}
	return out
}

func chatsMarkdown(list []chatJSON) string {
	var b strings.Builder
	for _, c := range list {
		mark := ""
		if c.IsEvidence {
			mark = " (근거)"
		}
		fmt.Fprintf(&b, "- %s [%s]%s %s\n", c.Time, c.Author, mark, mdEscape(c.Text))
	}
	return b.String()
}

func pageLine(p pageOut) string {
	line := fmt.Sprintf("전체 %d개 중 %d~%d번째", p.Total, p.Offset+min(1, p.Count), p.Offset+p.Count)
	if p.HasMore {
		line += fmt.Sprintf(", 다음 쪽 cursor=%s", p.NextCursor)
	}
	return line + "\n"
}

// --- mf_list_sessions ---

type listSessionsIn struct {
	IncludeHidden bool `json:"include_hidden,omitempty" jsonschema:"앱의 저장 목록에서 숨긴 세션도 포함"`
	pageIn
	formatIn
}

type listSessionsOut struct {
	pageOut
	Sessions []sessionJSON `json:"sessions"`
}

func (t *tools) listSessions(ctx context.Context, in listSessionsIn) (listSessionsOut, string, error) {
	offset, limit, err := in.bounds(20, 100)
	if err != nil {
		return listSessionsOut{}, "", err
	}
	list, err := t.r.ExploreSessions(ctx, in.IncludeHidden)
	if err != nil {
		return listSessionsOut{}, "", err
	}
	out := listSessionsOut{Sessions: []sessionJSON{}}
	for _, s := range list[min(offset, len(list)):min(offset+limit, len(list))] {
		out.Sessions = append(out.Sessions, session(s))
	}
	out.pageOut = makePage(len(list), offset, len(out.Sessions))
	text, err := in.render(out, func() string {
		var b strings.Builder
		b.WriteString("# 세션\n" + pageLine(out.pageOut))
		for _, s := range out.Sessions {
			fmt.Fprintf(&b, "- %s (%s, %s) 길이 %s, 채팅 %d개, session_id=%s\n", mdEscape(s.Title), s.Platform, s.Kind,
				s.Duration, s.ChatCount, s.ID)
		}
		return b.String()
	})
	return out, text, err
}

// --- mf_get_session ---

type sessionIn struct {
	SessionID string `json:"session_id" jsonschema:"mf_list_sessions의 session_id"`
	formatIn
}

type gapJSON struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Start   string `json:"start"`
	End     string `json:"end"`
}

type commentsJSON struct {
	Source          string `json:"source" jsonschema:"website(영상 댓글) 또는 paste(직접 붙여넣음)"`
	CommentCount    int    `json:"comment_count"`
	Recommendations int    `json:"recommendations" jsonschema:"댓글에서 찾은 시각 수"`
}

type aiRunJSON struct {
	ID               string   `json:"run_id"`
	Provider         string   `json:"provider"`
	Model            string   `json:"model"`
	Delivery         string   `json:"delivery" jsonschema:"single(바로) 또는 batch(배치)"`
	State            string   `json:"state"`
	Recommendations  int      `json:"recommendations"`
	InputTokens      int      `json:"input_tokens"`
	CacheReadTokens  int      `json:"cache_read_tokens" jsonschema:"프롬프트 캐시에서 읽은 입력 토큰"`
	OutputTokens     int      `json:"output_tokens"`
	CostUSD          *float64 `json:"cost_usd" jsonschema:"공시 가격 기준 비용(미국 달러); 가격을 모르면 null"`
	CreatedAt        string   `json:"created_at"`
	RequestCount     int      `json:"request_count"`
	CompletedRequest int      `json:"completed_requests"`
}

type getSessionOut struct {
	Session    sessionJSON    `json:"session"`
	Candidates map[string]int `json:"candidates" jsonschema:"출처별 후보 수 (rule, viewer_comment, ai)"`
	Gaps       []gapJSON      `json:"gaps" jsonschema:"수집이 끊긴 구간"`
	Comments   *commentsJSON  `json:"comments" jsonschema:"영상 댓글을 읽지 않았으면 null"`
	AIRuns     []aiRunJSON    `json:"ai_runs" jsonschema:"AI 검토 기록, 최신순"`
}

func (t *tools) getSession(ctx context.Context, in sessionIn) (getSessionOut, string, error) {
	s, err := t.r.Session(ctx, in.SessionID)
	if err != nil {
		return getSessionOut{}, "", err
	}
	out := getSessionOut{Session: session(s), Candidates: map[string]int{app.SourceRule: 0, app.SourceViewer: 0, app.SourceAI: 0},
		Gaps: []gapJSON{}, AIRuns: []aiRunJSON{}}
	moments, err := t.r.Moments(ctx, s.ID)
	if err != nil {
		return getSessionOut{}, "", err
	}
	for _, m := range moments {
		out.Candidates[m.Source]++
	}
	act, err := t.r.ChatActivity(ctx, s.ID, 3600_000, 0, 1<<50)
	if err != nil {
		return getSessionOut{}, "", err
	}
	for _, g := range act.Gaps {
		out.Gaps = append(out.Gaps, gapJSON{g.StartMs, g.EndMs, tc(g.StartMs), tc(g.EndMs)})
	}
	if c, err := t.r.Comments(ctx, s.ID); err != nil {
		return getSessionOut{}, "", err
	} else if c != nil {
		out.Comments = &commentsJSON{Source: c.Source, CommentCount: c.CommentCount, Recommendations: len(c.Recommendations)}
	}
	runs, err := t.r.AIRuns(ctx, s.ID)
	if err != nil {
		return getSessionOut{}, "", err
	}
	for _, r := range runs {
		out.AIRuns = append(out.AIRuns, aiRunJSON{ID: r.ID, Provider: r.Provider, Model: r.Model, Delivery: r.Delivery, State: r.State,
			Recommendations: r.Recommendations, InputTokens: r.InputTokens, CacheReadTokens: r.CacheReadTokens,
			OutputTokens: r.OutputTokens, CostUSD: r.CostUSD, CreatedAt: r.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
			RequestCount: r.RequestCount, CompletedRequest: r.CompletedRequests})
	}
	text, err := in.render(out, func() string {
		var b strings.Builder
		ss := out.Session
		fmt.Fprintf(&b, "# %s\n- session_id: %s\n- 플랫폼 %s, 종류 %s, 상태 %s\n- 길이 %s, 채팅 %d개\n- 후보: 규칙 %d개, 시청자 댓글 %d개, AI %d개\n",
			mdEscape(ss.Title), ss.ID, ss.Platform, ss.Kind, ss.State, ss.Duration, ss.ChatCount,
			out.Candidates[app.SourceRule], out.Candidates[app.SourceViewer], out.Candidates[app.SourceAI])
		for _, g := range out.Gaps {
			fmt.Fprintf(&b, "- 수집 공백 %s~%s\n", g.Start, g.End)
		}
		if out.Comments != nil {
			fmt.Fprintf(&b, "- 영상 댓글 %d개에서 시각 %d개\n", out.Comments.CommentCount, out.Comments.Recommendations)
		}
		for _, r := range out.AIRuns {
			fmt.Fprintf(&b, "- AI 검토 %s: %s %s (%s), 추천 %d개, 입력 %d토큰(캐시 적중 %d), 출력 %d토큰\n", r.ID, r.Provider, r.Model,
				r.State, r.Recommendations, r.InputTokens, r.CacheReadTokens, r.OutputTokens)
		}
		return b.String()
	})
	return out, text, err
}

// --- mf_search_chats ---

type searchIn struct {
	SessionID string `json:"session_id"`
	Query     string `json:"query,omitempty" jsonschema:"찾을 글자 (1~100자). 메시지에 이 글자가 들어 있으면 찾습니다"`
	Author    string `json:"author,omitempty" jsonschema:"이 작성자 가명이 보낸 메시지만 (예: 채팅 결과의 author 값)"`
	Start     string `json:"start,omitempty" jsonschema:"이 시각부터 (예: 0:10:00)"`
	End       string `json:"end,omitempty" jsonschema:"이 시각 전까지"`
	pageIn
	formatIn
}

type chatsOut struct {
	pageOut
	Chats []chatJSON `json:"chats"`
}

func (t *tools) searchChats(ctx context.Context, in searchIn) (chatsOut, string, error) {
	offset, limit, err := in.bounds(50, 200)
	if err != nil {
		return chatsOut{}, "", err
	}
	from, to, err := span(in.Start, in.End)
	if err != nil {
		return chatsOut{}, "", err
	}
	list, total, err := t.r.SearchChats(ctx, in.SessionID, in.Query, in.Author, from, to, offset, limit)
	if err != nil {
		return chatsOut{}, "", err
	}
	out := chatsOut{Chats: chats(list, nil)}
	out.pageOut = makePage(total, offset, len(out.Chats))
	text, err := in.render(out, func() string {
		head := "# 채팅 검색"
		if in.Query != "" {
			head += fmt.Sprintf(" \"%s\"", mdEscape(in.Query))
		}
		if in.Author != "" {
			head += fmt.Sprintf(", 작성자 %s", mdEscape(in.Author))
		}
		return head + "\n" + pageLine(out.pageOut) + chatsMarkdown(out.Chats)
	})
	return out, text, err
}

// --- mf_get_chats_around ---

type aroundIn struct {
	SessionID     string `json:"session_id"`
	Time          string `json:"time" jsonschema:"기준 시각 (예: 1:02:03)"`
	WindowSeconds int    `json:"window_seconds,omitempty" jsonschema:"앞뒤로 볼 초 (1~600, 기본 30)"`
	pageIn
	formatIn
}

type aroundOut struct {
	FromMs int64  `json:"from_ms"`
	ToMs   int64  `json:"to_ms"`
	From   string `json:"from"`
	To     string `json:"to"`
	chatsOut
}

func (t *tools) chatsAround(ctx context.Context, in aroundIn) (aroundOut, string, error) {
	offset, limit, err := in.bounds(100, 200)
	if err != nil {
		return aroundOut{}, "", err
	}
	at, err := parseTime("time", in.Time)
	if err != nil {
		return aroundOut{}, "", err
	}
	w := in.WindowSeconds
	if w == 0 {
		w = 30
	}
	if w < 1 || w > 600 {
		return aroundOut{}, "", inputErr("window_seconds는 1~600으로 지정하세요")
	}
	s, err := t.r.Session(ctx, in.SessionID)
	if err != nil {
		return aroundOut{}, "", err
	}
	from, to := max(0, at-int64(w)*1000), at+int64(w)*1000
	if s.DurationMs > 0 {
		to = min(to, s.DurationMs)
	}
	if to <= from {
		return aroundOut{}, "", inputErr("time이 영상 길이(%s)를 넘었습니다", tc(s.DurationMs))
	}
	list, total, err := t.r.ChatsInRange(ctx, in.SessionID, from, to, offset, limit)
	if err != nil {
		return aroundOut{}, "", err
	}
	out := aroundOut{FromMs: from, ToMs: to, From: tc(from), To: tc(to), chatsOut: chatsOut{Chats: chats(list, nil)}}
	out.pageOut = makePage(total, offset, len(out.Chats))
	text, err := in.render(out, func() string {
		return fmt.Sprintf("# %s~%s 채팅\n", out.From, out.To) + pageLine(out.pageOut) + chatsMarkdown(out.Chats)
	})
	return out, text, err
}

// --- mf_list_candidates ---

type candidatesIn struct {
	SessionID string `json:"session_id"`
	Source    string `json:"source,omitempty" jsonschema:"all(기본), rule, viewer_comment, ai 중 하나"`
	pageIn
	formatIn
}

type candidateJSON struct {
	ID          string   `json:"candidate_id"`
	Source      string   `json:"source" jsonschema:"rule, viewer_comment, ai"`
	StartMs     int64    `json:"start_ms"`
	EndMs       int64    `json:"end_ms"`
	Start       string   `json:"start" jsonschema:"편집 구간 시작 H:MM:SS"`
	End         string   `json:"end"`
	Title       string   `json:"title"`
	Rules       []string `json:"rules,omitempty" jsonschema:"규칙 후보: 판정한 규칙 이름"`
	PeakChats   *int     `json:"peak_chats,omitempty" jsonschema:"규칙 후보: 최고점 10초 창의 채팅 수"`
	PeakAuthors *int     `json:"peak_authors,omitempty" jsonschema:"규칙 후보: 최고점 창의 서로 다른 작성자 수"`
	Ratio       *float64 `json:"ratio,omitempty" jsonschema:"규칙 후보: 평소 대비 배율"`
	Evidence    *int     `json:"evidence_count,omitempty" jsonschema:"규칙 후보: 판정 근거로 표시된 채팅 수"`
	RunID       string   `json:"run_id,omitempty" jsonschema:"AI 추천: 추천을 만든 AI 검토 (mf_get_session의 ai_runs)"`
	Overlaps    []string `json:"overlaps" jsonschema:"편집 구간이 겹치는 다른 출처의 후보 ID"`
	Comment     string   `json:"comment,omitempty" jsonschema:"시청자 추천: 영상 댓글"`
	Rationale   string   `json:"rationale,omitempty" jsonschema:"AI 추천: 근거 요약"`
	Uncertainty string   `json:"uncertainty,omitempty" jsonschema:"AI 추천: 판단 한계"`
	Priority    string   `json:"priority,omitempty" jsonschema:"AI 추천: high, medium, low"`
}

// candidate shapes m; all is every candidate of the session, for overlaps.
func candidate(m app.Moment, all []app.Moment) candidateJSON {
	c := candidateJSON{ID: m.ID, Source: m.Source, StartMs: m.StartMs, EndMs: m.EndMs, Start: tc(m.StartMs), End: tc(m.EndMs),
		Title: m.Title, Rules: m.Reasons, RunID: m.RunID, Comment: m.CommentText, Rationale: m.Rationale,
		Uncertainty: m.Uncertainty, Priority: m.Priority, Overlaps: []string{}}
	if m.Peak != nil {
		n, u, e := m.Peak.N, m.Peak.U, len(m.EvidenceIDs)
		c.PeakChats, c.PeakAuthors, c.Ratio, c.Evidence = &n, &u, m.Peak.Ratio, &e
	}
	for _, o := range all {
		if o.Source != m.Source && min(o.EndMs, m.EndMs) > max(o.StartMs, m.StartMs) {
			c.Overlaps = append(c.Overlaps, o.ID)
		}
	}
	return c
}

type candidatesOut struct {
	pageOut
	Candidates []candidateJSON `json:"candidates"`
}

func (t *tools) listCandidates(ctx context.Context, in candidatesIn) (candidatesOut, string, error) {
	offset, limit, err := in.bounds(50, 200)
	if err != nil {
		return candidatesOut{}, "", err
	}
	switch in.Source {
	case "", "all", app.SourceRule, app.SourceViewer, app.SourceAI:
	default:
		return candidatesOut{}, "", inputErr("source는 all, rule, viewer_comment, ai 중 하나입니다")
	}
	moments, err := t.r.Moments(ctx, in.SessionID)
	if err != nil {
		return candidatesOut{}, "", err
	}
	var picked []app.Moment
	for _, m := range moments {
		if in.Source == "" || in.Source == "all" || m.Source == in.Source {
			picked = append(picked, m)
		}
	}
	out := candidatesOut{Candidates: []candidateJSON{}}
	for _, m := range picked[min(offset, len(picked)):min(offset+limit, len(picked))] {
		out.Candidates = append(out.Candidates, candidate(m, moments))
	}
	out.pageOut = makePage(len(picked), offset, len(out.Candidates))
	text, err := in.render(out, func() string {
		var b strings.Builder
		b.WriteString("# 편집 후보\n" + pageLine(out.pageOut))
		for _, c := range out.Candidates {
			fmt.Fprintf(&b, "- %s~%s [%s] %s (candidate_id=%s)", c.Start, c.End, c.Source, mdEscape(c.Title), c.ID)
			if c.PeakChats != nil {
				fmt.Fprintf(&b, " 최고점 채팅 %d개, 작성자 %d명", *c.PeakChats, *c.PeakAuthors)
			}
			if c.Ratio != nil {
				fmt.Fprintf(&b, ", 평소의 %.1f배", *c.Ratio)
			}
			if c.Evidence != nil {
				fmt.Fprintf(&b, ", 근거 채팅 %d개", *c.Evidence)
			}
			if c.Priority != "" {
				fmt.Fprintf(&b, " 우선순위 %s", c.Priority)
			}
			if c.RunID != "" {
				fmt.Fprintf(&b, " (AI 검토 %s)", c.RunID)
			}
			if len(c.Overlaps) > 0 {
				fmt.Fprintf(&b, "; 겹치는 후보: %s", strings.Join(c.Overlaps, ", "))
			}
			b.WriteString("\n")
		}
		return b.String()
	})
	return out, text, err
}

// --- mf_get_candidate_context ---

type contextIn struct {
	SessionID   string `json:"session_id"`
	CandidateID string `json:"candidate_id" jsonschema:"mf_list_candidates의 candidate_id"`
	pageIn
	formatIn
}

type contextOut struct {
	Candidate candidateJSON `json:"candidate"`
	chatsOut
}

func (t *tools) candidateContext(ctx context.Context, in contextIn) (contextOut, string, error) {
	offset, limit, err := in.bounds(100, 200)
	if err != nil {
		return contextOut{}, "", err
	}
	moments, err := t.r.Moments(ctx, in.SessionID)
	if err != nil {
		return contextOut{}, "", err
	}
	var m *app.Moment
	for i := range moments {
		if moments[i].ID == in.CandidateID {
			m = &moments[i]
		}
	}
	if m == nil {
		return contextOut{}, "", errCandidate
	}
	list, total, err := t.r.ChatsInRange(ctx, in.SessionID, m.StartMs, m.EndMs, offset, limit)
	if err != nil {
		return contextOut{}, "", err
	}
	evidence := map[string]bool{}
	for _, id := range m.EvidenceIDs {
		evidence[id] = true
	}
	out := contextOut{Candidate: candidate(*m, moments), chatsOut: chatsOut{Chats: chats(list, evidence)}}
	out.pageOut = makePage(total, offset, len(out.Chats))
	text, err := in.render(out, func() string {
		c := out.Candidate
		head := fmt.Sprintf("# 후보 %s~%s [%s] %s\n", c.Start, c.End, c.Source, mdEscape(c.Title))
		for _, s := range []string{c.Comment, c.Rationale, c.Uncertainty} {
			if s != "" {
				head += "- " + mdEscape(s) + "\n"
			}
		}
		return head + "## 편집 구간 채팅\n" + pageLine(out.pageOut) + chatsMarkdown(out.Chats)
	})
	return out, text, err
}

// --- mf_get_timeline ---

type timelineIn struct {
	SessionID     string `json:"session_id"`
	BucketSeconds int    `json:"bucket_seconds,omitempty" jsonschema:"구간 길이 초 (10~3600, 기본 60)"`
	Start         string `json:"start,omitempty" jsonschema:"이 시각부터"`
	End           string `json:"end,omitempty" jsonschema:"이 시각 전까지"`
	pageIn
	formatIn
}

type bucketJSON struct {
	StartMs int64  `json:"start_ms"`
	Start   string `json:"start" jsonschema:"구간 시작 H:MM:SS"`
	Chats   int    `json:"chats"`
	Authors int    `json:"authors" jsonschema:"서로 다른 작성자 수"`
}

type timelineOut struct {
	BucketSeconds int          `json:"bucket_seconds"`
	Busiest       []bucketJSON `json:"busiest" jsonschema:"채팅이 가장 많은 구간 5개 (많은 순)"`
	Gaps          []gapJSON    `json:"gaps"`
	pageOut
	Buckets []bucketJSON `json:"buckets" jsonschema:"채팅이 있는 구간, 시간순"`
}

func (t *tools) timeline(ctx context.Context, in timelineIn) (timelineOut, string, error) {
	offset, limit, err := in.bounds(100, 500)
	if err != nil {
		return timelineOut{}, "", err
	}
	sec := in.BucketSeconds
	if sec == 0 {
		sec = 60
	}
	if sec < 10 || sec > 3600 {
		return timelineOut{}, "", inputErr("bucket_seconds는 10~3600으로 지정하세요")
	}
	from, to, err := span(in.Start, in.End)
	if err != nil {
		return timelineOut{}, "", err
	}
	act, err := t.r.ChatActivity(ctx, in.SessionID, int64(sec)*1000, from, to)
	if err != nil {
		return timelineOut{}, "", err
	}
	all := make([]bucketJSON, 0, len(act.Buckets))
	for _, b := range act.Buckets {
		start := b.Index * act.BucketMs
		all = append(all, bucketJSON{StartMs: start, Start: tc(start), Chats: b.Chats, Authors: b.Authors})
	}
	out := timelineOut{BucketSeconds: sec, Gaps: []gapJSON{}, Buckets: []bucketJSON{}}
	busiest := append([]bucketJSON(nil), all...)
	sort.SliceStable(busiest, func(i, j int) bool { return busiest[i].Chats > busiest[j].Chats })
	out.Busiest = append([]bucketJSON{}, busiest[:min(5, len(busiest))]...)
	for _, g := range act.Gaps {
		out.Gaps = append(out.Gaps, gapJSON{g.StartMs, g.EndMs, tc(g.StartMs), tc(g.EndMs)})
	}
	out.Buckets = append(out.Buckets, all[min(offset, len(all)):min(offset+limit, len(all))]...)
	out.pageOut = makePage(len(all), offset, len(out.Buckets))
	text, err := in.render(out, func() string {
		var b strings.Builder
		fmt.Fprintf(&b, "# 채팅 활동 (%d초 구간)\n## 가장 많은 구간\n", sec)
		for _, x := range out.Busiest {
			fmt.Fprintf(&b, "- %s 채팅 %d개, 작성자 %d명\n", x.Start, x.Chats, x.Authors)
		}
		for _, g := range out.Gaps {
			fmt.Fprintf(&b, "- 수집 공백 %s~%s\n", g.Start, g.End)
		}
		b.WriteString("## 구간별\n" + pageLine(out.pageOut))
		for _, x := range out.Buckets {
			fmt.Fprintf(&b, "- %s %d개 (%d명)\n", x.Start, x.Chats, x.Authors)
		}
		return b.String()
	})
	return out, text, err
}
