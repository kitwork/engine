package highlight

import "strings"

// tokenizeYAML reads YAML line by line, the way its indentation is meant.
//
//   - A key (a scalar followed by ": " or by ':' at the line's end) is
//     Property, quoted or not: name:, "on":, <<:.
//   - A plain scalar value is typed as the YAML 1.2 core schema types it:
//     true/false/null/~ (and their capitalised forms) Constant; 8107, -2.5e3,
//     0x1F, 0o17, .inf, .nan Number; anything else (ubuntu-latest,
//     2026-10-12) String. Quoted scalars are String.
//   - Block scalars: the | or > indicator (with -, + and a digit) is
//     Punctuation, and the lines indented past the key or - that holds it are
//     String.
//   - &anchor and *alias are Variable; !tag and !!str are Type; # … after a
//     blank or at a line's start is Comment.
//   - Indicators - ? : , [ ] { } are Punctuation, and so are the document
//     markers --- and ...; a directive's name (%YAML) is Keyword.
//
// Flow collections ([a, b], {a: 1}) may span lines; their depth is a counter,
// so nesting costs nothing and nothing recurses.
func tokenizeYAML(scan *scanner) {
	yaml := &yamlTokenizer{scan: scan, lineStart: true}
	for loop := scan.loop(); loop.next(); {
		yaml.step()
	}
}

type yamlTokenizer struct {
	scan *scanner

	lineStart   bool // the position is at the start of a line
	lineOffset  int  // where the current line starts
	nodeIndent  int  // the column of the line's innermost key or - : a block scalar's body is indented past it
	flowDepth   int  // how many [ and { are open
	blockScalar bool // a | or > was read: its body starts on the next line
}

var (
	yamlConstants      = newWordSet("true", "True", "TRUE", "false", "False", "FALSE", "null", "Null", "NULL", "~")
	yamlDoubleQuoted   = quoted{closer: `"`, escape: '\\', multiline: true}
	yamlSingleQuoted   = quoted{closer: "'", doubled: true, multiline: true}
	yamlFlowIndicators = ",[]{}"
)

// step reads one thing: a line's indentation and marker, a line break, blanks,
// a comment, or a token.
func (yaml *yamlTokenizer) step() {
	scan := yaml.scan
	source, start := scan.source, scan.position
	if yaml.lineStart {
		yaml.lineStart = false
		yaml.startLine()
		if scan.position != start {
			return
		}
	}
	switch character := scan.peek(); {
	case character == '\n' || character == '\r':
		scan.emit(Plain, scanNewline(source, start))
		yaml.lineStart = true
		if yaml.blockScalar {
			yaml.blockScalar = false
			yaml.blockBody()
		}
	case isWhitespace(character):
		scan.emit(Plain, scanWhile(source, start, bashIsBlank))
	case character == '#' && (start == 0 || isWhitespace(source[start-1])):
		scan.emit(Comment, scanToLineEnd(source, start))
	default:
		yaml.token()
	}
}

// startLine reads a line's indentation, then a document marker or a
// directive's name when the line starts with one.
func (yaml *yamlTokenizer) startLine() {
	scan := yaml.scan
	source, start := scan.source, scan.position
	yaml.lineOffset = start
	indentEnd := scanWhile(source, start, func(character byte) bool { return character == ' ' })
	yaml.nodeIndent = indentEnd - start
	scan.emit(Plain, indentEnd)
	if indentEnd != start {
		return
	}
	switch {
	case yamlIsDocumentMarker(source, start):
		scan.emitLength(Punctuation, 3)
		yaml.nodeIndent, yaml.flowDepth = -1, 0 // a block scalar after --- may start at column 0
	case byteAt(source, start) == '%' && yaml.flowDepth == 0:
		scan.emit(Keyword, scanWhile(source, start, func(character byte) bool { return !isWhitespace(character) }))
	}
}

// token reads an indicator, an anchor, alias or tag, a block scalar's
// indicator, or a scalar.
func (yaml *yamlTokenizer) token() {
	scan := yaml.scan
	source, start := scan.source, scan.position
	character := scan.peek()
	separated := start+1 >= len(source) || isWhitespace(source[start+1])
	switch {
	case (character == '-' || character == '?') && separated:
		scan.emitLength(Punctuation, 1) // a sequence entry, an explicit key
		yaml.nodeIndent = start - yaml.lineOffset
	case character == ':' && (separated || yaml.flowDepth > 0):
		scan.emitLength(Punctuation, 1)
	case character == '[' || character == '{':
		scan.emitLength(Punctuation, 1)
		yaml.flowDepth++
	case character == ']' || character == '}':
		scan.emitLength(Punctuation, 1)
		if yaml.flowDepth > 0 {
			yaml.flowDepth--
		}
	case character == ',' && yaml.flowDepth > 0:
		scan.emitLength(Punctuation, 1)
	case character == '&' || character == '*':
		if end := yamlNameEnd(source, start+1); end > start+1 {
			scan.emit(Variable, end) // &anchor, *alias
		} else {
			scan.emitLength(Punctuation, 1)
		}
	case character == '!':
		scan.emit(Type, yamlTagEnd(source, start))
	case (character == '|' || character == '>') && yaml.flowDepth == 0:
		end := scanWhile(source, start+1, func(character byte) bool {
			return character == '-' || character == '+' || isDecimalDigit(character)
		})
		scan.emit(Punctuation, end)
		if end >= len(source) || isWhitespace(source[end]) {
			yaml.blockScalar = true
		}
	case character == '"' || character == '\'':
		rules := yamlDoubleQuoted
		if character == '\'' {
			rules = yamlSingleQuoted
		}
		end := scanQuoted(source, start+1, rules)
		if yaml.isKeyAt(end) {
			scan.emit(Property, end)
			yaml.nodeIndent = start - yaml.lineOffset
		} else {
			scan.emit(String, end)
		}
	default:
		yaml.plainScalar()
	}
}

// plainScalar reads an unquoted scalar: a key, or a value typed by the core
// schema.
func (yaml *yamlTokenizer) plainScalar() {
	scan := yaml.scan
	source, start := scan.source, scan.position
	end, key := yamlPlainEnd(source, start, yaml.flowDepth > 0)
	if end == start {
		scan.emitCharacter(Plain)
		return
	}
	text := source[start:end]
	switch {
	case key:
		scan.emit(Property, end)
		yaml.nodeIndent = start - yaml.lineOffset
	case yamlConstants.has(text):
		scan.emit(Constant, end)
	case yamlIsNumber(text):
		scan.emit(Number, end)
	default:
		scan.emit(String, end)
	}
}

// isKeyAt reports whether a ':' that marks a key follows end on its line, after
// blanks: "name": value, {"a":1}.
func (yaml *yamlTokenizer) isKeyAt(end int) bool {
	source := yaml.scan.source
	colon := scanWhile(source, end, func(character byte) bool { return character == ' ' || character == '\t' })
	if byteAt(source, colon) != ':' || colon >= len(source) {
		return false
	}
	return colon+1 >= len(source) || isWhitespace(source[colon+1]) || yaml.flowDepth > 0
}

// blockBody reads a block scalar's body from the start of the line after its
// indicator: the lines indented past the key or - that holds it, blank lines
// among them included, as String from its first character to the end of its
// last non-blank line. A document marker at column 0 ends it too.
func (yaml *yamlTokenizer) blockBody() {
	scan := yaml.scan
	source := scan.source
	parent := yaml.nodeIndent
	contentStart, contentEnd := -1, -1
	for line := scan.position; line < len(source); {
		indentEnd := scanWhile(source, line, func(character byte) bool { return character == ' ' })
		end := scanToLineEnd(source, line)
		if !(indentEnd == end || isAllWhitespace(source[indentEnd:end])) {
			if indentEnd-line <= parent || (indentEnd == line && yamlIsDocumentMarker(source, line)) {
				break
			}
			if contentStart < 0 {
				contentStart = indentEnd
			}
			contentEnd = end
		}
		next := scanNewline(source, end)
		if next == end {
			break
		}
		line = next
	}
	if contentStart < 0 {
		return // an empty body: the lines that follow are read as usual
	}
	scan.emit(Plain, contentStart)
	scan.emit(String, contentEnd)
	yaml.lineStart = false
}

// yamlPlainEnd reads a plain scalar from start: up to " #", the line's end, or
// a ':' that marks it as a key (then key is true); in a flow collection also
// up to , [ ] { }. Blanks at its end are left out.
func yamlPlainEnd(source string, start int, flow bool) (end int, key bool) {
	end = start
	for index := start; index < len(source); index++ {
		character := source[index]
		switch {
		case character == '\n' || character == '\r':
			return end, false
		case character == ' ' || character == '\t':
			continue
		case character == '#' && index > start && isWhitespace(source[index-1]):
			return end, false
		case character == ':' && index > start && (index+1 >= len(source) || isWhitespace(source[index+1]) ||
			(flow && strings.IndexByte(yamlFlowIndicators, source[index+1]) >= 0)):
			return end, true
		case flow && strings.IndexByte(yamlFlowIndicators, character) >= 0:
			return end, false
		}
		end = index + 1
	}
	return end, false
}

// yamlNameEnd reads an anchor's or alias's name: up to whitespace or a flow
// indicator.
func yamlNameEnd(source string, start int) int {
	return scanWhile(source, start, func(character byte) bool {
		return !isWhitespace(character) && strings.IndexByte(yamlFlowIndicators, character) < 0
	})
}

// yamlTagEnd reads a tag from its '!': !local, !!str, !<tag:yaml.org,2002:str>.
func yamlTagEnd(source string, start int) int {
	if byteAt(source, start+1) == '<' {
		end := scanWhile(source, start+2, func(character byte) bool { return !isWhitespace(character) && character != '>' })
		if byteAt(source, end) == '>' {
			end++
		}
		return end
	}
	return yamlNameEnd(source, start+1)
}

// yamlIsDocumentMarker reports whether --- or ... starts at index, followed by
// whitespace or the end.
func yamlIsDocumentMarker(source string, index int) bool {
	if !strings.HasPrefix(source[index:], "---") && !strings.HasPrefix(source[index:], "...") {
		return false
	}
	return index+3 >= len(source) || isWhitespace(source[index+3])
}

// yamlIsNumber reports whether a plain scalar is a number in the YAML 1.2 core
// schema: 8107, -2.5e3, +.5, 1., 0x1F, 0o17, .inf, -.Inf, .nan.
func yamlIsNumber(text string) bool {
	switch text {
	case ".nan", ".NaN", ".NAN":
		return true
	}
	if len(text) > 2 && text[0] == '0' && (text[1] == 'x' || text[1] == 'o') {
		digit := isHexadecimalDigit
		if text[1] == 'o' {
			digit = isOctalDigit
		}
		return scanWhile(text, 2, digit) == len(text)
	}
	body := text
	if body != "" && (body[0] == '-' || body[0] == '+') {
		body = body[1:]
	}
	switch body {
	case ".inf", ".Inf", ".INF":
		return true
	}
	index := scanWhile(body, 0, isDecimalDigit)
	digits := index
	if index < len(body) && body[index] == '.' {
		fraction := scanWhile(body, index+1, isDecimalDigit)
		digits += fraction - index - 1
		index = fraction
	}
	if digits == 0 {
		return false
	}
	if index < len(body) && (body[index] == 'e' || body[index] == 'E') {
		index++
		if index < len(body) && (body[index] == '-' || body[index] == '+') {
			index++
		}
		exponent := scanWhile(body, index, isDecimalDigit)
		if exponent == index {
			return false
		}
		index = exponent
	}
	return index == len(body)
}
