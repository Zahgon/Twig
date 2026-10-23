// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"math"
	"strings"

	"github.com/twigphp/twig/internal/php"
)

// argOr returns argument i or a default.
func argOr(args []any, i int, def any) any {
	if i < len(args) {
		return args[i]
	}
	return def
}

// internalArity checks the argument count of a PHP internal function (ArgumentCountError).
func internalArity(fn string, given, min, max int) {
	if given >= min && (max < 0 || given <= max) {
		return
	}
	if min == max {
		s := "s"
		if min == 1 {
			s = ""
		}
		panic(php.Throw("ArgumentCountError", "%s() expects exactly %d argument%s, %d given", fn, min, s, given))
	}
	if given < min {
		s := "s"
		if min == 1 {
			s = ""
		}
		panic(php.Throw("ArgumentCountError", "%s() expects at least %d argument%s, %d given", fn, min, s, given))
	}
	s := "s"
	if max == 1 {
		s = ""
	}
	panic(php.Throw("ArgumentCountError", "%s() expects at most %d argument%s, %d given", fn, max, s, given))
}

func must(v any, err error) any {
	throwErr(err)
	return v
}

func mustStr(s string, err error) string {
	throwErr(err)
	return s
}

// strArg is a string parameter of a Twig static method (weak mode: scalars convert, arrays are TypeErrors).
func strOf(v any) string {
	s, err := php.ToString(v)
	throwErr(err)
	return s
}

func (r *renderer) evalFilter(f *frame, n *FilterExpr) any {
	switch n.Name {
	case "raw":
		return r.eval(f, n.Node)
	case "default":
		// DefaultFilter: a variable or attribute is tested with "defined" first
		switch n.Node.(type) {
		case *NameExpr, *GetAttrExpr:
			if !r.definedTest(f, n.Node) {
				if len(n.Args) > 0 {
					return r.eval(f, n.Args[0].Value)
				}
				return ""
			}
		}
	}
	value := r.eval(f, n.Node)
	args := r.evalArgsList(f, n.bound)
	f.line = r.callLine(f, n)
	return r.applyFilter(f, n.Name, value, args)
}

func (r *renderer) callLine(f *frame, e Expr) int {
	if l, ok := f.tpl.mod.dbg[e]; ok {
		return l
	}
	return f.line
}

func (r *renderer) applyFilter(f *frame, name string, value any, args []any) any {
	switch name {
	case "date":
		return r.formatDate(value, argOr(args, 0, nil), argOr(args, 1, nil))
	case "date_modify":
		return r.modifyDate(value, argOr(args, 0, nil))
	case "format":
		format := ""
		if value != nil {
			format = mustStr(php.StringArg("sprintf", 1, "format", value))
		}
		return mustStr(php.Sprintf(format, args...))
	case "replace":
		from := argOr(args, 0, nil)
		fa, ok := from.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "replace" filter expects a sequence or a mapping, got "%s".`, php.TypeName(from)))
		}
		s := ""
		if value != nil {
			s = mustStr(php.StringArg("strtr", 1, "string", value))
		}
		return mustStr(php.Strtr(s, fa))
	case "number_format":
		return r.formatNumber(value, argOr(args, 0, nil), argOr(args, 1, nil), argOr(args, 2, nil))
	case "abs":
		internalArity("abs", 1+len(args), 1, 1)
		return must(php.Abs(value))
	case "round":
		return must(php.Round(toFloatCast(value), argOr(args, 0, int64(0)), strOf(argOr(args, 1, "common"))))
	case "url_encode":
		if a, ok := value.(*php.Array); ok {
			return php.HTTPBuildQuery(a)
		}
		s := ""
		if value != nil {
			s = mustStr(php.StringArg("rawurlencode", 1, "string", value))
		}
		return php.RawURLEncode(s)
	case "json_encode":
		internalArity("json_encode", 1+len(args), 1, 3)
		flags := int64(0)
		depth := int64(512)
		if len(args) > 0 {
			flags = must(php.IntArg("json_encode", 2, "flags", args[0])).(int64)
		}
		if len(args) > 1 {
			depth = must(php.IntArg("json_encode", 3, "depth", args[1])).(int64)
		}
		out, ok, msg := php.JSONEncode(value, flags, int(depth))
		if !ok {
			if flags&4194304 != 0 {
				panic(php.Throw("JsonException", "%s", msg))
			}
			return false
		}
		return out
	case "title", "capitalize", "upper", "lower":
		s := ""
		if value != nil {
			fn := map[string]string{"title": "mb_convert_case", "capitalize": "mb_substr", "upper": "mb_strtoupper", "lower": "mb_strtolower"}[name]
			s = mustStr(php.StringArg(fn, 1, "string", value))
		}
		switch name {
		case "title":
			return php.MbTitle(s)
		case "upper":
			return php.MbStrtoupper(s)
		case "lower":
			return php.MbStrtolower(s)
		}
		return php.MbStrtoupper(php.MbSubstr(s, 0, 1, true)) + php.MbStrtolower(php.MbSubstr(s, 1, 0, false))
	case "striptags":
		s := ""
		if value != nil {
			s = mustStr(php.StringArg("strip_tags", 1, "string", value))
		}
		allowed := mustStr(php.AllowedTags(argOr(args, 0, nil)))
		return php.StripTags(s, allowed)
	case "trim":
		return r.trimFilter(value, argOr(args, 0, nil), argOr(args, 1, "both"))
	case "nl2br":
		s := ""
		if value != nil {
			s = mustStr(php.StringArg("nl2br", 1, "string", value))
		}
		return php.Nl2br(s)
	case "spaceless":
		s := ""
		if value != nil {
			s = mustStr(php.StringArg("preg_replace", 3, "subject", value))
		}
		re, err := php.CompilePCRE(`/>\s+</`)
		throwErr(err)
		out, err := re.ReplaceAll(s, "><")
		throwErr(err)
		return strings.Trim(out, phpTrimChars)
	case "join":
		return r.join(value, argOr(args, 0, ""), argOr(args, 1, nil))
	case "split":
		return r.split(value, argOr(args, 0, nil), argOr(args, 1, nil))
	case "sort":
		return r.sortFilter(value, argOr(args, 0, nil))
	case "merge":
		return r.callMerge(f, append([]any{value}, args...), 0)
	case "batch":
		return r.batch(value, argOr(args, 0, nil), argOr(args, 1, nil), argOr(args, 2, true))
	case "column":
		return r.column(value, argOr(args, 0, nil), argOr(args, 1, nil))
	case "filter":
		a, ok := value.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "filter" filter expects a sequence/mapping or "Traversable", got "%s".`, php.TypeName(value)))
		}
		arrow := argOr(args, 0, nil)
		out := php.NewArray()
		if arrow != nil {
			checkCallback("array_filter", " or null", arrow)
		}
		a.Each(func(k, v any) bool {
			if arrow == nil && truthy(v) || arrow != nil && truthy(r.callArrow(arrow, v, k)) {
				out.Set(k, v)
			}
			return true
		})
		return out
	case "map":
		a, ok := value.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "map" filter expects a sequence or a mapping, got "%s".`, php.TypeName(value)))
		}
		arrow := argOr(args, 0, nil)
		out := php.NewArray()
		a.Each(func(k, v any) bool {
			out.Set(k, r.callArrow(arrow, v, k))
			return true
		})
		return out
	case "reduce":
		a, ok := value.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "reduce" filter expects a sequence or a mapping, got "%s".`, php.TypeName(value)))
		}
		arrow := argOr(args, 0, nil)
		acc := argOr(args, 1, nil)
		a.Each(func(k, v any) bool {
			acc = r.callArrow(arrow, acc, v, k)
			return true
		})
		return acc
	case "find":
		a, ok := value.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "find" filter expects a sequence or a mapping, got "%s".`, php.TypeName(value)))
		}
		arrow := argOr(args, 0, nil)
		var found any
		a.Each(func(k, v any) bool {
			if truthy(r.callArrow(arrow, v, k)) {
				found = v
				return false
			}
			return true
		})
		return found
	case "reverse":
		return r.reverse(value, truthy(argOr(args, 0, false)))
	case "length":
		return lengthOf(value)
	case "slice":
		return r.slice(value, argOr(args, 0, nil), argOr(args, 1, nil), truthy(argOr(args, 2, false)))
	case "first":
		v := r.slice(value, int64(0), int64(1), false)
		if a, ok := v.(*php.Array); ok {
			_, x, ok := a.First()
			if !ok {
				return false
			}
			return x
		}
		return v
	case "last":
		v := r.slice(value, int64(-1), int64(1), false)
		if a, ok := v.(*php.Array); ok {
			_, x, ok := a.First()
			if !ok {
				return false
			}
			return x
		}
		return v
	case "default":
		if testEmpty(value) {
			return argOr(args, 0, "")
		}
		return value
	case "keys":
		a, ok := value.(*php.Array)
		if !ok {
			return php.NewArray()
		}
		return php.NewList(a.Keys()...)
	case "invoke":
		c, ok := value.(*Closure)
		if !ok {
			panic(php.Throw("TypeError", `Twig\Extension\CoreExtension::invoke(): Argument #1 ($arrow) must be of type Closure, %s given`, php.TypeName(value)))
		}
		return c.Call(args...)
	case "escape", "e":
		strategy := "html"
		if len(args) > 0 && args[0] != nil {
			strategy = strOf(args[0])
		}
		autoescape := len(args) > 2 && truthy(args[2])
		return must(php.Escape(value, strategy, autoescape))
	case "convert_encoding", "shuffle":
		// Character set conversion and shuffling (randomness) are outside this port's scope; the names stay
		// registered so that parsing, argument checks and "did you mean" suggestions match Twig.
		panic(php.Throw("LogicException", `The "%s" filter is not supported by the Go port of Twig.`, name))
	}
	if fd := filters[name]; fd != nil && fd.ignored {
		return ""
	}
	panic("unknown filter " + name)
}

// lengthOf is CoreExtension::length.
func lengthOf(v any) any {
	switch x := v.(type) {
	case nil:
		return int64(0)
	case bool, int64, float64, string:
		return php.MbStrlen(toStr(x))
	case *php.Array:
		return int64(x.Len())
	case php.Markup:
		return php.MbStrlen(x.S)
	}
	return int64(1)
}

// testEmpty is CoreExtension::testEmpty.
func testEmpty(v any) bool {
	switch x := v.(type) {
	case php.Markup:
		return x.S == ""
	case nil:
		return true
	case string:
		return x == ""
	case bool:
		return !x
	case *php.Array:
		return x.Len() == 0
	}
	return false
}

func (r *renderer) trimFilter(value, mask, side any) any {
	m := phpTrimChars
	defaultMask := true
	if mask != nil {
		m = strOf(mask)
		defaultMask = m == phpTrimChars
	}
	if value == nil {
		value = ""
	}
	var out string
	switch sd := strOf(side); sd {
	case "both", "left", "right":
		fn := map[string]string{"both": "trim", "left": "ltrim", "right": "rtrim"}[sd]
		s := mustStr(php.StringArg(fn, 1, "string", value))
		out = php.TrimChars(s, m, sd != "right", sd != "left")
	default:
		panic(php.Runtime(`Trimming side must be "left", "right" or "both".`))
	}
	if _, ok := value.(php.Markup); ok && defaultMask {
		return php.Markup{S: out}
	}
	return out
}

func toArrayValues(v any) *php.Array {
	switch x := v.(type) {
	case *php.Array:
		return php.NewList(x.Values()...)
	case php.Markup:
		// (array) of a Twig\Markup object: its private properties content and charset
		return php.NewList(x.S, "UTF-8")
	case php.Object:
		return php.NewArray()
	}
	// (array) $scalar
	if v == nil {
		return php.NewArray()
	}
	return php.NewList(v)
}

func (r *renderer) join(value, glue, and any) any {
	vals := toArrayValues(value)
	if vals.Len() == 0 {
		return ""
	}
	g := mustStr(php.StringArg("implode", 1, "separator", glue))
	if and == nil || php.Identical(and, glue) {
		return mustStr(php.Implode(g, vals))
	}
	if vals.Len() == 1 {
		v, _ := vals.Get(int64(0))
		return v
	}
	list := vals.Values()
	head := mustStr(php.Implode(g, php.NewList(list[:len(list)-1]...)))
	return head + strOf(and) + strOf(list[len(list)-1])
}

func (r *renderer) split(value, delimiter, limit any) any {
	if value == nil {
		value = ""
	}
	if ds, ok := delimiter.(string); !ok || ds != "" {
		s := mustStr(php.StringArg("explode", 2, "string", value))
		d := mustStr(php.StringArg("explode", 1, "separator", delimiter))
		if limit == nil {
			return must(php.Explode(d, s, 0, false))
		}
		l := must(php.IntArg("explode", 3, "limit", limit)).(int64)
		return must(php.Explode(d, s, l, true))
	}
	if limit == nil || php.LessEq(limit, int64(1)) {
		s := mustStr(php.StringArg("preg_match_all", 2, "subject", value))
		chars, ok := php.MbSplitChars(s)
		if !ok {
			panic(php.Runtime(`Unable to split the string passed to "split" into characters: Malformed UTF-8 characters, possibly incorrectly encoded.`))
		}
		vals := make([]any, len(chars))
		for i, c := range chars {
			vals[i] = c
		}
		return php.NewList(vals...)
	}
	s := mustStr(php.StringArg("mb_strlen", 1, "string", value))
	length := php.MbStrlen(s)
	if php.Less(length, limit) {
		return php.NewList(value)
	}
	l := must(php.IntArg("mb_substr", 3, "length", limit)).(int64)
	out := php.NewArray()
	for i := int64(0); i < length; i += l {
		out.Append(php.MbSubstr(s, i, l, true))
	}
	return out
}

func (r *renderer) reverse(value any, preserveKeys bool) any {
	if a, ok := value.(*php.Array); ok {
		return php.ArrayReverse(a, preserveKeys)
	}
	s := strOf(value)
	chars, ok := php.MbSplitChars(s)
	if !ok {
		panic(php.Runtime(`Unable to split the string passed to "reverse" into characters: Malformed UTF-8 characters, possibly incorrectly encoded.`))
	}
	var b strings.Builder
	for i := len(chars) - 1; i >= 0; i-- {
		b.WriteString(chars[i])
	}
	return b.String()
}

// slice is CoreExtension::slice.
func (r *renderer) slice(item, start, length any, preserveKeys bool) any {
	if a, ok := item.(*php.Array); ok {
		st := must(php.IntArg("array_slice", 2, "offset", start)).(int64)
		var l int64
		hasLen := length != nil
		if hasLen {
			l = must(php.IntArg("array_slice", 3, "length", length)).(int64)
		}
		return php.ArraySlice(a, st, l, hasLen, preserveKeys)
	}
	s := strOf(item)
	st := must(php.IntArg("mb_substr", 2, "start", start)).(int64)
	if length == nil {
		return php.MbSubstr(s, st, 0, false)
	}
	l := must(php.IntArg("mb_substr", 3, "length", length)).(int64)
	return php.MbSubstr(s, st, l, true)
}

// callMerge is CoreExtension::merge.
func (r *renderer) callMerge(f *frame, arrays []any, line int) *php.Array {
	list := make([]*php.Array, len(arrays))
	for i, v := range arrays {
		a, ok := v.(*php.Array)
		if !ok {
			panic(php.Runtime(`The "merge" filter expects a sequence or a mapping, got "%s" for argument %d.`, php.TypeName(v), i+1))
		}
		list[i] = a
	}
	return php.ArrayMerge(list...)
}

func (r *renderer) batch(items, size, fill, preserveKeys any) any {
	a, ok := items.(*php.Array)
	if !ok {
		panic(php.Runtime(`The "batch" filter expects a sequence or a mapping, got "%s".`, php.TypeName(items)))
	}
	sz := int64(math.Ceil(must(php.FloatArg("ceil", 1, "num", size)).(float64)))
	result := must(php.ArrayChunk(a, sz, truthy(preserveKeys))).(*php.Array)
	if _, last, ok := result.Last(); ok && fill != nil {
		cur := last.(*php.Array)
		for int64(cur.Len()) < sz {
			cur.Append(fill)
		}
	}
	return result
}

// column is array_column.
func (r *renderer) column(value, name, index any) any {
	a, ok := value.(*php.Array)
	if !ok {
		panic(php.Runtime(`The "column" filter expects a sequence or a mapping, got "%s".`, php.TypeName(value)))
	}
	out := php.NewArray()
	a.Each(func(_, row any) bool {
		ra, ok := row.(*php.Array)
		if !ok {
			return true
		}
		var v any
		if name == nil {
			v = ra
		} else {
			x, ok := ra.Get(name)
			if !ok {
				return true
			}
			v = x
		}
		if index != nil {
			if k, ok := ra.Get(index); ok {
				switch k.(type) {
				case int64, string:
					out.Set(k, v)
					return true
				case bool, float64, nil:
					out.Set(k, v)
					return true
				}
			}
		}
		out.Append(v)
		return true
	})
	return out
}

func (r *renderer) formatNumber(number, decimal, point, sep any) any {
	d := r.env.Decimals
	if decimal != nil {
		d = must(php.IntArg("number_format", 2, "decimals", decimal)).(int64)
	}
	p := r.env.DecimalPoint
	if point != nil {
		p = strOf(point)
	}
	s := r.env.ThousandsSep
	if sep != nil {
		s = strOf(sep)
	}
	num := must(php.FloatArg("number_format", 1, "num", toFloatCast(number))).(float64)
	return php.NumberFormat(num, d, p, s)
}

// toFloatCast is (float) $v; an object casts to 1.
func toFloatCast(v any) any {
	switch v.(type) {
	case php.Markup, php.Object:
		return float64(1)
	}
	if _, ok := v.(*php.Array); ok {
		if v.(*php.Array).Len() == 0 {
			return float64(0)
		}
		return float64(1)
	}
	return php.ToFloat(v)
}
