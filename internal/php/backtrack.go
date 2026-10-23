// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package php

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A small backtracking matcher for the PCRE features Go's regexp lacks (back references, lookahead and
// lookbehind, \K). Single-character items are still matched by Go regexps built by the translator.

var errUnsupported = errors.New("pcre construct needs the backtracking matcher")

// pcreBacktrackLimit is pcre.backtrack_limit.
const pcreBacktrackLimit = 1000000

type btNode interface {
	match(m *btMatcher, i int, k func(int) bool) bool
}

type btProg struct {
	root     btNode
	anchored bool
	groups   int
}

type btMatcher struct {
	s     string
	caps  [][2]int
	steps int
	utf   bool
	limit bool
}

func (m *btMatcher) step() bool {
	m.steps++
	if m.steps > pcreBacktrackLimit {
		m.limit = true
	}
	return !m.limit
}

func (p *btProg) match(s string) (bool, error) {
	m := &btMatcher{s: s}
	for start := 0; start <= len(s); {
		m.caps = make([][2]int, p.groups+1)
		for i := range m.caps {
			m.caps[i] = [2]int{-1, -1}
		}
		if p.root.match(m, start, func(int) bool { return true }) {
			return true, nil
		}
		if m.limit {
			return false, &PregFailure{"Backtrack limit exhausted"}
		}
		if p.anchored || start == len(s) {
			break
		}
		_, n := utf8.DecodeRuneInString(s[start:])
		start += n
	}
	return false, nil
}

type btSeq []btNode

func (q btSeq) match(m *btMatcher, i int, k func(int) bool) bool {
	if len(q) == 0 {
		return k(i)
	}
	return q[0].match(m, i, func(j int) bool { return q[1:].match(m, j, k) })
}

type btAlt []btNode

func (a btAlt) match(m *btMatcher, i int, k func(int) bool) bool {
	for _, n := range a {
		if !m.step() {
			return false
		}
		if n.match(m, i, k) {
			return true
		}
	}
	return false
}

type btFrag struct{ re *regexp.Regexp }

func (f btFrag) match(m *btMatcher, i int, k func(int) bool) bool {
	if !m.step() {
		return false
	}
	loc := f.re.FindStringIndex(m.s[i:])
	if loc == nil {
		return false
	}
	return k(i + loc[1])
}

type btRepeat struct {
	n        btNode
	min, max int
	greedy   bool
}

func (r btRepeat) match(m *btMatcher, i int, k func(int) bool) bool {
	var rec func(count, at int) bool
	rec = func(count, at int) bool {
		if !m.step() {
			return false
		}
		try := func() bool {
			return (r.max < 0 || count < r.max) && r.n.match(m, at, func(j int) bool {
				if j == at && count >= r.min {
					return false // an empty iteration cannot make progress
				}
				return rec(count+1, j)
			})
		}
		if r.greedy {
			if try() {
				return true
			}
			return count >= r.min && k(at)
		}
		if count >= r.min && k(at) {
			return true
		}
		return try()
	}
	return rec(0, i)
}

type btGroup struct {
	n   btNode
	idx int
}

func (g btGroup) match(m *btMatcher, i int, k func(int) bool) bool {
	old := m.caps[g.idx]
	if g.n.match(m, i, func(j int) bool {
		saved := m.caps[g.idx]
		m.caps[g.idx] = [2]int{i, j}
		if k(j) {
			return true
		}
		m.caps[g.idx] = saved
		return false
	}) {
		return true
	}
	m.caps[g.idx] = old
	return false
}

type btBackref struct {
	idx      int
	caseless bool
}

func (b btBackref) match(m *btMatcher, i int, k func(int) bool) bool {
	c := m.caps[b.idx]
	if c[0] < 0 {
		return false
	}
	sub := m.s[c[0]:c[1]]
	if i+len(sub) > len(m.s) {
		return false
	}
	cand := m.s[i : i+len(sub)]
	if cand == sub || b.caseless && strings.EqualFold(cand, sub) {
		return k(i + len(sub))
	}
	return false
}

type btLook struct {
	n      btNode
	behind bool
	neg    bool
}

func (l btLook) match(m *btMatcher, i int, k func(int) bool) bool {
	found := false
	if l.behind {
		for j := i; j >= 0 && !found; j-- {
			found = l.n.match(m, j, func(e int) bool { return e == i })
		}
	} else {
		found = l.n.match(m, i, func(int) bool { return true })
	}
	if found != l.neg {
		return k(i)
	}
	return false
}

type btAssert func(m *btMatcher, i int) bool

func (a btAssert) match(m *btMatcher, i int, k func(int) bool) bool {
	return a(m, i) && k(i)
}

type btParser struct {
	t      *pcreTranslator
	opt    pcreOptions
	groups int
	names  map[string]int
}

// compileBacktrack parses a pattern the translator found valid but beyond Go's regexp.
func compileBacktrack(pattern string, o pcreOptions) (*btProg, error) {
	p := &btParser{t: &pcreTranslator{src: pattern, opt: o, names: map[string]bool{}}, opt: o, names: map[string]int{}}
	root, err := p.alternation()
	if err != nil {
		return nil, err
	}
	if p.t.pos < len(pattern) {
		return nil, p.t.fail("unmatched closing parenthesis", p.t.pos)
	}
	return &btProg{root: root, anchored: o.anchored, groups: p.groups}, nil
}

func (p *btParser) alternation() (btNode, error) {
	var alts btAlt
	for {
		seq, err := p.sequence()
		if err != nil {
			return nil, err
		}
		alts = append(alts, seq)
		t := p.t
		if t.pos < len(t.src) && t.src[t.pos] == '|' {
			t.pos++
			continue
		}
		if len(alts) == 1 {
			return alts[0], nil
		}
		return alts, nil
	}
}

func (p *btParser) sequence() (btNode, error) {
	var seq btSeq
	t := p.t
	for {
		t.opt = p.opt
		t.skipExtended()
		if t.pos >= len(t.src) || t.src[t.pos] == '|' || t.src[t.pos] == ')' {
			return seq, nil
		}
		node, repeatable, err := p.atom()
		if err != nil {
			return nil, err
		}
		if node == nil {
			continue
		}
		if repeatable {
			node, err = p.quantifier(node)
			if err != nil {
				return nil, err
			}
		}
		seq = append(seq, node)
	}
}

func (p *btParser) quantifier(n btNode) (btNode, error) {
	t := p.t
	if t.pos >= len(t.src) {
		return n, nil
	}
	min, max := -1, -1
	switch t.src[t.pos] {
	case '*':
		min, max = 0, -1
		t.pos++
	case '+':
		min, max = 1, -1
		t.pos++
	case '?':
		min, max = 0, 1
		t.pos++
	case '{':
		save := t.pos
		q, ok, err := t.braceQuantifier()
		if err != nil {
			return nil, err
		}
		if !ok {
			t.pos = save
			return n, nil
		}
		body := q[1 : len(q)-1]
		lo, hi, comma := body, body, false
		if c := strings.IndexByte(body, ','); c >= 0 {
			lo, hi, comma = body[:c], body[c+1:], true
		}
		min, _ = strconv.Atoi(lo)
		max = min
		if comma {
			max = -1
			if hi != "" {
				max, _ = strconv.Atoi(hi)
			}
		}
	default:
		return n, nil
	}
	greedy := !p.opt.ungreedy
	if t.pos < len(t.src) && t.src[t.pos] == '?' {
		greedy = !greedy
		t.pos++
	} else if t.pos < len(t.src) && t.src[t.pos] == '+' {
		t.pos++
	}
	return btRepeat{n: n, min: min, max: max, greedy: greedy}, nil
}

func (p *btParser) wordAt(m *btMatcher, i int) bool {
	if i < 0 || i >= len(m.s) {
		return false
	}
	r, _ := utf8.DecodeRuneInString(m.s[i:])
	if p.opt.utf {
		return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
	}
	return r == '_' || r < 0x80 && isAlnumByte(byte(r))
}

func (p *btParser) wordBefore(m *btMatcher, i int) bool {
	if i <= 0 {
		return false
	}
	_, n := utf8.DecodeLastRuneInString(m.s[:i])
	return p.wordAt(m, i-n)
}

func (p *btParser) atom() (btNode, bool, error) {
	t := p.t
	c := t.src[t.pos]
	o := p.opt
	switch c {
	case '(':
		return p.group()
	case '^':
		t.pos++
		if o.multiline {
			return btAssert(func(m *btMatcher, i int) bool {
				return i == 0 || m.s[i-1] == '\n' && i < len(m.s)
			}), false, nil
		}
		return btAssert(func(m *btMatcher, i int) bool { return i == 0 }), false, nil
	case '$':
		t.pos++
		switch {
		case o.multiline:
			return btAssert(func(m *btMatcher, i int) bool { return i == len(m.s) || m.s[i] == '\n' }), false, nil
		case o.dollarEndOnly:
			return btAssert(func(m *btMatcher, i int) bool { return i == len(m.s) }), false, nil
		}
		return btAssert(endOrFinalNewline), false, nil
	case '\\':
		if t.pos+1 < len(t.src) {
			e := t.src[t.pos+1]
			switch e {
			case 'b', 'B':
				t.pos += 2
				want := e == 'b'
				return btAssert(func(m *btMatcher, i int) bool { return (p.wordBefore(m, i) != p.wordAt(m, i)) == want }), false, nil
			case 'A':
				t.pos += 2
				return btAssert(func(m *btMatcher, i int) bool { return i == 0 }), false, nil
			case 'z':
				t.pos += 2
				return btAssert(func(m *btMatcher, i int) bool { return i == len(m.s) }), false, nil
			case 'Z':
				t.pos += 2
				return btAssert(endOrFinalNewline), false, nil
			case 'K', 'E':
				t.pos += 2
				return nil, false, nil
			case 'G':
				t.pos += 2
				return btAssert(func(m *btMatcher, i int) bool { return i == 0 }), false, nil
			case 'k', 'g':
				return p.namedRef()
			}
			if e >= '1' && e <= '9' {
				j := t.pos + 1
				for j < len(t.src) && t.src[j] >= '0' && t.src[j] <= '9' {
					j++
				}
				n, _ := strconv.Atoi(t.src[t.pos+1 : j])
				t.pos = j
				return btBackref{idx: n, caseless: o.caseless}, true, nil
			}
		}
	}
	// A character item: let the translator write it as a Go regexp and match one item at a time.
	t.out.Reset()
	var err error
	if c == '[' {
		err = t.class()
	} else {
		_, err = t.atom(1)
	}
	if err != nil {
		return nil, false, err
	}
	flags := ""
	if o.caseless {
		flags = "(?i)"
	}
	re, cerr := regexp.Compile(`\A` + flags + `(?:` + t.out.String() + `)`)
	if cerr != nil {
		return nil, false, t.fail(cerr.Error(), t.pos)
	}
	return btFrag{re}, true, nil
}

func endOrFinalNewline(m *btMatcher, i int) bool {
	return i == len(m.s) || i == len(m.s)-1 && m.s[i] == '\n'
}

func (p *btParser) namedRef() (btNode, bool, error) {
	t := p.t
	t.pos += 2
	if t.pos >= len(t.src) {
		return nil, false, t.fail("\\k is not followed by a braced, angle-bracketed, or quoted name", t.pos)
	}
	open := t.src[t.pos]
	closer := map[byte]byte{'{': '}', '<': '>', '\'': '\''}[open]
	if closer == 0 {
		// \g1 / \g-1 style numeric references.
		j := t.pos
		for j < len(t.src) && t.src[j] >= '0' && t.src[j] <= '9' {
			j++
		}
		n, _ := strconv.Atoi(t.src[t.pos:j])
		t.pos = j
		return btBackref{idx: n, caseless: p.opt.caseless}, true, nil
	}
	end := strings.IndexByte(t.src[t.pos+1:], closer)
	if end < 0 {
		return nil, false, t.fail("syntax error in subpattern name (missing terminator?)", len(t.src))
	}
	name := t.src[t.pos+1 : t.pos+1+end]
	t.pos += end + 2
	if n, err := strconv.Atoi(name); err == nil {
		return btBackref{idx: n, caseless: p.opt.caseless}, true, nil
	}
	idx, ok := p.names[name]
	if !ok {
		idx = namedGroupIndex(t.src, name)
	}
	if idx == 0 {
		return nil, false, t.fail("reference to non-existent subpattern", t.pos-1)
	}
	return btBackref{idx: idx, caseless: p.opt.caseless}, true, nil
}

func (p *btParser) group() (btNode, bool, error) {
	t := p.t
	t.pos++
	saved := p.opt
	defer func() { p.opt = saved }()
	capture := !p.opt.noAutoCapture
	var look *btLook
	name := ""
	if t.pos < len(t.src) && t.src[t.pos] == '?' {
		t.pos++
		switch c := t.src[t.pos]; {
		case c == ':' || c == '>' || c == '|':
			t.pos++
			capture = false
		case c == '#':
			end := strings.IndexByte(t.src[t.pos:], ')')
			t.pos += end + 1
			return nil, false, nil
		case c == '=' || c == '!':
			t.pos++
			look = &btLook{neg: c == '!'}
		case c == '<' && t.pos+1 < len(t.src) && (t.src[t.pos+1] == '=' || t.src[t.pos+1] == '!'):
			look = &btLook{behind: true, neg: t.src[t.pos+1] == '!'}
			t.pos += 2
		case c == 'P' || c == '<' || c == '\'':
			if c == 'P' {
				t.pos++
			}
			closer := byte('>')
			if t.src[t.pos] == '\'' {
				closer = '\''
			}
			end := strings.IndexByte(t.src[t.pos+1:], closer)
			name = t.src[t.pos+1 : t.pos+1+end]
			t.pos += end + 2
			capture = true
		default:
			// Inline options.
			neg := false
			for t.pos < len(t.src) && t.src[t.pos] != ')' && t.src[t.pos] != ':' {
				switch t.src[t.pos] {
				case '-':
					neg = true
				case 'i':
					p.opt.caseless = !neg
				case 'm':
					p.opt.multiline = !neg
				case 's':
					p.opt.dotall = !neg
				case 'x':
					p.opt.extended = !neg
				case 'U':
					p.opt.ungreedy = !neg
				}
				t.pos++
			}
			if t.pos < len(t.src) && t.src[t.pos] == ')' {
				// The options apply to the rest of the enclosing group.
				t.pos++
				saved = p.opt
				return nil, false, nil
			}
			t.pos++
			capture = false
		}
	}
	idx := 0
	if capture && look == nil {
		p.groups++
		idx = p.groups
		if name != "" {
			p.names[name] = idx
		}
	}
	body, err := p.alternation()
	if err != nil {
		return nil, false, err
	}
	if t.pos >= len(t.src) || t.src[t.pos] != ')' {
		return nil, false, t.fail("missing closing parenthesis", len(t.src))
	}
	t.pos++
	if look != nil {
		look.n = body
		return *look, false, nil
	}
	if idx > 0 {
		return btGroup{n: body, idx: idx}, true, nil
	}
	return body, true, nil
}

// countGroups counts the capturing groups of a pattern (for checking back references).
func countGroups(src string) int {
	n := 0
	inClass := false
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '\\':
			i++
		case inClass:
			if c == ']' {
				inClass = false
			}
		case c == '[':
			inClass = true
			if i+1 < len(src) && src[i+1] == ']' {
				i++
			}
		case c == '(':
			if i+1 < len(src) && src[i+1] == '?' {
				if i+2 < len(src) && (src[i+2] == 'P' || src[i+2] == '\'' ||
					src[i+2] == '<' && i+3 < len(src) && src[i+3] != '=' && src[i+3] != '!') {
					n++
				}
				continue
			}
			n++
		}
	}
	return n
}

func namedGroupIndex(src, name string) int {
	n := 0
	for i := 0; i < len(src); i++ {
		if src[i] == '\\' {
			i++
			continue
		}
		if src[i] != '(' {
			continue
		}
		if i+1 < len(src) && src[i+1] == '?' {
			rest := src[i+2:]
			for _, pre := range []string{"P<", "<", "'"} {
				if strings.HasPrefix(rest, pre) && !strings.HasPrefix(rest, "<=") && !strings.HasPrefix(rest, "<!") {
					n++
					nm := rest[len(pre):]
					if j := strings.IndexAny(nm, ">'"); j >= 0 && nm[:j] == name {
						return n
					}
					break
				}
			}
			continue
		}
		n++
	}
	return 0
}
