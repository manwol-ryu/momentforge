package main

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/detect/rules"
	"github.com/manwol-ryu/momentforge/internal/mcp"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// evalAI is a deterministic AI provider for the MCP evaluation data: it
// recommends the first "헐" of a chunk (high priority) or else its first
// chat (low), and reports a prompt-cache hit on every request.
type evalAI struct{}

func (evalAI) ID() string           { return "evalai" }
func (evalAI) Name() string         { return "평가용 AI" }
func (evalAI) KeyEnv() string       { return "" }
func (evalAI) DefaultModel() string { return "eval-model" }
func (evalAI) MaxOutputTokens() int { return ai.MaxOutputTokens }
func (evalAI) Review(ctx context.Context, key, model string, c ai.Chunk) (json.RawMessage, ai.Usage, error) {
	var pick *ai.Evidence
	for i, e := range c.Evidence {
		if strings.HasPrefix(e.Ref, "chat:") && (pick == nil || (e.Content == "헐" && pick.Content != "헐")) {
			pick = &c.Evidence[i]
		}
	}
	recs := []any{}
	if pick != nil {
		title, priority := "채팅 반응", "low"
		if pick.Content == "헐" {
			title, priority = "놀람 반응", "high"
		}
		recs = append(recs, map[string]any{"source_candidate_ids": []string{}, "start_ms": pick.TimeMs,
			"end_ms": min(pick.TimeMs+15000, c.EndMs), "title": title, "rationale": "채팅이 몰림",
			"evidence_refs": []string{pick.Ref}, "uncertainty": "평가용 응답", "priority": priority})
	}
	out, _ := json.Marshal(map[string]any{"status": "ok", "recommendations": recs})
	return out, ai.Usage{InputTokens: 120, CacheReadTokens: 1000, OutputTokens: 80, TotalTokens: 1200}, nil
}

// evalComments are pasted viewer comments with times.
const evalComments = "5:05 다들 웃음 터진 장면\n7:35 놀란 반응\n2:00 여기 분위기 좋았음"

// buildEvalData writes the MCP evaluation data set into dir: the built-in
// sample analyzed with the default rules, pasted viewer comments, and one
// review by evalAI. Everything is synthetic.
func buildEvalData(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dir, "eval")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	reg := &ai.Registry{}
	reg.Register(evalAI{})
	core := app.New(app.Config{Version: "eval", DataDir: dir, Store: st, Params: detect.DefaultParams(), Rules: rules.All(),
		Platforms: registerPlatforms(nil), AI: reg})
	defer core.Shutdown(ctx)
	demo, err := core.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.StartComments(ctx, app.CommentRequest{SessionID: demo.ID, Text: evalComments}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); core.Job().Busy; time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("comments job did not finish")
		}
	}
	tl, err := core.Timeline(ctx, demo.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range tl.Items {
		ids = append(ids, it.ID)
	}
	core.SetAIKey("evalai", "k")
	pv, err := core.PreviewAI(ctx, app.AIPreviewRequest{SessionID: demo.ID, Provider: "evalai", Model: "eval-model", ItemIDs: ids})
	if err != nil {
		t.Fatal(err)
	}
	id, err := core.StartAI(ctx, pv.ID, pv.Plan.Fingerprint, false)
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		if v, _ := core.AIRun(ctx, id); v.State != "running" {
			break
		} else if time.Now().After(deadline) {
			t.Fatal("AI run did not finish")
		}
	}
}

// mcpClient serves dir read-only through the MCP server and connects a client.
func mcpClient(t *testing.T, dir string) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, err := store.OpenReadOnly(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	core := app.New(app.Config{Version: "test", DataDir: dir, Store: st, Params: detect.DefaultParams(), Platforms: registerPlatforms(nil)})
	ct, sv := sdk.NewInMemoryTransports()
	if _, err := mcp.New(core, "test").Connect(ctx, sv, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call runs a tool and decodes its structured result into out (when not
// nil); it returns the text content and whether the tool reported an error.
func call(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any, out any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var text strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*sdk.TextContent); ok {
			text.WriteString(tc.Text)
		}
	}
	if out != nil && !res.IsError {
		b, _ := json.Marshal(res.StructuredContent)
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return text.String(), res.IsError
}

func TestMCPToolsAreReadOnlyPagedAndHelpful(t *testing.T) {
	dir := t.TempDir()
	buildEvalData(t, dir)
	cs := mcpClient(t, dir)
	ctx := context.Background()
	list, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mf_list_sessions", "mf_get_session", "mf_search_chats", "mf_get_chats_around", "mf_list_candidates",
		"mf_get_candidate_context", "mf_get_timeline"}
	if len(list.Tools) != len(want) {
		t.Fatalf("%d tools", len(list.Tools))
	}
	for _, tool := range list.Tools {
		a := tool.Annotations
		if !slices.Contains(want, tool.Name) || a == nil || !a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint ||
			a.OpenWorldHint == nil || *a.OpenWorldHint || tool.OutputSchema == nil {
			t.Fatalf("tool %s annotations %+v", tool.Name, a)
		}
		in, _ := json.Marshal(tool.InputSchema)
		if !strings.Contains(string(in), `"response_format"`) {
			t.Fatalf("%s has no response_format: %s", tool.Name, in)
		}
		if tool.Name != "mf_get_session" && !strings.Contains(string(in), `"cursor"`) {
			t.Fatalf("%s is not paged: %s", tool.Name, in)
		}
	}

	var sessions struct {
		Sessions []struct {
			ID string `json:"session_id"`
		} `json:"sessions"`
	}
	call(t, cs, "mf_list_sessions", nil, &sessions)
	sid := sessions.Sessions[0].ID

	// Paging: a search of 280 messages comes in pages with a cursor.
	var first, second struct {
		Total      int        `json:"total"`
		HasMore    bool       `json:"has_more"`
		NextCursor string     `json:"next_cursor"`
		Chats      []evalChat `json:"chats"`
	}
	call(t, cs, "mf_search_chats", map[string]any{"session_id": sid, "query": "ㅋㅋㅋㅋ"}, &first)
	if first.Total != 280 || len(first.Chats) != 50 || !first.HasMore || first.NextCursor != "50" {
		t.Fatalf("first page total %d, %d chats, cursor %q", first.Total, len(first.Chats), first.NextCursor)
	}
	call(t, cs, "mf_search_chats", map[string]any{"session_id": sid, "query": "ㅋㅋㅋㅋ", "cursor": first.NextCursor}, &second)
	if len(second.Chats) != 50 || second.Chats[0].ID == first.Chats[0].ID {
		t.Fatal("the cursor did not move")
	}

	// Counting by author, evidence counts, overlaps and the AI run are direct.
	var byAuthor struct {
		Total int `json:"total"`
	}
	call(t, cs, "mf_search_chats", map[string]any{"session_id": sid, "author": "spammer", "limit": 1}, &byAuthor)
	if byAuthor.Total != 100 {
		t.Fatalf("spammer sent %d", byAuthor.Total)
	}
	var cands struct {
		Candidates []struct {
			ID       string   `json:"candidate_id"`
			Source   string   `json:"source"`
			Evidence *int     `json:"evidence_count"`
			RunID    string   `json:"run_id"`
			Overlaps []string `json:"overlaps"`
		} `json:"candidates"`
	}
	call(t, cs, "mf_list_candidates", map[string]any{"session_id": sid}, &cands)
	for _, c := range cands.Candidates {
		switch {
		case c.ID == "rule:m0003" && (c.Evidence == nil || *c.Evidence != 210):
			t.Fatalf("evidence of %s: %v", c.ID, c.Evidence)
		case c.Source == "ai" && c.RunID == "":
			t.Fatalf("%s has no run", c.ID)
		case c.Source == "rule" && len(c.Overlaps) == 0:
			t.Fatalf("%s overlaps nothing", c.ID)
		}
	}
	var around struct {
		ToMs int64 `json:"to_ms"`
	}
	call(t, cs, "mf_get_chats_around", map[string]any{"session_id": sid, "time": "9:50", "window_seconds": 30}, &around)
	if around.ToMs != 600000 {
		t.Fatalf("window not clipped to the video: %d", around.ToMs)
	}

	// Errors say what to do next.
	for _, c := range []struct {
		tool string
		args map[string]any
		hint string
	}{
		{"mf_get_session", map[string]any{"session_id": "nope"}, "mf_list_sessions"},
		{"mf_get_candidate_context", map[string]any{"session_id": sid, "candidate_id": "rule:nope"}, "mf_list_candidates"},
		{"mf_get_chats_around", map[string]any{"session_id": sid, "time": "7분"}, "1:02:03"},
		{"mf_search_chats", map[string]any{"session_id": sid, "query": "ㅋ", "limit": 1000}, "limit"},
		{"mf_list_candidates", map[string]any{"session_id": sid, "source": "x"}, "source"},
	} {
		if text, isErr := call(t, cs, c.tool, c.args, nil); !isErr || !strings.Contains(text, c.hint) {
			t.Fatalf("%s %v: error %v %q", c.tool, c.args, isErr, text)
		}
	}

	// JSON text on request; raw platform responses never appear.
	text, _ := call(t, cs, "mf_get_session", map[string]any{"session_id": sid, "response_format": "json"}, nil)
	if !json.Valid([]byte(text)) {
		t.Fatalf("json text %q", text)
	}
	extra := map[string]map[string]any{"mf_search_chats": {"query": "ㅋ"}, "mf_get_chats_around": {"time": "5:00"},
		"mf_get_candidate_context": {"candidate_id": "rule:m0001"}}
	for _, tool := range want {
		args := map[string]any{"response_format": "json"}
		if tool != "mf_list_sessions" {
			args["session_id"] = sid
		}
		for k, v := range extra[tool] {
			args[k] = v
		}
		res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %+v", tool, err, res)
		}
		b, _ := json.Marshal(res)
		if strings.Contains(string(b), "synthetic") {
			t.Fatalf("%s exposed a raw page", tool)
		}
	}
}

type evalChat struct {
	ID         string `json:"id"`
	TimeMs     int64  `json:"time_ms"`
	Author     string `json:"author"`
	Text       string `json:"text"`
	IsEvidence bool   `json:"is_evidence"`
}

type evalCandidate struct {
	ID       string   `json:"candidate_id"`
	Source   string   `json:"source"`
	StartMs  int64    `json:"start_ms"`
	EndMs    int64    `json:"end_ms"`
	Start    string   `json:"start"`
	Rules    []string `json:"rules"`
	Priority string   `json:"priority"`
}

type evalPage struct {
	HasMore    bool            `json:"has_more"`
	NextCursor string          `json:"next_cursor"`
	Chats      []evalChat      `json:"chats"`
	Candidates []evalCandidate `json:"candidates"`
}

// pages calls a paged tool until the last page and returns every page.
func pages(t *testing.T, cs *sdk.ClientSession, name string, args map[string]any) []evalPage {
	t.Helper()
	var out []evalPage
	for cursor := ""; ; {
		a := map[string]any{"limit": 200}
		for k, v := range args {
			a[k] = v
		}
		if cursor != "" {
			a["cursor"] = cursor
		}
		var p evalPage
		if text, isErr := call(t, cs, name, a, &p); isErr {
			t.Fatalf("%s: %s", name, text)
		}
		out = append(out, p)
		if !p.HasMore {
			return out
		}
		cursor = p.NextCursor
	}
}

func allChats(ps []evalPage) (out []evalChat) {
	for _, p := range ps {
		out = append(out, p.Chats...)
	}
	return out
}

func allCandidates(ps []evalPage) (out []evalCandidate) {
	for _, p := range ps {
		out = append(out, p.Candidates...)
	}
	return out
}

// TestMCPEvalAnswers checks that each question of e2e/mcp-eval.xml can be
// answered through the MCP tools alone, and that the recorded answer is
// right. With MF_MCP_EVAL_DATA set it also leaves the data set there for a
// model-driven evaluation (momentforge mcp --data <dir>).
func TestMCPEvalAnswers(t *testing.T) {
	dir := os.Getenv("MF_MCP_EVAL_DATA")
	if dir == "" {
		dir = t.TempDir()
	} else if entries, _ := os.ReadDir(dir); len(entries) > 0 {
		t.Fatalf("MF_MCP_EVAL_DATA=%s must be a new or empty folder", dir)
	}
	buildEvalData(t, dir)
	cs := mcpClient(t, dir)

	raw, err := os.ReadFile("../../e2e/mcp-eval.xml")
	if err != nil {
		t.Fatal(err)
	}
	var eval struct {
		Pairs []struct {
			Question string `xml:"question"`
			Answer   string `xml:"answer"`
		} `xml:"qa_pair"`
	}
	if err := xml.Unmarshal(raw, &eval); err != nil {
		t.Fatal(err)
	}
	var sessions struct {
		Sessions []struct {
			ID     string `json:"session_id"`
			IsDemo bool   `json:"is_demo"`
		} `json:"sessions"`
	}
	call(t, cs, "mf_list_sessions", nil, &sessions)
	sid := sessions.Sessions[0].ID
	with := func(extra map[string]any) map[string]any {
		m := map[string]any{"session_id": sid}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	rulesCands := func() []evalCandidate {
		return allCandidates(pages(t, cs, "mf_list_candidates", with(map[string]any{"source": "rule"})))
	}
	overlaps := func(a, b evalCandidate) bool { return min(a.EndMs, b.EndMs) > max(a.StartMs, b.StartMs) }
	topAuthor := func(chats []evalChat) (string, int) {
		count := map[string]int{}
		best := ""
		for _, c := range chats {
			if count[c.Author]++; count[c.Author] > count[best] {
				best = c.Author
			}
		}
		return best, count[best]
	}
	solvers := []func() string{
		func() string { // 1: who sent "ㅋㅋㅋㅋ" most
			var exact []evalChat
			for _, c := range allChats(pages(t, cs, "mf_search_chats", with(map[string]any{"query": "ㅋㅋㅋㅋ"}))) {
				if c.Text == "ㅋㅋㅋㅋ" {
					exact = append(exact, c)
				}
			}
			a, _ := topAuthor(exact)
			return a
		},
		func() string { // 2: the lone spammer's count, and whether a rule candidate covers it
			laughs := allChats(pages(t, cs, "mf_search_chats", with(map[string]any{"query": "ㅋ"})))
			who, _ := topAuthor(laughs)
			var mine []evalChat
			for _, c := range laughs {
				if c.Author == who {
					mine = append(mine, c)
				}
			}
			covered := "아니오"
			for _, r := range rulesCands() {
				if r.StartMs <= mine[0].TimeMs && mine[0].TimeMs < r.EndMs {
					covered = "예"
				}
			}
			return fmt.Sprintf("%d, %s", len(mine), covered)
		},
		func() string { // 3: authors of the repeated sentence
			authors := map[string]bool{}
			for _, c := range allChats(pages(t, cs, "mf_search_chats", with(map[string]any{"query": "다시 보고 싶은 장면"}))) {
				authors[c.Author] = true
			}
			return fmt.Sprint(len(authors))
		},
		func() string { // 4: the viewer recommendation no rule candidate overlaps
			viewers := allCandidates(pages(t, cs, "mf_list_candidates", with(map[string]any{"source": "viewer_comment"})))
			rs := rulesCands()
			var lone []string
			for _, v := range viewers {
				hit := false
				for _, r := range rs {
					hit = hit || overlaps(v, r)
				}
				if !hit {
					lone = append(lone, v.Start)
				}
			}
			return strings.Join(lone, ",")
		},
		func() string { // 5: cache-read tokens of the AI reviews
			var s struct {
				AIRuns []struct {
					CacheRead int `json:"cache_read_tokens"`
				} `json:"ai_runs"`
			}
			call(t, cs, "mf_get_session", with(nil), &s)
			n := 0
			for _, r := range s.AIRuns {
				n += r.CacheRead
			}
			return fmt.Sprint(n)
		},
		func() string { // 6: non-"헐" messages in [7:25, 7:35)
			n := 0
			for _, c := range allChats(pages(t, cs, "mf_get_chats_around", with(map[string]any{"time": "0:07:30", "window_seconds": 5}))) {
				if c.Text != "헐" {
					n++
				}
			}
			return fmt.Sprint(n)
		},
		func() string { // 7: minutes busier than the usual minute
			var tl struct {
				Buckets []struct {
					Chats int `json:"chats"`
				} `json:"buckets"`
			}
			call(t, cs, "mf_get_timeline", with(map[string]any{"bucket_seconds": 60}), &tl)
			freq := map[int]int{}
			usual := 0
			for _, b := range tl.Buckets {
				if freq[b.Chats]++; freq[b.Chats] > freq[usual] {
					usual = b.Chats
				}
			}
			n := 0
			for _, b := range tl.Buckets {
				if b.Chats > usual {
					n++
				}
			}
			return fmt.Sprint(n)
		},
		func() string { // 8: evidence chats of the emotion candidate
			for _, r := range rulesCands() {
				if slices.Contains(r.Rules, "감정 반응") {
					n := 0
					for _, c := range allChats(pages(t, cs, "mf_get_candidate_context", with(map[string]any{"candidate_id": r.ID}))) {
						if c.IsEvidence {
							n++
						}
					}
					return fmt.Sprint(n)
				}
			}
			return ""
		},
		func() string { // 9: the message repeated in the AI's high-priority scenes
			var chats []evalChat
			for _, c := range allCandidates(pages(t, cs, "mf_list_candidates", with(map[string]any{"source": "ai"}))) {
				if c.Priority == "high" {
					chats = append(chats, allChats(pages(t, cs, "mf_get_candidate_context", with(map[string]any{"candidate_id": c.ID})))...)
				}
			}
			count := map[string]int{}
			best := ""
			for _, c := range chats {
				if count[c.Text]++; count[c.Text] > count[best] {
					best = c.Text
				}
			}
			return best
		},
		func() string { // 10: AI recommendations inside the repetition candidate
			ais := allCandidates(pages(t, cs, "mf_list_candidates", with(map[string]any{"source": "ai"})))
			for _, r := range rulesCands() {
				if slices.Contains(r.Rules, "집단 반복") {
					n := 0
					for _, a := range ais {
						if r.StartMs <= a.StartMs && a.EndMs <= r.EndMs {
							n++
						}
					}
					return fmt.Sprint(n)
				}
			}
			return ""
		},
	}
	if len(eval.Pairs) != len(solvers) {
		t.Fatalf("%d questions, %d solvers", len(eval.Pairs), len(solvers))
	}
	for i, p := range eval.Pairs {
		if got := solvers[i](); got != strings.TrimSpace(p.Answer) {
			t.Errorf("question %d: tools give %q, answer is %q", i+1, got, p.Answer)
		}
	}
}
