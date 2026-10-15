package highlight

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokenizeJavaScript reads JavaScript. One scanner reads javascript, typescript
// and work; a dialect says which words mean what (javascriptDialect below).
//
// What it tells apart, and how, all in one forward pass:
//
//   - Template literals: the text is a string, "${" and its "}" are
//     punctuation, and the code between them is read as code, nested to any
//     depth. Nesting is a stack of open brackets, never a recursive call.
//   - A '/' starts a regular expression or divides, decided by the token before
//     it: after a name, a number, a string, ')' or ']' it divides; after an
//     operator, '(' ',' '{' ';' or a keyword (except this, super) it opens a
//     regular expression. An unterminated one stops at the line break.
//   - function: a name followed by '(' (spaces allowed), by "?.(" or by a
//     template literal (a tagged template), and the name after `function`.
//   - type: a name declared by class (and in typescript interface, enum, type);
//     a name starting with a capital letter after new, extends, instanceof or
//     implements; and the built-in constructors and namespaces (Math, Promise).
//     A capitalised name anywhere else stays plain: without the keyword before
//     it, Repository.count is a value as often as a class.
//   - property: an object key (a name or a quoted string before ':' right after
//     '{' or ','), a name after '.' or "?." that is not called, a private #name,
//     and in a class (interface, enum) body a member name before ':' '=' or ';'.
//   - constant: true, false, null, undefined, NaN, Infinity.
//   - variable: names the host provides (console, window; router, env, ctx in
//     work). A name nobody declared for the host stays plain, and so does a
//     host's name once the code declares it with const, let or var.
//   - Contextual words (of, from, as, async, get, set, static, and typescript's
//     type, readonly, declare…) are keywords only where an operand or a name
//     follows them, so { type: 1 }, x.of and const get = … stay what they are.
func tokenizeJavaScript(scan *scanner) { tokenizeJavaScriptDialect(scan, &dialectJavaScript) }

// javascriptDialect is what sets javascript, typescript and work apart.
type javascriptDialect struct {
	keywords      wordSet // keywords wherever they stand, except after a '.'
	beforeOperand wordSet // keywords only when an operand follows: of, from, as, async
	beforeName    wordSet // keywords only when a name, bracket or quote follows: get, static, type
	modifiers     wordSet // words after which a class or interface member's name still comes
	reserved      wordSet // words the language rejects: always plain, never a call or a key
	constants     wordSet
	types         wordSet // built-in constructors and namespaces
	hostNames     wordSet // names the host provides, coloured as variables
	typed         bool    // typescript: annotations, type declarations, generic arguments
	subset        bool    // work: no regular-expression literals, #private names or @decorators
}

var (
	javascriptKeywordList = []string{
		"await", "break", "case", "catch", "class", "const", "continue", "debugger", "default",
		"delete", "do", "else", "enum", "export", "extends", "finally", "for", "function", "if",
		"import", "in", "instanceof", "let", "new", "return", "super", "switch", "this", "throw",
		"try", "typeof", "var", "void", "while", "with", "yield",
	}
	javascriptBeforeOperandList = []string{"as", "async", "from", "of"}
	javascriptBeforeNameList    = []string{"accessor", "get", "set", "static"}
	javascriptModifierList      = []string{"accessor", "static"}
	javascriptConstantList      = []string{"false", "Infinity", "NaN", "null", "true", "undefined"}
	javascriptTypeList          = []string{
		"AbortController", "Array", "ArrayBuffer", "BigInt", "BigInt64Array", "BigUint64Array", "Blob",
		"Boolean", "CustomEvent", "DataView", "Date", "Document", "Element", "Error", "EvalError",
		"Event", "EventTarget", "File", "Float32Array", "Float64Array", "FormData", "Function",
		"Headers", "HTMLElement", "Int16Array", "Int32Array", "Int8Array", "Intl", "JSON", "Map",
		"Math", "Node", "Number", "Object", "Promise", "Proxy", "RangeError", "ReferenceError",
		"Reflect", "RegExp", "Request", "Response", "Set", "SharedArrayBuffer", "String", "Symbol",
		"SyntaxError", "TextDecoder", "TextEncoder", "TypeError", "Uint16Array", "Uint32Array",
		"Uint8Array", "Uint8ClampedArray", "URIError", "URL", "URLSearchParams", "WeakMap",
		"WeakRef", "WeakSet", "Window", "Worker",
	}
	javascriptHostNameList = []string{
		"console", "document", "globalThis", "localStorage", "navigator", "sessionStorage", "window",
	}
)

var dialectJavaScript = javascriptDialectLists{
	keywords:      javascriptKeywordList,
	beforeOperand: javascriptBeforeOperandList,
	beforeName:    javascriptBeforeNameList,
	modifiers:     javascriptModifierList,
	constants:     javascriptConstantList,
	types:         javascriptTypeList,
	hostNames:     javascriptHostNameList,
}.dialect()

// javascriptDialectLists is a dialect written as word lists, so typescript can
// extend javascript's lists instead of repeating them.
type javascriptDialectLists struct {
	keywords, beforeOperand, beforeName, modifiers, reserved, constants, types, hostNames []string
	typed, subset                                                                         bool
}

func (lists javascriptDialectLists) dialect() javascriptDialect {
	return javascriptDialect{
		keywords:      newWordSet(lists.keywords...),
		beforeOperand: newWordSet(lists.beforeOperand...),
		beforeName:    newWordSet(lists.beforeName...),
		modifiers:     newWordSet(lists.modifiers...),
		reserved:      newWordSet(lists.reserved...),
		constants:     newWordSet(lists.constants...),
		types:         newWordSet(lists.types...),
		hostNames:     newWordSet(lists.hostNames...),
		typed:         lists.typed,
		subset:        lists.subset,
	}
}

// javascriptOperators are matched longest first by matchLongest. '/', '?' and
// '.' are read on their own (regular expressions, ?. and ?:, spread).
var javascriptOperators = []string{
	">>>=", "===", "!==", "**=", "<<=", ">>=", ">>>", "&&=", "||=",
	"=>", "==", "!=", "<=", ">=", "&&", "||", "++", "--", "+=", "-=", "*=", "%=", "&=", "|=", "^=", "**", "<<", ">>",
	"=", "<", ">", "+", "-", "*", "%", "&", "|", "^", "!", "~",
}

// javascriptMaximumBracketDepth bounds the bracket stack. Openers past it are only
// counted, so ten megabytes of "(" cost a counter, not a slice of ten million.
const javascriptMaximumBracketDepth = 1024

// javascriptBracket is one open bracket.
type javascriptBracket struct {
	opener      byte  // '(' '[' '{', '$' for the "${" of a template, 0 for the top level
	members     bool  // a '{' holding class, interface, enum or type-literal members
	resumeType  bool  // a type literal's '{': closing it goes back to reading that type
	typeNesting int32 // the type nesting to go back to
	ternaries   int32 // '?' at this level still waiting for their ':'
}

// javascriptState is one tokenizer run. The last* fields describe the last
// token that is neither whitespace nor a comment: that is what decides whether
// a '/' divides and whether a name is a key.
type javascriptState struct {
	scan    *scanner
	dialect *javascriptDialect
	source  string

	brackets []javascriptBracket // brackets[0] is the top level and never closes
	overflow int                 // openers past javascriptMaximumBracketDepth, not on the stack

	lastKind  Kind
	lastText  string // "" before the first token
	lineBreak bool   // a line break since the last token

	declared    bool            // the last token is a name just declared by const, let or var
	shadowed    map[string]bool // host names the code declared as its own: plain from there on
	optional    bool            // the last token is a '?' marking an optional member or parameter
	bodyPending bool            // class, interface or enum seen: the next '{' at bodyLevel holds members
	bodyLevel   int

	// typescript only: reading a type (typescript.go).
	inType         bool
	typeWantsTerm  bool  // a type name, literal or opening bracket comes next
	typeNesting    int32 // '<' '(' '[' opened inside this type
	typeAlias      bool  // `type Name` waits for its '='
	typeList       bool  // after extends or implements a ',' continues the type
	typeGroupEnded bool  // the last token closed a '(' of the type: '=>' continues it
}

// tokenizeJavaScriptDialect reads source as one dialect of JavaScript.
func tokenizeJavaScriptDialect(scan *scanner, dialect *javascriptDialect) {
	state := &javascriptState{
		scan:      scan,
		dialect:   dialect,
		source:    scan.source,
		brackets:  make([]javascriptBracket, 1, 16),
		lineBreak: true,
	}
	for loop := scan.loop(); loop.next(); {
		state.step()
	}
}

// token emits up to end as kind and remembers it as the last token.
func (state *javascriptState) token(kind Kind, end int) {
	start := state.scan.position
	state.scan.emit(kind, end)
	if state.scan.position == start {
		return
	}
	state.lastKind, state.lastText = kind, state.source[start:state.scan.position]
	state.lineBreak, state.declared, state.optional, state.typeGroupEnded = false, false, false, false
}

func (state *javascriptState) top() *javascriptBracket {
	return &state.brackets[len(state.brackets)-1]
}

func (state *javascriptState) step() {
	scan, source, position := state.scan, state.source, state.scan.position
	character, next := scan.peek(), scan.peekAt(1)
	switch {
	case isWhitespace(character):
		end := scanWhitespace(source, position)
		breaks := strings.ContainsAny(source[position:end], "\n\r")
		scan.emit(Plain, end)
		if breaks {
			state.lineBreak = true
			if state.inType && !state.typeContinuesOnNextLine() {
				state.inType = false
			}
		}
		return
	case character == '/' && next == '/':
		scan.emit(Comment, scanToLineEnd(source, position))
		return
	case character == '/' && next == '*':
		scan.emit(Comment, scanBlockComment(source, position, "/*", "*/"))
		return
	case character == '#' && next == '!' && position == 0 && scan.base == 0:
		scan.emit(Comment, scanToLineEnd(source, position)) // #!/usr/bin/env node
		return
	}
	if state.inType {
		if state.typeStep() {
			return
		}
		state.inType = false
	}
	state.codeStep()
}

func (state *javascriptState) codeStep() {
	source, position := state.source, state.scan.position
	character, next := state.scan.peek(), state.scan.peekAt(1)
	switch {
	case character == '"' || character == '\'':
		rules := doubleQuoted
		if character == '\'' {
			rules = singleQuoted
		}
		end := scanQuoted(source, position+1, rules)
		kind := String
		if state.keyPosition(end) {
			kind = Property
		}
		state.token(kind, end)
	case character == '`':
		state.templateText(position + 1)
	case isDecimalDigit(character) || (character == '.' && isDecimalDigit(next)):
		state.token(Number, scanNumber(source, position))
	case (character == '#' || character == '@') && !state.dialect.subset:
		// #private names and @decorators.
		end := scanIdentifier(source, position+1, "$")
		if end == position+1 {
			state.token(Plain, position+1)
			return
		}
		kind := Property
		if character == '@' || state.callFollows(end) {
			kind = Function
		}
		state.token(kind, end)
	case character == '/':
		if !state.dialect.subset && state.regularExpressionAllowed() {
			state.token(RegularExpression, scanJavaScriptRegularExpression(source, position+1))
			return
		}
		end := position + 1
		if next == '=' {
			end++
		}
		state.token(Operator, end)
	default:
		if end := scanIdentifier(source, position, "$"); end > position {
			state.word(end)
			return
		}
		state.punctuationOrOperator()
	}
}

// templateText emits template text from the position (a backtick, or the text
// after a "}" that closed a "${") up to the closing backtick, or up to a "${"
// which it emits and opens.
func (state *javascriptState) templateText(from int) {
	end, interpolation := scanJavaScriptTemplateText(state.source, from)
	state.token(String, end)
	if interpolation {
		state.token(Punctuation, end+2)
		state.open(javascriptBracket{opener: '$'})
	}
}

// scanJavaScriptTemplateText reads template text from start: through the closing
// backtick (false), or up to a "${" (true), or to the end of the source.
func scanJavaScriptTemplateText(source string, start int) (int, bool) {
	for index := clampStart(source, start); index < len(source); index++ {
		switch source[index] {
		case '\\':
			index++ // the escaped byte is text: a backtick, a '$', a line break
		case '`':
			return index + 1, false
		case '$':
			if byteAt(source, index+1) == '{' {
				return index, true
			}
		}
	}
	return len(source), false
}

// scanJavaScriptRegularExpression reads a regular-expression literal whose '/' ends just
// before start: through the closing '/' (one inside [...] does not close it)
// and its flags, or up to the line break when it never closes.
func scanJavaScriptRegularExpression(source string, start int) int {
	inClass := false
	for index := clampStart(source, start); index < len(source); index++ {
		switch source[index] {
		case '\\':
			if next := byteAt(source, index+1); next == '\n' || next == '\r' {
				return index + 1
			}
			index++
		case '\n', '\r':
			return index
		case '[':
			inClass = true
		case ']':
			inClass = false
		case '/':
			if !inClass {
				return scanWhile(source, index+1, isASCIILetter)
			}
		}
	}
	return len(source)
}

// regularExpressionAllowed reports whether a '/' here opens a regular
// expression rather than dividing, from the token before it.
func (state *javascriptState) regularExpressionAllowed() bool {
	switch state.lastKind {
	case Plain:
		return state.lastText == ""
	case Keyword:
		return state.lastText != "this" && state.lastText != "super"
	case Operator:
		return state.lastText != "++" && state.lastText != "--"
	case Punctuation:
		return state.lastText != ")" && state.lastText != "]"
	}
	return false
}

// word reads a name, a keyword or a literal word ending at end.
func (state *javascriptState) word(end int) {
	text := state.source[state.scan.position:end]
	dialect := state.dialect
	if (state.lastKind == Punctuation && state.lastText == ".") || (state.lastKind == Operator && state.lastText == "?.") {
		kind := Property
		if state.callFollows(end) {
			kind = Function
		}
		state.token(kind, end)
		return
	}
	switch {
	case state.objectKey(end) && !(text == "default" && state.lastText == "{"):
		// Any word is a key before ':' in an object: { class: "x", type: 1 }.
		// Not `default:` first in braces, which is a switch's far more often.
		state.token(Property, end)
		return
	case dialect.reserved.has(text):
		state.token(Plain, end)
		return
	case dialect.keywords.has(text),
		dialect.beforeOperand.has(text) && state.beforeOperand(end),
		dialect.beforeName.has(text) && state.beforeName(end):
		state.keyword(text, end)
		return
	case dialect.constants.has(text):
		state.token(Constant, end)
		return
	}

	previousKeyword := ""
	if state.lastKind == Keyword {
		previousKeyword = state.lastText
	}
	member := state.top().members && state.memberStart()
	genericFollows := dialect.typed && byteAt(state.source, end) == '<'
	switch {
	case state.keyPosition(end):
		state.token(Property, end)
	case previousKeyword == "class" || previousKeyword == "interface" || previousKeyword == "enum" || previousKeyword == "type":
		// A type being declared. In typescript its generic parameters, its
		// heritage and a type alias's right side are read as a type.
		state.token(Type, end)
		if dialect.typed {
			state.enterType(false)
			state.typeAlias = previousKeyword == "type"
		}
	case (previousKeyword == "new" || previousKeyword == "extends" || previousKeyword == "instanceof" || previousKeyword == "implements") && javascriptCapitalised(text),
		dialect.types.has(text):
		state.token(Type, end)
		if genericFollows {
			state.enterType(false) // new Map<string, Item>()
		}
	case previousKeyword == "function", state.callFollows(end), member && genericFollows:
		state.token(Function, end)
		if genericFollows {
			state.enterType(false) // function identity<T>(…), find<Key>(…) in a class
		}
	case dialect.hostNames.has(text) && state.hostName(text, previousKeyword):
		state.token(Variable, end)
	default:
		state.token(Plain, end)
	}
	if previousKeyword == "const" || previousKeyword == "let" || previousKeyword == "var" {
		state.declared = true
	}
}

// hostName reports whether a host's name stands for the host here. A name the
// code declares with that spelling (const path = …, for (const file of …)) is a
// local, there and from then on. The set holds host names only, so it stays as
// small as the dialect's list.
func (state *javascriptState) hostName(text, previousKeyword string) bool {
	if previousKeyword == "const" || previousKeyword == "let" || previousKeyword == "var" {
		if state.shadowed == nil {
			state.shadowed = map[string]bool{}
		}
		state.shadowed[text] = true
		return false
	}
	return !state.shadowed[text]
}

// keyword emits a keyword and notes what it opens.
func (state *javascriptState) keyword(text string, end int) {
	state.token(Keyword, end)
	switch text {
	case "class", "interface", "enum":
		state.bodyPending, state.bodyLevel = true, len(state.brackets)
	}
	if state.dialect.typed {
		switch text {
		case "as", "satisfies":
			state.enterType(true)
		case "extends", "implements":
			state.enterType(true)
			state.typeList = true
		}
	}
}

// beforeOperand: of, from, as, async… are keywords when an operand follows,
// not when the word is itself a value: { of: 1 }, as = 2, f(from), of(x).
func (state *javascriptState) beforeOperand(end int) bool {
	if byteAt(state.source, end) == '(' {
		return false
	}
	next := javascriptSkipSpaces(state.source, end)
	if next >= len(state.source) {
		return false
	}
	switch state.source[next] {
	case ':', '=', ',', ')', ';', ']', '}', '.', '?', '\n', '\r':
		return false
	}
	return true
}

// beforeName: get, set, static, type, readonly… are keywords when a name, a
// bracket or a quote follows on the same line: get size(), static {,
// type Money =, declare module "x". Not in get(), type = 1 or { type: 1 }.
func (state *javascriptState) beforeName(end int) bool {
	next := javascriptSkipSpaces(state.source, end)
	if next == end || next >= len(state.source) {
		return false
	}
	character := state.source[next]
	return javascriptNameStart(character) || strings.IndexByte("[#*{\"'", character) >= 0
}

// keyPosition reports whether a name or string ending at end is a key: before
// ':' right after '{' or ',' in braces, or a member name in a class,
// interface or enum body.
func (state *javascriptState) keyPosition(end int) bool {
	top := state.top()
	if top.opener != '{' {
		return false
	}
	colon, character, after := state.colonFollows(end)
	if colon && state.lastKind == Punctuation && (state.lastText == "{" || state.lastText == ",") {
		return true
	}
	if !top.members || !state.memberStart() {
		return false
	}
	switch {
	case colon, character == ';', character == ',', character == '}':
		return true
	case character == '=':
		return after != '=' && after != '>'
	}
	return false
}

// objectKey reports whether a word ending at end is an object's key: before
// ':' right after '{' or ','.
func (state *javascriptState) objectKey(end int) bool {
	if state.top().opener != '{' || state.lastKind != Punctuation || (state.lastText != "{" && state.lastText != ",") {
		return false
	}
	colon, _, _ := state.colonFollows(end)
	return colon
}

// colonFollows reports whether a key's ':' follows end (spaces allowed; in
// typescript also "?:" and "!:"), with the two bytes found there.
func (state *javascriptState) colonFollows(end int) (bool, byte, byte) {
	next := javascriptSkipSpaces(state.source, end)
	character, after := byteAt(state.source, next), byteAt(state.source, next+1)
	colon := character == ':' && after != ':'
	if state.dialect.typed && (character == '?' || character == '!') && after == ':' {
		colon = true // name?: T, name!: T
	}
	return colon, character, after
}

// memberStart reports whether a member of a class or interface body can start
// here: first in the body, after ';' '}' ',' or a line break, or after a
// modifier such as static or readonly.
func (state *javascriptState) memberStart() bool {
	if state.lineBreak {
		return true
	}
	switch state.lastKind {
	case Punctuation:
		switch state.lastText {
		case "{", ";", "}", ",":
			return true
		}
	case Keyword:
		return state.dialect.modifiers.has(state.lastText)
	}
	return false
}

// callFollows reports whether a name ending at end is called: f(…), f (…),
// f?.(…), or a tagged template f`…`.
func (state *javascriptState) callFollows(end int) bool {
	source := state.source
	switch byteAt(source, end) {
	case '`':
		return true
	case '?':
		return byteAt(source, end+1) == '.' && byteAt(source, end+2) == '('
	}
	return byteAt(source, javascriptSkipSpaces(source, end)) == '('
}

func (state *javascriptState) punctuationOrOperator() {
	source, position := state.source, state.scan.position
	character := state.scan.peek()
	switch character {
	case '(', '[':
		state.open(javascriptBracket{opener: character})
		state.token(Punctuation, position+1)
	case '{':
		bracket := javascriptBracket{opener: '{'}
		if state.bodyPending && len(state.brackets) == state.bodyLevel {
			bracket.members, state.bodyPending = true, false
		}
		state.open(bracket)
		state.token(Punctuation, position+1)
	case ')', ']':
		state.closeRound(character)
		state.token(Punctuation, position+1)
	case '}':
		state.closeBrace()
	case ';':
		state.top().ternaries = 0
		if state.bodyPending && len(state.brackets) == state.bodyLevel {
			state.bodyPending = false
		}
		state.token(Punctuation, position+1)
	case ',':
		state.token(Punctuation, position+1)
	case ':':
		state.colon()
	case '?':
		state.question()
	case '.':
		if strings.HasPrefix(source[position:], "...") {
			state.token(Operator, position+3)
			return
		}
		state.token(Punctuation, position+1)
	default:
		if strings.IndexByte(javascriptOperatorStarts, character) >= 0 {
			state.token(Operator, matchLongest(source, position, javascriptOperators))
			return
		}
		// Anything else (a stray backslash, a NUL, bytes that are not UTF-8)
		// is one plain character, without trying every operator on it.
		_, size := utf8.DecodeRuneInString(source[position:])
		state.token(Plain, position+size)
	}
}

// javascriptOperatorStarts are the bytes a javascriptOperators entry starts with.
const javascriptOperatorStarts = "=<>+-*%&|^!~"

// open pushes a bracket, or only counts it past javascriptMaximumBracketDepth.
func (state *javascriptState) open(bracket javascriptBracket) {
	if state.overflow > 0 || len(state.brackets) > javascriptMaximumBracketDepth {
		state.overflow++
		return
	}
	state.brackets = append(state.brackets, bracket)
}

// closeRound closes a ')' or ']' when it matches the innermost bracket. A
// stray one closes nothing.
func (state *javascriptState) closeRound(closer byte) {
	if state.overflow > 0 {
		state.overflow--
		return
	}
	opener := byte('(')
	if closer == ']' {
		opener = '['
	}
	if last := len(state.brackets) - 1; last > 0 && state.brackets[last].opener == opener {
		state.brackets = state.brackets[:last]
	}
}

// closeBrace emits a '}' and closes the innermost brace, the parentheses left
// open inside it included. Closing a "${" goes back to its template's text;
// closing a type literal goes back to reading its type.
func (state *javascriptState) closeBrace() {
	position := state.scan.position
	if state.overflow > 0 {
		state.overflow--
		state.token(Punctuation, position+1)
		return
	}
	last := len(state.brackets) - 1
	for last > 0 && (state.brackets[last].opener == '(' || state.brackets[last].opener == '[') {
		last--
	}
	closed := state.brackets[last]
	if last > 0 {
		state.brackets = state.brackets[:last]
	} else {
		state.brackets = state.brackets[:1]
	}
	state.token(Punctuation, position+1)
	switch {
	case last > 0 && closed.opener == '$':
		state.templateText(state.scan.position)
	case last > 0 && closed.resumeType:
		state.enterType(false)
		state.typeNesting = closed.typeNesting
	}
}

// colon emits a ':' and decides what it is: the ':' of a ternary, a key's ':',
// or in typescript a type annotation, after which a type is read.
func (state *javascriptState) colon() {
	position := state.scan.position
	top := state.top()
	if top.ternaries > 0 {
		top.ternaries--
		state.token(Punctuation, position+1)
		return
	}
	annotation := state.dialect.typed && state.annotates()
	state.token(Punctuation, position+1)
	if annotation {
		state.enterType(true)
	}
}

// annotates reports whether a ':' that is not a ternary's starts a type
// annotation: after an optional '?', after a name const or let declares, inside
// parentheses (parameters), in a member body or its index signature, or after
// ')' (a return type) or '}' (a destructured parameter).
func (state *javascriptState) annotates() bool {
	if state.optional || state.declared {
		return true
	}
	if top := state.top(); top.opener == '(' || top.members {
		return true
	}
	if count := len(state.brackets); state.top().opener == '[' && count > 1 && state.brackets[count-2].members {
		return true // an index signature: [key: string]: T
	}
	return state.lastKind == Punctuation && (state.lastText == ")" || state.lastText == "}")
}

// question reads "??", "??=", "?." and a lone '?': a ternary's, or in
// typescript the mark of an optional member or parameter (a?: T, (a?) =>).
func (state *javascriptState) question() {
	source, position := state.source, state.scan.position
	next, after := byteAt(source, position+1), byteAt(source, position+2)
	switch {
	case next == '?':
		end := position + 2
		if after == '=' {
			end++
		}
		state.token(Operator, end)
	case next == '.' && !isDecimalDigit(after): // a?.5:b is a ternary
		state.token(Operator, position+2)
	case state.dialect.typed && strings.IndexByte(":),;", next) >= 0:
		state.token(Operator, position+1)
		state.optional = true
	default:
		state.top().ternaries++
		state.token(Operator, position+1)
	}
}

// javascriptSkipSpaces reads spaces and tabs, not line breaks: what follows a name on
// its own line decides what the name is.
func javascriptSkipSpaces(source string, start int) int {
	return scanWhile(source, start, func(character byte) bool { return character == ' ' || character == '\t' })
}

// javascriptNameStart reports whether a byte can start a name: an ASCII letter, '_',
// '$', or the first byte of a character outside ASCII.
func javascriptNameStart(character byte) bool {
	return isASCIILetter(character) || character == '_' || character == '$' || character >= utf8.RuneSelf
}

// javascriptCapitalised reports whether a name starts with a capital letter.
func javascriptCapitalised(name string) bool {
	first, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(first)
}
