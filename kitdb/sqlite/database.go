// Package sqlite is KitDB on SQLite. SQLite (modernc.org/sqlite, pure Go)
// keeps the rows of one database file; this package keeps what KitDB adds
// above them:
//
//   - one writer per file, which gathers concurrent requests into one SQLite
//     transaction, each request in its own savepoint;
//   - a history row in kitwork_history for every commit, written inside the
//     same transaction as the changes it records, chained by checksum;
//   - readers on their own connections, each knowing the revision its
//     snapshot contains;
//   - anchors and replay, which restore the database as it was at any
//     committed moment.
//
// Every write must go through Database: a write made by another process or
// tool is not in the history. The design is docs/KITWORK_KITDB.md of the
// Kitwork repository.
package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/kitwork/engine/search"
	_ "modernc.org/sqlite"
)

var (
	// ErrLocked means another Database, in this or another process, owns the file.
	ErrLocked = errors.New("kitdb: database already has a writer")
	// ErrClosed means the Database was closed.
	ErrClosed = errors.New("kitdb: database is closed")
	// ErrStatementRefused means a statement would escape the writer's
	// transaction or is not allowed where it was used.
	ErrStatementRefused = errors.New("kitdb: statement refused")
	// ErrUnsupportedSchema means a table shape or schema object whose changes
	// the history cannot record.
	ErrUnsupportedSchema = errors.New("kitdb: schema is not supported by the history")
	// ErrReentrant means a write asked for on the goroutine of the writer, from inside one of its requests: it
	// would wait for the request it is part of.
	ErrReentrant = errors.New("kitdb: a write from inside a write of the same database")
	// ErrCorruptHistory means kitwork_history does not decode or does not chain.
	ErrCorruptHistory = errors.New("kitdb: history is corrupt")
	// ErrNoAnchor means no anchor is old enough for the requested moment.
	ErrNoAnchor = errors.New("kitdb: no anchor at or before the moment")
	// ErrPruned means the moment is before the history Retain kept.
	ErrPruned = errors.New("kitdb: the history no longer reaches the moment")
)

// Options bound one Database. Zero values take the defaults.
type Options struct {
	// MaximumBatch caps the requests one SQLite transaction gathers (100).
	MaximumBatch int
	// HistoryOfEveryTable records the changes of every table, as KitDB did before 07/11/2026 (the old engine, the
	// tools). Without it the history records a table whose history is on (kitwork_tables, SetHistory), a table an
	// index is built from (Options.Search, a columns copy of kitwork_columnar, a search_weight of kitwork_columns) and
	// the engine's own tables; the rows of any other table change with no trace (KITWORK_KITDB.md câu 13.11). A file
	// whose history has commits and no kitwork_tables, written before, keeps the history of every table it has.
	HistoryOfEveryTable bool
	// Queue bounds the requests waiting for the writer (4096).
	Queue int
	// Readers bounds concurrent read connections (8).
	Readers int
	// PageSize is the page size of a new file (16384).
	PageSize int
	// Search lists the tables Snapshot.Search can rank.
	Search []SearchTable
	// SearchFold is how many changes a search index keeps in memory before a
	// background fold writes them to disk (1024).
	SearchFold int
	// Structure, when set, is called on the writer's connection, between
	// commits, once the database opens and after every commit that changed the
	// file's schema or its kitwork_columns: the owner writes the file's
	// structure beside it (<file>.sql, kitdb/gateway's Structure). What it does
	// with an error is its own.
	Structure func(ctx context.Context, conn *sql.Conn)
}

func (options Options) normalized() (Options, error) {
	if options.MaximumBatch == 0 {
		options.MaximumBatch = 100
	}
	if options.Queue == 0 {
		options.Queue = 4096
	}
	if options.Readers == 0 {
		options.Readers = 8
	}
	if options.PageSize == 0 {
		options.PageSize = 16384
	}
	if options.SearchFold == 0 {
		options.SearchFold = 1024
	}
	if options.MaximumBatch < 1 || options.Queue < 1 || options.Readers < 1 || options.SearchFold < 1 {
		return Options{}, fmt.Errorf("kitdb: options must be positive")
	}
	if options.PageSize < 512 || options.PageSize > 65536 || options.PageSize&(options.PageSize-1) != 0 {
		return Options{}, fmt.Errorf("kitdb: page size %d is not a power of two in 512..65536", options.PageSize)
	}
	return options, nil
}

// Database is one SQLite file served by one writer and a pool of readers.
type Database struct {
	path        string
	lock        *fileLock
	writer      *writer
	readers     *sql.DB
	container   *search.Container
	searchables map[string]*searchable
	closed      atomic.Bool

	snapshots sync.Mutex
	open      map[uint64]int

	// anchoring lets one anchor or one Retain of the anchors run at a time; anchorFiles is held for reading while
	// Restore reads anchor files and for writing while a fold or Retain changes or removes them.
	anchoring   sync.Mutex
	anchorFiles sync.RWMutex

	// anchored is the revision of the newest anchor, written or being written in the background. background counts
	// the anchors written in the background (anchorLater), which stopBackground cancels as the file closes;
	// backgroundErr is the first of them that failed, which Close returns.
	anchored          atomic.Uint64
	background        sync.WaitGroup
	backgroundContext context.Context
	stopBackground    context.CancelFunc
	backgroundMutex   sync.Mutex
	backgroundErr     error
}

func dataSourceName(path string, parameters ...string) string {
	name := "file:" + filepath.ToSlash(path) + "?_pragma=busy_timeout(10000)"
	for _, parameter := range parameters {
		name += "&" + parameter
	}
	return name
}

// Open opens or creates the database at path and takes its writer lock
// (path + ".lock"). It writes no anchor because the file opens (KITWORK_KITDB.md
// câu 13.11, 07/11/2026): an old file opens without copying itself, and a file
// that keeps no table's history has no anchor at all. A file that keeps one is
// anchored by Retain, at a Barrier, as a table's history starts (in the
// background, anchorWhenStarted) and by Anchor; an anchor a Barrier lacks
// (Maintain stopped between the two) is written here, and one a start lacks
// (the file closed while it was written) is started here. With
// Options.HistoryOfEveryTable a file is anchored as it opens, as before: a new
// walSizeLimit is the most bytes the WAL keeps once it restarts (the writer's PRAGMA journal_size_limit). A long read,
// an anchor's pages or a Backup, holds the checkpoint back, so the WAL grows by every commit meanwhile (2.46 GB during
// shopping's anchor, 08/11/2026), and SQLite reuses the file at that size, never shrinking it. With the limit the
// file is cut back as the WAL restarts, the commit after the checkpoint that follows the read. 64 MiB is above what
// the WAL holds between two automatic checkpoints (1,000 pages of 16 KiB), so an ordinary WAL is never cut.
var walSizeLimit int64 = 64 << 20

// one at revision 0, an existing one without anchors.
func Open(ctx context.Context, path string, options Options) (*Database, error) {
	options, err := options.normalized()
	if err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb: open context is nil")
	}
	path = filepath.Clean(path)
	lock, err := lockFile(path + ".lock")
	if err != nil {
		return nil, fmt.Errorf("kitdb: lock %q: %w", path, err)
	}
	database := &Database{path: path, lock: lock, searchables: map[string]*searchable{}, open: map[uint64]int{}}
	database.backgroundContext, database.stopBackground = context.WithCancel(context.Background())
	opened := false
	defer func() {
		if !opened {
			database.release()
		}
	}()
	_, statErr := os.Stat(path)
	created := errors.Is(statErr, os.ErrNotExist)

	database.writer, err = openWriter(ctx, dataSourceName(path,
		"_pragma=page_size("+strconv.Itoa(options.PageSize)+")",
		"_pragma=journal_mode(WAL)", "_pragma=synchronous(FULL)", "_pragma=foreign_keys(1)",
		"_pragma=journal_size_limit("+strconv.FormatInt(walSizeLimit, 10)+")",
	), options, database.Anchor, func(ctx context.Context, conn *sql.Conn) error { return database.anchorWhenStarted(ctx, conn) })
	if err != nil {
		return nil, err
	}
	database.readers, err = sql.Open("sqlite", dataSourceName(path, "mode=ro", "_pragma=query_only(1)"))
	if err != nil {
		return nil, err
	}
	database.readers.SetMaxOpenConns(options.Readers)
	database.readers.SetMaxIdleConns(options.Readers)
	if err := database.cleanAnchorDirectory(); err != nil {
		return nil, err
	}
	if anchors, err := database.anchors(); err != nil {
		return nil, err
	} else if len(anchors) > 0 {
		database.anchored.Store(anchors[len(anchors)-1].revision)
	}
	none, barrier, err := database.unanchored(ctx)
	if err != nil {
		return nil, err
	}
	anchorNeeded := barrier
	if options.HistoryOfEveryTable {
		anchorNeeded = created || none || barrier
	} else if barrier {
		if anchorNeeded, err = database.keepsHistory(ctx, database.readers); err != nil {
			return nil, err
		}
	}
	if anchorNeeded {
		if _, err := database.Anchor(ctx); err != nil {
			return nil, err
		}
	} else if !options.HistoryOfEveryTable {
		// the anchor of a table's start the file closed before writing
		if err := database.anchorWhenStarted(ctx, database.readers); err != nil {
			return nil, err
		}
	}
	if len(options.Search) > 0 {
		// The indexes live in one file beside the database. A directory there is
		// the earlier layout: derived data, rebuilt into the file.
		searchPath := path + ".search"
		if info, err := os.Stat(searchPath); err == nil && info.IsDir() {
			if err := os.RemoveAll(searchPath); err != nil {
				return nil, err
			}
		}
		if database.container, err = search.OpenContainer(ctx, searchPath); err != nil {
			return nil, err
		}
	}
	for _, table := range options.Search {
		if database.searchables[table.Table] != nil {
			return nil, fmt.Errorf("kitdb: table %q is searchable twice", table.Table)
		}
		searchable, err := newSearchable(database, table, options.SearchFold)
		if err != nil {
			return nil, err
		}
		database.searchables[table.Table] = searchable
	}
	opened = true
	if options.Structure != nil {
		// once now, on the writer's goroutine; after that the writer calls it itself
		if _, err := database.Maintain(ctx, "", func(conn *sql.Conn) error {
			options.Structure(ctx, conn)
			return nil
		}); err != nil {
			_ = database.Close()
			return nil, err
		}
	}
	return database, nil
}

func (database *Database) ensureOpen() error {
	if database == nil || database.closed.Load() {
		return ErrClosed
	}
	return nil
}

// Path is the database file.
func (database *Database) Path() string { return database.path }

// Revision is the last committed revision.
func (database *Database) Revision() uint64 { return database.writer.committed.Load() }

// Write runs request inside the writer's next group transaction and returns the
// revision that made it durable. A request that returns an error, panics, or
// touches what the history cannot record leaves no trace; the other requests of
// its group are unaffected. by names who wrote, and is kept with every change.
func (database *Database) Write(ctx context.Context, by string, request func(*Transaction) error) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if ctx == nil || request == nil {
		return 0, fmt.Errorf("kitdb: write needs a context and a request")
	}
	return database.writer.submit(ctx, writerRequest{ctx: ctx, by: by, run: request})
}

// SetHistory turns the history of table on or off (kitwork_tables, KITWORK_KITDB.md câu 13.11 (a)) in a commit of
// its own: from the next commit the writer records the table's changes, after a Tracked change, or no longer records
// them unless an index is built from the table. A table the file does not have is refused, and so is turning on the
// history of one it cannot record (WITHOUT ROWID, generated columns), which a table without history may be. A file
// without kitwork_tables (open with Options.HistoryOfEveryTable) gets it first, every table it has written with its
// history on, as recorded all along, so setting one table's history leaves the others' as they were.
func (database *Database) SetHistory(ctx context.Context, by, table string, on bool) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	var held int
	if err := database.readers.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_tables'`).Scan(&held); err != nil {
		return 0, err
	}
	adopting := held == 0
	if held == 0 {
		if _, err := database.Migrate(ctx, by, TablesSchema); err != nil {
			return 0, err
		}
	}
	return database.Write(ctx, by, func(transaction *Transaction) error {
		if adopting {
			for name := range transaction.writer.shapes {
				if bookkeeping(strings.ToLower(name)) {
					continue
				}
				if _, err := transaction.Exec(`INSERT INTO kitwork_tables (table_name, history, recorded) VALUES (?, 1, 1) ON CONFLICT DO NOTHING`, name); err != nil {
					return err
				}
			}
		}
		return transaction.SetHistory(table, on)
	})
}

// SetHistory writes in the transaction the history setting of table (kitwork_tables), as Database.SetHistory does
// in a commit of its own: kitdb/gateway sets a table's history with the statement that makes it, or with the file
// made from a structure file. The file must have kitwork_tables (TablesSchema).
func (transaction *Transaction) SetHistory(table string, on bool) error {
	rows, err := transaction.Query(`SELECT name FROM sqlite_schema WHERE type = 'table' AND name = ? COLLATE NOCASE`, table)
	if err != nil {
		return err
	}
	var name string
	found := rows.Next() && rows.Scan(&name) == nil
	rows.Close()
	if !found || bookkeeping(strings.ToLower(name)) {
		return fmt.Errorf("kitdb: the file has no table %q whose history can be set", table)
	}
	setting := 0
	if on {
		setting = 1
		if reason := transaction.writer.shapes[name].reason; reason != "" {
			return fmt.Errorf("%w: table %q: %s", ErrUnsupportedSchema, name, reason)
		}
	}
	_, err = transaction.Exec(`INSERT INTO kitwork_tables (table_name, history) VALUES (?, ?) ON CONFLICT (table_name) DO UPDATE SET history = excluded.history`, name, setting)
	return err
}

// Migrate runs schema statements (CREATE, ALTER, DROP, ANALYZE, REINDEX) as one
// commit of their own. The history keeps them as written, and replay runs them
// again; tables they create must keep rowids and have no generated columns.
func (database *Database) Migrate(ctx context.Context, by string, statements ...string) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if ctx == nil || len(statements) == 0 {
		return 0, fmt.Errorf("kitdb: migrate needs a context and statements")
	}
	for _, statement := range statements {
		shape, err := classifyStatement(statement)
		if err != nil {
			return 0, err
		}
		if shape.kind != statementSchema {
			return 0, fmt.Errorf("%w: Migrate runs schema statements only", ErrStatementRefused)
		}
	}
	return database.writer.submit(ctx, writerRequest{ctx: ctx, by: by, schema: statements})
}

// Maintain runs work on the writer's connection, between commits and outside
// any transaction of the writer's: what the owner of the file changes in it
// itself, a migration that adds a column or rebuilds a table, its own tables.
// work may open and commit transactions of its own and must end outside one.
// The history does not follow the rows work changes. When work moves the
// schema, or changes rows of a table that is not bookkeeping (kitwork_*,
// _kitwork_*, sqlite_*), Maintain commits a Barrier and writes an anchor at it
// before the writer takes another request, and returns that revision; work
// that changed nothing else commits nothing and returns the last revision. A
// schema work leaves with what the history cannot record (a trigger, a
// virtual table) fails Maintain and every later write.
func (database *Database) Maintain(ctx context.Context, by string, work func(*sql.Conn) error) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if ctx == nil || work == nil {
		return 0, fmt.Errorf("kitdb: maintain needs a context and work")
	}
	return database.writer.submit(ctx, writerRequest{ctx: ctx, by: by, maintain: work})
}

// unanchored reports what Open anchors for: a file with no anchor (none), and a
// Barrier after the newest anchor (barrier: the anchor Maintain writes did not
// get written).
func (database *Database) unanchored(ctx context.Context) (none, barrier bool, err error) {
	anchors, err := database.anchors()
	if err != nil || len(anchors) == 0 {
		return err == nil, false, err
	}
	// a commit holding a Barrier is the one written in barrierFormat, columnsBarrierFormat or lightBarrierFormat; one
	// that does not decode is for VerifyHistory and Restore to report
	var barriers int64
	err = database.readers.QueryRowContext(ctx, `SELECT count(*) FROM kitwork_history WHERE rev > ? AND substr(changes, 1, 1) IN (?, ?, ?)`,
		int64(anchors[len(anchors)-1].revision), []byte{barrierFormat}, []byte{columnsBarrierFormat}, []byte{lightBarrierFormat}).Scan(&barriers)
	return false, barriers > 0, err
}

// keepsHistory reports a file whose history keeps some table's (kitwork_tables: on and recorded), which a restore can
// bring back and anchors are written for; with Options.HistoryOfEveryTable, or without kitwork_tables (a file of
// before), every table's.
func (database *Database) keepsHistory(ctx context.Context, querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}) (bool, error) {
	if database.writer.every {
		return true, nil
	}
	return keepsHistoryOf(ctx, querier)
}

func keepsHistoryOf(ctx context.Context, querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}) (bool, error) {
	rows, err := querier.QueryContext(ctx, `SELECT (SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_tables')`)
	if err != nil {
		return false, err
	}
	held := 0
	if rows.Next() {
		err = rows.Scan(&held)
	}
	rows.Close()
	if err != nil || held == 0 {
		return err == nil, err
	}
	rows, err = querier.QueryContext(ctx, `SELECT EXISTS (SELECT 1 FROM kitwork_tables WHERE history = 1 AND recorded > 0)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	keeps := false
	if rows.Next() {
		err = rows.Scan(&keeps)
	}
	return keeps, errors.Join(err, rows.Err())
}

// Snapshot is one read transaction. Revision is the last commit it sees: rows
// and history are written together, so what it reads is exactly that revision.
type Snapshot struct {
	Revision uint64
	ctx      context.Context
	tx       *sql.Tx
	database *Database
}

// Query runs one read statement inside the snapshot.
func (snapshot *Snapshot) Query(query string, arguments ...any) (*sql.Rows, error) {
	shape, err := classifyStatement(query)
	if err != nil {
		return nil, err
	}
	if shape.kind != statementRead {
		return nil, fmt.Errorf("%w: a snapshot only reads", ErrStatementRefused)
	}
	return snapshot.tx.QueryContext(snapshot.ctx, query, arguments...)
}

// Read runs read inside one snapshot on a reader connection.
func (database *Database) Read(ctx context.Context, read func(*Snapshot) error) error {
	if err := database.ensureOpen(); err != nil {
		return err
	}
	tx, err := database.readers.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var revision int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0) FROM kitwork_history`).Scan(&revision); err != nil {
		return err
	}
	database.trackSnapshot(uint64(revision), 1)
	defer database.trackSnapshot(uint64(revision), -1)
	return read(&Snapshot{Revision: uint64(revision), ctx: ctx, tx: tx, database: database})
}

func (database *Database) trackSnapshot(revision uint64, delta int) {
	database.snapshots.Lock()
	defer database.snapshots.Unlock()
	database.open[revision] += delta
	if database.open[revision] <= 0 {
		delete(database.open, revision)
	}
}

// oldestSnapshot is the lowest revision an open Read still holds.
func (database *Database) oldestSnapshot() (uint64, bool) {
	database.snapshots.Lock()
	defer database.snapshots.Unlock()
	var oldest uint64
	found := false
	for revision := range database.open {
		if !found || revision < oldest {
			oldest, found = revision, true
		}
	}
	return oldest, found
}

// Close waits for queued writes, then releases the file. An anchor being written in the background is stopped (the
// next Open writes it again); one that failed is returned. It is safe to call twice.
func (database *Database) Close() error {
	if database == nil || !database.closed.CompareAndSwap(false, true) {
		return nil
	}
	return database.release()
}

func (database *Database) release() error {
	var errs []error
	for _, searchable := range database.searchables {
		errs = append(errs, searchable.close())
	}
	if database.container != nil {
		errs = append(errs, database.container.Close())
	}
	if database.writer != nil {
		database.writer.stop()
	}
	// after the writer, which starts them
	database.stopBackground()
	database.background.Wait()
	database.backgroundMutex.Lock()
	errs = append(errs, database.backgroundErr)
	database.backgroundMutex.Unlock()
	if database.readers != nil {
		errs = append(errs, database.readers.Close())
	}
	// The writer's connection last: closing the last connection of the file, SQLite checkpoints the whole WAL into it
	// and removes the WAL, so a file closed is whole by itself (08/11/2026). A read-only connection closed last does
	// neither: the WAL stayed beside the file, 2.64 GB on shopping's copy, and a copy of kitwork.db alone lacked it.
	if database.writer != nil {
		errs = append(errs, database.writer.closeConnection())
	}
	if database.lock != nil {
		errs = append(errs, database.lock.close())
	}
	return errors.Join(errs...)
}
