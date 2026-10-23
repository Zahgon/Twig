// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import "strings"

func isCSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + 32
	}
	return c
}

// tagAllowed is php_tag_find: the tag in tbuf is normalised to "<name>" and looked up in the allowed set.
func tagAllowed(tbuf []byte, set string) bool {
	if len(tbuf) == 0 {
		return false
	}
	norm := []byte{}
	state := 0
	at := func(i int) byte {
		if i < 0 || i >= len(tbuf) {
			return 0
		}
		return tbuf[i]
	}
	for t := 0; t < len(tbuf); t++ {
		c := lowerByte(tbuf[t])
		if c == '<' {
			norm = append(norm, c)
			continue
		}
		if c == '>' {
			break
		}
		if !isCSpace(c) {
			if state == 0 {
				state = 1
			}
			if c != '/' || (at(t-1) != '<' && at(t+1) != '>') {
				norm = append(norm, c)
			}
		} else if state == 1 {
			break
		}
	}
	norm = append(norm, '>')
	return strings.Contains(set, string(norm))
}

// StripTags is strip_tags($s, $allowed) with PHP's tag state machine (HTML tags, <? ?> blocks, <! declarations
// and <!-- --> comments; quotes inside tags are honoured). allowed is the normalised "<a><b>" form.
func StripTags(s string, allowed string) string {
	allowed = strings.ToLower(allowed)
	allow := allowed != ""
	buf := s
	at := func(i int) byte {
		if i < 0 || i >= len(buf) {
			return 0
		}
		return buf[i]
	}
	var out []byte
	var tbuf []byte
	state := 0
	depth := 0
	var inQ, lc byte
	br := 0
	isXML := false
	for p := 0; p < len(buf); p++ {
		c := buf[p]
		switch state {
		case 0:
			switch c {
			case 0:
			case '<':
				if inQ != 0 {
					break
				}
				// A '<' followed by whitespace is text, not a tag.
				if isCSpace(at(p + 1)) {
					out = append(out, c)
					break
				}
				lc = '<'
				state = 1
				if allow {
					tbuf = append(tbuf[:0], '<')
				}
			case '>':
				if depth > 0 {
					depth--
					break
				}
				if inQ != 0 {
					break
				}
				out = append(out, c)
			default:
				out = append(out, c)
			}
		case 1:
			regChar := false
			switch c {
			case 0:
			case '<':
				if inQ != 0 {
					break
				}
				if isCSpace(at(p + 1)) {
					regChar = true
					break
				}
				depth++
			case '>':
				if depth > 0 {
					depth--
					break
				}
				if inQ != 0 {
					break
				}
				lc = '>'
				if isXML && at(p-1) == '-' {
					break
				}
				inQ, state, isXML = 0, 0, false
				if allow {
					tbuf = append(tbuf, '>')
					if tagAllowed(tbuf, allowed) {
						out = append(out, tbuf...)
					}
					tbuf = tbuf[:0]
				}
			case '"', '\'':
				if p != 0 && (inQ == 0 || c == inQ) {
					if inQ != 0 {
						inQ = 0
					} else {
						inQ = c
					}
				}
				regChar = true
			case '!':
				// <! starts a declaration or comment.
				if at(p-1) == '<' && p >= 1 {
					state = 3
					lc = c
					break
				}
				regChar = true
			case '?':
				if at(p-1) == '<' && p >= 1 {
					br = 0
					state = 2
					break
				}
				regChar = true
			default:
				regChar = true
			}
			if regChar && allow {
				tbuf = append(tbuf, c)
			}
		case 2:
			switch c {
			case '(':
				if lc != '"' && lc != '\'' {
					lc = '('
					br++
				}
			case ')':
				if lc != '"' && lc != '\'' {
					lc = ')'
					br--
				}
			case '>':
				if depth > 0 {
					depth--
					break
				}
				if inQ != 0 {
					break
				}
				if br == 0 && p >= 1 && lc != '"' && at(p-1) == '?' {
					inQ, state = 0, 0
					tbuf = tbuf[:0]
				}
			case '"', '\'':
				if p >= 1 && at(p-1) != '\\' {
					if lc == c {
						lc = 0
					} else if lc != '\\' {
						lc = c
					}
				}
			case 'l', 'L':
				// "<?xml" is not a PHP block: back to tag state.
				if p > 4 && strings.EqualFold(buf[p-4:p], "<?xm") {
					state = 1
					isXML = true
				}
			}
		case 3:
			switch c {
			case '>':
				if depth > 0 {
					depth--
					break
				}
				if inQ != 0 {
					break
				}
				inQ, state = 0, 0
				tbuf = tbuf[:0]
			case '"', '\'':
				if p != 0 && at(p-1) != '\\' && (inQ == 0 || c == inQ) {
					if inQ != 0 {
						inQ = 0
					} else {
						inQ = c
					}
				}
			case '-':
				if p >= 2 && at(p-1) == '-' && at(p-2) == '!' {
					state = 4
				}
			case 'E', 'e':
				// <!DOCTYPE is a tag.
				if p > 6 && strings.EqualFold(buf[p-6:p], "doctyp") {
					state = 1
				}
			}
		case 4:
			if c == '>' && p >= 2 && at(p-1) == '-' && at(p-2) == '-' {
				inQ, state = 0, 0
				tbuf = tbuf[:0]
			}
		}
	}
	return string(out)
}

// AllowedTags turns strip_tags' allowed_tags argument (a string like "<a><b>" or an array of names) into the
// lookup form.
func AllowedTags(v any) (string, error) {
	switch x := v.(type) {
	case nil:
		return "", nil
	case *Array:
		var b strings.Builder
		var err error
		x.Each(func(_, item any) bool {
			var name string
			name, err = StringArg("strip_tags", 2, "allowed_tags", item)
			if err != nil {
				return false
			}
			b.WriteString("<" + name + ">")
			return true
		})
		return b.String(), err
	}
	return StringArg("strip_tags", 2, "allowed_tags", v)
}
