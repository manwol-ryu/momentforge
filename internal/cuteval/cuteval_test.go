package cuteval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTime(t *testing.T) {
	for in, want := range map[string]int64{"3:40": 220_000, "1:02:03.5": 3_723_500, "45": 45_000, "0:00:01.25": 1_250} {
		if got, err := ParseTime(in); err != nil || got != want {
			t.Errorf("%q = %d, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "3:60", "1:2:3:4", "a:10", "-1"} {
		if _, err := ParseTime(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if Timecode(220_000) != "0:03:40" || Timecode(3_723_500) != "1:02:03.5" {
		t.Errorf("timecode = %s %s", Timecode(220_000), Timecode(3_723_500))
	}
}

func goldCase() Case {
	w := func(a, b int64) Window { return Window{From: Ms(a), To: Ms(b)} }
	origin := w(215_000, 226_000)
	return Case{ID: "c", Session: SessionRef{Demo: true}, Gold: Gold{
		Start: w(210_000, 221_000), End: w(318_000, 340_000),
		MustInclude: []Window{w(220_000, 226_000), w(290_000, 300_000), w(312_000, 318_000)},
		Cause:       []Window{w(220_000, 226_000)}, Origin: &origin, Ending: "confirmed"}}
}

func TestScore(t *testing.T) {
	c := goldCase()
	origin := int64(220_000)
	// A good clip: setup and event, then the payoff; right cause and origin.
	good := Score(c, Attempt{Method: MethodCut, IsCut: true, Sources: []string{"utterances", "chat"},
		Spans: []Span{{219_700, 226_500}, {289_700, 319_000}}, CauseMs: []int64{220_000, 302_000}, OriginMs: &origin, Ending: "confirmed"})
	if !good.StartOK || !good.EndOK || good.BoundaryErrMs != 0 || good.Coverage != 1 || good.Kept != 3 ||
		!*good.CauseOK || !*good.OriginOK || !*good.EndingOK || good.LengthMs != 6_800+29_300 || !good.Attempt.Transcript() {
		t.Fatalf("good = %+v", good)
	}
	// The detector's clip: starts 64 s late, ends 15 s late, drops the promise.
	det := Score(c, Attempt{Method: MethodDetector, Spans: []Span{{265_000, 355_000}}})
	if det.StartOK || det.EndOK || det.BoundaryErrMs != 44_000+15_000 || det.Kept != 2 || det.CauseOK != nil {
		t.Fatalf("detector = %+v", det)
	}
	if want := float64(10_000+6_000) / 22_000; det.Coverage != want {
		t.Errorf("coverage = %v, want %v", det.Coverage, want)
	}
	// A plan that misses the callback: wrong cause, no origin, ending cut off.
	miss := Score(c, Attempt{Method: MethodCut, IsCut: true, Spans: []Span{{289_700, 305_000}}, CauseMs: []int64{297_000}, Ending: "cut_off"})
	if *miss.CauseOK || *miss.OriginOK || *miss.EndingOK || miss.Attempt.Transcript() {
		t.Fatalf("miss = %+v", miss)
	}
	// A moment that stands alone: claiming an origin is wrong, claiming none is right.
	alone := c
	alone.Gold.Origin = nil
	if r := Score(alone, Attempt{IsCut: true, Spans: []Span{{219_000, 320_000}}, OriginMs: &origin}); *r.OriginOK {
		t.Error("origin claimed for a moment without one")
	}
	if r := Score(alone, Attempt{IsCut: true, Spans: []Span{{219_000, 320_000}}}); !*r.OriginOK {
		t.Error("no origin was not accepted")
	}
	if r := Score(c, Attempt{Method: MethodExplain}); !r.Missing {
		t.Error("an attempt without spans is not missing")
	}
	// Overlapping spans count once.
	if r := Score(c, Attempt{Spans: []Span{{219_000, 300_000}, {250_000, 320_000}}}); r.LengthMs != 101_000 {
		t.Errorf("merged length = %d", r.LengthMs)
	}
}

func TestLoadCasesChecksFiles(t *testing.T) {
	cases, err := LoadCases(filepath.Join("..", "..", "e2e", "cut-eval", "cases"))
	if err != nil || len(cases) == 0 {
		t.Fatalf("committed cases: %d, %v", len(cases), err)
	}
	for _, c := range cases {
		if err := c.Check(); err != nil {
			t.Error(err)
		}
	}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"unknown-field.json": `{"id":"a","session":{"demo":true},"moment":"1:00","gold":{"start":{"from":0,"to":1},"end":{"from":2,"to":3},"cause":[{"from":0,"to":1}]},"extra":1}`,
		"no-cause.json":      `{"id":"b","session":{"demo":true},"moment":"1:00","gold":{"start":{"from":0,"to":1},"end":{"from":2,"to":3}}}`,
		"bad-time.json":      `{"id":"c","session":{"demo":true},"moment":"1:99","gold":{"start":{"from":0,"to":1},"end":{"from":2,"to":3},"cause":[{"from":0,"to":1}]}}`,
		"bad-ending.json":    `{"id":"d","session":{"demo":true},"moment":1,"gold":{"start":{"from":0,"to":1},"end":{"from":2,"to":3},"cause":[{"from":0,"to":1}],"ending":"done"}}`,
		"no-session.json":    `{"id":"e","session":{},"moment":1,"gold":{"start":{"from":0,"to":1},"end":{"from":2,"to":3},"cause":[{"from":0,"to":1}]}}`,
	} {
		sub := filepath.Join(dir, strings.TrimSuffix(name, ".json"))
		os.MkdirAll(sub, 0o700)
		os.WriteFile(filepath.Join(sub, name), []byte(body), 0o600)
		if _, err := LoadCases(sub); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
