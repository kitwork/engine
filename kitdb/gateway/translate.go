package gateway

import (
	"strconv"
	"strings"
	"unicode/utf8"
)

// What PostgreSQL clients write that SQLite does not read is rewritten before the statement is checked and run, one
// rewrite at a time, each on SQLite's own tokens, until none applies (KITWORK_KITDB.md question 13.5). A database tool
// reads the catalog with PostgreSQL's SQL: value::type casts, pg_catalog.pg_class, information_schema.tables,
// (SELECT …) UNION (SELECT …), x = ANY(array), unnest(array), ~ and ILIKE, E'…' strings, pg_catalog.format_type(…).
// Each rewrite removes what it matched, so the loop ends; text it does not match is copied as written, comments and
// all. What it cannot rewrite is left for SQLite, whose error the client sees. Only a read statement (SELECT, WITH,
// VALUES, EXPLAIN, or a parenthesized SELECT) is rewritten.

// maxRewrites bounds the rewrites of one statement: each takes away at least one token of what it matched.
const maxRewrites = 4096

// translator holds what a rewrite needs of the session: its database's name, its user, SQLite's version.
type translator struct {
	database string
	user     string
	version  string
}

func (session *Session) translator() translator {
	user := session.options.User
	if user == "" {
		user = "kitwork"
	}
	return translator{database: session.target.Name, user: user, version: session.sqliteVersion}
}

// translate rewrites query when it is a read statement; anything else is returned as it is.
func (translator translator) translate(query string) string {
	tokens, err := scan(query)
	if err != nil || len(tokens) == 0 {
		return query
	}
	switch tokens[0].word() {
	case "SELECT", "WITH", "VALUES", "EXPLAIN":
	default:
		if tokens[0].text != "(" {
			return query
		}
	}
	if isVersionCall(withoutSemicolon(tokens)) {
		return query // answered by the gateway itself, named "version"
	}
	text := query
	for range maxRewrites {
		tokens, err := scan(text)
		if err != nil {
			return text
		}
		rewritten, changed := translator.rewriteOnce(text, tokens)
		if !changed {
			return text
		}
		text = rewritten
	}
	return text
}

func withoutSemicolon(tokens []token) []token {
	for index, candidate := range tokens {
		if candidate.kind == tokenSemicolon {
			return tokens[:index]
		}
	}
	return tokens
}

// rewriteOnce applies the first rewrite that matches, in a fixed order: what a later one reads (a whole operand, a
// call) is already in SQLite's form when it runs.
func (translator translator) rewriteOnce(text string, tokens []token) (string, bool) {
	for _, rewrite := range []func(string, []token) (string, bool){
		splitVariableCasts,
		escapeStrings,
		compoundParentheses,
		translator.casts,
		subscripts,
		positionCalls,
		anyAll,
		arrayConstructors,
		unnestCalls,
		patternOperators,
		qualifiers,
		translator.sessionCalls,
		renameFunctions,
	} {
		if rewritten, changed := rewrite(text, tokens); changed {
			return rewritten, true
		}
	}
	return text, false
}

// splice replaces tokens [from, to) of text with replacement.
func splice(text string, tokens []token, from, to int, replacement string) string {
	return text[:tokens[from].start] + replacement + text[tokens[to-1].end:]
}

// source is the text of tokens [from, to) as written.
func source(text string, tokens []token, from, to int) string {
	if from >= to {
		return ""
	}
	return text[tokens[from].start:tokens[to-1].end]
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// matchingClose is the index of the ")" that closes the "(" at open, or -1.
func matchingClose(tokens []token, open int) int {
	depth := 0
	for index := open; index < len(tokens); index++ {
		switch tokens[index].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				return index
			}
		}
		if tokens[index].kind == tokenSemicolon {
			return -1
		}
	}
	return -1
}

// matchingOpen is the index of the "(" that the ")" at close closes, or -1.
func matchingOpen(tokens []token, close int) int {
	depth := 0
	for index := close; index >= 0; index-- {
		switch tokens[index].text {
		case ")":
			depth++
		case "(":
			depth--
			if depth == 0 {
				return index
			}
		}
	}
	return -1
}

// isPunctuation reports a punctuation token of the given text (SQLite's scanner gives every operator character its
// own token).
func isPunctuation(candidate token, text string) bool {
	return candidate.kind == tokenPunctuation && candidate.text == text
}

func isBracket(candidate token) bool {
	return candidate.kind == tokenQuoted && strings.HasPrefix(candidate.text, "[")
}

func isIdentifier(candidate token) bool {
	return candidate.kind == tokenWord || (candidate.kind == tokenQuoted && !isBracket(candidate))
}

// unquoted is an identifier token's name: its text, or what its quotes hold.
func unquoted(candidate token) string {
	if candidate.kind == tokenQuoted && len(candidate.text) >= 2 {
		inner := candidate.text[1 : len(candidate.text)-1]
		if candidate.text[0] == '"' {
			return strings.ReplaceAll(inner, `""`, `"`)
		}
		if candidate.text[0] == '`' {
			return strings.ReplaceAll(inner, "``", "`")
		}
		return inner
	}
	return candidate.text
}

// notFunctions are the keywords a "(" may follow without being a call.
var notFunctions = map[string]bool{
	"SELECT": true, "WHERE": true, "AND": true, "OR": true, "NOT": true, "IN": true, "ON": true, "FROM": true,
	"JOIN": true, "AS": true, "BY": true, "WHEN": true, "THEN": true, "ELSE": true, "CASE": true, "VALUES": true,
	"EXISTS": true, "ANY": true, "ALL": true, "SOME": true, "HAVING": true, "LIMIT": true, "OFFSET": true,
	"UNION": true, "INTERSECT": true, "EXCEPT": true, "IS": true, "LIKE": true, "BETWEEN": true, "DISTINCT": true,
	"USING": true, "OVER": true, "FILTER": true, "WITH": true, "INTO": true, "LATERAL": true, "ESCAPE": true,
	"GLOB": true, "REGEXP": true, "MATCH": true, "RETURNING": true, "SET": true, "ORDER": true, "GROUP": true,
	"WINDOW": true, "PARTITION": true, "ROWS": true, "RANGE": true, "GROUPS": true, "RECURSIVE": true,
	"MATERIALIZED": true, "TABLE": true, "END": true, "NULL": true,
}

// qualifiedStart walks back over "a." prefixes of the identifier at index: a.b.c starts at a.
func qualifiedStart(tokens []token, index int) int {
	for index >= 2 && isPunctuation(tokens[index-1], ".") && isIdentifier(tokens[index-2]) {
		index -= 2
	}
	return index
}

// operandStart is where the operand that ends just before end begins: a literal, a parameter, a (qualified) name, a
// call, a parenthesized expression or a CASE … END; -1 when there is none.
func operandStart(tokens []token, end int) int {
	last := end - 1
	if last < 0 {
		return -1
	}
	candidate := tokens[last]
	switch {
	case isPunctuation(candidate, ")"):
		open := matchingOpen(tokens, last)
		if open < 0 {
			return -1
		}
		if open > 0 && isIdentifier(tokens[open-1]) && !notFunctions[tokens[open-1].word()] {
			return qualifiedStart(tokens, open-1)
		}
		return open
	case candidate.word() == "END":
		depth := 0
		for index := last; index >= 0; index-- {
			switch tokens[index].word() {
			case "END":
				depth++
			case "CASE":
				depth--
				if depth == 0 {
					return index
				}
			}
		}
		return -1
	case candidate.kind == tokenString || candidate.kind == tokenNumber || candidate.kind == tokenVariable:
		return last
	case isIdentifier(candidate) && !notFunctions[candidate.word()]:
		return qualifiedStart(tokens, last)
	}
	return -1
}

// operandEnd is where the operand that starts at start ends (exclusive): the mirror of operandStart; -1 for none.
func operandEnd(tokens []token, start int) int {
	if start >= len(tokens) {
		return -1
	}
	candidate := tokens[start]
	switch {
	case isPunctuation(candidate, "("):
		if close := matchingClose(tokens, start); close > 0 {
			return close + 1
		}
		return -1
	case candidate.word() == "CASE":
		depth := 0
		for index := start; index < len(tokens); index++ {
			switch tokens[index].word() {
			case "CASE":
				depth++
			case "END":
				depth--
				if depth == 0 {
					return index + 1
				}
			}
		}
		return -1
	case candidate.kind == tokenString || candidate.kind == tokenNumber || candidate.kind == tokenVariable:
		return start + 1
	case isPunctuation(candidate, "-") || isPunctuation(candidate, "+"):
		return operandEnd(tokens, start+1)
	case isIdentifier(candidate) && !notFunctions[candidate.word()]:
		index := start
		for index+2 < len(tokens) && isPunctuation(tokens[index+1], ".") && isIdentifier(tokens[index+2]) {
			index += 2
		}
		if index+1 < len(tokens) && isPunctuation(tokens[index+1], "(") {
			if close := matchingClose(tokens, index+1); close > 0 {
				return close + 1
			}
			return -1
		}
		return index + 1
	}
	return -1
}

// selectItemAlias is the name PostgreSQL gives a select-list item SQLite would name by its text, when tokens
// [from, to) are a whole item of a select list: " AS name"; "" otherwise.
func selectItemAlias(tokens []token, from, to int, name string) string {
	if name == "" || from == 0 {
		return ""
	}
	switch before := tokens[from-1]; {
	case before.word() == "SELECT", before.word() == "DISTINCT", before.word() == "ALL", isPunctuation(before, ","):
	default:
		return ""
	}
	if to < len(tokens) {
		after := tokens[to]
		switch {
		case isPunctuation(after, ","), isPunctuation(after, ")"), after.kind == tokenSemicolon:
		case after.word() == "FROM", after.word() == "UNION", after.word() == "INTERSECT", after.word() == "EXCEPT",
			after.word() == "ORDER", after.word() == "LIMIT", after.word() == "WHERE", after.word() == "GROUP":
		default:
			return ""
		}
	}
	// The item belongs to a select list: walking back at its own depth meets SELECT before an unclosed "(" or a
	// clause keyword.
	depth := 0
	for index := from - 1; index >= 0; index-- {
		candidate := tokens[index]
		switch {
		case isPunctuation(candidate, ")"):
			depth++
		case isPunctuation(candidate, "("):
			if depth == 0 {
				return ""
			}
			depth--
		case depth == 0 && candidate.word() == "SELECT":
			return " AS " + quoteIdentifier(name)
		case depth == 0 && (candidate.word() == "FROM" || candidate.word() == "WHERE" || candidate.word() == "ON" ||
			candidate.word() == "BY" || candidate.word() == "HAVING" || candidate.word() == "WHEN" ||
			candidate.word() == "THEN" || candidate.word() == "VALUES"):
			return ""
		}
	}
	return ""
}

// splitVariableCasts separates a cast SQLite's scanner glued to a parameter ($1::int is one variable token, Tcl's
// namespace syntax): "$1 ::int", so the cast reads as any other.
func splitVariableCasts(text string, tokens []token) (string, bool) {
	for _, candidate := range tokens {
		if candidate.kind != tokenVariable {
			continue
		}
		if at := strings.Index(candidate.text[1:], "::"); at >= 0 {
			position := candidate.start + 1 + at
			return text[:position] + " " + text[position:], true
		}
	}
	return text, false
}

// escapeStrings turns PostgreSQL's E'…' string into a plain one: its backslash escapes written out.
func escapeStrings(text string, tokens []token) (string, bool) {
	for index := 0; index+1 < len(tokens); index++ {
		prefix, literal := tokens[index], tokens[index+1]
		if prefix.kind != tokenWord || !strings.EqualFold(prefix.text, "E") || literal.kind != tokenString ||
			literal.start != prefix.end || !strings.HasPrefix(literal.text, "'") {
			continue
		}
		inner := strings.ReplaceAll(literal.text[1:len(literal.text)-1], "''", "'")
		return splice(text, tokens, index, index+2, quoteLiteral(unescape(inner))), true
	}
	return text, false
}

// unescape writes out PostgreSQL's backslash escapes: \n \t \r \b \f \\ \' \ooo \xhh \uXXXX \UXXXXXXXX.
func unescape(value string) string {
	var builder strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character != '\\' || index+1 >= len(value) {
			builder.WriteByte(character)
			continue
		}
		index++
		switch next := value[index]; next {
		case 'n':
			builder.WriteByte('\n')
		case 't':
			builder.WriteByte('\t')
		case 'r':
			builder.WriteByte('\r')
		case 'b':
			builder.WriteByte('\b')
		case 'f':
			builder.WriteByte('\f')
		case 'x', 'u', 'U':
			digits := map[byte]int{'x': 2, 'u': 4, 'U': 8}[next]
			end := index + 1
			for end < len(value) && end < index+1+digits && isHexadecimal(value[end]) {
				end++
			}
			if code, err := strconv.ParseUint(value[index+1:end], 16, 32); err == nil && end > index+1 {
				if next == 'x' {
					builder.WriteByte(byte(code))
				} else if utf8.ValidRune(rune(code)) {
					builder.WriteRune(rune(code))
				}
				index = end - 1
			} else {
				builder.WriteByte(next)
			}
		default:
			if next >= '0' && next <= '7' {
				end := index
				for end < len(value) && end < index+3 && value[end] >= '0' && value[end] <= '7' {
					end++
				}
				code, _ := strconv.ParseUint(value[index:end], 8, 16)
				builder.WriteByte(byte(code))
				index = end - 1
			} else {
				builder.WriteByte(next)
			}
		}
	}
	return builder.String()
}

// compoundParentheses rewrites a parenthesized member of a compound SELECT, which SQLite does not read:
// (SELECT …) UNION (SELECT …) becomes SELECT * FROM (SELECT …) UNION SELECT * FROM (SELECT …).
func compoundParentheses(text string, tokens []token) (string, bool) {
	for index := 0; index+1 < len(tokens); index++ {
		if !isPunctuation(tokens[index], "(") {
			continue
		}
		switch tokens[index+1].word() {
		case "SELECT", "WITH", "VALUES":
		default:
			continue
		}
		member := index == 0
		if index > 0 {
			switch before := tokens[index-1].word(); before {
			case "UNION", "INTERSECT", "EXCEPT":
				member = true
			case "ALL", "DISTINCT":
				member = index > 1 && (tokens[index-2].word() == "UNION" || tokens[index-2].word() == "INTERSECT" ||
					tokens[index-2].word() == "EXCEPT")
			}
		}
		if member {
			return splice(text, tokens, index, index+1, "SELECT * FROM ("), true
		}
	}
	return text, false
}

// castMarker reports a cast at index: SQLite's scanner reads "::type" as a ":" token then a ":type" variable (or a
// second ":" before a quoted type or a space). It returns the type's first word and the index after the marker.
func castMarker(tokens []token, index int) (string, int, bool) {
	marker := tokens[index]
	if (marker.kind != tokenVariable && marker.kind != tokenPunctuation) || marker.text != ":" || index+1 >= len(tokens) {
		return "", 0, false
	}
	next := tokens[index+1]
	if next.start != marker.end || !strings.HasPrefix(next.text, ":") || (next.kind != tokenVariable && next.kind != tokenPunctuation) {
		return "", 0, false
	}
	if next.text == ":" {
		if index+2 >= len(tokens) || !isIdentifier(tokens[index+2]) {
			return "", 0, false
		}
		return unquoted(tokens[index+2]), index + 3, true
	}
	return next.text[1:], index + 2, true
}

// castType reads the rest of a cast's type from after (a qualifier, PostgreSQL's two-word types, a modifier, []):
// its name in lower case without modifier, whether it is an array, and the index after it.
func castType(tokens []token, first string, after int) (string, bool, int) {
	name := strings.ToLower(first)
	if cut := strings.IndexByte(name, '('); cut >= 0 {
		name = name[:cut] // ":varchar(255)": the scanner kept the modifier in the variable
	}
	if (name == "pg_catalog" || name == "public") && after+1 < len(tokens) && isPunctuation(tokens[after], ".") &&
		isIdentifier(tokens[after+1]) {
		name = strings.ToLower(unquoted(tokens[after+1]))
		after += 2
	}
	word := func(index int) string {
		if index < len(tokens) {
			return tokens[index].word()
		}
		return ""
	}
	switch name {
	case "character", "char", "bit", "national":
		if word(after) == "VARYING" {
			name += " varying"
			after++
		}
	case "double":
		if word(after) == "PRECISION" {
			name, after = "double precision", after+1
		}
	}
	if after < len(tokens) && isPunctuation(tokens[after], "(") {
		if close := matchingClose(tokens, after); close > 0 {
			after = close + 1
		}
	}
	if name == "timestamp" || name == "time" {
		if (word(after) == "WITH" || word(after) == "WITHOUT") && word(after+1) == "TIME" && word(after+2) == "ZONE" {
			after += 3
		}
	}
	array := false
	for after < len(tokens) && isBracket(tokens[after]) {
		array, after = true, after+1
	}
	return name, array, after
}

// casts rewrites value::type, the leftmost first, so a chain x::regclass::text casts x first.
func (translator translator) casts(text string, tokens []token) (string, bool) {
	for index := range tokens {
		first, after, found := castMarker(tokens, index)
		if !found {
			continue
		}
		start := operandStart(tokens, index)
		if start < 0 {
			continue
		}
		name, array, end := castType(tokens, first, after)
		operand := source(text, tokens, start, index)
		replacement := castExpression(operand, name, array)
		alias := ""
		switch last := tokens[index-1]; {
		case isIdentifier(last):
			alias = unquoted(last)
		case isPunctuation(last, ")"):
			if open := matchingOpen(tokens, index-1); open > 0 && isIdentifier(tokens[open-1]) {
				alias = strings.ToLower(unquoted(tokens[open-1]))
			}
		}
		if alias == "" {
			alias = name
		}
		return splice(text, tokens, start, end, replacement+selectItemAlias(tokens, start, end, alias)), true
	}
	return text, false
}

// castExpression is SQLite's form of operand::name.
func castExpression(operand, name string, array bool) string {
	if array {
		return "kitwork_pg_array_text(" + operand + ")"
	}
	switch name {
	case "regclass":
		return objectReference(operand, "class")
	case "regnamespace":
		return objectReference(operand, "namespace")
	case "regrole":
		return objectReference(operand, "role")
	case "regproc", "regprocedure":
		return objectReference(operand, "proc")
	case "regtype":
		return "(CASE WHEN typeof(" + operand + ") IN ('integer', 'real') THEN kitwork_pg_format_type(" + operand +
			", NULL) ELSE kitwork_pg_type_oid(" + operand + ") END)"
	case "int", "int2", "int4", "int8", "integer", "smallint", "bigint", "oid", "xid", "cid", "serial", "bigserial":
		return "CAST(" + operand + " AS INTEGER)"
	case "real", "float", "float4", "float8", "double precision":
		return "CAST(" + operand + " AS REAL)"
	case "numeric", "decimal":
		return "CAST(" + operand + " AS NUMERIC)"
	case "bool", "boolean":
		return "kitwork_pg_bool(" + operand + ")"
	case "bytea":
		return "CAST(" + operand + " AS BLOB)"
	}
	return "CAST(" + operand + " AS TEXT)"
}

// objectReference is a reg* cast: a number is an OID and becomes the object's name; anything else is a name and
// becomes its OID (PostgreSQL's reg* types are both; a client casts an OID to show it, a name to compare it).
func objectReference(operand, kind string) string {
	return "(CASE WHEN typeof(" + operand + ") IN ('integer', 'real') THEN " +
		lookup(kind, "CAST(CAST("+operand+" AS INTEGER) AS TEXT)") +
		" ELSE CAST(" + lookup(kind+" oid", "kitwork_pg_object_key("+operand+")") + " AS INTEGER) END)"
}

// lookup reads the catalog's kitwork_pg_lookup by kind and key. The table has no rowid and no column a client's
// statement names, so a name in key (oid, relname) still means the client's own column, not the table's.
func lookup(kind, key string) string {
	return `(SELECT "kitwork:l".kitwork_value FROM temp.kitwork_pg_lookup "kitwork:l" WHERE "kitwork:l".kitwork_kind = '` +
		kind + `' AND "kitwork:l".kitwork_key = ` + key + ")"
}

// subscripts rewrites array[n], which SQLite's scanner reads as an identifier in brackets right after a value.
func subscripts(text string, tokens []token) (string, bool) {
	for index := 1; index < len(tokens); index++ {
		candidate := tokens[index]
		if !isBracket(candidate) || candidate.start != tokens[index-1].end || len(candidate.text) < 3 {
			continue
		}
		previous := tokens[index-1]
		if previous.word() == "ARRAY" {
			continue
		}
		if !isIdentifier(previous) && !isPunctuation(previous, ")") {
			continue
		}
		start := operandStart(tokens, index)
		if start < 0 {
			continue
		}
		inner := candidate.text[1 : len(candidate.text)-1]
		if strings.Contains(inner, ":") {
			continue // a slice a[1:2]
		}
		return splice(text, tokens, start, index+1,
			"kitwork_pg_subscript("+source(text, tokens, start, index)+", "+inner+")"), true
	}
	return text, false
}

// positionCalls rewrites position(needle IN haystack) as instr(haystack, needle).
func positionCalls(text string, tokens []token) (string, bool) {
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].word() != "POSITION" || !isPunctuation(tokens[index+1], "(") {
			continue
		}
		close := matchingClose(tokens, index+1)
		if close < 0 {
			continue
		}
		depth := 0
		for middle := index + 2; middle < close; middle++ {
			switch {
			case isPunctuation(tokens[middle], "("):
				depth++
			case isPunctuation(tokens[middle], ")"):
				depth--
			case depth == 0 && tokens[middle].word() == "IN":
				needle := source(text, tokens, index+2, middle)
				haystack := source(text, tokens, middle+1, close)
				return splice(text, tokens, index, close+1, "instr("+haystack+", "+needle+")"+
					selectItemAlias(tokens, index, close+1, "position")), true
			}
		}
	}
	return text, false
}

// anyAll rewrites x = ANY(array) as x IN (the array's elements), and x <> ALL(array) as x NOT IN; a subquery stays a
// subquery.
func anyAll(text string, tokens []token) (string, bool) {
	for index := 1; index+1 < len(tokens); index++ {
		word := tokens[index].word()
		if (word != "ANY" && word != "SOME" && word != "ALL") || !isPunctuation(tokens[index+1], "(") {
			continue
		}
		operatorStart, negated := -1, false
		switch {
		case isPunctuation(tokens[index-1], "=") && index >= 2 && (isPunctuation(tokens[index-2], "!") ||
			isPunctuation(tokens[index-2], "<") || isPunctuation(tokens[index-2], ">")):
			if isPunctuation(tokens[index-2], "!") {
				operatorStart, negated = index-2, true
			}
		case isPunctuation(tokens[index-1], "="):
			operatorStart = index - 1
		case isPunctuation(tokens[index-1], ">") && index >= 2 && isPunctuation(tokens[index-2], "<"):
			operatorStart, negated = index-2, true
		}
		if operatorStart < 0 || negated != (word == "ALL") {
			continue
		}
		close := matchingClose(tokens, index+1)
		if close < 0 {
			continue
		}
		inner := source(text, tokens, index+2, close)
		elements := "(SELECT value FROM json_each(kitwork_pg_json(" + inner + ")))"
		switch tokens[index+2].word() {
		case "SELECT", "WITH", "VALUES":
			elements = "(" + inner + ")"
		}
		operator := " IN "
		if negated {
			operator = " NOT IN "
		}
		return splice(text, tokens, operatorStart, close+1, operator+elements), true
	}
	return text, false
}

// arrayConstructors rewrites ARRAY[a, b] (one bracketed token to SQLite's scanner) as kitwork_pg_array(a, b).
func arrayConstructors(text string, tokens []token) (string, bool) {
	for index := 0; index+1 < len(tokens); index++ {
		if tokens[index].word() != "ARRAY" || !isBracket(tokens[index+1]) {
			continue
		}
		inner := tokens[index+1].text[1 : len(tokens[index+1].text)-1]
		replacement := "'{}'"
		if strings.TrimSpace(inner) != "" {
			replacement = "kitwork_pg_array(" + inner + ")"
		}
		return splice(text, tokens, index, index+2, replacement+selectItemAlias(tokens, index, index+2, "array")), true
	}
	return text, false
}

// unnestCalls rewrites unnest(array) [WITH ORDINALITY] [AS] alias[(column[, ordinal])] in a FROM clause as a
// subquery over json_each.
func unnestCalls(text string, tokens []token) (string, bool) {
	for index := 1; index+1 < len(tokens); index++ {
		if tokens[index].word() != "UNNEST" || !isPunctuation(tokens[index+1], "(") {
			continue
		}
		if before := tokens[index-1]; before.word() != "FROM" && before.word() != "JOIN" && !isPunctuation(before, ",") {
			continue
		}
		close := matchingClose(tokens, index+1)
		if close < 0 {
			continue
		}
		array := source(text, tokens, index+2, close)
		next := close + 1
		ordinality := false
		if next+1 < len(tokens) && tokens[next].word() == "WITH" && tokens[next+1].word() == "ORDINALITY" {
			ordinality, next = true, next+2
		}
		alias, columns := "unnest", []string{}
		if next < len(tokens) && tokens[next].word() == "AS" {
			next++
		}
		if next < len(tokens) && isIdentifier(tokens[next]) && !notFunctions[tokens[next].word()] &&
			!joinWords[tokens[next].word()] {
			alias = unquoted(tokens[next])
			next++
			if next < len(tokens) && isPunctuation(tokens[next], "(") {
				if end := matchingClose(tokens, next); end > 0 {
					for _, part := range tokens[next+1 : end] {
						if isIdentifier(part) {
							columns = append(columns, unquoted(part))
						}
					}
					next = end + 1
				}
			}
		}
		value := alias
		if len(columns) > 0 {
			value = columns[0]
		}
		selection := "value AS " + quoteIdentifier(value)
		if ordinality {
			ordinal := "ordinality"
			if len(columns) > 1 {
				ordinal = columns[1]
			}
			selection += ", key + 1 AS " + quoteIdentifier(ordinal)
		}
		return splice(text, tokens, index, next, "(SELECT "+selection+" FROM json_each(kitwork_pg_json("+array+"))) AS "+
			quoteIdentifier(alias)), true
	}
	return text, false
}

// joinWords may follow a FROM item without being its alias.
var joinWords = map[string]bool{"LEFT": true, "RIGHT": true, "FULL": true, "INNER": true, "OUTER": true, "CROSS": true,
	"NATURAL": true, "LIMIT": true}

// patternOperators rewrites PostgreSQL's ~ ~* !~ !~* (regular expressions) as kitwork_pg_regexp(…), ~~ !~~ and
// ILIKE as LIKE (SQLite's LIKE ignores ASCII case).
func patternOperators(text string, tokens []token) (string, bool) {
	for index, candidate := range tokens {
		if candidate.word() == "ILIKE" {
			return splice(text, tokens, index, index+1, "LIKE"), true
		}
		if !isPunctuation(candidate, "~") {
			continue
		}
		start, negated := index, false
		if index > 0 && isPunctuation(tokens[index-1], "!") && tokens[index-1].end == candidate.start {
			start, negated = index-1, true
		}
		next := index + 1
		if next < len(tokens) && isPunctuation(tokens[next], "~") && tokens[next].start == candidate.end {
			end := next + 1
			if end < len(tokens) && isPunctuation(tokens[end], "*") && tokens[end].start == tokens[next].end {
				end++
			}
			operator := " LIKE "
			if negated {
				operator = " NOT LIKE "
			}
			return splice(text, tokens, start, end, operator), true
		}
		insensitive := false
		if next < len(tokens) && isPunctuation(tokens[next], "*") && tokens[next].start == candidate.end {
			insensitive, next = true, next+1
		}
		left := operandStart(tokens, start)
		right := operandEnd(tokens, next)
		if left < 0 || right < 0 {
			continue // ~ as SQLite's bitwise not
		}
		flags := "''"
		if insensitive {
			flags = "'i'"
		}
		call := "kitwork_pg_regexp(" + source(text, tokens, left, start) + ", " + source(text, tokens, next, right) + ", " + flags + ")"
		if negated {
			call = "NOT " + call
		}
		return splice(text, tokens, left, right, call), true
	}
	return text, false
}

// qualifiers rewrites pg_catalog.x and information_schema.x: a catalog table is the session's TEMP table
// (temp.pg_class, temp.information_schema_tables); a function or a type loses the qualifier (SQLite's functions have
// no schema).
func qualifiers(text string, tokens []token) (string, bool) {
	for index := 0; index+2 < len(tokens); index++ {
		if !isIdentifier(tokens[index]) || !isPunctuation(tokens[index+1], ".") || !isIdentifier(tokens[index+2]) {
			continue
		}
		if index > 0 && isPunctuation(tokens[index-1], ".") {
			continue
		}
		qualifier := strings.ToLower(unquoted(tokens[index]))
		name := strings.ToLower(unquoted(tokens[index+2]))
		switch qualifier {
		case "pg_catalog":
			if isCatalogTable(name) && !(index+3 < len(tokens) && isPunctuation(tokens[index+3], "(")) {
				return splice(text, tokens, index, index+3, "temp."+quoteIdentifier(name)), true
			}
			return splice(text, tokens, index, index+2, ""), true
		case "information_schema":
			return splice(text, tokens, index, index+3, "temp."+quoteIdentifier("information_schema_"+name)), true
		}
	}
	return text, false
}

// sessionCalls rewrites what answers from the session: current_database(), current_user, current_schema,
// pg_get_userbyid(…), pg_get_indexdef(oid) and its kin, to_regclass(…).
func (translator translator) sessionCalls(text string, tokens []token) (string, bool) {
	for index, candidate := range tokens {
		if candidate.kind != tokenWord || (index > 0 && isPunctuation(tokens[index-1], ".")) {
			continue
		}
		name := strings.ToLower(candidate.text)
		call := index+1 < len(tokens) && isPunctuation(tokens[index+1], "(")
		if !call {
			var literal string
			switch name {
			case "current_user", "session_user", "current_role":
				literal = quoteLiteral(translator.user)
			case "current_catalog":
				literal = quoteLiteral(translator.database)
			case "current_schema":
				literal = "'public'"
			default:
				continue
			}
			return splice(text, tokens, index, index+1, literal+selectItemAlias(tokens, index, index+1, name)), true
		}
		close := matchingClose(tokens, index+1)
		if close < 0 {
			continue
		}
		arguments := splitArguments(text, tokens, index+2, close)
		var replacement string
		switch name {
		case "current_database":
			replacement = quoteLiteral(translator.database)
		case "current_schema":
			replacement = "'public'"
		case "current_schemas":
			replacement = "'{public}'"
		case "current_user", "session_user":
			replacement = quoteLiteral(translator.user)
		case "pg_get_userbyid":
			replacement = quoteLiteral(translator.user)
		case "version":
			replacement = quoteLiteral("PostgreSQL 16.0 (Kitwork, SQLite " + translator.version + ")")
		case "pg_get_indexdef", "pg_get_constraintdef", "pg_get_viewdef", "pg_get_triggerdef", "pg_get_functiondef",
			"pg_get_ruledef":
			if len(arguments) == 0 {
				continue
			}
			replacement = lookup("definition", "CAST(CAST(("+arguments[0]+") AS INTEGER) AS TEXT)")
		case "to_regclass":
			if len(arguments) != 1 {
				continue
			}
			replacement = castExpression("CAST("+arguments[0]+" AS TEXT)", "regclass", false)
		case "to_regtype":
			if len(arguments) != 1 {
				continue
			}
			replacement = castExpression("CAST("+arguments[0]+" AS TEXT)", "regtype", false)
		default:
			continue
		}
		return splice(text, tokens, index, close+1, replacement+selectItemAlias(tokens, index, close+1, name)), true
	}
	return text, false
}

// splitArguments is the text of each argument of a call whose parentheses hold tokens [from, to).
func splitArguments(text string, tokens []token, from, to int) []string {
	var arguments []string
	depth, start := 0, from
	for index := from; index < to; index++ {
		switch {
		case isPunctuation(tokens[index], "("):
			depth++
		case isPunctuation(tokens[index], ")"):
			depth--
		case depth == 0 && isPunctuation(tokens[index], ","):
			arguments = append(arguments, source(text, tokens, start, index))
			start = index + 1
		}
	}
	if start < to {
		arguments = append(arguments, source(text, tokens, start, to))
	}
	return arguments
}

// pgFunctionNames maps PostgreSQL's functions SQLite does not have to the gateway's (functions.go), and a few to
// SQLite's own under another name.
var pgFunctionNames = map[string]string{
	"format_type": "kitwork_pg_format_type", "obj_description": "kitwork_pg_null", "col_description": "kitwork_pg_null",
	"shobj_description": "kitwork_pg_null", "pg_get_expr": "kitwork_pg_first", "pg_table_is_visible": "kitwork_pg_true",
	"pg_type_is_visible": "kitwork_pg_true", "pg_function_is_visible": "kitwork_pg_true",
	"has_table_privilege": "kitwork_pg_true", "has_schema_privilege": "kitwork_pg_true",
	"has_database_privilege": "kitwork_pg_true", "has_column_privilege": "kitwork_pg_true",
	"has_any_column_privilege": "kitwork_pg_true", "has_function_privilege": "kitwork_pg_true",
	"has_sequence_privilege": "kitwork_pg_true", "pg_has_role": "kitwork_pg_true",
	"pg_is_in_recovery": "kitwork_pg_false", "pg_encoding_to_char": "kitwork_pg_encoding_to_char",
	"pg_get_function_identity_arguments": "kitwork_pg_empty", "pg_get_function_arguments": "kitwork_pg_empty",
	"pg_get_function_result": "kitwork_pg_null", "pg_get_partkeydef": "kitwork_pg_null",
	"pg_get_serial_sequence": "kitwork_pg_null", "pg_get_statisticsobjdef": "kitwork_pg_null",
	"pg_total_relation_size": "kitwork_pg_null", "pg_relation_size": "kitwork_pg_null",
	"pg_table_size": "kitwork_pg_null", "pg_indexes_size": "kitwork_pg_null", "pg_database_size": "kitwork_pg_null",
	"pg_tablespace_location": "kitwork_pg_empty", "pg_size_pretty": "kitwork_pg_size_pretty",
	"quote_ident": "kitwork_pg_quote_ident", "quote_literal": "kitwork_pg_quote_literal",
	"quote_nullable": "kitwork_pg_quote_nullable", "regexp_replace": "kitwork_pg_regexp_replace",
	"current_setting": "kitwork_pg_current_setting", "set_config": "kitwork_pg_set_config",
	"pg_backend_pid": "kitwork_pg_backend_pid", "pg_postmaster_start_time": "kitwork_pg_start_time",
	"inet_server_addr": "kitwork_pg_null", "inet_server_port": "kitwork_pg_null", "inet_client_addr": "kitwork_pg_null",
	"txid_current": "kitwork_pg_null", "array_to_string": "kitwork_pg_array_to_string",
	"array_length": "kitwork_pg_array_length", "array_upper": "kitwork_pg_array_length",
	"array_lower": "kitwork_pg_array_lower", "cardinality": "kitwork_pg_cardinality",
	"string_to_array": "kitwork_pg_string_to_array", "array_agg": "kitwork_pg_array_agg", "now": "kitwork_pg_now",
	"split_part": "kitwork_pg_split_part", "left": "kitwork_pg_left", "right": "kitwork_pg_right",
	"lpad": "kitwork_pg_lpad", "rpad": "kitwork_pg_rpad", "initcap": "kitwork_pg_initcap", "strpos": "instr",
	"btrim": "trim", "char_length": "length", "character_length": "length", "greatest": "max", "least": "min",
}

// renameFunctions renames a call of one of pgFunctionNames, keeping the name PostgreSQL gives a lone select item.
func renameFunctions(text string, tokens []token) (string, bool) {
	for index := 0; index+1 < len(tokens); index++ {
		candidate := tokens[index]
		if candidate.kind != tokenWord || !isPunctuation(tokens[index+1], "(") || (index > 0 && isPunctuation(tokens[index-1], ".")) {
			continue
		}
		name := strings.ToLower(candidate.text)
		renamed, known := pgFunctionNames[name]
		if !known {
			continue
		}
		alias := ""
		if close := matchingClose(tokens, index+1); close > 0 {
			alias = selectItemAlias(tokens, index, close+1, name)
			if alias != "" {
				return text[:candidate.start] + renamed + text[candidate.end:tokens[close].end] + alias + text[tokens[close].end:], true
			}
		}
		return splice(text, tokens, index, index+1, renamed), true
	}
	return text, false
}
