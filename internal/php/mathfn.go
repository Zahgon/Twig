// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"fmt"
	"math"
)

// Abs is abs(int|float $num).
func Abs(v any) (any, error) {
	n, err := NumberArg("abs", 1, "num", v)
	if err != nil {
		return nil, err
	}
	switch x := n.(type) {
	case int64:
		if x == math.MinInt64 {
			return -float64(x), nil
		}
		if x < 0 {
			return -x, nil
		}
		return x, nil
	case float64:
		return math.Abs(x), nil
	}
	return n, nil
}

func minMax(fn string, args []any, want int) (any, error) {
	if len(args) == 0 {
		return nil, Throw("ArgumentCountError", "%s() expects at least 1 argument, 0 given", fn)
	}
	if len(args) == 1 {
		a, ok := args[0].(*Array)
		if !ok {
			return nil, Throw("TypeError", "%s(): Argument #1 ($value) must be of type array, %s given", fn, ValueName(args[0]))
		}
		if a.Len() == 0 {
			return nil, Throw("ValueError", "%s(): Argument #1 ($value) must contain at least one element", fn)
		}
		// zend_hash_minmax keeps the current result unless it compares on the wrong side of the candidate.
		vals := a.Values()
		res := vals[0]
		for _, v := range vals[1:] {
			if c := Compare(res, v); want > 0 && c < 0 || want < 0 && c > 0 {
				res = v
			}
		}
		return res, nil
	}
	res := args[0]
	for _, v := range args[1:] {
		if c := Compare(v, res); want > 0 && c > 0 || want < 0 && c < 0 {
			res = v
		}
	}
	return res, nil
}

// Max is max(...$values).
func Max(args []any) (any, error) { return minMax("max", args, 1) }

// Min is min(...$values).
func Min(args []any) (any, error) { return minMax("min", args, -1) }

const (
	rangeLong = iota + 1
	rangeDouble
	rangeString
)

var rangeArgNames = [...]string{"", "start", "end", "step"}

// rangeInput is php_range_process_input.
func rangeInput(v any, n int) (kind int, l int64, d float64, err error) {
	switch x := v.(type) {
	case int64:
		return rangeLong, x, float64(x), nil
	case float64:
		if math.IsInf(x, 0) {
			return 0, 0, 0, Throw("ValueError", "range(): Argument #%d ($%s) must be a finite number, INF provided", n, rangeArgNames[n])
		}
		if math.IsNaN(x) {
			return 0, 0, 0, Throw("ValueError", "range(): Argument #%d ($%s) must be a finite number, NAN provided", n, rangeArgNames[n])
		}
		return rangeDouble, FloatToInt(x), x, nil
	case string, Markup:
		s := MustString(x)
		if s == "" {
			return rangeLong, 0, 0, nil
		}
		num, k := ParseNumeric(s)
		if k == Numeric {
			if f, ok := num.(float64); ok {
				return rangeDouble, FloatToInt(f), f, nil
			}
			i := num.(int64)
			if len(s) == 1 {
				return rangeString, i, float64(i), nil
			}
			return rangeLong, i, float64(i), nil
		}
		return rangeString, 0, 0, nil
	case nil:
		return rangeLong, 0, 0, nil
	case bool:
		i := ToInt(x)
		return rangeLong, i, float64(i), nil
	}
	return 0, 0, 0, Throw("TypeError", "range(): Argument #%d ($%s) must be of type string|int|float, %s given", n, rangeArgNames[n], ValueName(v))
}

const htMaxSize = 0x40000000

func rangeSpanError() error {
	return Throw("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)")
}

func rangeIncreasingError() error {
	return Throw("ValueError", "range(): Argument #3 ($step) must be greater than 0 for increasing ranges")
}

// Range is PHP 8.3's range($start, $end, $step); step nil means 1.
func Range(start, end, step any) (*Array, error) {
	st, sl, sdv, err := rangeInput(start, 1)
	if err != nil {
		return nil, err
	}
	et, el, edv, err := rangeInput(end, 2)
	if err != nil {
		return nil, err
	}
	isStepDouble, isStepNegative := false, false
	stepDouble := 1.0
	lstep := uint64(1)
again:
	if step != nil {
		var sd float64
		switch x := step.(type) {
		case int64:
			sd = float64(x)
			if x < 0 {
				isStepNegative = true
				lstep = uint64(-x)
			} else {
				lstep = uint64(x)
			}
			if x == 0 {
				return nil, Throw("ValueError", "range(): Argument #3 ($step) cannot be 0")
			}
			stepDouble = math.Abs(sd)
			goto steps
		case float64:
			sd = x
		case string, Markup:
			num, k := ParseNumeric(MustString(x))
			if k != Numeric {
				return nil, Throw("TypeError", "range(): Argument #3 ($step) must be of type int|float, %s given", ValueName(step))
			}
			if i, ok := num.(int64); ok {
				step = i
				goto again
			}
			sd = num.(float64)
		case nil, bool:
			sd = ToFloat(x)
		default:
			return nil, Throw("TypeError", "range(): Argument #3 ($step) must be of type int|float, %s given", ValueName(step))
		}
		if sd < 0 {
			isStepNegative = true
			sd = -sd
		}
		stepDouble = sd
		lstep = uint64(FloatToInt(sd))
		if float64(int64(lstep)) != sd {
			isStepDouble = true
		}
		if sd == 0 {
			return nil, Throw("ValueError", "range(): Argument #3 ($step) cannot be 0")
		}
	}
steps:
	if st == rangeString || et == rangeString {
		if st != rangeString || et != rangeString {
			if st != rangeString {
				et = rangeLong
			} else {
				st = rangeLong
			}
		} else if isStepDouble {
			// Float steps are not allowed for characters: the inputs become numbers (0 for letters).
			st, et = rangeLong, rangeLong
		} else {
			low := MustString(start)[0]
			high := MustString(end)[0]
			out := NewArray()
			switch {
			case low > high:
				if uint64(low-high) < lstep {
					return nil, rangeSpanError()
				}
				for c := int(low); c >= int(high); c -= int(lstep) {
					out.Append(string([]byte{byte(c)}))
				}
			case high > low:
				if isStepNegative {
					return nil, rangeIncreasingError()
				}
				if uint64(high-low) < lstep {
					return nil, rangeSpanError()
				}
				for c := int(low); c <= int(high); c += int(lstep) {
					out.Append(string([]byte{byte(c)}))
				}
			default:
				out.Append(string([]byte{low}))
			}
			return out, nil
		}
	}
	out := NewArray()
	if st == rangeDouble || et == rangeDouble || isStepDouble {
		if sdv > edv {
			if sdv-edv < stepDouble {
				return nil, rangeSpanError()
			}
			size, err := doubleRangeSize(edv, sdv, stepDouble)
			if err != nil {
				return nil, err
			}
			for i, e := uint32(0), sdv; i < size && e >= edv; i, e = i+1, sdv-float64(i+1)*stepDouble {
				out.Append(e)
			}
		} else if edv > sdv {
			if isStepNegative {
				return nil, rangeIncreasingError()
			}
			if edv-sdv < stepDouble {
				return nil, rangeSpanError()
			}
			size, err := doubleRangeSize(sdv, edv, stepDouble)
			if err != nil {
				return nil, err
			}
			for i, e := uint32(0), sdv; i < size && e <= edv; i, e = i+1, sdv+float64(i+1)*stepDouble {
				out.Append(e)
			}
		} else {
			out.Append(sdv)
		}
		return out, nil
	}
	if sl > el {
		if uint64(sl)-uint64(el) < lstep {
			return nil, rangeSpanError()
		}
		size := (uint64(sl) - uint64(el)) / lstep
		if size >= htMaxSize-1 {
			return nil, Throw("ValueError", "The supplied range exceeds the maximum array size: start=%d end=%d step=%d", el, sl, lstep)
		}
		for i := uint64(0); i <= size; i++ {
			out.Append(int64(uint64(sl) - i*lstep))
		}
	} else if el > sl {
		if isStepNegative {
			return nil, rangeIncreasingError()
		}
		if uint64(el)-uint64(sl) < lstep {
			return nil, rangeSpanError()
		}
		size := (uint64(el) - uint64(sl)) / lstep
		if size >= htMaxSize-1 {
			return nil, Throw("ValueError", "The supplied range exceeds the maximum array size: start=%d end=%d step=%d", sl, el, lstep)
		}
		for i := uint64(0); i <= size; i++ {
			out.Append(int64(uint64(sl) + i*lstep))
		}
	} else {
		out.Append(sl)
	}
	return out, nil
}

func doubleRangeSize(low, high, step float64) (uint32, error) {
	calc := (high-low)/step + 1
	if calc >= htMaxSize {
		return 0, Throw("ValueError", "The supplied range exceeds the maximum array size: start=%s end=%s step=%s",
			fmt.Sprintf("%.1f", low), fmt.Sprintf("%.1f", high), fmt.Sprintf("%.1f", step))
	}
	return uint32(RoundFloat(calc, 0, RoundHalfUp)), nil
}
