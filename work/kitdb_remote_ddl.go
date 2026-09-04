package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	requestscope "github.com/kitwork/engine/request"
)

const (
	kitSQLMaximumDDLColumns       = 512
	kitSQLDropTableOperationLimit = 100_000
	kitSQLDropTableByteLimit      = 32 << 20
	kitSQLDropOperationOverhead   = 9
)

// kitSQLCreateTable is a frontend plan. Execution normalizes it into the same
// Schema IR v2 used by Kitwork struct(), then commits that IR through KitDB's
// ordinary catalog transaction. SQL never becomes a second schema authority.
type kitSQLCreateTable struct {
	ifNotExists bool
	columns     []kitSQLDDLColumn
	checks      []kitSQLDDLCheck
}

type kitSQLCreateIndex struct {
	ifNotExists bool
	unique      bool
	name        string
	columns     []string
}

type kitSQLCreateDatabase struct {
	name       string
	capability string
	source     string
	timestamp  time.Time
}

type kitSQLDropTable struct {
	ifExists bool
	cascade  bool
}

type kitSQLDropDatabase struct {
	ifExists bool
	name     string
}

type kitSQLRenameDatabase struct {
	name    string
	newName string
}

type kitSQLDropIndex struct {
	ifExists bool
	cascade  bool
	name     string
}

type kitSQLRenameTable struct {
	newName string
}

type kitSQLRenameIndex struct {
	ifExists bool
	name     string
	newName  string
}

type kitSQLDDLUnique struct {
	name    string
	columns []string
}

type kitSQLAlterColumn struct {
	action      string
	ifNotExists bool
	ifExists    bool
	column      kitSQLDDLColumn
	name        string
	newName     string
	kind        string
	choices     []string
}

type kitSQLAlterConstraint struct {
	action    string
	ifExists  bool
	cascade   bool
	name      string
	kind      string
	columns   []string
	reference *kitSQLDDLReference
	check     *kitSQLDDLCheck
}

type kitSQLAlterPrimaryKey struct {
	columns []string
}

const (
	kitDBDDLConstraintPrimary      = "primary"
	kitDBDDLConstraintFieldUnique  = "field_unique"
	kitDBDDLConstraintFieldForeign = "field_foreign"
	kitDBDDLConstraintTupleUnique  = "tuple_unique"
	kitDBDDLConstraintTupleForeign = "tuple_foreign"
	kitDBDDLConstraintCheck        = "check"
)

type kitDBDDLConstraint struct {
	kind    string
	name    string
	fieldID string
	id      string
}

type kitSQLDDLColumn struct {
	name   string
	spec   *ColumnSpec
	checks []kitSQLDDLCheck
}

type kitSQLDDLCheck struct {
	name       string
	column     string
	expression *kitSQLExpression
}

type kitSQLDDLReference struct {
	name     string
	columns  []string
	table    string
	fields   []string
	onDelete string
	onUpdate string
}

func (parser *kitSQLParser) parseCreate() (kitSQLStatement, error) {
	unique := parser.acceptKeyword("unique")
	if parser.acceptKeyword("database") {
		if unique {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: UNIQUE cannot modify CREATE DATABASE")
		}
		return parser.parseCreateDatabase()
	}
	if parser.acceptKeyword("table") {
		if unique {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: UNIQUE cannot modify CREATE TABLE")
		}
		return parser.parseCreateTable()
	}
	if parser.acceptKeyword("index") {
		return parser.parseCreateIndex(unique)
	}
	return kitSQLStatement{}, fmt.Errorf("kitdb SQL: only CREATE DATABASE, CREATE TABLE and CREATE INDEX are supported")
}

func (parser *kitSQLParser) parseCreateDatabase() (kitSQLStatement, error) {
	name, err := parser.databaseIdentifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	plan := &kitSQLCreateDatabase{name: name}
	switch {
	case parser.acceptKeyword("from"):
		plan.source, err = parser.databaseIdentifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("as"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("of"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("timestamp"); err != nil {
			return kitSQLStatement{}, err
		}
		timestampToken := parser.take()
		if timestampToken.kind != kitSQLTokenString || strings.TrimSpace(timestampToken.text) == "" {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: AS OF TIMESTAMP requires a quoted RFC3339 timestamp")
		}
		timestamp, parseErr := time.Parse(time.RFC3339Nano, timestampToken.text)
		if parseErr != nil {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: invalid AS OF TIMESTAMP %q: %w", timestampToken.text, parseErr)
		}
		plan.timestamp = timestamp.UTC()
	case parser.acceptKeyword("with"):
		if err := parser.expectKeyword("capability"); err != nil {
			return kitSQLStatement{}, err
		}
		plan.capability, err = parser.databaseIdentifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
	}
	return kitSQLStatement{kind: "create_database", createDatabase: plan}, nil
}

func (parser *kitSQLParser) databaseIdentifier() (string, error) {
	token := parser.take()
	if token.kind != kitSQLTokenIdentifier || token.text == "" {
		return "", fmt.Errorf("kitdb SQL: expected database identifier, got %q", token.text)
	}
	if parser.peek().kind == kitSQLTokenSymbol && parser.peek().text == "." {
		return "", fmt.Errorf("kitdb SQL: database names are logical names without a .kitdb suffix")
	}
	return token.text, nil
}

func (parser *kitSQLParser) parseDrop() (kitSQLStatement, error) {
	if parser.acceptKeyword("database") {
		ifExists := false
		if parser.acceptKeyword("if") {
			if err := parser.expectKeyword("exists"); err != nil {
				return kitSQLStatement{}, err
			}
			ifExists = true
		}
		name, err := parser.databaseIdentifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		return kitSQLStatement{
			kind:         "drop_database",
			dropDatabase: &kitSQLDropDatabase{ifExists: ifExists, name: name},
		}, nil
	}
	kind := ""
	switch {
	case parser.acceptKeyword("table"):
		kind = "drop_table"
	case parser.acceptKeyword("index"):
		kind = "drop_index"
	default:
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: only DROP DATABASE, DROP TABLE and DROP INDEX are supported")
	}
	if kind == "drop_index" && parser.acceptKeyword("concurrently") {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: DROP INDEX CONCURRENTLY is not supported; index retirement is already online and resumable")
	}
	ifExists := false
	if parser.acceptKeyword("if") {
		if err := parser.expectKeyword("exists"); err != nil {
			return kitSQLStatement{}, err
		}
		ifExists = true
	}
	name, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	cascade := parser.acceptKeyword("cascade")
	if !cascade {
		parser.acceptKeyword("restrict")
	}
	if kind == "drop_table" {
		return kitSQLStatement{
			kind: kind, table: name,
			dropTable: &kitSQLDropTable{ifExists: ifExists, cascade: cascade},
		}, nil
	}
	return kitSQLStatement{
		kind:      kind,
		dropIndex: &kitSQLDropIndex{ifExists: ifExists, cascade: cascade, name: name},
	}, nil
}

func (parser *kitSQLParser) parseCreateTable() (kitSQLStatement, error) {
	ifNotExists := false
	if parser.acceptKeyword("if") {
		if err := parser.expectKeyword("not"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("exists"); err != nil {
			return kitSQLStatement{}, err
		}
		ifNotExists = true
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectSymbol("("); err != nil {
		return kitSQLStatement{}, err
	}
	if parser.acceptSymbol(")") {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE TABLE needs at least one column")
	}

	definition := &kitSQLCreateTable{ifNotExists: ifNotExists}
	var primaryColumns []string
	var uniqueConstraints []kitSQLDDLUnique
	var references []kitSQLDDLReference
	for {
		if len(definition.columns) > kitSQLMaximumDDLColumns {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE TABLE exceeds %d columns", kitSQLMaximumDDLColumns)
		}
		switch {
		case parser.acceptKeyword("primary"):
			if err := parser.expectKeyword("key"); err != nil {
				return kitSQLStatement{}, err
			}
			if len(primaryColumns) != 0 {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE TABLE repeats PRIMARY KEY")
			}
			columns, err := parser.ddlColumnList("PRIMARY KEY")
			if err != nil {
				return kitSQLStatement{}, err
			}
			primaryColumns = columns
		case parser.acceptKeyword("unique"):
			columns, err := parser.ddlColumnList("UNIQUE")
			if err != nil {
				return kitSQLStatement{}, err
			}
			uniqueConstraints = append(uniqueConstraints, kitSQLDDLUnique{columns: columns})
		case parser.acceptKeyword("foreign"):
			if err := parser.expectKeyword("key"); err != nil {
				return kitSQLStatement{}, err
			}
			columns, err := parser.ddlColumnList("FOREIGN KEY")
			if err != nil {
				return kitSQLStatement{}, err
			}
			reference, err := parser.ddlReference(columns)
			if err != nil {
				return kitSQLStatement{}, err
			}
			references = append(references, reference)
		case parser.acceptKeyword("check"):
			check, err := parser.ddlCheck("")
			if err != nil {
				return kitSQLStatement{}, err
			}
			definition.checks = append(definition.checks, check)
		case parser.acceptKeyword("constraint"):
			name, err := parser.identifier()
			if err != nil {
				return kitSQLStatement{}, err
			}
			switch {
			case parser.acceptKeyword("unique"):
				columns, err := parser.ddlColumnList("UNIQUE")
				if err != nil {
					return kitSQLStatement{}, err
				}
				uniqueConstraints = append(uniqueConstraints, kitSQLDDLUnique{name: name, columns: columns})
			case parser.acceptKeyword("foreign"):
				if err := parser.expectKeyword("key"); err != nil {
					return kitSQLStatement{}, err
				}
				columns, err := parser.ddlColumnList("FOREIGN KEY")
				if err != nil {
					return kitSQLStatement{}, err
				}
				reference, err := parser.ddlReference(columns)
				if err != nil {
					return kitSQLStatement{}, err
				}
				reference.name = name
				references = append(references, reference)
			case parser.acceptKeyword("check"):
				check, err := parser.ddlCheck(name)
				if err != nil {
					return kitSQLStatement{}, err
				}
				definition.checks = append(definition.checks, check)
			default:
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: named constraint %q expects UNIQUE, FOREIGN KEY, or CHECK", name)
			}
		default:
			if len(definition.columns) >= kitSQLMaximumDDLColumns {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE TABLE exceeds %d columns", kitSQLMaximumDDLColumns)
			}
			column, reference, err := parser.ddlColumn(len(definition.columns))
			if err != nil {
				return kitSQLStatement{}, err
			}
			for _, existing := range definition.columns {
				if strings.EqualFold(existing.name, column.name) {
					return kitSQLStatement{}, fmt.Errorf("kitdb SQL: duplicate column %q", column.name)
				}
			}
			definition.columns = append(definition.columns, column)
			definition.checks = append(definition.checks, column.checks...)
			if reference != nil {
				references = append(references, *reference)
			}
		}

		if parser.acceptSymbol(")") {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return kitSQLStatement{}, err
		}
		if parser.peek().kind == kitSQLTokenEOF || parser.acceptSymbol(")") {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: trailing comma in CREATE TABLE")
		}
	}
	if len(definition.columns) == 0 {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE TABLE needs at least one column")
	}
	if len(definition.checks) > kitDBMaximumCheckConstraints {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE TABLE exceeds %d CHECK constraints", kitDBMaximumCheckConstraints)
	}

	if parser.acceptKeyword("without") {
		if err := parser.expectKeyword("rowid"); err != nil {
			return kitSQLStatement{}, err
		}
	}
	_ = parser.acceptKeyword("strict")

	for position, requested := range primaryColumns {
		column, err := ddlFindColumn(definition.columns, requested)
		if err != nil {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: PRIMARY KEY: %w", err)
		}
		if column.spec.primary {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: column %q repeats PRIMARY KEY", column.name)
		}
		column.spec.primary = true
		column.spec.primaryOrder = position + 1
	}
	for _, unique := range uniqueConstraints {
		members := make([]*kitSQLDDLColumn, len(unique.columns))
		canonical := make([]string, len(unique.columns))
		for index, requested := range unique.columns {
			column, err := ddlFindColumn(definition.columns, requested)
			if err != nil {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: UNIQUE: %w", err)
			}
			members[index], canonical[index] = column, column.name
		}
		if len(members) == 1 {
			if members[0].spec.unique {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: column %q repeats UNIQUE", members[0].name)
			}
			members[0].spec.unique = true
			continue
		}
		name := unique.name
		if name == "" {
			name = "unique_" + table + "_" + strings.Join(canonical, "_")
		}
		for position, column := range members {
			column.spec.uniques = append(column.spec.uniques, colUniqueRef{name: name, pos: position + 1})
		}
	}
	for _, reference := range references {
		if len(reference.columns) != len(reference.fields) {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: FOREIGN KEY has %d local columns but REFERENCES has %d", len(reference.columns), len(reference.fields))
		}
		canonical := make([]string, len(reference.columns))
		for position, requested := range reference.columns {
			column, err := ddlFindColumn(definition.columns, requested)
			if err != nil {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: FOREIGN KEY: %w", err)
			}
			if column.spec.fk != nil {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: column %q repeats REFERENCES", column.name)
			}
			canonical[position] = column.name
		}
		name := reference.name
		if len(canonical) > 1 && name == "" {
			name = "foreign_" + table + "_" + strings.Join(canonical, "_")
		}
		for position, requested := range reference.columns {
			column, _ := ddlFindColumn(definition.columns, requested)
			column.spec.fk = &fkRef{
				name: name, pos: position + 1,
				table: reference.table, column: reference.fields[position],
				onDelete: reference.onDelete, onUpdate: reference.onUpdate,
			}
		}
	}
	return kitSQLStatement{kind: "create_table", table: table, createTable: definition}, nil
}

func (parser *kitSQLParser) parseCreateIndex(unique bool) (kitSQLStatement, error) {
	ifNotExists := false
	if parser.acceptKeyword("if") {
		if err := parser.expectKeyword("not"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("exists"); err != nil {
			return kitSQLStatement{}, err
		}
		ifNotExists = true
	}
	name, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectKeyword("on"); err != nil {
		return kitSQLStatement{}, err
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	if err := parser.expectSymbol("("); err != nil {
		return kitSQLStatement{}, err
	}
	columns := make([]string, 0, 4)
	for {
		column, err := parser.ddlIdentifier("index column")
		if err != nil {
			return kitSQLStatement{}, err
		}
		for _, existing := range columns {
			if strings.EqualFold(existing, column) {
				return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE INDEX repeats column %q", column)
			}
		}
		columns = append(columns, column)
		_ = parser.acceptKeyword("asc")
		if parser.acceptKeyword("desc") {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: descending index members are not supported yet")
		}
		if parser.acceptSymbol(")") {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return kitSQLStatement{}, err
		}
	}
	if len(columns) == 0 {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: CREATE INDEX needs at least one column")
	}
	if unique && len(columns) < 2 {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: single-column CREATE UNIQUE INDEX is not supported; declare the column UNIQUE")
	}
	if parser.acceptKeyword("where") {
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: partial CREATE INDEX is not supported yet")
	}
	return kitSQLStatement{
		kind: "create_index", table: table,
		createIndex: &kitSQLCreateIndex{
			ifNotExists: ifNotExists, unique: unique, name: name, columns: columns,
		},
	}, nil
}

func (parser *kitSQLParser) parseAlter() (kitSQLStatement, error) {
	if parser.acceptKeyword("database") {
		name, err := parser.databaseIdentifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("rename"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("to"); err != nil {
			return kitSQLStatement{}, err
		}
		newName, err := parser.databaseIdentifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		return kitSQLStatement{
			kind:           "alter_rename_database",
			renameDatabase: &kitSQLRenameDatabase{name: name, newName: newName},
		}, nil
	}
	if parser.acceptKeyword("index") {
		ifExists := false
		if parser.acceptKeyword("if") {
			if err := parser.expectKeyword("exists"); err != nil {
				return kitSQLStatement{}, err
			}
			ifExists = true
		}
		name, err := parser.identifier()
		if err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("rename"); err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("to"); err != nil {
			return kitSQLStatement{}, err
		}
		newName, err := parser.ddlIdentifier("new index")
		if err != nil {
			return kitSQLStatement{}, err
		}
		return kitSQLStatement{
			kind:        "alter_rename_index",
			renameIndex: &kitSQLRenameIndex{ifExists: ifExists, name: name, newName: newName},
		}, nil
	}
	if err := parser.expectKeyword("table"); err != nil {
		return kitSQLStatement{}, err
	}
	table, err := parser.identifier()
	if err != nil {
		return kitSQLStatement{}, err
	}
	switch {
	case parser.acceptKeyword("add"):
		if parser.acceptKeyword("constraint") {
			name, err := parser.ddlIdentifier("constraint")
			if err != nil {
				return kitSQLStatement{}, err
			}
			plan := &kitSQLAlterConstraint{action: "add", name: name}
			switch {
			case parser.acceptKeyword("unique"):
				columns, err := parser.ddlColumnList("UNIQUE")
				if err != nil {
					return kitSQLStatement{}, err
				}
				plan.kind, plan.columns = "unique", columns
			case parser.acceptKeyword("foreign"):
				if err := parser.expectKeyword("key"); err != nil {
					return kitSQLStatement{}, err
				}
				columns, err := parser.ddlColumnList("FOREIGN KEY")
				if err != nil {
					return kitSQLStatement{}, err
				}
				reference, err := parser.ddlReference(columns)
				if err != nil {
					return kitSQLStatement{}, err
				}
				reference.name = name
				plan.kind, plan.columns, plan.reference = "foreign", columns, &reference
			case parser.acceptKeyword("check"):
				check, err := parser.ddlCheck(name)
				if err != nil {
					return kitSQLStatement{}, err
				}
				plan.kind, plan.check = "check", &check
			case parser.acceptKeyword("primary"):
				return kitSQLStatement{}, fmt.Errorf(
					"kitdb SQL: adding primary key constraint %q is not supported; declare the primary key when creating the table",
					name,
				)
			default:
				return kitSQLStatement{}, fmt.Errorf(
					"kitdb SQL: ADD CONSTRAINT %q expects UNIQUE, FOREIGN KEY, or CHECK", name,
				)
			}
			return kitSQLStatement{
				kind: "alter_add_constraint", table: table, alterConstraint: plan,
			}, nil
		}
		_ = parser.acceptKeyword("column")
		ifNotExists := false
		if parser.acceptKeyword("if") {
			if err := parser.expectKeyword("not"); err != nil {
				return kitSQLStatement{}, err
			}
			if err := parser.expectKeyword("exists"); err != nil {
				return kitSQLStatement{}, err
			}
			ifNotExists = true
		}
		column, reference, err := parser.ddlColumn(0)
		if err != nil {
			return kitSQLStatement{}, err
		}
		if reference != nil {
			column.spec.fk = &fkRef{
				table: reference.table, column: reference.fields[0],
				onDelete: reference.onDelete, onUpdate: reference.onUpdate,
			}
		}
		return kitSQLStatement{
			kind: "alter_add_column", table: table,
			alterColumn: &kitSQLAlterColumn{
				action: "add", ifNotExists: ifNotExists, column: column,
			},
		}, nil
	case parser.acceptKeyword("rename"):
		if parser.acceptKeyword("to") {
			newName, err := parser.ddlIdentifier("new table")
			if err != nil {
				return kitSQLStatement{}, err
			}
			return kitSQLStatement{
				kind: "alter_rename_table", table: table,
				renameTable: &kitSQLRenameTable{newName: newName},
			}, nil
		}
		if !parser.acceptKeyword("column") {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: ALTER TABLE RENAME expects TO or COLUMN")
		}
		name, err := parser.ddlIdentifier("column")
		if err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("to"); err != nil {
			return kitSQLStatement{}, err
		}
		newName, err := parser.ddlIdentifier("new column")
		if err != nil {
			return kitSQLStatement{}, err
		}
		return kitSQLStatement{
			kind: "alter_rename_column", table: table,
			alterColumn: &kitSQLAlterColumn{action: "rename", name: name, newName: newName},
		}, nil
	case parser.acceptKeyword("drop"):
		if parser.acceptKeyword("constraint") {
			ifExists := false
			if parser.acceptKeyword("if") {
				if err := parser.expectKeyword("exists"); err != nil {
					return kitSQLStatement{}, err
				}
				ifExists = true
			}
			name, err := parser.ddlIdentifier("constraint")
			if err != nil {
				return kitSQLStatement{}, err
			}
			cascade := parser.acceptKeyword("cascade")
			if !cascade {
				parser.acceptKeyword("restrict")
			}
			return kitSQLStatement{
				kind: "alter_drop_constraint", table: table,
				alterConstraint: &kitSQLAlterConstraint{
					action: "drop", ifExists: ifExists, cascade: cascade, name: name,
				},
			}, nil
		}
		_ = parser.acceptKeyword("column")
		ifExists := false
		if parser.acceptKeyword("if") {
			if err := parser.expectKeyword("exists"); err != nil {
				return kitSQLStatement{}, err
			}
			ifExists = true
		}
		name, err := parser.ddlIdentifier("column")
		if err != nil {
			return kitSQLStatement{}, err
		}
		return kitSQLStatement{
			kind: "alter_drop_column", table: table,
			alterColumn: &kitSQLAlterColumn{action: "drop", ifExists: ifExists, name: name},
		}, nil
	case parser.acceptKeyword("alter"):
		if parser.acceptKeyword("primary") {
			if err := parser.expectKeyword("key"); err != nil {
				return kitSQLStatement{}, err
			}
			columns, err := parser.ddlColumnList("PRIMARY KEY")
			if err != nil {
				return kitSQLStatement{}, err
			}
			return kitSQLStatement{
				kind: "alter_primary_key", table: table,
				alterPrimaryKey: &kitSQLAlterPrimaryKey{columns: columns},
			}, nil
		}
		_ = parser.acceptKeyword("column")
		name, err := parser.ddlIdentifier("column")
		if err != nil {
			return kitSQLStatement{}, err
		}
		if err := parser.expectKeyword("type"); err != nil {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: ALTER COLUMN %q expects TYPE: %w", name, err)
		}
		kind, choices, err := parser.ddlType()
		if err != nil {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: ALTER COLUMN %q: %w", name, err)
		}
		return kitSQLStatement{
			kind: "alter_column_type", table: table,
			alterColumn: &kitSQLAlterColumn{
				action: "type", name: name, kind: kind, choices: choices,
			},
		}, nil
	case parser.acceptKeyword("cancel"):
		if err := parser.expectKeyword("migration"); err != nil {
			return kitSQLStatement{}, fmt.Errorf("kitdb SQL: ALTER TABLE CANCEL expects MIGRATION: %w", err)
		}
		return kitSQLStatement{kind: "alter_cancel_migration", table: table}, nil
	default:
		return kitSQLStatement{}, fmt.Errorf("kitdb SQL: ALTER TABLE expects ADD COLUMN/CONSTRAINT, RENAME COLUMN, DROP COLUMN/CONSTRAINT, ALTER COLUMN TYPE, ALTER PRIMARY KEY, or CANCEL MIGRATION")
	}
}

func (parser *kitSQLParser) ddlColumn(position int) (kitSQLDDLColumn, *kitSQLDDLReference, error) {
	name, err := parser.ddlIdentifier("column")
	if err != nil {
		return kitSQLDDLColumn{}, nil, err
	}
	kind, choices, err := parser.ddlType()
	if err != nil {
		return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: column %q: %w", name, err)
	}
	spec := &ColumnSpec{kind: kind, enumVals: choices, seq: uint64(position + 1)}
	var reference *kitSQLDDLReference
	checks := make([]kitSQLDDLCheck, 0)
	seenDefault := false
	for {
		if parser.peek().kind == kitSQLTokenEOF ||
			(parser.peek().kind == kitSQLTokenSymbol &&
				(parser.peek().text == "," || parser.peek().text == ")" || parser.peek().text == ";")) {
			break
		}
		switch {
		case parser.acceptKeyword("primary"):
			if err := parser.expectKeyword("key"); err != nil {
				return kitSQLDDLColumn{}, nil, err
			}
			if spec.primary {
				return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: column %q repeats PRIMARY KEY", name)
			}
			spec.primary = true
		case parser.acceptKeyword("not"):
			if err := parser.expectKeyword("null"); err != nil {
				return kitSQLDDLColumn{}, nil, err
			}
			spec.notNull = true
		case parser.acceptKeyword("null"):
			// Nullable is the default; accepting NULL improves SQLite tooling compatibility.
		case parser.acceptKeyword("unique"):
			spec.unique = true
		case parser.acceptKeyword("default"):
			if seenDefault {
				return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: column %q repeats DEFAULT", name)
			}
			seenDefault = true
			if parser.acceptKeyword("current_timestamp") {
				if spec.kind != "datetime" {
					return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: CURRENT_TIMESTAMP requires a DATETIME or TIMESTAMP column")
				}
				spec.defaultNow = true
				break
			}
			if parser.peek().kind == kitSQLTokenPlaceholder {
				return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: DEFAULT cannot use a bound parameter")
			}
			wrapped := parser.acceptSymbol("(")
			item, err := parser.operand()
			if err != nil {
				return kitSQLDDLColumn{}, nil, err
			}
			if wrapped {
				if err := parser.expectSymbol(")"); err != nil {
					return kitSQLDDLColumn{}, nil, err
				}
			}
			spec.hasDefault, spec.def = true, item
		case parser.acceptKeyword("references"):
			if reference != nil {
				return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: column %q repeats REFERENCES", name)
			}
			parsed, err := parser.ddlReferenceAfterKeyword([]string{name})
			if err != nil {
				return kitSQLDDLColumn{}, nil, err
			}
			reference = &parsed
		case parser.acceptKeyword("autoincrement"):
			return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: AUTOINCREMENT is not supported; supply the integer key or use KITID PRIMARY KEY")
		case parser.acceptKeyword("check"):
			check, err := parser.ddlCheck("")
			if err != nil {
				return kitSQLDDLColumn{}, nil, err
			}
			check.column = name
			checks = append(checks, check)
		case parser.acceptKeyword("collate"):
			return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: COLLATE is not supported yet")
		default:
			return kitSQLDDLColumn{}, nil, fmt.Errorf("kitdb SQL: unsupported column clause %q", parser.peek().text)
		}
	}
	return kitSQLDDLColumn{name: name, spec: spec, checks: checks}, reference, nil
}

func (parser *kitSQLParser) ddlCheck(name string) (kitSQLDDLCheck, error) {
	if err := parser.expectSymbol("("); err != nil {
		return kitSQLDDLCheck{}, err
	}
	start := parser.tokenPosition()
	expression, err := parser.expression()
	if err != nil {
		return kitSQLDDLCheck{}, err
	}
	end := parser.tokenPosition()
	if err := parser.expectSymbol(")"); err != nil {
		return kitSQLDDLCheck{}, err
	}
	for _, token := range parser.tokenRange(start, end) {
		if token.kind == kitSQLTokenPlaceholder {
			return kitSQLDDLCheck{}, fmt.Errorf("kitdb SQL: CHECK cannot use a bound parameter")
		}
	}
	if kitSQLExpressionContainsAggregate(expression) {
		return kitSQLDDLCheck{}, fmt.Errorf("kitdb SQL: aggregate expressions are not valid in CHECK")
	}
	return kitSQLDDLCheck{name: name, expression: expression}, nil
}

func (parser *kitSQLParser) ddlType() (string, []string, error) {
	token := parser.take()
	if token.kind != kitSQLTokenIdentifier {
		return "", nil, fmt.Errorf("expected a column type, got %q", token.text)
	}
	typeName := strings.ToLower(token.text)
	if typeName == "double" && parser.acceptKeyword("precision") {
		typeName = "double precision"
	} else if typeName == "character" && parser.acceptKeyword("varying") {
		typeName = "character varying"
	}

	var choices []string
	hadArguments := parser.acceptSymbol("(")
	if hadArguments {
		if typeName == "choice" || typeName == "enum" {
			for {
				item := parser.take()
				if item.kind != kitSQLTokenString || item.text == "" {
					return "", nil, fmt.Errorf("%s values must be non-empty strings", strings.ToUpper(typeName))
				}
				for _, existing := range choices {
					if existing == item.text {
						return "", nil, fmt.Errorf("%s repeats value %q", strings.ToUpper(typeName), item.text)
					}
				}
				choices = append(choices, item.text)
				if parser.acceptSymbol(")") {
					break
				}
				if err := parser.expectSymbol(","); err != nil {
					return "", nil, err
				}
			}
		} else {
			arguments := 0
			for {
				number := parser.take()
				if number.kind != kitSQLTokenNumber {
					return "", nil, fmt.Errorf("type parameters for %s must be numeric", strings.ToUpper(typeName))
				}
				arguments++
				if parser.acceptSymbol(")") {
					break
				}
				if err := parser.expectSymbol(","); err != nil {
					return "", nil, err
				}
			}
			if arguments > 2 {
				return "", nil, fmt.Errorf("type %s has too many parameters", strings.ToUpper(typeName))
			}
		}
	}

	typeInfo, found := kitdbsql.ResolveName(typeName)
	if !found {
		return "", nil, fmt.Errorf("unsupported column type %q", token.text)
	}
	// This legacy Kitwork parser authors Schema IR v2. Preserve its historical
	// TIMESTAMP-as-datetime contract instead of emitting an exact v5 kind without
	// precision metadata. Standalone KitDB SQL owns the exact temporal profile.
	if typeInfo.ID == kitdbsql.TypeTimestamp || typeInfo.ID == kitdbsql.TypeTimestampTZ {
		typeInfo, _ = kitdbsql.LookupKind("datetime")
	}
	if typeInfo.ID == kitdbsql.TypeChoice {
		if !hadArguments || len(choices) == 0 {
			return "", nil, fmt.Errorf("%s needs at least one string value", strings.ToUpper(typeName))
		}
	}
	return typeInfo.Kind, choices, nil
}

func (parser *kitSQLParser) ddlColumnList(label string) ([]string, error) {
	if err := parser.expectSymbol("("); err != nil {
		return nil, err
	}
	columns := make([]string, 0, 2)
	for {
		column, err := parser.ddlIdentifier(label + " column")
		if err != nil {
			return nil, err
		}
		columns = append(columns, column)
		if parser.acceptSymbol(")") {
			break
		}
		if err := parser.expectSymbol(","); err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func (parser *kitSQLParser) ddlReference(columns []string) (kitSQLDDLReference, error) {
	if err := parser.expectKeyword("references"); err != nil {
		return kitSQLDDLReference{}, err
	}
	return parser.ddlReferenceAfterKeyword(columns)
}

func (parser *kitSQLParser) ddlReferenceAfterKeyword(columns []string) (kitSQLDDLReference, error) {
	table, err := parser.identifier()
	if err != nil {
		return kitSQLDDLReference{}, err
	}
	fields, err := parser.ddlColumnList("REFERENCES")
	if err != nil {
		return kitSQLDDLReference{}, err
	}
	if len(fields) != len(columns) {
		return kitSQLDDLReference{}, fmt.Errorf("kitdb SQL: FOREIGN KEY has %d local columns but REFERENCES has %d", len(columns), len(fields))
	}
	reference := kitSQLDDLReference{columns: append([]string(nil), columns...), table: table, fields: fields}
	for parser.acceptKeyword("on") {
		switch {
		case parser.acceptKeyword("delete"):
			if reference.onDelete != "" {
				return kitSQLDDLReference{}, fmt.Errorf("kitdb SQL: REFERENCES repeats ON DELETE")
			}
			reference.onDelete, err = parser.ddlReferenceAction()
		case parser.acceptKeyword("update"):
			if reference.onUpdate != "" {
				return kitSQLDDLReference{}, fmt.Errorf("kitdb SQL: REFERENCES repeats ON UPDATE")
			}
			reference.onUpdate, err = parser.ddlReferenceAction()
		default:
			return kitSQLDDLReference{}, fmt.Errorf("kitdb SQL: REFERENCES expects ON DELETE or ON UPDATE")
		}
		if err != nil {
			return kitSQLDDLReference{}, err
		}
	}
	return reference, nil
}

func (parser *kitSQLParser) ddlReferenceAction() (string, error) {
	switch {
	case parser.acceptKeyword("restrict"):
		return "restrict", nil
	case parser.acceptKeyword("cascade"):
		return "cascade", nil
	case parser.acceptKeyword("no"):
		if err := parser.expectKeyword("action"); err != nil {
			return "", err
		}
		return "noAction", nil
	case parser.acceptKeyword("set"):
		switch {
		case parser.acceptKeyword("null"):
			return "setNull", nil
		case parser.acceptKeyword("default"):
			return "setDefault", nil
		default:
			return "", fmt.Errorf("kitdb SQL: REFERENCES SET expects NULL or DEFAULT")
		}
	default:
		return "", fmt.Errorf("kitdb SQL: unsupported reference action %q", parser.peek().text)
	}
}

func (parser *kitSQLParser) ddlIdentifier(label string) (string, error) {
	token := parser.take()
	if token.kind != kitSQLTokenIdentifier || strings.TrimSpace(token.text) == "" {
		return "", fmt.Errorf("kitdb SQL: expected %s identifier, got %q", label, token.text)
	}
	return token.text, nil
}

func ddlFindColumn(columns []kitSQLDDLColumn, requested string) (*kitSQLDDLColumn, error) {
	var match *kitSQLDDLColumn
	for index := range columns {
		if columns[index].name == requested {
			return &columns[index], nil
		}
		if strings.EqualFold(columns[index].name, requested) {
			if match != nil {
				return nil, fmt.Errorf("ambiguous column %q", requested)
			}
			match = &columns[index]
		}
	}
	if match == nil {
		return nil, fmt.Errorf("no such column %q", requested)
	}
	return match, nil
}

func executeKitDBRemoteCreateTable(
	ctx context.Context,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	if statement.createTable == nil || len(statement.createTable.columns) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid CREATE TABLE plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	tables, definitions := database.schemaSnapshot()
	if existingName, existing := ddlFindStruct(definitions, statement.table); existing != nil {
		if statement.createTable.ifNotExists {
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q already exists as %q", statement.table, existingName)
	}
	columns := make(map[string]*ColumnSpec, len(statement.createTable.columns))
	for _, column := range statement.createTable.columns {
		columns[column.name] = column.spec
	}
	tables[statement.table] = columns
	if err := canonicalizeKitSQLReferences(columns, tables); err != nil {
		return kitDBRemoteResult{}, err
	}
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	definition := bindStructDef(statement.table, nil, columns)
	if err := appendKitSQLCheckConstraints(definition, statement.createTable.checks); err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[definition.Name] = definition
	if err := validateKitDBStruct(definition, definitions); err != nil {
		return kitDBRemoteResult{}, err
	}
	for name, existing := range definitions {
		if strings.EqualFold(name, definition.Name) {
			continue
		}
		if err := ddlValidateDefinitionIndexNames(definition, existing); err != nil {
			return kitDBRemoteResult{}, err
		}
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, database.scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	usedStructIDs := make(map[string]string, len(catalog.Structs))
	for _, entry := range catalog.Structs {
		usedStructIDs[entry.ID] = entry.Name
	}
	if owner := usedStructIDs[definition.ID]; owner != "" && !strings.EqualFold(owner, definition.Name) {
		definition = bindStructDefWithID(
			statement.table, ddlAvailableStructIdentity(statement.table, usedStructIDs), nil, columns,
		)
		if err := appendKitSQLCheckConstraints(definition, statement.createTable.checks); err != nil {
			return kitDBRemoteResult{}, err
		}
		definitions[definition.Name] = definition
		if err := validateKitDBStruct(definition, definitions); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	for _, entry := range catalog.Structs {
		if !strings.EqualFold(entry.Name, statement.table) {
			stored, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
			if decodeErr != nil {
				return kitDBRemoteResult{}, decodeErr
			}
			if err := ddlValidateDefinitionIndexNames(definition, stored); err != nil {
				return kitDBRemoteResult{}, err
			}
			continue
		}
		if statement.createTable.ifNotExists {
			stored, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
			if decodeErr != nil {
				return kitDBRemoteResult{}, decodeErr
			}
			database.publishKitDBDefinition(managed, stored)
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q already exists as %q", statement.table, entry.Name)
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := commitKitDBCatalog(managed.database, definition, nil, ""); err != nil {
		return kitDBRemoteResult{}, err
	}
	if ready, err := ensureKitDBSecondaryIndexCodec(managed.database, definition); err != nil {
		return kitDBRemoteResult{}, err
	} else if !ready {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: fresh struct %q did not publish its physical index contract",
			definition.Name,
		)
	}
	database.publishKitDBDefinition(managed, definition)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteDropTable(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.dropTable
	if plan == nil || strings.TrimSpace(statement.table) == "" {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid DROP TABLE plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if database.kitDBStructIsSourceDeclared(statement.table) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: DROP TABLE for source-declared struct %q is not supported; remove it from struct() deliberately",
			statement.table,
		)
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	_, definitions := database.schemaSnapshot()
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		if strings.EqualFold(entry.Name, statement.table) {
			if stored != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous table %q", statement.table)
			}
			stored = definition
		}
	}
	if stored == nil {
		if plan.ifExists {
			database.unpublishKitDBDefinition(managed, statement.table, "")
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such table: %s", statement.table)
	}
	if database.kitDBStructIsSourceDeclared(stored.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: DROP TABLE for source-declared struct %q is not supported; remove it from struct() deliberately",
			stored.Name,
		)
	}
	if dependent, detail := kitDBDropTableDependent(definitions, stored); dependent != nil {
		if plan.cascade {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: cannot drop table %q because table %q depends on it through %s; cascading dependent constraints is not supported yet",
				stored.Name, dependent.Name, detail,
			)
		}
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: cannot drop table %q because table %q depends on it through %s",
			stored.Name, dependent.Name, detail,
		)
	}

	keys, err := collectKitDBDropTableKeys(ctx, managed.database, stored)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	active, found, err := managed.database.CatalogStructByID(stored.ID)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if !found || active.Hash != stored.Hash {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: table %q changed while DROP TABLE was being prepared; retry the statement",
			stored.Name,
		)
	}

	transaction, err := managed.database.Begin()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	fail := func(err error) (kitDBRemoteResult, error) {
		_ = transaction.Rollback()
		return kitDBRemoteResult{}, err
	}
	for index, key := range keys {
		if index&255 == 0 {
			if err := ctx.Err(); err != nil {
				return fail(err)
			}
		}
		if err := transaction.Delete(key); err != nil {
			return fail(err)
		}
	}
	if err := transaction.DeleteStruct(stored.ID); err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if _, err := transaction.Commit(); err != nil {
		return kitDBRemoteResult{}, err
	}
	database.unpublishKitDBDefinition(managed, stored.Name, stored.ID)
	return kitDBRemoteResult{}, nil
}

func kitDBDropTableDependent(
	definitions map[string]*StructDef,
	target *StructDef,
) (*StructDef, string) {
	if target == nil {
		return nil, ""
	}
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		definition := definitions[name]
		if definition == nil || definition.ID == target.ID {
			continue
		}
		for _, field := range definition.Fields {
			if field.Reference != nil && strings.EqualFold(field.Reference.Struct, target.Name) {
				return definition, fmt.Sprintf("foreign key column %q", field.Name)
			}
		}
		for _, constraint := range definition.ForeignConstraints {
			if constraint.TargetStructID == target.ID ||
				strings.EqualFold(constraint.TargetStruct, target.Name) {
				return definition, fmt.Sprintf("foreign key constraint %q", constraint.Name)
			}
		}
	}
	return nil, ""
}

func collectKitDBDropTableKeys(
	ctx context.Context,
	database *kitdbengine.DB,
	definition *StructDef,
) ([][]byte, error) {
	if database == nil || definition == nil {
		return nil, fmt.Errorf("kitdb SQL: DROP TABLE storage is unavailable")
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()

	keys := make([][]byte, 0, 256)
	bytesUsed := kitSQLDropOperationOverhead + 17
	for _, namespace := range []byte{
		kitDBMigrationNamespace,
		kitDBPhysicalNamespace,
		kitDBStatisticsNamespace,
		kitDBRowNamespace,
		kitDBShadowRowNamespace,
		kitDBUniqueNamespace,
		kitDBIndexNamespace,
	} {
		prefix, err := kitDBFixedKey(namespace, definition.ID, "")
		if err != nil {
			return nil, err
		}
		cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
		if err != nil {
			return nil, err
		}
		for cursor.Next() {
			if len(keys)&255 == 0 {
				if err := ctx.Err(); err != nil {
					_ = cursor.Close()
					return nil, err
				}
			}
			key := cursor.Key()
			if len(keys)+2 > kitSQLDropTableOperationLimit ||
				len(key)+kitSQLDropOperationOverhead > kitSQLDropTableByteLimit-bytesUsed {
				_ = cursor.Close()
				return nil, fmt.Errorf(
					"kitdb SQL: table %q exceeds the bounded DROP TABLE budget (%d operations or %d bytes); online DROP is required",
					definition.Name, kitSQLDropTableOperationLimit, kitSQLDropTableByteLimit,
				)
			}
			keys = append(keys, key)
			bytesUsed += len(key) + kitSQLDropOperationOverhead
		}
		if err := cursor.Err(); err != nil {
			_ = cursor.Close()
			return nil, err
		}
		_ = cursor.Close()
	}
	return keys, nil
}

func executeKitDBRemoteCreateIndex(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.createIndex
	if plan == nil || plan.name == "" || len(plan.columns) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid CREATE INDEX plan")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	for _, definition := range definitions {
		if owner, found := ddlDefinitionIndex(definition, plan.name); found {
			if plan.ifNotExists {
				return kitDBRemoteResult{}, nil
			}
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: index %q already exists on table %q", plan.name, owner)
		}
		pending, building, pendingErr := kitDBPendingIndexDefinition(managed.database, definition)
		if pendingErr != nil {
			return kitDBRemoteResult{}, pendingErr
		}
		if building {
			if owner, found := ddlDefinitionIndex(pending, plan.name); found {
				if plan.ifNotExists {
					return kitDBRemoteResult{}, nil
				}
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: index %q already exists on table %q", plan.name, owner)
			}
		}
	}
	var target *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if strings.EqualFold(entry.Name, table.table) {
			target = definition
		}
		if owner, found := ddlDefinitionIndex(definition, plan.name); found {
			if plan.ifNotExists {
				return kitDBRemoteResult{}, nil
			}
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: index %q already exists on table %q", plan.name, owner)
		}
	}
	if target == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q has no durable catalog definition", table.table)
	}
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()

	columns := cloneKitDBDDLColumns(target.columns)
	resolvedColumns := make([]string, len(plan.columns))
	for position, requested := range plan.columns {
		fieldName, _, fieldErr := kitDBRemoteField(target, requested)
		if fieldErr != nil {
			return kitDBRemoteResult{}, fieldErr
		}
		resolvedColumns[position] = fieldName
		spec := columns[fieldName]
		if plan.unique {
			spec.uniques = append(spec.uniques, colUniqueRef{name: plan.name, pos: position + 1})
		}
	}
	if !plan.unique {
		identity := ddlAvailableIndexIdentity(target, indexDef{name: plan.name, columns: resolvedColumns})
		for position, fieldName := range resolvedColumns {
			columns[fieldName].indexes = append(columns[fieldName].indexes, colIndexRef{
				name: plan.name, id: identity, pos: position + 1,
			})
		}
	}
	current := bindStructDefWithID(target.Name, target.ID, target, columns)
	if err := reconcileKitDBDefinition(target, current); err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	tables[current.Name] = current.columns
	resolver = &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	if err := validateKitDBStruct(current, definitions); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBCatalog(
		managed.database, current, definitions, true,
		func() error { return nil },
	); err != nil {
		return kitDBRemoteResult{}, err
	}
	if pending, err := wakeKitDBSecondaryIndexIfPending(managed); err != nil {
		return kitDBRemoteResult{}, err
	} else if pending {
		database.markKitDBCatalogPending()
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteDropIndex(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.dropIndex
	if plan == nil || strings.TrimSpace(plan.name) == "" {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid DROP INDEX plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	var target *StructDef
	var protected *StructDef
	protectedConstraint := ""
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if _, found := ddlDefinitionSecondaryIndex(definition, plan.name); found {
			if target != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous index %q", plan.name)
			}
			target = definition
		}
		if constraint, found := ddlDefinitionConstraintIndex(definition, plan.name); found {
			if protected != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous index %q", plan.name)
			}
			protected, protectedConstraint = definition, constraint
		}
	}
	if target != nil && protected != nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous index %q", plan.name)
	}
	if protected != nil {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: cannot drop index %q because constraint %q on table %q requires it; use ALTER TABLE %q DROP CONSTRAINT %q",
			plan.name, protectedConstraint, protected.Name,
			protected.Name, protectedConstraint,
		)
	}
	if target == nil {
		if plan.ifExists {
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such index: %s", plan.name)
	}
	if database.kitDBStructIsSourceDeclared(target.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: DROP INDEX for source-declared struct %q is not supported; change its struct() declaration deliberately",
			target.Name,
		)
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	removed := false
	for column, spec := range target.columns {
		retained := make([]colIndexRef, 0, len(spec.indexes))
		for _, member := range spec.indexes {
			name := member.name
			if name == "" {
				name = "idx_" + target.Name + "_" + column
			}
			if strings.EqualFold(name, plan.name) {
				removed = true
				continue
			}
			retained = append(retained, member)
		}
		spec.indexes = retained
	}
	if !removed {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: index %q changed while DROP INDEX was being prepared; retry the statement", plan.name)
	}
	current := bindStructDefWithID(target.Name, target.ID, target, target.columns)
	if err := reconcileKitDBDefinition(target, current); err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	if err := validateKitDBStruct(current, definitions); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBCatalog(
		managed.database, current, definitions, true,
		func() error { return nil },
	); err != nil {
		return kitDBRemoteResult{}, err
	}
	if pending, err := wakeKitDBSecondaryIndexIfPending(managed); err != nil {
		return kitDBRemoteResult{}, err
	} else if pending {
		database.markKitDBCatalogPending()
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

type kitDBDDLRenameChange struct {
	stored  *StructDef
	current *StructDef
	steps   []planStep
}

type kitDBDDLMetadataWrite struct {
	key   []byte
	value []byte
}

func executeKitDBRemoteRenameTable(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.renameTable
	if plan == nil || strings.TrimSpace(statement.table) == "" || strings.TrimSpace(plan.newName) == "" {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE RENAME plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if database.kitDBStructIsSourceDeclared(statement.table) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: table rename for source-declared struct %q is not supported; rename its struct() binding deliberately",
			statement.table,
		)
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	durable := make(map[string]*StructDef, len(catalog.Structs))
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		durable[definition.ID] = definition
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if strings.EqualFold(entry.Name, statement.table) {
			if stored != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous table %q", statement.table)
			}
			stored = definition
		}
	}
	if stored == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such table: %s", statement.table)
	}
	if database.kitDBStructIsSourceDeclared(stored.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: table rename for source-declared struct %q is not supported; rename its struct() binding deliberately",
			stored.Name,
		)
	}
	if existingName, existing := ddlFindStruct(definitions, plan.newName); existing != nil && existing.ID != stored.ID {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: table %q already exists as %q", plan.newName, existingName,
		)
	}
	if stored.Name == plan.newName {
		return kitDBRemoteResult{}, nil
	}

	changes := make(map[string]kitDBDDLRenameChange)
	published := make(map[string]*StructDef)
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		definition := definitions[name]
		if definition == nil {
			continue
		}
		var current *StructDef
		var changed bool
		if definition.ID == stored.ID {
			current, err = ddlRenameTableDefinition(definition, plan.newName)
			changed = err == nil
		} else {
			current, changed, err = ddlRetargetStructDefinition(
				definition, stored.ID, stored.Name, plan.newName,
			)
		}
		if err != nil {
			return kitDBRemoteResult{}, err
		}
		if !changed {
			continue
		}
		if database.kitDBStructIsSourceDeclared(definition.Name) {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: table %q cannot be renamed because source-declared struct %q references it",
				stored.Name, definition.Name,
			)
		}
		if durable[definition.ID] == nil {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: table %q cannot be renamed because dependent struct %q has no durable catalog definition",
				stored.Name, definition.Name,
			)
		}
		if err := ddlEnsureRenameReady(managed.database, definition); err != nil {
			return kitDBRemoteResult{}, err
		}
		steps := []planStep{{
			Action: "reference_target_rename", Column: current.Name,
			From: stored.Name, To: plan.newName, WillApply: true,
		}}
		if definition.ID == stored.ID {
			steps[0] = planStep{
				Action: "table_rename", Column: plan.newName,
				From: stored.Name, To: plan.newName, WillApply: true,
			}
		}
		changes[definition.ID] = kitDBDDLRenameChange{stored: definition, current: current, steps: steps}
		published[current.Name] = current
		if definition.ID == stored.ID {
			delete(definitions, definition.Name)
			delete(tables, definition.Name)
		}
		definitions[current.Name] = current
		tables[current.Name] = current.columns
	}
	if len(changes) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q rename produced no catalog change", stored.Name)
	}
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	if err := ddlValidateDefinitionGraph(definitions); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := commitKitDBDDLRenames(managed.database, changes, nil); err != nil {
		return kitDBRemoteResult{}, err
	}
	database.publishKitDBDefinitions(managed, published)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteRenameIndex(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.renameIndex
	if plan == nil || strings.TrimSpace(plan.name) == "" || strings.TrimSpace(plan.newName) == "" {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER INDEX RENAME plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	var stored *StructDef
	var sourceIndex indexDef
	var protected *StructDef
	protectedConstraint := ""
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if index, found := ddlDefinitionSecondaryIndex(definition, plan.name); found {
			if stored != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous index %q", plan.name)
			}
			stored, sourceIndex = definition, index
		}
		if constraint, found := ddlDefinitionConstraintIndex(definition, plan.name); found {
			if protected != nil {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous index %q", plan.name)
			}
			protected, protectedConstraint = definition, constraint
		}
	}
	if stored != nil && protected != nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ambiguous index %q", plan.name)
	}
	if protected != nil {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: cannot rename index %q because constraint %q on table %q owns it",
			plan.name, protectedConstraint, protected.Name,
		)
	}
	if stored == nil {
		if plan.ifExists {
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such index: %s", plan.name)
	}
	if database.kitDBStructIsSourceDeclared(stored.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: index rename for source-declared struct %q is not supported; change its struct() declaration deliberately",
			stored.Name,
		)
	}
	if sourceIndex.name == plan.newName {
		return kitDBRemoteResult{}, nil
	}
	for _, definition := range definitions {
		if owner, found := ddlDefinitionIndex(definition, plan.newName); found {
			if definition.ID == stored.ID && strings.EqualFold(plan.newName, sourceIndex.name) {
				continue
			}
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: index %q already exists on table %q", plan.newName, owner,
			)
		}
	}
	if err := ddlEnsureRenameReady(managed.database, stored); err != nil {
		return kitDBRemoteResult{}, err
	}
	current, err := ddlRenameSecondaryIndexDefinition(stored, sourceIndex, plan.newName)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	tables[current.Name] = current.columns
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	if err := ddlValidateDefinitionGraph(definitions); err != nil {
		return kitDBRemoteResult{}, err
	}

	metadata, found, err := loadKitDBIndexGenerationMetadata(managed.database, stored)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	var metadataWrites []kitDBDDLMetadataWrite
	if found {
		targetIndex, targetFound := ddlDefinitionSecondaryIndex(current, plan.newName)
		if !targetFound {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: renamed index %q is unavailable", plan.newName)
		}
		oldSignature := kitDBIndexSignature(sourceIndex)
		newSignature := kitDBIndexSignature(targetIndex)
		if generation := metadata.Active[oldSignature]; generation != 0 && oldSignature != newSignature {
			if existing := metadata.Active[newSignature]; existing != 0 && existing != generation {
				return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: index %q generation identity already exists", plan.newName)
			}
			delete(metadata.Active, oldSignature)
			metadata.Active[newSignature] = generation
			encoded, encodeErr := encodeKitDBIndexGenerationMetadata(metadata)
			if encodeErr != nil {
				return kitDBRemoteResult{}, encodeErr
			}
			key, keyErr := kitDBIndexGenerationMetadataKey(stored)
			if keyErr != nil {
				return kitDBRemoteResult{}, keyErr
			}
			metadataWrites = append(metadataWrites, kitDBDDLMetadataWrite{key: key, value: encoded})
		}
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	changes := map[string]kitDBDDLRenameChange{
		stored.ID: {
			stored: stored, current: current,
			steps: []planStep{{
				Action: "index_rename", Column: plan.newName,
				From: sourceIndex.name, To: plan.newName, WillApply: true,
			}},
		},
	}
	if err := commitKitDBDDLRenames(managed.database, changes, metadataWrites); err != nil {
		return kitDBRemoteResult{}, err
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

func cloneKitDBDDLDefinition(source *StructDef) (*StructDef, error) {
	if source == nil {
		return nil, fmt.Errorf("kitdb SQL: schema definition is unavailable")
	}
	encoded, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	var cloned StructDef
	if err := json.Unmarshal(encoded, &cloned); err != nil {
		return nil, err
	}
	hydrateKitDBStructColumns(&cloned)
	refreshStructLookups(&cloned)
	if err := prepareStructCheckConstraints(&cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func ddlRenameTableDefinition(stored *StructDef, newName string) (*StructDef, error) {
	current, err := cloneKitDBDDLDefinition(stored)
	if err != nil {
		return nil, err
	}
	for fieldIndex := range current.Fields {
		field := &current.Fields[fieldIndex]
		for memberIndex := range field.Indexes {
			member := &field.Indexes[memberIndex]
			if member.Name == "" {
				member.Name = "idx_" + stored.Name + "_" + field.Name
			}
		}
	}
	current.Name = newName
	ddlRetargetStructReferences(current, stored.ID, stored.Name, newName)
	hydrateKitDBStructColumns(current)
	refreshStructHash(current)
	if err := prepareStructCheckConstraints(current); err != nil {
		return nil, err
	}
	return current, nil
}

func ddlRetargetStructDefinition(
	stored *StructDef,
	targetID, oldName, newName string,
) (*StructDef, bool, error) {
	if !ddlStructReferencesTarget(stored, targetID, oldName) {
		return stored, false, nil
	}
	current, err := cloneKitDBDDLDefinition(stored)
	if err != nil {
		return nil, false, err
	}
	if !ddlRetargetStructReferences(current, targetID, oldName, newName) {
		return stored, false, nil
	}
	hydrateKitDBStructColumns(current)
	refreshStructHash(current)
	if err := prepareStructCheckConstraints(current); err != nil {
		return nil, false, err
	}
	return current, true, nil
}

func ddlStructReferencesTarget(definition *StructDef, targetID, targetName string) bool {
	if definition == nil {
		return false
	}
	for index := range definition.Fields {
		reference := definition.Fields[index].Reference
		if reference != nil && strings.EqualFold(reference.Struct, targetName) {
			return true
		}
	}
	for index := range definition.ForeignConstraints {
		constraint := &definition.ForeignConstraints[index]
		if constraint.TargetStructID == targetID || strings.EqualFold(constraint.TargetStruct, targetName) {
			return true
		}
	}
	return false
}

func ddlRetargetStructReferences(definition *StructDef, targetID, oldName, newName string) bool {
	changed := false
	for index := range definition.Fields {
		reference := definition.Fields[index].Reference
		if reference != nil && strings.EqualFold(reference.Struct, oldName) {
			reference.Struct = newName
			changed = true
		}
	}
	for index := range definition.ForeignConstraints {
		constraint := &definition.ForeignConstraints[index]
		if constraint.TargetStructID == targetID || strings.EqualFold(constraint.TargetStruct, oldName) {
			constraint.TargetStruct = newName
			changed = true
		}
	}
	return changed
}

func ddlRenameSecondaryIndexDefinition(
	stored *StructDef,
	source indexDef,
	newName string,
) (*StructDef, error) {
	current, err := cloneKitDBDDLDefinition(stored)
	if err != nil {
		return nil, err
	}
	identity := kitDBIndexStorageIdentity(stored, source)
	renamed := 0
	for fieldIndex := range current.Fields {
		field := &current.Fields[fieldIndex]
		for memberIndex := range field.Indexes {
			member := &field.Indexes[memberIndex]
			name := member.Name
			if name == "" {
				name = "idx_" + stored.Name + "_" + field.Name
			}
			if !strings.EqualFold(name, source.name) {
				continue
			}
			member.Name = newName
			member.ID = identity
			renamed++
		}
	}
	if renamed == 0 {
		return nil, fmt.Errorf("kitdb SQL: index %q changed while rename was being prepared; retry the statement", source.name)
	}
	hydrateKitDBStructColumns(current)
	refreshStructHash(current)
	if err := prepareStructCheckConstraints(current); err != nil {
		return nil, err
	}
	return current, nil
}

func ddlEnsureRenameReady(database *kitdbengine.DB, definition *StructDef) error {
	if _, found, err := loadKitDBRowMigrationState(database, definition); err != nil {
		return err
	} else if found {
		return fmt.Errorf("kitdb SQL: struct %q has a pending row migration; finish or cancel it before rename", definition.Name)
	}
	for _, mode := range []byte{kitDBIndexBuildCodec, kitDBIndexBuildSchema} {
		if _, found, err := loadKitDBIndexBuildState(database, definition, mode); err != nil {
			return err
		} else if found {
			return fmt.Errorf("kitdb SQL: struct %q has pending index maintenance; finish it before rename", definition.Name)
		}
	}
	return nil
}

func ddlValidateDefinitionGraph(definitions map[string]*StructDef) error {
	names := sortedKitDBDefinitionNames(definitions)
	indexOwners := make(map[string]string)
	for _, name := range names {
		definition := definitions[name]
		if err := validateKitDBStruct(definition, definitions); err != nil {
			return err
		}
		for _, indexName := range ddlDefinitionIndexNames(definition) {
			key := strings.ToLower(indexName)
			if owner := indexOwners[key]; owner != "" {
				return fmt.Errorf("kitdb SQL: index %q already exists on table %q", indexName, owner)
			}
			indexOwners[key] = definition.Name
		}
	}
	return nil
}

func commitKitDBDDLRenames(
	database *kitdbengine.DB,
	changes map[string]kitDBDDLRenameChange,
	metadata []kitDBDDLMetadataWrite,
) error {
	if database == nil || len(changes) == 0 {
		return fmt.Errorf("kitdb SQL: rename catalog transaction is unavailable")
	}
	ids := make([]string, 0, len(changes))
	batchParts := make([]string, 0, len(changes))
	for id, change := range changes {
		active, found, err := database.CatalogStructByID(id)
		if err != nil {
			return err
		}
		if !found || change.stored == nil || active.Hash != change.stored.Hash {
			return fmt.Errorf("kitdb SQL: struct changed while rename was being prepared; retry the statement")
		}
		ids = append(ids, id)
		batchParts = append(batchParts, id+":"+change.current.Hash)
	}
	sort.Strings(ids)
	sort.Strings(batchParts)
	batch := stableSchemaID("migration-batch", strings.Join(batchParts, "|"))
	appliedAt := time.Now().UTC()
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		if errors.Is(cause, kitdbengine.ErrTransactionTooLarge) {
			return fmt.Errorf("kitdb SQL: rename transaction exceeds the format limit")
		}
		return cause
	}
	for _, id := range ids {
		change := changes[id]
		catalog, auditKey, audit, err := encodeKitDBMigrationAt(
			change.current, change.steps, change.stored.Hash, batch, appliedAt,
		)
		if err != nil {
			return rollback(err)
		}
		if err := tx.DefineStruct(catalog); err != nil {
			return rollback(err)
		}
		if len(audit) != 0 {
			if err := tx.Put(auditKey, audit); err != nil {
				return rollback(err)
			}
		}
	}
	for _, write := range metadata {
		if err := tx.Put(write.key, write.value); err != nil {
			return rollback(err)
		}
	}
	_, err = tx.Commit()
	if errors.Is(err, kitdbengine.ErrTransactionTooLarge) {
		return fmt.Errorf("kitdb SQL: rename transaction exceeds the format limit")
	}
	return err
}

func ddlAvailableStructIdentity(name string, used map[string]string) string {
	for attempt := 1; ; attempt++ {
		identityName := name
		if attempt > 1 {
			identityName = fmt.Sprintf("%s#%d", name, attempt)
		}
		identity := stableSchemaID("struct", identityName)
		if used[identity] == "" {
			return identity
		}
	}
}

func ddlAvailableIndexIdentity(definition *StructDef, candidate indexDef) string {
	used := make(map[string]struct{})
	for _, index := range collectIndexes(definition.Name, definition.columns) {
		used[kitDBIndexStorageIdentity(definition, index)] = struct{}{}
	}
	base := definition.ID + ":" + candidate.name + ":" + strings.Join(candidate.columns, ",")
	for attempt := 1; ; attempt++ {
		identityName := base
		if attempt > 1 {
			identityName = fmt.Sprintf("%s#%d", base, attempt)
		}
		identity := stableSchemaID("index", identityName)
		if _, found := used[identity]; !found {
			return identity
		}
	}
}

func executeKitDBRemoteDropConstraint(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.alterConstraint
	if plan == nil || strings.TrimSpace(statement.table) == "" || strings.TrimSpace(plan.name) == "" {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE DROP CONSTRAINT plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if strings.EqualFold(entry.Name, statement.table) {
			stored = definition
		}
	}
	if stored == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such table: %s", statement.table)
	}
	constraint, found, err := ddlDefinitionConstraint(stored, plan.name)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if !found {
		if plan.ifExists {
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: no such constraint %q on table %q", plan.name, stored.Name,
		)
	}
	if database.kitDBStructIsSourceDeclared(stored.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: DROP CONSTRAINT for source-declared struct %q is not supported; change its struct() declaration deliberately",
			stored.Name,
		)
	}
	if constraint.kind == kitDBDDLConstraintPrimary {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: primary key constraint %q on table %q cannot be dropped without a replacement; direct primary-key removal is not supported; use ALTER TABLE %s ALTER PRIMARY KEY (...) for a catalog-owned rekey",
			constraint.name, stored.Name, stored.Name,
		)
	}
	if dependent, detail := ddlUniqueConstraintDependent(definitions, stored, constraint); dependent != nil {
		if plan.cascade {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: cannot drop constraint %q on table %q because table %q depends on it through %s; cascading dependent constraints is not supported yet",
				constraint.name, stored.Name, dependent.Name, detail,
			)
		}
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: cannot drop constraint %q on table %q because table %q depends on it through %s",
			constraint.name, stored.Name, dependent.Name, detail,
		)
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	current, err := ddlDropConstraintDefinition(stored, constraint)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	tables[current.Name] = current.columns
	resolver = &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		if err := validateKitDBStruct(definitions[name], definitions); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	intents := make(map[string]kitDBMigrationIntent)
	addKitDBReferenceChangeIntents(intents, stored, current)
	if err := ensureKitDBSchemaWithIntents(
		managed.database, definitions, true,
		func() error { return nil },
		intents,
	); err != nil {
		return kitDBRemoteResult{}, err
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteAddConstraint(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.alterConstraint
	if plan == nil || plan.action != "add" || strings.TrimSpace(statement.table) == "" ||
		strings.TrimSpace(plan.name) == "" ||
		(plan.kind != "unique" && plan.kind != "foreign" && plan.kind != "check") {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE ADD CONSTRAINT plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if strings.EqualFold(entry.Name, statement.table) {
			stored = definition
		}
	}
	if stored == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such table: %s", statement.table)
	}
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	if database.kitDBStructIsSourceDeclared(stored.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: ADD CONSTRAINT for source-declared struct %q is not supported; change its struct() declaration deliberately",
			stored.Name,
		)
	}
	if existing, found, findErr := ddlDefinitionConstraint(stored, plan.name); findErr != nil {
		return kitDBRemoteResult{}, findErr
	} else if found {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: constraint %q already exists on table %q as %q",
			plan.name, stored.Name, existing.name,
		)
	}
	if plan.kind == "unique" {
		for _, name := range sortedKitDBDefinitionNames(definitions) {
			definition := definitions[name]
			if definition == nil || definition.ID == stored.ID {
				continue
			}
			if owner, found := ddlDefinitionIndex(definition, plan.name); found {
				return kitDBRemoteResult{}, fmt.Errorf(
					"kitdb SQL: index %q already exists on table %q", plan.name, owner,
				)
			}
		}
	}

	current, err := ddlAddConstraintDefinition(stored, plan, tables, definitions)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	tables[current.Name] = current.columns
	resolver = &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		if err := validateKitDBStruct(definitions[name], definitions); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBSchema(
		managed.database, definitions, true,
		func() error { return nil },
	); err != nil {
		return kitDBRemoteResult{}, err
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteAlterPrimaryKey(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.alterPrimaryKey
	if plan == nil || strings.TrimSpace(statement.table) == "" || len(plan.columns) == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE ALTER PRIMARY KEY plan")
	}
	if err := database.refreshKitDBCatalogDefinitions(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if strings.EqualFold(entry.Name, statement.table) {
			stored = definition
		}
	}
	if stored == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: no such table: %s", statement.table)
	}
	if database.kitDBStructIsSourceDeclared(stored.Name) {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: ALTER PRIMARY KEY for source-declared struct %q is not supported; open it as catalog-owned for SQL migration, then update struct() to the published key",
			stored.Name,
		)
	}
	if _, found, err := loadKitDBRowMigrationState(managed.database, stored); err != nil {
		return kitDBRemoteResult{}, err
	} else if found {
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: table %q must finish or cancel its current row migration before changing its primary key",
			stored.Name,
		)
	}
	for _, mode := range []byte{kitDBIndexBuildCodec, kitDBIndexBuildSchema} {
		if _, found, err := loadKitDBIndexBuildState(managed.database, stored, mode); err != nil {
			return kitDBRemoteResult{}, err
		} else if found {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: table %q must finish its current index maintenance before changing its primary key",
				stored.Name,
			)
		}
	}

	requested := make([]StructFieldDef, len(plan.columns))
	seen := make(map[string]struct{}, len(plan.columns))
	for position, column := range plan.columns {
		name, field, fieldErr := kitDBRemoteField(stored, column)
		if fieldErr != nil {
			return kitDBRemoteResult{}, fieldErr
		}
		identity := strings.ToLower(name)
		if _, duplicate := seen[identity]; duplicate {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: PRIMARY KEY repeats column %q", field.Name,
			)
		}
		seen[identity] = struct{}{}
		requested[position] = field
	}
	previous := stored.primaryFields()
	if sameStructFieldIdentity(previous, requested) {
		return kitDBRemoteResult{}, nil
	}

	columns := cloneKitDBDDLColumns(stored.columns)
	for _, field := range stored.Fields {
		spec := columns[field.Name]
		if spec == nil {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: column metadata for %q is unavailable", field.Name,
			)
		}
		spec.primary = false
		spec.primaryOrder = 0
		if field.Primary {
			// Struct IR v2 records the uniqueness implied by a single primary
			// key. Demotion removes that implicit constraint just as DROP PK does.
			spec.unique = false
		}
	}
	for position, field := range requested {
		spec := columns[field.Name]
		spec.primary = true
		spec.primaryOrder = position + 1
		spec.notNull = true
		spec.unique = false
	}

	current := bindStructDefWithID(stored.Name, stored.ID, stored, columns)
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	tables[current.Name] = current.columns
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		if err := validateKitDBStruct(definitions[name], definitions); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	intents := make(map[string]kitDBMigrationIntent)
	changed := make(map[string]struct{}, len(previous)+len(requested))
	for _, field := range previous {
		changed[field.ID] = struct{}{}
	}
	for _, field := range requested {
		changed[field.ID] = struct{}{}
	}
	for fieldID := range changed {
		addKitDBMigrationIntent(intents, stored.ID, fieldID, "primary")
	}

	if err := ensureKitDBSchemaWithIntents(
		managed.database, definitions, true,
		func() error { return nil },
		intents,
	); err != nil {
		if errors.Is(err, errKitDBRowMigrationPending) {
			database.markKitDBCatalogPending()
			if _, scheduleErr := scheduleKitDBRowMigration(managed); scheduleErr != nil {
				return kitDBRemoteResult{}, errors.Join(err, scheduleErr)
			}
		}
		return kitDBRemoteResult{}, err
	}
	if _, found, err := loadKitDBRowMigrationState(managed.database, current); err != nil {
		return kitDBRemoteResult{}, err
	} else if found {
		database.markKitDBCatalogPending()
		if _, scheduleErr := scheduleKitDBRowMigration(managed); scheduleErr != nil {
			return kitDBRemoteResult{}, scheduleErr
		}
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteAlterAddColumn(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.alterColumn
	if plan == nil || plan.column.name == "" || plan.column.spec == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE ADD COLUMN plan")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := table.ensureKitDBStruct(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		if strings.EqualFold(entry.Name, table.table) {
			stored = definition
		}
	}
	if stored == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q has no durable catalog definition", table.table)
	}
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	for _, field := range stored.Fields {
		if !strings.EqualFold(field.Name, plan.column.name) {
			continue
		}
		if plan.ifNotExists {
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q already has column %q", stored.Name, field.Name)
	}

	plan.column.spec.seq = uint64(len(stored.Fields) + 1)
	stored.columns[plan.column.name] = plan.column.spec
	tables[stored.Name] = stored.columns
	if err := canonicalizeKitSQLReferences(
		map[string]*ColumnSpec{plan.column.name: plan.column.spec}, tables,
	); err != nil {
		return kitDBRemoteResult{}, err
	}
	resolver = &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	current := bindStructDefWithID(stored.Name, stored.ID, stored, stored.columns)
	if err := appendKitSQLCheckConstraints(current, plan.column.checks); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	if err := validateKitDBStruct(current, definitions); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBCatalog(
		managed.database, current, definitions, true,
		func() error { return nil },
	); err != nil {
		return kitDBRemoteResult{}, err
	}
	if pending, err := wakeKitDBSecondaryIndexIfPending(managed); err != nil {
		return kitDBRemoteResult{}, err
	} else if pending {
		database.markKitDBCatalogPending()
	}
	database.publishKitDBDefinition(managed, current)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteAlterColumn(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	plan := statement.alterColumn
	if plan == nil || plan.name == "" ||
		(plan.action != "rename" && plan.action != "drop" && plan.action != "type") {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: invalid ALTER TABLE column plan")
	}
	table, err := kitDBRemoteTable(database, scope, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := table.ensureKitDBStruct(); err != nil && !errors.Is(err, errKitDBRowMigrationPending) {
		return kitDBRemoteResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}

	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()

	catalog, err := managed.database.Catalog()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	tables, definitions := database.schemaSnapshot()
	durableDefinitions := make(map[string]*StructDef, len(catalog.Structs))
	var stored *StructDef
	for _, entry := range catalog.Structs {
		definition, decodeErr := decodeKitDBCatalog(entry.Definition, entry.Name)
		if decodeErr != nil {
			return kitDBRemoteResult{}, decodeErr
		}
		definitions[entry.Name] = definition
		tables[entry.Name] = definition.columns
		durableDefinitions[entry.Name] = definition
		if strings.EqualFold(entry.Name, table.table) {
			stored = definition
		}
	}
	if stored == nil {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: table %q has no durable catalog definition", table.table)
	}
	fieldName, field, fieldErr := kitDBRemoteField(stored, plan.name)
	if fieldErr != nil {
		if plan.action == "drop" && plan.ifExists {
			return kitDBRemoteResult{}, nil
		}
		return kitDBRemoteResult{}, fieldErr
	}
	if err := validateKitDBAlterColumnDependencies(definitions, stored, field, plan.action); err != nil {
		return kitDBRemoteResult{}, err
	}

	columns := cloneKitDBDDLColumns(stored.columns)
	intents := make(map[string]kitDBMigrationIntent)
	switch plan.action {
	case "rename":
		if plan.newName == "" {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: RENAME COLUMN needs a new name")
		}
		for _, existing := range stored.Fields {
			if existing.ID != field.ID && strings.EqualFold(existing.Name, plan.newName) {
				return kitDBRemoteResult{}, fmt.Errorf(
					"kitdb SQL: table %q already has column %q", stored.Name, existing.Name,
				)
			}
			if existing.ID == field.ID {
				continue
			}
			for _, alias := range existing.Aliases {
				if strings.EqualFold(alias, plan.newName) {
					return kitDBRemoteResult{}, fmt.Errorf(
						"kitdb SQL: column name %q is reserved by field %q history", plan.newName, existing.Name,
					)
				}
			}
		}
		if field.Name == plan.newName {
			return kitDBRemoteResult{}, nil
		}
		spec := columns[fieldName]
		delete(columns, fieldName)
		spec.from = field.Name
		columns[plan.newName] = spec
		renameKitDBDDLColumnDependencies(columns, stored.Name, field.Name, plan.newName)
	case "drop":
		delete(columns, fieldName)
		addKitDBMigrationIntent(intents, stored.ID, field.ID, "drop")
	case "type":
		spec := columns[fieldName]
		if spec == nil || plan.kind == "" {
			return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: ALTER COLUMN TYPE is incomplete")
		}
		if spec.kind == plan.kind && equalKitDBDDLStrings(spec.enumVals, plan.choices) {
			return kitDBRemoteResult{}, nil
		}
		if spec.defaultNow && plan.kind != "datetime" {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: column %q uses CURRENT_TIMESTAMP and cannot change to %s", field.Name, plan.kind,
			)
		}
		if spec.touch && plan.kind != "datetime" {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: column %q is updated automatically and cannot change to %s", field.Name, plan.kind,
			)
		}
		if spec.hasDefault {
			converted, castErr := castKitDBMigrationValue(spec.kind, plan.kind, spec.def)
			if castErr != nil {
				return kitDBRemoteResult{}, fmt.Errorf(
					"kitdb SQL: column %q default cannot cast from %s to %s: %w",
					field.Name, spec.kind, plan.kind, castErr,
				)
			}
			spec.def = converted
		}
		spec.kind = plan.kind
		spec.enumVals = append([]string(nil), plan.choices...)
		if spec.hasDefault && spec.kind == "enum" && !inEnum(spec, spec.def.String()) {
			return kitDBRemoteResult{}, fmt.Errorf(
				"kitdb SQL: column %q default %q is not in the new choice values",
				field.Name, spec.def.String(),
			)
		}
		addKitDBMigrationIntent(intents, stored.ID, field.ID, "type")
	}

	tables[stored.Name] = columns
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	current := bindStructDefWithID(stored.Name, stored.ID, stored, columns)
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		return kitDBRemoteResult{}, err
	}
	definitions[current.Name] = current
	tables[current.Name] = current.columns
	published := map[string]*StructDef{current.Name: current}
	if plan.action == "rename" {
		addKitDBReferenceChangeIntents(intents, stored, current)
		for _, name := range sortedKitDBDefinitionNames(durableDefinitions) {
			candidate := durableDefinitions[name]
			if candidate == nil || candidate.ID == stored.ID {
				continue
			}
			locals := kitDBDDLFieldsReferencing(candidate, stored, field)
			if len(locals) == 0 {
				continue
			}
			dependentColumns := cloneKitDBDDLColumns(candidate.columns)
			for _, local := range locals {
				spec := dependentColumns[local.Name]
				if spec == nil || spec.fk == nil {
					return kitDBRemoteResult{}, fmt.Errorf(
						"kitdb SQL: reference metadata for %s.%s is unavailable", candidate.Name, local.Name,
					)
				}
				spec.fk.column = plan.newName
				spec.fk.target = nil
			}
			tables[candidate.Name] = dependentColumns
			resolver = &dbProxy{tables: tables, structs: definitions}
			resolver.resolveForeignKeys()
			dependent := bindStructDefWithID(candidate.Name, candidate.ID, candidate, dependentColumns)
			if err := reconcileKitDBDefinition(candidate, dependent); err != nil {
				return kitDBRemoteResult{}, err
			}
			definitions[dependent.Name] = dependent
			tables[dependent.Name] = dependent.columns
			published[dependent.Name] = dependent
			addKitDBReferenceChangeIntents(intents, candidate, dependent)
		}
	}
	resolver = &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		if err := validateKitDBStruct(definitions[name], definitions); err != nil {
			return kitDBRemoteResult{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := ensureKitDBSchemaWithIntents(
		managed.database, definitions, true,
		func() error { return nil },
		intents,
	); err != nil {
		if _, scheduleErr := wakeKitDBSecondaryIndexIfPending(managed); scheduleErr != nil {
			return kitDBRemoteResult{}, errors.Join(err, scheduleErr)
		}
		if errors.Is(err, errKitDBRowMigrationPending) {
			database.markKitDBCatalogPending()
			if _, scheduleErr := scheduleKitDBRowMigration(managed); scheduleErr != nil {
				return kitDBRemoteResult{}, errors.Join(err, scheduleErr)
			}
		}
		return kitDBRemoteResult{}, err
	}
	if pending, err := wakeKitDBSecondaryIndexIfPending(managed); err != nil {
		return kitDBRemoteResult{}, err
	} else if pending {
		database.markKitDBCatalogPending()
	}
	database.publishKitDBDefinitions(managed, published)
	return kitDBRemoteResult{}, nil
}

func executeKitDBRemoteCancelMigration(
	ctx context.Context,
	scope *requestscope.Scope,
	database *dbProxy,
	statement kitSQLStatement,
) (kitDBRemoteResult, error) {
	_, definition, err := kitDBRemoteDefinition(database, statement.table)
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	managed, err := kitDBForRequest(database.tenant, database.dbName, scope).database()
	if err != nil {
		return kitDBRemoteResult{}, err
	}
	defer managed.Release()
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	if err := checkpointKitDBBeforeWrite(managed); err != nil {
		return kitDBRemoteResult{}, err
	}
	managed.writeMu.Lock()
	defer managed.writeMu.Unlock()
	state, found, err := loadKitDBRowMigrationState(managed.database, definition)
	if err != nil || !found {
		return kitDBRemoteResult{}, err
	}
	if state.TargetGeneration == 0 {
		return kitDBRemoteResult{}, fmt.Errorf("kitdb SQL: legacy in-place migration for table %q cannot be cancelled safely", definition.Name)
	}
	switch state.Phase {
	case kitDBRowMigrationPhaseRows:
		state, err = beginKitDBRowMigrationCancellation(managed.database, state)
		if err != nil {
			return kitDBRemoteResult{}, err
		}
	case kitDBRowMigrationPhaseCancel:
	case kitDBRowMigrationPhaseCleanup:
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: migration for table %q has already published its target and cannot be cancelled",
			definition.Name,
		)
	default:
		return kitDBRemoteResult{}, fmt.Errorf(
			"kitdb SQL: migration for table %q has invalid phase %q", definition.Name, state.Phase,
		)
	}
	database.markKitDBCatalogPending()
	if err := ctx.Err(); err != nil {
		return kitDBRemoteResult{}, err
	}
	complete, _, err := advanceKitDBCancelledRowCleanup(managed.database, state)
	if err != nil || complete {
		return kitDBRemoteResult{}, err
	}
	_, scheduleErr := scheduleKitDBRowMigration(managed)
	return kitDBRemoteResult{}, scheduleErr
}

func cloneKitDBDDLColumns(columns map[string]*ColumnSpec) map[string]*ColumnSpec {
	cloned := make(map[string]*ColumnSpec, len(columns))
	for name, source := range columns {
		if source == nil {
			continue
		}
		spec := *source
		spec.enumVals = append([]string(nil), source.enumVals...)
		spec.uniques = append([]colUniqueRef(nil), source.uniques...)
		spec.checks = append([]colCheckRef(nil), source.checks...)
		spec.indexes = make([]colIndexRef, len(source.indexes))
		for index, member := range source.indexes {
			spec.indexes[index] = member
			spec.indexes[index].filter = append([]indexCond(nil), member.filter...)
		}
		if source.fk != nil {
			reference := *source.fk
			reference.target = nil
			spec.fk = &reference
		}
		cloned[name] = &spec
	}
	return cloned
}

func addKitDBMigrationIntent(
	intents map[string]kitDBMigrationIntent,
	structID, fieldID, action string,
) {
	intent := intents[structID]
	if intent.actions == nil {
		intent.actions = make(map[string]string)
	}
	intent.actions[fieldID] = action
	intents[structID] = intent
}

func addKitDBReferenceChangeIntents(
	intents map[string]kitDBMigrationIntent,
	stored, current *StructDef,
) {
	if stored == nil || current == nil {
		return
	}
	previous := make(map[string]StructFieldDef, len(stored.Fields))
	for _, field := range stored.Fields {
		previous[field.ID] = field
	}
	for _, field := range current.Fields {
		old, found := previous[field.ID]
		if found && !equalKitDBDDLReference(old.Reference, field.Reference) {
			addKitDBMigrationIntent(intents, current.ID, field.ID, "reference")
		}
	}
}

func equalKitDBDDLReference(left, right *StructReferenceDef) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Struct == right.Struct && left.Field == right.Field &&
		left.OnDelete == right.OnDelete && left.OnUpdate == right.OnUpdate
}

func kitDBDDLFieldsReferencing(
	definition, target *StructDef,
	targetField StructFieldDef,
) []StructFieldDef {
	if definition == nil || target == nil {
		return nil
	}
	fields := make([]StructFieldDef, 0)
	for _, field := range definition.Fields {
		if field.Reference == nil || !strings.EqualFold(field.Reference.Struct, target.Name) {
			continue
		}
		resolved, found := kitDBField(target, field.Reference.Field)
		if found && resolved.ID == targetField.ID {
			fields = append(fields, field)
		}
	}
	return fields
}

func renameKitDBDDLColumnDependencies(
	columns map[string]*ColumnSpec,
	table, previous, current string,
) {
	for _, spec := range columns {
		for index := range spec.indexes {
			for condition := range spec.indexes[index].filter {
				if strings.EqualFold(spec.indexes[index].filter[condition].col, previous) {
					spec.indexes[index].filter[condition].col = current
				}
			}
		}
		if spec.fk != nil && strings.EqualFold(spec.fk.table, table) &&
			strings.EqualFold(spec.fk.column, previous) {
			spec.fk.column = current
			spec.fk.target = nil
		}
	}
}

func validateKitDBAlterColumnDependencies(
	definitions map[string]*StructDef,
	definition *StructDef,
	field StructFieldDef,
	action string,
) error {
	if action == "rename" {
		return nil
	}
	if field.Primary {
		return fmt.Errorf("kitdb SQL: primary key column %q cannot be dropped or retyped", field.Name)
	}
	if field.Reference != nil {
		return fmt.Errorf("kitdb SQL: column %q participates in a foreign key", field.Name)
	}
	for _, constraint := range definition.ForeignConstraints {
		if containsKitDBFieldTag(constraint.Fields, field.Tag) {
			return fmt.Errorf(
				"kitdb SQL: column %q participates in foreign key %q", field.Name, constraint.Name,
			)
		}
	}
	for _, candidate := range definitions {
		if candidate == nil {
			continue
		}
		for _, local := range candidate.Fields {
			if local.Reference == nil || !strings.EqualFold(local.Reference.Struct, definition.Name) {
				continue
			}
			target, found := kitDBField(definition, local.Reference.Field)
			if found && target.ID == field.ID {
				return fmt.Errorf(
					"kitdb SQL: column %q is referenced by %s.%s", field.Name, candidate.Name, local.Name,
				)
			}
		}
		for _, constraint := range candidate.ForeignConstraints {
			if constraint.TargetStructID != definition.ID {
				continue
			}
			for _, targetID := range constraint.TargetFields {
				if targetID == field.ID {
					return fmt.Errorf(
						"kitdb SQL: column %q is referenced by %s constraint %q",
						field.Name, candidate.Name, constraint.Name,
					)
				}
			}
		}
	}
	if action == "type" {
		return nil
	}
	if field.Unique || len(field.Indexes) != 0 {
		return fmt.Errorf("kitdb SQL: column %q participates in an index or unique constraint", field.Name)
	}
	for _, constraint := range definition.UniqueConstraints {
		if containsKitDBFieldTag(constraint.Fields, field.Tag) {
			return fmt.Errorf(
				"kitdb SQL: column %q participates in unique constraint %q", field.Name, constraint.Name,
			)
		}
	}
	for _, constraint := range definition.CheckConstraints {
		if structCheckExpressionReferencesField(constraint.Expression, field.Tag) {
			return fmt.Errorf(
				"kitdb SQL: column %q is used by check constraint %q", field.Name, constraint.Name,
			)
		}
	}
	for _, candidate := range definition.Fields {
		for _, member := range candidate.Indexes {
			for _, condition := range member.Filter {
				if strings.EqualFold(condition.Field, field.Name) {
					return fmt.Errorf(
						"kitdb SQL: column %q is used by index %q", field.Name, member.Name,
					)
				}
			}
		}
	}
	return nil
}

func containsKitDBFieldTag(tags []uint32, requested uint32) bool {
	for _, tag := range tags {
		if tag == requested {
			return true
		}
	}
	return false
}

func equalKitDBDDLStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func ddlDefinitionConstraint(
	definition *StructDef,
	requested string,
) (kitDBDDLConstraint, bool, error) {
	if definition == nil {
		return kitDBDDLConstraint{}, false, nil
	}
	matches := make([]kitDBDDLConstraint, 0, 1)
	appendMatch := func(candidate kitDBDDLConstraint) {
		if strings.EqualFold(candidate.name, requested) {
			matches = append(matches, candidate)
		}
	}
	if _, found := definition.primaryField(); found {
		appendMatch(kitDBDDLConstraint{
			kind: kitDBDDLConstraintPrimary, name: definition.Name + "_pkey",
		})
	}
	for _, field := range definition.Fields {
		if field.Unique && !field.Primary {
			appendMatch(kitDBDDLConstraint{
				kind: kitDBDDLConstraintFieldUnique,
				name: "unique_" + definition.Name + "_" + field.Name, fieldID: field.ID,
			})
		}
		if field.Reference != nil {
			appendMatch(kitDBDDLConstraint{
				kind: kitDBDDLConstraintFieldForeign,
				name: definition.Name + "_" + field.Name + "_fkey", fieldID: field.ID,
			})
		}
	}
	for _, constraint := range definition.UniqueConstraints {
		appendMatch(kitDBDDLConstraint{
			kind: kitDBDDLConstraintTupleUnique, name: constraint.Name, id: constraint.ID,
		})
	}
	for _, constraint := range definition.ForeignConstraints {
		appendMatch(kitDBDDLConstraint{
			kind: kitDBDDLConstraintTupleForeign, name: constraint.Name, id: constraint.ID,
		})
	}
	for _, constraint := range definition.CheckConstraints {
		appendMatch(kitDBDDLConstraint{
			kind: kitDBDDLConstraintCheck, name: constraint.Name, id: constraint.ID,
		})
	}
	if len(matches) == 0 {
		return kitDBDDLConstraint{}, false, nil
	}
	if len(matches) != 1 {
		return kitDBDDLConstraint{}, false, fmt.Errorf(
			"kitdb SQL: constraint %q is ambiguous on table %q", requested, definition.Name,
		)
	}
	return matches[0], true, nil
}

func ddlDropConstraintDefinition(
	stored *StructDef,
	constraint kitDBDDLConstraint,
) (*StructDef, error) {
	if stored == nil {
		return nil, fmt.Errorf("kitdb SQL: constraint storage definition is unavailable")
	}
	columns := cloneKitDBDDLColumns(stored.columns)
	source := *stored
	source.CheckConstraints = cloneStructCheckConstraints(stored.CheckConstraints)
	removed := false

	switch constraint.kind {
	case kitDBDDLConstraintFieldUnique, kitDBDDLConstraintFieldForeign:
		for _, field := range stored.Fields {
			if field.ID != constraint.fieldID {
				continue
			}
			spec := columns[field.Name]
			if spec == nil {
				break
			}
			if constraint.kind == kitDBDDLConstraintFieldUnique && spec.unique {
				spec.unique = false
				removed = true
			}
			if constraint.kind == kitDBDDLConstraintFieldForeign && spec.fk != nil && spec.fk.name == "" {
				spec.fk = nil
				removed = true
			}
			break
		}
	case kitDBDDLConstraintTupleUnique:
		for _, spec := range columns {
			retained := make([]colUniqueRef, 0, len(spec.uniques))
			for _, member := range spec.uniques {
				if strings.EqualFold(member.name, constraint.name) {
					removed = true
					continue
				}
				retained = append(retained, member)
			}
			spec.uniques = retained
		}
	case kitDBDDLConstraintTupleForeign:
		for _, spec := range columns {
			if spec.fk != nil && strings.EqualFold(spec.fk.name, constraint.name) {
				spec.fk = nil
				removed = true
			}
		}
	case kitDBDDLConstraintCheck:
		retained := make([]StructCheckConstraint, 0, len(source.CheckConstraints))
		for _, check := range source.CheckConstraints {
			if check.ID == constraint.id {
				removed = true
				continue
			}
			retained = append(retained, check)
		}
		source.CheckConstraints = retained
	default:
		return nil, fmt.Errorf("kitdb SQL: constraint %q cannot be dropped", constraint.name)
	}
	if !removed {
		return nil, fmt.Errorf(
			"kitdb SQL: constraint %q changed while DROP CONSTRAINT was being prepared; retry the statement",
			constraint.name,
		)
	}
	current := bindStructDefWithID(stored.Name, stored.ID, &source, columns)
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		return nil, err
	}
	return current, nil
}

func ddlAddConstraintDefinition(
	stored *StructDef,
	plan *kitSQLAlterConstraint,
	tables map[string]map[string]*ColumnSpec,
	definitions map[string]*StructDef,
) (*StructDef, error) {
	if stored == nil || plan == nil {
		return nil, fmt.Errorf("kitdb SQL: constraint storage definition is unavailable")
	}
	columns := cloneKitDBDDLColumns(stored.columns)
	source := *stored
	source.CheckConstraints = cloneStructCheckConstraints(stored.CheckConstraints)

	resolveColumns := func(requested []string) ([]string, error) {
		resolved := make([]string, len(requested))
		seen := make(map[string]string, len(requested))
		for index, name := range requested {
			canonical, field, err := kitDBRemoteField(stored, name)
			if err != nil {
				return nil, err
			}
			if previous := seen[field.ID]; previous != "" {
				return nil, fmt.Errorf(
					"kitdb SQL: constraint %q repeats column %q", plan.name, previous,
				)
			}
			seen[field.ID], resolved[index] = canonical, canonical
		}
		return resolved, nil
	}

	switch plan.kind {
	case "unique":
		resolved, err := resolveColumns(plan.columns)
		if err != nil {
			return nil, err
		}
		for position, name := range resolved {
			spec := columns[name]
			if spec == nil {
				return nil, fmt.Errorf("kitdb SQL: column metadata for %q is unavailable", name)
			}
			spec.uniques = append(spec.uniques, colUniqueRef{name: plan.name, pos: position + 1})
		}
	case "foreign":
		if plan.reference == nil || len(plan.columns) == 0 ||
			len(plan.columns) != len(plan.reference.fields) {
			return nil, fmt.Errorf("kitdb SQL: foreign key constraint %q has mismatched fields", plan.name)
		}
		resolved, err := resolveColumns(plan.columns)
		if err != nil {
			return nil, err
		}
		for position, name := range resolved {
			spec := columns[name]
			if spec == nil {
				return nil, fmt.Errorf("kitdb SQL: column metadata for %q is unavailable", name)
			}
			if spec.fk != nil {
				return nil, fmt.Errorf(
					"kitdb SQL: column %q already belongs to a foreign key constraint", name,
				)
			}
			spec.fk = &fkRef{
				name: plan.name, pos: position + 1,
				table: plan.reference.table, column: plan.reference.fields[position],
				onDelete: plan.reference.onDelete, onUpdate: plan.reference.onUpdate,
			}
		}
	case "check":
		if plan.check == nil {
			return nil, fmt.Errorf("kitdb SQL: check constraint %q is unavailable", plan.name)
		}
	default:
		return nil, fmt.Errorf("kitdb SQL: unsupported constraint kind %q", plan.kind)
	}

	tables[stored.Name] = columns
	if err := canonicalizeKitSQLReferences(columns, tables); err != nil {
		return nil, err
	}
	resolver := &dbProxy{tables: tables, structs: definitions}
	resolver.resolveForeignKeys()
	current := bindStructDefWithID(stored.Name, stored.ID, &source, columns)
	if plan.kind == "check" {
		if err := appendKitSQLCheckConstraints(current, []kitSQLDDLCheck{*plan.check}); err != nil {
			return nil, err
		}
	}
	if err := reconcileKitDBDefinition(stored, current); err != nil {
		return nil, err
	}
	return current, nil
}

func ddlUniqueConstraintDependent(
	definitions map[string]*StructDef,
	target *StructDef,
	constraint kitDBDDLConstraint,
) (*StructDef, string) {
	fields, found := ddlUniqueConstraintFields(target, constraint)
	if !found {
		return nil, ""
	}
	targetIDs := make([]string, len(fields))
	for index, field := range fields {
		targetIDs[index] = field.ID
	}
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		definition := definitions[name]
		if definition == nil {
			continue
		}
		if len(fields) == 1 {
			for _, field := range definition.Fields {
				if field.Reference == nil || !strings.EqualFold(field.Reference.Struct, target.Name) {
					continue
				}
				referenced, ok := kitDBField(target, field.Reference.Field)
				if ok && referenced.ID == fields[0].ID {
					return definition, fmt.Sprintf(
						"foreign key constraint %q", definition.Name+"_"+field.Name+"_fkey",
					)
				}
			}
		}
		for _, foreign := range definition.ForeignConstraints {
			if foreign.TargetStructID != target.ID && !strings.EqualFold(foreign.TargetStruct, target.Name) {
				continue
			}
			if equalKitDBDDLStrings(foreign.TargetFields, targetIDs) {
				return definition, fmt.Sprintf("foreign key constraint %q", foreign.Name)
			}
		}
	}
	return nil, ""
}

func ddlUniqueConstraintFields(
	definition *StructDef,
	constraint kitDBDDLConstraint,
) ([]StructFieldDef, bool) {
	if definition == nil {
		return nil, false
	}
	switch constraint.kind {
	case kitDBDDLConstraintFieldUnique:
		for _, field := range definition.Fields {
			if field.ID == constraint.fieldID {
				return []StructFieldDef{field}, true
			}
		}
	case kitDBDDLConstraintTupleUnique:
		for _, unique := range definition.UniqueConstraints {
			if unique.ID != constraint.id {
				continue
			}
			fields, err := structUniqueConstraintFields(definition, unique)
			return fields, err == nil
		}
	}
	return nil, false
}

func ddlDefinitionIndex(definition *StructDef, requested string) (string, bool) {
	if definition == nil {
		return "", false
	}
	for _, field := range definition.Fields {
		if field.Unique && !field.Primary && strings.EqualFold("unique_"+definition.Name+"_"+field.Name, requested) {
			return definition.Name, true
		}
	}
	for _, constraint := range definition.UniqueConstraints {
		if strings.EqualFold(constraint.Name, requested) {
			return definition.Name, true
		}
	}
	for _, index := range collectIndexes(definition.Name, definition.columns) {
		if strings.EqualFold(index.name, requested) {
			return definition.Name, true
		}
	}
	return "", false
}

func ddlDefinitionSecondaryIndex(definition *StructDef, requested string) (indexDef, bool) {
	if definition != nil {
		for _, index := range collectIndexes(definition.Name, definition.columns) {
			if strings.EqualFold(index.name, requested) {
				return index, true
			}
		}
	}
	return indexDef{}, false
}

func ddlDefinitionConstraintIndex(definition *StructDef, requested string) (string, bool) {
	if definition == nil {
		return "", false
	}
	if _, found := definition.primaryField(); found {
		name := definition.Name + "_pkey"
		if strings.EqualFold(name, requested) {
			return name, true
		}
	}
	for _, field := range definition.Fields {
		name := "unique_" + definition.Name + "_" + field.Name
		if field.Unique && !field.Primary && strings.EqualFold(name, requested) {
			return name, true
		}
	}
	for _, constraint := range definition.UniqueConstraints {
		if strings.EqualFold(constraint.Name, requested) {
			return constraint.Name, true
		}
	}
	return "", false
}

func ddlDefinitionIndexNames(definition *StructDef) []string {
	if definition == nil {
		return nil
	}
	names := make([]string, 0)
	for _, field := range definition.Fields {
		if field.Unique && !field.Primary {
			names = append(names, "unique_"+definition.Name+"_"+field.Name)
		}
	}
	for _, constraint := range definition.UniqueConstraints {
		names = append(names, constraint.Name)
	}
	for _, index := range collectIndexes(definition.Name, definition.columns) {
		names = append(names, index.name)
	}
	sort.Strings(names)
	return names
}

func ddlValidateDefinitionIndexNames(candidate, existing *StructDef) error {
	for _, name := range ddlDefinitionIndexNames(candidate) {
		if owner, found := ddlDefinitionIndex(existing, name); found {
			return fmt.Errorf("kitdb SQL: index %q already exists on table %q", name, owner)
		}
	}
	return nil
}

func ddlFindStruct(definitions map[string]*StructDef, requested string) (string, *StructDef) {
	for name, definition := range definitions {
		if name == requested {
			return name, definition
		}
	}
	for name, definition := range definitions {
		if strings.EqualFold(name, requested) {
			return name, definition
		}
	}
	return "", nil
}

func canonicalizeKitSQLReferences(
	columns map[string]*ColumnSpec,
	tables map[string]map[string]*ColumnSpec,
) error {
	for columnName, spec := range columns {
		if spec.fk == nil {
			continue
		}
		tableName := ""
		for candidate := range tables {
			if candidate == spec.fk.table || strings.EqualFold(candidate, spec.fk.table) {
				if tableName != "" && tableName != candidate {
					return fmt.Errorf("kitdb SQL: column %q has ambiguous reference table %q", columnName, spec.fk.table)
				}
				tableName = candidate
			}
		}
		if tableName == "" {
			return fmt.Errorf("kitdb SQL: column %q references missing table %q", columnName, spec.fk.table)
		}
		fieldName := ""
		for candidate := range tables[tableName] {
			if candidate == spec.fk.column || strings.EqualFold(candidate, spec.fk.column) {
				if fieldName != "" && fieldName != candidate {
					return fmt.Errorf("kitdb SQL: column %q has ambiguous reference field %q", columnName, spec.fk.column)
				}
				fieldName = candidate
			}
		}
		if fieldName == "" {
			return fmt.Errorf("kitdb SQL: column %q references missing field %s.%s", columnName, tableName, spec.fk.column)
		}
		spec.fk.table, spec.fk.column = tableName, fieldName
	}
	return nil
}
