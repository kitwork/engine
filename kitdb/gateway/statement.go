package gateway

import (
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// The gateway decides where a statement ends before SQLite sees it: SQLite (through modernc) runs every statement of
// a script, so a second statement after the first would run unchecked. The scanner below cuts tokens exactly where
// SQLite's own tokenizer (sqlite3GetToken) cuts them for everything that can hide a semicolon: '…' strings, "…" `…`
// and […] identifiers, -- and /* */ comments, x'…' blobs and the $name(…) form of a parameter. Only what comes
// before the first semicolon reaches SQLite.

type tokenKind int

const (
	tokenWord        tokenKind = iota // a keyword or a bare identifier
	tokenQuoted                       // an identifier written "…", `…` or […]
	tokenString                       // a string '…' or a blob x'…'
	tokenNumber                       // a numeric literal
	tokenVariable                     // a parameter: ?, ?NNN, $name, :name, @name, #name
	tokenSemicolon                    // the end of a statement
	tokenPunctuation                  // any other character
)

type token struct {
	kind  tokenKind
	text  string
	start int
	end   int
}

// word is the token's text in upper case when it is a bare word, empty otherwise.
func (token token) word() string {
	if token.kind != tokenWord {
		return ""
	}
	return strings.ToUpper(token.text)
}

func syntaxError(message string) error {
	return pgwire.NewError("42601", message)
}

// isSpaceStart reports a byte that begins whitespace for SQLite (tab, newline, form feed, carriage return, space).
func isSpaceStart(character byte) bool {
	switch character {
	case '\t', '\n', '\f', '\r', ' ':
		return true
	}
	return false
}

// isSpace reports a byte that continues whitespace for SQLite (also the vertical tab).
func isSpace(character byte) bool {
	return isSpaceStart(character) || character == '\v'
}

func isDigit(character byte) bool { return character >= '0' && character <= '9' }

func isHexadecimal(character byte) bool {
	return isDigit(character) || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')
}

// isIdentifierCharacter is SQLite's IdChar: letters, digits, '_', '$' and every byte from 0x80.
func isIdentifierCharacter(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || isDigit(character) ||
		character == '_' || character == '$' || character >= 0x80
}

// isIdentifierStart reports a byte that begins a word for SQLite: a letter, '_' or a byte from 0x80.
func isIdentifierStart(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || character == '_' ||
		character >= 0x80
}

// scan splits query into SQLite's tokens, without whitespace and comments.
func scan(query string) ([]token, error) {
	if strings.IndexByte(query, 0) >= 0 {
		// SQLite reads the statement as a C string: it would stop at the zero byte where this scanner goes on.
		return nil, syntaxError("the statement contains a zero byte")
	}
	var tokens []token
	length := len(query)
	at := func(index int) byte {
		if index < length {
			return query[index]
		}
		return 0
	}
	for index := 0; index < length; {
		character := query[index]
		start := index
		kind := tokenPunctuation
		switch {
		case isSpaceStart(character):
			index++
			for index < length && isSpace(query[index]) {
				index++
			}
			continue
		case character == 0xEF && at(index+1) == 0xBB && at(index+2) == 0xBF:
			index += 3 // a byte order mark is whitespace for SQLite
			continue
		case character == '-' && at(index+1) == '-':
			for index < length && query[index] != '\n' {
				index++
			}
			continue
		case character == '/' && at(index+1) == '*' && index+2 < length:
			end := strings.Index(query[index+2:], "*/")
			if end < 0 {
				index = length // an unterminated comment runs to the end, as in SQLite
			} else {
				index += 2 + end + 2
			}
			continue
		case character == '\'' || character == '"' || character == '`':
			index++
			closed := false
			for index < length {
				if query[index] == character {
					if at(index+1) == character {
						index += 2
						continue
					}
					index++
					closed = true
					break
				}
				index++
			}
			if !closed {
				if character == '\'' {
					return nil, syntaxError("unterminated quoted string")
				}
				return nil, syntaxError("unterminated quoted identifier")
			}
			kind = tokenQuoted
			if character == '\'' {
				kind = tokenString
			}
		case character == '[':
			end := strings.IndexByte(query[index:], ']')
			if end < 0 {
				return nil, syntaxError("unterminated quoted identifier")
			}
			index += end + 1
			kind = tokenQuoted
		case character == ';':
			index++
			kind = tokenSemicolon
		case character == '?':
			index++
			for index < length && isDigit(query[index]) {
				index++
			}
			kind = tokenVariable
		case character == '$' || character == '@' || character == ':' || character == '#':
			// SQLite's variable: identifier characters, then optionally "(…)" running to the first ")" or
			// whitespace (a Tcl array element), or "::" continuing the name.
			index++
			characters := 0
			for index < length {
				next := query[index]
				if isIdentifierCharacter(next) {
					characters++
					index++
					continue
				}
				if next == '(' && characters > 0 {
					index++
					for index < length && !isSpace(query[index]) && query[index] != ')' {
						index++
					}
					if index < length && query[index] == ')' {
						index++
					}
					break
				}
				if next == ':' && at(index+1) == ':' {
					index += 2
					continue
				}
				break
			}
			kind = tokenVariable
		case (character == 'x' || character == 'X') && at(index+1) == '\'':
			index += 2
			for index < length && isHexadecimal(query[index]) {
				index++
			}
			if index >= length || query[index] != '\'' || (index-start)%2 != 0 {
				for index < length && query[index] != '\'' {
					index++
				}
			}
			if index >= length {
				return nil, syntaxError("unterminated blob literal")
			}
			index++
			kind = tokenString
		case isDigit(character) || (character == '.' && isDigit(at(index+1))):
			index++
			for index < length {
				next := query[index]
				if isIdentifierCharacter(next) || next == '.' {
					index++
					continue
				}
				if (next == '+' || next == '-') && (query[index-1] == 'e' || query[index-1] == 'E') {
					index++
					continue
				}
				break
			}
			kind = tokenNumber
		case isIdentifierStart(character):
			index++
			for index < length && isIdentifierCharacter(query[index]) {
				index++
			}
			kind = tokenWord
		default:
			index++
		}
		tokens = append(tokens, token{kind: kind, text: query[start:index], start: start, end: index})
	}
	return tokens, nil
}

type statementKind int

const (
	statementEmpty    statementKind = iota // nothing but whitespace, comments and semicolons
	statementRead                          // SELECT, VALUES, WITH … SELECT
	statementExplain                       // EXPLAIN [QUERY PLAN] of a read statement
	statementVersion                       // SELECT version()
	statementShow                          // SHOW name
	statementSet                           // SET … / RESET …
	statementBegin                         // BEGIN / START TRANSACTION
	statementCommit                        // COMMIT / END
	statementRollback                      // ROLLBACK / ABORT
	statementAccount                       // CREATE, ALTER, DROP, GRANT, REVOKE: Access.Command first
	statementRefused                       // anything else: a write
)

// statement is one statement of a query, cut at its terminator.
type statement struct {
	kind statementKind
	// text is what SQLite runs: the query up to its first terminator.
	text string
	// authorized is the read statement EXPLAIN checks: text, or the statement a user's EXPLAIN explains.
	authorized string
	// verb is the first keyword in upper case (for a WITH, the keyword of its main statement).
	verb   string
	tokens []token
}

// readOnlyError is PostgreSQL's refusal of a write in a read-only transaction.
func readOnlyError(verb string) error {
	if verb == "" {
		verb = "this statement"
	}
	return pgwire.NewError("25006", "cannot execute "+verb+" in a read-only transaction")
}

// parseStatement cuts query at its first terminator and classifies the one statement it holds. A second statement
// is refused: the gateway runs one statement at a time.
func parseStatement(query string) (statement, error) {
	tokens, err := scan(query)
	if err != nil {
		return statement{}, err
	}
	end := len(query)
	for index, candidate := range tokens {
		if candidate.kind != tokenSemicolon {
			continue
		}
		for _, rest := range tokens[index+1:] {
			if rest.kind != tokenSemicolon {
				return statement{}, pgwire.NewError("25006",
					"cannot execute more than one statement at a time in a read-only transaction")
			}
		}
		end = candidate.start
		tokens = tokens[:index]
		break
	}
	result := statement{text: query[:end], tokens: tokens}
	if len(tokens) == 0 {
		result.kind = statementEmpty
		return result, nil
	}
	for index, candidate := range tokens {
		if candidate.kind != tokenVariable && candidate.kind != tokenPunctuation {
			continue
		}
		if strings.Contains(candidate.text, "::") ||
			(candidate.text == ":" && index+1 < len(tokens) && tokens[index+1].start == candidate.end &&
				strings.HasPrefix(tokens[index+1].text, ":")) {
			return statement{}, syntaxError("PostgreSQL casts written with :: are not supported: write CAST(value AS type)")
		}
		if candidate.kind == tokenVariable && strings.Contains(candidate.text, "(") {
			return statement{}, syntaxError("parameters written as " + candidate.text + " are not supported: use $1, $2, …")
		}
	}
	result.verb = tokens[0].word()
	if result.verb == "" {
		result.kind = statementRefused
		result.verb = strings.ToUpper(tokens[0].text)
		return result, nil
	}
	switch result.verb {
	case "SELECT", "VALUES", "WITH":
		result.kind = statementRead
		result.authorized = result.text
		if result.verb == "WITH" {
			result.verb = mainVerb(tokens)
			if result.verb != "SELECT" && result.verb != "VALUES" && result.verb != "" {
				result.kind = statementRefused
			}
		}
		if isVersionCall(tokens) {
			result.kind = statementVersion
		}
	case "EXPLAIN":
		inner := 1
		if inner+1 < len(tokens) && tokens[inner].word() == "QUERY" && tokens[inner+1].word() == "PLAN" {
			inner += 2
		}
		if inner >= len(tokens) {
			return statement{}, syntaxError("EXPLAIN needs a statement to explain")
		}
		explained, err := parseStatement(query[tokens[inner].start:end])
		if err != nil {
			return statement{}, err
		}
		switch explained.kind {
		case statementRead, statementVersion:
			result.kind = statementExplain
			result.authorized = explained.text
		case statementRefused, statementAccount:
			result.kind = statementRefused
			result.verb = explained.verb
		default:
			return statement{}, syntaxError("EXPLAIN explains a read statement only (SELECT, VALUES or WITH)")
		}
	case "SHOW":
		result.kind = statementShow
	case "SET", "RESET":
		result.kind = statementSet
	case "BEGIN", "START":
		result.kind = statementBegin
	case "COMMIT", "END":
		result.kind = statementCommit
	case "ROLLBACK", "ABORT":
		result.kind = statementRollback
	case "CREATE", "ALTER", "DROP", "GRANT", "REVOKE":
		result.kind = statementAccount
	default:
		result.kind = statementRefused
	}
	return result, nil
}

// mainVerb finds the keyword of the statement a WITH clause leads to: the first SELECT, VALUES, INSERT, UPDATE,
// DELETE or REPLACE outside the parentheses of its common table expressions.
func mainVerb(tokens []token) string {
	depth := 0
	for _, candidate := range tokens[1:] {
		switch candidate.text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth != 0 {
			continue
		}
		switch word := candidate.word(); word {
		case "SELECT", "VALUES", "INSERT", "UPDATE", "DELETE", "REPLACE":
			return word
		}
	}
	return ""
}

// isVersionCall reports SELECT version() or SELECT pg_catalog.version(), which the gateway answers itself.
func isVersionCall(tokens []token) bool {
	if len(tokens) >= 6 && tokens[1].word() == "PG_CATALOG" && tokens[2].text == "." {
		tokens = append(tokens[:1:1], tokens[3:]...)
	}
	return len(tokens) == 4 && tokens[0].word() == "SELECT" && tokens[1].word() == "VERSION" &&
		tokens[2].text == "(" && tokens[3].text == ")"
}

// words is the statement's tokens as upper-case words (quotes removed from strings and identifiers), for the
// small grammars of SET, SHOW and BEGIN.
func words(tokens []token) []string {
	result := make([]string, 0, len(tokens))
	for _, candidate := range tokens {
		text := candidate.text
		switch candidate.kind {
		case tokenString, tokenQuoted:
			if len(text) >= 2 {
				text = text[1 : len(text)-1]
			}
		}
		result = append(result, strings.ToUpper(text))
	}
	return result
}
