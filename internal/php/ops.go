// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"math"
	"strings"
)

func unsupported(a, b any, op string) error {
	return Throw("TypeError", "Unsupported operand types: %s %s %s", TypeName(a), op, TypeName(b))
}

// operand converts one arithmetic operand: null and bool become ints, numeric and leading-numeric strings become
// numbers, anything else (non-numeric strings, arrays, objects) is unsupported.
func operand(v any) (any, bool) {
	switch x := v.(type) {
	case nil:
		return int64(0), true
	case bool:
		if x {
			return int64(1), true
		}
		return int64(0), true
	case int64, float64:
		return x, true
	case string:
		n, k := ParseNumeric(x)
		if k == NotNumeric {
			return nil, false
		}
		return n, true
	}
	// Arrays and objects (Twig\Markup included) are unsupported operands.
	return nil, false
}

func operands(a, b any, op string) (any, any, error) {
	x, ok1 := operand(a)
	y, ok2 := operand(b)
	if !ok1 || !ok2 {
		return nil, nil, unsupported(a, b, op)
	}
	return x, y, nil
}

// Add is $a + $b, including array union.
func Add(a, b any) (any, error) {
	if aa, ok := a.(*Array); ok {
		if bb, ok := b.(*Array); ok {
			r := aa.Copy()
			bb.Each(func(k, v any) bool {
				if !r.Has(k) {
					r.Set(k, v)
				}
				return true
			})
			return r, nil
		}
	}
	x, y, err := operands(a, b, "+")
	if err != nil {
		return nil, err
	}
	if i, ok := x.(int64); ok {
		if j, ok := y.(int64); ok {
			s := i + j
			if (s > i) == (j > 0) {
				return s, nil
			}
			return float64(i) + float64(j), nil
		}
	}
	return ToFloat(x) + ToFloat(y), nil
}

// Sub is $a - $b.
func Sub(a, b any) (any, error) {
	x, y, err := operands(a, b, "-")
	if err != nil {
		return nil, err
	}
	if i, ok := x.(int64); ok {
		if j, ok := y.(int64); ok {
			s := i - j
			if (s < i) == (j > 0) {
				return s, nil
			}
			return float64(i) - float64(j), nil
		}
	}
	return ToFloat(x) - ToFloat(y), nil
}

func mulInt(i, j int64) (int64, bool) {
	if i == 0 || j == 0 {
		return 0, true
	}
	if (i == -1 && j == math.MinInt64) || (j == -1 && i == math.MinInt64) {
		return 0, false
	}
	p := i * j
	if p/j != i {
		return 0, false
	}
	return p, true
}

// Mul is $a * $b.
func Mul(a, b any) (any, error) {
	x, y, err := operands(a, b, "*")
	if err != nil {
		return nil, err
	}
	if i, ok := x.(int64); ok {
		if j, ok := y.(int64); ok {
			if p, ok := mulInt(i, j); ok {
				return p, nil
			}
			return float64(i) * float64(j), nil
		}
	}
	return ToFloat(x) * ToFloat(y), nil
}

// Div is $a / $b: an int when both are ints and the division is exact.
func Div(a, b any) (any, error) {
	x, y, err := operands(a, b, "/")
	if err != nil {
		return nil, err
	}
	if ToFloat(y) == 0 {
		return nil, Throw("DivisionByZeroError", "Division by zero")
	}
	if i, ok := x.(int64); ok {
		if j, ok := y.(int64); ok {
			if !(i == math.MinInt64 && j == -1) && i%j == 0 {
				return i / j, nil
			}
			return float64(i) / float64(j), nil
		}
	}
	return ToFloat(x) / ToFloat(y), nil
}

// FloorDiv is Twig's `//`: (int) floor($a / $b).
func FloorDiv(a, b any) (any, error) {
	q, err := Div(a, b)
	if err != nil {
		return nil, err
	}
	return FloatToInt(math.Floor(ToFloat(q))), nil
}

// Mod is $a % $b on integers (floats are truncated).
func Mod(a, b any) (any, error) {
	x, y, err := operands(a, b, "%")
	if err != nil {
		return nil, err
	}
	i, j := ToInt(x), ToInt(y)
	if j == 0 {
		return nil, Throw("DivisionByZeroError", "Modulo by zero")
	}
	if j == -1 {
		return int64(0), nil
	}
	return i % j, nil
}

// Pow is $a ** $b.
func Pow(a, b any) (any, error) {
	x, y, err := operands(a, b, "**")
	if err != nil {
		return nil, err
	}
	if i, ok := x.(int64); ok {
		if j, ok := y.(int64); ok && j >= 0 {
			// pow_function_base: square-and-multiply in integers; on overflow the rest is done with pow().
			l1, l2 := int64(1), i
			if j == 0 {
				return int64(1), nil
			}
			if l2 == 0 {
				return int64(0), nil
			}
			for j >= 1 {
				if j%2 != 0 {
					j--
					p, ok := mulInt(l1, l2)
					if !ok {
						return float64(l1) * float64(l2) * powFloat(float64(l2), float64(j)), nil
					}
					l1 = p
				} else {
					j /= 2
					p, ok := mulInt(l2, l2)
					if !ok {
						return float64(l1) * powFloat(float64(l2)*float64(l2), float64(j)), nil
					}
					l2 = p
				}
			}
			return l1, nil
		}
	}
	return powFloat(ToFloat(x), ToFloat(y)), nil
}

// Neg is unary minus, which PHP compiles as $a * -1.
func Neg(a any) (any, error) {
	if i, ok := a.(int64); ok && i == math.MinInt64 {
		return -float64(i), nil
	}
	return Mul(a, int64(-1))
}

// Pos is unary plus ($a * 1).
func Pos(a any) (any, error) {
	return Mul(a, int64(1))
}

func bitOperands(a, b any, op string) (int64, int64, error) {
	x, y, err := operands(a, b, op)
	if err != nil {
		return 0, 0, err
	}
	return ToInt(x), ToInt(y), nil
}

func bitStrings(a, b any, f func(x, y byte) byte, longest bool) (string, bool) {
	s, ok1 := a.(string)
	t, ok2 := b.(string)
	if !ok1 || !ok2 {
		return "", false
	}
	if len(s) > len(t) == !longest {
		s, t = t, s
	}
	// s is now the result length (shorter for & and ^, longer for |).
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if i < len(t) {
			out.WriteByte(f(s[i], t[i]))
		} else {
			out.WriteByte(s[i])
		}
	}
	return out.String(), true
}

// BitAnd is $a & $b (bytewise on two strings).
func BitAnd(a, b any) (any, error) {
	if s, ok := bitStrings(a, b, func(x, y byte) byte { return x & y }, false); ok {
		return s, nil
	}
	x, y, err := bitOperands(a, b, "&")
	return x & y, err
}

// BitOr is $a | $b.
func BitOr(a, b any) (any, error) {
	if s, ok := bitStrings(a, b, func(x, y byte) byte { return x | y }, true); ok {
		return s, nil
	}
	x, y, err := bitOperands(a, b, "|")
	return x | y, err
}

// BitXor is $a ^ $b.
func BitXor(a, b any) (any, error) {
	if s, ok := bitStrings(a, b, func(x, y byte) byte { return x ^ y }, false); ok {
		return s, nil
	}
	x, y, err := bitOperands(a, b, "^")
	return x ^ y, err
}

// Concat is $a . $b.
func Concat(a, b any) (string, error) {
	s, err := ToString(a)
	if err != nil {
		return "", err
	}
	t, err := ToString(b)
	if err != nil {
		return "", err
	}
	return s + t, nil
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpFloat is ZEND_THREEWAY_COMPARE for doubles: NaN compares as uncomparable (1).
func cmpFloat(a, b float64) int {
	switch {
	case a == b:
		return 0
	case a < b:
		return -1
	}
	return 1
}

func cmpStr(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func cmpNum(x, y any) int {
	if i, ok := x.(int64); ok {
		if j, ok := y.(int64); ok {
			return cmpInt(i, j)
		}
	}
	return cmpFloat(ToFloat(x), ToFloat(y))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// compareStrings is zendi_smart_strcmp: numeric strings compare as numbers.
func compareStrings(a, b string) int {
	x, k1 := ParseNumeric(a)
	if k1 == Numeric {
		if y, k2 := ParseNumeric(b); k2 == Numeric {
			// Two integer strings that overflowed compare as strings when both overflow the same way.
			return cmpNum(x, y)
		}
	}
	return cmpStr(a, b)
}

// compareNumString compares a number with a string under PHP 8 rules: a numeric string compares numerically,
// otherwise the number is turned into a string.
func compareNumString(n any, s string) int {
	if y, k := ParseNumeric(s); k == Numeric {
		return cmpNum(n, y)
	}
	return cmpStr(MustString(n), s)
}

// Compare is zend_compare: -1, 0 or 1, where uncomparable pairs give 1.
func Compare(a, b any) int {
	if m, ok := a.(Markup); ok {
		return compareMarkup(m, b, false)
	}
	if m, ok := b.(Markup); ok {
		return compareMarkup(m, a, true)
	}
	switch x := a.(type) {
	case int64:
		switch y := b.(type) {
		case int64, float64:
			return cmpNum(x, y)
		case string:
			return compareNumString(x, y)
		}
	case float64:
		switch y := b.(type) {
		case int64, float64:
			return cmpNum(x, y)
		case string:
			return compareNumString(x, y)
		}
	case string:
		switch y := b.(type) {
		case string:
			return compareStrings(x, y)
		case int64, float64:
			return -compareNumString(y, x)
		case nil:
			return boolInt(x != "")
		}
	case nil:
		switch y := b.(type) {
		case nil:
			return 0
		case bool:
			return cmpInt(0, int64(boolInt(y)))
		case string:
			return -boolInt(y != "")
		case *Array:
			return -boolInt(y.Len() > 0)
		}
	case *Array:
		if y, ok := b.(*Array); ok {
			return compareArrays(x, y)
		}
	}
	// Bools and nulls compare as bools; arrays are greater than any other type; objects are uncomparable.
	if _, ok := a.(bool); ok || a == nil {
		return cmpInt(int64(boolInt(ToBool(a))), int64(boolInt(ToBool(b))))
	}
	if _, ok := b.(bool); ok || b == nil {
		return cmpInt(int64(boolInt(ToBool(a))), int64(boolInt(ToBool(b))))
	}
	if _, ok := a.(*Array); ok {
		return 1
	}
	if _, ok := b.(*Array); ok {
		return -1
	}
	if x, ok := a.(*DateTime); ok {
		if y, ok := b.(*DateTime); ok {
			return x.compare(y)
		}
	}
	if a == b {
		return 0
	}
	if s, ok := b.(string); ok {
		if as, err := ToString(a); err == nil {
			return cmpStr(as, s)
		}
	}
	if s, ok := a.(string); ok {
		if bs, err := ToString(b); err == nil {
			return cmpStr(s, bs)
		}
	}
	return 1
}

// compareMarkup compares a Twig\Markup object with another value (zend_std_compare_objects): with a string or
// another Markup through its text, with null and booleans as true, with numbers as the failed int cast 1.
func compareMarkup(m Markup, other any, swapped bool) int {
	var c int
	switch o := other.(type) {
	case Markup:
		c = Compare(m.S, o.S)
	case string:
		c = Compare(m.S, o)
	case nil, bool:
		c = cmpInt(1, int64(boolInt(ToBool(o))))
	case int64:
		c = cmpInt(1, o)
	case float64:
		c = cmpFloat(1, o)
	default:
		return 1
	}
	if swapped {
		return -c
	}
	return c
}

// compareArrays is zend_hash_compare in unordered mode: count first, then each key of a looked up in b.
func compareArrays(a, b *Array) int {
	if a.Len() != b.Len() {
		return cmpInt(int64(a.Len()), int64(b.Len()))
	}
	result := 0
	a.Each(func(k, v any) bool {
		w, ok := b.Get(k)
		if !ok {
			result = 1
			return false
		}
		if c := Compare(v, w); c != 0 {
			result = c
			return false
		}
		return true
	})
	return result
}

// Equal is $a == $b.
func Equal(a, b any) bool {
	// Loose equality of a bool/null with anything is a bool comparison; strings with strings compare numerically
	// only when both are numeric. zend_compare covers all of it except NaN, which is never equal.
	return Compare(a, b) == 0 && !isNaNValue(a) && !isNaNValue(b)
}

func isNaNValue(v any) bool {
	f, ok := v.(float64)
	return ok && math.IsNaN(f)
}

// NotEqual is $a != $b.
func NotEqual(a, b any) bool { return !Equal(a, b) }

// Less is $a < $b.
func Less(a, b any) bool { return Compare(a, b) < 0 }

// LessEq is $a <= $b.
func LessEq(a, b any) bool { return Compare(a, b) <= 0 && !(isNaNValue(a) || isNaNValue(b)) }

// Greater is $a > $b, evaluated by PHP as $b < $a.
func Greater(a, b any) bool { return Compare(b, a) < 0 }

// GreaterEq is $a >= $b, evaluated as $b <= $a.
func GreaterEq(a, b any) bool { return LessEq(b, a) }

// Spaceship is $a <=> $b.
func Spaceship(a, b any) int64 { return int64(Compare(a, b)) }

// Identical is $a === $b.
func Identical(a, b any) bool {
	switch x := a.(type) {
	case nil:
		return b == nil
	case bool, int64, string:
		return a == b
	case float64:
		y, ok := b.(float64)
		return ok && x == y
	case Markup:
		y, ok := b.(Markup)
		return ok && x == y
	case *Array:
		y, ok := b.(*Array)
		if !ok || x.Len() != y.Len() {
			return false
		}
		xk, yk := x.Keys(), y.Keys()
		for i := range xk {
			if xk[i] != yk[i] {
				return false
			}
			xv, _ := x.Get(xk[i])
			yv, _ := y.Get(yk[i])
			if !Identical(xv, yv) {
				return false
			}
		}
		return true
	}
	return a == b
}
