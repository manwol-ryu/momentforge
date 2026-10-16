package domain

// ChatEvent is one chat message on a session's timeline.
type ChatEvent struct {
	ID        string // stable per session (platform message id or a derived hash)
	Seq       int64  // arrival order; breaks ties between equal offsets
	OffsetMs  int64  // position on the timeline (VOD play position or LIVE elapsed)
	AuthorKey string // per-session pseudonym; empty means the author is unknown
	Text      string
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
	Evidence      []string           `json:"-"`
}

// PeakStats are the numbers of a candidate's strongest window.
type PeakStats struct {
	N             int                `json:"n"`
	U             int                `json:"u"`
	Ratio         *float64           `json:"ratio,omitempty"`
	Z             *float64           `json:"z,omitempty"`
	BaselineN     *float64           `json:"baselineN,omitempty"`
	ReactionRatio map[string]float64 `json:"reactionRatios,omitempty"`
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
}
