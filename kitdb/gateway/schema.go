package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// Schema statements through the port (05/11/2026, KITWORK_DATABASE.md "Database là gốc"): the database is the root
// of its structure, so a table is made, given a column, renamed or dropped with SQL as on PostgreSQL, by an admin
// (the grants that would let another account do it are not there yet). The statement is written as PostgreSQL
// writes it, the way a tool sends it, and becomes SQLite's:
//
//   - CREATE TABLE [IF NOT EXISTS] [schema.]t (column type [constraints], …, [table constraints]): a STRICT table,
//     each type mapped (below); serial is the rowid's INTEGER PRIMARY KEY, only as the primary key.
//   - ALTER TABLE [IF EXISTS] [ONLY] [schema.]t ADD [COLUMN] [IF NOT EXISTS] …, DROP [COLUMN] [IF EXISTS] c,
//     RENAME [COLUMN] a TO b, RENAME TO n; several actions separated by commas. A column's type, NOT NULL or
//     DEFAULT is not changed (SQLite rebuilds a table for that): 0A000.
//   - CREATE [UNIQUE] INDEX [IF NOT EXISTS] name ON [schema.]t [USING btree] (columns) [WHERE …]; DROP INDEX
//     [IF EXISTS] [schema.]name; DROP TABLE [IF EXISTS] [schema.]t, …
//
// Types: smallint, integer, bigint (int2, int4, int8, int) and their serials are INTEGER; real, double precision
// (float4, float8, float) REAL; numeric(p, s) and decimal(p, s) INTEGER holding the value × 10^s, kind decimal, as
// the engine stores one; boolean INTEGER 0/1, kind bool; date TEXT, kind date; timestamptz (timestamp with time zone)
// TEXT, kind datetime, the engine's timestamp(); kitid TEXT, kind kitid, KitDB's own domain (CREATE DOMAIN kitid AS
// text is taken and does nothing); text, varchar(n), char(n), timestamp without a time zone, time, interval and uuid
// TEXT; json and jsonb TEXT, kind json; bytea BLOB. Another type, an array, numeric without its scale is 0A000. The
// kinds go to kitwork_columns, which the port reads a decimal's scale from, as the engine writes them for its own
// tables. kitwork migrate writes an app's tables with the same statements (TranslateSchema).
//
// The statements run as one commit through the file's writer (Migrate), whose history keeps them as written; the
// kinds are written after, in a commit of their own. A schema statement inside a transaction block commits at once,
// whatever the block does after (SQLite cannot hold one in the writer's transaction of rows); one inside a block that
// wrote is refused.

// columnsTable is kitwork_columns as the engine makes it (work/db.sqlite_columns.go).
const columnsTable = `CREATE TABLE IF NOT EXISTS kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, search_weight INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (table_name, column_name))`

// columnKind is what a column's PostgreSQL type becomes: its SQLite storage and the engine's kind.
type columnKind struct {
	storage   string
	kind      string
	precision int
	scale     int
	serial    bool
	column    string // the column's name as kitwork_columns writes it (structure.go)
	weight    int    // its .searchable() weight, search_weight of kitwork_columns (structure.go)
}

// kindChange is what a schema statement does to kitwork_columns.
type kindChange struct {
	table, column string
	kind          columnKind
	drop, rename  bool
	to            string // a rename's new name
	// the table's search index (searchIndex): unsearch takes every column of the table out of it, search puts column
	// in with weight
	search, unsearch bool
	weight           int
}

// searchIndexName is the name of a table's search index: one per table, the columns its full-text index reads, each
// with a weight (KITWORK_DATABASE.md "Database là gốc"; the port's MATCH, an app's matches). It is no SQLite index: its
// columns are search_weight of kitwork_columns.
func searchIndexName(table string) string { return table + "_search" }

// schemaPlan is a schema statement as the writer runs it, and what it changes of the kinds.
type schemaPlan struct {
	schema     string
	statements []string
	kinds      []kindChange
	dropTable  []string
	renameFrom string
	renameTo   string
	// rewrites are the values a number's new scale rewrites (ALTER COLUMN … TYPE numeric), run with the kinds
	rewrites []Rescale
}

// Rescale is the rewrite of a number column's stored values from one scale to another: a decimal holds its value ×
// 10^scale, an integer is a decimal of scale 0 (KITWORK_KITDB.md mục 8). Guard counts the values the new scale would
// carry past the declared precision (none must be); Update rewrites them, a scale made smaller rounded half away from
// zero as PostgreSQL's numeric rounds.
type Rescale struct {
	Column        string
	Guard, Update string
}

// RescaleStatements is the rewrite of column of table from scale from to scale to, precision digits at most (18).
func RescaleStatements(table, column string, precision, from, to int) Rescale {
	quotedTable, quotedColumn := quoteIdentifier(table), quoteIdentifier(column)
	rescale := Rescale{Column: column}
	if to > from {
		factor := pow10(to - from)
		limit := pow10(min(precision, 18)) - 1
		rescale.Guard = fmt.Sprintf("SELECT count(*) FROM %s WHERE abs(%s) > %d", quotedTable, quotedColumn, limit/factor)
		rescale.Update = fmt.Sprintf("UPDATE %s SET %s = %s * %d WHERE %s IS NOT NULL", quotedTable, quotedColumn, quotedColumn, factor, quotedColumn)
		return rescale
	}
	factor := pow10(from - to)
	half := factor / 2
	rescale.Update = fmt.Sprintf("UPDATE %s SET %s = CASE WHEN %s < 0 THEN -((-%s + %d) / %d) ELSE (%s + %d) / %d END WHERE %s IS NOT NULL",
		quotedTable, quotedColumn, quotedColumn, quotedColumn, half, factor, quotedColumn, half, factor, quotedColumn)
	return rescale
}

// RescalePower is the factor a rescale multiplies or divides by: 10^(to - from).
func RescalePower(from, to int) int64 { return pow10(to - from) }

func pow10(exponent int) int64 {
	value := int64(1)
	for range exponent {
		value *= 10
	}
	return value
}

// schemaCursor reads a schema statement's tokens.
type schemaCursor struct {
	tokens   []token
	position int
	session  *Session
}

func (c *schemaCursor) peek() token {
	if c.position < len(c.tokens) {
		return c.tokens[c.position]
	}
	return token{}
}

func (c *schemaCursor) word() string { return c.peek().word() }

func (c *schemaCursor) keyword(words ...string) bool {
	mark := c.position
	for _, word := range words {
		if c.word() != word {
			c.position = mark
			return false
		}
		c.position++
	}
	return true
}

func (c *schemaCursor) done() bool { return c.position >= len(c.tokens) }

// name reads an identifier.
func (c *schemaCursor) name() (string, error) {
	current := c.peek()
	if current.kind != tokenWord && current.kind != tokenQuoted {
		return "", syntaxError(fmt.Sprintf("expected a name at %q", current.text))
	}
	c.position++
	return identifierText(current), nil
}

// qualified reads [schema.]name; the schema is public when none is written.
func (c *schemaCursor) qualified() (string, string, error) {
	first, err := c.name()
	if err != nil {
		return "", "", err
	}
	if c.peek().text != "." {
		return "public", first, nil
	}
	c.position++
	second, err := c.name()
	if err != nil {
		return "", "", err
	}
	return first, second, nil
}

// group reads a parenthesized group, returning the tokens inside it.
func (c *schemaCursor) group() ([]token, error) {
	if c.peek().text != "(" {
		return nil, syntaxError(fmt.Sprintf("expected ( at %q", c.peek().text))
	}
	depth, start := 0, c.position+1
	for ; c.position < len(c.tokens); c.position++ {
		switch c.tokens[c.position].text {
		case "(":
			depth++
		case ")":
			depth--
			if depth == 0 {
				inside := c.tokens[start:c.position]
				c.position++
				return inside, nil
			}
		}
	}
	return nil, syntaxError("a parenthesis is not closed")
}

// splitTop cuts tokens at the commas outside parentheses.
func splitTop(tokens []token) [][]token {
	var parts [][]token
	depth, start := 0, 0
	for index, current := range tokens {
		switch current.text {
		case "(":
			depth++
		case ")":
			depth--
		case ",":
			if depth == 0 {
				parts = append(parts, tokens[start:index])
				start = index + 1
			}
		}
	}
	return append(parts, tokens[start:])
}

// renderTokens writes tokens back as SQL: a space between two, none around a dot, before a comma or a closing
// parenthesis, or after an opening one, nor between two characters of one operator as written (>=, <=, <>, !=, ||:
// "a > = 0" is no SQL, 07/11/2026).
func renderTokens(tokens []token) string {
	var text strings.Builder
	for index, current := range tokens {
		if index > 0 {
			before := tokens[index-1]
			previous := before.text
			operator := before.kind == tokenPunctuation && current.kind == tokenPunctuation && before.end > 0 && before.end == current.start
			if previous != "." && previous != "(" && current.text != "." && current.text != "," && current.text != ")" && !operator {
				text.WriteByte(' ')
			}
		}
		text.WriteString(current.text)
	}
	return text.String()
}

// decimalCheck reads the CHECK at tokens[at] of a decimal column, CHECK (price >= 0 AND price <= 1000000): comparisons
// of the column with numbers, joined by AND (BETWEEN a AND b too), and gives it with each number rewritten by convert
// (the value to the stored integer, or back), and the index of its closing parenthesis. A CHECK of any other shape
// reads the stored integer where its author means the value: ok is false.
func decimalCheck(tokens []token, at int, column string, convert func(string) (string, error)) (out []token, end int, ok bool, err error) {
	if at+1 >= len(tokens) || tokens[at].word() != "CHECK" || tokens[at+1].text != "(" {
		return nil, 0, false, nil
	}
	depth := 0
	end = -1
	for index := at + 1; index < len(tokens); index++ {
		switch tokens[index].text {
		case "(":
			depth++
		case ")":
			depth--
		}
		if depth == 0 {
			end = index
			break
		}
	}
	if end < 0 {
		return nil, 0, false, nil
	}
	out = append(out, tokens[at], tokens[at+1])
	for index := at + 2; index < end; index++ {
		current := tokens[index]
		switch {
		case current.kind == tokenWord && (current.word() == "AND" || current.word() == "BETWEEN"),
			(current.kind == tokenWord || current.kind == tokenQuoted) && strings.EqualFold(identifierText(current), column):
			out = append(out, current)
		case current.kind == tokenPunctuation && current.text != "" && strings.Contains("<>=!", current.text):
			out = append(out, current)
		case current.kind == tokenNumber, (current.text == "-" || current.text == "+") && index+1 < end && tokens[index+1].kind == tokenNumber:
			last := index
			if current.kind != tokenNumber {
				last = index + 1
			}
			literal, _ := literalText(tokens[index : last+1])
			written, err := convert(literal)
			if err != nil {
				return nil, 0, false, err
			}
			out = append(out, token{kind: tokenNumber, text: written})
			index = last
		default:
			return nil, 0, false, nil
		}
	}
	return append(out, tokens[end]), end, true, nil
}

// typeOf reads a PostgreSQL type at the cursor.
func (c *schemaCursor) typeOf(column string) (columnKind, error) {
	first := c.word()
	if first == "" {
		return columnKind{}, syntaxError(fmt.Sprintf("column %s needs a type", column))
	}
	c.position++
	words := []string{first}
	// the types of more than one word
	switch first {
	case "DOUBLE":
		if c.keyword("PRECISION") {
			words = append(words, "PRECISION")
		}
	case "CHARACTER", "BIT":
		if c.keyword("VARYING") {
			words = append(words, "VARYING")
		}
	case "TIMESTAMP", "TIME":
		switch {
		case c.keyword("WITH", "TIME", "ZONE"):
			words = append(words, "WITH", "ZONE")
		case c.keyword("WITHOUT", "TIME", "ZONE"):
			words = append(words, "ZONE")
		}
	}
	var arguments []int
	if c.peek().text == "(" {
		inside, err := c.group()
		if err != nil {
			return columnKind{}, err
		}
		for _, part := range splitTop(inside) {
			if len(part) != 1 || part[0].kind != tokenNumber {
				return columnKind{}, syntaxError(fmt.Sprintf("the arguments of the type of column %s are numbers", column))
			}
			value, err := strconv.Atoi(part[0].text)
			if err != nil {
				return columnKind{}, syntaxError(fmt.Sprintf("the arguments of the type of column %s are numbers", column))
			}
			arguments = append(arguments, value)
		}
	}
	if strings.HasPrefix(c.peek().text, "[") { // the scanner reads [] as SQLite's bracketed name
		return columnKind{}, pgwire.NewError("0A000", fmt.Sprintf("column %s: arrays are not supported", column))
	}
	name := strings.Join(words, " ")
	switch name {
	case "SMALLINT", "INTEGER", "INT", "BIGINT", "INT2", "INT4", "INT8":
		return columnKind{storage: "INTEGER", kind: "integer"}, nil
	case "SERIAL", "BIGSERIAL", "SMALLSERIAL", "SERIAL4", "SERIAL8", "SERIAL2":
		return columnKind{storage: "INTEGER", kind: "integer", serial: true}, nil
	case "REAL", "FLOAT4", "FLOAT8", "FLOAT", "DOUBLE PRECISION", "DOUBLE":
		return columnKind{storage: "REAL", kind: "float"}, nil
	case "NUMERIC", "DECIMAL":
		if len(arguments) != 2 || arguments[0] < 1 || arguments[1] < 0 || arguments[1] > arguments[0] || arguments[0] > 18 {
			return columnKind{}, pgwire.NewError("0A000", fmt.Sprintf("column %s: write numeric(precision, scale), a precision of 18 "+
				"digits at most: the file holds the value × 10^scale as a 64-bit integer", column))
		}
		return columnKind{storage: "INTEGER", kind: "decimal", precision: arguments[0], scale: arguments[1]}, nil
	case "BOOLEAN", "BOOL":
		return columnKind{storage: "INTEGER", kind: "bool"}, nil
	case "KITID":
		return columnKind{storage: "TEXT", kind: "kitid"}, nil // KitDB's own domain (kitidDomain)
	case "DATE":
		return columnKind{storage: "TEXT", kind: "date"}, nil
	case "UUID":
		return columnKind{storage: "TEXT", kind: "uuid"}, nil
	case "TIMESTAMPTZ", "TIMESTAMP WITH ZONE":
		return columnKind{storage: "TEXT", kind: "datetime"}, nil // the engine's timestamp(): RFC 3339 text in UTC
	case "TEXT", "VARCHAR", "CHARACTER VARYING", "CHAR", "CHARACTER", "BPCHAR", "CITEXT", "NAME",
		"TIMESTAMP", "TIMESTAMP ZONE", "TIME", "TIMETZ", "TIME ZONE", "TIME WITH ZONE", "INTERVAL":
		return columnKind{storage: "TEXT", kind: "text"}, nil
	case "JSON", "JSONB":
		return columnKind{storage: "TEXT", kind: "json"}, nil
	case "BYTEA":
		return columnKind{storage: "BLOB", kind: "blob"}, nil
	}
	return columnKind{}, pgwire.NewError("0A000", fmt.Sprintf("column %s: type %s is not supported through the port", column, strings.ToLower(name)))
}

// columnDefinition translates one column of CREATE TABLE or ADD COLUMN: its name, type and constraints.
func (c *schemaCursor) columnDefinition(schema string, adding bool) (string, string, columnKind, error) {
	name, err := c.name()
	if err != nil {
		return "", "", columnKind{}, err
	}
	kind, err := c.typeOf(name)
	if err != nil {
		return "", "", columnKind{}, err
	}
	rest := c.tokens[c.position:]
	c.position = len(c.tokens)
	constraints, primary, err := translateConstraints(rest, schema, name, kind)
	if err != nil {
		return "", "", columnKind{}, err
	}
	if kind.serial && (!primary || adding) {
		return "", "", columnKind{}, pgwire.NewError("0A000", fmt.Sprintf("column %s: serial is the table's PRIMARY KEY only (the rowid): "+
			"SQLite has no sequences", name))
	}
	definition := quoteIdentifier(name) + " " + kind.storage
	if constraints != "" {
		definition += " " + constraints
	}
	return name, definition, kind, nil
}

// translateConstraints translates a column's constraints: REFERENCES of the same schema, a decimal's or a boolean's
// literal DEFAULT as the file holds it; a CHECK on a decimal is refused (it would compare the stored integer).
func translateConstraints(tokens []token, schema, column string, kind columnKind) (string, bool, error) {
	var out []token
	primary := false
	for index := 0; index < len(tokens); index++ {
		current := tokens[index]
		switch current.word() {
		case "PRIMARY":
			primary = true
		case "REFERENCES":
			out = append(out, current)
			if index+2 < len(tokens) && tokens[index+2].text == "." {
				if !strings.EqualFold(identifierText(tokens[index+1]), schema) {
					return "", false, pgwire.NewError("0A000", fmt.Sprintf("column %s references a table of schema %s: a foreign key "+
						"stays in its schema's file", column, identifierText(tokens[index+1])))
				}
				index += 2 // the schema's name and its dot
			}
			continue
		case "DEFAULT":
			if kind.kind == "bool" && index+1 < len(tokens) && (tokens[index+1].word() == "TRUE" || tokens[index+1].word() == "FALSE") {
				stored := "0"
				if tokens[index+1].word() == "TRUE" {
					stored = "1"
				}
				out = append(out, current, token{kind: tokenNumber, text: stored})
				index++
				continue
			}
			if (kind.kind == "decimal" || kind.kind == "bool") && index+1 < len(tokens) {
				end := index + 2
				if (tokens[index+1].text == "-" || tokens[index+1].text == "+") && end < len(tokens) {
					end++
				}
				if literal, ok := literalText(tokens[index+1 : end]); ok {
					stored, err := conversion{column: column, decimal: kind.kind == "decimal", precision: kind.precision, scale: kind.scale}.convert(literal)
					if err != nil {
						return "", false, err
					}
					out = append(out, current, token{kind: tokenNumber, text: stored})
					index = end - 1
					continue
				}
			}
		case "CHECK":
			if kind.kind == "decimal" {
				// the column's comparisons with numbers, each number the stored integer the file compares (07/11/2026)
				target := conversion{column: column, decimal: true, precision: kind.precision, scale: kind.scale}
				exact := func(literal string) (string, error) {
					// a bound is never rounded: rounding would change what the CHECK takes
					if _, fraction, pointed := strings.Cut(literal, "."); pointed && len(strings.TrimRight(fraction, "0")) > kind.scale {
						return "", pgwire.NewError("22P02", fmt.Sprintf("CHECK of column %s: %s has more decimal places than the column's %d", column, literal, kind.scale))
					}
					return target.convert(literal)
				}
				scaled, end, ok, err := decimalCheck(tokens, index, column, exact)
				if err != nil {
					return "", false, err
				}
				if !ok {
					return "", false, pgwire.NewError("0A000", fmt.Sprintf("column %s is decimal: a CHECK on it compares the column "+
						"with numbers, joined by AND (CHECK (%s >= 0 AND %s <= 100)); any other would compare the stored integer, not the value", column, column, column))
				}
				out = append(out, scaled...)
				index = end
				continue
			}
		}
		if current.text == "::" || (current.kind == tokenVariable) {
			return "", false, pgwire.NewError("0A000", fmt.Sprintf("column %s: casts and parameters are not taken in a schema statement", column))
		}
		out = append(out, current)
	}
	return renderTokens(out), primary, nil
}

// schemaStatement plans a CREATE, ALTER or DROP the port runs; handled is false for one it does not.
func (session *Session) schemaStatement(parsed statement) (plan schemaPlan, err error) {
	c := &schemaCursor{tokens: parsed.tokens, session: session}
	switch {
	case c.keyword("CREATE", "TABLE"):
		return c.createTable()
	case c.keyword("CREATE", "UNIQUE", "INDEX"):
		return c.createIndex(true)
	case c.keyword("CREATE", "INDEX"):
		return c.createIndex(false)
	case c.keyword("ALTER", "TABLE"):
		return c.alterTable()
	case c.keyword("DROP", "TABLE"):
		return c.dropTable()
	case c.keyword("DROP", "INDEX"):
		return c.dropIndex()
	case c.keyword("CREATE", "DOMAIN"):
		return c.createDomain()
	}
	return plan, pgwire.NewError("0A000", fmt.Sprintf("%s is not supported through the port: CREATE TABLE, ALTER TABLE, DROP "+
		"TABLE, CREATE INDEX and DROP INDEX are", strings.Join(words(parsed.tokens[:min(2, len(parsed.tokens))]), " ")))
}

// kitidDomain is the one domain a KitDB database has, always: kitid, the engine's key of id() (a text the engine makes,
// sortable by when it was made). A structure file writes it before the tables that use it, so PostgreSQL runs the file
// too; KitDB takes the statement and does nothing.
const kitidDomain = "CREATE DOMAIN kitid AS text"

// createDomain takes CREATE DOMAIN kitid AS text, which KitDB always has; any other domain is refused.
func (c *schemaCursor) createDomain() (schemaPlan, error) {
	name, err := c.name()
	if err != nil {
		return schemaPlan{}, err
	}
	if !strings.EqualFold(name, "kitid") || !c.keyword("AS", "TEXT") || !c.done() {
		return schemaPlan{}, pgwire.NewError("0A000", "CREATE DOMAIN is not supported through the port: the one domain is KitDB's own, "+
			"kitid AS text")
	}
	return schemaPlan{}, nil
}

// TranslateSchema is a schema statement written as PostgreSQL writes it, the way the port takes it, as the statements
// of SQLite that run it on a file (a STRICT table, a decimal's DEFAULT as the file holds it); kitwork migrate writes
// the tables an app declares with it (work/database_migrate.go). No session: any schema is taken, no column is looked
// up. An error is what the port would answer.
func TranslateSchema(text string) ([]string, error) {
	tokens, err := scan(strings.TrimSuffix(strings.TrimSpace(text), ";"))
	if err != nil {
		return nil, err
	}
	c := &schemaCursor{tokens: tokens}
	var plan schemaPlan
	switch {
	case c.keyword("CREATE", "TABLE"):
		plan, err = c.createTable()
	case c.keyword("CREATE", "UNIQUE", "INDEX"):
		plan, err = c.createIndex(true)
	case c.keyword("CREATE", "INDEX"):
		plan, err = c.createIndex(false)
	case c.keyword("ALTER", "TABLE"):
		plan, err = c.alterTable()
	case c.keyword("DROP", "INDEX"):
		plan, err = c.dropIndex()
	case c.keyword("CREATE", "DOMAIN"):
		plan, err = c.createDomain()
	default:
		return nil, pgwire.NewError("0A000", fmt.Sprintf("%q is not a schema statement kitwork migrate writes", text))
	}
	return plan.statements, err
}

// WrittenName is a name as PostgreSQL and the structure file write it: bare when it can be, quoted when it must.
func WrittenName(name string) string { return writtenName(name) }

// known checks that schema is one of the database's.
func (session *Session) known(schema string) (string, error) {
	for _, name := range session.order {
		if strings.EqualFold(name, schema) {
			return name, nil
		}
	}
	return "", pgwire.NewError("3F000", fmt.Sprintf("schema %q does not exist", schema))
}

func (c *schemaCursor) createTable() (schemaPlan, error) {
	ifNotExists := c.keyword("IF", "NOT", "EXISTS")
	schema, table, err := c.qualified()
	if err != nil {
		return schemaPlan{}, err
	}
	if schema, err = c.known(schema); err != nil {
		return schemaPlan{}, err
	}
	inside, err := c.group()
	if err != nil {
		return schemaPlan{}, err
	}
	if !c.done() {
		return schemaPlan{}, pgwire.NewError("0A000", fmt.Sprintf("table options after the columns (%s) are not supported", c.peek().text))
	}
	plan := schemaPlan{schema: schema}
	var definitions []string
	for _, part := range splitTop(inside) {
		if len(part) == 0 {
			return schemaPlan{}, syntaxError("an empty column")
		}
		switch part[0].word() {
		case "PRIMARY", "UNIQUE", "CHECK", "FOREIGN", "CONSTRAINT":
			constraint, _, err := translateConstraints(part, schema, "", columnKind{})
			if err != nil {
				return schemaPlan{}, err
			}
			definitions = append(definitions, constraint)
			continue
		}
		column := &schemaCursor{tokens: part, session: c.session}
		name, definition, kind, err := column.columnDefinition(schema, false)
		if err != nil {
			return schemaPlan{}, err
		}
		definitions = append(definitions, definition)
		plan.kinds = append(plan.kinds, kindChange{table: table, column: name, kind: kind})
	}
	create := "CREATE TABLE "
	if ifNotExists {
		create += "IF NOT EXISTS "
	}
	plan.statements = []string{create + quoteIdentifier(table) + " (" + strings.Join(definitions, ", ") + ") STRICT"}
	return plan, nil
}

func (c *schemaCursor) createIndex(unique bool) (schemaPlan, error) {
	if c.keyword("CONCURRENTLY") {
		return schemaPlan{}, pgwire.NewError("0A000", "CREATE INDEX CONCURRENTLY is not supported: an index is made in one commit")
	}
	ifNotExists := c.keyword("IF", "NOT", "EXISTS")
	var name string
	if c.word() != "ON" {
		var err error
		if name, err = c.name(); err != nil {
			return schemaPlan{}, err
		}
	}
	if !c.keyword("ON") {
		return schemaPlan{}, syntaxError("expected ON")
	}
	c.keyword("ONLY")
	schema, table, err := c.qualified()
	if err != nil {
		return schemaPlan{}, err
	}
	if schema, err = c.known(schema); err != nil {
		return schemaPlan{}, err
	}
	if c.keyword("USING") {
		method := c.word()
		c.position++
		if method == "SEARCH" {
			return c.searchIndex(unique, name, schema, table)
		}
		if method != "BTREE" {
			return schemaPlan{}, pgwire.NewError("0A000", fmt.Sprintf("index method %s is not supported: SQLite's indexes are b-trees; "+
				"a table's full-text index is USING search", strings.ToLower(method)))
		}
	}
	if strings.EqualFold(name, searchIndexName(table)) {
		return schemaPlan{}, pgwire.NewError("0A000", fmt.Sprintf("%s is the name of table %s's search index: CREATE INDEX %s ON %s USING search (…)",
			name, table, name, table))
	}
	columns, err := c.group()
	if err != nil {
		return schemaPlan{}, err
	}
	if name == "" {
		var parts []string
		for _, part := range splitTop(columns) {
			if len(part) > 0 {
				parts = append(parts, identifierText(part[0]))
			}
		}
		name = table + "_" + strings.Join(parts, "_") + "_idx"
	}
	text := "CREATE "
	if unique {
		text += "UNIQUE "
	}
	text += "INDEX "
	if ifNotExists {
		text += "IF NOT EXISTS "
	}
	text += quoteIdentifier(name) + " ON " + quoteIdentifier(table) + " (" + renderTokens(columns) + ")"
	if c.keyword("INCLUDE") {
		return schemaPlan{}, pgwire.NewError("0A000", "INCLUDE is not supported: list the columns in the index")
	}
	if c.keyword("WHERE") {
		text += " WHERE " + renderTokens(c.tokens[c.position:])
		c.position = len(c.tokens)
	}
	if !c.done() {
		return schemaPlan{}, syntaxError(fmt.Sprintf("unexpected %q after the index's columns", c.peek().text))
	}
	return schemaPlan{schema: schema, statements: []string{text}}, nil
}

func (c *schemaCursor) alterTable() (schemaPlan, error) {
	ifExists := c.keyword("IF", "EXISTS")
	c.keyword("ONLY")
	schema, table, err := c.qualified()
	if err != nil {
		return schemaPlan{}, err
	}
	if schema, err = c.known(schema); err != nil {
		return schemaPlan{}, err
	}
	_ = ifExists
	plan := schemaPlan{schema: schema}
	prefix := "ALTER TABLE " + quoteIdentifier(table) + " "
	for _, action := range splitTop(c.tokens[c.position:]) {
		a := &schemaCursor{tokens: action, session: c.session}
		switch {
		case a.keyword("ADD", "CONSTRAINT"), a.word() == "ADD" && len(action) > 1 && (action[1].word() == "PRIMARY" ||
			action[1].word() == "UNIQUE" || action[1].word() == "FOREIGN" || action[1].word() == "CHECK"):
			return schemaPlan{}, pgwire.NewError("0A000", "ADD CONSTRAINT is not supported: SQLite adds a constraint only by rebuilding the table")
		case a.keyword("ADD"):
			a.keyword("COLUMN")
			skip := a.keyword("IF", "NOT", "EXISTS")
			name, definition, kind, err := a.columnDefinition(schema, true)
			if err != nil {
				return schemaPlan{}, err
			}
			if skip && c.hasColumn(schema, table, name) {
				continue
			}
			plan.statements = append(plan.statements, prefix+"ADD COLUMN "+definition)
			plan.kinds = append(plan.kinds, kindChange{table: table, column: name, kind: kind})
		case a.keyword("DROP", "CONSTRAINT"):
			return schemaPlan{}, pgwire.NewError("0A000", "DROP CONSTRAINT is not supported: SQLite drops a constraint only by rebuilding the table")
		case a.keyword("DROP"):
			a.keyword("COLUMN")
			skip := a.keyword("IF", "EXISTS")
			name, err := a.name()
			if err != nil {
				return schemaPlan{}, err
			}
			if a.keyword("CASCADE") {
				return schemaPlan{}, pgwire.NewError("0A000", "DROP COLUMN … CASCADE is not supported: drop what depends on the column first")
			}
			a.keyword("RESTRICT")
			if skip && !c.hasColumn(schema, table, name) {
				continue
			}
			plan.statements = append(plan.statements, prefix+"DROP COLUMN "+quoteIdentifier(name))
			plan.kinds = append(plan.kinds, kindChange{table: table, column: name, drop: true})
		case a.keyword("RENAME", "TO"):
			to, err := a.name()
			if err != nil {
				return schemaPlan{}, err
			}
			plan.statements = append(plan.statements, prefix+"RENAME TO "+quoteIdentifier(to))
			plan.renameFrom, plan.renameTo = table, to
		case a.keyword("RENAME"):
			a.keyword("COLUMN")
			from, err := a.name()
			if err != nil {
				return schemaPlan{}, err
			}
			if !a.keyword("TO") {
				return schemaPlan{}, syntaxError("expected TO")
			}
			to, err := a.name()
			if err != nil {
				return schemaPlan{}, err
			}
			plan.statements = append(plan.statements, prefix+"RENAME COLUMN "+quoteIdentifier(from)+" TO "+quoteIdentifier(to))
			plan.kinds = append(plan.kinds, kindChange{table: table, column: from, rename: true, to: to})
		case a.keyword("ALTER"):
			a.keyword("COLUMN")
			name, err := a.name()
			if err != nil {
				return schemaPlan{}, err
			}
			if !a.keyword("TYPE") && !a.keyword("SET", "DATA", "TYPE") {
				return schemaPlan{}, pgwire.NewError("0A000", "ALTER COLUMN … NOT NULL or DEFAULT is not supported: SQLite changes a column "+
					"only by rebuilding the table")
			}
			kind, err := a.typeOf(name)
			if err != nil {
				return schemaPlan{}, err
			}
			if !a.done() {
				return schemaPlan{}, pgwire.NewError("0A000", "ALTER COLUMN … TYPE … USING is not supported")
			}
			rescale, err := c.rescale(schema, table, name, kind)
			if err != nil {
				return schemaPlan{}, err
			}
			plan.rewrites = append(plan.rewrites, rescale)
			plan.kinds = append(plan.kinds, kindChange{table: table, column: name, kind: kind})
			continue
		default:
			return schemaPlan{}, pgwire.NewError("0A000", fmt.Sprintf("ALTER TABLE … %s is not supported through the port", strings.Join(words(action[:min(2, len(action))]), " ")))
		}
		if !a.done() {
			return schemaPlan{}, syntaxError(fmt.Sprintf("unexpected %q in ALTER TABLE", a.peek().text))
		}
	}
	return plan, nil
}

func (c *schemaCursor) dropTable() (schemaPlan, error) {
	ifExists := c.keyword("IF", "EXISTS")
	var plan schemaPlan
	for {
		schema, table, err := c.qualified()
		if err != nil {
			return schemaPlan{}, err
		}
		if schema, err = c.known(schema); err != nil {
			return schemaPlan{}, err
		}
		if plan.schema != "" && plan.schema != schema {
			return schemaPlan{}, pgwire.NewError("0A000", "drop the tables of one schema in one statement")
		}
		plan.schema = schema
		text := "DROP TABLE "
		if ifExists {
			text += "IF EXISTS "
		}
		plan.statements = append(plan.statements, text+quoteIdentifier(table))
		plan.dropTable = append(plan.dropTable, table)
		if c.peek().text != "," {
			break
		}
		c.position++
	}
	if c.keyword("CASCADE") {
		return schemaPlan{}, pgwire.NewError("0A000", "DROP TABLE … CASCADE is not supported: drop what depends on the table first")
	}
	c.keyword("RESTRICT")
	if !c.done() {
		return schemaPlan{}, syntaxError(fmt.Sprintf("unexpected %q in DROP TABLE", c.peek().text))
	}
	return plan, nil
}

// searchIndex reads the rest of CREATE INDEX <table>_search ON <table> USING search (column, …) [WITH (column = weight,
// …)]: the table's search index, set as written (the columns it had before are taken out), a column's weight 1 unless
// WITH gives another. It changes kitwork_columns only.
func (c *schemaCursor) searchIndex(unique bool, name, schema, table string) (schemaPlan, error) {
	want := searchIndexName(table)
	switch {
	case unique:
		return schemaPlan{}, pgwire.NewError("0A000", "a search index is not UNIQUE")
	case name != "" && !strings.EqualFold(name, want):
		return schemaPlan{}, pgwire.NewError("0A000", fmt.Sprintf("a table has one search index, named %s: CREATE INDEX %s ON %s USING search (…)",
			want, want, table))
	}
	inside, err := c.group()
	if err != nil {
		return schemaPlan{}, err
	}
	weights := map[string]int{}
	var columns []string
	for _, part := range splitTop(inside) {
		if len(part) != 1 || (part[0].kind != tokenWord && part[0].kind != tokenQuoted) {
			return schemaPlan{}, syntaxError("a search index lists its columns by name")
		}
		column := identifierText(part[0])
		if _, twice := weights[strings.ToLower(column)]; twice {
			return schemaPlan{}, syntaxError(fmt.Sprintf("column %s is in the search index twice", column))
		}
		weights[strings.ToLower(column)] = 1
		columns = append(columns, column)
	}
	if len(columns) == 0 {
		return schemaPlan{}, syntaxError("a search index reads at least one column")
	}
	if c.keyword("WITH") {
		options, err := c.group()
		if err != nil {
			return schemaPlan{}, err
		}
		for _, option := range splitTop(options) {
			if len(option) != 3 || option[1].text != "=" || option[2].kind != tokenNumber {
				return schemaPlan{}, syntaxError("a search index's WITH gives a column's weight: WITH (title = 2)")
			}
			column := strings.ToLower(identifierText(option[0]))
			weight, err := strconv.Atoi(option[2].text)
			if _, listed := weights[column]; !listed || err != nil || weight < 1 {
				return schemaPlan{}, syntaxError(fmt.Sprintf("WITH (%s = %s): a weight is a whole number from 1, of a column the index reads",
					identifierText(option[0]), option[2].text))
			}
			weights[column] = weight
		}
	}
	if !c.done() {
		return schemaPlan{}, syntaxError(fmt.Sprintf("unexpected %q after the search index", c.peek().text))
	}
	plan := schemaPlan{schema: schema, kinds: []kindChange{{table: table, unsearch: true}}}
	for _, column := range columns {
		plan.kinds = append(plan.kinds, kindChange{table: table, column: column, search: true, weight: weights[strings.ToLower(column)]})
	}
	return plan, nil
}

func (c *schemaCursor) dropIndex() (schemaPlan, error) {
	if c.keyword("CONCURRENTLY") {
		return schemaPlan{}, pgwire.NewError("0A000", "DROP INDEX CONCURRENTLY is not supported")
	}
	ifExists := c.keyword("IF", "EXISTS")
	schema, name, err := c.qualified()
	if err != nil {
		return schemaPlan{}, err
	}
	if schema, err = c.known(schema); err != nil {
		return schemaPlan{}, err
	}
	c.keyword("RESTRICT")
	if !c.done() {
		return schemaPlan{}, syntaxError(fmt.Sprintf("unexpected %q in DROP INDEX", c.peek().text))
	}
	text := "DROP INDEX "
	if ifExists {
		text += "IF EXISTS "
	}
	if table, search := strings.CutSuffix(name, "_search"); search && table != "" {
		// a table's search index: its columns leave it; a SQLite index of that name, from before, goes too
		return schemaPlan{schema: schema, statements: []string{"DROP INDEX IF EXISTS " + quoteIdentifier(name)},
			kinds: []kindChange{{table: table, unsearch: true}}}, nil
	}
	return schemaPlan{schema: schema, statements: []string{text + quoteIdentifier(name)}}, nil
}

// rescale is ALTER COLUMN column TYPE kind of a number column: its scale changes, numeric(p, s) to numeric(p, s2) or to
// bigint and back, the values it holds rewritten (a decimal stores its value × 10^scale). Any other change of type is a
// rebuild of the table SQLite needs, refused. The column's kind is the session's (kitwork_columns).
func (c *schemaCursor) rescale(schema, table, column string, kind columnKind) (Rescale, error) {
	if kind.kind != "decimal" && kind.kind != "integer" || kind.serial {
		return Rescale{}, pgwire.NewError("0A000", "ALTER COLUMN … TYPE changes the scale of a number only (numeric(p, s), bigint): "+
			"another type is a rebuild of the table SQLite needs")
	}
	if c.session == nil || c.session.columns == nil {
		return Rescale{}, pgwire.NewError("0A000", "ALTER COLUMN … TYPE needs the column's kind, which only a session reads")
	}
	current, _, scale, found := c.session.columns.Column(schema, table, column)
	if !found || current != "decimal" && current != "integer" {
		return Rescale{}, pgwire.NewError("0A000", fmt.Sprintf("ALTER COLUMN … TYPE changes the scale of a number only: column %s is no "+
			"number of kitwork_columns (decimal, integer), and another change of type is a rebuild of the table SQLite needs", column))
	}
	from, to := 0, kind.scale
	if current == "decimal" {
		from = scale
	}
	precision := kind.precision
	if kind.kind == "integer" {
		precision = 18
	}
	return RescaleStatements(table, column, precision, from, to), nil
}

// hasColumn reports whether table of schema has column, as the session's catalog last read it; false without a
// session (a structure file read, structure.go).
func (c *schemaCursor) hasColumn(schema, table, column string) bool {
	if c.session == nil {
		return false
	}
	found := c.session.tables[strings.ToLower(schema)][strings.ToLower(table)]
	return found != nil && found.known[strings.ToLower(column)]
}

// known is schema as the session names it: one of the database's; any without a session (a structure file names
// none: its tables are its file's).
func (c *schemaCursor) known(schema string) (string, error) {
	if c.session == nil {
		return schema, nil
	}
	return c.session.known(schema)
}

// changeSchema runs a schema statement: an admin's, through the writer of its file, then its kinds.
func (session *Session) changeSchema(ctx context.Context, parsed statement) (pgwire.Result, error) {
	admin, err := session.access.Admin(ctx)
	if err != nil {
		return pgwire.Result{}, protocolError(err, "checking the session's account")
	}
	if !admin {
		return pgwire.Result{}, pgwire.NewError("42501", "permission denied: only an admin changes the schema through the port")
	}
	if session.block != nil {
		return pgwire.Result{}, pgwire.NewError("0A000", "a schema statement cannot follow a write in the same transaction: commit first")
	}
	if session.transaction == pgwire.TransactionIdle {
		if err := session.refreshCatalog(ctx); err != nil {
			return pgwire.Result{}, err
		}
	}
	plan, err := session.schemaStatement(parsed)
	if err != nil {
		return pgwire.Result{}, err
	}
	tag := strings.Join(words(parsed.tokens[:2]), " ")
	if tag == "CREATE UNIQUE" {
		tag = "CREATE INDEX"
	}
	if len(plan.statements) == 0 && len(plan.kinds) == 0 {
		return pgwire.Result{CommandTag: tag}, nil // IF [NOT] EXISTS found nothing to do
	}
	writer, err := session.writerOf(plan.schema)
	if err != nil {
		return pgwire.Result{}, err
	}
	statements := plan.statements
	if len(plan.kinds) > 0 || len(plan.dropTable) > 0 || plan.renameFrom != "" {
		prefix := []string{columnsTable}
		if lacksSearchWeight(session.files[plan.schema]) {
			prefix = append(prefix, columnsSearchWeight) // a kitwork_columns written before 31/10/2026
		}
		statements = append(prefix, statements...)
	}
	if _, err := writer.Migrate(ctx, session.by(), statements...); err != nil {
		return pgwire.Result{}, writeError(err)
	}
	if len(statements) != len(plan.statements) {
		if _, err := writer.Write(ctx, session.by(), func(transaction *kitdbsqlite.Transaction) error {
			for _, rewrite := range plan.rewrites {
				if err := runRescale(transaction, rewrite); err != nil {
					return err
				}
			}
			return writeKinds(transaction, plan)
		}); err != nil {
			return pgwire.Result{}, fmt.Errorf("gateway: the schema changed, its column kinds were not written: %w", writeError(err))
		}
	}
	session.kindsChanged = true
	return pgwire.Result{CommandTag: tag}, nil
}

// writeKinds writes what plan changed of kitwork_columns.
func writeKinds(transaction *kitdbsqlite.Transaction, plan schemaPlan) error {
	for _, table := range plan.dropTable {
		if _, err := transaction.Exec(`DELETE FROM kitwork_columns WHERE table_name = ?`, table); err != nil {
			return err
		}
	}
	if plan.renameFrom != "" {
		if _, err := transaction.Exec(`UPDATE kitwork_columns SET table_name = ? WHERE table_name = ?`, plan.renameTo, plan.renameFrom); err != nil {
			return err
		}
	}
	for _, change := range plan.kinds {
		var err error
		switch {
		case change.drop:
			_, err = transaction.Exec(`DELETE FROM kitwork_columns WHERE table_name = ? AND column_name = ?`, change.table, change.column)
		case change.rename:
			_, err = transaction.Exec(`UPDATE kitwork_columns SET column_name = ? WHERE table_name = ? AND column_name = ?`, change.to, change.table, change.column)
		case change.search || change.unsearch:
			err = writeSearch(transaction, change)
		default:
			_, err = transaction.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (table_name, column_name) DO UPDATE SET kind = excluded.kind, precision = excluded.precision, scale = excluded.scale`,
				change.table, change.column, change.kind.kind, change.kind.precision, change.kind.scale)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// runRescale rewrites a number column's values in the transaction, refused when one would pass its precision.
func runRescale(transaction *kitdbsqlite.Transaction, rescale Rescale) error {
	if rescale.Guard != "" {
		rows, err := transaction.Query(rescale.Guard)
		if err != nil {
			return err
		}
		over := 0
		if rows.Next() {
			err = rows.Scan(&over)
		}
		rows.Close()
		if err != nil {
			return err
		}
		if over > 0 {
			return pgwire.NewError("22003", fmt.Sprintf("numeric field overflow: %d values of column %s pass its precision at the new scale", over, rescale.Column))
		}
	}
	_, err := transaction.Exec(rescale.Update)
	return err
}

// columnsSearchWeight gives a kitwork_columns written before 31/10/2026 its search_weight, as the engine did at each
// boot until the database became the root (work/db.sqlite_columns.go).
const columnsSearchWeight = `ALTER TABLE kitwork_columns ADD COLUMN search_weight INTEGER NOT NULL DEFAULT 0`

// lacksSearchWeight reports a file whose kitwork_columns has no search_weight, read on a connection of its own.
func lacksSearchWeight(path string) bool {
	if path == "" {
		return false
	}
	file, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return false
	}
	defer file.Close()
	var tables, weights int
	if file.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_columns'`).Scan(&tables) != nil || tables == 0 {
		return false
	}
	if file.QueryRow(`SELECT count(*) FROM pragma_table_info('kitwork_columns') WHERE name = 'search_weight'`).Scan(&weights) != nil {
		return false
	}
	return weights == 0
}

// writeSearch writes a change of a table's search index in kitwork_columns: every column of the table out of it, or one
// column in with its weight (a column without a row of its own gets one of kind text, what a searchable column is).
func writeSearch(transaction *kitdbsqlite.Transaction, change kindChange) error {
	if change.unsearch {
		_, err := transaction.Exec(`UPDATE kitwork_columns SET search_weight = 0 WHERE table_name = ?`, change.table)
		return err
	}
	_, err := transaction.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale, search_weight) VALUES (?, ?, 'text', 0, 0, ?)
		ON CONFLICT (table_name, column_name) DO UPDATE SET search_weight = excluded.search_weight`, change.table, change.column, change.weight)
	return err
}

// fileColumns are the kinds the files' kitwork_columns hold, read again after the session changed the schema, over
// what the session was opened with.
type fileColumns struct {
	base  Columns
	kinds map[string]columnKind
}

func (columns fileColumns) Column(schema, table, column string) (string, int, int, bool) {
	if found, ok := columns.kinds[strings.ToLower(schema+"."+table+"."+column)]; ok {
		return found.kind, found.precision, found.scale, true
	}
	if columns.base != nil {
		return columns.base.Column(schema, table, column)
	}
	return "", 0, 0, false
}

// reloadKinds reads kitwork_columns of every schema again, outside a transaction block.
func (session *Session) reloadKinds(ctx context.Context) error {
	kinds := map[string]columnKind{}
	for _, schema := range session.order {
		var held int
		if err := session.connection.QueryRowContext(ctx, `SELECT count(*) FROM `+quoteIdentifier(schema)+
			`.sqlite_schema WHERE type = 'table' AND name = 'kitwork_columns'`).Scan(&held); err != nil {
			return sqliteError(err)
		}
		if held == 0 {
			continue
		}
		rows, err := session.connection.QueryContext(ctx, `SELECT table_name, column_name, kind, precision, scale FROM `+
			quoteIdentifier(schema)+`.kitwork_columns`)
		if err != nil {
			return sqliteError(err)
		}
		for rows.Next() {
			var table, column string
			var found columnKind
			if err := rows.Scan(&table, &column, &found.kind, &found.precision, &found.scale); err != nil {
				rows.Close()
				return sqliteError(err)
			}
			kinds[strings.ToLower(schema+"."+table+"."+column)] = found
		}
		rows.Close()
	}
	base := session.columns
	if previous, ok := base.(fileColumns); ok {
		base = previous.base
	}
	session.columns = fileColumns{base: base, kinds: kinds}
	session.catalogVersions = nil // the decimal tables are read again with the kinds
	return nil
}
