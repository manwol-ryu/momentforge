package cut

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func author(s string) *string { return &s }

func evidence() []Evidence {
	return []Evidence{
		{Ref: "utt:p", Kind: KindUtterance, StartMs: 220_000, EndMs: 226_000, Speaker: "방송자", Text: "지면 벌칙"},
		{Ref: "chat:a", Kind: KindChat, StartMs: 280_000, Author: author("u1"), Text: "평소"},
		{Ref: "utt:b", Kind: KindUtterance, StartMs: 290_000, EndMs: 294_000, Speaker: "방송자", Text: "안 죽는다"},
		{Ref: "utt:d", Kind: KindUtterance, StartMs: 297_000, EndMs: 300_000, Speaker: "방송자", Text: "죽었다"},
		{Ref: "chat:l", Kind: KindChat, StartMs: 302_000, Author: author("u2"), Text: "ㅋㅋㅋ 아까 벌칙"},
		{Ref: "utt:e", Kind: KindUtterance, StartMs: 312_000, EndMs: 318_000, Speaker: "방송자", Text: "벌칙 하겠습니다"},
	}
}

// Claude caches a prompt prefix only from 512 tokens; every step's
// instruction must stay above that and must not vary between requests.
func TestPromptsAreCacheable(t *testing.T) {
	for name, p := range map[string]string{"analyze": PromptAnalyze, "plan": PromptPlan, "verify_clip": PromptVerifyClip, "verify_source": PromptVerifySource} {
		if n := len([]rune(p)); n < 1200 {
			t.Errorf("%s prompt has %d characters", name, n)
		}
		for _, varying := range []string{"2026", "chat:e", "utt:e", "demo-"} {
			if strings.Contains(p, varying) {
				t.Errorf("%s prompt contains %q", name, varying)
			}
		}
		if _, ok := PromptVersions[name]; !ok {
			t.Errorf("%s has no prompt version", name)
		}
	}
}

func TestSchemasAreStrictObjects(t *testing.T) {
	for name, s := range map[string]map[string]any{"analyze": AnalyzeSchema(), "plan": PlanSchema(), "verify_clip": VerifyClipSchema(), "verify_source": VerifySourceSchema()} {
		props := s["properties"].(map[string]any)
		req := s["required"].([]string)
		if len(props) != len(req) || s["additionalProperties"] != false {
			t.Errorf("%s: %d properties, %d required, extras %v", name, len(props), len(req), s["additionalProperties"])
		}
	}
}

func TestResolveSpansUsesEvidenceTimes(t *testing.T) {
	known := Known(evidence())
	spans, adj, issues := ResolveSpans([]SpanRefs{
		{StartRef: "utt:p", EndRef: "utt:p", Role: "setup"},
		{StartRef: "chat:a", EndRef: "utt:e", Role: "main"},
	}, known, 600_000)
	if len(issues) != 0 || len(spans) != 2 {
		t.Fatalf("spans %+v issues %v", spans, issues)
	}
	if spans[0].StartMs != 220_000-UtteranceLeadMs || spans[0].EndMs != 226_000+UtteranceTailMs {
		t.Errorf("utterance span = %+v (want its own start - lead and end + tail)", spans[0])
	}
	if spans[1].StartMs != 280_000-ChatLeadMs || spans[1].EndMs != 318_500 {
		t.Errorf("chat-started span = %+v", spans[1])
	}
	whys := map[string]string{}
	for _, a := range adj {
		whys[a.Ref] += a.Why + ";"
	}
	if !strings.Contains(whys["utt:p"], "0.3초 전") || !strings.Contains(whys["utt:p"], "0.5초 뒤") ||
		!strings.Contains(whys["chat:a"], "6초") || !strings.Contains(whys["utt:e"], "0.5초 뒤") {
		t.Errorf("adjustments = %+v", adj)
	}
	// Touching spans merge (and say so); a reversed one is an issue; an unknown ref is an issue.
	merged, madj, _ := ResolveSpans([]SpanRefs{{StartRef: "utt:b", EndRef: "utt:b", Role: "main"}, {StartRef: "utt:e", EndRef: "utt:e", Role: "payoff"}}, known, 600_000)
	if len(merged) != 1 || merged[0].EndMs != 318_500 || merged[0].Role != "main" || !strings.Contains(madj[len(madj)-1].Why, "합침") {
		t.Errorf("merged = %+v adj %+v", merged, madj)
	}
	if _, _, issues := ResolveSpans([]SpanRefs{{StartRef: "utt:e", EndRef: "utt:b"}, {StartRef: "utt:zz", EndRef: "utt:b"}}, known, 600_000); len(issues) != 2 {
		t.Errorf("issues = %v", issues)
	}
}

// A chat boundary that lands inside an utterance moves to that utterance's edge.
func TestChatBoundariesSnapToUtterances(t *testing.T) {
	ev := append(evidence(),
		Evidence{Ref: "chat:s", Kind: KindChat, StartMs: 297_500, Text: "시작"}, // -6 s = 291.5 s, inside utt:b (290–294 s)
		Evidence{Ref: "chat:x", Kind: KindChat, StartMs: 288_000, Text: "끝"},  // +3 s = 291 s, inside utt:b
	)
	known := Known(ev)
	spans, adj, issues := ResolveSpans([]SpanRefs{{StartRef: "chat:s", EndRef: "chat:l", Role: "main"}}, known, 600_000)
	if len(issues) != 0 || spans[0].StartMs != 290_000-UtteranceLeadMs {
		t.Fatalf("snapped start %+v %v", spans, issues)
	}
	if adj[1].Ref != "utt:b" || !strings.Contains(adj[1].Why, "발언 시작") {
		t.Errorf("snap adjustment %+v", adj)
	}
	spans, _, _ = ResolveSpans([]SpanRefs{{StartRef: "utt:p", EndRef: "chat:x", Role: "main"}}, known, 600_000)
	if spans[0].EndMs != 294_000+UtteranceTailMs {
		t.Errorf("snapped end %+v", spans)
	}
	// Every boundary candidate resolves on its own; a broken one keeps its issues.
	res := ResolveBoundaries([]BoundaryCandidate{
		{Name: "short", Spans: []SpanRefs{{StartRef: "utt:b", EndRef: "utt:d", Role: "main"}}},
		{Name: "full_event", Spans: []SpanRefs{{StartRef: "utt:zz", EndRef: "utt:d", Role: "main"}}},
	}, known, 600_000)
	if len(res) != 2 || len(res[0].Spans) != 1 || res[0].Spans[0].EndMs != 300_500 || len(res[1].Spans) != 0 || len(res[1].Issues) != 1 {
		t.Errorf("resolved %+v", res)
	}
	// The plan keeps the time and words of what it cites; a chat seeks a few seconds early.
	p := Plan{Causes: []Cause{{Support: []string{"utt:b", "chat:l"}}}}
	p.Times.StoryOriginRef = "utt:p"
	IndexRefs(&p, known)
	if p.Refs["utt:p"].AtMs != 220_000 || p.Refs["chat:l"].AtMs != 302_000-ChatLeadMs || p.Refs["utt:b"].Text != "안 죽는다" {
		t.Errorf("refs %+v", p.Refs)
	}
}

func TestParseAnalysisDropsUnreadRefs(t *testing.T) {
	known := Known(evidence())
	raw, _ := json.Marshal(map[string]any{"target": "벌칙 약속과 실패", "cause_status": "likely", "questions": []string{}, "search_requests": []any{},
		"causes": []any{
			map[string]any{"kind": "viewer_joke", "summary": "x", "support": []string{"chat:nope"}, "against": []string{}, "direction": "unknown"},
			map[string]any{"kind": "callback_to_earlier", "summary": "y", "support": []string{"utt:p", "utt:p", "chat:l"}, "against": []string{"utt:zz"}, "direction": "broadcast_to_viewers"},
		}})
	an, dropped, err := ParseAnalysis(raw, known)
	if err != nil || dropped != 2 {
		t.Fatalf("err %v dropped %d", err, dropped)
	}
	if an.Causes[0].Kind != "callback_to_earlier" || len(an.Causes[0].Support) != 2 || len(an.Causes[1].Support) != 0 {
		t.Errorf("causes = %+v (supported first, duplicates and unknown refs gone)", an.Causes)
	}
	if _, _, err := ParseAnalysis([]byte(`{"target":"x"}`), known); err == nil {
		t.Error("an incomplete answer passed")
	}
}

func planAnswer(spans []SpanRefs, required []string) PlanAnswer {
	return PlanAnswer{StoryOriginRef: "utt:p", OriginConfidence: "found_in_search_range", EventStartRef: "utt:b",
		AnalysisEvidenceRefs: []string{"utt:p", "utt:b", "utt:d", "chat:l", "utt:e"}, RequiredContextRefs: required,
		OptionalContextRefs: []string{}, Candidates: []BoundaryCandidate{{Name: "context", Spans: spans}}, Chosen: "context",
		StartReason: "약속부터", EndReason: "인정까지", Ending: Ending{State: "confirmed", EventKind: "promise_penalty"}}
}

func TestCheckPlanRequiresContextInsideSpans(t *testing.T) {
	known := Known(evidence())
	good := planAnswer([]SpanRefs{{StartRef: "utt:p", EndRef: "utt:p", Role: "setup"}, {StartRef: "utt:b", EndRef: "utt:e", Role: "main"}}, []string{"utt:p", "utt:d"})
	checked, issues := CheckPlan(&good, known, 600_000)
	if len(issues) != 0 || len(checked.Spans) != 2 {
		t.Fatalf("good plan: spans %d issues %v", len(checked.Spans), issues)
	}
	// The promise is required but its span is missing: an issue the re-plan can act on.
	bad := planAnswer([]SpanRefs{{StartRef: "utt:b", EndRef: "utt:e", Role: "main"}}, []string{"utt:p", "utt:d"})
	_, issues = CheckPlan(&bad, known, 600_000)
	if len(issues) != 1 || !strings.Contains(issues[0], "utt:p") {
		t.Errorf("issues = %v", issues)
	}
	// Required context must be among the analysis evidence; unknown refs are dropped and reported.
	odd := planAnswer([]SpanRefs{{StartRef: "utt:b", EndRef: "utt:e", Role: "main"}}, []string{"chat:a"})
	odd.AnalysisEvidenceRefs = []string{"utt:b", "utt:zz"}
	checked, issues = CheckPlan(&odd, known, 600_000)
	if checked.Dropped != 1 || len(issues) < 2 {
		t.Errorf("dropped %d issues %v", checked.Dropped, issues)
	}
	// A chosen name without a candidate is an issue, not a crash.
	none := planAnswer(nil, nil)
	none.Chosen = "short"
	if _, issues = CheckPlan(&none, known, 600_000); len(issues) != 1 {
		t.Errorf("issues = %v", issues)
	}
}

func TestVerificationParsing(t *testing.T) {
	clip, err := ParseClipVerification([]byte(`{"pass":false,"answers":{"what":"x","condition":"알 수 없음","result":"x","references":"x","why_reaction":"x"},"gaps":[{"refs":[],"text":"약속이 없음"}],"asides":[]}`), nil)
	if err != nil || clip.Pass || len(clip.Gaps) != 1 || clip.Gaps[0].Text != "약속이 없음" {
		t.Fatalf("clip %+v err %v", clip, err)
	}
	src, err := ParseSourceVerification([]byte(`{"pass":true,"mixed_attempt":false,"cut_condition":true,"missed_correction":false,"ended_early":false,"notes":[]}`))
	if err != nil || src.Pass {
		t.Fatalf("pass must follow the findings: %+v err %v", src, err)
	}
	issues := Issues(&clip, &src)
	if len(issues) != 2 || !strings.HasPrefix(issues[0], "클립만") || !strings.HasPrefix(issues[1], "원본") {
		t.Errorf("issues = %v", issues)
	}
	if _, err := ParseClipVerification([]byte(`{"pass":true,"answers":{"what":"x"},"gaps":[]}`), nil); err == nil {
		t.Error("missing answers passed")
	}
	if _, err := ParseClipVerification([]byte(`{"pass":true,"answers":{"what":"x","condition":"x","result":"x","references":"x","why_reaction":"x"},"gaps":[],"server_changes":[{"from":"fail","to":"pass","why":"x"}]}`), nil); err == nil {
		t.Error("the model wrote the server's field")
	}
}

func TestAsidesNeverFailTheClipCheck(t *testing.T) {
	clip, err := ParseClipVerification([]byte(`{"pass":true,"answers":{"what":"x","condition":"해당 없음","result":"x","references":"x","why_reaction":"x"},"gaps":[],"asides":[{"refs":[],"text":"채팅 '이미 써먹었죠'가 가리키는 일은 알 수 없음"}]}`), nil)
	if err != nil || len(clip.Asides) != 1 || !clip.Pass {
		t.Fatalf("clip %+v err %v", clip, err)
	}
	if issues := Issues(&clip, nil); len(issues) != 0 {
		t.Errorf("an aside became an issue: %v", issues)
	}
	// An answer from before asides existed still reads, with an empty list.
	old, err := ParseClipVerification([]byte(`{"pass":true,"answers":{"what":"x","condition":"x","result":"x","references":"x","why_reaction":"x"},"gaps":[]}`), nil)
	if err != nil || old.Asides == nil {
		t.Fatalf("old %+v err %v", old, err)
	}
}

// A plan checked with prompt v2 stored gaps and asides as plain strings.
func TestStoredStringFindingsStillRead(t *testing.T) {
	var p Plan
	raw := `{"verification":{"clip_only":{"pass":false,"answers":{},"gaps":["약속이 없음"],"asides":["곁가지"]},"evidence_match":"all_read","boundary_ok":false,"issues":[]}}`
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		t.Fatal(err)
	}
	v := p.Verification.ClipOnly
	if v == nil || len(v.Gaps) != 1 || v.Gaps[0].Text != "약속이 없음" || v.Gaps[0].Refs == nil || v.Asides[0].Text != "곁가지" {
		t.Fatalf("clip = %+v", v)
	}
	b, _ := json.Marshal(v)
	if !strings.Contains(string(b), `"gaps":[{"refs":[],"text":"약속이 없음"}]`) || strings.Contains(string(b), "server_changes") {
		t.Errorf("rewritten as %s", b)
	}
}

func TestVerifyClipMarksKeyEvidence(t *testing.T) {
	ev := []Evidence{
		{Ref: "utt:1", Kind: KindUtterance, StartMs: 10_000, EndMs: 15_000, Text: "받아 갔잖아 아니네 다른 사람이었네"},
		{Ref: "chat:a", Kind: KindChat, StartMs: 16_000, Text: "ㅋㅋㅋㅋ"},
		{Ref: "chat:b", Kind: KindChat, StartMs: 17_000, Text: "이미 써먹었죠"},
		{Ref: "chat:out", Kind: KindChat, StartMs: 90_000, Text: "범위 밖"},
	}
	an := Analysis{Causes: []Cause{{Support: []string{"utt:1", "chat:a"}}, {Support: []string{"chat:b"}}}}
	answer := PlanAnswer{EventStartRef: "utt:1", RequiredContextRefs: []string{"utt:1"}}
	key := KeyRefs(an, answer)
	clip := ClipSpans([]Span{{StartMs: 9_000, EndMs: 20_000, Role: "main"}}, ev, key)
	step := VerifyClipStep("오인과 정정", clip)
	spans := step.Input.(map[string]any)["spans"].([]ClipSpan)
	got := map[string]bool{}
	for _, e := range spans[0].Evidence {
		got[e.Ref] = e.Key
	}
	if len(got) != 3 || !got["utt:1"] || !got["chat:a"] || got["chat:b"] {
		t.Errorf("key marks = %v (only the first cause, required context and event start are key)", got)
	}
	b, _ := json.Marshal(spans[0].Evidence[2])
	if strings.Contains(string(b), `"key"`) || !strings.Contains(string(b), `"ref":"chat:b"`) {
		t.Errorf("a side remark is sent without a key mark: %s", b)
	}
}

func TestServerReclassifiesClipFindings(t *testing.T) {
	clip := ClipSpans([]Span{{StartMs: 0, EndMs: 60_000, Role: "main"}}, []Evidence{
		{Ref: "utt:1", Kind: KindUtterance, StartMs: 1_000, EndMs: 4_000, Text: "아 죽었다"},
		{Ref: "utt:2", Kind: KindUtterance, StartMs: 5_000, EndMs: 8_000, Text: "그거 말고"},
		{Ref: "chat:k", Kind: KindChat, StartMs: 10_000, Author: author("u1"), Text: "아까 벌칙 건다며"},
		{Ref: "chat:b", Kind: KindChat, StartMs: 11_000, Author: author("u2"), Text: "이미 써먹었죠"},
		{Ref: "chat:c", Kind: KindChat, StartMs: 12_000, Author: author("u3"), Text: "아까 그거 ㅋㅋ"},
		{Ref: "chat:d", Kind: KindChat, StartMs: 13_000, Author: author("u2"), Text: "또 그 얘기"},
		{Ref: "chat:n", Kind: KindChat, StartMs: 14_000, Text: "작성자 모름"},
		{Ref: "chat:out", Kind: KindChat, StartMs: 90_000, Author: author("u4"), Text: "클립 밖"},
	}, []string{"utt:1", "chat:k"})
	answers := `"answers":{"what":"x","condition":"x","result":"x","references":"x","why_reaction":"x"}`
	parse := func(t *testing.T, pass bool, gaps, asides string) ClipVerification {
		t.Helper()
		raw := fmt.Sprintf(`{"pass":%v,%s,"gaps":[%s],"asides":[%s]}`, pass, answers, gaps, asides)
		v, err := ParseClipVerification([]byte(raw), clip)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	texts := func(list []ClipFinding) []string {
		out := []string{}
		for _, f := range list {
			out = append(out, f.Text)
		}
		return out
	}

	t.Run("an aside citing key evidence is a gap", func(t *testing.T) {
		v := parse(t, true, ``, `{"refs":["chat:k"],"text":"'아까 벌칙'이 가리키는 약속이 없음"}`)
		if v.Pass || len(v.Gaps) != 1 || len(v.Asides) != 0 || len(v.Changes) != 2 ||
			v.Changes[0].From != "aside" || v.Changes[0].To != "gap" || !strings.Contains(v.Changes[0].Why, "chat:k") ||
			v.Changes[1].From != "pass" || v.Changes[1].To != "fail" {
			t.Fatalf("v = %+v", v)
		}
		if issues := Issues(&v, nil); len(issues) != 1 || !strings.Contains(issues[0], "(근거 chat:k)") {
			t.Errorf("issues = %v", issues)
		}
	})
	t.Run("chats of two authors pointing at one past are a gap", func(t *testing.T) {
		v := parse(t, true, ``, `{"refs":["chat:b","chat:c","chat:d"],"text":"여러 사람이 말하는 그 일"}`)
		if v.Pass || len(v.Gaps) != 1 || !strings.Contains(v.Changes[0].Why, "작성자 2명") {
			t.Fatalf("v = %+v", v)
		}
	})
	t.Run("one viewer's chats only are an aside", func(t *testing.T) {
		v := parse(t, false, `{"refs":["chat:b","chat:d"],"text":"'이미 써먹었죠'가 가리키는 일"}`, ``)
		if !v.Pass || len(v.Gaps) != 0 || len(v.Asides) != 1 || len(v.Changes) != 2 ||
			v.Changes[0].From != "gap" || v.Changes[0].To != "aside" || v.Changes[1].From != "fail" || v.Changes[1].To != "pass" {
			t.Fatalf("v = %+v", v)
		}
	})
	t.Run("what the server cannot judge stays where the model put it", func(t *testing.T) {
		v := parse(t, false,
			`{"refs":[],"text":"target의 사건이 클립에 없음"},{"refs":["utt:2"],"text":"'그거'가 무엇인지"},{"refs":["chat:n"],"text":"작성자 모름"}`,
			`{"refs":["chat:b"],"text":"곁가지"},{"refs":[],"text":"배경 음악"}`)
		if v.Pass || len(v.Changes) != 0 {
			t.Fatalf("v = %+v", v)
		}
		if g, a := texts(v.Gaps), texts(v.Asides); len(g) != 3 || len(a) != 2 {
			t.Errorf("gaps %v asides %v", g, a)
		}
	})
	t.Run("refs outside the clip are dropped", func(t *testing.T) {
		v := parse(t, false, `{"refs":["chat:out","chat:out","chat:b"],"text":"클립 밖을 가리킴"}`, ``)
		// Left with one viewer's chat: an aside, and the check passes.
		if !v.Pass || len(v.Asides) != 1 || len(v.Asides[0].Refs) != 1 || v.Asides[0].Refs[0] != "chat:b" ||
			v.Changes[0].From != "gap" || v.Changes[0].To != "gap" || !strings.Contains(v.Changes[0].Why, "chat:out") {
			t.Fatalf("v = %+v", v)
		}
	})
	t.Run("a failure without a gap stays a failure", func(t *testing.T) {
		v := parse(t, false, ``, `{"refs":["chat:b"],"text":"곁가지"}`)
		if v.Pass || len(v.Changes) != 0 {
			t.Fatalf("v = %+v", v)
		}
		if issues := Issues(&v, nil); len(issues) != 1 || !strings.Contains(issues[0], "이해할 수 없다고") {
			t.Errorf("issues = %v", issues)
		}
	})
	t.Run("a pass with a gap is a failure", func(t *testing.T) {
		v := parse(t, true, `{"refs":["utt:2"],"text":"'그거'가 무엇인지"}`, ``)
		if v.Pass || len(v.Changes) != 1 || v.Changes[0].To != "fail" {
			t.Fatalf("v = %+v", v)
		}
	})
}

func TestClampAppliesDefaultsAndLimits(t *testing.T) {
	in := Input{WindowStartMs: 215000, WindowEndMs: 415000, DurationMs: 1_200_000,
		Candidates: []Candidate{{ID: "rule:m1", ClipStartMs: 275000, ClipEndMs: 355000}}}
	r, why := Clamp(SearchRequest{Tool: ToolSearch, Query: "  벌칙  ", AtMs: 5}, in)
	if why != "" || r.Query != "벌칙" || r.BeforeMs != 275000 || r.In != "both" || r.AtMs != 0 {
		t.Fatalf("search %+v %q", r, why)
	}
	long := SearchRequest{Tool: ToolSearch, Query: string(make([]rune, 0)) + "가나다라마바사아자차카타파하가나다라마바사아자차카타파하가나다라마바사아자차카타파하", BeforeMs: 9_999_999}
	r, _ = Clamp(long, in)
	if len([]rune(r.Query)) != SearchMaxQueryRunes || r.BeforeMs != 275000 {
		t.Fatalf("long query %+v", r)
	}
	if _, why := Clamp(SearchRequest{Tool: ToolSearch, Query: " "}, in); why == "" {
		t.Fatal("empty query ran")
	}
	r, why = Clamp(SearchRequest{Tool: ToolReadAround, AtMs: 60000, WindowS: 900}, in)
	if why != "" || r.WindowS != ReadAroundMaxWindowS {
		t.Fatalf("read_around %+v %q", r, why)
	}
	if _, why := Clamp(SearchRequest{Tool: ToolReadAround, AtMs: 2_000_000}, in); why == "" {
		t.Fatal("read_around past the end ran")
	}
	r, why = Clamp(SearchRequest{Tool: ToolReadRange, FromMs: 0, ToMs: 1_100_000}, in)
	if why != "" || r.ToMs != ReadRangeMaxMs {
		t.Fatalf("read_range %+v %q", r, why)
	}
	r, why = Clamp(SearchRequest{Tool: ToolActivity}, in)
	if why != "" || r.FromMs != 0 || r.ToMs != 1_200_000 || r.WindowS < ActivityMinBucketS {
		t.Fatalf("activity %+v %q", r, why)
	}
	for _, tool := range []string{ToolEvents, ToolListThreads, "sql"} {
		if _, why := Clamp(SearchRequest{Tool: tool}, in); why == "" {
			t.Fatalf("%s ran", tool)
		}
	}
	if MaxRequests(DefaultBudget()) != BaseRequestsPerCandidate+DefaultSearchRounds {
		t.Fatalf("max requests %d", MaxRequests(DefaultBudget()))
	}
}

func TestMergeMarksNewEvidenceAndKeepsOrder(t *testing.T) {
	in := Input{Evidence: []Evidence{{Ref: "chat:b", Kind: KindChat, StartMs: 300000, Text: "b"}}}
	known := Known(in.Evidence)
	added := Merge(&in, known, []Evidence{
		{Ref: "utt:a", Kind: KindUtterance, StartMs: 30000, EndMs: 34000, Text: "a"},
		{Ref: "chat:b", Kind: KindChat, StartMs: 300000, Text: "b"}, // already there
	})
	if len(added) != 1 || added[0] != "utt:a" || len(in.Evidence) != 2 || in.Evidence[0].Ref != "utt:a" || !in.Evidence[0].FoundBySearch ||
		!known["utt:a"].FoundBySearch || in.Evidence[1].FoundBySearch {
		t.Fatalf("added %v evidence %+v", added, in.Evidence)
	}
	if !SameRequest(SearchRequest{Tool: ToolSearch, Query: "a", BeforeMs: 1}, SearchRequest{Tool: ToolSearch, Query: "a", BeforeMs: 1}) ||
		SameRequest(SearchRequest{Tool: ToolSearch, Query: "a"}, SearchRequest{Tool: ToolSearch, Query: "b"}) {
		t.Fatal("SameRequest")
	}
}

func TestCheckPlanReplansOnEveryInventedRef(t *testing.T) {
	known := Known(evidence())
	for _, tc := range []struct {
		name string
		edit func(*PlanAnswer)
	}{
		{"required", func(p *PlanAnswer) { p.RequiredContextRefs = append(p.RequiredContextRefs, "utt:zz") }},
		{"analysis", func(p *PlanAnswer) { p.AnalysisEvidenceRefs = append(p.AnalysisEvidenceRefs, "chat:zz") }},
		{"optional", func(p *PlanAnswer) { p.OptionalContextRefs = []string{"utt:zz"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := planAnswer([]SpanRefs{{StartRef: "utt:p", EndRef: "utt:p", Role: "setup"}, {StartRef: "utt:b", EndRef: "utt:e", Role: "main"}},
				[]string{"utt:p", "utt:d"})
			tc.edit(&p)
			checked, issues := CheckPlan(&p, known, 600_000)
			if checked.Dropped != 1 || len(issues) != 1 || !strings.Contains(issues[0], "읽지 않은 근거") {
				t.Fatalf("dropped %d issues %v", checked.Dropped, issues)
			}
		})
	}
}

func TestMergeLeavesTheSharedEvidenceAlone(t *testing.T) {
	// The preview's list has room to grow in place; a search must not write into it.
	shared := make([]Evidence, 2, 8)
	shared[0] = Evidence{Ref: "chat:b", Kind: KindChat, StartMs: 300000, Text: "b"}
	shared[1] = Evidence{Ref: "chat:c", Kind: KindChat, StartMs: 310000, Text: "c"}
	in := Input{Evidence: shared}
	added := Merge(&in, Known(in.Evidence), []Evidence{{Ref: "utt:a", Kind: KindUtterance, StartMs: 30000, Text: "a"}})
	if len(added) != 1 || in.Evidence[0].Ref != "utt:a" || len(in.Evidence) != 3 {
		t.Fatalf("merged %+v", in.Evidence)
	}
	if shared[0].Ref != "chat:b" || shared[1].Ref != "chat:c" || shared[:3][2].Ref != "" {
		t.Fatalf("the shared list changed: %+v", shared[:3])
	}
	// Nothing new: the input keeps its list.
	before := in.Evidence
	if Merge(&in, Known(in.Evidence), in.Evidence[:1]) != nil || &before[0] != &in.Evidence[0] {
		t.Fatal("a merge without news replaced the list")
	}
}
