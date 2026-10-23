// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"os"
	"strings"

	"github.com/twigphp/twig/internal/php"
)

func (r *renderer) evalFunction(f *frame, n *FunctionExpr) any {
	if n.def != nil && n.def.ignored {
		return ""
	}
	args := r.evalArgsList(f, n.bound)
	f.line = r.callLine(f, n)
	switch n.Name {
	case "max", "min":
		internalArity(n.Name, len(args), 1, -1)
		if n.Name == "max" {
			return must(php.Max(args))
		}
		return must(php.Min(args))
	case "range":
		internalArity("range", len(args), 2, 3)
		return must(phpRange(args[0], args[1], argOr(args, 2, int64(1))))
	case "cycle":
		values := argOr(args, 0, nil)
		a, ok := values.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "cycle" function expects an array or "ArrayAccess" as first argument.`))
		}
		if a.Len() == 0 {
			panic(php.Runtime(`The "cycle" function expects a non-empty sequence.`))
		}
		idx, err := php.Mod(argOr(args, 1, nil), int64(a.Len()))
		throwErr(err)
		v, _ := a.Get(idx)
		return v
	case "date":
		return r.convertDate(argOr(args, 0, nil), argOr(args, 1, nil))
	case "include", "include_only":
		return r.includeFunction(f, n.Name, args)
	case "source":
		name := strOf(argOr(args, 0, nil))
		ignore := truthy(argOr(args, 1, false))
		file, lerr := r.env.loader.findTemplate(name)
		if lerr != nil {
			if ignore {
				return ""
			}
			panic(lerr)
		}
		data, err := os.ReadFile(file)
		if err != nil {
			return ""
		}
		return string(data)
	case "random", "constant", "enum", "enum_cases":
		// Randomness, PHP constants and PHP enums are outside this port's scope; the names stay registered so
		// that parsing, argument checks and "did you mean" suggestions match Twig.
		panic(php.Throw("LogicException", `The "%s" function is not supported by the Go port of Twig.`, n.Name))
	}
	panic("unknown function " + n.Name)
}

func (r *renderer) includeFunction(f *frame, name string, args []any) any {
	tmpl := argOr(args, 0, nil)
	variables := argOr(args, 1, php.NewArray())
	withContext := true
	ignoreIdx := 3
	if name == "include" {
		withContext = truthy(argOr(args, 2, true))
	} else {
		withContext = false
		ignoreIdx = 2
	}
	ignoreMissing := truthy(argOr(args, ignoreIdx, false))
	vars, ok := variables.(*php.Array)
	if withContext {
		if !ok {
			panic(php.Throw("TypeError", "array_merge(): Argument #2 must be of type array, %s given", php.TypeName(variables)))
		}
		merged := f.ctx.Copy()
		vars.Each(func(k, v any) bool {
			if _, isInt := k.(int64); isInt {
				merged.Append(v)
			} else {
				merged.Set(k, v)
			}
			return true
		})
		vars = merged
	} else if !ok {
		panic(php.Throw("TypeError", `Twig\TemplateWrapper::render(): Argument #1 ($context) must be of type array, %s given`, php.TypeName(variables)))
	}
	var tpl *Template
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if e, isErr := rec.(*Error); isErr && e.Kind == LoaderErrorKind && ignoreMissing {
					tpl = nil
					return
				}
				panic(rec)
			}
		}()
		switch t := tmpl.(type) {
		case *php.Array:
			tpl = r.env.resolveTemplate(t)
		default:
			tpl = r.env.load(strOf(t))
		}
	}()
	if tpl == nil {
		return ""
	}
	var b strings.Builder
	tpl.yield(r, vars, nil, &b)
	if b.Len() == 0 {
		return ""
	}
	return php.Markup{S: b.String()}
}

func phpRange(low, high, step any) (any, error) {
	return php.Range(low, high, step)
}

func (r *renderer) evalTest(f *frame, n *TestExpr) any {
	if n.def != nil && n.def.ignored {
		return ""
	}
	switch n.Name {
	case "defined":
		return r.eval(f, n.Node)
	}
	value := r.eval(f, n.Node)
	args := r.evalArgsList(f, n.bound)
	f.line = r.callLine(f, n)
	switch n.Name {
	case "even", "odd":
		m, err := php.Mod(value, int64(2))
		throwErr(err)
		if n.Name == "even" {
			return php.Equal(m, int64(0))
		}
		return !php.Equal(m, int64(0))
	case "same as":
		return php.Identical(value, argOr(args, 0, nil))
	case "none", "null":
		return value == nil
	case "divisible by":
		m, err := php.Mod(value, argOr(args, 0, nil))
		throwErr(err)
		return php.Equal(int64(0), m)
	case "empty":
		return testEmpty(value)
	case "iterable":
		internalArity("is_iterable", 1+len(args), 1, 1)
		_, ok := value.(*php.Array)
		return ok
	case "sequence":
		a, ok := value.(*php.Array)
		return ok && a.IsList()
	case "mapping":
		if a, ok := value.(*php.Array); ok {
			return !a.IsList()
		}
		switch value.(type) {
		case php.Object, php.Markup:
			return true
		}
		return false
	case "true":
		return truthyTrueTestValue(value)
	case "constant":
		// PHP constants are outside this port's scope (see the constant function).
		panic(php.Throw("LogicException", `The "constant" test is not supported by the Go port of Twig.`))
	}
	panic("unknown test " + n.Name)
}

// truthyTrueTestValue is TrueTest's value: the Markup converted to a string when it is truthy.
func truthyTrueTestValue(v any) any {
	if m, ok := v.(php.Markup); ok {
		return m.S
	}
	return v
}
