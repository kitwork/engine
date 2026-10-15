package highlight

import (
	"strings"
	"unicode/utf8"
)

// tokenizeCSS reads CSS. What a word is depends on where it stands, so the
// reader knows which part of a statement it is in:
//
//   - A selector (a statement that runs into '{'): element names are tags,
//     .class and #id are attributes (they select by an attribute), :hover and
//     ::before are keywords, [href^="x"] is punctuation, attribute, operator,
//     string; combinators > + ~ are operators, * is a tag, & is a keyword.
//     Inside @keyframes, from and to are keywords and 50% a number.
//   - A declaration (a statement that ends at ';' or '}'): the property is a
//     property (a custom one, --brand, is a variable, as it is in var(--brand)).
//     In the value, a name followed by '(' is a function, other names are
//     constants (auto, none, sans-serif, red), numbers keep their unit (1.5rem,
//     150ms, 92%, -4px), #b45309 is a number (see below), url(…) without quotes
//     is a string, and !important is a keyword.
//   - An at-rule: @media and every @name are keywords; in the prelude, and, or,
//     not and only are keywords and a media feature before ':' is a property.
//   - /* comments */, "strings" and 'strings' anywhere.
//
// Whether a statement is a selector or a declaration is decided at its first
// byte by looking ahead to its delimiter, past comments, strings, url(…) and
// parentheses, which handles CSS nesting (a rule inside a rule) and a bare
// declaration pasted on its own (color: red). Each look-ahead starts where the
// last one ended, so the reader stays linear.
//
// A hexadecimal colour is a number: its digits are the channels' values, it is
// scanned like any numeric literal, and constant is kept for colours and other
// values written as words. A renderer can still tell them apart by the '#'.
func tokenizeCSS(scan *scanner) {
	reader := cssReader{scan: scan, statementStart: true, keyframesDepth: -1, decidedUntil: -1}
	reader.read()
}

// cssPart is the part of a statement the reader is in.
type cssPart uint8

const (
	cssSelector    cssPart = iota // up to a rule's '{'
	cssDeclaration                // property: value, up to ';' or '}'
	cssAtRule                     // @name and its prelude
)

// The parts of an attribute selector: [name operator value flags].
const (
	cssOutsideBracket = iota
	cssBracketName
	cssBracketValue
	cssBracketFlags
)

type cssReader struct {
	scan           *scanner
	part           cssPart
	statementStart bool // the next byte that is not blank begins a statement
	afterColon     bool // a declaration's value has begun
	parentheses    int  // '(' open in this statement
	bracket        int  // where in an attribute selector
	depth          int  // '{' open
	keyframesDepth int  // the depth of an @keyframes block, -1 outside one
	keyframes      bool // this statement is @keyframes: its '{' opens a keyframes block
	decidedUntil   int  // the delimiter the last look-ahead stopped at
	decidedBlock   bool // whether that delimiter is '{'
}

func (reader *cssReader) read() {
	scan := reader.scan
	for loop := scan.loop(); loop.next(); {
		source, at := scan.source, scan.position
		character := source[at]
		if isWhitespace(character) {
			scan.emit(Plain, scanWhitespace(source, at))
			continue
		}
		if character == '/' && byteAt(source, at+1) == '*' {
			scan.emit(Comment, scanBlockComment(source, at, "/*", "*/"))
			continue
		}
		if reader.statementStart {
			reader.begin(at)
		}
		switch character {
		case '{':
			scan.emitLength(Punctuation, 1)
			reader.depth++
			if reader.keyframes {
				reader.keyframesDepth = reader.depth
			}
			reader.statementStart = true
		case '}':
			scan.emitLength(Punctuation, 1)
			if reader.depth == reader.keyframesDepth {
				reader.keyframesDepth = -1
			}
			if reader.depth > 0 {
				reader.depth--
			}
			reader.statementStart = true
		case ';':
			scan.emitLength(Punctuation, 1)
			if reader.parentheses == 0 {
				reader.statementStart = true
			}
		default:
			switch {
			case reader.part == cssSelector:
				reader.selector(at)
			case reader.part == cssDeclaration && !reader.afterColon:
				reader.property(at)
			default:
				reader.value(at, reader.part == cssAtRule)
			}
		}
	}
}

// begin starts a statement at its first byte and decides what it is.
func (reader *cssReader) begin(at int) {
	source := reader.scan.source
	reader.statementStart = false
	reader.afterColon, reader.parentheses, reader.bracket, reader.keyframes = false, 0, cssOutsideBracket, false
	if source[at] == '@' {
		reader.part = cssAtRule
		return
	}
	if at > reader.decidedUntil {
		reader.decidedUntil, reader.decidedBlock = cssStatementEnd(source, at)
	}
	reader.part = cssDeclaration
	if reader.decidedBlock {
		reader.part = cssSelector
	}
}

// cssStatementEnd looks from start for the byte that ends the statement there:
// a '{' (it was a selector), a '}' or a ';' outside parentheses (it was a
// declaration), or the end of the source. Comments, strings and an unquoted
// url(…) are stepped over the way the reader reads them.
func cssStatementEnd(source string, start int) (int, bool) {
	parentheses := 0
	for index := start; index < len(source); {
		switch source[index] {
		case '/':
			if byteAt(source, index+1) == '*' {
				index = scanBlockComment(source, index, "/*", "*/")
				continue
			}
		case '"', '\'':
			index = cssStringEnd(source, index)
			continue
		case '\\':
			index += 2
			continue
		case '(':
			parentheses++
			if index >= 3 && strings.EqualFold(source[index-3:index], "url") && !cssNameByte(byteAt(source, index-4)) {
				if end := cssUnquotedAddressEnd(source, index); end >= 0 {
					index = end
					continue
				}
			}
		case ')':
			if parentheses > 0 {
				parentheses--
			}
		case '{':
			return index, true
		case '}':
			return index, false
		case ';':
			if parentheses == 0 {
				return index, false
			}
		}
		index++
	}
	return len(source), false
}

// selector reads one token of a selector.
func (reader *cssReader) selector(at int) {
	scan := reader.scan
	source := scan.source
	character := source[at]
	switch {
	case character == '"' || character == '\'':
		scan.emit(String, cssStringEnd(source, at))
		if reader.bracket == cssBracketValue {
			reader.bracket = cssBracketFlags
		}
	case character == '[':
		scan.emitLength(Punctuation, 1)
		reader.bracket = cssBracketName
	case character == ']':
		scan.emitLength(Punctuation, 1)
		reader.bracket = cssOutsideBracket
	case reader.bracket != cssOutsideBracket && character == '=':
		scan.emitLength(Operator, 1)
		reader.bracket = cssBracketValue
	case reader.bracket != cssOutsideBracket && strings.IndexByte("~|^$*", character) >= 0 && byteAt(source, at+1) == '=':
		scan.emitLength(Operator, 2)
		reader.bracket = cssBracketValue
	case character == '(' || character == ')' || character == ',':
		reader.punctuation(at)
	case character == '.' && cssNameStarts(source, at+1):
		scan.emit(Attribute, cssNameEnd(source, at+1))
	case character == '#' && cssNameContinues(source, at+1):
		scan.emit(Attribute, cssNameEnd(source, at+1))
	case character == ':':
		name := at + 1
		if byteAt(source, name) == ':' {
			name++
		}
		if cssNameStarts(source, name) {
			scan.emit(Keyword, cssNameEnd(source, name))
		} else {
			scan.emit(Punctuation, name)
		}
	case character == '&':
		scan.emitLength(Keyword, 1)
	case character == '*':
		scan.emitLength(Tag, 1)
	case strings.IndexByte(">+~|", character) >= 0:
		scan.emitLength(Operator, 1)
	case cssNumberEnd(source, at) > at:
		scan.emit(Number, cssNumberEnd(source, at))
	case character == '@' && cssNameStarts(source, at+1):
		scan.emit(Keyword, cssNameEnd(source, at+1))
	case cssNameStarts(source, at):
		end := cssNameEnd(source, at)
		scan.emit(reader.selectorWord(source[at:end]), end)
	default:
		scan.emitCharacter(Plain)
	}
}

// selectorWord is the kind of a bare name in a selector.
func (reader *cssReader) selectorWord(word string) Kind {
	switch {
	case reader.bracket == cssBracketName:
		return Attribute
	case reader.bracket == cssBracketValue:
		reader.bracket = cssBracketFlags
		return String
	case reader.bracket == cssBracketFlags:
		return Keyword // the i or s after the value
	case reader.depth == reader.keyframesDepth:
		return Keyword // from, to
	case reader.parentheses > 0 && strings.EqualFold(word, "of"):
		return Keyword // :nth-child(2n of .card)
	case reader.parentheses > 0 && (strings.EqualFold(word, "odd") || strings.EqualFold(word, "even") ||
		strings.EqualFold(word, "n") || strings.EqualFold(word, "-n")):
		return Constant
	}
	return Tag
}

// property reads one token of a declaration before its ':'.
func (reader *cssReader) property(at int) {
	scan := reader.scan
	source := scan.source
	switch {
	case source[at] == ':':
		scan.emitLength(Punctuation, 1)
		reader.afterColon = true
	case cssNameStarts(source, at):
		end := cssNameEnd(source, at)
		kind := Property
		if strings.HasPrefix(source[at:end], "--") {
			kind = Variable
		}
		scan.emit(kind, end)
	default:
		reader.value(at, false)
	}
}

// value reads one token of a declaration's value, or of an at-rule's prelude.
func (reader *cssReader) value(at int, prelude bool) {
	scan := reader.scan
	source := scan.source
	character := source[at]
	switch {
	case character == '"' || character == '\'':
		scan.emit(String, cssStringEnd(source, at))
	case character == '(' || character == ')' || character == ',':
		reader.punctuation(at)
	case character == ':' || character == '[' || character == ']':
		scan.emitLength(Punctuation, 1)
	case character == '@' && cssNameStarts(source, at+1):
		end := cssNameEnd(source, at+1)
		scan.emit(Keyword, end)
		if name := source[at+1 : end]; prelude && len(name) >= 9 && strings.EqualFold(name[len(name)-9:], "keyframes") {
			reader.keyframes = true
		}
	case character == '!':
		reader.important(at)
	case character == '#' && cssNameContinues(source, at+1):
		end := cssNameEnd(source, at+1)
		kind := Number
		for index := at + 1; index < end; index++ {
			if !isHexadecimalDigit(source[index]) {
				kind = Constant
				break
			}
		}
		scan.emit(kind, end)
	case cssUnicodeRangeEnd(source, at) > at:
		scan.emit(Number, cssUnicodeRangeEnd(source, at))
	case cssNumberEnd(source, at) > at:
		scan.emit(Number, cssNumberEnd(source, at))
	case cssNameStarts(source, at):
		reader.valueWord(at, prelude)
	case strings.IndexByte("/*+-=<>", character) >= 0:
		scan.emitLength(Operator, 1)
	default:
		scan.emitCharacter(Plain)
	}
}

// valueWord reads a name in a value or a prelude.
func (reader *cssReader) valueWord(at int, prelude bool) {
	scan := reader.scan
	source := scan.source
	end := cssNameEnd(source, at)
	word := source[at:end]
	switch {
	case byteAt(source, end) == '(' && end < len(source):
		scan.emit(Function, end)
		if strings.EqualFold(word, "url") {
			reader.address(end)
		}
	case strings.HasPrefix(word, "--"):
		scan.emit(Variable, end)
	case prelude && (strings.EqualFold(word, "and") || strings.EqualFold(word, "or") ||
		strings.EqualFold(word, "not") || strings.EqualFold(word, "only")):
		scan.emit(Keyword, end)
	case prelude && reader.parentheses > 0 && cssFeatureFollows(source, end):
		scan.emit(Property, end) // (min-width: 640px), (width >= 640px)
	default:
		scan.emit(Constant, end)
	}
}

// cssFeatureFollows reports whether the next byte after any whitespace from end
// is ':' or a comparison, which makes the name before it a media feature.
func cssFeatureFollows(source string, end int) bool {
	next := scanWhitespace(source, end)
	return next < len(source) && strings.IndexByte(":<>=", source[next]) >= 0
}

// address reads the '(' of url( and, when the address is not quoted, the
// address itself as one string: url(data:image/svg+xml;base64,…) holds ';' and
// ',' that are not CSS. A quoted address is left to the reader.
func (reader *cssReader) address(open int) {
	end := cssUnquotedAddressEnd(reader.scan.source, open)
	if end < 0 {
		return
	}
	scan := reader.scan
	scan.emitLength(Punctuation, 1)
	reader.parentheses++
	scan.emit(Plain, scanWhitespace(scan.source, scan.position))
	scan.emit(String, end)
}

// cssUnquotedAddressEnd is, for the '(' of url( at open, where an unquoted
// address ends: before the ')' or line break that ends it, or at the end of the
// source. It is -1 when the address is quoted.
func cssUnquotedAddressEnd(source string, open int) int {
	start := scanWhitespace(source, open+1)
	if quote := byteAt(source, start); start < len(source) && (quote == '"' || quote == '\'') {
		return -1
	}
	index := start
	for index < len(source) {
		character := source[index]
		if character == ')' || character == '\n' || character == '\r' || character == '\f' {
			break
		}
		if character == '\\' && cssEscapes(source, index) {
			index += 2
			continue
		}
		index++
	}
	return index
}

// important reads at '!': !important (or ! important) is a keyword, any other
// '!' an operator.
func (reader *cssReader) important(at int) {
	scan := reader.scan
	source := scan.source
	name := scanWhitespace(source, at+1)
	if cssNameStarts(source, name) {
		if end := cssNameEnd(source, name); strings.EqualFold(source[name:end], "important") {
			scan.emitLength(Keyword, 1)
			scan.emit(Plain, name)
			scan.emit(Keyword, end)
			return
		}
	}
	scan.emitLength(Operator, 1)
}

// punctuation reads '(', ')' or ',' and keeps count of the parentheses.
func (reader *cssReader) punctuation(at int) {
	switch reader.scan.source[at] {
	case '(':
		reader.parentheses++
	case ')':
		if reader.parentheses > 0 {
			reader.parentheses--
		}
	}
	reader.scan.emitLength(Punctuation, 1)
}

// cssStringEnd is where the string whose quote is at start ends. A CSS string
// takes backslash escapes, an escaped line break continues it, and an
// unterminated one stops before the line break.
func cssStringEnd(source string, start int) int {
	rules := doubleQuoted
	if source[start] == '\'' {
		rules = singleQuoted
	}
	return scanQuoted(source, start+1, rules)
}

// cssNameByte is a byte that continues a CSS name: a letter, a digit, '-', '_',
// or any byte of a non-ASCII character (all of which are name characters in
// CSS, so a name never ends inside a character).
func cssNameByte(character byte) bool {
	return isASCIILetter(character) || isDecimalDigit(character) || character == '-' || character == '_' || character >= utf8.RuneSelf
}

// cssEscapes reports whether the '\' at index escapes the character after it
// (anything but a line break or the end).
func cssEscapes(source string, index int) bool {
	next := byteAt(source, index+1)
	return index+1 < len(source) && next != '\n' && next != '\r' && next != '\f'
}

// cssNameContinues reports whether a name byte or an escape is at index.
func cssNameContinues(source string, index int) bool {
	if index >= len(source) {
		return false
	}
	if source[index] == '\\' {
		return cssEscapes(source, index)
	}
	return cssNameByte(source[index])
}

// cssNameStarts reports whether a name starts at index: a letter, '_', a
// non-ASCII character or an escape, or '-' before one of those or another '-'
// (-webkit-box, --brand). '-' before a digit starts a number instead.
func cssNameStarts(source string, index int) bool {
	if index >= len(source) {
		return false
	}
	character := source[index]
	if character == '-' {
		next := byteAt(source, index+1)
		return index+1 < len(source) && (next == '-' || (cssNameContinues(source, index+1) && !isDecimalDigit(next)))
	}
	return cssNameContinues(source, index) && !isDecimalDigit(character) && character != '-'
}

// cssNameEnd reads name bytes and escapes from start (.hover\:underline is one
// class): the escape takes the byte after its backslash, and the bytes of a
// non-ASCII character are name bytes, so no character is cut.
func cssNameEnd(source string, start int) int {
	index := start
	for index < len(source) {
		switch {
		case cssNameByte(source[index]):
			index++
		case source[index] == '\\' && cssEscapes(source, index):
			index += 2
		default:
			return index
		}
	}
	return index
}

// cssNumberEnd reads a number at start with its unit or '%' (7, 1.5rem, .75,
// 150ms, 92%, 2n), and a sign where a value can begin (-4px, +2): not right
// after a name, a digit, ')' or '%', where '-' and '+' are operators. It returns
// start when no number begins there.
func cssNumberEnd(source string, start int) int {
	digits := start
	if sign := byteAt(source, start); sign == '-' || sign == '+' {
		if before := byteAt(source, start-1); start > 0 && (cssNameByte(before) || before == ')' || before == '%') {
			return start
		}
		digits++
	}
	end := scanNumber(source, digits)
	if end == digits {
		return start
	}
	if byteAt(source, end) == '%' && end < len(source) {
		end++
	}
	return end
}

// cssUnicodeRangeEnd reads U+0000-00FF or U+4?? at start, or returns start.
func cssUnicodeRangeEnd(source string, start int) int {
	if letter := byteAt(source, start); (letter != 'u' && letter != 'U') || byteAt(source, start+1) != '+' {
		return start
	}
	hexadecimalOrWildcard := func(character byte) bool { return isHexadecimalDigit(character) || character == '?' }
	end := scanWhile(source, start+2, hexadecimalOrWildcard)
	if end == start+2 {
		return start
	}
	if byteAt(source, end) == '-' && isHexadecimalDigit(byteAt(source, end+1)) {
		end = scanWhile(source, end+1, isHexadecimalDigit)
	}
	return end
}
