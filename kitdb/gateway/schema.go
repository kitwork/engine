package gateway

import (
	"context"
	"fmt"
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
// the engine stores one; boolean INTEGER 0/1, kind bool; text, varchar(n), char(n) and the times, dates and uuid
// TEXT; json and jsonb TEXT, kind json; bytea BLOB. Another type, an array, numeric without its scale is 0A000. The
// kinds go to kitwork_columns, which the port reads a decimal's scale from, as the engine writes them for its own
// tables.
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
}

// kindChange is what a schema statement does to kitwork_columns.
type kindChange struct {
	table, column string
	kind          columnKind
	drop, rename  bool
	to            string // a rename's new name
}

// schemaPlan is a schema statement as the writer runs it, and what it changes of the kinds.
type schemaPlan struct {
	schema     string
	statements []string
	kinds      []kindChange
	dropTable  []string
	renameFrom string
	renameTo   string
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
// parenthesis, or after an opening one.
func renderTokens(tokens []token) string {
	var text strings.Builder
	for index, current := range tokens {
		if index > 0 {
			previous := tokens[index-1].text
			if previous != "." && previous != "(" && current.text != "." && current.text != "," && current.text != ")" {
				text.WriteByte(' ')
			}
		}
		text.WriteString(current.text)
	}
	return text.String()
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
		if c.keyword("WITH", "TIME", "ZONE") || c.keyword("WITHOUT", "TIME", "ZONE") {
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
	case "TEXT", "VARCHAR", "CHARACTER VARYING", "CHAR", "CHARACTER", "BPCHAR", "CITEXT", "NAME",
		"TIMESTAMP", "TIMESTAMPTZ", "TIMESTAMP ZONE", "DATE", "TIME", "TIMETZ", "TIME ZONE", "UUID", "INTERVAL":
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
	constraints, primary, err := c.session.constraints(rest, schema, name, kind)
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

// constraints translates a column's constraints: REFERENCES of the same schema, a decimal's or a boolean's literal
// DEFAULT as the file holds it; a CHECK on a decimal is refused (it would compare the stored integer).
func (session *Session) constraints(tokens []token, schema, column string, kind columnKind) (string, bool, error) {
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
				return "", false, pgwire.NewError("0A000", fmt.Sprintf("column %s is decimal: a CHECK on it would compare the "+
					"stored integer, not the value", column))
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
	}
	return plan, pgwire.NewError("0A000", fmt.Sprintf("%s is not supported through the port: CREATE TABLE, ALTER TABLE, DROP "+
		"TABLE, CREATE INDEX and DROP INDEX are", strings.Join(words(parsed.tokens[:min(2, len(parsed.tokens))]), " ")))
}

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
	if schema, err = c.session.known(schema); err != nil {
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
			constraint, _, err := c.session.constraints(part, schema, "", columnKind{})
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
	if schema, err = c.session.known(schema); err != nil {
		return schemaPlan{}, err
	}
	if c.keyword("USING") {
		method := c.word()
		c.position++
		if method != "BTREE" {
			return schemaPlan{}, pgwire.NewError("0A000", fmt.Sprintf("index method %s is not supported: SQLite's indexes are b-trees", strings.ToLower(method)))
		}
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
	if schema, err = c.session.known(schema); err != nil {
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
			if skip && c.session.hasColumn(schema, table, name) {
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
			if skip && !c.session.hasColumn(schema, table, name) {
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
			return schemaPlan{}, pgwire.NewError("0A000", "ALTER COLUMN (its type, NOT NULL, DEFAULT) is not supported: SQLite changes "+
				"a column only by rebuilding the table")
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
		if schema, err = c.session.known(schema); err != nil {
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

func (c *schemaCursor) dropIndex() (schemaPlan, error) {
	if c.keyword("CONCURRENTLY") {
		return schemaPlan{}, pgwire.NewError("0A000", "DROP INDEX CONCURRENTLY is not supported")
	}
	ifExists := c.keyword("IF", "EXISTS")
	schema, name, err := c.qualified()
	if err != nil {
		return schemaPlan{}, err
	}
	if schema, err = c.session.known(schema); err != nil {
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
	return schemaPlan{schema: schema, statements: []string{text + quoteIdentifier(name)}}, nil
}

// hasColumn reports whether table of schema has column, as the catalog last read it.
func (session *Session) hasColumn(schema, table, column string) bool {
	found := session.tables[strings.ToLower(schema)][strings.ToLower(table)]
	return found != nil && found.known[strings.ToLower(column)]
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
	if len(plan.statements) == 0 {
		return pgwire.Result{CommandTag: tag}, nil // IF [NOT] EXISTS found nothing to do
	}
	writer, err := session.writerOf(plan.schema)
	if err != nil {
		return pgwire.Result{}, err
	}
	statements := plan.statements
	if len(plan.kinds) > 0 || len(plan.dropTable) > 0 || plan.renameFrom != "" {
		statements = append([]string{columnsTable}, statements...)
	}
	if _, err := writer.Migrate(ctx, session.by(), statements...); err != nil {
		return pgwire.Result{}, writeError(err)
	}
	if len(statements) != len(plan.statements) {
		if _, err := writer.Write(ctx, session.by(), func(transaction *kitdbsqlite.Transaction) error {
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
