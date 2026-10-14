package relational

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kernel "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
)

func TestPostgresResourceRejectionCodes(t *testing.T) {
	for _, cause := range []error{
		kernel.ErrSnapshotMemory, kernel.ErrSnapshotAdmissionFull, kernel.ErrSnapshotAdmissionTimeout,
		kernel.ErrTooManySnapshots, kernel.ErrTooManyTransactions, kernel.ErrActiveTransactionMemory,
		kernel.ErrSnapshotsActive, node.ErrCheckpointBackoff, node.ErrMaintenanceQueueFull,
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			for _, err := range []error{cause, fmt.Errorf("wrapped: %w", cause)} {
				var protocol *pgwire.Error
				if !errors.As(postgresError(err), &protocol) || protocol.Code != "53300" {
					t.Fatalf("expected resource rejection for %v: %v", err, postgresError(err))
				}
			}
		})
	}
}

func TestPostgresSnapshotBudgetRejectionAndRecovery(t *testing.T) {
	engine, err := OpenWithOptions(filepath.Join(t.TempDir(), "budget.kitdb"), Options{Kernel: kernel.OpenOptions{SnapshotMemoryBytes: 256 << 10}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := engine.Execute(ctx, "CREATE TABLE items(id BIGINT PRIMARY KEY, version BIGINT NOT NULL, payload TEXT)"); err != nil {
		t.Fatal(err)
	}
	payload := strings.Repeat("x", 32<<10)
	if _, err := engine.Execute(ctx, "INSERT INTO items VALUES(1,0,$1)", payload); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serverCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- engine.ServePostgres(serverCtx, listener, PostgresServerOptions{
			PostgresOptions: PostgresOptions{Database: "budget", User: "kitdb", Password: "wire-test"},
			MaxConnections:  2, QueryTimeout: 3 * time.Second,
		})
	}()
	t.Cleanup(func() {
		stop()
		_ = listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("pgwire did not drain")
		}
	})
	client := openPostgresNodeTestClient(t, listener.Addr().String(), "budget", "wire-test")
	t.Cleanup(func() { _ = client.Close() })
	var held []*kernel.Snapshot
	release := func() {
		for _, snapshot := range held {
			if err := snapshot.Close(); err != nil {
				t.Error(err)
			}
		}
		held = nil
	}
	t.Cleanup(release)
	var admissionErr error
	for range 32 {
		snapshot, err := engine.database.Snapshot()
		if err != nil {
			admissionErr = err
			break
		}
		held = append(held, snapshot)
	}
	if !errors.Is(admissionErr, kernel.ErrSnapshotMemory) {
		t.Fatalf("fixture did not exhaust memory budget: %v", admissionErr)
	}
	var version int64
	readErr := client.QueryRowContext(ctx, "SELECT version FROM items WHERE id=1").Scan(&version)
	if code := postgresNodeSQLState(readErr); code != "53300" {
		t.Fatalf("read rejection code=%s: %v", code, readErr)
	}
	_, writeErr := client.ExecContext(ctx, "UPDATE items SET version=version+1 WHERE id=1")
	if code := postgresNodeSQLState(writeErr); code != "53300" {
		t.Fatalf("write rejection code=%s: %v", code, writeErr)
	}
	release()
	if err := client.QueryRowContext(ctx, "SELECT version FROM items WHERE id=1").Scan(&version); err != nil || version != 0 {
		t.Fatalf("rejected write changed data: %d / %v", version, err)
	}
	if _, err := client.ExecContext(ctx, "UPDATE items SET version=version+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	var text string
	if err := client.QueryRowContext(ctx, "SELECT version,payload FROM items WHERE id=1").Scan(&version, &text); err != nil || version != 1 || text != payload {
		t.Fatalf("recovery mismatch: %d / %v", version, err)
	}
	if err := engine.database.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestPostgresDurabilityUncertaintyIsNotRejection(t *testing.T) {
	for _, cause := range []error{
		kernel.ErrSnapshotMemory, kernel.ErrTransactionConflict, ErrTransactionConflict,
		context.Canceled, pgwire.NewError("53300", "resource rejection"),
		pgwire.NewError("40001", "conflict"),
	} {
		for _, err := range []error{errors.Join(kernel.ErrDurabilityUncertain, cause), errors.Join(cause, kernel.ErrDurabilityUncertain)} {
			var protocol *pgwire.Error
			if !errors.As(postgresError(err), &protocol) || protocol.Code != "XX000" {
				t.Fatalf("uncertain write became a rejection: %v => %v", err, postgresError(err))
			}
		}
	}
	for _, err := range []error{kernel.ErrDurabilityUncertain, errors.New("kitdb: snapshot memory budget reached")} {
		var protocol *pgwire.Error
		if !errors.As(postgresError(err), &protocol) || protocol.Code != "XX000" {
			t.Fatalf("unexpected classification: %v", err)
		}
	}
	if postgresError(nil) != nil {
		t.Fatal("nil error changed")
	}
	for _, cause := range []error{kernel.ErrTransactionConflict, ErrTransactionConflict} {
		var protocol *pgwire.Error
		if !errors.As(postgresError(cause), &protocol) || protocol.Code != "40001" {
			t.Fatalf("conflict code lost: %v", cause)
		}
	}
}
