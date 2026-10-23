// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"strings"
)

// importedSymbol is a macro import known to the parser.
type importedSymbol struct {
	name string // the macro name (for functions)
	ref  string // the macro variable
}

// Parser is Twig\Parser.
type Parser struct {
	env           *Environment
	stream        *TokenStream
	parent        Expr
	blocks        map[string]*Block
	blockOrder    []string
	blockStack    []string
	macros        map[string]*Macro
	macroOrder    []string
	traits        []*Trait
	embedded      []*Module
	imported      []map[string]map[string]importedSymbol
	lastEmbed     int
	depth         int
	varSalt       int
	ignoreUnknown bool
	applySalt     int
}

type parserState struct {
	stream     *TokenStream
	parent     Expr
	blocks     map[string]*Block
	blockOrder []string
	blockStack []string
	macros     map[string]*Macro
	macroOrder []string
	traits     []*Trait
	embedded   []*Module
	imported   []map[string]map[string]importedSymbol
	ignore     bool
}

func (p *Parser) save() parserState {
	return parserState{p.stream, p.parent, p.blocks, p.blockOrder, p.blockStack, p.macros, p.macroOrder, p.traits,
		p.embedded, p.imported, p.ignoreUnknown}
}

func (p *Parser) restore(s parserState) {
	p.stream, p.parent, p.blocks, p.blockOrder, p.blockStack, p.macros, p.macroOrder, p.traits, p.embedded, p.imported,
		p.ignoreUnknown = s.stream, s.parent, s.blocks, s.blockOrder, s.blockStack, s.macros, s.macroOrder, s.traits,
		s.embedded, s.imported, s.ignore
}

// endTest decides whether a tag closes the current subparse; tag is the owning tag's name, line its line.
type endTest struct {
	tag   string
	names []string
}

func (t *endTest) match(tok *Token) bool { return tok.Test(NameType, t.names...) }

// parse is Parser::parse: a module from a token stream, stopping at a closing tag when test is set (embed).
func (p *Parser) parse(stream *TokenStream, test *endTest, dropNeedle bool) (mod *Module) {
	if p.depth == 0 {
		p.lastEmbed = 0
	}
	saved := p.save()
	p.depth++
	p.stream = stream
	p.parent = nil
	p.blocks = map[string]*Block{}
	p.blockOrder = nil
	p.blockStack = nil
	p.macros = map[string]*Macro{}
	p.macroOrder = nil
	p.traits = nil
	p.embedded = nil
	p.imported = []map[string]map[string]importedSymbol{{}}
	defer func() {
		if r := recover(); r != nil {
			if e, ok := r.(*Error); ok && e.Kind == SyntaxErrorKind {
				if e.Source == nil {
					e.Source = p.stream.source
				}
				if e.Line <= 0 {
					e.Line = p.stream.cur().Line
				}
			}
			p.depth--
			p.restore(saved)
			panic(r)
		}
	}()
	body := p.subparse(test, dropNeedle)
	if p.parent != nil {
		body = cleanupBodyForChildTemplates(body)
	}
	mod = &Module{Source: stream.source, Body: body, Parent: p.parent, Blocks: p.blocks, order: p.blockOrder,
		Macros: p.macros, macroSeq: p.macroOrder, Traits: p.traits, Embedded: p.embedded}
	p.checkCorrectness(mod)
	p.escape(mod)
	optimize(mod)
	p.depth--
	p.restore(saved)
	return mod
}

func isBlankText(n Node) bool {
	t, ok := n.(*TextNode)
	// TextNode::isBlank: ctype_space
	return ok && strings.Trim(t.Data, " \t\n\r\v\f") == ""
}

func cleanupBodyForChildTemplates(body Node) Node {
	if _, ok := body.(*BlockRefNode); ok {
		return &EmptyNode{}
	}
	if isBlankText(body) {
		return &EmptyNode{}
	}
	if ns, ok := body.(*Nodes); ok {
		var kept []Node
		for _, n := range ns.List {
			if _, ok := n.(*BlockRefNode); ok {
				continue
			}
			if isBlankText(n) {
				continue
			}
			kept = append(kept, n)
		}
		ns.List = kept
	}
	return body
}

func nodesOf(rv []Node, line int) Node {
	if len(rv) == 1 {
		return rv[0]
	}
	return &Nodes{pos{line}, rv}
}

// subparse is Parser::subparse.
func (p *Parser) subparse(test *endTest, dropNeedle bool) Node {
	s := p.stream
	lineno := s.cur().Line
	var rv []Node
	for !s.isEOF() {
		tok := s.cur()
		switch tok.Type {
		case TextType:
			s.next()
			rv = append(rv, &TextNode{pos{tok.Line}, tok.Value.(string)})
		case VarStartType:
			s.next()
			expr := p.parseExpression(0)
			s.expect(VarEndType, "", "")
			rv = append(rv, &PrintNode{pos{tok.Line}, expr})
		case BlockStartType:
			s.next()
			tok = s.cur()
			if tok.Type != NameType {
				panic(newSyntaxError("A block must start with a tag name.", tok.Line, s.source))
			}
			if test != nil && test.match(tok) {
				if dropNeedle {
					s.next()
				}
				return nodesOf(rv, lineno)
			}
			name := tok.Value.(string)
			tp := tagParsers[name]
			if tp == nil {
				var e *Error
				if test != nil {
					e = newSyntaxError(sprintf(`Unexpected "%s" tag`, name), tok.Line, s.source)
					e.Raw += sprintf(` (expecting closing tag for the "%s" tag defined near line %d).`, test.tag, lineno)
				} else {
					e = newSyntaxError(sprintf(`Unknown "%s" tag.`, name), tok.Line, s.source)
					e.addSuggestions(name, tagNames)
				}
				panic(e)
			}
			s.next()
			if n := tp(p, tok); n != nil {
				rv = append(rv, n)
			}
		default:
			panic(newSyntaxError("The lexer or the parser ended up in an unsupported state.", tok.Line, s.source))
		}
	}
	return nodesOf(rv, lineno)
}

func (p *Parser) peekBlockStack() string {
	if len(p.blockStack) == 0 {
		return ""
	}
	return p.blockStack[len(p.blockStack)-1]
}

func (p *Parser) setBlock(name string, b *Block) {
	if old, ok := p.blocks[name]; ok {
		panic(newSyntaxError(sprintf("The block '%s' has already been defined line %d.", name, old.Line), p.stream.cur().Line, p.stream.source))
	}
	p.blocks[name] = b
	p.blockOrder = append(p.blockOrder, name)
}

func (p *Parser) setMacro(name string, m *Macro) {
	if _, ok := p.macros[name]; !ok {
		p.macroOrder = append(p.macroOrder, name)
	}
	p.macros[name] = m
}

func (p *Parser) addImportedSymbol(typ, alias, name, ref string) {
	m := p.imported[0][typ]
	if m == nil {
		m = map[string]importedSymbol{}
		p.imported[0][typ] = m
	}
	m[alias] = importedSymbol{name: name, ref: ref}
}

func (p *Parser) getImportedSymbol(typ, alias string) *importedSymbol {
	if s, ok := p.imported[0][typ][alias]; ok {
		return &s
	}
	if s, ok := p.imported[len(p.imported)-1][typ][alias]; ok {
		return &s
	}
	return nil
}

func (p *Parser) isMainScope() bool { return len(p.imported) == 1 }

func (p *Parser) pushLocalScope() {
	p.imported = append([]map[string]map[string]importedSymbol{{}}, p.imported...)
}

func (p *Parser) popLocalScope() { p.imported = p.imported[1:] }

func (p *Parser) hasInheritance() bool { return p.parent != nil || len(p.traits) > 0 }

func (p *Parser) varName() string {
	p.varSalt++
	return sprintf("\x00internal_%d", p.varSalt)
}

func (p *Parser) getFilter(name string, line int) *callable {
	if f := filters[name]; f != nil {
		return f
	}
	if p.ignoreUnknown {
		return ignoredCallable("filter", name)
	}
	e := newSyntaxError(sprintf(`Unknown "%s" filter.`, name), line, p.stream.source)
	e.addSuggestions(name, filterNames)
	panic(e)
}

func (p *Parser) getFunction(name string, line int) *callable {
	if f := functions[name]; f != nil {
		return f
	}
	if p.ignoreUnknown {
		return ignoredCallable("function", name)
	}
	e := newSyntaxError(sprintf(`Unknown "%s" function.`, name), line, p.stream.source)
	e.addSuggestions(name, functionNames)
	panic(e)
}

func (p *Parser) getTest(line int) *callable {
	s := p.stream
	name := s.expect(NameType, "", "").Value.(string)
	var t *callable
	if s.test(NameType) {
		name = name + " " + s.cur().Value.(string)
		t = tests[name]
		s.next()
	} else {
		t = tests[name]
	}
	if t == nil {
		if p.ignoreUnknown {
			return ignoredCallable("test", name)
		}
		e := newSyntaxError(sprintf(`Unknown "%s" test.`, name), line, s.source)
		e.addSuggestions(name, testNames)
		panic(e)
	}
	return t
}

// ---- expressions ----------------------------------------------------------------------------------------------

type infixOp struct {
	prec       int
	rightAssoc bool
}

// binaryOps are the binary operators with their precedence and associativity (CoreExtension::getExpressionParsers).
var binaryOps = map[string]infixOp{
	"?:": {5, true}, "? :": {5, true}, "??": {300, true}, "or": {10, false}, "xor": {12, false}, "and": {15, false},
	"b-or": {16, false}, "b-xor": {17, false}, "b-and": {18, false}, "==": {20, false}, "!=": {20, false},
	"<=>": {20, false}, "<": {20, false}, ">": {20, false}, ">=": {20, false}, "<=": {20, false},
	"not in": {20, false}, "in": {20, false}, "matches": {20, false}, "starts with": {20, false},
	"ends with": {20, false}, "has some": {20, false}, "has every": {20, false}, "===": {20, false},
	"!==": {20, false}, "..": {25, false}, "+": {30, false}, "-": {30, false}, "~": {40, false}, "*": {60, false},
	"/": {60, false}, "//": {60, false}, "%": {60, false}, "**": {200, true},
}

// infixPrecedence returns the precedence of an infix operator token, or -1 when it is not one.
func infixPrecedence(op string) int {
	if o, ok := binaryOps[op]; ok {
		return o.prec
	}
	switch op {
	case "?", "=":
		return 0
	case "is", "is not":
		return 100
	case "|", "(", ".", "?.", "[":
		return 512
	case "=>":
		return 250
	}
	return -1
}

var unaryOps = map[string]int{"not": 50, "...": 512, "-": 500, "+": 500}

func (p *Parser) parseExpression(precedence int) Expr {
	s := p.stream
	tok := s.cur()
	var expr Expr
	if tok.Type == OperatorType {
		op := tok.Str()
		if prec, ok := unaryOps[op]; ok {
			s.next()
			operandPrec := prec
			if op == "..." {
				operandPrec = 0
			}
			expr = &UnaryExpr{pos: pos{tok.Line}, Op: op, X: p.parseExpression(operandPrec)}
		} else if op == "(" {
			s.next()
			expr = p.parseGroup(tok)
		} else {
			expr = p.parseLiteral(tok)
		}
	} else {
		expr = p.parseLiteral(tok)
	}
	tok = s.cur()
	for tok.Type == OperatorType {
		op := tok.Str()
		prec := infixPrecedence(op)
		if prec < 0 || prec < precedence {
			break
		}
		s.next()
		expr = p.parseInfix(op, expr, tok)
		tok = s.cur()
	}
	return expr
}

func (p *Parser) parseInfix(op string, left Expr, tok *Token) Expr {
	s := p.stream
	line := tok.Line
	switch op {
	case "?":
		then := p.parseExpression(0)
		var els Expr
		if s.nextIf(PunctuationType, ":") != nil {
			els = p.parseExpression(0)
		} else {
			els = &ConstExpr{pos: pos{line}, Value: ""}
		}
		return &TernaryExpr{pos: pos{line}, Cond: left, Then: then, Else: els}
	case "=":
		return p.parseAssignment(left, tok)
	case "is":
		return p.parseTestExpr(left, tok)
	case "is not":
		return &UnaryExpr{pos: pos{line}, Op: "not", X: p.parseTestExpr(left, tok)}
	case "|":
		return p.parseFilter(left)
	case "(":
		return p.parseFunctionCall(left, tok)
	case ".", "?.":
		return p.parseDot(left, tok)
	case "[":
		return p.parseSubscript(left, tok)
	case "=>":
		return p.parseArrow(left, tok)
	}
	o := binaryOps[op]
	rp := o.prec + 1
	if o.rightAssoc {
		rp = o.prec
	}
	right := p.parseExpression(rp)
	if op == "matches" {
		checkConstantRegexp(right, line)
	}
	if op == "? :" {
		op = "?:"
	}
	if op == "**" {
		return powerOf(left, right, line)
	}
	b := &BinaryExpr{pos: pos{line}, Op: op, L: left, R: right}
	if op == "??" && !supportsDefinedTest(left) {
		panic(newSyntaxError(`The "defined" test only works with simple variables.`, left.Line(), nil))
	}
	if op == "??" || op == "?:" {
		b.testL = left
		if n, ok := left.(*NameExpr); ok {
			// NullCoalesceBinary and ElvisBinary mark the left operand always defined
			n.alwaysDefined = true
		}
	}
	return b
}

// powerOf builds left ** right. Twig compiles a unary operator without parentheses as " -x", and PHP's "**" binds
// tighter than its unary minus and plus, so "-2 ** 2" is -(2 ** 2) even though Twig parsed (-2) ** 2.
func powerOf(left, right Expr, line int) Expr {
	if u, ok := left.(*UnaryExpr); ok && !u.parens && (u.Op == "-" || u.Op == "+") {
		return &UnaryExpr{pos: u.pos, Op: u.Op, X: powerOf(u.X, right, line)}
	}
	return &BinaryExpr{pos: pos{line}, Op: "**", L: left, R: right}
}

func setParens(e Expr) Expr {
	switch e := e.(type) {
	case *ConstExpr:
	case *NameExpr:
		e.parens = true
	case *UnaryExpr:
		e.parens = true
	case *BinaryExpr:
		e.parens = true
	case *TernaryExpr:
		e.parens = true
	case *GetAttrExpr:
		e.parens = true
	case *FilterExpr:
		e.parens = true
	case *FunctionExpr:
		e.parens = true
	case *TestExpr:
		e.parens = true
	case *ArrowExpr:
		e.parens = true
	case *AssignExpr:
		e.parens = true
	}
	return e
}

func (p *Parser) parseGroup(tok *Token) Expr {
	s := p.stream
	expr := p.parseExpression(0)
	if s.nextIf(PunctuationType, ")") != nil {
		if !s.test(OperatorType, "=>") {
			return setParens(expr)
		}
		return &ListExpr{pos{tok.Line}, []string{toAssignName(expr)}}
	}
	if !s.test(PunctuationType, ",") {
		s.expect(PunctuationType, ")", "An opened parenthesis is not properly closed")
	}
	names := []string{toAssignName(expr)}
	line := tok.Line
	for {
		if s.nextIf(PunctuationType, ")") != nil {
			break
		}
		s.expect(PunctuationType, ",", "")
		t := s.expect(NameType, "", "")
		names = append(names, t.Value.(string))
		line = t.Line
	}
	if !s.test(OperatorType, "=>") {
		panic(newSyntaxError("A list of variables must be followed by an arrow.", s.cur().Line, s.source))
	}
	return &ListExpr{pos{line}, names}
}

func toAssignName(e Expr) string {
	n, ok := e.(*NameExpr)
	if !ok {
		panic(newSyntaxError("A list must only contain variables.", e.Line(), nil))
	}
	return n.Name
}

func (p *Parser) parseLiteral(tok *Token) Expr {
	s := p.stream
	switch tok.Type {
	case NameType:
		s.next()
		switch tok.Value.(string) {
		case "true", "TRUE":
			return &ConstExpr{pos: pos{tok.Line}, Value: true}
		case "false", "FALSE":
			return &ConstExpr{pos: pos{tok.Line}, Value: false}
		case "none", "NONE", "null", "NULL":
			return &ConstExpr{pos: pos{tok.Line}, Value: nil}
		}
		return &NameExpr{pos: pos{tok.Line}, Name: tok.Value.(string)}
	case NumberType:
		s.next()
		return &ConstExpr{pos: pos{tok.Line}, Value: tok.Value}
	case StringType, InterpolationStartType:
		return p.parseStringExpression()
	case PunctuationType:
		if tok.Value == "{" {
			return p.parseMapping()
		}
	}
	if tok.Type == PunctuationType || tok.Type == OperatorType {
		if tok.Type == OperatorType && tok.Value == "[" {
			return p.parseSequence()
		}
		if tok.Type == OperatorType && isName(tok.Str()) {
			s.next()
			return &NameExpr{pos: pos{tok.Line}, Name: tok.Str()}
		}
	}
	panic(newSyntaxError(sprintf(`Unexpected token "%s" of value "%s".`, tok.Type.English(), tok.Str()), tok.Line, s.source))
}

func (p *Parser) parseStringExpression() Expr {
	s := p.stream
	var nodes []Expr
	nextCanBeString := true
	for {
		if nextCanBeString {
			if tok := s.nextIf(StringType); tok != nil {
				nodes = append(nodes, &ConstExpr{pos: pos{tok.Line}, Value: tok.Value})
				nextCanBeString = false
				continue
			}
		}
		if s.nextIf(InterpolationStartType) != nil {
			nodes = append(nodes, p.parseExpression(0))
			s.expect(InterpolationEndType, "", "")
			nextCanBeString = true
			continue
		}
		break
	}
	expr := nodes[0]
	for _, n := range nodes[1:] {
		expr = &BinaryExpr{pos: pos{n.Line()}, Op: "~", L: expr, R: n}
	}
	return expr
}

func (p *Parser) parseSequence() Expr {
	s := p.stream
	s.expect(OperatorType, "[", "A sequence element was expected")
	node := &ArrayExpr{pos: pos{s.cur().Line}}
	first := true
	for !s.test(PunctuationType, "]") {
		if !first {
			s.expect(PunctuationType, ",", "A sequence element must be followed by a comma")
			if s.test(PunctuationType, "]") {
				break
			}
		}
		first = false
		if s.test(PunctuationType, ",") {
			node.Elems = append(node.Elems, ArrayElem{Value: &EmptyExpr{pos{s.cur().Line}}})
		} else {
			node.Elems = append(node.Elems, ArrayElem{Value: p.parseExpression(0)})
		}
	}
	s.expect(PunctuationType, "]", "An opened sequence is not properly closed")
	return node
}

func (p *Parser) parseMapping() Expr {
	s := p.stream
	s.expect(PunctuationType, "{", "A mapping element was expected")
	node := &ArrayExpr{pos: pos{s.cur().Line}}
	first := true
	for !s.test(PunctuationType, "}") {
		if !first {
			s.expect(PunctuationType, ",", "A mapping value must be followed by a comma")
			if s.test(PunctuationType, "}") {
				break
			}
		}
		first = false
		if s.test(OperatorType, "...") {
			node.Elems = append(node.Elems, ArrayElem{Value: p.parseExpression(0)})
			continue
		}
		var key Expr
		if tok := s.nextIf(NameType); tok != nil {
			key = &ConstExpr{pos: pos{tok.Line}, Value: tok.Value}
			if s.test(PunctuationType, ",") || s.test(PunctuationType, "}") {
				node.Elems = append(node.Elems, ArrayElem{Key: key, Value: &NameExpr{pos: pos{tok.Line}, Name: tok.Value.(string)}})
				continue
			}
		} else if tok := s.nextIf(StringType); tok != nil {
			key = &ConstExpr{pos: pos{tok.Line}, Value: tok.Value}
		} else if tok := s.nextIf(NumberType); tok != nil {
			key = &ConstExpr{pos: pos{tok.Line}, Value: tok.Value}
		} else if s.test(OperatorType, "(") {
			key = p.parseExpression(0)
		} else {
			cur := s.cur()
			panic(newSyntaxError(sprintf(`A mapping key must be a quoted string, a number, a name, or an expression enclosed in parentheses (unexpected token "%s" of value "%s".`, cur.Type.English(), cur.Str()), cur.Line, s.source))
		}
		s.expect(PunctuationType, ":", "A mapping key must be followed by a colon (:)")
		value := p.parseExpression(0)
		node.Elems = append(node.Elems, ArrayElem{Key: key, Value: value})
	}
	s.expect(PunctuationType, "}", "An opened mapping is not properly closed")
	return node
}

func exprClassName(e Expr) string {
	switch e := e.(type) {
	case *ConstExpr:
		return `Twig\Node\Expression\ConstantExpression`
	case *NameExpr:
		return `Twig\Node\Expression\Variable\ContextVariable`
	case *ArrayExpr:
		return `Twig\Node\Expression\ArrayExpression`
	case *GetAttrExpr:
		return `Twig\Node\Expression\GetAttrExpression`
	case *FilterExpr:
		switch e.Name {
		case "default":
			return `Twig\Node\Expression\Filter\DefaultFilter`
		case "escape", "e":
			return `Twig\Node\Expression\Filter\EscapeFilter`
		case "raw":
			return `Twig\Node\Expression\Filter\RawFilter`
		}
		return `Twig\Node\Expression\FilterExpression`
	case *FunctionExpr:
		return `Twig\Node\Expression\FunctionExpression`
	case *TestExpr:
		return testClassName(e.Name)
	case *MacroRefExpr:
		return `Twig\Node\Expression\MacroReferenceExpression`
	case *ArrowExpr:
		return `Twig\Node\Expression\ArrowFunctionExpression`
	case *TernaryExpr:
		return `Twig\Node\Expression\Ternary\ConditionalTernary`
	case *BlockRefExpr:
		return `Twig\Node\Expression\BlockReferenceExpression`
	case *ParentExpr:
		return `Twig\Node\Expression\ParentExpression`
	case *AssignExpr:
		return `Twig\Node\Expression\Binary\SetBinary`
	case *UnaryExpr:
		switch e.Op {
		case "not":
			return `Twig\Node\Expression\Unary\NotUnary`
		case "-":
			return `Twig\Node\Expression\Unary\NegUnary`
		case "+":
			return `Twig\Node\Expression\Unary\PosUnary`
		}
		return `Twig\Node\Expression\Unary\SpreadUnary`
	case *BinaryExpr:
		return `Twig\Node\Expression\Binary\` + binaryClass[e.Op] + "Binary"
	case *ListExpr:
		return `Twig\Node\Expression\ListExpression`
	}
	return `Twig\Node\Expression\AbstractExpression`
}

var binaryClass = map[string]string{
	"?:": "Elvis", "??": "NullCoalesce", "or": "Or", "xor": "Xor", "and": "And", "b-or": "BitwiseOr",
	"b-xor": "BitwiseXor", "b-and": "BitwiseAnd", "==": "Equal", "!=": "NotEqual", "<=>": "Spaceship", "<": "Less",
	">": "Greater", ">=": "GreaterEqual", "<=": "LessEqual", "not in": "NotIn", "in": "In", "matches": "Matches",
	"starts with": "StartsWith", "ends with": "EndsWith", "has some": "HasSome", "has every": "HasEvery",
	"===": "SameAs", "!==": "NotSameAs", "..": "Range", "+": "Add", "-": "Sub", "~": "Concat", "*": "Mul",
	"/": "Div", "//": "FloorDiv", "%": "Mod", "**": "Power",
}

func testClassName(name string) string {
	switch name {
	case "even":
		return `Twig\Node\Expression\Test\EvenTest`
	case "odd":
		return `Twig\Node\Expression\Test\OddTest`
	case "defined":
		return `Twig\Node\Expression\Test\DefinedTest`
	case "same as":
		return `Twig\Node\Expression\Test\SameasTest`
	case "none", "null":
		return `Twig\Node\Expression\Test\NullTest`
	case "divisible by":
		return `Twig\Node\Expression\Test\DivisiblebyTest`
	case "constant":
		return `Twig\Node\Expression\Test\ConstantTest`
	case "true":
		return `Twig\Node\Expression\Test\TrueTest`
	}
	return `Twig\Node\Expression\TestExpression`
}

// parseNamedArguments is ArgumentsTrait::parseNamedArguments.
func (p *Parser) parseNamedArguments(parseOpen bool) []Arg {
	s := p.stream
	if parseOpen {
		s.expect(OperatorType, "(", "A list of arguments must begin with an opening parenthesis")
	}
	args := []Arg{}
	hasSpread := false
	for !s.test(PunctuationType, ")") {
		if len(args) > 0 {
			s.expect(PunctuationType, ",", "Arguments must be separated by a comma")
			if s.test(PunctuationType, ")") {
				break
			}
		}
		value := p.parseExpression(0)
		if u, ok := value.(*UnaryExpr); ok && u.Op == "..." {
			hasSpread = true
		} else if hasSpread {
			panic(newSyntaxError("Normal arguments must be placed before argument unpacking.", s.cur().Line, s.source))
		}
		name := ""
		if a, ok := value.(*AssignExpr); ok {
			if n, ok := a.Target.(*NameExpr); ok {
				name = n.Name
				value = a.Value
			}
		} else if tok := s.nextIf(OperatorType, "="); tok != nil {
			name, value = p.namedArgValue(value, tok)
		} else if tok := s.nextIf(PunctuationType, ":"); tok != nil {
			name, value = p.namedArgValue(value, tok)
		}
		// a repeated name replaces the earlier value at its position ($args[$name] = $value)
		replaced := false
		if name != "" {
			for i := range args {
				if args[i].Name == name {
					args[i].Value = value
					replaced = true
				}
			}
		}
		if !replaced {
			args = append(args, Arg{Name: name, Value: value})
		}
	}
	s.expect(PunctuationType, ")", "A list of arguments must be closed by a parenthesis")
	return args
}

func (p *Parser) namedArgValue(value Expr, tok *Token) (string, Expr) {
	n, ok := value.(*NameExpr)
	if !ok {
		panic(newSyntaxError(sprintf(`A parameter name must be a string, "%s" given.`, exprClassName(value)), tok.Line, p.stream.source))
	}
	return n.Name, p.parseExpression(0)
}

func (p *Parser) parseAssignment(left Expr, tok *Token) Expr {
	switch l := left.(type) {
	case *NameExpr:
		right := p.parseExpression(0)
		return &AssignExpr{pos: pos{tok.Line}, Target: l, Value: right}
	case *ArrayExpr:
		right := p.parseExpression(0)
		empty := len(l.Elems) == 0
		if !empty && l.isSequence() {
			empty = true
			for _, e := range l.Elems {
				if _, ok := e.Value.(*EmptyExpr); !ok {
					empty = false
				}
			}
		}
		if empty {
			panic(newSyntaxError("Cannot destructure to an empty list of variables.", tok.Line, p.stream.source))
		}
		seq := l.isSequence()
		for _, e := range l.Elems {
			switch e.Value.(type) {
			case *NameExpr, *EmptyExpr:
			default:
				what := "sequence"
				if !seq {
					what = "object/mapping"
				}
				panic(newSyntaxError(sprintf(`Cannot assign to "%s", only variables can be assigned in %s destructuring.`, exprClassName(e.Value), what), tok.Line, nil))
			}
		}
		return &AssignExpr{pos: pos{tok.Line}, Target: l, Value: right}
	}
	panic(newSyntaxError(sprintf(`Cannot assign to "%s", only variables can be assigned.`, exprClassName(left)), tok.Line, p.stream.source))
}

func (p *Parser) parseArrow(left Expr, tok *Token) Expr {
	var params []string
	switch l := left.(type) {
	case *NameExpr:
		params = []string{l.Name}
	case *ListExpr:
		params = l.Names
	default:
		panic(newSyntaxError("The arrow function argument must be a list of variables or a single variable.", left.Line(), p.stream.source))
	}
	body := p.parseExpression(0)
	return &ArrowExpr{pos: pos{tok.Line}, Params: params, Body: body}
}

func (p *Parser) parseTestExpr(node Expr, tok *Token) Expr {
	s := p.stream
	t := p.getTest(tok.Line)
	var args []Arg
	if s.test(OperatorType, "(") {
		args = p.parseNamedArguments(true)
	} else if t.oneMandatoryArgument {
		args = []Arg{{Value: p.parseExpression(100)}}
	}
	if t.name == "defined" {
		if n, ok := node.(*NameExpr); ok {
			if alias := p.getImportedSymbol("function", n.Name); alias != nil {
				node = &MacroRefExpr{pos: pos{n.Line()}, Var: alias.ref, Name: alias.name}
			}
		}
		if !enableDefinedTest(node) {
			panic(newSyntaxError(`The "defined" test only works with simple variables.`, s.cur().Line, nil))
		}
	}
	return &TestExpr{pos: pos{s.cur().Line}, Node: node, Name: t.name, Args: args, def: t}
}

// supportsDefinedTest reports whether a node implements SupportDefinedTestInterface.
func supportsDefinedTest(e Expr) bool {
	switch e.(type) {
	case *NameExpr, *GetAttrExpr, *ConstExpr, *ArrayExpr, *FunctionExpr, *MacroRefExpr, *BlockRefExpr:
		return true
	}
	return false
}

// enableDefinedTest is SupportDefinedTestInterface::enableDefinedTest; it reports whether the node supports it.
func enableDefinedTest(e Expr) bool {
	switch n := e.(type) {
	case *NameExpr:
		n.definedTest = true
	case *GetAttrExpr:
		n.definedTest = true
		changeIgnoreStrictCheck(n)
	case *ConstExpr, *ArrayExpr, *FunctionExpr:
		// constants and sequences are always defined; functions other than constant() ignore the flag
		switch c := n.(type) {
		case *ConstExpr:
			return enableConst(c)
		case *ArrayExpr:
			c.definedTest = true
		}
	case *MacroRefExpr:
		n.definedTest = true
	case *BlockRefExpr:
		n.definedTest = true
	default:
		return false
	}
	return true
}

// enableConst marks a constant under a defined test: it evaluates to true there.
func enableConst(c *ConstExpr) bool {
	c.Value = definedTrue{c.Value}
	return true
}

// definedTrue marks a constant under a defined test: it evaluates to true.
type definedTrue struct{ v any }

func changeIgnoreStrictCheck(n *GetAttrExpr) {
	n.optimizable = false
	n.ignoreStrictCheck = true
	if inner, ok := n.Node.(*GetAttrExpr); ok {
		changeIgnoreStrictCheck(inner)
	}
}

func (p *Parser) parseFilter(node Expr) Expr {
	s := p.stream
	tok := s.expect(NameType, "", "")
	var args []Arg
	if s.test(OperatorType, "(") {
		args = p.parseNamedArguments(true)
	}
	f := p.getFilter(tok.Value.(string), tok.Line)
	return &FilterExpr{pos: pos{tok.Line}, Node: node, Name: f.name, Args: args, def: f}
}

func (p *Parser) parseFunctionCall(left Expr, tok *Token) Expr {
	line := tok.Line
	n, ok := left.(*NameExpr)
	if !ok {
		panic(newSyntaxError("Function name must be an identifier.", line, p.stream.source))
	}
	if alias := p.getImportedSymbol("function", n.Name); alias != nil {
		args := p.parseNamedArguments(false)
		return &MacroRefExpr{pos: pos{line}, Var: alias.ref, Name: alias.name, Args: args, hasParens: true}
	}
	args := p.parseNamedArguments(false)
	f := p.getFunction(n.Name, line)
	switch f.name {
	case "parent":
		block := p.peekBlockStack()
		if block == "" {
			panic(newSyntaxError(`Calling the "parent" function outside of a block is forbidden.`, line, p.stream.source))
		}
		if !p.hasInheritance() {
			panic(newSyntaxError(`Calling the "parent" function on a template that does not call "extends" or "use" is forbidden.`, line, p.stream.source))
		}
		return &ParentExpr{pos: pos{line}, Block: block}
	case "block":
		bound := bindArgs(blockFunctionDef, args, line, p.stream.source)
		b := &BlockRefExpr{pos: pos{line}, Name: bound[0]}
		if len(bound) > 1 {
			b.Template = bound[1]
		}
		return b
	case "attribute":
		bound := bindArgs(attributeFunctionDef, args, line, p.stream.source)
		g := &GetAttrExpr{pos: pos{line}, Node: bound[0], Attr: bound[1], Type: anyCall, optimizable: true}
		if len(bound) > 2 {
			g.Args = []Arg{{Value: &spreadArgs{bound[2]}}}
		}
		return g
	}
	return &FunctionExpr{pos: pos{line}, Name: f.name, Args: args, def: f}
}

// spreadArgs passes a computed arguments array to attribute().
type spreadArgs struct{ Expr }

func (p *Parser) parseDot(expr Expr, opTok *Token) Expr {
	s := p.stream
	nullSafe := opTok.Value == "?."
	tok := s.cur()
	lineno := tok.Line
	typ := anyCall
	var attr Expr
	if s.nextIf(OperatorType, "(") != nil {
		attr = p.parseExpression(0)
		s.expect(PunctuationType, ")", "")
	} else {
		tok = s.next()
		if tok.Type == NameType || tok.Type == NumberType || tok.Type == OperatorType && startsWithName(tok.Str()) {
			attr = &ConstExpr{pos: pos{tok.Line}, Value: tok.Value}
		} else {
			panic(newSyntaxError(sprintf(`Expected name or number, got value "%s" of type "%s".`, tok.Str(), tok.Type.English()), tok.Line, s.source))
		}
	}
	n, isName := expr.(*NameExpr)
	isMacroTarget := isName && (p.getImportedSymbol("template", n.Name) != nil || n.Name == "_self")
	var args []Arg
	if s.test(OperatorType, "(") {
		typ = methodCall
		args = p.parseNamedArguments(true)
	}
	if isMacroTarget {
		m := &MacroRefExpr{pos: pos{expr.Line()}, Var: n.Name, Args: args, hasParens: typ == methodCall}
		if c, ok := attr.(*ConstExpr); ok {
			m.Name = toStr(c.Value)
		} else {
			m.NameExpr = attr
		}
		return m
	}
	return &GetAttrExpr{pos: pos{lineno}, Node: expr, Attr: attr, Args: args, Type: typ, NullSafe: nullSafe, optimizable: !nullSafe}
}

func (p *Parser) parseSubscript(expr Expr, tok *Token) Expr {
	s := p.stream
	line := tok.Line
	slice := false
	var attr Expr
	if s.test(PunctuationType, ":") {
		slice = true
		attr = &ConstExpr{pos: pos{line}, Value: int64(0)}
	} else {
		attr = p.parseExpression(0)
	}
	if s.nextIf(PunctuationType, ":") != nil {
		slice = true
	}
	if slice {
		var length Expr
		if s.test(PunctuationType, "]") {
			length = &ConstExpr{pos: pos{line}, Value: nil}
		} else {
			length = p.parseExpression(0)
		}
		f := p.getFilter("slice", line)
		s.expect(PunctuationType, "]", "")
		return &FilterExpr{pos: pos{line}, Node: expr, Name: f.name, Args: []Arg{{Value: attr}, {Value: length}}, def: f}
	}
	s.expect(PunctuationType, "]", "")
	return &GetAttrExpr{pos: pos{line}, Node: expr, Attr: attr, Type: arrayCall, optimizable: true}
}

// parseAssignmentExpression is AbstractTokenParser::parseAssignmentExpression.
func (p *Parser) parseAssignmentExpression() []string {
	s := p.stream
	var targets []string
	for {
		tok := s.cur()
		if s.test(OperatorType) && startsWithName(tok.Str()) {
			s.next()
		} else {
			s.expect(NameType, "", "Only variables can be assigned to")
		}
		name := tok.Str()
		checkAssignable(name, tok.Line)
		targets = append(targets, name)
		if s.nextIf(PunctuationType, ",") == nil {
			break
		}
	}
	return targets
}

// checkAssignable is AssignContextVariable's check of reserved names.
func checkAssignable(name string, line int) {
	switch strings.ToLower(name) {
	case "true", "false", "none", "null":
		panic(newSyntaxError(sprintf(`You cannot assign a value to "%s".`, name), line, nil))
	}
}
