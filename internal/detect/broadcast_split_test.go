package detect

import "testing"

// A steady plateau with no valley is cut into parts whose reactions span at
// most MaxSpanMs, even when a window raised only by its 20 s count reaches
// back before the part's first window.
func TestSplitKeepsPartsWithinMaxSpan(t *testing.T) {
	p := DefaultParams()
	b := &broadcast{p: p, bp: *p.Broadcast}
	for i := range 61 {
		w := bwindow{span: p.WindowMs, score: 4, short: 4}
		w.w.T = 100_000 + int64(i)*p.StepMs
		w.w.Valid = true
		b.wins = append(b.wins, w)
	}
	b.wins[1].span, b.wins[1].short = 2*p.WindowMs, 0
	parts := b.split(region{0, len(b.wins) - 1}, b.bp.OnZ)
	if len(parts) < 2 {
		t.Fatalf("parts %v", parts)
	}
	next := 0
	for _, r := range parts {
		if r.from != next {
			t.Fatalf("parts %v leave out windows", parts)
		}
		next = r.to + 1
		if e := b.extent(r); e.to-e.from > p.MaxSpanMs {
			t.Fatalf("part %v spans %d ms", r, e.to-e.from)
		}
	}
}
