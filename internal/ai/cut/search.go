package cut

import (
	"strings"
	"unicode/utf8"
)

// Searching (docs/AI-CUT-ANALYSIS.md 3.2, phase 6-C): after the analyze
// step the model may ask the server to read more of the same session. The
// server runs the requests within a budget and sends the analyze step again
// with what it found. This file holds the request limits and the budget;
// the app runs the queries.

// Tool names a request may use.
const (
	ToolSearch      = "search"
	ToolReadAround  = "read_around"
	ToolReadRange   = "read_range"
	ToolEvents      = "events"   // sound and screen observations: none before phase 6-E
	ToolActivity    = "activity" // chat volume per bucket, to scan for where a reaction was
	ToolListThreads = "list_threads"
)

// Limits of one request, from the design's table.
const (
	SearchMaxQueryRunes  = 40
	SearchMaxResults     = 30
	ReadMaxUtterances    = 60
	ReadMaxChats         = 200
	ReadAroundMaxWindowS = 300
	ReadRangeMaxMs       = 10 * 60 * 1000
	ActivityMaxBuckets   = 500
	ActivityMinBucketS   = 10
)

// Budget is what one candidate's searching may use at most. Rounds are
// analyze steps after the first; bytes count the text of evidence added by
// searches (what the searches make the user send beyond the preview).
type Budget struct {
	MaxRounds           int `json:"max_rounds"`
	MaxRequestsPerRound int `json:"max_requests_per_round"`
	MaxBytes            int `json:"max_bytes"`
}

// Default budget (the settings screen changes rounds and bytes).
const (
	DefaultSearchRounds           = 4
	DefaultSearchRequestsPerRound = 6
	DefaultSearchKB               = 80
	MaxSearchRounds               = 10
	MaxSearchKB                   = 1024
)

// DefaultBudget is the design's default.
func DefaultBudget() Budget {
	return Budget{MaxRounds: DefaultSearchRounds, MaxRequestsPerRound: DefaultSearchRequestsPerRound, MaxBytes: DefaultSearchKB * 1024}
}

// MaxRequests is the most requests one candidate takes: the four steps,
// one re-plan with its two checks, and one more analyze step per search round.
func MaxRequests(b Budget) int { return BaseRequestsPerCandidate + max(0, b.MaxRounds) }

// ActivityPoint is one bucket of the activity tool's answer.
type ActivityPoint struct {
	StartMs int64 `json:"start_ms"`
	Chats   int   `json:"chats"`
	Authors int   `json:"authors"`
}

// SearchResult is one executed request and what it added. FoundRefs are the
// evidence refs the request brought into the input (already-known ones are
// not repeated); Note says why a request did less than asked.
type SearchResult struct {
	Request   SearchRequest   `json:"request"`
	Round     int             `json:"round"`
	Ran       bool            `json:"ran"` // false: skipped (limit, repeat or a bad request; Note says why)
	FoundRefs []string        `json:"found_refs"`
	Activity  []ActivityPoint `json:"activity,omitempty"`
	Bytes     int             `json:"bytes"`
	Note      string          `json:"note,omitempty"`
}

// BudgetUse is how much of the budget a candidate's searching used.
type BudgetUse struct {
	Rounds    int  `json:"rounds"`
	Requests  int  `json:"requests"`
	Bytes     int  `json:"bytes"`
	Exhausted bool `json:"exhausted"` // a round or bytes limit stopped a search the model still wanted
	Declined  bool `json:"declined"`  // the user chose not to send a round's results
}

// Clamp applies the defaults and limits to a request the model made:
// before_ms defaults to the candidate's clip start, windows and ranges are
// capped, queries are trimmed. The second value says why the request will
// not run ("" when it will).
func Clamp(r SearchRequest, in Input) (SearchRequest, string) {
	clipStart := in.WindowStartMs
	if len(in.Candidates) > 0 {
		clipStart = in.Candidates[0].ClipStartMs
	}
	end := in.DurationMs
	if end <= 0 {
		end = in.WindowEndMs
	}
	switch r.Tool {
	case ToolSearch:
		r.Query = strings.TrimSpace(r.Query)
		if r.Query == "" {
			return r, "query가 비어 있어 건너뜀"
		}
		if utf8.RuneCountInString(r.Query) > SearchMaxQueryRunes {
			r.Query = string([]rune(r.Query)[:SearchMaxQueryRunes])
		}
		if r.BeforeMs <= 0 || r.BeforeMs > end {
			r.BeforeMs = clipStart
		}
		switch r.In {
		case "utterances", "chats", "both":
		default:
			r.In = "both"
		}
		r.AtMs, r.FromMs, r.ToMs, r.WindowS = 0, 0, 0, 0
	case ToolReadAround:
		if r.AtMs < 0 || r.AtMs > end {
			return r, "at_ms가 영상 범위 밖이라 건너뜀"
		}
		if r.WindowS <= 0 {
			r.WindowS = 60
		}
		r.WindowS = min(r.WindowS, ReadAroundMaxWindowS)
		r.Query, r.In, r.BeforeMs, r.FromMs, r.ToMs = "", "", 0, 0, 0
	case ToolReadRange:
		if r.FromMs < 0 || r.ToMs <= r.FromMs || r.FromMs > end {
			return r, "from_ms~to_ms가 올바르지 않아 건너뜀"
		}
		r.ToMs = min(r.ToMs, end, r.FromMs+ReadRangeMaxMs)
		r.Query, r.In, r.BeforeMs, r.AtMs, r.WindowS = "", "", 0, 0, 0
	case ToolActivity:
		if r.FromMs < 0 || r.ToMs <= r.FromMs {
			r.FromMs, r.ToMs = 0, end
		}
		r.ToMs = min(r.ToMs, end)
		if r.WindowS < ActivityMinBucketS {
			r.WindowS = 60 // bucket seconds travel in window_s
		}
		if buckets := (r.ToMs - r.FromMs) / (r.WindowS * 1000); buckets > ActivityMaxBuckets {
			r.WindowS = ((r.ToMs-r.FromMs)/ActivityMaxBuckets + 999) / 1000
		}
		r.Query, r.In, r.BeforeMs, r.AtMs = "", "", 0, 0
	case ToolEvents, ToolListThreads:
		return r, "이 세션에는 아직 소리·화면 관측과 이야기 색인이 없음"
	default:
		return r, "모르는 tool: " + r.Tool
	}
	return r, ""
}

// SameRequest says two requests would read the same thing (a repeat the
// server need not run again).
func SameRequest(a, b SearchRequest) bool {
	return a.Tool == b.Tool && a.Query == b.Query && a.In == b.In && a.BeforeMs == b.BeforeMs && a.AtMs == b.AtMs &&
		a.FromMs == b.FromMs && a.ToMs == b.ToMs && a.WindowS == b.WindowS
}

// Merge adds evidence found by a search to the input (marked, in time
// order) and to known, skipping what is already there. It returns the refs
// actually added.
func Merge(in *Input, known map[string]Evidence, found []Evidence) []string {
	var added []string
	for _, e := range found {
		if _, ok := known[e.Ref]; ok {
			continue
		}
		e.FoundBySearch = true
		known[e.Ref] = e
		in.Evidence = append(in.Evidence, e)
		added = append(added, e.Ref)
	}
	if len(added) > 0 {
		sortEvidence(in.Evidence)
	}
	return added
}

func sortEvidence(ev []Evidence) {
	for i := 1; i < len(ev); i++ {
		for j := i; j > 0 && ev[j].StartMs < ev[j-1].StartMs; j-- {
			ev[j], ev[j-1] = ev[j-1], ev[j]
		}
	}
}
