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

// Closure is an arrow function value (a PHP \Closure capturing the context and the macros).
type Closure struct {
	r      *renderer
	tpl    *Template
	params []string
	body   Expr
	ctx    *php.Array
	macros *php.Array
	blocks map[string]blockRef
}

// ClassName names the PHP class.
func (c *Closure) ClassName() string { return "Closure" }

// Call runs the arrow function with positional arguments.
func (c *Closure) Call(args ...any) any {
	ctx := c.ctx.Copy()
	for i, p := range c.params {
		if i < len(args) {
			ctx.Set(p, args[i])
		} else {
			panic(php.Throw("ArgumentCountError", "Too few arguments to function {closure}(), %d passed and exactly %d expected", len(args), len(c.params)))
		}
	}
	f := &frame{r: c.r, tpl: c.tpl, ctx: ctx, macros: c.macros, blocks: c.blocks, out: &strings.Builder{}}
	if top := c.r.top(); top != nil {
		f.line = top.line
	}
	// the closure is code of the template: errors inside it are located from its own debug lines
	c.r.push(f)
	defer c.r.pop()
	defer c.tpl.handle(f)
	return c.r.eval(f, c.body)
}

func (r *renderer) evalArgsList(f *frame, args []Expr) []any {
	out := make([]any, 0, len(args))
	for _, a := range args {
		if va, ok := a.(*variadicArgs); ok {
			for _, x := range va.args {
				out = append(out, r.eval(f, x.Value))
			}
			continue
		}
		if u, ok := a.(*UnaryExpr); ok && u.Op == "..." {
			v := r.eval(f, u.X)
			if arr, ok := v.(*php.Array); ok {
				out = append(out, arr.Values()...)
				continue
			}
			panic(php.Throw("Error", "Only arrays and Traversables can be unpacked"))
		}
		out = append(out, r.eval(f, a))
	}
	return out
}

// eval evaluates an expression.
func (r *renderer) eval(f *frame, e Expr) any {
	switch n := e.(type) {
	case *ConstExpr:
		switch n.Value.(type) {
		case definedTrue:
			return true
		case emptyArray:
			return php.NewArray()
		}
		return n.Value
	case *NameExpr:
		return r.evalName(f, n)
	case *ArrayExpr:
		if n.definedTest {
			return true
		}
		return r.evalArray(f, n)
	case *UnaryExpr:
		return r.evalUnary(f, n)
	case *BinaryExpr:
		return r.evalBinary(f, n)
	case *TernaryExpr:
		if truthy(r.eval(f, n.Cond)) {
			return r.eval(f, n.Then)
		}
		return r.eval(f, n.Else)
	case *GetAttrExpr:
		return r.evalGetAttr(f, n)
	case *FilterExpr:
		return r.evalFilter(f, n)
	case *FunctionExpr:
		return r.evalFunction(f, n)
	case *TestExpr:
		return r.evalTest(f, n)
	case *MacroRefExpr:
		return r.evalMacroRef(f, n)
	case *ArrowExpr:
		f.line = n.line
		return &Closure{r: r, tpl: f.tpl, params: n.Params, body: n.Body, ctx: f.ctx.Copy(), macros: f.macros, blocks: f.blocks}
	case *AssignExpr:
		return r.evalAssign(f, n)
	case *BlockRefExpr:
		return r.evalBlockRef(f, n)
	case *ParentExpr:
		f.line = r.callLine(f, n)
		var b strings.Builder
		f.tpl.yieldParentBlock(r, f, n.Block, &b)
		return b.String()
	case *spreadArgs:
		return r.eval(f, n.Expr)
	case *EmptyExpr:
		panic(newSyntaxError("Empty array elements are only allowed in destructuring assignments.", n.line, f.tpl.src))
	case *ListExpr:
		panic(newSyntaxError("A list of variables must be followed by an arrow.", n.line, f.tpl.src))
	}
	panic(sprintf("unknown expression %T", e))
}

func (r *renderer) evalName(f *frame, n *NameExpr) any {
	if isLocalName(n.Name) {
		return f.locals[n.Name]
	}
	f.line = n.line
	if n.definedTest {
		switch n.Name {
		case "_self", "_context", "_charset":
			return true
		}
		if n.alwaysDefined {
			return true
		}
		return f.ctx.Has(n.Name)
	}
	switch n.Name {
	case "_self":
		return f.tpl.src.Name
	case "_context":
		return f.ctx.Copy()
	case "_charset":
		return "UTF-8"
	}
	v, ok := f.ctx.Get(n.Name)
	if ok || n.alwaysDefined || n.ignoreStrictCheck || !r.env.StrictVariables {
		return v
	}
	panic(newRuntimeError(sprintf(`Variable "%s" does not exist.`, n.Name), n.line, f.tpl.src))
}

func (r *renderer) evalArray(f *frame, n *ArrayExpr) any {
	out := php.NewArray()
	isSeq := true
	for i, el := range n.Elems {
		if _, ok := el.Value.(*EmptyExpr); ok {
			panic(newSyntaxError("Empty array elements are only allowed in destructuring assignments.", el.Value.Line(), f.tpl.src))
		}
		_ = i
		if u, ok := el.Value.(*UnaryExpr); ok && u.Op == "..." {
			v := r.eval(f, u.X)
			arr, ok := v.(*php.Array)
			if !ok {
				panic(php.Throw("Error", "Only arrays and Traversables can be unpacked"))
			}
			arr.Each(func(k, v any) bool {
				if _, isInt := k.(int64); isInt {
					out.Append(v)
				} else {
					out.Set(k, v)
				}
				return true
			})
			continue
		}
		var key any
		hasKey := false
		if el.Key != nil {
			if c, ok := el.Key.(*ConstExpr); ok {
				key = c.Value
				if ki, ok := key.(int64); !ok || ki != int64(i) {
					isSeq = false
				}
			} else {
				isSeq = false
				kv := r.eval(f, el.Key)
				s, err := php.ToString(kv)
				throwErr(err)
				key = s
			}
			hasKey = true
		} else {
			isSeq = false
		}
		if hasKey && !isSeq {
			v := r.eval(f, el.Value)
			setArrayKey(out, key, v)
		} else {
			out.Append(r.eval(f, el.Value))
		}
	}
	return out
}

// setArrayKey is $a[$key] = $v for a literal key (floats truncate, null is "", bools are ints).
func setArrayKey(a *php.Array, key, v any) {
	switch key.(type) {
	case *php.Array:
		panic(php.Throw("TypeError", "Illegal offset type"))
	}
	a.Set(key, v)
}

func (r *renderer) evalUnary(f *frame, n *UnaryExpr) any {
	switch n.Op {
	case "not":
		return !truthy(r.eval(f, n.X))
	case "-":
		v, err := php.Neg(r.eval(f, n.X))
		throwErr(err)
		return v
	case "+":
		v, err := php.Pos(r.eval(f, n.X))
		throwErr(err)
		return v
	case "...":
		return r.eval(f, n.X)
	}
	panic("unknown unary " + n.Op)
}

func (r *renderer) evalBinary(f *frame, n *BinaryExpr) any {
	switch n.Op {
	case "or":
		return truthy(r.eval(f, n.L)) || truthy(r.eval(f, n.R))
	case "and":
		return truthy(r.eval(f, n.L)) && truthy(r.eval(f, n.R))
	case "xor":
		a := truthy(r.eval(f, n.L))
		b := truthy(r.eval(f, n.R))
		return a != b
	case "??":
		test := r.definedTest(f, n.testL)
		if _, isBlock := n.testL.(*BlockRefExpr); !isBlock && test {
			test = r.eval(f, n.testL) != nil
		}
		if test {
			return r.eval(f, n.L)
		}
		return r.eval(f, n.R)
	case "?:":
		if truthyTrueTest(r.eval(f, n.testL)) {
			return r.eval(f, n.L)
		}
		return r.eval(f, n.R)
	}
	var a, b any
	if n.Op == "matches" {
		b = r.eval(f, n.R)
		a = r.eval(f, n.L)
	} else {
		a = r.eval(f, n.L)
		b = r.eval(f, n.R)
	}
	f.line = r.callLine(f, n)
	var v any
	var err error
	switch n.Op {
	case "b-or":
		v, err = php.BitOr(a, b)
	case "b-xor":
		v, err = php.BitXor(a, b)
	case "b-and":
		v, err = php.BitAnd(a, b)
	case "==":
		v = php.Equal(a, b)
	case "!=":
		v = php.NotEqual(a, b)
	case "<=>":
		v = php.Spaceship(a, b)
	case "<":
		v = php.Less(a, b)
	case ">":
		v = php.Greater(a, b)
	case ">=":
		v = php.GreaterEq(a, b)
	case "<=":
		v = php.LessEq(a, b)
	case "===":
		v = php.Identical(a, b)
	case "!==":
		v = !php.Identical(a, b)
	case "in":
		v = inFilter(a, b)
	case "not in":
		v = !inFilter(a, b)
	case "matches":
		v = r.matches(b, a)
	case "starts with":
		as, ok1 := a.(string)
		bs, ok2 := b.(string)
		v = ok1 && ok2 && strings.HasPrefix(as, bs)
	case "ends with":
		as, ok1 := a.(string)
		bs, ok2 := b.(string)
		v = ok1 && ok2 && strings.HasSuffix(as, bs)
	case "has some":
		v = r.arraySome(a, b, true)
	case "has every":
		v = r.arraySome(a, b, false)
	case "..":
		v, err = phpRange(a, b, int64(1))
	case "+":
		v, err = php.Add(a, b)
	case "-":
		v, err = php.Sub(a, b)
	case "~":
		v, err = php.Concat(a, b)
	case "*":
		v, err = php.Mul(a, b)
	case "/":
		v, err = php.Div(a, b)
	case "//":
		v, err = php.FloorDiv(a, b)
	case "%":
		v, err = php.Mod(a, b)
	case "**":
		v, err = php.Pow(a, b)
	default:
		panic("unknown binary " + n.Op)
	}
	throwErr(err)
	return v
}

// truthyTrueTest is TrueTest: a Markup is tested by its string.
func truthyTrueTest(v any) bool {
	if m, ok := v.(php.Markup); ok {
		return m.S != ""
	}
	return truthy(v)
}

// definedTest evaluates the "defined" test of a copy of an expression (DefinedTest on a clone).
func (r *renderer) definedTest(f *frame, e Expr) bool {
	switch n := e.(type) {
	case *NameExpr:
		c := *n
		c.definedTest = true
		return truthy(r.evalName(f, &c))
	case *GetAttrExpr:
		c := cloneGetAttrDefined(n)
		return truthy(r.evalGetAttr(f, c))
	case *MacroRefExpr:
		c := *n
		c.definedTest = true
		return truthy(r.evalMacroRef(f, &c))
	case *BlockRefExpr:
		c := *n
		c.definedTest = true
		return truthy(r.evalBlockRef(f, &c))
	case *ConstExpr, *ArrayExpr:
		return true
	case *FunctionExpr:
		return truthy(r.eval(f, n))
	}
	panic(newSyntaxError(`The "defined" test only works with simple variables.`, e.Line(), f.tpl.src))
}

func cloneGetAttrDefined(n *GetAttrExpr) *GetAttrExpr {
	c := *n
	c.definedTest = true
	c.optimizable = false
	c.ignoreStrictCheck = true
	if inner, ok := c.Node.(*GetAttrExpr); ok {
		c.Node = cloneGetAttrIgnore(inner)
	}
	return &c
}

func cloneGetAttrIgnore(n *GetAttrExpr) *GetAttrExpr {
	c := *n
	c.optimizable = false
	c.ignoreStrictCheck = true
	if inner, ok := c.Node.(*GetAttrExpr); ok {
		c.Node = cloneGetAttrIgnore(inner)
	}
	return &c
}

// evalAlwaysDefined evaluates the left operand of ?? and ?: (a variable there is read without the strict check).
func (r *renderer) evalAlwaysDefined(f *frame, e Expr) any {
	if n, ok := e.(*NameExpr); ok {
		c := *n
		c.alwaysDefined = true
		return r.evalName(f, &c)
	}
	return r.eval(f, e)
}

func (r *renderer) evalAssign(f *frame, n *AssignExpr) any {
	v := r.eval(f, n.Value)
	switch t := n.Target.(type) {
	case *NameExpr:
		f.ctx.Set(t.Name, v)
	case *ArrayExpr:
		f.line = n.line
		if t.isSequence() {
			arr, _ := v.(*php.Array)
			for i, el := range t.Elems {
				nm, ok := el.Value.(*NameExpr)
				if !ok {
					continue
				}
				var x any
				if arr != nil {
					x, _ = arr.Get(int64(i))
				}
				f.ctx.Set(nm.Name, x)
			}
		} else {
			type mapping struct {
				variable string
				property any
			}
			var ms []mapping
			for _, el := range t.Elems {
				nm := el.Value.(*NameExpr)
				var prop any = nm.Name
				if c, ok := el.Key.(*ConstExpr); ok {
					prop = c.Value
				}
				ms = append(ms, mapping{nm.Name, prop})
			}
			vals := make([]any, len(ms))
			for i, m := range ms {
				vals[i] = r.getAttribute(f, v, m.property, nil, anyCall, false, false, n.Value.Line())
			}
			for i, m := range ms {
				f.ctx.Set(m.variable, vals[i])
			}
		}
	}
	return v
}

func (r *renderer) evalBlockRef(f *frame, n *BlockRefExpr) any {
	name := toStr(r.eval(f, n.Name))
	tpl := f.tpl
	blocks := f.blocks
	if n.Template != nil {
		tn := r.eval(f, n.Template)
		tpl = f.tpl.loadTemplate(f, tn, n.line)
		blocks = nil
	}
	if n.definedTest {
		return tpl.hasBlock(r, f, name, blocks)
	}
	f.line = r.callLine(f, n)
	var b strings.Builder
	tpl.yieldBlock(r, name, f.ctx, blocks, true, nil, &b)
	return b.String()
}

func (r *renderer) evalMacroRef(f *frame, n *MacroRefExpr) any {
	var ns *MacroNamespace
	if n.Var == "_self" {
		ns = f.tpl.macroNamespace()
	} else {
		v, ok := f.macros.Get(n.Var)
		if !ok {
			panic(newRuntimeError(sprintf(`Macros imported in the body of template "%s" are not available because the body was not rendered; move the "import" or "from" tag inside the block or the macro that uses it.`, f.tpl.src.Name), n.line, f.tpl.src))
		}
		ns = v.(*MacroNamespace)
	}
	name := n.Name
	if n.NameExpr != nil {
		s, err := php.ToString(r.eval(f, n.NameExpr))
		throwErr(err)
		name = s
	}
	if n.definedTest {
		return ns.has(r, f, name)
	}
	// arguments: positional ones are a list, named ones keep their names
	args := php.NewArray()
	for _, a := range n.Args {
		if u, ok := a.Value.(*UnaryExpr); ok && u.Op == "..." && a.Name == "" {
			v := r.eval(f, u.X)
			if arr, ok := v.(*php.Array); ok {
				arr.Each(func(k, v any) bool {
					if _, isInt := k.(int64); isInt {
						args.Append(v)
					} else {
						args.Set(k, v)
					}
					return true
				})
			}
			continue
		}
		v := r.eval(f, a.Value)
		if a.Name == "" {
			args.Append(v)
		} else {
			args.Set(a.Name, v)
		}
	}
	f.line = r.callLine(f, n)
	return r.callMacro(f, ns, name, args, n.line)
}

func (r *renderer) callMacro(f *frame, ns *MacroNamespace, name string, args *php.Array, line int) any {
	var rm *resolvedMacro
	if mac := ns.findDeclared(name); mac != nil {
		rm = &resolvedMacro{mac, ns.tpl}
	} else if rm = ns.resolve(r, f, name); rm == nil {
		if strings.HasPrefix(name, "macro_") {
			rm = ns.resolve(r, f, name[len("macro_"):])
		}
		if rm == nil {
			panic(newRuntimeError(sprintf(`Macro "%s" is not defined in template "%s".`, name, ns.tpl.src.Name), line, f.tpl.src))
		}
	}
	m := rm.m
	// TwigMacro::callLegacy argument checks
	if !args.IsList() {
		positional := 0
		sawNamed, misordered := false, false
		duplicate := ""
		index := map[string]int{}
		for i, p := range m.Params {
			index[stripReserved(p)] = i
		}
		args.Each(func(k, _ any) bool {
			if _, isInt := k.(int64); isInt {
				misordered = misordered || sawNamed
				positional++
			} else {
				sawNamed = true
				if i, ok := index[k.(string)]; ok && duplicate == "" && i < positional {
					duplicate = k.(string)
				}
			}
			return true
		})
		if misordered {
			panic(newRuntimeError(sprintf(`Positional arguments cannot be used after named arguments for macro "%s".`, m.Name), line, f.tpl.src))
		}
		if duplicate != "" {
			panic(newRuntimeError(sprintf(`Argument "%s" is defined twice for macro "%s".`, duplicate, m.Name), line, f.tpl.src))
		}
	}
	return rm.tpl.runMacro(r, m, args)
}

// runMacro calls a macro function: arguments bound to parameters, the rest in the variadic bucket.
func (t *Template) runMacro(r *renderer, m *Macro, args *php.Array) any {
	f := &frame{r: r, tpl: t, ctx: php.NewArray(), blocks: map[string]blockRef{}, macros: t.macros.Copy(), out: &strings.Builder{}}
	r.push(f)
	defer r.pop()
	defer t.handle(f)
	f.line = m.Line
	values := make([]any, len(m.Params))
	set := make([]bool, len(m.Params))
	bucket := php.NewArray()
	pos := 0
	args.Each(func(k, v any) bool {
		if _, isInt := k.(int64); isInt {
			if pos < len(m.Params) {
				values[pos] = v
				set[pos] = true
			} else {
				bucket.Append(v)
			}
			pos++
			return true
		}
		name := k.(string)
		for i, p := range m.Params {
			if stripReserved(p) == name {
				values[i] = v
				set[i] = true
				return true
			}
		}
		bucket.Set(name, v)
		return true
	})
	for i, p := range m.Params {
		if !set[i] {
			values[i] = r.eval(f, m.Defaults[i])
		}
		f.ctx.Set(stripReserved(p), values[i])
	}
	bucketName := "varargs"
	if m.Variadic != "" {
		bucketName = m.Variadic
	}
	f.ctx.Set(bucketName, bucket)
	f.ctx = r.env.withGlobals(f.ctx)
	return r.capture(f, m.Body)
}

// inFilter is CoreExtension::inFilter.
func inFilter(value, compare any) bool {
	if m, ok := value.(php.Markup); ok {
		value = m.S
	}
	if m, ok := compare.(php.Markup); ok {
		compare = m.S
	}
	if s, ok := compare.(string); ok {
		switch v := value.(type) {
		case string:
			return v == "" || strings.Contains(s, v)
		case int64, float64:
			return strings.Contains(s, toStr(v))
		}
		return false
	}
	arr, ok := compare.(*php.Array)
	if !ok {
		return false
	}
	found := false
	switch value.(type) {
	case php.Object:
		arr.Each(func(_, item any) bool {
			if item == value {
				found = true
				return false
			}
			return true
		})
		return found
	}
	arr.Each(func(_, item any) bool {
		if twigCompare(value, item) == 0 {
			found = true
			return false
		}
		return true
	})
	return found
}

const trimCompareChars = " \t\n\r\v\f"

// twigCompare is CoreExtension::compare: PHP 8 comparison with Twig's handling of numeric strings.
func twigCompare(a, b any) int {
	switch x := a.(type) {
	case int64:
		if s, ok := b.(string); ok {
			t := strings.Trim(s, trimCompareChars)
			if !php.IsNumeric(t) {
				return php.Compare(toStr(x), s)
			}
			if php.Equal(php.ToInt(t), t) {
				return php.Compare(x, php.ToInt(t))
			}
			return php.Compare(float64(x), php.ToFloat(t))
		}
	case string:
		switch y := b.(type) {
		case int64:
			t := strings.Trim(x, trimCompareChars)
			if !php.IsNumeric(t) {
				return php.Compare(x, toStr(y))
			}
			if php.Equal(php.ToInt(t), t) {
				return php.Compare(php.ToInt(t), y)
			}
			return php.Compare(php.ToFloat(t), float64(y))
		case float64:
			if y != y {
				return 1
			}
			t := strings.Trim(x, trimCompareChars)
			if !php.IsNumeric(t) {
				return php.Compare(x, toStr(y))
			}
			return php.Compare(php.ToFloat(t), y)
		}
	case float64:
		if s, ok := b.(string); ok {
			if x != x {
				return 1
			}
			t := strings.Trim(s, trimCompareChars)
			if !php.IsNumeric(t) {
				return php.Compare(toStr(x), s)
			}
			return php.Compare(x, php.ToFloat(t))
		}
	}
	return php.Compare(a, b)
}

func (r *renderer) callArrow(arrow any, args ...any) any {
	switch c := arrow.(type) {
	case *Closure:
		return c.Call(args...)
	case string:
		panic(php.Throw("Error", "Call to undefined function %s()", c))
	case *php.Array:
		panic(php.Throw("Error", "Array callback must have exactly two elements"))
	}
	panic(php.Throw("Error", "Value of type %s is not callable", php.TypeName(arrow)))
}

// checkCallback is the callback validation of array_filter and uasort, done before any element is visited.
func checkCallback(fn, orNull string, arrow any) {
	switch c := arrow.(type) {
	case *Closure:
		return
	case string:
		panic(php.Throw("TypeError", `%s(): Argument #2 ($callback) must be a valid callback%s, function "%s" not found or invalid function name`, fn, orNull, c))
	case *php.Array:
		panic(php.Throw("TypeError", "%s(): Argument #2 ($callback) must be a valid callback%s, array callback must have exactly two members", fn, orNull))
	}
	panic(php.Throw("TypeError", "%s(): Argument #2 ($callback) must be a valid callback%s, no array or string given", fn, orNull))
}

func (r *renderer) arraySome(arr, arrow any, some bool) bool {
	a, ok := arr.(*php.Array)
	if !ok {
		what := "has every"
		if some {
			what = "has some"
		}
		panic(php.Runtime(`The "%s" test expects a sequence or a mapping, got "%s".`, what, php.TypeName(arr)))
	}
	result := !some
	a.Each(func(k, v any) bool {
		t := truthy(r.callArrow(arrow, v, k))
		if some && t {
			result = true
			return false
		}
		if !some && !t {
			result = false
			return false
		}
		return true
	})
	return result
}
