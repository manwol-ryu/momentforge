package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/ai/anthropic"
	"github.com/manwol-ryu/momentforge/internal/ai/cliproxy"
	"github.com/manwol-ryu/momentforge/internal/ai/codex"
	"github.com/manwol-ryu/momentforge/internal/ai/cut"
	"github.com/manwol-ryu/momentforge/internal/ai/openai"
	"github.com/manwol-ryu/momentforge/internal/ai/opencode"
	"github.com/manwol-ryu/momentforge/internal/ai/opencodex"
	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/cuteval"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/detect/rules"
	"github.com/manwol-ryu/momentforge/internal/store"
)

// The clip-only check's strictness (docs/AI-CUT-ANALYSIS.md 9): a plan
// that passed is checked again with the time of its story origin and
// required context cut out of the clip. A strict check fails it; one that
// still passes it is too loose, or the plan's context was not needed.

// strictProbe is one passed plan with its context cut out.
type strictProbe struct {
	Name    string
	Target  string
	Removed []cut.Evidence // the origin and required context taken out
	Clip    []cut.ClipSpan // what verify_clip reads
}

// keyRefsOf is cut.KeyRefs for a stored plan.
func keyRefsOf(p cut.Plan) []string {
	return cut.KeyRefs(cut.Analysis{Causes: p.Causes}, cut.PlanAnswer{RequiredContextRefs: p.RequiredContextRefs,
		StoryOriginRef: p.Times.StoryOriginRef, EventStartRef: p.Times.EventStartRef})
}

// withoutContext cuts the time of the plan's story origin and required
// context out of its chosen spans: a span that holds only them goes, one
// that also holds other evidence is split around them.
func withoutContext(p cut.Plan, ev []cut.Evidence) strictProbe {
	byRef := cut.Known(ev)
	refs := append([]string{p.Times.StoryOriginRef}, p.RequiredContextRefs...)
	var removed []cut.Evidence
	spans := slices.Clone(p.SelectedSpans)
	for _, r := range refs {
		e, ok := byRef[r]
		if !ok || slices.ContainsFunc(removed, func(x cut.Evidence) bool { return x.Ref == r }) || !cut.Within(e, spans) {
			continue
		}
		removed = append(removed, e)
		from, to := e.StartMs, max(e.EndMs, e.StartMs)
		var next []cut.Span
		for _, s := range spans {
			if to < s.StartMs || from > s.EndMs {
				next = append(next, s)
				continue
			}
			if from > s.StartMs {
				next = append(next, cut.Span{StartMs: s.StartMs, EndMs: from - 1, Role: s.Role})
			}
			if to < s.EndMs {
				next = append(next, cut.Span{StartMs: to + 1, EndMs: s.EndMs, Role: s.Role})
			}
		}
		spans = next
	}
	clip := cut.ClipSpans(spans, ev, keyRefsOf(p))
	clip = slices.DeleteFunc(clip, func(s cut.ClipSpan) bool { return len(s.Evidence) == 0 })
	return strictProbe{Name: p.ID, Target: p.Reaction.Target, Removed: removed, Clip: clip}
}

func clipHas(clip []cut.ClipSpan, ref string) (in, key bool) {
	for _, s := range clip {
		for _, e := range s.Evidence {
			if e.Ref == ref {
				return true, e.Key
			}
		}
	}
	return false, false
}

// demoStrictProbe is the sample's "아까 약속" moment as a passed plan (the
// promise at 3:40, the boast and the death before 5:00, the viewers' "아까
// 벌칙", the admission) with the promise cut out.
func demoStrictProbe(t *testing.T) (strictProbe, cut.Plan) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, dir, "eval")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	core := app.New(app.Config{Version: "eval", DataDir: dir, Store: st, Params: detect.DefaultParams(), Rules: rules.All(),
		Platforms: registerPlatforms(nil), AI: &ai.Registry{}})
	t.Cleanup(func() { core.Shutdown(ctx) })
	demo, err := core.CreateDemo(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateAIRun(ctx, store.AIRun{SessionID: demo.ID, Provider: "evalai", Model: "m", Kind: store.AIKindCut, State: store.AICompleted})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := st.CreateCutPlan(ctx, store.CutPlan{RunID: run.ID, SessionID: demo.ID, CandidateIDs: []string{"rule:m0001"}, State: cut.StateOK})
	if err != nil {
		t.Fatal(err)
	}
	p := cut.Plan{SessionID: demo.ID, CandidateIDs: rec.CandidateIDs, State: cut.StateOK,
		SelectedSpans: []cut.Span{{StartMs: 219_700, EndMs: 226_500, Role: "setup"}, {StartMs: 289_700, EndMs: 318_500, Role: "main"}}}
	rec.PlanJSON = ai.Encode(p)
	if err := st.SaveCutPlan(ctx, rec); err != nil {
		t.Fatal(err)
	}
	p, ev, err := core.CutClipEvidence(ctx, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	find := func(prefix string) string {
		t.Helper()
		for _, e := range ev {
			if strings.HasPrefix(e.Text, prefix) {
				return e.Ref
			}
		}
		t.Fatalf("the sample has no %q in the clip", prefix)
		return ""
	}
	promise, boast, death := find("이번 판 지면 벌칙"), find("아 이건 안 죽는다"), find("어 어 어")
	callback := find("아까 벌칙 건다며")
	p.Reaction.Target = "벌칙을 걸고 안 죽는다고 장담하다 바로 죽자, 시청자가 아까 한 벌칙 약속을 떠올리며 웃음"
	p.Causes = []cut.Cause{{Kind: cut.CauseCallback, Summary: "앞서 건 벌칙 약속과 바로 뒤의 실패가 맞물림",
		Support: []string{promise, death, callback}, Against: []string{}, Direction: "broadcast_to_viewers"}}
	p.Times.StoryOriginRef, p.Times.EventStartRef = promise, boast
	p.RequiredContextRefs = []string{promise}
	return withoutContext(p, ev), p
}

// TestCutStrictnessDemoProbe checks what the strictness check sends for the
// sample, and that the server alone fails it when a loose answer files the
// viewers' "아까 벌칙" as a side remark. Nothing is sent.
func TestCutStrictnessDemoProbe(t *testing.T) {
	probe, p := demoStrictProbe(t)
	full := cut.ClipSpans(p.SelectedSpans, nil, nil)
	if len(full) != 2 || len(probe.Removed) != 1 || probe.Removed[0].Ref != p.Times.StoryOriginRef {
		t.Fatalf("removed %+v from %+v", probe.Removed, p.SelectedSpans)
	}
	if in, _ := clipHas(probe.Clip, p.Times.StoryOriginRef); in {
		t.Fatal("the promise is still in the clip")
	}
	callback := p.Causes[0].Support[2]
	if in, key := clipHas(probe.Clip, callback); !in || !key {
		t.Fatalf("the viewers' callback must stay in the clip as key evidence: in %v key %v", in, key)
	}
	if in, _ := clipHas(probe.Clip, p.Causes[0].Support[1]); !in {
		t.Fatal("the death must stay in the clip")
	}
	var echo string
	for _, s := range probe.Clip {
		for _, e := range s.Evidence {
			if strings.HasPrefix(e.Text, "아까 그거") {
				echo = e.Ref
			}
		}
	}
	if echo == "" {
		t.Fatal("the second viewer's callback is not in the clip")
	}
	loose := fmt.Sprintf(`{"pass":true,"answers":{"what":"장담 뒤 실패","condition":"알 수 없음","result":"죽음","references":"알 수 없음","why_reaction":"장담 뒤 실패"},
		"gaps":[],"asides":[{"refs":[%q,%q],"text":"'아까 벌칙'이 가리키는 약속은 클립에 없음"}]}`, callback, echo)
	v, err := cut.ParseClipVerification([]byte(loose), probe.Clip)
	if err != nil {
		t.Fatal(err)
	}
	if v.Pass || len(v.Gaps) != 1 || len(v.Changes) != 2 {
		t.Fatalf("a clip without the promise passed: %+v", v)
	}
}

// TestCutStrictness sends verify_clip for the probes to a real provider:
//
//	MF_CUT_STRICT_PROVIDER=cliproxy MF_CUT_STRICT_MODEL=<model> [MF_CUT_STRICT_DATA=<data folder>] [MF_CUT_STRICT_REPORT=<file>] go test ./cmd/momentforge -run TestCutStrictness -v
//
// It is a development check, off unless the provider is set, and the one
// test here that uses the AI programs and keys of this computer (TestMain
// keeps the others away from them): it sends the sample's probe, plus one
// for every current passed plan in the data folder (opened read-only), one
// request each. The sample must fail; a data plan that still passes is
// reported. The key is the provider's own (CLIProxyAPI's config, a
// program's login) or its environment variable.
func TestCutStrictness(t *testing.T) {
	id := os.Getenv("MF_CUT_STRICT_PROVIDER")
	if id == "" {
		t.Skip("MF_CUT_STRICT_PROVIDER is not set")
	}
	model := os.Getenv("MF_CUT_STRICT_MODEL")
	provider, key := strictProvider(t, id)
	if model == "" {
		model = provider.DefaultModel()
	}
	if model == "" {
		t.Fatal("MF_CUT_STRICT_MODEL is not set")
	}
	sp, ok := provider.(ai.StepProvider)
	if !ok {
		t.Fatalf("%s does not send steps", id)
	}
	ctx := context.Background()

	demo, _ := demoStrictProbe(t)
	demo.Name = "demo-promise-penalty"
	probes := []strictProbe{demo}
	if dir := os.Getenv("MF_CUT_STRICT_DATA"); dir != "" {
		probes = append(probes, dataStrictProbes(t, dir)...)
	}
	var report strings.Builder
	fmt.Fprintf(&report, "# 클립만 보기 엄격함 검사 (%s · %s · 지시문 v%d)\n\n", id, model, cut.PromptVersions["verify_clip"])
	loose := 0
	for _, pr := range probes {
		step := cut.VerifyClipStep(pr.Target, pr.Clip)
		reqCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		raw, _, err := sp.Step(reqCtx, key, model, step)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v", pr.Name, err)
		}
		v, err := cut.ParseClipVerification(raw, pr.Clip)
		if err != nil {
			t.Fatalf("%s: %v\n%s", pr.Name, err, raw)
		}
		writeStrictResult(&report, pr, v)
		if v.Pass {
			loose++
			if pr.Name == demo.Name {
				t.Errorf("the sample without its promise passed the clip-only check")
			}
		}
	}
	fmt.Fprintf(&report, "## 합계\n\n맥락을 뺀 클립 %d개 중 실패 %d · 통과 %d (통과는 검수가 느슨하거나 그 맥락이 필요 없던 것)\n", len(probes), len(probes)-loose, loose)
	t.Log("\n" + report.String())
	if path := os.Getenv("MF_CUT_STRICT_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report.String()), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// strictProvider is a fresh provider (not the test-wide ones TestMain points
// at missing programs) and its key.
func strictProvider(t *testing.T, id string) (ai.Provider, string) {
	t.Helper()
	for _, p := range []ai.Provider{anthropic.New(), codex.New(), opencode.New(), cliproxy.New(), opencodex.New(), openai.New()} {
		if p.ID() != id {
			continue
		}
		if sa, ok := p.(ai.SelfAuthenticated); ok {
			if err := sa.Ready(); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			return p, ""
		}
		if kf, ok := p.(ai.KeyFinder); ok {
			if k, _ := kf.FindKey(); k != "" {
				return p, k
			}
		}
		if env := p.KeyEnv(); env != "" && os.Getenv(env) != "" {
			return p, os.Getenv(env)
		}
		t.Fatalf("%s: no key (its own config or its environment variable)", id)
	}
	t.Fatalf("unknown provider %q", id)
	return nil, ""
}

// dataStrictProbes are the current passed plans of every session in dir
// that hold a story origin or required context to cut out.
func dataStrictProbes(t *testing.T, dir string) []strictProbe {
	t.Helper()
	ctx := context.Background()
	core := readOnlyApp(t, dir)
	sessions, err := core.ExploreSessions(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var out []strictProbe
	for _, s := range sessions {
		details, err := core.CutPlanDetails(ctx, s.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range details {
			if d.State != cut.StateOK {
				continue
			}
			p, ev, err := core.CutClipEvidence(ctx, d.PlanID)
			if err != nil {
				t.Fatal(err)
			}
			pr := withoutContext(p, ev)
			if len(pr.Removed) == 0 || len(pr.Clip) == 0 {
				t.Logf("%s v%d: nothing to cut out (or nothing left)", d.PlanID, d.Version)
				continue
			}
			pr.Name = fmt.Sprintf("%s %s v%d", s.Title, strings.Join(d.CandidateIDs, ","), d.Version)
			out = append(out, pr)
		}
	}
	return out
}

func writeStrictResult(b *strings.Builder, pr strictProbe, v cut.ClipVerification) {
	verdict := "실패 (맥락 누락을 잡음)"
	if v.Pass {
		verdict = "통과 (느슨할 수 있음)"
	}
	n := 0
	for _, s := range pr.Clip {
		n += len(s.Evidence)
	}
	fmt.Fprintf(b, "## %s: %s\n\n- target: %s\n- 클립 근거 %d개, 뺀 맥락:\n", pr.Name, verdict, pr.Target, n)
	for _, e := range pr.Removed {
		fmt.Fprintf(b, "  - %s %s %q\n", cuteval.Timecode(e.StartMs), e.Ref, e.Text)
	}
	line := func(label string, list []cut.ClipFinding) {
		for _, f := range list {
			fmt.Fprintf(b, "- %s: %s (근거 %s)\n", label, f.Text, strings.Join(f.Refs, ", "))
		}
	}
	line("실패 사유", v.Gaps)
	line("참고", v.Asides)
	for _, c := range v.Changes {
		raw, _ := json.Marshal(c)
		fmt.Fprintf(b, "- 서버가 바꾼 분류: %s\n", raw)
	}
	b.WriteString("\n")
}
