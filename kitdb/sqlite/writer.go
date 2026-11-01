package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	modernc "modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

type writerRequest struct {
	ctx    context.Context
	by     string
	run    func(*Transaction) error
	schema []string
	done   chan writerOutcome
}

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

	events   []event
	hookErr  error
	broken   error
	by       string
	shapes   map[string]tableShape
	revision uint64
	at       int64
	checksum []byte
}

func openWriter(ctx context.Context, name string, options Options) (*writer, error) {
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
		finished: make(chan struct{}), maximumBatch: options.MaximumBatch,
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
	if data.DatabaseName != "main" || data.TableName == historyTable || w.hookErr != nil {
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
		if first.schema != nil {
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
				if request.schema != nil {
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

func (w *writer) exec(query string, arguments ...any) error {
	_, err := w.conn.ExecContext(context.Background(), query, arguments...)
	return err
}

func (w *writer) commitBatch(batch []writerRequest) {
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
	revision, err := w.finish(changes, err)
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
			if old, existed := previous[name]; shape.reason != "" && (!existed || old.reason == "") {
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
	}
	request.done <- writerOutcome{revision: revision, err: err}
}

// finish writes the history row and commits, or rolls back on err. It returns
// the revision every successful request of the commit gets.
func (w *writer) finish(changes []Change, err error) (uint64, error) {
	var revision uint64
	var at int64
	var checksum []byte
	if err == nil && len(changes) > 0 {
		var encoded []byte
		encoded, err = encodeChanges(changes)
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
		case entry.existed:
			change.Operation, change.After = Delete, nil
		case exists:
			change.Operation, change.Before, change.Columns = Insert, nil, columns[key.table]
		default:
			continue
		}
		changes = append(changes, change)
	}
	return changes, nil
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
func (transaction *Transaction) Exec(query string, arguments ...any) (sql.Result, error) {
	if transaction.done {
		return nil, fmt.Errorf("kitdb: transaction used after its request returned")
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
