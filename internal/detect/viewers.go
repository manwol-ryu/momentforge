package detect

import (
	"math"
	"sort"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// ViewerSample is one observation of concurrent viewers on the timeline.
type ViewerSample struct {
	TimeMs      int64  `json:"time_ms"`      // timeline position (elapsed since origin)
	BroadcastMs int64  `json:"broadcast_ms"` // elapsed since the broadcast started
	LiveID      string `json:"live_id"`
	Segment     int    `json:"segment"`
	Viewers     int    `json:"viewer_count"` // ignored unless Quality is "ok"
	Quality     string `json:"quality"`      // "ok" | "missing"
}

// ViewerEpoch records when the baseline reference was (re)set.
type ViewerEpoch struct {
	AtMs          int64    `json:"at_ms"`
	StartMs       int64    `json:"start_ms"`
	Previous      *float64 `json:"previous"`
	Reference     float64  `json:"reference"`
	Reason        string   `json:"reason"` // "initial" | "drop_10_percent"
	MeanStartMs   int64    `json:"mean_start_ms"`
	MeanEndMs     int64    `json:"mean_end_ms"`
	DecreaseRatio *float64 `json:"decrease_ratio"`
	AnchorKind    string   `json:"anchor_kind,omitempty"`
}

// Viewer-baseline constants (CzzEdit viewers.Baseline).
const (
	viewerWarmupMs   = 1_800_000 // no reference before 30 min of broadcast
	viewerMeanMs     = 300_000   // reference = 5-minute time-weighted mean
	viewerHoldMs     = 120_000   // one sample stands for at most 2 minutes
	viewerCoverMs    = 240_000   // the mean needs 4 minutes of coverage…
	viewerMinObs     = 3         // …from at least 3 observations
	viewerKeepMs     = 420_000
	viewerRelearnGap = 300_000 // at least 5 minutes between reference changes
	viewerDropFactor = 0.9     // a 10 % drop…
	viewerDropHits   = 3       // …confirmed 3 times…
	viewerDropSpanMs = 120_000 // …over at least 2 minutes
	viewerFreshMs    = 120_000
	noChange         = math.MinInt64 / 2
)

// ViewerBaseline is a causal viewer-count reference. A sustained 10 % drop
// starts a new epoch, and detection relearns its chat baseline from there.
type ViewerBaseline struct {
	samples    []ViewerSample
	index      int
	past       []ViewerSample
	ref        *float64
	epoch      int64
	lastChange int64
	hits       []int64
	epochs     []ViewerEpoch
	lastSeg    *[2]any
}

// NewViewerBaseline starts from samples (any order).
func NewViewerBaseline(samples []ViewerSample) *ViewerBaseline {
	s := append([]ViewerSample(nil), samples...)
	sort.SliceStable(s, func(i, j int) bool { return s[i].TimeMs < s[j].TimeMs })
	return &ViewerBaseline{samples: s, lastChange: noChange}
}

// Epochs lists reference changes so far.
func (b *ViewerBaseline) Epochs() []ViewerEpoch { return append([]ViewerEpoch(nil), b.epochs...) }

// Append adds LIVE samples newer than everything seen, keeping state.
func (b *ViewerBaseline) Append(rows []ViewerSample) {
	last := int64(-1)
	if len(b.samples) > 0 {
		last = b.samples[len(b.samples)-1].TimeMs
	} else if len(b.past) > 0 {
		last = b.past[len(b.past)-1].TimeMs
	}
	var fresh []ViewerSample
	for _, s := range rows {
		if s.TimeMs > last {
			fresh = append(fresh, s)
		}
	}
	sort.SliceStable(fresh, func(i, j int) bool { return fresh[i].TimeMs < fresh[j].TimeMs })
	b.samples = append(append([]ViewerSample(nil), b.samples[b.index:]...), fresh...)
	b.index = 0
}

func valid(s ViewerSample) bool { return s.Quality == "ok" && s.Viewers > 0 }

func (b *ViewerBaseline) average(t, minimum int64) *float64 {
	start := max(minimum, t-viewerMeanMs)
	if t-start < viewerMeanMs {
		return nil
	}
	var weighted, covered float64
	obs := 0
	for i, s := range b.past {
		end := min(t, s.TimeMs+viewerHoldMs)
		if i+1 < len(b.past) {
			end = min(end, b.past[i+1].TimeMs)
		}
		length := max(0, end-max(start, s.TimeMs))
		if length > 0 && valid(s) {
			covered += float64(length)
			weighted += float64(length) * float64(s.Viewers)
			obs++
		}
	}
	if covered >= viewerCoverMs && obs >= viewerMinObs {
		v := weighted / covered
		return &v
	}
	return nil
}

// At consumes samples up to t and returns the baseline in effect.
func (b *ViewerBaseline) At(t int64) domain.ViewerState {
	for b.index < len(b.samples) && b.samples[b.index].TimeMs <= t {
		s := b.samples[b.index]
		b.index++
		seg := [2]any{s.LiveID, s.Segment}
		if b.lastSeg != nil && seg != *b.lastSeg {
			b.past, b.ref, b.hits, b.lastChange = nil, nil, nil, noChange
		}
		b.lastSeg = &seg
		now := s.TimeMs
		origin := now - s.BroadcastMs
		b.past = append(b.past, s)
		kept := b.past[:0]
		for _, p := range b.past {
			if p.TimeMs >= now-viewerKeepMs {
				kept = append(kept, p)
			}
		}
		b.past = kept
		avg := b.average(now, origin+viewerWarmupMs)
		if !valid(s) || avg == nil {
			b.hits = nil
			continue
		}
		switch {
		case b.ref == nil:
			b.ref = ptr(*avg)
			b.epoch = max(origin+viewerWarmupMs, now-viewerMeanMs)
			b.lastChange = now
			anchor := "late"
			if now == origin+viewerWarmupMs+viewerMeanMs {
				anchor = "on_time"
			}
			b.epochs = append(b.epochs, ViewerEpoch{AtMs: now, StartMs: b.epoch, Reference: *avg, Reason: "initial",
				MeanStartMs: now - viewerMeanMs, MeanEndMs: now, AnchorKind: anchor})
		case now-b.lastChange >= viewerRelearnGap && *avg <= *b.ref*viewerDropFactor+1e-9:
			b.hits = append(b.hits, now)
			if len(b.hits) >= viewerDropHits && now-b.hits[0] >= viewerDropSpanMs {
				old := *b.ref
				b.ref, b.epoch, b.lastChange, b.hits = ptr(*avg), now, now, nil
				b.epochs = append(b.epochs, ViewerEpoch{AtMs: now, StartMs: now, Previous: ptr(old), Reference: *avg,
					Reason: "drop_10_percent", MeanStartMs: now - viewerMeanMs, MeanEndMs: now, DecreaseRatio: ptr(1 - *avg/old)})
			}
		default:
			b.hits = nil
		}
	}
	var last *ViewerSample
	if len(b.past) > 0 {
		last = &b.past[len(b.past)-1]
	}
	fresh := last != nil && valid(*last) && t-last.TimeMs < viewerFreshMs
	if !fresh {
		b.hits = nil
	}
	var avg *float64
	if last != nil {
		avg = b.average(t, last.TimeMs-last.BroadcastMs+viewerWarmupMs)
	}
	st := domain.ViewerState{Mode: "chat_only", Reference: b.ref, Average: avg, Status: "missing"}
	switch {
	case fresh && avg != nil && b.ref != nil:
		st.Mode, st.EpochStartMs, st.Status = "viewer_adaptive", b.epoch, "active"
	case fresh:
		st.Status = "warming_up"
	}
	if st.Reference != nil {
		st.Reference = ptr(*st.Reference)
	}
	return st
}
