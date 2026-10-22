package detect

import (
	"math"
	"slices"
)

// level is what a count usually is around some time: the mean and variance
// of reference bins after capping the top WinsorQuantile of them, so the
// reactions inside the reference span barely raise their own baseline. It
// is the number a window is compared with (REDESIGN 6.2.2).
type level struct {
	Mean, Var float64
	Bins      int
}

// levelOf summarizes reference bins. buf is scratch space (reused).
func levelOf(vals []float64, quantile float64, buf []float64) (level, []float64) {
	if len(vals) == 0 {
		return level{}, buf
	}
	buf = append(buf[:0], vals...)
	slices.Sort(buf)
	capAt := buf[int(quantile*float64(len(buf)-1))]
	sum := 0.0
	for _, v := range buf {
		sum += math.Min(v, capAt)
	}
	mean := sum / float64(len(buf))
	ss := 0.0
	for _, v := range buf {
		d := math.Min(v, capAt) - mean
		ss += d * d
	}
	variance := 0.0
	if len(buf) > 1 {
		variance = ss / float64(len(buf)-1)
	}
	return level{Mean: mean, Var: variance, Bins: len(buf)}, buf
}

// surprise reports how unusual x is for a count that is usually mean with
// the given variance, on the scale of a standard normal z-score (one-sided).
// Counts are modeled as negative binomial (Poisson when the variance is not
// above the mean), which suits both a quiet channel, where a burst of six
// laughs is rare, and a busy one, whose chat swings far more than Poisson.
// The mid-p tail P(X>x)+½P(X=x) avoids the jumps of discrete counts. x at
// or below the mean is 0. mean is raised to floor first.
//
// The tail comes from the regularized incomplete beta (negative binomial)
// or gamma (Poisson) function by continued fraction or series, so it costs
// about √x steps however large the chat.
func surprise(x int, mean, variance, floor float64) float64 {
	mean = math.Max(mean, floor)
	fx := float64(x)
	if fx <= mean {
		return 0
	}
	var logPMF, logUpper float64 // log P(X=x), log P(X≥x+1)
	if k := mean * mean / (variance - mean); variance > mean && k < 1e7 {
		// P(X=i) = C(i+k-1, i) q^k p^i with p = mean/(k+mean), q = 1-p;
		// P(X≥m) = I_p(m, k).
		lp, lq := math.Log(mean/(k+mean)), math.Log(k/(k+mean))
		a, _ := math.Lgamma(k + fx)
		b, _ := math.Lgamma(k)
		c, _ := math.Lgamma(fx + 1)
		logPMF = a - b - c + k*lq + fx*lp
		logUpper = logBetaInc(fx+1, k, mean/(k+mean))
	} else {
		lg, _ := math.Lgamma(fx + 1)
		logPMF = -mean + fx*math.Log(mean) - lg
		logUpper = logGammaLower(fx+1, mean) // P(X≥m) = P(m, mean)
	}
	logTail := logAddExp(logUpper, logPMF+math.Log(0.5))
	if logTail >= math.Log(0.5) {
		return 0
	}
	return zFromLogTail(logTail)
}

func logAddExp(a, b float64) float64 {
	if a < b {
		a, b = b, a
	}
	if math.IsInf(b, -1) {
		return a
	}
	return a + math.Log1p(math.Exp(b-a))
}

// logBetaInc is log I_x(a, b), the regularized incomplete beta function,
// by Lentz's continued fraction (Numerical Recipes 6.4). The upper tails
// surprise needs fall on the side where the fraction converges directly.
func logBetaInc(a, b, x float64) float64 {
	if x <= 0 {
		return math.Inf(-1)
	}
	if x >= 1 {
		return 0
	}
	lab, _ := math.Lgamma(a + b)
	la, _ := math.Lgamma(a)
	lb, _ := math.Lgamma(b)
	front := lab - la - lb + a*math.Log(x) + b*math.Log1p(-x)
	if x < (a+1)/(a+b+2) {
		return front + math.Log(betaCF(a, b, x)/a)
	}
	rest := math.Exp(front) * betaCF(b, a, 1-x) / b
	return math.Log(math.Max(1-rest, math.SmallestNonzeroFloat64))
}

func betaCF(a, b, x float64) float64 {
	const tiny, eps = 1e-300, 1e-14
	qab, qap, qam := a+b, a+1, a-1
	c, d := 1.0, 1-qab*x/qap
	if math.Abs(d) < tiny {
		d = tiny
	}
	d = 1 / d
	h := d
	for m := 1.0; m < 100_000; m++ {
		m2 := 2 * m
		aa := m * (b - m) * x / ((qam + m2) * (a + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		h *= d * c
		aa = -(a + m) * (qab + m) * x / ((a + m2) * (qap + m2))
		d = 1 + aa*d
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = 1 + aa/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < eps {
			break
		}
	}
	return h
}

// logGammaLower is log P(a, x), the regularized lower incomplete gamma
// function (Numerical Recipes 6.2): the series when x < a+1 (the small
// upper tails of a Poisson count well above its mean), else 1 - Q by
// continued fraction.
func logGammaLower(a, x float64) float64 {
	if x <= 0 {
		return math.Inf(-1)
	}
	lga, _ := math.Lgamma(a)
	front := -x + a*math.Log(x) - lga
	if x < a+1 {
		sum, del := 1/a, 1/a
		for n := 1.0; n < 100_000; n++ {
			del *= x / (a + n)
			sum += del
			if del < sum*1e-15 {
				break
			}
		}
		return front + math.Log(sum)
	}
	const tiny = 1e-300
	b := x + 1 - a
	c, d := 1/tiny, 1/b
	h := d
	for i := 1.0; i < 100_000; i++ {
		an := -i * (i - a)
		b += 2
		d = an*d + b
		if math.Abs(d) < tiny {
			d = tiny
		}
		c = b + an/c
		if math.Abs(c) < tiny {
			c = tiny
		}
		d = 1 / d
		del := d * c
		h *= del
		if math.Abs(del-1) < 1e-15 {
			break
		}
	}
	return math.Log(math.Max(1-math.Exp(front)*h, math.SmallestNonzeroFloat64))
}

// zFromLogTail converts a one-sided upper tail probability (as a log) to
// the standard normal quantile. Very small tails use the asymptotic
// expansion, which math.Erfcinv cannot reach.
func zFromLogTail(logTail float64) float64 {
	// math.Erfcinv(x) is computed as Erfinv(1-x), exact only while 1-x is
	// representable; past about 1e-12 use the asymptotic expansion.
	if logTail > -27 {
		return math.Sqrt2 * math.Erfcinv(2*math.Exp(logTail))
	}
	// tail ≈ φ(z)/z ⇒ z² ≈ -2·logTail - log(2π) - 2·log z (fixed point).
	z := math.Sqrt(-2 * logTail)
	for range 4 {
		z = math.Sqrt(math.Max(-2*logTail-math.Log(2*math.Pi)-2*math.Log(z), 1))
	}
	return z
}
