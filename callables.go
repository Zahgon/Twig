// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"regexp"
	"strings"
)

// param is one PHP parameter of a callable.
type param struct {
	name       string
	optional   bool
	variadic   bool
	hasDefault bool
	def        any
}

// emptyArray is the default value [] of a PHP parameter.
type emptyArray struct{}

// callable is a Twig filter, function or test.
type callable struct {
	kind                 string
	name                 string
	hasCallable          bool
	phpName              string
	required             int
	min                  int
	internal             bool
	params               []param
	safe                 []string
	preEscape            string
	oneMandatoryArgument bool
	ignored              bool
}

// The callable registries are filled once by init from callableTable and only read afterwards.
var (
	filters, functions, tests             = map[string]*callable{}, map[string]*callable{}, map[string]*callable{}
	filterNames, functionNames, testNames []string
)

func init() {
	for _, c := range callableTable {
		switch c.kind {
		case "filter":
			filters[c.name] = c
			filterNames = append(filterNames, c.name)
		case "function":
			functions[c.name] = c
			functionNames = append(functionNames, c.name)
		case "test":
			tests[c.name] = c
			testNames = append(testNames, c.name)
		}
	}
}

// ignoredCallable stands for an unknown callable inside a guard tag whose callable does not exist.
func ignoredCallable(kind, name string) *callable {
	return &callable{kind: kind, name: name, hasCallable: true, ignored: true}
}

// The fake callables of the block() and attribute() parser callables.
var (
	blockFunctionDef = &callable{kind: "function", name: "block", hasCallable: true, required: 1,
		params: []param{{name: "name"}, {name: "template", optional: true, hasDefault: true}}}
	attributeFunctionDef = &callable{kind: "function", name: "attribute", hasCallable: true, required: 2,
		params: []param{{name: "variable"}, {name: "attribute"}, {name: "arguments", optional: true, hasDefault: true}}}
)

func normalizeArgName(name string) string { return strings.ToLower(strings.ReplaceAll(name, "_", "")) }

var (
	snake1 = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	snake2 = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

func toSnakeCase(name string) string {
	return strings.ToLower(snake2.ReplaceAllString(snake1.ReplaceAllString(name, "${1}_${2}"), "${1}_${2}"))
}

// variadicArgs carries the arbitrary arguments of a variadic PHP parameter (positional and named).
type variadicArgs struct {
	pos
	args []Arg
}

// bindArgs is CallableArgumentsExtractor::extractArguments. It returns the arguments in parameter order; a
// default value is a ConstExpr; arbitrary arguments of a variadic callable come last as one *variadicArgs.
func bindArgs(c *callable, args []Arg, line int, src *Source) []Expr {
	type extracted struct {
		key   string // normalised name, "" for positional
		pos   int
		value Expr
	}
	var list []extracted
	named := false
	npos := 0
	for _, a := range args {
		if a.Name != "" {
			named = true
			k := normalizeArgName(a.Name)
			replaced := false
			for i := range list {
				if list[i].key == k {
					list[i].value = a.Value
					replaced = true
				}
			}
			if !replaced {
				list = append(list, extracted{key: k, value: a.Value})
			}
		} else if named {
			panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Positional arguments cannot be used after named arguments for %s "%s".`, c.kind, c.name), Line: line, Column: -1, Source: src})
		} else {
			list = append(list, extracted{pos: npos, value: a.Value})
			npos++
		}
	}
	if !named {
		if len(list) < c.required-c.min && !c.ignored {
			idx := len(list)
			name := ""
			if idx < len(c.params) {
				name = c.params[idx].name
			}
			panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Value for argument "%s" is required for %s "%s".`, toSnakeCase(name), c.kind, c.name), Line: line, Column: -1, Source: src})
		}
		out := make([]Expr, len(list))
		for i, e := range list {
			out[i] = e.value
		}
		return out
	}
	if !c.hasCallable {
		panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Named arguments are not supported for %s "%s".`, c.kind, c.name), Line: -1, Column: -1})
	}
	find := func(key string, p int) int {
		for i, e := range list {
			if key != "" && e.key == key || key == "" && e.key == "" && e.pos == p {
				return i
			}
		}
		return -1
	}
	take := func(i int) Expr {
		v := list[i].value
		list = append(list[:i], list[i+1:]...)
		return v
	}
	var out, optional []Expr
	var names, missing []string
	p := 0
	for _, prm := range c.params {
		pname := prm.name
		if c.phpName == "range" {
			switch pname {
			case "start":
				pname = "low"
			case "end":
				pname = "high"
			}
		}
		names = append(names, pname)
		if i := find(normalizeArgName(pname), 0); i >= 0 {
			if find("", p) >= 0 {
				panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Argument "%s" is defined twice for %s "%s".`, pname, c.kind, c.name), Line: line, Column: -1, Source: src})
			}
			if len(missing) > 0 {
				snake := make([]string, len(names))
				for j, n := range names {
					snake[j] = toSnakeCase(n)
				}
				s := ""
				if len(missing) > 1 {
					s = "s"
				}
				panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Argument "%s" could not be assigned for %s "%s(%s)" because it is mapped to an internal PHP function which cannot determine default value for optional argument%s "%s".`,
					pname, c.kind, c.name, strings.Join(snake, ", "), s, strings.Join(missing, `", "`)), Line: line, Column: -1, Source: src})
			}
			out = append(out, optional...)
			out = append(out, take(i))
			optional = nil
		} else if i := find("", p); i >= 0 {
			out = append(out, optional...)
			out = append(out, take(i))
			optional = nil
			p++
		} else if prm.hasDefault {
			optional = append(optional, defaultExpr(prm.def, line))
		} else if prm.optional {
			if len(list) == 0 {
				break
			}
			missing = append(missing, pname)
		} else {
			panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Value for argument "%s" is required for %s "%s".`, toSnakeCase(pname), c.kind, c.name), Line: line, Column: -1, Source: src})
		}
	}
	if len(list) > 0 {
		var keys []string
		for _, e := range list {
			if e.key != "" {
				keys = append(keys, e.key)
			} else {
				keys = append(keys, sprintf("%d", e.pos))
			}
		}
		snake := make([]string, len(names))
		for j, n := range names {
			snake[j] = toSnakeCase(n)
		}
		s := ""
		if len(list) > 1 {
			s = "s"
		}
		panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Unknown argument%s "%s" for %s "%s(%s)".`, s, strings.Join(keys, `", "`), c.kind, c.name, strings.Join(snake, ", ")),
			Line: list[0].value.Line(), Column: -1, Source: src})
	}
	return out
}

func defaultExpr(v any, line int) Expr {
	return &ConstExpr{pos: pos{line}, Value: v}
}
