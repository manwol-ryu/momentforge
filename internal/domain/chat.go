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
	Evidence      []string           `json:"-"`
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

	// LIVE only.
	State              string       `json:"state,omitempty"` // generating | waiting_context | confirmed | partial
	Viewer             *ViewerState `json:"viewerBaseline,omitempty"`
	BaselineTransition bool         `json:"baselineTransition,omitempty"`
}

// LIVE candidate states.
const (
	CandidateGenerating     = "generating"
	CandidateWaitingContext = "waiting_context"
	CandidateConfirmed      = "confirmed"
	CandidatePartial        = "partial"
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
}
