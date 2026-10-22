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

	// Adaptive gate (REDESIGN 6.2.1), used when GateBusyRatio > 0 and
	// Sensitive is off. A window is judged when it is busy for the broadcast
	// (messages ≥ GateBusyRatio × the median of the finished 10 s bins of the
	// last GateLookbackMs) or a sudden spike (authors ≥ GateSpikeRatio × the
	// baseline's median authors). Neither has an upper cap, so both follow the
	// channel's size and slow changes such as late night. Before
	// GateWarmupBins finished bins the normal fixed gate applies.
	// GateBusyRatio 0 keeps the fixed gate (CzzEdit).
	GateBusyRatio  float64 `json:"gateBusyRatio"`
	GateLookbackMs int64   `json:"gateLookbackMs"`
	GateWarmupBins int     `json:"gateWarmupBins"`
	GateSpikeRatio float64 `json:"gateSpikeRatio"`

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

	// Whole-broadcast analysis of a finished timeline (AnalyzeBroadcast,
	// REDESIGN 6.2.2). Nil keeps the incremental engine for every analysis
	// (CzzEdit, and LIVE sessions started before it existed). LIVE always
	// uses the incremental engine: it cannot see the minutes after a window.
	Broadcast *BroadcastParams `json:"broadcast,omitempty"`
}

// BroadcastParams tune AnalyzeBroadcast (REDESIGN 6.2.2). A window's chat
// is compared with the same time of the broadcast on two scales — the
// surrounding minutes (LocalMs on each side) and the broadcast's slower flow
// (TrendMs on each side) — and with whichever is quieter, so a reaction
// stands out both in a quiet stretch and inside a lively half hour. How
// unusual a count is for its baseline is a z-score from a negative binomial
// tail (surprise), which fits a 20-viewer chat and a 2,000-viewer chat alike.
type BroadcastParams struct {
	LocalMs        int64   `json:"localMs"`        // surrounding minutes, each side
	TrendMs        int64   `json:"trendMs"`        // slower flow (audience size, late night), each side
	ExcludeMs      int64   `json:"excludeMs"`      // kept out of a window's own baseline, each side
	WinsorQuantile float64 `json:"winsorQuantile"` // reference bins above this quantile are capped
	MinRefBins     int     `json:"minRefBins"`     // a scale with fewer valid 10 s bins is not used
	CountFloor     float64 `json:"countFloor"`     // a usual count is never taken as below this
	MinReactors    int     `json:"minReactors"`    // distinct authors a reaction measure needs at least
	VolumeWeight   float64 `json:"volumeWeight"`   // chat and participant surges count this much of a reaction
	ComboCorr      float64 `json:"comboCorr"`      // assumed correlation when a reaction and a surge combine (0: off)
	// Tails at least as heavy as the whole broadcast's own burstiness of
	// each measure (around its moving level). Without it a rare reaction
	// word ("와" a few times a minute) reads as extraordinary whenever it
	// clumps, and every bump of a large chat as significant.
	BroadcastDispersion bool    `json:"broadcastDispersion"`
	MaxDispersion       float64 `json:"maxDispersion"` // at most this burstiness (1/k), however reactive the broadcast
	LongWindow          bool    `json:"longWindow"`    // also judge 20 s windows (reactions that build up)

	OnZ          float64 `json:"onZ"`          // a candidate's peak reaches this score
	OffZ         float64 `json:"offZ"`         // and it lasts while windows stay at or above this
	SensitiveOnZ float64 `json:"sensitiveOnZ"` // OnZ with "항상 가장 민감하게"
	ValleyDepth  float64 `json:"valleyDepth"`  // a long stretch is cut at a dip this far below the peaks around it
	// Strength: strong needs StrongZ and a rank within the top StrongShare
	// of the analysis's candidates, medium MediumZ and the top MediumShare;
	// the rest are weak. The shares keep "strong" a short list in a big,
	// clearly reacting chat too.
	MediumZ     float64 `json:"mediumZ"`
	StrongZ     float64 `json:"strongZ"`
	MediumShare float64 `json:"mediumShare"`
	StrongShare float64 `json:"strongShare"`
}

// DefaultBroadcastParams are the whole-broadcast defaults, tuned on a real
// 3-hour VOD against moments three editor-judges picked and on rescaled
// copies of it (REDESIGN 6.2.2).
func DefaultBroadcastParams() BroadcastParams {
	return BroadcastParams{
		LocalMs: 300_000, TrendMs: 1_800_000, ExcludeMs: 30_000, WinsorQuantile: 0.8, MinRefBins: 12,
		CountFloor: 0.25, MinReactors: 3, VolumeWeight: 0.85, ComboCorr: 0.5,
		BroadcastDispersion: true, MaxDispersion: 1, LongWindow: true,
		OnZ: 3, OffZ: 1.5, SensitiveOnZ: 2.6, ValleyDepth: 1.5,
		MediumZ: 3.4, StrongZ: 4, MediumShare: 0.5, StrongShare: 0.2,
	}
}

// onZ is the peak score a candidate needs with the current sensitivity.
func (b BroadcastParams) onZ(sensitive bool) float64 {
	if sensitive {
		return b.SensitiveOnZ
	}
	return b.OnZ
}

// DefaultParams returns the thresholds new analyses use. Tune defaults here;
// CzzEditParams stays fixed as the legacy reference.
func DefaultParams() Params {
	p := CzzEditParams()
	// The fixed 15 messages were 3.75 × the median 10 s bin (4 messages) of a
	// real 30-messages-a-minute VOD, so the busy gate keeps CzzEdit's behavior
	// there and scales it to other sizes. Medians ignore the reactions
	// themselves even when they are frequent. Two hours follow slow changes
	// (late night) without treating a hot half hour as normal; the spike gate
	// catches reactions in quiet stretches. From REDESIGN 6.2.1's experiments.
	p.GateBusyRatio, p.GateLookbackMs, p.GateWarmupBins, p.GateSpikeRatio = 3.75, 7_200_000, 18, 5
	b := DefaultBroadcastParams()
	p.Broadcast = &b
	return p
}

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
	if p.GateBusyRatio < 0 || p.GateSpikeRatio < 0 ||
		(p.GateBusyRatio > 0 && (p.GateWarmupBins < 1 || p.GateLookbackMs < p.WindowMs)) ||
		p.SensitiveMinMessages > p.MinMessages || p.SensitiveMinMessages < 1 {
		return domain.NewUserError("CONFIG", "판정 시작 기준 설정이 올바르지 않습니다. 기본값으로 되돌린 뒤 다시 시도하세요.")
	}
	if b := p.Broadcast; b != nil {
		if b.LocalMs < p.WindowMs || b.TrendMs < b.LocalMs || b.ExcludeMs < 0 || b.ExcludeMs*2 >= b.LocalMs ||
			b.WinsorQuantile <= 0 || b.WinsorQuantile > 1 || b.MinRefBins < 1 || b.CountFloor <= 0 || b.MinReactors < 1 ||
			b.VolumeWeight < 0 || b.ComboCorr < 0 || b.ComboCorr > 1 || b.MaxDispersion < 0 || b.OffZ <= 0 || b.OnZ < b.OffZ || b.SensitiveOnZ < b.OffZ ||
			b.ValleyDepth <= 0 || b.MediumZ < b.OnZ || b.StrongZ < b.MediumZ || b.MediumShare < 0 || b.MediumShare > 1 || b.StrongShare < 0 || b.StrongShare > b.MediumShare {
			return domain.NewUserError("CONFIG", "방송 전체 분석 설정이 올바르지 않습니다. 기본값으로 되돌린 뒤 다시 시도하세요.")
		}
	}
	return nil
}
