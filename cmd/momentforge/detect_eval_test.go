package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/detect/rules"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// detectEvalLabels holds moments people (or AI editor-judges, as drafts)
// picked in a session: e2e/detect-eval/labels/<session id>.json. They are
// derived from a real broadcast and stay out of Git.
const detectEvalLabels = "../../e2e/detect-eval/labels"

type labelFile struct {
	SessionID string        `json:"session_id"`
	Source    string        `json:"source"` // people | ai-judges
	Moments   []labelMoment `json:"moments"`
}

type labelMoment struct {
	ReactionMs int64   `json:"reaction_ms"` // where chat reacts most
	EventMs    int64   `json:"event_ms"`    // what it reacts to
	Judges     int     `json:"judges"`      // how many of the judges picked it
	Strength   float64 `json:"strength"`    // mean strength 0–3 over all judges
	Why        string  `json:"why"`
}

// TestDetectEvalReport compares the whole-broadcast analysis with the
// incremental engine on the finished VODs of a real data folder (it reads a
// copy of the database and never writes to the folder):
//
//	MF_DETECT_EVAL_DATA=<data folder> [MF_DETECT_EVAL_SESSION=<id>] [MF_DETECT_EVAL_LIST=1] go test ./cmd/momentforge -run TestDetectEvalReport -v
//
// For each session it reports the candidates per half hour, how many
// candidates survive the same chat from a smaller, larger, fading or
// alternating audience (authors kept or copied by a hash), how many appear
// in chat shuffled within 10-minute blocks (no real reactions), and, with a
// label file, how many candidates hit a picked moment against random spans.
func TestDetectEvalReport(t *testing.T) {
	dir := os.Getenv("MF_DETECT_EVAL_DATA")
	if dir == "" {
		t.Skip("MF_DETECT_EVAL_DATA is not set")
	}
	ctx := context.Background()
	// Work on a copy so an older data folder can be migrated without
	// touching it.
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, "data"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(filepath.Join(dir, "data", "momentforge.db"+suffix))
		if os.IsNotExist(err) && suffix != "" {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, "data", "momentforge.db"+suffix), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.Open(ctx, tmp, "eval")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	sessions, err := st.Sessions(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	only := os.Getenv("MF_DETECT_EVAL_SESSION")
	var report strings.Builder
	for _, s := range sessions {
		if (only != "" && s.ID != only) || (only == "" && (s.Kind != "vod" || s.State != store.StateFinished)) {
			continue
		}
		events, err := st.Chats(ctx, s.ID, store.AllSeq, false)
		if err != nil {
			t.Fatal(err)
		}
		gaps, err := st.Gaps(ctx, s.ID)
		if err != nil {
			t.Fatal(err)
		}
		evalSession(t, &report, s, events, gaps)
	}
	t.Log("\n" + report.String())
}

type evalMoment struct {
	start, end, peak int64
	score            float64
	strength         string
}

func evalRun(t *testing.T, events []domain.ChatEvent, endMs int64, gaps []domain.Gap, whole bool) []evalMoment {
	t.Helper()
	analyze := detect.Analyze
	if whole {
		analyze = detect.AnalyzeBroadcast
	}
	res, err := analyze(context.Background(), events, endMs, gaps, detect.DefaultParams(), rules.All(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]evalMoment, 0, len(res.Candidates))
	for _, c := range res.Candidates {
		out = append(out, evalMoment{c.ReactionStartMs, c.ReactionEndMs, c.PeakMs, c.Score, c.Strength})
	}
	return out
}

func evalHash(s string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(s))
	return h.Sum32()
}

func evalSession(t *testing.T, w *strings.Builder, s store.Session, events []domain.ChatEvent, gaps []domain.Gap) {
	end := s.DurationMs
	fmt.Fprintf(w, "## %s (%s, %d chats, %s)\n\n", s.Title, s.ID, len(events), evalClock(end))
	keep := func(f func(e domain.ChatEvent) bool) []domain.ChatEvent {
		var out []domain.ChatEvent
		for _, e := range events {
			if f(e) {
				out = append(out, e)
			}
		}
		return out
	}
	share := func(e domain.ChatEvent) float64 { return float64(evalHash(e.AuthorKey) % 100) }
	variants := []struct {
		name   string
		events []domain.ChatEvent
	}{
		{"원본", events},
		{"시청자 50%", keep(func(e domain.ChatEvent) bool { return share(e) < 50 })},
		{"시청자 30%", keep(func(e domain.ChatEvent) bool { return share(e) < 30 })},
		{"3배", evalReplicate(events, 3)},
		{"10배", evalReplicate(events, 10)},
		{"새벽형 100%→25%", keep(func(e domain.ChatEvent) bool { return share(e) < 100*(1-0.75*float64(e.OffsetMs)/float64(end)) })},
		{"30분마다 100%·30%", keep(func(e domain.ChatEvent) bool { return (e.OffsetMs/1_800_000)%2 == 0 || share(e) < 30 })},
	}
	var base, baseOld []evalMoment
	fmt.Fprintln(w, "| 채팅 | 새 방식 후보 (강·보통) | 원본 후보 유지 | 강·보통 유지 | 30분별 | 기존 방식 후보 | 기존 유지 |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|")
	for _, v := range variants {
		whole, old := evalRun(t, v.events, end, gaps, true), evalRun(t, v.events, end, gaps, false)
		if base == nil {
			base, baseOld = whole, old
		}
		top := slices.DeleteFunc(slices.Clone(base), func(m evalMoment) bool { return m.strength == domain.StrengthWeak })
		fmt.Fprintf(w, "| %s | %d (%d) | %.0f%% | %.0f%% | %s | %d | %.0f%% |\n", v.name, len(whole), evalStrong(whole),
			100*evalKept(base, whole), 100*evalKept(top, whole), evalPerHalfHour(whole, end), len(old), 100*evalKept(baseOld, old))
	}
	for seed := range 3 {
		shuffled := evalShuffle(events, 600_000, end, uint32(seed))
		whole, old := evalRun(t, shuffled, end, gaps, true), evalRun(t, shuffled, end, gaps, false)
		fmt.Fprintf(w, "| 10분 안에서 섞은 채팅 %d | %d (%d) | | | | %d | |\n", seed+1, len(whole), evalStrong(whole), len(old))
	}
	fmt.Fprintln(w)

	if labels, ok := evalLoadLabels(t, s.ID); ok {
		fmt.Fprintf(w, "고른 순간 %d개 (%s)\n\n", len(labels.Moments), labels.Source)
		fmt.Fprintln(w, "| 방식 | 후보 | 좋은 순간과 일치 (1.5+) | 2+ | 최상급(2.5+) 찾음 | 2+ 찾음 |")
		fmt.Fprintln(w, "|---|---|---|---|---|---|")
		for _, row := range []struct {
			name string
			ms   []evalMoment
		}{
			{"새 방식", base},
			{"새 방식 강·보통", slices.DeleteFunc(slices.Clone(base), func(m evalMoment) bool { return m.strength == domain.StrengthWeak })},
			{"기존 방식", baseOld},
			{"무작위 구간", evalRandom(base, end)},
		} {
			fmt.Fprintf(w, "| %s | %d | %s |\n", row.name, len(row.ms), evalLabelScore(row.ms, labels.Moments))
		}
		fmt.Fprintln(w)
	}
	if os.Getenv("MF_DETECT_EVAL_LIST") != "" {
		for _, m := range base {
			fmt.Fprintf(w, "- %s–%s %s %.2f\n", evalClock(m.start), evalClock(m.end), m.strength, m.score)
		}
		fmt.Fprintln(w)
	}
}

func evalReplicate(events []domain.ChatEvent, k int) []domain.ChatEvent {
	var out []domain.ChatEvent
	for _, e := range events {
		for j := range k {
			c := e
			c.ID, c.AuthorKey = fmt.Sprintf("%s#%d", e.ID, j), fmt.Sprintf("%s#%d", e.AuthorKey, j)
			if j > 0 { // each copy within ±1.5 s, not drifting later copy by copy
				c.OffsetMs = max(0, c.OffsetMs+int64(evalHash(c.ID)%3001)-1500)
			}
			c.Seq = e.Seq*int64(k) + int64(j)
			out = append(out, c)
		}
	}
	evalSort(out)
	return out
}

func evalShuffle(events []domain.ChatEvent, blockMs, endMs int64, seed uint32) []domain.ChatEvent {
	out := slices.Clone(events)
	for i := range out {
		from := out[i].OffsetMs / blockMs * blockMs
		out[i].OffsetMs = from + int64(evalHash(fmt.Sprintf("%s/%d", out[i].ID, seed)))%min(blockMs, endMs-from)
	}
	evalSort(out)
	return out
}

func evalSort(events []domain.ChatEvent) {
	slices.SortStableFunc(events, func(a, b domain.ChatEvent) int {
		return cmp.Or(cmp.Compare(a.OffsetMs, b.OffsetMs), cmp.Compare(a.Seq, b.Seq))
	})
}

// evalKept is the share of ref moments that overlap one of ms (±15 s).
func evalKept(ref, ms []evalMoment) float64 {
	if len(ref) == 0 {
		return 1
	}
	n := 0
	for _, r := range ref {
		if slices.ContainsFunc(ms, func(m evalMoment) bool { return m.start-15000 <= r.end && r.start <= m.end+15000 }) {
			n++
		}
	}
	return float64(n) / float64(len(ref))
}

func evalStrong(ms []evalMoment) int {
	n := 0
	for _, m := range ms {
		if m.strength == domain.StrengthStrong || m.strength == domain.StrengthMedium {
			n++
		}
	}
	return n
}

func evalPerHalfHour(ms []evalMoment, end int64) string {
	var parts []string
	for s := int64(0); s < end; s += 1_800_000 {
		parts = append(parts, fmt.Sprint(len(slices.DeleteFunc(slices.Clone(ms), func(m evalMoment) bool { return m.peak < s || m.peak >= s+1_800_000 }))))
	}
	return strings.Join(parts, " ")
}

// evalRandom spreads as many spans of the same lengths evenly with jitter.
func evalRandom(ms []evalMoment, end int64) []evalMoment {
	out := make([]evalMoment, len(ms))
	for i, m := range ms {
		at := int64(i)*end/int64(max(1, len(ms))) + int64(evalHash(fmt.Sprint(i))%120_000)
		out[i] = evalMoment{start: at, end: at + m.end - m.start, peak: at}
	}
	return out
}

func evalLoadLabels(t *testing.T, sid string) (labelFile, bool) {
	b, err := os.ReadFile(filepath.Join(detectEvalLabels, sid+".json"))
	if os.IsNotExist(err) {
		return labelFile{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	var lf labelFile
	if err := json.Unmarshal(b, &lf); err != nil {
		t.Fatalf("%s: %v", sid, err)
	}
	return lf, true
}

// evalLabelScore: a candidate hits a moment when the moment's reaction is
// within its span ±15 s; moments count when two or more judges picked them.
func evalLabelScore(ms []evalMoment, labels []labelMoment) string {
	covers := func(m evalMoment, l labelMoment) bool {
		return l.ReactionMs >= m.start-15000 && l.ReactionMs <= m.end+15000
	}
	tier := func(min float64) []labelMoment {
		return slices.DeleteFunc(slices.Clone(labels), func(l labelMoment) bool { return l.Judges < 2 || l.Strength < min })
	}
	hit := func(m evalMoment, min float64) bool {
		return slices.ContainsFunc(tier(min), func(l labelMoment) bool { return covers(m, l) })
	}
	found := func(min float64) string {
		g := tier(min)
		n := 0
		for _, l := range g {
			if slices.ContainsFunc(ms, func(m evalMoment) bool { return covers(m, l) }) {
				n++
			}
		}
		return fmt.Sprintf("%d/%d", n, len(g))
	}
	good, better := 0, 0
	for _, m := range ms {
		if hit(m, 1.5) {
			good++
		}
		if hit(m, 2) {
			better++
		}
	}
	pct := func(n int) string { return fmt.Sprintf("%.0f%%", 100*float64(n)/float64(max(1, len(ms)))) }
	return fmt.Sprintf("%s | %s | %s | %s", pct(good), pct(better), found(2.5), found(2))
}

func evalClock(ms int64) string {
	s := ms / 1000
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s%3600/60, s%60)
}
