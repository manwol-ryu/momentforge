package rules

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// Scripts are produced from CzzEdit 0.3.2's LiveAnalyzer by testdata/legacy/gen_live.py.
var liveCases = []string{"live_viewer", "live_sensitive"}

type liveStep struct {
	Op     string               `json:"op"`
	Events []legacyEvent        `json:"events"`
	Sample *detect.ViewerSample `json:"sample"`
	Start  int64                `json:"start"`
	End    int64                `json:"end"`
	Final  bool                 `json:"final"`
	Expect json.RawMessage      `json:"expect"`
}

type liveCase struct {
	StartMs        int64 `json:"startMs"`
	ViewerAdaptive bool  `json:"viewerAdaptive"`
	Config         struct {
		Sensitive bool `json:"sensitive"`
	} `json:"config"`
	Steps []liveStep `json:"steps"`
}

func TestLiveMatchesLegacy(t *testing.T) {
	for _, name := range liveCases {
		t.Run(name, func(t *testing.T) {
			var c liveCase
			readJSON(t, name+".steps.json", &c)
			p := detect.CzzEditParams()
			p.Sensitive = c.Config.Sensitive
			an, err := detect.NewLiveAnalyzer(c.StartMs, p, CzzEdit(), c.ViewerAdaptive)
			if err != nil {
				t.Fatal(err)
			}
			released, previews := 0, 0
			for i, st := range c.Steps {
				var got []domain.Candidate
				switch st.Op {
				case "add":
					evs := make([]domain.ChatEvent, len(st.Events))
					for j, e := range st.Events {
						evs[j] = domain.ChatEvent{ID: e.ID, Seq: e.Seq, OffsetMs: e.OffsetMs, AuthorKey: *e.AuthorKey, Text: e.Text}
					}
					an.Add(evs)
					continue
				case "sample":
					an.Sample(*st.Sample)
					continue
				case "gap":
					an.Gap(st.Start, st.End)
					continue
				case "advance":
					got, err = an.Advance(st.End, st.Final)
					released += len(got)
				case "previews":
					got, err = an.Previews()
					previews += len(got)
				}
				if err != nil {
					t.Fatalf("step %d (%s): %v", i, st.Op, err)
				}
				var want any
				json.Unmarshal(st.Expect, &want)
				gotJSON := roundTrip(t, nonNilCands(got))
				if diffs := compare(fmt.Sprintf("step%d.%s", i, st.Op), want, gotJSON); len(diffs) > 0 {
					if len(diffs) > 10 {
						diffs = diffs[:10]
					}
					t.Fatalf("differs from CzzEdit LiveAnalyzer:\n  %s", joinLines(diffs))
				}
			}
			if released == 0 || previews == 0 {
				t.Fatalf("script exercised nothing (released %d, previews %d)", released, previews)
			}
		})
	}
}

func nonNilCands(c []domain.Candidate) []domain.Candidate {
	if c == nil {
		return []domain.Candidate{}
	}
	return c
}
