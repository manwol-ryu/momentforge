package app

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/manwol-ryu/momentforge/internal/ai"
	"github.com/manwol-ryu/momentforge/internal/domain"
)

// AIRecommendation is an accepted AI recommendation.
type AIRecommendation = ai.Recommendation

// AIComparison sets two reviews of one session side by side: moments both
// recommended (overlapping in time) and moments only one did.
type AIComparison struct {
	A, B      AIRunView
	SameInput bool // both reviewed exactly the same chats
	Matched   []AIMatch
	OnlyA     []AIRecommendation
	OnlyB     []AIRecommendation
}

// AIMatch is a pair of recommendations that overlap in time.
type AIMatch struct {
	A, B      AIRecommendation
	OverlapMs int64
}

// CompareAI compares two reviews of the same session.
func (a *App) CompareAI(ctx context.Context, idA, idB string) (*AIComparison, error) {
	if idA == idB {
		return nil, domain.NewUserError("INPUT", "서로 다른 AI 검토 두 개를 골라 주세요.")
	}
	ra, err := a.cfg.Store.AIRun(ctx, idA)
	if err != nil {
		return nil, err
	}
	rb, err := a.cfg.Store.AIRun(ctx, idB)
	if err != nil {
		return nil, err
	}
	if ra.SessionID != rb.SessionID {
		return nil, domain.NewUserError("INPUT", "같은 영상의 AI 검토끼리만 비교할 수 있습니다.")
	}
	recsA, recsB := dedupeRecs(runRecs(ra)), dedupeRecs(runRecs(rb))
	var pa, pb ai.Plan
	json.Unmarshal(ra.PlanJSON, &pa)
	json.Unmarshal(rb.PlanJSON, &pb)
	out := &AIComparison{A: a.aiRunView(ra, len(recsA)), B: a.aiRunView(rb, len(recsB)),
		SameInput: string(ai.Encode(pa.Chunks)) == string(ai.Encode(pb.Chunks)),
		Matched:   []AIMatch{}, OnlyA: []ai.Recommendation{}, OnlyB: []ai.Recommendation{}}
	out.Matched, out.OnlyA, out.OnlyB = matchRecs(recsA, recsB)
	return out, nil
}

// matchRecs pairs each recommendation of a with the unpaired one of b that
// overlaps it most (in a's time order).
func matchRecs(a, b []ai.Recommendation) (matched []AIMatch, onlyA, onlyB []ai.Recommendation) {
	matched, onlyA, onlyB = []AIMatch{}, []ai.Recommendation{}, []ai.Recommendation{}
	used := make([]bool, len(b))
	for _, ra := range a {
		best, bestOverlap := -1, int64(0)
		for j, rb := range b {
			if used[j] {
				continue
			}
			if o := min(ra.EndMs, rb.EndMs) - max(ra.StartMs, rb.StartMs); o > bestOverlap {
				best, bestOverlap = j, o
			}
		}
		if best < 0 {
			onlyA = append(onlyA, ra)
			continue
		}
		used[best] = true
		matched = append(matched, AIMatch{A: ra, B: b[best], OverlapMs: bestOverlap})
	}
	for j, rb := range b {
		if !used[j] {
			onlyB = append(onlyB, rb)
		}
	}
	sort.SliceStable(matched, func(i, j int) bool { return matched[i].A.StartMs < matched[j].A.StartMs })
	return matched, onlyA, onlyB
}
