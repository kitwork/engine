package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// The port writes since 05/11/2026 (KITWORK_KITDB.md câu 13.5): KitDB is a database other clients use as they use
// PostgreSQL, the engine one way in among others, so an account granted INSERT, UPDATE or DELETE on a table writes it
// from any tool. A write never runs on the session's own connection, which stays read-only: it goes to the writer of
// its schema's file, KitDB's (kitdb/sqlite.Database.Write), the one the engine's statements write through, so the
// history records every row it changes, by the session's user, and at(), restores, the column copies and the
// full-text index see it.
//
// What a write may do is decided as a read's is, from its program (authorize.go): SQLite EXPLAINs it on the session's
// connection, where foreign keys are off, so the program holds what the statement itself reads and writes, not the
// checks of its foreign keys (PostgreSQL runs those as the table's owner, asking the user nothing). It must write one
// table (and its indexes) of one schema, and read only tables of that schema; the user needs INSERT, UPDATE or DELETE
// on the table by the statement's verb (INSERT OR REPLACE also DELETE, an upsert's DO UPDATE also UPDATE), SELECT on
// what it reads, SELECT on the table for an UPDATE or a DELETE that has a WHERE or a FROM, as PostgreSQL asks. A
// foreign key whose action changes another table when a row of this one goes or changes its key (ON DELETE CASCADE,
// SET NULL, SET DEFAULT; ON UPDATE the same) makes the write an admin's only: the rows it would change are not the
// statement's.
//
// The statement runs as written but for its schema's name: the writer opens the file as SQLite's main, so public.t
// (or billing.t) becomes main.t. A decimal column's value (the file holds it times 10^scale) and a boolean written as
// 't' or 'false' are made the file's own where the statement gives them as a value: a number or a string, or a $n
// parameter, in INSERT … VALUES and in UPDATE … SET. A decimal column the statement reads (a WHERE, an expression)
// is refused, not misread: the stored integer is not the value.
//
// Outside a transaction block each write commits by itself. In one (BEGIN … COMMIT), the first write takes its
// file's writer and keeps it until COMMIT or ROLLBACK: the writer is one per file, and the engine's own writes wait
// meanwhile, so a block that waits more than WriteIdle for its next statement, or holds the writer more than
// WriteLimit in all, is rolled back. The block's reads see the snapshot the block began with, not its own writes.

// Writers opens the writer of a schema file for the session; nil writes nothing (a write is 25006).
type Writers interface {
	Open(path string) (Writer, error)
}

// Writer is one file's writer while the session holds it: kitdb/sqlite.Database's Write, and Migrate for a schema
// statement (schema.go).
type Writer interface {
	Write(ctx context.Context, by string, request func(*kitdbsqlite.Transaction) error) (uint64, error)
	Migrate(ctx context.Context, by string, statements ...string) (uint64, error)
	Close() error
}

const (
	// DefaultWriteIdle is how long a transaction block that wrote waits for its next statement when Options.WriteIdle
	// is not set.
	DefaultWriteIdle = 5 * time.Second
	// DefaultWriteLimit is how long a transaction block may hold its file's writer when Options.WriteLimit is not set.
	DefaultWriteLimit = 30 * time.Second
)

// errBlockRolledBack is a block's request ended by ROLLBACK (or COMMIT of a failed block): nothing is kept.
var errBlockRolledBack = errors.New("gateway: the transaction block was rolled back")

// writeBlock is a transaction block holding its file's writer: a request of the writer that runs the block's
// statements as they come, until COMMIT, ROLLBACK, or a limit.
type writeBlock struct {
	schema     string
	statements chan blockStatement
	outcome    chan error
	cancel     context.CancelFunc
	ended      error // set once the request ended before COMMIT or ROLLBACK (a limit, a failure)
}

// blockStatement is one statement of a block, or its end (commit or rollback when text is empty).
type blockStatement struct {
	text      string
	arguments []any
	end       bool
	commit    bool
	reply     chan blockReply
}

type blockReply struct {
	affected int64
	err      error
}

// writeShape is what a write statement is, read from its tokens.
type writeShape struct {
	verb       string // INSERT, UPDATE, DELETE (REPLACE is INSERT OR REPLACE)
	privileges []string
	reads      bool // UPDATE or DELETE with a WHERE or a FROM: SELECT on the table too
}

// shapeOfWrite reads the verb and what it needs of the user.
func shapeOfWrite(parsed statement) (writeShape, error) {
	tokens := parsed.tokens
	shape := writeShape{verb: parsed.verb}
	depth := 0
	words := map[string]bool{}
	var sequence []string // the words at depth 0, in order
	for _, candidate := range tokens {
		switch candidate.text {
		case "(":
			depth++
			continue
		case ")":
			depth--
			continue
		}
		if depth == 0 && candidate.kind == tokenWord {
			word := candidate.word()
			words[word] = true
			sequence = append(sequence, word)
		}
	}
	if words["RETURNING"] {
		return shape, pgwire.NewError("0A000", "RETURNING is not supported by the port yet: read the rows again after the write")
	}
	switch shape.verb {
	case "INSERT", "REPLACE":
		shape.verb = "INSERT"
		shape.privileges = []string{"insert"}
		replace := parsed.verb == "REPLACE"
		for index := 0; index+1 < len(sequence); index++ {
			if sequence[index] == "OR" && sequence[index+1] == "REPLACE" {
				replace = true
			}
			if sequence[index] == "DO" && sequence[index+1] == "UPDATE" {
				shape.privileges = append(shape.privileges, "update")
			}
		}
		if replace {
			shape.privileges = append(shape.privileges, "delete")
		}
	case "UPDATE":
		shape.privileges = []string{"update"}
		shape.reads = words["WHERE"] || words["FROM"]
	case "DELETE":
		shape.privileges = []string{"delete"}
		shape.reads = words["WHERE"]
	default:
		return shape, readOnlyError(parsed.verb)
	}
	return shape, nil
}

// write runs a write statement: authorized from its program, its values made the file's, then run by its file's
// writer, by itself or inside the transaction block.
func (session *Session) write(ctx context.Context, parsed statement, parameters []pgwire.Parameter) (pgwire.Result, error) {
	if session.options.Writers == nil {
		return pgwire.Result{}, readOnlyError(parsed.verb)
	}
	shape, err := shapeOfWrite(parsed)
	if err != nil {
		return pgwire.Result{}, err
	}
	arguments, err := bindParameters(parameters)
	if err != nil {
		return pgwire.Result{}, err
	}
	// The statement is read (its program, the schema's tables) in a read transaction of the session's, the block's
	// when one is open; outside a block that transaction ends before the writer writes, so a file not yet in WAL mode
	// is not held by it.
	var schema, text string
	prepare := func() error {
		if err := session.refreshSchemas(ctx); err != nil {
			return err
		}
		var table string
		var err error
		if schema, table, err = session.authorizeWrite(ctx, parsed, shape, arguments); err != nil {
			return err
		}
		text, arguments, err = session.fileStatement(parsed, schema, table, arguments)
		return err
	}
	if session.transaction == pgwire.TransactionIdle {
		if err := session.refreshCatalog(ctx); err != nil {
			return pgwire.Result{}, err
		}
		if _, err := session.connection.ExecContext(ctx, "BEGIN"); err != nil {
			return pgwire.Result{}, sqliteError(err)
		}
		err := prepare()
		_, _ = session.connection.ExecContext(context.WithoutCancel(ctx), "ROLLBACK")
		if err != nil {
			return pgwire.Result{}, err
		}
	} else if err := prepare(); err != nil {
		return pgwire.Result{}, err
	}
	var affected int64
	if session.transaction == pgwire.TransactionActive {
		affected, err = session.writeInBlock(ctx, schema, text, arguments)
	} else {
		affected, err = session.writeAlone(ctx, schema, text, arguments)
	}
	if err != nil {
		return pgwire.Result{}, err
	}
	tag := fmt.Sprintf("%s %d", shape.verb, affected)
	if shape.verb == "INSERT" {
		tag = fmt.Sprintf("INSERT 0 %d", affected)
	}
	return pgwire.Result{CommandTag: tag}, nil
}

// authorizeWrite checks a write's program: one table of one attached schema written, only tables of that schema
// read, the privileges its verb needs and SELECT on what it reads; a program that runs another needs an admin.
func (session *Session) authorizeWrite(ctx context.Context, parsed statement, shape writeShape, arguments []any) (string, string, error) {
	operations, err := session.explain(ctx, parsed.text, arguments)
	if err != nil {
		return "", "", err
	}
	type table struct{ schema, name string }
	var written []table
	var read []table
	seenWritten, seenRead := map[table]bool{}, map[table]bool{}
	program := false
	for _, current := range operations {
		switch current.opcode {
		case "OpenWrite", "OpenRead", "ReopenIdx":
			if current.p5&p2IsRegister != 0 {
				return "", "", pgwire.NewError("42501", "permission denied: the statement opens a table SQLite chooses only while it runs")
			}
			if current.p2 == schemaRootPage && current.opcode != "OpenWrite" {
				continue // sqlite_schema: names and definitions only
			}
			schema, attached := session.databases[current.p3]
			if current.p3 == temporaryDatabase || !attached {
				return "", "", pgwire.NewError("0A000", "a statement that writes reads and writes the tables of one schema of the database: "+
					"the catalog (pg_catalog, information_schema) is read by a statement of its own")
			}
			name, known := session.schemas[schema].tables[current.p2]
			if !known {
				return "", "", pgwire.NewError("42501", fmt.Sprintf(
					"permission denied: the statement opens a table of schema %s the session cannot name", schema))
			}
			candidate := table{schema: schema, name: name}
			if current.opcode == "OpenWrite" {
				if !seenWritten[candidate] {
					seenWritten[candidate] = true
					written = append(written, candidate)
				}
			} else if !seenRead[candidate] {
				seenRead[candidate] = true
				read = append(read, candidate)
			}
		case "Program":
			program = true // a trigger: a KitDB file holds none, but a file may
		case "VOpen":
			if _, allowed := session.tableFunctions[current.p4]; !allowed {
				return "", "", pgwire.NewError("42501",
					"permission denied: virtual tables and table-valued functions other than json_each and json_tree are not available")
			}
		case "CreateBtree", "Destroy", "DropTable", "DropIndex", "DropTrigger", "SqlExec", "ParseSchema", "Vacuum",
			"IncrVacuum", "SetCookie", "JournalMode", "Checkpoint", "VUpdate", "VCreate", "VDestroy", "VRename", "LoadAnalysis":
			return "", "", readOnlyError(parsed.verb)
		}
	}
	if len(written) != 1 {
		if len(written) == 0 {
			return "", "", pgwire.NewError("0A000", "the statement writes no table the port can name")
		}
		return "", "", pgwire.NewError("0A000", "a statement through the port writes one table")
	}
	target := written[0]
	for _, other := range read {
		if other.schema != target.schema {
			return "", "", pgwire.NewError("0A000", fmt.Sprintf(
				"a statement that writes %s.%s reads tables of schema %s only: it reads %s.%s", target.schema, target.name,
				target.schema, other.schema, other.name))
		}
	}
	for _, privilege := range shape.privileges {
		allowed, err := session.access.CanModify(ctx, target.schema, target.name, privilege)
		if err != nil {
			return "", "", protocolError(err, fmt.Sprintf("checking the permission on table %s.%s", target.schema, target.name))
		}
		if !allowed {
			return "", "", pgwire.NewError("42501", fmt.Sprintf("permission denied for table %s.%s", target.schema, target.name))
		}
	}
	actions, err := session.foreignKeyActions(ctx, target.schema, target.name, shape)
	if err != nil {
		return "", "", err
	}
	if program || actions != "" {
		admin, err := session.access.Admin(ctx)
		if err != nil {
			return "", "", protocolError(err, "checking the session's account")
		}
		if !admin {
			return "", "", pgwire.NewError("42501", fmt.Sprintf(
				"permission denied: writing %s.%s runs a foreign key's action on other tables (%s), which only an admin may run through the port",
				target.schema, target.name, actions))
		}
	}
	if shape.reads && !seenRead[target] {
		read = append(read, target)
	}
	for _, candidate := range read {
		allowed, err := session.access.CanSelect(ctx, candidate.schema, candidate.name)
		if err != nil {
			return "", "", protocolError(err, fmt.Sprintf("checking the permission on table %s.%s", candidate.schema, candidate.name))
		}
		if !allowed {
			return "", "", pgwire.NewError("42501", fmt.Sprintf("permission denied for table %s.%s", candidate.schema, candidate.name))
		}
	}
	return target.schema, target.name, nil
}

// foreignKeyActions names the foreign keys of schema's tables that change rows of their own when a write of shape
// deletes rows of table (ON DELETE) or changes them (ON UPDATE) with an action other than NO ACTION or RESTRICT;
// empty when none does.
func (session *Session) foreignKeyActions(ctx context.Context, schema, table string, shape writeShape) (string, error) {
	deletes := shape.verb == "DELETE" || slices.Contains(shape.privileges, "delete")
	updates := shape.verb == "UPDATE" || slices.Contains(shape.privileges, "update")
	if !deletes && !updates {
		return "", nil
	}
	rows, err := session.connection.QueryContext(ctx, `SELECT m.name, f."table", f.on_update, f.on_delete FROM `+
		quoteIdentifier(schema)+`.sqlite_schema AS m, pragma_foreign_key_list(m.name, ?) AS f WHERE m.type = 'table'`, schema)
	if err != nil {
		return "", sqliteError(err)
	}
	defer rows.Close()
	var actions []string
	for rows.Next() {
		var child, parent, onUpdate, onDelete string
		if err := rows.Scan(&child, &parent, &onUpdate, &onDelete); err != nil {
			return "", sqliteError(err)
		}
		if !strings.EqualFold(parent, table) {
			continue
		}
		acts := func(action string) bool { return action != "NO ACTION" && action != "RESTRICT" }
		if deletes && acts(onDelete) {
			actions = append(actions, child+" ON DELETE "+onDelete)
		}
		if updates && acts(onUpdate) {
			actions = append(actions, child+" ON UPDATE "+onUpdate)
		}
	}
	if err := rows.Err(); err != nil {
		return "", sqliteError(err)
	}
	return strings.Join(actions, ", "), nil
}

// conversion is how a column's value becomes what the file holds: a decimal of a positive scale, a boolean.
type conversion struct {
	column    string
	decimal   bool
	precision int
	scale     int
}

// conversions are the columns of table whose values the file holds otherwise than written, by lower-case name.
func (session *Session) conversions(schema, table string) (map[string]conversion, []string) {
	found := session.tables[strings.ToLower(schema)][strings.ToLower(table)]
	if found == nil || session.columns == nil {
		return nil, nil
	}
	result := map[string]conversion{}
	for _, column := range found.columns {
		kind, precision, scale, known := session.columns.Column(schema, table, column)
		switch {
		case !known:
		case kind == "decimal" && scale > 0:
			result[strings.ToLower(column)] = conversion{column: column, decimal: true, precision: precision, scale: scale}
		case kind == "bool":
			result[strings.ToLower(column)] = conversion{column: column}
		}
	}
	return result, found.columns
}

// textSplice replaces text[from:to] of the statement.
type textSplice struct {
	from, to int
	text     string
}

// fileStatement is the statement as the writer runs it on the file: its schema's name made main, the values of the
// columns the file holds otherwise made the file's.
func (session *Session) fileStatement(parsed statement, schema, table string, arguments []any) (string, []any, error) {
	tokens := parsed.tokens
	var splices []textSplice
	for index := 0; index+1 < len(tokens); index++ {
		candidate := tokens[index]
		if (candidate.kind == tokenWord || candidate.kind == tokenQuoted) && tokens[index+1].text == "." &&
			strings.EqualFold(identifierText(candidate), schema) {
			splices = append(splices, textSplice{from: candidate.start, to: candidate.end, text: "main"})
		}
	}
	conversions, columns := session.conversions(schema, table)
	if len(conversions) > 0 {
		converted, err := convertValues(parsed, conversions, columns, arguments)
		if err != nil {
			return "", nil, err
		}
		splices = append(splices, converted...)
	}
	text := parsed.text
	// applied from the end, so the earlier positions stay right; splices never overlap
	sort.Slice(splices, func(left, right int) bool { return splices[left].from < splices[right].from })
	for index := len(splices) - 1; index >= 0; index-- {
		text = text[:splices[index].from] + splices[index].text + text[splices[index].to:]
	}
	return text, arguments, nil
}

// identifierText is an identifier token's name, quotes taken off.
func identifierText(candidate token) string {
	if candidate.kind == tokenQuoted && len(candidate.text) >= 2 {
		inner := candidate.text[1 : len(candidate.text)-1]
		return strings.ReplaceAll(inner, `""`, `"`)
	}
	return candidate.text
}

// convertValues finds where the statement gives a converted column its value (INSERT … VALUES, UPDATE … SET, an
// upsert's SET) and makes each value the file's: a literal rewritten, a $n parameter's argument converted. A
// converted decimal column named anywhere else is refused.
func convertValues(parsed statement, conversions map[string]conversion, columns []string, arguments []any) ([]textSplice, error) {
	tokens := parsed.tokens
	var splices []textSplice
	assigned := map[int]bool{} // token indexes that name a converted column as a target, or excluded.column
	converted := map[int]conversion{}
	convertValue := func(from, to int, target conversion) error {
		value := tokens[from:to]
		switch {
		case len(value) == 1 && (value[0].word() == "NULL" || value[0].word() == "DEFAULT"):
			return nil
		case len(value) == 1 && !target.decimal && (value[0].word() == "TRUE" || value[0].word() == "FALSE"):
			return nil
		case len(value) == 3 && value[0].word() == "EXCLUDED" && value[1].text == "." &&
			strings.EqualFold(identifierText(value[2]), target.column):
			assigned[from+2] = true
			return nil
		case len(value) == 1 && value[0].kind == tokenVariable:
			number, err := strconv.Atoi(strings.TrimPrefix(value[0].text, "$"))
			if !strings.HasPrefix(value[0].text, "$") || err != nil || number < 1 || number > len(arguments) {
				return pgwire.NewError("0A000", fmt.Sprintf("column %s takes its value through the port as $1, $2, …", target.column))
			}
			if _, twice := converted[number]; twice {
				return pgwire.NewError("0A000", fmt.Sprintf("parameter $%d gives two columns their values: give each its own", number))
			}
			converted[number] = target
			return nil
		}
		literal, ok := literalText(value)
		if !ok {
			return pgwire.NewError("0A000", fmt.Sprintf("column %s is %s: a write through the port gives it a number, a string "+
				"or a parameter, not an expression", target.column, target.describe()))
		}
		stored, err := target.convert(literal)
		if err != nil {
			return err
		}
		splices = append(splices, textSplice{from: value[0].start, to: value[len(value)-1].end, text: stored})
		return nil
	}
	depthAt := make([]int, len(tokens))
	depth := 0
	for index, candidate := range tokens {
		if candidate.text == ")" {
			depth--
		}
		depthAt[index] = depth
		if candidate.text == "(" {
			depth++
		}
	}
	// SET col = value, … at depth 0 (UPDATE, an upsert's DO UPDATE SET), up to the clause that follows
	for index, candidate := range tokens {
		if candidate.word() != "SET" || depthAt[index] != 0 {
			continue
		}
		position := index + 1
		for position < len(tokens) {
			if tokens[position].text == "(" {
				// (a, b) = (…): a converted column among a is refused below, where it is named
				end := position
				for end < len(tokens) && !(tokens[end].text == ")" && depthAt[end] == 0) {
					end++
				}
				position = end + 1
			} else {
				name := tokens[position]
				if position+1 >= len(tokens) || tokens[position+1].text != "=" {
					break
				}
				valueFrom := position + 2
				valueTo := valueFrom
				for valueTo < len(tokens) && !(depthAt[valueTo] == 0 && (tokens[valueTo].text == "," || isClauseWord(tokens[valueTo].word()))) {
					valueTo++
				}
				if target, found := conversions[strings.ToLower(identifierText(name))]; found && valueFrom < valueTo {
					assigned[position] = true
					if err := convertValue(valueFrom, valueTo, target); err != nil {
						return nil, err
					}
				}
				position = valueTo
			}
			if position < len(tokens) && tokens[position].text == "," && depthAt[position] == 0 {
				position++
				continue
			}
			break
		}
	}
	// INSERT … [(columns)] VALUES (…), (…)
	if parsed.verb == "INSERT" || parsed.verb == "REPLACE" {
		targets := columns
		into := -1
		for index, candidate := range tokens {
			if candidate.word() == "INTO" && depthAt[index] == 0 {
				into = index
				break
			}
		}
		if into >= 0 {
			position := into + 1
			for position < len(tokens) && (tokens[position].kind == tokenWord || tokens[position].kind == tokenQuoted || tokens[position].text == ".") &&
				tokens[position].word() != "VALUES" && tokens[position].word() != "SELECT" && tokens[position].word() != "DEFAULT" &&
				tokens[position].word() != "WITH" {
				position++
			}
			if position < len(tokens) && tokens[position].text == "(" {
				targets = nil
				for position++; position < len(tokens) && tokens[position].text != ")"; position++ {
					if tokens[position].text == "," {
						continue
					}
					targets = append(targets, identifierText(tokens[position]))
					if _, found := conversions[strings.ToLower(identifierText(tokens[position]))]; found {
						assigned[position] = true
					}
				}
				position++
			}
			convertedTargets := false
			for _, name := range targets {
				if _, found := conversions[strings.ToLower(name)]; found {
					convertedTargets = true
				}
			}
			if position < len(tokens) && tokens[position].word() == "VALUES" {
				position++
				for position < len(tokens) && tokens[position].text == "(" {
					column := 0
					valueFrom := position + 1
					end := valueFrom
					for ; end < len(tokens); end++ {
						if (tokens[end].text == "," && depthAt[end] == 1) || (tokens[end].text == ")" && depthAt[end] == 0) {
							if column < len(targets) {
								if target, found := conversions[strings.ToLower(targets[column])]; found {
									if err := convertValue(valueFrom, end, target); err != nil {
										return nil, err
									}
								}
							}
							column++
							valueFrom = end + 1
							if tokens[end].text == ")" {
								break
							}
						}
					}
					position = end + 1
					if position < len(tokens) && tokens[position].text == "," {
						position++
						continue
					}
					break
				}
			} else if convertedTargets && position < len(tokens) && tokens[position].word() != "DEFAULT" {
				return nil, pgwire.NewError("0A000", "a column the file holds otherwise than written (a decimal, a boolean) takes "+
					"its value from INSERT … VALUES through the port, not from a SELECT")
			}
		}
	}
	// a decimal column named anywhere else: the statement would read the stored integer as the value
	for index, candidate := range tokens {
		if assigned[index] || (candidate.kind != tokenWord && candidate.kind != tokenQuoted) {
			continue
		}
		if index+1 < len(tokens) && tokens[index+1].text == "." {
			continue // a qualifier
		}
		if target, found := conversions[strings.ToLower(identifierText(candidate))]; found && target.decimal {
			return nil, pgwire.NewError("0A000", fmt.Sprintf("column %s is %s: a write through the port gives it a value, and "+
				"does not read it (in a WHERE, an expression) yet; find the rows by their key", target.column, target.describe()))
		}
	}
	for number, target := range converted {
		value, err := target.convertArgument(arguments[number-1])
		if err != nil {
			return nil, err
		}
		arguments[number-1] = value
	}
	return splices, nil
}

// isClauseWord reports a word that ends a SET list.
func isClauseWord(word string) bool {
	switch word {
	case "FROM", "WHERE", "RETURNING", "ORDER", "LIMIT", "ON":
		return true
	}
	return false
}

// literalText is the text of a literal value: a number, signed or not, or a string unquoted.
func literalText(value []token) (string, bool) {
	switch {
	case len(value) == 1 && value[0].kind == tokenNumber:
		return value[0].text, true
	case len(value) == 2 && (value[0].text == "-" || value[0].text == "+") && value[1].kind == tokenNumber:
		return value[0].text + value[1].text, true
	case len(value) == 1 && value[0].kind == tokenString && strings.HasPrefix(value[0].text, "'"):
		return unescapeString(value[0].text), true
	}
	return "", false
}

func (target conversion) describe() string {
	if target.decimal {
		return fmt.Sprintf("decimal(%d, %d)", target.precision, target.scale)
	}
	return "boolean"
}

// convert is a literal's text as the file holds the column's value, written as SQL.
func (target conversion) convert(text string) (string, error) {
	if !target.decimal {
		value, err := booleanValue(text, target.column)
		if err != nil {
			return "", err
		}
		return strconv.FormatInt(value, 10), nil
	}
	value, err := storedDecimal(strings.TrimSpace(text), target)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(value, 10), nil
}

// convertArgument is a parameter's value as the file holds the column's.
func (target conversion) convertArgument(argument any) (any, error) {
	switch value := argument.(type) {
	case nil:
		return nil, nil
	case string:
		if !target.decimal {
			return booleanValue(value, target.column)
		}
		return storedDecimal(strings.TrimSpace(value), target)
	case int64:
		if !target.decimal {
			if value != 0 && value != 1 {
				return nil, pgwire.NewError("22P02", fmt.Sprintf("invalid input syntax for type boolean: %d (column %s)", value, target.column))
			}
			return value, nil
		}
		return storedDecimal(strconv.FormatInt(value, 10), target)
	case float64:
		if !target.decimal {
			return nil, pgwire.NewError("22P02", fmt.Sprintf("invalid input syntax for type boolean: %v (column %s)", value, target.column))
		}
		return storedDecimal(strconv.FormatFloat(value, 'f', -1, 64), target)
	}
	return nil, pgwire.NewError("22P02", fmt.Sprintf("column %s is %s: a parameter of %T is not one", target.column, target.describe(), argument))
}

// booleanValue reads a boolean as PostgreSQL does: t, true, yes, on, 1, y and their opposites, any case.
func booleanValue(text, column string) (int64, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "t", "true", "1", "yes", "on", "y":
		return 1, nil
	case "f", "false", "0", "no", "off", "n":
		return 0, nil
	}
	return 0, pgwire.NewError("22P02", fmt.Sprintf("invalid input syntax for type boolean: %q (column %s)", text, column))
}

// storedDecimal is a decimal's text as the file holds it, the integer of value × 10^scale, rounded half away from
// zero to the scale as PostgreSQL rounds numeric, within the column's precision.
func storedDecimal(text string, target conversion) (int64, error) {
	invalid := pgwire.NewError("22P02", fmt.Sprintf("invalid input syntax for type numeric: %q (column %s)", text, target.column))
	scaled, ok := scaledDecimal(text, target.scale)
	if !ok {
		return 0, invalid
	}
	negative := strings.HasPrefix(scaled, "-")
	digits := strings.TrimPrefix(scaled, "-")
	whole, fraction, _ := strings.Cut(digits, ".")
	value, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, pgwire.NewError("22003", fmt.Sprintf("numeric field overflow: %s does not fit column %s", text, target.column))
	}
	if fraction != "" && fraction[0] >= '5' {
		value++
	}
	if target.precision > 0 && len(strconv.FormatInt(value, 10)) > target.precision {
		return 0, pgwire.NewError("22003", fmt.Sprintf("numeric field overflow: %s does not fit column %s, %s", text, target.column, target.describe()))
	}
	if negative {
		value = -value
	}
	return value, nil
}

// by is who wrote, as the history keeps it: the port's user, told from the engine's own principals.
func (session *Session) by() string { return "port:" + session.options.User }

// writerOf is the writer of schema's file, opened at the session's first write to it.
func (session *Session) writerOf(schema string) (Writer, error) {
	if writer := session.writers[schema]; writer != nil {
		return writer, nil
	}
	writer, err := session.options.Writers.Open(session.files[schema])
	if err != nil {
		return nil, pgwire.NewError("55000", fmt.Sprintf("schema %s cannot be written now: %v", schema, err))
	}
	if session.writers == nil {
		session.writers = map[string]Writer{}
	}
	session.writers[schema] = writer
	return writer, nil
}

// writeAlone commits one statement by itself.
func (session *Session) writeAlone(ctx context.Context, schema, text string, arguments []any) (int64, error) {
	writer, err := session.writerOf(schema)
	if err != nil {
		return 0, err
	}
	var affected int64
	_, err = writer.Write(ctx, session.by(), func(transaction *kitdbsqlite.Transaction) error {
		result, err := transaction.Exec(text, arguments...)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		return err
	})
	if err != nil {
		return 0, writeError(err)
	}
	return affected, nil
}

// writeInBlock runs a statement in the transaction block's request, taking the writer at the block's first write.
func (session *Session) writeInBlock(ctx context.Context, schema, text string, arguments []any) (int64, error) {
	if session.block == nil {
		if err := session.startBlock(schema); err != nil {
			return 0, err
		}
	}
	block := session.block
	if block.ended != nil {
		return 0, block.ended
	}
	if block.schema != schema {
		return 0, pgwire.NewError("0A000", fmt.Sprintf("this transaction writes schema %s: write schema %s in a transaction of its own",
			block.schema, schema))
	}
	reply := make(chan blockReply, 1)
	select {
	case block.statements <- blockStatement{text: text, arguments: arguments, reply: reply}:
	case err := <-block.outcome:
		block.ended = blockEnd(err, session.writeIdle(), session.writeLimit())
		return 0, block.ended
	case <-ctx.Done():
		return 0, sqliteError(ctx.Err())
	}
	select {
	case answer := <-reply:
		if answer.err != nil {
			return 0, writeError(answer.err)
		}
		return answer.affected, nil
	case <-ctx.Done():
		// The statement runs in the block's request, which a canceled statement ends: the block is rolled back,
		// as PostgreSQL's is once a statement in it fails.
		block.cancel()
		<-reply
		block.ended = pgwire.NewError("57014", "canceling statement due to user request: the transaction was rolled back")
		return 0, block.ended
	}
}

func (session *Session) writeIdle() time.Duration {
	if session.options.WriteIdle > 0 {
		return session.options.WriteIdle
	}
	return DefaultWriteIdle
}

func (session *Session) writeLimit() time.Duration {
	if session.options.WriteLimit > 0 {
		return session.options.WriteLimit
	}
	return DefaultWriteLimit
}

// errWriteIdle and errWriteLimit end a block's request.
var (
	errWriteIdle  = errors.New("gateway: the block waited too long for its next statement")
	errWriteLimit = errors.New("gateway: the block held the writer too long")
)

// startBlock takes schema's writer for the block: a request that runs the block's statements until it ends.
func (session *Session) startBlock(schema string) error {
	writer, err := session.writerOf(schema)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	block := &writeBlock{schema: schema, statements: make(chan blockStatement), outcome: make(chan error, 1), cancel: cancel}
	idle, limit := session.writeIdle(), session.writeLimit()
	user := session.by()
	started := make(chan struct{})
	go func() {
		_, err := writer.Write(ctx, user, func(transaction *kitdbsqlite.Transaction) error {
			close(started)
			deadline := time.NewTimer(limit)
			defer deadline.Stop()
			for {
				wait := time.NewTimer(idle)
				select {
				case next := <-block.statements:
					wait.Stop()
					if next.end {
						if next.commit {
							next.reply <- blockReply{}
							return nil
						}
						next.reply <- blockReply{}
						return errBlockRolledBack
					}
					result, err := transaction.Exec(next.text, next.arguments...)
					var affected int64
					if err == nil {
						affected, err = result.RowsAffected()
					}
					next.reply <- blockReply{affected: affected, err: err}
				case <-wait.C:
					return errWriteIdle
				case <-deadline.C:
					wait.Stop()
					return errWriteLimit
				case <-ctx.Done():
					wait.Stop()
					return ctx.Err()
				}
			}
		})
		block.outcome <- err
	}()
	select {
	case <-started:
	case err := <-block.outcome: // the writer refused the request before it ran
		cancel()
		return writeError(err)
	}
	session.block = block
	return nil
}

// endBlock ends the block's request: commit keeps its writes (a failed block's COMMIT rolls back, as PostgreSQL's).
func (session *Session) endBlock(commit bool) error {
	block := session.block
	if block == nil {
		return nil
	}
	session.block = nil
	defer block.cancel()
	if block.ended != nil {
		return nil // already rolled back: the statement that met the limit said so
	}
	reply := make(chan blockReply, 1)
	select {
	case block.statements <- blockStatement{end: true, commit: commit, reply: reply}:
		<-reply
		err := <-block.outcome
		if commit && err != nil {
			return writeError(err)
		}
		return nil
	case err := <-block.outcome:
		if commit {
			return blockEnd(err, session.writeIdle(), session.writeLimit())
		}
		return nil
	}
}

// blockEnd is what a block whose request ended by itself tells the client.
func blockEnd(err error, idle, limit time.Duration) error {
	switch {
	case errors.Is(err, errWriteIdle):
		return pgwire.NewError("25P03", fmt.Sprintf("the transaction was rolled back: it held the writer of its file and waited more than %s "+
			"for its next statement (the engine's writes wait meanwhile)", idle))
	case errors.Is(err, errWriteLimit):
		return pgwire.NewError("25P04", fmt.Sprintf("the transaction was rolled back: it held the writer of its file more than %s", limit))
	case err == nil:
		return pgwire.NewError("25P02", "current transaction is aborted, commands ignored until end of transaction block")
	}
	return writeError(err)
}

// closeWriters lets the session's writers go, a block's request rolled back first.
func (session *Session) closeWriters() {
	if session.block != nil {
		_ = session.endBlock(false)
	}
	for schema, writer := range session.writers {
		_ = writer.Close()
		delete(session.writers, schema)
	}
}

// writeError gives a failed write the SQLSTATE PostgreSQL would use.
func writeError(err error) error {
	if err == nil {
		return nil
	}
	var protocol *pgwire.Error
	if errors.As(err, &protocol) {
		return err
	}
	switch {
	case errors.Is(err, kitdbsqlite.ErrStatementRefused):
		return pgwire.NewError("0A000", cleanMessage(err.Error()))
	case errors.Is(err, kitdbsqlite.ErrClosed):
		return pgwire.NewError("55000", "the database's writer is closed")
	}
	return sqliteError(err)
}
