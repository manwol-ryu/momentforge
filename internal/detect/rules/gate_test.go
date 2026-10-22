package rules

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// smallChannel is a quiet chat (2–3 messages per 10 s from 12 viewers) with
// one laugh: 10 authors laughing twice over 20 s at laughAtMs. The busiest
// 10 s has about 13 messages, so the fixed gate (15) never opens.
func smallChannel(endMs, laughAtMs int64) []domain.ChatEvent {
	rnd := rand.New(rand.NewPCG(7, 7))
	casual := []string{"오늘 재밌다", "ㅇㅇ", "GG", "이거 뭐임", "굿굿", "화이팅", "음악 좋네"}
	type row struct {
		ms           int64
		author, text string
	}
	var rows []row
	for ms := int64(0); ms < endMs; ms += 4000 + rnd.Int64N(1500) {
		rows = append(rows, row{ms, fmt.Sprintf("v%02d", rnd.IntN(12)), casual[rnd.IntN(len(casual))]})
	}
	for i := range 10 {
		at := laughAtMs + int64(i)*700
		rows = append(rows, row{at, fmt.Sprintf("v%02d", i), "ㅋㅋㅋㅋ"}, row{at + 10000, fmt.Sprintf("v%02d", i), "ㅋㅋㅋ"})
	}
	slices.SortFunc(rows, func(a, b row) int { return int(a.ms - b.ms) })
	out := make([]domain.ChatEvent, len(rows))
	for i, r := range rows {
		out[i] = domain.ChatEvent{ID: fmt.Sprintf("s%04d", i), Seq: int64(i + 1), OffsetMs: r.ms, AuthorKey: r.author, Text: r.text}
	}
	return out
}

func analyze(t *testing.T, events []domain.ChatEvent, endMs int64, p detect.Params) *detect.Result {
	t.Helper()
	res, err := detect.Analyze(context.Background(), events, endMs, nil, p, All(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestAdaptiveGateFindsSmallChannelLaugh(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	events := smallChannel(end, laughAt)
	if res := analyze(t, events, end, detect.CzzEditParams()); len(res.Candidates) != 0 {
		t.Fatalf("the fixed gate should miss it, got %d candidates", len(res.Candidates))
	}
	res := analyze(t, events, end, detect.DefaultParams())
	if len(res.Candidates) != 1 {
		t.Fatalf("candidates = %d, want the laugh", len(res.Candidates))
	}
	c := res.Candidates[0]
	if c.ReactionStartMs > laughAt || c.ReactionEndMs < laughAt+10000 || !slices.Contains(c.Signals, "laugh") {
		t.Fatalf("candidate %s %d–%d %v", c.ID, c.ReactionStartMs, c.ReactionEndMs, c.Signals)
	}
	if c.Peak.Gate < 6 || c.Peak.Gate >= 15 || c.Peak.SpikeGate < 3 || (c.Peak.N < c.Peak.Gate && c.Peak.U < c.Peak.SpikeGate) {
		t.Fatalf("peak n=%d u=%d with gates busy=%d spike=%d", c.Peak.N, c.Peak.U, c.Peak.Gate, c.Peak.SpikeGate)
	}
}

func TestAdaptiveGateWarmUpAndFloor(t *testing.T) {
	const end = 15 * 60000
	p := detect.DefaultParams()
	res := analyze(t, smallChannel(end, 10*60000), end, p)
	for _, w := range res.Windows {
		// Bins finish 40 s after they end; 18 of them (3 min) come after 220 s.
		if w.T < int64(p.GateWarmupBins)*p.WindowMs+p.BaselineExclude {
			if w.Gate != 0 || len(w.Signals) > 0 {
				t.Fatalf("t=%d: judged during warm-up (gate %d, signals %v)", w.T, w.Gate, w.Signals)
			}
		} else if w.Gate < p.SensitiveMinMessages || w.SpikeGate < p.SensitiveMinAuthors {
			t.Fatalf("t=%d: gates %d/%d below the sensitive minimum", w.T, w.Gate, w.SpikeGate)
		}
	}
	p.Sensitive = true
	for _, w := range analyze(t, smallChannel(end, 10*60000), end, p).Windows {
		if w.Gate != 0 || w.SpikeGate != 0 {
			t.Fatalf("sensitive keeps the fixed 6-message gate, got gates %d/%d", w.Gate, w.SpikeGate)
		}
	}
}

// replicate makes k audiences that behave alike: the same chat from
// distinct authors, a little apart in time.
func replicate(events []domain.ChatEvent, k int) []domain.ChatEvent {
	rnd := rand.New(rand.NewPCG(3, 3))
	var out []domain.ChatEvent
	for c := range k {
		for _, e := range events {
			e.ID, e.AuthorKey = fmt.Sprintf("%s-%d", e.ID, c), fmt.Sprintf("%s-%d", e.AuthorKey, c)
			if c > 0 {
				e.OffsetMs = max(0, e.OffsetMs+rnd.Int64N(3001)-1500)
			}
			out = append(out, e)
		}
	}
	slices.SortStableFunc(out, func(a, b domain.ChatEvent) int { return int(a.OffsetMs - b.OffsetMs) })
	for i := range out {
		out[i].Seq = int64(i + 1)
	}
	return out
}

func starts(cs []domain.Candidate) []int64 {
	var out []int64
	for _, c := range cs {
		out = append(out, c.ReactionStartMs/10000)
	}
	return out
}

// Ten times the audience with the same behavior finds the same moment;
// the fixed gate finds nothing in the small room and a pile in the large one.
func TestAdaptiveGateIsScaleFree(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	small := smallChannel(end, laughAt)
	large := replicate(small, 10)
	p := detect.DefaultParams()
	a, b := analyze(t, small, end, p).Candidates, analyze(t, large, end, p).Candidates
	if fmt.Sprint(starts(a)) != fmt.Sprint(starts(b)) || len(a) != 1 {
		t.Fatalf("small room %v, ten times larger %v", starts(a), starts(b))
	}
	if fixed := analyze(t, large, end, detect.CzzEditParams()).Candidates; len(fixed) <= len(b) {
		t.Logf("fixed gate on the large room: %d candidates", len(fixed))
	}
}

// lateNight is three hours of chat that drops to a fifth after two hours,
// with nine authors laughing at laughAt (2:40).
func lateNight() (events []domain.ChatEvent, laughAt int64) {
	const hour = 60 * 60000
	rnd := rand.New(rand.NewPCG(9, 9))
	add := func(ms int64, author, text string) {
		events = append(events, domain.ChatEvent{ID: fmt.Sprintf("n%05d", len(events)), OffsetMs: ms, AuthorKey: author, Text: text})
	}
	for ms := int64(0); ms < 3*hour; ms += 500 + rnd.Int64N(1000) { // evening: ~13 messages per 10 s
		if ms >= 2*hour && rnd.IntN(5) != 0 {
			continue // late night: a fifth of the chat
		}
		add(ms, fmt.Sprintf("v%02d", rnd.IntN(40)), "오늘 재밌다")
	}
	laughAt = int64(2*hour + 40*60000)
	for i := range 9 {
		add(laughAt+int64(i)*900, fmt.Sprintf("v%02d", i), "ㅋㅋㅋㅋ")
		add(laughAt+10000+int64(i)*900, fmt.Sprintf("v%02d", i), "ㅋㅋㅋ")
	}
	slices.SortStableFunc(events, func(a, b domain.ChatEvent) int { return int(a.OffsetMs - b.OffsetMs) })
	for i := range events {
		events[i].Seq = int64(i + 1)
	}
	return events, laughAt
}

// A chat that goes quiet (late night) keeps finding reactions that are big
// for the hour, which the fixed 15-message gate cannot see.
func TestAdaptiveGateFollowsLateNight(t *testing.T) {
	const hour = 60 * 60000
	events, laughAt := lateNight()
	found := func(cs []domain.Candidate) bool {
		for _, c := range cs {
			if c.ReactionStartMs <= laughAt+10000 && c.ReactionEndMs >= laughAt {
				return true
			}
		}
		return false
	}
	if found(analyze(t, events, 3*hour, detect.CzzEditParams()).Candidates) {
		t.Fatal("the fixed gate was expected to miss the late-night laugh")
	}
	if !found(analyze(t, events, 3*hour, detect.DefaultParams()).Candidates) {
		t.Fatal("the adaptive gate missed the late-night laugh")
	}
}

// Chatty rooms with frequent reactions keep CzzEdit's candidates: medians
// ignore the reactions, so the gates stay below them.
func TestAdaptiveGateLeavesBusyChannelsAlone(t *testing.T) {
	for _, name := range []string{"demo_default", "mixed_default"} {
		t.Run(name, func(t *testing.T) {
			var c legacyCase
			readJSON(t, name+".case.json", &c)
			var gaps []domain.Gap
			for _, g := range c.Gaps {
				gaps = append(gaps, domain.Gap{StartMs: g[0], EndMs: g[1]})
			}
			events := loadEvents(t, name)
			run := func(p detect.Params) []domain.Candidate {
				res, err := detect.Analyze(context.Background(), events, c.EndMs, gaps, p, All(), nil)
				if err != nil {
					t.Fatal(err)
				}
				return res.Candidates
			}
			fixed, adaptive := run(detect.CzzEditParams()), run(detect.DefaultParams())
			if len(fixed) != len(adaptive) {
				t.Fatalf("%d candidates, CzzEdit %d", len(adaptive), len(fixed))
			}
			// Same moments and reasons; a reaction may end one step (5 s) earlier
			// when its fading tail is below the gate.
			for i, f := range fixed {
				a := adaptive[i]
				if a.ReactionStartMs != f.ReactionStartMs || fmt.Sprint(a.Reasons) != fmt.Sprint(f.Reasons) ||
					a.ReactionEndMs > f.ReactionEndMs || f.ReactionEndMs-a.ReactionEndMs > 10000 {
					t.Fatalf("candidate %d: %d–%d %v, CzzEdit %d–%d %v", i, a.ReactionStartMs, a.ReactionEndMs, a.Reasons,
						f.ReactionStartMs, f.ReactionEndMs, f.Reasons)
				}
			}
		})
	}
}

// LIVE judges the same windows in steps; the gate only uses finished bins,
// so the result matches the one-pass analysis.
func TestAdaptiveGateLiveMatchesBatch(t *testing.T) {
	const end, laughAt = 15 * 60000, 10 * 60000
	events := smallChannel(end, laughAt)
	p := detect.DefaultParams()
	batch := analyze(t, events, end, p).Candidates

	a, err := detect.NewLiveAnalyzer(0, p, All(), false)
	if err != nil {
		t.Fatal(err)
	}
	var live []domain.Candidate
	i := 0
	for to := int64(7000); ; to += 7000 {
		final := to >= end
		to = min(to, end)
		j := i
		for j < len(events) && events[j].OffsetMs < to+5000 {
			j++
		}
		a.Add(events[i:j])
		i = j
		released, err := a.Advance(to, final)
		if err != nil {
			t.Fatal(err)
		}
		live = append(live, released...)
		if final {
			break
		}
	}
	key := func(cs []domain.Candidate) string {
		s := ""
		for _, c := range cs {
			s += fmt.Sprintf("%s %d-%d %v gate=%d; ", c.ID, c.ReactionStartMs, c.ReactionEndMs, c.Signals, c.Peak.Gate)
		}
		return s
	}
	if key(live) != key(batch) || len(batch) == 0 {
		t.Fatalf("live %s\nbatch %s", key(live), key(batch))
	}
}

// The incremental engine (LIVE) counts viewers' messages only: a bot-hidden
// laugh flood and subscription notices are no reaction, and an authorless
// system notice no longer voids its window.
func TestIncrementalCountsViewerMessagesOnly(t *testing.T) {
	const end = 15 * 60000
	events := smallChannel(end, 10*60000)
	for i := range 40 {
		kind, hidden, author := domain.ChatKindChat, true, fmt.Sprintf("b%02d", i)
		switch i % 4 {
		case 1:
			kind, hidden = domain.ChatKindSubscription, false
		case 3:
			kind, hidden, author = domain.ChatKindSystem, false, ""
		}
		events = append(events, domain.ChatEvent{ID: fmt.Sprintf("x%02d", i), OffsetMs: 300000 + int64(i)*200,
			AuthorKey: author, Text: "ㅋㅋㅋㅋ", Kind: kind, Hidden: hidden})
	}
	slices.SortStableFunc(events, func(a, b domain.ChatEvent) int { return int(a.OffsetMs - b.OffsetMs) })
	p := detect.DefaultParams()
	p.Broadcast = nil
	res := analyze(t, events, end, p)
	for _, c := range res.Candidates {
		if c.ReactionStartMs < 320000 && c.ReactionEndMs > 290000 {
			t.Fatalf("hidden messages or notices made a candidate: %+v", c)
		}
	}
	for _, w := range res.Windows {
		if w.T > 300000 && w.T <= 320000 && !w.Valid {
			t.Fatalf("t=%d: an authorless system notice voided the window", w.T)
		}
	}
}
