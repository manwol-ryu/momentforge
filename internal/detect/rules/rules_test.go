package rules

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

func TestMessageKeys(t *testing.T) {
	cases := []struct {
		text string
		want []string
	}{
		{"ㅋㅋ", []string{"laugh"}},
		{"ㅎㅎ 진짜", []string{"laugh", "repeat:ㅎㅎ 진짜"}},
		{"😂", []string{"laugh"}},
		{"와", []string{"emotion:surprise"}},
		{"와 대박", []string{"emotion:surprise", "repeat:와 대박"}},
		{"대박이네", []string{"repeat:대박이네"}}, // not a standalone word
		{"미쳤다 ㄷㄷ", []string{"emotion:surprise", "repeat:미쳤다 ㄷㄷ"}},
		{"ㄷㄷ", []string{"emotion:surprise"}},
		{"아깝다!!", []string{"emotion:sadness"}},
		{"ㅠㅠ", []string{"emotion:sadness"}},
		{"다시 보고 싶은 장면", []string{"repeat:다시 보고 싶은 장면"}},
	}
	for _, c := range cases {
		var got []string
		for _, r := range All() {
			if mr, ok := r.(detect.MessageRule); ok {
				got = append(got, mr.Keys(detect.Normalize(c.text))...)
			}
		}
		if !sameSet(got, c.want) {
			t.Errorf("%q: keys %v, want %v", c.text, got, c.want)
		}
	}
}

func sameSet(a, b []string) bool {
	m := map[string]int{}
	for _, x := range a {
		m[x]++
	}
	for _, x := range b {
		m[x]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
	}
	return true
}

func TestAnalyzeIsCancellable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := detect.Analyze(ctx, domain.DemoChat(), domain.DemoDurationMs, nil, detect.DefaultParams(), All(), nil)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "CANCELLED" || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want CANCELLED wrapping context.Canceled", err)
	}
}

func TestInvalidRangeFromSettings(t *testing.T) {
	// Laughter in the first 20 s forms a candidate before any baseline exists
	// (two consecutive windows; the fixed gate, as the adaptive one waits for
	// 3 minutes of chat). A 300 s reaction delay puts its clip end before its
	// clip start.
	var events []domain.ChatEvent
	for offset := int64(0); offset < 20000; offset += 2000 {
		for i := int64(0); i < 18; i++ {
			n := int64(len(events)) + 1
			events = append(events, domain.ChatEvent{ID: fmt.Sprint(n), Seq: n, OffsetMs: offset + i*30,
				AuthorKey: fmt.Sprintf("r%d", i), Text: "ㅋㅋㅋ"})
		}
	}
	p := detect.CzzEditParams()
	p.ReactionDelayMs, p.PreRollMs, p.PostRollMs = 300000, 0, 0
	_, err := detect.Analyze(context.Background(), events, 120000, nil, p, All(), nil)
	if ue, ok := domain.AsUserError(err); !ok || ue.Code != "INVALID_RANGE" {
		t.Fatalf("err = %v, want INVALID_RANGE", err)
	}
}

func TestParamsValidate(t *testing.T) {
	p := detect.DefaultParams()
	p.PreRollMs = 300001
	if _, err := detect.NewEngine(p, All()); err == nil {
		t.Fatal("pre_roll_ms over 300000 must be rejected")
	}
	if _, err := detect.NewEngine(detect.DefaultParams(), append(All(), Laugh{})); err == nil {
		t.Fatal("duplicate rule ids must be rejected")
	}
}

// Feeding the demo in time-ordered batches with late arrivals must give the
// same windows as one finished batch (LIVE and VOD share the engine).
func TestIncrementalMatchesBatch(t *testing.T) {
	events := domain.DemoChat()
	p := detect.DefaultParams()

	batch, err := detect.NewEngine(p, All())
	if err != nil {
		t.Fatal(err)
	}
	batch.Add(events)

	live, _ := detect.NewEngine(p, All())
	var held []domain.ChatEvent
	next := 0
	for tm := p.WindowMs; tm <= domain.DemoDurationMs; tm += p.StepMs {
		var arrived []domain.ChatEvent
		for next < len(events) && events[next].OffsetMs < tm {
			// Hold back every 7th event one step to simulate late delivery.
			if next%7 == 0 && events[next].OffsetMs >= tm-p.StepMs {
				held = append(held, events[next])
			} else {
				arrived = append(arrived, events[next])
			}
			next++
		}
		live.Add(arrived)
		// The held events belong to the newest step; deliver them before evaluating.
		live.Add(held)
		held = nil

		bw, bs := batch.Evaluate(tm, 0)
		lw, ls := live.Evaluate(tm, 0)
		if bs != ls || !reflect.DeepEqual(bw, lw) {
			t.Fatalf("t=%d: incremental window differs from batch\nbatch %+v\nlive  %+v", tm, bw, lw)
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"ㅋㅋㅋㅋㅋ":              "ㅋㅋ",
		"  Nice   PLAY ":     "nice play",
		"\u110b\u116a":       "와", // conjoining jamo → syllable
		"\u1112\u1165\u11af": "헐", // L V T
		"가\u11a8":            "각", // LV syllable + T
		"우와아아아":              "우와아아",
	}
	for in, want := range cases {
		if got := detect.Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}
