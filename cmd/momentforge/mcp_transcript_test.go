package main

import (
	"context"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/detect/rules"
	"github.com/manwol-ryu/momentforge/internal/domain"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// buildTranscriptData writes the sample (with its planted utterances) and a
// session without a transcript.
func buildTranscriptData(t *testing.T, dir string) (demo, bare string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dir, "eval")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	core := app.New(app.Config{Version: "eval", DataDir: dir, Store: st, Params: detect.DefaultParams(), Rules: rules.All(),
		Platforms: registerPlatforms(nil), AI: &ai.Registry{}})
	defer core.Shutdown(ctx)
	d, err := core.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	s, err := st.CreateSession(ctx, store.NewSession{Platform: "chzzk", Kind: "vod", TargetID: "1", Title: "대본 없는 다시보기",
		DurationMs: 60_000, Salt: []byte("0123456789abcdef0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID, s.ID
}

type utterancesResult struct {
	Transcript struct {
		ID             string `json:"transcript_id"`
		Source         string `json:"source"`
		UtteranceCount int    `json:"utterance_count"`
		Coverage       []struct {
			EndMs int64 `json:"end_ms"`
		} `json:"coverage"`
	} `json:"transcript"`
	Total      int    `json:"total"`
	HasMore    bool   `json:"has_more"`
	NextCursor string `json:"next_cursor"`
	Utterances []struct {
		Ref     string `json:"ref"`
		StartMs int64  `json:"start_ms"`
		Start   string `json:"start"`
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	} `json:"utterances"`
}

func TestMCPTranscriptTools(t *testing.T) {
	dir := t.TempDir()
	sid, bare := buildTranscriptData(t, dir)
	cs := mcpClient(t, dir)
	planted := domain.DemoUtterances()

	// The promise said at 3:40 is found by a word in it, with the ref the cut analysis cites.
	var found utterancesResult
	text, isErr := call(t, cs, "mf_search_utterances", map[string]any{"session_id": sid, "query": "벌칙"}, &found)
	if isErr || found.Total < 1 || found.Utterances[0].StartMs != 220_000 || found.Utterances[0].Start != "0:03:40" ||
		!strings.HasPrefix(found.Utterances[0].Ref, "utt:"+found.Transcript.ID+"-") || found.Transcript.Source != "demo" {
		t.Fatalf("search %+v %q", found, text)
	}
	if !strings.Contains(text, "0:03:40") || !strings.Contains(text, "벌칙") || !strings.Contains(text, found.Utterances[0].Ref) {
		t.Fatalf("search text %q", text)
	}
	// start/end narrow the search; nothing matches before the promise.
	call(t, cs, "mf_search_utterances", map[string]any{"session_id": sid, "query": "벌칙", "end": "3:00"}, &found)
	if found.Total != 0 || len(found.Utterances) != 0 {
		t.Fatalf("narrowed search %+v", found)
	}

	// The transcript pages through every utterance in time order.
	var page utterancesResult
	call(t, cs, "mf_get_transcript", map[string]any{"session_id": sid, "limit": 5}, &page)
	if page.Total != len(planted) || len(page.Utterances) != 5 || !page.HasMore || page.NextCursor != "5" ||
		page.Transcript.UtteranceCount != len(planted) || len(page.Transcript.Coverage) != 1 {
		t.Fatalf("first page %+v", page)
	}
	seen := len(page.Utterances)
	last := page.Utterances[len(page.Utterances)-1].StartMs
	for page.HasMore {
		cursor := page.NextCursor
		page = utterancesResult{}
		call(t, cs, "mf_get_transcript", map[string]any{"session_id": sid, "limit": 500, "cursor": cursor}, &page)
		for _, u := range page.Utterances {
			if u.StartMs < last {
				t.Fatalf("out of order at %d", u.StartMs)
			}
			last = u.StartMs
		}
		seen += len(page.Utterances)
	}
	if seen != len(planted) {
		t.Fatalf("paged %d of %d utterances", seen, len(planted))
	}
	// A range returns the utterances starting in it: the failure at 4:50~5:00.
	call(t, cs, "mf_get_transcript", map[string]any{"session_id": sid, "start": "4:50", "end": "5:00"}, &page)
	if len(page.Utterances) == 0 || page.Utterances[0].StartMs < 290_000 || page.Utterances[len(page.Utterances)-1].StartMs >= 300_000 {
		t.Fatalf("range %+v", page)
	}

	// Errors say what to do next.
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"mf_search_utterances", map[string]any{"session_id": bare, "query": "벌칙"}, "mf_search_chats"},
		{"mf_get_transcript", map[string]any{"session_id": bare}, "대본 탭"},
		{"mf_get_transcript", map[string]any{"session_id": "nope"}, "mf_list_sessions"},
		{"mf_search_utterances", map[string]any{"session_id": sid, "query": " "}, "1~100자"},
		{"mf_get_transcript", map[string]any{"session_id": sid, "limit": 501}, "1~500"},
		{"mf_get_transcript", map[string]any{"session_id": sid, "start": "5:00", "end": "4:00"}, "end는 start보다"},
	} {
		text, isErr := call(t, cs, c.tool, c.args, nil)
		if !isErr || !strings.Contains(text, c.want) {
			t.Errorf("%s %v: %v %q", c.tool, c.args, isErr, text)
		}
	}
}
