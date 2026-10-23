// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import "math"

// Parameter coercion of PHP internal functions in weak (non-strict_types) mode. Twig calls PHP functions such as
// mb_substr or number_format with template values; a value of the wrong type raises a TypeError naming the
// function, the parameter position and name, and the given type.

func argError(fn string, n int, name, want string, v any) error {
	return Throw("TypeError", "%s(): Argument #%d ($%s) must be of type %s, %s given", fn, n, name, want, ValueName(v))
}

// StringArg coerces an argument to a string parameter.
func StringArg(fn string, n int, name string, v any) (string, error) {
	switch x := v.(type) {
	case *Array:
		return "", argError(fn, n, name, "string", v)
	case nil, bool, int64, float64, string, Markup:
		return MustString(x), nil
	case StringConverter:
		return x.PHPString()
	}
	return "", argError(fn, n, name, "string", v)
}

// IntArg coerces an argument to an int parameter.
func IntArg(fn string, n int, name string, v any) (int64, error) {
	switch x := v.(type) {
	case nil:
		return 0, nil
	case bool:
		return ToInt(x), nil
	case int64:
		return x, nil
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) || !floatFitsInt(x) {
			return 0, argError(fn, n, name, "int", v)
		}
		return int64(x), nil
	case string, Markup:
		s := MustString(x)
		num, kind := ParseNumeric(s)
		if kind != Numeric {
			return 0, argError(fn, n, name, "int", v)
		}
		if i, ok := num.(int64); ok {
			return i, nil
		}
		f := num.(float64)
		if math.IsNaN(f) || math.IsInf(f, 0) || !floatFitsInt(f) {
			return 0, argError(fn, n, name, "int", v)
		}
		return int64(f), nil
	}
	return 0, argError(fn, n, name, "int", v)
}

// NullableIntArg is IntArg for ?int parameters: null stays null (ok=false).
func NullableIntArg(fn string, n int, name string, v any) (int64, bool, error) {
	if v == nil {
		return 0, false, nil
	}
	i, err := IntArg(fn, n, name, v)
	return i, true, err
}

// FloatArg coerces an argument to a float parameter.
func FloatArg(fn string, n int, name string, v any) (float64, error) {
	switch x := v.(type) {
	case nil, bool, int64, float64:
		return ToFloat(x), nil
	case string, Markup:
		num, kind := ParseNumeric(MustString(x))
		if kind != Numeric {
			return 0, argError(fn, n, name, "float", v)
		}
		return ToFloat(num), nil
	}
	return 0, argError(fn, n, name, "float", v)
}

// NumberArg coerces an argument to an int|float parameter (abs, round).
func NumberArg(fn string, n int, name string, v any) (any, error) {
	switch x := v.(type) {
	case nil, bool:
		return ToInt(x), nil
	case int64, float64:
		return x, nil
	case string, Markup:
		num, kind := ParseNumeric(MustString(x))
		if kind != Numeric {
			return nil, argError(fn, n, name, "int|float", v)
		}
		return num, nil
	}
	return nil, argError(fn, n, name, "int|float", v)
}

// BoolArg coerces an argument to a bool parameter.
func BoolArg(fn string, n int, name string, v any) (bool, error) {
	switch v.(type) {
	case nil, bool, int64, float64, string, Markup:
		return ToBool(v), nil
	}
	return false, argError(fn, n, name, "bool", v)
}

// ArrayArg requires an array parameter.
func ArrayArg(fn string, n int, name string, v any) (*Array, error) {
	if a, ok := v.(*Array); ok {
		return a, nil
	}
	return nil, argError(fn, n, name, "array", v)
}
