package highlight

import "strings"

// tokenizeTypeScript reads TypeScript: javascript (javascript.go) plus its
// declarations and the types written in the code.
//
// Keywords: interface, implements, enum, private, protected, public always;
// type, readonly, declare, abstract, override, namespace, module, global when a
// name follows; satisfies and as when an operand follows; keyof, typeof,
// infer, unique, asserts, is inside a type.
//
// A type is read, "where simple", after:
//   - an annotation ':' — of a parameter (inside parentheses), of a member (in
//     a class, interface or type-literal body), of a const or let name, of a
//     return (after ')'), or after an optional '?';
//   - as, satisfies, extends, implements;
//   - a declared type name (class, interface, enum, type): its generic
//     parameters, and a type alias's right side after '=';
//   - a type name directly followed by '<' (new Map<string, Item>()).
//
// Inside a type, names are types (string, Drink, Promise), null/undefined/
// true/false are constants, string and number literal types keep their kind,
// a parameter or tuple label before ':' stays plain, and the names of a type
// literal { id: string } are properties whose types are read in turn. The type
// ends where a type cannot go on: '=', ';', ',' or ')' outside its own
// brackets, a '{' after a complete type (a function body), a name after a
// complete type, or a line break after a complete type unless the next line
// starts with '|' or '&'. Generic calls (f<T>(x)), conditional types and
// mapped types are not followed; their names stay plain or end the type.
func tokenizeTypeScript(scan *scanner) { tokenizeJavaScriptDialect(scan, &dialectTypeScript) }

var dialectTypeScript = javascriptDialectLists{
	keywords:      javascriptWordsAnd(javascriptKeywordList, "implements", "interface", "private", "protected", "public"),
	beforeOperand: javascriptWordsAnd(javascriptBeforeOperandList, "satisfies"),
	beforeName: javascriptWordsAnd(javascriptBeforeNameList,
		"abstract", "declare", "global", "module", "namespace", "override", "readonly", "type"),
	modifiers: javascriptWordsAnd(javascriptModifierList,
		"abstract", "declare", "override", "private", "protected", "public", "readonly"),
	constants: javascriptConstantList,
	types: javascriptWordsAnd(javascriptTypeList,
		"ArrayLike", "Awaited", "Capitalize", "ConstructorParameters", "Exclude", "Extract",
		"InstanceType", "Iterable", "IterableIterator", "Iterator", "Lowercase", "NonNullable",
		"Omit", "Parameters", "Partial", "Pick", "PromiseLike", "PropertyKey", "Readonly",
		"ReadonlyArray", "Record", "Required", "ReturnType", "ThisType", "Uncapitalize", "Uppercase"),
	hostNames: javascriptHostNameList,
	typed:     true,
}.dialect()

// typescriptTypeOperators are the keywords a type can start with: keyof T,
// readonly T[], typeof value, infer U, unique symbol, asserts x, new () => T,
// abstract new () => T, and the const of `as const`.
var typescriptTypeOperators = newWordSet("abstract", "asserts", "const", "infer", "keyof", "new", "readonly", "typeof", "unique")

// javascriptWordsAnd is a word list followed by more words, as a new list.
func javascriptWordsAnd(list []string, more ...string) []string {
	return append(append(make([]string, 0, len(list)+len(more)), list...), more...)
}

// enterType starts reading a type. wantsTerm: a type comes next (after ':' or
// as); otherwise one was just read (a declared name, before its '<' or '=').
func (state *javascriptState) enterType(wantsTerm bool) {
	state.inType, state.typeWantsTerm, state.typeNesting = true, wantsTerm, 0
	state.typeAlias, state.typeList, state.typeGroupEnded = false, false, false
}

// typeToken emits a token of a type and says whether another term must follow.
func (state *javascriptState) typeToken(kind Kind, end int, wantsTerm bool) {
	state.token(kind, end)
	state.typeWantsTerm = wantsTerm
}

// typeContinuesOnNextLine reports whether a type survives the line break just
// read: it still wants a term, it is inside its own brackets, or the next line
// starts with '|' or '&' (a union written one member per line).
func (state *javascriptState) typeContinuesOnNextLine() bool {
	if state.typeWantsTerm || state.typeNesting > 0 {
		return true
	}
	character, next := state.scan.peek(), state.scan.peekAt(1)
	return (character == '|' || character == '&') && next != character
}

// typeStep reads one token of a type, or reports false when the type has
// ended here; the code reader then reads the same position.
func (state *javascriptState) typeStep() bool {
	source, position := state.source, state.scan.position
	character, next := state.scan.peek(), state.scan.peekAt(1)
	wants := state.typeWantsTerm
	switch {
	case character == '"' || character == '\'':
		if !wants {
			return false
		}
		rules := doubleQuoted
		if character == '\'' {
			rules = singleQuoted
		}
		state.typeToken(String, scanQuoted(source, position+1, rules), false)
	case isDecimalDigit(character) || (character == '-' && isDecimalDigit(next)):
		if !wants {
			return false
		}
		start := position
		if character == '-' {
			start++
		}
		state.typeToken(Number, scanNumber(source, start), false)
	case character == '<':
		if next == '=' || next == '<' {
			return false
		}
		state.typeNesting++
		state.typeToken(Punctuation, position+1, true)
	case character == '>':
		if state.typeNesting == 0 || next == '=' {
			return false
		}
		state.typeNesting--
		state.typeToken(Punctuation, position+1, false)
	case character == '(':
		if !wants {
			return false
		}
		state.typeNesting++
		state.typeToken(Punctuation, position+1, true)
	case character == ')':
		if state.typeNesting == 0 {
			return false
		}
		state.typeNesting--
		state.typeToken(Punctuation, position+1, false)
		state.typeGroupEnded = true
	case character == '[':
		if !wants && next == ']' {
			state.typeToken(Punctuation, position+2, false) // Size[]
			return true
		}
		state.typeNesting++ // [string, number], Item[Key]
		state.typeToken(Punctuation, position+1, true)
	case character == ']':
		if state.typeNesting == 0 {
			return false
		}
		state.typeNesting--
		state.typeToken(Punctuation, position+1, false)
	case character == '{':
		if !wants {
			return false // a function body after a return type
		}
		// A type literal: its members are read like an interface's, and
		// closing it comes back here (closeBrace).
		state.open(javascriptBracket{opener: '{', members: true, resumeType: true, typeNesting: state.typeNesting})
		state.token(Punctuation, position+1)
		state.inType = false
	case (character == '|' || character == '&') && next != character && next != '=':
		state.typeToken(Operator, position+1, true)
	case character == ',':
		if state.typeNesting == 0 && !state.typeList {
			return false
		}
		state.typeToken(Punctuation, position+1, true)
	case character == ':':
		if state.typeNesting == 0 {
			return false
		}
		state.typeToken(Punctuation, position+1, true)
	case character == '?':
		if state.typeNesting == 0 || next == '.' || next == '?' {
			return false
		}
		state.typeToken(Operator, position+1, true)
	case character == '=' && next == '>':
		if !state.typeGroupEnded && state.typeNesting == 0 {
			return false // the arrow of a function whose return type this was
		}
		state.typeToken(Operator, position+2, true)
	case character == '=' && next != '=':
		switch {
		case state.typeNesting > 0: // <T = {}>
		case state.typeAlias:
			state.typeAlias = false
		default:
			return false
		}
		state.typeToken(Operator, position+1, true)
	case character == '.':
		switch {
		case strings.HasPrefix(source[position:], "..."):
			state.typeToken(Operator, position+3, true)
		case !wants && javascriptNameStart(byteAt(source, position+1)):
			state.typeToken(Punctuation, position+1, true) // Intl.Locale
		default:
			return false
		}
	default:
		end := scanIdentifier(source, position, "$")
		if end == position {
			return false
		}
		return state.typeWord(source[position:end], end)
	}
	return true
}

// typeWord reads a name inside a type, or reports false when a name cannot go
// on the type here (it follows a complete type and is not is, extends or
// implements).
func (state *javascriptState) typeWord(word string, end int) bool {
	switch {
	case word == "is":
		state.typeToken(Keyword, end, true)
	case word == "extends" || word == "implements":
		state.typeToken(Keyword, end, true)
		state.typeList = true
	case !state.typeWantsTerm:
		return false
	case typescriptTypeOperators.has(word):
		state.typeToken(Keyword, end, true)
	case state.dialect.constants.has(word):
		state.typeToken(Constant, end, false)
	case state.lastKind == Keyword && state.lastText == "typeof":
		state.typeToken(Plain, end, false) // typeof names a value, not a type
	case state.typeNesting > 0 && state.labelFollows(end), state.isFollows(end):
		// (request: Request) => …, [first: string], item is Item
		state.typeToken(Plain, end, true)
	default:
		state.typeToken(Type, end, false)
	}
	return true
}

// labelFollows reports whether ':' or "?:" follows a name: a parameter of a
// function type or a tuple label, not a type.
func (state *javascriptState) labelFollows(end int) bool {
	next := javascriptSkipSpaces(state.source, end)
	character, after := byteAt(state.source, next), byteAt(state.source, next+1)
	return (character == ':' && after != ':') || (character == '?' && after == ':')
}

// isFollows reports whether " is " follows a name: the parameter a type
// predicate speaks of (item is Item).
func (state *javascriptState) isFollows(end int) bool {
	next := javascriptSkipSpaces(state.source, end)
	return next > end && strings.HasPrefix(state.source[next:], "is") && !javascriptNameStart(byteAt(state.source, next+2)) &&
		!isDecimalDigit(byteAt(state.source, next+2))
}
