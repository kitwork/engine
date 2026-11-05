package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// The port's statements and SQLite's temporary files (docs/KITWORK_KITDB.md câu 13.5 of the Kitwork repository).
//
// A statement SQLite cannot sort, group or hold in memory spills to temporary files. Nothing bounds them: on
// 05/11/2026 a statement reading no table, sorting an endless WITH RECURSIVE of 100 KB values, wrote 1,010 MB in 3 s
// through the port, about 10 GB in the 30 s of its QueryTimeout. PostgreSQL bounds this with temp_file_limit, and
// cancels the statement with 53400. SQLite has no such limit and says nothing of a statement's temporary files while
// it runs (SQLITE_DBSTATUS_TEMPBUF_SPILL counts a sort's spill only once the sort is closed). So the port measures
// the files themselves: while any of its statements runs, a watcher reads the size of every SQLite temporary file of
// the process every 100 ms, and cancels each running statement whose limit the total passes. The files are the
// process's, not the statement's: SQLite does not say which connection a temporary file is of, so an app's own
// spilling query counts too, and an attacker's sort stops the other port statements running with it. Where the files
// cannot be measured (temporarySupported false) no statement is stopped for them.

// DefaultMaxTemporaryBytes is the most bytes the process's SQLite temporary files may hold while a statement runs,
// when Options.MaxTemporaryBytes is not set.
const DefaultMaxTemporaryBytes = int64(2 << 30)

// temporaryEvery is how often the watcher measures.
const temporaryEvery = 100 * time.Millisecond

// temporaryLimit is the cause a statement is canceled with when the temporary files pass its limit.
type temporaryLimit struct {
	used, limit int64
}

func (limit *temporaryLimit) Error() string {
	return fmt.Sprintf("SQLite's temporary files hold %d MB, the port's limit is %d MB", limit.used>>20, limit.limit>>20)
}

// protocolError is PostgreSQL's error for a statement over temp_file_limit.
func (limit *temporaryLimit) protocolError() error {
	return pgwire.NewError("53400", fmt.Sprintf(
		"temporary file size exceeds the port's limit (%d MB): the statement sorts, groups or holds more than fits in "+
			"memory; add LIMIT to an ORDER BY, a WHERE an index serves, or group fewer rows", limit.limit>>20))
}

// temporaryCause turns err into 53400 when the statement of ctx was canceled for its temporary files.
func temporaryCause(ctx context.Context, err error) error {
	var limit *temporaryLimit
	if err != nil && errors.As(context.Cause(ctx), &limit) {
		return limit.protocolError()
	}
	return err
}

// temporaryGuard watches the process's SQLite temporary files while port statements run.
type temporaryGuard struct {
	mutex    sync.Mutex
	running  map[*guardedStatement]struct{}
	watching bool
	measure  func() (int64, bool)
	every    time.Duration
}

type guardedStatement struct {
	limit  int64
	cancel context.CancelCauseFunc
}

var temporaryFiles = &temporaryGuard{running: map[*guardedStatement]struct{}{}, measure: sqliteTemporaryBytes, every: temporaryEvery}

// watch watches one statement until the returned function is called; cancel is called with a *temporaryLimit when
// the files pass limit.
func (guard *temporaryGuard) watch(limit int64, cancel context.CancelCauseFunc) func() {
	if !temporarySupported {
		return func() {}
	}
	statement := &guardedStatement{limit: limit, cancel: cancel}
	guard.mutex.Lock()
	guard.running[statement] = struct{}{}
	if !guard.watching {
		guard.watching = true
		go guard.loop()
	}
	guard.mutex.Unlock()
	return func() {
		guard.mutex.Lock()
		delete(guard.running, statement)
		guard.mutex.Unlock()
	}
}

// loop measures until no statement runs.
func (guard *temporaryGuard) loop() {
	ticker := time.NewTicker(guard.every)
	defer ticker.Stop()
	for range ticker.C {
		guard.mutex.Lock()
		if len(guard.running) == 0 {
			guard.watching = false
			guard.mutex.Unlock()
			return
		}
		guard.mutex.Unlock()
		used, measured := guard.measure()
		if !measured {
			continue
		}
		guard.mutex.Lock()
		for statement := range guard.running {
			if used > statement.limit {
				statement.cancel(&temporaryLimit{used: used, limit: statement.limit})
				delete(guard.running, statement)
			}
		}
		guard.mutex.Unlock()
	}
}
