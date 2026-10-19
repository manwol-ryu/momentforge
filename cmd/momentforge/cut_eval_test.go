package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manwol-ryu/momentforge/internal/app"
	"github.com/manwol-ryu/momentforge/internal/cuteval"
	"github.com/manwol-ryu/momentforge/internal/detect"
	"github.com/manwol-ryu/momentforge/internal/store"
)

const evalCases = "../../e2e/cut-eval/cases"

// readOnlyApp opens a data folder the way the MCP server does.
func readOnlyApp(t *testing.T, dir string) *app.App {
	t.Helper()
	st, err := store.OpenReadOnly(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return app.New(app.Config{Version: "eval", DataDir: dir, Store: st, Params: detect.DefaultParams(), Platforms: registerPlatforms(nil)})
}

// TestCutEvalDemo scores the sample's callback case against the stored
// plans of buildCutData: the detector's clip misses the promise, the cut
// plan finds cause and origin but ends before the payoff.
func TestCutEvalDemo(t *testing.T) {
	dir := t.TempDir()
	sid, v1, v2 := buildCutData(t, dir)
	core := readOnlyApp(t, dir)
	ctx := context.Background()
	cases, err := cuteval.LoadCases(evalCases)
	if err != nil {
		t.Fatal(err)
	}
	var demo []cuteval.Case
	for _, c := range cases {
		if c.Session.Demo {
			demo = append(demo, c)
		}
	}
	if len(demo) == 0 {
		t.Fatal("no demo case")
	}
	results, skipped, err := cuteval.Run(ctx, core, demo)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("run: %v, skipped %v", err, skipped)
	}
	by := map[string]cuteval.Result{}
	for _, r := range results {
		if r.Case.ID == "demo-promise-penalty" {
			by[r.Attempt.Method] = r
		}
	}
	det, cut := by[cuteval.MethodDetector], by[cuteval.MethodCut]
	if det.Missing || det.StartOK || det.Coverage >= 1 {
		t.Errorf("detector = %+v", det)
	}
	if !by[cuteval.MethodExplain].Missing {
		t.Errorf("explain without an AI review = %+v", by[cuteval.MethodExplain])
	}
	// The newest version (v2) is scored; its chosen boundary is "context".
	if cut.Missing || cut.Attempt.PlanID != v2 || cut.Attempt.Version != 2 || !cut.StartOK || cut.EndOK ||
		!*cut.CauseOK || !*cut.OriginOK || !*cut.EndingOK || cut.Kept != 2 {
		t.Fatalf("cut = %+v (v1 %s)", cut, v1)
	}
	if full := by[cuteval.MethodCut+":full_event"]; full.Missing || full.Coverage <= cut.Coverage-0.01 {
		t.Errorf("full_event = %+v", full)
	}
	report := cuteval.Report(results, skipped)
	for _, want := range []string{"## demo-promise-penalty", "탐지기 구간", "컷 분석 (AI 선택)", "컷 분석 · 전체", "## 방식별 합계", "발언+채팅"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q", want)
		}
	}

	// The confirmed v1 becomes a draft case that loads and scores its own plan well.
	drafts, err := cuteval.Drafts(ctx, core, sid)
	if err != nil || len(drafts) != 1 {
		t.Fatalf("drafts = %+v, %v", drafts, err)
	}
	d := drafts[0]
	if err := d.Check(); err != nil || !d.Session.Demo || d.Gold.Origin == nil || len(d.Gold.MustInclude) == 0 {
		t.Fatalf("draft = %+v, %v", d, err)
	}
	b, _ := json.Marshal(d)
	draftDir := t.TempDir()
	os.WriteFile(filepath.Join(draftDir, "d.json"), b, 0o600)
	loaded, err := cuteval.LoadCases(draftDir)
	if err != nil || len(loaded) != 1 || loaded[0].Gold.Start != d.Gold.Start {
		t.Fatalf("draft does not load back: %+v, %v", loaded, err)
	}
}

// TestCutEvalReport scores e2e/cut-eval/cases against a real data folder:
//
//	MF_CUT_EVAL_DATA=<data folder> [MF_CUT_EVAL_REPORT=<file>] [MF_CUT_EVAL_DRAFT=<session id>] go test ./cmd/momentforge -run TestCutEvalReport -v
//
// The folder is opened read-only. With MF_CUT_EVAL_DRAFT, the session's
// confirmed cuts are written as drafts to e2e/cut-eval/drafts/ for a person
// to check and move to cases/.
func TestCutEvalReport(t *testing.T) {
	dir := os.Getenv("MF_CUT_EVAL_DATA")
	if dir == "" {
		t.Skip("MF_CUT_EVAL_DATA is not set")
	}
	core := readOnlyApp(t, dir)
	ctx := context.Background()
	if sid := os.Getenv("MF_CUT_EVAL_DRAFT"); sid != "" {
		drafts, err := cuteval.Drafts(ctx, core, sid)
		if err != nil {
			t.Fatal(err)
		}
		out := "../../e2e/cut-eval/drafts"
		if err := os.MkdirAll(out, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, d := range drafts {
			b, err := json.MarshalIndent(d, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(out, d.ID+".json")
			if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("draft %s", path)
		}
		if len(drafts) == 0 {
			t.Log("the session has no confirmed cut plan")
		}
	}
	cases, err := cuteval.LoadCases(evalCases)
	if err != nil {
		t.Fatal(err)
	}
	results, skipped, err := cuteval.Run(ctx, core, cases)
	if err != nil {
		t.Fatal(err)
	}
	report := cuteval.Report(results, skipped)
	t.Log("\n" + report)
	if path := os.Getenv("MF_CUT_EVAL_REPORT"); path != "" {
		if err := os.WriteFile(path, []byte(report), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}
