// This file is part of the Go port of Twig.
//
// (c) Fabien Potencier
//
// For the full copyright and license information, please view the LICENSE
// file that was distributed with this source code.

package twig

import (
	"sort"
	"strconv"
	"strings"
)

// Lexer states (Twig\Lexer::STATE_*).
const (
	stateData = iota
	stateBlock
	stateVar
	stateString
	stateInterpolation
)

// isSpace is PCRE's \s: space, \t, \n, \v, \f and \r.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

// isLineChar is the lexer's whitespace_line_chars class: space, tab, NUL and vertical tab.
func isLineChar(c byte) bool { return c == ' ' || c == '\t' || c == 0 || c == '\v' }

// phpTrimChars are the characters trim(), rtrim() and ltrim() remove by default.
const phpTrimChars = " \t\n\r\x00\x0B"

func isNameStart(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c >= 0x7f
}

func isNameChar(c byte) bool { return isNameStart(c) || c >= '0' && c <= '9' }

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isAlpha(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// matchName is Lexer::REGEX_NAME anchored at s[0]; it returns the match length.
func matchName(s string) int {
	if s == "" || !isNameStart(s[0]) {
		return 0
	}
	i := 1
	for i < len(s) && isNameChar(s[i]) {
		i++
	}
	return i
}

// isName reports whether the whole of s is a name.
func isName(s string) bool { return s != "" && matchName(s) == len(s) }

// startsWithName is preg_match(REGEX_NAME, s) with the /A anchor: s starts with a name.
func startsWithName(s string) bool { return matchName(s) > 0 }

func skipSpaces(s string, i int) int {
	for i < len(s) && isSpace(s[i]) {
		i++
	}
	return i
}

func matchDigits(s string, i int) int {
	// LNUM: [0-9]+(_[0-9]+)*
	if i >= len(s) || !isDigit(s[i]) {
		return -1
	}
	for i < len(s) && isDigit(s[i]) {
		i++
	}
	for i+1 < len(s) && s[i] == '_' && isDigit(s[i+1]) {
		i++
		for i < len(s) && isDigit(s[i]) {
			i++
		}
	}
	return i
}

// matchNumber is Lexer::REGEX_NUMBER anchored at s[0].
func matchNumber(s string) int {
	i := matchDigits(s, 0)
	if i < 0 {
		return 0
	}
	if i < len(s) && s[i] == '.' {
		if j := matchDigits(s, i+1); j > 0 {
			i = j
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if k := matchDigits(s, j); k > 0 {
			i = k
		}
	}
	return i
}

// operator is one entry of the lexer's operator regular expression.
type operator struct {
	words      []string // the operator split at its spaces ("not in" -> "not", "in")
	alphaStart bool
	alphaEnd   bool
}

// operatorTokens are the operator tokens of the core expression parsers in registration order
// (ExpressionParsers::getOperatorTokensFor for every parser of CoreExtension::getExpressionParsers).
var operatorTokens = []string{
	"not", "...", "-", "+", "?:", "? :", "??", "or", "xor", "and", "b-or", "b-xor", "b-and", "==", "!=", "<=>", "<",
	">", ">=", "<=", "not in", "in", "matches", "starts with", "ends with", "has some", "has every", "===", "!==", "..",
	"+", "-", "~", "*", "/", "//", "%", "**", "?", "=", "is", "is not", "|", "(", ".", "?.", "[", "(", "=>",
}

// operators are sorted longest first, as Lexer::getOperatorRegex does with arsort (stable for equal lengths).
var operators = func() []operator {
	seen := map[string]bool{}
	var names []string
	for _, op := range operatorTokens {
		if !seen[op] {
			seen[op] = true
			names = append(names, op)
		}
	}
	sort.SliceStable(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	out := make([]operator, len(names))
	for i, n := range names {
		out[i] = operator{words: strings.Split(n, " "), alphaStart: isAlpha(n[0]), alphaEnd: isAlpha(n[len(n)-1])}
	}
	return out
}()

// matchOperator tries the operator regex at code[pos:]; it returns the matched length and the normalised operator.
func matchOperator(code string, pos int) (int, string) {
	for _, op := range operators {
		if op.alphaStart {
			// (?<![\.\|]\s|.[\.\|]): not right after "." or "|", nor after one of them and a space.
			if pos >= 2 && ((code[pos-2] == '.' || code[pos-2] == '|') && isSpace(code[pos-1]) ||
				code[pos-1] == '.' || code[pos-1] == '|') {
				continue
			}
		}
		i := pos
		ok := true
		for w, word := range op.words {
			if w > 0 {
				j := skipSpaces(code, i)
				if j == i {
					ok = false
					break
				}
				i = j
			}
			if !strings.HasPrefix(code[i:], word) {
				ok = false
				break
			}
			i += len(word)
		}
		if !ok {
			continue
		}
		if op.alphaEnd {
			// (?=[\s()\[{])
			if i >= len(code) || !(isSpace(code[i]) || strings.IndexByte("()[{", code[i]) >= 0) {
				continue
			}
		}
		return i - pos, strings.Join(op.words, " ")
	}
	return 0, ""
}

type bracket struct {
	char string
	line int
}

// tokenStart is one match of the lex_tokens_start regular expression.
type tokenStart struct {
	pos    int
	length int
	kind   string // "{{", "{%", "{##" or "{#"
	trim   byte   // '-', '~' or 0
}

type lexer struct {
	source   *Source
	code     string
	cursor   int
	lineno   int
	end      int
	tokens   []*Token
	state    int
	states   []int
	brackets []bracket
	position int
	starts   []tokenStart
	varLine  int
}

// Tokenize is Lexer::tokenize.
func Tokenize(src *Source) (*TokenStream, error) {
	lx := &lexer{source: src, code: src.Code, lineno: 1, end: len(src.Code), position: -1}
	lx.findStarts()
	for lx.cursor < lx.end {
		var err error
		switch lx.state {
		case stateData:
			err = lx.lexData()
		case stateBlock:
			err = lx.lexBlock()
		case stateVar:
			err = lx.lexVar()
		case stateString:
			err = lx.lexString()
		case stateInterpolation:
			err = lx.lexInterpolation()
		}
		if err != nil {
			return nil, err
		}
	}
	lx.push(EOFType, "", lx.cursor, lx.lineno)
	if len(lx.brackets) > 0 {
		b := lx.brackets[len(lx.brackets)-1]
		return nil, newSyntaxError(sprintf(`Unclosed "%s".`, b.char), b.line, src)
	}
	return &TokenStream{tokens: lx.tokens, source: src}, nil
}

// findStarts is preg_match_all of lex_tokens_start over the whole template.
func (lx *lexer) findStarts() {
	code := lx.code
	for i := 0; i+1 < len(code); {
		if code[i] != '{' {
			i++
			continue
		}
		var kind string
		switch code[i+1] {
		case '{':
			kind = "{{"
		case '%':
			kind = "{%"
		case '#':
			kind = "{#"
			// {## starts a documentation comment, except for the empty comment {##}.
			if i+2 < len(code) && code[i+2] == '#' && !(i+3 < len(code) && code[i+3] == '}') {
				kind = "{##"
			}
		default:
			i++
			continue
		}
		n := len(kind)
		var trim byte
		if i+n < len(code) && (code[i+n] == '-' || code[i+n] == '~') {
			trim = code[i+n]
			n++
		}
		lx.starts = append(lx.starts, tokenStart{pos: i, length: n, kind: kind, trim: trim})
		i += n
	}
}

func (lx *lexer) push(typ TokenType, value any, offset, line int) {
	if typ == TextType && value == "" {
		return
	}
	lx.tokens = append(lx.tokens, &Token{Type: typ, Value: value, Line: line, Offset: offset})
}

func (lx *lexer) pushHere(typ TokenType, value any) { lx.push(typ, value, lx.cursor, lx.lineno) }

func normalizeNewlines(s string) string {
	if !strings.Contains(s, "\r") {
		return s
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func (lx *lexer) moveCursor(text string) {
	lx.cursor += len(text)
	lx.lineno += strings.Count(normalizeNewlines(text), "\n")
}

func (lx *lexer) pushState(s int) {
	lx.states = append(lx.states, lx.state)
	lx.state = s
}

func (lx *lexer) popState() {
	lx.state = lx.states[len(lx.states)-1]
	lx.states = lx.states[:len(lx.states)-1]
}

func (lx *lexer) lexData() error {
	if lx.position == len(lx.starts)-1 {
		text := lx.code[lx.cursor:]
		lx.pushHere(TextType, normalizeNewlines(text))
		lx.moveCursor(text)
		return nil
	}
	lx.position++
	start := lx.starts[lx.position]
	for start.pos < lx.cursor {
		if lx.position == len(lx.starts)-1 {
			return nil
		}
		lx.position++
		start = lx.starts[lx.position]
	}
	textContent := lx.code[lx.cursor:start.pos]
	text := textContent
	switch start.trim {
	case '-':
		text = strings.TrimRight(text, phpTrimChars)
	case '~':
		text = strings.TrimRight(text, " \t\x00\x0B")
	}
	lx.pushHere(TextType, normalizeNewlines(text))
	lx.moveCursor(textContent)
	whole := lx.code[start.pos : start.pos+start.length]
	switch start.kind {
	case "{##":
		lx.moveCursor(whole)
		return lx.lexComment(true)
	case "{#":
		lx.moveCursor(whole)
		return lx.lexComment(false)
	case "{%":
		lineno, cursor := lx.lineno, lx.cursor
		lx.moveCursor(whole)
		if n := lx.matchBlockRaw(); n > 0 {
			lx.moveCursor(lx.code[lx.cursor : lx.cursor+n])
			return lx.lexRawData()
		} else if n, line := lx.matchBlockLine(); n > 0 {
			lx.moveCursor(lx.code[lx.cursor : lx.cursor+n])
			lx.lineno = line
		} else {
			lx.push(BlockStartType, "", cursor, lineno)
			lx.pushState(stateBlock)
			lx.varLine = lx.lineno
		}
	case "{{":
		lineno, cursor := lx.lineno, lx.cursor
		lx.moveCursor(whole)
		lx.push(VarStartType, "", cursor, lineno)
		lx.pushState(stateVar)
		lx.varLine = lx.lineno
	}
	return nil
}

// matchCloser matches one of "-X\s*" + extra, "~X[ \t\0\x0B]*" or "X" + tail at s[i:]; X is the closing tag.
// For "-X" the optional newline after the spaces is consumed by the \s* already (it is greedy), so only "%}" and "#}"
// take their own optional newline (tail).
func matchCloser(s string, i int, closer string, plainNewline bool) int {
	switch {
	case strings.HasPrefix(s[i:], "-"+closer):
		j := skipSpaces(s, i+1+len(closer))
		return j
	case strings.HasPrefix(s[i:], "~"+closer):
		j := i + 1 + len(closer)
		for j < len(s) && isLineChar(s[j]) {
			j++
		}
		return j
	case strings.HasPrefix(s[i:], closer):
		j := i + len(closer)
		if plainNewline {
			if strings.HasPrefix(s[j:], "\r\n") {
				j += 2
			} else if j < len(s) && (s[j] == '\r' || s[j] == '\n') {
				j++
			}
		}
		return j
	}
	return -1
}

// matchBlockRaw is lex_block_raw: \s*verbatim\s*(-%}\s*|~%}[ \t\0\x0B]*|%}) at the cursor.
func (lx *lexer) matchBlockRaw() int {
	i := skipSpaces(lx.code, lx.cursor)
	if !strings.HasPrefix(lx.code[i:], "verbatim") {
		return 0
	}
	i = skipSpaces(lx.code, i+len("verbatim"))
	j := matchCloser(lx.code, i, "%}", false)
	if j < 0 {
		return 0
	}
	return j - lx.cursor
}

// matchBlockLine is lex_block_line: \s*line\s+(\d+)\s*%} at the cursor.
func (lx *lexer) matchBlockLine() (int, int) {
	i := skipSpaces(lx.code, lx.cursor)
	if !strings.HasPrefix(lx.code[i:], "line") {
		return 0, 0
	}
	i += 4
	j := skipSpaces(lx.code, i)
	if j == i {
		return 0, 0
	}
	k := j
	for k < len(lx.code) && isDigit(lx.code[k]) {
		k++
	}
	if k == j {
		return 0, 0
	}
	line, _ := strconv.Atoi(lx.code[j:k])
	k = skipSpaces(lx.code, k)
	if !strings.HasPrefix(lx.code[k:], "%}") {
		return 0, 0
	}
	return k + 2 - lx.cursor, line
}

// matchEnd matches the closing of a block or variable tag at the cursor (lex_block / lex_var).
func (lx *lexer) matchEnd(closer string) int {
	i := skipSpaces(lx.code, lx.cursor)
	var j int
	if closer == "%}" {
		switch {
		case strings.HasPrefix(lx.code[i:], "-%}"):
			// -%}\s*\n? : the greedy \s* already took any newline
			j = skipSpaces(lx.code, i+3)
		default:
			j = matchCloser(lx.code, i, closer, true)
		}
	} else {
		j = matchCloser(lx.code, i, closer, false)
	}
	if j < 0 {
		return -1
	}
	return j - lx.cursor
}

func (lx *lexer) lexBlock() error {
	if len(lx.brackets) == 0 {
		if n := lx.matchEnd("%}"); n >= 0 {
			lx.pushClosing(BlockEndType, lx.code[lx.cursor:lx.cursor+n])
			lx.popState()
			return nil
		}
	}
	return lx.lexExpression()
}

func (lx *lexer) lexVar() error {
	if len(lx.brackets) == 0 {
		if n := lx.matchEnd("}}"); n >= 0 {
			lx.pushClosing(VarEndType, lx.code[lx.cursor:lx.cursor+n])
			lx.popState()
			return nil
		}
	}
	return lx.lexExpression()
}

func (lx *lexer) pushClosing(typ TokenType, match string) {
	lead := len(match) - len(strings.TrimLeft(match, phpTrimChars))
	if lead > 0 {
		lx.moveCursor(match[:lead])
	}
	lx.pushHere(typ, "")
	lx.moveCursor(match[lead:])
}

func (lx *lexer) column(offset int) int { return lx.source.Column(offset) }

func (lx *lexer) lexExpression() error {
	code := lx.code
	if i := skipSpaces(code, lx.cursor); i > lx.cursor {
		lx.moveCursor(code[lx.cursor:i])
		if lx.cursor >= lx.end {
			what := "variable"
			if lx.state == stateBlock {
				what = "block"
			}
			return newSyntaxError(sprintf(`Unclosed "%s".`, what), lx.varLine, lx.source)
		}
	}
	if n, op := matchOperator(code, lx.cursor); n > 0 {
		if op == "(" || op == "[" || op == "{" {
			if err := lx.checkBrackets(op); err != nil {
				return err
			}
		}
		lx.pushHere(OperatorType, op)
		lx.moveCursor(code[lx.cursor : lx.cursor+n])
		return nil
	}
	if n := matchName(code[lx.cursor:]); n > 0 {
		name := code[lx.cursor : lx.cursor+n]
		lx.pushHere(NameType, name)
		lx.moveCursor(name)
		return nil
	}
	if n := matchNumber(code[lx.cursor:]); n > 0 {
		text := code[lx.cursor : lx.cursor+n]
		lx.pushHere(NumberType, numericLiteral(strings.ReplaceAll(text, "_", "")))
		lx.moveCursor(text)
		return nil
	}
	c := code[lx.cursor]
	if strings.IndexByte("()[]{}?:.,|", c) >= 0 {
		if err := lx.checkBrackets(string(c)); err != nil {
			return err
		}
		lx.pushHere(PunctuationType, string(c))
		lx.moveCursor(string(c))
		return nil
	}
	if n := matchQuoted(code, lx.cursor); n > 0 {
		raw := code[lx.cursor : lx.cursor+n]
		lx.pushHere(StringType, lx.stripcslashes(normalizeNewlines(raw[1:len(raw)-1]), raw[0]))
		lx.moveCursor(raw)
		return nil
	}
	if c == '"' {
		lx.brackets = append(lx.brackets, bracket{`"`, lx.lineno})
		lx.pushState(stateString)
		lx.moveCursor(`"`)
		return nil
	}
	if c == '#' {
		j := lx.cursor
		for j < len(code) && code[j] != '\r' && code[j] != '\n' {
			j++
		}
		lx.moveCursor(code[lx.cursor:j])
		return nil
	}
	return newSyntaxErrorAt(sprintf(`Unexpected character "%s".`, string(c)), lx.lineno, lx.source, lx.column(lx.cursor))
}

// matchQuoted is REGEX_STRING: a double-quoted string without "#", or a single-quoted string, with backslash
// escapes; it returns the length including the quotes, or 0.
func matchQuoted(code string, pos int) int {
	q := code[pos]
	if q != '"' && q != '\'' {
		return 0
	}
	for i := pos + 1; i < len(code); i++ {
		switch c := code[i]; {
		case c == '\\':
			i++
		case c == q:
			return i + 1 - pos
		case q == '"' && c == '#':
			return 0
		}
	}
	return 0
}

// numericLiteral converts a number literal the way PHP's `0 + "literal"` does: an int when it is an integer that
// fits, a float otherwise.
func numericLiteral(s string) any {
	if !strings.ContainsAny(s, ".eE") {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n
		}
	}
	f, _ := strconv.ParseFloat(s, 64)
	return f
}

func isXDigit(c byte) bool { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// stripcslashes is Lexer::stripcslashes: C-like escapes, where an unknown escape keeps the character only.
func (lx *lexer) stripcslashes(str string, quote byte) string {
	var b strings.Builder
	n := len(str)
	for i := 0; i < n; {
		pos := strings.IndexByte(str[i:], '\\')
		if pos < 0 {
			b.WriteString(str[i:])
			break
		}
		b.WriteString(str[i : i+pos])
		i += pos + 1
		if i >= n {
			b.WriteByte('\\')
			break
		}
		c := str[i]
		switch {
		case c == 'f':
			b.WriteByte('\f')
		case c == 'n':
			b.WriteByte('\n')
		case c == 'r':
			b.WriteByte('\r')
		case c == 't':
			b.WriteByte('\t')
		case c == 'v':
			b.WriteByte('\v')
		case c == '\\', c == '\'', c == '"':
			b.WriteByte(c)
		case c == '#' && i+1 < n && str[i+1] == '{':
			b.WriteString("#{")
			i++
		case c == 'x' && i+1 < n && isXDigit(str[i+1]):
			i++
			hex := str[i : i+1]
			if i+1 < n && isXDigit(str[i+1]) {
				i++
				hex = str[i-1 : i+1]
			}
			v, _ := strconv.ParseUint(hex, 16, 8)
			b.WriteByte(byte(v))
		case c >= '0' && c < '8':
			start := i
			for i+1 < n && str[i+1] >= '0' && str[i+1] < '8' && i+1-start < 3 {
				i++
			}
			v, _ := strconv.ParseUint(str[start:i+1], 8, 16)
			b.WriteByte(byte(v % 256))
		default:
			b.WriteByte(c)
		}
		i++
	}
	return b.String()
}

func (lx *lexer) lexRawData() error {
	// lex_raw_data: {%(-|~)?\s*endverbatim\s*(-%}\s*|~%}[ \t\0\x0B]*|%}) searched from the cursor
	code := lx.code
	for i := lx.cursor; i+1 < len(code); i++ {
		if code[i] != '{' || code[i+1] != '%' {
			continue
		}
		j := i + 2
		var trim byte
		if j < len(code) && (code[j] == '-' || code[j] == '~') {
			trim = code[j]
			j++
		}
		j = skipSpaces(code, j)
		if !strings.HasPrefix(code[j:], "endverbatim") {
			continue
		}
		j = skipSpaces(code, j+len("endverbatim"))
		k := matchCloser(code, j, "%}", false)
		if k < 0 {
			continue
		}
		offset := lx.cursor
		text := code[lx.cursor:i]
		lx.moveCursor(code[lx.cursor:k])
		switch trim {
		case '-':
			text = strings.TrimRight(text, phpTrimChars)
		case '~':
			text = strings.TrimRight(text, " \t\x00\x0B")
		}
		lx.push(TextType, normalizeNewlines(text), offset, lx.lineno)
		return nil
	}
	return newSyntaxError(`Unexpected end of file: Unclosed "verbatim" block.`, lx.lineno, lx.source)
}

func (lx *lexer) lexComment(doc bool) error {
	code := lx.code
	for i := lx.cursor; i < len(code); i++ {
		k := -1
		if doc {
			if k = matchCloserNL(code, i, "##}"); k < 0 {
				k = matchCloserNL(code, i, "#}")
			}
			// only the "-" and "~" forms of ##} exist; a plain "##}" is "#" followed by "#}"
			if k >= 0 && strings.HasPrefix(code[i:], "##}") {
				k = -1
			}
		} else {
			k = matchCloserNL(code, i, "#}")
		}
		if k >= 0 {
			lx.moveCursor(code[lx.cursor:k])
			return nil
		}
	}
	return newSyntaxError("Unclosed comment.", lx.lineno, lx.source)
}

// matchCloserNL matches "-X\s*\n?", "~X[ \t\0\x0B]*" or "X(\r\n?|\n)?" at s[i:].
func matchCloserNL(s string, i int, closer string) int {
	return matchCloser(s, i, closer, true)
}

func (lx *lexer) lexString() error {
	code := lx.code
	if strings.HasPrefix(code[lx.cursor:], "#{") {
		lx.brackets = append(lx.brackets, bracket{"#{", lx.lineno})
		lx.pushHere(InterpolationStartType, "")
		lx.moveCursor(code[lx.cursor:skipSpaces(code, lx.cursor+2)])
		lx.pushState(stateInterpolation)
		return nil
	}
	// REGEX_DQ_STRING_PART: [^#"\\]*(?:(?:\\.|#(?!\{))[^#"\\]*)*
	i := lx.cursor
	for i < len(code) {
		c := code[i]
		if c == '\\' && i+1 < len(code) {
			i += 2
			continue
		}
		if c == '\\' || c == '"' {
			break
		}
		if c == '#' && i+1 < len(code) && code[i+1] == '{' {
			break
		}
		i++
	}
	if i > lx.cursor {
		part := code[lx.cursor:i]
		lx.pushHere(StringType, lx.stripcslashes(normalizeNewlines(part), '"'))
		lx.moveCursor(part)
		return nil
	}
	if code[lx.cursor] == '"' {
		lx.brackets = lx.brackets[:len(lx.brackets)-1]
		lx.popState()
		lx.moveCursor(`"`)
		return nil
	}
	return newSyntaxErrorAt(sprintf(`Unexpected character "%s".`, string(code[lx.cursor])), lx.lineno, lx.source, lx.column(lx.cursor))
}

func (lx *lexer) lexInterpolation() error {
	b := lx.brackets[len(lx.brackets)-1]
	if b.char == "#{" {
		i := skipSpaces(lx.code, lx.cursor)
		if i < len(lx.code) && lx.code[i] == '}' {
			lx.brackets = lx.brackets[:len(lx.brackets)-1]
			lx.pushClosing(InterpolationEndType, lx.code[lx.cursor:i+1])
			lx.popState()
			return nil
		}
	}
	return lx.lexExpression()
}

var openingToClosing = map[string]string{"{": "}", "(": ")", "[": "]"}

func (lx *lexer) checkBrackets(code string) error {
	switch code {
	case "{", "(", "[":
		lx.brackets = append(lx.brackets, bracket{code, lx.lineno})
	case "}", ")", "]":
		if len(lx.brackets) == 0 {
			return newSyntaxErrorAt(sprintf(`Unexpected "%s".`, code), lx.lineno, lx.source, lx.column(lx.cursor))
		}
		b := lx.brackets[len(lx.brackets)-1]
		lx.brackets = lx.brackets[:len(lx.brackets)-1]
		// str_replace of opening by closing brackets: '"' and '#{' stay as they are and never match
		if openingToClosing[b.char] != code {
			return newSyntaxError(sprintf(`Unclosed "%s".`, b.char), b.line, lx.source)
		}
	}
	return nil
}
