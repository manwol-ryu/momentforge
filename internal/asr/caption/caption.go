// Package caption reads caption files the user adds (SubRip .srt and
// WebVTT .vtt) into utterances, so a session can have a transcript without
// speech recognition (docs/AI-CUT-ANALYSIS.md 8절 6-B ⑤). It is not an
// asr.Engine: nothing is recognised, the file is only converted. Speaker
// names in the file (WebVTT voice tags) become roles; no name is kept.
package caption

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/manwol-ryu/momentforge/internal/asr"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// ErrFormat says the input is neither SRT nor WebVTT.
var ErrFormat = errors.New("caption: not an SRT or WebVTT file")

// MaxBytes is the largest file read (a day-long broadcast's captions are a few MB).
const MaxBytes = 64 << 20

// Options of one conversion.
type Options struct {
	// OffsetMs is added to every time (negative when the captions start
	// later than the broadcast). Lines that end before 0 are dropped.
	OffsetMs int64
}

// Format is what Parse recognised.
type Format string

const (
	SRT Format = "srt"
	VTT Format = "vtt"
)

var (
	// 00:01:02,345 or 00:01:02.345 or 01:02.345 (VTT allows no hours).
	timeRe  = regexp.MustCompile(`^(?:(\d{1,2}):)?(\d{1,2}):(\d{2})[.,](\d{1,3})$`)
	arrowRe = regexp.MustCompile(`^\s*(\S+)\s+-->\s+(\S+)`)
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	voiceRe = regexp.MustCompile(`<v(?:\.[^\s>]*)?\s+([^>]*)>`)
)

// Parse reads a caption file and returns utterances in time order. The
// format is detected from the content (a WEBVTT header or SRT cue blocks),
// not the file name.
func Parse(r io.Reader, opts Options) ([]domain.Utterance, Format, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("read caption: %w", err)
	}
	if len(data) > MaxBytes {
		return nil, "", domain.NewUserError("INPUT", "자막 파일이 너무 큽니다 (64MB까지).")
	}
	text := strings.TrimPrefix(string(data), "\xEF\xBB\xBF")
	if !utf8.ValidString(text) {
		return nil, "", domain.NewUserError("INPUT", "자막 파일을 UTF-8로 읽지 못했습니다. UTF-8로 저장한 뒤 다시 넣어 주세요.")
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	format := SRT
	if strings.HasPrefix(strings.TrimLeft(text, "\n\t "), "WEBVTT") {
		format = VTT
	}
	cues, err := blocks(text, format)
	if err != nil {
		return nil, "", err
	}
	if len(cues) == 0 {
		return nil, "", ErrFormat
	}
	roles := map[string]string{}
	var out []domain.Utterance
	for _, c := range cues {
		start, end := c.start+opts.OffsetMs, c.end+opts.OffsetMs
		if end <= 0 {
			continue
		}
		start = max(start, 0)
		speaker, body := speakerOf(c.text, roles)
		body = clean(body)
		if body == "" {
			continue
		}
		out = append(out, domain.Utterance{StartMs: start, EndMs: end, Speaker: speaker, Text: body})
	}
	if len(out) == 0 {
		return nil, format, domain.NewUserError("INPUT", "자막 파일에 읽을 수 있는 줄이 없습니다. 시간 오프셋이 너무 큰지 확인하세요.")
	}
	return asr.Sorted(out), format, nil
}

type cue struct {
	start, end int64
	text       string
}

// blocks splits the file into cues: a timing line "a --> b" followed by
// text lines until a blank line. SRT numbers and VTT identifiers before the
// timing line are ignored, as are VTT NOTE/STYLE/REGION blocks.
func blocks(text string, format Format) ([]cue, error) {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	var cues []cue
	var cur *cue
	var lines []string
	flush := func() {
		if cur != nil {
			cur.text = strings.Join(lines, "\n")
			cues = append(cues, *cur)
		}
		cur, lines = nil, nil
	}
	skipping := false // inside a NOTE/STYLE/REGION block
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), " \t")
		if strings.TrimSpace(line) == "" {
			flush()
			skipping = false
			continue
		}
		if cur != nil {
			lines = append(lines, line)
			continue
		}
		if skipping {
			continue
		}
		if m := arrowRe.FindStringSubmatch(line); m != nil {
			start, ok1 := parseTime(m[1])
			end, ok2 := parseTime(m[2])
			if !ok1 || !ok2 {
				return nil, domain.NewUserError("INPUT", fmt.Sprintf("자막 시각을 읽지 못했습니다: %q", strings.TrimSpace(line)))
			}
			if end < start {
				end = start
			}
			cur = &cue{start: start, end: end}
			continue
		}
		if format == VTT {
			switch {
			case strings.HasPrefix(line, "WEBVTT"), strings.HasPrefix(line, "NOTE"), line == "STYLE", line == "REGION":
				skipping = line != "WEBVTT" && !strings.HasPrefix(line, "WEBVTT ")
				continue
			}
		}
		// An SRT number or a VTT cue identifier: the next line should be the timing.
		if format == SRT {
			if _, err := strconv.Atoi(strings.TrimSpace(line)); err != nil && len(cues) == 0 {
				return nil, ErrFormat
			}
		}
	}
	flush()
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan caption: %w", err)
	}
	return cues, nil
}

// parseTime reads HH:MM:SS,mmm (SRT) or [HH:]MM:SS.mmm (VTT) into ms.
func parseTime(s string) (int64, bool) {
	m := timeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	sec, _ := strconv.Atoi(m[3])
	frac := m[4]
	for len(frac) < 3 {
		frac += "0"
	}
	ms, _ := strconv.Atoi(frac)
	if mi > 59 || sec > 59 {
		return 0, false
	}
	return int64(h)*3_600_000 + int64(mi)*60_000 + int64(sec)*1000 + int64(ms), true
}

// speakerOf turns a WebVTT voice tag (<v Name>) into a role: the first
// voice seen is 방송자, later ones 게스트 1, 2, …. The name itself is not kept.
// Text without a voice tag is the broadcaster's.
func speakerOf(text string, roles map[string]string) (string, string) {
	m := voiceRe.FindStringSubmatch(text)
	if m == nil {
		return "방송자", text
	}
	name := strings.TrimSpace(m[1])
	role, ok := roles[name]
	if !ok {
		if len(roles) == 0 {
			role = "방송자"
		} else {
			role = fmt.Sprintf("게스트 %d", len(roles))
		}
		roles[name] = role
	}
	return role, voiceRe.ReplaceAllString(text, "")
}

// clean drops markup (<i>, <b>, <c.class>, </v>), SRT's {\an8}-style
// positioning, and collapses whitespace so one cue is one line of text.
func clean(s string) string {
	s = tagRe.ReplaceAllString(s, "")
	for {
		i := strings.Index(s, "{\\")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			break
		}
		s = s[:i] + s[i+j+1:]
	}
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&nbsp;", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}
