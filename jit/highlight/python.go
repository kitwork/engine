package highlight

import "strings"

// Python 3, as of 3.14.
//
// Hard keywords are keywords; True, False, None (and Ellipsis, NotImplemented,
// __debug__, `...`) are constants. The soft keywords are keywords only where
// they open a statement: `match` and `case` first on a line and followed, after
// a blank, by something that can start an expression (so `match = re.match(x)`
// and `case(1)` stay names), `type` first on a line and followed by a name
// (`type Point = tuple[float, float]`), and `_` right after `case`.
//
// A name after `def` is a function and one after `class` (or a `type`
// statement) a type. A decorator, `@` first on a line with its dotted name, is
// a function: it is called with what it decorates. The builtin types and
// exception classes (int, str, dict, ValueError, …) are types, except after a
// dot (an attribute) or before a single `=` (a variable or keyword argument
// that shadows the builtin); after an annotation's ':' they are types whatever
// follows (`price: int = 0`). Any name followed by `(` is a call: a
// function. That includes the builtin functions (len, print), which are
// functions only where called, as in Go: id, input, format and filter are
// field, parameter and loop names as often as calls.
//
// Strings take any prefix of r, u, b, f, t, in either case (rb'…', F"…",
// Tr'…'), one or three quotes. In an f-string or t-string the {fields} are
// Python again: the braces are punctuation, the expression is tokenized as
// Python, a !conversion is an operator and a :format spec is string text that
// may hold fields of its own. Inside a field of a one-quote string, the
// string's own quote opens a nested string when that string closes on the same
// line (Python 3.12, PEP 701: f"{user["name"]}"); otherwise it ends the
// f-string (the rule before 3.12), so one unclosed field cannot colour the rest
// of the line. In a triple-quoted f-string its own three quotes always end it.

var (
	pythonKeywords = newWordSet(
		"and", "as", "assert", "async", "await", "break", "class", "continue", "def", "del",
		"elif", "else", "except", "finally", "for", "from", "global", "if", "import", "in",
		"is", "lambda", "nonlocal", "not", "or", "pass", "raise", "return", "try", "while",
		"with", "yield",
	)
	pythonConstants = newWordSet("True", "False", "None", "Ellipsis", "NotImplemented", "__debug__")
	pythonTypes     = newWordSet(
		"bool", "bytearray", "bytes", "complex", "dict", "float", "frozenset", "int", "list",
		"memoryview", "object", "range", "set", "slice", "str", "tuple", "type",
		"BaseException", "BaseExceptionGroup", "Exception", "ExceptionGroup", "ArithmeticError",
		"AssertionError", "AttributeError", "BlockingIOError", "BrokenPipeError", "BufferError",
		"ChildProcessError", "ConnectionAbortedError", "ConnectionError", "ConnectionRefusedError",
		"ConnectionResetError", "EOFError", "EncodingWarning", "EnvironmentError", "FileExistsError",
		"FileNotFoundError", "FloatingPointError", "GeneratorExit", "IOError", "ImportError",
		"IndentationError", "IndexError", "InterruptedError", "IsADirectoryError", "KeyError",
		"KeyboardInterrupt", "LookupError", "MemoryError", "ModuleNotFoundError", "NameError",
		"NotADirectoryError", "NotImplementedError", "OSError", "OverflowError", "PermissionError",
		"ProcessLookupError", "PythonFinalizationError", "RecursionError", "ReferenceError",
		"RuntimeError", "StopAsyncIteration", "StopIteration", "SyntaxError", "SystemError",
		"SystemExit", "TabError", "TimeoutError", "TypeError", "UnboundLocalError",
		"UnicodeDecodeError", "UnicodeEncodeError", "UnicodeError", "UnicodeTranslateError",
		"ValueError", "ZeroDivisionError",
		"Warning", "BytesWarning", "DeprecationWarning", "FutureWarning", "ImportWarning",
		"PendingDeprecationWarning", "ResourceWarning", "RuntimeWarning", "SyntaxWarning",
		"UnicodeWarning", "UserWarning",
	)
	// pythonOperators: '.', ',', ':', ';' and the brackets are punctuation; `...`
	// is the Ellipsis constant.
	pythonOperators = newOperatorsByFirstByte([]string{
		"**=", "//=", ">>=", "<<=", "->", ":=", "**", "//", "<<", ">>", "<=", ">=", "==", "!=",
		"+=", "-=", "*=", "/=", "%=", "&=", "|=", "^=", "@=",
		"+", "-", "*", "/", "%", "@", "&", "|", "^", "~", "<", ">", "=", "!",
	})
)

func tokenizePython(scan *scanner) { pythonTokenize(scan, true) }

// tokenizePythonField reads the expression of an f-string field: Python that
// does not start a line, so `@` there is matrix multiplication, not a decorator.
func tokenizePythonField(scan *scanner) { pythonTokenize(scan, false) }

func pythonTokenize(scan *scanner, lineStart bool) {
	source := scan.source
	var (
		defining, classing bool // the token before was `def` / `class` or a `type` statement
		afterDot           bool // the token before was '.': this name is an attribute
		afterCase          bool // the token before was the soft keyword `case`
		annotating         bool // the token before was ':': this name may be an annotation
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
		case character == '#':
			scan.emit(Comment, scanToLineEnd(source, position))
			continue
		case character == '\\' && scanNewline(source, position+1) > position+1:
			// A line continuation: the logical line goes on, so the next line's
			// first token does not start a statement.
			scan.emit(Plain, scanNewline(source, position+1))
			continue
		}

		// Anything else is a token: it ends the line start and what came before.
		wasLineStart, wasDefining, wasClassing, wasAfterDot, wasAfterCase, wasAnnotating := lineStart, defining, classing, afterDot, afterCase, annotating
		lineStart, defining, classing, afterDot, afterCase, annotating = false, false, false, false, false, false

		switch {
		case character == '"' || character == '\'':
			pythonString(scan, position, false, false)
		case isDecimalDigit(character) || (character == '.' && isDecimalDigit(byteAt(source, position+1))):
			scan.emit(Number, scanNumberTrailingDot(source, position))
		case character == '@' && wasLineStart:
			scan.emit(Function, pythonDottedNameEnd(source, position+1))
		case character == '.' && strings.HasPrefix(source[position:], "..."):
			scan.emitLength(Constant, 3)
		case character == '.':
			afterDot = true
			scan.emitLength(Punctuation, 1)
		default:
			if end := scanIdentifier(source, position, ""); end > position {
				word := source[position:end]
				if raw, formatted, isPrefix := pythonStringPrefix(word); isPrefix && (byteAt(source, end) == '"' || byteAt(source, end) == '\'') {
					pythonString(scan, end, raw, formatted)
					continue
				}
				kind := Plain
				switch {
				case pythonKeywords.has(word):
					kind = Keyword
					defining, classing = word == "def", word == "class"
				case pythonConstants.has(word):
					kind = Constant
				case wasDefining:
					kind = Function
				case wasClassing:
					kind = Type
				case wasLineStart && (word == "match" || word == "case") && pythonOpensStatement(source, end):
					kind = Keyword
					afterCase = word == "case"
				case wasLineStart && word == "type" && pythonNameFollows(source, end):
					kind = Keyword
					classing = true
				case wasAfterCase && word == "_":
					kind = Keyword
				case wasAfterDot:
					if pythonCalled(source, end) {
						kind = Function
					}
				case pythonTypes.has(word) && (wasAnnotating || !pythonAssigned(source, end)):
					// `price: int = 0` annotates: the '=' gives the default, it
					// does not assign to int.
					kind = Type
				case pythonCalled(source, end):
					kind = Function
				}
				scan.emit(kind, end)
				continue
			}
			if end := pythonOperators.match(source, position); end > position {
				scan.emit(Operator, end)
				continue
			}
			if strings.IndexByte("()[]{},:;", character) >= 0 {
				annotating = character == ':'
				scan.emitLength(Punctuation, 1)
				continue
			}
			scan.emitCharacter(Plain)
		}
	}
}

// pythonStringPrefix reports whether word is a string prefix (r, u, b, f, t and
// their two-letter mixes, any case) and whether it makes the string raw and
// formatted (an f-string or t-string, which holds {fields}).
func pythonStringPrefix(word string) (raw, formatted, isPrefix bool) {
	if len(word) == 0 || len(word) > 2 {
		return false, false, false
	}
	var seen [2]byte
	for index := 0; index < len(word); index++ {
		character := word[index] | 0x20 // lower case for letters
		switch character {
		case 'r':
			raw = true
		case 'f', 't':
			formatted = true
		case 'b', 'u':
		default:
			return false, false, false
		}
		seen[index] = character
	}
	if len(word) == 2 {
		first, second := seen[0], seen[1]
		// Two letters: one of them r, the other b, f or t; never u, never twice.
		if first == second || (first != 'r' && second != 'r') || first == 'u' || second == 'u' {
			return false, false, false
		}
	}
	return raw, formatted, true
}

// pythonString emits a string whose prefix (if any) starts at the scanner's
// position and whose opening quote is at quoteStart.
func pythonString(scan *scanner, quoteStart int, raw, formatted bool) {
	source := scan.source
	closer := pythonQuote(source, quoteStart)
	bodyStart := quoteStart + len(closer)
	if formatted {
		pythonFormattedString(scan, bodyStart, closer, raw)
		return
	}
	scan.emit(String, scanQuoted(source, bodyStart, quoted{closer: closer, escape: '\\', multiline: len(closer) == 3}))
}

// pythonFormattedString emits an f-string (or t-string) from the scanner's
// position (its prefix) through its closer: the text as string, each {field}
// as punctuation around Python. bodyStart is just after the opening quotes.
//
// specDepth counts the format specs open: in "{value:{width}}" the "{width}"
// field sits inside value's spec, and the '}' after it closes value's field.
// Every branch moves index forward, and the expression of a field is read
// once to find its end and once by the embedded tokenizer, which the embed
// depth bounds, so the whole stays linear.
func pythonFormattedString(scan *scanner, bodyStart int, closer string, raw bool) {
	source := scan.source
	multiline := len(closer) == 3
	index, specDepth := bodyStart, 0
	for index < len(source) {
		character := source[index]
		switch {
		case strings.HasPrefix(source[index:], closer):
			scan.emit(String, index+len(closer))
			return
		case character == '\n' || character == '\r':
			if !multiline {
				// Unterminated: the string stops at the line's end.
				scan.emit(String, index)
				return
			}
			index++
		case character == '\\':
			next := byteAt(source, index+1)
			switch {
			case next == '{' || next == '}':
				// A backslash does not escape a brace: the field still opens.
				index++
			case !raw && next == 'N' && byteAt(source, index+2) == '{':
				// \N{EM DASH} names a character; its braces are not a field.
				index = scanWhile(source, index+3, func(character byte) bool {
					return isASCIILetter(character) || isDecimalDigit(character) || character == ' ' || character == '-'
				})
				if byteAt(source, index) == '}' {
					index++
				}
			case strings.HasPrefix(source[index+1:], "\r\n"):
				index += 3
			default:
				// The backslash and the character after it: an escaped quote
				// does not close the string, raw or not.
				index = min(index+2, len(source))
			}
		case specDepth == 0 && (character == '{' || character == '}') && byteAt(source, index+1) == character:
			index += 2 // {{ and }} are a literal brace
		case character == '}' && specDepth == 0:
			index++ // a lone '}' is an error in Python; it stays text
		case character == '}':
			scan.emit(String, index)
			scan.emitLength(Punctuation, 1)
			specDepth--
			index = scan.position
		case character == '{':
			scan.emit(String, index)
			scan.emitLength(Punctuation, 1)
			scan.embed(tokenizePythonField, pythonFieldEnd(source, scan.position, closer, multiline))
			index = scan.position
			if byteAt(source, index) == '!' {
				// The conversion: !r, !s, !a, one operator.
				scan.emit(Operator, scanIdentifier(source, index+1, ""))
				index = scan.position
			}
			switch byteAt(source, index) {
			case ':':
				scan.emitLength(Punctuation, 1)
				specDepth++
			case '}':
				scan.emitLength(Punctuation, 1)
			}
			index = scan.position
		default:
			index++
		}
	}
	scan.emit(String, index)
}

// pythonFieldEnd is where the expression of a field that starts at start
// ends: at a '}', a !conversion or a :format spec outside any bracket, at the
// string's closer, at a line break in a one-line string, or at the end. Quotes
// other than the closer open a string inside the expression, which is skipped
// whole.
func pythonFieldEnd(source string, start int, closer string, multiline bool) int {
	depth := 0
	index := start
	for index < len(source) {
		character := source[index]
		switch {
		case strings.HasPrefix(source[index:], closer):
			if len(closer) == 1 {
				if end, closed := pythonClosesOnItsLine(source, index+1, closer[0]); closed {
					index = end // f"{user["name"]}": a nested string (PEP 701)
					continue
				}
			}
			return index
		case !multiline && (character == '\n' || character == '\r'):
			return index
		case character == '(' || character == '[' || character == '{':
			depth++
		case character == ')' || character == ']' || character == '}':
			if depth == 0 {
				if character == '}' {
					return index
				}
			} else {
				depth--
			}
		case depth == 0 && character == '!' && byteAt(source, index+1) != '=':
			return index
		case depth == 0 && character == ':':
			return index
		case character == '"' || character == '\'':
			nested := pythonQuote(source, index)
			index = scanQuoted(source, index+len(nested), quoted{closer: nested, escape: '\\', multiline: multiline && len(nested) == 3})
			continue
		}
		index++
	}
	return len(source)
}

// pythonClosesOnItsLine reads a one-quote string whose body starts at start and
// reports whether quote closes it before the line ends, and where it ends. A
// backslash takes the byte after it, but a line break after one still ends the
// line here. When it does not close, the bytes read hold no unescaped quote, so
// the f-string's quote that opened it is never read past twice: linear.
func pythonClosesOnItsLine(source string, start int, quote byte) (int, bool) {
	for index := start; index < len(source); index++ {
		switch source[index] {
		case quote:
			return index + 1, true
		case '\n', '\r':
			return index, false
		case '\\':
			if next := byteAt(source, index+1); next == '\n' || next == '\r' {
				return index, false
			}
			index++
		}
	}
	return len(source), false
}

// pythonQuote is the opening quote at index, which is also the string's
// closer: three quotes when three of the same follow, else one. A slice of
// source, so it costs no copy.
func pythonQuote(source string, index int) string {
	quote := source[index]
	if byteAt(source, index+1) == quote && byteAt(source, index+2) == quote {
		return source[index : index+3]
	}
	return source[index : index+1]
}

// pythonDottedNameEnd reads a decorator's name after its '@': a.b.c.
func pythonDottedNameEnd(source string, start int) int {
	end := start
	for {
		next := scanIdentifier(source, end, "")
		if next == end {
			return end
		}
		end = next
		if byteAt(source, end) != '.' || scanIdentifier(source, end+1, "") == end+1 {
			return end
		}
		end++
	}
}

// pythonBlankEnd skips spaces and tabs from index.
func pythonBlankEnd(source string, index int) int {
	for index < len(source) && (source[index] == ' ' || source[index] == '\t') {
		index++
	}
	return index
}

// pythonCalled reports whether the name that ends at end is called.
func pythonCalled(source string, end int) bool {
	return byteAt(source, pythonBlankEnd(source, end)) == '('
}

// pythonAssigned reports whether a single '=' follows the name: an assignment
// or a keyword argument, where a builtin's name is only a name.
func pythonAssigned(source string, end int) bool {
	next := pythonBlankEnd(source, end)
	return byteAt(source, next) == '=' && byteAt(source, next+1) != '='
}

// pythonOpensStatement reports whether `match` or `case`, ending at end, opens
// a statement: a blank follows, then something that can start an expression
// or a pattern, not an operator, '.', ',', ':' or the line's end.
func pythonOpensStatement(source string, end int) bool {
	next := pythonBlankEnd(source, end)
	if next == end || next >= len(source) {
		return false
	}
	character := source[next]
	return isASCIILetter(character) || isDecimalDigit(character) || character == '_' || character >= 0x80 ||
		strings.IndexByte("([{'\"-~", character) >= 0
}

// pythonNameFollows reports whether a blank and then a name follow end, as in
// `type Alias = …`.
func pythonNameFollows(source string, end int) bool {
	next := pythonBlankEnd(source, end)
	return next > end && scanIdentifier(source, next, "") > next
}
