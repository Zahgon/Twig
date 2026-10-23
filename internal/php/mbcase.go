// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Full (multi-character) case mappings from Unicode's SpecialCasing.txt (unconditional part), which PHP 8.3's
// mbstring applies in mb_strtoupper, mb_strtolower and mb_convert_case.
var specialUpper = map[rune]string{
	0x00DF: "\u0053\u0053",
	0x0149: "\u02BC\u004E",
	0x01F0: "\u004A\u030C",
	0x0390: "\u0399\u0308\u0301",
	0x03B0: "\u03A5\u0308\u0301",
	0x0587: "\u0535\u0552",
	0x1E96: "\u0048\u0331",
	0x1E97: "\u0054\u0308",
	0x1E98: "\u0057\u030A",
	0x1E99: "\u0059\u030A",
	0x1E9A: "\u0041\u02BE",
	0x1F50: "\u03A5\u0313",
	0x1F52: "\u03A5\u0313\u0300",
	0x1F54: "\u03A5\u0313\u0301",
	0x1F56: "\u03A5\u0313\u0342",
	0x1F80: "\u1F08\u0399",
	0x1F81: "\u1F09\u0399",
	0x1F82: "\u1F0A\u0399",
	0x1F83: "\u1F0B\u0399",
	0x1F84: "\u1F0C\u0399",
	0x1F85: "\u1F0D\u0399",
	0x1F86: "\u1F0E\u0399",
	0x1F87: "\u1F0F\u0399",
	0x1F88: "\u1F08\u0399",
	0x1F89: "\u1F09\u0399",
	0x1F8A: "\u1F0A\u0399",
	0x1F8B: "\u1F0B\u0399",
	0x1F8C: "\u1F0C\u0399",
	0x1F8D: "\u1F0D\u0399",
	0x1F8E: "\u1F0E\u0399",
	0x1F8F: "\u1F0F\u0399",
	0x1F90: "\u1F28\u0399",
	0x1F91: "\u1F29\u0399",
	0x1F92: "\u1F2A\u0399",
	0x1F93: "\u1F2B\u0399",
	0x1F94: "\u1F2C\u0399",
	0x1F95: "\u1F2D\u0399",
	0x1F96: "\u1F2E\u0399",
	0x1F97: "\u1F2F\u0399",
	0x1F98: "\u1F28\u0399",
	0x1F99: "\u1F29\u0399",
	0x1F9A: "\u1F2A\u0399",
	0x1F9B: "\u1F2B\u0399",
	0x1F9C: "\u1F2C\u0399",
	0x1F9D: "\u1F2D\u0399",
	0x1F9E: "\u1F2E\u0399",
	0x1F9F: "\u1F2F\u0399",
	0x1FA0: "\u1F68\u0399",
	0x1FA1: "\u1F69\u0399",
	0x1FA2: "\u1F6A\u0399",
	0x1FA3: "\u1F6B\u0399",
	0x1FA4: "\u1F6C\u0399",
	0x1FA5: "\u1F6D\u0399",
	0x1FA6: "\u1F6E\u0399",
	0x1FA7: "\u1F6F\u0399",
	0x1FA8: "\u1F68\u0399",
	0x1FA9: "\u1F69\u0399",
	0x1FAA: "\u1F6A\u0399",
	0x1FAB: "\u1F6B\u0399",
	0x1FAC: "\u1F6C\u0399",
	0x1FAD: "\u1F6D\u0399",
	0x1FAE: "\u1F6E\u0399",
	0x1FAF: "\u1F6F\u0399",
	0x1FB2: "\u1FBA\u0399",
	0x1FB3: "\u0391\u0399",
	0x1FB4: "\u0386\u0399",
	0x1FB6: "\u0391\u0342",
	0x1FB7: "\u0391\u0342\u0399",
	0x1FBC: "\u0391\u0399",
	0x1FC2: "\u1FCA\u0399",
	0x1FC3: "\u0397\u0399",
	0x1FC4: "\u0389\u0399",
	0x1FC6: "\u0397\u0342",
	0x1FC7: "\u0397\u0342\u0399",
	0x1FCC: "\u0397\u0399",
	0x1FD2: "\u0399\u0308\u0300",
	0x1FD3: "\u0399\u0308\u0301",
	0x1FD6: "\u0399\u0342",
	0x1FD7: "\u0399\u0308\u0342",
	0x1FE2: "\u03A5\u0308\u0300",
	0x1FE3: "\u03A5\u0308\u0301",
	0x1FE4: "\u03A1\u0313",
	0x1FE6: "\u03A5\u0342",
	0x1FE7: "\u03A5\u0308\u0342",
	0x1FF2: "\u1FFA\u0399",
	0x1FF3: "\u03A9\u0399",
	0x1FF4: "\u038F\u0399",
	0x1FF6: "\u03A9\u0342",
	0x1FF7: "\u03A9\u0342\u0399",
	0x1FFC: "\u03A9\u0399",
	0xFB00: "\u0046\u0046",
	0xFB01: "\u0046\u0049",
	0xFB02: "\u0046\u004C",
	0xFB03: "\u0046\u0046\u0049",
	0xFB04: "\u0046\u0046\u004C",
	0xFB05: "\u0053\u0054",
	0xFB06: "\u0053\u0054",
	0xFB13: "\u0544\u0546",
	0xFB14: "\u0544\u0535",
	0xFB15: "\u0544\u053B",
	0xFB16: "\u054E\u0546",
	0xFB17: "\u0544\u053D",
}

var specialTitle = map[rune]string{
	0x00DF: "\u0053\u0073",
	0x0149: "\u02BC\u004E",
	0x01F0: "\u004A\u030C",
	0x0390: "\u0399\u0308\u0301",
	0x03B0: "\u03A5\u0308\u0301",
	0x0587: "\u0535\u0582",
	0x1E96: "\u0048\u0331",
	0x1E97: "\u0054\u0308",
	0x1E98: "\u0057\u030A",
	0x1E99: "\u0059\u030A",
	0x1E9A: "\u0041\u02BE",
	0x1F50: "\u03A5\u0313",
	0x1F52: "\u03A5\u0313\u0300",
	0x1F54: "\u03A5\u0313\u0301",
	0x1F56: "\u03A5\u0313\u0342",
	0x1F80: "\u1F88",
	0x1F81: "\u1F89",
	0x1F82: "\u1F8A",
	0x1F83: "\u1F8B",
	0x1F84: "\u1F8C",
	0x1F85: "\u1F8D",
	0x1F86: "\u1F8E",
	0x1F87: "\u1F8F",
	0x1F88: "\u1F88",
	0x1F89: "\u1F89",
	0x1F8A: "\u1F8A",
	0x1F8B: "\u1F8B",
	0x1F8C: "\u1F8C",
	0x1F8D: "\u1F8D",
	0x1F8E: "\u1F8E",
	0x1F8F: "\u1F8F",
	0x1F90: "\u1F98",
	0x1F91: "\u1F99",
	0x1F92: "\u1F9A",
	0x1F93: "\u1F9B",
	0x1F94: "\u1F9C",
	0x1F95: "\u1F9D",
	0x1F96: "\u1F9E",
	0x1F97: "\u1F9F",
	0x1F98: "\u1F98",
	0x1F99: "\u1F99",
	0x1F9A: "\u1F9A",
	0x1F9B: "\u1F9B",
	0x1F9C: "\u1F9C",
	0x1F9D: "\u1F9D",
	0x1F9E: "\u1F9E",
	0x1F9F: "\u1F9F",
	0x1FA0: "\u1FA8",
	0x1FA1: "\u1FA9",
	0x1FA2: "\u1FAA",
	0x1FA3: "\u1FAB",
	0x1FA4: "\u1FAC",
	0x1FA5: "\u1FAD",
	0x1FA6: "\u1FAE",
	0x1FA7: "\u1FAF",
	0x1FA8: "\u1FA8",
	0x1FA9: "\u1FA9",
	0x1FAA: "\u1FAA",
	0x1FAB: "\u1FAB",
	0x1FAC: "\u1FAC",
	0x1FAD: "\u1FAD",
	0x1FAE: "\u1FAE",
	0x1FAF: "\u1FAF",
	0x1FB2: "\u1FBA\u0345",
	0x1FB3: "\u1FBC",
	0x1FB4: "\u0386\u0345",
	0x1FB6: "\u0391\u0342",
	0x1FB7: "\u0391\u0342\u0345",
	0x1FBC: "\u1FBC",
	0x1FC2: "\u1FCA\u0345",
	0x1FC3: "\u1FCC",
	0x1FC4: "\u0389\u0345",
	0x1FC6: "\u0397\u0342",
	0x1FC7: "\u0397\u0342\u0345",
	0x1FCC: "\u1FCC",
	0x1FD2: "\u0399\u0308\u0300",
	0x1FD3: "\u0399\u0308\u0301",
	0x1FD6: "\u0399\u0342",
	0x1FD7: "\u0399\u0308\u0342",
	0x1FE2: "\u03A5\u0308\u0300",
	0x1FE3: "\u03A5\u0308\u0301",
	0x1FE4: "\u03A1\u0313",
	0x1FE6: "\u03A5\u0342",
	0x1FE7: "\u03A5\u0308\u0342",
	0x1FF2: "\u1FFA\u0345",
	0x1FF3: "\u1FFC",
	0x1FF4: "\u038F\u0345",
	0x1FF6: "\u03A9\u0342",
	0x1FF7: "\u03A9\u0342\u0345",
	0x1FFC: "\u1FFC",
	0xFB00: "\u0046\u0066",
	0xFB01: "\u0046\u0069",
	0xFB02: "\u0046\u006C",
	0xFB03: "\u0046\u0066\u0069",
	0xFB04: "\u0046\u0066\u006C",
	0xFB05: "\u0053\u0074",
	0xFB06: "\u0053\u0074",
	0xFB13: "\u0544\u0576",
	0xFB14: "\u0544\u0565",
	0xFB15: "\u0544\u056B",
	0xFB16: "\u054E\u0576",
	0xFB17: "\u0544\u056D",
}

var specialLower = map[rune]string{
	0x0130: "\u0069\u0307",
}

// caseIgnorable approximates Unicode's Case_Ignorable property: marks, format characters, modifier letters and
// symbols, and the word-internal punctuation of UAX #29 (MidLetter, MidNumLet, Single_Quote).
func caseIgnorable(r rune) bool {
	switch r {
	case '\'', '.', ':', '^', '`', '·', '¨', '¯', '´', '¸', '­', '·', '՟', '״', '‘', '’',
		'․', '‧', '︓', '﹒', '﹕', '＇', '．', '：':
		return true
	}
	return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk)
}

// cased is Unicode's Cased property.
func cased(r rune) bool {
	return unicode.In(r, unicode.Lu, unicode.Ll, unicode.Lt, unicode.Other_Lowercase, unicode.Other_Uppercase)
}

// decodeRunes splits UTF-8 into code points the way mbstring's decoder does: an invalid or truncated sequence
// becomes one error marker (-1) per maximal ill-formed subpart, which mbstring outputs as "?".
func decodeRunes(s string) []rune {
	out := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, n := decodeMaximal(s[i:])
		out = append(out, r)
		i += n
	}
	return out
}

// decodeMaximal decodes one code point, or returns -1 and the length of the maximal ill-formed subpart.
func decodeMaximal(s string) (rune, int) {
	c := s[0]
	if c < 0x80 {
		return rune(c), 1
	}
	var need int
	lo, hi := byte(0x80), byte(0xBF)
	switch {
	case c >= 0xC2 && c <= 0xDF:
		need = 1
	case c == 0xE0:
		need, lo = 2, 0xA0
	case c >= 0xE1 && c <= 0xEC, c == 0xEE, c == 0xEF:
		need = 2
	case c == 0xED:
		need, hi = 2, 0x9F
	case c == 0xF0:
		need, lo = 3, 0x90
	case c >= 0xF1 && c <= 0xF3:
		need = 3
	case c == 0xF4:
		need, hi = 3, 0x8F
	default:
		return -1, 1
	}
	for k := 1; k <= need; k++ {
		if k >= len(s) {
			return -1, k
		}
		b := s[k]
		if k == 1 && (b < lo || b > hi) || k > 1 && (b < 0x80 || b > 0xBF) {
			return -1, k
		}
	}
	r, n := utf8.DecodeRuneInString(s)
	return r, n
}

func writeRune(b *strings.Builder, r rune) {
	if r < 0 {
		b.WriteByte('?')
		return
	}
	b.WriteRune(r)
}

func toUpperFull(r rune) string {
	if s, ok := specialUpper[r]; ok {
		return s
	}
	return string(unicode.ToUpper(r))
}

func toTitleFull(r rune) string {
	if s, ok := specialTitle[r]; ok {
		return s
	}
	return string(unicode.ToTitle(r))
}

// finalSigma decides whether a capital sigma at i lowercases to ς: preceded by a cased letter and not followed by
// one, ignoring case-ignorable characters in between.
func finalSigma(rs []rune, i int) bool {
	before := false
	for j := i - 1; j >= 0; j-- {
		if rs[j] >= 0 && caseIgnorable(rs[j]) {
			continue
		}
		before = rs[j] >= 0 && cased(rs[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(rs); j++ {
		if rs[j] >= 0 && caseIgnorable(rs[j]) {
			continue
		}
		return !(rs[j] >= 0 && cased(rs[j]))
	}
	return true
}

func toLowerAt(rs []rune, i int) string {
	r := rs[i]
	if r == 'Σ' && finalSigma(rs, i) {
		return "ς"
	}
	if s, ok := specialLower[r]; ok {
		return s
	}
	return string(unicode.ToLower(r))
}

// MbStrtoupper is mb_strtoupper for UTF-8.
func MbStrtoupper(s string) string {
	var b strings.Builder
	for _, r := range decodeRunes(s) {
		if r < 0 {
			b.WriteByte('?')
			continue
		}
		b.WriteString(toUpperFull(r))
	}
	return b.String()
}

// MbStrtolower is mb_strtolower for UTF-8.
func MbStrtolower(s string) string {
	rs := decodeRunes(s)
	var b strings.Builder
	for i, r := range rs {
		if r < 0 {
			b.WriteByte('?')
			continue
		}
		b.WriteString(toLowerAt(rs, i))
	}
	return b.String()
}

// MbTitle is mb_convert_case($s, MB_CASE_TITLE): the first cased letter of each word is title-cased and the rest
// lowercased; case-ignorable characters (apostrophes, periods, combining marks) do not end a word, anything else
// that is not cased (digits, spaces, '-', '_') does.
func MbTitle(s string) string {
	rs := decodeRunes(s)
	var b strings.Builder
	inWord := false
	for i, r := range rs {
		if r < 0 {
			b.WriteByte('?')
			inWord = false
			continue
		}
		if inWord {
			b.WriteString(toLowerAt(rs, i))
		} else {
			b.WriteString(toTitleFull(r))
		}
		if !caseIgnorable(r) {
			inWord = cased(r)
		}
	}
	return b.String()
}

// MbStrlen is mb_strlen for UTF-8 (an invalid byte counts as one character).
func MbStrlen(s string) int64 {
	return int64(len(decodeRunes(s)))
}

// MbSubstr is mb_substr($s, $start, $length) for UTF-8; hasLength false means a null length.
func MbSubstr(s string, start int64, length int64, hasLength bool) string {
	rs := decodeRunes(s)
	n := int64(len(rs))
	if start < 0 {
		start += n
		if start < 0 {
			start = 0
		}
	}
	if start > n {
		return ""
	}
	end := n
	if hasLength {
		if length < 0 {
			end = n + length
			if end < start {
				return ""
			}
		} else if length < n-start {
			end = start + length
		}
	}
	var b strings.Builder
	for _, r := range rs[start:end] {
		writeRune(&b, r)
	}
	return b.String()
}

// MbSplitChars is preg_match_all('/./us') as Twig uses it to split a string into characters; ok is false when the
// subject is not valid UTF-8 (preg fails with "Malformed UTF-8 characters, possibly incorrectly encoded").
func MbSplitChars(s string) ([]string, bool) {
	if !utf8.ValidString(s) {
		return nil, false
	}
	out := []string{}
	for _, r := range s {
		out = append(out, string(r))
	}
	if len(out) == 0 {
		out = []string{""}
	}
	return out, true
}
