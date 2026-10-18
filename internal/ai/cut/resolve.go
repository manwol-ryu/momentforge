package cut

import "fmt"

// How evidence IDs become times (docs/AI-CUT-ANALYSIS.md 4.4). An utterance
// has its own start and end; a chat message comes a few seconds after what
// it reacts to, so a span that starts at a message starts a little earlier.
const (
	ChatLeadMs      = 6000
	ChatTailMs      = 3000
	UtteranceTailMs = 500
	MergeGapMs      = 20000
)

// StartOf is the time a span starting at this evidence begins.
func StartOf(e Evidence) int64 {
	if e.Kind == KindUtterance {
		return e.StartMs
	}
	return max(0, e.StartMs-ChatLeadMs)
}

// EndOf is the time a span ending at this evidence ends.
func EndOf(e Evidence) int64 {
	if e.Kind == KindUtterance {
		end := e.EndMs
		if end < e.StartMs {
			end = e.StartMs
		}
		return end + UtteranceTailMs
	}
	return e.StartMs + ChatTailMs
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

// ResolveSpans turns the model's spans into times, in order, merged when
// they touch, clamped to the video. Unknown refs and reversed spans are
// errors the caller reports as issues.
func ResolveSpans(refs []SpanRefs, known map[string]Evidence, durationMs int64) ([]Span, []Adjustment, []string) {
	var spans []Span
	var adj []Adjustment
	var issues []string
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
		s := Span{StartRef: r.StartRef, EndRef: r.EndRef, Role: r.Role, StartMs: StartOf(a), EndMs: EndOf(b)}
		if s.StartMs != a.StartMs {
			adj = append(adj, Adjustment{Ref: r.StartRef, FromMs: a.StartMs, ToMs: s.StartMs, Why: "채팅은 장면보다 늦으므로 앞으로 당김"})
		}
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
	spans = mergeSpans(spans)
	return spans, adj, issues
}

// mergeSpans orders spans by start and joins ones that overlap or sit less
// than MergeGapMs apart (a cut that short reads as a glitch).
func mergeSpans(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	for i := 1; i < len(spans); i++ {
		for j := i; j > 0 && spans[j].StartMs < spans[j-1].StartMs; j-- {
			spans[j], spans[j-1] = spans[j-1], spans[j]
		}
	}
	out := []Span{spans[0]}
	for _, s := range spans[1:] {
		last := &out[len(out)-1]
		if s.StartMs <= last.EndMs+MergeGapMs {
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
	return out
}

// TotalMs is the length of all spans together.
func TotalMs(spans []Span) int64 {
	var t int64
	for _, s := range spans {
		t += s.EndMs - s.StartMs
	}
	return t
}
