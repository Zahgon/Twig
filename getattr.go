// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"strings"

	"github.com/twigphp/twig/internal/php"
)

func (r *renderer) evalGetAttr(f *frame, n *GetAttrExpr) any {
	// optimised array access: ($v = node) && is_array($v) ? $v[key] ?? null : null
	if n.optimizable && (!r.env.StrictVariables || n.ignoreStrictCheck) && !n.definedTest && n.Type == arrayCall {
		v := r.eval(f, n.Node)
		key := r.eval(f, n.Attr)
		arr, ok := v.(*php.Array)
		if !truthy(v) || !ok {
			return nil
		}
		if _, bad := key.(*php.Array); bad {
			panic(php.Throw("TypeError", "Cannot access offset of type array in isset or empty"))
		}
		if m, ok := key.(php.Markup); ok {
			key = m.S
		}
		x, _ := arr.Get(key)
		return x
	}
	ignore := n.ignoreStrictCheck
	// null-safe chains: a?.b returns null when a is null
	obj := r.evalAttrBase(f, n.Node, ignore)
	if n.NullSafe && obj == nil {
		return nil
	}
	attr := r.eval(f, n.Attr)
	var args []any
	for _, a := range n.Args {
		if sa, ok := a.Value.(*spreadArgs); ok {
			v := r.eval(f, sa.Expr)
			if arr, ok := v.(*php.Array); ok {
				args = append(args, arr.Values()...)
			}
			continue
		}
		args = append(args, r.eval(f, a.Value))
	}
	f.line = r.callLine(f, n)
	return r.getAttribute(f, obj, attr, args, n.Type, n.definedTest, ignore, n.Node.Line())
}

// evalAttrBase evaluates the object of an attribute access; with ignore_strict_check set, a missing variable there
// is null instead of an error.
func (r *renderer) evalAttrBase(f *frame, e Expr, ignore bool) any {
	if ignore {
		if n, ok := e.(*NameExpr); ok {
			c := *n
			c.ignoreStrictCheck = true
			return r.evalName(f, &c)
		}
	}
	return r.eval(f, e)
}

// getAttribute is CoreExtension::getAttribute for the values a JSON context and Twig produce.
func (r *renderer) getAttribute(f *frame, object, item any, args []any, typ string, isDefinedTest, ignoreStrictCheck bool, lineno int) any {
	src := f.tpl.src
	strict := r.env.StrictVariables
	if typ != methodCall {
		arrayItem := item
		switch x := item.(type) {
		case bool:
			arrayItem = php.ToInt(x)
		case float64:
			arrayItem = php.FloatToInt(x)
		}
		if arr, ok := object.(*php.Array); ok {
			key, err := php.ToString(arrayItem)
			if err != nil {
				panic(err)
			}
			if _, isArr := arrayItem.(*php.Array); isArr {
				key = "Array"
			}
			if arr.Has(key) {
				if isDefinedTest {
					return true
				}
				v, _ := arr.Get(key)
				return v
			}
			arrayItem = key
		}
		_, isObj := object.(php.Object)
		if _, ok := object.(php.Markup); ok {
			isObj = true
		}
		if typ == arrayCall || !isObj {
			if isDefinedTest {
				return false
			}
			if ignoreStrictCheck || !strict {
				return nil
			}
			var msg string
			switch o := object.(type) {
			case *php.Array:
				if o.Len() == 0 {
					msg = sprintf(`Key "%s" does not exist as the sequence/mapping is empty.`, toStr(arrayItem))
				} else {
					keys := make([]string, 0, o.Len())
					for _, k := range o.Keys() {
						keys = append(keys, toStr(k))
					}
					msg = sprintf(`Key "%s" for sequence/mapping with keys "%s" does not exist.`, toStr(arrayItem), strings.Join(keys, ", "))
				}
			default:
				if isObj {
					msg = sprintf(`Impossible to access a key "%s" on an object of class "%s" that does not implement ArrayAccess interface.`, attrString(item), php.TypeName(object))
				} else if typ == arrayCall {
					if object == nil {
						msg = sprintf(`Impossible to access a key ("%s") on a null variable.`, attrString(item))
					} else {
						msg = sprintf(`Impossible to access a key ("%s") on a %s variable ("%s").`, attrString(item), php.TypeName(object), toStr(object))
					}
				} else if object == nil {
					msg = sprintf(`Impossible to access an attribute ("%s") on a null variable.`, attrString(item))
				} else {
					msg = sprintf(`Impossible to access an attribute ("%s") on a %s variable ("%s").`, attrString(item), php.TypeName(object), toStr(object))
				}
			}
			panic(newRuntimeError(msg, lineno, src))
		}
	}
	itemStr := attrString(item)
	_, isObj := object.(php.Object)
	if _, ok := object.(php.Markup); ok {
		isObj = true
	}
	if !isObj {
		if isDefinedTest {
			return false
		}
		if ignoreStrictCheck || !strict {
			return nil
		}
		var msg string
		switch object.(type) {
		case nil:
			msg = sprintf(`Impossible to invoke a method ("%s") on a null variable.`, itemStr)
		case *php.Array:
			msg = sprintf(`Impossible to invoke a method ("%s") on a sequence/mapping.`, itemStr)
		default:
			msg = sprintf(`Impossible to invoke a method ("%s") on a %s variable ("%s").`, itemStr, php.TypeName(object), toStr(object))
		}
		panic(newRuntimeError(msg, lineno, src))
	}
	if _, ok := object.(*Template); ok {
		panic(newRuntimeError(`Accessing \Twig\Template attributes is forbidden.`, lineno, src))
	}
	if v, ok := r.objectAttribute(f, object, itemStr, args, typ, isDefinedTest); ok {
		return v
	}
	if isDefinedTest {
		return false
	}
	if ignoreStrictCheck || !strict {
		return nil
	}
	panic(newRuntimeError(sprintf(`Neither the property "%[1]s" nor one of the methods "%[1]s()", "get%[1]s()", "is%[1]s()", "has%[1]s()" or "__call()" exist and have public access in class "%[2]s".`, itemStr, php.TypeName(object)), lineno, src))
}

func attrString(item any) string {
	if _, ok := item.(*php.Array); ok {
		return "Array"
	}
	return toStr(item)
}

// objectAttribute resolves methods of the objects templates can hold (Markup, closures, macro namespaces, dates).
func (r *renderer) objectAttribute(f *frame, object any, item string, args []any, typ string, isDefinedTest bool) (any, bool) {
	lower := strings.ToLower(item)
	switch o := object.(type) {
	case php.Markup:
		switch lower {
		case "count":
			if isDefinedTest {
				return true, true
			}
			return php.MbStrlen(o.S), true
		case "__tostring":
			if isDefinedTest {
				return true, true
			}
			return o.S, true
		case "jsonserialize":
			if isDefinedTest {
				return true, true
			}
			return o.S, true
		}
	case *Closure:
		if lower == "__invoke" {
			if isDefinedTest {
				return true, true
			}
			return o.Call(args...), true
		}
	}
	switch o := object.(type) {
	case *php.DateTime:
		return r.dateTimeAttribute(o, item, args, typ, isDefinedTest)
	case *php.DateTimeZone:
		return timeZoneAttribute(o, item, args, isDefinedTest)
	}
	if m, ok := object.(attributeObject); ok {
		return m.Attribute(item, args, typ, isDefinedTest)
	}
	return nil, false
}

// attributeObject is implemented by runtime objects with their own properties and methods.
type attributeObject interface {
	Attribute(item string, args []any, typ string, isDefinedTest bool) (any, bool)
}

// dateTimeMethods are the DateTime methods templates can call, by lower-case name, with their PHP name and arity.
var dateTimeMethods = map[string]struct {
	name     string
	min, max int
}{
	"format": {"format", 1, 1}, "gettimestamp": {"getTimestamp", 0, 0}, "getoffset": {"getOffset", 0, 0},
	"gettimezone": {"getTimezone", 0, 0}, "modify": {"modify", 1, 1}, "settimezone": {"setTimezone", 1, 1},
}

// dateTimeAttribute is CoreExtension::getAttribute on a DateTime: the date, timezone and timezone_type
// properties of its array cast, then the methods item, getItem, isItem and hasItem.
func (r *renderer) dateTimeAttribute(d *php.DateTime, item string, args []any, typ string, isDefinedTest bool) (any, bool) {
	if typ != methodCall {
		switch item {
		case "date", "timezone", "timezone_type":
			if isDefinedTest {
				return true, true
			}
			v, _ := d.JSONSerialize().(*php.Array).Get(item)
			return v, true
		}
	}
	lower := strings.ToLower(item)
	for _, cand := range []string{lower, "get" + lower, "is" + lower, "has" + lower} {
		m, ok := dateTimeMethods[cand]
		if !ok {
			continue
		}
		if isDefinedTest {
			return true, true
		}
		if len(args) < m.min || len(args) > m.max {
			s := "s"
			if m.min == 1 {
				s = ""
			}
			panic(php.Throw("ArgumentCountError", "DateTime::%s() expects exactly %d argument%s, %d given", m.name, m.min, s, len(args)))
		}
		switch m.name {
		case "format":
			return d.Format(mustStr(php.StringArg("DateTime::format", 1, "format", args[0]))), true
		case "getTimestamp":
			return d.Timestamp(), true
		case "getOffset":
			return d.Offset(), true
		case "getTimezone":
			return d.Timezone(), true
		case "modify":
			throwErr(d.Modify(mustStr(php.StringArg("DateTime::modify", 1, "modifier", args[0]))))
			return d, true
		case "setTimezone":
			z, ok := args[0].(*php.DateTimeZone)
			if !ok {
				panic(php.Throw("TypeError", "DateTime::setTimezone(): Argument #1 ($timezone) must be of type DateTimeZone, %s given", php.TypeName(args[0])))
			}
			d.SetTimezone(z)
			return d, true
		}
	}
	return nil, false
}

// timeZoneAttribute resolves DateTimeZone::getName().
func timeZoneAttribute(z *php.DateTimeZone, item string, args []any, isDefinedTest bool) (any, bool) {
	switch strings.ToLower(item) {
	case "name", "getname":
		if isDefinedTest {
			return true, true
		}
		if len(args) > 0 {
			panic(php.Throw("ArgumentCountError", "DateTimeZone::getName() expects exactly 0 arguments, %d given", len(args)))
		}
		return z.Name(), true
	}
	return nil, false
}
