package highlight

import (
	"strings"
)

// tokenizePowerShell reads a PowerShell script the way PowerShell parses it: a
// statement that starts with a name is a command (arguments are words, -Name
// is a parameter), any other statement is an expression (-eq is an operator).
//
//   - A command's name is Function, and so is a Verb-Noun name anywhere
//     (Get-ChildItem, Where-Object) whose verb is one of PowerShell's verbs.
//     -Parameter is Attribute; in an expression -eq, -and, -f … are Operator.
//   - $name, $env:PATH, ${any name}, $_ and @splat are Variable; $true, $false,
//     $null are Constant. $( … ), @( … ) and @{ … } open nested statements.
//   - '…' (with ” inside) is String as written; "…" and @"…"@ are String with
//     each $name and $( … ) inside as Variable; @'…'@ is String as written.
//   - # … and <# … #> are Comment. Keywords (if, foreach, function, param,
//     return, try …) are Keyword at a statement's start, case-insensitively.
//   - [int], [System.IO.Path] are Type in Punctuation, and a [ right after
//     one opens another ([Parameter()][string]); a name after . or :: is
//     Property, or Function when a ( follows; a key before = in @{ } and a
//     name among an attribute's arguments ([Parameter(Mandatory)]) are
//     Property; numbers (with kb/mb suffixes) are Number.
//
// One pass with a few flags and a capped stack of open brackets; no recursion.
func tokenizePowerShell(scan *scanner) {
	powershell := &powershellTokenizer{scan: scan, statementStart: true}
	for loop := scan.loop(); loop.next(); {
		powershell.step()
	}
}

// powershellFrame is one open bracket and the statement it interrupted.
type powershellFrame struct {
	commandMode        bool // the enclosing statement's mode, back at the closer
	typeLiteral        bool // [int], [string[]]: names inside are types
	attributeArguments bool // [Parameter(Mandatory, Position = 0)]: names inside are properties
}

// powershellMaximumFrames caps the stack; deeper brackets are only counted.
const powershellMaximumFrames = 1 << 12

type powershellTokenizer struct {
	scan *scanner

	statementStart bool // the next token starts a statement
	commandMode    bool // the statement started with a command's name
	afterBlock     bool // a } just closed: else, elseif, catch, finally, until, while follow
	member         bool // the next name follows . or ::
	callOperator   bool // after & or . at a statement's start: the next word is the command
	typeName       bool // after class or enum: the next name is a type
	closedType     bool // a ] just closed a type literal: a [ right after it opens another

	frames   []powershellFrame
	overflow int
}

var (
	powershellKeywords = newWordSet("begin", "break", "catch", "class", "clean", "configuration", "continue", "data",
		"default", "define", "do", "dynamicparam", "else", "elseif", "end", "enum", "exit", "filter", "finally", "for",
		"foreach", "from", "function", "hidden", "if", "in", "inlinescript", "parallel", "param", "process", "return",
		"sequence", "static", "switch", "throw", "trap", "try", "until", "using", "var", "while", "workflow")

	// powershellAfterBlock are the keywords that continue a statement after its }.
	powershellAfterBlock = newWordSet("else", "elseif", "catch", "finally", "until", "while")

	// powershellOperatorWords are the -word operators, without the dash and in
	// lower case; the comparison ones also come with a c (case-sensitive) and an
	// i (case-insensitive) prefix.
	powershellOperatorWords = func() wordSet {
		comparisons := []string{"eq", "ne", "gt", "ge", "lt", "le", "like", "notlike", "match", "notmatch",
			"replace", "contains", "notcontains", "in", "notin", "split"}
		set := newWordSet("is", "isnot", "as", "and", "or", "not", "xor", "band", "bor", "bxor", "bnot",
			"shl", "shr", "join", "f")
		for _, word := range comparisons {
			set[word], set["c"+word], set["i"+word] = struct{}{}, struct{}{}, struct{}{}
		}
		return set
	}()

	// powershellVerbs are the verbs of PowerShell's command names, lower case:
	// Get-ChildItem is a command wherever it is written.
	powershellVerbs = newWordSet("add", "approve", "assert", "backup", "block", "build", "checkpoint", "clear",
		"close", "compare", "complete", "compress", "confirm", "connect", "convert", "convertfrom", "convertto",
		"copy", "debug", "deny", "deploy", "disable", "disconnect", "dismount", "edit", "enable", "enter", "exit",
		"expand", "export", "find", "foreach", "format", "get", "grant", "group", "hide", "import", "initialize",
		"install", "invoke", "join", "limit", "lock", "measure", "merge", "mount", "move", "new", "open",
		"optimize", "out", "ping", "pop", "protect", "publish", "push", "read", "receive", "redo", "register",
		"remove", "rename", "repair", "request", "reset", "resize", "resolve", "restart", "restore", "resume",
		"revoke", "save", "search", "select", "send", "set", "show", "skip", "sort", "split", "start", "step",
		"stop", "submit", "suspend", "switch", "sync", "tee", "test", "trace", "unblock", "undo", "uninstall",
		"unlock", "unprotect", "unpublish", "unregister", "update", "use", "wait", "watch", "where", "write")

	// powershellOperators are the symbol operators, longest first where it matters.
	powershellOperators = []string{"??=", "??", "?.", "++", "--", "+=", "-=", "*=", "/=", "%=", "..", "::",
		"&&", "||", ">>", ">", "<", "=", "!", "+", "-", "*", "/", "%", "|", "&", "?"}

	// powershellOperatorStarts are the bytes an operator can start with, so a
	// byte that starts none (\, ^, …) is not matched against every operator.
	powershellOperatorStarts = func() string {
		var starts []byte
		for _, operator := range powershellOperators {
			if !strings.Contains(string(starts), operator[:1]) {
				starts = append(starts, operator[0])
			}
		}
		return string(starts)
	}()

	powershellSingleQuoted = quoted{closer: "'", doubled: true, multiline: true}
	powershellDoubleQuoted = quoted{closer: `"`, escape: '`', doubled: true, multiline: true}
)

// step reads one thing: a line break, blanks, a comment, an escape, or a token.
func (powershell *powershellTokenizer) step() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	switch character := scan.peek(); {
	case character == '\n' || character == '\r':
		scan.emit(Plain, scanNewline(source, start))
		powershell.statementStart, powershell.commandMode, powershell.callOperator = true, false, false
	case isWhitespace(character):
		scan.emit(Plain, scanWhile(source, start, bashIsBlank))
	case character == '#':
		scan.emit(Comment, scanToLineEnd(source, start))
	case scan.startsWith("<#"):
		scan.emit(Comment, scanBlockComment(source, start, "<#", "#>"))
	case character == '`':
		scan.emit(Plain, shellEscapeEnd(source, start)) // an escape; before a line break it continues the line
	default:
		powershell.token()
	}
}

// token reads one token that is not blank or a comment.
func (powershell *powershellTokenizer) token() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	character := scan.peek()
	statementStart, member, afterBlock, callOperator, typeName, closedType :=
		powershell.statementStart, powershell.member, powershell.afterBlock, powershell.callOperator, powershell.typeName, powershell.closedType
	powershell.statementStart, powershell.member, powershell.afterBlock, powershell.callOperator, powershell.typeName, powershell.closedType =
		false, false, false, false, false, false
	if statementStart {
		powershell.commandMode = false // an expression until a command's name says otherwise
	}
	if callOperator {
		powershell.commandMode = true // & "C:\tools\git.exe" status --short: the rest are arguments
	}
	blockKeywordEnd := 0 // } else {, } catch {, … : where the keyword ends
	if afterBlock && powershellStartsName(source, start) {
		if end := scanIdentifier(source, start, ""); powershellAfterBlock.has(strings.ToLower(source[start:end])) {
			blockKeywordEnd = end
		}
	}

	switch {
	case character == '\'':
		end := scanQuoted(source, start+1, powershellSingleQuoted)
		scan.emit(powershell.keyOr(String, statementStart, end), end)
	case character == '"' && statementStart && powershell.keyOr(String, true, scanQuoted(source, start+1, powershellDoubleQuoted)) == Property:
		scan.emit(Property, scanQuoted(source, start+1, powershellDoubleQuoted)) // @{ "key" = … }
	case character == '"':
		scan.emit(String, start+1)
		powershell.expandingString(len(source), true)
	case character == '@':
		powershell.at()
	case character == '$':
		powershell.dollar()
	case character == '(' || character == '{':
		attributeArguments := character == '(' && powershellFrameIsType(powershell.top())
		scan.emitLength(Punctuation, 1)
		powershell.push(false)
		if frame := powershell.top(); frame != nil && attributeArguments {
			frame.attributeArguments = true
		}
		powershell.statementStart = true
	case character == '[':
		powershell.bracket(closedType)
	case character == ')' || character == '}' || character == ']':
		powershell.close(character)
	case character == ',':
		scan.emitLength(Punctuation, 1)
	case character == ';':
		scan.emitLength(Punctuation, 1)
		powershell.statementStart = true
	case (isDecimalDigit(character) || character == '*') && scan.peekAt(1) == '>':
		powershell.redirection()
	case character == '-' && (isASCIILetter(scan.peekAt(1)) ||
		(powershell.commandMode && scan.peekAt(1) == '-' && isASCIILetter(scan.peekAt(2)))):
		powershell.dash()
	case character == '.' && !member && powershellAfterValue(source, start) && powershellStartsName(source, start+1):
		scan.emitLength(Punctuation, 1)
		powershell.member = true
	case scan.startsWith("::"):
		scan.emitLength(Operator, 2)
		powershell.member = true
	case member && powershellStartsName(source, start):
		end := scanIdentifier(source, start, "")
		scan.emit(powershell.callOr(Property, end), end)
	case typeName && powershellStartsName(source, start):
		scan.emit(Type, scanIdentifier(source, start, ""))
	case powershellFrameIsType(powershell.top()) && powershellStartsName(source, start):
		scan.emit(Type, scanIdentifier(source, start, "."))
	case powershellFrameIsAttributeArguments(powershell.top()) && powershellStartsName(source, start):
		scan.emit(Property, scanIdentifier(source, start, "")) // [Parameter(Mandatory, Position = 0)]
	case statementStart || callOperator:
		powershell.commandStart(statementStart, callOperator)
	case blockKeywordEnd > 0:
		scan.emit(Keyword, blockKeywordEnd)
		powershell.statementStart = true
	case powershell.commandMode:
		powershell.argument(false)
	default:
		powershell.expression()
	}
}

// commandStart reads the first token of a statement: a keyword, a key before
// =, a command's name, the call operators & and ., or an expression.
func (powershell *powershellTokenizer) commandStart(statementStart, callOperator bool) {
	scan := powershell.scan
	source, start := scan.source, scan.position
	character := scan.peek()
	switch {
	case callOperator:
		powershell.argument(true)
	case character == '&' && !scan.startsWith("&&"):
		scan.emitLength(Operator, 1)
		powershell.callOperator = true
	case character == '.' && (scan.peekAt(1) == ' ' || scan.peekAt(1) == '\t'):
		scan.emitLength(Operator, 1) // dot-sourcing: . ./helpers.ps1
		powershell.callOperator = true
	case (character == '%' || character == '?') && (isWhitespace(scan.peekAt(1)) || scan.peekAt(1) == '{'):
		scan.emitLength(Function, 1) // the aliases of ForEach-Object and Where-Object
		powershell.commandMode = true
	case powershellStartsName(source, start) || powershellStartsPath(source, start):
		end := powershellNameEnd(source, start)
		name := source[start:end]
		switch {
		case scanIdentifier(name, 0, "") == len(name) && powershellKeywords.has(strings.ToLower(name)):
			scan.emit(Keyword, end)
			powershell.keyword(strings.ToLower(name))
		case powershell.keyOr(Function, true, end) == Property:
			scan.emit(Property, end) // @{ Port = 8107 }, [Parameter(Mandatory = $true)]
		default:
			scan.emit(Function, end)
			powershell.commandMode = true
		}
	default:
		powershell.expression()
	}
}

// keyword sets what follows a keyword.
func (powershell *powershellTokenizer) keyword(word string) {
	switch word {
	case "class", "enum":
		powershell.typeName = true
	default:
		// if (…), foreach (…), return $x, throw "…", function Name, param(…):
		// what follows starts afresh.
		powershell.statementStart = true
	}
}

// argument reads a word of a command: a number, a Verb-Noun, or plain text;
// or an operator that ends or redirects the command. command says the word is
// the command itself (after & or .).
func (powershell *powershellTokenizer) argument(command bool) {
	scan := powershell.scan
	source, start := scan.source, scan.position
	switch scan.peek() {
	case '|', '&', '>', '<':
		powershell.operator()
		return
	}
	end := powershellArgumentEnd(source, start)
	if end == start {
		scan.emitCharacter(Plain)
		return
	}
	word := source[start:end]
	switch {
	case command || powershellIsCommandName(word):
		scan.emit(Function, end)
	case isDecimalDigit(word[0]) && scanNumber(source, start) == end:
		scan.emit(Number, end)
	default:
		scan.emit(Plain, end)
	}
}

// expression reads a token of an expression.
func (powershell *powershellTokenizer) expression() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	character := scan.peek()
	switch {
	case isDecimalDigit(character) || (character == '.' && isDecimalDigit(scan.peekAt(1))):
		scan.emit(Number, scanNumber(source, start))
	case character == '.' && scan.peekAt(1) != '.':
		scan.emitLength(Punctuation, 1)
	case powershellStartsName(source, start):
		end := scanIdentifier(source, start, "")
		if verb := strings.ToLower(source[start:end]); powershellVerbs.has(verb) && byteAt(source, end) == '-' && powershellStartsName(source, end+1) {
			scan.emit(Function, scanIdentifier(source, end+1, "")) // Get-Date in the middle of an expression
			return
		}
		switch word := strings.ToLower(source[start:end]); {
		case powershellKeywords.has(word):
			scan.emit(Keyword, end) // foreach ($x in $list)
		default:
			scan.emit(powershell.callOr(Plain, end), end)
		}
	default:
		powershell.operator()
	}
}

// operator reads a symbol operator, or one character as plain text.
func (powershell *powershellTokenizer) operator() {
	scan := powershell.scan
	start := scan.position
	end := start
	if !scan.done() && strings.IndexByte(powershellOperatorStarts, scan.peek()) >= 0 {
		end = matchLongest(scan.source, start, powershellOperators)
	}
	if end == start {
		scan.emitCharacter(Plain)
		return
	}
	operator := scan.source[start:end]
	scan.emit(Operator, end)
	switch operator {
	case "=", "+=", "-=", "*=", "/=", "%=", "??=", "|", "||", "&&", "&":
		// $x = Get-Item …, … | Sort-Object …, a && b, a background job &:
		// what follows starts a statement.
		powershell.statementStart = true
	}
}

// dash reads -Word: a parameter in a command, an operator in an expression.
func (powershell *powershellTokenizer) dash() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	nameStart := start + 1
	if byteAt(source, nameStart) == '-' {
		nameStart++ // a native command's --long flag
	}
	end := scanWhile(source, nameStart, func(character byte) bool {
		return isASCIILetter(character) || isDecimalDigit(character) || character == '_' ||
			(powershell.commandMode && character == '-')
	})
	if !powershell.commandMode && powershellOperatorWords.has(strings.ToLower(source[nameStart:end])) {
		scan.emit(Operator, end)
		return
	}
	scan.emit(Attribute, end)
}

// redirection reads 2>&1, 2>>, 2>, *> and the like.
func (powershell *powershellTokenizer) redirection() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	end := start + 2
	switch {
	case byteAt(source, end) == '>':
		end++
	case byteAt(source, end) == '&' && isDecimalDigit(byteAt(source, end+1)):
		end += 2
	}
	scan.emit(Operator, end)
}

// at reads what an @ starts: a here-string, @( … ), @{ … }, or @splat.
func (powershell *powershellTokenizer) at() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	switch next := scan.peekAt(1); {
	case next == '"' || next == '\'':
		end := powershellHereStringEnd(source, start, next)
		switch {
		case end < 0:
			scan.emitLength(Operator, 1) // not a here-string: the quote that follows is an ordinary string
		case next == '"':
			powershell.expandingString(end, false)
		default:
			scan.emit(String, end)
		}
	case next == '(' || next == '{':
		scan.emitLength(Punctuation, 2)
		powershell.push(false)
		powershell.statementStart = true
	case powershellStartsName(source, start+1):
		scan.emit(Variable, scanIdentifier(source, start+1, ""))
	default:
		scan.emitLength(Operator, 1)
	}
}

// dollar reads what a $ starts outside a string.
func (powershell *powershellTokenizer) dollar() {
	scan := powershell.scan
	source, start := scan.source, scan.position
	if scan.startsWith("$(") {
		scan.emitLength(Punctuation, 2)
		powershell.push(false)
		powershell.statementStart = true
		return
	}
	if end := powershellExpansionEnd(source, start); end > start {
		scan.emit(powershellVariableKind(source[start:end]), end)
		return
	}
	scan.emitLength(Plain, 1)
}

// bracket reads a [: a type literal ([int], [Parameter(…)]) where a value
// starts or right after another type literal ([Parameter()][string]), an index
// ($list[0], $grid[0][1]) right after a value.
func (powershell *powershellTokenizer) bracket(closedType bool) {
	scan := powershell.scan
	typeLiteral := powershellFrameIsType(powershell.top()) || !powershellAfterValue(scan.source, scan.position) ||
		(closedType && byteAt(scan.source, scan.position-1) == ']')
	scan.emitLength(Punctuation, 1)
	powershell.push(typeLiteral)
}

// close reads ), } or ] and goes back to the statement the bracket interrupted.
func (powershell *powershellTokenizer) close(closer byte) {
	powershell.scan.emitLength(Punctuation, 1)
	switch {
	case powershell.overflow > 0:
		powershell.overflow--
	case len(powershell.frames) > 0:
		last := len(powershell.frames) - 1
		powershell.commandMode = powershell.frames[last].commandMode
		powershell.closedType = closer == ']' && powershell.frames[last].typeLiteral
		powershell.frames = powershell.frames[:last]
	}
	powershell.afterBlock = closer == '}'
}

// push opens a bracket, remembering the statement's mode; inside, a new
// statement (or a type literal) starts.
func (powershell *powershellTokenizer) push(typeLiteral bool) {
	if len(powershell.frames) < powershellMaximumFrames {
		powershell.frames = append(powershell.frames, powershellFrame{commandMode: powershell.commandMode, typeLiteral: typeLiteral})
	} else {
		powershell.overflow++
	}
	powershell.commandMode = false
}

func (powershell *powershellTokenizer) top() *powershellFrame {
	if powershell.overflow > 0 || len(powershell.frames) == 0 {
		return nil
	}
	return &powershell.frames[len(powershell.frames)-1]
}

func powershellFrameIsType(frame *powershellFrame) bool { return frame != nil && frame.typeLiteral }

func powershellFrameIsAttributeArguments(frame *powershellFrame) bool {
	return frame != nil && frame.attributeArguments
}

// keyOr is Property when a statement's first token, ending at end, is a key:
// followed on its line by = (not ==); otherwise it is kind.
func (powershell *powershellTokenizer) keyOr(kind Kind, statementStart bool, end int) Kind {
	if !statementStart {
		return kind
	}
	source := powershell.scan.source
	after := scanWhile(source, end, func(character byte) bool { return character == ' ' || character == '\t' })
	if byteAt(source, after) == '=' && byteAt(source, after+1) != '=' {
		return Property
	}
	return kind
}

// callOr is Function when a ( follows end directly (a method call), else kind.
func (powershell *powershellTokenizer) callOr(kind Kind, end int) Kind {
	if byteAt(powershell.scan.source, end) == '(' {
		return Function
	}
	return kind
}

// expandingString emits text from the position as String with each $name,
// ${…} and $( … ) inside as Variable (or Constant for $true …), up to limit,
// or through the closing " when closes (a doubled "" stays inside). Literal
// stretches are emitted only next to an expansion or at the end.
func (powershell *powershellTokenizer) expandingString(limit int, closes bool) {
	scan := powershell.scan
	source := scan.source[:limit]
	marks := "$`"
	if closes {
		marks = "$`\""
	}
	index := scan.position
	for index < limit {
		found := strings.IndexAny(source[index:], marks)
		if found < 0 {
			break
		}
		index += found
		switch source[index] {
		case '`':
			index = shellEscapeEnd(source, index)
		case '"':
			if byteAt(source, index+1) == '"' {
				index += 2
				continue
			}
			scan.emit(String, index+1)
			return
		case '$':
			end := powershellExpansionEnd(source, index)
			if end == index {
				index++
				continue
			}
			scan.emit(String, index)
			scan.emit(powershellVariableKind(source[index:end]), end)
			index = end
		}
	}
	scan.emit(String, limit)
}

// powershellExpansionEnd is where what a $ at start names ends, or start when
// it names nothing: $( … ) through its closer, ${…}, $name (with a scope or
// drive: $env:PATH), $_, $$, $?, $^.
func powershellExpansionEnd(source string, start int) int {
	if start+1 >= len(source) {
		return start
	}
	switch next := source[start+1]; {
	case next == '(':
		return powershellSubexpressionEnd(source, start+2)
	case next == '{':
		return scanQuoted(source, start+2, quoted{closer: "}", escape: '`'})
	case next == '$' || next == '?' || next == '^':
		return start + 2
	case isDecimalDigit(next):
		return scanWhile(source, start+1, isDecimalDigit)
	case powershellStartsName(source, start+1):
		end := scanIdentifier(source, start+1, "")
		if byteAt(source, end) == ':' && powershellStartsName(source, end+1) {
			end = scanIdentifier(source, end+1, "") // $env:PATH, $script:count
		}
		return end
	}
	return start
}

// powershellSubexpressionEnd reads $( … ) from just inside the parenthesis
// through its closer: nested parentheses counted, quoted text skipped whole.
func powershellSubexpressionEnd(source string, index int) int {
	depth := 1
	for index < len(source) {
		switch source[index] {
		case '`':
			index = shellEscapeEnd(source, index)
			continue
		case '\'':
			index = scanQuoted(source, index+1, powershellSingleQuoted)
			continue
		case '"':
			index = scanQuoted(source, index+1, powershellDoubleQuoted)
			continue
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return index + 1
			}
		}
		index++
	}
	return len(source)
}

// powershellHereStringEnd reads a here-string whose @ is at start: @" or @'
// then only blanks to the line's end, through the line that starts with "@ or
// '@ (or to the end of the source). It is -1 when the opener is not one.
func powershellHereStringEnd(source string, start int, quote byte) int {
	after := scanWhile(source, start+2, func(character byte) bool { return character == ' ' || character == '\t' })
	if after >= len(source) || (source[after] != '\n' && source[after] != '\r') {
		return -1
	}
	closer := string([]byte{quote, '@'})
	for line := scanNewline(source, after); line < len(source); {
		if strings.HasPrefix(source[line:], closer) {
			return line + len(closer)
		}
		end := scanToLineEnd(source, line)
		next := scanNewline(source, end)
		if next == end {
			break
		}
		line = next
	}
	return len(source)
}

// powershellVariableKind is Constant for $true, $false and $null, else Variable.
func powershellVariableKind(text string) Kind {
	if strings.EqualFold(text, "$true") || strings.EqualFold(text, "$false") || strings.EqualFold(text, "$null") {
		return Constant
	}
	return Variable
}

// powershellIsCommandName reports whether a word is Verb-Noun with a
// PowerShell verb: Get-ChildItem, ForEach-Object, but not en-US.
func powershellIsCommandName(word string) bool {
	dash := strings.IndexByte(word, '-')
	if dash <= 0 || dash+1 >= len(word) || !isASCIILetter(word[dash+1]) {
		return false
	}
	return powershellVerbs.has(strings.ToLower(word[:dash]))
}

// powershellStartsName reports whether a name starts at index: a letter of any
// script or '_'.
func powershellStartsName(source string, index int) bool {
	return index < len(source) && scanIdentifier(source, index, "") > index
}

// powershellStartsPath reports whether a command path starts at index: .\x,
// ./x, ..\x, \x, ~/x.
func powershellStartsPath(source string, index int) bool {
	switch byteAt(source, index) {
	case '\\', '~':
		return true
	case '.':
		next := byteAt(source, index+1)
		return next == '\\' || next == '/' || (next == '.' && (byteAt(source, index+2) == '\\' || byteAt(source, index+2) == '/'))
	}
	return false
}

// powershellAfterValue reports whether the byte before index ends a value, so
// a . there is member access and a [ an index: $x.Name, $list[0], (…).Count.
func powershellAfterValue(source string, index int) bool {
	if index == 0 {
		return false
	}
	previous := source[index-1]
	return isASCIILetter(previous) || isDecimalDigit(previous) || previous == '_' || previous >= 0x80 ||
		strings.IndexByte(`)]}'"`, previous) >= 0
}

// powershellNameEnd reads a command's name at a statement's start: up to
// whitespace or a byte that is syntax (an = too, so @{a=1} keeps its key).
func powershellNameEnd(source string, start int) int {
	return scanWhile(source, start, func(character byte) bool {
		return !isWhitespace(character) && strings.IndexByte(";|(){}[],&<>='\"$`#", character) < 0
	})
}

// powershellArgumentEnd reads a bare word of a command: *.ui, go.exe,
// C:\tools, key=value.
func powershellArgumentEnd(source string, start int) int {
	return scanWhile(source, start, func(character byte) bool {
		return !isWhitespace(character) && strings.IndexByte(";|(){},&<>'\"$`", character) < 0
	})
}
