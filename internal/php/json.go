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
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// json_encode option bits.
const (
	JSONHexTag                = 1
	JSONHexAmp                = 2
	JSONHexApos               = 4
	JSONHexQuot               = 8
	JSONForceObject           = 16
	JSONNumericCheck          = 32
	JSONUnescapedSlashes      = 64
	JSONPrettyPrint           = 128
	JSONUnescapedUnicode      = 256
	JSONPartialOutputOnError  = 512
	JSONPreserveZeroFraction  = 1024
	JSONUnescapedLineTerms    = 2048
	JSONInvalidUTF8Ignore     = 0x100000
	JSONInvalidUTF8Substitute = 0x200000
	JSONThrowOnError          = 0x400000
	jsonErrorDepth            = 1
	jsonErrorUTF8             = 5
	jsonErrorInfOrNaN         = 7
	jsonErrorUnsupportedType  = 8
	defaultJSONDepth          = 512
	jsonMsgUTF8               = "Malformed UTF-8 characters, possibly incorrectly encoded"
	jsonMsgInfNaN             = "Inf and NaN cannot be JSON encoded"
	jsonMsgDepth              = "Maximum stack depth exceeded"
	jsonMsgUnsupported        = "Type is not supported"
	jsonMsgSyntax             = "Syntax error"
	jsonMsgCtrl               = "Control character error, possibly incorrectly encoded"
	jsonMsgSurrogate          = "Single unpaired UTF-16 surrogate in unicode escape"
	jsonMsgPropertyName       = "The decoded property name is invalid"
	jsonMsgStateMismatch      = "State mismatch (invalid or malformed JSON)"
)

// JSONSerializer is implemented by objects with a jsonSerialize() method.
type JSONSerializer interface {
	JSONSerialize() any
}

type jsonEncoder struct {
	b       strings.Builder
	opts    int64
	depth   int
	maxDep  int
	errCode int
	errMsg  string
}

func (e *jsonEncoder) fail(code int, msg string) {
	if e.errCode == 0 {
		e.errCode, e.errMsg = code, msg
	}
}

// JSONEncode is json_encode($v, $flags, $depth). ok is false when PHP returns false; errMsg is
// json_last_error_msg() (with JSON_THROW_ON_ERROR the caller throws JsonException with it).
func JSONEncode(v any, flags int64, depth int) (out string, ok bool, errMsg string) {
	if depth <= 0 {
		depth = defaultJSONDepth
	}
	e := &jsonEncoder{opts: flags, maxDep: depth}
	e.value(v)
	if e.errCode != 0 {
		if flags&JSONPartialOutputOnError != 0 && e.errCode != jsonErrorDepth {
			return e.b.String(), true, e.errMsg
		}
		return "", false, e.errMsg
	}
	return e.b.String(), true, ""
}

func (e *jsonEncoder) newline() {
	if e.opts&JSONPrettyPrint != 0 {
		e.b.WriteByte('\n')
		e.b.WriteString(strings.Repeat("    ", e.depth))
	}
}

func (e *jsonEncoder) value(v any) {
	switch x := v.(type) {
	case nil:
		e.b.WriteString("null")
	case bool:
		if x {
			e.b.WriteString("true")
		} else {
			e.b.WriteString("false")
		}
	case int64:
		e.b.WriteString(strconv.FormatInt(x, 10))
	case float64:
		e.float(x)
	case string:
		e.str(x)
	case Markup:
		e.str(x.S)
	case *Array:
		e.array(x)
	case JSONSerializer:
		e.value(x.JSONSerialize())
	default:
		// Other objects encode their (absent) public properties.
		e.b.WriteString("{}")
	}
}

func (e *jsonEncoder) float(x float64) {
	if math.IsInf(x, 0) || math.IsNaN(x) {
		e.fail(jsonErrorInfOrNaN, jsonMsgInfNaN)
		e.b.WriteByte('0')
		return
	}
	s := FormatFloatJSON(x, 'e')
	if e.opts&JSONPreserveZeroFraction != 0 && !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	e.b.WriteString(s)
}

func (e *jsonEncoder) array(a *Array) {
	object := e.opts&JSONForceObject != 0 || !a.IsList()
	if e.depth >= e.maxDep {
		e.fail(jsonErrorDepth, jsonMsgDepth)
	}
	if a.Len() == 0 {
		if object {
			e.b.WriteString("{}")
		} else {
			e.b.WriteString("[]")
		}
		return
	}
	open, closing := byte('['), byte(']')
	if object {
		open, closing = '{', '}'
	}
	e.b.WriteByte(open)
	e.depth++
	first := true
	a.Each(func(k, v any) bool {
		if !first {
			e.b.WriteByte(',')
		}
		first = false
		e.newline()
		if object {
			e.key(MustString(k))
			e.b.WriteByte(':')
			if e.opts&JSONPrettyPrint != 0 {
				e.b.WriteByte(' ')
			}
		}
		e.value(v)
		return true
	})
	e.depth--
	e.newline()
	e.b.WriteByte(closing)
}

func (e *jsonEncoder) str(s string) {
	if e.opts&JSONNumericCheck != 0 {
		if n, k := ParseNumeric(s); k == Numeric {
			e.value(n)
			return
		}
	}
	e.key(s)
}

// key writes a JSON string (object keys are never subject to JSON_NUMERIC_CHECK).
func (e *jsonEncoder) key(s string) {
	const hex = "0123456789abcdef"
	start := e.b.Len()
	e.b.WriteByte('"')
	for i := 0; i < len(s); {
		c := s[i]
		if c >= utf8.RuneSelf {
			r, n := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && n <= 1 {
				switch {
				case e.opts&JSONInvalidUTF8Ignore != 0:
				case e.opts&JSONInvalidUTF8Substitute != 0:
					if e.opts&JSONUnescapedUnicode != 0 {
						e.b.WriteString("�")
					} else {
						e.b.WriteString(`\ufffd`)
					}
				default:
					e.fail(jsonErrorUTF8, jsonMsgUTF8)
					// Partial output replaces the whole string with null.
					str := e.b.String()[:start]
					e.b.Reset()
					e.b.WriteString(str)
					e.b.WriteString("null")
					return
				}
				i++
				continue
			}
			if e.opts&JSONUnescapedUnicode != 0 && !((r == 0x2028 || r == 0x2029) && e.opts&JSONUnescapedLineTerms == 0) {
				e.b.WriteString(s[i : i+n])
			} else {
				for _, u := range utf16.Encode([]rune{r}) {
					fmt.Fprintf(&e.b, `\u%04x`, u)
				}
			}
			i += n
			continue
		}
		switch c {
		case '"':
			if e.opts&JSONHexQuot != 0 {
				e.b.WriteString(`\u0022`)
			} else {
				e.b.WriteString(`\"`)
			}
		case '\\':
			e.b.WriteString(`\\`)
		case '/':
			if e.opts&JSONUnescapedSlashes != 0 {
				e.b.WriteByte('/')
			} else {
				e.b.WriteString(`\/`)
			}
		case '\b':
			e.b.WriteString(`\b`)
		case '\f':
			e.b.WriteString(`\f`)
		case '\n':
			e.b.WriteString(`\n`)
		case '\r':
			e.b.WriteString(`\r`)
		case '\t':
			e.b.WriteString(`\t`)
		case '<':
			if e.opts&JSONHexTag != 0 {
				e.b.WriteString(`\u003C`)
			} else {
				e.b.WriteByte(c)
			}
		case '>':
			if e.opts&JSONHexTag != 0 {
				e.b.WriteString(`\u003E`)
			} else {
				e.b.WriteByte(c)
			}
		case '&':
			if e.opts&JSONHexAmp != 0 {
				e.b.WriteString(`\u0026`)
			} else {
				e.b.WriteByte(c)
			}
		case '\'':
			if e.opts&JSONHexApos != 0 {
				e.b.WriteString(`\u0027`)
			} else {
				e.b.WriteByte(c)
			}
		default:
			if c < 0x20 {
				e.b.WriteString(`\u00`)
				e.b.WriteByte(hex[c>>4])
				e.b.WriteByte(hex[c&15])
			} else {
				e.b.WriteByte(c)
			}
		}
		i++
	}
	e.b.WriteByte('"')
}

// JSONError is a json_decode failure with json_last_error_msg().
type JSONError struct {
	Msg string
}

func (e *JSONError) Error() string { return e.Msg }

type jsonDecoder struct {
	s     string
	i     int
	assoc bool
	depth int
}

// JSONObject is a decoded JSON object when assoc is false (stdClass); its properties are in an Array.
type JSONObject struct {
	Props *Array
}

// ClassName implements Object.
func (JSONObject) ClassName() string { return "stdClass" }

// JSONDecode is json_decode($s, $assoc) with PHP's parser: objects become arrays (assoc) or *JSONObject, integers
// that overflow become floats, and errors carry json_last_error_msg().
func JSONDecode(s string, assoc bool) (any, error) {
	d := &jsonDecoder{s: s, assoc: assoc}
	d.ws()
	v, err := d.value()
	if err != nil {
		return nil, err
	}
	d.ws()
	if d.i != len(d.s) {
		return nil, &JSONError{jsonMsgSyntax}
	}
	return v, nil
}

func (d *jsonDecoder) ws() {
	for d.i < len(d.s) {
		switch d.s[d.i] {
		case ' ', '\t', '\n', '\r':
			d.i++
		default:
			return
		}
	}
}

func (d *jsonDecoder) value() (any, error) {
	if d.i >= len(d.s) {
		return nil, &JSONError{jsonMsgSyntax}
	}
	switch c := d.s[d.i]; {
	case c == '{':
		return d.object()
	case c == '[':
		return d.list()
	case c == '"':
		return d.str()
	case c == '-' || c >= '0' && c <= '9':
		return d.number()
	case strings.HasPrefix(d.s[d.i:], "true"):
		d.i += 4
		return true, nil
	case strings.HasPrefix(d.s[d.i:], "false"):
		d.i += 5
		return false, nil
	case strings.HasPrefix(d.s[d.i:], "null"):
		d.i += 4
		return nil, nil
	}
	return nil, &JSONError{jsonMsgSyntax}
}

func (d *jsonDecoder) enter() error {
	d.depth++
	if d.depth >= defaultJSONDepth {
		return &JSONError{jsonMsgDepth}
	}
	return nil
}

func (d *jsonDecoder) object() (any, error) {
	d.i++
	if err := d.enter(); err != nil {
		return nil, err
	}
	a := NewArray()
	d.ws()
	if d.i < len(d.s) && d.s[d.i] == '}' {
		d.i++
		d.depth--
		return d.wrapObject(a), nil
	}
	for {
		d.ws()
		if d.i >= len(d.s) || d.s[d.i] != '"' {
			return nil, &JSONError{jsonMsgSyntax}
		}
		k, err := d.str()
		if err != nil {
			return nil, err
		}
		d.ws()
		if d.i >= len(d.s) || d.s[d.i] != ':' {
			return nil, &JSONError{jsonMsgSyntax}
		}
		d.i++
		d.ws()
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		key := k.(string)
		if !d.assoc && key != "" && key[0] == 0 {
			return nil, &JSONError{jsonMsgPropertyName}
		}
		if d.assoc {
			a.Set(key, v)
		} else {
			// stdClass properties keep string keys; numeric names are still looked up by the same string.
			a.Set(key, v)
		}
		d.ws()
		if d.i >= len(d.s) {
			return nil, &JSONError{jsonMsgSyntax}
		}
		if d.s[d.i] == ',' {
			d.i++
			continue
		}
		if d.s[d.i] == '}' {
			d.i++
			d.depth--
			return d.wrapObject(a), nil
		}
		return nil, &JSONError{jsonMsgSyntax}
	}
}

func (d *jsonDecoder) wrapObject(a *Array) any {
	if d.assoc {
		return a
	}
	return &JSONObject{Props: a}
}

func (d *jsonDecoder) list() (any, error) {
	d.i++
	if err := d.enter(); err != nil {
		return nil, err
	}
	a := NewArray()
	d.ws()
	if d.i < len(d.s) && d.s[d.i] == ']' {
		d.i++
		d.depth--
		return a, nil
	}
	for {
		d.ws()
		v, err := d.value()
		if err != nil {
			return nil, err
		}
		a.Append(v)
		d.ws()
		if d.i >= len(d.s) {
			return nil, &JSONError{jsonMsgSyntax}
		}
		if d.s[d.i] == ',' {
			d.i++
			continue
		}
		if d.s[d.i] == ']' {
			d.i++
			d.depth--
			return a, nil
		}
		return nil, &JSONError{jsonMsgSyntax}
	}
}

func (d *jsonDecoder) number() (any, error) {
	start := d.i
	if d.s[d.i] == '-' {
		d.i++
	}
	digits := func() int {
		n := 0
		for d.i < len(d.s) && d.s[d.i] >= '0' && d.s[d.i] <= '9' {
			d.i++
			n++
		}
		return n
	}
	if d.i < len(d.s) && d.s[d.i] == '0' {
		d.i++
	} else if digits() == 0 {
		return nil, &JSONError{jsonMsgSyntax}
	}
	isFloat := false
	if d.i < len(d.s) && d.s[d.i] == '.' {
		d.i++
		if digits() == 0 {
			return nil, &JSONError{jsonMsgSyntax}
		}
		isFloat = true
	}
	if d.i < len(d.s) && (d.s[d.i] == 'e' || d.s[d.i] == 'E') {
		d.i++
		if d.i < len(d.s) && (d.s[d.i] == '+' || d.s[d.i] == '-') {
			d.i++
		}
		if digits() == 0 {
			return nil, &JSONError{jsonMsgSyntax}
		}
		isFloat = true
	}
	text := d.s[start:d.i]
	if !isFloat {
		if n, err := strconv.ParseInt(text, 10, 64); err == nil {
			return n, nil
		}
	}
	f, _ := strconv.ParseFloat(text, 64)
	return f, nil
}

func (d *jsonDecoder) str() (any, error) {
	d.i++
	var b strings.Builder
	for {
		if d.i >= len(d.s) {
			return nil, &JSONError{jsonMsgSyntax}
		}
		c := d.s[d.i]
		switch {
		case c == '"':
			d.i++
			return b.String(), nil
		case c < 0x20:
			return nil, &JSONError{jsonMsgCtrl}
		case c == '\\':
			d.i++
			if d.i >= len(d.s) {
				return nil, &JSONError{jsonMsgSyntax}
			}
			switch e := d.s[d.i]; e {
			case '"', '\\', '/':
				b.WriteByte(e)
			case 'b':
				b.WriteByte('\b')
			case 'f':
				b.WriteByte('\f')
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case 'u':
				r, ok := d.hex4(d.i + 1)
				if !ok {
					return nil, &JSONError{jsonMsgSyntax}
				}
				d.i += 4
				if r >= 0xD800 && r <= 0xDBFF {
					if d.i+2 < len(d.s) && d.s[d.i+1] == '\\' && d.s[d.i+2] == 'u' {
						if lo, ok := d.hex4(d.i + 3); ok && lo >= 0xDC00 && lo <= 0xDFFF {
							d.i += 6
							b.WriteRune(utf16.DecodeRune(r, lo))
							break
						}
					}
					return nil, &JSONError{jsonMsgSurrogate}
				}
				if r >= 0xDC00 && r <= 0xDFFF {
					return nil, &JSONError{jsonMsgSurrogate}
				}
				b.WriteRune(r)
			default:
				return nil, &JSONError{jsonMsgSyntax}
			}
			d.i++
		case c >= utf8.RuneSelf:
			// The scanner validates UTF-8 inside strings only; elsewhere a non-ASCII byte is a syntax error.
			r, n := utf8.DecodeRuneInString(d.s[d.i:])
			if r == utf8.RuneError && n <= 1 {
				return nil, &JSONError{jsonMsgUTF8}
			}
			b.WriteString(d.s[d.i : d.i+n])
			d.i += n
		default:
			b.WriteByte(c)
			d.i++
		}
	}
}

func (d *jsonDecoder) hex4(at int) (rune, bool) {
	if at+4 > len(d.s) {
		return 0, false
	}
	n, err := strconv.ParseUint(d.s[at:at+4], 16, 32)
	if err != nil || strings.ContainsAny(d.s[at:at+4], "+-") {
		return 0, false
	}
	return rune(n), true
}
