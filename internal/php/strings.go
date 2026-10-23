// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"sort"
	"strings"
)

// charMask is php_charmask: a list of bytes where "a..z" denotes a range; malformed ranges are skipped (PHP only
// warns about them).
func charMask(mask string) [256]bool {
	var m [256]bool
	for i := 0; i < len(mask); i++ {
		c := mask[i]
		if i+3 < len(mask) && mask[i+1] == '.' && mask[i+2] == '.' && mask[i+3] >= c {
			for x := int(c); x <= int(mask[i+3]); x++ {
				m[x] = true
			}
			i += 3
		} else if i+1 < len(mask) && mask[i] == '.' && mask[i+1] == '.' {
			continue
		} else {
			m[c] = true
		}
	}
	return m
}

// TrimChars is trim/ltrim/rtrim with a character mask; left and right select the sides.
func TrimChars(s, mask string, left, right bool) string {
	m := charMask(mask)
	start, end := 0, len(s)
	if left {
		for start < end && m[s[start]] {
			start++
		}
	}
	if right {
		for end > start && m[s[end-1]] {
			end--
		}
	}
	return s[start:end]
}

// Nl2br is nl2br: "<br />" before every \r\n, \n\r, \n or \r.
func Nl2br(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' || c == '\n' {
			b.WriteString("<br />")
			b.WriteByte(c)
			if i+1 < len(s) && (c == '\r' && s[i+1] == '\n' || c == '\n' && s[i+1] == '\r') {
				b.WriteByte(s[i+1])
				i++
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// Explode is explode($sep, $s, $limit); hasLimit false means PHP_INT_MAX.
func Explode(sep, s string, limit int64, hasLimit bool) (*Array, error) {
	if sep == "" {
		return nil, Throw("ValueError", "explode(): Argument #1 ($separator) cannot be empty")
	}
	if !hasLimit {
		limit = 1<<63 - 1
	}
	parts := strings.Split(s, sep)
	out := NewArray()
	switch {
	case limit > 0:
		if int64(len(parts)) > limit {
			parts = append(parts[:limit-1], strings.Join(parts[limit-1:], sep))
		}
	case limit < 0:
		if s == "" {
			return out, nil
		}
		n := int64(len(parts)) + limit
		if n <= 0 {
			return out, nil
		}
		parts = parts[:n]
	default:
		parts = []string{s}
	}
	for _, p := range parts {
		out.Append(p)
	}
	return out, nil
}

// Implode is implode($glue, $array) with each element converted to a string.
func Implode(glue string, a *Array) (string, error) {
	var b strings.Builder
	first := true
	var err error
	a.Each(func(_, v any) bool {
		if !first {
			b.WriteString(glue)
		}
		first = false
		var s string
		if arr, ok := v.(*Array); ok {
			_ = arr
			s = "Array"
		} else {
			s, err = ToString(v)
			if err != nil {
				return false
			}
		}
		b.WriteString(s)
		return true
	})
	return b.String(), err
}

// Strtr is strtr($s, $pairs): longest keys first, no rescanning of replaced text; an empty key makes PHP return
// the string unchanged... except that PHP 8 ignores empty keys.
func Strtr(s string, pairs *Array) (string, error) {
	type pair struct{ from, to string }
	var list []pair
	var err error
	pairs.Each(func(k, v any) bool {
		from := MustString(k)
		var to string
		to, err = ToString(v)
		if err != nil {
			return false
		}
		if from != "" {
			list = append(list, pair{from, to})
		}
		return true
	})
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return s, nil
	}
	sort.SliceStable(list, func(i, j int) bool { return len(list[i].from) > len(list[j].from) })
	lookup := map[string]string{}
	maxLen, minLen := 0, len(list[0].from)
	for _, p := range list {
		if _, dup := lookup[p.from]; !dup {
			lookup[p.from] = p.to
		}
		if len(p.from) > maxLen {
			maxLen = len(p.from)
		}
		if len(p.from) < minLen {
			minLen = len(p.from)
		}
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		matched := false
		for l := maxLen; l >= minLen; l-- {
			if i+l > len(s) {
				continue
			}
			if to, ok := lookup[s[i:i+l]]; ok {
				b.WriteString(to)
				i += l
				matched = true
				break
			}
		}
		if !matched {
			b.WriteByte(s[i])
			i++
		}
	}
	return b.String(), nil
}

// HTTPBuildQuery is http_build_query($data, ”, '&', PHP_QUERY_RFC3986).
func HTTPBuildQuery(data *Array) string {
	var parts []string
	var walk func(prefix string, a *Array)
	walk = func(prefix string, a *Array) {
		a.Each(func(k, v any) bool {
			key := RawURLEncode(MustString(k))
			if prefix != "" {
				key = prefix + "%5B" + key + "%5D"
			}
			switch x := v.(type) {
			case nil:
				// null values are skipped
			case *Array:
				walk(key, x)
			case bool:
				if x {
					parts = append(parts, key+"=1")
				} else {
					parts = append(parts, key+"=0")
				}
			default:
				parts = append(parts, key+"="+RawURLEncode(MustString(x)))
			}
			return true
		})
	}
	walk("", data)
	return strings.Join(parts, "&")
}

// Ucfirst is ucfirst (ASCII only).
func Ucfirst(s string) string {
	if s == "" {
		return s
	}
	c := s[0]
	if c >= 'a' && c <= 'z' {
		return string(c-32) + s[1:]
	}
	return s
}
