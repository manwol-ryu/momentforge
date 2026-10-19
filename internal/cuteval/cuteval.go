// Package cuteval scores AI cut plans against hand-written answers
// (docs/AI-CUT-ANALYSIS.md 9). A case names one detected moment and what a
// good clip of it keeps; the scorer reads what the app already saved for
// that moment (the detector's clip, the first-generation AI range, every
// boundary option of the newest cut plan) and says how each one fares. It
// only reads: nothing is sent to an AI provider here, so the cut analysis
// still runs from the app after its preview is confirmed.
package cuteval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Ms is a video time in milliseconds. In a case file it is a number of
// milliseconds or a timecode ("3:40", "1:02:03.5").
type Ms int64

func (m *Ms) UnmarshalJSON(b []byte) error {
	var n int64
	if err := json.Unmarshal(b, &n); err == nil {
		*m = Ms(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("time must be milliseconds or a timecode: %s", b)
	}
	v, err := ParseTime(s)
	if err != nil {
		return err
	}
	*m = Ms(v)
	return nil
}

func (m Ms) MarshalJSON() ([]byte, error) { return json.Marshal(Timecode(int64(m))) }

// ParseTime reads "H:MM:SS", "M:SS" or "SS", each with optional ".mmm".
func ParseTime(s string) (int64, error) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	if s == "" || len(parts) > 3 {
		return 0, fmt.Errorf("bad timecode %q", s)
	}
	var total float64
	for i, p := range parts {
		v, err := strconv.ParseFloat(p, 64)
		if err != nil || v < 0 || (i > 0 && v >= 60) {
			return 0, fmt.Errorf("bad timecode %q", s)
		}
		total = total*60 + v
	}
	return int64(total*1000 + 0.5), nil
}

// Timecode is "H:MM:SS" with tenths when they are not zero.
func Timecode(ms int64) string {
	neg := ""
	if ms < 0 {
		neg, ms = "-", -ms
	}
	s := fmt.Sprintf("%s%d:%02d:%02d", neg, ms/3_600_000, ms/60_000%60, ms/1000%60)
	if t := ms % 1000 / 100; t != 0 {
		s += fmt.Sprintf(".%d", t)
	}
	return s
}

// Window is a span of video time [From, To].
type Window struct {
	From Ms     `json:"from"`
	To   Ms     `json:"to"`
	Why  string `json:"why,omitempty"`
}

func (w Window) has(ms int64) bool { return ms >= int64(w.From) && ms <= int64(w.To) }

// SessionRef says which saved session a case is about: the app's sample,
// a platform video ("chzzk:15723059"), or a title.
type SessionRef struct {
	Demo  bool   `json:"demo,omitempty"`
	Video string `json:"video,omitempty"`
	Title string `json:"title,omitempty"`
}

// Gold is what a good clip of the moment does.
type Gold struct {
	// Start and End are where the clip may begin and end (the first span's
	// start and the last span's end).
	Start Window `json:"start"`
	End   Window `json:"end"`
	// MustInclude are the spans an editor would not cut: the setup the
	// reaction refers to, the event, its payoff.
	MustInclude []Window `json:"must_include"`
	// Cause holds where the evidence for the real cause is; a plan's leading
	// cause is right when one of its supporting refs falls in one of them.
	Cause []Window `json:"cause"`
	// Origin is where the story the reaction calls back to begins; nil when
	// the moment stands alone (then a plan should not claim one).
	Origin *Window `json:"origin,omitempty"`
	// Ending is the ending state a plan should report (confirmed, pending,
	// cut_off, insufficient, unresolved); empty to skip.
	Ending string `json:"ending,omitempty"`
}

// Case is one hand-written answer.
type Case struct {
	ID      string     `json:"id"`
	Title   string     `json:"title"`
	Tags    []string   `json:"tags,omitempty"` // situations it tests (callback, donation_overlap, irony, …)
	Session SessionRef `json:"session"`
	// MomentMs picks the detected candidate: the rule candidate whose clip
	// holds this time, else the one whose clip starts nearest (within a minute).
	MomentMs Ms     `json:"moment"`
	Gold     Gold   `json:"gold"`
	Note     string `json:"note,omitempty"`
	file     string
}

// File is where the case was read from.
func (c Case) File() string { return c.file }

// Check reports what is wrong with a case file.
func (c Case) Check() error {
	g := c.Gold
	switch {
	case c.ID == "":
		return fmt.Errorf("id is missing")
	case !c.Session.Demo && c.Session.Video == "" && c.Session.Title == "":
		return fmt.Errorf("%s: session needs demo, video or title", c.ID)
	case g.Start.To < g.Start.From || g.End.To < g.End.From:
		return fmt.Errorf("%s: a window ends before it starts", c.ID)
	case g.End.To <= g.Start.From:
		return fmt.Errorf("%s: the clip would end before it starts", c.ID)
	case len(g.Cause) == 0:
		return fmt.Errorf("%s: cause needs at least one window", c.ID)
	}
	for _, w := range append(append([]Window{}, g.MustInclude...), g.Cause...) {
		if w.To < w.From {
			return fmt.Errorf("%s: a window ends before it starts", c.ID)
		}
	}
	if g.Origin != nil && g.Origin.To < g.Origin.From {
		return fmt.Errorf("%s: origin ends before it starts", c.ID)
	}
	switch g.Ending {
	case "", "confirmed", "pending", "cut_off", "insufficient", "unresolved":
	default:
		return fmt.Errorf("%s: unknown ending %q", c.ID, g.Ending)
	}
	return nil
}

// LoadCases reads every *.json case in dir (sorted by name).
func LoadCases(dir string) ([]Case, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var out []Case
	seen := map[string]string{}
	for _, name := range names {
		raw, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		var c Case
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
		if err := c.Check(); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(name), err)
		}
		if prev, ok := seen[c.ID]; ok {
			return nil, fmt.Errorf("%s: id %q is also in %s", filepath.Base(name), c.ID, prev)
		}
		seen[c.ID] = filepath.Base(name)
		c.file = name
		out = append(out, c)
	}
	return out, nil
}
