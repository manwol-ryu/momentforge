package domain

// ChatEvent is one chat message on a session's timeline.
type ChatEvent struct {
	ID        string // stable per session (platform message id or a derived hash)
	Seq       int64  // arrival order; breaks ties between equal offsets
	OffsetMs  int64  // position on the timeline (VOD play position or LIVE elapsed)
	AuthorKey string // per-session pseudonym; empty means the author is unknown
	Text      string
	// Kind is "" or ChatKindChat for a message; donations and subscriptions
	// (from NAS backups) carry their amount: won, or months subscribed.
	Kind   string
	Amount int64
	Hidden bool // the platform hid it (bot filter); kept, shown dimmed
}

// Chat event kinds.
const (
	ChatKindChat         = "chat"
	ChatKindDonation     = "donation"
	ChatKindSubscription = "subscription"
	ChatKindSystem       = "system"
)

// KindOrChat is the event's kind with the default spelled out.
func (e ChatEvent) KindOrChat() string {
	if e.Kind == "" {
		return ChatKindChat
	}
	return e.Kind
}

// Gap is a span with no reliable chat (disconnects, before capture). Analysis
// does not learn "quiet" from it and marks overlapping windows invalid.
type Gap struct {
	StartMs int64
	EndMs   int64
}

// Window is the detector's view of one evaluation time T over [T-10s, T).
type Window struct {
	T             int64              `json:"t"`
	N             int                `json:"n"` // counted messages (per-author cap applied)
	U             int                `json:"u"` // distinct authors
	Ratio         *float64           `json:"ratio,omitempty"`
	Z             *float64           `json:"z,omitempty"`
	BaselineN     *float64           `json:"baselineN,omitempty"`
	BaselineReady bool               `json:"baselineReady"`
	Valid         bool               `json:"valid"`
	Signals       []string           `json:"signals"`
	ReactionRatio map[string]float64 `json:"reactionRatios,omitempty"`
	Viewer        *ViewerState       `json:"viewerBaseline,omitempty"` // LIVE with viewer-count adaptation
	Gate          int                `json:"gate,omitempty"`           // adaptive gate: messages that make a window busy for the broadcast
	SpikeGate     int                `json:"spikeGate,omitempty"`      // adaptive gate: authors that make a window a sudden spike
	// Whole-broadcast analysis (VOD): how unusual the window is for its
	// time in the broadcast, on a z-score scale; the strongest of its
	// reaction, chat and participant measures.
	Score    *float64 `json:"score,omitempty"`
	Evidence []string `json:"-"`
}

// ViewerState is the viewer-count baseline in effect at a window (LIVE).
type ViewerState struct {
	Mode         string   `json:"mode"` // "viewer_adaptive" | "chat_only"
	EpochStartMs int64    `json:"epochStartMs"`
	Reference    *float64 `json:"reference"`
	Average      *float64 `json:"average"`
	Status       string   `json:"status"` // "active" | "warming_up" | "missing"
}

// PeakStats are the numbers of a candidate's strongest window.
type PeakStats struct {
	N             int                `json:"n"`
	U             int                `json:"u"`
	Ratio         *float64           `json:"ratio,omitempty"`
	Z             *float64           `json:"z,omitempty"`
	BaselineN     *float64           `json:"baselineN,omitempty"`
	ReactionRatio map[string]float64 `json:"reactionRatios,omitempty"`
	Gate          int                `json:"gate,omitempty"`      // adaptive gate at the peak (see Window)
	SpikeGate     int                `json:"spikeGate,omitempty"` // adaptive gate at the peak (see Window)
}

// Candidate strengths (whole-broadcast analysis).
const (
	StrengthStrong = "strong"
	StrengthMedium = "medium"
	StrengthWeak   = "weak"
)

// Measure is one count at a candidate's peak next to what is usual for that
// time in the broadcast: "laugh: 7 authors in 10 s, usually 0.6".
type Measure struct {
	Key      string  `json:"key"`      // signal key: laugh, emotion:surprise, repeat:<text>, reaction, surge, participants
	Observed int     `json:"observed"` // in the peak 10 s window
	Usual    float64 `json:"usual"`    // typical for a 10 s window around then
	Z        float64 `json:"z"`        // how unusual (z-score scale)
	WindowMs int64   `json:"windowMs"` // length of the counted window (10 s, or 20 s for a reaction that builds up)
}

// Candidate is a suggested editing moment.
type Candidate struct {
	ID              string    `json:"id"`      // m0001…
	GroupID         string    `json:"groupId"` // g<start>; split parts share it
	ReactionStartMs int64     `json:"reactionStartMs"`
	ReactionEndMs   int64     `json:"reactionEndMs"`
	PeakMs          int64     `json:"peakMs"`
	ClipStartMs     int64     `json:"clipStartMs"`
	ClipEndMs       int64     `json:"clipEndMs"`
	Signals         []string  `json:"signals"` // rule keys, e.g. "laugh", "repeat:<text>"
	Reasons         []string  `json:"reasons"` // rule names shown to users
	Peak            PeakStats `json:"peak"`
	BaselineReady   bool      `json:"baselineReady"`
	EvidenceIDs     []string  `json:"evidenceIds"`
	PartialContext  bool      `json:"partialContext"`

	// Whole-broadcast analysis (VOD) only.
	Score    float64   `json:"score,omitempty"`    // peak window's score (z-score scale)
	Strength string    `json:"strength,omitempty"` // strong | medium | weak
	Rank     int       `json:"rank,omitempty"`     // 1 = the strongest candidate of its analysis
	Measures []Measure `json:"measures,omitempty"` // what stood out at the peak, strongest first

	// LIVE only.
	State              string       `json:"state,omitempty"` // generating | waiting_context | confirmed | partial | dropped
	Viewer             *ViewerState `json:"viewerBaseline,omitempty"`
	BaselineTransition bool         `json:"baselineTransition,omitempty"`
	// Tier is how far a LIVE capture judged the moment (captures from rules
	// 3.1.0 on; "" before): early | judged | final. ReplacedBy is the judged
	// candidate that took a dropped one's place ("" none).
	Tier       string `json:"tier,omitempty"`
	ReplacedBy string `json:"replacedBy,omitempty"`
}

// LIVE candidate states.
const (
	CandidateGenerating     = "generating"
	CandidateWaitingContext = "waiting_context"
	CandidateConfirmed      = "confirmed"
	CandidatePartial        = "partial"
	CandidateDropped        = "dropped" // a judgment did not keep it (screens hide it)
)

// LIVE candidate tiers (REDESIGN 6.2.3).
const (
	TierEarly  = "early"  // the incremental engine with its gate eased: seconds after the reaction (예비)
	TierJudged = "judged" // the whole-broadcast analysis with LocalMs of chat after it (5분 판정)
	TierFinal  = "final"  // with TrendMs after it, or the capture ended: never revised (확정)
)

// Utterance is one thing the broadcaster (or a guest) said, from local
// speech recognition or a caption file. Speaker is a role such as 방송자,
// never a name.
type Utterance struct {
	ID         string
	StartMs    int64
	EndMs      int64
	Speaker    string
	Text       string
	Confidence float64 // 0 when the source gives none
	RawText    string  // what speech recognition heard, when AI fixed Text ("" unchanged)
}
