// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import "path"

// ---- correctness (CorrectnessNodeVisitor) ------------------------------------------------------------------------

type correctness struct {
	hasParent  bool
	hasExtends bool
	blockDepth int
	macroDepth int
	capture    int
	tagStack   []Node
	tagNames   []string
}

func (p *Parser) checkCorrectness(mod *Module) {
	c := &correctness{hasParent: mod.Parent != nil}
	var roots []Node
	if ns, ok := mod.Body.(*Nodes); ok {
		roots = ns.List
	} else {
		roots = []Node{mod.Body}
	}
	for _, n := range roots {
		if c.hasParent && !isEmptyOutputNode(n) {
			panic(newSyntaxError("A template that extends another one cannot include content outside Twig blocks. Did you forget to put the content inside a {% block %} tag?", n.Line(), mod.Source))
		}
	}
	c.walk(mod, mod.Body, "")
	for _, name := range mod.order {
		c.blockDepth++
		c.walk(mod, mod.Blocks[name].Body, "")
		c.blockDepth--
	}
	for _, name := range mod.macroSeq {
		c.macroDepth++
		c.walk(mod, mod.Macros[name].Body, "")
		c.macroDepth--
	}
}

func isEmptyOutputNode(n Node) bool {
	switch n := n.(type) {
	case *SetNode:
		return true
	case *TextNode:
		return isBlankText(n)
	case *PrintNode, *IncludeNode:
		return false
	case *Nodes:
		// the apply tag's nodes contain a capture and a print
		for _, c := range n.List {
			if !isEmptyOutputNode(c) {
				return false
			}
		}
		return true
	case *IfNode:
		for _, b := range n.Bodies {
			if !isEmptyOutputNode(b) {
				return false
			}
		}
		return n.Else == nil || isEmptyOutputNode(n.Else)
	case *ForNode:
		return isEmptyOutputNode(n.Body) && (n.Else == nil || isEmptyOutputNode(n.Else))
	case *WithNode:
		return isEmptyOutputNode(n.Body)
	case *AutoEscapeNode:
		return isEmptyOutputNode(n.Body)
	}
	return true
}

func (c *correctness) walk(mod *Module, n Node, tag string) {
	if n == nil {
		return
	}
	push := func(tag string) {
		c.tagStack = append(c.tagStack, n)
		c.tagNames = append(c.tagNames, tag)
	}
	pop := func() {
		c.tagStack = c.tagStack[:len(c.tagStack)-1]
		c.tagNames = c.tagNames[:len(c.tagNames)-1]
	}
	switch n := n.(type) {
	case *Nodes:
		if t := nodesTag(n); t != "" {
			push(t)
			defer pop()
		}
		for _, x := range n.List {
			c.walk(mod, x, "")
		}
	case *configNode:
		if n.tag == "extends" {
			if c.blockDepth > 0 {
				panic(newSyntaxError(`Cannot use "extend" in a block.`, n.line, mod.Source))
			}
			if c.macroDepth > 0 {
				panic(newSyntaxError(`Cannot use "extend" in a macro.`, n.line, mod.Source))
			}
			if c.hasExtends {
				panic(newSyntaxError("Multiple extends tags are forbidden.", n.line, mod.Source))
			}
			c.hasExtends = true
		}
	case *BlockRefNode:
		if c.hasParent && c.blockDepth == 0 && c.macroDepth == 0 && c.capture == 0 && len(c.tagStack) > 0 {
			t := c.tagStack[len(c.tagStack)-1]
			panic(newSyntaxError(sprintf(`A "block" tag cannot be under a "%s" tag (line %d).`, c.tagNames[len(c.tagNames)-1], t.Line()), n.line, mod.Source))
		}
	case *IfNode:
		push("if")
		for _, b := range n.Bodies {
			c.walk(mod, b, "")
		}
		c.walk(mod, n.Else, "")
		pop()
	case *ForNode:
		push("for")
		c.walk(mod, n.Body, "")
		c.walk(mod, n.Else, "")
		pop()
	case *SetNode:
		push("set")
		c.capture++
		c.walk(mod, n.Capture, "")
		c.capture--
		pop()
	case *WithNode:
		push("with")
		c.walk(mod, n.Body, "")
		pop()
	case *AutoEscapeNode:
		push("autoescape")
		c.walk(mod, n.Body, "")
		pop()
	}
}

// nodesTag recognises the node list an apply tag returns.
func nodesTag(n *Nodes) string {
	if len(n.List) == 2 {
		if s, ok := n.List[0].(*SetNode); ok && s.capture && len(s.Names) == 1 && len(s.Names[0]) > 0 && s.Names[0][0] == 0 {
			return "apply"
		}
	}
	return ""
}

// ---- escaping (EscaperNodeVisitor and SafeAnalysisNodeVisitor) ---------------------------------------------------

type escaper struct {
	defaultStrategy any // string or false
	statusStack     []any
	blocks          map[string]any
	safeVars        map[string]bool
}

// guessStrategy is FileExtensionEscapingStrategy::guess.
func guessStrategy(name string) any {
	if name != "" && (name[len(name)-1] == '/' || name[len(name)-1] == '\\') {
		return "html"
	}
	if len(name) >= 5 && name[len(name)-5:] == ".twig" {
		name = name[:len(name)-5]
	}
	ext := path.Ext(path.Base(name))
	if ext != "" {
		ext = ext[1:]
	}
	switch ext {
	case "js", "json":
		return "js"
	case "css":
		return "css"
	case "txt":
		return false
	}
	return "html"
}

func (env *Environment) defaultStrategy(name string) any {
	switch env.Autoescape {
	case "":
		return false
	case "name":
		return guessStrategy(name)
	}
	return env.Autoescape
}

func (p *Parser) escape(mod *Module) {
	e := &escaper{defaultStrategy: false, blocks: map[string]any{}, safeVars: map[string]bool{}}
	if s := p.env.defaultStrategy(mod.Source.Name); s != false {
		e.defaultStrategy = s
	}
	mod.strategy = e.defaultStrategy
	e.node(mod.Body)
	for _, name := range mod.order {
		b := mod.Blocks[name]
		if s, ok := e.blocks[name]; ok {
			e.statusStack = append(e.statusStack, s)
		} else {
			e.statusStack = append(e.statusStack, e.need())
		}
		e.node(b.Body)
		e.statusStack = e.statusStack[:len(e.statusStack)-1]
	}
	for _, name := range mod.macroSeq {
		m := mod.Macros[name]
		for _, d := range m.Defaults {
			e.expr(d)
		}
		e.node(m.Body)
	}
}

func (e *escaper) need() any {
	if len(e.statusStack) > 0 {
		return e.statusStack[len(e.statusStack)-1]
	}
	return e.defaultStrategy
}

func (e *escaper) node(n Node) {
	switch n := n.(type) {
	case *Nodes:
		for _, c := range n.List {
			e.node(c)
		}
	case *PrintNode:
		n.X = e.expr(n.X)
		if t := e.need(); t != false && t != nil {
			typ := toStr(t)
			n.X = e.escapeOperand(n.X, typ)
		}
	case *IfNode:
		for i := range n.Tests {
			n.Tests[i] = e.expr(n.Tests[i])
			e.node(n.Bodies[i])
		}
		e.node(n.Else)
	case *ForNode:
		n.Seq = e.expr(n.Seq)
		e.node(n.Body)
		e.node(n.Else)
	case *SetNode:
		for i := range n.Values {
			n.Values[i] = e.expr(n.Values[i])
		}
		e.node(n.Capture)
	case *BlockRefNode:
		e.blocks[n.Name] = e.need()
	case *IncludeNode:
		n.Template = e.expr(n.Template)
		if n.Variables != nil {
			n.Variables = e.expr(n.Variables)
		}
	case *EmbedNode:
		if n.Variables != nil {
			n.Variables = e.expr(n.Variables)
		}
	case *WithNode:
		if n.Variables != nil {
			n.Variables = e.expr(n.Variables)
		}
		e.node(n.Body)
	case *ImportNode:
		n.Template = e.expr(n.Template)
		e.safeVars[n.Var] = true
	case *AutoEscapeNode:
		e.statusStack = append(e.statusStack, n.Strategy)
		e.node(n.Body)
		e.statusStack = e.statusStack[:len(e.statusStack)-1]
	case *DoNode:
		n.X = e.expr(n.X)
	case *DeprecatedNode:
		n.X = e.expr(n.X)
	}
}

// escapeOperand escapes a printed expression; conditional operators escape each branch.
func (e *escaper) escapeOperand(x Expr, typ string) Expr {
	switch n := x.(type) {
	case *TernaryExpr:
		n.Then = e.escapeOperand(n.Then, typ)
		n.Else = e.escapeOperand(n.Else, typ)
		return n
	case *BinaryExpr:
		if n.Op == "??" || n.Op == "?:" {
			n.L = e.escapeOperand(n.L, typ)
			n.R = e.escapeOperand(n.R, typ)
			return n
		}
	}
	if e.isSafeFor(typ, x) {
		return x
	}
	return escapeFilterFor(x, typ)
}

func escapeFilterFor(x Expr, typ string) Expr {
	line := x.Line()
	args := []Arg{{Value: &ConstExpr{pos: pos{line}, Value: typ}}, {Value: &ConstExpr{pos: pos{line}, Value: nil}}, {Value: &ConstExpr{pos: pos{line}, Value: true}}}
	return &FilterExpr{pos: pos{line}, Node: x, Name: "escape", Args: args, def: filters["escape"], autoEscape: true}
}

func (e *escaper) isSafeFor(typ string, x Expr) bool {
	safe := e.safe(x)
	for _, s := range safe {
		if s == typ || s == "all" {
			return true
		}
		if s == "html_attr" && (typ == "html" || typ == "html_attr_relaxed") {
			return true
		}
		if s == "html_attr_relaxed" && typ == "html" {
			return true
		}
	}
	return false
}

func intersectSafe(a, b []string) []string {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	if contains(a, "all") {
		return b
	}
	if contains(b, "all") {
		return a
	}
	var out []string
	for _, x := range a {
		if contains(b, x) {
			out = append(out, x)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func withAttrSafety(s []string) []string {
	if contains(s, "html_attr") {
		s = append(s, "html", "html_attr_relaxed")
	}
	if contains(s, "html_attr_relaxed") {
		s = append(s, "html")
	}
	return s
}

func (e *escaper) safe(x Expr) []string {
	switch n := x.(type) {
	case *ConstExpr, *BlockRefExpr, *ParentExpr, *MacroRefExpr:
		return []string{"all"}
	case *TernaryExpr:
		return intersectSafe(withAttrSafety(e.safe(n.Then)), withAttrSafety(e.safe(n.Else)))
	case *BinaryExpr:
		if n.Op == "??" || n.Op == "?:" {
			return intersectSafe(withAttrSafety(e.safe(n.L)), withAttrSafety(e.safe(n.R)))
		}
	case *FilterExpr:
		var s []string
		switch n.Name {
		case "escape", "e":
			if len(n.Args) == 0 {
				s = []string{"html"}
			} else if c, ok := n.Args[0].Value.(*ConstExpr); ok {
				s = []string{toStr(c.Value)}
				if c.Value == false {
					s = []string{""}
				}
			}
		default:
			if n.def != nil {
				s = n.def.safe
			}
		}
		return s
	case *FunctionExpr:
		if n.def != nil {
			return n.def.safe
		}
	case *GetAttrExpr:
		if nm, ok := n.Node.(*NameExpr); ok && e.safeVars[nm.Name] {
			return []string{"all"}
		}
	}
	return nil
}

// expr runs the escaper over an expression: filters with pre_escape get their input escaped.
func (e *escaper) expr(x Expr) Expr {
	switch n := x.(type) {
	case nil:
		return nil
	case *FilterExpr:
		n.Node = e.expr(n.Node)
		e.args(n.Args)
		if n.def != nil && n.def.preEscape != "" && !e.isSafeFor(n.def.preEscape, n.Node) {
			n.Node = escapeFilterFor(n.Node, n.def.preEscape)
		}
	case *FunctionExpr:
		e.args(n.Args)
	case *TestExpr:
		n.Node = e.expr(n.Node)
		e.args(n.Args)
	case *UnaryExpr:
		n.X = e.expr(n.X)
	case *BinaryExpr:
		n.L = e.expr(n.L)
		n.R = e.expr(n.R)
	case *TernaryExpr:
		n.Cond = e.expr(n.Cond)
		n.Then = e.expr(n.Then)
		n.Else = e.expr(n.Else)
	case *GetAttrExpr:
		n.Node = e.expr(n.Node)
		n.Attr = e.expr(n.Attr)
		e.args(n.Args)
	case *ArrayExpr:
		for i := range n.Elems {
			if n.Elems[i].Key != nil {
				n.Elems[i].Key = e.expr(n.Elems[i].Key)
			}
			n.Elems[i].Value = e.expr(n.Elems[i].Value)
		}
	case *MacroRefExpr:
		e.args(n.Args)
	case *ArrowExpr:
		n.Body = e.expr(n.Body)
	case *AssignExpr:
		n.Value = e.expr(n.Value)
	case *BlockRefExpr:
		n.Name = e.expr(n.Name)
		if n.Template != nil {
			n.Template = e.expr(n.Template)
		}
	case *spreadArgs:
		n.Expr = e.expr(n.Expr)
	}
	return x
}

func (e *escaper) args(args []Arg) {
	for i := range args {
		args[i].Value = e.expr(args[i].Value)
	}
}

// ---- compilation checks and debug lines --------------------------------------------------------------------------

// compiler walks a module in the order the PHP compiler generates code: it binds callable arguments (raising the
// compile-time argument errors) and records, for each call, the template line of the last debug information before
// it, which is the line PHP reports for an exception raised there.
type compiler struct {
	mod  *Module
	last int
}

func (env *Environment) compileModule(mod *Module) {
	c := &compiler{mod: mod}
	mod.dbg = map[any]int{}
	for _, t := range mod.Traits {
		c.debug(t.Template.Line())
		c.expr(t.Template)
	}
	c.node(mod.Body)
	if mod.Parent != nil {
		c.debug(mod.Parent.Line())
		c.expr(mod.Parent)
		mod.dbg[mod.Parent] = c.last
	}
	for _, name := range mod.order {
		b := mod.Blocks[name]
		c.debug(b.Line)
		c.node(b.Body)
	}
	for _, name := range mod.macroSeq {
		m := mod.Macros[name]
		for _, d := range m.Defaults {
			c.expr(d)
		}
		c.debug(m.Line)
		c.node(m.Body)
	}
	for _, em := range mod.Embedded {
		env.compileModule(em)
	}
}

func (m *Module) dbgOf(e any) int {
	if l, ok := m.dbg[e]; ok {
		return l
	}
	if x, ok := e.(Expr); ok {
		return x.Line()
	}
	return 0
}

func (c *compiler) debug(line int) {
	if line > 0 {
		c.last = line
	}
}

func (c *compiler) node(n Node) {
	switch n := n.(type) {
	case nil:
	case *Nodes:
		for _, x := range n.List {
			c.node(x)
		}
	case *TextNode:
		c.debug(n.line)
	case *PrintNode:
		c.debug(n.line)
		switch x := n.X.(type) {
		case *BlockRefExpr, *ParentExpr:
			// the optimizer turns these prints into output calls, which have their own debug information
			c.debug(x.Line())
		}
		c.expr(n.X)
	case *IfNode:
		c.debug(n.line)
		for i := range n.Tests {
			c.expr(n.Tests[i])
			c.node(n.Bodies[i])
		}
		c.node(n.Else)
	case *ForNode:
		c.debug(n.line)
		c.expr(n.Seq)
		c.node(n.Body)
		if n.Else != nil {
			c.debug(n.elseLine)
			c.node(n.Else)
		}
	case *SetNode:
		c.debug(n.line)
		for _, v := range n.Values {
			c.expr(v)
		}
		c.node(n.Capture)
	case *BlockRefNode:
		c.debug(n.line)
	case *IncludeNode:
		c.debug(n.line)
		c.expr(n.Template)
		c.expr(n.Variables)
	case *EmbedNode:
		c.debug(n.line)
		c.expr(n.Variables)
	case *WithNode:
		c.debug(n.line)
		c.expr(n.Variables)
		c.node(n.Body)
	case *ImportNode:
		c.debug(n.line)
		c.expr(n.Template)
	case *DoNode:
		c.debug(n.line)
		c.expr(n.X)
	case *DeprecatedNode:
		c.debug(n.line)
		c.expr(n.X)
		c.expr(n.Package)
		c.expr(n.Version)
	case *AutoEscapeNode:
		c.node(n.Body)
	}
}

func (c *compiler) args(args []Arg) {
	for _, a := range args {
		c.expr(a.Value)
	}
}

func (c *compiler) expr(x Expr) {
	switch n := x.(type) {
	case nil:
	case *NameExpr:
		c.debug(n.line)
	case *FilterExpr:
		c.mod.dbg[n] = c.last
		c.expr(n.Node)
		if n.Name != "raw" {
			n.bound = bindArgs(n.def, n.Args, n.line, c.mod.Source)
		}
		if n.Name == "default" {
			c.args(n.Args)
		}
		for _, b := range n.bound {
			c.expr(b)
		}
	case *FunctionExpr:
		c.mod.dbg[n] = c.last
		n.bound = bindArgs(n.def, n.Args, n.line, c.mod.Source)
		for _, b := range n.bound {
			c.expr(b)
		}
	case *TestExpr:
		c.mod.dbg[n] = c.last
		c.expr(n.Node)
		if n.def != nil && n.def.hasCallable {
			n.bound = bindArgs(n.def, n.Args, n.line, c.mod.Source)
		} else {
			n.bound = nil
			for _, a := range n.Args {
				if a.Name != "" {
					panic(&Error{Kind: SyntaxErrorKind, Raw: sprintf(`Named arguments are not supported for test "%s".`, n.Name), Line: -1, Column: -1})
				}
				n.bound = append(n.bound, a.Value)
			}
		}
		for _, b := range n.bound {
			c.expr(b)
		}
	case *UnaryExpr:
		c.expr(n.X)
	case *BinaryExpr:
		switch n.Op {
		case "matches":
			// compiled as a call: CoreExtension::matches(right, left)
			c.mod.dbg[n] = c.last
			c.expr(n.R)
			c.expr(n.L)
		case "in", "not in", "..", "has some", "has every":
			c.mod.dbg[n] = c.last
			c.expr(n.L)
			c.expr(n.R)
		default:
			// a PHP operator: an error is reported at the operator, after the left operand
			c.expr(n.L)
			c.mod.dbg[n] = c.last
			c.expr(n.R)
		}
	case *TernaryExpr:
		c.expr(n.Cond)
		c.expr(n.Then)
		c.expr(n.Else)
	case *GetAttrExpr:
		c.mod.dbg[n] = c.last
		c.expr(n.Node)
		c.expr(n.Attr)
		c.args(n.Args)
	case *ArrayExpr:
		for _, el := range n.Elems {
			if _, ok := el.Value.(*EmptyExpr); ok {
				panic(newSyntaxError("Empty array elements are only allowed in destructuring assignments.", el.Value.Line(), c.mod.Source))
			}
		}
		for _, el := range n.Elems {
			c.expr(el.Key)
			c.expr(el.Value)
		}
	case *MacroRefExpr:
		c.mod.dbg[n] = c.last
		c.expr(n.NameExpr)
		c.args(n.Args)
	case *ArrowExpr:
		c.debug(n.line)
		c.expr(n.Body)
	case *AssignExpr:
		if _, ok := n.Target.(*ArrayExpr); ok {
			c.debug(n.line)
		}
		c.expr(n.Value)
	case *BlockRefExpr:
		c.mod.dbg[n] = c.last
		c.expr(n.Name)
		c.expr(n.Template)
	case *ParentExpr:
		c.mod.dbg[n] = c.last
	case *spreadArgs:
		c.expr(n.Expr)
	case *variadicArgs:
		c.args(n.args)
	}
}

// ---- optimizer (OptimizerNodeVisitor's loop analysis) -------------------------------------------------------------

type optimizer struct {
	loops   []*ForNode
	targets []string
}

// optimize decides which for loops need the loop variable, and marks the loop variables always defined.
func optimize(mod *Module) {
	o := &optimizer{}
	o.node(mod.Body)
	for _, name := range mod.order {
		o.node(mod.Blocks[name].Body)
	}
	for _, name := range mod.macroSeq {
		m := mod.Macros[name]
		for _, d := range m.Defaults {
			o.expr(d)
		}
		o.node(m.Body)
	}
}

func (o *optimizer) addLoopToCurrent() { o.loops[0].withLoop = true }

func (o *optimizer) addLoopToAll() {
	for _, l := range o.loops {
		l.withLoop = true
	}
}

func (o *optimizer) isTarget(name string) bool {
	for _, t := range o.targets {
		if t == name {
			return true
		}
	}
	return false
}

func (o *optimizer) node(n Node) {
	switch n := n.(type) {
	case nil:
	case *Nodes:
		for _, c := range n.List {
			o.node(c)
		}
	case *ForNode:
		n.withLoop = false
		o.loops = append([]*ForNode{n}, o.loops...)
		o.targets = append([]string{n.KeyTarget, n.ValueTarget}, o.targets...)
		o.expr(n.Seq)
		o.node(n.Body)
		o.node(n.Else)
		o.loops = o.loops[1:]
		o.targets = o.targets[2:]
	case *PrintNode:
		o.expr(n.X)
	case *IfNode:
		for i := range n.Tests {
			o.expr(n.Tests[i])
			o.node(n.Bodies[i])
		}
		o.node(n.Else)
	case *SetNode:
		for _, v := range n.Values {
			o.expr(v)
		}
		o.node(n.Capture)
	case *BlockRefNode:
		if len(o.loops) > 0 {
			o.addLoopToCurrent()
		}
	case *IncludeNode:
		if len(o.loops) > 0 && !n.Only {
			o.addLoopToAll()
		}
		o.expr(n.Template)
		o.expr(n.Variables)
	case *EmbedNode:
		// EmbedNode extends IncludeNode
		if len(o.loops) > 0 && !n.Only {
			o.addLoopToAll()
		}
		o.expr(n.Variables)
	case *WithNode:
		o.expr(n.Variables)
		o.node(n.Body)
	case *ImportNode:
		o.expr(n.Template)
	case *DoNode:
		o.expr(n.X)
	case *DeprecatedNode:
		o.expr(n.X)
		o.expr(n.Package)
		o.expr(n.Version)
	case *AutoEscapeNode:
		o.node(n.Body)
	}
}

func (o *optimizer) args(args []Arg) {
	for _, a := range args {
		o.expr(a.Value)
	}
}

func (o *optimizer) expr(x Expr) {
	if x == nil {
		return
	}
	if len(o.loops) > 0 {
		switch n := x.(type) {
		case *NameExpr:
			if n.Name == "loop" {
				n.alwaysDefined = true
				o.addLoopToCurrent()
			} else if o.isTarget(n.Name) {
				n.alwaysDefined = true
			}
		case *BlockRefExpr:
			o.addLoopToCurrent()
		case *FunctionExpr:
			if n.Name == "include" {
				withContext := true
				for _, a := range n.Args {
					if a.Name == "with_context" {
						if c, ok := a.Value.(*ConstExpr); ok && c.Value == false {
							withContext = false
						}
					}
				}
				if withContext {
					o.addLoopToAll()
				}
			}
		case *GetAttrExpr:
			c, isConst := n.Attr.(*ConstExpr)
			if !isConst || c.Value == "parent" {
				nm, isName := n.Node.(*NameExpr)
				if o.loops[0].withLoop || isName && nm.Name == "loop" {
					o.addLoopToAll()
				}
			}
		}
	}
	switch n := x.(type) {
	case *FilterExpr:
		o.expr(n.Node)
		o.args(n.Args)
	case *FunctionExpr:
		o.args(n.Args)
	case *TestExpr:
		o.expr(n.Node)
		o.args(n.Args)
	case *UnaryExpr:
		o.expr(n.X)
	case *BinaryExpr:
		o.expr(n.L)
		o.expr(n.R)
	case *TernaryExpr:
		o.expr(n.Cond)
		o.expr(n.Then)
		o.expr(n.Else)
	case *GetAttrExpr:
		o.expr(n.Node)
		o.expr(n.Attr)
		o.args(n.Args)
	case *ArrayExpr:
		for _, el := range n.Elems {
			o.expr(el.Key)
			o.expr(el.Value)
		}
	case *MacroRefExpr:
		o.expr(n.NameExpr)
		o.args(n.Args)
	case *ArrowExpr:
		o.expr(n.Body)
	case *AssignExpr:
		o.expr(n.Target)
		o.expr(n.Value)
	case *BlockRefExpr:
		o.expr(n.Name)
		o.expr(n.Template)
	case *spreadArgs:
		o.expr(n.Expr)
	}
}
