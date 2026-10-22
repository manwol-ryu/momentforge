package asr

import (
	"testing"
)

// words lays out words back to back from start, each lasting its given ms
// (as Apple reports them: no gap, the silence inside the next word).
func words(start int64, spec ...any) []Word {
	var out []Word
	for i := 0; i < len(spec); i += 2 {
		d := int64(spec[i+1].(int))
		out = append(out, Word{StartMs: start, EndMs: start + d, Text: " " + spec[i].(string)})
		start += d
	}
	return out
}

func TestSplitAtSentenceEndsAndPauses(t *testing.T) {
	// "팔죠" ends at 2000; "애나" holds 1500 ms of silence before 500 ms of speech.
	ws := words(1000, "지금도", 400, "잘", 300, "팔죠", 300, "애나", 2000, "개", 300, "맛있어요.", 600, "휘바람", 400, "사탕", 300)
	got := Split(ws, SplitParams{})
	want := []struct {
		start, end int64
		text       string
	}{
		{1000, 2000, "지금도 잘 팔죠"},
		{3200, 4900, "애나 개 맛있어요."}, // speech of "애나" taken as its last 800 ms
		{4900, 5600, "휘바람 사탕"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, w := range want {
		if got[i].StartMs != w.start || got[i].EndMs != w.end || got[i].Text != w.text {
			t.Errorf("utterance %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestSplitKeepsShortPausesTogether(t *testing.T) {
	// A word just over its speech length (900 ms for 2 characters: 100 ms of pause) does not split.
	got := Split(words(0, "그래서", 400, "이거", 900, "먹어", 300), SplitParams{})
	if len(got) != 1 || got[0].StartMs != 0 || got[0].EndMs != 1600 || got[0].Text != "그래서 이거 먹어" {
		t.Fatalf("got %+v", got)
	}
	// A leading word's silence is trimmed too (the result's own start is not the speech).
	got = Split(words(10_000, "아", 2600, "네", 300), SplitParams{})
	if len(got) != 1 || got[0].StartMs != 11_800 {
		t.Fatalf("leading silence: %+v", got)
	}
}

func TestSplitLimitsLength(t *testing.T) {
	// 24 words of 500 ms with no pause: 12 s, cut once near the middle.
	var spec []any
	for range 24 {
		spec = append(spec, "말", 500)
	}
	got := Split(words(0, spec...), SplitParams{MaxMs: 8000})
	if len(got) != 2 || got[0].EndMs != 6000 || got[1].StartMs != 6000 {
		t.Fatalf("no pause: %+v", got)
	}
	// With a small pause (900 ms word: 100 ms) off the middle, the cut goes there.
	spec[13*2+1] = 900
	got = Split(words(0, spec...), SplitParams{MaxMs: 8000})
	if len(got) != 2 || got[0].EndMs != 6500 || got[1].StartMs != 6600 {
		t.Fatalf("with a pause: %+v", got)
	}
	// Both parts keep MinMs when they can: a pause right at the start is not used.
	spec[13*2+1] = 500
	spec[1*2+1] = 2000
	got = Split(words(0, spec...), SplitParams{MaxMs: 8000, MinMs: 1500, PauseMs: 5000})
	if len(got) != 2 || got[0].EndMs-got[0].StartMs < 1500 {
		t.Fatalf("min length: %+v", got)
	}
	for _, u := range got {
		if u.EndMs-u.StartMs > 8000 {
			t.Errorf("too long: %+v", u)
		}
	}
	// One long word stays one utterance.
	if got := Split(words(0, "아아아", 9000), SplitParams{MaxMs: 8000}); len(got) != 1 {
		t.Errorf("single word: %+v", got)
	}
	if got := Split([]Word{{Text: " "}}, SplitParams{}); got != nil {
		t.Errorf("blank: %+v", got)
	}
}
