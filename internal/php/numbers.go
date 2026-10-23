// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"math"
	"strconv"
	"strings"
)

// Rounding modes of round().
const (
	RoundHalfUp = iota + 1
	RoundHalfDown
	RoundHalfEven
	RoundHalfOdd
)

func intPow10(power int) float64 {
	if power < 0 || power > 22 {
		return math.Pow(10, float64(power))
	}
	return [...]float64{1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13, 1e14, 1e15, 1e16,
		1e17, 1e18, 1e19, 1e20, 1e21, 1e22}[power]
}

func absInt32(i int) int {
	if i < 0 {
		return -i
	}
	return i
}

func roundHelper(value float64, mode int) float64 {
	switch mode {
	case RoundHalfDown:
		if value >= 0 {
			return math.Ceil(value - 0.5)
		}
		return math.Floor(value + 0.5)
	case RoundHalfEven, RoundHalfOdd:
		t := math.Floor(value + 0.5)
		if value < 0 {
			t = math.Ceil(value - 0.5)
		}
		if math.Abs(t-value) == 0.5 {
			even := math.Mod(t, 2) == 0
			if (mode == RoundHalfEven) != even {
				if value < 0 {
					t++
				} else {
					t--
				}
			}
		}
		return t
	}
	if value >= 0 {
		return math.Floor(value + 0.5)
	}
	return math.Ceil(value - 0.5)
}

// RoundFloat is _php_math_round of PHP 8.3, including its "pre-rounding" to 15 significant digits, which makes
// round(1.955, 2) give 1.96.
func RoundFloat(value float64, places int, mode int) float64 {
	if math.IsInf(value, 0) || math.IsNaN(value) || value == 0 {
		return value
	}
	precisionPlaces := 14 - int(math.Floor(math.Log10(math.Abs(value))))
	f1 := intPow10(absInt32(places))
	var tmp float64
	if precisionPlaces > places && precisionPlaces-15 < places {
		usePrecision := precisionPlaces
		if usePrecision < math.MinInt32+1 {
			usePrecision = math.MinInt32 + 1
		}
		f := intPow10(absInt32(usePrecision))
		if usePrecision >= 0 {
			tmp = value * f
		} else {
			tmp = value / f
		}
		tmp = roundHelper(tmp, mode)
		usePrecision = places - usePrecision
		if usePrecision < math.MinInt32+1 {
			usePrecision = math.MinInt32 + 1
		}
		tmp = tmp / intPow10(absInt32(usePrecision))
	} else {
		if places >= 0 {
			tmp = value * f1
		} else {
			tmp = value / f1
		}
		if math.Abs(tmp) >= 1e15 {
			return value
		}
	}
	tmp = roundHelper(tmp, mode)
	if absInt32(places) < 23 {
		if places > 0 {
			tmp = tmp / f1
		} else {
			tmp = tmp * f1
		}
	} else {
		s := strconv.FormatFloat(tmp, 'f', 6, 64) + "e" + strconv.Itoa(-places)
		t, err := strconv.ParseFloat(s, 64)
		if err != nil || math.IsInf(t, 0) || math.IsNaN(t) {
			return value
		}
		tmp = t
	}
	return tmp
}

// PHPRound is PHP's round($num, $precision, $mode) for an int|float value (the result is always a float).
func PHPRound(value any, places int64, mode int) any {
	p := int(places)
	if places > math.MaxInt32 {
		p = math.MaxInt32
	} else if places < math.MinInt32 {
		p = math.MinInt32
	}
	if i, ok := value.(int64); ok {
		if p >= 0 {
			return float64(i)
		}
		return RoundFloat(float64(i), p, mode)
	}
	return RoundFloat(ToFloat(value), p, mode)
}

// NumberFormat is number_format($num, $decimals, $decimal_separator, $thousands_separator).
func NumberFormat(d float64, decimals int64, point, sep string) string {
	dec := int(decimals)
	if decimals > math.MaxInt32 {
		dec = math.MaxInt32
	} else if decimals < math.MinInt32 {
		dec = math.MinInt32
	}
	d = RoundFloat(d, dec, RoundHalfUp)
	if dec < 0 {
		dec = 0
	}
	neg := false
	if d < 0 {
		neg = true
		d = -d
	}
	if dec > 1000 {
		dec = 1000
	}
	tmp := phpFormatF(math.Abs(d), dec)
	if neg && d == 0 {
		neg = false
	}
	if tmp == "" || tmp[0] < '0' || tmp[0] > '9' {
		// INF and NAN
		if neg {
			return "-" + tmp
		}
		return tmp
	}
	intPart, frac := tmp, ""
	if i := strings.IndexByte(tmp, '.'); i >= 0 {
		intPart, frac = tmp[:i], tmp[i+1:]
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteByte(intPart[i])
	}
	if dec > 0 {
		b.WriteString(point)
		b.WriteString(frac)
		if len(frac) < dec {
			b.WriteString(strings.Repeat("0", dec-len(frac)))
		}
	}
	return b.String()
}

// phpFormatF is PHP's "%.*F" for a non-negative value.
func phpFormatF(d float64, prec int) string {
	switch {
	case math.IsNaN(d):
		return "NAN"
	case math.IsInf(d, 0):
		return "INF"
	}
	return strconv.FormatFloat(d, 'f', prec, 64)
}

// Round is CoreExtension::round: the value is cast to float; "common" is PHP's round(), "ceil" and "floor" scale
// by 10 ** precision; any other method is a RuntimeError.
func Round(v any, precision any, method string) (any, error) {
	value := ToFloat(v)
	if method == "common" {
		p, err := IntArg("round", 2, "precision", precision)
		if err != nil {
			return nil, err
		}
		return PHPRound(value, p, RoundHalfUp), nil
	}
	if method != "ceil" && method != "floor" {
		return nil, Runtime("The \"round\" filter only supports the \"common\", \"ceil\", and \"floor\" methods.")
	}
	scale, err := Pow(int64(10), precision)
	if err != nil {
		return nil, err
	}
	scaled, err := Mul(value, scale)
	if err != nil {
		return nil, err
	}
	f := ToFloat(scaled)
	if method == "ceil" {
		f = math.Ceil(f)
	} else {
		f = math.Floor(f)
	}
	return Div(f, scale)
}
