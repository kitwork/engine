package relational

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectRowStreamUsesBoundedSnapshotCursor(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "stream.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, bucket INTEGER NOT NULL, name TEXT NOT NULL)`,
		`INSERT INTO items VALUES (1, 1, 'one'), (2, 1, 'two'), (3, 2, 'three')`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}

	stream, handled, err := engine.openSelectRowStream(
		ctx, `SELECT id, name FROM items WHERE bucket >= $1`, []any{int64(1)},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || stream == nil {
		t.Fatal("simple SELECT did not use the snapshot row stream")
	}
	if columns := stream.Columns(); len(columns) != 2 || columns[0].Name != "id" || columns[1].Name != "name" {
		t.Fatalf("columns = %+v", columns)
	}
	row, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if len(row) != 2 || row[0] != int64(1) || row[1] != "one" {
		t.Fatalf("first row = %#v", row)
	}

	// More rows than the one-row channel keeps the producer, snapshot and
	// engine read lease alive until Close applies backpressure cancellation.
	if engine.mu.TryLock() {
		engine.mu.Unlock()
		t.Fatal("stream released its engine snapshot before the consumer closed it")
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	if !engine.mu.TryLock() {
		t.Fatal("closing the stream did not release its engine snapshot")
	}
	engine.mu.Unlock()
	if err := engine.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSelectRowStreamShutdownWaitsForSnapshotRelease(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "shutdown-stream.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO items VALUES (1, 'one'), (2, 'two'), (3, 'three'), (4, 'four')`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	stream, handled, err := engine.openSelectRowStream(ctx, `SELECT id FROM items`, nil)
	if err != nil || !handled {
		t.Fatalf("open stream: handled=%v err=%v", handled, err)
	}
	defer stream.Close()
	if _, err := stream.Next(); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		done <- engine.Close()
	}()
	<-started
	select {
	case err := <-done:
		t.Fatalf("engine closed with an active snapshot: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("engine shutdown did not resume after snapshot release")
	}
}

func TestSelectRowStreamFallsBackForMaterializingOperators(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "fallback.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	if _, err := engine.Execute(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{
		`SELECT DISTINCT name FROM items`,
		`SELECT count(*) FROM items`,
		`SELECT id, name FROM items ORDER BY name`,
	} {
		stream, handled, err := engine.openSelectRowStream(ctx, query, nil)
		if err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		if handled || stream != nil {
			t.Fatalf("materializing query unexpectedly streamed: %s", query)
		}
	}
}

func TestSelectRowStreamKeepsItsCapturedSnapshot(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "snapshot-stream.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO items VALUES (1, 'one'), (2, 'two'), (3, 'three')`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	stream, handled, err := engine.openSelectRowStream(ctx, `SELECT id, name FROM items`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("simple SELECT did not stream")
	}
	defer stream.Close()
	first, err := stream.Next()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `UPDATE items SET name = 'changed' WHERE id = 3`); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Execute(ctx, `INSERT INTO items VALUES (4, 'four')`); err != nil {
		t.Fatal(err)
	}

	rows := [][]any{first}
	for {
		row, err := stream.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	if len(rows) != 3 {
		t.Fatalf("snapshot rows = %#v", rows)
	}
	foundOriginal := false
	for _, row := range rows {
		if row[0] == int64(3) && row[1] == "three" {
			foundOriginal = true
		}
		if row[0] == int64(4) {
			t.Fatalf("snapshot observed a later insert: %#v", rows)
		}
	}
	if !foundOriginal {
		t.Fatalf("snapshot did not preserve the pre-update row: %#v", rows)
	}
}

func TestSelectRowStreamReportsUnboundedResultOverflow(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "overflow-stream.kitdb"), Options{
		MaximumResultRows: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY)`,
		`INSERT INTO items VALUES (1), (2), (3)`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	stream, handled, err := engine.openSelectRowStream(ctx, `SELECT id FROM items`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !handled {
		t.Fatal("simple SELECT did not stream")
	}
	defer stream.Close()
	for range 2 {
		if _, err := stream.Next(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := stream.Next(); err == nil || !strings.Contains(err.Error(), "result exceeds 2 rows") {
		t.Fatalf("overflow error = %v", err)
	}
}

func TestSelectRowStreamPreservesCoveredIndexOrder(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "ordered.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, bucket INTEGER NOT NULL, score INTEGER NOT NULL)`,
		`CREATE INDEX idx_items_bucket_score ON items (bucket, score)`,
		`INSERT INTO items VALUES (1, 1, 30), (2, 1, 10), (3, 1, 20), (4, 2, 5)`,
	} {
		if _, err := engine.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	stream, handled, err := engine.openSelectRowStream(
		ctx,
		`SELECT id, score FROM items WHERE bucket = 1 ORDER BY score DESC LIMIT 3`,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !handled || stream == nil {
		t.Fatal("covered ORDER BY did not use the row stream")
	}
	defer stream.Close()
	want := []int64{30, 20, 10}
	for index, expected := range want {
		row, err := stream.Next()
		if err != nil {
			t.Fatalf("row %d: %v", index, err)
		}
		if row[1] != expected {
			t.Fatalf("row %d score = %#v, want %d", index, row[1], expected)
		}
	}
	if _, err := stream.Next(); !errors.Is(err, io.EOF) {
		t.Fatalf("terminal error = %v, want EOF", err)
	}
}

func TestNativeSQLClosesSnapshotWhenRowsCloseEarly(t *testing.T) {
	engine, err := Open(filepath.Join(t.TempDir(), "native-stream.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	database, err := OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	ctx := context.Background()
	for _, source := range []string{
		`CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT NOT NULL)`,
		`INSERT INTO items VALUES (1, 'one'), (2, 'two'), (3, 'three')`,
	} {
		if _, err := database.ExecContext(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := database.QueryContext(ctx, `SELECT id, name FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatalf("first row unavailable: %v", rows.Err())
	}
	var id int64
	var name string
	if err := rows.Scan(&id, &name); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `UPDATE items SET name = 'updated' WHERE id = 1`); err != nil {
		t.Fatalf("write after early cursor close: %v", err)
	}
	if err := database.QueryRowContext(ctx, `SELECT name FROM items WHERE id = 1`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "updated" {
		t.Fatalf("name = %q", name)
	}
}

func TestNativeSQLCacheableSelectKeepsResultCache(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "cached.kitdb"), Options{
		QueryCache: QueryCacheOptions{Select: time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	database, err := OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.ExecContext(ctx, `CREATE TABLE items (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO items VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var id int64
		if err := database.QueryRowContext(ctx, `SELECT id FROM items WHERE id = 1`).Scan(&id); err != nil {
			t.Fatal(err)
		}
	}
	if stats := engine.QueryCacheStats(); stats.Hits != 1 {
		t.Fatalf("cache stats = %+v", stats)
	}
}
