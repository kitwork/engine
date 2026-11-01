package work

import (
	"database/sql"
	"fmt"
)

// kitwork_columns keeps, in each SQLite file the engine migrates, the kind each column of a table was declared with
// (KITWORK_KITDB.md câu 13.5): what the file alone does not say, a decimal's scale above all (the file stores the
// integer × 10^scale), so the PostgreSQL wire port gives 4.50 for 450 and a boolean as true or false. The rows of a
// table are replaced by its declared columns each time the engine checks the table against its declaration
// (ensureTable), in one transaction; the kinds are the engine's own (decimal, bool, datetime, date, json, kitid,
// text, …).

const sqliteColumnsSchema = `CREATE TABLE IF NOT EXISTS kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, PRIMARY KEY (table_name, column_name))`

// recordColumnKinds writes the declared columns of table into kitwork_columns of db, replacing what it held for the
// table, in one transaction.
func recordColumnKinds(db *sql.DB, table string, columns map[string]*ColumnSpec) (err error) {
	if db == nil {
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

// writeColumnKinds replaces the rows of table in kitwork_columns with its declared columns, in the caller's
// transaction.
func writeColumnKinds(writer columnKindsWriter, table string, columns map[string]*ColumnSpec) error {
	if _, err := writer.Exec(sqliteColumnsSchema); err != nil {
		return err
	}
	if _, err := writer.Exec(`DELETE FROM kitwork_columns WHERE table_name = ?`, table); err != nil {
		return err
	}
	for _, name := range orderedColumns(columns) {
		spec := columns[name]
		precision, scale := 0, 0
		if spec.kind == "decimal" {
			precision, scale = spec.precision, spec.scale
		}
		if _, err := writer.Exec(`INSERT INTO kitwork_columns (table_name, column_name, kind, precision, scale) VALUES (?, ?, ?, ?, ?)`,
			table, name, spec.kind, precision, scale); err != nil {
			return err
		}
	}
	return nil
}
