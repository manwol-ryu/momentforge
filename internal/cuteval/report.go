package cuteval

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/app"
)

func mark(ok bool) string {
	if ok {
		return "O"
	}
	return "X"
}

func opt(b *bool) string {
	if b == nil {
		return "-"
	}
	return mark(*b)
}

func seconds(ms int64) string { return fmt.Sprintf("%.1f", float64(ms)/1000) }

// source names what a cut plan read.
func source(a Attempt) string {
	switch {
	case !a.IsCut:
		return "채팅 통계"
	case a.Transcript():
		return "발언+채팅"
	}
	return "채팅만"
}

// methodName is how the report names a method.
func methodName(m string) string {
	switch m {
	case MethodDetector:
		return "탐지기 구간"
	case MethodExplain:
		return "1세대 장면 설명"
	case MethodCut:
		return "컷 분석 (AI 선택)"
	}
	if b := Boundary(m); b != "" {
		return "컷 분석 · " + b
	}
	return m
}

// Report is the evaluation as Markdown: a table per case, then each method's
// totals, cut plans split by whether they read a transcript.
func Report(results []Result, skipped []string) string {
	var b strings.Builder
	b.WriteString("# 컷 분석 평가\n\n")
	b.WriteString("시작·끝: 허용 범위 안이면 O. 경계 오차: 범위 밖으로 벗어난 초(시작+끝). 필수 맥락: 꼭 남길 구간 중 남긴 비율. ")
	b.WriteString("원인·기원·결말은 컷 분석만 채점합니다(-는 채점 안 함). 독립적 이해도는 사람이 따로 봅니다.\n")
	var order []string
	byCase := map[string][]Result{}
	for _, r := range results {
		if _, ok := byCase[r.Case.ID]; !ok {
			order = append(order, r.Case.ID)
		}
		byCase[r.Case.ID] = append(byCase[r.Case.ID], r)
	}
	for _, id := range order {
		rs := byCase[id]
		c := rs[0].Case
		fmt.Fprintf(&b, "\n## %s — %s\n\n", c.ID, c.Title)
		if len(c.Tags) > 0 {
			fmt.Fprintf(&b, "상황: %s. ", strings.Join(c.Tags, ", "))
		}
		fmt.Fprintf(&b, "정답 시작 %s~%s, 끝 %s~%s.\n\n", Timecode(int64(c.Gold.Start.From)), Timecode(int64(c.Gold.Start.To)),
			Timecode(int64(c.Gold.End.From)), Timecode(int64(c.Gold.End.To)))
		b.WriteString("| 방식 | 자료 | 검수 | 구간 | 시작 | 끝 | 경계 오차(초) | 필수 맥락 | 원인 | 기원 | 결말 | 길이(초) | 검색 |\n")
		b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|---|\n")
		for _, r := range rs {
			a := r.Attempt
			if r.Missing {
				fmt.Fprintf(&b, "| %s | - | - | 없음 | | | | | | | | | |\n", methodName(a.Method))
				continue
			}
			var spans []string
			for _, s := range merged(a.Spans) {
				spans = append(spans, Timecode(s.StartMs)+"~"+Timecode(s.EndMs))
			}
			check, search := "-", "-"
			if a.IsCut {
				check = map[string]string{"ok": "통과", "verify_failed": "실패", "failed": "구간 없음"}[a.State]
				search = fmt.Sprintf("%d회 +%d", a.Searches, a.Added)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %.0f%% | %s | %s | %s | %s | %s |\n", methodName(a.Method), source(a), check,
				strings.Join(spans, ", "), mark(r.StartOK), mark(r.EndOK), seconds(r.BoundaryErrMs), r.Coverage*100,
				opt(r.CauseOK), opt(r.OriginOK), opt(r.EndingOK), seconds(r.LengthMs), search)
		}
	}
	b.WriteString("\n## 방식별 합계\n\n")
	b.WriteString("| 방식 | 자료 | 사례 | 없음 | 시작 O | 끝 O | 평균 경계 오차(초) | 평균 필수 맥락 | 원인 O | 기원 O | 결말 O | 평균 검색 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|---|---|---|\n")
	type key struct{ method, source string }
	var keys []key
	group := map[key][]Result{}
	for _, r := range results {
		k := key{r.Attempt.Method, source(r.Attempt)}
		if r.Missing {
			k.source = "-"
		}
		if _, ok := group[k]; !ok {
			keys = append(keys, k)
		}
		group[k] = append(group[k], r)
	}
	rank := func(m string) int {
		return slices.Index([]string{MethodDetector, MethodExplain, MethodCut, MethodCut + ":short", MethodCut + ":context", MethodCut + ":full_event"}, m)
	}
	slices.SortStableFunc(keys, func(x, y key) int {
		if d := rank(x.method) - rank(y.method); d != 0 {
			return d
		}
		return strings.Compare(x.source, y.source)
	})
	for _, k := range keys {
		rs := group[k]
		var n, missing, startOK, endOK, causeN, causeOK, originN, originOK, endingN, endingOK, searches int
		var errMs int64
		var cov float64
		for _, r := range rs {
			if r.Missing {
				missing++
				continue
			}
			n++
			startOK += btoi(r.StartOK)
			endOK += btoi(r.EndOK)
			errMs += r.BoundaryErrMs
			cov += r.Coverage
			searches += r.Attempt.Searches
			if r.CauseOK != nil {
				causeN, causeOK = causeN+1, causeOK+btoi(*r.CauseOK)
			}
			if r.OriginOK != nil {
				originN, originOK = originN+1, originOK+btoi(*r.OriginOK)
			}
			if r.EndingOK != nil {
				endingN, endingOK = endingN+1, endingOK+btoi(*r.EndingOK)
			}
		}
		if n == 0 {
			fmt.Fprintf(&b, "| %s | %s | %d | %d | | | | | | | | |\n", methodName(k.method), k.source, len(rs), missing)
			continue
		}
		frac := func(ok, of int) string {
			if of == 0 {
				return "-"
			}
			return fmt.Sprintf("%d/%d", ok, of)
		}
		avgSearch := "-"
		if causeN > 0 {
			avgSearch = fmt.Sprintf("%.1f", float64(searches)/float64(n))
		}
		fmt.Fprintf(&b, "| %s | %s | %d | %d | %d/%d | %d/%d | %s | %.0f%% | %s | %s | %s | %s |\n", methodName(k.method), k.source, len(rs), missing,
			startOK, n, endOK, n, seconds(errMs/int64(n)), cov/float64(n)*100, frac(causeOK, causeN), frac(originOK, originN),
			frac(endingOK, endingN), avgSearch)
	}
	if len(skipped) > 0 {
		b.WriteString("\n## 채점하지 못한 사례\n\n")
		for _, s := range skipped {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	return b.String()
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Drafts turns a session's confirmed cut plans into case drafts: the
// confirmed clip with a little slack, its setup and main spans as
// must-include, the leading cause's evidence and the story origin. They are
// a starting point; a person checks every window before they become cases.
func Drafts(ctx context.Context, r Reader, sid string) ([]Case, error) {
	list, err := r.ExploreSessions(ctx, true)
	if err != nil {
		return nil, err
	}
	var s *app.SessionInfo
	for i := range list {
		if list[i].ID == sid {
			s = &list[i]
		}
	}
	if s == nil {
		return nil, fmt.Errorf("no session %s", sid)
	}
	ref := SessionRef{Title: s.Title}
	switch {
	case s.IsDemo:
		ref = SessionRef{Demo: true}
	case s.TargetID != "":
		ref = SessionRef{Video: s.Platform + ":" + s.TargetID}
	}
	tl, err := r.Timeline(ctx, sid)
	if err != nil {
		return nil, err
	}
	details, err := r.CutPlanDetails(ctx, sid, true)
	if err != nil {
		return nil, err
	}
	var out []Case
	for _, d := range details {
		if !d.Confirmed {
			continue
		}
		name := d.ConfirmedBoundary
		if name == "" {
			name = d.Chosen
		}
		var spans []app.TimelineSpan
		for _, b := range d.Boundaries {
			if b.Name == name {
				spans = b.Spans
			}
		}
		if len(spans) == 0 {
			continue
		}
		var moment int64 = spans[0].StartMs
		for _, it := range tl.Items {
			if it.Source == app.SourceRule && it.Kind != "cut" && slices.Contains(d.CandidateIDs, it.ID) {
				moment = it.ClipStartMs
			}
		}
		first, last := spans[0].StartMs, spans[len(spans)-1].EndMs
		c := Case{ID: fmt.Sprintf("draft-%s-v%d", d.PlanID[:min(8, len(d.PlanID))], d.Version), Title: d.Target, Session: ref,
			MomentMs: Ms(moment), Note: fmt.Sprintf("초안: 확정한 컷 계획 %s v%d(%s)에서 만듦. 모든 범위를 영상으로 확인하고 고친 뒤 cases/로 옮기세요.",
				d.PlanID, d.Version, Boundary(name)),
			Gold: Gold{Start: Window{Ms(first - 3000), Ms(first + 3000), ""}, End: Window{Ms(last - 3000), Ms(last + 5000), ""},
				Ending: d.Ending.State}}
		for _, s := range spans {
			if s.Role == "setup" || s.Role == "main" {
				c.Gold.MustInclude = append(c.Gold.MustInclude, Window{Ms(s.StartMs + 1000), Ms(max(s.StartMs+1000, s.EndMs-1000)), s.Role})
			}
		}
		if len(d.Causes) > 0 {
			for _, e := range d.Causes[0].Support {
				if e.AtMs != nil {
					c.Gold.Cause = append(c.Gold.Cause, Window{Ms(*e.AtMs - 5000), Ms(*e.AtMs + 5000), e.Text})
				}
			}
		}
		if len(c.Gold.Cause) == 0 {
			c.Gold.Cause = []Window{{Ms(first), Ms(last), "원인 근거를 적어 주세요"}}
		}
		if d.Origin != nil && d.Origin.AtMs != nil {
			c.Gold.Origin = &Window{Ms(*d.Origin.AtMs - 10000), Ms(*d.Origin.AtMs + 10000), d.Origin.Text}
		}
		out = append(out, c)
	}
	return out, nil
}
