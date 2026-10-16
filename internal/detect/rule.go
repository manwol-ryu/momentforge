package detect

import (
	"fmt"
	"strings"
)

// Rule is one detection rule. Its ID is the signal key ("surge") or the
// prefix of its keys ("repeat:<text>"); Name is shown to users.
//
// A rule implements exactly one of MessageRule or WindowRule. New rules go
// in internal/detect/rules and are registered in cmd/momentforge.
type Rule interface {
	ID() string
	Name() string
}

// MessageRule tags single messages with reaction keys. The engine counts
// distinct authors per key and fires the key when enough of the window's
// authors share it and that share rose above the baseline.
type MessageRule interface {
	Rule
	// Keys returns this rule's keys for one normalized message: ID() or
	// ID()+":"+detail. Nil when the message does not match.
	Keys(text string) []string
	// Share is the minimum fraction of window authors that must share a key.
	Share(p Params) float64
}

// WindowRule judges a whole window (volume, participants).
type WindowRule interface {
	Rule
	Fires(cur WindowStats, base Baseline, p Params) bool
}

// WindowStats are the counted numbers of one window.
type WindowStats struct {
	N int // messages after the per-author cap
	U int // distinct authors
}

// Baseline is what history says about a typical window.
type Baseline struct {
	Ready bool
	N     float64 // median messages per window
	U     float64 // median authors per window
	Ratio float64 // current N / max(baseline N, floor); 0 when not ready
	Z     float64 // robust z-score of current N; 0 when not ready
}

// ruleSet indexes rules by ID and checks the registration contract.
type ruleSet struct {
	all     []Rule
	message []MessageRule
	window  []WindowRule
	byID    map[string]Rule
}

func newRuleSet(rules []Rule) (*ruleSet, error) {
	rs := &ruleSet{byID: make(map[string]Rule)}
	for _, r := range rules {
		id := r.ID()
		if id == "" || strings.Contains(id, ":") {
			return nil, fmt.Errorf("rule id %q: must be non-empty without ':'", id)
		}
		if _, dup := rs.byID[id]; dup {
			return nil, fmt.Errorf("rule id %q registered twice", id)
		}
		mr, isMsg := r.(MessageRule)
		wr, isWin := r.(WindowRule)
		switch {
		case isMsg && isWin:
			return nil, fmt.Errorf("rule %q implements both MessageRule and WindowRule", id)
		case isMsg:
			rs.message = append(rs.message, mr)
		case isWin:
			rs.window = append(rs.window, wr)
		default:
			return nil, fmt.Errorf("rule %q implements neither MessageRule nor WindowRule", id)
		}
		rs.byID[id] = r
		rs.all = append(rs.all, r)
	}
	return rs, nil
}

// ruleOf returns the rule that owns a signal key.
func (rs *ruleSet) ruleOf(key string) Rule {
	id, _, _ := strings.Cut(key, ":")
	return rs.byID[id]
}
