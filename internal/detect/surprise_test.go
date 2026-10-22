package detect

import (
	"math"
	"testing"
)

func TestSurprise(t *testing.T) {
	near := func(got, want float64) bool { return math.Abs(got-want) < 0.01 }
	// Poisson(1): P(X>5)+½P(X=5) = 0.000594 + 0.5·0.003066 → z 2.86.
	if z := surprise(5, 1, 1, 0.1); !near(z, 2.859) {
		t.Errorf("Poisson z = %.3f", z)
	}
	// Overdispersed counts have heavier tails, so the same count is less unusual.
	if p, nb := surprise(9, 2.8, 2.8, 0.1), surprise(9, 2.8, 6, 0.1); nb >= p || nb <= 0 {
		t.Errorf("Poisson %.2f, negative binomial %.2f", p, nb)
	}
	// At or below the usual count nothing is unusual; more is always more.
	if surprise(2, 3, 3, 0.1) != 0 || surprise(3, 3, 3, 0.1) != 0 {
		t.Error("counts at or below the mean must score 0")
	}
	prev := 0.0
	for x := 4; x < 60; x++ {
		z := surprise(x, 3, 5, 0.1)
		if z <= prev || math.IsInf(z, 0) || math.IsNaN(z) {
			t.Fatalf("x=%d: z %.3f after %.3f", x, z, prev)
		}
		prev = z
	}
	// The floor stands in for an empty baseline; extreme tails stay finite.
	if a, b := surprise(3, 0, 0, 0.25), surprise(3, 0.25, 0.25, 0.25); a != b || a < 3 {
		t.Errorf("floored %.3f vs %.3f", a, b)
	}
	if z := surprise(2000, 50, 50, 0.1); math.IsInf(z, 0) || z < 50 {
		t.Errorf("extreme z = %f", z)
	}
}

// The closed forms match summing the probabilities term by term.
func TestSurpriseMatchesDirectSum(t *testing.T) {
	direct := func(x int, mean, variance float64) float64 {
		logPMF := func(i float64) float64 {
			if variance <= mean {
				lg, _ := math.Lgamma(i + 1)
				return -mean + i*math.Log(mean) - lg
			}
			k := mean * mean / (variance - mean)
			a, _ := math.Lgamma(k + i)
			b, _ := math.Lgamma(k)
			c, _ := math.Lgamma(i + 1)
			return a - b - c + k*math.Log(k/(k+mean)) + i*math.Log(mean/(k+mean))
		}
		fx := float64(x)
		first := logPMF(fx)
		sum := 0.5
		for i := fx + 1; i < fx+1e6; i++ {
			term := math.Exp(logPMF(i) - first)
			sum += term
			if term < sum*1e-14 {
				break
			}
		}
		return zFromLogTail(first + math.Log(sum))
	}
	for _, c := range []struct {
		x    int
		m, v float64
	}{{5, 1, 1}, {6, 0.57, 0.9}, {14, 3, 3}, {14, 3, 3.1}, {14, 3, 12}, {9, 2.8, 6}, {40, 10, 50}, {400, 280, 280 + 280*280}, {700, 280, 2000}, {3, 0.25, 0.25}, {30, 2, 6}} {
		got, want := surprise(c.x, c.m, c.v, 0.1), direct(c.x, c.m, c.v)
		if math.Abs(got-want) > 0.002 {
			t.Errorf("x=%d mean=%g var=%g: %.4f, direct sum %.4f", c.x, c.m, c.v, got, want)
		}
	}
	// No step where the variance passes the mean.
	if a, b := surprise(14, 3, 3, 0.1), surprise(14, 3, 3.0001, 0.1); math.Abs(a-b) > 0.001 {
		t.Errorf("Poisson %.4f vs barely overdispersed %.4f", a, b)
	}
}

func TestLevelOfCapsTheTop(t *testing.T) {
	vals := []float64{1, 1, 2, 2, 2, 3, 3, 3, 4, 40}
	l, _ := levelOf(vals, 0.8, nil)
	if l.Bins != 10 || l.Mean >= 4 || l.Mean <= 2 {
		t.Fatalf("level %+v: the 40 must be capped", l)
	}
	if l, _ := levelOf(nil, 0.8, nil); l.Bins != 0 {
		t.Fatalf("empty level %+v", l)
	}
}

func TestBroadcastParamsValidate(t *testing.T) {
	if err := DefaultParams().Validate(); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(b *BroadcastParams){
		func(b *BroadcastParams) { b.OnZ = b.OffZ - 1 },
		func(b *BroadcastParams) { b.StrongZ = b.MediumZ - 1 },
		func(b *BroadcastParams) { b.ExcludeMs = b.LocalMs },
		func(b *BroadcastParams) { b.StrongShare = b.MediumShare + 0.1 },
		func(b *BroadcastParams) { b.WinsorQuantile = 0 },
	} {
		p := DefaultParams()
		b := *p.Broadcast
		change(&b)
		p.Broadcast = &b
		if p.Validate() == nil {
			t.Errorf("accepted %+v", b)
		}
	}
}
