package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	goruntime "runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type writerRequest struct {
	ctx      context.Context
	by       string
	run      func(*Transaction) error
	schema   []string
	maintain func(*sql.Conn) error
	done     chan writerOutcome
}

// alone reports a request that commits by itself, never gathered with others.
func (request writerRequest) alone() bool { return request.schema != nil || request.maintain != nil }

type writerOutcome struct {
	revision uint64
	err      error
}

// event is one row change as SQLite's pre-update hook reports it.
type event struct {
	operation Operation
	table     string
	oldRowID  int64
	newRowID  int64
	before    []any
	by        string
}

type tableShape struct {
	reason string // empty when the history can record the table
}

// writer owns the one connection that writes. Everything below queue is used
// only by its goroutine, the hook included: SQLite calls the hook on the
// goroutine running the statement.
type writer struct {
	pool         *sql.DB
	conn         *sql.Conn
	queue        chan writerRequest
	finished     chan struct{}
	maximumBatch int

	sending sync.RWMutex
	stopped bool

	committed atomic.Uint64
	// goroutine is the writer goroutine's id: a submit on it comes from inside one of its requests.
	goroutine atomic.Int64

	events   []event
	hookErr  error
	broken   error
	by       string
	shapes   map[string]tableShape
	revision uint64
	at       int64
	checksum []byte

	// anchor writes an anchor of the file (Database.Anchor); refused is why the history can no longer follow the
	// file, after a Maintain left in it what the history cannot record: every later write fails with it.
	anchor  func(context.Context) (uint64, error)
	refused error

	// started is told, on the writer's connection, of every commit that changed what the history follows, once follow
	// has run after it: the database anchors the file when a table's history starts (Database.anchorWhenStarted).
	started func(context.Context, *sql.Conn) error

	// structure is Options.Structure; structureDue says the last commit changed the schema or kitwork_columns.
	structure    func(context.Context, *sql.Conn)
	structureDue bool

	// whole is, by table, the columns an update keeps whole in format 3 when one of them changes: the searchable
	// columns of Options.Search, whose index is built from the history's values.
	whole map[string]map[string]bool

	// every is Options.HistoryOfEveryTable. tracked are the tables (lower case) whose changes the history records
	// besides the engine's own (records); followDue says a commit changed what decides them (the schema, kitwork_tables,
	// kitwork_columnar, kitwork_columns) and follow has not run since.
	every     bool
	tracked   map[string]bool
	followDue bool
}

func openWriter(ctx context.Context, name string, options Options, anchor func(context.Context) (uint64, error),
	started func(context.Context, *sql.Conn) error) (*writer, error) {
	pool, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(1)
	pool.SetMaxIdleConns(1)
	pool.SetConnMaxLifetime(0)
	conn, err := pool.Conn(ctx)
	if err != nil {
		_ = pool.Close()
		return nil, err
	}
	w := &writer{
		pool: pool, conn: conn, queue: make(chan writerRequest, options.Queue),
		finished: make(chan struct{}), maximumBatch: options.MaximumBatch, anchor: anchor,
		whole: map[string]map[string]bool{}, structure: options.Structure, every: options.HistoryOfEveryTable,
	}
	for _, table := range options.Search {
		if w.whole[table.Table] == nil {
			w.whole[table.Table] = map[string]bool{}
		}
		for _, column := range table.Columns {
			w.whole[table.Table][column.Name] = true
		}
	}
	fail := func(err error) (*writer, error) {
		_ = conn.Close()
		_ = pool.Close()
		return nil, err
	}
	if err := conn.Raw(func(driverConn any) error {
		hooks, ok := driverConn.(modernc.HookRegisterer)
		if !ok {
			return errors.New("kitdb: the SQLite driver has no pre-update hook")
		}
		hooks.RegisterPreUpdateHook(w.hook)
		return nil
	}); err != nil {
		return fail(err)
	}
	if _, err := conn.ExecContext(ctx, historySchema); err != nil {
		return fail(err)
	}
	if err := w.refuseUnsupportedObjects(ctx); err != nil {
		return fail(err)
	}
	if err := w.loadShapes(ctx); err != nil {
		return fail(err)
	}
	if err := w.resync(ctx); err != nil {
		return fail(err)
	}
	if err := w.adopt(ctx); err != nil {
		return fail(err)
	}
	if err := w.follow(ctx); err != nil {
		return fail(err)
	}
	// from here; a start as the file opens is Open's, once the anchor directory is clean
	w.started = started
	go w.run()
	return w, nil
}

// refuseUnsupportedObjects refuses a file whose triggers or virtual tables
// would change rows the history cannot see.
func (w *writer) refuseUnsupportedObjects(ctx context.Context) error {
	var kind, name string
	err := w.conn.QueryRowContext(ctx, `SELECT type, name FROM sqlite_schema
		WHERE type = 'trigger' OR (type = 'table' AND sql LIKE 'CREATE VIRTUAL TABLE%') LIMIT 1`).Scan(&kind, &name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return fmt.Errorf("%w: %s %q", ErrUnsupportedSchema, kind, name)
}

// loadShapes records which tables the history can follow: rowid tables
// without generated columns.
func (w *writer) loadShapes(ctx context.Context) error {
	shapes := map[string]tableShape{}
	rows, err := w.conn.QueryContext(ctx, `SELECT name, wr FROM pragma_table_list WHERE schema = 'main' AND type = 'table'`)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		var withoutRowID int
		if err := rows.Scan(&name, &withoutRowID); err != nil {
			rows.Close()
			return err
		}
		if withoutRowID != 0 {
			shapes[name] = tableShape{reason: "WITHOUT ROWID tables are not followed"}
			continue
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, name := range names {
		var generated int
		if err := w.conn.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_xinfo(?) WHERE hidden IN (2, 3)`, name).Scan(&generated); err != nil {
			return err
		}
		if generated != 0 {
			shapes[name] = tableShape{reason: "generated columns are not followed"}
		} else {
			shapes[name] = tableShape{}
		}
	}
	w.shapes = shapes
	return nil
}

// resync reads the last committed revision back from the file. It runs at open
// and after any failed commit, whose outcome SQLite decided.
func (w *writer) resync(ctx context.Context) error {
	var revision, at int64
	var checksum []byte
	err := w.conn.QueryRowContext(ctx, `SELECT rev, at, checksum FROM kitwork_history ORDER BY rev DESC LIMIT 1`).Scan(&revision, &at, &checksum)
	if errors.Is(err, sql.ErrNoRows) {
		w.revision, w.at, w.checksum = 0, 0, nil
		w.committed.Store(0)
		return nil
	}
	if err != nil {
		return err
	}
	w.revision, w.at, w.checksum = uint64(revision), at, checksum
	w.committed.Store(uint64(revision))
	return nil
}

func (w *writer) hook(data modernc.SQLitePreUpdateData) {
	if data.DatabaseName != "main" || data.TableName == historyTable || w.hookErr != nil || !w.records(data.TableName) {
		return
	}
	if shape, known := w.shapes[data.TableName]; known && shape.reason != "" {
		w.hookErr = fmt.Errorf("%w: table %q: %s", ErrUnsupportedSchema, data.TableName, shape.reason)
		return
	}
	change := event{table: data.TableName, oldRowID: data.OldRowID, newRowID: data.NewRowID, by: w.by}
	switch data.Op {
	case sqlite3.SQLITE_INSERT:
		change.operation = Insert
	case sqlite3.SQLITE_UPDATE:
		change.operation = Update
	case sqlite3.SQLITE_DELETE:
		change.operation = Delete
	default:
		w.hookErr = fmt.Errorf("kitdb: unknown row operation %d", data.Op)
		return
	}
	if change.operation != Insert {
		change.before = make([]any, data.Count())
		if err := data.Old(change.before...); err != nil {
			w.hookErr = err
			return
		}
	}
	w.events = append(w.events, change)
}

func (w *writer) submit(ctx context.Context, request writerRequest) (uint64, error) {
	if goroutineID() == w.goroutine.Load() {
		return 0, ErrReentrant
	}
	request.done = make(chan writerOutcome, 1)
	w.sending.RLock()
	if w.stopped {
		w.sending.RUnlock()
		return 0, ErrClosed
	}
	select {
	case w.queue <- request:
		w.sending.RUnlock()
	case <-ctx.Done():
		w.sending.RUnlock()
		return 0, ctx.Err()
	}
	// Once queued the request is committed or refused as a whole; waiting for
	// that answer is the only way to know which.
	outcome := <-request.done
	return outcome.revision, outcome.err
}

func (w *writer) close() error {
	w.sending.Lock()
	if !w.stopped {
		w.stopped = true
		close(w.queue)
	}
	w.sending.Unlock()
	<-w.finished
	return errors.Join(w.conn.Close(), w.pool.Close())
}

func (w *writer) run() {
	defer close(w.finished)
	w.goroutine.Store(goroutineID())
	var pending *writerRequest
	for {
		var first writerRequest
		if pending != nil {
			first, pending = *pending, nil
		} else {
			request, open := <-w.queue
			if !open {
				return
			}
			first = request
		}
		switch {
		case w.refused != nil:
			first.done <- writerOutcome{err: w.refused}
			continue
		case first.maintain != nil:
			w.commitMaintain(first)
			continue
		case first.schema != nil:
			w.commitSchema(first)
			continue
		}
		batch := []writerRequest{first}
	gather:
		for len(batch) < w.maximumBatch {
			select {
			case request, open := <-w.queue:
				if !open {
					break gather
				}
				if request.alone() {
					pending = &request
					break gather
				}
				batch = append(batch, request)
			default:
				break gather
			}
		}
		w.commitBatch(batch)
	}
}

// writeStructure gives the owner the file's structure to write when the last commit changed it; it runs before the
// commit's requests are answered, so a caller that changed the schema finds the structure written when it returns.
func (w *writer) writeStructure() {
	if !w.structureDue {
		return
	}
	w.structureDue = false
	if w.structure != nil {
		w.structure(context.Background(), w.conn)
	}
}

// touchesColumnKinds reports a change of kitwork_columns, kitwork_columnar or kitwork_tables among the events: the
// kinds, the columns copies and the history settings the structure writes.
func touchesColumnKinds(events []event) bool {
	for _, change := range events {
		if strings.EqualFold(change.table, "kitwork_columns") || strings.EqualFold(change.table, "kitwork_columnar") ||
			strings.EqualFold(change.table, "kitwork_tables") {
			return true
		}
	}
	return false
}

func (w *writer) exec(query string, arguments ...any) error {
	_, err := w.conn.ExecContext(context.Background(), query, arguments...)
	return err
}

func (w *writer) commitBatch(batch []writerRequest) {
	if err := w.followFirst(); err != nil {
		for _, request := range batch {
			request.done <- writerOutcome{err: err}
		}
		return
	}
	outcomes := make([]writerOutcome, len(batch))
	w.events, w.broken = w.events[:0], nil
	err := w.exec("BEGIN IMMEDIATE")
	for position := 0; err == nil && position < len(batch); position++ {
		request := batch[position]
		if contextErr := request.ctx.Err(); contextErr != nil {
			outcomes[position].err = contextErr
			continue
		}
		mark := len(w.events)
		if err = w.exec("SAVEPOINT kitwork_request"); err != nil {
			break
		}
		w.by, w.hookErr = request.by, nil
		requestErr := w.runRequest(request)
		if w.broken != nil {
			err = w.broken
			break
		}
		if requestErr == nil {
			requestErr = w.hookErr
		}
		if requestErr != nil {
			outcomes[position].err = requestErr
			if err = w.exec("ROLLBACK TO kitwork_request"); err != nil {
				break
			}
			w.events = w.events[:mark]
		}
		if err = w.exec("RELEASE kitwork_request"); err != nil {
			break
		}
	}
	var changes []Change
	if err == nil {
		changes, err = w.netChanges(context.Background(), nil)
	}
	kinds := touchesColumnKinds(w.events)
	followed := touchesFollowed(w.events)
	revision, err := w.finish(changes, err)
	w.structureDue = w.structureDue || err == nil && kinds
	if err == nil && followed {
		w.refollow()
	}
	w.writeStructure()
	for position, request := range batch {
		switch {
		case err != nil:
			request.done <- writerOutcome{err: err}
		case outcomes[position].err != nil:
			request.done <- outcomes[position]
		default:
			request.done <- writerOutcome{revision: revision}
		}
	}
}

func (w *writer) commitSchema(request writerRequest) {
	if err := w.followFirst(); err != nil {
		request.done <- writerOutcome{err: err}
		return
	}
	w.events, w.broken, w.by, w.hookErr = w.events[:0], nil, request.by, nil
	previous := w.shapes
	err := request.ctx.Err()
	if err == nil {
		err = w.exec("BEGIN IMMEDIATE")
	}
	var changes []Change
	for _, statement := range request.schema {
		if err != nil {
			break
		}
		if err = w.exec(statement); err == nil {
			changes = append(changes, Change{Operation: Statement, Statement: statement, By: request.by})
		}
	}
	if err == nil {
		err = w.loadShapes(context.Background())
	}
	if err == nil {
		for name, shape := range w.shapes {
			if old, existed := previous[name]; shape.reason != "" && w.records(name) && (!existed || old.reason == "") {
				err = fmt.Errorf("%w: table %q: %s", ErrUnsupportedSchema, name, shape.reason)
				break
			}
		}
	}
	if err == nil {
		err = w.refuseUnsupportedObjects(context.Background())
	}
	if err == nil && w.hookErr != nil {
		err = w.hookErr
	}
	// Rows a schema statement changed behind its back (a cascade, a stat table)
	// follow the statements, so replay ends in the same rows.
	if err == nil {
		var rows []Change
		rows, err = w.netChanges(context.Background(), changes)
		changes = rows
	}
	revision, err := w.finish(changes, err)
	if err != nil {
		_ = w.loadShapes(context.Background())
	} else {
		w.refollow() // a table made or dropped, kitwork_columnar made
	}
	w.structureDue = w.structureDue || err == nil
	w.writeStructure()
	request.done <- writerOutcome{revision: revision, err: err}
}

// commitMaintain runs request.maintain on the writer's connection, outside any
// transaction of the writer's. The rows it changes are not followed one by one:
// when its work moved the schema or changed rows of a table that is not
// bookkeeping, one commit marks it (Barrier) and an anchor is written at that
// commit before the writer takes the next request, so a restore after it
// starts from that anchor and replay never crosses the change. Work that
// changed nothing else commits nothing.
func (w *writer) commitMaintain(request writerRequest) {
	if err := w.followFirst(); err != nil {
		request.done <- writerOutcome{err: err}
		return
	}
	defer w.refollow() // its work may have changed the schema or the engine's tables
	ctx := context.Background()
	before, err := w.schemaVersion(ctx)
	if err == nil {
		err = request.ctx.Err()
	}
	if err != nil {
		request.done <- writerOutcome{revision: w.revision, err: err}
		return
	}
	w.events, w.broken, w.by, w.hookErr = w.events[:0], nil, request.by, nil
	workErr := w.runMaintain(request)
	rows := ""
	for _, change := range w.events {
		if !bookkeeping(change.table) {
			rows = change.table
			break
		}
	}
	kinds := touchesColumnKinds(w.events)
	w.events, w.hookErr = w.events[:0], nil
	after, err := w.schemaVersion(ctx)
	w.structureDue = w.structureDue || kinds || err == nil && after != before
	if err != nil || after == before && rows == "" {
		w.writeStructure()
		request.done <- writerOutcome{revision: w.revision, err: errors.Join(workErr, err)}
		return
	}
	moved := fmt.Sprintf("schema_version %d", after)
	if after == before {
		moved = fmt.Sprintf("rows of %s", rows)
	}
	if err = w.loadShapes(ctx); err == nil {
		err = w.refuseUnsupportedObjects(ctx)
	}
	if errors.Is(err, ErrUnsupportedSchema) {
		w.refused = fmt.Errorf("%w; the history no longer follows the file", err)
	}
	if err != nil {
		w.writeStructure()
		request.done <- writerOutcome{revision: w.revision, err: errors.Join(workErr, err)}
		return
	}
	err = w.exec("BEGIN IMMEDIATE")
	revision, err := w.finish([]Change{{Operation: Barrier, Statement: moved, By: request.by}}, err)
	keeps := w.every
	if err == nil && !keeps {
		// a file that keeps no table's history has nothing a restore brings back: no anchor (KITWORK_KITDB.md câu 13.11)
		keeps, err = keepsHistoryOf(ctx, w.conn)
	}
	if err == nil && keeps {
		_, err = w.anchor(ctx)
	}
	w.writeStructure()
	request.done <- writerOutcome{revision: revision, err: errors.Join(workErr, err)}
}

func (w *writer) runMaintain(request writerRequest) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("kitdb: maintenance panicked: %v", recovered)
		}
	}()
	return request.maintain(w.conn)
}

// goroutineID is the id of the goroutine calling it, read from the first line of its stack ("goroutine 7
// [running]:"); -1 when it cannot be read.
func goroutineID() int64 {
	var buffer [64]byte
	line := buffer[:goruntime.Stack(buffer[:], false)]
	line = []byte(strings.TrimPrefix(string(line), "goroutine "))
	end := strings.IndexByte(string(line), ' ')
	if end < 0 {
		return -1
	}
	id, err := strconv.ParseInt(string(line[:end]), 10, 64)
	if err != nil {
		return -1
	}
	return id
}

// bookkeeping reports a table of the file's owner or of SQLite itself, whose
// rows Maintain may change without a Barrier: kitwork_* and _kitwork_* (the
// engine's: the declared kinds of columns, the runs of jobs), sqlite_*.
func bookkeeping(table string) bool {
	name := strings.ToLower(table)
	return strings.HasPrefix(name, "kitwork_") || strings.HasPrefix(name, "_kitwork_") || strings.HasPrefix(name, "sqlite_")
}

// schemaVersion is the file's schema cookie, which every change of its schema moves.
func (w *writer) schemaVersion(ctx context.Context) (int64, error) {
	var version int64
	err := w.conn.QueryRowContext(ctx, "PRAGMA schema_version").Scan(&version)
	return version, err
}

// finish writes the history row and commits, or rolls back on err. It returns
// the revision every successful request of the commit gets.
func (w *writer) finish(changes []Change, err error) (uint64, error) {
	var revision uint64
	var at int64
	var checksum []byte
	if err == nil && len(changes) > 0 {
		var encoded []byte
		encoded, err = encodeColumns(changes)
		if err == nil {
			revision = w.revision + 1
			at = time.Now().UTC().UnixNano()
			if at <= w.at {
				at = w.at + 1
			}
			checksum = chainChecksum(w.checksum, revision, at, encoded)
			err = w.exec(`INSERT INTO kitwork_history (rev, at, checksum, changes) VALUES (?, ?, ?, ?)`,
				int64(revision), at, checksum, encoded)
		}
	}
	if err == nil {
		err = w.exec("COMMIT")
	}
	if err != nil {
		_ = w.exec("ROLLBACK")
		if syncErr := w.resync(context.Background()); syncErr != nil {
			err = errors.Join(err, syncErr)
		}
		return 0, err
	}
	if revision != 0 {
		w.revision, w.at, w.checksum = revision, at, checksum
		w.committed.Store(revision)
	}
	return w.revision, nil
}

func (w *writer) runRequest(request writerRequest) (err error) {
	transaction := &Transaction{writer: w, ctx: request.ctx}
	defer func() {
		transaction.done = true
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("kitdb: write request panicked: %v", recovered)
		}
	}()
	return request.run(transaction)
}

type rowKey struct {
	table string
	rowID int64
}

type rowEntry struct {
	existed bool
	before  []any
	by      string
}

// netChanges folds the events of the commit into one change per row, after
// leading: the row as the commit left it is read back from its table, which
// is exact where the hook's TEXT values are not.
func (w *writer) netChanges(ctx context.Context, leading []Change) ([]Change, error) {
	entries := map[rowKey]*rowEntry{}
	var order []rowKey
	touch := func(key rowKey, existed bool, before []any, by string) {
		if entry := entries[key]; entry != nil {
			entry.by = by
			return
		}
		entries[key] = &rowEntry{existed: existed, before: before, by: by}
		order = append(order, key)
	}
	for _, change := range w.events {
		switch change.operation {
		case Insert:
			touch(rowKey{change.table, change.newRowID}, false, nil, change.by)
		case Delete:
			touch(rowKey{change.table, change.oldRowID}, true, change.before, change.by)
		case Update:
			touch(rowKey{change.table, change.oldRowID}, true, change.before, change.by)
			if change.newRowID != change.oldRowID {
				touch(rowKey{change.table, change.newRowID}, false, nil, change.by)
			}
		}
	}
	rowIDs := map[string][]int64{}
	var tables []string
	for _, key := range order {
		if _, seen := rowIDs[key.table]; !seen {
			tables = append(tables, key.table)
		}
		rowIDs[key.table] = append(rowIDs[key.table], key.rowID)
	}
	after := map[rowKey][]any{}
	columns := map[string][]string{}
	for _, table := range tables {
		if _, exists := w.shapes[table]; !exists && table != "sqlite_sequence" && table != "sqlite_stat1" {
			continue // dropped by a schema statement of this commit
		}
		names, err := w.readRows(ctx, table, rowIDs[table], after)
		if err != nil {
			return nil, err
		}
		columns[table] = names
	}
	changes := append([]Change(nil), leading...)
	for _, key := range order {
		entry := entries[key]
		values, exists := after[key]
		change := Change{Table: key.table, RowID: key.rowID, Before: entry.before, After: values, By: entry.by}
		switch {
		case entry.existed && exists:
			change.Operation, change.Columns = Update, columns[key.table]
			updates, err := w.columnUpdates(key.table, change.Columns, entry.before, values)
			if err != nil {
				return nil, err
			}
			// format 3: an update is the columns it changed, and its whole images are rebuilt by who reads them
			change.Updates, change.Before, change.After = updates, nil, nil
		case entry.existed:
			change.Operation, change.After, change.Columns = Delete, nil, columns[key.table]
		case exists:
			change.Operation, change.Before, change.Columns = Insert, nil, columns[key.table]
		default:
			continue
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// columnUpdates is an update of table in format 3: each column whose value before (the pre-update hook) and after
// (read back at commit) differ, with both, and, when one of the table's columns kept whole changed, every one of them.
// An update that changed nothing (SET x = x) has none, and is still a change of the history, as in format 1.
func (w *writer) columnUpdates(table string, columns []string, before, after []any) ([]ColumnUpdate, error) {
	if len(before) != len(after) || len(after) != len(columns) {
		return nil, fmt.Errorf("kitdb: update of %q read %d values before and %d after for %d columns", table, len(before), len(after), len(columns))
	}
	whole := w.whole[table]
	keepWhole := false
	for position, column := range columns {
		if whole[column] && !sameValue(before[position], after[position]) {
			keepWhole = true
			break
		}
	}
	var updates []ColumnUpdate
	for position, column := range columns {
		if !sameValue(before[position], after[position]) || keepWhole && whole[column] {
			updates = append(updates, ColumnUpdate{Position: position, Old: before[position], New: after[position]})
		}
	}
	return updates, nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// readRows reads rows back by rowid and returns the table's column names.
func (w *writer) readRows(ctx context.Context, table string, rowIDs []int64, after map[rowKey][]any) ([]string, error) {
	const chunk = 500
	var names []string
	for start := 0; start < len(rowIDs); start += chunk {
		part := rowIDs[start:min(start+chunk, len(rowIDs))]
		arguments := make([]any, len(part))
		for position, rowID := range part {
			arguments[position] = rowID
		}
		marks := strings.TrimSuffix(strings.Repeat("?, ", len(part)), ", ")
		rows, err := w.conn.QueryContext(ctx, `SELECT _rowid_, * FROM `+quoteIdentifier(table)+` WHERE _rowid_ IN (`+marks+`)`, arguments...)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		names = columns[1:]
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for position := range values {
				pointers[position] = &values[position]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				return nil, err
			}
			rowID, ok := values[0].(int64)
			if !ok {
				rows.Close()
				return nil, fmt.Errorf("kitdb: table %q returned a rowid of type %T", table, values[0])
			}
			after[rowKey{table, rowID}] = values[1:]
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return names, nil
}

// Transaction is a write request's handle on the writer's transaction. It is
// valid only while the request runs. Rows from Query must be closed before
// the next statement.
type Transaction struct {
	writer *writer
	ctx    context.Context
	done   bool
}

// Exec runs one data statement (INSERT, UPDATE, DELETE, REPLACE, or WITH …
// one of them). A statement that fails leaves nothing, whatever its conflict
// clause: each statement runs in a savepoint of its own.
//
// A text argument holding the NUL character is refused: TEXT never holds one,
// as PostgreSQL's text never does, because the pre-update hook reads a TEXT
// value up to its first NUL and the row's Before in the history would be cut
// there. A value with NUL bytes is a BLOB ([]byte).
func (transaction *Transaction) Exec(query string, arguments ...any) (sql.Result, error) {
	if transaction.done {
		return nil, fmt.Errorf("kitdb: transaction used after its request returned")
	}
	for position, argument := range arguments {
		if text, ok := argument.(string); ok && strings.IndexByte(text, 0) >= 0 {
			return nil, fmt.Errorf("%w: argument %d is a text holding the NUL character", ErrStatementRefused, position+1)
		}
	}
	shape, err := classifyStatement(query)
	if err != nil {
		return nil, err
	}
	switch shape.kind {
	case statementSchema:
		return nil, fmt.Errorf("%w: schema statements go through Migrate", ErrStatementRefused)
	case statementRead:
		return nil, fmt.Errorf("%w: use Query to read", ErrStatementRefused)
	}
	w := transaction.writer
	mark := len(w.events)
	if err := w.exec("SAVEPOINT kitwork_statement"); err != nil {
		w.broken = err
		return nil, err
	}
	result, err := w.conn.ExecContext(transaction.ctx, query, arguments...)
	if err != nil {
		if rollbackErr := w.exec("ROLLBACK TO kitwork_statement"); rollbackErr != nil {
			w.broken = errors.Join(err, rollbackErr)
			return nil, w.broken
		}
		w.events = w.events[:mark]
	}
	if releaseErr := w.exec("RELEASE kitwork_statement"); releaseErr != nil {
		w.broken = errors.Join(err, releaseErr)
		return nil, w.broken
	}
	return result, err
}

// Query runs one read statement inside the write transaction, seeing the
// request's own changes.
func (transaction *Transaction) Query(query string, arguments ...any) (*sql.Rows, error) {
	if transaction.done {
		return nil, fmt.Errorf("kitdb: transaction used after its request returned")
	}
	shape, err := classifyStatement(query)
	if err != nil {
		return nil, err
	}
	if shape.kind != statementRead {
		return nil, fmt.Errorf("%w: use Exec to change rows", ErrStatementRefused)
	}
	return transaction.writer.conn.QueryContext(transaction.ctx, query, arguments...)
}

// TablesSchema is kitwork_tables (KITWORK_KITDB.md câu 13.11): a table's history setting (history: on when 1) and
// whether the writer records its changes (recorded): 0 when it does not, else the revision from which every change of
// the table is in the history, the commit of its Tracked change (1 for a table recorded from the file's first
// commit), which tells a table whose recording starts again and how far back its history reaches (TableHistory). A file without it, whose history has commits, was written before the table and keeps the history of every
// table it has; one made from a structure file always has it (kitdb/gateway's LoadStructure), so its tables have the
// history the structure says.
const TablesSchema = `CREATE TABLE IF NOT EXISTS kitwork_tables (table_name TEXT PRIMARY KEY, history INTEGER NOT NULL DEFAULT 0, recorded INTEGER NOT NULL DEFAULT 0) STRICT`

// records reports a table whose changes the history records: every table with Options.HistoryOfEveryTable; else the
// engine's own (kitwork_*, _kitwork_*: the kinds of columns, the copies declared, the history settings), SQLite's
// own while some table is followed (a sequence or a statistic replay needs), and the tables followed.
func (w *writer) records(table string) bool {
	if w.every {
		return true
	}
	name := strings.ToLower(table)
	switch {
	case strings.HasPrefix(name, "kitwork_"), strings.HasPrefix(name, "_kitwork_"):
		return true
	case strings.HasPrefix(name, "sqlite_"):
		return len(w.tracked) > 0
	}
	return w.tracked[name]
}

// touchesFollowed reports a change of what decides the tables followed among the events: kitwork_tables,
// kitwork_columnar, kitwork_columns (a search_weight).
func touchesFollowed(events []event) bool {
	for _, change := range events {
		switch strings.ToLower(change.table) {
		case "kitwork_tables", "kitwork_columnar", "kitwork_columns":
			return true
		}
	}
	return false
}

// hasTable reports a table of the file's main schema, read on the writer's connection.
func (w *writer) hasTable(ctx context.Context, name string) (bool, error) {
	var count int
	err := w.conn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count > 0, err
}

// readNames reads one column of names, lower case, into names.
func (w *writer) readNames(ctx context.Context, names map[string]bool, query string) error {
	rows, err := w.conn.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		names[strings.ToLower(name)] = true
	}
	return rows.Err()
}

// followed reads the tables the file says the history follows: history on in kitwork_tables, a columns copy declared
// in kitwork_columnar, a search_weight in kitwork_columns, and the tables of Options.Search (whole); lower case, of
// the tables the file has.
func (w *writer) followed(ctx context.Context) (map[string]bool, error) {
	wanted := map[string]bool{}
	for table := range w.whole {
		wanted[strings.ToLower(table)] = true
	}
	sources := []struct{ table, query string }{
		{"kitwork_tables", `SELECT table_name FROM kitwork_tables WHERE history = 1`},
		{"kitwork_columnar", `SELECT table_name FROM kitwork_columnar`},
		{"kitwork_columns", `SELECT table_name FROM kitwork_columns WHERE search_weight > 0`},
	}
	for _, source := range sources {
		held, err := w.hasTable(ctx, source.table)
		if err != nil {
			return nil, err
		}
		if !held {
			continue
		}
		if source.table == "kitwork_columns" {
			var weights int
			if err := w.conn.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('kitwork_columns') WHERE name = 'search_weight'`).Scan(&weights); err != nil {
				return nil, err
			}
			if weights == 0 {
				continue
			}
		}
		if err := w.readNames(ctx, wanted, source.query); err != nil {
			return nil, err
		}
	}
	tables := map[string]bool{}
	for name := range w.shapes {
		if lower := strings.ToLower(name); wanted[lower] && !bookkeeping(lower) {
			tables[lower] = true
		}
	}
	return tables, nil
}

// adopt gives a file without kitwork_tables one as it opens, unless Options.HistoryOfEveryTable. The tables a file
// already has were made before 07/11/2026, when the history followed every table, or outside KitDB, where a writer
// would have followed them: each keeps its history, written on and recorded, with no Tracked change since nothing of
// it went unrecorded while a writer held the file. A new file has no table yet, and its kitwork_tables starts empty:
// a table made in it has no history until one is set.
func (w *writer) adopt(ctx context.Context) error {
	if w.every {
		return nil
	}
	held, err := w.hasTable(ctx, "kitwork_tables")
	if err != nil || held {
		return err
	}
	err = w.exec("BEGIN IMMEDIATE")
	if err == nil {
		err = w.exec(TablesSchema)
	}
	for name := range w.shapes {
		if err != nil {
			break
		}
		if !bookkeeping(strings.ToLower(name)) {
			err = w.exec(`INSERT INTO kitwork_tables (table_name, history, recorded) VALUES (?, 1, 1) ON CONFLICT DO NOTHING`, name)
		}
	}
	if err == nil {
		err = w.exec("COMMIT")
	}
	w.events = w.events[:0] // the file's own settings, not changes to replay
	if err != nil {
		_ = w.exec("ROLLBACK")
		return err
	}
	return w.loadShapes(ctx)
}

// follow brings the tables the history records to what the file says (followed). A table whose recording starts gets
// a Tracked change, in a commit of its own written before the next, so an index of it built before goes stale; its
// recorded mark in kitwork_tables is set, or taken off for a table no longer followed. It runs at open and before the
// first commit after one that changed what decides it (refollow).
func (w *writer) follow(ctx context.Context) error {
	if w.every {
		w.followDue = false
		return nil
	}
	held, err := w.hasTable(ctx, "kitwork_tables")
	if err != nil {
		return err
	}
	wanted, err := w.followed(ctx)
	if err != nil {
		return err
	}
	recorded := map[string]bool{}
	if held {
		if err := w.readNames(ctx, recorded, `SELECT table_name FROM kitwork_tables WHERE recorded > 0`); err != nil {
			return err
		}
	}
	written := map[string]string{} // a table's name as the file writes it, by lower case
	for name := range w.shapes {
		written[strings.ToLower(name)] = name
	}
	var started, stopped []string
	for name := range wanted {
		if !recorded[name] {
			started = append(started, written[name])
		}
	}
	for name := range recorded {
		if !wanted[name] {
			stopped = append(stopped, name)
		}
	}
	if len(started) == 0 && len(stopped) == 0 {
		w.tracked, w.followDue = wanted, false
		return nil
	}
	slices.Sort(started)
	err = w.exec("BEGIN IMMEDIATE")
	if err == nil {
		err = w.exec(TablesSchema)
	}
	var changes []Change
	from := w.revision + 1 // the revision this commit takes: it has a change for each table started
	for _, name := range started {
		if err != nil {
			break
		}
		err = w.exec(`INSERT INTO kitwork_tables (table_name, recorded) VALUES (?, ?) ON CONFLICT (table_name) DO UPDATE SET recorded = excluded.recorded`, name, int64(from))
		changes = append(changes, Change{Operation: Tracked, Table: name, Statement: "the history records " + name + " from here"})
	}
	for _, name := range stopped {
		if err != nil {
			break
		}
		err = w.exec(`UPDATE kitwork_tables SET recorded = 0 WHERE lower(table_name) = ?`, name)
	}
	w.events = w.events[:0] // kitwork_tables' rows are this commit's own, not changes to replay
	if _, err = w.finish(changes, err); err != nil {
		return err
	}
	w.tracked, w.followDue = wanted, false
	return nil
}

// refollow runs follow after a commit that changed what decides it; when it fails, the next commit runs it first.
func (w *writer) refollow() {
	if err := w.follow(context.Background()); err != nil {
		w.followDue = true
		return
	}
	w.tellStarted()
}

// followFirst runs follow before a commit when the one after the last change of what decides it failed.
func (w *writer) followFirst() error {
	if !w.followDue {
		return nil
	}
	if err := w.follow(context.Background()); err != nil {
		return fmt.Errorf("kitdb: the tables the history follows were not read: %w", err)
	}
	w.tellStarted()
	return nil
}

// tellStarted tells the database that what the history follows may have changed (started). Its failure leaves the
// file unanchored until Retain or the next Open, as a crash between the commit and the anchor would; the commit stands.
func (w *writer) tellStarted() {
	if w.started != nil && !w.every {
		_ = w.started(context.Background(), w.conn)
	}
}
