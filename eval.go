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

// renderer holds the state of one render call.
type renderer struct {
	env    *Environment
	frames []*frame
}

// frame is one running template function (doDisplay, a block or a macro).
type frame struct {
	r      *renderer
	tpl    *Template
	ctx    *php.Array
	blocks map[string]blockRef
	macros *php.Array // $macros
	out    *strings.Builder
	line   int // the template line of the last debug information passed (for error line guessing)
	// locals are the PHP local variables of the generated code (the apply tag's capture), outside the context
	locals map[string]any
}

func isLocalName(name string) bool { return name != "" && name[0] == 0 }

func (r *renderer) push(f *frame) { r.frames = append(r.frames, f) }
func (r *renderer) pop()          { r.frames = r.frames[:len(r.frames)-1] }

// guessLine is Error::guessTemplateInfo: the debug line of the innermost running function of the template named
// in the error.
func (r *renderer) guessLine(name string) int {
	for i := len(r.frames) - 1; i >= 0; i-- {
		if r.frames[i].tpl.src.Name == name {
			return r.frames[i].line
		}
	}
	return 0
}

// handle is Template::handleException, deferred by every template entry point.
func (t *Template) handle(f *frame) {
	rec := recover()
	if rec == nil {
		return
	}
	switch e := rec.(type) {
	case *Error:
		if e.Source == nil {
			e.Source = t.src
		}
		if e.Line == -1 {
			e.Line = f.r.guessLine(e.Source.Name)
		}
		panic(e)
	case *php.Error:
		err := newRuntimeError(sprintf(`An exception has been thrown during the rendering of a template ("%s").`, e.Msg), -1, t.src)
		err.Line = f.r.guessLine(t.src.Name)
		panic(err)
	case *php.RuntimeError:
		err := newRuntimeError(e.Msg, -1, t.src)
		err.Line = f.r.guessLine(t.src.Name)
		panic(err)
	}
	panic(rec)
}

// throwPHP raises a PHP error value from a Go error.
func throwErr(err error) {
	if err == nil {
		return
	}
	panic(err)
}

// RenderTemplate renders a template by name (TemplateWrapper::render) or one of its blocks.
func (env *Environment) Render(name string, context *php.Array, block string, hasBlock bool) (out string, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			switch e := rec.(type) {
			case *Error:
				err = e
			case *php.Error:
				err = e
			case *php.RuntimeError:
				err = newRuntimeError(e.Msg, -1, nil)
			case FatalError:
				err = e
			default:
				panic(rec)
			}
		}
	}()
	t := env.load(name)
	r := &renderer{env: env}
	var b strings.Builder
	if hasBlock {
		// TemplateWrapper::renderBlock merges the globals into the context
		t.yieldBlock(r, block, env.withGlobals(context), nil, true, nil, &b)
	} else {
		t.yield(r, context, nil, &b)
	}
	return b.String(), nil
}

// withGlobals is `$context + $this->env->getGlobals()`.
func (env *Environment) withGlobals(ctx *php.Array) *php.Array {
	out := ctx.Copy()
	env.globals.Each(func(k, v any) bool {
		if !out.Has(k) {
			out.Set(k, v)
		}
		return true
	})
	return out
}

func mergeBlocks(a, b map[string]blockRef) map[string]blockRef {
	out := make(map[string]blockRef, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// FatalError ends the process like a PHP fatal error with display_errors off: no output, exit status 255.
type FatalError struct{}

func (FatalError) Error() string { return "fatal error" }

// maxDepth bounds template nesting; PHP runs out of memory on runaway recursion.
const maxDepth = 2000

// yield is Template::yield (display).
func (t *Template) yield(r *renderer, ctx *php.Array, blocks map[string]blockRef, out *strings.Builder) {
	if len(r.frames) > maxDepth {
		panic(FatalError{})
	}
	ctx = r.env.withGlobals(ctx)
	blocks = mergeBlocks(t.blocks, blocks)
	f := &frame{r: r, tpl: t, ctx: ctx, blocks: blocks, macros: t.macros.Copy(), out: out}
	r.push(f)
	defer r.pop()
	defer t.handle(f)
	t.doDisplay(f)
}

func (t *Template) doDisplay(f *frame) {
	r := f.r
	r.exec(f, t.mod.Body)
	if t.mod.Parent != nil {
		var parent *Template
		if c, ok := t.mod.Parent.(*ConstExpr); ok {
			f.line = t.mod.dbgOf(t.mod.Parent)
			parent = t.loadTemplate(f, c.Value, c.Line())
			t.parent = parent
		} else {
			parent = t.getParent(r, f)
		}
		parent.yield(r, f.ctx, mergeBlocks(t.blocks, f.blocks), f.out)
	}
}

// getParent is Template::getParent.
func (t *Template) getParent(r *renderer, f *frame) *Template {
	if t.parent != nil {
		return t.parent
	}
	if t.mod.Parent == nil {
		return nil
	}
	pf := &frame{r: r, tpl: t, ctx: f.ctx, blocks: f.blocks, macros: t.macros.Copy(), out: f.out}
	r.push(pf)
	defer r.pop()
	defer t.handle(pf)
	pf.line = t.mod.dbgOf(t.mod.Parent)
	name := r.eval(pf, t.mod.Parent)
	if name == nil || !truthy(name) {
		return nil
	}
	if tw, ok := name.(*Template); ok {
		t.parents[tw.src.Name] = tw
		return tw
	}
	key := ""
	if s, ok := name.(string); ok {
		key = s
		if p, ok := t.parents[key]; ok {
			return p
		}
	}
	p := t.loadTemplate(pf, name, -1)
	if key != "" {
		t.parents[key] = p
	}
	return p
}

// loadTemplate is Template::load: a template by name (or the first existing one of a sequence).
func (t *Template) loadTemplate(f *frame, name any, line int) (tpl *Template) {
	defer func() {
		if rec := recover(); rec != nil {
			if e, ok := rec.(*Error); ok {
				if e.Source == nil {
					e.Source = t.src
				}
				if e.Line > 0 {
					panic(e)
				}
				if line == -1 {
					e.Line = f.r.guessLine(e.Source.Name)
				} else {
					e.Line = line
				}
				panic(e)
			}
			panic(rec)
		}
	}()
	switch n := name.(type) {
	case *Template:
		return n
	case *php.Array:
		return t.env.resolveTemplate(n)
	}
	s, err := php.ToString(name)
	throwErr(err)
	if t.mod.Index > 0 && s == t.src.Name {
		return t
	}
	return t.env.load(s)
}

// resolveTemplate is Environment::resolveTemplate for a sequence of names.
func (env *Environment) resolveTemplate(names *php.Array) *Template {
	count := names.Len()
	var list []string
	var found *Template
	names.Each(func(_, v any) bool {
		if tw, ok := v.(*Template); ok {
			found = tw
			return false
		}
		s := toStr(v)
		list = append(list, s)
		if count != 1 && !env.loader.exists(s) {
			return true
		}
		found = env.load(s)
		return false
	})
	if found != nil {
		return found
	}
	if count == 0 {
		panic(newLoaderError(`Unable to find one of the following templates: "".`))
	}
	panic(newLoaderError(sprintf(`Unable to find one of the following templates: "%s".`, strings.Join(list, `", "`))))
}

// yieldBlock is Template::yieldBlock.
func (t *Template) yieldBlock(r *renderer, name string, ctx *php.Array, blocks map[string]blockRef, useBlocks bool, templateContext *Template, out *strings.Builder) {
	var ref *blockRef
	if b, ok := blocks[name]; ok && useBlocks {
		ref = &b
	} else if b, ok := t.blocks[name]; ok {
		ref = &b
		blocks = mergeBlocks(t.blocks, blocks)
	}
	if ref != nil {
		ref.tpl.runBlock(r, ref.block, ctx, blocks, out)
		return
	}
	pf := r.top()
	if pf == nil || pf.tpl != t {
		pf = &frame{r: r, tpl: t, ctx: ctx, blocks: blocks, macros: t.macros.Copy(), out: out}
	}
	if parent := t.getParent(r, &frame{r: r, tpl: t, ctx: ctx, blocks: blocks, macros: t.macros.Copy(), out: out}); parent != nil {
		if templateContext == nil {
			templateContext = t
		}
		parent.yieldBlock(r, name, ctx, mergeBlocks(t.blocks, blocks), false, templateContext, out)
		return
	}
	if b, ok := blocks[name]; ok {
		panic(newRuntimeError(sprintf(`Block "%s" should not call parent() in "%s" as the block does not exist in the parent template "%s".`, name, b.tpl.src.Name, t.src.Name), -1, b.tpl.src))
	}
	src := t.src
	if templateContext != nil {
		src = templateContext.src
	}
	panic(newRuntimeError(sprintf(`Block "%s" on template "%s" does not exist.`, name, t.src.Name), -1, src))
}

func (r *renderer) top() *frame {
	if len(r.frames) == 0 {
		return nil
	}
	return r.frames[len(r.frames)-1]
}

// runBlock calls a block function of this template.
func (t *Template) runBlock(r *renderer, b *Block, ctx *php.Array, blocks map[string]blockRef, out *strings.Builder) {
	f := &frame{r: r, tpl: t, ctx: ctx.Copy(), blocks: blocks, macros: t.macros.Copy(), out: out}
	r.push(f)
	defer r.pop()
	defer t.handle(f)
	f.line = b.Line
	r.exec(f, b.Body)
}

// yieldParentBlock is Template::yieldParentBlock.
func (t *Template) yieldParentBlock(r *renderer, f *frame, name string, out *strings.Builder) {
	if b, ok := t.traits[name]; ok {
		alias := name
		if a, ok := t.traitAliases[name]; ok {
			alias = a
		}
		b.tpl.yieldBlock(r, alias, f.ctx, f.blocks, false, nil, out)
		return
	}
	if parent := t.getParent(r, f); parent != nil {
		parent.yieldBlock(r, name, f.ctx, f.blocks, false, nil, out)
		return
	}
	panic(newRuntimeError(sprintf(`The template has no parent and no traits defining the "%s" block.`, name), -1, t.src))
}

// hasBlock is Template::hasBlock.
func (t *Template) hasBlock(r *renderer, f *frame, name string, blocks map[string]blockRef) bool {
	if _, ok := blocks[name]; ok {
		return true
	}
	if _, ok := t.blocks[name]; ok {
		return true
	}
	if parent := t.getParent(r, f); parent != nil {
		return parent.hasBlock(r, f, name, nil)
	}
	return false
}
