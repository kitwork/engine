package highlight

import "strings"

// tokenizeJSON reads JSON: a string before ':' is a property (the key), any
// other string is a string; true, false and null are constants; numbers keep a
// leading minus; { } [ ] , : are punctuation.
//
// It reads what people actually paste, losslessly and without complaint:
// comments (// and /* */, as in tsconfig.json and editor settings) are
// comments, single-quoted strings and bare keys ({name: 1}) are read like their
// strict forms, NaN and Infinity (signed too) are constants, and anything else
// is plain.
func tokenizeJSON(scan *scanner) {
	for loop := scan.loop(); loop.next(); {
		source, position := scan.source, scan.position
		character, next := scan.peek(), scan.peekAt(1)
		switch {
		case isWhitespace(character):
			scan.emit(Plain, scanWhitespace(source, position))
		case character == '"' || character == '\'':
			rules := doubleQuoted
			if character == '\'' {
				rules = singleQuoted
			}
			end := scanQuoted(source, position+1, rules)
			kind := String
			if jsonKeyFollows(source, end) {
				kind = Property
			}
			scan.emit(kind, end)
		case character == '/' && next == '/':
			scan.emit(Comment, scanToLineEnd(source, position))
		case character == '/' && next == '*':
			scan.emit(Comment, scanBlockComment(source, position, "/*", "*/"))
		case isDecimalDigit(character) || (character == '.' && isDecimalDigit(next)):
			scan.emit(Number, scanNumber(source, position))
		case (character == '-' || character == '+') && (isDecimalDigit(next) || (next == '.' && isDecimalDigit(scan.peekAt(2)))):
			scan.emit(Number, scanNumber(source, position+1))
		case (character == '-' || character == '+') && strings.HasPrefix(source[position+1:], "Infinity"):
			scan.emit(Constant, position+1+len("Infinity"))
		case strings.IndexByte("{}[],:", character) >= 0:
			scan.emitCharacter(Punctuation)
		default:
			end := scanIdentifier(source, position, "$")
			if end == position {
				scan.emitCharacter(Plain)
				continue
			}
			switch word := source[position:end]; {
			case jsonKeyFollows(source, end):
				scan.emit(Property, end)
			case jsonConstants.has(word):
				scan.emit(Constant, end)
			default:
				scan.emit(Plain, end)
			}
		}
	}
}

var jsonConstants = newWordSet("false", "Infinity", "NaN", "null", "true")

// jsonKeyFollows reports whether a ':' follows end, whitespace and line breaks
// allowed in between: what makes a string a key.
func jsonKeyFollows(source string, end int) bool {
	return byteAt(source, scanWhitespace(source, end)) == ':'
}
