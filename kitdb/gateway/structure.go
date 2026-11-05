package gateway

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
)

// The structure file (KITWORK_KITDB.md mục 3; KITWORK_DATABASE.md "Database là gốc", slice 3, 05/11/2026): beside
// each schema file of a database, <file>.sql holds the file's structure, its tables and indexes, as PostgreSQL writes
// them, the way the port's schema statements take them (schema.go): numeric(p, s) for a decimal, boolean, json,
// bigserial for the rowid's key. KitDB writes it again whenever the file's schema or kitwork_columns changes, so it
// always says what the file is; a machine whose folder has the .sql and no file makes the file from it. It holds no
// row.
//
// What the port's statements cannot say is written as SQLite's own, after a line "-- SQLite: why", and run as it is:
// a table that is not STRICT, a CHECK or a partial index that reads a decimal (the stored integer is not the value),
// a column of a type the port does not know, a view. Such a table's kinds follow it as rows of kitwork_columns, after
// a line "-- KitDB:"; so do a translated table's kinds that its PostgreSQL types do not say (a timestamp kept in
// milliseconds, a kitid, a .searchable() weight). The engine never writes kitwork_columns because the app declares a
// column (KITWORK_DATABASE.md "Database là gốc", slice 4), so the file says every kind the database holds.

// StructureHeader begins every structure file.
const StructureHeader = `-- Cấu trúc của file database bên cạnh (bảng và chỉ mục, không có dòng dữ liệu), viết theo cách PostgreSQL viết.
-- KitDB viết lại file này mỗi khi schema của file đổi; đừng sửa tay. Máy nào có file này mà chưa có file database
-- thì KitDB tạo database từ nó. Câu sau dòng "-- SQLite:" là câu của SQLite, chạy nguyên văn.
`

// structureQuerier reads a file: a *sql.Conn of its writer, a *sql.Tx.
type structureQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// bookkeepingTable is a table of the engine's or SQLite's own, which the structure leaves out: they make themselves.
func bookkeepingTable(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasPrefix(lower, "kitwork_") || strings.HasPrefix(lower, "_kitwork_") || strings.HasPrefix(lower, "sqlite_")
}

// Structure is the structure file of the file source reads (its main schema): "" when the file holds no table of its
// own.
func Structure(ctx context.Context, source structureQuerier) (string, error) {
	type object struct{ kind, name, table, text string }
	rows, err := source.QueryContext(ctx, `SELECT type, name, tbl_name, sql FROM sqlite_schema WHERE sql IS NOT NULL ORDER BY tbl_name, type DESC, name`)
	if err != nil {
		return "", err
	}
	var objects []object
	for rows.Next() {
		var current object
		if err := rows.Scan(&current.kind, &current.name, &current.table, &current.text); err != nil {
			rows.Close()
			return "", err
		}
		if !bookkeepingTable(current.table) {
			objects = append(objects, current)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	kinds, err := structureKinds(ctx, source)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	tables := 0
	for _, current := range objects {
		var text, native string
		switch current.kind {
		case "table":
			tables++
			text, native = renderTable(current.text, kinds[strings.ToLower(current.name)])
		case "index":
			text, native = renderIndex(current.text, kinds[strings.ToLower(current.table)])
		case "view":
			native = "a view"
		default:
			native = "a " + current.kind
		}
		out.WriteString("\n")
		if native != "" {
			out.WriteString("-- SQLite: " + native + "\n")
			text = strings.TrimSpace(current.text)
		}
		out.WriteString(text + ";\n")
		if current.kind != "table" {
			continue
		}
		tableKinds := kinds[strings.ToLower(current.name)]
		if native != "" && len(tableKinds) > 0 {
			out.WriteString("-- KitDB: the kinds of its columns\n" + kindRows(current.name, tableKinds) + ";\n")
			continue
		}
		unsaid, err := unsaidKinds(ctx, source, current.name, tableKinds)
		if err != nil {
			return "", err
		}
		if len(unsaid) > 0 {
			out.WriteString("-- KitDB: the kinds its PostgreSQL types do not say\n" + kindRows(current.name, unsaid) + ";\n")
		}
	}
	if tables == 0 {
		return "", nil
	}
	body := out.String()
	if strings.Contains(body, " kitid") {
		body = "\n" + kitidDomain + ";\n" + body
	}
	return StructureHeader + body, nil
}

// kindRows is the INSERT of table's kinds into kitwork_columns, by column name.
func kindRows(table string, kinds map[string]columnKind) string {
	names := make([]string, 0, len(kinds))
	for name := range kinds {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, len(names))
	for index, name := range names {
		kind := kinds[name]
		values[index] = fmt.Sprintf("(%s, %s, %s, %d, %d, %d)", quoteLiteral(table), quoteLiteral(kind.column), quoteLiteral(kind.kind),
			kind.precision, kind.scale, kind.weight)
	}
	return "INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale, search_weight) VALUES\n  " + strings.Join(values, ",\n  ")
}

// unsaidKinds are the kinds of a translated table that the PostgreSQL types the structure writes do not say: the
// port makes a bigint's kind integer, a double precision's float, a text's text, a bytea's blob, and numeric(p, s),
// boolean and json their own; any other kind, or a .searchable() weight, is written as a row.
func unsaidKinds(ctx context.Context, source structureQuerier, table string, kinds map[string]columnKind) (map[string]columnKind, error) {
	if len(kinds) == 0 {
		return nil, nil
	}
	rows, err := source.QueryContext(ctx, `SELECT name, type FROM pragma_table_info(?)`, table)
	if err != nil {
		return nil, err
	}
	storage := map[string]string{}
	for rows.Next() {
		var name, declared string
		if err := rows.Scan(&name, &declared); err != nil {
			rows.Close()
			return nil, err
		}
		storage[strings.ToLower(name)] = strings.ToUpper(strings.TrimSpace(declared))
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	implied := map[string]string{"INTEGER": "integer", "INT": "integer", "REAL": "float", "TEXT": "text", "BLOB": "blob"}
	unsaid := map[string]columnKind{}
	for column, kind := range kinds {
		said := kind.kind == "decimal" || kind.kind == "bool" || kind.kind == "json" || implied[storage[column]] == kind.kind ||
			storage[column] == "TEXT" && (kind.kind == "kitid" || kind.kind == "date" || kind.kind == "datetime")
		if kind.weight == 0 && said {
			continue // numeric(p, s), boolean, json, kitid, date, timestamptz, or the kind its storage gives
		}
		unsaid[column] = kind
	}
	return unsaid, nil
}

// structureKinds reads kitwork_columns, when the file has it: table → column → kind, lower case.
func structureKinds(ctx context.Context, source structureQuerier) (map[string]map[string]columnKind, error) {
	kinds := map[string]map[string]columnKind{}
	rows, err := source.QueryContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_columns'`)
	if err != nil {
		return nil, err
	}
	held := 0
	if rows.Next() {
		err = rows.Scan(&held)
	}
	rows.Close()
	if err != nil || held == 0 {
		return kinds, err
	}
	// search_weight came 31/10/2026; a file of before has none
	weight := "0"
	rows, err = source.QueryContext(ctx, `SELECT count(*) FROM pragma_table_info('kitwork_columns') WHERE name = 'search_weight'`)
	if err != nil {
		return nil, err
	}
	held = 0
	if rows.Next() {
		err = rows.Scan(&held)
	}
	rows.Close()
	if err != nil {
		return nil, err
	}
	if held > 0 {
		weight = "search_weight"
	}
	rows, err = source.QueryContext(ctx, `SELECT table_name, column_name, kind, precision, scale, `+weight+` FROM kitwork_columns`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var table, column string
		var kind columnKind
		if err := rows.Scan(&table, &column, &kind.kind, &kind.precision, &kind.scale, &kind.weight); err != nil {
			return nil, err
		}
		if kinds[strings.ToLower(table)] == nil {
			kinds[strings.ToLower(table)] = map[string]columnKind{}
		}
		kind.column = column
		kinds[strings.ToLower(table)][strings.ToLower(column)] = kind
	}
	return kinds, rows.Err()
}

// plainName is a name PostgreSQL and SQLite both read unquoted.
var plainName = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// reservedNames are key words a column or a table may be named by only when quoted.
var reservedNames = map[string]bool{}

func init() {
	for _, word := range strings.Fields(`all and any as asc between by case cast check collate column constraint create cross
		current_date current_time current_timestamp default deferrable delete desc distinct do drop else end except
		exists false fetch for foreign from full grant group having in index inner insert intersect into is join key
		left like limit natural not null offset on or order outer primary references right select set table then to
		true union unique update user using values when where window with`) {
		reservedNames[word] = true
	}
}

// writtenName is name as the structure writes it: bare when it can be, quoted when it must.
func writtenName(name string) string {
	if plainName.MatchString(name) && !reservedNames[name] {
		return name
	}
	return quoteIdentifier(name)
}

// columnConstraintWords end a column's type in SQLite's CREATE TABLE.
var columnConstraintWords = map[string]bool{"CONSTRAINT": true, "PRIMARY": true, "NOT": true, "NULL": true, "UNIQUE": true,
	"CHECK": true, "DEFAULT": true, "COLLATE": true, "REFERENCES": true, "GENERATED": true, "AS": true}

// renderTable writes a CREATE TABLE of SQLite as PostgreSQL's; native says why it cannot be ("" when it can).
func renderTable(text string, kinds map[string]columnKind) (string, string) {
	tokens, err := scan(text)
	if err != nil {
		return "", "its CREATE TABLE does not read"
	}
	c := &schemaCursor{tokens: tokens}
	if !c.keyword("CREATE", "TABLE") {
		return "", "its CREATE TABLE does not read"
	}
	c.keyword("IF", "NOT", "EXISTS")
	_, table, err := c.qualified()
	if err != nil {
		return "", "its name does not read"
	}
	inside, err := c.group()
	if err != nil {
		return "", "its columns do not read"
	}
	strict := false
	for _, option := range c.tokens[c.position:] {
		switch option.word() {
		case "STRICT":
			strict = true
		case "WITHOUT":
			return "", "a table WITHOUT ROWID"
		}
	}
	if !strict {
		return "", "a table that is not STRICT keeps the values of any type its columns were given"
	}
	decimals := map[string]bool{}
	for column, kind := range kinds {
		if kind.kind == "decimal" && kind.scale > 0 {
			decimals[column] = true
		}
	}
	readsDecimal := func(part []token) bool {
		for _, current := range part {
			if (current.kind == tokenWord || current.kind == tokenQuoted) && decimals[strings.ToLower(identifierText(current))] {
				return true
			}
		}
		return false
	}
	var definitions []string
	for _, part := range splitTop(inside) {
		if len(part) == 0 {
			return "", "an empty column"
		}
		switch part[0].word() {
		case "PRIMARY", "UNIQUE", "FOREIGN", "CONSTRAINT":
			definitions = append(definitions, renderTokens(part))
			continue
		case "CHECK":
			if readsDecimal(part) {
				return "", "a CHECK reads a decimal column, whose stored integer is not its value"
			}
			definitions = append(definitions, renderTokens(part))
			continue
		}
		name := identifierText(part[0])
		position := 1
		var declared []string
		for position < len(part) && !columnConstraintWords[part[position].word()] {
			declared = append(declared, strings.ToUpper(part[position].text))
			position++
		}
		rest := part[position:]
		kind, found := kinds[strings.ToLower(name)]
		primary := false
		for index, current := range rest {
			switch current.word() {
			case "GENERATED", "AS":
				return "", "a generated column"
			case "CHECK":
				if kind.kind == "decimal" && kind.scale > 0 || readsDecimal(rest[index:]) {
					return "", "a CHECK reads a decimal column, whose stored integer is not its value"
				}
			case "PRIMARY":
				primary = true
			}
		}
		storage := strings.Join(declared, " ")
		var written string
		switch {
		case found && kind.kind == "decimal" && storage == "INTEGER":
			written = fmt.Sprintf("numeric(%d, %d)", kind.precision, kind.scale)
		case found && kind.kind == "bool" && storage == "INTEGER":
			written = "boolean"
		case found && kind.kind == "json" && storage == "TEXT":
			written = "json"
		case found && kind.kind == "kitid" && storage == "TEXT":
			written = "kitid"
		case found && kind.kind == "date" && storage == "TEXT":
			written = "date"
		case found && kind.kind == "datetime" && storage == "TEXT":
			written = "timestamptz"
		case storage == "INTEGER" && primary && len(rest) > 1 && rest[0].word() == "PRIMARY" && rest[1].word() == "KEY":
			written = "bigserial" // the rowid's own key
		case storage == "INTEGER" || storage == "INT":
			written = "bigint"
		case storage == "REAL":
			written = "double precision"
		case storage == "TEXT":
			written = "text"
		case storage == "BLOB":
			written = "bytea"
		default:
			return "", fmt.Sprintf("column %s is %s, which the port's statements do not write", name, strings.ToLower(storage))
		}
		constraints := renderTokens(storedDefaults(rest, kind, found))
		definition := writtenName(name) + " " + written
		if constraints != "" {
			definition += " " + constraints
		}
		definitions = append(definitions, definition)
	}
	return "CREATE TABLE " + writtenName(table) + " (\n  " + strings.Join(definitions, ",\n  ") + "\n)", ""
}

// storedDefaults writes a decimal's or a boolean's DEFAULT as its value: the file holds 150 for 1.50 and 0 for false.
func storedDefaults(rest []token, kind columnKind, found bool) []token {
	if !found || (kind.kind != "decimal" && kind.kind != "bool") {
		return rest
	}
	out := make([]token, 0, len(rest))
	for index := 0; index < len(rest); index++ {
		out = append(out, rest[index])
		if rest[index].word() != "DEFAULT" || index+1 >= len(rest) {
			continue
		}
		end := index + 2
		if (rest[index+1].text == "-" || rest[index+1].text == "+") && end < len(rest) {
			end++
		}
		literal, ok := literalText(rest[index+1 : end])
		if !ok {
			continue
		}
		value, err := strconv.ParseInt(strings.TrimSpace(literal), 10, 64)
		if err != nil {
			continue
		}
		var written string
		if kind.kind == "bool" {
			written = strconv.FormatBool(value != 0)
		} else {
			written = formatScaled(value, kind.scale)
		}
		out = append(out, token{kind: tokenWord, text: written})
		index = end - 1
	}
	return out
}

// renderIndex writes a CREATE INDEX of SQLite as PostgreSQL's (the same words, the table unqualified); native when its
// WHERE reads a decimal column.
func renderIndex(text string, kinds map[string]columnKind) (string, string) {
	tokens, err := scan(text)
	if err != nil {
		return "", "its CREATE INDEX does not read"
	}
	where := false
	for _, current := range tokens {
		if current.word() == "WHERE" {
			where = true
		}
		if where && (current.kind == tokenWord || current.kind == tokenQuoted) {
			if kind, found := kinds[strings.ToLower(identifierText(current))]; found && kind.kind == "decimal" && kind.scale > 0 {
				return "", "its WHERE reads a decimal column, whose stored integer is not its value"
			}
		}
	}
	c := &schemaCursor{tokens: tokens}
	unique := c.keyword("CREATE", "UNIQUE", "INDEX")
	if !unique && !c.keyword("CREATE", "INDEX") {
		return "", "its CREATE INDEX does not read"
	}
	text = "CREATE INDEX "
	if unique {
		text = "CREATE UNIQUE INDEX "
	}
	if c.keyword("IF", "NOT", "EXISTS") {
		text += "IF NOT EXISTS "
	}
	name, err := c.name()
	if err != nil || !c.keyword("ON") {
		return "", "its CREATE INDEX does not read"
	}
	table, err := c.name()
	if err != nil {
		return "", "its CREATE INDEX does not read"
	}
	return text + writtenName(name) + " ON " + writtenName(table) + " " + renderTokens(c.tokens[c.position:]), ""
}

// StructureFileWriter is what makes a file from its structure: the file's writer (kitdb/sqlite.Database).
type StructureFileWriter interface {
	Write(ctx context.Context, by string, request func(*kitdbsqlite.Transaction) error) (uint64, error)
	Migrate(ctx context.Context, by string, statements ...string) (uint64, error)
}

// LoadStructure makes the tables and indexes of a structure file in the file writer writes (a new, empty one), and
// the kinds of their columns: the statements in one commit, the kinds in the next.
func LoadStructure(ctx context.Context, writer StructureFileWriter, by, text string) error {
	statements, kinds, rows, err := structureStatements(text)
	if err != nil {
		return err
	}
	if len(statements) == 0 {
		return nil
	}
	if _, err := writer.Migrate(ctx, by, append([]string{columnsTable}, statements...)...); err != nil {
		return err
	}
	if len(kinds) == 0 && len(rows) == 0 {
		return nil
	}
	_, err = writer.Write(ctx, by, func(transaction *kitdbsqlite.Transaction) error {
		// the rows the file writes first: a kind its PostgreSQL type does not say stays over the one it does
		for _, row := range rows {
			if _, err := transaction.Exec(row); err != nil {
				return err
			}
		}
		for _, change := range kinds {
			if _, err := transaction.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (table_name, column_name) DO NOTHING`, change.table, change.column, change.kind.kind, change.kind.precision, change.kind.scale); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

// markedBy reports a line of gap that begins with marker (the header names the markers inside its lines).
func markedBy(gap, marker string) bool {
	for _, line := range strings.Split(gap, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), marker) {
			return true
		}
	}
	return false
}

// structureStatements reads a structure file: each schema statement as the writer runs it (PostgreSQL's translated,
// one after "-- SQLite:" as it is), the kinds of the columns its tables make, and the rows of kitwork_columns written
// after "-- KitDB:".
func structureStatements(text string) ([]string, []kindChange, []string, error) {
	tokens, err := scan(text)
	if err != nil {
		return nil, nil, nil, err
	}
	var statements, rows []string
	var kinds []kindChange
	previousEnd, start := 0, 0
	for index := 0; index <= len(tokens); index++ {
		if index < len(tokens) && tokens[index].kind != tokenSemicolon {
			continue
		}
		part := tokens[start:index]
		start = index + 1
		if len(part) == 0 {
			continue
		}
		gap := text[previousEnd:part[0].start]
		previousEnd = part[len(part)-1].end
		if index < len(tokens) {
			previousEnd = tokens[index].end
		}
		if markedBy(gap, "-- SQLite:") {
			statements = append(statements, text[part[0].start:part[len(part)-1].end])
			continue
		}
		if markedBy(gap, "-- KitDB:") {
			if len(part) < 3 || part[0].word() != "INSERT" || part[1].word() != "INTO" || !strings.EqualFold(identifierText(part[2]), "kitwork_columns") {
				return nil, nil, nil, fmt.Errorf("gateway: after %q a structure file has the rows of kitwork_columns only", "-- KitDB:")
			}
			rows = append(rows, text[part[0].start:part[len(part)-1].end])
			continue
		}
		c := &schemaCursor{tokens: part}
		var plan schemaPlan
		switch {
		case c.keyword("CREATE", "DOMAIN"):
			plan, err = c.createDomain()
		case c.keyword("CREATE", "TABLE"):
			plan, err = c.createTable()
		case c.keyword("CREATE", "UNIQUE", "INDEX"):
			plan, err = c.createIndex(true)
		case c.keyword("CREATE", "INDEX"):
			plan, err = c.createIndex(false)
		default:
			return nil, nil, nil, fmt.Errorf("gateway: a structure file makes tables and indexes: %q is neither", renderTokens(part[:min(3, len(part))]))
		}
		if err != nil {
			return nil, nil, nil, fmt.Errorf("gateway: the structure file's %s: %w", renderTokens(part[:min(3, len(part))]), err)
		}
		statements = append(statements, plan.statements...)
		kinds = append(kinds, plan.kinds...)
	}
	return statements, kinds, rows, nil
}
