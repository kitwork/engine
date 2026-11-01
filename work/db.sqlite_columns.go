package work

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
)

// kitwork_columns keeps, in each SQLite file the engine migrates, the kind each column of a table was declared with
// (KITWORK_KITDB.md câu 13.5): what the file alone does not say, a decimal's scale above all (the file stores the
// integer × 10^scale), so the PostgreSQL wire port gives 4.50 for 450 and a boolean as true or false. The rows of a
// table are replaced by its declared columns each time the engine checks the table against its declaration
// (ensureTable), in one transaction; the kinds are the engine's own (decimal, bool, datetime, date, json, kitid,
// text, …). search_weight is a .searchable() column's weight in the table's full-text index, 0 for any other column:
// the database port builds the index of each table that has one and answers MATCH on it (app_database_search.go).
//
// A table with a .searchable() column is tracked for its full-text index (engine/database/fulltext): triggers count
// every insert, delete and change of a searchable column in kitwork_search_revisions and name the rowid in
// kitwork_search_changes, so the index folds in the rows changed since it last read, whoever wrote them, instead of
// reading the table again. A change of another column (a stock count) is not one. The triggers are written again with
// kitwork_columns, from the table's declared columns.

const sqliteColumnsSchema = `CREATE TABLE IF NOT EXISTS kitwork_columns (table_name TEXT NOT NULL, column_name TEXT NOT NULL, kind TEXT NOT NULL, precision INTEGER NOT NULL, scale INTEGER NOT NULL, search_weight INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (table_name, column_name))`

// sqliteColumnsSearch gives a kitwork_columns written before search_weight (31/10/2026) the column.
const sqliteColumnsSearch = `ALTER TABLE kitwork_columns ADD COLUMN search_weight INTEGER NOT NULL DEFAULT 0`

// The two tables a tracked table's triggers write.
var sqliteSearchTracking = []string{
	`CREATE TABLE IF NOT EXISTS kitwork_search_revisions (table_name TEXT PRIMARY KEY, revision INTEGER NOT NULL) WITHOUT ROWID`,
	`CREATE TABLE IF NOT EXISTS kitwork_search_changes (table_name TEXT NOT NULL, row_id INTEGER NOT NULL, revision INTEGER NOT NULL, PRIMARY KEY (table_name, row_id)) WITHOUT ROWID`,
	`CREATE INDEX IF NOT EXISTS kitwork_search_changes_revision ON kitwork_search_changes (table_name, revision)`,
}

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
	if _, err := writer.Exec(sqliteColumnsSearch); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
		return err
	}
	if _, err := writer.Exec(`DELETE FROM kitwork_columns WHERE table_name = ?`, table); err != nil {
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
	return writeSearchTracking(writer, table, columns)
}

// writeSearchTracking writes the triggers that track table for its full-text index, from its .searchable() columns
// (and its key, whose change moves the rowid); a table with none loses them and its rows of the two tables.
func writeSearchTracking(writer columnKindsWriter, table string, columns map[string]*ColumnSpec) error {
	var watched []string
	searchable := false
	for _, name := range orderedColumns(columns) {
		spec := columns[name]
		if spec.searchable {
			searchable = true
		}
		if spec.searchable || spec.primary {
			watched = append(watched, quoteSearchIdentifier(name))
		}
	}
	digest := sha256.Sum256([]byte(table))
	stem := "kitwork_search_" + hex.EncodeToString(digest[:8])
	for _, event := range []string{"insert", "update", "delete"} {
		if _, err := writer.Exec(`DROP TRIGGER IF EXISTS ` + quoteSearchIdentifier(stem+"_"+event)); err != nil {
			return err
		}
	}
	if !searchable {
		for _, statement := range []string{`DELETE FROM kitwork_search_revisions WHERE table_name = ?`, `DELETE FROM kitwork_search_changes WHERE table_name = ?`} {
			if _, err := writer.Exec(statement, table); err != nil && !strings.Contains(err.Error(), "no such table") {
				return err
			}
		}
		return nil
	}
	for _, statement := range sqliteSearchTracking {
		if _, err := writer.Exec(statement); err != nil {
			return err
		}
	}
	if _, err := writer.Exec(`INSERT OR IGNORE INTO kitwork_search_revisions (table_name, revision) VALUES (?, 0)`, table); err != nil {
		return err
	}
	name, quoted := quoteSearchLiteral(table), quoteSearchIdentifier(table)
	bump := `UPDATE kitwork_search_revisions SET revision = revision + 1 WHERE table_name = ` + name + `; `
	note := func(row, condition string) string {
		return `INSERT OR REPLACE INTO kitwork_search_changes (table_name, row_id, revision) SELECT ` + name + `, ` + row +
			`, revision FROM kitwork_search_revisions WHERE table_name = ` + name + condition + `; `
	}
	triggers := map[string]string{
		"insert": `AFTER INSERT ON ` + quoted + ` BEGIN ` + bump + note("NEW.rowid", "") + `END`,
		"delete": `AFTER DELETE ON ` + quoted + ` BEGIN ` + bump + note("OLD.rowid", "") + `END`,
		"update": `AFTER UPDATE OF ` + strings.Join(watched, ", ") + ` ON ` + quoted + ` BEGIN ` + bump + note("NEW.rowid", "") +
			note("OLD.rowid", " AND OLD.rowid <> NEW.rowid") + `END`,
	}
	for _, event := range []string{"insert", "update", "delete"} {
		if _, err := writer.Exec(`CREATE TRIGGER ` + quoteSearchIdentifier(stem+"_"+event) + ` ` + triggers[event]); err != nil {
			return err
		}
	}
	return nil
}
