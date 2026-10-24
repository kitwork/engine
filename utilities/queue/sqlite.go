package queue

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// The runs on SQLite: the database of a name of the host's catalogue (app.databases({ jobs: { sqlite: path } })),
// which queue.database(db) or schedule.database(db) chooses (KITWORK_QUEUE.md mục 2, 24/10/2026). The
// statements are the store's own, the same as on the app's KitDB. Two things make a claim, a settle, a cancel,
// a replay and a numbering one step there:
//
//   - the file's gate (FileGate): the store's writes of one file in this process run one at a time, whichever
//     pool they go through. They never wait for each other on SQLite's lock, so never through its busy timeout,
//     and what keeps two of them apart is not the VFS's locking of connections of one process.
//   - one transaction begun IMMEDIATE for each write: the app's ORM writes the same file through the same pool
//     and holds no gate of it, so SQLite's own write lock, taken at the transaction's start, is what keeps the
//     app's statements from writing between a check and the change after it; and a write that fails half way
//     leaves nothing. A transaction begun DEFERRED (BEGIN) takes the lock at its first write, after its reads:
//     a statement of the app that wrote in between makes it fail (SQLITE_BUSY), whatever the busy timeout.
//
// When another writer holds the file's write lock (an app statement through the same pool, another process),
// a write with no Done (a request's send) waits the pool's busy timeout, then gives up (SQLITE_BUSY). A write
// with Done, the worker's (a claim, a settle, a heartbeat), waits in short tries (Try, the busy timeout of its
// BEGIN alone) until the lock is free or Done closes, so a settle is not given up while the worker runs. Each
// try holds the gate at most Try, and a try after the first looks at Done once it has the gate: stopping the
// worker or closing the app costs each waiting write one short try, and a request's write waits for one short
// try of each worker write ahead of it, then makes its own.

// Immediate is a Database over a SQLite pool: a read runs on the pool (each of the store's reads is one
// statement), a write is one transaction begun IMMEDIATE on a connection of it, with Gate held around it.
type Immediate struct {
	Pool *sql.DB
	// Gate is the file's gate (FileGate); nil holds none.
	Gate *sync.Mutex
	// Done, when set, makes a write begin again each time its try found another writer's lock of the file,
	// until Done closes; nil tries once, with the pool's busy timeout.
	Done <-chan struct{}
	// Try is how long one try of a write with Done waits for another writer's lock (the busy timeout of its
	// BEGIN); 0 is the pool's own.
	Try time.Duration
	// Waiting, when set, is told how long a write with Done has waited so far, once each WaitingEvery of
	// waiting (each try for 0).
	Waiting      func(waited time.Duration)
	WaitingEvery time.Duration
}

// stopped is a try of a write with Done that found Done closed once it had the gate.
var stopped = errors.New("queue: the write's Done closed")

// fileGates is the gate of each SQLite file the runs are written in, by the file's clean absolute path. A
// gate is kept for the life of the process: one per file of the host's catalogue.
var fileGates = struct {
	sync.Mutex
	files map[string]*sync.Mutex
}{files: map[string]*sync.Mutex{}}

// FileGate is the gate of the SQLite file at path, the same for every store and pool of this process that
// writes the runs there.
func FileGate(path string) *sync.Mutex {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	fileGates.Lock()
	defer fileGates.Unlock()
	gate := fileGates.files[path]
	if gate == nil {
		gate = &sync.Mutex{}
		fileGates.files[path] = gate
	}
	return gate
}

func (i Immediate) Read(fn func(db SQL) error) error { return fn(i.Pool) }

func (i Immediate) Write(fn func(db SQL) error) error {
	start := time.Now()
	var last error
	var said time.Duration
	for again := false; ; again = true {
		begun, err := i.write(fn, again)
		if err == stopped {
			return last
		}
		if begun || err == nil || !busy(err) || i.Done == nil {
			return err
		}
		last = err
		if waited := time.Since(start); i.Waiting != nil && waited-said >= i.WaitingEvery {
			i.Waiting(waited)
			said = waited
		}
		// the gate free a moment, for the other writes of the file; and a pool with no busy timeout, which
		// says SQLITE_BUSY at once, is not a loop that spins
		select {
		case <-i.Done:
			return last
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// write is one try of a write: begun is whether its transaction began, so fn ran. again is a try after the
// first: it gives way (stopped) when Done closed while it waited for the gate.
func (i Immediate) write(fn func(db SQL) error, again bool) (begun bool, err error) {
	if i.Gate != nil {
		i.Gate.Lock()
		defer i.Gate.Unlock()
	}
	if again {
		select {
		case <-i.Done:
			return false, stopped
		default:
		}
	}
	ctx := context.Background()
	conn, err := i.Pool.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if err := i.begin(ctx, conn); err != nil {
		return false, err
	}
	return true, i.commit(ctx, conn, fn)
}

// begin begins the write's transaction on conn. A write with Done and a Try waits Try for another writer's
// lock (the connection's busy timeout for the BEGIN alone, the pool's set back after it; a connection that
// cannot be set back never goes back to the pool).
func (i Immediate) begin(ctx context.Context, conn *sql.Conn) error {
	if i.Done == nil || i.Try <= 0 {
		_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
		return err
	}
	var pool int64
	if err := conn.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&pool); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", i.Try.Milliseconds())); err != nil {
		return err
	}
	_, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE")
	if _, back := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout = %d", pool)); back != nil {
		if err == nil {
			_, _ = conn.ExecContext(ctx, "ROLLBACK")
			err = back
		}
		_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	}
	return err
}

// commit runs fn in the transaction begun on conn and commits it, or rolls it back when fn fails.
func (i Immediate) commit(ctx context.Context, conn *sql.Conn, fn func(db SQL) error) error {
	committed := false
	defer func() {
		if committed {
			return
		}
		if _, err := conn.ExecContext(ctx, "ROLLBACK"); err != nil {
			// a connection whose transaction did not end never goes back to the pool
			_ = conn.Raw(func(any) error { return driver.ErrBadConn })
		}
	}()
	if err := fn(transaction{ctx: ctx, conn: conn}); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// busy is whether err is SQLite's SQLITE_BUSY (5), or one of its extended codes: another connection held the
// file's write lock for the whole busy timeout.
func busy(err error) bool {
	var coded interface{ Code() int }
	return errors.As(err, &coded) && coded.Code()&0xff == 5
}

// transaction is the connection that holds a write's transaction, as SQL.
type transaction struct {
	ctx  context.Context
	conn *sql.Conn
}

func (t transaction) Exec(query string, args ...any) (sql.Result, error) {
	return t.conn.ExecContext(t.ctx, query, args...)
}

func (t transaction) Query(query string, args ...any) (*sql.Rows, error) {
	return t.conn.QueryContext(t.ctx, query, args...)
}

func (t transaction) QueryRow(query string, args ...any) *sql.Row {
	return t.conn.QueryRowContext(t.ctx, query, args...)
}
