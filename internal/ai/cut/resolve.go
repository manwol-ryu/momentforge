package cut

import (
	"fmt"
	"sort"
)

// How evidence IDs become times (docs/AI-CUT-ANALYSIS.md 4.4). An utterance
// has its own start and end, padded a little so the first and last syllable
// are not clipped; a chat message comes a few seconds after what it reacts
// to, so a span that starts at a message starts earlier, and when that lands
// inside an utterance the span moves to the utterance's start (no cut in the
// middle of a sentence).
const (
	UtteranceLeadMs = 300
	UtteranceTailMs = 500
	ChatLeadMs      = 6000
	ChatTailMs      = 3000
	SnapMaxMs       = 10_000 // the farthest a chat boundary moves to reach an utterance edge
	MergeGapMs      = 20_000
)

// SeekOf is where to start watching a piece of evidence: an utterance's own
// start, or a few seconds before a chat or comment (what it reacts to).
func SeekOf(e Evidence) int64 {
	if e.Kind == KindUtterance {
		return e.StartMs
	}
	return max(0, e.StartMs-ChatLeadMs)
}

// StartOf is the time a span starting at this evidence begins, before
// snapping to an utterance.
func StartOf(e Evidence) int64 {
	if e.Kind == KindUtterance {
		return max(0, e.StartMs-UtteranceLeadMs)
	}
	return max(0, e.StartMs-ChatLeadMs)
}

// EndOf is the time a span ending at this evidence ends, before snapping.
func EndOf(e Evidence) int64 {
	if e.Kind == KindUtterance {
		return utteranceEnd(e) + UtteranceTailMs
	}
	return e.StartMs + ChatTailMs
}

func utteranceEnd(e Evidence) int64 {
	return max(e.EndMs, e.StartMs)
}

// Within says whether the evidence's own time lies inside one of the spans.
func Within(e Evidence, spans []Span) bool {
	for _, s := range spans {
		if e.StartMs >= s.StartMs && e.StartMs <= s.EndMs {
			return true
		}
	}
	return false
}

// utterances are the known utterances in time order, for snapping.
func utterances(known map[string]Evidence) []Evidence {
	var out []Evidence
	for _, e := range known {
		if e.Kind == KindUtterance {
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

// inside is the utterance that is being spoken at ms, if any.
func inside(utts []Evidence, ms int64) (Evidence, bool) {
	for _, u := range utts {
		if u.StartMs < ms && ms < utteranceEnd(u) {
			return u, true
		}
	}
	return Evidence{}, false
}

// spanStart resolves where a span starting at e begins and why it moved.
func spanStart(e Evidence, utts []Evidence) (int64, []Adjustment) {
	at := StartOf(e)
	if e.Kind == KindUtterance {
		if at == e.StartMs {
			return at, nil
		}
		return at, []Adjustment{{Ref: e.Ref, FromMs: e.StartMs, ToMs: at, Why: "발언 시작 0.3초 전부터"}}
	}
	adj := []Adjustment{{Ref: e.Ref, FromMs: e.StartMs, ToMs: at, Why: "채팅은 장면보다 늦으므로 6초 앞으로 당김"}}
	if u, ok := inside(utts, at); ok && at-u.StartMs <= SnapMaxMs {
		to := max(0, u.StartMs-UtteranceLeadMs)
		adj = append(adj, Adjustment{Ref: u.Ref, FromMs: at, ToMs: to, Why: "발언 중간에서 시작하지 않도록 그 발언 시작으로 당김"})
		at = to
	}
	return at, adj
}

// spanEnd resolves where a span ending at e ends and why it moved.
func spanEnd(e Evidence, utts []Evidence) (int64, []Adjustment) {
	at := EndOf(e)
	if e.Kind == KindUtterance {
		return at, []Adjustment{{Ref: e.Ref, FromMs: utteranceEnd(e), ToMs: at, Why: "발언 끝 0.5초 뒤까지"}}
	}
	adj := []Adjustment{{Ref: e.Ref, FromMs: e.StartMs, ToMs: at, Why: "채팅 뒤 3초까지"}}
	if u, ok := inside(utts, at); ok && utteranceEnd(u)-at <= SnapMaxMs {
		to := utteranceEnd(u) + UtteranceTailMs
		adj = append(adj, Adjustment{Ref: u.Ref, FromMs: at, ToMs: to, Why: "발언 중간에서 끊지 않도록 그 발언 끝까지 늘림"})
		at = to
	}
	return at, adj
}

// ResolveSpans turns the model's spans into times, in order, merged when
// they touch, clamped to the video. Every move away from the evidence's own
// time is recorded as an adjustment. Unknown refs and reversed spans are
// errors the caller reports as issues.
func ResolveSpans(refs []SpanRefs, known map[string]Evidence, durationMs int64) ([]Span, []Adjustment, []string) {
	var spans []Span
	var adj []Adjustment
	var issues []string
	utts := utterances(known)
	for _, r := range refs {
		a, okA := known[r.StartRef]
		b, okB := known[r.EndRef]
		switch {
		case !okA:
			issues = append(issues, fmt.Sprintf("start_ref %s는 이 분석에서 읽은 근거가 아닙니다", r.StartRef))
			continue
		case !okB:
			issues = append(issues, fmt.Sprintf("end_ref %s는 이 분석에서 읽은 근거가 아닙니다", r.EndRef))
			continue
		case a.StartMs > b.StartMs:
			issues = append(issues, fmt.Sprintf("span %s~%s의 시작 근거가 끝 근거보다 뒤에 있습니다", r.StartRef, r.EndRef))
			continue
		}
		start, sa := spanStart(a, utts)
		end, ea := spanEnd(b, utts)
		adj = append(append(adj, sa...), ea...)
		s := Span{StartRef: r.StartRef, EndRef: r.EndRef, Role: r.Role, StartMs: start, EndMs: end}
		if durationMs > 0 && s.EndMs > durationMs {
			adj = append(adj, Adjustment{Ref: r.EndRef, FromMs: s.EndMs, ToMs: durationMs, Why: "영상 끝에 맞춤"})
			s.EndMs = durationMs
		}
		if s.EndMs <= s.StartMs {
			issues = append(issues, fmt.Sprintf("span %s~%s의 길이가 0입니다", r.StartRef, r.EndRef))
			continue
		}
		spans = append(spans, s)
	}
	spans, merged := mergeSpans(spans)
	return spans, append(adj, merged...), issues
}

// mergeSpans orders spans by start and joins ones that overlap or sit less
// than MergeGapMs apart (a cut that short reads as a glitch).
func mergeSpans(spans []Span) ([]Span, []Adjustment) {
	if len(spans) < 2 {
		return spans, nil
	}
	sort.SliceStable(spans, func(i, j int) bool { return spans[i].StartMs < spans[j].StartMs })
	var adj []Adjustment
	out := []Span{spans[0]}
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.StartMs <= last.EndMs+MergeGapMs {
			if s.StartMs > last.EndMs {
				adj = append(adj, Adjustment{Ref: s.StartRef, FromMs: s.StartMs, ToMs: last.EndMs, Why: "앞 구간과 20초 미만 떨어져 하나로 합침"})
			}
			if s.EndMs > last.EndMs {
				last.EndMs, last.EndRef = s.EndMs, s.EndRef
			}
			if last.Role != s.Role {
				last.Role = "main"
			}
			continue
		}
		out = append(out, s)
	}
	return out, adj
}

// TotalMs is the length of all spans together.
func TotalMs(spans []Span) int64 {
	var t int64
	for _, s := range spans {
		t += s.EndMs - s.StartMs
	}
	return t
}

// ResolvedBoundary is one boundary candidate with its times, so the screen
// can switch between short, context and the whole event without the model.
type ResolvedBoundary struct {
	Name   string   `json:"name"`
	Spans  []Span   `json:"spans"`
	Issues []string `json:"issues,omitempty"` // refs that could not be resolved
}

// ResolveBoundaries resolves every boundary candidate the same way as the
// chosen one. A candidate without a usable span is kept with its issues.
func ResolveBoundaries(cands []BoundaryCandidate, known map[string]Evidence, durationMs int64) []ResolvedBoundary {
	out := make([]ResolvedBoundary, 0, len(cands))
	for _, c := range cands {
		spans, _, issues := ResolveSpans(c.Spans, known, durationMs)
		if spans == nil {
			spans = []Span{}
		}
		out = append(out, ResolvedBoundary{Name: c.Name, Spans: spans, Issues: issues})
	}
	return out
}

// RefInfo is what a plan keeps about each piece of evidence it cites, so
// the result box can show and play it without the evidence list.
type RefInfo struct {
	Kind    string `json:"kind"`
	AtMs    int64  `json:"at_ms"` // where to start watching (SeekOf)
	StartMs int64  `json:"start_ms"`
	EndMs   int64  `json:"end_ms,omitempty"`
	Text    string `json:"text"` // the first characters only
}

const refTextRunes = 120

// IndexRefs records the cited evidence of a plan: causes, story origin,
// event start, context and every boundary's ends.
func IndexRefs(p *Plan, known map[string]Evidence) {
	idx := map[string]RefInfo{}
	add := func(refs ...string) {
		for _, r := range refs {
			e, ok := known[r]
			if !ok || r == "" {
				continue
			}
			text := []rune(e.Text)
			if len(text) > refTextRunes {
				text = append(text[:refTextRunes], '…')
			}
			idx[r] = RefInfo{Kind: e.Kind, AtMs: SeekOf(e), StartMs: e.StartMs, EndMs: e.EndMs, Text: string(text)}
		}
	}
	for _, c := range p.Causes {
		add(c.Support...)
		add(c.Against...)
	}
	add(p.Times.StoryOriginRef, p.Times.EventStartRef)
	add(p.RequiredContextRefs...)
	add(p.OptionalContextRefs...)
	for _, c := range p.Boundaries.Candidates {
		for _, s := range c.Spans {
			add(s.StartRef, s.EndRef)
		}
	}
	p.Refs = idx
}
