package highlight

import "strings"

// Go, as the Go specification writes it.
//
// Keywords, the predeclared constants (true, false, nil, iota) and the
// predeclared types (int, string, error, any, …) come from fixed sets. A name
// right after `func` is a function; a name right after `type`, or first on its
// line inside a `type ( … )` group, is a type; a name followed by `(` is a call,
// so a function. The predeclared functions (len, append, make, panic, …) are
// functions by that last rule: Go lets them appear only as calls, so a `len`
// that is not called is a variable shadowing the builtin, and plain.
//
// Strings: "interpreted" (one line, backslash escapes), `raw` (any number of
// lines, no escapes) and 'r'unes, all three string. Numbers through
// scanNumber: 0x, 0b, 0o, 1_000, 0x1p-2, and the imaginary suffix (2i, 1.5e3i).

var (
	goKeywords = newWordSet(
		"break", "case", "chan", "const", "continue", "default", "defer", "else",
		"fallthrough", "for", "func", "go", "goto", "if", "import", "interface",
		"map", "package", "range", "return", "select", "struct", "switch", "type", "var",
	)
	goConstants        = newWordSet("true", "false", "nil", "iota")
	goPredeclaredTypes = newWordSet(
		"any", "bool", "byte", "comparable", "complex64", "complex128", "error",
		"float32", "float64", "int", "int8", "int16", "int32", "int64", "rune", "string",
		"uint", "uint8", "uint16", "uint32", "uint64", "uintptr",
	)
	// goOperators are the specification's operators; the brackets, ',', ';',
	// '.' and ':' are punctuation.
	goOperators = newOperatorsByFirstByte([]string{
		"&^=", "<<=", ">>=", "...", "&&", "||", "<-", "++", "--", "==", "!=", "<=", ">=", ":=",
		"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "<<", ">>", "&^",
		"+", "-", "*", "/", "%", "&", "|", "^", "<", ">", "=", "!", "~",
	})
	goRawString = quoted{closer: "`", multiline: true}
)

// goDeclaring is what the next name declares.
type goDeclaring uint8

const (
	goDeclaringNothing goDeclaring = iota
	goDeclaringFunction
	goDeclaringType
)

func tokenizeGo(scan *scanner) {
	source := scan.source
	var (
		lineStart   = true // only blanks and comments since the last line break
		declaring   = goDeclaringNothing
		typeKeyword bool // the token before was `type`, so a `(` opens a group
		depth       int  // brackets ( [ { open
		typeGroup   int  // the depth just inside `type ( … )`, 0 outside one
	)
	for loop := scan.loop(); loop.next(); {
		position := scan.position
		character := source[position]
		switch {
		case isWhitespace(character):
			end := scanWhitespace(source, position)
			if strings.ContainsAny(source[position:end], "\n\r") {
				lineStart = true
			}
			scan.emit(Plain, end)
			continue
		case character == '/' && byteAt(source, position+1) == '/':
			scan.emit(Comment, scanToLineEnd(source, position))
			continue
		case character == '/' && byteAt(source, position+1) == '*':
			scan.emit(Comment, scanBlockComment(source, position, "/*", "*/"))
			continue
		}

		// Anything else is a token: it ends the line start and the declaration.
		wasTypeKeyword, wasDeclaring, wasLineStart := typeKeyword, declaring, lineStart
		typeKeyword, declaring, lineStart = false, goDeclaringNothing, false

		switch {
		case character == '"':
			scan.emit(String, scanQuoted(source, position+1, doubleQuoted))
		case character == '`':
			scan.emit(String, scanQuoted(source, position+1, goRawString))
		case character == '\'':
			scan.emit(String, scanQuoted(source, position+1, singleQuoted))
		case isDecimalDigit(character) || (character == '.' && isDecimalDigit(byteAt(source, position+1))):
			scan.emit(Number, scanNumberTrailingDot(source, position))
		case character == '(' || character == '[' || character == '{':
			depth++
			if character == '(' && wasTypeKeyword {
				typeGroup = depth
			}
			scan.emitLength(Punctuation, 1)
		case character == ')' || character == ']' || character == '}':
			if depth == typeGroup {
				typeGroup = 0
			}
			if depth > 0 {
				depth--
			}
			scan.emitLength(Punctuation, 1)
		default:
			if end := scanIdentifier(source, position, ""); end > position {
				word := source[position:end]
				kind := Plain
				switch {
				case goKeywords.has(word):
					kind = Keyword
					switch word {
					case "func":
						declaring = goDeclaringFunction
					case "type":
						declaring, typeKeyword = goDeclaringType, true
					}
				case goConstants.has(word):
					kind = Constant
				case goPredeclaredTypes.has(word):
					kind = Type
				case wasDeclaring == goDeclaringFunction:
					kind = Function
				case wasDeclaring == goDeclaringType || (wasLineStart && typeGroup > 0 && depth == typeGroup):
					kind = Type
				case goCalled(source, end):
					kind = Function
				}
				scan.emit(kind, end)
				continue
			}
			if end := goOperators.match(source, position); end > position {
				scan.emit(Operator, end)
				continue
			}
			if strings.IndexByte(",;.:", character) >= 0 {
				scan.emitLength(Punctuation, 1)
				continue
			}
			scan.emitCharacter(Plain)
		}
	}
}

// operatorsByFirstByte holds a language's operators by their first byte, so
// finding the operator at a position looks at the few that can match there
// instead of the whole list: a byte that starts none (a NUL, a letter of some
// other script) costs one lookup. The go, python and sql tokenizers use it.
type operatorsByFirstByte [256][]string

func newOperatorsByFirstByte(operators []string) *operatorsByFirstByte {
	table := new(operatorsByFirstByte)
	for _, operator := range operators {
		if operator != "" {
			table[operator[0]] = append(table[operator[0]], operator)
		}
	}
	return table
}

// match returns the end of the longest operator at start, or start for none.
func (table *operatorsByFirstByte) match(source string, start int) int {
	if start < 0 || start >= len(source) {
		return start
	}
	return matchLongest(source, start, table[source[start]])
}

// goCalled reports whether the name that ends at end is called: a '(' follows,
// after spaces or tabs at most.
func goCalled(source string, end int) bool {
	for end < len(source) && (source[end] == ' ' || source[end] == '\t') {
		end++
	}
	return byteAt(source, end) == '('
}

// scanNumberTrailingDot is scanNumber that also takes a decimal point with no
// digits after it, the float literal "1." that Go and Python both allow, with
// the exponent or suffix that may follow it (1.e5, 1.j, 1.i). It is shared by
// the go and python tokenizers. "1..real" reads "1.": the second dot is not the
// number's. A number with a fraction, an exponent or a base prefix already
// ended where scanNumber stopped.
func scanNumberTrailingDot(source string, start int) int {
	end := scanNumber(source, start)
	if end == start || byteAt(source, end) != '.' {
		return end
	}
	for index := start; index < end; index++ {
		if !isDecimalDigit(source[index]) && source[index] != '_' {
			return end
		}
	}
	end++
	if exponent := byteAt(source, end); exponent == 'e' || exponent == 'E' {
		after := end + 1
		if sign := byteAt(source, after); sign == '+' || sign == '-' {
			after++
		}
		if isDecimalDigit(byteAt(source, after)) {
			end = scanWhile(source, after, func(character byte) bool { return isDecimalDigit(character) || character == '_' })
		}
	}
	return scanWhile(source, end, func(character byte) bool {
		return isASCIILetter(character) || isDecimalDigit(character) || character == '_'
	})
}
