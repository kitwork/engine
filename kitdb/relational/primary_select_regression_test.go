package relational

import (
	"context"
	"errors"
	"io"
	"os"
	"syscall"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

type preparedReadHandoff struct {
	engine      *Engine
	statement   kitdbsql.ParsedStatement
	parameters  []any
	reservation *kitdbengine.SnapshotReservation
	catalog     kitdbengine.CatalogSnapshot
	binding     rowSelectBinding
	consumed    bool
}

func prepareReadHandoff(t *testing.T, e *Engine, ctx context.Context, query string, args ...any) *preparedReadHandoff {
	t.Helper()
	h := &preparedReadHandoff{engine: e, statement: primaryStatement(t, query), parameters: args}
	e.mu.RLock()
	t.Cleanup(func() {
		if !h.consumed {
			_ = h.reservation.Close()
			e.mu.RUnlock()
		}
	})
	var err error
	h.reservation, err = e.database.ReserveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	h.catalog, err = e.database.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	schema, err := schemaFromCatalog(h.catalog, h.statement.Select.Table)
	if err != nil {
		t.Fatal(err)
	}
	h.binding, err = bindRowSelect(schema, h.statement.Select, args)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *preparedReadHandoff) execute(ctx context.Context) (Result, error) {
	h.consumed = true
	return h.engine.executePreparedAutocommitSelect(ctx, h.statement, h.parameters, h.reservation, h.catalog, &h.binding)
}

type scriptedPrimaryReader struct {
	*kitdbengine.DB
	point   string
	reads   int
	reached bool
	action  func()
	failure error
}

func (r *scriptedPrimaryReader) hit(point string) error {
	if point != r.point {
		return nil
	}
	r.reached = true
	if r.action != nil {
		r.action()
	}
	return r.failure
}

func (r *scriptedPrimaryReader) Get(key []byte) ([]byte, bool, error) {
	r.reads++
	value, found, err := r.DB.Get(key)
	point := ""
	switch r.reads {
	case 1:
		point = "generation"
	case 2:
		point = "row"
	}
	if injected := r.hit(point); injected != nil {
		return nil, false, injected
	}
	return value, found, err
}

func (r *scriptedPrimaryReader) LastTransaction() (uint64, error) {
	transaction, err := r.DB.LastTransaction()
	if injected := r.hit("fence"); injected != nil {
		return 0, injected
	}
	return transaction, err
}

func requirePrimaryHandoffReleased(t *testing.T, e *Engine) {
	t.Helper()
	assertPrimaryAdmissionDrained(t, e)
	stats, err := e.database.Stats()
	if err != nil || stats.SnapshotBytes != 0 || stats.FrontendTransactionBytes != 0 || stats.ActiveTransactions != 0 || stats.PendingCommits != 0 {
		t.Fatalf("transaction resources did not drain: %+v %v", stats, err)
	}
	if !e.mu.TryLock() {
		t.Fatal("engine lifetime read lock leaked")
	}
	e.mu.Unlock()
}

func TestPreparedPrimarySelectFallbackCommit(t *testing.T) {
	for _, point := range []string{"generation", "row"} {
		t.Run(point, func(t *testing.T) {
			e := pointSQLFixture(t, 8)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			h := prepareReadHandoff(t, e, ctx, "SELECT payload FROM items WHERE id=1")
			reader := &scriptedPrimaryReader{DB: e.database, point: point, action: func() {
				if _, err := e.Execute(ctx, "UPDATE items SET payload='committed' WHERE id=1"); err != nil {
					t.Fatal(err)
				}
			}}
			if _, handled, err := readBoundPrimarySelect(ctx, reader, h.catalog.Transaction, h.statement.Select, h.binding); handled || err != nil || !reader.reached {
				t.Fatal("commit fence not exercised", handled, err, reader.reached)
			}
			// Leave only the prepared reservation available. Re-admission instead
			// of consumption would time out, even though snapshot capture fits.
			var held []*kitdbengine.Snapshot
			defer func() {
				for _, snapshot := range held {
					_ = snapshot.Close()
				}
			}()
			for range 31 {
				snapshot, err := e.database.Snapshot()
				if err != nil {
					t.Fatal(err)
				}
				held = append(held, snapshot)
			}
			got, err := h.execute(ctx)
			if err != nil || len(got.Rows) != 1 || got.Rows[0][0] != "committed" {
				t.Fatal("stale/failing snapshot fallback", got, err)
			}
			for _, snapshot := range held {
				_ = snapshot.Close()
			}
			requirePrimaryHandoffReleased(t, e)
		})
	}
}

func TestPreparedPrimarySelectLayoutHandoff(t *testing.T) {
	for _, point := range []string{"generation", "row"} {
		t.Run(point, func(t *testing.T) {
			e := pointSQLFixture(t, 8)
			ctx := context.Background()
			// Relocate the one implicit ordered-primary path with the existing
			// generation fixture; no unrelated declared index is left behind.
			if _, err := e.Execute(ctx, "DROP INDEX items_bucket"); err != nil {
				t.Fatal(err)
			}
			h := prepareReadHandoff(t, e, ctx, "SELECT id,payload FROM items WHERE id=1")
			indexes, err := collectSecondaryIndexes(h.binding.schema)
			if err != nil || len(indexes) != 1 {
				t.Fatal(indexes, err)
			}
			oldKey, err := rowKey(h.binding.schema, map[string]any{"id": int64(1)}, 0)
			if err != nil {
				t.Fatal(err)
			}
			reader := &scriptedPrimaryReader{DB: e.database, point: point, action: func() {
				installGeneratedRowsAndIndex(t, e, h.binding.schema, indexes[0], 41)
			}}
			if _, handled, err := readBoundPrimarySelect(ctx, reader, h.catalog.Transaction, h.statement.Select, h.binding); handled || err != nil || !reader.reached {
				t.Fatal("layout fence not exercised", handled, err, reader.reached)
			}
			version, err := e.database.CatalogVersion()
			if err != nil || version.Revision != h.catalog.Revision || version.Transaction <= h.catalog.Transaction {
				t.Fatal("fixture changed schema or failed to advance data", version, err)
			}
			if _, found, err := e.database.Get(oldKey); err != nil || found {
				t.Fatal("old physical key still present", found, err)
			}
			got, err := h.execute(ctx)
			if err != nil || len(got.Rows) != 1 || got.Rows[0][0] != int64(1) || got.Rows[0][1] == nil {
				t.Fatal("fallback used old row generation", got, err)
			}
			requirePrimaryHandoffReleased(t, e)
		})
	}
}

func TestPreparedPrimarySelectReadErrors(t *testing.T) {
	for _, point := range []string{"generation", "row", "fence"} {
		t.Run(point, func(t *testing.T) {
			e := pointSQLFixture(t, 8)
			ctx := context.Background()
			h := prepareReadHandoff(t, e, ctx, "SELECT id FROM items WHERE id=1")
			failure := &os.PathError{Op: "test-read", Path: "disposable", Err: syscall.EIO}
			reader := &scriptedPrimaryReader{DB: e.database, point: point, failure: failure}
			if _, handled, err := readBoundPrimarySelect(ctx, reader, h.catalog.Transaction, h.statement.Select, h.binding); handled || !errors.Is(err, failure) || !reader.reached {
				t.Fatal("read error accepted or lost", handled, err, reader.reached)
			}
			// A transient speculative read error must not poison the owner.
			if got, err := h.execute(ctx); err != nil || len(got.Rows) != 1 {
				t.Fatal(got, err)
			}
			requirePrimaryHandoffReleased(t, e)
		})
	}
}

func TestPreparedPrimarySelectCancelledHandoff(t *testing.T) {
	for _, point := range []string{"generation", "row", "fence"} {
		t.Run(point, func(t *testing.T) {
			e := pointSQLFixture(t, 8)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := prepareReadHandoff(t, e, ctx, "SELECT id FROM items WHERE id=1")
			reader := &scriptedPrimaryReader{DB: e.database, point: point, action: cancel}
			if _, handled, err := readBoundPrimarySelect(ctx, reader, h.catalog.Transaction, h.statement.Select, h.binding); handled || !errors.Is(err, context.Canceled) || !reader.reached {
				t.Fatal("cancelled read accepted", handled, err, reader.reached)
			}
			if _, err := h.execute(ctx); !errors.Is(err, context.Canceled) {
				t.Fatal("lost cancellation during handoff", err)
			}
			requirePrimaryHandoffReleased(t, e)
		})
	}
}

func TestPreparedPrimarySelectCaptureFailure(t *testing.T) {
	e := pointSQLFixture(t, 8)
	ctx := context.Background()
	h := prepareReadHandoff(t, e, ctx, "SELECT id FROM items WHERE bucket=1")
	_ = h.reservation.Close()
	if _, err := h.execute(ctx); !errors.Is(err, kitdbengine.ErrSnapshotClosed) {
		t.Fatal("invalid reservation was accepted", err)
	}
	requirePrimaryHandoffReleased(t, e)
}

func TestPreparedPrimarySelectStorageReadFailure(t *testing.T) {
	old := pointSQLFixture(t, 8)
	path := old.database.Path()
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	e, err := OpenWithOptions(path, Options{Kernel: kitdbengine.OpenOptions{PageCacheBytes: -1}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close() })
	ctx := context.Background()
	h := prepareReadHandoff(t, e, ctx, "SELECT id,payload FROM items WHERE id=1")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	restored := false
	restore := func() error {
		if restored {
			return nil
		}
		restored = true
		_, writeErr := file.WriteAt(before, 0)
		return errors.Join(writeErr, file.Sync(), file.Close())
	}
	t.Cleanup(func() {
		if err := restore(); err != nil {
			t.Error(err)
		}
	})
	if err := file.Truncate(0); err != nil {
		t.Fatal(err)
	}
	if got, err := h.execute(ctx); (!errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF)) || len(got.Rows) != 0 {
		t.Fatal("physical short read was hidden", got, err)
	}
	requirePrimaryHandoffReleased(t, e)
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	if err := e.database.Verify(); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Execute(ctx, "SELECT id FROM items WHERE id=1"); err != nil || len(got.Rows) != 1 {
		t.Fatal("owner unusable after fixture restoration", got, err)
	}
}

func TestPreparedPrimarySelectAllocationEnvelope(t *testing.T) {
	e := pointSQLFixture(t, 128)
	ctx := context.Background()
	for _, test := range []struct {
		name, query string
		rows        int
		limit       float64
	}{
		{"primary", "SELECT id,bucket,payload FROM items WHERE id=$1", 1, 160},
		{"fallback", "SELECT id,bucket,payload FROM items WHERE bucket=$1", 16, 460},
	} {
		t.Run(test.name, func(t *testing.T) {
			run := func(snapshot bool) {
				var got Result
				var err error
				if snapshot {
					var tx *Transaction
					tx, err = e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
					if err == nil {
						got, err = tx.Execute(ctx, test.query, 1)
						err = errors.Join(err, tx.Rollback())
					}
				} else {
					got, err = e.Execute(ctx, test.query, 1)
				}
				if err != nil || len(got.Rows) != test.rows || got.Rows[0][0] != int64(1) {
					t.Fatal(got, err)
				}
			}
			baseline := testing.AllocsPerRun(40, func() { run(true) })
			prepared := testing.AllocsPerRun(40, func() { run(false) })
			if prepared > test.limit || (test.name == "fallback" && prepared > baseline+4) || (test.name == "primary" && prepared > baseline-8) {
				t.Fatalf("allocation regression: prepared=%.0f snapshot=%.0f cap=%.0f", prepared, baseline, test.limit)
			}
			t.Logf("prepared=%.0f snapshot=%.0f cap=%.0f", prepared, baseline, test.limit)
			requirePrimaryHandoffReleased(t, e)
		})
	}
}
