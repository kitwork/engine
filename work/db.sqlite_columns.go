package work

import (
	"database/sql"
	"fmt"
	"strings"
)

// kitwork_columns keeps, in each SQLite file the engine migrates, the kind each column of a table was declared with
// (KITWORK_KITDB.md câu 13.5): what the file alone does not say, a decimal's scale above all (the file stores the
// integer × 10^scale), so the PostgreSQL wire port gives 4.50 for 450 and a boolean as true or false. The rows of a
// table's declared columns are written again each time the engine checks the table against its declaration
// (ensureTable), in one transaction; the rows of the columns it does not declare stay, the database's own (a column
// added through the port, KITWORK_DATABASE.md "Database là gốc", 05/11/2026). The kinds are the engine's own
// (decimal, bool, datetime, date, json, kitid, text, …). search_weight is a .searchable() column's weight in the table's full-text index, 0 for any other column:
// the database port builds the index of each table that has one and answers MATCH on it (app_database_search.go).
//
// A table with a .searchable() column has its full-text index folded from the history of its file (engine/database/
// fulltext, database_history.go): every write goes through the file's one writer, which records the rows it changed.
// Before 03/11/2026 triggers counted them in kitwork_search_revisions and kitwork_search_changes; the writer refuses a
// file with triggers, so they are dropped before it opens (dropSearchTriggers).

const sqliteColumnsSchema = `CREATE TABLE IF NOT EXISTS kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, search_weight INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (table_name, column_name))`

// sqliteColumnsSearch gives a kitwork_columns written before search_weight (31/10/2026) the column.
const sqliteColumnsSearch = `ALTER TABLE kitwork_columns ADD COLUMN search_weight INTEGER NOT NULL DEFAULT 0`

// recordColumnKinds writes the declared columns of table into kitwork_columns of db, replacing what it held for the
// table, in one transaction.
func recordColumnKinds(db schemaRunner, table string, columns map[string]*ColumnSpec) (err error) {
	if noRunner(db) {
		return fmt.Errorf("connection unavailable")
	}
	transaction, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = transaction.Rollback()
		}
	}()
	if err = writeColumnKinds(transaction, table, columns); err != nil {
		return err
	}
	return transaction.Commit()
}

// columnKindsWriter is what writes kitwork_columns: a transaction of the engine's, or of the copy of a KitDB file.
type columnKindsWriter interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// writeColumnKinds writes the rows of table's declared columns in kitwork_columns, in the caller's transaction; the
// rows of the columns it does not declare are left as they are, unless the table no longer has them.
func writeColumnKinds(writer columnKindsWriter, table string, columns map[string]*ColumnSpec) error {
	if _, err := writer.Exec(sqliteColumnsSchema); err != nil {
		return err
	}
	if _, err := writer.Exec(sqliteColumnsSearch); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	declared := make([]any, 0, len(columns)+1)
	declared = append(declared, table)
	for name := range columns {
		declared = append(declared, name)
	}
	marks := strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")
	if marks == "" {
		marks = "NULL"
	}
	// the declared columns' rows are written again; another column's row goes only with its column
	if _, err := writer.Exec(`DELETE FROM kitwork_columns WHERE table_name = ? AND (column_name IN (`+marks+`)
		OR column_name NOT IN (SELECT name FROM pragma_table_xinfo(table_name)))`, declared...); err != nil {
		return err
	}
	for _, name := range orderedColumns(columns) {
		spec := columns[name]
		precision, scale, weight := 0, 0, 0
		if spec.kind == "decimal" {
			precision, scale = spec.precision, spec.scale
		}
		if spec.searchable {
			weight = max(spec.searchWt, 1)
		}
		if _, err := writer.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale, search_weight) VALUES (?, ?, ?, ?, ?, ?)`,
			table, name, spec.kind, precision, scale, weight); err != nil {
			return err
		}
	}
	return nil
}
