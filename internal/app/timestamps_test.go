package app

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// TestParseTimestampsMatchesLegacy compares with CzzEdit 0.3.2's parser on
// synthetic comments (testdata/gen_comment_parse.py).
func TestParseTimestampsMatchesLegacy(t *testing.T) {
	var data struct {
		DurationMs int64 `json:"durationMs"`
		Cases      []struct {
			Text  string `json:"text"`
			Found []struct {
				StartMs     int64  `json:"start_ms"`
				EndMs       *int64 `json:"end_ms"`
				Description string `json:"description"`
				Offset      int    `json:"offset"`
			} `json:"found"`
			Issues []struct {
				Offset int    `json:"offset"`
				Reason string `json:"reason"`
			} `json:"issues"`
		} `json:"cases"`
	}
	b, err := os.ReadFile("testdata/comment_parse.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	for _, c := range data.Cases {
		found, issues := ParseTimestamps(c.Text, data.DurationMs)
		var want, got []string
		for _, f := range c.Found {
			end := int64(0)
			if f.EndMs != nil {
				end = *f.EndMs
			}
			want = append(want, fmt.Sprintf("%d-%d@%d %q", f.StartMs, end, f.Offset, f.Description))
		}
		for _, i := range c.Issues {
			want = append(want, fmt.Sprintf("issue %s@%d", i.Reason, i.Offset))
		}
		for _, f := range found {
			got = append(got, fmt.Sprintf("%d-%d@%d %q", f.StartMs, f.EndMs, f.Offset, f.Description))
		}
		for _, i := range issues {
			got = append(got, fmt.Sprintf("issue %s@%d", i.Reason, i.Offset))
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("%q:\n got %v\nwant %v", c.Text, got, want)
		}
	}
}
