// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// RuntimeError is a Twig\Error\RuntimeError raised by a runtime function, without template context; the engine
// adds the template name and line.
type RuntimeError struct {
	Msg string
}

func (e *RuntimeError) Error() string { return e.Msg }

// Runtime builds a RuntimeError.
func Runtime(format string, args ...any) *RuntimeError {
	return &RuntimeError{Msg: fmt.Sprintf(format, args...)}
}

// EscapeStrategies are the built-in strategies in the order Twig lists them in its error message.
var EscapeStrategies = []string{"html", "js", "url", "css", "html_attr", "html_attr_relaxed"}

// Escape is EscaperRuntime::escape for the UTF-8 charset. autoescape is true when the call comes from automatic
// escaping (then Markup passes through unchanged). Non-string values other than Markup are returned as they are.
func Escape(v any, strategy string, autoescape bool) (any, error) {
	if m, ok := v.(Markup); ok {
		if autoescape {
			return m, nil
		}
		v = m.S
	}
	s, isString := v.(string)
	if !isString {
		if sc, ok := v.(StringConverter); ok {
			str, err := sc.PHPString()
			if err != nil {
				return nil, err
			}
			s = str
		} else {
			switch strategy {
			case "html", "js", "css", "html_attr", "html_attr_relaxed", "url":
				return v, nil
			}
			return nil, Runtime("Invalid escaping strategy \"%s\" (valid ones: \"%s\").", strategy, strings.Join(EscapeStrategies, "\", \""))
		}
	}
	if s == "" {
		return "", nil
	}
	switch strategy {
	case "html":
		return HTMLSpecialChars(s), nil
	case "js":
		if !utf8.ValidString(s) {
			return nil, Runtime("The string to escape is not a valid UTF-8 string.")
		}
		return escapeJS(s), nil
	case "css":
		if !utf8.ValidString(s) {
			return nil, Runtime("The string to escape is not a valid UTF-8 string.")
		}
		return escapeCSS(s), nil
	case "html_attr", "html_attr_relaxed":
		if !utf8.ValidString(s) {
			return nil, Runtime("The string to escape is not a valid UTF-8 string.")
		}
		return escapeHTMLAttr(s, strategy == "html_attr_relaxed"), nil
	case "url":
		return RawURLEncode(s), nil
	}
	return nil, Runtime("Invalid escaping strategy \"%s\" (valid ones: \"%s\").", strategy, strings.Join(EscapeStrategies, "\", \""))
}

// HTMLSpecialChars is htmlspecialchars($s, ENT_QUOTES | ENT_SUBSTITUTE, 'UTF-8'): invalid UTF-8 becomes U+FFFD.
func HTMLSpecialChars(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 16)
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch c {
			case '&':
				b.WriteString("&amp;")
			case '"':
				b.WriteString("&quot;")
			case '\'':
				b.WriteString("&#039;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			default:
				b.WriteByte(c)
			}
			i++
			continue
		}
		n, ok := htmlNextChar(s, i)
		if !ok {
			b.WriteString("\uFFFD")
		} else {
			b.WriteString(s[i : i+n])
		}
		i += n
	}
	return b.String()
}

func utf8Lead(c byte) bool  { return c < 0x80 || c >= 0xC2 && c <= 0xF4 }
func utf8Trail(c byte) bool { return c >= 0x80 && c <= 0xBF }

// htmlNextChar is get_next_char of ext/standard/html.c for UTF-8 (strategy 2 of UTR #36): it returns how many
// bytes the character, or the ill-formed sequence, at i covers.
func htmlNextChar(s string, i int) (int, bool) {
	c := s[i]
	avail := len(s) - i
	switch {
	case c < 0x80:
		return 1, true
	case c < 0xC2:
		return 1, false
	case c < 0xE0:
		if avail < 2 {
			return 1, false
		}
		if !utf8Trail(s[i+1]) {
			if utf8Lead(s[i+1]) {
				return 1, false
			}
			return 2, false
		}
		return 2, true
	case c < 0xF0:
		if avail < 3 || !utf8Trail(s[i+1]) || !utf8Trail(s[i+2]) {
			if avail < 2 || utf8Lead(s[i+1]) {
				return 1, false
			} else if avail < 3 || utf8Lead(s[i+2]) {
				return 2, false
			}
			return 3, false
		}
		cp := rune(c&0x0f)<<12 | rune(s[i+1]&0x3f)<<6 | rune(s[i+2]&0x3f)
		if cp < 0x800 || cp >= 0xD800 && cp <= 0xDFFF {
			return 3, false
		}
		return 3, true
	case c < 0xF5:
		if avail < 4 || !utf8Trail(s[i+1]) || !utf8Trail(s[i+2]) || !utf8Trail(s[i+3]) {
			if avail < 2 || utf8Lead(s[i+1]) {
				return 1, false
			} else if avail < 3 || utf8Lead(s[i+2]) {
				return 2, false
			} else if avail < 4 || utf8Lead(s[i+3]) {
				return 3, false
			}
			return 4, false
		}
		cp := rune(c&0x07)<<18 | rune(s[i+1]&0x3f)<<12 | rune(s[i+2]&0x3f)<<6 | rune(s[i+3]&0x3f)
		if cp < 0x10000 || cp > 0x10FFFF {
			return 4, false
		}
		return 4, true
	}
	return 1, false
}

func isAlnum(c rune) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

func escapeJS(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isAlnum(r) || r == ',' || r == '.' || r == '_' {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '/':
			b.WriteString(`\/`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x10000 {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				u := r - 0x10000
				fmt.Fprintf(&b, `\u%04X\u%04X`, 0xD800|(u>>10), 0xDC00|(u&0x3FF))
			}
		}
	}
	return b.String()
}

func escapeCSS(s string) string {
	var b strings.Builder
	for _, r := range s {
		if isAlnum(r) {
			b.WriteRune(r)
			continue
		}
		fmt.Fprintf(&b, `\%X `, r)
	}
	return b.String()
}

func escapeHTMLAttr(s string, relaxed bool) string {
	var b strings.Builder
	for _, r := range s {
		if isAlnum(r) || r == ',' || r == '.' || r == '-' || r == '_' ||
			relaxed && (r == ':' || r == '@' || r == '[' || r == ']') {
			b.WriteRune(r)
			continue
		}
		// Only the first byte is checked: C1 controls are two bytes long in UTF-8, so only DEL qualifies.
		if r <= 0x1F && r != '\t' && r != '\n' && r != '\r' || r == 0x7F {
			b.WriteString("&#xFFFD;")
			continue
		}
		if r < utf8.RuneSelf {
			switch r {
			case '"':
				b.WriteString("&quot;")
			case '&':
				b.WriteString("&amp;")
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			default:
				fmt.Fprintf(&b, "&#x%02X;", r)
			}
			continue
		}
		fmt.Fprintf(&b, "&#x%04X;", r)
	}
	return b.String()
}

// RawURLEncode is rawurlencode (RFC 3986 unreserved characters kept).
func RawURLEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// URLEncode is urlencode (spaces as '+', '~' encoded).
func URLEncode(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == ' ':
			b.WriteByte('+')
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.':
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(hex[c>>4])
			b.WriteByte(hex[c&15])
		}
	}
	return b.String()
}
