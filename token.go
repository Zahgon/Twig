// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

// TokenType is the kind of a lexer token (Twig\Token constants).
type TokenType int

// Token types, numbered as in Twig\Token.
const (
	EOFType TokenType = iota - 1
	TextType
	BlockStartType
	VarStartType
	BlockEndType
	VarEndType
	NameType
	NumberType
	StringType
	OperatorType
	PunctuationType
	InterpolationStartType
	InterpolationEndType
)

// Token is one lexer token. Value is a string, except for number tokens, whose value is an int64 or a float64.
type Token struct {
	Type   TokenType
	Value  any
	Line   int
	Offset int // byte offset in the source, -1 when unknown
}

// Str returns the token value as a string (number tokens are converted as PHP converts numbers).
func (t *Token) Str() string {
	if s, ok := t.Value.(string); ok {
		return s
	}
	return toStr(t.Value)
}

// operatorLike are the operator tokens that also count as punctuation when a parser asks for them (Token::test).
var operatorLike = map[string]bool{"(": true, "[": true, "|": true, ".": true, "?": true, "?:": true}

// Test is Token::test(type, value): a punctuation token also matches an operator test, and an empty value list
// matches any value.
func (t *Token) Test(typ TokenType, values ...string) bool {
	typeMatches := t.Type == typ
	if !typeMatches && typ == OperatorType && t.Type == PunctuationType {
		if len(values) == 0 {
			typeMatches = true
		}
		for _, v := range values {
			if operatorLike[v] {
				typeMatches = true
			}
		}
	}
	if !typeMatches {
		return false
	}
	if len(values) == 0 {
		return true
	}
	s, isStr := t.Value.(string)
	for _, v := range values {
		if isStr && s == v {
			return true
		}
		// A single value is compared loosely in PHP ($this->value == $values); only a number token differs.
		if !isStr && len(values) == 1 && looseEqualsNumberString(t.Value, v) {
			return true
		}
	}
	return false
}

// TestName is Token::test('name') shorthand: a name token with one of the values.
func (t *Token) TestName(values ...string) bool { return t.Test(NameType, values...) }

// English is Token::typeToEnglish.
func (typ TokenType) English() string {
	switch typ {
	case EOFType:
		return "end of template"
	case TextType:
		return "text"
	case BlockStartType:
		return "begin of statement block"
	case VarStartType:
		return "begin of print statement"
	case BlockEndType:
		return "end of statement block"
	case VarEndType:
		return "end of print statement"
	case NameType:
		return "name"
	case NumberType:
		return "number"
	case StringType:
		return "string"
	case OperatorType:
		return "operator"
	case PunctuationType:
		return "punctuation"
	case InterpolationStartType:
		return "begin of string interpolation"
	case InterpolationEndType:
		return "end of string interpolation"
	}
	return "unknown"
}
