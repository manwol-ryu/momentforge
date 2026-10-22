// Package detect finds reaction moments in chat. The engine owns windows,
// baselines and candidate grouping; rules (internal/detect/rules) only judge.
package detect

import (
	"context"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

// RulesVersion changes whenever detection output can change for the same input.
// 1.0.0 reproduces CzzEdit 0.3.2 rules 0.1.0.
// 2.0.0: adaptive gate (REDESIGN 6.2.1). 3.0.0: stored timelines are
// analyzed as a whole (AnalyzeBroadcast, REDESIGN 6.2.2); both engines skip
// hidden messages and subscription/system notices.
const RulesVersion = "3.0.0"

// Result is one analysis of a finished timeline.
type Result struct {
	Candidates   []domain.Candidate `json:"candidates"`
	Windows      []domain.Window    `json:"windows"`
	DurationMs   int64              `json:"durationMs"`
	RulesVersion string             `json:"rulesVersion"`
	Params       Params             `json:"params"`
}

// Progress reports analysis position.
type Progress struct {
	PositionMs int64
	DurationMs int64
	Candidates int
}

// Analyze evaluates [0, endMs) every StepMs and groups the signals into candidates.
func Analyze(ctx context.Context, events []domain.ChatEvent, endMs int64, gaps []domain.Gap,
	p Params, rules []Rule, progress func(Progress)) (*Result, error) {
	e, err := NewEngine(p, rules)
	if err != nil {
		return nil, err
	}
	inRange := make([]domain.ChatEvent, 0, len(events))
	for _, ev := range events {
		if ev.OffsetMs >= 0 && ev.OffsetMs < endMs {
			inRange = append(inRange, ev)
		}
	}
	e.Add(inRange)
	for _, g := range gaps {
		e.Gap(g.StartMs, g.EndMs)
	}
	grouper := NewGrouper(e)

	var times []int64
	for t := p.WindowMs; t <= endMs; t += p.StepMs {
		times = append(times, t)
	}
	if endMs >= p.WindowMs && (len(times) == 0 || times[len(times)-1] != endMs) {
		times = append(times, endMs)
	}

	res := &Result{Candidates: []domain.Candidate{}, DurationMs: endMs, RulesVersion: RulesVersion, Params: p}
	res.Windows = make([]domain.Window, 0, len(times))
	for i, t := range times {
		if i%100 == 0 && ctx.Err() != nil {
			return nil, domain.WrapUserError("CANCELLED", "분석을 중단했습니다. 채팅 원본은 저장되어 있습니다.", ctx.Err())
		}
		w, strong := e.Evaluate(t, 0)
		res.Windows = append(res.Windows, w)
		found, err := grouper.Step(w, strong, endMs)
		if err != nil {
			return nil, err
		}
		res.Candidates = append(res.Candidates, found...)
		if progress != nil && (i%100 == 0 || i == len(times)-1) {
			progress(Progress{PositionMs: t, DurationMs: endMs, Candidates: len(res.Candidates)})
		}
	}
	last, err := grouper.Finish(endMs)
	if err != nil {
		return nil, err
	}
	res.Candidates = append(res.Candidates, last...)
	return res, nil
}
