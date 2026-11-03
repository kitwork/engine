package statement

import (
	"fmt"
	"strings"
)

// Database is what a statement may name: the tables of one connection().tables({ … }), each with its
// columns in the order they are declared. The compiler reads it from the schema file's struct({ … })
// literals; the engine builds it from the live schema. Both check a statement with the same rules below,
// so a name is refused in the same words at compile time and at run time.
type Database struct {
	// Export is the name the schema file exports it under (db); "" when it is not known.
	Export string
	// Connection is the name connection("warehouse") picks in the host's catalogue (app.databases of
	// application.work); "" for connection(), the app's own database.
	Connection string
	// External is the connector of connect({ postgresql: … }), a database outside the app that has no
	// tables here and answers only query(sql, [giá trị]); "" for a database declared with connection().
	External string
	// Schema is the schema connection().schema("billing") picks, a file of its own beside the database's
	// public one (KITWORK_DATABASE.md mục 1, "Schema", 29/10/2026); "" for public.
	Schema string
	Tables []Table
}

// Table is one table of a database.
type Table struct {
	Name    string
	Columns []Column
}

// Column is one column of a table, in the spec's vocabulary.
type Column struct {
	Name string
	// Kind is the builder that declared it: id, text, integer, float, decimal, boolean, date, json,
	// timestamp.
	Kind     string
	Nullable bool
	// Unique is .unique(); UniqueGroup the name of .unique("tên"), a group of columns unique together.
	Unique      bool
	UniqueGroup string
	Search      bool
	Primary     bool
	// Generated: the engine makes the value at insert (id()).
	Generated bool
	// WriteProtected: timestamp().created() or .updated(), which the engine fills; the author never writes it.
	WriteProtected bool
	HasDefault     bool
	// Precision and Scale are decimal(precision, scale); 0 for other kinds.
	Precision int
	Scale     int
}

// Table is the table of that name.
func (d *Database) Table(name string) (*Table, error) {
	for index := range d.Tables {
		if d.Tables[index].Name == name {
			return &d.Tables[index], nil
		}
	}
	return nil, fmt.Errorf("không có bảng %s; bảng của %s: %s", name, d.name(), strings.Join(d.tableNames(), ", "))
}

// Column is the column of that name.
func (t *Table) Column(name string) (*Column, error) {
	for index := range t.Columns {
		if t.Columns[index].Name == name {
			return &t.Columns[index], nil
		}
	}
	return nil, fmt.Errorf("bảng %s không có cột %s; cột: %s", t.Name, name, strings.Join(t.columnNames(), ", "))
}

// Searchable reports a table with a .searchable() column: matches reads it.
func (t *Table) Searchable() bool {
	for _, column := range t.Columns {
		if column.Search {
			return true
		}
	}
	return false
}

// SearchReserved is the error of a table with a .searchable() column and a column named score or snippet, the two
// fields matches adds to its rows (KITWORK_DATABASE.md mục 1); "" otherwise.
func SearchReserved(t *Table) string {
	if !t.Searchable() {
		return ""
	}
	for _, column := range t.Columns {
		if column.Name == "score" || column.Name == "snippet" {
			return fmt.Sprintf("cột %s: bảng có cột .searchable() không có cột tên score hay snippet, hai trường matches thêm vào dòng; đặt tên khác", column.Name)
		}
	}
	return ""
}

// Required reports a column an insert must write: not nullable, no default, and not filled by the engine
// (id(), .created(), .updated()).
func (c *Column) Required() bool {
	return RequiredByDefault(*c) && !c.HasDefault && !c.Primary && !c.Generated && !c.WriteProtected
}

// CheckInsert checks the keys of an insert's data: each is a column, none is filled by the engine, and
// every required column is there. Missing columns are named all at once, in declaration order.
func (t *Table) CheckInsert(keys []string) error {
	given := make(map[string]bool, len(keys))
	for _, key := range keys {
		column, err := t.Column(key)
		if err != nil {
			return err
		}
		if err := column.CheckWrite(); err != nil {
			return err
		}
		given[key] = true
	}
	var missing []string
	for _, column := range t.Columns {
		if column.Required() && !given[column.Name] {
			missing = append(missing, column.Name)
		}
	}
	if len(missing) != 0 {
		return fmt.Errorf("insert vào %s thiếu cột bắt buộc: %s (cột được bỏ trống khai .nullable())", t.Name, strings.Join(missing, ", "))
	}
	return nil
}

// CheckWrite refuses data for a column the engine fills. A .created() or .updated() column is the spec's
// rule (KITWORK_DATABASE.md mục 5); an id() column is the engine's limit today: the spec leaves how an id
// is made open (mục 1) and does not forbid writing one.
func (c *Column) CheckWrite() error {
	switch {
	case c.WriteProtected:
		return fmt.Errorf("%s tự điền lúc ghi (timestamp().created() hay .updated()), không viết trong dữ liệu", c.Name)
	case c.Generated:
		return fmt.Errorf("%s là id(), engine sinh lúc insert; ghi id tường minh chưa có trong engine hôm nay (cách sinh id còn mở, KITWORK_DATABASE.md mục 1)", c.Name)
	}
	return nil
}

// CheckNames checks every name a statement uses against the database: the table, each column it reads,
// orders by or writes, the keys of an insert or an upsert, and what update and upsert data read.
func (d *Database) CheckNames(s *Statement) error {
	table, err := d.Table(s.Table)
	if err != nil {
		return err
	}
	keys := make([]string, 0, len(s.Values))
	for _, field := range s.Values {
		keys = append(keys, field.Column)
	}
	switch s.Action {
	case ActionInsert:
		return table.CheckInsert(keys)
	case ActionUpsert:
		if err := table.CheckInsert(keys); err != nil {
			return err
		}
		target, err := table.UpsertTarget(keys)
		if err != nil {
			return err
		}
		if strings.Join(target, ",") != strings.Join(s.Target, ",") {
			return fmt.Errorf("upsert vào %s so trùng theo %s, bảng nay so theo %s: dịch lại", table.Name, strings.Join(s.Target, ", "), strings.Join(target, ", "))
		}
	case ActionUpdate:
		for _, key := range keys {
			column, err := table.Column(key)
			if err != nil {
				return err
			}
			if err := column.CheckWrite(); err != nil {
				return err
			}
		}
	}
	for _, column := range s.Columns() {
		if _, err := table.Column(column); err != nil {
			return err
		}
	}
	target := map[string]bool{}
	for _, column := range s.Target {
		target[column] = true
	}
	for _, field := range s.Values {
		if s.Action == ActionInsert {
			break
		}
		if err := table.CheckData(s.Action, field.Column, field.Value, target[field.Column]); err != nil {
			return err
		}
	}
	return nil
}

// numeric reports a kind + and - read as a number.
func numeric(kind string) bool { return kind == "integer" || kind == "float" || kind == "decimal" }

// spelling is a column's kind as the schema writes it.
func (c *Column) spelling() string {
	if c.Kind == "decimal" {
		return fmt.Sprintf("decimal(%d, %d)", c.Precision, c.Scale)
	}
	return c.Kind + "()"
}

// CheckData checks one value of update or upsert data for column (KITWORK_DATABASE.md mục 2): + and - stand on a
// number column, between numbers; a column it reads is of column's kind (a decimal of its scale); upsert reads only
// number columns, null on the row it inserts and read as 0 (null + 1 is 1, as JavaScript reads it); the target of an
// upsert is written as values.
func (t *Table) CheckData(action, column string, written *Node, target bool) error {
	into, err := t.Column(column)
	if err != nil {
		return err
	}
	var check func(*Node) error
	check = func(node *Node) error {
		switch node.Op {
		case OpAdd, OpSubtract:
			if !numeric(into.Kind) {
				return fmt.Errorf("cột %s là %s: %s là phép tính số, chỉ trên cột số", column, into.spelling(), node.Op)
			}
			for _, argument := range node.Args {
				if err := check(argument); err != nil {
					return err
				}
			}
		case OpColumn:
			if target {
				return fmt.Errorf("upsert so trùng theo %s: giá trị của nó viết thẳng, không đọc bảng", column)
			}
			read, err := t.Column(node.Column)
			if err != nil {
				return err
			}
			if action == ActionUpsert && !numeric(read.Kind) {
				return fmt.Errorf("upsert đọc %s.%s, là null khi chưa có dòng: chỉ cột số đọc được như vậy (null + 1 là 1), cột %s là %s",
					t.Name, node.Column, node.Column, read.spelling())
			}
			if read.Kind != into.Kind || read.Scale != into.Scale {
				return fmt.Errorf("cột %s là %s, %s.%s là %s: dữ liệu đọc một cột cùng kiểu", column, into.spelling(), t.Name, node.Column, read.spelling())
			}
		}
		return nil
	}
	return check(written)
}

// UpsertTarget is the columns an upsert writing keys compares its data by (KITWORK_DATABASE.md mục 2): a .unique()
// column among keys, or every column of a .unique("tên") group, in the order the table declares them. None, or more
// than one, is the error naming the targets the table has.
func (t *Table) UpsertTarget(keys []string) ([]string, error) {
	given := make(map[string]bool, len(keys))
	for _, key := range keys {
		given[key] = true
	}
	var found [][]string
	var named []string
	groups := map[string][]string{}
	var order []string
	for _, column := range t.Columns {
		if column.Unique {
			named = append(named, column.Name)
			if given[column.Name] {
				found = append(found, []string{column.Name})
			}
		}
		if group := column.UniqueGroup; group != "" {
			if _, seen := groups[group]; !seen {
				order = append(order, group)
			}
			groups[group] = append(groups[group], column.Name)
		}
	}
	for _, group := range order {
		columns := groups[group]
		named = append(named, fmt.Sprintf(`.unique("%s") (%s)`, group, strings.Join(columns, ", ")))
		every := true
		for _, column := range columns {
			every = every && given[column]
		}
		if every {
			found = append(found, columns)
		}
	}
	switch {
	case len(found) == 1:
		return found[0], nil
	case len(named) == 0:
		return nil, fmt.Errorf("upsert vào %s cần một đích so trùng: bảng không có cột .unique() hay nhóm .unique(\"tên\")", t.Name)
	case len(found) == 0:
		return nil, fmt.Errorf("upsert vào %s cần dữ liệu có đủ một đích so trùng; bảng có: %s", t.Name, strings.Join(named, "; "))
	}
	described := make([]string, len(found))
	for index, columns := range found {
		described[index] = strings.Join(columns, ", ")
	}
	return nil, fmt.Errorf("upsert vào %s có %d đích so trùng trong dữ liệu (%s): để đúng một", t.Name, len(found), strings.Join(described, "; "))
}

func (d *Database) name() string {
	if d.Export != "" {
		return d.Export
	}
	return "database"
}

func (d *Database) tableNames() []string {
	names := make([]string, 0, len(d.Tables))
	for _, table := range d.Tables {
		names = append(names, table.Name)
	}
	return names
}

func (t *Table) columnNames() []string {
	names := make([]string, 0, len(t.Columns))
	for _, column := range t.Columns {
		names = append(names, column.Name)
	}
	return names
}
