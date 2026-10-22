// Package cut is the second-generation AI analysis (docs/AI-CUT-ANALYSIS.md):
// from one detected candidate it explains the reaction's cause with
// evidence, chooses the context a clip must keep, proposes boundaries as
// evidence IDs (never milliseconds) and verifies the result twice. This
// package holds the step instructions, their schemas, the parsing and the
// checks; it sends nothing. The app runs the steps and resolves times.
package cut

import (
	"bytes"
	"encoding/json"
)

// Evidence kinds and their ref prefixes.
const (
	KindUtterance = "utterance" // utt:<id>, what the broadcaster said (local speech recognition or a caption file)
	KindChat      = "chat"      // chat:<id>, a viewer's message
	KindComment   = "comment"   // comment:<id>, a viewer's timestamped video comment
)

// Evidence is one thing the model may cite. Utterances span a time; chats
// and comments are moments (EndMs 0).
type Evidence struct {
	Ref     string  `json:"ref"`
	Kind    string  `json:"kind"`
	StartMs int64   `json:"start_ms"`
	EndMs   int64   `json:"end_ms,omitempty"`
	Speaker string  `json:"speaker,omitempty"` // utterances: a role such as 방송자
	Author  *string `json:"author,omitempty"`  // chats: the per-session pseudonym
	Text    string  `json:"text"`
	// ChatKind marks a chat that is not an ordinary message: donation,
	// subscription or system (absent for a message).
	ChatKind string `json:"chat_kind,omitempty"`
	// FoundBySearch marks evidence a search brought in after the first reading.
	FoundBySearch bool `json:"found_by_search,omitempty"`
}

// Candidate is the detected moment the analysis is about.
type Candidate struct {
	ID          string         `json:"id"` // unified timeline item ID (rule:… or viewer_comment:…)
	SourceType  string         `json:"source_type"`
	AnchorMs    int64          `json:"anchor_ms"`
	ClipStartMs int64          `json:"clip_start_ms"`
	ClipEndMs   int64          `json:"clip_end_ms"`
	Title       string         `json:"title"`
	Statistics  map[string]any `json:"statistics"`
	// Reaction is what the detector measured (rule candidates only).
	Reaction *Reaction `json:"reaction,omitempty"`
}

// Reaction is a rule candidate's chat reaction as the detector found it:
// the span the chat reacted in, its peak and, from a whole-broadcast
// analysis, how strong it was for this broadcast with the counts that stood
// out at the peak next to what is usual then.
type Reaction struct {
	StartMs  int64     `json:"start_ms"`
	EndMs    int64     `json:"end_ms"`
	PeakMs   int64     `json:"peak_ms"`
	Strength string    `json:"strength,omitempty"` // strong | medium | weak
	Score    float64   `json:"score,omitempty"`    // how unusual the peak was for its time in the broadcast (z-score scale)
	Measures []Measure `json:"measures,omitempty"`
}

// Measure is one count at the peak: "laugh: 7 authors in 10 s, usually 0.6".
type Measure struct {
	Key      string  `json:"key"` // laugh, emotion:surprise, repeat:<words>, reaction, surge (messages), participants (authors)
	Observed int     `json:"observed"`
	Usual    float64 `json:"usual"`
	WindowS  int64   `json:"window_s"`
}

// Curve is the chat over the candidate in fixed bins, every counted message
// included (also those left out of the evidence; never hidden ones), so the
// model can see where the reaction rises and fades.
type Curve struct {
	BinMs int64 `json:"bin_ms"`
	// What a bin usually holds in the minutes around (median), for scale.
	UsualChats   float64    `json:"usual_chats"`
	UsualAuthors float64    `json:"usual_authors"`
	Bins         []CurveBin `json:"bins"`
	Partial      bool       `json:"partial,omitempty"` // too much chat to count it all: bins stop where the count stopped
}

// CurveBin is one bin of the curve.
type CurveBin struct {
	StartMs int64 `json:"start_ms"`
	Chats   int   `json:"chats"`
	Authors int   `json:"authors"`
	// Reacting counts authors who share a reaction (laughter, surprise, the
	// same words) with another author in the bin, as the detector does.
	Reacting int `json:"reacting"`
}

// Input is what one candidate's analysis starts from: the candidate(s), the
// window read around them and every piece of evidence in it. The same
// evidence list is the only thing the first steps may cite.
type Input struct {
	Candidates    []Candidate `json:"candidates"`
	WindowStartMs int64       `json:"window_start_ms"`
	WindowEndMs   int64       `json:"window_end_ms"`
	DurationMs    int64       `json:"duration_ms"`
	Sources       []string    `json:"sources"` // utterances | chat | comment, what this session has
	Evidence      []Evidence  `json:"evidence"`
	// ChatsLeftOut counts the window's chat that did not fit in the evidence
	// (the reaction itself is kept first; the curve counts everything).
	ChatsLeftOut int    `json:"chats_left_out,omitempty"`
	Curve        *Curve `json:"curve,omitempty"`
	// Searched lists the requests already run for this candidate and what
	// they found (empty on the first analyze step).
	Searched []SearchResult `json:"searched,omitempty"`
}

// Cause kinds and reaction directions.
const (
	CauseBroadcastEvent = "broadcast_event"
	CauseCallback       = "callback_to_earlier"
	CauseViewerJoke     = "viewer_joke"
	CauseDonation       = "donation"
	CauseUnknown        = "unknown"
)

// Cause is one explanation of the reaction, with the evidence for and against it.
type Cause struct {
	Kind      string   `json:"kind"`
	Summary   string   `json:"summary"`
	Support   []string `json:"support"`
	Against   []string `json:"against"`
	Direction string   `json:"direction"` // broadcast_to_viewers | viewer_to_broadcaster_to_viewers | unknown
}

// SearchRequest is what the model asks the server to look up (search.go:
// the server runs it within the budget and analyses again with the result).
type SearchRequest struct {
	Tool     string `json:"tool"` // search | read_around | read_range | events | activity | list_threads
	Query    string `json:"query,omitempty"`
	In       string `json:"in,omitempty"` // utterances | chats | both
	BeforeMs int64  `json:"before_ms,omitempty"`
	AtMs     int64  `json:"at_ms,omitempty"`
	FromMs   int64  `json:"from_ms,omitempty"`
	ToMs     int64  `json:"to_ms,omitempty"`
	WindowS  int64  `json:"window_s,omitempty"`
}

// Analysis is the answer of the analyze step.
type Analysis struct {
	Target         string          `json:"target"` // what the viewers reacted to
	Causes         []Cause         `json:"causes"`
	CauseStatus    string          `json:"cause_status"` // confirmed | likely | undetermined
	Questions      []string        `json:"questions"`    // still open after this reading
	SearchRequests []SearchRequest `json:"search_requests"`
}

// SpanRefs is a span of the video said with evidence IDs.
type SpanRefs struct {
	StartRef string `json:"start_ref"`
	EndRef   string `json:"end_ref"`
	Role     string `json:"role"` // setup | main | payoff
}

// BoundaryCandidate is one way to cut: short, with context, or the whole event.
type BoundaryCandidate struct {
	Name  string     `json:"name"` // short | context | full_event
	Spans []SpanRefs `json:"spans"`
}

// Ending is how the event closes and whether the clip reaches it.
type Ending struct {
	State     string   `json:"state"`      // confirmed | pending | cut_off | insufficient | unresolved
	EventKind string   `json:"event_kind"` // question_answer | success_failure | joke_mistake | claim_argument | promise_penalty | other
	Checks    []string `json:"checks"`
}

// PlanAnswer is the answer of the plan step: what to keep and where to cut,
// as evidence IDs.
type PlanAnswer struct {
	StoryOriginRef       string              `json:"story_origin_ref"` // "" when none was found
	OriginConfidence     string              `json:"origin_confidence"`
	EventStartRef        string              `json:"event_start_ref"`
	AnalysisEvidenceRefs []string            `json:"analysis_evidence_refs"`
	RequiredContextRefs  []string            `json:"required_context_refs"`
	OptionalContextRefs  []string            `json:"optional_context_refs"`
	Candidates           []BoundaryCandidate `json:"boundary_candidates"`
	Chosen               string              `json:"chosen"`
	StartReason          string              `json:"start_reason"`
	EndReason            string              `json:"end_reason"`
	Ending               Ending              `json:"ending"`
	UnresolvedQuestions  []string            `json:"unresolved_questions"`
}

// Span is a resolved span: the IDs the model named and the times the
// server derived from them.
type Span struct {
	StartRef string `json:"start_ref"`
	EndRef   string `json:"end_ref"`
	StartMs  int64  `json:"start_ms"`
	EndMs    int64  `json:"end_ms"`
	Role     string `json:"role"`
}

// Adjustment records a time the server moved from the evidence's own time.
type Adjustment struct {
	Ref    string `json:"ref"`
	FromMs int64  `json:"from_ms"`
	ToMs   int64  `json:"to_ms"`
	Why    string `json:"why"`
}

// ClipVerification is the first check: the clip alone, without the rest of
// the broadcast, must make sense. The model sorts what the clip leaves
// unknown into gaps and asides; the server sorts them again by the refs
// (ReclassifyClip) and records every move in Changes.
type ClipVerification struct {
	Pass    bool              `json:"pass"`
	Answers map[string]string `json:"answers"` // what, condition, result, references, why_reaction
	Gaps    []ClipFinding     `json:"gaps"`    // missing pieces the target needs: these fail the check
	Asides  []ClipFinding     `json:"asides"`  // unknowns the target does not need (a single viewer's side remark): shown, never a failure
	Changes []ClipChange      `json:"server_changes,omitempty"`
}

// ClipFinding is one thing the clip leaves unknown: the clip evidence that
// points at it and what is missing.
type ClipFinding struct {
	Refs []string `json:"refs"`
	Text string   `json:"text"`
}

// UnmarshalJSON also reads a plain string, the form of plans checked with
// prompt v2 or earlier (no refs).
func (f *ClipFinding) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		*f = ClipFinding{Refs: []string{}, Text: s}
		return nil
	}
	type plain ClipFinding
	var v plain
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		return err
	}
	*f = ClipFinding(v)
	if f.Refs == nil {
		f.Refs = []string{}
	}
	return nil
}

// ClipChange is a move the server made to the model's sorting.
type ClipChange struct {
	Text string `json:"text,omitempty"` // the finding as the model wrote it ("" for the verdict)
	From string `json:"from"`           // gap | aside, or pass | fail for the verdict
	To   string `json:"to"`
	Why  string `json:"why"`
}

// SourceVerification is the second check: against the surrounding
// broadcast, nothing was mixed up, cut off or left out.
type SourceVerification struct {
	Pass             bool     `json:"pass"`
	MixedAttempt     bool     `json:"mixed_attempt"`
	CutCondition     bool     `json:"cut_condition"`
	MissedCorrection bool     `json:"missed_correction"`
	EndedEarly       bool     `json:"ended_early"`
	Notes            []string `json:"notes"`
}

// Verification is everything that checked the plan.
type Verification struct {
	ClipOnly      *ClipVerification   `json:"clip_only,omitempty"`
	AgainstSource *SourceVerification `json:"against_source,omitempty"`
	EvidenceMatch string              `json:"evidence_match"` // all_read | some_unread (refs the model never saw were dropped)
	BoundaryOK    bool                `json:"boundary_ok"`
	Issues        []string            `json:"issues"` // why the plan is not trusted (empty when it is)
}

// Version pins what produced a plan, so plans can be compared and redone.
type Version struct {
	Pipeline     int            `json:"pipeline"`
	Prompts      map[string]int `json:"prompts"`
	Provider     string         `json:"provider"`
	Model        string         `json:"model"`
	Sources      []string       `json:"sources"`
	TranscriptID string         `json:"transcript_id,omitempty"`
}

// UserNote is a correction the user left on a plan; the next analysis reads it.
type UserNote struct {
	At        string `json:"at"`
	Text      string `json:"text"`
	AppliesTo string `json:"applies_to,omitempty"`
}

// Plan states.
const (
	StateOK           = "ok"            // both verifications passed
	StateVerifyFailed = "verify_failed" // saved with its issues; not to be trusted as is
	StateFailed       = "failed"        // a step could not be completed
)

// Plan is the result for one candidate (docs/AI-CUT-ANALYSIS.md 4.1).
type Plan struct {
	ID           string   `json:"id"`
	RunID        string   `json:"run_id"`
	SessionID    string   `json:"session_id"`
	CandidateIDs []string `json:"candidate_ids"`
	StoryID      string   `json:"story_id,omitempty"`
	Version      int      `json:"version"`
	Supersedes   string   `json:"supersedes,omitempty"`
	State        string   `json:"state"`
	Sources      []string `json:"sources"`

	Reaction struct {
		PeakMs int64  `json:"peak_ms"`
		Target string `json:"target"`
	} `json:"reaction"`
	// CandidateClip is the analysed candidate's clip (pipeline 4 and later),
	// so the plan finds its candidate again by time after the session is
	// analysed anew: rule candidate IDs are serial and change.
	CandidateClip *ClipRange `json:"candidate_clip,omitempty"`

	Causes      []Cause `json:"causes"`
	CauseStatus string  `json:"cause_status"`

	Times struct {
		StoryOriginRef   string `json:"story_origin_ref,omitempty"`
		StoryOriginMs    int64  `json:"story_origin_ms,omitempty"`
		OriginConfidence string `json:"origin_confidence,omitempty"`
		EventStartRef    string `json:"event_start_ref,omitempty"`
		EventStartMs     int64  `json:"event_start_ms,omitempty"`
		CutStartRef      string `json:"cut_start_ref,omitempty"`
	} `json:"times"`

	AnalysisEvidenceRefs []string `json:"analysis_evidence_refs"`
	RequiredContextRefs  []string `json:"required_context_refs"`
	OptionalContextRefs  []string `json:"optional_context_refs"`
	SelectedSpans        []Span   `json:"selected_spans"`
	Boundaries           struct {
		Candidates  []BoundaryCandidate `json:"candidates"`
		Chosen      string              `json:"chosen"`
		StartReason string              `json:"start_reason"`
		EndReason   string              `json:"end_reason"`
		Adjustments []Adjustment        `json:"adjustments"`
		// Resolved holds every candidate with its times (ResolveBoundaries);
		// the chosen one equals SelectedSpans.
		Resolved []ResolvedBoundary `json:"resolved"`
	} `json:"boundary_decisions"`
	Ending Ending `json:"ending"`

	SearchedRanges      []SearchRequest `json:"searched_ranges"` // the requests that ran
	Searches            []SearchResult  `json:"searches"`        // each with what it found
	SearchBudget        BudgetUse       `json:"search_budget"`
	UnresolvedQuestions []string        `json:"unresolved_questions"`
	Verification        Verification    `json:"verification"`
	AnalysisVersion     Version         `json:"analysis_version"`
	// Refs keeps the time and first words of every cited ref (IndexRefs).
	Refs map[string]RefInfo `json:"refs"`
	// UserNotes are the notes left on this version; NotesApplied are the
	// notes (left on earlier versions) this version was planned with.
	UserNotes    []UserNote `json:"user_notes"`
	NotesApplied []UserNote `json:"notes_applied"`
	// ConfirmedByUser marks the version the user settled on; the boundary is
	// the candidate they confirmed (short | context | full_event), which the
	// confirmed exports cut by.
	ConfirmedByUser   bool   `json:"confirmed_by_user"`
	ConfirmedBoundary string `json:"confirmed_boundary,omitempty"`
	ConfirmedAt       string `json:"confirmed_at,omitempty"`
}

// EffectiveSpans are the spans the plan stands for: the confirmed boundary
// candidate when the user picked one, else the chosen spans.
func (p *Plan) EffectiveSpans() []Span {
	if p.ConfirmedBoundary != "" && p.ConfirmedBoundary != p.Boundaries.Chosen {
		for _, b := range p.Boundaries.Resolved {
			if b.Name == p.ConfirmedBoundary && len(b.Spans) > 0 {
				return b.Spans
			}
		}
	}
	return p.SelectedSpans
}

// ClipRange is a span of the video in milliseconds.
type ClipRange struct {
	StartMs int64 `json:"start_ms"`
	EndMs   int64 `json:"end_ms"`
}

// Limits of a plan.
const (
	MaxSpans        = 4
	MaxTotalSpanMs  = 10 * 60 * 1000
	PipelineVersion = 4 // 2: searches run (6-C); 3: speech-edge padding, every boundary resolved, user notes (6-D); 4: the detector's reaction and a chat curve in the input, reaction-first chat
)
