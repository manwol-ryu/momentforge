package rules

import (
	"math"

	"github.com/manwol-ryu/momentforge/internal/detect"
)

// All returns every registered rule. Add new rules here.
func All() []detect.Rule {
	return CzzEdit()
}

// CzzEdit returns CzzEdit 0.3.2's five rules (D1–D5). The legacy comparison
// test pins this set; new rules go in All, not here.
func CzzEdit() []detect.Rule {
	return []detect.Rule{Laugh{}, Surge{}, Repeat{}, Emotion{}, Participants{}}
}

// Laugh (D1): many authors laughing at once.
type Laugh struct{}

func (Laugh) ID() string                    { return "laugh" }
func (Laugh) Name() string                  { return "웃음 증가" }
func (Laugh) Share(p detect.Params) float64 { return p.LaughShare }
func (Laugh) Keys(text string) []string {
	if laughPattern.MatchString(text) {
		return []string{"laugh"}
	}
	return nil
}

// Emotion (D4): surprise or disappointment shared by many authors.
type Emotion struct{}

func (Emotion) ID() string                    { return "emotion" }
func (Emotion) Name() string                  { return "감정 반응" }
func (Emotion) Share(p detect.Params) float64 { return p.EmotionShare }
func (Emotion) Keys(text string) []string {
	var keys []string
	if surpriseJamo.MatchString(text) || anyStandalone(text, surpriseWords) {
		keys = append(keys, "emotion:surprise")
	}
	if sadnessPattern.MatchString(text) {
		keys = append(keys, "emotion:sadness")
	}
	return keys
}

func anyStandalone(text string, words []string) bool {
	for _, w := range words {
		if hasStandaloneWord(text, w) {
			return true
		}
	}
	return false
}

// Repeat (D3): many authors posting the same phrase.
type Repeat struct{}

func (Repeat) ID() string                    { return "repeat" }
func (Repeat) Name() string                  { return "집단 반복" }
func (Repeat) Share(p detect.Params) float64 { return p.RepeatShare }
func (Repeat) Keys(text string) []string {
	if text == "" || isPureReaction(text) {
		return nil
	}
	return []string{"repeat:" + text}
}

// Surge (D2): message volume far above the learned baseline.
type Surge struct{}

func (Surge) ID() string   { return "surge" }
func (Surge) Name() string { return "채팅 급증" }
func (Surge) Fires(cur detect.WindowStats, b detect.Baseline, p detect.Params) bool {
	return b.Ready && b.Ratio >= p.SurgeRatio && b.Z >= p.SurgeZ
}

// Participants (D5): many more distinct authors than usual.
type Participants struct{}

func (Participants) ID() string   { return "participants" }
func (Participants) Name() string { return "참여자 급증" }
func (Participants) Fires(cur detect.WindowStats, b detect.Baseline, p detect.Params) bool {
	return b.Ready && cur.U >= p.ParticipantsMinU && float64(cur.U)/math.Max(b.U, p.CountFloor) >= p.ParticipantsRatio
}
