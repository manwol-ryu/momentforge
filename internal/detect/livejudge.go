package detect

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// A LIVE capture's candidates come in tiers (REDESIGN 6.2.3). The
// incremental engine, its gate eased (Params.Early), shows early ones
// seconds after a reaction ends. The whole-broadcast analysis, run again on
// the chat stored so far, judges a moment once LocalMs of chat followed it
// (judged, "5분 판정") and settles it once TrendMs did or the capture ended
// (final, "확정"): the criteria a finished broadcast gets, so a capture ends
// with the candidates its VOD would. A judged candidate takes the place of
// the early ones it overlaps; early ones no judgment keeps are dropped.

// JudgeLive merges found, a whole-broadcast analysis of the chat in
// [fromMs, endMs), into a LIVE capture's stored candidates and returns
// those to store again (unchanged ones included; the store skips them).
// Judged candidates are "b0001"…: a moment keeps its ID while later
// analyses find it, and one no longer found is dropped. Final candidates
// are never revised. With final (the capture ended) every moment found is
// settled and every early candidate left is dropped.
func JudgeLive(stored, found []domain.Candidate, fromMs, endMs int64, final bool, p Params) []domain.Candidate {
	b := p.Broadcast
	judgedBy := endMs - b.LocalMs // a reaction ending by here has its surrounding minutes
	settledBy := endMs - b.TrendMs
	reliableFrom := int64(0) // earlier moments lack chat before them in this read: an earlier one judged them
	if fromMs > 0 {
		reliableFrom = fromMs + b.TrendMs
	}
	same := func(x, y domain.Candidate) bool {
		return x.ReactionStartMs <= y.ReactionEndMs+p.MergeGapMs && y.ReactionStartMs <= x.ReactionEndMs+p.MergeGapMs
	}

	serial := 0
	var open, early, settled []domain.Candidate
	for _, c := range stored {
		if n, ok := judgedNumber(c.ID); ok {
			serial = max(serial, n)
		}
		switch {
		case c.Tier == domain.TierFinal:
			settled = append(settled, c)
		case c.Tier == domain.TierJudged:
			open = append(open, c)
		case c.Tier == domain.TierEarly && c.State != domain.CandidateDropped:
			early = append(early, c)
		}
	}
	// Candidates still standing come before dropped ones when a moment is matched again.
	slices.SortStableFunc(open, func(x, y domain.Candidate) int {
		return cmp.Or(cmp.Compare(rank(dropped(x)), rank(dropped(y))), cmp.Compare(x.ReactionStartMs, y.ReactionStartMs))
	})

	var out, kept []domain.Candidate
	claimed := map[string]bool{}
	found = slices.Clone(found)
	slices.SortStableFunc(found, func(x, y domain.Candidate) int { return cmp.Compare(x.ReactionStartMs, y.ReactionStartMs) })
	for _, f := range found {
		if f.ReactionStartMs < reliableFrom || slices.ContainsFunc(settled, func(s domain.Candidate) bool { return same(s, f) }) {
			continue
		}
		id := ""
		for _, o := range open {
			if !claimed[o.ID] && same(o, f) {
				id = o.ID
				break
			}
		}
		if id == "" {
			if !final && f.ReactionEndMs > judgedBy {
				continue // not judged yet: the early tier shows it
			}
			serial++
			id = fmt.Sprintf("b%04d", serial)
		}
		claimed[id] = true
		c := f
		c.ID, c.Tier, c.ReplacedBy = id, domain.TierJudged, ""
		c.Rank = 0 // a rank among one read's candidates says nothing across the broadcast
		if final || f.ReactionEndMs <= settledBy {
			c.Tier = domain.TierFinal
		}
		c.State = domain.CandidateConfirmed
		if c.PartialContext {
			c.State = domain.CandidatePartial
		}
		kept = append(kept, c)
		out = append(out, c)
	}
	standing := append(slices.Clone(settled), kept...)
	replacement := func(c domain.Candidate) string {
		for _, k := range standing {
			if same(k, c) {
				return k.ID
			}
		}
		return ""
	}
	for _, o := range open {
		if claimed[o.ID] || dropped(o) {
			continue
		}
		o.State, o.ReplacedBy = domain.CandidateDropped, replacement(o)
		out = append(out, o)
	}
	for _, e := range early {
		forming := e.State != domain.CandidateConfirmed && e.State != domain.CandidatePartial
		if forming && !final {
			continue // its range still grows; it is matched once it stops
		}
		if id := replacement(e); id != "" {
			e.State, e.ReplacedBy = domain.CandidateDropped, id
			out = append(out, e)
		} else if final || e.ReactionEndMs <= judgedBy {
			e.State, e.ReplacedBy = domain.CandidateDropped, ""
			out = append(out, e)
		}
	}
	return out
}

func dropped(c domain.Candidate) bool { return c.State == domain.CandidateDropped }

func rank(b bool) int {
	if b {
		return 1
	}
	return 0
}

// judgedNumber reads a judged candidate's number ("b0012" is 12).
func judgedNumber(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, "b")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}
