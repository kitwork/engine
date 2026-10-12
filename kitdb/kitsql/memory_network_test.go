package kitsql_test

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/relational"
)

func TestKitSQLSlowReaderMemoryAndCheckpoint(t *testing.T) {
	kernel, err := kitdb.OpenWithOptions(filepath.Join(t.TempDir(), "slow.kitdb"), kitdb.OpenOptions{SnapshotMemoryBytes: 3 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = kernel.Close() })
	engine, err := relational.Attach(kernel, relational.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	native, err := relational.OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	if _, err := native.Exec(`CREATE TABLE products(id BIGINT PRIMARY KEY, name TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(`INSERT INTO products VALUES (1, $1)`, strings.Repeat("x", 2<<20)); err != nil {
		t.Fatal(err)
	}
	if _, err := native.Exec(`INSERT INTO products VALUES (2, 'second'), (3, 'third'), (4, 'fourth')`); err != nil {
		t.Fatal(err)
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: fixtureOpener(native), AllowInsecureLocal: true, QueryTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var requests atomic.Int32
	var once sync.Once
	release := func() { once.Do(func() { close(resume) }) }
	remote, _ := networkDatabase(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			defer close(done)
			handler.ServeHTTP(&pausedRowWriter{ResponseWriter: w, ctx: r.Context(), entered: entered, resume: resume}, r)
			return
		}
		handler.ServeHTTP(w, r)
	}), false)
	t.Cleanup(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows, err := remote.QueryContext(ctx, `SELECT name FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("reader did not stall")
	}
	stats, err := kernel.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.SnapshotBytes < 2<<20 || stats.ActiveSnapshots != 1 {
		t.Fatalf("reader did not retain charged snapshot: %+v", stats)
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	var count int64
	err = remote.QueryRowContext(waitCtx, `SELECT count(*) FROM products`).Scan(&count)
	waitCancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("second reader should wait on bytes: %v", err)
	}
	if _, err := kernel.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := remote.QueryRow(`SELECT count(*) FROM products`).Scan(&count); err != nil || count != 4 {
		t.Fatalf("checkpoint did not restore admission: count=%d err=%v", count, err)
	}
	release()
	_ = rows.Close()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("slow reader did not drain")
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		stats, _ = kernel.Stats()
		if stats.SnapshotBytes == 0 && stats.SnapshotReservedBytes == 0 && stats.SnapshotWaiters == 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("reader charges leaked: %+v", stats)
}
