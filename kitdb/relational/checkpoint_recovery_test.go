package relational

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func TestSQLTransactionCommitsWhileFullCheckpointDrains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "writer-drain.kitdb")
	m, err := kitdbnode.NewManager(kitdbnode.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	l, err := m.Acquire(ctx, path, kitdbengine.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	e, err := Attach(l.DB(), Options{CheckpointManager: m})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, query := range []string{"CREATE TABLE items(id BIGINT PRIMARY KEY, version BIGINT)", "INSERT INTO items VALUES(1,0),(2,0)"} {
		if _, err := e.Execute(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	for {
		stats, err := l.DB().Stats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.MainSegments == 32 {
			break
		}
		if _, err := e.Execute(ctx, "UPDATE items SET version=version+1 WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		if _, err := l.DB().Checkpoint(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Execute(ctx, "UPDATE items SET version=version+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	tx, err := e.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Execute(ctx, "UPDATE items SET version=1 WHERE id=2"); err != nil {
		t.Fatal(err)
	}
	ticket, err := m.ScheduleCheckpoint(ctx, path, kitdbengine.OpenOptions{}, kitdbnode.MaintenanceUrgent)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		s, err := l.DB().Snapshot()
		if errors.Is(err, kitdbengine.ErrSnapshotsActive) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
		if time.Now().After(deadline) {
			t.Fatal("managed full checkpoint did not pause readers")
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := tx.Commit(ctx); err != nil {
		t.Fatalf("existing SQL writer could not commit during drain: %v", err)
	}
	if _, err := ticket.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	stats, err := l.DB().Stats()
	if err != nil || stats.MainSegments != 1 || stats.LastTransaction != stats.CheckpointTransaction {
		t.Fatalf("checkpoint missed writer commit: %+v %v", stats, err)
	}
	concurrencyCheckRows(t, e, "SELECT version FROM items WHERE id=2", [][]any{{int64(1)}})
}

func TestSQLCheckpointRecoversAfterLongReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "reader.kitdb")
	m, err := kitdbnode.NewManager(kitdbnode.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	options := kitdbengine.OpenOptions{SnapshotMemoryBytes: 256 << 10}
	l, err := m.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()
	e, err := Attach(l.DB(), Options{Kernel: options, CheckpointManager: m})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	ctx := context.Background()
	for _, source := range []string{"CREATE TABLE items(id BIGINT PRIMARY KEY, version BIGINT, payload TEXT)", "CREATE INDEX items_version ON items(version)", "INSERT INTO items VALUES(1,0,'seed')"} {
		if _, err := e.Execute(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := e.BeginTransaction(ctx, TransactionOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var acknowledged int64
	blocked := false
	for generation := 0; generation < 64; generation++ {
		if _, err := e.Execute(ctx, "UPDATE items SET version=version+1 WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		acknowledged++
		_, err := l.DB().Checkpoint()
		if errors.Is(err, kitdbengine.ErrSnapshotsActive) {
			blocked = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !blocked {
		t.Fatal("fixture did not require full compaction")
	}
	payload := strings.Repeat("r", 160<<10)
	if _, err := e.Execute(ctx, "UPDATE items SET payload=$1,version=version+1 WHERE id=1", payload); err != nil {
		t.Fatalf("durable write returned maintenance failure: %v", err)
	}
	acknowledged++
	deadline := time.Now().Add(5 * time.Second)
	for m.Stats().MaintenanceFailures == 0 || m.Stats().RunningMaintenance != 0 {
		if time.Now().After(deadline) {
			t.Fatal("checkpoint failure was not observed")
		}
		time.Sleep(time.Millisecond)
	}
	if tx, err := e.BeginTransaction(ctx, TransactionOptions{}); !errors.Is(err, kitdbnode.ErrCheckpointBackoff) {
		if tx != nil {
			_ = tx.Rollback()
		}
		t.Fatalf("hard pressure did not back off: %v", err)
	}
	old, err := reader.Execute(ctx, "SELECT version,payload FROM items WHERE id=1")
	if err != nil || len(old.Rows) != 1 || old.Rows[0][0] != int64(0) || old.Rows[0][1] != "seed" {
		t.Fatalf("reader lost fixed snapshot: %+v %v", old.Rows, err)
	}
	if err := reader.Rollback(); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for {
		tx, err := e.BeginTransaction(ctx, TransactionOptions{})
		if err == nil {
			_ = tx.Rollback()
			break
		}
		if !errors.Is(err, kitdbnode.ErrCheckpointBackoff) || time.Now().After(deadline) {
			t.Fatalf("reader release did not restore admission: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := e.Execute(ctx, "UPDATE items SET version=version+1 WHERE id=1"); err != nil {
		t.Fatal(err)
	}
	acknowledged++
	stats, err := l.DB().Stats()
	if err != nil || stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 || stats.SnapshotWaiters != 0 || stats.FrontendTransactionBytes != 0 || stats.ActiveTransactionBytes != 0 || stats.PendingCommits != 0 {
		t.Fatalf("ownership retained: %+v %v", stats, err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	for _, source := range []string{"SELECT version,payload FROM items WHERE id=1", "SELECT version,payload FROM items WHERE version=$1"} {
		result, err := reopened.Execute(ctx, source, acknowledged)
		if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != acknowledged || result.Rows[0][1] != payload {
			t.Fatalf("recovery changed acknowledged row/index: %+v %v", result.Rows, err)
		}
	}
}
