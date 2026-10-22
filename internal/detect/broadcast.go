package detect

import (
	"context"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Built-in measures of the whole-broadcast analysis. Message rules add one
// measure each (their ID); the signal keys match the incremental engine's.
const (
	keyReaction     = "reaction"     // distinct authors with a shared reaction key
	keySurge        = "surge"        // messages (per-author cap applied)
	keyParticipants = "participants" // distinct authors
)

// builtinNames name the built-in signals when no registered rule does.
// "reaction" is a signal only when no single reaction key had MinReactors
// authors (several kinds of reaction at once).
var builtinNames = map[string]string{keySurge: "채팅 급증", keyParticipants: "참여자 급증", keyReaction: "여러 반응"}

// AnalyzeBroadcast analyzes a finished timeline as a whole (REDESIGN 6.2.2).
// Unlike Analyze, which only looks back like LIVE must, every window is
// compared with the minutes before and after it, so the start of a broadcast
// needs no warm-up and a slowly changing audience is followed in both
// directions. Each 10 s window gets a score: how unusual its reactions (per
// message rule and in total), messages and participants are for that time
// of the broadcast. Candidates are the stretches around score peaks, ranked
// by their peak and labeled strong, medium or weak. p.Broadcast must be set.
func AnalyzeBroadcast(ctx context.Context, events []domain.ChatEvent, endMs int64, gaps []domain.Gap,
	p Params, rules []Rule, progress func(Progress)) (*Result, error) {
	if p.Broadcast == nil {
		return nil, fmt.Errorf("detect: AnalyzeBroadcast needs Params.Broadcast")
	}
	e, err := NewEngine(p, rules)
	if err != nil {
		return nil, err
	}
	for _, r := range e.rules.message {
		if builtinNames[r.ID()] != "" {
			return nil, fmt.Errorf("detect: message rule id %q is a built-in measure", r.ID())
		}
	}
	inRange := make([]domain.ChatEvent, 0, len(events))
	for _, ev := range events {
		if ev.OffsetMs >= 0 && ev.OffsetMs < endMs {
			inRange = append(inRange, ev)
		}
	}
	e.Add(inRange)
	for _, g := range gaps {
		e.Gap(g.StartMs, g.EndMs)
	}
	b := &broadcast{e: e, p: p, bp: *p.Broadcast, endMs: endMs}
	b.measures()

	res := &Result{Candidates: []domain.Candidate{}, DurationMs: endMs, RulesVersion: RulesVersion, Params: p}
	times := windowTimes(p, endMs)
	b.wins = make([]bwindow, len(times))
	res.Windows = make([]domain.Window, len(times))
	for i, t := range times {
		if i%100 == 0 {
			if ctx.Err() != nil {
				return nil, domain.WrapUserError("CANCELLED", "분석을 중단했습니다. 채팅 원본은 저장되어 있습니다.", ctx.Err())
			}
			if progress != nil {
				progress(Progress{PositionMs: t, DurationMs: endMs})
			}
		}
		b.wins[i] = b.window(t)
		res.Windows[i] = b.wins[i].w
	}
	res.Candidates = b.candidates()
	if progress != nil && len(times) > 0 {
		progress(Progress{PositionMs: endMs, DurationMs: endMs, Candidates: len(res.Candidates)})
	}
	return res, nil
}

// windowTimes lists the evaluation times of [0, endMs): every StepMs from
// WindowMs, plus endMs when it falls between steps (as Analyze).
func windowTimes(p Params, endMs int64) []int64 {
	var times []int64
	for t := p.WindowMs; t <= endMs; t += p.StepMs {
		times = append(times, t)
	}
	if endMs >= p.WindowMs && (len(times) == 0 || times[len(times)-1] != endMs) {
		times = append(times, endMs)
	}
	return times
}

type broadcast struct {
	e     *Engine
	p     Params
	bp    BroadcastParams
	endMs int64

	keys    []string       // measure keys: reaction, then one per message rule, then surge, participants
	index   map[string]int // measure key → position in keys
	bins    []bcounts      // aligned 10 s bins over the whole timeline
	bins2   []bcounts      // aligned bins of twice the window (LongWindow)
	wins    []bwindow      // evaluated windows in time order
	scratch [2][]float64
	disp    []float64 // per measure: the broadcast's own burstiness, as negative binomial 1/k (0: Poisson)

	// Reused by counting-only stats (bins and scoring): a long VOD has
	// tens of thousands of them, and the Repeat rule gives nearly every
	// message its own key.
	perAuthor map[string]int
	seen      map[[2]string]bool
	keysOf    map[string][]string
	counts    map[string]int
}

// bcounts are the measures of one span of chat.
type bcounts struct {
	valid bool
	x     []int // per measure key
}

// bwindow is one evaluated window. Evaluation keeps only the numbers; the
// candidate builder counts its stretch's windows again with full detail.
type bwindow struct {
	w     domain.Window
	span  int64   // length of the counted window (WindowMs, or twice it)
	short float64 // score of the WindowMs window alone
	score float64
	ready bool
	x     []int
	usual []float64
	z     []float64

	// Filled by stats; only full stats keep evidence, times and ids, and
	// counting-only stats' maps are valid until the next stats call.
	top      map[string]string   // measure → its most shared key (message rules)
	counts   map[string]int      // key → distinct authors
	evidence map[string][]string // key → counted event IDs
	ids      []string
	times    map[string][]int64 // key → offsets of counted reactions (peak time)
}

// measures fixes the measure order and computes the aligned bins.
func (b *broadcast) measures() {
	b.keys = []string{keyReaction}
	for _, r := range b.e.rules.message {
		b.keys = append(b.keys, r.ID())
	}
	b.keys = append(b.keys, keySurge, keyParticipants)
	b.index = make(map[string]int, len(b.keys))
	for i, k := range b.keys {
		b.index[k] = i
	}
	w := b.p.WindowMs
	b.bins = b.alignedBins(w)
	if b.bp.LongWindow {
		b.bins2 = b.alignedBins(2 * w)
	}
	// How bursty each measure is over the whole broadcast. Rare reactions
	// (a few "와" a minute) come in clumps; a baseline of nearly empty bins
	// alone would read any clump as extraordinary.
	b.disp = make([]float64, len(b.keys))
	if b.bp.BroadcastDispersion {
		for m := range b.keys {
			b.disp[m] = b.dispersion(m)
		}
	}
}

// dispersion estimates how bursty measure m is in this broadcast, as the
// negative binomial 1/k of variance = mean + mean²/k. Each 10 s bin is
// compared with the mean of the valid bins within LocalMs around it (itself
// left out), so a slow rise or fall of the audience is not taken as
// burstiness. A broadcast whose laughs all come in big bursts would learn
// from them that bursts are normal; MaxDispersion bounds that.
func (b *broadcast) dispersion(m int) float64 {
	radius := int(b.bp.LocalMs / b.p.WindowMs)
	sum := make([]float64, len(b.bins)+1) // prefix sums over valid bins
	cnt := make([]int, len(b.bins)+1)
	for i, bin := range b.bins {
		sum[i+1], cnt[i+1] = sum[i], cnt[i]
		if bin.valid {
			sum[i+1] += float64(bin.x[m])
			cnt[i+1]++
		}
	}
	type dev struct{ mu, d float64 }
	var devs []dev
	for i, bin := range b.bins {
		lo, hi := max(0, i-radius), min(len(b.bins), i+radius+1)
		n := cnt[hi] - cnt[lo] - 1
		if !bin.valid || n <= 0 {
			continue
		}
		x := float64(bin.x[m])
		mu := (sum[hi] - sum[lo] - x) / float64(n)
		devs = append(devs, dev{mu, x - mu})
	}
	var sumMu, sumMu2, sumSq float64
	for _, v := range devs {
		sumMu += v.mu
		sumMu2 += v.mu * v.mu
		sumSq += v.d * v.d
	}
	if sumMu2 == 0 || sumSq <= sumMu {
		return 0
	}
	return min((sumSq-sumMu)/sumMu2, b.bp.MaxDispersion)
}

// alignedBins counts the timeline in consecutive spans of length w.
func (b *broadcast) alignedBins(w int64) []bcounts {
	bins := make([]bcounts, (b.endMs+w-1)/w)
	for i := range bins {
		s := b.stats(int64(i)*w, min(int64(i+1)*w, b.endMs), false)
		// A partial last bin would read as quiet; leave it out of baselines.
		bins[i] = bcounts{valid: s.w.Valid && int64(i+1)*w <= b.endMs, x: s.x}
	}
	return bins
}

// stats counts [start, end). Messages from unknown authors are left out
// (they cannot be capped or told apart) without invalidating the window;
// gaps invalidate it. Only full stats collect evidence, times and ids, in
// maps of their own.
func (b *broadcast) stats(start, end int64, full bool) bwindow {
	e := b.e
	var s bwindow
	s.w.Valid = true
	for _, g := range e.gaps {
		if start < g.EndMs && end > g.StartMs {
			s.w.Valid = false
			break
		}
	}
	perAuthor, seen, keysOf := b.perAuthor, b.seen, b.keysOf
	if full || perAuthor == nil {
		perAuthor, seen, keysOf, s.counts = map[string]int{}, map[[2]string]bool{}, map[string][]string{}, map[string]int{}
		if !full {
			b.perAuthor, b.seen, b.keysOf, b.counts = perAuthor, seen, keysOf, s.counts
		}
	} else {
		clear(perAuthor)
		clear(seen)
		clear(keysOf)
		clear(b.counts)
		s.counts = b.counts
	}
	if full {
		s.evidence, s.times = map[string][]string{}, map[string][]int64{}
	}
	for _, en := range e.events[e.search(start):e.search(end)] {
		author := en.ev.AuthorKey
		if en.skip || author == "" {
			continue
		}
		perAuthor[author]++
		if perAuthor[author] > b.p.PerAuthorCap {
			continue
		}
		if full {
			s.ids = append(s.ids, en.ev.ID)
		}
		for _, k := range en.keys {
			if pair := [2]string{k, author}; !seen[pair] {
				seen[pair] = true
				s.counts[k]++
				keysOf[author] = append(keysOf[author], k)
			}
			if full {
				s.evidence[k] = append(s.evidence[k], en.ev.ID)
				s.times[k] = append(s.times[k], en.ev.OffsetMs)
			}
		}
	}
	s.top = map[string]string{}
	s.x = make([]int, len(b.keys))
	for k, c := range s.counts {
		id, _, _ := strings.Cut(k, ":")
		i, ok := b.index[id]
		if !ok {
			continue
		}
		if c > s.x[i] || (c == s.x[i] && k < s.top[id]) {
			s.x[i], s.top[id] = c, k
		}
	}
	// A reactor shares at least one reaction key with another author.
	for _, keys := range keysOf {
		for _, k := range keys {
			if s.counts[k] >= 2 {
				s.x[b.index[keyReaction]]++
				break
			}
		}
	}
	n := 0
	for _, c := range perAuthor {
		n += min(c, b.p.PerAuthorCap)
	}
	s.x[b.index[keySurge]] = n
	s.x[b.index[keyParticipants]] = len(perAuthor)
	s.w.N, s.w.U = n, len(perAuthor)
	return s
}

// window evaluates the window ending at t: [t-WindowMs, t), and with
// LongWindow also [t-2·WindowMs, t), keeping the higher score (a reaction
// that builds over 20 s). The window's counts stay those of WindowMs.
func (b *broadcast) window(t int64) bwindow {
	s := b.evaluate(t, b.p.WindowMs, b.bins)
	if b.bp.LongWindow && t >= 2*b.p.WindowMs {
		if long := b.evaluate(t, 2*b.p.WindowMs, b.bins2); long.score > s.score && s.w.Valid {
			// The timeline draws 10 s counts; the score, signals and their
			// shares of authors stay those of the 20 s window.
			long.w.N, long.w.U, long.w.BaselineN, long.w.Ratio, long.w.Z = s.w.N, s.w.U, s.w.BaselineN, s.w.Ratio, s.w.Z
			long.short = s.score
			return long
		}
	}
	s.short = s.score
	return s
}

// evaluate judges [t-span, t) against bins of the same span around it.
func (b *broadcast) evaluate(t, span int64, bins []bcounts) (s bwindow) {
	p, bp := b.p, b.bp
	s = b.stats(t-span, t, false)
	defer func() { s.top, s.counts = nil, nil }() // scratch, valid until the next stats call
	s.span = span
	s.w.T = t
	s.w.Signals = []string{}
	s.usual = make([]float64, len(b.keys))
	s.z = make([]float64, len(b.keys))

	// Reference bins of each scale, leaving the window and ExcludeMs on
	// each side out. A scale needs MinRefBins valid bins.
	var levels [2][]level
	for scale, reach := range []int64{bp.LocalMs, bp.TrendMs} {
		lo := max(0, (t-span-reach)/span)
		hi := min(int64(len(bins)), ceilDiv(t+reach, span))
		exFrom, exTo := t-span-bp.ExcludeMs, t+bp.ExcludeMs
		levels[scale] = make([]level, len(b.keys))
		for m := range b.keys {
			vals := b.scratch[0][:0]
			for i := lo; i < hi; i++ {
				bs, be := i*span, (i+1)*span
				if !bins[i].valid || (be > exFrom && bs < exTo) {
					continue
				}
				vals = append(vals, float64(bins[i].x[m]))
			}
			b.scratch[0] = vals
			levels[scale][m], b.scratch[1] = levelOf(vals, bp.WinsorQuantile, b.scratch[1])
		}
	}
	s.ready = false
	for m, key := range b.keys {
		var best *level
		for scale := range levels {
			l := &levels[scale][m]
			if int64(l.Bins)*span >= int64(bp.MinRefBins)*p.WindowMs && (best == nil || l.Mean < best.Mean) {
				best = l
			}
		}
		if best == nil {
			continue
		}
		s.ready = true
		s.usual[m] = best.Mean
		if !isVolume(key) && s.x[m] < bp.MinReactors {
			continue
		}
		mean := math.Max(best.Mean, bp.CountFloor)
		s.z[m] = surprise(s.x[m], best.Mean, math.Max(best.Var, mean+mean*mean*b.disp[m]), bp.CountFloor)
	}
	s.w.BaselineReady = s.ready
	if !s.ready || !s.w.Valid {
		return s
	}
	var used []int
	s.score, used = b.combine(s.z)
	s.w.Score = ptr(round2(min(s.score, maxScore)))
	surge := b.index[keySurge]
	s.w.BaselineN = ptr(round2(s.usual[surge]))
	s.w.Ratio = ptr(round2(float64(s.x[surge]) / math.Max(s.usual[surge], bp.CountFloor)))
	s.w.Z = ptr(round2(s.z[surge]))

	// Signals: the measures a candidate-level score rests on.
	if s.score >= bp.onZ(p.Sensitive) {
		for _, m := range used {
			for _, sig := range b.signalKeys(&s, b.keys[m]) {
				if !slices.Contains(s.w.Signals, sig) {
					s.w.Signals = append(s.w.Signals, sig)
				}
			}
		}
	}
	sort.Strings(s.w.Signals)
	for _, k := range s.w.Signals {
		if c, ok := s.counts[k]; ok && s.w.U > 0 {
			if s.w.ReactionRatio == nil {
				s.w.ReactionRatio = map[string]float64{}
			}
			s.w.ReactionRatio[k] = float64(c) / float64(s.w.U)
		}
	}
	return s
}

func isVolume(key string) bool { return key == keySurge || key == keyParticipants }

// combine turns the measures' z-scores into the window's score and reports
// the measures it rests on. The score is the strongest reaction measure, a
// chat or participant surge at VolumeWeight, or — when a reaction and a
// surge come together — their sum scaled for their correlation (more
// authors bring more reactions), whichever is highest. Besides the best, a
// measure counts when its own (weighted) z is within 1 of it and at least
// OffZ.
func (b *broadcast) combine(z []float64) (float64, []int) {
	bp := b.bp
	bestR, bestV := -1, -1
	for m, key := range b.keys {
		if z[m] <= 0 {
			continue
		}
		if isVolume(key) {
			if bestV < 0 || z[m] > z[bestV] {
				bestV = m
			}
		} else if bestR < 0 || z[m] > z[bestR] {
			bestR = m
		}
	}
	single := 0.0
	if bestR >= 0 {
		single = z[bestR]
	}
	if bestV >= 0 {
		single = max(single, bp.VolumeWeight*z[bestV])
	}
	if bestR >= 0 && bestV >= 0 && bp.ComboCorr > 0 {
		if combo := (z[bestR] + z[bestV]) / math.Sqrt(2+2*bp.ComboCorr); combo > single {
			return combo, []int{bestR, bestV}
		}
	}
	var used []int
	for m, key := range b.keys {
		w := 1.0
		if isVolume(key) {
			w = bp.VolumeWeight
		}
		if z[m] > 0 && z[m]*w >= min(single, max(bp.OffZ, single-1)) {
			used = append(used, m)
		}
	}
	return single, used
}

// signalKeys are the rule keys a measure stands for in a window: a message
// rule's most shared key, the reaction total's most shared key of any rule
// (or "reaction" itself when none has MinReactors authors), or the measure
// itself for chat and participants.
func (b *broadcast) signalKeys(s *bwindow, measure string) []string {
	switch measure {
	case keySurge, keyParticipants:
		return []string{measure}
	case keyReaction:
		best, bestN := "", 0
		for k, c := range s.counts {
			if c < 2 || b.e.rules.ruleOf(k) == nil {
				continue
			}
			if c > bestN || (c == bestN && k < best) {
				best, bestN = k, c
			}
		}
		if bestN < b.bp.MinReactors {
			return []string{keyReaction} // several kinds, none shared by enough authors alone
		}
		return []string{best}
	}
	if k := s.top[measure]; k != "" {
		return []string{k}
	}
	return nil
}

// region is a stretch of consecutive windows around one or more peaks.
type region struct{ from, to int } // window indexes, inclusive

// candidates finds score peaks, grows each into the stretch where the score
// stays at or above OffZ, merges stretches closer than MergeGapMs, splits
// ones longer than MaxSpanMs at their deepest valley, and ranks them.
func (b *broadcast) candidates() []domain.Candidate {
	p, bp := b.p, b.bp
	on := bp.onZ(p.Sensitive)
	above := func(i int) bool { return b.wins[i].w.Valid && b.wins[i].ready && b.wins[i].score >= bp.OffZ }
	var regions []region
	for i := 0; i < len(b.wins); i++ {
		if !above(i) || b.wins[i].score < on {
			continue
		}
		r := region{i, i}
		for r.from > 0 && above(r.from-1) {
			r.from--
		}
		for r.to+1 < len(b.wins) && above(r.to+1) {
			r.to++
		}
		if n := len(regions); n > 0 && b.wins[r.from].w.T-p.WindowMs-b.wins[regions[n-1].to].w.T <= p.MergeGapMs &&
			b.validBetween(regions[n-1].to, r.from) {
			regions[n-1].to = r.to
		} else {
			regions = append(regions, r)
		}
		i = r.to
	}
	out := make([]domain.Candidate, 0, len(regions))
	for _, r := range regions {
		group := fmt.Sprintf("g%d", b.extent(r).from)
		for _, part := range b.split(r, on) {
			notBefore := int64(0) // a part starts where the previous one ended
			if n := len(out); n > 0 {
				notBefore = out[n-1].ReactionEndMs
			}
			if c := b.candidate(part, notBefore); c != nil {
				c.GroupID = group // parts of one long reaction share it
				out = append(out, *c)
			}
		}
	}
	// IDs follow time; ranks follow the peak score.
	for i := range out {
		out[i].ID = fmt.Sprintf("m%04d", i+1)
	}
	order := make([]int, len(out))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return out[order[i]].Score > out[order[j]].Score })
	within := func(rank int, share float64) bool { return float64(rank) <= math.Ceil(share*float64(len(out))) }
	for r, i := range order {
		rank := r + 1
		out[i].Rank = rank
		switch c := &out[i]; {
		case c.Score >= bp.StrongZ && within(rank, bp.StrongShare):
			c.Strength = domain.StrengthStrong
		case c.Score >= bp.MediumZ && within(rank, bp.MediumShare):
			c.Strength = domain.StrengthMedium
		default:
			c.Strength = domain.StrengthWeak
		}
	}
	return out
}

// validBetween reports whether every window between two indexes is valid
// (a gap never joins two stretches).
func (b *broadcast) validBetween(from, to int) bool {
	for i := from; i <= to; i++ {
		if !b.wins[i].w.Valid {
			return false
		}
	}
	return true
}

// split cuts a stretch longer than MaxSpanMs. It cuts at its clearest
// valley — the window whose score lies furthest (at least ValleyDepth) below
// the lower of the highest peaks on either side, each reaching on — so two
// reactions in a row become two candidates; a plateau of steady reaction
// has no such valley and is cut every MaxSpanMs like the incremental engine.
// Parts without a window reaching on (the fading rest of a long stretch
// after its peak) are dropped: a candidate always has a peak.
func (b *broadcast) split(r region, on float64) []region {
	p := b.p
	if !b.peaks(r, on) {
		return nil
	}
	if e := b.extent(r); e.to-e.from <= p.MaxSpanMs {
		return []region{r}
	}
	n := r.to - r.from + 1
	left, right := make([]float64, n), make([]float64, n) // highest score before / after each window
	for i := 1; i < n; i++ {
		left[i] = max(left[i-1], b.wins[r.from+i-1].score)
	}
	for i := n - 2; i >= 0; i-- {
		right[i] = max(right[i+1], b.wins[r.from+i+1].score)
	}
	cut, depth := -1, b.bp.ValleyDepth
	for i := 1; i < n-1; i++ {
		if left[i] < on || right[i] < on {
			continue
		}
		if d := min(left[i], right[i]) - b.wins[r.from+i].score; d >= depth {
			cut, depth = r.from+i, d
		}
	}
	if cut < 0 {
		// Grow the first part while its extent (which a 20 s-only window
		// can start before r.from's own) stays within MaxSpanMs.
		end := r.from
		for end < r.to {
			if e := b.extent(region{r.from, end + 1}); e.to-e.from > p.MaxSpanMs {
				break
			}
			end++
		}
		if end >= r.to {
			return []region{r}
		}
		return append(b.split(region{r.from, end}, on), b.split(region{end + 1, r.to}, on)...)
	}
	// The valley window belongs to neither side.
	return append(b.split(region{r.from, cut - 1}, on), b.split(region{cut + 1, r.to}, on)...)
}

func (b *broadcast) peaks(r region, on float64) bool {
	for i := r.from; i <= r.to; i++ {
		if b.wins[i].score >= on {
			return true
		}
	}
	return false
}

// extent is the time a stretch's reactions cover (see reach).
func (b *broadcast) extent(r region) (e struct{ from, to int64 }) {
	e.from = math.MaxInt64
	for i := r.from; i <= r.to; i++ {
		from, to := b.reach(i)
		e.from, e.to = min(e.from, from), max(e.to, to)
	}
	return e
}

// reach is where the reaction a window shows lies: its 10 s, or for a
// window raised only by its 20 s count, the earlier half of those 20 s
// (it stays raised for 10 s after a reaction ends).
func (b *broadcast) reach(i int) (from, to int64) {
	w := &b.wins[i]
	if w.span > b.p.WindowMs && w.short < b.bp.OffZ {
		return w.w.T - w.span, w.w.T - b.p.WindowMs
	}
	return w.w.T - b.p.WindowMs, w.w.T
}

// candidate builds the candidate of one stretch, starting no earlier than
// notBefore, counting its windows again with full detail for evidence.
func (b *broadcast) candidate(r region, notBefore int64) *domain.Candidate {
	p := b.p
	e := b.extent(r)
	start, end := max(0, e.from, notBefore), min(e.to, b.endMs)
	if end <= start {
		return nil
	}
	peak := r.from
	for i := r.from; i <= r.to; i++ {
		if b.wins[i].score > b.wins[peak].score {
			peak = i
		}
	}
	detail := make([]bwindow, r.to-r.from+1)
	for i := range detail {
		w := &b.wins[r.from+i]
		detail[i] = b.stats(w.w.T-w.span, w.w.T, true)
	}
	pw := &b.wins[peak]
	pd := &detail[peak-r.from]

	// Signals, reasons and evidence come from the windows that reached the
	// candidate threshold: each signal's messages, or every counted message
	// of the window for a chat or participant surge (as the incremental
	// engine does).
	keys := map[string]bool{}
	evidence := map[string]position{}
	add := func(id string) {
		if pos, ok := b.e.position(id); ok && pos.offset >= start && pos.offset < end {
			evidence[id] = pos
		}
	}
	for i := r.from; i <= r.to; i++ {
		w, d := &b.wins[i], &detail[i-r.from]
		for _, k := range w.w.Signals {
			keys[k] = true
			switch {
			case isVolume(k):
				for _, id := range d.ids {
					add(id)
				}
			case k == keyReaction: // every reaction shared by two or more authors
				for key, c := range d.counts {
					if c >= 2 && b.e.rules.ruleOf(key) != nil {
						for _, id := range d.evidence[key] {
							add(id)
						}
					}
				}
			default:
				for _, id := range d.evidence[k] {
					add(id)
				}
			}
		}
	}
	signals := sortedKeys(keys)
	reasonSet := map[string]bool{}
	for _, k := range signals {
		if r := b.e.rules.ruleOf(k); r != nil {
			reasonSet[r.Name()] = true
		} else if name := builtinNames[k]; name != "" {
			reasonSet[name] = true
		}
	}
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, c := evidence[ids[i]], evidence[ids[j]]
		if a.offset != c.offset {
			return a.offset < c.offset
		}
		return a.seq < c.seq
	})

	// Peak time: the middle of the peak window's reactions, else its middle.
	var at []int64
	for _, k := range pw.w.Signals {
		at = append(at, pd.times[k]...)
	}
	peakMs := pw.w.T - pw.span/2
	if len(at) > 0 {
		slices.Sort(at)
		peakMs = at[len(at)/2]
	}
	peakMs = min(end-1, max(start, peakMs))

	var measures []domain.Measure
	for m, key := range b.keys {
		if pw.z[m] < 2 {
			continue
		}
		mk := key
		if !isVolume(key) && key != keyReaction {
			if sig := b.signalKeys(pd, key); len(sig) > 0 {
				mk = sig[0]
			}
		}
		measures = append(measures, domain.Measure{Key: mk, Observed: pw.x[m], Usual: round2(pw.usual[m]),
			Z: round2(pw.z[m]), WindowMs: pw.span})
	}
	sort.SliceStable(measures, func(i, j int) bool { return measures[i].Z > measures[j].Z })

	clipStart := max(0, start-p.ReactionDelayMs-p.PreRollMs)
	clipEnd := max(clipStart, min(b.endMs, end-p.ReactionDelayMs+p.PostRollMs))
	partial := end+p.PostRollMs > b.endMs || start-p.ReactionDelayMs-p.PreRollMs < 0
	for _, g := range b.e.gaps {
		partial = partial || (g.StartMs < clipEnd && g.EndMs > clipStart) // chat around it is missing
	}
	return &domain.Candidate{
		ReactionStartMs: start,
		ReactionEndMs:   end,
		PeakMs:          peakMs,
		ClipStartMs:     clipStart,
		ClipEndMs:       clipEnd,
		Signals:         signals,
		Reasons:         sortedKeys(reasonSet),
		Peak: domain.PeakStats{
			N: pw.w.N, U: pw.w.U, Ratio: pw.w.Ratio, Z: pw.w.Z, BaselineN: pw.w.BaselineN, ReactionRatio: pw.w.ReactionRatio,
		},
		BaselineReady:  true,
		EvidenceIDs:    ids,
		PartialContext: partial,
		Score:          round2(min(pw.score, maxScore)),
		Measures:       measures,
	}
}

// maxScore caps reported scores; beyond it every chat agrees anyway.
const maxScore = 99

func round2(v float64) float64 { return math.Round(v*100) / 100 }
