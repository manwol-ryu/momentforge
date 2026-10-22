package cut

import (
	"strings"
	"testing"
)

// Short utterances cut from one recognition result (asr.Split) touch or sit
// a few hundred ms apart; a boundary at one must not reach into the next.
func shortUtterances() []Evidence {
	return []Evidence{
		{Ref: "utt:1", Kind: KindUtterance, StartMs: 100_000, EndMs: 103_000, Text: "이거 진짜 맛있다"},
		{Ref: "utt:2", Kind: KindUtterance, StartMs: 103_000, EndMs: 107_500, Text: "다들 꼭 사 드세요"}, // max-length split: no gap
		{Ref: "utt:3", Kind: KindUtterance, StartMs: 107_700, EndMs: 110_000, Text: "다음은 젤리"},     // 200 ms gap
		{Ref: "utt:4", Kind: KindUtterance, StartMs: 112_000, EndMs: 115_000, Text: "젤리 먹어 볼게요"},  // 2 s pause
		{Ref: "chat:x", Kind: KindChat, StartMs: 110_500, Text: "ㅋㅋㅋ"},
	}
}

func TestResolvePaddingStopsAtNeighbouringUtterances(t *testing.T) {
	known := Known(shortUtterances())
	cases := []struct {
		name       string
		start, end string
		want       [2]int64
		why        string
	}{
		{"touching before, short gap after", "utt:2", "utt:2", [2]int64{103_000, 107_700}, "겹치지 않게"},
		{"short gap after", "utt:1", "utt:2", [2]int64{99_700, 107_700}, "다음 발언"},
		{"short gap before", "utt:3", "utt:3", [2]int64{107_500, 110_500}, "앞 발언"},
		{"real pause keeps the full padding", "utt:4", "utt:4", [2]int64{111_700, 115_500}, "0.5초 뒤"},
	}
	for _, c := range cases {
		spans, adj, issues := ResolveSpans([]SpanRefs{{StartRef: c.start, EndRef: c.end, Role: "main"}}, known, 600_000)
		if len(issues) != 0 || len(spans) != 1 || spans[0].StartMs != c.want[0] || spans[0].EndMs != c.want[1] {
			t.Errorf("%s: spans %+v issues %v, want %v", c.name, spans, issues, c.want)
			continue
		}
		var whys []string
		for _, a := range adj {
			whys = append(whys, a.Why)
		}
		if !strings.Contains(strings.Join(whys, ";"), c.why) {
			t.Errorf("%s: adjustments %v, want one saying %q", c.name, whys, c.why)
		}
	}
}

// A chat boundary snaps to a short utterance's edge with the same limits,
// and an end that lands between two touching utterances snaps to the one it is in.
func TestChatBoundariesSnapToShortUtterances(t *testing.T) {
	known := Known(shortUtterances())
	// chat:x - 6 s = 104.5 s: inside utt:2, so the span starts at utt:2's start (utt:1 touches it).
	spans, _, issues := ResolveSpans([]SpanRefs{{StartRef: "chat:x", EndRef: "utt:4", Role: "main"}}, known, 600_000)
	if len(issues) != 0 || spans[0].StartMs != 103_000 {
		t.Fatalf("snapped start %+v %v", spans, issues)
	}
	// utt:1 + 3 s chat tail: a chat at 104 s ends at 107 s, inside utt:2 → its end, stopped at utt:3.
	ev := append(shortUtterances(), Evidence{Ref: "chat:y", Kind: KindChat, StartMs: 104_000, Text: "와"})
	spans, _, _ = ResolveSpans([]SpanRefs{{StartRef: "utt:1", EndRef: "chat:y", Role: "main"}}, Known(ev), 600_000)
	if spans[0].EndMs != 107_700 {
		t.Errorf("snapped end %+v", spans)
	}
}
