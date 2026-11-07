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
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb/gateway"
	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// The history of a database (KITWORK_KITDB.md mục 4–5, 03/11/2026). Every SQLite file the engine declares tables on
// (the app's .database/<database>/<schema>.db, a host's file of connection("tên")) has one writer for the process,
// engine/kitdb/sqlite's, which records each row a commit changed in kitwork_history in the same transaction and keeps
// the anchors a moment is restored from. A statement that writes goes to the writer; one that reads goes to the file's
// pool as before. The engine's own change of the file (kitwork migrate, database_migrate.go) runs on the writer's
// connection (Maintain): a change of the schema or of an app's rows there commits a Barrier and an anchor, so a
// restore never replays across it. The runs of jobs (kitwork_runs) are the engine's bookkeeping, written on the pool.
//
// The writer opens at the first statement on the file, before its tables are checked (database_follows.go). An app holds the files it
// opened until it closes; the last app that holds a file closes its writer and lets go of kitwork.db.lock.

// historyResourceName is the app resource holding the files whose history the app's statements write.
const historyResourceName = "history"

// The history keeps what app.history({ days }) of application.work says (KITWORK_KITDB.md câu 13.6, 03/11/2026),
// historyDaysDefault days when it says nothing: each writer anchors its file once a day of commits is in no anchor, and
// takes out what no moment of the days kept needs (older anchors, older commits, the copies at() made of them), when
// it opens and every historyRetainEvery after.
const historyDaysDefault = 30

var historyDays atomic.Int64

// historyRetainEvery is how often an open writer applies the retention.
var historyRetainEvery = time.Hour

// SetHistoryDays is app.history({ days }) of the host's manifest, set at boot; 0 keeps the default.
func SetHistoryDays(days int) { historyDays.Store(int64(days)) }

// HistoryDays is how many days the history of the apps' databases keeps on this host.
func HistoryDays() int { return int(historyRetention().Keep / (24 * time.Hour)) }

func historyRetention() kitdbsqlite.Retention {
	days := historyDays.Load()
	if days <= 0 {
		days = historyDaysDefault
	}
	return kitdbsqlite.Retention{Keep: time.Duration(days) * 24 * time.Hour, Every: 24 * time.Hour}
}

// historyWriter is the writer of one file and how many apps hold it.
type historyWriter struct {
	database *kitdbsqlite.Database
	users    int
	// stop ends the retention of the file, done says it ended
	stop context.CancelFunc
	done chan struct{}
}

// keep applies the retention now and every historyRetainEvery, until stop.
func (entry *historyWriter) keep(ctx context.Context) {
	defer close(entry.done)
	ticker := time.NewTicker(historyRetainEvery)
	defer ticker.Stop()
	for {
		entry.retain(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (entry *historyWriter) retain(ctx context.Context, now time.Time) {
	kept, err := entry.database.Retain(ctx, now, historyRetention())
	if kept > 0 {
		dropAtFilesBefore(entry.database.Path()+".at", kept)
	}
	if err != nil && ctx.Err() == nil && !errors.Is(err, kitdbsqlite.ErrClosed) {
		fmt.Printf("[database] the history of %s: %v\n", filepath.Base(entry.database.Path()), err)
	}
}

var historyWriters = struct {
	sync.Mutex
	files map[string]*historyWriter
}{files: map[string]*historyWriter{}}

// OpenWriter opens the writer of the file at path, or shares the one the engine's statements write through, until
// release: the host's database port writes through it (KITWORK_KITDB.md câu 13.5), so the history sees its writes too.
func OpenWriter(path string) (*kitdbsqlite.Database, func(), error) { return acquireHistory(path) }

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
		structure, made := readStructureToMake(absolute)
		database, err := kitdbsqlite.Open(context.Background(), absolute, kitdbsqlite.Options{Structure: structureWriter(absolute), HistoryOfEveryTable: true})
		if err != nil {
			return nil, nil, historyRefusal(absolute, err)
		}
		if made {
			if err := gateway.LoadStructure(context.Background(), database, "kitwork", structure); err != nil {
				_ = database.Close()
				removeMadeFile(absolute) // nothing is in it but what the structure made: the next open tries again
				return nil, nil, fmt.Errorf("%s: tạo database từ %s.sql: %w", filepath.Base(absolute), filepath.Base(absolute), err)
			}
			fmt.Printf("[database] %s tạo từ %s.sql\n", filepath.Base(absolute), filepath.Base(absolute))
		}
		ctx, stop := context.WithCancel(context.Background())
		entry = &historyWriter{database: database, stop: stop, done: make(chan struct{})}
		historyWriters.files[absolute] = entry
		go entry.keep(ctx)
	}
	entry.users++
	var once sync.Once
	return entry.database, func() { once.Do(func() { releaseHistory(absolute, entry) }) }, nil
}

// The structure file (KITWORK_DATABASE.md "Database là gốc", slice 3): beside each file the engine declares tables
// on, <file>.sql holds its tables and indexes as PostgreSQL writes them (kitdb/gateway's Structure), written again by
// the file's writer whenever its schema or its kitwork_columns changes; a folder holding the .sql and not the file
// gets the file made from it when the writer first opens it.

// readStructureToMake is the structure file of path when path does not exist yet and its .sql does: the file to make.
func readStructureToMake(path string) (string, bool) {
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return "", false
	}
	content, err := os.ReadFile(path + ".sql")
	if err != nil {
		return "", false
	}
	return string(content), true
}

// removeMadeFile takes away a file made from its structure that could not be finished, with what SQLite and KitDB
// made beside it; the .sql stays.
func removeMadeFile(path string) {
	for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
		_ = os.Remove(path + suffix)
	}
	_ = os.RemoveAll(path + ".history")
}

// structureWriter writes path's structure file whenever the writer gives it the file's structure, only when it
// changed (no new bytes, no new time, for git). A file of no table writes none, and never takes away one there: the
// file may be new and about to be made from it (acquireHistory opens it first). Only after this writer wrote tables
// does a file that lost all of them get the header alone, a structure of no table.
func structureWriter(path string) func(context.Context, *sql.Conn) {
	wroteTables := false // the hook runs on the writer's goroutine alone
	return func(ctx context.Context, conn *sql.Conn) {
		structure, err := gateway.Structure(ctx, conn)
		if err != nil {
			fmt.Printf("[database] %s: cấu trúc không đọc được, %s.sql giữ như cũ: %v\n", filepath.Base(path), filepath.Base(path), err)
			return
		}
		target := path + ".sql"
		switch {
		case structure == "" && !wroteTables:
			return
		case structure == "":
			structure = gateway.StructureHeader
		default:
			wroteTables = true
		}
		if previous, err := os.ReadFile(target); err == nil && string(previous) == structure {
			return
		}
		temporary := target + ".tmp"
		if err := os.WriteFile(temporary, []byte(structure), 0o644); err != nil {
			fmt.Printf("[database] %s: %v\n", filepath.Base(target), err)
			return
		}
		if err := os.Rename(temporary, target); err != nil {
			_ = os.Remove(temporary)
			fmt.Printf("[database] %s: %v\n", filepath.Base(target), err)
		}
	}
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
		entry.stop()
		<-entry.done
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
		return heldError{path: path}
	}
	return fmt.Errorf("%s: %w", filepath.Base(path), err)
}

// heldError is a file another process writes (a server running): said in words, and still kitdbsqlite.ErrLocked, so
// kitwork migrate knows to go through that process's port (database_migrate.go).
type heldError struct{ path string }

func (e heldError) Error() string {
	return fmt.Sprintf("%s đang được một tiến trình khác ghi (%s.lock): một database một tiến trình ghi", filepath.Base(e.path), filepath.Base(e.path))
}

func (e heldError) Unwrap() error { return kitdbsqlite.ErrLocked }

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
	err := t.write(func(exec writeExecutor) error {
		result, err := exec.Exec(insert, arguments...)
		if err != nil {
			return err
		}
		rowID, err := result.LastInsertId()
		if err != nil {
			return err
		}
		rows, err := exec.Query("SELECT * FROM "+table+" WHERE _rowid_ = ?", rowID)
		if err != nil {
			return err
		}
		written, err = scanValueRows(rows)
		return err
	})
	if err != nil {
		return databaseWriteFailure("database insert error", err)
	}
	if len(written) == 0 {
		return value.NewNil()
	}
	return value.New(written[0])
}

// databaseWriteFailure is a write's error as the program's failure: one that would wait for the transaction it runs
// in is said in words (database_transaction.go).
func databaseWriteFailure(what string, err error) value.Value {
	if errors.Is(err, kitdbsqlite.ErrReentrant) {
		return reentrantFailure()
	}
	return query.DatabaseFailure(what, err)
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
