package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// The history of a database (KITWORK_KITDB.md mục 4–5, 03/11/2026). Every SQLite file the engine declares tables on
// (the app's .database/<database>/<schema>.db, a host's file of connection("tên")) has one writer for the process,
// engine/kitdb/sqlite's, which records each row a commit changed in kitwork_history in the same transaction and keeps
// the anchors a moment is restored from. A statement that writes goes to the writer; one that reads goes to the file's
// pool as before. The engine's own change of the file (a migration, the copy of a KitDB file) runs on the writer's
// connection (Maintain): a change of the schema or of an app's rows there commits a Barrier and an anchor, so a
// restore never replays across it. The runs of jobs (kitwork_runs) are the engine's bookkeeping, written on the pool.
//
// The writer opens at the first statement on the file, before its tables are migrated. An app holds the files it
// opened until it closes; the last app that holds a file closes its writer and lets go of kitwork.db.lock.

// historyResourceName is the app resource holding the files whose history the app's statements write.
const historyResourceName = "history"

// historyWriter is the writer of one file and how many apps hold it.
type historyWriter struct {
	database *kitdbsqlite.Database
	users    int
}

var historyWriters = struct {
	sync.Mutex
	files map[string]*historyWriter
}{files: map[string]*historyWriter{}}

// acquireHistory opens the writer of the file at path, or shares the one open, until release.
func acquireHistory(path string) (*kitdbsqlite.Database, func(), error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, nil, err
	}
	absolute = filepath.Clean(absolute)
	historyWriters.Lock()
	defer historyWriters.Unlock()
	entry := historyWriters.files[absolute]
	if entry == nil {
		if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
			return nil, nil, err
		}
		if err := dropSearchTriggers(absolute); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", filepath.Base(absolute), err)
		}
		database, err := kitdbsqlite.Open(context.Background(), absolute, kitdbsqlite.Options{})
		if err != nil {
			return nil, nil, historyRefusal(absolute, err)
		}
		entry = &historyWriter{database: database}
		historyWriters.files[absolute] = entry
	}
	entry.users++
	var once sync.Once
	return entry.database, func() { once.Do(func() { releaseHistory(absolute, entry) }) }, nil
}

func releaseHistory(path string, entry *historyWriter) {
	historyWriters.Lock()
	entry.users--
	last := entry.users == 0
	if last {
		delete(historyWriters.files, path)
	}
	historyWriters.Unlock()
	if last {
		_ = entry.database.Close()
	}
}

// historyRefusal says why the writer did not open: what the history cannot record (a trigger, a virtual table, a
// table WITHOUT ROWID) is the file's to change, another process holding the file is said as such.
func historyRefusal(path string, err error) error {
	switch {
	case errors.Is(err, kitdbsqlite.ErrUnsupportedSchema):
		return fmt.Errorf("%s có thứ lịch sử của database không ghi được (%v): engine giữ lịch sử của mọi file nó khai bảng, "+
			"nên file không có trigger, bảng ảo, bảng WITHOUT ROWID (KITWORK_KITDB.md mục 4)", filepath.Base(path), err)
	case errors.Is(err, kitdbsqlite.ErrLocked):
		return fmt.Errorf("%s đang được một tiến trình khác ghi (%s.lock): một database một tiến trình ghi", filepath.Base(path), filepath.Base(path))
	}
	return fmt.Errorf("%s: %w", filepath.Base(path), err)
}

// dropSearchTriggers takes out of a file what the engine wrote before 03/11/2026 to track a table for its full-text
// index: the triggers kitwork_search_* and their two tables. The writer refuses a file with triggers, and the index
// now reads the changes from the history.
func dropSearchTriggers(path string) error {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer database.Close()
	rows, err := database.Query(`SELECT name FROM sqlite_schema WHERE type = 'trigger' AND name LIKE 'kitwork\_search\_%' ESCAPE '\'`)
	if err != nil {
		return err
	}
	var triggers []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		triggers = append(triggers, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range triggers {
		if _, err := database.Exec(`DROP TRIGGER IF EXISTS ` + quoteSearchIdentifier(name)); err != nil {
			return err
		}
	}
	for _, table := range []string{"kitwork_search_changes", "kitwork_search_revisions"} {
		if _, err := database.Exec(`DROP TABLE IF EXISTS ` + table); err != nil {
			return err
		}
	}
	return nil
}

// historyFiles are the files an app's statements opened the writer of, released when the app closes.
type historyFiles struct {
	mutex     sync.Mutex
	databases map[string]*kitdbsqlite.Database
	releases  []func()
	closed    bool
}

func (files *historyFiles) Close() {
	files.mutex.Lock()
	releases := files.releases
	files.releases, files.databases, files.closed = nil, nil, true
	files.mutex.Unlock()
	for _, release := range releases {
		release()
	}
}

// historyDatabase is the writer of the file at path, held for the app until it closes.
func (t *Tenant) historyDatabase(path string) (*kitdbsqlite.Database, error) {
	if t.appRuntime == nil {
		return nil, fmt.Errorf("lịch sử của database cần runtime của app")
	}
	resource, _, err := t.appRuntime.InstallResource(historyResourceName, &historyFiles{databases: map[string]*kitdbsqlite.Database{}})
	if err != nil {
		return nil, err
	}
	files, ok := resource.(*historyFiles)
	if !ok {
		return nil, fmt.Errorf("app resource %s is not the history files", historyResourceName)
	}
	key := filepath.Clean(path)
	if absolute, err := filepath.Abs(path); err == nil {
		key = filepath.Clean(absolute)
	}
	files.mutex.Lock()
	defer files.mutex.Unlock()
	if files.closed {
		return nil, fmt.Errorf("app đang đóng")
	}
	if database := files.databases[key]; database != nil {
		return database, nil
	}
	database, release, err := acquireHistory(key)
	if err != nil {
		return nil, err
	}
	files.databases[key] = database
	files.releases = append(files.releases, release)
	return database, nil
}

// insertKept writes row into the table through the writer of its file, one commit of the history, and gives the row
// as the file holds it, read back in the writer's transaction (the key and defaults the file gave it).
func (t *SchemaTable) insertKept(row map[string]value.Value) value.Value {
	database, err := t.tenant.historyDatabase(t.sqliteFile())
	if err != nil {
		return value.InvalidFailure(value.DatabaseCode, err.Error())
	}
	names := make([]string, 0, len(row))
	for name := range row {
		names = append(names, name)
	}
	sort.Strings(names)
	quoted := make([]string, len(names))
	marks := make([]string, len(names))
	arguments := make([]any, len(names))
	for position, name := range names {
		quoted[position], marks[position], arguments[position] = quoteSearchIdentifier(name), "?", row[name].Interface()
	}
	table := quoteSearchIdentifier(t.table)
	insert := "INSERT INTO " + table + " (" + strings.Join(quoted, ", ") + ") VALUES (" + strings.Join(marks, ", ") + ")"
	if len(names) == 0 {
		insert = "INSERT INTO " + table + " DEFAULT VALUES"
	}
	var written []map[string]value.Value
	_, err = database.Write(t.writeContext(), historyBy(t), func(transaction *kitdbsqlite.Transaction) error {
		result, err := transaction.Exec(insert, arguments...)
		if err != nil {
			return err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		rows, err := transaction.Query("SELECT * FROM "+table+" WHERE _rowid_ = ?", rowID)
		if err != nil {
			return err
		}
		written, err = scanValueRows(rows)
		return err
	})
	if err != nil {
		return query.DatabaseFailure("database insert error", err)
	}
	if len(written) == 0 {
		return value.NewNil()
	}
	return value.New(written[0])
}

// writeContext is the request's context, or the background for a statement no request runs.
func (t *SchemaTable) writeContext() context.Context {
	if t.scope != nil {
		if ctx := t.scope.Context(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// scanValueRows reads every row of rows, each a map of its columns, and closes rows.
func scanValueRows(rows *sql.Rows) ([]map[string]value.Value, error) {
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var read []map[string]value.Value
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for position := range values {
			pointers[position] = &values[position]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := make(map[string]value.Value, len(columns))
		for position, column := range columns {
			row[column] = value.New(values[position])
		}
		read = append(read, row)
	}
	return read, rows.Err()
}

// connRunner runs what a migration runs (schemaRunner) on one connection: the writer's, inside Maintain.
type connRunner struct {
	ctx  context.Context
	conn *sql.Conn
}

func (runner connRunner) Exec(query string, args ...any) (sql.Result, error) {
	return runner.conn.ExecContext(runner.ctx, query, args...)
}

func (runner connRunner) Query(query string, args ...any) (*sql.Rows, error) {
	return runner.conn.QueryContext(runner.ctx, query, args...)
}

func (runner connRunner) QueryRow(query string, args ...any) *sql.Row {
	return runner.conn.QueryRowContext(runner.ctx, query, args...)
}

func (runner connRunner) Begin() (*sql.Tx, error) { return runner.conn.BeginTx(runner.ctx, nil) }

// schemaRunner is what a migration runs on: a file's pool (*sql.DB), or the writer's connection (connRunner).
type schemaRunner interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Begin() (*sql.Tx, error)
}

// noRunner reports a runner that is not there: none, or a pool that did not open.
func noRunner(db schemaRunner) bool {
	pool, isPool := db.(*sql.DB)
	return db == nil || isPool && pool == nil
}

// historyBy is who a commit of the history names: the request's authenticated subject, or nobody.
func historyBy(table *SchemaTable) string {
	if table == nil || table.scope == nil {
		return ""
	}
	if principal := table.scope.Principal(); principal.Authenticated {
		return strings.TrimSpace(principal.Subject)
	}
	return ""
}
