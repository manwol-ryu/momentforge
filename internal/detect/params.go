package detect

import (
	"fmt"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Params holds every detection threshold. Rules and the engine read only
// these values; defaults reproduce CzzEdit 0.3.2 (rules 0.1.0).
type Params struct {
	// Clip shaping (user settings in CzzEdit's Config).
	Sensitive       bool     `json:"sensitive"`
	ReactionDelayMs int64    `json:"reactionDelayMs"`
	PreRollMs       int64    `json:"preRollMs"`
	PostRollMs      int64    `json:"postRollMs"`
	IgnoredPhrases  []string `json:"ignoredPhrases"`

	// Windows and baseline.
	WindowMs         int64   `json:"windowMs"`           // current window length (10s)
	StepMs           int64   `json:"stepMs"`             // evaluation interval (5s)
	BaselineLookback int64   `json:"baselineLookbackMs"` // history bins start at T-lookback
	BaselineExclude  int64   `json:"baselineExcludeMs"`  // and end at T-exclude
	MinBaselineBins  int     `json:"minBaselineBins"`
	PerAuthorCap     int     `json:"perAuthorCap"`
	CountFloor       float64 `json:"countFloor"` // denominators never drop below this
	MADScale         float64 `json:"madScale"`

	// Gate every signal must pass (normal / sensitive).
	MinMessages          int `json:"minMessages"`
	MinAuthors           int `json:"minAuthors"`
	MinReactionAuthors   int `json:"minReactionAuthors"`
	SensitiveMinMessages int `json:"sensitiveMinMessages"`
	SensitiveMinAuthors  int `json:"sensitiveMinAuthors"`
	SensitiveMinReaction int `json:"sensitiveMinReactionAuthors"`

	// Rule thresholds.
	SurgeRatio            float64 `json:"surgeRatio"`
	SurgeZ                float64 `json:"surgeZ"`
	ParticipantsMinU      int     `json:"participantsMinAuthors"`
	ParticipantsRatio     float64 `json:"participantsRatio"`
	LaughShare            float64 `json:"laughShare"`
	RepeatShare           float64 `json:"repeatShare"`
	EmotionShare          float64 `json:"emotionShare"`
	ReactionShareIncrease float64 `json:"reactionShareIncrease"`

	// A strong window may start a candidate alone.
	StrongMinMessages int     `json:"strongMinMessages"`
	StrongMinAuthors  int     `json:"strongMinAuthors"`
	StrongRatio       float64 `json:"strongRatio"`
	StrongZ           float64 `json:"strongZ"`

	// Candidate grouping.
	MergeGapMs   int64 `json:"mergeGapMs"`   // signals this close extend a candidate
	MaxSpanMs    int64 `json:"maxSpanMs"`    // longer reactions are split
	CloseAfterMs int64 `json:"closeAfterMs"` // quiet time that closes a candidate (at least PostRollMs)
}

// DefaultParams returns the thresholds new analyses use. Tune defaults here;
// CzzEditParams stays fixed as the legacy reference.
func DefaultParams() Params { return CzzEditParams() }

// CzzEditParams returns the CzzEdit 0.3.2 thresholds (rules 0.1.0). The
// legacy comparison test pins these; do not change them.
func CzzEditParams() Params {
	return Params{
		PreRollMs:      30000,
		PostRollMs:     30000,
		IgnoredPhrases: []string{"안녕하세요", "ㅎㅇ", "출첵"},

		WindowMs:         10000,
		StepMs:           5000,
		BaselineLookback: 940000,
		BaselineExclude:  40000,
		MinBaselineBins:  18,
		PerAuthorCap:     3,
		CountFloor:       3,
		MADScale:         1.4826,

		MinMessages:          15,
		MinAuthors:           5,
		MinReactionAuthors:   5,
		SensitiveMinMessages: 6,
		SensitiveMinAuthors:  3,
		SensitiveMinReaction: 3,

		SurgeRatio:            3,
		SurgeZ:                3,
		ParticipantsMinU:      8,
		ParticipantsRatio:     3,
		LaughShare:            0.35,
		RepeatShare:           0.35,
		EmotionShare:          0.30,
		ReactionShareIncrease: 0.15,

		StrongMinMessages: 30,
		StrongMinAuthors:  10,
		StrongRatio:       5,
		StrongZ:           5,

		MergeGapMs:   20000,
		MaxSpanMs:    180000,
		CloseAfterMs: 30000,
	}
}

// Gate returns the minimum messages, authors and reaction authors in effect.
func (p Params) Gate() (messages, authors, reaction int) {
	if p.Sensitive {
		return p.SensitiveMinMessages, p.SensitiveMinAuthors, p.SensitiveMinReaction
	}
	return p.MinMessages, p.MinAuthors, p.MinReactionAuthors
}

// Validate checks user-adjustable values with CzzEdit's messages.
func (p Params) Validate() error {
	for name, v := range map[string]int64{
		"reaction_delay_ms": p.ReactionDelayMs, "pre_roll_ms": p.PreRollMs, "post_roll_ms": p.PostRollMs,
	} {
		if v < 0 || v > 300000 {
			return domain.NewUserError("CONFIG", fmt.Sprintf("%s: 0~300000 범위의 밀리초 정수가 필요합니다.", name))
		}
	}
	if p.WindowMs <= 0 || p.StepMs <= 0 || p.BaselineLookback <= p.BaselineExclude || p.MinBaselineBins < 1 || p.PerAuthorCap < 1 {
		return domain.NewUserError("CONFIG", "탐지 창 설정이 올바르지 않습니다. 기본값으로 되돌린 뒤 다시 시도하세요.")
	}
	return nil
}
