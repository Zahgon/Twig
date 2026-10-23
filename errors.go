// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"fmt"
	"strings"
)

// Source is a template's code and logical name (Twig\Source).
type Source struct {
	Code string
	Name string
	Path string
}

// Column is Source::getColumn: the 1-based byte column of an offset.
func (s *Source) Column(offset int) int {
	if offset < 0 {
		return -1
	}
	if offset > len(s.Code) {
		offset = len(s.Code)
	}
	before := normalizeNewlines(s.Code[:offset])
	if i := strings.LastIndexByte(before, '\n'); i >= 0 {
		return len(before) - i
	}
	return len(before) + 1
}

// ErrorKind is the Twig exception class.
type ErrorKind int

// The Twig exception classes.
const (
	SyntaxErrorKind ErrorKind = iota
	LoaderErrorKind
	RuntimeErrorKind
)

// Error is a Twig\Error\Error: a raw message completed with the template name and line (and column) when known.
type Error struct {
	Kind    ErrorKind
	Raw     string
	Line    int // -1: unknown and to be guessed; 0: unknown
	Column  int // -1: none
	Source  *Source
	guessed bool
}

func (e *Error) Error() string {
	msg := e.Raw
	punct := ""
	if strings.HasSuffix(msg, ".") || strings.HasSuffix(msg, "?") {
		punct = msg[len(msg)-1:]
		msg = msg[:len(msg)-1]
	}
	if e.Source != nil && e.Source.Name != "" {
		msg += fmt.Sprintf(` in "%s"`, e.Source.Name)
	}
	if e.Line > 0 {
		msg += fmt.Sprintf(" at line %d", e.Line)
		if e.Column >= 0 {
			msg += fmt.Sprintf(" column %d", e.Column)
		}
	}
	return msg + punct
}

// ClassName is the short PHP class name printed by the command line.
func (e *Error) ClassName() string {
	switch e.Kind {
	case SyntaxErrorKind:
		return "SyntaxError"
	case LoaderErrorKind:
		return "LoaderError"
	}
	return "RuntimeError"
}

func newSyntaxError(msg string, line int, src *Source) *Error {
	return &Error{Kind: SyntaxErrorKind, Raw: msg, Line: line, Column: -1, Source: src}
}

func newSyntaxErrorAt(msg string, line int, src *Source, column int) *Error {
	return &Error{Kind: SyntaxErrorKind, Raw: msg, Line: line, Column: column, Source: src}
}

func newRuntimeError(msg string, line int, src *Source) *Error {
	return &Error{Kind: RuntimeErrorKind, Raw: msg, Line: line, Column: -1, Source: src}
}

func newLoaderError(msg string) *Error {
	return &Error{Kind: LoaderErrorKind, Raw: msg, Line: -1, Column: -1}
}

// addSuggestions is SyntaxError::addSuggestions: names within a third of the length (Levenshtein) or containing
// the name, closest first.
func (e *Error) addSuggestions(name string, items []string) {
	type alt struct {
		item string
		lev  int
	}
	var alts []alt
	for _, item := range items {
		lev := levenshtein(name, item)
		if float64(lev) <= float64(len(name))/3 || strings.Contains(item, name) {
			alts = append(alts, alt{item, lev})
		}
	}
	if len(alts) == 0 {
		return
	}
	// asort is stable in PHP 8
	for i := 1; i < len(alts); i++ {
		for j := i; j > 0 && alts[j].lev < alts[j-1].lev; j-- {
			alts[j], alts[j-1] = alts[j-1], alts[j]
		}
	}
	names := make([]string, len(alts))
	for i, a := range alts {
		names[i] = a.item
	}
	e.Raw += fmt.Sprintf(` Did you mean "%s"?`, strings.Join(names, `", "`))
}

// levenshtein is PHP's byte-wise levenshtein() with unit costs.
func levenshtein(a, b string) int {
	if a == "" {
		return len(b)
	}
	if b == "" {
		return len(a)
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 0; i < len(a); i++ {
		cur[0] = i + 1
		for j := 0; j < len(b); j++ {
			cost := 1
			if a[i] == b[j] {
				cost = 0
			}
			cur[j+1] = min(prev[j]+cost, prev[j+1]+1, cur[j]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
