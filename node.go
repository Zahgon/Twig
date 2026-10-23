// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

// Expr is an expression node.
type Expr interface {
	Line() int
}

// Node is a statement node.
type Node interface {
	Line() int
}

type pos struct{ line int }

func (p pos) Line() int { return p.line }

// ConstExpr is a constant (ConstantExpression).
type ConstExpr struct {
	pos
	Value    any
	implicit bool // a macro argument without a default value
}

// NameExpr reads a context variable (ContextVariable).
type NameExpr struct {
	pos
	Name              string
	definedTest       bool
	ignoreStrictCheck bool
	alwaysDefined     bool
	parens            bool
}

// ArrayElem is one element of an array literal; Key is nil for an implicit key.
type ArrayElem struct {
	Key   Expr
	Value Expr
}

// ArrayExpr is a sequence or mapping literal (ArrayExpression).
type ArrayExpr struct {
	pos
	Elems []ArrayElem
	// definedTest marks a literal under a defined test: it evaluates to true there.
	definedTest bool
}

// isSequence is ArrayExpression::isSequence: every key is implicit or the next integer.
func (a *ArrayExpr) isSequence() bool {
	for i, e := range a.Elems {
		if e.Key == nil {
			continue
		}
		c, ok := e.Key.(*ConstExpr)
		if !ok {
			return false
		}
		n, ok := c.Value.(int64)
		if !ok || n != int64(i) {
			return false
		}
	}
	return true
}

// EmptyExpr is a skipped element in a sequence destructuring.
type EmptyExpr struct{ pos }

// UnaryExpr is not, -, + or the spread operator.
type UnaryExpr struct {
	pos
	Op     string
	X      Expr
	parens bool
}

// BinaryExpr is a binary operator.
type BinaryExpr struct {
	pos
	Op     string
	L, R   Expr
	parens bool
	// testL is the left operand of ?? and ?: as parsed: the escaper may wrap L, the tests use the original.
	testL Expr
}

// TernaryExpr is cond ? then : else.
type TernaryExpr struct {
	pos
	Cond, Then, Else Expr
	parens           bool
}

// Arg is one call argument; Name is "" for a positional argument.
type Arg struct {
	Name  string
	Value Expr
}

// Call types of GetAttrExpr.
const (
	anyCall    = "any"
	arrayCall  = "array"
	methodCall = "method"
)

// GetAttrExpr is a.b, a.b(args) or a[b].
type GetAttrExpr struct {
	pos
	Node              Expr
	Attr              Expr
	Args              []Arg
	Type              string
	NullSafe          bool
	definedTest       bool
	ignoreStrictCheck bool
	optimizable       bool
	parens            bool
}

// FilterExpr is a filter call; Args is nil without parentheses.
type FilterExpr struct {
	pos
	Node   Expr
	Name   string
	Args   []Arg
	def    *callable
	bound  []Expr
	parens bool
	// escaper bookkeeping: an escape filter added by auto-escaping
	autoEscape bool
}

// FunctionExpr is a function call.
type FunctionExpr struct {
	pos
	Name   string
	Args   []Arg
	def    *callable
	bound  []Expr
	parens bool
}

// TestExpr is "node is test(args)".
type TestExpr struct {
	pos
	Node   Expr
	Name   string
	Args   []Arg
	def    *callable
	bound  []Expr
	parens bool
}

// MacroRefExpr calls a macro of an imported template (alias.name(args) or an imported function).
type MacroRefExpr struct {
	pos
	Var         string // the macro variable ("_self" or an alias)
	Name        string
	NameExpr    Expr // a computed macro name (alias.(expr))
	Args        []Arg
	hasParens   bool
	definedTest bool
	parens      bool
}

// ArrowExpr is an arrow function.
type ArrowExpr struct {
	pos
	Params []string
	Body   Expr
	parens bool
}

// ListExpr is a parenthesised list of names before "=>".
type ListExpr struct {
	pos
	Names []string
}

// AssignExpr is the "=" operator; Target is a NameExpr or an ArrayExpr (destructuring).
type AssignExpr struct {
	pos
	Target Expr
	Value  Expr
	parens bool
}

// BlockRefExpr is block(name[, template]).
type BlockRefExpr struct {
	pos
	Name        Expr
	Template    Expr
	definedTest bool
	parens      bool
}

// ParentExpr is parent().
type ParentExpr struct {
	pos
	Block  string
	parens bool
}

// TextNode is raw template text.
type TextNode struct {
	pos
	Data string
}

// PrintNode prints an expression.
type PrintNode struct {
	pos
	X Expr
}

// Nodes is a sequence of statements.
type Nodes struct {
	pos
	List []Node
}

// IfNode is if/elseif/else.
type IfNode struct {
	pos
	Tests  []Expr
	Bodies []Node
	Else   Node
}

// ForNode is a for loop.
type ForNode struct {
	pos
	KeyTarget, ValueTarget string
	Seq                    Expr
	Body                   Node
	Else                   Node
	elseLine               int
	withLoop               bool
}

// SetNode is set (with an expression or capture).
type SetNode struct {
	pos
	Names   []string
	Values  []Expr
	Capture Node
	capture bool
}

// BlockRefNode renders a block where it is defined.
type BlockRefNode struct {
	pos
	Name string
}

// IncludeNode is the include tag.
type IncludeNode struct {
	pos
	Template      Expr
	Variables     Expr
	Only          bool
	IgnoreMissing bool
}

// EmbedNode is the embed tag; Module is the embedded template.
type EmbedNode struct {
	pos
	Module        *Module
	Variables     Expr
	Only          bool
	IgnoreMissing bool
}

// WithNode is the with tag.
type WithNode struct {
	pos
	Body      Node
	Variables Expr
	Only      bool
}

// ImportNode imports macros; Var is the macro variable it assigns.
type ImportNode struct {
	pos
	Template Expr
	Var      string
	Global   bool
}

// AutoEscapeNode changes the escaping strategy of its body (false disables it).
type AutoEscapeNode struct {
	pos
	Strategy any
	Body     Node
}

// DoNode evaluates an expression.
type DoNode struct {
	pos
	X Expr
}

// DeprecatedNode is the deprecated tag (the message is evaluated; the notice is not reported).
type DeprecatedNode struct {
	pos
	X, Package, Version Expr
}

// EmptyNode prints nothing (extends, use, flush, macro declarations, types).
type EmptyNode struct{ pos }

// Block is a template block.
type Block struct {
	Name string
	Body Node
	Line int
	// strategy is the escaping strategy in effect for the block (computed by the escaper pass)
}

// Macro is a template macro.
type Macro struct {
	Name     string
	Params   []string
	Defaults []Expr
	Variadic string
	Body     Node
	Line     int
}

// Trait is a use tag.
type Trait struct {
	Template Expr
	Targets  []TraitTarget
	Line     int
}

// TraitTarget renames a block of a used template.
type TraitTarget struct{ Name, Alias string }

// Module is a parsed template (ModuleNode).
type Module struct {
	Source   *Source
	Body     Node
	Parent   Expr
	Blocks   map[string]*Block
	order    []string
	Macros   map[string]*Macro
	macroSeq []string
	Traits   []*Trait
	Embedded []*Module
	Index    int // embedded template index (0 for a top-level template)
	strategy any
	dbg      map[any]int
}
