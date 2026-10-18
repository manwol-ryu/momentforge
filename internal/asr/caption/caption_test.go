package caption

import (
	"errors"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

const srtSample = "\xEF\xBB\xBF1\r\n00:00:05,000 --> 00:00:08,500\r\n안녕하세요 <i>방송</i> 시작합니다\r\n\r\n2\r\n00:00:10,000 --> 00:00:12,000\r\n{\\an8}오늘은\r\n벌칙 게임\r\n\r\n3\r\n01:02:03,004 --> 01:02:04,000\r\n마지막 줄\r\n"

const vttSample = `WEBVTT - 제목

NOTE 설명 블록은
무시합니다

STYLE
::cue { color: red }

intro
00:05.000 --> 00:08.500
<v 홍길동>안녕하세요 &amp; 반갑습니다</v>

00:00:10.000 --> 00:00:12.000
<v 김게스트>저도요</v>

00:00:13.000 --> 00:00:14.000
<v.loud 홍길동>또 저예요</v>

00:00:15.000 --> 00:00:16.000
<c.yellow>태그만 있는 줄</c>
`

func TestParseSRT(t *testing.T) {
	utts, format, err := Parse(strings.NewReader(srtSample), Options{})
	if err != nil || format != SRT {
		t.Fatalf("Parse = %v, %v", format, err)
	}
	want := []domain.Utterance{
		{StartMs: 5000, EndMs: 8500, Speaker: "방송자", Text: "안녕하세요 방송 시작합니다"},
		{StartMs: 10000, EndMs: 12000, Speaker: "방송자", Text: "오늘은 벌칙 게임"},
		{StartMs: 3_723_004, EndMs: 3_724_000, Speaker: "방송자", Text: "마지막 줄"},
	}
	if len(utts) != len(want) {
		t.Fatalf("got %d utterances: %+v", len(utts), utts)
	}
	for i := range want {
		if utts[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, utts[i], want[i])
		}
	}
}

func TestParseVTTVoicesBecomeRoles(t *testing.T) {
	utts, format, err := Parse(strings.NewReader(vttSample), Options{})
	if err != nil || format != VTT {
		t.Fatalf("Parse = %v, %v", format, err)
	}
	want := []domain.Utterance{
		{StartMs: 5000, EndMs: 8500, Speaker: "방송자", Text: "안녕하세요 & 반갑습니다"},
		{StartMs: 10000, EndMs: 12000, Speaker: "게스트 1", Text: "저도요"},
		{StartMs: 13000, EndMs: 14000, Speaker: "방송자", Text: "또 저예요"},
		{StartMs: 15000, EndMs: 16000, Speaker: "방송자", Text: "태그만 있는 줄"},
	}
	if len(utts) != len(want) {
		t.Fatalf("got %d utterances: %+v", len(utts), utts)
	}
	for i := range want {
		if utts[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, utts[i], want[i])
		}
	}
	for _, u := range utts {
		if strings.Contains(u.Speaker, "홍길동") || strings.Contains(u.Speaker, "김게스트") || strings.Contains(u.Text, "홍길동") {
			t.Errorf("a name survived: %+v", u)
		}
	}
}

func TestParseOffset(t *testing.T) {
	cases := []struct {
		offset int64
		starts []int64
	}{
		{offset: 2500, starts: []int64{7500, 12500, 3_725_504}},
		{offset: -9000, starts: []int64{1000, 3_714_004}}, // the first cue ends before 0 and is dropped
		{offset: -6000, starts: []int64{0, 4000, 3_717_004}},
	}
	for _, c := range cases {
		utts, _, err := Parse(strings.NewReader(srtSample), Options{OffsetMs: c.offset})
		if err != nil {
			t.Fatalf("offset %d: %v", c.offset, err)
		}
		var got []int64
		for _, u := range utts {
			got = append(got, u.StartMs)
		}
		if len(got) != len(c.starts) {
			t.Fatalf("offset %d: starts %v, want %v", c.offset, got, c.starts)
		}
		for i := range got {
			if got[i] != c.starts[i] {
				t.Errorf("offset %d: starts %v, want %v", c.offset, got, c.starts)
				break
			}
		}
	}
}

func TestParseRejectsOtherFiles(t *testing.T) {
	for _, in := range []string{"", "그냥 글\n두 줄", `{"json":true}`, "1\n00:00:xx,000 --> 00:00:01,000\n글\n"} {
		_, _, err := Parse(strings.NewReader(in), Options{})
		if err == nil {
			t.Errorf("%q: want an error", in)
			continue
		}
		var ue *domain.UserError
		if !errors.Is(err, ErrFormat) && !errors.As(err, &ue) {
			t.Errorf("%q: unexpected error %v", in, err)
		}
	}
	// Everything shifted before zero: a user error, not a silent empty transcript.
	_, _, err := Parse(strings.NewReader(srtSample), Options{OffsetMs: -5_000_000})
	var ue *domain.UserError
	if !errors.As(err, &ue) || ue.Code != "INPUT" {
		t.Errorf("huge negative offset: %v", err)
	}
}

func TestParseTime(t *testing.T) {
	cases := map[string]int64{"00:00:01,500": 1500, "01:02:03.004": 3_723_004, "02:03.4": 123_400, "00:00:00,000": 0}
	for in, want := range cases {
		got, ok := parseTime(in)
		if !ok || got != want {
			t.Errorf("parseTime(%q) = %d, %v; want %d", in, got, ok, want)
		}
	}
	for _, bad := range []string{"1:2", "00:61:00,000", "00:00:60,000", "abc"} {
		if _, ok := parseTime(bad); ok {
			t.Errorf("parseTime(%q) accepted", bad)
		}
	}
}
