package app

import (
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// TimeRef is a video time (or range) written in a viewer comment.
type TimeRef struct {
	StartMs     int64
	EndMs       int64  // 0 when the comment gives a single time
	Description string // the text after the time, up to the next time
	Offset      int    // position in the comment, in characters
}

// TimeIssue is a time-like text that was not used.
type TimeIssue struct {
	Offset int
	Reason string // invalid_time | invalid_range | needs_review (clock or relative time)
}

var (
	timeHMS       = regexp.MustCompile(`^\d+:\d{2}:\d{2}`)
	timeMS        = regexp.MustCompile(`^\d+:\d{2}`)
	timeKorean    = regexp.MustCompile(`^(?:\d+시간\s*)?\d+분\s*\d+초`)
	koreanParts   = regexp.MustCompile(`^(?:(\d+)시간\s*)?(\d+)분\s*(\d+)초$`)
	rangeBetween  = regexp.MustCompile(`^\s*(?:~|〜|～|-|–|—)\s*$`)
	relativeAfter = regexp.MustCompile(`^\s*(후|뒤|전|남음)(?:\s|$)`)
	clockBefore   = regexp.MustCompile(`(오전|오후|시계|점수|비율|내일|오늘|날짜|뒤|후|전)\s*$`)
)

type timeMatch struct {
	start, end int // rune offsets
	text       string
}

// findTimes finds "1:23", "1:02:03" and "1시간 2분 3초" not glued to other
// digits, colons or a leading minus, as CzzEdit's regular expression did.
func findTimes(r []rune) []timeMatch {
	var out []timeMatch
	for i := 0; i < len(r); i++ {
		if r[i] < '0' || r[i] > '9' {
			continue
		}
		if i > 0 && (isDigit(r[i-1]) || r[i-1] == ':' || r[i-1] == '-') {
			continue
		}
		window := string(r[i:min(len(r), i+64)])
		for _, re := range []*regexp.Regexp{timeHMS, timeMS, timeKorean} {
			loc := re.FindStringIndex(window)
			if loc == nil {
				continue
			}
			end := i + utf8.RuneCountInString(window[:loc[1]])
			if end < len(r) && (isDigit(r[end]) || r[end] == ':') {
				continue
			}
			out = append(out, timeMatch{i, end, window[:loc[1]]})
			i = end - 1
			break
		}
	}
	return out
}

func isDigit(c rune) bool { return c >= '0' && c <= '9' }

// timeMs converts a found time; ok is false for minutes or seconds ≥ 60.
func timeMs(text string) (int64, bool) {
	if strings.Contains(text, ":") {
		var p []int64
		for _, s := range strings.Split(text, ":") {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return 0, false
			}
			p = append(p, n)
		}
		if p[len(p)-1] >= 60 || (len(p) == 3 && p[1] >= 60) {
			return 0, false
		}
		if len(p) == 2 {
			return (p[0]*60 + p[1]) * 1000, true
		}
		return (p[0]*3600 + p[1]*60 + p[2]) * 1000, true
	}
	m := koreanParts.FindStringSubmatch(text)
	if m == nil {
		return 0, false
	}
	var v [3]int64
	for i, s := range m[1:] {
		if s != "" {
			v[i], _ = strconv.ParseInt(s, 10, 64)
		}
	}
	h, mm, s := v[0], v[1], v[2]
	if s >= 60 || (h != 0 && mm >= 60) {
		return 0, false
	}
	return (h*3600 + mm*60 + s) * 1000, true
}

// ParseTimestamps reads the video times in a comment (CzzEdit comments.parse).
// Times outside the video, broken ranges and times that look like a clock or
// a relative time ("오후 3:00", "5:00 후") become issues instead.
func ParseTimestamps(text string, durationMs int64) ([]TimeRef, []TimeIssue) {
	r := []rune(text)
	ms := findTimes(r)
	var found []TimeRef
	var issues []TimeIssue
	type span struct{ s, e int64 }
	seen := map[span]bool{}
	for i := 0; i < len(ms); {
		m := ms[i]
		start, startOK := timeMs(m.text)
		var end int64
		after, next := m.end, i+1
		if next < len(ms) && rangeBetween.MatchString(string(r[after:ms[next].start])) {
			var endOK bool
			end, endOK = timeMs(ms[next].text)
			after, next = ms[next].end, next+1
			if !endOK || !startOK || end <= start || end > durationMs {
				issues = append(issues, TimeIssue{m.start, "invalid_range"})
				i = next
				continue
			}
		}
		prefix := string(r[max(0, m.start-16):m.start])
		ambiguous := relativeAfter.MatchString(string(r[after:])) || clockBefore.MatchString(prefix)
		switch {
		case !startOK || start < 0 || start >= durationMs || ambiguous:
			reason := "invalid_time"
			if ambiguous {
				reason = "needs_review"
			}
			issues = append(issues, TimeIssue{m.start, reason})
		case !seen[span{start, end}]:
			stop := len(r)
			if next < len(ms) {
				stop = ms[next].start
			}
			found = append(found, TimeRef{StartMs: start, EndMs: end, Offset: m.start,
				Description: strings.Trim(string(r[after:stop]), " \n\r/|-~")})
			seen[span{start, end}] = true
		}
		i = next
	}
	return found, issues
}
