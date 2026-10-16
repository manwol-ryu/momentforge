package rules

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Cases are produced from CzzEdit 0.3.2 by testdata/legacy/gen_cases.py.
var legacyCases = []string{"demo_default", "demo_sensitive", "mixed_default", "mixed_tuned"}

type legacyEvent struct {
	ID        string  `json:"id"`
	Seq       int64   `json:"seq"`
	OffsetMs  int64   `json:"offsetMs"`
	AuthorKey *string `json:"authorKey"`
	Text      string  `json:"text"`
}

type legacyCase struct {
	EndMs  int64      `json:"endMs"`
	Gaps   [][2]int64 `json:"gaps"`
	Config struct {
		Sensitive       bool   `json:"sensitive"`
		ReactionDelayMs int64  `json:"reaction_delay_ms"`
		PreRollMs       *int64 `json:"pre_roll_ms"`
		PostRollMs      *int64 `json:"post_roll_ms"`
	} `json:"config"`
}

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "legacy", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func loadEvents(t *testing.T, name string) []domain.ChatEvent {
	var raw []legacyEvent
	readJSON(t, name+".events.json", &raw)
	out := make([]domain.ChatEvent, len(raw))
	for i, e := range raw {
		out[i] = domain.ChatEvent{ID: e.ID, Seq: e.Seq, OffsetMs: e.OffsetMs, Text: e.Text}
		if e.AuthorKey != nil {
			out[i].AuthorKey = *e.AuthorKey
		}
	}
	return out
}

func TestMatchesLegacy(t *testing.T) {
	for _, name := range legacyCases {
		t.Run(name, func(t *testing.T) {
			var c legacyCase
			readJSON(t, name+".case.json", &c)
			p := detect.CzzEditParams()
			p.Sensitive = c.Config.Sensitive
			p.ReactionDelayMs = c.Config.ReactionDelayMs
			if c.Config.PreRollMs != nil {
				p.PreRollMs = *c.Config.PreRollMs
			}
			if c.Config.PostRollMs != nil {
				p.PostRollMs = *c.Config.PostRollMs
			}
			var gaps []domain.Gap
			for _, g := range c.Gaps {
				gaps = append(gaps, domain.Gap{StartMs: g[0], EndMs: g[1]})
			}

			res, err := detect.Analyze(context.Background(), loadEvents(t, name), c.EndMs, gaps, p, CzzEdit(), nil)
			if err != nil {
				t.Fatal(err)
			}
			got := roundTrip(t, map[string]any{"candidates": res.Candidates, "windows": res.Windows})
			var want any
			readJSON(t, name+".legacy.json", &want)

			diffs := compare("", want, got)
			if len(diffs) > 0 {
				if len(diffs) > 15 {
					diffs = append(diffs[:15], fmt.Sprintf("… and %d more", len(diffs)-15))
				}
				t.Fatalf("differs from CzzEdit 0.3.2 (%d candidates expected):\n  %s",
					len(want.(map[string]any)["candidates"].([]any)), joinLines(diffs))
			}
		})
	}
}

func TestDemoChatMatchesLegacyDemoRows(t *testing.T) {
	want := loadEvents(t, "demo_default")
	got := domain.DemoChat()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("domain.DemoChat differs from legacy demo_rows (%d vs %d events)", len(got), len(want))
	}
}

func roundTrip(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// compare walks two JSON values. Missing keys equal null and empty objects
// (Go omits them, Python writes null / {}); numbers match within 1e-9.
func compare(path string, want, got any) []string {
	switch w := want.(type) {
	case map[string]any:
		g, _ := got.(map[string]any)
		if len(w) == 0 && len(g) == 0 {
			return nil
		}
		keys := map[string]bool{}
		for k := range w {
			keys[k] = true
		}
		for k := range g {
			keys[k] = true
		}
		names := make([]string, 0, len(keys))
		for k := range keys {
			names = append(names, k)
		}
		sort.Strings(names)
		var out []string
		for _, k := range names {
			out = append(out, compare(path+"."+k, w[k], g[k])...)
		}
		return out
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return []string{fmt.Sprintf("%s: length want %d got %v", path, len(w), lenOf(got))}
		}
		var out []string
		for i := range w {
			out = append(out, compare(fmt.Sprintf("%s[%d]", path, i), w[i], g[i])...)
		}
		return out
	case float64:
		g, ok := got.(float64)
		if !ok || math.Abs(w-g) > 1e-9 {
			return []string{fmt.Sprintf("%s: want %v got %v", path, w, got)}
		}
		return nil
	case nil:
		if got == nil {
			return nil
		}
		if m, ok := got.(map[string]any); ok && len(m) == 0 {
			return nil
		}
		return []string{fmt.Sprintf("%s: want null got %v", path, got)}
	default:
		if !reflect.DeepEqual(want, got) {
			return []string{fmt.Sprintf("%s: want %v got %v", path, want, got)}
		}
		return nil
	}
}

func lenOf(v any) any {
	if s, ok := v.([]any); ok {
		return len(s)
	}
	return v
}

func joinLines(s []string) string {
	out := ""
	for i, l := range s {
		if i > 0 {
			out += "\n  "
		}
		out += l
	}
	return out
}
