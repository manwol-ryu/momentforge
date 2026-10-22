package detect

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Minimal rules for engine tests (the real ones live in detect/rules, which
// this package cannot import): a laugh key, a repeat key per text, and the
// two window rules.
type benchLaugh struct{}

func (benchLaugh) ID() string             { return "laugh" }
func (benchLaugh) Name() string           { return "웃음" }
func (benchLaugh) Share(p Params) float64 { return p.LaughShare }
func (benchLaugh) Keys(text string) []string {
	if strings.Contains(text, "ㅋㅋ") {
		return []string{"laugh"}
	}
	return nil
}

type benchRepeat struct{}

func (benchRepeat) ID() string             { return "repeat" }
func (benchRepeat) Name() string           { return "반복" }
func (benchRepeat) Share(p Params) float64 { return p.RepeatShare }
func (benchRepeat) Keys(text string) []string {
	if len([]rune(text)) >= 2 {
		return []string{"repeat:" + text}
	}
	return nil
}

type benchSurge struct{}

func (benchSurge) ID() string   { return "surge" }
func (benchSurge) Name() string { return "급증" }
func (benchSurge) Fires(cur WindowStats, base Baseline, p Params) bool {
	return base.Ready && base.Ratio >= p.SurgeRatio && base.Z >= p.SurgeZ
}

type benchParticipants struct{}

func (benchParticipants) ID() string   { return "participants" }
func (benchParticipants) Name() string { return "참여" }
func (benchParticipants) Fires(cur WindowStats, base Baseline, p Params) bool {
	return base.Ready && cur.U >= p.ParticipantsMinU && float64(cur.U) >= p.ParticipantsRatio*math.Max(base.U, 1)
}

func benchRules() []Rule {
	return []Rule{benchLaugh{}, benchRepeat{}, benchSurge{}, benchParticipants{}}
}

// syntheticChat is a deterministic broadcast of durationMs: a slowly
// changing rate around perSecond messages a second, many distinct texts,
// and a laugh or repeated-phrase burst every few minutes.
func syntheticChat(durationMs int64, perSecond float64) []domain.ChatEvent {
	var out []domain.ChatEvent
	x := uint64(88172645463325252)
	rnd := func() float64 { // xorshift: stable across Go versions
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		return float64(x%1_000_000) / 1_000_000
	}
	texts := []string{"ㅋㅋㅋㅋ", "와", "미쳤다", "ㄷㄷ", "gg", "안녕하세요", "!명령", "그게 되네", "아니 왜", "레전드"}
	seq := int64(0)
	for sec := int64(0); sec*1000 < durationMs; sec++ {
		rate := perSecond * (0.6 + 0.4*math.Sin(float64(sec)/1800))
		burst := sec%300 >= 120 && sec%300 < 135
		if burst {
			rate *= 6
		}
		n := int(rate + rnd())
		for i := 0; i < n; i++ {
			seq++
			text := fmt.Sprintf("잡담 %d", int(rnd()*400))
			switch r := rnd(); {
			case burst && r < 0.6:
				text = texts[(sec/300)%3]
			case r < 0.15:
				text = texts[int(rnd()*float64(len(texts)))]
			}
			author := fmt.Sprintf("u%d", int(rnd()*rate*40))
			if rnd() < 0.002 {
				author = ""
			}
			out = append(out, domain.ChatEvent{ID: fmt.Sprintf("e%d", seq), Seq: seq,
				OffsetMs: sec*1000 + int64(i)*1000/int64(n), AuthorKey: author, Text: text})
		}
	}
	return out
}

func incrementalParams() Params {
	p := DefaultParams()
	p.Broadcast = nil
	return p
}

// BenchmarkAnalyzeIncremental measures the incremental engine on a 6-hour,
// ~300,000-message broadcast. Run with: go test ./internal/detect -run '^$' -bench Incremental
func BenchmarkAnalyzeIncremental(b *testing.B) {
	events := syntheticChat(6*3600*1000, 14)
	gaps := []domain.Gap{{StartMs: 3_600_000, EndMs: 3_660_000}}
	b.ResetTimer()
	for b.Loop() {
		if _, err := Analyze(context.Background(), events, 6*3600*1000, gaps, incrementalParams(), benchRules(), nil); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLiveAnalyzer feeds the same broadcast to a LIVE analyzer one
// second at a time, as a capture does.
func BenchmarkLiveAnalyzer(b *testing.B) {
	events := syntheticChat(2*3600*1000, 14)
	b.ResetTimer()
	for b.Loop() {
		la, err := NewLiveAnalyzer(0, incrementalParams(), benchRules(), false)
		if err != nil {
			b.Fatal(err)
		}
		i := 0
		for sec := int64(1); sec <= 2*3600; sec++ {
			j := i
			for j < len(events) && events[j].OffsetMs < sec*1000 {
				j++
			}
			la.Add(events[i:j])
			i = j
			if _, err := la.Advance(sec*1000-5000, false); err != nil {
				b.Fatal(err)
			}
		}
	}
}
