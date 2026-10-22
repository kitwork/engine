package relational

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Split Done publication from delivery to children, the two phases of cancel.
// AfterFunc gives this test explicit control without sleeps or engine hooks.
type stagedCancellationContext struct {
	context.Context
	mu        sync.Mutex
	done      chan struct{}
	err       error
	next      uint64
	callbacks map[uint64]func()
}

func (ctx *stagedCancellationContext) Done() <-chan struct{} { return ctx.done }
func (ctx *stagedCancellationContext) Err() error {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.err
}

// Hide the parent's cancellation implementation so WithCancel uses AfterFunc.
func (ctx *stagedCancellationContext) Value(any) any { return nil }

func (ctx *stagedCancellationContext) AfterFunc(callback func()) func() bool {
	ctx.mu.Lock()
	ctx.next++
	id := ctx.next
	ctx.callbacks[id] = callback
	ctx.mu.Unlock()
	return func() bool {
		ctx.mu.Lock()
		defer ctx.mu.Unlock()
		_, pending := ctx.callbacks[id]
		delete(ctx.callbacks, id)
		return pending
	}
}

func (ctx *stagedCancellationContext) publishDone() {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	if ctx.err == nil {
		ctx.err = context.Canceled
		close(ctx.done)
	}
}

func (ctx *stagedCancellationContext) finish() {
	ctx.publishDone()
	ctx.mu.Lock()
	callbacks := ctx.callbacks
	ctx.callbacks = make(map[uint64]func())
	ctx.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func TestSQLCancellationCompletionBarrier(t *testing.T) {
	for _, completed := range []bool{false, true} {
		name := "done-only-races-cancel"
		if completed {
			name = "completed-cancel-precedes-commit"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "barrier.kitdb")
			engine, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			db, err := OpenNativeSQL(engine)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.ExecContext(ctx, "CREATE TABLE probe(id BIGINT PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			parent := &stagedCancellationContext{Context: ctx, done: make(chan struct{}), callbacks: make(map[uint64]func())}
			defer parent.finish()
			tx, err := db.BeginTx(parent, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			if _, err := tx.ExecContext(ctx, "INSERT INTO probe VALUES(1)"); err != nil {
				t.Fatal(err)
			}
			var order atomic.Uint64
			barrier := make(chan struct{})
			var release sync.Once
			defer release.Do(func() { close(barrier) })
			type outcome struct {
				err   error
				start uint64
			}
			finished := make(chan outcome, 1)
			go func() {
				if completed {
					select {
					case <-barrier:
					case <-ctx.Done():
						finished <- outcome{err: ctx.Err()}
						return
					}
				} else {
					<-parent.Done()
				}
				start := order.Add(1)
				finished <- outcome{start: start, err: tx.Commit()}
			}()
			parent.publishDone()
			var result outcome
			var complete uint64
			if completed {
				parent.finish()
				complete = order.Add(1)
				release.Do(func() { close(barrier) })
				select {
				case result = <-finished:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if result.start <= complete || !errors.Is(result.err, context.Canceled) && !errors.Is(result.err, sql.ErrTxDone) {
					t.Fatalf("completed cancel order=%d commit=%+v", complete, result)
				}
			} else {
				select {
				case result = <-finished:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				parent.finish()
				complete = order.Add(1)
				if result.err != nil || result.start >= complete {
					t.Fatalf("in-flight cancel order=%d commit=%+v", complete, result)
				}
			}
			observer, err := OpenNativeSQL(engine)
			if err != nil {
				t.Fatal(err)
			}
			var rows int
			err = observer.QueryRowContext(ctx, "SELECT COUNT(*) FROM probe").Scan(&rows)
			observer.Close()
			want := 1
			if completed {
				want = 0
			}
			if err != nil || rows != want {
				t.Fatalf("independent rows=%d want=%d: %v", rows, want, err)
			}
			db.Close()
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if err := reopened.database.Verify(); err != nil {
				t.Fatal(err)
			}
			resultRows, err := reopened.Execute(ctx, "SELECT COUNT(*) FROM probe")
			if err != nil || len(resultRows.Rows) != 1 || resultRows.Rows[0][0] != int64(want) {
				t.Fatalf("reopened rows=%+v want=%d: %v", resultRows.Rows, want, err)
			}
			t.Logf("completed=%t cancel-complete=%d commit-start=%d commit-error=%v independent-and-reopened-rows=%d", completed, complete, result.start, result.err, rows)
		})
	}
}
