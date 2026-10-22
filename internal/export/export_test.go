package export

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

func TestWriteAnalysisMarkersHoldTheClipRange(t *testing.T) {
	var events []domain.ChatEvent
	for i := 0; i < 400; i++ { // two messages every 250 ms, in time order
		events = append(events, domain.ChatEvent{ID: fmt.Sprintf("e%03d", i), Seq: int64(i + 1), OffsetMs: int64(i/2) * 250,
			AuthorKey: "a", Text: "ㅋㅋ"})
	}
	cands := []domain.Candidate{
		{ID: "m0001", ClipStartMs: 0, ClipEndMs: 1000, EvidenceIDs: []string{"e002"}},
		{ID: "m0002", ClipStartMs: 10_000, ClipEndMs: 12_500, EvidenceIDs: []string{"e080", "e099"}}, // ends on a message's time
		{ID: "m0003", ClipStartMs: 49_000, ClipEndMs: 60_000},                                        // runs past the chat
		{ID: "m0004", ClipStartMs: 70_000, ClipEndMs: 80_000},                                        // after it
		{ID: "m0005", ClipStartMs: 5_000, ClipEndMs: 5_000},                                          // empty
	}
	dir := t.TempDir()
	if err := WriteAnalysis(dir, AnalysisManifest{AnalysisID: "a1", DurationMs: 60_000}, "방송", cands, nil, events); err != nil {
		t.Fatal(err)
	}
	for _, c := range cands {
		want := "# * = 판정 근거에 포함된 채팅. 나머지는 앞뒤 문맥입니다.\n"
		for _, e := range events {
			if e.OffsetMs >= c.ClipStartMs && e.OffsetMs < c.ClipEndMs {
				mark := "  "
				for _, id := range c.EvidenceIDs {
					if id == e.ID {
						mark = "* "
					}
				}
				want += mark + ChatLine(e)
			}
		}
		got, err := os.ReadFile(filepath.Join(dir, "markers", c.ID+".txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s markers:\n%s\nwant:\n%s", c.ID, got, want)
		}
		if c.ID == "m0002" && strings.Count(string(got), "\n* ") != 2 {
			t.Errorf("m0002 evidence marks: %s", got)
		}
	}
}
