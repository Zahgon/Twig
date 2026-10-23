// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

// TokenStream is Twig\TokenStream.
type TokenStream struct {
	tokens  []*Token
	current int
	source  *Source
}

func (s *TokenStream) injectTokens(tokens []*Token) {
	rest := append([]*Token{}, s.tokens[s.current:]...)
	s.tokens = append(append(s.tokens[:s.current:s.current], tokens...), rest...)
}

// next moves to the next token and returns the previous one.
func (s *TokenStream) next() *Token {
	s.current++
	if s.current >= len(s.tokens) {
		panic(newSyntaxError("Unexpected end of template.", s.tokens[s.current-1].Line, s.source))
	}
	return s.tokens[s.current-1]
}

func (s *TokenStream) nextIf(typ TokenType, values ...string) *Token {
	if s.tokens[s.current].Test(typ, values...) {
		return s.next()
	}
	return nil
}

func (s *TokenStream) expect(typ TokenType, value string, message string) *Token {
	tok := s.tokens[s.current]
	var values []string
	if value != "" {
		values = []string{value}
	}
	if !tok.Test(typ, values...) {
		prefix := ""
		if message != "" {
			prefix = message + ". "
		}
		ofValue := ""
		if truthy(tok.Value) {
			ofValue = sprintf(` of value "%s"`, tok.Str())
		}
		withValue := ""
		if value != "" {
			withValue = sprintf(` with value "%s"`, value)
		}
		panic(newSyntaxErrorAt(sprintf(`%sUnexpected token "%s"%s ("%s" expected%s).`, prefix, tok.Type.English(), ofValue,
			typ.English(), withValue), tok.Line, s.source, s.source.Column(tok.Offset)))
	}
	s.next()
	return tok
}

func (s *TokenStream) look(n int) *Token {
	if s.current+n >= len(s.tokens) {
		panic(newSyntaxError("Unexpected end of template.", s.tokens[s.current+n-1].Line, s.source))
	}
	return s.tokens[s.current+n]
}

func (s *TokenStream) test(typ TokenType, values ...string) bool {
	return s.tokens[s.current].Test(typ, values...)
}

func (s *TokenStream) isEOF() bool { return s.tokens[s.current].Type == EOFType }

func (s *TokenStream) cur() *Token { return s.tokens[s.current] }
