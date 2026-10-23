// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"math"
	"math/big"
)

// PHP's ** on floats is the C library's pow(), which glibc rounds correctly; Go's math.Pow can be a few units in
// the last place off. powFloat evaluates x**y with 256-bit intermediates and rounds once.

const powPrec = 256

func powFloat(x, y float64) float64 {
	switch {
	case y == 0 || x == 1:
		return 1
	case math.IsNaN(x) || math.IsNaN(y) || math.IsInf(x, 0) || math.IsInf(y, 0) || x == 0:
		return math.Pow(x, y)
	}
	neg := false
	if x < 0 {
		if y != math.Trunc(y) {
			return math.Float64frombits(0x7ff8000000000000)
		}
		// An odd integral exponent keeps the sign.
		neg = math.Mod(y, 2) != 0
		x = -x
	}
	var r *big.Float
	if y == math.Trunc(y) && math.Abs(y) <= 1<<53 {
		r = bigPowInt(x, int64(math.Abs(y)))
		if y < 0 {
			r = new(big.Float).SetPrec(powPrec).Quo(big.NewFloat(1).SetPrec(powPrec), r)
		}
	} else {
		l := bigLog(x)
		l.Mul(l, new(big.Float).SetPrec(powPrec).SetFloat64(y))
		// Results far outside the float64 range need no precision.
		if f, _ := l.Float64(); f > 710 {
			return signed(math.Inf(1), neg)
		} else if f < -746 {
			return signed(0, neg)
		}
		r = bigExp(l)
	}
	f, _ := r.Float64()
	return signed(f, neg)
}

func signed(f float64, neg bool) float64 {
	if neg {
		return -f
	}
	return f
}

func bigPowInt(x float64, n int64) *big.Float {
	result := new(big.Float).SetPrec(powPrec).SetInt64(1)
	base := new(big.Float).SetPrec(powPrec).SetFloat64(x)
	for n > 0 {
		if n&1 == 1 {
			result.Mul(result, base)
		}
		n >>= 1
		if n > 0 {
			base.Mul(base, base)
		}
	}
	return result
}

var bigLn2 = func() *big.Float {
	// ln 2 = 2 atanh(1/3)
	z := new(big.Float).SetPrec(powPrec+64).Quo(big.NewFloat(1), big.NewFloat(3))
	return atanhSeries(z)
}()

// atanhSeries returns 2*atanh(z) = ln((1+z)/(1-z)) for small |z|.
func atanhSeries(z *big.Float) *big.Float {
	prec := z.Prec()
	sum := new(big.Float).SetPrec(prec).Set(z)
	z2 := new(big.Float).SetPrec(prec).Mul(z, z)
	term := new(big.Float).SetPrec(prec).Set(z)
	eps := new(big.Float).SetPrec(prec).SetMantExp(big.NewFloat(1), -int(prec)-8)
	for k := int64(3); ; k += 2 {
		term.Mul(term, z2)
		t := new(big.Float).SetPrec(prec).Quo(term, new(big.Float).SetPrec(prec).SetInt64(k))
		sum.Add(sum, t)
		if t.Sign() == 0 || new(big.Float).Abs(t).Cmp(eps) < 0 {
			break
		}
	}
	return sum.Mul(sum, big.NewFloat(2))
}

// bigLog is ln(x) for x > 0.
func bigLog(x float64) *big.Float {
	frac, exp := math.Frexp(x) // x = frac * 2**exp, frac in [0.5, 1)
	m := new(big.Float).SetPrec(powPrec + 64).SetFloat64(frac)
	one := new(big.Float).SetPrec(powPrec + 64).SetInt64(1)
	num := new(big.Float).SetPrec(powPrec+64).Sub(m, one)
	den := new(big.Float).SetPrec(powPrec+64).Add(m, one)
	z := num.Quo(num, den)
	l := atanhSeries(z)
	e := new(big.Float).SetPrec(powPrec + 64).SetInt64(int64(exp))
	return l.Add(l, e.Mul(e, bigLn2))
}

// bigExp is e**t.
func bigExp(t *big.Float) *big.Float {
	prec := uint(powPrec + 64)
	kf := new(big.Float).SetPrec(prec).Quo(t, bigLn2)
	k, _ := kf.Float64()
	ki := int64(math.Round(k))
	r := new(big.Float).SetPrec(prec).Sub(t, new(big.Float).SetPrec(prec).Mul(bigLn2, new(big.Float).SetInt64(ki)))
	sum := new(big.Float).SetPrec(prec).SetInt64(1)
	term := new(big.Float).SetPrec(prec).SetInt64(1)
	eps := new(big.Float).SetPrec(prec).SetMantExp(big.NewFloat(1), -int(prec)-8)
	for n := int64(1); n < 400; n++ {
		term.Mul(term, r)
		term.Quo(term, new(big.Float).SetPrec(prec).SetInt64(n))
		sum.Add(sum, term)
		if new(big.Float).Abs(term).Cmp(eps) < 0 {
			break
		}
	}
	return sum.SetMantExp(sum, int(ki))
}
