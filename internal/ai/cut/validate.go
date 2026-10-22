package cut

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

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
	if out.Dropped > 0 {
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

// ParseClipVerification reads the clip-only check's answer and sorts its
// findings again by the clip it was given (ReclassifyClip).
func ParseClipVerification(raw json.RawMessage, clip []ClipSpan) (ClipVerification, error) {
	var v ClipVerification
	var answer struct {
		Pass    bool              `json:"pass"`
		Answers map[string]string `json:"answers"`
		Gaps    []ClipFinding     `json:"gaps"`
		Asides  []ClipFinding     `json:"asides"`
	}
	if err := decodeStrict(raw, &answer); err != nil {
		return v, err
	}
	for _, k := range []string{"what", "condition", "result", "references", "why_reaction"} {
		if _, ok := answer.Answers[k]; !ok {
			return v, errFormat
		}
	}
	v = ClipVerification{Pass: answer.Pass, Answers: answer.Answers, Gaps: firstFindings(answer.Gaps), Asides: firstFindings(answer.Asides)}
	ReclassifyClip(&v, clip)
	return v, nil
}

const maxFindings = 6

func firstFindings(list []ClipFinding) []ClipFinding {
	out := make([]ClipFinding, 0, min(len(list), maxFindings))
	for _, f := range list {
		if len(out) == maxFindings {
			break
		}
		if f.Refs == nil {
			f.Refs = []string{}
		}
		out = append(out, f)
	}
	return out
}

// ReclassifyClip sorts the model's gaps and asides again by the refs they
// cite, so whether a finding fails the check does not rest on the model's
// wording alone:
//   - refs that are not in the clip are dropped;
//   - an aside that cites key evidence, or chats of two or more authors
//     pointing at the same past, is a gap;
//   - a gap that cites only chats of one author, none of them key, is an
//     aside (a single viewer's side remark);
//   - the check passes when no gap is left, unless the model failed it
//     without naming a gap.
//
// Every move is recorded in v.Changes for the result box.
func ReclassifyClip(v *ClipVerification, clip []ClipSpan) {
	in := map[string]ClipEvidence{}
	for _, s := range clip {
		for _, e := range s.Evidence {
			in[e.Ref] = e
		}
	}
	failedOnGaps := !v.Pass && len(v.Gaps) > 0
	gaps, asides := []ClipFinding{}, []ClipFinding{}
	for _, f := range v.Gaps {
		f = v.keepInClip(f, "gap", in)
		if why := sideRemark(f, in); why != "" {
			asides = append(asides, f)
			v.Changes = append(v.Changes, ClipChange{Text: f.Text, From: "gap", To: "aside", Why: why})
			continue
		}
		gaps = append(gaps, f)
	}
	for _, f := range v.Asides {
		f = v.keepInClip(f, "aside", in)
		if why := neededRef(f, in); why != "" {
			gaps = append(gaps, f)
			v.Changes = append(v.Changes, ClipChange{Text: f.Text, From: "aside", To: "gap", Why: why})
			continue
		}
		asides = append(asides, f)
	}
	v.Gaps, v.Asides = gaps, asides
	pass := len(gaps) == 0 && (v.Pass || failedOnGaps)
	switch {
	case pass && !v.Pass:
		v.Changes = append(v.Changes, ClipChange{From: "fail", To: "pass", Why: "실패 사유가 모두 곁가지로 내려가 통과로 바꿈"})
	case !pass && v.Pass:
		v.Changes = append(v.Changes, ClipChange{From: "pass", To: "fail", Why: "실패 사유가 남아 있어 실패로 바꿈"})
	}
	v.Pass = pass
}

// keepInClip drops the refs of f that are not in the clip, recording it.
func (v *ClipVerification) keepInClip(f ClipFinding, kind string, in map[string]ClipEvidence) ClipFinding {
	kept, dropped := []string{}, []string{}
	seen := map[string]bool{}
	for _, r := range f.Refs {
		switch {
		case seen[r]:
		case in[r].Ref == "":
			dropped = append(dropped, r)
		default:
			kept = append(kept, r)
		}
		seen[r] = true
	}
	if len(dropped) > 0 {
		v.Changes = append(v.Changes, ClipChange{Text: f.Text, From: kind, To: kind,
			Why: "클립에 없는 근거 " + strings.Join(dropped, ", ") + "를 뺌"})
	}
	f.Refs = kept
	return f
}

// neededRef says why an aside is needed for the target ("" when it is not):
// it cites key evidence or chats of several authors.
func neededRef(f ClipFinding, in map[string]ClipEvidence) string {
	for _, r := range f.Refs {
		if in[r].Key {
			return "계획이 기댄 근거(key) " + r + "를 가리킴"
		}
	}
	if n := len(chatAuthors(f, in)); n >= 2 {
		return fmt.Sprintf("서로 다른 작성자 %d명의 채팅이 같은 과거를 가리킴", n)
	}
	return ""
}

// sideRemark says why a gap is a single viewer's side remark ("" when it is
// not): every ref is a chat that is not key, all of one known author.
func sideRemark(f ClipFinding, in map[string]ClipEvidence) string {
	if len(f.Refs) == 0 {
		return ""
	}
	for _, r := range f.Refs {
		e := in[r]
		if e.Key || e.Kind != KindChat || e.Author == nil {
			return ""
		}
	}
	if len(chatAuthors(f, in)) != 1 {
		return ""
	}
	return "key가 아닌 시청자 한 사람의 채팅만 가리킴"
}

// chatAuthors are the distinct known authors of the chats f cites.
func chatAuthors(f ClipFinding, in map[string]ClipEvidence) map[string]bool {
	out := map[string]bool{}
	for _, r := range f.Refs {
		if e := in[r]; e.Kind == KindChat && e.Author != nil {
			out[*e.Author] = true
		}
	}
	return out
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
			issue := "클립만 봐서는 알 수 없음: " + g.Text
			if len(g.Refs) > 0 {
				issue += " (근거 " + strings.Join(g.Refs, ", ") + ")"
			}
			out = append(out, issue)
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
