package app

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/export"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// The user's decisions on cut plans (docs/AI-CUT-ANALYSIS.md 6.3, phase
// 6-D): a note on a version goes into the next analysis of the same
// candidates, which makes a new version; confirming a version keeps it as
// the current one (a later version is shown beside it, not over it) and
// puts it in the confirmed exports for editnest and editors.
const (
	maxNoteRunes     = 500
	maxNotesPerPlan  = 20
	maxAppliesToChar = 60
)

// cutDecisionMu serialises read-modify-write of a plan's decisions (a note
// and a confirmation arriving together must not lose one another).
var cutDecisionMu sync.Mutex

// decodePlan reads a stored plan with the user's decisions from its columns
// (the columns are the record; the JSON copy follows them).
func decodePlan(rec store.CutPlan) (cut.Plan, bool) {
	var p cut.Plan
	if json.Unmarshal(rec.PlanJSON, &p) != nil {
		return p, false
	}
	p.ID, p.Version, p.Supersedes, p.State = rec.ID, rec.Version, rec.Supersedes, rec.State
	p.ConfirmedByUser, p.ConfirmedBoundary = rec.Confirmed, rec.ConfirmedBoundary
	p.ConfirmedAt = ""
	if !rec.ConfirmedAt.IsZero() {
		p.ConfirmedAt = rec.ConfirmedAt.UTC().Format(time.RFC3339)
	}
	var notes []cut.UserNote
	if json.Unmarshal(rec.UserNotesJSON, &notes) == nil && notes != nil {
		p.UserNotes = notes
	}
	if p.UserNotes == nil {
		p.UserNotes = []cut.UserNote{}
	}
	return p, true
}

// cutNotes are the notes left on any version of one candidate set's plans,
// oldest first, without repeats: the next plan step reads them all.
func (a *App) cutNotes(ctx context.Context, sid string, candidateIDs []string) ([]cut.UserNote, error) {
	versions, err := a.cfg.Store.CutPlanVersions(ctx, sid, candidateIDs)
	if err != nil {
		return nil, err
	}
	var out []cut.UserNote
	seen := map[string]bool{}
	for _, rec := range versions {
		var notes []cut.UserNote
		if json.Unmarshal(rec.UserNotesJSON, &notes) != nil {
			continue
		}
		for _, n := range notes {
			if k := n.At + "\x00" + n.Text; !seen[k] {
				seen[k] = true
				out = append(out, n)
			}
		}
	}
	return out, nil
}

// saveDecision writes a plan whose decisions changed: the row, the JSON
// copy and the plan's own files.
func (a *App) saveDecision(ctx context.Context, rec store.CutPlan, p cut.Plan) error {
	if spans := p.EffectiveSpans(); len(spans) > 0 {
		rec.StartMs, rec.EndMs = spans[0].StartMs, spans[len(spans)-1].EndMs
	}
	rec.Confirmed, rec.ConfirmedBoundary = p.ConfirmedByUser, p.ConfirmedBoundary
	rec.ConfirmedAt = time.Time{}
	if p.ConfirmedAt != "" {
		rec.ConfirmedAt, _ = time.Parse(time.RFC3339, p.ConfirmedAt)
	}
	rec.UserNotesJSON = ai.Encode(p.UserNotes)
	rec.PlanJSON = ai.Encode(p)
	if err := a.cfg.Store.SaveCutPlan(ctx, rec); err != nil {
		return err
	}
	a.writeCutPlan(rec.SessionID, p)
	return nil
}

// ConfirmCut confirms a plan with one of its boundary candidates ("" is the
// one the analysis chose), or takes a confirmation back. Only one version
// per candidate set is confirmed: confirming one releases the others.
func (a *App) ConfirmCut(ctx context.Context, id string, confirmed bool, boundary string) (CutPlanView, error) {
	cutDecisionMu.Lock()
	defer cutDecisionMu.Unlock()
	rec, err := a.cfg.Store.CutPlan(ctx, id)
	if err != nil {
		return CutPlanView{}, err
	}
	p, ok := decodePlan(rec)
	if !ok {
		return CutPlanView{}, domain.NewUserError("INPUT", "이 컷 분석 결과를 읽지 못해 확정할 수 없습니다.")
	}
	if confirmed {
		if len(p.SelectedSpans) == 0 {
			return CutPlanView{}, domain.NewUserError("INPUT", "구간을 정하지 못한 결과는 확정할 수 없습니다. 다시 분석해 주세요.")
		}
		if boundary == "" {
			boundary = p.Boundaries.Chosen
		}
		if !boundaryUsable(p, boundary) {
			return CutPlanView{}, domain.NewUserError("INPUT", "고른 경계 후보는 시각으로 바꾸지 못해 확정할 수 없습니다. 다른 후보를 고르세요.")
		}
		versions, err := a.cfg.Store.CutPlanVersions(ctx, rec.SessionID, rec.CandidateIDs)
		if err != nil {
			return CutPlanView{}, err
		}
		for _, other := range versions {
			if other.ID == rec.ID || !other.Confirmed {
				continue
			}
			op, ok := decodePlan(other)
			if !ok {
				continue
			}
			op.ConfirmedByUser, op.ConfirmedBoundary, op.ConfirmedAt = false, "", ""
			if err := a.saveDecision(ctx, other, op); err != nil {
				return CutPlanView{}, err
			}
		}
		p.ConfirmedByUser, p.ConfirmedBoundary, p.ConfirmedAt = true, boundary, time.Now().UTC().Format(time.RFC3339)
	} else {
		p.ConfirmedByUser, p.ConfirmedBoundary, p.ConfirmedAt = false, "", ""
	}
	if err := a.saveDecision(ctx, rec, p); err != nil {
		return CutPlanView{}, err
	}
	a.writeConfirmed(ctx, rec.SessionID)
	a.writeUnified(ctx, rec.SessionID)
	rec, err = a.cfg.Store.CutPlan(ctx, id)
	return cutView(rec), err
}

// boundaryUsable says whether a boundary candidate of the plan has times.
func boundaryUsable(p cut.Plan, name string) bool {
	if name == p.Boundaries.Chosen {
		return len(p.SelectedSpans) > 0
	}
	for _, b := range p.Boundaries.Resolved {
		if b.Name == name {
			return len(b.Spans) > 0
		}
	}
	return false
}

// AddCutNote keeps the user's correction on a plan; the next cut analysis
// of the same candidates plans with it.
func (a *App) AddCutNote(ctx context.Context, id, text, appliesTo string) (CutPlanView, error) {
	text, appliesTo = strings.TrimSpace(text), strings.TrimSpace(appliesTo)
	switch n := utf8.RuneCountInString(text); {
	case n == 0:
		return CutPlanView{}, domain.NewUserError("INPUT", "메모 내용을 적어 주세요.")
	case n > maxNoteRunes:
		return CutPlanView{}, domain.NewUserError("INPUT", "메모는 500자 이하로 적어 주세요.")
	}
	if utf8.RuneCountInString(appliesTo) > maxAppliesToChar {
		return CutPlanView{}, domain.NewUserError("INPUT", "메모의 대상은 60자 이하로 적어 주세요.")
	}
	cutDecisionMu.Lock()
	defer cutDecisionMu.Unlock()
	rec, err := a.cfg.Store.CutPlan(ctx, id)
	if err != nil {
		return CutPlanView{}, err
	}
	p, ok := decodePlan(rec)
	if !ok {
		return CutPlanView{}, domain.NewUserError("INPUT", "이 컷 분석 결과를 읽지 못해 메모를 남길 수 없습니다.")
	}
	if len(p.UserNotes) >= maxNotesPerPlan {
		return CutPlanView{}, domain.NewUserError("INPUT", "한 결과에 남길 수 있는 메모는 20개까지입니다.")
	}
	p.UserNotes = append(p.UserNotes, cut.UserNote{At: time.Now().UTC().Format(time.RFC3339Nano), Text: text, AppliesTo: appliesTo})
	if err := a.saveDecision(ctx, rec, p); err != nil {
		return CutPlanView{}, err
	}
	rec, err = a.cfg.Store.CutPlan(ctx, id)
	return cutView(rec), err
}

// writeConfirmed rewrites exports/<session>/cuts/confirmed.* from the
// confirmed plans (removed when none is left).
func (a *App) writeConfirmed(ctx context.Context, sid string) {
	if err := a.writeConfirmedTo(ctx, sid, filepath.Join(a.exportDir(sid), "cuts")); err != nil {
		a.logError("write confirmed cuts", err)
	}
}

// writeConfirmedTo writes confirmed.edl, confirmed.csv and confirmed.editnest.json into dir.
func (a *App) writeConfirmedTo(ctx context.Context, sid, dir string) error {
	s, err := a.cfg.Store.Session(ctx, sid)
	if err != nil {
		return err
	}
	plans, err := a.cfg.Store.ConfirmedCutPlans(ctx, sid)
	if err != nil {
		return err
	}
	f := export.ConfirmedCutsFile{SessionID: sid, ClipName: s.Title, DurationMs: s.DurationMs}
	if refs, err := a.cfg.Store.MediaRefs(ctx, sid); err == nil {
		for _, m := range refs {
			if m.Kind == "video" {
				f.ClipName, f.VideoName = filepath.Base(m.Path), filepath.Base(m.Path)
			}
		}
	}
	for _, rec := range plans {
		p, ok := decodePlan(rec)
		spans := p.EffectiveSpans()
		if !ok || len(spans) == 0 {
			continue
		}
		c := export.ConfirmedCut{PlanID: rec.ID, Version: rec.Version, Boundary: p.ConfirmedBoundary, Target: p.Reaction.Target, State: rec.State}
		for i, sp := range spans {
			c.Spans = append(c.Spans, export.CutSpan{Order: i + 1, StartMs: sp.StartMs, EndMs: sp.EndMs, Role: sp.Role, StartRef: sp.StartRef, EndRef: sp.EndRef})
		}
		f.Cuts = append(f.Cuts, c)
	}
	sort.SliceStable(f.Cuts, func(i, j int) bool { return f.Cuts[i].Spans[0].StartMs < f.Cuts[j].Spans[0].StartMs })
	return export.WriteConfirmedCuts(dir, f)
}

// CutEvidence is a cited ref with where to watch it (absent for a plan made
// before the times were kept) and its first words.
type CutEvidence struct {
	Ref  string `json:"ref"`
	Kind string `json:"kind"`
	AtMs *int64 `json:"atMs,omitempty"`
	Text string `json:"text,omitempty"`
}

// CutCause is one explanation with the evidence for and against it.
type CutCause struct {
	Kind      string        `json:"kind"`
	Summary   string        `json:"summary"`
	Direction string        `json:"direction"`
	Support   []CutEvidence `json:"support"`
	Against   []CutEvidence `json:"against"`
}

// CutBoundary is one boundary candidate with its spans in milliseconds.
type CutBoundary struct {
	Name   string         `json:"name"`
	Spans  []TimelineSpan `json:"spans"`
	Issues []string       `json:"issues,omitempty"`
}

// CutAdjustment is a time the server moved from the evidence's own time.
type CutAdjustment struct {
	Ref    string `json:"ref"`
	FromMs int64  `json:"fromMs"`
	ToMs   int64  `json:"toMs"`
	Why    string `json:"why"`
}

// CutEnding is how the event closes.
type CutEnding struct {
	State     string   `json:"state"`
	EventKind string   `json:"eventKind"`
	Checks    []string `json:"checks"`
}

// CutVerification is what the two checks found, in words.
type CutVerification struct {
	ClipPass      *bool             `json:"clipPass,omitempty"`
	Answers       map[string]string `json:"answers,omitempty"` // what, condition, result, references, why_reaction
	Gaps          []string          `json:"gaps"`
	Asides        []string          `json:"asides"` // unknowns the clip check judged not needed for the target
	SourcePass    *bool             `json:"sourcePass,omitempty"`
	Findings      []string          `json:"findings"` // which source checks flagged a problem
	Notes         []string          `json:"notes"`
	Issues        []string          `json:"issues"` // why the plan is not trusted
	EvidenceMatch string            `json:"evidenceMatch"`
}

// CutNote is a note the user left.
type CutNote struct {
	At        string `json:"at"`
	Text      string `json:"text"`
	AppliesTo string `json:"appliesTo,omitempty"`
}

// CutDetail is a cut plan as the result box (and MCP) shows it.
type CutDetail struct {
	PlanID            string    `json:"planId"`
	SessionID         string    `json:"sessionId"`
	CandidateIDs      []string  `json:"candidateIds"`
	Version           int       `json:"version"`
	State             string    `json:"state"`
	Confirmed         bool      `json:"confirmed"`
	ConfirmedBoundary string    `json:"confirmedBoundary,omitempty"`
	ConfirmedAt       string    `json:"confirmedAt,omitempty"`
	KeptVersion       int       `json:"keptVersion,omitempty"` // a newer version: the confirmed one that stays
	Sources           []string  `json:"sources"`
	Provider          string    `json:"provider"`
	Model             string    `json:"model"`
	CreatedAt         time.Time `json:"createdAt"`

	Target           string        `json:"target"`
	CauseStatus      string        `json:"causeStatus"`
	Causes           []CutCause    `json:"causes"`
	Origin           *CutEvidence  `json:"origin,omitempty"`
	OriginConfidence string        `json:"originConfidence,omitempty"`
	EventStart       *CutEvidence  `json:"eventStart,omitempty"`
	Required         []CutEvidence `json:"required"`
	Optional         []CutEvidence `json:"optional"`

	Boundaries  []CutBoundary   `json:"boundaries"`
	Chosen      string          `json:"chosen"`
	StartReason string          `json:"startReason"`
	EndReason   string          `json:"endReason"`
	Adjustments []CutAdjustment `json:"adjustments"`
	Ending      CutEnding       `json:"ending"`

	Verification CutVerification `json:"verification"`
	Unresolved   []string        `json:"unresolved"`
	SearchRounds int             `json:"searchRounds"`
	SearchAdded  int             `json:"searchAdded"`
	Notes        []CutNote       `json:"notes"`
	NotesApplied []CutNote       `json:"notesApplied"`
}

func evidenceOf(p cut.Plan, ref string) CutEvidence {
	e := CutEvidence{Ref: ref, Kind: refKind(ref)}
	if info, ok := p.Refs[ref]; ok {
		at := info.AtMs
		e.AtMs, e.Kind, e.Text = &at, info.Kind, info.Text
	}
	return e
}

func refKind(ref string) string {
	switch {
	case strings.HasPrefix(ref, "utt:"):
		return cut.KindUtterance
	case strings.HasPrefix(ref, "comment:"):
		return cut.KindComment
	}
	return cut.KindChat
}

func evidenceList(p cut.Plan, refs []string) []CutEvidence {
	out := make([]CutEvidence, 0, len(refs))
	for _, r := range refs {
		out = append(out, evidenceOf(p, r))
	}
	return out
}

func timelineSpans(spans []cut.Span) []TimelineSpan {
	out := make([]TimelineSpan, 0, len(spans))
	for _, s := range spans {
		out = append(out, TimelineSpan{StartMs: s.StartMs, EndMs: s.EndMs, Role: s.Role})
	}
	return out
}

func notesOf(notes []cut.UserNote) []CutNote {
	out := make([]CutNote, 0, len(notes))
	for _, n := range notes {
		out = append(out, CutNote{At: n.At, Text: n.Text, AppliesTo: n.AppliesTo})
	}
	return out
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// sourceFindings names the against-source checks that found a problem.
func sourceFindings(v *cut.SourceVerification) []string {
	out := []string{}
	if v == nil {
		return out
	}
	for _, f := range []struct {
		on   bool
		text string
	}{{v.MixedAttempt, "다른 시도와 섞임"}, {v.CutCondition, "조건·약속이 잘림"}, {v.MissedCorrection, "바로 뒤 정정을 놓침"}, {v.EndedEarly, "결말 전에 끊음"}} {
		if f.on {
			out = append(out, f.text)
		}
	}
	return out
}

// cutDetail is the result box's view of a stored plan.
func cutDetail(rec store.CutPlan, p cut.Plan) CutDetail {
	d := CutDetail{PlanID: rec.ID, SessionID: rec.SessionID, CandidateIDs: orEmpty(rec.CandidateIDs), Version: rec.Version, State: rec.State,
		Confirmed: p.ConfirmedByUser, ConfirmedBoundary: p.ConfirmedBoundary, ConfirmedAt: p.ConfirmedAt, Sources: orEmpty(p.Sources),
		Provider: p.AnalysisVersion.Provider, Model: p.AnalysisVersion.Model, CreatedAt: rec.CreatedAt,
		Target: p.Reaction.Target, CauseStatus: p.CauseStatus, Causes: []CutCause{}, OriginConfidence: p.Times.OriginConfidence,
		Required: evidenceList(p, p.RequiredContextRefs), Optional: evidenceList(p, p.OptionalContextRefs),
		Boundaries: []CutBoundary{}, Chosen: p.Boundaries.Chosen, StartReason: p.Boundaries.StartReason, EndReason: p.Boundaries.EndReason,
		Adjustments: []CutAdjustment{}, Ending: CutEnding{State: p.Ending.State, EventKind: p.Ending.EventKind, Checks: orEmpty(p.Ending.Checks)},
		Unresolved: orEmpty(p.UnresolvedQuestions), SearchRounds: p.SearchBudget.Rounds, Notes: notesOf(p.UserNotes), NotesApplied: notesOf(p.NotesApplied)}
	for _, c := range p.Causes {
		d.Causes = append(d.Causes, CutCause{Kind: c.Kind, Summary: c.Summary, Direction: c.Direction,
			Support: evidenceList(p, c.Support), Against: evidenceList(p, c.Against)})
	}
	if p.Times.StoryOriginRef != "" {
		o := evidenceOf(p, p.Times.StoryOriginRef)
		if o.AtMs == nil {
			at := p.Times.StoryOriginMs
			o.AtMs = &at
		}
		d.Origin = &o
	}
	if p.Times.EventStartRef != "" {
		e := evidenceOf(p, p.Times.EventStartRef)
		if e.AtMs == nil {
			at := p.Times.EventStartMs
			e.AtMs = &at
		}
		d.EventStart = &e
	}
	if len(p.Boundaries.Resolved) > 0 {
		for _, b := range p.Boundaries.Resolved {
			d.Boundaries = append(d.Boundaries, CutBoundary{Name: b.Name, Spans: timelineSpans(b.Spans), Issues: b.Issues})
		}
	} else if len(p.SelectedSpans) > 0 {
		// A plan from before every boundary was resolved: only the chosen one has times.
		d.Boundaries = append(d.Boundaries, CutBoundary{Name: p.Boundaries.Chosen, Spans: timelineSpans(p.SelectedSpans)})
	}
	for _, a := range p.Boundaries.Adjustments {
		d.Adjustments = append(d.Adjustments, CutAdjustment{Ref: a.Ref, FromMs: a.FromMs, ToMs: a.ToMs, Why: a.Why})
	}
	v := p.Verification
	d.Verification = CutVerification{Gaps: []string{}, Asides: []string{}, Findings: sourceFindings(v.AgainstSource), Notes: []string{}, Issues: orEmpty(v.Issues),
		EvidenceMatch: v.EvidenceMatch}
	if v.ClipOnly != nil {
		pass := v.ClipOnly.Pass
		d.Verification.ClipPass, d.Verification.Answers, d.Verification.Gaps = &pass, v.ClipOnly.Answers, orEmpty(v.ClipOnly.Gaps)
		d.Verification.Asides = orEmpty(v.ClipOnly.Asides)
	}
	if v.AgainstSource != nil {
		pass := v.AgainstSource.Pass
		d.Verification.SourcePass, d.Verification.Notes = &pass, orEmpty(v.AgainstSource.Notes)
	}
	for _, r := range p.Searches {
		d.SearchAdded += len(r.FoundRefs)
	}
	return d
}

// CutPlanDetail returns one plan as the result box shows it.
func (a *App) CutPlanDetail(ctx context.Context, id string) (CutDetail, error) {
	rec, err := a.cfg.Store.CutPlan(ctx, id)
	if err != nil {
		return CutDetail{}, err
	}
	p, ok := decodePlan(rec)
	if !ok {
		return CutDetail{}, domain.NewUserError("INPUT", "이 컷 분석 결과를 읽지 못했습니다.")
	}
	return cutDetail(rec, p), nil
}

// CutPlanDetails lists a session's plans in time order: the current ones
// (the confirmed version and a newer one, CurrentCutPlans) or every version.
func (a *App) CutPlanDetails(ctx context.Context, sid string, all bool) ([]CutDetail, error) {
	if _, err := a.cfg.Store.Session(ctx, sid); err != nil {
		return nil, err
	}
	var recs []store.CutPlan
	var err error
	if all {
		recs, err = a.cfg.Store.CutPlans(ctx, sid, true)
	} else {
		recs, err = a.cfg.Store.CurrentCutPlans(ctx, sid)
	}
	if err != nil {
		return nil, err
	}
	out := []CutDetail{}
	kept := keptVersions(recs)
	for _, rec := range recs {
		if p, ok := decodePlan(rec); ok {
			d := cutDetail(rec, p)
			d.KeptVersion = kept.of(rec)
			out = append(out, d)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		x, y := spanStart(out[i]), spanStart(out[j])
		if x != y {
			return x < y
		}
		return out[i].Version < out[j].Version
	})
	return out, nil
}

// kept maps a candidate set to its confirmed version.
type kept map[string]int

func keptVersions(recs []store.CutPlan) kept {
	k := kept{}
	for _, rec := range recs {
		if rec.Confirmed {
			k[strings.Join(rec.CandidateIDs, "\x00")] = rec.Version
		}
	}
	return k
}

// of is the confirmed version that stays when rec is a later, unconfirmed one (else 0).
func (k kept) of(rec store.CutPlan) int {
	if v := k[strings.Join(rec.CandidateIDs, "\x00")]; !rec.Confirmed && rec.Version > v {
		return v
	}
	return 0
}

// spanStart is where a plan's spans begin (the effective boundary), or -1.
func spanStart(d CutDetail) int64 {
	name := d.ConfirmedBoundary
	if name == "" {
		name = d.Chosen
	}
	for _, b := range d.Boundaries {
		if b.Name == name && len(b.Spans) > 0 {
			return b.Spans[0].StartMs
		}
	}
	return -1
}
