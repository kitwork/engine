package highlight

import (
	"strings"
	"unicode/utf8"
)

// tokenizeBash reads a shell script the way the shell reads it: word by word,
// knowing whether the next word names a command.
//
//   - The first word of a command is Function (echo, ./deploy.sh, [); its
//     arguments are plain, -x and --long flags Attribute, whole numbers Number.
//     NAME=value before a command (or after export/local/declare/readonly/
//     typeset) is Variable, Operator, value.
//   - Keywords (if then else elif fi for select while until do done case esac
//     function time coproc [[ ]]) count only where a command could start, so
//     `echo done` prints a plain word; `in` only as the third word of for,
//     select and case.
//   - '…' is String as written; "…" and the body of an unquoted heredoc are
//     String with $NAME, ${…}, $(…), $((…)) and `…` inside as Variable; $'…'
//     is String with escapes.
//   - Outside quotes, $( … ) and ( … ) hold commands, read like any others;
//     $(( … )) and (( … )) hold arithmetic: names Variable, numbers Number,
//     symbols Operator.
//   - A heredoc (<<WORD, <<-WORD, <<'WORD') is Operator, its WORD String, and
//     from the next line its body String through the line that is WORD.
//   - | || && ; & ;; and redirections (> >> < 2>&1 &> <<<) are Operator;
//     parentheses and the braces of a group are Punctuation; # starts a Comment
//     only where a word could start.
//
// It is one pass with a few flags and a stack of open parentheses, never a
// recursive call, so nesting costs memory, not stack, and the stack is capped.
func tokenizeBash(scan *scanner) {
	bash := &bashTokenizer{scan: scan, commandPosition: true}
	for loop := scan.loop(); loop.next(); {
		bash.step()
	}
}

// bashRole is what the plain text of the word being read is.
type bashRole uint8

const (
	bashArgument bashRole = iota // an argument, a file name, a pattern
	bashCommand                  // the name of the command (or of a function being defined)
	bashFlag                     // -x, --long: up to an '='
)

func (role bashRole) kind() Kind {
	switch role {
	case bashCommand:
		return Function
	case bashFlag:
		return Attribute
	}
	return Plain
}

// bashFrame is one open parenthesis and what to go back to at its closer.
type bashFrame struct {
	arithmetic    bool     // (( … )), $(( … )): names are variables, symbols operators
	closesTwice   bool     // this frame ends with "))"
	group         bool     // a ( inside arithmetic: its closer restores nothing
	array         bool     // NAME=( … ): the words are values, never commands
	resumeWord    bool     // a word goes on after the closer: $(date)-suffix
	role          bashRole // that word's role
	afterWord     bool     // command position once that word ends
	expectPattern bool     // the state the opener interrupted
	doubleBracket bool
}

// bashMaximumFrames caps the stack of open parentheses; deeper ones are only
// counted, so ten megabytes of "(" cost a counter, not a slice of frames.
const bashMaximumFrames = 1 << 12

// bashHeredoc is a heredoc whose body starts after the current line.
type bashHeredoc struct {
	delimiter string // the closing line, quotes removed
	stripTabs bool   // <<-: the closing line may be indented with tabs
	expands   bool   // the word was not quoted: $NAME in the body expands
}

// bashMaximumHeredocs caps how many heredocs one line may open.
const bashMaximumHeredocs = 1 << 8

type bashTokenizer struct {
	scan *scanner

	commandPosition bool     // the next word names a command
	inWord          bool     // the position is inside a word
	role            bashRole // the role of the word being read
	afterWord       bool     // commandPosition once the current word ends
	fresh           bool     // nothing of the current word (or of a flag's value) read yet
	valueStart      bool     // just after NAME=: a '(' opens an array
	redirectTarget  bool     // the next word is what > or < points at
	expectPattern   bool     // inside case … in: words up to ')' are a pattern
	doubleBracket   bool     // inside [[ … ]]: && and < are tests, not command syntax
	command         string   // the current command's name, for what its arguments mean
	arguments       int      // how many arguments the current command has had

	frames   []bashFrame
	overflow int // frames opened past bashMaximumFrames
	heredocs []bashHeredoc
}

var (
	bashKeywords     = newWordSet("if", "then", "else", "elif", "fi", "case", "esac", "for", "select", "while", "until", "do", "done", "function", "time", "coproc", "[[", "]]")
	bashDeclarations = newWordSet("declare", "local", "export", "readonly", "typeset")

	// bashOperators are the operators made of the bytes that end a word.
	bashOperators = []string{"&&", "&>>", "&>", "&", "||", "|&", "|", ";;&", ";;", ";&", ";",
		"<<<", "<<-", "<<", "<&", "<>", "<(", "<", ">>", ">&", ">|", ">(", ">", "(", ")"}

	bashSingleQuoted   = quoted{closer: "'", multiline: true}
	bashANSIQuoted     = quoted{closer: "'", escape: '\\', multiline: true}
	bashDoubleQuoted   = quoted{closer: `"`, escape: '\\', multiline: true}
	bashBackticks      = quoted{closer: "`", escape: '\\', multiline: true}
	bashExpansionMarks = "$`\\"
	bashStringMarks    = "$`\\\""
)

// step reads one thing: a line break, blanks, a comment, an operator or one
// part of a word.
func (bash *bashTokenizer) step() {
	scan := bash.scan
	source, start := scan.source, scan.position
	if frame := bash.top(); frame != nil && frame.arithmetic {
		bash.arithmetic()
		return
	}
	character := scan.peek()
	switch {
	case character == '\n' || character == '\r':
		bash.endWord()
		scan.emit(Plain, scanNewline(source, start))
		bash.lineBreak()
	case isWhitespace(character):
		bash.endWord()
		scan.emit(Plain, scanWhile(source, start, bashIsBlank))
	case character == '\\' && (scan.peekAt(1) == '\n' || scan.peekAt(1) == '\r'):
		scan.emit(Plain, scanNewline(source, start+1)) // a line continuation changes nothing
	case character == '#' && !bash.inWord:
		scan.emit(Comment, scanToLineEnd(source, start))
	case character == '(' && bash.inWord && bash.valueStart:
		bash.openArray()
	case bashEndsWord(character):
		bash.endWord()
		bash.operator()
	default:
		if !bash.inWord {
			bash.startWord()
		}
		if scan.position == start {
			bash.part()
		}
	}
}

// lineBreak ends a command: the next word names one, unless the line is inside
// an array, and a heredoc opened on the line starts its body here.
func (bash *bashTokenizer) lineBreak() {
	bash.commandPosition = true
	if frame := bash.top(); frame != nil && frame.array {
		bash.commandPosition = false
	}
	bash.redirectTarget, bash.doubleBracket = false, false
	if len(bash.heredocs) > 0 {
		bash.heredocBodies()
	}
}

func (bash *bashTokenizer) endWord() {
	if bash.inWord {
		bash.inWord, bash.valueStart = false, false
		bash.commandPosition = bash.afterWord
	}
}

// startWord decides what the word at the position is, from its first plain
// run (its chunk) and the state, and emits it whole when it is a keyword or a
// mark; otherwise it sets the role its parts are read with.
func (bash *bashTokenizer) startWord() {
	scan := bash.scan
	source, start := scan.source, scan.position
	chunkEnd := bashChunkEnd(source, start)
	chunk := source[start:chunkEnd]
	whole := chunk != "" && (chunkEnd == len(source) || bashEndsWord(source[chunkEnd]))
	bash.inWord, bash.fresh, bash.valueStart = true, true, false
	bash.role, bash.afterWord = bashArgument, bash.commandPosition

	switch next := byteAt(source, chunkEnd); {
	case whole && bashIsDigits(chunk) && (next == '>' || next == '<'):
		scan.emit(Number, chunkEnd) // a descriptor: 2>/dev/null, 2>&1
	case bash.redirectTarget:
		bash.redirectTarget = false
	case bash.doubleBracket:
		bash.afterWord = false
		switch {
		case whole && chunk == "]]":
			scan.emit(Keyword, chunkEnd)
			bash.doubleBracket = false
		case whole && chunk == "!":
			scan.emit(Operator, chunkEnd)
		case bashIsFlag(chunk):
			bash.role = bashFlag
		}
	case bash.expectPattern:
		if whole && chunk == "esac" {
			scan.emit(Keyword, chunkEnd)
			bash.expectPattern, bash.afterWord = false, false
		}
	case bash.commandPosition:
		bash.startCommand(chunk, chunkEnd, whole)
	default:
		bash.startArgument(chunk, chunkEnd, whole)
	}
}

// startCommand reads the word where a command starts: an assignment, a
// keyword, a group's brace, or the command's name.
func (bash *bashTokenizer) startCommand(chunk string, chunkEnd int, whole bool) {
	scan := bash.scan
	if nameEnd, valueStart := bashAssignment(scan.source, scan.position); valueStart > 0 {
		scan.emit(Variable, nameEnd)
		scan.emit(Operator, valueStart)
		bash.afterWord, bash.valueStart = true, true // ALLOW_LOCAL=true go run .
		return
	}
	bash.afterWord = false
	if whole {
		switch {
		case bashKeywords.has(chunk):
			scan.emit(Keyword, chunkEnd)
			bash.keyword(chunk)
			return
		case chunk == "{":
			scan.emit(Punctuation, chunkEnd)
			bash.afterWord = true
			return
		case chunk == "}":
			scan.emit(Punctuation, chunkEnd)
			return
		case chunk == "!":
			scan.emit(Operator, chunkEnd)
			bash.afterWord = true
			return
		}
	}
	bash.role = bashCommand
	bash.command, bash.arguments = chunk, 0
}

// keyword sets what follows a keyword.
func (bash *bashTokenizer) keyword(word string) {
	bash.command, bash.arguments = word, 0
	switch word {
	case "[[":
		bash.doubleBracket = true
	case "]]":
		bash.doubleBracket = false
	case "esac":
		bash.expectPattern = false
	case "if", "then", "else", "elif", "while", "until", "do", "time", "coproc":
		bash.afterWord = true // a command follows
	}
}

// startArgument reads a word after the command's name.
func (bash *bashTokenizer) startArgument(chunk string, chunkEnd int, whole bool) {
	scan := bash.scan
	bash.afterWord = false
	bash.arguments++
	switch bash.command {
	case "for", "select", "case":
		if whole && chunk == "in" && bash.arguments == 2 {
			scan.emit(Keyword, chunkEnd)
			bash.expectPattern = bash.command == "case"
			return
		}
		if whole && bash.arguments == 1 && bash.command != "case" && bashIsName(chunk) {
			scan.emit(Variable, chunkEnd) // for file in …
			return
		}
	case "function":
		if bash.arguments == 1 && chunk != "" {
			bash.role = bashCommand // the name being defined
			return
		}
	case "[":
		if whole && chunk == "]" {
			scan.emit(Punctuation, chunkEnd)
			return
		}
	}
	if bashDeclarations.has(bash.command) {
		if nameEnd, valueStart := bashAssignment(scan.source, scan.position); valueStart > 0 {
			scan.emit(Variable, nameEnd)
			scan.emit(Operator, valueStart)
			bash.valueStart = true
			return
		}
		if whole && bashIsName(chunk) {
			scan.emit(Variable, chunkEnd) // local count
			return
		}
	}
	if whole && (chunk == "{" || chunk == "}") {
		scan.emit(Punctuation, chunkEnd) // function name {
		bash.afterWord = chunk == "{"
		return
	}
	if bashIsFlag(chunk) {
		bash.role = bashFlag
	}
}

// part reads one part of a word: a quoted string, an expansion, an escape or
// a plain run.
func (bash *bashTokenizer) part() {
	scan := bash.scan
	source, start := scan.source, scan.position
	fresh := bash.fresh
	bash.fresh, bash.valueStart = false, false
	switch scan.peek() {
	case '\'':
		scan.emit(String, scanQuoted(source, start+1, bashSingleQuoted))
	case '"':
		scan.emit(String, start+1)
		bash.expandingString(len(source), bashStringMarks)
	case '`':
		scan.emit(Variable, scanQuoted(source, start+1, bashBackticks))
	case '$':
		bash.dollar(bash.role.kind())
	case '\\':
		scan.emit(bash.role.kind(), shellEscapeEnd(source, start))
	default:
		bash.chunk(bashChunkEnd(source, start), fresh)
	}
}

// chunk emits a plain run of a word in the word's role.
func (bash *bashTokenizer) chunk(end int, fresh bool) {
	scan := bash.scan
	source := scan.source
	text := source[scan.position:end]
	switch {
	case bash.role == bashFlag:
		bash.role = bashArgument
		if equals := strings.IndexByte(text, '='); equals > 0 {
			scan.emit(Attribute, scan.position+equals) // --port=8107
			scan.emit(Operator, scan.position+1)
			bash.fresh = true
			return
		}
		scan.emit(Attribute, end)
	case bash.role == bashArgument && fresh && bashIsNumber(text) && (end == len(source) || bashEndsWord(source[end])):
		scan.emit(Number, end)
	default:
		scan.emit(bash.role.kind(), end)
	}
}

// dollar reads what a '$' starts outside double quotes. lone is the kind of a
// '$' that starts nothing.
func (bash *bashTokenizer) dollar(lone Kind) {
	scan := bash.scan
	source, start := scan.source, scan.position
	switch {
	case scan.startsWith("$(("):
		scan.emit(Punctuation, start+3)
		bash.push(bashFrame{arithmetic: true, closesTwice: true})
	case scan.startsWith("$("):
		scan.emit(Punctuation, start+2)
		bash.push(bashFrame{})
		bash.commandPosition = true
	case scan.startsWith("$'"):
		scan.emit(String, scanQuoted(source, start+2, bashANSIQuoted))
	case scan.startsWith(`$"`):
		scan.emit(String, start+2)
		bash.expandingString(len(source), bashStringMarks)
	default:
		if end := bashExpansionEnd(source, start); end > start {
			scan.emit(Variable, end)
		} else {
			scan.emit(lone, start+1)
		}
	}
}

// expandingString emits text from the position as String, each expansion in it
// as Variable, up to limit, or through the first unescaped '"' when marks holds
// one. A literal stretch is emitted only next to an expansion or at the end, so
// the space in " " stays inside the string's token.
func (bash *bashTokenizer) expandingString(limit int, marks string) {
	scan := bash.scan
	source := scan.source[:limit]
	index := scan.position
	for index < limit {
		found := strings.IndexAny(source[index:], marks)
		if found < 0 {
			break
		}
		index += found
		switch source[index] {
		case '\\':
			index = shellEscapeEnd(source, index)
		case '`':
			scan.emit(String, index)
			index = scanQuoted(source, index+1, bashBackticks)
			scan.emit(Variable, index)
		case '$':
			end := bashExpansionEnd(source, index)
			if end == index {
				index++
				continue
			}
			scan.emit(String, index)
			scan.emit(Variable, end)
			index = end
		default: // the closing quote
			scan.emit(String, index+1)
			return
		}
	}
	scan.emit(String, limit)
}

// operator reads an operator made of the bytes that end a word.
func (bash *bashTokenizer) operator() {
	scan := bash.scan
	end := matchLongest(scan.source, scan.position, bashOperators)
	operator := scan.source[scan.position:end]
	switch operator {
	case "(":
		bash.openParenthesis()
	case ")":
		bash.closeParenthesis()
	case "<(", ">(":
		scan.emit(Punctuation, end) // process substitution: a word whose value is a command's output
		bash.inWord, bash.role, bash.afterWord = true, bashArgument, bash.commandPosition
		bash.push(bashFrame{})
		bash.commandPosition = true
	case "<<", "<<-":
		scan.emit(Operator, end)
		bash.heredocDelimiter(operator == "<<-")
	case "|", "||", "&&":
		scan.emit(Operator, end)
		if !bash.expectPattern && !bash.doubleBracket {
			bash.commandPosition = true
		}
	case "&", "|&", ";":
		scan.emit(Operator, end)
		bash.commandPosition, bash.doubleBracket, bash.redirectTarget = true, false, false
	case ";;", ";;&", ";&":
		scan.emit(Operator, end)
		bash.commandPosition, bash.expectPattern, bash.doubleBracket = true, true, false
	case "<", ">":
		scan.emit(Operator, end)
		if !bash.doubleBracket {
			bash.redirectTarget = true
		}
	default: // >> >& <& &> &>> >| <> <<<
		scan.emit(Operator, end)
		bash.redirectTarget = true
	}
}

func (bash *bashTokenizer) openParenthesis() {
	scan := bash.scan
	switch {
	case bash.expectPattern || bash.doubleBracket:
		scan.emitLength(Punctuation, 1) // (a|b) in a pattern, ( … ) in a test
	case scan.startsWith("(("):
		scan.emitLength(Punctuation, 2)
		bash.push(bashFrame{arithmetic: true, closesTwice: true})
	case scan.startsWith("()"):
		scan.emitLength(Punctuation, 2) // name() { … }
		bash.commandPosition = true
	default:
		scan.emitLength(Punctuation, 1)
		bash.push(bashFrame{})
		bash.commandPosition = true
	}
}

func (bash *bashTokenizer) closeParenthesis() {
	bash.scan.emitLength(Punctuation, 1)
	switch {
	case bash.expectPattern:
		bash.expectPattern, bash.commandPosition = false, true // *.localhost) command
	case bash.doubleBracket:
	case bash.overflow > 0 || len(bash.frames) > 0:
		bash.pop()
	default:
		bash.commandPosition = true
	}
}

// openArray reads the '(' of NAME=( … ).
func (bash *bashTokenizer) openArray() {
	bash.scan.emitLength(Punctuation, 1)
	bash.valueStart = false
	bash.push(bashFrame{array: true})
}

// push opens a frame, remembering the word and state it interrupts. The caller
// sets the command position inside.
func (bash *bashTokenizer) push(frame bashFrame) {
	frame.resumeWord, frame.role, frame.afterWord = bash.inWord, bash.role, bash.afterWord
	frame.expectPattern, frame.doubleBracket = bash.expectPattern, bash.doubleBracket
	if len(bash.frames) < bashMaximumFrames {
		bash.frames = append(bash.frames, frame)
	} else {
		bash.overflow++
	}
	bash.inWord, bash.expectPattern, bash.doubleBracket, bash.redirectTarget = false, false, false, false
	bash.commandPosition = false
}

// pop closes the innermost frame and goes back to what it interrupted.
func (bash *bashTokenizer) pop() {
	if bash.overflow > 0 {
		bash.overflow--
		bash.inWord, bash.commandPosition = false, false
		return
	}
	if len(bash.frames) == 0 {
		return
	}
	last := len(bash.frames) - 1
	frame := bash.frames[last]
	bash.frames = bash.frames[:last]
	if frame.group {
		return
	}
	bash.expectPattern, bash.doubleBracket = frame.expectPattern, frame.doubleBracket
	if frame.resumeWord {
		bash.inWord, bash.role, bash.afterWord, bash.fresh = true, frame.role, frame.afterWord, false
		return
	}
	bash.inWord, bash.commandPosition = false, false
}

// top is the innermost frame, or nil. Frames past the cap read as the last
// kept one.
func (bash *bashTokenizer) top() *bashFrame {
	if len(bash.frames) == 0 {
		return nil
	}
	return &bash.frames[len(bash.frames)-1]
}

// arithmetic reads one thing inside (( … )): names are variables, symbols
// operators.
func (bash *bashTokenizer) arithmetic() {
	scan := bash.scan
	source, start := scan.source, scan.position
	character := scan.peek()
	switch {
	case isWhitespace(character):
		scan.emit(Plain, scanWhitespace(source, start))
	case character == ')':
		if frame := bash.top(); bash.overflow == 0 && frame.closesTwice && scan.startsWith("))") {
			scan.emitLength(Punctuation, 2)
		} else {
			scan.emitLength(Punctuation, 1)
		}
		bash.pop()
	case character == '(':
		scan.emitLength(Punctuation, 1)
		if len(bash.frames) < bashMaximumFrames {
			bash.frames = append(bash.frames, bashFrame{arithmetic: true, group: true})
		} else {
			bash.overflow++
		}
	case isDecimalDigit(character):
		scan.emit(Number, scanNumber(source, start))
	case character == '$':
		bash.dollar(Plain)
	case character == '"':
		scan.emit(String, start+1)
		bash.expandingString(len(source), bashStringMarks)
	case character == '\'':
		scan.emit(String, scanQuoted(source, start+1, bashSingleQuoted))
	case character == '`':
		scan.emit(Variable, scanQuoted(source, start+1, bashBackticks))
	case character == '\\':
		scan.emit(Plain, shellEscapeEnd(source, start))
	case character == ';' || character == ',':
		scan.emitLength(Punctuation, 1)
	case strings.IndexByte("+-*/%=!<>&|^~?:", character) >= 0:
		scan.emit(Operator, scanWhile(source, start, func(character byte) bool {
			return strings.IndexByte("+-*/%=!<>&|^~?:", character) >= 0
		}))
	default:
		if end := scanIdentifier(source, start, ""); end > start {
			scan.emit(Variable, end)
		} else {
			scan.emitCharacter(Plain)
		}
	}
}

// heredocDelimiter reads the word after << or <<- and queues the heredoc whose
// body starts on the next line.
func (bash *bashTokenizer) heredocDelimiter(stripTabs bool) {
	scan := bash.scan
	source := scan.source
	wordStart := scanWhile(source, scan.position, func(character byte) bool { return character == ' ' || character == '\t' })
	end := wordStart
word:
	for end < len(source) && !bashEndsWord(source[end]) {
		switch source[end] {
		case '\'':
			end = scanQuoted(source, end+1, quoted{closer: "'"})
		case '"':
			end = scanQuoted(source, end+1, doubleQuoted)
		case '\\':
			if next := byteAt(source, end+1); next == '\n' || next == '\r' {
				break word
			}
			end = shellEscapeEnd(source, end)
		default:
			end++
		}
	}
	if end == wordStart {
		return // << with no word: the operator alone
	}
	scan.emit(Plain, wordStart)
	scan.emit(String, end)
	word := source[wordStart:end]
	var delimiter strings.Builder
	for index := 0; index < len(word); index++ {
		if character := word[index]; character != '\'' && character != '"' && character != '\\' {
			delimiter.WriteByte(character)
		}
	}
	if len(bash.heredocs) < bashMaximumHeredocs {
		bash.heredocs = append(bash.heredocs, bashHeredoc{
			delimiter: delimiter.String(),
			stripTabs: stripTabs,
			expands:   delimiter.Len() == len(word),
		})
	}
}

// heredocBodies reads, right after a line break, the body of each heredoc the
// line opened, in order: String through the line that is its word (to the end
// of the source when no line is), with expansions as Variable when the word
// was not quoted.
func (bash *bashTokenizer) heredocBodies() {
	scan := bash.scan
	source := scan.source
	for index, heredoc := range bash.heredocs {
		if index > 0 {
			scan.emit(Plain, scanNewline(source, scan.position))
		}
		if scan.done() {
			break
		}
		closed := len(source)
		for line := scan.position; line < len(source); {
			end := scanToLineEnd(source, line)
			text := source[line:end]
			if heredoc.stripTabs {
				text = strings.TrimLeft(text, "\t")
			}
			if text == heredoc.delimiter {
				closed = end
				break
			}
			next := scanNewline(source, end)
			if next == end {
				break
			}
			line = next
		}
		if heredoc.expands {
			bash.expandingString(closed, bashExpansionMarks)
		} else {
			scan.emit(String, closed)
		}
	}
	bash.heredocs = bash.heredocs[:0]
}

// bashExpansionEnd is where the expansion a '$' at start opens ends, or start
// when it opens none: ${…}, $(…) and $((…)) to their closer, $NAME, $1, and
// the special $@ $* $# $? $$ $! $-.
func bashExpansionEnd(source string, start int) int {
	if start+1 >= len(source) {
		return start
	}
	switch next := source[start+1]; {
	case next == '{':
		return bashBraceEnd(source, start+2)
	case next == '(':
		return bashSubstitutionEnd(source, start+2)
	case next == '_' || isASCIILetter(next):
		return scanWhile(source, start+1, bashIsNameByte)
	case isDecimalDigit(next) || strings.IndexByte("@*#?$!-", next) >= 0:
		return start + 2
	}
	return start
}

// bashBraceEnd reads ${…} from just inside the brace, nested braces counted,
// through the closer; an unclosed one stops before the line break.
func bashBraceEnd(source string, index int) int {
	depth := 1
	for index < len(source) {
		switch source[index] {
		case '\\':
			index = shellEscapeEnd(source, index)
			continue
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return index + 1
			}
		case '\n', '\r':
			return index
		}
		index++
	}
	return len(source)
}

// bashSubstitutionEnd reads $(…) from just inside the parenthesis through its
// closer: nested parentheses counted, quoted text skipped whole. One pass.
func bashSubstitutionEnd(source string, index int) int {
	depth := 1
	for index < len(source) {
		switch source[index] {
		case '\\':
			index = shellEscapeEnd(source, index)
			continue
		case '\'':
			index = scanQuoted(source, index+1, bashSingleQuoted)
			continue
		case '"':
			index = scanQuoted(source, index+1, bashDoubleQuoted)
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

// shellEscapeEnd is where an escape at index ends: the escape byte and the
// whole character after it (CRLF counts as one). Bash escapes with '\',
// PowerShell with '`'.
func shellEscapeEnd(source string, index int) int {
	next := index + 1
	if next >= len(source) {
		return len(source)
	}
	if strings.HasPrefix(source[next:], "\r\n") {
		return next + 2
	}
	_, size := utf8.DecodeRuneInString(source[next:])
	return next + size
}

// bashChunkEnd reads a plain run of a word: up to whatever ends the word or
// starts a quote, an expansion or an escape.
func bashChunkEnd(source string, start int) int {
	index := start
	for index < len(source) && !bashEndsWord(source[index]) && strings.IndexByte(`'"$`+"`\\", source[index]) < 0 {
		index++
	}
	return index
}

// bashEndsWord reports whether a byte ends an unquoted word: whitespace or one
// of the bytes operators are made of.
func bashEndsWord(character byte) bool {
	return isWhitespace(character) || strings.IndexByte("|&;<>()", character) >= 0
}

// bashIsBlank is whitespace that is not a line break.
func bashIsBlank(character byte) bool {
	return isWhitespace(character) && character != '\n' && character != '\r'
}

func bashIsNameByte(character byte) bool {
	return isASCIILetter(character) || isDecimalDigit(character) || character == '_'
}

// bashIsName reports whether text is a shell variable name.
func bashIsName(text string) bool {
	return text != "" && !isDecimalDigit(text[0]) && scanWhile(text, 0, bashIsNameByte) == len(text)
}

// bashAssignment reads NAME=, NAME+= or NAME[subscript]= at start: where the
// name (with its subscript) ends and where the value starts, or zeros when
// there is none.
func bashAssignment(source string, start int) (nameEnd, valueStart int) {
	if character := byteAt(source, start); !isASCIILetter(character) && character != '_' {
		return 0, 0
	}
	nameEnd = scanWhile(source, start, bashIsNameByte)
	if byteAt(source, nameEnd) == '[' {
		if nameEnd = bashSubscriptEnd(source, nameEnd+1); nameEnd < 0 {
			return 0, 0
		}
	}
	switch {
	case byteAt(source, nameEnd) == '=':
		return nameEnd, nameEnd + 1
	case byteAt(source, nameEnd) == '+' && byteAt(source, nameEnd+1) == '=':
		return nameEnd, nameEnd + 2
	}
	return 0, 0
}

// bashSubscriptEnd reads an array subscript from just inside its '[' through
// the ']', quoted text skipped whole; -1 when the word or the line ends first.
func bashSubscriptEnd(source string, index int) int {
	for index < len(source) {
		switch character := source[index]; {
		case character == ']':
			return index + 1
		case character == '\'':
			index = scanQuoted(source, index+1, quoted{closer: "'"})
		case character == '"':
			index = scanQuoted(source, index+1, doubleQuoted)
		case character == '\\':
			index = shellEscapeEnd(source, index)
		case bashEndsWord(character):
			return -1
		default:
			index++
		}
	}
	return -1
}

// bashIsFlag reports whether a word's first run is an option: -x, --long.
func bashIsFlag(chunk string) bool { return len(chunk) > 1 && chunk[0] == '-' }

func bashIsDigits(text string) bool {
	return text != "" && scanWhile(text, 0, isDecimalDigit) == len(text)
}

// bashIsNumber reports whether a word is a decimal number: 8107, 1.5.
func bashIsNumber(text string) bool {
	whole := scanWhile(text, 0, isDecimalDigit)
	if whole == 0 {
		return false
	}
	if whole < len(text) && text[whole] == '.' {
		fraction := scanWhile(text, whole+1, isDecimalDigit)
		return fraction > whole+1 && fraction == len(text)
	}
	return whole == len(text)
}
