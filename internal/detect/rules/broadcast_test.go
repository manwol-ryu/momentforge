package rules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

func analyzeBroadcast(t *testing.T, events []domain.ChatEvent, endMs int64, gaps []domain.Gap, p detect.Params) *detect.Result {
	t.Helper()
	res, err := detect.AnalyzeBroadcast(context.Background(), events, endMs, gaps, p, All(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// covering returns the candidate whose reaction overlaps [from, to).
func covering(cs []domain.Candidate, from, to int64) *domain.Candidate {
	for i := range cs {
		if cs[i].ReactionStartMs < to && cs[i].ReactionEndMs > from {
			return &cs[i]
		}
	}
	return nil
}

// A quiet channel's laugh is found with its measures, and nothing else is.
func TestBroadcastFindsSmallChannelLaugh(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	res := analyzeBroadcast(t, smallChannel(end, laughAt), end, nil, detect.DefaultParams())
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %v, want the laugh only", starts(res.Candidates))
	}
	c := res.Candidates[0]
	if c.ReactionStartMs > laughAt || c.ReactionEndMs < laughAt+10000 || !slices.Contains(c.Signals, "laugh") ||
		c.Rank != 1 || c.Strength == "" || c.Score < detect.DefaultBroadcastParams().OnZ {
		t.Fatalf("candidate %+v", c)
	}
	if c.PeakMs < laughAt || c.PeakMs >= laughAt+20000 {
		t.Fatalf("peak %d, the laugh is at %d–%d", c.PeakMs, laughAt, laughAt+16300)
	}
	if len(c.Measures) == 0 || c.Measures[0].Observed < 3 || c.Measures[0].Usual >= float64(c.Measures[0].Observed) {
		t.Fatalf("measures %+v", c.Measures)
	}
	for _, w := range res.Windows {
		if w.Score == nil || !w.BaselineReady {
			t.Fatalf("t=%d: a whole-broadcast window is judged from the start", w.T)
		}
	}
}

// The minutes after a window count too, so a reaction one minute in is
// found; the incremental engine is still learning then.
func TestBroadcastNeedsNoWarmUp(t *testing.T) {
	const end, laughAt = 15 * 60000, 60000
	events := smallChannel(end, laughAt)
	if c := covering(analyze(t, events, end, detect.DefaultParams()).Candidates, laughAt, laughAt+20000); c != nil {
		t.Fatalf("the incremental engine judged during its warm-up: %+v", c)
	}
	if c := covering(analyzeBroadcast(t, events, end, nil, detect.DefaultParams()).Candidates, laughAt, laughAt+20000); c == nil {
		t.Fatal("the early laugh was missed")
	}
}

// The same behavior from ten times the audience is the same moment.
func TestBroadcastIsScaleFree(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	small := smallChannel(end, laughAt)
	p := detect.DefaultParams()
	a := analyzeBroadcast(t, small, end, nil, p).Candidates
	b := analyzeBroadcast(t, replicate(small, 10), end, nil, p).Candidates
	if len(a) != 1 || covering(b, laughAt, laughAt+20000) == nil {
		t.Fatalf("small room %v, ten times larger %v", starts(a), starts(b))
	}
}

// When the chat drops to a fifth late at night, a laugh that is big for the
// hour still stands out.
func TestBroadcastFollowsLateNight(t *testing.T) {
	events, laughAt := lateNight()
	res := analyzeBroadcast(t, events, 3*60*60000, nil, detect.DefaultParams())
	if covering(res.Candidates, laughAt, laughAt+20000) == nil {
		t.Fatalf("missed the late-night laugh; candidates %v", starts(res.Candidates))
	}
}

// Busy rooms with planted reactions: every reaction is found once with its
// reason, the spammer and the reaction inside a gap are not, a stretch never
// crosses a gap, and the 220 s laugh is cut into parts of at most 180 s.
func TestBroadcastBusyChannels(t *testing.T) {
	type want struct {
		at     int64
		reason string
	}
	cases := map[string][]want{
		"demo_default":  {{300000, "웃음 증가"}, {370000, "집단 반복"}, {450000, "감정 반응"}},
		"mixed_default": {{360000, "웃음 증가"}, {500000, "집단 반복"}, {720000, "감정 반응"}, {900000, "웃음 증가"}, {1200000, "감정 반응"}},
	}
	for name, wants := range cases {
		t.Run(name, func(t *testing.T) {
			var c legacyCase
			readJSON(t, name+".case.json", &c)
			var gaps []domain.Gap
			for _, g := range c.Gaps {
				gaps = append(gaps, domain.Gap{StartMs: g[0], EndMs: g[1]})
			}
			p := detect.DefaultParams()
			res := analyzeBroadcast(t, loadEvents(t, name), c.EndMs, gaps, p)
			for _, w := range wants {
				got := covering(res.Candidates, w.at, w.at+20000)
				if got == nil || !slices.Contains(got.Reasons, w.reason) {
					t.Fatalf("reaction at %d: %+v (all %v)", w.at, got, starts(res.Candidates))
				}
			}
			for i, cand := range res.Candidates {
				if cand.ReactionEndMs-cand.ReactionStartMs > p.MaxSpanMs {
					t.Errorf("%s spans %d ms", cand.ID, cand.ReactionEndMs-cand.ReactionStartMs)
				}
				if len(cand.Signals) == 0 || len(cand.Reasons) == 0 || len(cand.EvidenceIDs) == 0 || cand.Score < p.Broadcast.OnZ {
					t.Errorf("%s has no peak: score %.2f, signals %v, %d evidence", cand.ID, cand.Score, cand.Signals, len(cand.EvidenceIDs))
				}
				if i > 0 && cand.ReactionStartMs < res.Candidates[i-1].ReactionEndMs {
					t.Errorf("%s starts at %d inside the previous one (to %d)", cand.ID, cand.ReactionStartMs, res.Candidates[i-1].ReactionEndMs)
				}
				if cand.PeakMs < cand.ReactionStartMs || cand.PeakMs >= cand.ReactionEndMs {
					t.Errorf("%s peak %d outside %d–%d", cand.ID, cand.PeakMs, cand.ReactionStartMs, cand.ReactionEndMs)
				}
				for _, g := range gaps {
					if cand.ReactionStartMs < g.EndMs && cand.ReactionEndMs > g.StartMs {
						t.Errorf("%s crosses the gap %v", cand.ID, g)
					}
				}
			}
			if name == "demo_default" {
				if len(res.Candidates) != 3 || covering(res.Candidates, 520000, 521000) != nil {
					t.Fatalf("demo: %v (the spammer at 520000 is not a reaction)", starts(res.Candidates))
				}
			}
			if name == "mixed_default" {
				long := 0
				for _, cand := range res.Candidates {
					if cand.ReactionStartMs >= 890000 && cand.ReactionStartMs < 1120000 {
						long++
					}
				}
				if long < 2 {
					t.Fatalf("the 220 s laugh should be cut in two: %v", starts(res.Candidates))
				}
				groups := map[string]int{}
				for _, cand := range res.Candidates {
					if cand.ReactionStartMs >= 890000 && cand.ReactionStartMs < 1120000 {
						groups[cand.GroupID]++
					}
				}
				if len(groups) != 1 {
					t.Fatalf("parts of the long laugh share one group: %v", groups)
				}
			}
		})
	}
}

// IDs follow time, ranks follow the score, and strength follows rank.
func TestBroadcastRanksAndStrength(t *testing.T) {
	var c legacyCase
	readJSON(t, "mixed_default.case.json", &c)
	bp := detect.DefaultBroadcastParams()
	res := analyzeBroadcast(t, loadEvents(t, "mixed_default"), c.EndMs, nil, detect.DefaultParams())
	n := len(res.Candidates)
	ranks := map[int]bool{}
	for i, cand := range res.Candidates {
		if cand.ID != fmt.Sprintf("m%04d", i+1) || (i > 0 && cand.ReactionStartMs < res.Candidates[i-1].ReactionStartMs) {
			t.Fatalf("candidate %d: %s at %d", i, cand.ID, cand.ReactionStartMs)
		}
		ranks[cand.Rank] = true
		for _, other := range res.Candidates {
			if other.Score > cand.Score && other.Rank > cand.Rank {
				t.Fatalf("%s (%.2f) ranks %d above %s (%.2f, rank %d)", cand.ID, cand.Score, cand.Rank, other.ID, other.Score, other.Rank)
			}
		}
		switch cand.Strength {
		case domain.StrengthStrong:
			if cand.Score < bp.StrongZ || float64(cand.Rank) > bp.StrongShare*float64(n)+1 {
				t.Errorf("%s strong at %.2f, rank %d of %d", cand.ID, cand.Score, cand.Rank, n)
			}
		case domain.StrengthMedium:
			if cand.Score < bp.MediumZ || float64(cand.Rank) > bp.MediumShare*float64(n)+1 {
				t.Errorf("%s medium at %.2f, rank %d of %d", cand.ID, cand.Score, cand.Rank, n)
			}
		case domain.StrengthWeak:
		default:
			t.Errorf("%s strength %q", cand.ID, cand.Strength)
		}
	}
	if len(ranks) != n || n == 0 {
		t.Fatalf("ranks %v for %d candidates", ranks, n)
	}
}

// Hidden messages and notices are not reactions; a message without an
// author is left out without making its window unusable.
func TestBroadcastCountsViewerMessagesOnly(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	events := smallChannel(end, laughAt)
	var noisy []domain.ChatEvent
	for _, e := range events {
		noisy = append(noisy, e)
		if e.OffsetMs >= laughAt && e.OffsetMs < laughAt+20000 {
			noisy = append(noisy, domain.ChatEvent{ID: e.ID + "-anon", Seq: e.Seq, OffsetMs: e.OffsetMs, Text: "ㅋㅋㅋ", Kind: domain.ChatKindDonation})
		}
	}
	for i := range 40 { // a bot-hidden flood and subscription notices at 5:00
		kind, hidden := domain.ChatKindChat, true
		if i%2 == 1 {
			kind, hidden = domain.ChatKindSubscription, false
		}
		noisy = append(noisy, domain.ChatEvent{ID: fmt.Sprintf("x%02d", i), OffsetMs: 300000 + int64(i)*200,
			AuthorKey: fmt.Sprintf("b%02d", i), Text: "ㅋㅋㅋㅋ", Kind: kind, Hidden: hidden})
	}
	slices.SortStableFunc(noisy, func(a, b domain.ChatEvent) int { return int(a.OffsetMs - b.OffsetMs) })
	res := analyzeBroadcast(t, noisy, end, nil, detect.DefaultParams())
	if len(res.Candidates) != 1 || covering(res.Candidates, laughAt, laughAt+20000) == nil {
		t.Fatalf("candidates %v, want only the laugh at %d", starts(res.Candidates), laughAt)
	}
	for _, w := range res.Windows {
		if !w.Valid {
			t.Fatalf("t=%d invalid: an unknown author must not void a window", w.T)
		}
	}
}

// Chat missing right after a reaction makes its clip context partial.
func TestBroadcastMarksGapsInTheClip(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	gaps := []domain.Gap{{StartMs: laughAt + 40000, EndMs: laughAt + 90000}}
	res := analyzeBroadcast(t, smallChannel(end, laughAt), end, gaps, detect.DefaultParams())
	c := covering(res.Candidates, laughAt, laughAt+20000)
	if c == nil || !c.PartialContext {
		t.Fatalf("candidate %+v: the gap inside its clip must mark the context partial", c)
	}
}

func TestBroadcastStopsWhenCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := detect.AnalyzeBroadcast(ctx, smallChannel(15*60000, 600000), 15*60000, nil, detect.DefaultParams(), All(), nil)
	var ue *domain.UserError
	if !errors.As(err, &ue) || ue.Code != "CANCELLED" {
		t.Fatalf("err = %v", err)
	}
	if _, err := detect.AnalyzeBroadcast(context.Background(), nil, 60000, nil, detect.CzzEditParams(), All(), nil); err == nil {
		t.Fatal("CzzEdit params have no whole-broadcast settings")
	}
}
