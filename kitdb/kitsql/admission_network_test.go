package kitsql_test

import (
	"context"
	"errors"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/relational"
)

func TestKitSQLSnapshotAdmissionOverHTTP(t *testing.T) {
	kernel, err := kitdb.Open(filepath.Join(t.TempDir(), "admission.kitdb"))
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
	if _, err := native.Exec(`CREATE TABLE products (id BIGINT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: fixtureOpener(native), AllowInsecureLocal: true})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	parsed, _ := url.Parse(server.URL)
	remote, err := kitsql.Open("kitsql://kitdb:secret@" + parsed.Host + "/shop?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	var held []*kitdb.Snapshot
	release := func() {
		for _, snapshot := range held {
			_ = snapshot.Close()
		}
		held = nil
	}
	t.Cleanup(release)
	for i := 0; i < 32; i++ {
		snapshot, err := kernel.Snapshot()
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, snapshot)
	}
	_, err = remote.Exec(`INSERT INTO products VALUES (1)`)
	var protocol *kitsql.ProtocolError
	if !errors.As(err, &protocol) || protocol.Code != "KSQL_ADMISSION_TIMEOUT" || protocol.Status != 503 || !protocol.Retryable() {
		t.Fatalf("admission error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := remote.ExecContext(ctx, `INSERT INTO products VALUES (2)`); done <- err }()
	await := func(waiters int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			stats, err := kernel.Stats()
			if err != nil {
				t.Fatal(err)
			}
			if stats.SnapshotWaiters == waiters {
				return
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatalf("waiters did not reach %d", waiters)
	}
	await(1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error = %v", err)
	}
	await(0)
	release()
	var count int64
	if err := native.QueryRow(`SELECT count(*) FROM products`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("rejected writes applied: count=%d, error=%v", count, err)
	}
	if _, err := remote.Exec(`INSERT INTO products VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if err := native.QueryRow(`SELECT count(*) FROM products`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("manual retry: count=%d, error=%v", count, err)
	}
	stats, err := kernel.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 || stats.SnapshotWaiters != 0 {
		t.Fatalf("admission resources leaked: %+v", stats)
	}
}
