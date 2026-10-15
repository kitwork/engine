package highlight

import "strings"

// SQL: the standard's words plus the common PostgreSQL, SQLite and MySQL ones,
// matched in any case (SELECT, select, Select).
//
// Strings are 'single-quoted' with '' for a quote inside, over any number of
// lines; E'…' (PostgreSQL) also takes backslash escapes (E'it''s \'quoted\''),
// and N'…', X'…', B'…'
// and U&'…' are strings with their prefix. $$ … $$ and $tag$ … $tag$ (PostgreSQL
// dollar quoting, a function body) are strings. "Quoted" and `quoted` names
// are names, plain like any other. Comments are -- to the line's end and
// /* … */, which nest as the standard and PostgreSQL say.
//
// Parameters are variables: $1, ?, ?1, :name, @name, $name. Casts (::), JSON
// operators (->, ->>, #>, @>, ?|) and the rest are operators.
//
// A type name is a type, and stays one with a length or precision glued to it
// (VARCHAR(320), NUMERIC(12, 2), INT(11)); followed by anything else in
// parentheses it is a call (date(created_at), json('[]')). A name glued to '('
// is a call, a function (count(*), lower(email)), except right after INTO,
// TABLE, REFERENCES, EXISTS or VIEW, where it names a table with its columns.
// A keyword stays a keyword before '(' (IN (…), VALUES(…), OVER (…)), except
// the few that are also functions: LEFT(s, 3), REPLACE(…), IF(…).

var (
	sqlKeywords = newWordSet(
		"ABORT", "ACTION", "ADD", "AFTER", "AGAINST", "ALL", "ALTER", "ALWAYS", "ANALYZE", "AND",
		"ANY", "ARRAY", "AS", "ASC", "ASYMMETRIC", "ATTACH", "AUTHORIZATION", "AUTOINCREMENT",
		"AUTO_INCREMENT", "BEFORE", "BEGIN", "BETWEEN", "BOTH", "BY", "CALL", "CASCADE", "CASE",
		"CHECK", "CHECKPOINT", "CLUSTER", "COLLATE", "COLUMN", "COMMIT", "CONCURRENTLY",
		"CONFLICT", "CONSTRAINT", "CONTINUE", "COPY", "CREATE", "CROSS", "CUBE", "CURRENT",
		"CURRENT_DATE", "CURRENT_ROLE", "CURRENT_TIME", "CURRENT_TIMESTAMP", "CURRENT_USER",
		"CURSOR", "CYCLE", "DATABASE", "DEALLOCATE", "DECLARE", "DEFAULT", "DEFERRABLE",
		"DEFERRED", "DELETE", "DESC", "DESCRIBE", "DETACH", "DISTINCT", "DO", "DROP", "EACH",
		"ELSE", "ELSIF", "END", "ESCAPE", "EXCEPT", "EXCEPTION", "EXCLUDE", "EXCLUSIVE",
		"EXECUTE", "EXISTS", "EXPLAIN", "EXTENSION", "FETCH", "FILTER", "FIRST", "FOLLOWING",
		"FOR", "FOREIGN", "FROM", "FULL", "FUNCTION", "GENERATED", "GLOB", "GRANT", "GROUP",
		"GROUPING", "GROUPS", "HAVING", "IDENTITY", "IF", "IGNORE", "ILIKE", "IMMEDIATE",
		"IMMUTABLE", "IN", "INCLUDE", "INDEX", "INDEXED", "INHERITS", "INITIALLY", "INNER",
		"INSERT", "INSTEAD", "INTERSECT", "INTO", "IS", "ISNULL", "JOIN", "KEY", "LANGUAGE",
		"LAST", "LATERAL", "LEADING", "LEFT", "LIKE", "LIMIT", "LISTEN", "LOCAL", "LOCALTIME",
		"LOCALTIMESTAMP", "LOCK", "LOOP", "MATCH", "MATCHED", "MATERIALIZED", "MERGE",
		"NATURAL", "NEXT", "NO", "NOT", "NOTHING", "NOTIFY", "NOTNULL", "NULLS", "OF", "OFFSET",
		"ON", "ONLY", "OR", "ORDER", "OTHERS", "OUTER", "OVER", "OVERRIDING", "PARTITION",
		"PERFORM", "PRAGMA", "PRECEDING", "PREPARE", "PRIMARY", "PRIVILEGES", "PROCEDURE",
		"RAISE", "RANGE", "RECURSIVE", "REFERENCES", "REFRESH", "REGEXP", "REINDEX", "RELEASE",
		"RENAME", "REPLACE", "RESTRICT", "RETURN", "RETURNING", "RETURNS", "REVOKE", "RIGHT",
		"ROLE", "ROLLBACK", "ROLLUP", "ROW", "ROWID", "ROWS", "SAVEPOINT", "SCHEMA", "SECURITY",
		"SELECT", "SEQUENCE", "SESSION_USER", "SET", "SETS", "SHOW", "SIMILAR", "SOME",
		"STABLE", "STORED", "STRICT", "SYMMETRIC", "TABLE", "TABLESAMPLE", "TABLESPACE", "TEMP",
		"TEMPORARY", "THEN", "TIES", "TO", "TRAILING", "TRANSACTION", "TRIGGER", "TRUNCATE",
		"TYPE", "UNBOUNDED", "UNION", "UNIQUE", "UNLOGGED", "UNTIL", "UPDATE", "USE", "USER",
		"USING", "VACUUM", "VALUES", "VARIADIC", "VERBOSE", "VIEW", "VIRTUAL", "VOLATILE",
		"WHEN", "WHERE", "WHILE", "WINDOW", "WITH", "WITHIN", "WITHOUT", "WORK", "ZONE",
	)
	sqlTypes = newWordSet(
		"BIGINT", "BIGSERIAL", "BINARY", "BIT", "BLOB", "BOOL", "BOOLEAN", "BOX", "BYTEA", "CHAR",
		"CHARACTER", "CIDR", "CITEXT", "CLOB", "DATE", "DATETIME", "DECIMAL", "DOUBLE", "ENUM",
		"FLOAT", "FLOAT4", "FLOAT8", "INET", "INT", "INT2", "INT4", "INT8", "INTEGER", "INTERVAL",
		"JSON", "JSONB", "LONGTEXT", "MACADDR", "MEDIUMINT", "MEDIUMTEXT", "MONEY", "NCHAR",
		"NUMERIC", "NVARCHAR", "POINT", "PRECISION", "REAL", "SERIAL", "SMALLINT", "SMALLSERIAL",
		"TEXT", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "TIMETZ", "TINYINT", "TINYTEXT", "TSQUERY",
		"TSVECTOR", "UUID", "VARBINARY", "VARCHAR", "VARYING", "XML",
	)
	sqlConstants = newWordSet("NULL", "TRUE", "FALSE", "UNKNOWN")
	// sqlCallableKeywords are keywords that are also functions: LEFT JOIN but
	// LEFT(name, 3).
	sqlCallableKeywords = newWordSet("GROUPING", "IF", "LEFT", "MATCH", "REPLACE", "RIGHT", "TRUNCATE", "USER")
	// sqlNamingKeywords are followed by a table's name, glued to its column list
	// sometimes: INSERT INTO orders(id, total) is not a call.
	sqlNamingKeywords = newWordSet("EXISTS", "INTO", "REFERENCES", "TABLE", "VIEW")
	sqlOperators      = newOperatorsByFirstByte([]string{
		"||/", "->>", "#>>", "!~*", "<>", "!=", "<=", ">=", "==", "||", "&&", "::", ":=", "->", "#>",
		"@>", "<@", "@@", "<<", ">>", "!~", "~*", "~~", "|/", "=>",
		"=", "<", ">", "+", "-", "*", "/", "%", "^", "&", "|", "~", "!", "#", "@",
	})
	// 'it''s', "a""b" and `a``b` need no doubled rule: the string closes at
	// the first quote and a new one opens at the second, two neighbours of one
	// kind that the scanner merges into the same token. An E'…' string does
	// need it, or the part after '' would be read without backslash escapes.
	sqlString        = quoted{closer: "'", multiline: true}
	sqlEscapedString = quoted{closer: "'", escape: '\\', doubled: true, multiline: true}
	sqlQuotedName    = quoted{closer: `"`}
	sqlBacktickName  = quoted{closer: "`"}
)

// sqlLongestWord is the buffer sqlUpper writes into: no word in the sets above
// is longer.
const sqlLongestWord = 32

func tokenizeSQL(scan *scanner) {
	source := scan.source
	var (
		naming bool // the token before was INTO, TABLE, …: a name follows, not a call
		upper  [sqlLongestWord]byte
	)
	for loop := scan.loop(); loop.next(); {
		position := scan.position
		character := source[position]
		next := byteAt(source, position+1)
		switch {
		case isWhitespace(character):
			scan.emit(Plain, scanWhitespace(source, position))
			continue
		case character == '-' && next == '-':
			scan.emit(Comment, scanToLineEnd(source, position))
			continue
		case character == '/' && next == '*':
			scan.emit(Comment, sqlBlockCommentEnd(source, position))
			continue
		}

		wasNaming := naming
		naming = false

		switch {
		case character == '\'':
			scan.emit(String, scanQuoted(source, position+1, sqlString))
		case character == '"':
			scan.emit(Plain, scanQuoted(source, position+1, sqlQuotedName))
		case character == '`':
			scan.emit(Plain, scanQuoted(source, position+1, sqlBacktickName))
		case isDecimalDigit(character) || (character == '.' && isDecimalDigit(next)):
			scan.emit(Number, scanNumber(source, position))
		case character == '$':
			sqlDollar(scan)
		case character == '?':
			switch {
			case next == '|' || next == '&':
				scan.emitLength(Operator, 2) // jsonb ?| and ?&
			default:
				scan.emit(Variable, scanWhile(source, position+1, isDecimalDigit))
			}
		case (character == ':' || character == '@') && scanIdentifier(source, position+1, "") > position+1:
			// :name and @name. "::" and ":=" never get here: no name starts
			// with ':' or '='.
			scan.emit(Variable, scanIdentifier(source, position+1, ""))
		default:
			end := scanIdentifier(source, position, "")
			if end == position {
				if operatorEnd := sqlOperators.match(source, position); operatorEnd > position {
					scan.emit(Operator, operatorEnd)
				} else if strings.IndexByte("()[]{},;.:", character) >= 0 {
					scan.emitLength(Punctuation, 1)
				} else {
					scan.emitCharacter(Plain)
				}
				continue
			}
			word := source[position:end]
			after := byteAt(source, end)
			// A one-letter prefix glued to a quote makes a string: E'\n', N'…'.
			if len(word) == 1 && after == '\'' {
				switch word[0] | 0x20 {
				case 'e':
					scan.emit(String, scanQuoted(source, end+1, sqlEscapedString))
					continue
				case 'n', 'x', 'b':
					scan.emit(String, scanQuoted(source, end+1, sqlString))
					continue
				}
			}
			if len(word) == 1 && word[0]|0x20 == 'u' && after == '&' && byteAt(source, end+1) == '\'' {
				scan.emit(String, scanQuoted(source, end+2, sqlString))
				continue
			}
			key := sqlUpper(&upper, word)
			kind := Plain
			if _, constant := sqlConstants[string(key)]; constant {
				kind = Constant
			} else if _, keyword := sqlKeywords[string(key)]; keyword {
				kind = Keyword
				if _, callable := sqlCallableKeywords[string(key)]; callable && after == '(' {
					kind = Function
				}
				_, naming = sqlNamingKeywords[string(key)]
			} else if _, isType := sqlTypes[string(key)]; isType {
				kind = Type
				inside := scanWhile(source, end+1, func(character byte) bool { return character == ' ' })
				if after == '(' && !isDecimalDigit(byteAt(source, inside)) {
					kind = Function
				}
			} else if after == '(' && !wasNaming {
				kind = Function
			}
			scan.emit(kind, end)
		}
	}
}

// sqlUpper is word in upper case, written into buffer, for a lookup in the
// word sets; a word longer than any of them comes back empty and finds nothing.
// Only ASCII letters change: no keyword has others.
func sqlUpper(buffer *[sqlLongestWord]byte, word string) []byte {
	if len(word) > sqlLongestWord {
		return nil
	}
	for index := 0; index < len(word); index++ {
		character := word[index]
		if character >= 'a' && character <= 'z' {
			character -= 'a' - 'A'
		}
		buffer[index] = character
	}
	return buffer[:len(word)]
}

// sqlDollar emits what starts with '$' at the position: a positional
// parameter ($1), a dollar-quoted string ($$ … $$, $body$ … $body$, to the end
// when it never closes), a named parameter ($name, SQLite), or a lone '$'.
func sqlDollar(scan *scanner) {
	source, position := scan.source, scan.position
	if isDecimalDigit(byteAt(source, position+1)) {
		scan.emit(Variable, scanWhile(source, position+1, isDecimalDigit))
		return
	}
	tagEnd := scanIdentifier(source, position+1, "")
	if byteAt(source, tagEnd) == '$' {
		tag := source[position : tagEnd+1]
		scan.emit(String, scanUntil(source, tagEnd+1, tag))
		return
	}
	if tagEnd > position+1 {
		scan.emit(Variable, tagEnd)
		return
	}
	scan.emitLength(Plain, 1)
}

// sqlBlockCommentEnd reads a /* … */ comment that opens at start, nested ones
// included: /* a /* b */ c */ is one comment. Unclosed, it runs to the end.
func sqlBlockCommentEnd(source string, start int) int {
	depth := 0
	index := start
	for index+1 < len(source) {
		switch {
		case source[index] == '/' && source[index+1] == '*':
			depth++
			index += 2
		case source[index] == '*' && source[index+1] == '/':
			depth--
			index += 2
			if depth == 0 {
				return index
			}
		default:
			index++
		}
	}
	return len(source)
}
