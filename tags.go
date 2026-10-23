// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import "strings"

type tagParser func(p *Parser, tok *Token) Node

// tagNames are the core tags in registration order (used for suggestions).
var tagNames = []string{"apply", "for", "if", "extends", "include", "block", "use", "macro", "import", "from", "set",
	"types", "flush", "do", "embed", "with", "deprecated", "guard", "autoescape"}

var tagParsers map[string]tagParser

func init() {
	tagParsers = map[string]tagParser{
		"apply": parseApply, "for": parseFor, "if": parseIf, "extends": parseExtends, "include": parseInclude,
		"block": parseBlock, "use": parseUse, "macro": parseMacro, "import": parseImport, "from": parseFrom,
		"set": parseSet, "types": parseTypes, "flush": parseFlush, "do": parseDo, "embed": parseEmbed,
		"with": parseWith, "deprecated": parseDeprecated, "guard": parseGuard, "autoescape": parseAutoescape,
	}
}

func end(tag string, names ...string) *endTest { return &endTest{tag: tag, names: names} }

func parseApply(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	p.applySalt++
	ref := &NameExpr{pos: pos{line}, Name: sprintf("\x00apply_%d", p.applySalt), alwaysDefined: true}
	var filter Expr = ref
	for {
		filter = p.parseFilter(filter)
		if !s.test(OperatorType, "|") {
			break
		}
		s.next()
	}
	s.expect(BlockEndType, "", "")
	body := p.subparse(end("apply", "endapply"), true)
	s.expect(BlockEndType, "", "")
	return &Nodes{pos{line}, []Node{
		newSetCapture([]string{ref.Name}, body, line),
		&PrintNode{pos{line}, filter},
	}}
}

// newSetCapture is SetNode for a capture: an empty body, a text or a constant print become a constant.
func newSetCapture(names []string, body Node, line int) *SetNode {
	n := &SetNode{pos: pos{line}, Names: names, capture: true, Capture: body}
	return n
}

func parseFor(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	targets := p.parseAssignmentExpression()
	s.expect(OperatorType, "in", "")
	seq := p.parseExpression(0)
	s.expect(BlockEndType, "", "")
	body := p.subparse(end("for", "else", "endfor"), false)
	var els Node
	elseLine := 0
	if s.next().Value == "else" {
		elseLine = s.cur().Line
		s.expect(BlockEndType, "", "")
		els = p.subparse(end("for", "endfor"), true)
	}
	s.expect(BlockEndType, "", "")
	n := &ForNode{pos: pos{line}, Seq: seq, Body: body, Else: els, elseLine: elseLine}
	if len(targets) > 1 {
		n.KeyTarget, n.ValueTarget = targets[0], targets[1]
	} else {
		n.KeyTarget, n.ValueTarget = "_key", targets[0]
	}
	return n
}

func parseIf(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	expr := p.parseExpression(0)
	s.expect(BlockEndType, "", "")
	body := p.subparse(end("if", "elseif", "else", "endif"), false)
	n := &IfNode{pos: pos{line}, Tests: []Expr{expr}, Bodies: []Node{body}}
	for done := false; !done; {
		switch s.next().Value {
		case "else":
			s.expect(BlockEndType, "", "")
			n.Else = p.subparse(end("if", "endif"), false)
		case "elseif":
			expr := p.parseExpression(0)
			s.expect(BlockEndType, "", "")
			body := p.subparse(end("if", "elseif", "else", "endif"), false)
			n.Tests = append(n.Tests, expr)
			n.Bodies = append(n.Bodies, body)
		case "endif":
			done = true
		default:
			panic(newSyntaxError(sprintf(`Unexpected end of template. Twig was looking for the following tags "else", "elseif", or "endif" to close the "if" block started at line %d).`, line), s.cur().Line, s.source))
		}
	}
	s.expect(BlockEndType, "", "")
	return n
}

// configNode marks where an extends or use tag was (checked by the correctness pass).
type configNode struct {
	pos
	tag string
}

func parseExtends(p *Parser, tok *Token) Node {
	s := p.stream
	parent := p.parseExpression(0)
	if p.parent == nil {
		p.parent = parent
	}
	s.expect(BlockEndType, "", "")
	return &configNode{pos{tok.Line}, "extends"}
}

func (p *Parser) parseIncludeArguments() (vars Expr, only, ignoreMissing bool) {
	s := p.stream
	if s.nextIf(NameType, "ignore") != nil {
		s.expect(NameType, "missing", "")
		ignoreMissing = true
	}
	if s.nextIf(NameType, "with") != nil {
		vars = p.parseExpression(0)
	}
	if s.nextIf(NameType, "only") != nil {
		only = true
	}
	s.expect(BlockEndType, "", "")
	return
}

func parseInclude(p *Parser, tok *Token) Node {
	expr := p.parseExpression(0)
	vars, only, ignore := p.parseIncludeArguments()
	return &IncludeNode{pos: pos{tok.Line}, Template: expr, Variables: vars, Only: only, IgnoreMissing: ignore}
}

func parseEmbed(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	parent := p.parseExpression(0)
	vars, only, ignore := p.parseIncludeArguments()
	fake := &Token{Type: StringType, Value: "__parent__", Line: line, Offset: -1}
	parentTok := fake
	switch e := parent.(type) {
	case *ConstExpr:
		parentTok = &Token{Type: StringType, Value: e.Value, Line: line, Offset: -1}
	case *NameExpr:
		parentTok = &Token{Type: NameType, Value: e.Name, Line: line, Offset: -1}
	}
	s.injectTokens([]*Token{
		{Type: BlockStartType, Value: "", Line: line, Offset: -1},
		{Type: NameType, Value: "extends", Line: line, Offset: -1},
		parentTok,
		{Type: BlockEndType, Value: "", Line: line, Offset: -1},
	})
	mod := p.parse(s, end("embed", "endembed"), true)
	if parentTok == fake {
		mod.Parent = parent
	}
	p.lastEmbed++
	mod.Index = p.lastEmbed
	p.embedded = append(p.embedded, mod)
	s.expect(BlockEndType, "", "")
	return &EmbedNode{pos: pos{line}, Module: mod, Variables: vars, Only: only, IgnoreMissing: ignore}
}

func parseBlock(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	name := s.expect(NameType, "", "").Value.(string)
	b := &Block{Name: name, Line: line, Body: &EmptyNode{}}
	p.setBlock(name, b)
	p.pushLocalScope()
	p.blockStack = append(p.blockStack, name)
	var body Node
	if s.nextIf(BlockEndType) != nil {
		body = p.subparse(end("block", "endblock"), true)
		if t := s.nextIf(NameType); t != nil {
			if v := t.Value.(string); v != name {
				panic(newSyntaxError(sprintf(`Expected endblock for block "%s" (but "%s" given).`, name, v), s.cur().Line, s.source))
			}
		}
	} else {
		body = &Nodes{pos{line}, []Node{&PrintNode{pos{line}, p.parseExpression(0)}}}
	}
	s.expect(BlockEndType, "", "")
	b.Body = body
	p.blockStack = p.blockStack[:len(p.blockStack)-1]
	p.popLocalScope()
	return &BlockRefNode{pos{line}, name}
}

func parseUse(p *Parser, tok *Token) Node {
	s := p.stream
	tmpl := p.parseExpression(0)
	if _, ok := tmpl.(*ConstExpr); !ok {
		panic(newSyntaxError(`The template references in a "use" statement must be a string.`, s.cur().Line, s.source))
	}
	t := &Trait{Template: tmpl, Line: tmpl.Line()}
	if s.nextIf(NameType, "with") != nil {
		for {
			name := s.expect(NameType, "", "").Value.(string)
			alias := name
			if s.nextIf(NameType, "as") != nil {
				alias = s.expect(NameType, "", "").Value.(string)
			}
			replaced := false
			for i := range t.Targets {
				if t.Targets[i].Name == name {
					t.Targets[i].Alias = alias
					replaced = true
				}
			}
			if !replaced {
				t.Targets = append(t.Targets, TraitTarget{name, alias})
			}
			if s.nextIf(PunctuationType, ",") == nil {
				break
			}
		}
	}
	s.expect(BlockEndType, "", "")
	p.traits = append(p.traits, t)
	return &configNode{pos{tok.Line}, "use"}
}

// macroDeclNode is where a macro was declared (it prints nothing; the correctness pass looks at it).
type macroDeclNode struct {
	pos
	name string
}

func parseMacro(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	name := s.expect(NameType, "", "").Value.(string)
	m := p.parseMacroDefinition(name)
	m.Line = line
	s.expect(BlockEndType, "", "")
	p.pushLocalScope()
	body := p.subparse(end("macro", "endmacro"), true)
	if t := s.nextIf(NameType); t != nil {
		if v := t.Value.(string); v != name {
			panic(newSyntaxError(sprintf(`Expected endmacro for macro "%s" (but "%s" given).`, name, v), s.cur().Line, s.source))
		}
	}
	p.popLocalScope()
	s.expect(BlockEndType, "", "")
	m.Body = body
	p.checkMacroArguments(m)
	p.setMacro(name, m)
	return &macroDeclNode{pos{line}, name}
}

func (p *Parser) checkMacroArguments(m *Macro) {
	seen := map[string]bool{}
	for i, a := range m.Params {
		line := m.Defaults[i].Line()
		if a == "͜varargs" {
			panic(newSyntaxError(sprintf(`The argument "varargs" in macro "%s" cannot be defined because the variable "varargs" is reserved for arbitrary arguments.`, m.Name), line, p.stream.source))
		}
		if m.Variadic != "" && m.Variadic == stripReserved(a) {
			panic(newSyntaxError(sprintf(`The variadic argument "%s" in macro "%s" cannot have the same name as another argument.`, m.Variadic, m.Name), line, p.stream.source))
		}
		if seen[a] {
			panic(newSyntaxError(sprintf(`Argument "%s" is defined twice for macro "%s".`, stripReserved(a), m.Name), line, p.stream.source))
		}
		seen[a] = true
	}
}

const reservedPrefix = "͜"

var reservedNames = map[string]bool{"varargs": true, "context": true, "macros": true, "blocks": true, "this": true}

// tempName is TempNameExpression's name handling: reserved names get a prefix.
func tempName(name string, line int) string {
	switch strings.ToLower(name) {
	case "true", "false", "none", "null":
		panic(newSyntaxError(sprintf(`You cannot assign a value to "%s".`, name), line, nil))
	}
	if reservedNames[name] {
		return reservedPrefix + name
	}
	return name
}

func stripReserved(name string) string {
	if len(name) > len(reservedPrefix) && name[:len(reservedPrefix)] == reservedPrefix {
		return name[len(reservedPrefix):]
	}
	return name
}

func (p *Parser) parseMacroDefinition(macroName string) *Macro {
	s := p.stream
	m := &Macro{Name: macroName}
	s.expect(OperatorType, "(", "A list of arguments must begin with an opening parenthesis")
	for !s.test(PunctuationType, ")") {
		if len(m.Params) > 0 {
			s.expect(PunctuationType, ",", "Arguments must be separated by a comma")
			if s.test(PunctuationType, ")") {
				break
			}
		}
		if s.nextIf(OperatorType, "...") != nil {
			t := s.expect(NameType, "", "A variadic argument must be a name")
			v := stripReserved(tempName(t.Value.(string), t.Line))
			if s.test(OperatorType, "=") {
				panic(newSyntaxError(sprintf(`The variadic argument "%s" in macro "%s" cannot have a default value.`, v, macroName), t.Line, s.source))
			}
			if s.nextIf(PunctuationType, ",") != nil && !s.test(PunctuationType, ")") {
				panic(newSyntaxError(sprintf(`The variadic argument "%s" in macro "%s" must be the last one.`, v, macroName), t.Line, s.source))
			}
			m.Variadic = v
			break
		}
		t := s.expect(NameType, "", "An argument must be a name")
		name := tempName(t.Value.(string), t.Line)
		var def Expr
		if eq := s.nextIf(OperatorType, "="); eq != nil {
			def = p.parseExpression(0)
			if !isConstantExpression(def) {
				panic(newSyntaxError("A default value for an argument must be a constant (a boolean, a string, a number, a sequence, or a mapping).", eq.Line, s.source))
			}
		} else {
			def = &ConstExpr{pos: pos{s.cur().Line}, Value: nil, implicit: true}
		}
		m.Params = append(m.Params, name)
		m.Defaults = append(m.Defaults, def)
	}
	s.expect(PunctuationType, ")", "A list of arguments must be closed by a parenthesis")
	return m
}

func isConstantExpression(e Expr) bool {
	switch n := e.(type) {
	case *ConstExpr:
		return true
	case *ArrayExpr:
		for _, el := range n.Elems {
			if el.Key != nil && !isConstantExpression(el.Key) || !isConstantExpression(el.Value) {
				return false
			}
		}
		return true
	case *UnaryExpr:
		return (n.Op == "-" || n.Op == "+") && isConstantExpression(n.X)
	}
	return false
}

func parseImport(p *Parser, tok *Token) Node {
	s := p.stream
	macro := p.parseExpression(0)
	s.expect(NameType, "as", "")
	nameTok := s.expect(NameType, "", "")
	name := nameTok.Value.(string)
	checkAssignable(name, nameTok.Line)
	s.expect(BlockEndType, "", "")
	p.addImportedSymbol("template", name, "", "")
	return &ImportNode{pos: pos{tok.Line}, Template: macro, Var: name, Global: p.isMainScope()}
}

func parseFrom(p *Parser, tok *Token) Node {
	s := p.stream
	macro := p.parseExpression(0)
	s.expect(NameType, "import", "")
	type target struct{ name, alias string }
	var targets []target
	for {
		name := s.expect(NameType, "", "").Value.(string)
		alias := name
		if s.nextIf(NameType, "as") != nil {
			alias = s.expect(NameType, "", "").Value.(string)
		}
		checkAssignable(alias, tok.Line)
		replaced := false
		for i := range targets {
			if targets[i].name == name {
				targets[i].alias = alias
				replaced = true
			}
		}
		if !replaced {
			targets = append(targets, target{name, alias})
		}
		if s.nextIf(PunctuationType, ",") == nil {
			break
		}
	}
	s.expect(BlockEndType, "", "")
	ref := p.varName()
	for _, t := range targets {
		p.addImportedSymbol("function", t.alias, t.name, ref)
	}
	return &ImportNode{pos: pos{tok.Line}, Template: macro, Var: ref, Global: p.isMainScope()}
}

func parseSet(p *Parser, tok *Token) Node {
	s := p.stream
	line := tok.Line
	names := p.parseAssignmentExpression()
	if s.nextIf(OperatorType, "=") != nil {
		var values []Expr
		for {
			values = append(values, p.parseExpression(0))
			if s.nextIf(PunctuationType, ",") == nil {
				break
			}
		}
		s.expect(BlockEndType, "", "")
		if len(names) != len(values) {
			panic(newSyntaxError("When using set, you must have the same number of variables and assignments.", s.cur().Line, s.source))
		}
		return &SetNode{pos: pos{line}, Names: names, Values: values}
	}
	if len(names) > 1 {
		panic(newSyntaxError("When using set with a block, you cannot have a multi-target.", s.cur().Line, s.source))
	}
	s.expect(BlockEndType, "", "")
	body := p.subparse(end("set", "endset"), true)
	s.expect(BlockEndType, "", "")
	return newSetCapture(names, body, line)
}

func parseTypes(p *Parser, tok *Token) Node {
	s := p.stream
	enclosed := s.nextIf(PunctuationType, "{") != nil
	first := true
	for !(s.test(PunctuationType, "}") || s.test(BlockEndType)) {
		if !first {
			s.expect(PunctuationType, ",", "A type string must be followed by a comma")
			if s.test(PunctuationType, "}") || s.test(BlockEndType) {
				break
			}
		}
		first = false
		s.expect(NameType, "", "")
		if s.nextIf(OperatorType, "?:") == nil {
			s.nextIf(OperatorType, "?")
			s.expect(PunctuationType, ":", "A type name must be followed by a colon (:)")
		}
		s.expect(StringType, "", "")
	}
	if enclosed {
		s.expect(PunctuationType, "}", "An opened mapping is not properly closed")
	}
	s.expect(BlockEndType, "", "")
	return &EmptyNode{pos{tok.Line}}
}

func parseFlush(p *Parser, tok *Token) Node {
	p.stream.expect(BlockEndType, "", "")
	return &EmptyNode{pos{tok.Line}}
}

func parseDo(p *Parser, tok *Token) Node {
	expr := p.parseExpression(0)
	p.stream.expect(BlockEndType, "", "")
	return &DoNode{pos{tok.Line}, expr}
}

func parseWith(p *Parser, tok *Token) Node {
	s := p.stream
	var vars Expr
	only := false
	if !s.test(BlockEndType) {
		vars = p.parseExpression(0)
		only = s.nextIf(NameType, "only") != nil
	}
	s.expect(BlockEndType, "", "")
	body := p.subparse(end("with", "endwith"), true)
	s.expect(BlockEndType, "", "")
	return &WithNode{pos: pos{tok.Line}, Body: body, Variables: vars, Only: only}
}

func parseDeprecated(p *Parser, tok *Token) Node {
	s := p.stream
	n := &DeprecatedNode{pos: pos{tok.Line}, X: p.parseExpression(0)}
	for s.test(NameType) {
		k := s.cur().Value.(string)
		s.next()
		s.expect(OperatorType, "=", "")
		switch k {
		case "package":
			n.Package = p.parseExpression(0)
		case "version":
			n.Version = p.parseExpression(0)
		default:
			panic(newSyntaxError(sprintf(`Unknown "%s" option.`, k), s.cur().Line, s.source))
		}
	}
	s.expect(BlockEndType, "", "")
	return n
}

func parseGuard(p *Parser, tok *Token) Node {
	s := p.stream
	typeTok := s.expect(NameType, "", "")
	typ := typeTok.Value.(string)
	if typ != "function" && typ != "filter" && typ != "test" {
		panic(newSyntaxError(sprintf(`Supported guard types are function, filter and test, "%s" given.`, typ), typeTok.Line, s.source))
	}
	name := s.expect(NameType, "", "").Value.(string)
	if typ == "test" && s.test(NameType) {
		name += " " + s.cur().Value.(string)
		s.next()
	}
	var exists bool
	switch typ {
	case "function":
		exists = functions[name] != nil
	case "filter":
		exists = filters[name] != nil
	case "test":
		exists = tests[name] != nil
	}
	s.expect(BlockEndType, "", "")
	var body Node = &EmptyNode{}
	if exists {
		body = p.subparse(end("guard", "else", "endguard"), false)
	} else {
		prev := p.ignoreUnknown
		p.ignoreUnknown = true
		p.subparse(end("guard", "else", "endguard"), false)
		p.ignoreUnknown = prev
	}
	var els Node = &EmptyNode{}
	if s.next().Value == "else" {
		s.expect(BlockEndType, "", "")
		els = p.subparse(end("guard", "endguard"), true)
	}
	s.expect(BlockEndType, "", "")
	if exists {
		return body
	}
	return els
}

func parseAutoescape(p *Parser, tok *Token) Node {
	s := p.stream
	var value any = "html"
	if !s.test(BlockEndType) {
		expr := p.parseExpression(0)
		c, ok := expr.(*ConstExpr)
		if !ok {
			panic(newSyntaxError("An escaping strategy must be a string or false.", s.cur().Line, s.source))
		}
		value = c.Value
	}
	s.expect(BlockEndType, "", "")
	body := p.subparse(end("autoescape", "endautoescape"), true)
	s.expect(BlockEndType, "", "")
	return &AutoEscapeNode{pos: pos{tok.Line}, Strategy: value, Body: body}
}
