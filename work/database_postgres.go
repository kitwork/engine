package work

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kitwork/engine/kitdb/gateway"
	"github.com/kitwork/engine/value"
)

// kitwork migrate writes in PostgreSQL's words (KITWORK_DATABASE.md "Database là gốc", slice 4, 05/11/2026): what it
// adds to a database is the statement an admin would send through the port, run through the port's own translator
// (gateway.TranslateSchema), so a table it makes is the STRICT table the port makes, the structure file says it as
// PostgreSQL does, and the plan shows statements one can run anywhere. What the translator does not take (a CHECK on a
// decimal, a decimal over 18 digits, a kind PostgreSQL has no word for) is written as the engine's SQLite statement, as
// before, and shown so.

// migrationStatement is one change kitwork migrate makes to a file.
type migrationStatement struct {
	table    string
	action   string   // create-table, add-column, add-unique, add-index, drop-index
	postgres string   // as PostgreSQL writes it; "" when its words cannot say it
	sqlite   []string // what runs on the file
}

// shown is the statement as the plan shows it: PostgreSQL's, or SQLite's after "-- SQLite:".
func (s migrationStatement) shown() string {
	if s.postgres != "" {
		return s.postgres + ";"
	}
	return "-- SQLite: " + strings.Join(s.sqlite, "; ") + ";"
}

// written is a change as PostgreSQL writes it when the port's translator takes it, else as SQLite's.
func written(table, action, postgres string, sqlite ...string) migrationStatement {
	if postgres != "" {
		if translated, err := gateway.TranslateSchema(postgres); err == nil && len(translated) > 0 {
			return migrationStatement{table: table, action: action, postgres: postgres, sqlite: translated}
		}
	}
	return migrationStatement{table: table, action: action, sqlite: sqlite}
}

// gapStatements are the changes that make the file what its tables' declarations ask, adding only, in the order
// they run; their kinds of kitwork_columns are written after (applyGap).
func gapStatements(tables map[string]map[string]*ColumnSpec, gap databaseGap) []migrationStatement {
	var statements []migrationStatement
	for _, name := range gap.names {
		columns, table := tables[name], gap.tables[name]
		if table.missing {
			create, _ := postgresCreateTable(name, columns)
			statements = append(statements, written(name, "create-table", create, schemaDDL(name, columns)))
			// a column's .unique() is in the table; a named group is an index of its own
			if groups, err := collectColumnUniqueGroups(columns); err == nil {
				for _, group := range groups {
					index := indexDef{name: group.name, columns: group.columns, unique: true}
					statements = append(statements, written(name, "add-unique", postgresIndex(name, index), indexSQL(name, index)))
				}
			}
			for _, index := range collectIndexes(name, columns) {
				statements = append(statements, written(name, "add-index", postgresIndex(name, index), indexSQL(name, index)))
			}
			continue
		}
		for _, column := range table.absent {
			add := ""
			if definition, ok := postgresColumn(column, columns[column], true); ok {
				add = "ALTER TABLE " + gateway.WrittenName(name) + " ADD COLUMN " + definition
			}
			statements = append(statements, written(name, "add-column", add,
				fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s", name, addColumnSQL(column, columns[column]))))
		}
		for _, unique := range table.uniques {
			index := indexDef{name: unique.name, columns: unique.columns, unique: true}
			statements = append(statements, written(name, "add-unique", postgresIndex(name, index), indexSQL(name, index)))
		}
		for _, index := range table.indexes {
			if table.replaced[index.name] {
				statements = append(statements, written(name, "drop-index", "DROP INDEX "+gateway.WrittenName(index.name),
					fmt.Sprintf("DROP INDEX IF EXISTS %q", index.name)))
			}
			statements = append(statements, written(name, "add-index", postgresIndex(name, index), indexSQL(name, index)))
		}
	}
	return statements
}

// postgresType is a column's type as PostgreSQL writes it and the port takes it: "" for a kind it has no word for.
func postgresType(spec *ColumnSpec) string {
	switch spec.kind {
	case "kitid":
		return "kitid" // KitDB's own domain
	case "text":
		return "text"
	case "integer":
		return "bigint"
	case "float":
		return "double precision"
	case "decimal":
		return fmt.Sprintf("numeric(%d, %d)", spec.precision, spec.scale)
	case "bool":
		return "boolean"
	case "date":
		return "date"
	case "json":
		return "json"
	case "datetime":
		return "timestamptz"
	}
	return ""
}

// postgresColumn is a column's definition as CREATE TABLE writes it, or ADD COLUMN (adding): the same as the engine's
// SQLite statements (columnDefinition, addColumnSQL), an added column taking no constraint but its DEFAULT. A static
// .default() is the table's DEFAULT too, which the engine's own statement leaves to the engine's writes: the database
// is the root, and another client's insert takes it. ok is false when PostgreSQL's words cannot say it.
func postgresColumn(name string, spec *ColumnSpec, adding bool) (string, bool) {
	kind := postgresType(spec)
	if kind == "" {
		return "", false
	}
	parts := []string{gateway.WrittenName(name), kind}
	if adding {
		if spec.hasDefault {
			literal, ok := postgresLiteral(spec.def)
			if !ok {
				return "", false
			}
			parts = append(parts, "DEFAULT "+literal)
		}
		return strings.Join(parts, " "), true
	}
	if spec.primary {
		parts = append(parts, "PRIMARY KEY")
	}
	if spec.notNull || spec.primary {
		parts = append(parts, "NOT NULL")
	}
	if spec.unique {
		parts = append(parts, "UNIQUE")
	}
	if spec.hasDefault {
		literal, ok := postgresLiteral(spec.def)
		if !ok {
			return "", false
		}
		parts = append(parts, "DEFAULT "+literal)
	}
	for _, check := range spec.checks {
		// the value as declared (true, not 1); a CHECK on a decimal the port's translator refuses (it would compare
		// the stored integer): SQLite's statement then
		literal, ok := postgresLiteral(check.value)
		if !ok {
			return "", false
		}
		constraint := ""
		if check.name != "" {
			constraint = "CONSTRAINT " + gateway.WrittenName(check.name) + " "
		}
		parts = append(parts, fmt.Sprintf("%sCHECK (%s %s %s)", constraint, gateway.WrittenName(name), check.operator, literal))
	}
	if spec.fk != nil && spec.fk.name == "" && spec.fk.table != "" {
		reference := "REFERENCES " + gateway.WrittenName(spec.fk.table) + " (" + gateway.WrittenName(spec.fk.column) + ")"
		if strings.TrimSpace(spec.fk.onDelete) != "" {
			reference += " ON DELETE " + fkAction(spec.fk.onDelete)
		}
		if strings.TrimSpace(spec.fk.onUpdate) != "" {
			reference += " ON UPDATE " + fkAction(spec.fk.onUpdate)
		}
		parts = append(parts, reference)
	}
	return strings.Join(parts, " "), true
}

// postgresCreateTable is a table's CREATE TABLE as PostgreSQL writes it; ok is false when its words cannot say a column.
func postgresCreateTable(table string, columns map[string]*ColumnSpec) (string, bool) {
	var definitions []string
	for _, name := range orderedColumns(columns) {
		definition, ok := postgresColumn(name, columns[name], false)
		if !ok {
			return "", false
		}
		definitions = append(definitions, definition)
	}
	groups, err := collectColumnForeignGroups(columns)
	if err != nil {
		return "", false
	}
	for _, group := range groups {
		local := make([]string, len(group.columns))
		target := make([]string, len(group.references))
		for index, column := range group.columns {
			local[index] = gateway.WrittenName(column)
			target[index] = gateway.WrittenName(group.references[index].column)
		}
		reference := group.references[0]
		constraint := fmt.Sprintf("CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)", gateway.WrittenName(group.name),
			strings.Join(local, ", "), gateway.WrittenName(reference.table), strings.Join(target, ", "))
		if strings.TrimSpace(reference.onDelete) != "" {
			constraint += " ON DELETE " + fkAction(reference.onDelete)
		}
		if strings.TrimSpace(reference.onUpdate) != "" {
			constraint += " ON UPDATE " + fkAction(reference.onUpdate)
		}
		definitions = append(definitions, constraint)
	}
	return "CREATE TABLE " + gateway.WrittenName(table) + " (\n  " + strings.Join(definitions, ",\n  ") + "\n)", true
}

// postgresIndex is an index's CREATE INDEX as PostgreSQL writes it.
func postgresIndex(table string, index indexDef) string {
	columns := make([]string, len(index.columns))
	for position, column := range index.columns {
		columns[position] = gateway.WrittenName(column)
	}
	text := "CREATE INDEX "
	if index.unique {
		text = "CREATE UNIQUE INDEX "
	}
	text += gateway.WrittenName(index.name) + " ON " + gateway.WrittenName(table) + " (" + strings.Join(columns, ", ") + ")"
	if where := partialWhere(index.filter); where != "" {
		text += " WHERE " + where
	}
	return text
}

// postgresLiteral is a declared value as PostgreSQL writes it in a DEFAULT or a CHECK.
func postgresLiteral(item value.Value) (string, bool) {
	switch item.K {
	case value.Nil:
		return "NULL", true
	case value.Bool:
		if item.N != 0 {
			return "true", true
		}
		return "false", true
	case value.Number:
		return numText(item.N), true
	case value.String:
		return "'" + strings.ReplaceAll(item.String(), "'", "''") + "'", true
	case value.Map, value.Array:
		encoded, err := json.Marshal(item)
		if err != nil {
			return "", false
		}
		return "'" + strings.ReplaceAll(string(encoded), "'", "''") + "'", true
	}
	return "", false
}
