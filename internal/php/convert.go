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

// Precision is PHP's "precision" ini setting (14), used when a float becomes a string.
const Precision = 14

// ToBool is PHP's (bool) cast.
func ToBool(v any) bool {
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case int64:
		return v != 0
	case float64:
		return v != 0
	case string:
		return v != "" && v != "0"
	case *Array:
		return v.Len() > 0
	}
	// Objects, Markup included, are always true.
	return true
}

// ToString is PHP's (string) cast. Arrays become "Array" (PHP only warns); objects without __toString throw.
func ToString(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case bool:
		if v {
			return "1", nil
		}
		return "", nil
	case int64:
		return strconv.FormatInt(v, 10), nil
	case int:
		return strconv.Itoa(v), nil
	case float64:
		return FormatFloat(v), nil
	case string:
		return v, nil
	case Markup:
		return v.S, nil
	case *Array:
		return "Array", nil
	case StringConverter:
		return v.PHPString()
	}
	return "", Throw("Error", "Object of class %s could not be converted to string", TypeName(v))
}

// MustString is ToString for values known to convert (scalars); errors give "".
func MustString(v any) string {
	s, _ := ToString(v)
	return s
}

// FormatFloat prints a float as PHP's echo does (precision=14, %G style with PHP's exponent spelling).
func FormatFloat(d float64) string {
	return formatG(d, Precision, 'E', false)
}

// FormatFloatJSON prints a float with serialize_precision=-1 (shortest round trip), as json_encode and
// var_export do; exp is the exponent letter ('e' for JSON).
func FormatFloatJSON(d float64, exp byte) string {
	return formatG(d, 17, exp, true)
}

// formatG implements zend_gcvt: digits in mode 2 (precision significant digits) or mode 0 (shortest), switching
// to scientific notation when the decimal exponent is below -4 or at least the precision.
func formatG(d float64, precision int, exp byte, shortest bool) string {
	switch {
	case math.IsNaN(d):
		return "NAN"
	case math.IsInf(d, 1):
		return "INF"
	case math.IsInf(d, -1):
		return "-INF"
	}
	if d == 0 {
		if math.Signbit(d) {
			return "-0"
		}
		return "0"
	}
	var s string
	if shortest {
		s = strconv.FormatFloat(d, 'e', -1, 64)
	} else {
		s = strconv.FormatFloat(d, 'e', precision-1, 64)
	}
	neg := false
	if s[0] == '-' {
		neg = true
		s = s[1:]
	}
	epos := strings.IndexByte(s, 'e')
	mant, e := s[:epos], s[epos+1:]
	x, _ := strconv.Atoi(e)
	digits := strings.Replace(mant, ".", "", 1)
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		digits = "0"
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	decpt := x + 1
	if decpt < -3 || decpt > precision {
		b.WriteByte(digits[0])
		b.WriteByte('.')
		if len(digits) > 1 {
			b.WriteString(digits[1:])
		} else {
			b.WriteByte('0')
		}
		b.WriteByte(exp)
		if x < 0 {
			b.WriteByte('-')
			x = -x
		} else {
			b.WriteByte('+')
		}
		b.WriteString(strconv.Itoa(x))
		return b.String()
	}
	if decpt <= 0 {
		b.WriteString("0.")
		b.WriteString(strings.Repeat("0", -decpt))
		b.WriteString(digits)
		return b.String()
	}
	if len(digits) <= decpt {
		b.WriteString(digits)
		b.WriteString(strings.Repeat("0", decpt-len(digits)))
		return b.String()
	}
	b.WriteString(digits[:decpt])
	b.WriteByte('.')
	b.WriteString(digits[decpt:])
	return b.String()
}

// Numeric string classification.
const (
	NotNumeric     = 0 // no leading number at all
	Numeric        = 1 // the whole string (with optional surrounding whitespace) is a number
	LeadingNumeric = 2 // a number followed by other characters ("5 apples")
)

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// ParseNumeric implements _is_numeric_string_ex: it returns the number (int64 or float64) at the start of s and
// how much of s it covers.
func ParseNumeric(s string) (any, int) {
	i := 0
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '-' || s[i] == '+') {
		i++
	}
	intDigits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		intDigits++
	}
	isFloat := false
	fracDigits := 0
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			fracDigits++
		}
		if intDigits > 0 || fracDigits > 0 {
			isFloat = true
			i = j
		}
	}
	if intDigits == 0 && fracDigits == 0 {
		return nil, NotNumeric
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '-' || s[j] == '+') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			isFloat = true
			i = j
		}
	}
	num := s[start:i]
	end := i
	for end < len(s) && isSpace(s[end]) {
		end++
	}
	kind := Numeric
	if end != len(s) {
		kind = LeadingNumeric
	}
	if !isFloat {
		if n, err := strconv.ParseInt(num, 10, 64); err == nil {
			return n, kind
		}
	}
	f, err := strconv.ParseFloat(num, 64)
	if err != nil {
		// Overflow gives ±Inf from ParseFloat together with an error; keep the infinity like strtod.
		if ne, ok := err.(*strconv.NumError); ok && ne.Err == strconv.ErrRange {
			return f, kind
		}
		return nil, NotNumeric
	}
	return f, kind
}

// IsNumeric is is_numeric() for any value.
func IsNumeric(v any) bool {
	switch v := v.(type) {
	case int64, float64:
		return true
	case string:
		_, k := ParseNumeric(v)
		return k == Numeric
	}
	return false
}

// ToInt is PHP's (int) cast.
func ToInt(v any) int64 {
	switch v := v.(type) {
	case nil:
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	case int64:
		return v
	case float64:
		return FloatToInt(v)
	case string:
		n, k := ParseNumeric(v)
		if k == NotNumeric {
			return 0
		}
		if i, ok := n.(int64); ok {
			return i
		}
		return FloatToInt(n.(float64))
	case Markup:
		return ToInt(v.S)
	case *Array:
		if v.Len() > 0 {
			return 1
		}
		return 0
	}
	return 1
}

// ToFloat is PHP's (float) cast.
func ToFloat(v any) float64 {
	switch v := v.(type) {
	case nil:
		return 0
	case bool:
		if v {
			return 1
		}
		return 0
	case int64:
		return float64(v)
	case float64:
		return v
	case string:
		n, k := ParseNumeric(v)
		if k == NotNumeric {
			return 0
		}
		if i, ok := n.(int64); ok {
			// strtod keeps the sign of "-0".
			if i == 0 && strings.HasPrefix(strings.TrimLeft(v, " \t\n\r\v\f"), "-") {
				return math.Copysign(0, -1)
			}
			return float64(i)
		}
		return n.(float64)
	case Markup:
		return ToFloat(v.S)
	case *Array:
		if v.Len() > 0 {
			return 1
		}
		return 0
	}
	return 1
}

// ToNumber converts a value to int64 or float64 the way (int|float) arithmetic does in a numeric context that
// accepts leading-numeric strings (non-numeric strings give 0), e.g. for +$x.
func ToNumber(v any) any {
	switch v := v.(type) {
	case int64, float64:
		return v
	case string:
		n, k := ParseNumeric(v)
		if k == NotNumeric {
			return int64(0)
		}
		return n
	}
	return ToInt(v)
}
