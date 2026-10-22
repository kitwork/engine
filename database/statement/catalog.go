package statement

import (
	"fmt"
	"strings"
)

// Database is what a statement may name: the tables of one connection().schema({ … }), each with its
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
	Tables   []Table
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
	Unique   bool
	Search   bool
	Primary  bool
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
// orders by or writes, and the keys of an insert.
func (d *Database) CheckNames(s *Statement) error {
	table, err := d.Table(s.Table)
	if err != nil {
		return err
	}
	if s.Action == ActionInsert {
		keys := make([]string, 0, len(s.Values))
		for _, field := range s.Values {
			keys = append(keys, field.Column)
		}
		return table.CheckInsert(keys)
	}
	for _, column := range s.Columns() {
		if _, err := table.Column(column); err != nil {
			return err
		}
	}
	return nil
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
