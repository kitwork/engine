package gateway

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// A CHECK a write breaks is told as PostgreSQL tells it (08/11/2026): new row for relation "notes" violates check
// constraint "notes_text_check", 23514, the schema, the table and the constraint in the error's fields s, t and n,
// which lib/pq, pgx and the ORMs read to know which rule a row broke. SQLite says only "CHECK constraint failed: " and
// the constraint's name, or for one written without a name the text of its expression (the trial of 07/11/2026 read
// "constraint failed: CHECK constraint failed: length (text) >= 1"). The port reads the table's CREATE TABLE and gives
// such a CHECK the name PostgreSQL gives it (AddRelationNewConstraints, ChooseConstraintName): <table>_<column>_check
// when its expression reads one column of the table, <table>_check when it reads more or none, check1, check2, … for a
// name the table already has, cut to 63 bytes as makeObjectName cuts it. As the catalog's <table>_pkey, _key and _fkey
// (catalog.go), the name is read from the file, never written to it: a file made before has it too. Two limits: a
// renamed table's CHECK takes the new name (PostgreSQL keeps the one it chose), and a name is unique within its table,
// not within the schema.

// checkFailed is how SQLite begins the message of a broken CHECK.
const checkFailed = "CHECK constraint failed: "

// maximumNameBytes is PostgreSQL's NAMEDATALEN - 1: the bytes of a name it makes.
const maximumNameBytes = 63

// checkConstraint is one CHECK of a table: the name PostgreSQL knows it by, what SQLite says of it when it fails, its
// expression as the file holds it, the tokens CHECK ( … ) for the catalog, and the table's columns it reads.
type checkConstraint struct {
	name       string
	reported   string
	expression string
	tokens     []token
	columns    []string
}

// checkConstraints are the CHECKs of a CREATE TABLE as SQLite keeps it, in the order written; nil when it does not read.
func checkConstraints(create string) []checkConstraint {
	tokens, err := scan(create)
	if err != nil {
		return nil
	}
	c := &schemaCursor{tokens: tokens}
	if !c.keyword("CREATE", "TABLE") {
		return nil
	}
	c.keyword("IF", "NOT", "EXISTS")
	_, table, err := c.qualified()
	if err != nil {
		return nil
	}
	inside, err := c.group()
	if err != nil {
		return nil
	}
	parts := splitTop(inside)
	columns := map[string]string{} // by lower-case name: the name as declared
	for _, part := range parts {
		if len(part) > 0 && !tableConstraintWords[part[0].word()] && isIdentifier(part[0]) {
			name := identifierText(part[0])
			columns[strings.ToLower(name)] = name
		}
	}
	var checks []checkConstraint
	taken := map[string]bool{}
	for _, part := range parts {
		for index := 0; index < len(part); index++ {
			if part[index].word() != "CHECK" || index+1 >= len(part) || part[index+1].text != "(" {
				continue
			}
			end := matchingClose(part, index+1)
			if end < 0 {
				return checks
			}
			expression := part[index+2 : end]
			check := checkConstraint{expression: strings.TrimSpace(source(create, expression, 0, len(expression))),
				tokens: part[index : end+1], columns: readColumns(expression, columns)}
			if index >= 2 && part[index-2].word() == "CONSTRAINT" && isIdentifier(part[index-1]) {
				check.name = identifierText(part[index-1])
				check.reported = check.name
			} else {
				column := ""
				if len(check.columns) == 1 {
					column = check.columns[0]
				}
				check.name = chooseCheckName(table, column, taken)
				check.reported = check.expression
			}
			taken[check.name] = true
			checks = append(checks, check)
			index = end
		}
	}
	return checks
}

// tableConstraintWords begin a table's constraint, not a column, in a CREATE TABLE.
var tableConstraintWords = map[string]bool{"CONSTRAINT": true, "PRIMARY": true, "UNIQUE": true, "CHECK": true, "FOREIGN": true}

// readColumns are the columns of the table an expression reads, each once, in the order it first reads them.
func readColumns(expression []token, columns map[string]string) []string {
	var found []string
	for index, current := range expression {
		if !isIdentifier(current) {
			continue
		}
		if next := index + 1; next < len(expression) && (expression[next].text == "(" || expression[next].text == ".") {
			continue // a function, or the table before a column
		}
		if name, known := columns[strings.ToLower(identifierText(current))]; known && !slices.Contains(found, name) {
			found = append(found, name)
		}
	}
	return found
}

// chooseCheckName is the name PostgreSQL chooses for a CHECK of table, of column when it reads one: the first of
// <table>_<column>_check, …_check1, …_check2 the table does not have yet.
func chooseCheckName(table, column string, taken map[string]bool) string {
	label := "check"
	for pass := 1; ; pass++ {
		if name := objectName(table, column, label); !taken[name] {
			return name
		}
		label = "check" + strconv.Itoa(pass)
	}
}

// objectName is PostgreSQL's makeObjectName: first_second_label, the longer of first and second cut a byte at a
// time (never inside a character) until the whole fits in 63 bytes.
func objectName(first, second, label string) string {
	overhead := len(label) + 1
	if second != "" {
		overhead++
	}
	available := maximumNameBytes - overhead
	firstBytes, secondBytes := len(first), len(second)
	for firstBytes+secondBytes > available {
		if firstBytes > secondBytes {
			firstBytes--
		} else {
			secondBytes--
		}
	}
	name := clipBytes(first, firstBytes)
	if second != "" {
		name += "_" + clipBytes(second, secondBytes)
	}
	return name + "_" + label
}

// clipBytes is the longest start of text of at most limit bytes that ends between two characters (pg_mbcliplen).
func clipBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

// checkViolation is err, the error of a write to table of schema, told as PostgreSQL tells a broken CHECK when it is
// one; any other error as it came.
func (session *Session) checkViolation(ctx context.Context, schema, table string, err error) error {
	var protocol *pgwire.Error
	if table == "" || !errors.As(err, &protocol) || protocol.Code != "23514" {
		return err
	}
	at := strings.Index(protocol.Message, checkFailed)
	if at < 0 {
		return err
	}
	reported := protocol.Message[at+len(checkFailed):]
	name := ""
	var create string
	query := "SELECT sql FROM " + quoteIdentifier(schema) + ".sqlite_schema WHERE type = 'table' AND name = ? COLLATE NOCASE"
	if session.connection.QueryRowContext(context.WithoutCancel(ctx), query, table).Scan(&create) == nil {
		for _, check := range checkConstraints(create) {
			if check.reported == reported {
				name = check.name
				break
			}
		}
	}
	if name == "" {
		// a CHECK of another table (a trigger's), or a file that does not read: what SQLite said of it
		return &pgwire.Error{Code: "23514", Message: `new row for relation "` + table + `" violates a check constraint: ` + reported,
			Schema: schema, Table: table}
	}
	return &pgwire.Error{Code: "23514", Message: `new row for relation "` + table + `" violates check constraint "` + name + `"`,
		Schema: schema, Table: table, Constraint: name}
}
