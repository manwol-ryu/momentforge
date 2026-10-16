// Package rules holds the detection rules. Each rule is registered in
// cmd/momentforge through All(); the engine in internal/detect runs them.
package rules

import (
	"regexp"
	"strings"
)

// Reaction vocabulary from CzzEdit 0.3.2. Text arrives normalized
// (lowercase, runs of 3+ characters shortened to 2).
var (
	laughPattern   = regexp.MustCompile(`ㅋ{2,}|ㅎ{2,}|[😂🤣]`)
	surpriseJamo   = regexp.MustCompile(`ㄷ{2,}`)
	surpriseWords  = []string{"와", "헐", "대박", "미쳤다"}
	sadnessPattern = regexp.MustCompile(`아깝다|아쉽다|ㅠ{2,}|ㅜ{2,}`)

	// Messages made only of these are reactions, not phrases to count as repeats.
	pureReaction = regexp.MustCompile(`^(?:[ㅋㅎ😂🤣]+|ㄷ+|ㅠ+|ㅜ+)$`)
	pureWords    = map[string]bool{"와": true, "헐": true, "대박": true, "미쳤다": true, "아깝다": true, "아쉽다": true}
)

// hasStandaloneWord reports whether word appears without a Hangul syllable
// directly before or after it (Python: (?<![가-힣])word(?![가-힣])).
func hasStandaloneWord(text, word string) bool {
	for from := 0; ; {
		i := strings.Index(text[from:], word)
		if i < 0 {
			return false
		}
		at := from + i
		before, after := lastRune(text[:at]), firstRune(text[at+len(word):])
		if !isSyllable(before) && !isSyllable(after) {
			return true
		}
		from = at + len(firstString(word))
	}
}

func isSyllable(r rune) bool { return r >= '가' && r <= '힣' }

func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return 0
	}
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// firstString returns the first character of s as a string.
func firstString(s string) string {
	for _, r := range s {
		return string(r)
	}
	return s
}

func isPureReaction(text string) bool {
	plain := strings.Trim(text, " !?.,~")
	return pureReaction.MatchString(plain) || pureWords[plain]
}
