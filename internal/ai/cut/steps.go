package cut

import (
	"github.com/manwol-ryu/momentforge/internal/ai"
)

// Output limits per step (thinking included where the provider counts it).
const (
	analyzeOutputTokens = 3000
	planOutputTokens    = 4000
	verifyOutputTokens  = 1500
)

// AnalyzeStep is step ②: the input as is.
func AnalyzeStep(in Input) ai.StepRequest {
	return ai.StepRequest{Name: "analyze", Prompt: PromptAnalyze, Schema: AnalyzeSchema(), SchemaName: "cut_analysis",
		Input: in, MaxOutputTokens: analyzeOutputTokens}
}

// PlanStep is step ④: the input, the analysis, the issues of a previous
// plan (empty the first time) and the user's notes.
func PlanStep(in Input, an Analysis, previousIssues []string, notes []UserNote) ai.StepRequest {
	if previousIssues == nil {
		previousIssues = []string{}
	}
	if notes == nil {
		notes = []UserNote{}
	}
	return ai.StepRequest{Name: "plan", Prompt: PromptPlan, Schema: PlanSchema(), SchemaName: "cut_plan",
		Input:           map[string]any{"input": in, "analysis": an, "previous_issues": previousIssues, "user_notes": notes},
		MaxOutputTokens: planOutputTokens}
}

// ClipSpan is one chosen span with only the evidence inside it.
type ClipSpan struct {
	StartMs  int64          `json:"start_ms"`
	EndMs    int64          `json:"end_ms"`
	Role     string         `json:"role"`
	Evidence []ClipEvidence `json:"evidence"`
}

// ClipEvidence is one piece of evidence in the clip. Key marks what the plan
// leans on to explain the target, so the check can tell a reference the
// target needs from a single viewer's side remark that only happens to be in
// the clip.
type ClipEvidence struct {
	Evidence
	Key bool `json:"key,omitempty"`
}

// KeyRefs is what a plan leans on to explain its target: the first cause's
// support, the required context, the story origin and the event start.
func KeyRefs(an Analysis, answer PlanAnswer) []string {
	var out []string
	if len(an.Causes) > 0 {
		out = append(out, an.Causes[0].Support...)
	}
	out = append(out, answer.RequiredContextRefs...)
	for _, r := range []string{answer.StoryOriginRef, answer.EventStartRef} {
		if r != "" {
			out = append(out, r)
		}
	}
	return out
}

// SelectedEvidence is the evidence that lies inside the spans, in time order.
func SelectedEvidence(spans []Span, evidence []Evidence) []Evidence {
	var out []Evidence
	for _, e := range evidence {
		if Within(e, spans) {
			out = append(out, e)
		}
	}
	return out
}

// VerifyClipStep is step ⑥: the chosen spans with their evidence only, the
// refs in key marked (KeyRefs).
func VerifyClipStep(target string, spans []Span, evidence []Evidence, key []string) ai.StepRequest {
	isKey := make(map[string]bool, len(key))
	for _, r := range key {
		isKey[r] = true
	}
	clip := make([]ClipSpan, 0, len(spans))
	for _, s := range spans {
		cs := ClipSpan{StartMs: s.StartMs, EndMs: s.EndMs, Role: s.Role, Evidence: []ClipEvidence{}}
		for _, e := range evidence {
			if Within(e, []Span{s}) {
				cs.Evidence = append(cs.Evidence, ClipEvidence{Evidence: e, Key: isKey[e.Ref]})
			}
		}
		clip = append(clip, cs)
	}
	return ai.StepRequest{Name: "verify_clip", Prompt: PromptVerifyClip, Schema: VerifyClipSchema(), SchemaName: "clip_check",
		Input: map[string]any{"target": target, "spans": clip}, MaxOutputTokens: verifyOutputTokens}
}

// VerifySourceStep is step ⑦: the plan, the evidence inside it and the
// evidence around it (everything read that is not inside).
func VerifySourceStep(target, chosen string, spans []Span, required []string, ending Ending, evidence []Evidence) ai.StepRequest {
	selected, context := []Evidence{}, []Evidence{}
	for _, e := range evidence {
		if Within(e, spans) {
			selected = append(selected, e)
		} else {
			context = append(context, e)
		}
	}
	refs := make([]SpanRefs, 0, len(spans))
	for _, s := range spans {
		refs = append(refs, SpanRefs{StartRef: s.StartRef, EndRef: s.EndRef, Role: s.Role})
	}
	if required == nil {
		required = []string{}
	}
	return ai.StepRequest{Name: "verify_source", Prompt: PromptVerifySource, Schema: VerifySourceSchema(), SchemaName: "source_check",
		Input: map[string]any{
			"plan":     map[string]any{"target": target, "chosen": chosen, "spans": refs, "required_context_refs": required, "ending": ending},
			"selected": selected,
			"context":  context,
		}, MaxOutputTokens: verifyOutputTokens}
}

// Steps lists the step names in order, for budgets and logs.
var Steps = []string{"analyze", "plan", "verify_clip", "verify_source"}

// BaseRequestsPerCandidate is the most requests one candidate takes without
// searching: the four steps and one re-plan with its two checks. Searching
// adds one analyze step per round (MaxRequests).
const BaseRequestsPerCandidate = 7
