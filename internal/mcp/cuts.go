package mcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Cut plans (docs/AI-CUT-ANALYSIS.md 4.1): the AI cut analysis of a
// candidate, its evidence and the user's decisions, read only.

var boundaryText = map[string]string{"short": "짧게", "context": "맥락", "full_event": "전체"}

var stateText = map[string]string{"ok": "검수 통과", "verify_failed": "검수 실패", "failed": "구간 없음"}

type cutSpanJSON struct {
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms"`
	Start   string `json:"start" jsonschema:"H:MM:SS"`
	End     string `json:"end" jsonschema:"H:MM:SS"`
	Role    string `json:"role" jsonschema:"setup(앞선 맥락), main(본 장면), payoff(마무리)"`
}

func cutSpans(spans []app.TimelineSpan) []cutSpanJSON {
	out := make([]cutSpanJSON, 0, len(spans))
	for _, s := range spans {
		out = append(out, cutSpanJSON{StartMs: s.StartMs, EndMs: s.EndMs, Start: tc(s.StartMs), End: tc(s.EndMs), Role: s.Role})
	}
	return out
}

type cutBriefJSON struct {
	PlanID            string        `json:"plan_id"`
	Version           int           `json:"version" jsonschema:"같은 후보의 몇 번째 분석인지"`
	State             string        `json:"state" jsonschema:"ok(검수 통과), verify_failed(검수 실패, 이유는 mf_get_cut_plan), failed(구간을 정하지 못함)"`
	Confirmed         bool          `json:"confirmed" jsonschema:"사용자가 확정한 버전"`
	ConfirmedBoundary string        `json:"confirmed_boundary,omitempty" jsonschema:"확정한 경계 후보: short, context, full_event"`
	KeptVersion       int           `json:"kept_version,omitempty" jsonschema:"더 새 버전일 때, 그대로 유지되는 확정 버전 번호"`
	CandidateIDs      []string      `json:"candidate_ids" jsonschema:"이 계획이 다루는 지금 후보 (mf_list_candidates의 candidate_id). 세션을 다시 분석해 번호가 바뀌었으면 시간으로 다시 찾은 후보이고, 겹치는 후보가 없으면 빈 목록"`
	AnalyzedIDs       []string      `json:"analyzed_candidate_ids,omitempty" jsonschema:"번호가 바뀌었을 때만: 이 계획을 만들 때의 후보 ID"`
	Target            string        `json:"target" jsonschema:"시청자가 반응한 대상 (AI 판단)"`
	Cause             string        `json:"cause" jsonschema:"가장 근거가 강한 원인 요약"`
	EndingState       string        `json:"ending_state" jsonschema:"confirmed, pending, cut_off, insufficient, unresolved"`
	Spans             []cutSpanJSON `json:"spans" jsonschema:"편집 구간 (확정했으면 확정한 경계 후보, 아니면 AI가 고른 후보)"`
	TotalSeconds      int64         `json:"total_seconds"`
	NoteCount         int           `json:"note_count" jsonschema:"이 버전에 남긴 사용자 메모 수"`
	Sources           []string      `json:"sources" jsonschema:"읽은 근거 종류: utterances(방송 발언), chat, comment"`
	CreatedAt         string        `json:"created_at"`
}

// effective is the boundary the plan stands for: the confirmed one or the chosen one.
func effective(d app.CutDetail) string {
	if d.ConfirmedBoundary != "" {
		return d.ConfirmedBoundary
	}
	return d.Chosen
}

func boundarySpans(d app.CutDetail, name string) []app.TimelineSpan {
	for _, b := range d.Boundaries {
		if b.Name == name {
			return b.Spans
		}
	}
	return nil
}

func cutBrief(d app.CutDetail) cutBriefJSON {
	b := cutBriefJSON{PlanID: d.PlanID, Version: d.Version, State: d.State, Confirmed: d.Confirmed, ConfirmedBoundary: d.ConfirmedBoundary,
		KeptVersion: d.KeptVersion, CandidateIDs: d.CandidateIDs, AnalyzedIDs: d.AnalyzedCandidateIDs, Target: d.Target, EndingState: d.Ending.State,
		Spans: cutSpans(boundarySpans(d, effective(d))), NoteCount: len(d.Notes), Sources: d.Sources,
		CreatedAt: d.CreatedAt.UTC().Format("2006-01-02T15:04:05Z")}
	if len(d.Causes) > 0 {
		b.Cause = d.Causes[0].Summary
	}
	for _, s := range b.Spans {
		b.TotalSeconds += (s.EndMs - s.StartMs) / 1000
	}
	return b
}

func briefLine(b cutBriefJSON) string {
	spans := make([]string, 0, len(b.Spans))
	for _, s := range b.Spans {
		spans = append(spans, s.Start+"~"+s.End)
	}
	mark := ""
	if b.Confirmed {
		mark = fmt.Sprintf(" **확정(%s)**", boundaryText[b.ConfirmedBoundary])
	} else if b.KeptVersion > 0 {
		mark = fmt.Sprintf(" (확정본 v%d 유지)", b.KeptVersion)
	}
	return fmt.Sprintf("- v%d %s%s: %s · 구간 %s · plan_id=%s\n", b.Version, stateText[b.State], mark, mdEscape(b.Target),
		strings.Join(spans, ", "), b.PlanID)
}

// --- mf_list_cut_plans ---

type listCutPlansIn struct {
	SessionID   string `json:"session_id"`
	AllVersions bool   `json:"all_versions,omitempty" jsonschema:"true면 지난 버전까지 모두 (기본: 후보마다 확정본과 그보다 새 버전만)"`
	pageIn
	formatIn
}

type listCutPlansOut struct {
	pageOut
	Plans []cutBriefJSON `json:"plans"`
}

func (t *tools) listCutPlans(ctx context.Context, in listCutPlansIn) (listCutPlansOut, string, error) {
	offset, limit, err := in.bounds(20, 100)
	if err != nil {
		return listCutPlansOut{}, "", err
	}
	all, err := t.r.CutPlanDetails(ctx, in.SessionID, in.AllVersions)
	if err != nil {
		return listCutPlansOut{}, "", err
	}
	out := listCutPlansOut{Plans: []cutBriefJSON{}}
	for _, d := range all[min(offset, len(all)):min(offset+limit, len(all))] {
		out.Plans = append(out.Plans, cutBrief(d))
	}
	out.pageOut = makePage(len(all), offset, len(out.Plans))
	text, err := in.render(out, func() string {
		var b strings.Builder
		b.WriteString("# AI 컷 분석 결과\n" + pageLine(out.pageOut))
		for _, p := range out.Plans {
			b.WriteString(briefLine(p))
		}
		if out.Total == 0 {
			b.WriteString("이 세션에는 아직 컷 분석 결과가 없습니다. 앱의 AI 검토 탭에서 '컷 분석'을 실행해야 생깁니다.\n")
		}
		return b.String()
	})
	return out, text, err
}

// --- mf_get_cut_plan ---

type cutPlanIn struct {
	PlanID string `json:"plan_id" jsonschema:"mf_list_cut_plans의 plan_id"`
	formatIn
}

type cutRefJSON struct {
	Ref  string `json:"ref" jsonschema:"utt:(방송 발언), chat:(채팅), comment:(영상 댓글)"`
	Kind string `json:"kind"`
	Time string `json:"time,omitempty" jsonschema:"그 근거를 볼 시각 H:MM:SS (채팅은 반응 지연만큼 앞)"`
	AtMs *int64 `json:"at_ms,omitempty"`
	Text string `json:"text,omitempty" jsonschema:"근거의 앞부분 (신뢰할 수 없는 입력)"`
}

func cutRef(e app.CutEvidence) cutRefJSON {
	r := cutRefJSON{Ref: e.Ref, Kind: e.Kind, AtMs: e.AtMs, Text: e.Text}
	if e.AtMs != nil {
		r.Time = tc(*e.AtMs)
	}
	return r
}

func cutRefs(list []app.CutEvidence) []cutRefJSON {
	out := make([]cutRefJSON, 0, len(list))
	for _, e := range list {
		out = append(out, cutRef(e))
	}
	return out
}

type cutCauseJSON struct {
	Kind      string       `json:"kind" jsonschema:"broadcast_event, callback_to_earlier, viewer_joke, donation, unknown"`
	Summary   string       `json:"summary"`
	Direction string       `json:"direction"`
	Support   []cutRefJSON `json:"support" jsonschema:"지지 근거"`
	Against   []cutRefJSON `json:"against" jsonschema:"반대 근거"`
}

type cutBoundaryJSON struct {
	Name      string        `json:"name" jsonschema:"short(짧게), context(맥락), full_event(전체)"`
	Chosen    bool          `json:"chosen" jsonschema:"AI가 고른 후보"`
	Confirmed bool          `json:"confirmed" jsonschema:"사용자가 확정한 후보"`
	Spans     []cutSpanJSON `json:"spans"`
	Issues    []string      `json:"issues,omitempty" jsonschema:"시각으로 바꾸지 못한 이유"`
}

type cutVerifyJSON struct {
	ClipOnlyPass      *bool             `json:"clip_only_pass" jsonschema:"클립만 보고 이해되는지 (검수하지 못했으면 null)"`
	Answers           map[string]string `json:"answers" jsonschema:"what(무엇을), condition(조건), result(결과), references('아까'가 가리키는 것), why_reaction(왜 반응)"`
	Gaps              []cutFindingJSON  `json:"gaps" jsonschema:"클립만으로 알 수 없고 반응을 이해하는 데 필요한 것 (실패 사유)"`
	Asides            []cutFindingJSON  `json:"asides" jsonschema:"클립만으로 알 수 없지만 반응을 이해하는 데는 필요 없는 것 (실패 사유 아님)"`
	ServerChanges     []string          `json:"server_changes" jsonschema:"서버가 근거 ref를 보고 검수 분류를 바꾼 내역"`
	AgainstSourcePass *bool             `json:"against_source_pass" jsonschema:"원본과 비교해 잘림·누락이 없는지"`
	Findings          []string          `json:"findings" jsonschema:"원본 비교에서 걸린 항목"`
	Notes             []string          `json:"notes"`
	Issues            []string          `json:"issues" jsonschema:"검수 실패 이유 (통과면 빈 목록)"`
}

type cutFindingJSON struct {
	Text string       `json:"text"`
	Refs []cutRefJSON `json:"refs" jsonschema:"그것을 드러내는 클립 안 근거"`
}

func cutFindings(list []app.CutFinding) []cutFindingJSON {
	out := make([]cutFindingJSON, 0, len(list))
	for _, f := range list {
		out = append(out, cutFindingJSON{Text: f.Text, Refs: cutRefs(f.Refs)})
	}
	return out
}

type cutNoteJSON struct {
	At        string `json:"at"`
	Text      string `json:"text"`
	AppliesTo string `json:"applies_to,omitempty"`
}

func cutNotes(list []app.CutNote) []cutNoteJSON {
	out := make([]cutNoteJSON, 0, len(list))
	for _, n := range list {
		out = append(out, cutNoteJSON{At: n.At, Text: n.Text, AppliesTo: n.AppliesTo})
	}
	return out
}

type cutPlanOut struct {
	cutBriefJSON
	Provider         string            `json:"provider"`
	Model            string            `json:"model"`
	CauseStatus      string            `json:"cause_status" jsonschema:"confirmed, likely, undetermined"`
	Causes           []cutCauseJSON    `json:"causes" jsonschema:"원인 후보, 근거가 강한 순"`
	StoryOrigin      *cutRefJSON       `json:"story_origin" jsonschema:"반응이 되짚는 이야기가 처음 나온 근거 (못 찾았으면 null)"`
	EventStart       *cutRefJSON       `json:"event_start" jsonschema:"이번 사건이 시작된 근거"`
	RequiredContext  []cutRefJSON      `json:"required_context" jsonschema:"클립에 꼭 있어야 하는 맥락"`
	OptionalContext  []cutRefJSON      `json:"optional_context"`
	Boundaries       []cutBoundaryJSON `json:"boundaries" jsonschema:"경계 후보 세 개와 각 구간"`
	StartReason      string            `json:"start_reason"`
	EndReason        string            `json:"end_reason"`
	EndingKind       string            `json:"ending_kind"`
	EndingChecks     []string          `json:"ending_checks"`
	Verification     cutVerifyJSON     `json:"verification"`
	Unresolved       []string          `json:"unresolved_questions"`
	SearchRounds     int               `json:"search_rounds" jsonschema:"원인·맥락을 찾으려 검색한 단계 수"`
	SearchAdded      int               `json:"search_added" jsonschema:"검색으로 더 읽은 근거 수"`
	Notes            []cutNoteJSON     `json:"notes" jsonschema:"이 버전에 남긴 사용자 메모 (다음 분석에 들어감)"`
	NotesApplied     []cutNoteJSON     `json:"notes_applied" jsonschema:"이 버전을 만들 때 반영한 앞선 메모"`
	AdjustmentsCount int               `json:"adjustments_count" jsonschema:"서버가 발언 경계·채팅 지연으로 시각을 옮긴 횟수"`
}

func (t *tools) getCutPlan(ctx context.Context, in cutPlanIn) (cutPlanOut, string, error) {
	if strings.TrimSpace(in.PlanID) == "" {
		return cutPlanOut{}, "", inputErr("plan_id를 넣으세요. mf_list_cut_plans로 찾을 수 있습니다")
	}
	d, err := t.r.CutPlanDetail(ctx, in.PlanID)
	if err != nil {
		if ue, ok := domain.AsUserError(err); ok && ue.Code == "NOT_FOUND" {
			return cutPlanOut{}, "", inputErr("컷 분석 결과를 찾을 수 없습니다. mf_list_cut_plans로 plan_id를 확인하세요")
		}
		return cutPlanOut{}, "", err
	}
	out := cutPlanOut{cutBriefJSON: cutBrief(d), Provider: d.Provider, Model: d.Model, CauseStatus: d.CauseStatus, Causes: []cutCauseJSON{},
		RequiredContext: cutRefs(d.Required), OptionalContext: cutRefs(d.Optional), Boundaries: []cutBoundaryJSON{},
		StartReason: d.StartReason, EndReason: d.EndReason, EndingKind: d.Ending.EventKind, EndingChecks: d.Ending.Checks,
		Unresolved: d.Unresolved, SearchRounds: d.SearchRounds, SearchAdded: d.SearchAdded, Notes: cutNotes(d.Notes),
		NotesApplied: cutNotes(d.NotesApplied), AdjustmentsCount: len(d.Adjustments)}
	for _, c := range d.Causes {
		out.Causes = append(out.Causes, cutCauseJSON{Kind: c.Kind, Summary: c.Summary, Direction: c.Direction,
			Support: cutRefs(c.Support), Against: cutRefs(c.Against)})
	}
	if d.Origin != nil {
		o := cutRef(*d.Origin)
		out.StoryOrigin = &o
	}
	if d.EventStart != nil {
		e := cutRef(*d.EventStart)
		out.EventStart = &e
	}
	for _, b := range d.Boundaries {
		out.Boundaries = append(out.Boundaries, cutBoundaryJSON{Name: b.Name, Chosen: b.Name == d.Chosen,
			Confirmed: d.Confirmed && b.Name == effective(d), Spans: cutSpans(b.Spans), Issues: b.Issues})
	}
	v := d.Verification
	out.Verification = cutVerifyJSON{ClipOnlyPass: v.ClipPass, Answers: v.Answers, Gaps: cutFindings(v.Gaps), Asides: cutFindings(v.Asides),
		ServerChanges: v.ServerChanges, AgainstSourcePass: v.SourcePass,
		Findings: v.Findings, Notes: v.Notes, Issues: v.Issues}
	if out.Verification.Answers == nil {
		out.Verification.Answers = map[string]string{}
	}
	text, err := in.render(out, func() string {
		var b strings.Builder
		fmt.Fprintf(&b, "# AI 컷 분석 v%d (%s)\n", out.Version, stateText[out.State])
		b.WriteString(briefLine(out.cutBriefJSON))
		fmt.Fprintf(&b, "## 원인 (%s)\n", out.CauseStatus)
		for i, c := range out.Causes {
			label := "대안"
			if i == 0 {
				label = "가장 유력"
			}
			fmt.Fprintf(&b, "- %s [%s] %s · 지지 %s · 반대 %s\n", label, c.Kind, mdEscape(c.Summary), refsLine(c.Support), refsLine(c.Against))
		}
		if out.StoryOrigin != nil {
			fmt.Fprintf(&b, "- 이야기 기원: %s\n", refsLine([]cutRefJSON{*out.StoryOrigin}))
		}
		if out.EventStart != nil {
			fmt.Fprintf(&b, "- 사건 시작: %s\n", refsLine([]cutRefJSON{*out.EventStart}))
		}
		if len(out.RequiredContext) > 0 {
			fmt.Fprintf(&b, "- 필수 맥락: %s\n", refsLine(out.RequiredContext))
		}
		b.WriteString("## 경계 후보\n")
		for _, x := range out.Boundaries {
			spans := make([]string, 0, len(x.Spans))
			for _, s := range x.Spans {
				spans = append(spans, s.Start+"~"+s.End)
			}
			mark := ""
			if x.Confirmed {
				mark = " (확정)"
			} else if x.Chosen {
				mark = " (AI 선택)"
			}
			fmt.Fprintf(&b, "- %s%s: %s\n", boundaryText[x.Name], mark, strings.Join(spans, ", "))
		}
		fmt.Fprintf(&b, "- 시작 이유: %s\n- 종료 이유: %s\n- 결말: %s (%s)\n", mdEscape(out.StartReason), mdEscape(out.EndReason), out.EndingState, out.EndingKind)
		b.WriteString("## 검수\n")
		for _, k := range []string{"what", "condition", "result", "references", "why_reaction"} {
			if a, ok := out.Verification.Answers[k]; ok {
				fmt.Fprintf(&b, "- %s: %s\n", k, mdEscape(a))
			}
		}
		for _, s := range out.Verification.Issues {
			fmt.Fprintf(&b, "- 실패 이유: %s\n", mdEscape(s))
		}
		for _, f := range out.Verification.Asides {
			fmt.Fprintf(&b, "- 참고 (실패 사유 아님): %s · 근거 %s\n", mdEscape(f.Text), refsLine(f.Refs))
		}
		for _, s := range out.Verification.ServerChanges {
			fmt.Fprintf(&b, "- 서버가 바꾼 분류: %s\n", mdEscape(s))
		}
		for _, q := range out.Unresolved {
			fmt.Fprintf(&b, "- 미확정: %s\n", mdEscape(q))
		}
		for _, n := range out.Notes {
			fmt.Fprintf(&b, "- 사용자 메모: %s\n", mdEscape(n.Text))
		}
		return b.String()
	})
	return out, text, err
}

func refsLine(refs []cutRefJSON) string {
	if len(refs) == 0 {
		return "없음"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		p := r.Ref
		if r.Time != "" {
			p = r.Time + " " + p
		}
		if r.Text != "" {
			p += " \"" + mdEscape(r.Text) + "\""
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}
