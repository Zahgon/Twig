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

const maxFloatPrecision = 53

// appendString is php_sprintf_appendstring: pads to width (zero padding goes after the sign), truncates to
// precision when expprec is set, and pads on the right for left alignment, even with zeros.
func appendString(b *strings.Builder, s string, width, precision int, padding byte, left bool, neg, expprec, alwaysSign bool) {
	copyLen := len(s)
	if expprec && precision < copyLen {
		copyLen = precision
	}
	npad := 0
	if width > copyLen {
		npad = width - copyLen
	}
	if !left {
		if (neg || alwaysSign) && padding == '0' && len(s) > 0 {
			b.WriteByte(s[0])
			s = s[1:]
			copyLen--
		}
		for ; npad > 0; npad-- {
			b.WriteByte(padding)
		}
	}
	b.WriteString(s[:copyLen])
	if left {
		for ; npad > 0; npad-- {
			b.WriteByte(padding)
		}
	}
}

// formatE is php_conv_fp for 'e'/'E': the exponent has no leading zeros.
func formatE(d float64, prec int, letter byte) string {
	s := strconv.FormatFloat(d, 'e', prec, 64)
	i := strings.IndexByte(s, 'e')
	mant, exp := s[:i], s[i+1:]
	sign := exp[0]
	exp = strings.TrimLeft(exp[1:], "0")
	if exp == "" {
		exp = "0"
	}
	return mant + string(letter) + string(sign) + exp
}

// Sprintf is PHP's sprintf with its argument conversions and errors.
func Sprintf(format string, args ...any) (string, error) {
	var b strings.Builder
	currarg := 0
	maxMissing := -1
	n := len(format)
	for i := 0; i < n; {
		if format[i] != '%' {
			b.WriteByte(format[i])
			i++
			continue
		}
		if i+1 < n && format[i+1] == '%' {
			b.WriteByte('%')
			i += 2
			continue
		}
		i++
		// Argument number "N$".
		argnum := -1
		j := i
		for j < n && format[j] >= '0' && format[j] <= '9' {
			j++
		}
		if j > i && j < n && format[j] == '$' {
			num, err := strconv.Atoi(format[i:j])
			if err != nil || num <= 0 || num >= math.MaxInt32 {
				return "", Throw("ValueError", "Argument number specifier must be greater than zero and less than %d", math.MaxInt32)
			}
			argnum = num - 1
			i = j + 1
		}
		// Flags.
		padding := byte(' ')
		left, alwaysSign := false, false
	flags:
		for i < n {
			switch format[i] {
			case ' ', '0':
				padding = format[i]
			case '-':
				left = true
			case '+':
				alwaysSign = true
			case '\'':
				if i+1 < n {
					i++
					padding = format[i]
				} else {
					return "", Throw("ValueError", "Missing padding character")
				}
			default:
				break flags
			}
			i++
		}
		width, precision := 0, 0
		hasPrecision := false
		if i < n && format[i] == '*' {
			i++
			w, err := starArg(args, &currarg, &maxMissing, "Width")
			if err != nil {
				return "", err
			}
			width = w
		} else {
			j = i
			for j < n && format[j] >= '0' && format[j] <= '9' {
				j++
			}
			if j > i {
				w, err := strconv.Atoi(format[i:j])
				if err != nil || w >= math.MaxInt32 {
					return "", Throw("ValueError", "Width must be greater than zero and less than %d", math.MaxInt32)
				}
				width = w
				i = j
			}
		}
		if i < n && format[i] == '.' {
			i++
			hasPrecision = true
			if i < n && format[i] == '*' {
				i++
				p, err := starArg(args, &currarg, &maxMissing, "Precision")
				if err != nil {
					return "", err
				}
				precision = p
				if p == -1 {
					hasPrecision = false
				}
			} else {
				j = i
				for j < n && format[j] >= '0' && format[j] <= '9' {
					j++
				}
				if j > i {
					p, err := strconv.Atoi(format[i:j])
					if err != nil || p >= math.MaxInt32 {
						return "", Throw("ValueError", "Precision must be greater than zero and less than %d", math.MaxInt32)
					}
					precision = p
				}
				i = j
			}
		}
		if i < n && format[i] == 'l' {
			i++
		}
		if i >= n {
			return "", Throw("ValueError", "Missing format specifier at end of string")
		}
		spec := format[i]
		i++
		if spec == '%' {
			b.WriteByte('%')
			continue
		}
		if argnum < 0 {
			argnum = currarg
			currarg++
		}
		if argnum >= len(args) {
			if argnum > maxMissing {
				maxMissing = argnum
			}
			continue
		}
		arg := args[argnum]
		switch spec {
		case 's':
			s, err := ToString(arg)
			if err != nil {
				return "", err
			}
			appendString(&b, s, width, precision, padding, left, false, hasPrecision, false)
		case 'd':
			v := ToInt(arg)
			if left && padding == '0' {
				// php_sprintf_appendint never pads integers with zeros on the right.
				padding = ' '
			}
			s := strconv.FormatInt(v, 10)
			if v >= 0 && alwaysSign {
				s = "+" + s
			}
			appendString(&b, s, width, 0, padding, left, v < 0, false, alwaysSign)
		case 'u':
			if left && padding == '0' {
				padding = ' '
			}
			appendString(&b, strconv.FormatUint(uint64(ToInt(arg)), 10), width, 0, padding, left, false, false, false)
		case 'e', 'E', 'f', 'F', 'g', 'G', 'h', 'H':
			appendDouble(&b, ToFloat(arg), width, precision, hasPrecision, padding, left, alwaysSign, spec)
		case 'c':
			b.WriteByte(byte(ToInt(arg)))
		case 'o':
			// The base-2^n conversions pass precision 0 with truncation on, so any precision empties them.
			appendString(&b, strconv.FormatUint(uint64(ToInt(arg)), 8), width, 0, padding, left, false, hasPrecision, false)
		case 'x':
			appendString(&b, strconv.FormatUint(uint64(ToInt(arg)), 16), width, 0, padding, left, false, hasPrecision, false)
		case 'X':
			appendString(&b, strings.ToUpper(strconv.FormatUint(uint64(ToInt(arg)), 16)), width, 0, padding, left, false, hasPrecision, false)
		case 'b':
			appendString(&b, strconv.FormatUint(uint64(ToInt(arg)), 2), width, 0, padding, left, false, hasPrecision, false)
		default:
			return "", Throw("ValueError", "Unknown format specifier \"%c\"", spec)
		}
	}
	if maxMissing >= 0 {
		return "", Throw("ArgumentCountError", "%d arguments are required, %d given", maxMissing+2, len(args)+1)
	}
	return b.String(), nil
}

func starArg(args []any, currarg, maxMissing *int, what string) (int, error) {
	if *currarg >= len(args) {
		if *currarg > *maxMissing {
			*maxMissing = *currarg
		}
		*currarg++
		return 0, nil
	}
	v := args[*currarg]
	*currarg++
	i, ok := v.(int64)
	if !ok {
		return 0, Throw("ValueError", "%s must be an integer", what)
	}
	if what == "Precision" {
		if i < -1 || i > math.MaxInt32 {
			return 0, Throw("ValueError", "Precision must be between -1 and %d", math.MaxInt32)
		}
		return int(i), nil
	}
	if i < 0 || i > math.MaxInt32 {
		return 0, Throw("ValueError", "Width must be between 0 and %d", math.MaxInt32)
	}
	return int(i), nil
}

func appendDouble(b *strings.Builder, d float64, width, precision int, hasPrecision bool, padding byte, left, alwaysSign bool, spec byte) {
	if !hasPrecision {
		precision = 6
	} else if precision > maxFloatPrecision {
		precision = maxFloatPrecision
	}
	if math.IsNaN(d) {
		appendString(b, "NaN", width, 0, padding, left, false, false, alwaysSign)
		return
	}
	if math.IsInf(d, 0) {
		s := "Inf"
		neg := d < 0
		if neg {
			s = "-Inf"
		} else if alwaysSign {
			s = "+Inf"
		}
		appendString(b, s, width, 0, padding, left, neg, false, alwaysSign)
		return
	}
	neg := math.Signbit(d) && d != 0 || d < 0
	var s string
	switch spec {
	case 'e', 'E':
		s = formatE(math.Abs(d), precision, spec)
	case 'f', 'F':
		s = strconv.FormatFloat(math.Abs(d), 'f', precision, 64)
	default:
		if precision == 0 {
			precision = 1
		}
		letter := byte('e')
		if spec == 'G' || spec == 'H' {
			letter = 'E'
		}
		s = formatG(math.Abs(d), precision, letter, false)
		if s == "0" && math.Signbit(d) {
			neg = true
		}
	}
	if neg {
		s = "-" + s
	} else if alwaysSign {
		s = "+" + s
	}
	appendString(b, s, width, 0, padding, left, neg, false, alwaysSign)
}
