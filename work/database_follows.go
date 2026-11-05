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
	// replaced are the declared indexes the table has under their name with another definition: dropped first
	replaced map[string]bool
	// search is the table's search index as declared (each .searchable() column's weight) when the file's differs;
	// searched tells the file has one, dropped first
	search      map[string]int
	searched    bool
	searchOrder []string // its columns in declared order
	// candidates are, by declared column the table lacks, the table's columns no table() declares of the same type:
	// what it may have been renamed from (kitwork migrate asks); tables the same for a table the file lacks
	candidates map[string][]string
	tables     []string
	// rescalable are the number columns whose scale differs from the file's (decimal, integer): a rewrite kitwork
	// migrate asks for; rescaled those it was told to rewrite
	rescalable []rescaleColumn
	rescaled   []rescaleColumn
}

// rescaleColumn is a number column whose stored values a new scale rewrites (gateway.RescaleStatements).
type rescaleColumn struct {
	column              string
	from, to, precision int
	decimal             bool   // declared a decimal (numeric(p, s)), else an integer (bigint)
	file, declared      string // the types, as said
}

// declaredSearch is a table's search index as declared: each .searchable() column's weight; none when no column is.
func declaredSearch(columns map[string]*ColumnSpec) map[string]int {
	search := map[string]int{}
	for name, spec := range columns {
		if weight := declaredWeight(spec); weight > 0 {
			search[name] = weight
		}
	}
	return search
}

// searchColumns lists a search index's columns in declared order.
func searchColumns(columns map[string]*ColumnSpec, search map[string]int) []string {
	var names []string
	for _, name := range orderedColumns(columns) {
		if search[name] > 0 {
			names = append(names, name)
		}
	}
	return names
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
		if table.search != nil {
			notes = append(notes, fmt.Sprintf("chỉ mục tìm kiếm %s của bảng %s (%s)", searchIndexName(name), name,
				strings.Join(table.searchOrder, ", ")))
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
	state, err := readDatabaseState(reader, tables, false)
	if err != nil {
		return databaseGap{}, err
	}
	return compareDatabase(state, tables, migrationChoices{}), nil
}

// tableState is what a file holds of one table, read once: its columns and their types, its rows of kitwork_columns,
// its indexes; for kitwork migrate's questions, how many rows it has and how many values each column holds.
type tableState struct {
	types   map[string]string // column → type, upper case
	order   []string
	stored  map[string]storedKind
	indexes []liveIndex
	rows    int
	values  map[string]int
}

// databaseState is what a file holds of the tables an app declares; read for kitwork migrate (every), its other tables
// too, which a declared table may have been renamed from, and their counts.
type databaseState struct {
	tables map[string]*tableState
}

// migrationChoices are what kitwork migrate was told beyond what the declaration says: a table or a column renamed, a
// column dropped for a new one, a number column's values rewritten to a new scale (KITWORK_DATABASE.md mục 6).
type migrationChoices struct {
	tables  map[string]string            // declared table → the file's table renamed to it
	columns map[string]map[string]string // declared table → declared column → the file's column renamed to it
	drops   map[string][]string          // declared table → the file's columns dropped
	rescale map[string]bool              // "table.column" rewritten to the declared scale
	decided map[string]bool              // a question answered without a change: "table", "table.column", "~table.column"
}

// hold checks the choices against the file as it is when they apply: what is renamed or dropped is still there.
func (choices migrationChoices) hold(state *databaseState) error {
	for name, file := range choices.tables {
		if state.tables[file] == nil {
			return fmt.Errorf("bảng %s (đổi tên thành %s) không còn trong database: lập kế hoạch lại", file, name)
		}
	}
	for name, renames := range choices.columns {
		source := name
		if file, renamed := choices.tables[name]; renamed {
			source = file
		}
		table := state.tables[source]
		for column, file := range renames {
			if table == nil || table.types[file] == "" && !containsName(table.order, file) {
				return fmt.Errorf("cột %s.%s (đổi tên thành %s) không còn trong database: lập kế hoạch lại", name, file, column)
			}
		}
		for _, file := range choices.drops[name] {
			if table == nil || !containsName(table.order, file) {
				return fmt.Errorf("cột %s.%s (bỏ) không còn trong database: lập kế hoạch lại", name, file)
			}
		}
	}
	return nil
}

func containsName(names []string, name string) bool {
	for _, each := range names {
		if each == name {
			return true
		}
	}
	return false
}

func readDatabaseState(reader structureReader, declared map[string]map[string]*ColumnSpec, every bool) (*databaseState, error) {
	state := &databaseState{tables: map[string]*tableState{}}
	if reader == nil {
		return state, nil
	}
	names := sortedTableNames(declared)
	if every {
		rows, err := reader.Query(`SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name`)
		if err != nil {
			return nil, err
		}
		others, err := scanTexts(rows)
		if err != nil {
			return nil, err
		}
		for _, name := range others {
			lower := strings.ToLower(name)
			if declared[name] == nil && !strings.HasPrefix(lower, "kitwork_") && !strings.HasPrefix(lower, "_kitwork_") && !strings.HasPrefix(lower, "sqlite_") {
				names = append(names, name)
			}
		}
	}
	for _, name := range names {
		table, err := readTableState(reader, name, every)
		if err != nil {
			return nil, fmt.Errorf("bảng %s: %w", name, err)
		}
		if table != nil {
			state.tables[name] = table
		}
	}
	return state, nil
}

func scanTexts(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return nil, err
		}
		out = append(out, text)
	}
	return out, rows.Err()
}

// readTableState reads one table; nil when the file has none of that name.
func readTableState(reader structureReader, table string, count bool) (*tableState, error) {
	rows, err := reader.Query(`SELECT name, type FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	state := &tableState{types: map[string]string{}}
	for rows.Next() {
		var name, declared string
		if err := rows.Scan(&name, &declared); err != nil {
			rows.Close()
			return nil, err
		}
		state.types[name] = strings.ToUpper(strings.TrimSpace(declared))
		state.order = append(state.order, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(state.order) == 0 {
		return nil, nil
	}
	if state.stored, err = readStoredKinds(reader, table); err != nil {
		return nil, err
	}
	if state.indexes, err = readLiveIndexes(reader, table); err != nil {
		return nil, err
	}
	if count {
		counts := []string{"count(*)"}
		for _, column := range state.order {
			counts = append(counts, fmt.Sprintf("count(%q)", column))
		}
		rows, err := reader.Query(fmt.Sprintf("SELECT %s FROM %q", strings.Join(counts, ", "), table))
		if err != nil {
			return nil, err
		}
		found := make([]int, len(counts))
		targets := make([]any, len(counts))
		for index := range found {
			targets[index] = &found[index]
		}
		if rows.Next() {
			err = rows.Scan(targets...)
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		state.rows, state.values = found[0], map[string]int{}
		for index, column := range state.order {
			state.values[column] = found[index+1]
		}
	}
	return state, nil
}

// after is the table as it reads once columns are renamed (declared name → the file's) and others dropped.
func (state *tableState) after(renames map[string]string, drops []string) *tableState {
	if len(renames) == 0 && len(drops) == 0 {
		return state
	}
	to := map[string]string{} // the file's name → its name after
	for declared, file := range renames {
		to[file] = declared
	}
	dropped := map[string]bool{}
	for _, column := range drops {
		dropped[column] = true
	}
	name := func(column string) string {
		if renamed, ok := to[column]; ok {
			return renamed
		}
		return column
	}
	out := &tableState{types: map[string]string{}, stored: map[string]storedKind{}, rows: state.rows, values: map[string]int{}}
	for _, column := range state.order {
		if dropped[column] {
			continue
		}
		out.order = append(out.order, name(column))
		out.types[name(column)] = state.types[column]
		out.values[name(column)] = state.values[column]
	}
	for column, row := range state.stored {
		if !dropped[column] {
			out.stored[name(column)] = row
		}
	}
	for _, index := range state.indexes {
		kept, columns := true, make([]string, len(index.columns))
		for position, column := range index.columns {
			kept = kept && !dropped[column]
			columns[position] = name(column)
		}
		if kept {
			index.columns = columns
			index.sql = "" // its text names the columns as they were: held by its columns
			out.indexes = append(out.indexes, index)
		}
	}
	return out
}

// compareDatabase holds the declared tables against the file's state as the choices leave it.
func compareDatabase(state *databaseState, tables map[string]map[string]*ColumnSpec, choices migrationChoices) databaseGap {
	gap := databaseGap{tables: map[string]*tableGap{}, names: sortedTableNames(tables)}
	used := map[string]bool{}
	for _, file := range choices.tables {
		used[file] = true
	}
	for _, name := range gap.names {
		source := name
		if file, renamed := choices.tables[name]; renamed {
			source = file
		}
		table := state.tables[source]
		if table != nil {
			table = table.after(choices.columns[name], choices.drops[name])
		}
		compared := compareTable(name, table, tables[name], choices)
		if compared.missing {
			for _, other := range sortedStateNames(state) {
				if tables[other] == nil && !used[other] && holdsColumns(state.tables[other], tables[name]) {
					compared.tables = append(compared.tables, other)
				}
			}
		}
		gap.tables[name] = compared
	}
	return gap
}

func sortedStateNames(state *databaseState) []string {
	names := make([]string, 0, len(state.tables))
	for name := range state.tables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// holdsColumns reports a table of the file with every declared column, of its storage: what a table may have been
// renamed from.
func holdsColumns(table *tableState, columns map[string]*ColumnSpec) bool {
	for name, spec := range columns {
		if current, has := table.types[name]; !has || !sameType(current, sqliteStorage(spec)) {
			return false
		}
	}
	return true
}

// compareTable holds one table's declaration against the file's table (nil: the file has none).
func compareTable(table string, state *tableState, columns map[string]*ColumnSpec, choices migrationChoices) *tableGap {
	gap := &tableGap{differs: map[string]bool{}, replaced: map[string]bool{}, candidates: map[string][]string{}}
	ordered := orderedColumns(columns)
	if state == nil {
		gap.missing = true
		gap.add = append(gap.add, fmt.Sprintf("bảng %s (%s)", table, declaredColumns(columns, ordered)))
		gap.indexes = collectIndexes(table, columns)
		gap.kinds = ordered
		if search := declaredSearch(columns); len(search) > 0 {
			gap.search, gap.searchOrder = search, searchColumns(columns, search)
		}
		return gap
	}
	live, stored := state.types, state.stored
	for _, name := range ordered {
		spec := columns[name]
		current, has := live[name]
		if !has {
			gap.absent = append(gap.absent, name)
			gap.kinds = append(gap.kinds, name)
			gap.candidates[name] = renameCandidates(state, columns, spec)
			continue
		}
		if want := sqliteStorage(spec); !sameType(current, want) {
			gap.differs[name] = true
			if spec.kind == "decimal" && sameType(current, "TEXT") {
				// written before 29/10/2026, when SQLite kept a decimal as its text (db.sqlite_decimal.go)
				gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s là decimal lưu dạng chữ (cách trước 29/10/2026), nay SQLite lưu decimal là số nguyên nhân 10^số_lẻ", table, name))
				continue
			}
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database lưu %s, code khai %s (lưu %s) (đổi cách lưu cần dựng lại bảng, chưa có)",
				table, name, current, declaredType(spec), want))
			continue
		}
		row, recorded := stored[name]
		from, fromNumber := storedScale(row, recorded)
		to, toNumber := declaredScale(spec)
		switch {
		case fromNumber && toNumber && from != to:
			// a number whose scale differs: its values rewritten, when kitwork migrate was told so
			rescale := rescaleColumn{column: name, from: from, to: to, precision: declaredPrecision(spec), decimal: spec.kind == "decimal",
				file: storedTypeOf(row, recorded), declared: declaredType(spec)}
			if choices.rescale[table+"."+name] {
				gap.rescaled = append(gap.rescaled, rescale)
				gap.kinds = append(gap.kinds, name)
				continue
			}
			gap.rescalable = append(gap.rescalable, rescale)
			gap.differs[name] = true
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database ghi %s (kitwork_columns), code khai %s", table, name, rescale.file, rescale.declared))
		case spec.kind == "decimal" && recorded && row.kind != "decimal" && row.kind != "integer",
			spec.kind != "decimal" && recorded && row.kind == "decimal":
			gap.differs[name] = true
			gap.differ = append(gap.differ, fmt.Sprintf("cột %s.%s: database ghi %s (kitwork_columns), code khai %s", table, name, storedType(row), declaredType(spec)))
		case !recorded || row.kind != spec.kind || spec.kind == "decimal" && row.precision != spec.precision:
			gap.kinds = append(gap.kinds, name)
		}
	}
	// the search index is the table's, one: declared by a .searchable() column, it is the file's to hold as declared
	held := map[string]int{}
	for column, row := range stored {
		if row.weight > 0 {
			held[column] = row.weight
		}
	}
	if search := declaredSearch(columns); len(search) > 0 && !sameWeights(search, held) {
		gap.search, gap.searched, gap.searchOrder = search, len(held) > 0, searchColumns(columns, search)
	}
	if len(gap.absent) > 0 {
		line := fmt.Sprintf("cột %s của bảng %s", declaredColumns(columns, gap.absent), table)
		var hints []string
		for _, name := range gap.absent {
			if candidates := gap.candidates[name]; len(candidates) > 0 {
				hints = append(hints, fmt.Sprintf("%s từ %s?", name, strings.Join(candidates, " hay ")))
			}
		}
		if len(hints) > 0 {
			line += " (database có cột cùng kiểu code không khai, có phải đổi tên: " + strings.Join(hints, ", ") + " kitwork migrate hỏi)"
		}
		gap.add = append(gap.add, line)
	}
	indexes := state.indexes
	for _, unique := range declaredUniques(table, columns) {
		if !uniqueHeld(indexes, unique.columns) {
			gap.uniques = append(gap.uniques, unique)
			gap.add = append(gap.add, fmt.Sprintf("ràng buộc unique (%s) của bảng %s", strings.Join(unique.columns, ", "), table))
		}
	}
	for _, index := range collectIndexes(table, columns) {
		if !indexHeld(indexes, table, index) {
			gap.indexes = append(gap.indexes, index)
			for _, current := range indexes {
				if current.name == index.name {
					gap.replaced[index.name] = true
				}
			}
		}
	}
	return gap
}

// renameCandidates are the table's columns no declaration names, of the declared column's storage and number kind:
// what it may have been renamed from.
func renameCandidates(state *tableState, columns map[string]*ColumnSpec, spec *ColumnSpec) []string {
	var candidates []string
	for _, column := range state.order {
		if columns[column] != nil || !sameType(state.types[column], sqliteStorage(spec)) {
			continue
		}
		row, recorded := state.stored[column]
		fileDecimal := recorded && row.kind == "decimal"
		if spec.kind == "decimal" && (!fileDecimal || row.scale != spec.scale) || spec.kind != "decimal" && fileDecimal {
			continue
		}
		candidates = append(candidates, column)
	}
	return candidates
}

// storedScale is the scale a number column's stored values have: a decimal's, 0 for an integer or a column of no row;
// false for any other kind.
func storedScale(row storedKind, recorded bool) (int, bool) {
	switch {
	case !recorded || row.kind == "integer":
		return 0, true
	case row.kind == "decimal":
		return row.scale, true
	}
	return 0, false
}

// declaredScale is the scale a declared number column's values are stored at: a decimal's, 0 for an integer.
func declaredScale(spec *ColumnSpec) (int, bool) {
	switch spec.kind {
	case "decimal":
		return spec.scale, true
	case "integer":
		return 0, true
	}
	return 0, false
}

func declaredPrecision(spec *ColumnSpec) int {
	if spec.kind == "decimal" {
		return spec.precision
	}
	return 18
}

func storedTypeOf(row storedKind, recorded bool) string {
	if !recorded {
		return "số nguyên (không ghi kiểu)"
	}
	return storedType(row)
}

// sameWeights reports two search indexes with the same columns and weights.
func sameWeights(a, b map[string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for column, weight := range a {
		if b[column] != weight {
			return false
		}
	}
	return true
}

// searchIndexName is the name of a table's search index (kitdb/gateway's): one per table.
func searchIndexName(table string) string { return table + "_search" }

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
	database   string // the file as the app names it
	add        []string
	differ     []string
	statements []string // what kitwork migrate runs, as PostgreSQL writes it: what one sends through the port
}

func (e *databaseGapError) Error() string {
	var out strings.Builder
	fmt.Fprintf(&out, "database %s chưa khớp bảng .shared/schema/ khai (database là gốc: engine không tự tạo bảng, thêm cột)", e.database)
	if len(e.add) > 0 {
		out.WriteString("; database chưa có: " + strings.Join(e.add, "; "))
		out.WriteString("; thêm bằng kitwork migrate (dừng server trước: một database một tiến trình ghi), hay bằng câu đổi schema qua cổng database")
		if len(e.statements) > 0 {
			out.WriteString(": " + strings.Join(e.statements, " "))
		}
	}
	if len(e.differ) > 0 {
		out.WriteString("; khác database: " + strings.Join(e.differ, "; "))
		out.WriteString("; sửa table() theo database, hay khi chỉ số lẻ của một cột số khác, để kitwork migrate viết lại giá trị (nó hỏi)")
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
		var statements []string
		for _, statement := range gapStatements(tables, gap, schemaOfDatabase(t.dbName)) {
			if statement.postgres != "" {
				statements = append(statements, strings.Join(strings.Fields(statement.postgres), " ")+";")
			}
		}
		return &databaseGapError{database: t.databaseLabel(), add: add, differ: differ, statements: statements}
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
// a column a table has not, a unique constraint, an index, each in PostgreSQL's words through the port's translator
// where they say it (gapStatements), then the kinds of kitwork_columns. What differs from the file is left; it is the
// code's to follow.
func applyGap(executor structureExecutor, tables map[string]map[string]*ColumnSpec, gap databaseGap, choices migrationChoices) error {
	if _, err := executor.Exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS %q (
		id TEXT PRIMARY KEY, table_name TEXT, action TEXT, statement TEXT, applied_at TEXT)`, migrationsTable)); err != nil {
		return err
	}
	for _, name := range gap.names {
		if err := validateSchema(tables[name]); err != nil {
			return fmt.Errorf("bảng %s: %w", name, err)
		}
	}
	for _, statement := range append(choiceStatements("", choices, gap), gapStatements(tables, gap, "")...) {
		if statement.guard != "" {
			rows, err := executor.Query(statement.guard)
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
				return fmt.Errorf("%s: %d giá trị vượt số chữ số của cột ở số lẻ mới", strings.TrimSuffix(statement.shown(), ";"), over)
			}
		}
		for _, text := range statement.sqlite {
			if _, err := executor.Exec(text); err != nil {
				return fmt.Errorf("%s: %w", strings.TrimSuffix(statement.shown(), ";"), err)
			}
		}
		if _, err := executor.Exec(fmt.Sprintf("INSERT INTO %q (id, table_name, action, statement, applied_at) VALUES (?, ?, ?, ?, ?)", migrationsTable),
			id.Entity(), statement.table, statement.action, statement.shown(), time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	for _, name := range sortedKeys(choices.tables) {
		// a table renamed takes its rows of kitwork_columns along, as the port's RENAME TO does
		if _, err := executor.Exec(`UPDATE kitwork_columns SET table_name = ? WHERE table_name = ?`, name, choices.tables[name]); err != nil &&
			!strings.Contains(err.Error(), "no such table") {
			return err
		}
	}
	for _, name := range gap.names {
		columns, table := tables[name], gap.tables[name]
		if table.search != nil {
			// the index as declared: a column of the file's own, not declared, leaves it too (kitwork_columns as
			// writeColumnKinds makes it, with search_weight)
			if _, err := executor.Exec(sqliteColumnsSchema); err != nil {
				return err
			}
			if _, err := executor.Exec(sqliteColumnsSearch); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
				return err
			}
			if _, err := executor.Exec(`UPDATE kitwork_columns SET search_weight = 0 WHERE table_name = ?`, name); err != nil {
				return fmt.Errorf("chỉ mục tìm kiếm của bảng %s: %w", name, err)
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
