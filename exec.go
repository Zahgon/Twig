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

// exec runs a statement node.
func (r *renderer) exec(f *frame, n Node) {
	if n == nil {
		return
	}
	switch n := n.(type) {
	case *Nodes:
		for _, c := range n.List {
			r.exec(f, c)
		}
	case *TextNode:
		f.line = n.line
		f.out.WriteString(n.Data)
	case *PrintNode:
		f.line = n.line
		v := r.eval(f, n.X)
		f.out.WriteString(r.printString(f, v))
	case *IfNode:
		f.line = n.line
		for i, test := range n.Tests {
			if truthy(r.eval(f, test)) {
				r.exec(f, n.Bodies[i])
				return
			}
		}
		if n.Else != nil {
			r.exec(f, n.Else)
		}
	case *ForNode:
		r.execFor(f, n)
	case *SetNode:
		r.execSet(f, n)
	case *BlockRefNode:
		f.line = n.line
		f.tpl.yieldBlock(r, n.Name, f.ctx, f.blocks, true, nil, f.out)
	case *IncludeNode:
		r.execInclude(f, n)
	case *EmbedNode:
		r.execEmbed(f, n)
	case *WithNode:
		r.execWith(f, n)
	case *ImportNode:
		r.execImport(f, n)
	case *DoNode:
		f.line = n.line
		r.eval(f, n.X)
	case *DeprecatedNode:
		f.line = n.line
		r.eval(f, n.X)
		if n.Package != nil {
			r.eval(f, n.Package)
		}
		if n.Version != nil {
			r.eval(f, n.Version)
		}
	case *AutoEscapeNode:
		r.exec(f, n.Body)
	case *EmptyNode, *configNode, *macroDeclNode:
	default:
		panic(sprintf("unknown node %T", n))
	}
}

// printString converts a printed value to text (echo).
func (r *renderer) printString(f *frame, v any) string {
	s, err := php.ToString(v)
	throwErr(err)
	return s
}

func (r *renderer) execFor(f *frame, n *ForNode) {
	f.line = n.line
	parent := f.ctx.Copy()
	f.ctx.Set("_parent", parent)
	seqVal := r.eval(f, n.Seq)
	seq, ok := seqVal.(*php.Array)
	if !ok {
		seq = php.NewArray()
	}
	f.ctx.Set("_seq", seq)
	if n.Else != nil {
		f.ctx.Set("_iterated", false)
	}
	if n.withLoop {
		loop := php.NewMap("parent", parent, "index0", int64(0), "index", int64(1), "first", true)
		length := int64(seq.Len())
		loop.Set("revindex0", length-1)
		loop.Set("revindex", length)
		loop.Set("length", length)
		loop.Set("last", length == 1)
		f.ctx.Set("loop", loop)
	}
	keys, values := seq.Keys(), seq.Values()
	for i := range keys {
		// foreach ($seq as $context[key] => $context[value]) assigns the value first
		f.ctx.Set(n.ValueTarget, values[i])
		f.ctx.Set(n.KeyTarget, keys[i])
		r.exec(f, n.Body)
		if n.Else != nil {
			f.ctx.Set("_iterated", true)
		}
		// the loop variable may have been replaced by the body
		lv, _ := f.ctx.Get("loop")
		if la, ok := lv.(*php.Array); ok && n.withLoop {
			la = la.Copy()
			la.Set("index0", incr(la, "index0"))
			la.Set("index", incr(la, "index"))
			la.Set("first", false)
			ri0, ok0 := la.Get("revindex0")
			ri, ok1 := la.Get("revindex")
			if ok0 && ok1 && ri0 != nil && ri != nil {
				d0, _ := php.Sub(ri0, int64(1))
				d1, _ := php.Sub(ri, int64(1))
				la.Set("revindex0", d0)
				la.Set("revindex", d1)
				la.Set("last", php.Identical(d0, int64(0)))
			}
			f.ctx.Set("loop", la)
		}
	}
	if n.Else != nil {
		it, _ := f.ctx.Get("_iterated")
		if !truthy(it) {
			f.line = n.elseLine
			r.exec(f, n.Else)
		}
	}
	pv, _ := f.ctx.Get("_parent")
	p, ok := pv.(*php.Array)
	if !ok {
		p = php.NewArray()
	}
	for _, k := range []string{"_seq", n.KeyTarget, n.ValueTarget, "_parent"} {
		f.ctx.Delete(k)
	}
	if n.Else != nil {
		f.ctx.Delete("_iterated")
	}
	if n.withLoop {
		f.ctx.Delete("loop")
	}
	// $context = array_intersect_key($context, $_parent) + $_parent
	out := php.NewArray()
	f.ctx.Each(func(k, v any) bool {
		if p.Has(k) {
			out.Set(k, v)
		}
		return true
	})
	p.Each(func(k, v any) bool {
		if !out.Has(k) {
			out.Set(k, v)
		}
		return true
	})
	f.ctx = out
}

func incr(a *php.Array, k string) any {
	v, _ := a.Get(k)
	out, err := php.Add(v, int64(1))
	if err != nil {
		return v
	}
	return out
}

func (r *renderer) execSet(f *frame, n *SetNode) {
	f.line = n.line
	if n.capture {
		v := r.capture(f, n.Capture)
		if isLocalName(n.Names[0]) {
			if f.locals == nil {
				f.locals = map[string]any{}
			}
			f.locals[n.Names[0]] = v
			return
		}
		f.ctx.Set(n.Names[0], v)
		return
	}
	if len(n.Names) > 1 {
		vals := make([]any, len(n.Values))
		for i, e := range n.Values {
			vals[i] = r.eval(f, e)
		}
		for i, name := range n.Names {
			f.ctx.Set(name, vals[i])
		}
		return
	}
	f.ctx.Set(n.Names[0], r.eval(f, n.Values[0]))
}

// capture renders a body into a string; a non-empty result is Markup (SetNode with capture, CaptureNode).
func (r *renderer) capture(f *frame, body Node) any {
	// SetNode's constructor folds constant captures; the result is the same.
	saved := f.out
	var b strings.Builder
	f.out = &b
	func() {
		defer func() { f.out = saved }()
		r.exec(f, body)
	}()
	if b.Len() == 0 {
		return ""
	}
	return php.Markup{S: b.String()}
}

func (r *renderer) includeContext(f *frame, vars Expr, only bool, line int) *php.Array {
	if vars == nil {
		if only {
			return php.NewArray()
		}
		return f.ctx
	}
	v := r.eval(f, vars)
	if !only {
		f.line = line
		return r.callMerge(f, []any{f.ctx, v}, line)
	}
	if a, ok := v.(*php.Array); ok {
		return a
	}
	// CoreExtension::toArray returns non-arrays unchanged; yield() then fails on the array type hint.
	panic(php.Throw("TypeError", `Twig\Template::yield(): Argument #1 ($context) must be of type array, %s given, called in %s on line %d`, php.TypeName(v), "", line))
}

func (r *renderer) execInclude(f *frame, n *IncludeNode) {
	f.line = n.line
	name := r.eval(f, n.Template)
	var tpl *Template
	if n.IgnoreMissing {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					if e, ok := rec.(*Error); ok && e.Kind == LoaderErrorKind {
						tpl = nil
						return
					}
					panic(rec)
				}
			}()
			tpl = f.tpl.loadTemplate(f, name, n.line)
		}()
		if tpl == nil {
			return
		}
	} else {
		tpl = f.tpl.loadTemplate(f, name, n.line)
	}
	ctx := r.includeContext(f, n.Variables, n.Only, n.line)
	tpl.yield(r, ctx, nil, f.out)
}

func (r *renderer) execEmbed(f *frame, n *EmbedNode) {
	f.line = n.line
	tpl := f.tpl.embeddedTemplate(n.Module)
	if n.IgnoreMissing {
		// EmbedNode loads the parent inside the try block when the template may be missing
		missing := func() (missing bool) {
			defer func() {
				if rec := recover(); rec != nil {
					if e, isErr := rec.(*Error); isErr && e.Kind == LoaderErrorKind {
						missing = true
						return
					}
					panic(rec)
				}
			}()
			tpl.getParent(r, f)
			return false
		}()
		if missing {
			return
		}
	}
	ctx := r.includeContext(f, n.Variables, n.Only, n.line)
	tpl.yield(r, ctx, nil, f.out)
}

// embeddedTemplate is the template class of an embed tag (loaded by name and index).
func (t *Template) embeddedTemplate(mod *Module) *Template {
	root := t
	for root.owner != nil {
		root = root.owner
	}
	if et, ok := root.embedded[mod.Index]; ok {
		return et
	}
	et := t.env.newTemplate(mod, root)
	root.embedded[mod.Index] = et
	et.construct()
	return et
}

func (r *renderer) execWith(f *frame, n *WithNode) {
	f.line = n.line
	saved := f.ctx
	if n.Variables != nil {
		v := r.eval(f, n.Variables)
		vars, ok := v.(*php.Array)
		if !ok {
			panic(newRuntimeError(`Variables passed to the "with" tag must be a mapping.`, n.Variables.Line(), f.tpl.src))
		}
		base := f.ctx
		if n.Only {
			base = php.NewArray()
		}
		ctx := vars.Copy()
		base.Each(func(k, v any) bool {
			if !ctx.Has(k) {
				ctx.Set(k, v)
			}
			return true
		})
		f.ctx = r.env.withGlobals(ctx)
	} else {
		f.ctx = f.ctx.Copy()
	}
	r.exec(f, n.Body)
	f.ctx = saved
}

func (r *renderer) execImport(f *frame, n *ImportNode) {
	f.line = n.line
	var ns *MacroNamespace
	if nm, ok := n.Template.(*NameExpr); ok && nm.Name == "_self" {
		ns = f.tpl.macroNamespace()
	} else {
		name := r.eval(f, n.Template)
		ns = f.tpl.loadTemplate(f, name, n.line).macroNamespace()
	}
	f.macros.Set(n.Var, ns)
	if n.Global {
		f.tpl.macros.Set(n.Var, ns)
	}
}
