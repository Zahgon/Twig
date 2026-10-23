// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// PregWarning is a warning preg_* functions emit for an unusable pattern; Msg is the text after the
// "preg_match(): " prefix (e.g. "Compilation failed: missing closing parenthesis at offset 3").
type PregWarning struct {
	Msg string
}

func (w *PregWarning) Error() string { return w.Msg }

// PregFailure is a matching failure (preg_* returns false); Msg is preg_last_error_msg().
type PregFailure struct {
	Msg string
}

func (f *PregFailure) Error() string { return f.Msg }

// Regexp is a PCRE pattern (with delimiters and modifiers) translated to Go's regexp. Without the u modifier
// PCRE works on bytes, so the pattern and subjects are mapped byte for byte onto code points 0-255.
type Regexp struct {
	bt  *btProg
	re  *regexp.Regexp
	utf bool
}

var pcreCache sync.Map

// CompilePCRE parses a PHP regular expression such as "/^a+$/i".
func CompilePCRE(regex string) (*Regexp, error) {
	if r, ok := pcreCache.Load(regex); ok {
		return r.(*Regexp), nil
	}
	r, err := compilePCRE(regex)
	if err != nil {
		return nil, err
	}
	pcreCache.Store(regex, r)
	return r, nil
}

func compilePCRE(regex string) (*Regexp, error) {
	p := 0
	for p < len(regex) && isCSpace(regex[p]) {
		p++
	}
	if p >= len(regex) {
		return nil, &PregWarning{"Empty regular expression"}
	}
	delim := regex[p]
	p++
	if isAlnumByte(delim) || delim == '\\' || delim == 0 {
		return nil, &PregWarning{"Delimiter must not be alphanumeric, backslash, or NUL"}
	}
	start, end := delim, delim
	if i := strings.IndexByte("([{<", delim); i >= 0 {
		end = ")]}>"[i]
	}
	pp := p
	if start == end {
		for pp < len(regex) {
			if regex[pp] == '\\' && pp+1 < len(regex) {
				pp++
			} else if regex[pp] == end {
				break
			}
			pp++
		}
	} else {
		depth := 1
		for pp < len(regex) {
			if regex[pp] == '\\' && pp+1 < len(regex) {
				pp++
			} else if regex[pp] == end {
				depth--
				if depth <= 0 {
					break
				}
			} else if regex[pp] == start {
				depth++
			}
			pp++
		}
	}
	if pp >= len(regex) {
		if start == end {
			return nil, &PregWarning{fmt.Sprintf("No ending delimiter '%c' found", end)}
		}
		return nil, &PregWarning{fmt.Sprintf("No ending matching delimiter '%c' found", end)}
	}
	pattern := regex[p:pp]
	var o pcreOptions
	evalMod := false
	for _, c := range []byte(regex[pp+1:]) {
		switch c {
		case 'i':
			o.caseless = true
		case 'm':
			o.multiline = true
		case 'n':
			o.noAutoCapture = true
		case 's':
			o.dotall = true
		case 'x':
			o.extended = true
		case 'A':
			o.anchored = true
		case 'D':
			o.dollarEndOnly = true
		case 'S', 'X', 'J':
		case 'U':
			o.ungreedy = true
		case 'u':
			o.utf = true
		case 'e':
			evalMod = true
		case ' ', '\n', '\r':
		case 0:
			return nil, &PregWarning{"NUL is not a valid modifier"}
		default:
			return nil, &PregWarning{"Unknown modifier '" + string([]byte{c}) + "'"}
		}
	}
	if evalMod {
		return nil, &PregWarning{"The /e modifier is no longer supported, use preg_replace_callback instead"}
	}
	if o.utf && !utf8.ValidString(pattern) {
		off := 0
		for off < len(pattern) {
			r, n := utf8.DecodeRuneInString(pattern[off:])
			if r == utf8.RuneError && n <= 1 {
				break
			}
			off += n
		}
		return nil, &PregWarning{fmt.Sprintf("Compilation failed: UTF-8 error: byte 2 top bits not 0x80 at offset %d", off)}
	}
	t := &pcreTranslator{src: pattern, opt: o}
	goSrc, err := t.translate()
	if err == errUnsupported {
		// Back references and lookaround: Go's regexp cannot express them, so a backtracking matcher runs.
		prog, berr := compileBacktrack(pattern, o)
		if berr != nil {
			return nil, berr
		}
		return &Regexp{bt: prog, utf: o.utf}, nil
	}
	if err != nil {
		return nil, err
	}
	re, cerr := regexp.Compile(goSrc)
	if cerr != nil {
		return nil, &PregWarning{"Compilation failed: " + cerr.Error() + " at offset 0"}
	}
	return &Regexp{re: re, utf: o.utf}, nil
}

func isAlnumByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// latin1 maps every byte to the code point of the same value, so Go's regexp matches bytes.
func latin1(s string) string {
	var b strings.Builder
	b.Grow(len(s) * 2)
	for i := 0; i < len(s); i++ {
		b.WriteRune(rune(s[i]))
	}
	return b.String()
}

func unlatin1(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

func (r *Regexp) subject(s string) (string, error) {
	if r.utf {
		if !utf8.ValidString(s) {
			return "", &PregFailure{"Malformed UTF-8 characters, possibly incorrectly encoded"}
		}
		return s, nil
	}
	return latin1(s), nil
}

// Match is preg_match's result.
func (r *Regexp) Match(s string) (bool, error) {
	subj, err := r.subject(s)
	if err != nil {
		return false, err
	}
	if r.bt != nil {
		return r.bt.match(subj)
	}
	return r.re.MatchString(subj), nil
}

// ReplaceAll is preg_replace with a literal replacement (no back-references).
func (r *Regexp) ReplaceAll(s, repl string) (string, error) {
	subj, err := r.subject(s)
	if err != nil {
		return "", err
	}
	if r.bt != nil {
		return "", &PregFailure{"Internal error"}
	}
	if r.utf {
		return r.re.ReplaceAllLiteralString(subj, repl), nil
	}
	return unlatin1(r.re.ReplaceAllLiteralString(subj, latin1(repl))), nil
}

// PregMatch is preg_match($pattern, $subject) as a boolean; errors are *PregWarning (bad pattern) or
// *PregFailure (matching failed).
func PregMatch(pattern, subject string) (bool, error) {
	r, err := CompilePCRE(pattern)
	if err != nil {
		return false, err
	}
	return r.Match(subject)
}

type pcreOptions struct {
	caseless, multiline, dotall, extended, anchored, dollarEndOnly, ungreedy, utf, noAutoCapture bool
}

type pcreTranslator struct {
	src    string
	pos    int
	opt    pcreOptions
	out    strings.Builder
	groups int
	names  map[string]bool
}

func (t *pcreTranslator) fail(msg string, off int) error {
	return &PregWarning{fmt.Sprintf("Compilation failed: %s at offset %d", msg, off)}
}

func (t *pcreTranslator) translate() (string, error) {
	t.names = map[string]bool{}
	flags := ""
	if t.opt.caseless {
		flags += "i"
	}
	if t.opt.multiline {
		flags += "m"
	}
	if t.opt.dotall {
		flags += "s"
	}
	if t.opt.ungreedy {
		flags += "U"
	}
	if flags != "" {
		t.out.WriteString("(?" + flags + ")")
	}
	if t.opt.anchored {
		t.out.WriteString(`\A(?:`)
	}
	if err := t.alternation(0); err != nil {
		return "", err
	}
	if t.pos < len(t.src) {
		return "", t.fail("unmatched closing parenthesis", t.pos)
	}
	if t.opt.anchored {
		t.out.WriteString(")")
	}
	return t.out.String(), nil
}

// lit writes one pattern character (a byte, or a UTF-8 code point in u mode) as a Go regexp literal.
func (t *pcreTranslator) lit(r rune) {
	t.out.WriteString(regexp.QuoteMeta(string(r)))
}

func (t *pcreTranslator) nextChar() rune {
	if t.opt.utf {
		r, n := utf8.DecodeRuneInString(t.src[t.pos:])
		t.pos += n
		return r
	}
	c := t.src[t.pos]
	t.pos++
	return rune(c)
}

func (t *pcreTranslator) skipExtended() {
	if !t.opt.extended {
		return
	}
	for t.pos < len(t.src) {
		c := t.src[t.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v' {
			t.pos++
			continue
		}
		if c == '#' {
			for t.pos < len(t.src) && t.src[t.pos] != '\n' {
				t.pos++
			}
			continue
		}
		return
	}
}

func (t *pcreTranslator) alternation(depth int) error {
	for {
		if err := t.sequence(depth); err != nil {
			return err
		}
		if t.pos < len(t.src) && t.src[t.pos] == '|' {
			t.pos++
			t.out.WriteByte('|')
			continue
		}
		return nil
	}
}

func (t *pcreTranslator) sequence(depth int) error {
	canRepeat := false
	for {
		t.skipExtended()
		if t.pos >= len(t.src) {
			return nil
		}
		c := t.src[t.pos]
		switch c {
		case '|':
			return nil
		case ')':
			if depth == 0 {
				return t.fail("unmatched closing parenthesis", t.pos)
			}
			return nil
		case '*', '+', '?':
			if !canRepeat {
				return t.fail("quantifier does not follow a repeatable item", t.pos)
			}
			t.pos++
			t.out.WriteByte(c)
			if err := t.quantSuffix(); err != nil {
				return err
			}
			canRepeat = false
			continue
		case '{':
			if q, ok, err := t.braceQuantifier(); err != nil {
				return err
			} else if ok {
				if !canRepeat {
					return t.fail("quantifier does not follow a repeatable item", t.pos-1)
				}
				t.out.WriteString(q)
				if err := t.quantSuffix(); err != nil {
					return err
				}
				canRepeat = false
				continue
			}
			t.pos++
			t.out.WriteString(`\{`)
			canRepeat = true
			continue
		}
		var err error
		canRepeat, err = t.atom(depth)
		if err != nil {
			return err
		}
	}
}

// quantSuffix handles the lazy (?) and possessive (+, treated as greedy) quantifier suffixes.
func (t *pcreTranslator) quantSuffix() error {
	if t.pos < len(t.src) {
		switch t.src[t.pos] {
		case '?':
			t.pos++
			t.out.WriteByte('?')
		case '+':
			t.pos++
		}
	}
	return nil
}

// braceQuantifier recognises {n}, {n,}, {n,m} (PCRE treats anything else as literal text).
func (t *pcreTranslator) braceQuantifier() (string, bool, error) {
	s := t.src[t.pos:]
	i := 1
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 1 {
		return "", false, nil
	}
	min, _ := strconv.Atoi(s[1:i])
	max := min
	if i < len(s) && s[i] == ',' {
		i++
		j := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if j == i {
			max = -1
		} else {
			max, _ = strconv.Atoi(s[j:i])
		}
	}
	if i >= len(s) || s[i] != '}' {
		return "", false, nil
	}
	if min > 65535 || max > 65535 {
		return "", false, t.fail("number too big in {} quantifier", t.pos+i-1)
	}
	if max >= 0 && max < min {
		return "", false, t.fail("numbers out of order in {} quantifier", t.pos+i)
	}
	q := s[:i+1]
	t.pos += i + 1
	if min > 1000 || max > 1000 {
		return "", false, t.fail("number too big in {} quantifier", t.pos-1)
	}
	return q, true, nil
}

func (t *pcreTranslator) atom(depth int) (bool, error) {
	c := t.src[t.pos]
	switch c {
	case '(':
		return true, t.group(depth)
	case '[':
		return true, t.class()
	case '\\':
		return t.escape(false)
	case '.':
		t.pos++
		if t.opt.dotall {
			t.out.WriteString(`(?s:.)`)
		} else {
			t.out.WriteString(`[^\n]`)
		}
		return true, nil
	case '^':
		t.pos++
		if t.opt.multiline {
			// PCRE's multiline ^ does not match after a final newline.
			t.out.WriteString(`(?m:^)`)
		} else {
			t.out.WriteString(`\A`)
		}
		return false, nil
	case '$':
		t.pos++
		switch {
		case t.opt.multiline:
			t.out.WriteString(`(?:(?m:$))`)
		case t.opt.dollarEndOnly:
			t.out.WriteString(`\z`)
		default:
			// End of subject or before a final newline (the newline is consumed, which does not change whether
			// there is a match).
			t.out.WriteString(`(?:\n?\z)`)
		}
		return false, nil
	}
	t.lit(t.nextChar())
	return true, nil
}

func (t *pcreTranslator) group(depth int) error {
	open := t.pos
	t.pos++
	capture := !t.opt.noAutoCapture
	prefix := "("
	if t.pos < len(t.src) && t.src[t.pos] == '?' {
		t.pos++
		if t.pos >= len(t.src) {
			return t.fail("unrecognized character after (? or (?-", t.pos)
		}
		switch c := t.src[t.pos]; {
		case c == ':':
			t.pos++
			prefix, capture = "(?:", false
		case c == '#':
			end := strings.IndexByte(t.src[t.pos:], ')')
			if end < 0 {
				return t.fail("missing ) after (?# comment", len(t.src))
			}
			t.pos += end + 1
			return nil
		case c == '=' || c == '!' || c == '<' && t.pos+1 < len(t.src) && (t.src[t.pos+1] == '=' || t.src[t.pos+1] == '!'):
			return errUnsupported
		case c == '>' || c == '|':
			// Atomic groups and branch reset groups behave like non-capturing groups for plain matching.
			t.pos++
			prefix, capture = "(?:", false
		case c == 'P' || c == '<' || c == '\'':
			if c == 'P' {
				t.pos++
				if t.pos >= len(t.src) || t.src[t.pos] != '<' {
					return t.fail("unrecognized character after (?P", t.pos)
				}
			}
			closer := byte('>')
			if t.src[t.pos] == '\'' {
				closer = '\''
			}
			t.pos++
			nameStart := t.pos
			for t.pos < len(t.src) && (isAlnumByte(t.src[t.pos]) || t.src[t.pos] == '_') {
				t.pos++
			}
			name := t.src[nameStart:t.pos]
			if name == "" {
				return t.fail("subpattern name expected", t.pos)
			}
			if name[0] >= '0' && name[0] <= '9' {
				return t.fail("subpattern name must not begin with a digit", nameStart)
			}
			if t.pos >= len(t.src) || t.src[t.pos] != closer {
				return t.fail("syntax error in subpattern name (missing terminator?)", t.pos)
			}
			t.pos++
			if t.names[name] {
				return t.fail("two named subpatterns have the same name (PCRE2_DUPNAMES not set)", t.pos-1)
			}
			t.names[name] = true
			prefix, capture = "(?P<"+name+">", false
			t.groups++
		default:
			// Inline options (?imsxU-imsxU) or (?i:...).
			on, neg := "", false
			flags := map[byte]bool{}
			for t.pos < len(t.src) && t.src[t.pos] != ')' && t.src[t.pos] != ':' {
				f := t.src[t.pos]
				switch f {
				case '-':
					neg = true
				case 'i', 'm', 's', 'U':
					if neg {
						on += "-"
						neg = false
					}
					on += string(f)
					flags[f] = true
				case 'x', 'n', 'J':
				default:
					return t.fail("unrecognized character after (? or (?-", t.pos)
				}
				t.pos++
			}
			if t.pos >= len(t.src) {
				return t.fail("missing closing parenthesis", len(t.src))
			}
			on = strings.ReplaceAll(on, "-", "-")
			if t.src[t.pos] == ')' {
				t.pos++
				if on != "" {
					t.out.WriteString("(?" + normalizeFlags(on) + ")")
				}
				return nil
			}
			t.pos++
			prefix, capture = "(?"+normalizeFlags(on)+":", false
			if on == "" {
				prefix = "(?:"
			}
		}
	}
	if capture {
		t.groups++
	}
	t.out.WriteString(prefix)
	if err := t.alternation(depth + 1); err != nil {
		return err
	}
	if t.pos >= len(t.src) || t.src[t.pos] != ')' {
		_ = open
		return t.fail("missing closing parenthesis", len(t.src))
	}
	t.pos++
	t.out.WriteByte(')')
	return nil
}

// normalizeFlags turns "i-ms" style flag strings (with possibly several '-') into Go's "i-ms".
func normalizeFlags(s string) string {
	var on, off strings.Builder
	neg := false
	for i := 0; i < len(s); i++ {
		if s[i] == '-' {
			neg = true
			continue
		}
		if neg {
			off.WriteByte(s[i])
		} else {
			on.WriteByte(s[i])
		}
	}
	if off.Len() > 0 {
		return on.String() + "-" + off.String()
	}
	return on.String()
}

var posixClasses = map[string]string{
	"alpha": "A-Za-z", "digit": "0-9", "alnum": "0-9A-Za-z", "upper": "A-Z", "lower": "a-z", "space": `\t\n\v\f\r `,
	"blank": `\t `, "punct": "!-/:-@\\[-`{-~", "xdigit": "0-9A-Fa-f", "word": "0-9A-Za-z_", "cntrl": `\x00-\x1f\x7f`,
	"print": ` -~`, "graph": `!-~`, "ascii": `\x00-\x7f`,
}

func (t *pcreTranslator) class() error {
	start := t.pos
	t.pos++
	var b strings.Builder
	b.WriteByte('[')
	if t.pos < len(t.src) && t.src[t.pos] == '^' {
		b.WriteByte('^')
		t.pos++
	}
	first := true
	prev := rune(-1)
	for {
		if t.pos >= len(t.src) {
			return t.fail("missing terminating ] for character class", len(t.src))
		}
		c := t.src[t.pos]
		if c == ']' && !first {
			t.pos++
			break
		}
		first = false
		if c == '[' && t.pos+1 < len(t.src) && t.src[t.pos+1] == ':' {
			end := strings.Index(t.src[t.pos:], ":]")
			if end > 0 {
				name := t.src[t.pos+2 : t.pos+end]
				negate := strings.HasPrefix(name, "^")
				name = strings.TrimPrefix(name, "^")
				set, ok := posixClasses[name]
				if !ok {
					return t.fail("unknown POSIX class name", t.pos+2)
				}
				if negate {
					b.WriteString("[:^" + name + ":]")
				} else {
					b.WriteString(set)
				}
				t.pos += end + 2
				prev = -1
				continue
			}
		}
		var r rune
		if c == '\\' {
			t.pos++
			if t.pos >= len(t.src) {
				return t.fail("\\ at end of pattern", t.pos)
			}
			e := t.src[t.pos]
			switch e {
			case 'd', 'D', 'w', 'W', 's', 'S', 'h', 'H', 'v', 'V':
				t.pos++
				if t.pos+1 < len(t.src) && t.src[t.pos] == '-' && t.src[t.pos+1] != ']' {
					return t.fail("invalid range in character class", t.pos)
				}
				b.WriteString(t.shorthand(e, true))
				prev = -1
				continue
			case 'p', 'P':
				p, err := t.property(e)
				if err != nil {
					return err
				}
				b.WriteString(p)
				prev = -1
				continue
			}
			if e == 'd' || e == 'D' || e == 'w' || e == 'W' || e == 's' || e == 'S' {
				// unreachable: handled above
			}
			var err error
			r, err = t.escapedChar()
			if err != nil {
				return err
			}
		} else {
			r = t.nextChar()
		}
		// Ranges.
		if t.pos+1 < len(t.src) && t.src[t.pos] == '-' && t.src[t.pos+1] != ']' {
			t.pos++
			rangeEndPos := t.pos
			var hi rune
			if t.src[t.pos] == '\\' {
				t.pos++
				if t.pos < len(t.src) && strings.IndexByte("dDwWsShHvVpP", t.src[t.pos]) >= 0 {
					return t.fail("invalid range in character class", t.pos)
				}
				var err error
				hi, err = t.escapedChar()
				if err != nil {
					return err
				}
			} else if t.src[t.pos] == '[' && t.pos+1 < len(t.src) && t.src[t.pos+1] == ':' {
				return t.fail("invalid range in character class", t.pos+1)
			} else {
				hi = t.nextChar()
			}
			if hi < r {
				return t.fail("range out of order in character class", t.pos-1)
			}
			_ = rangeEndPos
			b.WriteString(classChar(r) + "-" + classChar(hi))
			prev = -1
			continue
		}
		b.WriteString(classChar(r))
		prev = r
	}
	_ = prev
	_ = start
	b.WriteByte(']')
	t.out.WriteString(b.String())
	return nil
}

func classChar(r rune) string {
	if r < 0x80 && !isAlnumByte(byte(r)) {
		return fmt.Sprintf(`\x{%x}`, r)
	}
	return string(r)
}

func (t *pcreTranslator) shorthand(e byte, inClass bool) string {
	var s string
	switch e {
	case 'd', 'D':
		s = `0-9`
		if t.opt.utf {
			s = `\p{Nd}`
		}
	case 'w', 'W':
		s = `0-9A-Za-z_`
		if t.opt.utf {
			s = `\p{L}\p{N}_\p{Mn}\p{Pc}`
		}
	case 's', 'S':
		s = `\t\n\x{b}\f\r `
		if t.opt.utf {
			s = `\t\n\x{b}\f\r \x{85}\p{Z}`
		}
	case 'h', 'H':
		s = `\t \x{a0}`
		if t.opt.utf {
			s = `\t \x{a0}\x{1680}\x{180e}\x{2000}-\x{200a}\x{202f}\x{205f}\x{3000}`
		}
	case 'v', 'V':
		s = `\n\x{b}\f\r\x{85}`
		if t.opt.utf {
			s += `\x{2028}\x{2029}`
		}
	}
	upper := e >= 'A' && e <= 'Z'
	if inClass {
		if upper {
			// A negated shorthand inside a class; Go supports \D etc. only for its own definitions, so this is an
			// approximation for the ASCII forms.
			return `\` + string(e)
		}
		return s
	}
	if upper {
		return "[^" + s + "]"
	}
	return "[" + s + "]"
}

func (t *pcreTranslator) property(e byte) (string, error) {
	start := t.pos - 1
	t.pos++
	var name string
	if t.pos < len(t.src) && t.src[t.pos] == '{' {
		end := strings.IndexByte(t.src[t.pos:], '}')
		if end < 0 {
			return "", t.fail("malformed \\P or \\p sequence", t.pos)
		}
		name = t.src[t.pos+1 : t.pos+end]
		t.pos += end + 1
	} else if t.pos < len(t.src) {
		name = t.src[t.pos : t.pos+1]
		t.pos++
	}
	neg := e == 'P'
	if strings.HasPrefix(name, "^") {
		neg = !neg
		name = name[1:]
	}
	if name == "L&" {
		name = "Lu}\\p{Ll}\\p{Lt"
	} else if _, err := regexp.Compile(`\p{` + name + `}`); err != nil || name == "" {
		_ = start
		return "", t.fail("unknown property after \\P or \\p", t.pos)
	}
	if neg {
		return `\P{` + name + `}`, nil
	}
	return `\p{` + name + `}`, nil
}

// escapedChar reads the character escape after a backslash (t.pos at the escape letter).
func (t *pcreTranslator) escapedChar() (rune, error) {
	e := t.src[t.pos]
	t.pos++
	switch e {
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	case 'f':
		return '\f', nil
	case 'e':
		return 0x1b, nil
	case 'a':
		return 7, nil
	case 'x':
		if t.pos < len(t.src) && t.src[t.pos] == '{' {
			end := strings.IndexByte(t.src[t.pos:], '}')
			if end < 0 {
				return 0, t.fail("missing terminating } in \\x{}", t.pos)
			}
			n, err := strconv.ParseUint(t.src[t.pos+1:t.pos+end], 16, 32)
			if err != nil {
				return 0, t.fail("non-hex character in \\x{} (closing brace missing?)", t.pos+1)
			}
			t.pos += end + 1
			if !t.opt.utf && n > 0xff {
				return 0, t.fail("character code point value in \\x{} or \\o{} is too large", t.pos-1)
			}
			return rune(n), nil
		}
		j := t.pos
		for j < len(t.src) && j < t.pos+2 && isHex(t.src[j]) {
			j++
		}
		n, _ := strconv.ParseUint("0"+t.src[t.pos:j], 16, 32)
		t.pos = j
		return rune(n), nil
	case 'c':
		if t.pos >= len(t.src) {
			return 0, t.fail("\\c at end of pattern", t.pos)
		}
		c := t.src[t.pos]
		t.pos++
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		return rune(c ^ 0x40), nil
	case 'o':
		if t.pos < len(t.src) && t.src[t.pos] == '{' {
			end := strings.IndexByte(t.src[t.pos:], '}')
			if end > 0 {
				n, err := strconv.ParseUint(t.src[t.pos+1:t.pos+end], 8, 32)
				if err == nil {
					t.pos += end + 1
					return rune(n), nil
				}
			}
		}
		return 0, t.fail("missing opening brace after \\o", t.pos)
	case '0':
		j := t.pos
		for j < len(t.src) && j < t.pos+2 && t.src[j] >= '0' && t.src[j] <= '7' {
			j++
		}
		n, _ := strconv.ParseUint("0"+t.src[t.pos:j], 8, 32)
		t.pos = j
		return rune(n), nil
	}
	if isAlnumByte(e) {
		return 0, t.fail("unrecognized character follows \\", t.pos-1)
	}
	if e >= 0x80 && t.opt.utf {
		t.pos--
		return t.nextChar(), nil
	}
	return rune(e), nil
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// escape handles a backslash sequence outside a class; it reports whether the result can be repeated.
func (t *pcreTranslator) escape(inClass bool) (bool, error) {
	t.pos++
	if t.pos >= len(t.src) {
		return false, t.fail("\\ at end of pattern", t.pos)
	}
	e := t.src[t.pos]
	switch e {
	case 'd', 'D', 'w', 'W', 's', 'S', 'h', 'H', 'v', 'V':
		t.pos++
		t.out.WriteString(t.shorthand(e, false))
		return true, nil
	case 'N':
		t.pos++
		t.out.WriteString(`[^\n]`)
		return true, nil
	case 'R':
		t.pos++
		t.out.WriteString(`(?:\r\n|[\n\x{b}\f\r\x{85}])`)
		return true, nil
	case 'p', 'P':
		p, err := t.property(e)
		if err != nil {
			return false, err
		}
		t.out.WriteString(p)
		return true, nil
	case 'b', 'B':
		t.pos++
		t.out.WriteString(`\` + string(e))
		return false, nil
	case 'A':
		t.pos++
		t.out.WriteString(`\A`)
		return false, nil
	case 'z':
		t.pos++
		t.out.WriteString(`\z`)
		return false, nil
	case 'Z':
		t.pos++
		t.out.WriteString(`(?:\n?\z)`)
		return false, nil
	case 'Q':
		t.pos++
		end := strings.Index(t.src[t.pos:], `\E`)
		lit := t.src[t.pos:]
		if end >= 0 {
			lit = t.src[t.pos : t.pos+end]
			t.pos += end + 2
		} else {
			t.pos = len(t.src)
		}
		for _, r := range t.runes(lit) {
			t.lit(r)
		}
		return true, nil
	case 'E':
		t.pos++
		return false, nil
	case 'G', 'K', 'g', 'k':
		if e == 'k' && (t.pos+1 >= len(t.src) || strings.IndexByte("{<'", t.src[t.pos+1]) < 0) {
			return false, t.fail("\\k is not followed by a braced, angle-bracketed, or quoted name", t.pos+1)
		}
		return false, errUnsupported
	}
	if e >= '1' && e <= '9' {
		j := t.pos
		for j < len(t.src) && t.src[j] >= '0' && t.src[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(t.src[t.pos:j])
		if n > countGroups(t.src) {
			return false, t.fail("reference to non-existent subpattern", j-1)
		}
		return false, errUnsupported
	}
	r, err := t.escapedChar()
	if err != nil {
		return false, err
	}
	t.lit(r)
	return true, nil
}

func (t *pcreTranslator) runes(s string) []rune {
	if t.opt.utf {
		return []rune(s)
	}
	out := make([]rune, len(s))
	for i := 0; i < len(s); i++ {
		out[i] = rune(s[i])
	}
	return out
}
