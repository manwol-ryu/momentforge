package detect

import (
	"strings"
	"unicode/utf8"
)

// Normalize prepares chat text for matching, like CzzEdit's normalized():
// compose Hangul (NFC for Korean), lowercase, collapse whitespace, and
// shorten any run of 3+ identical characters to 2 ("ㅋㅋㅋㅋ" → "ㅋㅋ").
//
// Only Hangul composition is applied, not full Unicode NFC, to avoid an
// extra dependency; other scripts do not affect the detection rules.
func Normalize(s string) string {
	s = composeHangul(s)
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	return collapseRuns(s)
}

const (
	hangulBase = 0xAC00
	hangulLast = 0xD7A3
	jamoLBase  = 0x1100
	jamoVBase  = 0x1161
	jamoTBase  = 0x11A7
	jamoLCount = 19
	jamoVCount = 21
	jamoTCount = 28
	jamoNCount = jamoVCount * jamoTCount
	jamoTFirst = jamoTBase + 1
	jamoTFinal = jamoTBase + jamoTCount - 1
	jamoVFinal = jamoVBase + jamoVCount - 1
	jamoLFinal = jamoLBase + jamoLCount - 1
)

// composeHangul joins conjoining jamo (L V [T]) and LV syllables + T into
// precomposed syllables, as Unicode canonical composition does.
func composeHangul(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r >= jamoLBase && r <= jamoTFinal }) {
		return s
	}
	runes := []rune(s)
	out := make([]rune, 0, len(runes))
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if r >= jamoLBase && r <= jamoLFinal && i+1 < len(runes) && runes[i+1] >= jamoVBase && runes[i+1] <= jamoVFinal {
			r = hangulBase + (r-jamoLBase)*jamoNCount + (runes[i+1]-jamoVBase)*jamoTCount
			i++
		}
		if r >= hangulBase && r <= hangulLast && (r-hangulBase)%jamoTCount == 0 &&
			i+1 < len(runes) && runes[i+1] >= jamoTFirst && runes[i+1] <= jamoTFinal {
			r += runes[i+1] - jamoTBase
			i++
		}
		out = append(out, r)
	}
	return string(out)
}

func collapseRuns(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	var prev rune = utf8.RuneError
	run := 0
	for _, r := range s {
		if r == prev {
			run++
		} else {
			prev, run = r, 1
		}
		if run <= 2 {
			b.WriteRune(r)
		}
	}
	return b.String()
}
