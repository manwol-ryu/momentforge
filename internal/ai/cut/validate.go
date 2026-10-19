package cut

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

var errFormat = domain.NewUserError("AI_FORMAT", "AI 응답 형식을 확인하지 못했습니다.")

// decodeStrict reads a step answer into v, refusing unknown fields.
func decodeStrict(raw json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errFormat
	}
	return nil
}

// Known indexes the evidence the model was given, by ref.
func Known(evidence []Evidence) map[string]Evidence {
	m := make(map[string]Evidence, len(evidence))
	for _, e := range evidence {
		m[e.Ref] = e
	}
	return m
}

// keepKnown drops refs the model never saw and counts them.
func keepKnown(refs []string, known map[string]Evidence) ([]string, int) {
	out := make([]string, 0, len(refs))
	dropped := 0
	seen := map[string]bool{}
	for _, r := range refs {
		if _, ok := known[r]; !ok {
			dropped++
			continue
		}
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	return out, dropped
}

func textOK(s string, max int) bool {
	n := len([]rune(s))
	return n > 0 && n <= max
}

// ParseAnalysis reads the analyze step's answer. Refs the model was not
// given are dropped (counted in dropped); a cause left without support is
// kept but moved last.
func ParseAnalysis(raw json.RawMessage, known map[string]Evidence) (Analysis, int, error) {
	var an Analysis
	if err := decodeStrict(raw, &an); err != nil {
		return an, 0, err
	}
	if !textOK(an.Target, 400) || len(an.Causes) == 0 {
		return an, 0, errFormat
	}
	dropped := 0
	var supported, unsupported []Cause
	for _, c := range an.Causes {
		if !textOK(c.Summary, 1000) {
			return an, 0, errFormat
		}
		var d int
		c.Support, d = keepKnown(c.Support, known)
		dropped += d
		c.Against, d = keepKnown(c.Against, known)
		dropped += d
		if len(c.Support) > 0 {
			supported = append(supported, c)
		} else {
			unsupported = append(unsupported, c)
		}
	}
	an.Causes = append(supported, unsupported...)
	if len(supported) == 0 {
		an.CauseStatus = "undetermined"
	}
	if len(an.Questions) > 6 {
		an.Questions = an.Questions[:6]
	}
	if len(an.SearchRequests) > 6 {
		an.SearchRequests = an.SearchRequests[:6]
	}
	if an.Questions == nil {
		an.Questions = []string{}
	}
	if an.SearchRequests == nil {
		an.SearchRequests = []SearchRequest{}
	}
	return an, dropped, nil
}

// ParsePlan reads the plan step's answer (format only; CheckPlan judges it).
func ParsePlan(raw json.RawMessage) (PlanAnswer, error) {
	var p PlanAnswer
	if err := decodeStrict(raw, &p); err != nil {
		return p, err
	}
	if !textOK(p.StartReason, 1000) || !textOK(p.EndReason, 1000) || len(p.Candidates) == 0 {
		return p, errFormat
	}
	for _, s := range []*[]string{&p.AnalysisEvidenceRefs, &p.RequiredContextRefs, &p.OptionalContextRefs, &p.UnresolvedQuestions, &p.Ending.Checks} {
		if *s == nil {
			*s = []string{}
		}
	}
	return p, nil
}

// Checked is a plan that passed the structural checks, with its times.
type Checked struct {
	Spans       []Span
	Adjustments []Adjustment
	Dropped     int // refs the model was not given
}

// CheckPlan applies the rules of docs/AI-CUT-ANALYSIS.md 4.3 to the chosen
// boundary candidate: every ref must have been read, the required context
// must be among the analysis evidence and inside the spans, spans must be
// ordered and within the limits. Issues are returned in words the model
// can act on in a re-plan; a plan with issues is not used as is.
func CheckPlan(p *PlanAnswer, known map[string]Evidence, durationMs int64) (Checked, []string) {
	var issues []string
	var out Checked
	var d int
	p.AnalysisEvidenceRefs, d = keepKnown(p.AnalysisEvidenceRefs, known)
	out.Dropped += d
	p.RequiredContextRefs, d = keepKnown(p.RequiredContextRefs, known)
	out.Dropped += d
	p.OptionalContextRefs, d = keepKnown(p.OptionalContextRefs, known)
	out.Dropped += d
	if d > 0 {
		issues = append(issues, "이 분석에서 읽지 않은 근거 ref를 썼습니다. 입력 evidence의 ref만 쓰세요")
	}
	for _, ref := range []*string{&p.StoryOriginRef, &p.EventStartRef} {
		if *ref != "" {
			if _, ok := known[*ref]; !ok {
				issues = append(issues, fmt.Sprintf("%s는 이 분석에서 읽은 근거가 아닙니다", *ref))
				*ref = ""
			}
		}
	}
	analysis := map[string]bool{}
	for _, r := range p.AnalysisEvidenceRefs {
		analysis[r] = true
	}
	for _, r := range p.RequiredContextRefs {
		if !analysis[r] {
			issues = append(issues, fmt.Sprintf("필수 맥락 %s가 analysis_evidence_refs에 없습니다", r))
		}
	}
	var chosen *BoundaryCandidate
	for i := range p.Candidates {
		if p.Candidates[i].Name == p.Chosen {
			chosen = &p.Candidates[i]
		}
	}
	if chosen == nil {
		issues = append(issues, fmt.Sprintf("chosen %q에 해당하는 boundary_candidates 항목이 없습니다", p.Chosen))
		return out, issues
	}
	if len(chosen.Spans) == 0 {
		issues = append(issues, "고른 후보에 span이 없습니다")
		return out, issues
	}
	if len(chosen.Spans) > MaxSpans {
		issues = append(issues, fmt.Sprintf("span이 %d개입니다. %d개 이하로 합치세요", len(chosen.Spans), MaxSpans))
	}
	spans, adj, spanIssues := ResolveSpans(chosen.Spans, known, durationMs)
	issues = append(issues, spanIssues...)
	if len(spans) == 0 {
		return out, issues
	}
	if total := TotalMs(spans); total > MaxTotalSpanMs {
		issues = append(issues, fmt.Sprintf("구간 합계가 %d초입니다. %d초 이하로 줄이되 필수 맥락은 빼지 마세요", total/1000, MaxTotalSpanMs/1000))
	}
	for _, r := range p.RequiredContextRefs {
		if e, ok := known[r]; ok && !Within(e, spans) {
			issues = append(issues, fmt.Sprintf("필수 맥락 %s가 고른 span 안에 없습니다. 그 근거를 담는 span을 두세요", r))
		}
	}
	out.Spans, out.Adjustments = spans, adj
	return out, issues
}

// ParseClipVerification reads the clip-only check's answer.
func ParseClipVerification(raw json.RawMessage) (ClipVerification, error) {
	var v ClipVerification
	if err := decodeStrict(raw, &v); err != nil {
		return v, err
	}
	for _, k := range []string{"what", "condition", "result", "references", "why_reaction"} {
		if _, ok := v.Answers[k]; !ok {
			return v, errFormat
		}
	}
	if v.Gaps == nil {
		v.Gaps = []string{}
	}
	if v.Asides == nil {
		v.Asides = []string{}
	}
	if len(v.Asides) > 6 {
		v.Asides = v.Asides[:6]
	}
	return v, nil
}

// ParseSourceVerification reads the against-source check's answer; pass is
// recomputed from the four findings so a model cannot pass with one set.
func ParseSourceVerification(raw json.RawMessage) (SourceVerification, error) {
	var v SourceVerification
	if err := decodeStrict(raw, &v); err != nil {
		return v, err
	}
	v.Pass = !(v.MixedAttempt || v.CutCondition || v.MissedCorrection || v.EndedEarly)
	if v.Notes == nil {
		v.Notes = []string{}
	}
	if len(v.Notes) > 6 {
		v.Notes = v.Notes[:6]
	}
	return v, nil
}

// Issues turns verification results into the issues a re-plan must fix.
func Issues(clip *ClipVerification, src *SourceVerification) []string {
	var out []string
	if clip != nil && !clip.Pass {
		for _, g := range clip.Gaps {
			out = append(out, "클립만 봐서는 알 수 없음: "+g)
		}
		if len(clip.Gaps) == 0 {
			out = append(out, "클립만 봐서는 상황을 이해할 수 없다고 검수됨")
		}
	}
	if src != nil && !src.Pass {
		for _, n := range src.Notes {
			out = append(out, "원본 비교: "+n)
		}
		if len(src.Notes) == 0 {
			out = append(out, "원본과 비교해 잘림이나 누락이 있다고 검수됨")
		}
	}
	return out
}
