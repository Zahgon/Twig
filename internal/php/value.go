// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

// Package php reproduces the PHP 8.3 value semantics that Twig templates observe: ordered arrays with normalised
// keys, type juggling, loose comparison, arithmetic with its errors, and the PHP library functions that Twig's core
// extension and escaper call.
//
// Values are plain Go values held in `any`: nil, bool, int64, float64, string, *Array, Markup, and objects defined
// by the engine (any other type; see Object).
package php

import (
	"fmt"
	"math"
)

// Markup is a string that is already safe for the output context (Twig\Markup).
type Markup struct {
	S string
}

// String returns the markup text.
func (m Markup) String() string { return m.S }

// Object is implemented by engine-defined values (closures, macros, templates) so that error messages can name
// their PHP class.
type Object interface {
	ClassName() string
}

// StringConverter is implemented by objects that have a __toString conversion.
type StringConverter interface {
	PHPString() (string, error)
}

// Callable is a PHP callable as seen by filters such as map, filter, reduce and sort.
type Callable func(args ...any) (any, error)

// Error is an exception thrown by PHP itself (TypeError, ValueError, DivisionByZeroError, ArgumentCountError,
// ArithmeticError, ...) rather than by Twig. Class is the short class name.
type Error struct {
	Class string
	Msg   string
}

func (e *Error) Error() string { return e.Msg }

// Throw builds a PHP exception value.
func Throw(class, format string, args ...any) *Error {
	return &Error{Class: class, Msg: fmt.Sprintf(format, args...)}
}

// TypeName is PHP's get_debug_type for the scalar part of the type system, as used in operator errors
// ("Unsupported operand types: array + int").
func TypeName(v any) string {
	switch v := v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case int64:
		return "int"
	case float64:
		return "float"
	case string:
		return "string"
	case *Array:
		return "array"
	case Markup:
		return "Twig\\Markup"
	case Object:
		return v.ClassName()
	}
	return fmt.Sprintf("%T", v)
}

// ValueName is zend_zval_value_name (PHP 8.3), used in argument type errors: like TypeName but "true"/"false"
// for booleans.
func ValueName(v any) string {
	if b, ok := v.(bool); ok {
		if b {
			return "true"
		}
		return "false"
	}
	return TypeName(v)
}

// GetType is PHP's gettype() naming ("integer", "double", "NULL", ...).
func GetType(v any) string {
	switch v.(type) {
	case nil:
		return "NULL"
	case bool:
		return "boolean"
	case int64:
		return "integer"
	case float64:
		return "double"
	case string:
		return "string"
	case *Array:
		return "array"
	}
	return "object"
}

// Normalize turns Go numeric types into int64/float64 so the rest of the package sees canonical values.
func Normalize(v any) any {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int32:
		return int64(n)
	case uint8:
		return int64(n)
	case float32:
		return float64(n)
	case []any:
		return NewList(n...)
	}
	return v
}

// FloatToInt converts like PHP's (int) cast on 64-bit platforms: NaN and infinities give 0, values out of range
// wrap modulo 2**64 (zend_dval_to_lval).
func FloatToInt(d float64) int64 {
	if math.IsNaN(d) || math.IsInf(d, 0) {
		return 0
	}
	if d >= -9.223372036854775808e18 && d < 9.223372036854775808e18 {
		return int64(d)
	}
	const two64 = 18446744073709551616.0
	dmod := math.Mod(d, two64)
	if dmod < 0 {
		dmod += two64
	}
	if dmod >= 9.223372036854775808e18 {
		dmod -= two64
	}
	return int64(dmod)
}

// IsInt reports whether a float has an exact int64 representation (ZEND_DOUBLE_FITS_LONG plus integrality).
func floatFitsInt(d float64) bool {
	return d >= -9.223372036854775808e18 && d < 9.223372036854775808e18
}
