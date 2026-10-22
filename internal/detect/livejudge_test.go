package detect

import (
	"testing"

	"github.com/manwol-ryu/momentforge/internal/domain"
)

func moment(id string, start, end int64) domain.Candidate {
	return domain.Candidate{ID: id, ReactionStartMs: start, ReactionEndMs: end, PeakMs: start, ClipStartMs: start - 30_000, ClipEndMs: end + 30_000}
}

func byID(cs []domain.Candidate) map[string]domain.Candidate {
	out := map[string]domain.Candidate{}
	for _, c := range cs {
		out[c.ID] = c
	}
	return out
}

const minute = 60_000

// A moment is judged once 5 minutes of chat followed it, keeps its ID while
// later analyses find it, and is settled once 30 minutes did.
func TestJudgeLiveTiers(t *testing.T) {
	p := DefaultParams()
	early := moment("m0001", 10*minute, 10*minute+20_000)
	early.Tier, early.State = domain.TierEarly, domain.CandidateConfirmed
	forming := moment("m0002", 18*minute, 18*minute+10_000)
	forming.Tier, forming.State = domain.TierEarly, domain.CandidateGenerating
	lone := moment("m0003", 12*minute, 12*minute+10_000)
	lone.Tier, lone.State = domain.TierEarly, domain.CandidateConfirmed

	// At 16:00 the 10:00 reaction is judged and takes the early one's place;
	// the 15:00 one is too recent; the lone early one waits for its 5 minutes.
	found := []domain.Candidate{moment("m0001", 10*minute+5_000, 10*minute+25_000), moment("m0002", 15*minute, 15*minute+10_000)}
	out := byID(JudgeLive([]domain.Candidate{early, forming, lone}, found, 0, 16*minute, false, p))
	if b := out["b0001"]; b.Tier != domain.TierJudged || b.State != domain.CandidateConfirmed || b.ReactionStartMs != 10*minute+5_000 {
		t.Fatalf("judged = %+v", b)
	}
	if e := out["m0001"]; e.State != domain.CandidateDropped || e.ReplacedBy != "b0001" {
		t.Errorf("early = %+v", e)
	}
	if _, ok := out["m0002"]; ok {
		t.Error("a forming early candidate was judged")
	}
	if _, ok := out["m0003"]; ok {
		t.Error("an early candidate was dropped before its 5 minutes")
	}
	if len(out) != 2 {
		t.Errorf("out = %v", out)
	}

	// Later analyses move it a little: same ID; the lone early one is dropped.
	stored := []domain.Candidate{out["b0001"], out["m0001"], forming, lone}
	out = byID(JudgeLive(stored, []domain.Candidate{moment("m0001", 10*minute, 10*minute+30_000)}, 0, 20*minute, false, p))
	if b := out["b0001"]; b.Tier != domain.TierJudged || b.ReactionEndMs != 10*minute+30_000 {
		t.Errorf("judged again = %+v", b)
	}
	if e := out["m0003"]; e.State != domain.CandidateDropped || e.ReplacedBy != "" {
		t.Errorf("lone early = %+v", e)
	}

	// 30 minutes after it: settled, and never revised again.
	stored = []domain.Candidate{out["b0001"]}
	out = byID(JudgeLive(stored, []domain.Candidate{moment("m0001", 10*minute, 10*minute+30_000)}, 0, 41*minute, false, p))
	if b := out["b0001"]; b.Tier != domain.TierFinal {
		t.Fatalf("settled = %+v", b)
	}
	out = byID(JudgeLive([]domain.Candidate{out["b0001"]}, []domain.Candidate{moment("m0001", 9*minute, 11*minute)}, 0, 50*minute, false, p))
	if len(out) != 0 {
		t.Errorf("a settled moment was revised: %v", out)
	}
}

// A judged moment no later analysis finds is dropped, and comes back with
// its ID when one does; numbering continues after the stored candidates.
func TestJudgeLiveDropsAndRevives(t *testing.T) {
	p := DefaultParams()
	judged := moment("b0007", 10*minute, 10*minute+20_000)
	judged.Tier, judged.State = domain.TierJudged, domain.CandidateConfirmed
	out := byID(JudgeLive([]domain.Candidate{judged}, nil, 0, 20*minute, false, p))
	if b := out["b0007"]; b.State != domain.CandidateDropped {
		t.Fatalf("not found = %+v", b)
	}
	out = byID(JudgeLive([]domain.Candidate{out["b0007"]},
		[]domain.Candidate{moment("m0001", 10*minute, 10*minute+20_000), moment("m0002", 13*minute, 13*minute+20_000)}, 0, 21*minute, false, p))
	if b := out["b0007"]; b.State != domain.CandidateConfirmed || b.Tier != domain.TierJudged {
		t.Errorf("revived = %+v", b)
	}
	if b := out["b0008"]; b.Tier != domain.TierJudged {
		t.Errorf("new = %+v", b)
	}
}

// When the capture ends every moment found is settled, recent ones too,
// and every early candidate left is dropped; moments too early in a
// partial read are left to the analyses that saw chat before them.
func TestJudgeLiveFinal(t *testing.T) {
	p := DefaultParams()
	forming := moment("m0004", 99*minute, 99*minute+10_000)
	forming.Tier, forming.State = domain.TierEarly, domain.CandidateWaitingContext
	from := 100*minute - p.LiveJudgeSpanMs()
	found := []domain.Candidate{moment("m0001", from+minute, from+minute+10_000), moment("m0002", 99*minute, 99*minute+20_000)}
	out := byID(JudgeLive([]domain.Candidate{forming}, found, from, 100*minute, true, p))
	if b := out["b0001"]; b.Tier != domain.TierFinal || b.ReactionStartMs != 99*minute {
		t.Errorf("final = %+v", b)
	}
	if e := out["m0004"]; e.State != domain.CandidateDropped || e.ReplacedBy != "b0001" {
		t.Errorf("early = %+v", e)
	}
	if len(out) != 2 {
		t.Errorf("a moment before the read's reliable part was judged: %v", out)
	}
}

func TestEarlyParams(t *testing.T) {
	p := DefaultParams()
	e := p.Early()
	if e.GateBusyRatio >= p.GateBusyRatio || e.GateSpikeRatio >= p.GateSpikeRatio || !p.LiveTiers() {
		t.Errorf("early = %v/%v", e.GateBusyRatio, e.GateSpikeRatio)
	}
	if c := CzzEditParams(); c.Early().GateBusyRatio != c.GateBusyRatio || c.LiveTiers() {
		t.Error("CzzEdit params changed")
	}
	p.EarlyGateScale = 1.5
	if p.Validate() == nil {
		t.Error("a scale above 1 passed")
	}
}
