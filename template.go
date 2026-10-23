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

// blockRef is a block bound to the template that defines it ([$template, 'block_name']).
type blockRef struct {
	tpl   *Template
	block *Block
}

// Template is a loaded template (the generated Twig\Template class and its instance).
type Template struct {
	env          *Environment
	mod          *Module
	src          *Source
	blocks       map[string]blockRef
	blockOrder   []string
	traits       map[string]blockRef
	traitAliases map[string]string
	parent       *Template // a constant parent once loaded
	parents      map[string]*Template
	macros       *php.Array // $this->macros
	namespace    *MacroNamespace
	embedded     map[int]*Template
	owner        *Template // the template an embedded template belongs to
}

func (env *Environment) newTemplate(mod *Module, owner *Template) *Template {
	t := &Template{env: env, mod: mod, src: mod.Source, blocks: map[string]blockRef{}, traits: map[string]blockRef{},
		traitAliases: map[string]string{}, parents: map[string]*Template{}, macros: php.NewArray(),
		embedded: map[int]*Template{}, owner: owner}
	return t
}

// Name is the template's logical name.
func (t *Template) Name() string { return t.src.Name }

// construct is the generated constructor: it loads the traits (use tags) and builds the block table.
func (t *Template) construct() {
	r := &renderer{env: t.env}
	f := &frame{r: r, tpl: t, ctx: php.NewArray()}
	r.push(f)
	defer r.pop()
	func() {
		defer t.handle(f)
		var traitBlocks map[string]blockRef
		var traitOrder []string
		for _, tr := range t.mod.Traits {
			f.line = t.mod.dbgOf(tr.Template)
			name := r.eval(f, tr.Template)
			ut := t.loadTemplate(f, name, tr.Template.Line())
			if !ut.isTraitable() {
				panic(newRuntimeError(sprintf(`Template "%s" cannot be used as a trait.`, toStr(name)), tr.Template.Line(), t.src))
			}
			blocks := map[string]blockRef{}
			var order []string
			for _, n := range ut.blockOrder {
				blocks[n] = ut.blocks[n]
				order = append(order, n)
			}
			for _, tg := range tr.Targets {
				b, ok := blocks[tg.Name]
				if !ok {
					panic(newRuntimeError(sprintf(`Block "%s" is not defined in trait "%s".`, tg.Name, toStr(name)), tr.Template.Line(), t.src))
				}
				// $blocks[alias] = $blocks[name]; unset($blocks[name]): aliasing a block to its own name drops it
				if _, exists := blocks[tg.Alias]; !exists {
					order = append(order, tg.Alias)
				}
				blocks[tg.Alias] = b
				delete(blocks, tg.Name)
				order = removeString(order, tg.Name)
				if tg.Alias != tg.Name {
					order = append(removeString(order, tg.Alias), tg.Alias)
				}
				t.traitAliases[tg.Alias] = tg.Name
			}
			if traitBlocks == nil {
				traitBlocks = map[string]blockRef{}
			}
			for _, n := range order {
				if _, exists := traitBlocks[n]; !exists {
					traitOrder = append(traitOrder, n)
				}
				traitBlocks[n] = blocks[n]
			}
		}
		for _, n := range traitOrder {
			t.traits[n] = traitBlocks[n]
			t.blocks[n] = traitBlocks[n]
			t.blockOrder = append(t.blockOrder, n)
		}
	}()
	for _, n := range t.mod.order {
		if _, exists := t.blocks[n]; !exists {
			t.blockOrder = append(t.blockOrder, n)
		}
		t.blocks[n] = blockRef{t, t.mod.Blocks[n]}
	}
}

func removeString(list []string, s string) []string {
	out := list[:0:0]
	for _, x := range list {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// isTraitable is the generated isTraitable(): no parent, no macros and a body without nodes that have children.
func (t *Template) isTraitable() bool {
	if t.mod.Parent != nil || len(t.mod.Macros) > 0 {
		return false
	}
	var nodes []Node
	if ns, ok := t.mod.Body.(*Nodes); ok {
		nodes = ns.List
	} else if childCount(t.mod.Body) == 0 {
		nodes = []Node{t.mod.Body}
	} else {
		for _, c := range childNodes(t.mod.Body) {
			if childCount(c) > 0 {
				return false
			}
		}
		return true
	}
	for _, n := range nodes {
		if childCount(n) > 0 {
			return false
		}
	}
	return true
}

func childCount(n any) int { return len(childNodes(n)) }

// childNodes approximates Node::getIterator for the traitable check.
func childNodes(n any) []any {
	switch n := n.(type) {
	case *Nodes:
		out := make([]any, len(n.List))
		for i, c := range n.List {
			out[i] = c
		}
		return out
	case *TextNode, *BlockRefNode, *configNode, *EmptyNode, *macroDeclNode, *ConstExpr, *NameExpr:
		return nil
	case *PrintNode:
		return []any{n.X}
	case *ArrayExpr:
		var out []any
		for _, e := range n.Elems {
			if e.Key != nil {
				out = append(out, e.Key)
			}
			out = append(out, e.Value)
		}
		return out
	}
	return []any{nil}
}

// MacroNamespace is Twig\MacroNamespace: the macros of a template, looked up through its parents.
type MacroNamespace struct {
	tpl *Template
}

// ClassName names the PHP class in messages.
func (m *MacroNamespace) ClassName() string { return `Twig\MacroNamespace` }

func (t *Template) macroNamespace() *MacroNamespace {
	if t.namespace == nil {
		t.namespace = &MacroNamespace{t}
	}
	return t.namespace
}

func (m *MacroNamespace) findDeclared(name string) *Macro {
	if mac, ok := m.tpl.mod.Macros[name]; ok {
		return mac
	}
	for _, n := range m.tpl.mod.macroSeq {
		if strings.EqualFold(n, name) {
			return m.tpl.mod.Macros[n]
		}
	}
	return nil
}

func (m *MacroNamespace) parentNS(r *renderer, f *frame) *MacroNamespace {
	p := m.tpl.getParent(r, f)
	if p == nil {
		return nil
	}
	return p.macroNamespace()
}

type resolvedMacro struct {
	m   *Macro
	tpl *Template
}

func (m *MacroNamespace) resolve(r *renderer, f *frame, name string) *resolvedMacro {
	for ns := m; ns != nil; ns = ns.parentNS(r, f) {
		if mac := ns.findDeclared(name); mac != nil {
			return &resolvedMacro{mac, ns.tpl}
		}
	}
	return nil
}

func (m *MacroNamespace) has(r *renderer, f *frame, name string) bool {
	if m.resolve(r, f, name) != nil {
		return true
	}
	return strings.HasPrefix(name, "macro_") && m.resolve(r, f, name[len("macro_"):]) != nil
}
