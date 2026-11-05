package work

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/id"
)

// The code follows the database (KITWORK_DATABASE.md "Database là gốc", slice 4, 05/11/2026). The database is the
// root of its structure: the engine never makes a table, adds a column or writes kitwork_columns because the app
// declares one. At the first statement on a database file, the tables .shared/schema/ declares on it are held against
// the file. What the file lacks (a table, a column, a unique constraint) and what differs from it (a column's storage,
// a decimal's scale) fail every statement on the file, saying what and how to change it; what the statements run
// without (an index, a column's kind in kitwork_columns, a .searchable() weight) is said once. kitwork migrate makes
// the file what the declaration asks, adding only (DatabaseMigration); an admin's schema statement through the port
// does it as well. What differs is the code's to change: the database is not rewritten to follow it.

// structureReader reads a file's structure: its pool, the writer's connection, a snapshot of the writer.
type structureReader interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// storedKind is a column's row of kitwork_columns.
type storedKind struct {
	kind             string
	precision, scale int
	weight           int
}

// liveIndex is an index a table has, as SQLite describes it.
type liveIndex struct {
	name    string
	unique  bool
	partial bool
	columns []string
	sql     string // "" for SQLite's own index of a UNIQUE or PRIMARY KEY constraint
}

// declaredUnique is a unique constraint a table declares: a column's .unique() (name ""), or a named group.
type declaredUnique struct {
	name    string
	columns []string
}

// tableGap is what one table's file lacks of the table's declaration.
type tableGap struct {
	missing bool     // the file has no such table
	absent  []string // declared columns the table does not have, in declared order
	uniques []declaredUnique
	indexes []indexDef
	kinds   []string // declared columns whose row of kitwork_columns kitwork migrate writes
	// add and differ are said as lines: what kitwork migrate adds, what the code must change to follow the file
	add    []string
	differ []string
	// differs are the columns that differ: kitwork migrate writes no kind of theirs
	differs map[string]bool
}

// databaseGap is what a database file lacks of the tables the app declares on it, table by table.
type databaseGap struct {
	tables map[string]*tableGap
	names  []string // the declared tables, sorted
}

// blocking reports what fails the statements: a table, a column or a unique constraint the file has not, or a
// column that differs from it.
func (gap databaseGap) blocking() bool {
	for _, name := range gap.names {
		if table := gap.tables[name]; len(table.add) > 0 || len(table.differ) > 0 {
			return true
		}
	}
	return false
}

// lines are the gap said line by line: what kitwork migrate adds, what differs, what it writes besides.
func (gap databaseGap) lines() (add, differ, notes []string) {
	for _, name := range gap.names {
		table := gap.tables[name]
		add = append(add, table.add...)
		differ = append(differ, table.differ...)
		if table.missing {
			continue // its indexes and kinds come with it
		}
		for _, index := range table.indexes {
			notes = append(notes, fmt.Sprintf("chỉ mục %s của bảng %s (%s)", index.name, name, strings.Join(index.columns, ", ")))
		}
		if len(table.kinds) > 0 {
			notes = append(notes, fmt.Sprintf("kiểu của cột %s trong kitwork_columns", qualifiedColumns(name, table.kinds)))
		}
	}
	return add, differ, notes
}

func qualifiedColumns(table string, columns []string) string {
	qualified := make([]string, len(columns))
	for index, column := range columns {
		qualified[index] = table + "." + column
	}
	return strings.Join(qualified, ", ")
}

// checkDatabase holds the declared tables against the file reader reads; a nil reader is a file that is not there.
func checkDatabase(reader structureReader, tables map[string]map[string]*ColumnSpec) (databaseGap, error) {
	gap := databaseGap{tables: map[string]*tableGap{}, names: sortedTableNames(tables)}
	for _, name := range gap.names {
		table, err := checkTable(reader, name, tables[name])
		if err != nil {
			return databaseGap{}, fmt.Errorf("bảng %s: %w", name, err)
		}
		gap.tables[name] = table
	}
	return gap, nil
}

// checkTable holds one table's declaration against the file.
func checkTable(reader structureReader, table string, columns map[string]*ColumnSpec) (*tableGap, error) {
	gap := &tableGap{differs: map[string]bool{}}
	live := map[string]string{}
	if reader != nil {
		var err error
		if live, err = liveColumns(reader, table); err != nil {
			return nil, err
		}
	}
	ordered := orderedColumns(columns)
	if len(live) == 0 {
		gap.missing = true
		gap.add = append(gap.add, fmt.Sprintf("bảng %s chưa có (%s)", table, declaredColumns(columns, ordered)))
		gap.indexes = collectIndexes(table, columns)
		gap.kinds = ordered
		return gap, nil
	}
	stored, err := readStoredKinds(reader, table)
	if err != nil {
		return nil, err
	}
	for _, name := range ordered {
		spec := columns[name]
		current, has := live[name]
		if !has {
			gap.absent = append(gap.absent, name)
			gap.kinds = append(gap.kinds, name)
			continue
		}
		if want := sqliteStorage(spec); !sameType(current, want) {
			gap.differs[name] = true
			if spec.kind == "decimal" && sameType(current, "TEXT") {
				// written before 29/10/2026, when SQLite kept a decimal as its text (db.sqlite_decimal.go)
				gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s là decimal lưu dạng chữ (cách trước 29/10/2026), nay SQLite lưu decimal là số nguyên nhân 10^số_lẻ", table, name))
				continue
			}
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database lưu %s, code khai %s (lưu %s)", table, name, current, declaredType(spec), want))
			continue
		}
		row, recorded := stored[name]
		switch {
		case spec.kind == "decimal" && !recorded:
			gap.differs[name] = true
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database không ghi nó là decimal (kitwork_columns) nên số trong file là số nguyên, code khai %s", table, name, declaredType(spec)))
		case spec.kind == "decimal" && (row.kind != "decimal" || row.scale != spec.scale):
			gap.differs[name] = true
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database ghi %s (kitwork_columns), code khai %s", table, name, storedType(row), declaredType(spec)))
		case spec.kind != "decimal" && recorded && row.kind == "decimal":
			gap.differs[name] = true
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database ghi %s (kitwork_columns), code khai %s", table, name, storedType(row), declaredType(spec)))
		case !recorded || row.kind != spec.kind || spec.kind == "decimal" && row.precision != spec.precision || row.weight != declaredWeight(spec):
			gap.kinds = append(gap.kinds, name)
		}
	}
	if len(gap.absent) > 0 {
		gap.add = append(gap.add, fmt.Sprintf("bảng %s chưa có cột %s", table, declaredColumns(columns, gap.absent)))
	}
	indexes, err := readLiveIndexes(reader, table)
	if err != nil {
		return nil, err
	}
	for _, unique := range declaredUniques(table, columns) {
		if !uniqueHeld(indexes, unique.columns) {
			gap.uniques = append(gap.uniques, unique)
			gap.add = append(gap.add, fmt.Sprintf("bảng %s chưa có ràng buộc unique (%s)", table, strings.Join(unique.columns, ", ")))
		}
	}
	for _, index := range collectIndexes(table, columns) {
		if !indexHeld(indexes, table, index) {
			gap.indexes = append(gap.indexes, index)
		}
	}
	return gap, nil
}

// declaredType is a column's type as .shared/schema/ writes it.
func declaredType(spec *ColumnSpec) string {
	switch {
	case spec.kind == "decimal":
		return fmt.Sprintf("decimal(%d, %d)", spec.precision, spec.scale)
	case spec.builder != "":
		return spec.builder
	}
	return spec.kind
}

// storedType is a row of kitwork_columns as a type.
func storedType(row storedKind) string {
	if row.kind == "decimal" {
		return fmt.Sprintf("decimal(%d, %d)", row.precision, row.scale)
	}
	return row.kind
}

// declaredColumns lists names with their declared types.
func declaredColumns(columns map[string]*ColumnSpec, names []string) string {
	listed := make([]string, len(names))
	for index, name := range names {
		listed[index] = name + " " + declaredType(columns[name])
	}
	return strings.Join(listed, ", ")
}

// declaredWeight is the search_weight kitwork_columns holds for a column: a .searchable() column's weight, 0 for any
// other (writeColumnKinds).
func declaredWeight(spec *ColumnSpec) int {
	if spec.searchable {
		return max(spec.searchWt, 1)
	}
	return 0
}

// declaredUniques are the unique constraints of a table's declaration: each column's .unique() (its key aside), and
// the named groups.
func declaredUniques(table string, columns map[string]*ColumnSpec) []declaredUnique {
	var uniques []declaredUnique
	for _, name := range orderedColumns(columns) {
		if spec := columns[name]; spec.unique && !spec.primary {
			uniques = append(uniques, declaredUnique{name: fmt.Sprintf("%s_%s_unique", table, name), columns: []string{name}})
		}
	}
	if groups, err := collectColumnUniqueGroups(columns); err == nil {
		for _, group := range groups {
			uniques = append(uniques, declaredUnique{name: group.name, columns: append([]string(nil), group.columns...)})
		}
	}
	return uniques
}

// uniqueHeld reports a unique index of the table on exactly columns, in any order, over every row.
func uniqueHeld(indexes []liveIndex, columns []string) bool {
	want := append([]string(nil), columns...)
	sort.Strings(want)
	for _, index := range indexes {
		if !index.unique || index.partial || len(index.columns) != len(want) {
			continue
		}
		have := append([]string(nil), index.columns...)
		sort.Strings(have)
		if strings.Join(have, "\x00") == strings.Join(want, "\x00") {
			return true
		}
	}
	return false
}

// indexHeld reports a declared index the table has: by its name as declared, or, for an index over every row, any
// index over every row on the same columns in the same order (one made through the port under another name).
func indexHeld(indexes []liveIndex, table string, declared indexDef) bool {
	want := normIndexSQL(indexSQL(table, declared))
	for _, index := range indexes {
		if index.name == declared.name && index.sql != "" && normIndexSQL(index.sql) == want {
			return true
		}
		if len(declared.filter) == 0 && !index.partial && strings.Join(index.columns, "\x00") == strings.Join(declared.columns, "\x00") {
			return true
		}
	}
	return false
}

// liveColumns are a table's columns and their types, upper case; none when the file has no such table.
func liveColumns(reader structureReader, table string) (map[string]string, error) {
	// the table-valued pragma, a SELECT: a snapshot of the writer runs no PRAGMA statement
	rows, err := reader.Query(`SELECT name, type FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns := map[string]string{}
	for rows.Next() {
		var name, declared string
		if err := rows.Scan(&name, &declared); err != nil {
			return nil, err
		}
		columns[name] = strings.ToUpper(strings.TrimSpace(declared))
	}
	return columns, rows.Err()
}

// readStoredKinds are kitwork_columns's rows of table, by column; none when the file has no kitwork_columns.
func readStoredKinds(reader structureReader, table string) (map[string]storedKind, error) {
	kinds := map[string]storedKind{}
	columns, err := liveColumns(reader, "kitwork_columns")
	if err != nil || len(columns) == 0 {
		return kinds, err
	}
	weight := "0"
	if _, held := columns["search_weight"]; held { // written before 31/10/2026 without it
		weight = "search_weight"
	}
	rows, err := reader.Query(`SELECT column_name, kind, precision, scale, `+weight+` FROM kitwork_columns WHERE table_name = ?`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		var row storedKind
		if err := rows.Scan(&column, &row.kind, &row.precision, &row.scale, &row.weight); err != nil {
			return nil, err
		}
		kinds[column] = row
	}
	return kinds, rows.Err()
}

// readLiveIndexes are the indexes a table has.
func readLiveIndexes(reader structureReader, table string) ([]liveIndex, error) {
	rows, err := reader.Query(`SELECT name, "unique", partial FROM pragma_index_list(?)`, table)
	if err != nil {
		return nil, err
	}
	var indexes []liveIndex
	for rows.Next() {
		var unique, partial int
		var name string
		if err := rows.Scan(&name, &unique, &partial); err != nil {
			rows.Close()
			return nil, err
		}
		indexes = append(indexes, liveIndex{name: name, unique: unique == 1, partial: partial == 1})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for position := range indexes {
		index := &indexes[position]
		columns, err := reader.Query(`SELECT name FROM pragma_index_info(?) ORDER BY seqno`, index.name)
		if err != nil {
			return nil, err
		}
		for columns.Next() {
			var name sql.NullString
			if err := columns.Scan(&name); err != nil {
				columns.Close()
				return nil, err
			}
			index.columns = append(index.columns, name.String)
		}
		columns.Close()
		if err := columns.Err(); err != nil {
			return nil, err
		}
		text, err := reader.Query(`SELECT sql FROM sqlite_schema WHERE type = 'index' AND name = ?`, index.name)
		if err != nil {
			return nil, err
		}
		if text.Next() {
			var statement sql.NullString
			if err := text.Scan(&statement); err != nil {
				text.Close()
				return nil, err
			}
			index.sql = statement.String
		}
		text.Close()
		if err := text.Err(); err != nil {
			return nil, err
		}
	}
	return indexes, nil
}

// databaseGapError is a database file that lacks, or differs from, what the app declares on it: every statement on
// it fails with it until the file is changed (kitwork migrate, the port) or the code is.
type databaseGapError struct {
	database string // the file as the app names it
	add      []string
	differ   []string
}

func (e *databaseGapError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "database %s chưa khớp bảng .shared/schema/ khai (database là gốc: engine không tự tạo bảng, thêm cột)", e.database)
	if len(e.add) > 0 {
		out.WriteString("; database chưa có: " + strings.Join(e.add, "; "))
		out.WriteString("; thêm bằng kitwork migrate (dừng server trước: một database một tiến trình ghi), hay bằng câu đổi schema qua cổng database (CREATE TABLE, ALTER TABLE … ADD COLUMN, CREATE UNIQUE INDEX)")
	}
	if len(e.differ) > 0 {
		out.WriteString("; khác database: " + strings.Join(e.differ, "; "))
		out.WriteString("; sửa table() theo database (kitwork migrate không đổi kiểu cột đã có)")
	}
	return out.String()
}

// saidNotes are the notes already said, so each is said once per process.
var saidNotes sync.Map

// sayNotes says, once, what kitwork migrate writes besides that the statements run without.
func sayNotes(database string, notes []string) {
	if len(notes) == 0 {
		return
	}
	line := fmt.Sprintf("[database] %s: câu vẫn chạy; kitwork migrate sẽ ghi thêm %s", database, strings.Join(notes, ", "))
	if _, said := saidNotes.LoadOrStore(line, true); !said {
		fmt.Println(line)
	}
}

// databaseLabel is a file the app declares tables on as the app names it: its path in the app's folder, or the host's
// name of connection("tên").
func (t *SchemaTable) databaseLabel() string {
	return databaseLabelOf(t.tenant, t.dbName, t.sqliteFile())
}

func databaseLabelOf(tenant *Tenant, name, path string) string {
	if strings.HasPrefix(name, catalogDatabasePrefix) {
		return fmt.Sprintf("connection(%q) (%s)", strings.TrimPrefix(name, catalogDatabasePrefix), filepath.Base(path))
	}
	if tenant != nil {
		if relative, err := filepath.Rel(tenant.resolveApp(), path); err == nil && !strings.HasPrefix(relative, "..") {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.Base(path)
}

// checkTables holds every declared table of the file against it (checkDatabase) and fails when the statements
// cannot run; the tables then are not marked done, so the next statement holds them again.
func (t *SchemaTable) checkTables(reader structureReader, tables map[string]map[string]*ColumnSpec, pending []string) error {
	gap, err := checkDatabase(reader, tables)
	if err != nil {
		return err
	}
	add, differ, notes := gap.lines()
	sayNotes(t.databaseLabel(), notes)
	if len(add) > 0 || len(differ) > 0 {
		return &databaseGapError{database: t.databaseLabel(), add: add, differ: differ}
	}
	for _, name := range pending {
		ensuredTables.Store(t.ensuredKey(name, tables[name]), true)
	}
	return nil
}

// structureExecutor runs what kitwork migrate changes: a transaction on the writer's connection.
type structureExecutor interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
}

// applyGap makes the file what its tables' declarations ask, adding only (kitwork migrate): a table the file has not,
// a column a table has not, a unique constraint, an index, the kinds of kitwork_columns. What differs from the file
// is left; it is the code's to follow.
func applyGap(executor structureExecutor, tables map[string]map[string]*ColumnSpec, gap databaseGap) error {
	if _, err := executor.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (
		id TEXT PRIMARY KEY, table_name TEXT, action TEXT, statement TEXT, applied_at TEXT)`, migrationsTable)); err != nil {
		return err
	}
	record := func(table, action, statement string) error {
		_, err := executor.Exec(fmt.Sprintf("INSERT INTO %q (id, table_name, action, statement, applied_at) VALUES (?, ?, ?, ?, ?)", migrationsTable),
			id.Entity(), table, action, statement, time.Now().UTC().Format(time.RFC3339))
		return err
	}
	for _, name := range gap.names {
		columns, table := tables[name], gap.tables[name]
		if err := validateSchema(columns); err != nil {
			return fmt.Errorf("bảng %s: %w", name, err)
		}
		if table.missing {
			statement := schemaDDL(name, columns)
			if _, err := executor.Exec(statement); err != nil {
				return fmt.Errorf("tạo bảng %s: %w", name, err)
			}
			if err := record(name, "create-table", statement); err != nil {
				return err
			}
		}
		for _, column := range table.absent {
			statement := fmt.Sprintf("ALTER TABLE %q ADD COLUMN %s", name, addColumnSQL(column, columns[column]))
			if _, err := executor.Exec(statement); err != nil {
				return fmt.Errorf("thêm cột %s.%s: %w", name, column, err)
			}
			if err := record(name, "add-column", statement); err != nil {
				return err
			}
		}
		// what the table now has: a table made here has its uniques inline, an added column has none
		indexes, err := readLiveIndexes(executor, name)
		if err != nil {
			return err
		}
		for _, unique := range declaredUniques(name, columns) {
			if uniqueHeld(indexes, unique.columns) {
				continue
			}
			statement := indexSQL(name, indexDef{name: unique.name, columns: unique.columns, unique: true})
			if _, err := executor.Exec(statement); err != nil {
				return fmt.Errorf("ràng buộc unique (%s) của bảng %s: %w", strings.Join(unique.columns, ", "), name, err)
			}
			if err := record(name, "add-unique", statement); err != nil {
				return err
			}
		}
		for _, index := range collectIndexes(name, columns) {
			if indexHeld(indexes, name, index) {
				continue
			}
			for _, current := range indexes {
				if current.name == index.name {
					if _, err := executor.Exec(fmt.Sprintf("DROP INDEX IF EXISTS %q", index.name)); err != nil {
						return fmt.Errorf("chỉ mục %s: %w", index.name, err)
					}
				}
			}
			statement := indexSQL(name, index)
			if _, err := executor.Exec(statement); err != nil {
				return fmt.Errorf("chỉ mục %s: %w", index.name, err)
			}
			if err := record(name, "add-index", statement); err != nil {
				return err
			}
		}
		kinds := make(map[string]*ColumnSpec, len(columns))
		for column, spec := range columns {
			if !table.differs[column] {
				kinds[column] = spec
			}
		}
		if err := writeColumnKinds(executor, name, kinds); err != nil {
			return fmt.Errorf("kitwork_columns của bảng %s: %w", name, err)
		}
	}
	return nil
}
