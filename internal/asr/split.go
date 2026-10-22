package asr

import (
	"strings"
	"unicode/utf8"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Word is one recognised word with its own audio time (Apple's
// SpeechTranscriber gives each word an audioTimeRange).
type Word struct {
	StartMs int64
	EndMs   int64
	Text    string // as the engine wrote it, with its leading space
}

// SplitParams says where one recognition result is cut into utterances.
// A cut analysis can only start or end a clip at an utterance's edge, so an
// utterance's length is how precise a clip boundary can be
// (docs/AI-CUT-ANALYSIS.md 2.2).
type SplitParams struct {
	// PauseMs: a silence at least this long between two words starts a new utterance.
	PauseMs int64
	// MaxMs: a longer utterance is cut again at its longest pause (both
	// parts at least MinMs when it can).
	MaxMs int64
	MinMs int64
	// Apple puts the silence before a word inside that word's range, so a
	// word longer than its speech is taken to be silence first: its speech
	// is its last max(WordMs, RuneMs per character).
	WordMs int64
	RuneMs int64
}

// DefaultSplit is the split the engines use unless Options says otherwise.
func DefaultSplit() SplitParams {
	return SplitParams{PauseMs: 600, MaxMs: 8000, MinMs: 1500, WordMs: 800, RuneMs: 200}
}

// orDefault fills unset values from DefaultSplit.
func (p SplitParams) orDefault() SplitParams {
	d := DefaultSplit()
	if p.PauseMs <= 0 {
		p.PauseMs = d.PauseMs
	}
	if p.MaxMs <= 0 {
		p.MaxMs = d.MaxMs
	}
	if p.MinMs <= 0 {
		p.MinMs = d.MinMs
	}
	if p.WordMs <= 0 {
		p.WordMs = d.WordMs
	}
	if p.RuneMs <= 0 {
		p.RuneMs = d.RuneMs
	}
	return p
}

// spokenStart is when the word's speech begins: its range start, or later
// when the range is longer than the word could take to say.
func (p SplitParams) spokenStart(w Word) int64 {
	speech := max(p.WordMs, p.RuneMs*int64(utf8.RuneCountInString(strings.TrimSpace(w.Text))))
	return max(w.StartMs, w.EndMs-speech)
}

// sentenceEnd says whether the word ends a sentence.
func sentenceEnd(w Word) bool {
	t := strings.TrimSpace(w.Text)
	return strings.HasSuffix(t, ".") || strings.HasSuffix(t, "?") || strings.HasSuffix(t, "!")
}

// Split cuts one recognition result's words into utterances: after a
// sentence end, at a pause of PauseMs or more, and then any part longer
// than MaxMs at its longest pause. Each utterance starts at its first
// word's speech and ends at its last word's end. Speaker is left empty.
func Split(words []Word, p SplitParams) []domain.Utterance {
	p = p.orDefault()
	var ws []Word
	for _, w := range words {
		if strings.TrimSpace(w.Text) != "" && w.EndMs >= w.StartMs {
			ws = append(ws, w)
		}
	}
	if len(ws) == 0 {
		return nil
	}
	// pause[i] is the silence before word i (from the previous word's end to i's speech).
	pause := make([]int64, len(ws))
	for i := 1; i < len(ws); i++ {
		pause[i] = max(0, p.spokenStart(ws[i])-ws[i-1].EndMs)
	}
	var out []domain.Utterance
	from := 0
	for i := 1; i <= len(ws); i++ {
		if i < len(ws) && pause[i] < p.PauseMs && !sentenceEnd(ws[i-1]) {
			continue
		}
		for _, part := range p.limit(ws, pause, from, i) {
			out = append(out, p.utterance(ws[part[0]:part[1]]))
		}
		from = i
	}
	return out
}

// limit splits words [a, b) until no part is longer than MaxMs (or a part
// is one word), each time at the longest pause, nearest the middle on a
// tie (pauses compared in 100 ms steps), keeping both parts MinMs or longer
// when some cut can.
func (p SplitParams) limit(ws []Word, pause []int64, a, b int) [][2]int {
	start := func(i int) int64 { return p.spokenStart(ws[i]) }
	if b-a < 2 || ws[b-1].EndMs-start(a) <= p.MaxMs {
		return [][2]int{{a, b}}
	}
	mid := (start(a) + ws[b-1].EndMs) / 2
	best, bestFits := -1, false
	var bestPause, bestDist int64
	for i := a + 1; i < b; i++ {
		fits := ws[i-1].EndMs-start(a) >= p.MinMs && ws[b-1].EndMs-start(i) >= p.MinMs
		pz := pause[i] / 100
		dist := max(start(i)-mid, mid-start(i))
		better := best < 0 || (fits && !bestFits) ||
			(fits == bestFits && (pz > bestPause || (pz == bestPause && dist < bestDist)))
		if better {
			best, bestFits, bestPause, bestDist = i, fits, pz, dist
		}
	}
	return append(p.limit(ws, pause, a, best), p.limit(ws, pause, best, b)...)
}

func (p SplitParams) utterance(ws []Word) domain.Utterance {
	var b strings.Builder
	for _, w := range ws {
		b.WriteString(w.Text)
	}
	return domain.Utterance{StartMs: p.spokenStart(ws[0]), EndMs: ws[len(ws)-1].EndMs, Text: strings.TrimSpace(b.String())}
}
