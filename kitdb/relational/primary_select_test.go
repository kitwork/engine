package relational

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

func primaryStatement(t testing.TB, query string) kitdbsql.ParsedStatement {
	t.Helper()
	statement, err := kitdbsql.ParseStatement(query)
	if err != nil {
		t.Fatal(err)
	}
	return statement
}

func TestPreparedPrimarySelectEquivalence(t *testing.T) {
	e := pointSQLFixture(t, 16)
	ctx := context.Background()
	queries := []string{
		"SELECT id,bucket,payload FROM items WHERE id=$1",
		"SELECT * FROM items WHERE id=$1",
		"SELECT payload AS value,id,id AS again FROM items WHERE id=$1",
		"SELECT id,bucket,payload FROM items WHERE bucket=$1",
		"SELECT id FROM items WHERE id=NULL",
		"SELECT missing FROM items WHERE id=$1",
		"SELECT id FROM missing WHERE id=$1",
	}
	for _, overlay := range []bool{false, true} {
		if overlay {
			if _, err := e.Execute(ctx, "UPDATE items SET payload='new' WHERE id=2"); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range queries {
			for _, args := range [][]any{{2}, {99}, {nil}, {"bad"}, nil} {
				tx, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				want, wantErr := tx.Execute(ctx, query, args...)
				_ = tx.Rollback()
				got, err := e.Execute(ctx, query, args...)
				if fmt.Sprint(err) != fmt.Sprint(wantErr) || !reflect.DeepEqual(got, want) {
					t.Fatalf("query=%s args=%v got=%+v want=%+v errors=%v/%v", query, args, got, want, err, wantErr)
				}
			}
		}
	}
	assertPrimaryAdmissionDrained(t, e)
}

func assertPrimaryAdmissionDrained(t *testing.T, e *Engine) {
	t.Helper()
	stats, err := e.database.Stats()
	if err != nil || stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 || stats.SnapshotWaiters != 0 || stats.SnapshotReservedBytes != 0 {
		t.Fatalf("admission leak: %+v %v", stats, err)
	}
}

type primaryInterleavingReader struct {
	*kitdbengine.DB
	reads, trigger int
	change         func()
}

func (r *primaryInterleavingReader) Get(key []byte) ([]byte, bool, error) {
	value, found, err := r.DB.Get(key)
	r.reads++
	if r.reads == r.trigger {
		r.change()
	}
	return value, found, err
}

func TestPreparedPrimarySelectCommitFence(t *testing.T) {
	for _, trigger := range []int{1, 2} {
		e := pointSQLFixture(t, 8)
		ctx := context.Background()
		catalog, err := e.database.Catalog()
		if err != nil {
			t.Fatal(err)
		}
		plan := primaryStatement(t, "SELECT payload FROM items WHERE id=1").Select
		schema, err := schemaFromCatalog(catalog, plan.Table)
		if err != nil {
			t.Fatal(err)
		}
		binding, err := bindRowSelect(schema, plan, nil)
		if err != nil {
			t.Fatal(err)
		}
		reader := &primaryInterleavingReader{DB: e.database, trigger: trigger, change: func() {
			if _, err := e.Execute(ctx, "UPDATE items SET payload='changed' WHERE id=1"); err != nil {
				t.Fatal(err)
			}
		}}
		if _, handled, err := readBoundPrimarySelect(ctx, reader, catalog.Transaction, plan, binding); handled || err != nil {
			t.Fatal("accepted interleaved commit", trigger, handled, err)
		}
	}
}

func TestPreparedPrimarySelectSchemaRebind(t *testing.T) {
	for _, column := range []string{"id", "bucket"} {
		e := pointSQLFixture(t, 8)
		other, err := Attach(e.database, Options{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = other.Close() })
		ctx := context.Background()
		st := primaryStatement(t, "SELECT payload FROM items WHERE "+column+"=1")
		e.mu.RLock()
		reservation, err := e.database.ReserveSnapshot(ctx)
		if err != nil {
			e.mu.RUnlock()
			t.Fatal(err)
		}
		catalog, err := e.database.Catalog()
		if err != nil {
			_ = reservation.Close()
			e.mu.RUnlock()
			t.Fatal(err)
		}
		schema, err := schemaFromCatalog(catalog, "items")
		if err != nil {
			_ = reservation.Close()
			e.mu.RUnlock()
			t.Fatal(err)
		}
		binding, err := bindRowSelect(schema, st.Select, nil)
		if err != nil {
			_ = reservation.Close()
			e.mu.RUnlock()
			t.Fatal(err)
		}
		for _, query := range []string{
			"DROP TABLE items",
			"CREATE TABLE items(id INTEGER PRIMARY KEY, bucket INTEGER, payload INTEGER)",
			"INSERT INTO items VALUES(1,1,456)",
		} {
			if _, err := other.Execute(ctx, query); err != nil {
				_ = reservation.Close()
				e.mu.RUnlock()
				t.Fatal(err)
			}
		}
		result, err := e.executePreparedAutocommitSelect(ctx, st, nil, reservation, catalog, &binding)
		if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(456) || result.Columns[0].Kind != "int32" {
			t.Fatal("stale bound schema reused", result, err)
		}
		assertPrimaryAdmissionDrained(t, e)
	}
}

func TestPreparedPrimarySelectCancelledPreparation(t *testing.T) {
	e := pointSQLFixture(t, 8)
	ctx, cancel := context.WithCancel(context.Background())
	e.mu.RLock()
	reservation, err := e.database.ReserveSnapshot(ctx)
	if err != nil {
		e.mu.RUnlock()
		t.Fatal(err)
	}
	catalog, err := e.database.Catalog()
	if err != nil {
		_ = reservation.Close()
		e.mu.RUnlock()
		t.Fatal(err)
	}
	cancel()
	st := primaryStatement(t, "SELECT id FROM items WHERE bucket=1")
	if _, err := e.executePreparedAutocommitSelect(ctx, st, nil, reservation, catalog, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation", err)
	}
	assertPrimaryAdmissionDrained(t, e)
	// Acquiring the writer lifetime lock would hang if ownership was leaked.
	e.mu.Lock()
	e.mu.Unlock()
}

func TestPreparedPrimarySelectAdmission(t *testing.T) {
	e := pointSQLFixture(t, 8)
	var held []*kitdbengine.Snapshot
	defer func() {
		for _, snapshot := range held {
			_ = snapshot.Close()
		}
	}()
	for i := 0; i < 32; i++ {
		snapshot, err := e.database.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, snapshot)
	}
	for _, column := range []string{"id", "bucket"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		_, err := e.Execute(ctx, "SELECT id FROM items WHERE "+column+"=1")
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("bypassed admission", column, err)
		}
	}
	for _, snapshot := range held {
		_ = snapshot.Close()
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.Execute(ctx, "SELECT id FROM items WHERE id=1"); !errors.Is(err, context.Canceled) {
		t.Fatal("ignored cancellation", err)
	}
	assertPrimaryAdmissionDrained(t, e)
}

func TestPreparedPrimarySelectConcurrentCheckpoint(t *testing.T) {
	e := pointSQLFixture(t, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := e.Execute(ctx, "UPDATE items SET bucket=0,payload='0' WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		for i := 1; i <= 64; i++ {
			if _, err := e.Execute(ctx, "UPDATE items SET bucket=$1,payload=$2 WHERE id=1", i, fmt.Sprint(i)); err != nil {
				done <- err
				return
			}
			if i%8 == 0 {
				if _, err := e.database.Checkpoint(); err != nil {
					done <- err
					return
				}
			}
		}
		done <- nil
	}()
	var readErr error
	for i := 0; i < 256; i++ {
		result, err := e.Execute(ctx, "SELECT bucket,payload FROM items WHERE id=1")
		if err != nil || len(result.Rows) != 1 || fmt.Sprint(result.Rows[0][0]) != result.Rows[0][1] {
			readErr = fmt.Errorf("inconsistent row: %+v %v", result, err)
			break
		}
	}
	if err := <-done; err != nil || readErr != nil {
		t.Fatal("concurrent work", err, readErr)
	}
	assertPrimaryAdmissionDrained(t, e)
}
