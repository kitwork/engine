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
	"github.com/kitwork/engine/kitdb/pgwire"
)

func TestSQLCheckpointPressureUsesOwnedMaintenance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "managed.kitdb")
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	options := kitdbengine.OpenOptions{SnapshotMemoryBytes: 256 << 10}
	lease, err := manager.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	engine, err := Attach(lease.DB(), Options{Kernel: options, CheckpointManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	native, err := OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	ctx := context.Background()
	if _, err := native.ExecContext(ctx, "CREATE TABLE items(id BIGINT PRIMARY KEY, payload TEXT)"); err != nil {
		t.Fatal(err)
	}
	for id := 1; id <= 80; id++ {
		if _, err := native.ExecContext(ctx, "INSERT INTO items VALUES($1,$2)", id, strings.Repeat("x", 4096)); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for manager.Stats().QueuedMaintenance != 0 || manager.Stats().RunningMaintenance != 0 {
		if time.Now().After(deadline) {
			t.Fatal("maintenance did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	if manager.Stats().CheckpointCompletions == 0 {
		t.Fatal("SQL never scheduled a checkpoint")
	}
	var count int64
	if err := native.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 80 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	if err := lease.DB().Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLCheckpointPressureThroughPostgresNode(t *testing.T) {
	root := t.TempDir()
	createPostgresNodeFixture(t, filepath.Join(root, "alpha.kitdb"), "rows", "seed")
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		Relational: Options{Kernel: kitdbengine.OpenOptions{SnapshotMemoryBytes: 256 << 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	address, stop := startPostgresNodeTestServer(t, node)
	defer stop()
	client := openPostgresNodeTestClient(t, address, "alpha", "node-secret")
	defer client.Close()
	ctx := context.Background()
	for id := 2; id <= 80; id++ {
		if _, err := client.ExecContext(ctx, "INSERT INTO rows VALUES($1,$2)", id, strings.Repeat("p", 4096)); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := client.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT INTO rows VALUES(81,'transaction')"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for node.manager.Stats().QueuedMaintenance != 0 || node.manager.Stats().RunningMaintenance != 0 {
		if time.Now().After(deadline) {
			t.Fatal("pgwire maintenance did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	if node.manager.Stats().CheckpointCompletions == 0 {
		t.Fatal("pgwire never scheduled a checkpoint")
	}
	var count int64
	if err := client.QueryRowContext(ctx, "SELECT COUNT(*) FROM rows").Scan(&count); err != nil || count != 81 {
		t.Fatalf("pgwire count=%d err=%v", count, err)
	}
}

func TestSQLCheckpointFailureDoesNotRewriteCommit(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		t.Run(map[bool]string{false: "autocommit", true: "transaction"}[explicit], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "commit.kitdb")
			db, err := kitdbengine.OpenWithOptions(path, kitdbengine.OpenOptions{SnapshotMemoryBytes: 256 << 10})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			engine, err := Attach(db, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			ctx := context.Background()
			if _, err := engine.Execute(ctx, "CREATE TABLE items(id BIGINT PRIMARY KEY, payload TEXT)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			// Deliberately unavailable maintenance owner: only the post-commit
			// evaluation fails on the first write, then hard admission rejects.
			manager, err := kitdbnode.NewManager(kitdbnode.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
			engine.checkpointManager = manager
			payload := strings.Repeat("v", 160<<10)
			if explicit {
				tx, err := engine.BeginTransaction(ctx, TransactionOptions{})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.Execute(ctx, "INSERT INTO items VALUES(1,$1)", payload); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Commit(ctx); err != nil {
					t.Fatalf("committed write became failure: %v", err)
				}
			} else if _, err := engine.Execute(ctx, "INSERT INTO items VALUES(1,$1)", payload); err != nil {
				t.Fatalf("committed write became failure: %v", err)
			}
			if _, err := engine.Execute(ctx, "INSERT INTO items VALUES(2,'rejected')"); !errors.Is(err, kitdbnode.ErrClosed) {
				t.Fatalf("hard pressure was not rejected before commit: %v", err)
			}
			if tx, err := engine.BeginTransaction(ctx, TransactionOptions{}); !errors.Is(err, kitdbnode.ErrClosed) {
				if tx != nil {
					_ = tx.Rollback()
				}
				t.Fatalf("hard pressure was not rejected before snapshot capture: %v", err)
			}
			stats, err := db.Stats()
			if err != nil || stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 {
				t.Fatalf("rejected writer retained snapshot ownership: %+v, %v", stats, err)
			}
			result, err := engine.Execute(ctx, "SELECT id,payload FROM items")
			if err != nil || len(result.Rows) != 1 || result.Rows[0][1] != payload {
				t.Fatalf("commit truth lost: rows=%d err=%v", len(result.Rows), err)
			}
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			result, err = reopened.Execute(ctx, "SELECT id,payload FROM items")
			if err != nil || len(result.Rows) != 1 || result.Rows[0][1] != payload {
				t.Fatalf("reopen lost commit: %v", err)
			}
		})
	}
}

func TestPostgresAcquisitionFailureDistinguishesDeadlineAndCapacity(t *testing.T) {
	for _, test := range []struct {
		err           error
		code, message string
	}{
		{context.DeadlineExceeded, "53300", "acquisition timed out"},
		{kitdbnode.ErrWarmCapacity, "53300", "warm databases exhaust capacity"},
		{context.Canceled, "57014", "acquisition canceled"},
	} {
		err := postgresNodeAcquireError(test.err)
		var protocol *pgwire.Error
		if !errors.As(err, &protocol) || protocol.Code != test.code || !strings.Contains(err.Error(), test.message) {
			t.Fatalf("failure %v mapped to %v", test.err, err)
		}
	}
	for _, err := range []error{kitdbnode.ErrCheckpointBackoff, kitdbnode.ErrMaintenanceQueueFull, kitdbengine.ErrSnapshotsActive} {
		var protocol *pgwire.Error
		if !errors.As(postgresError(err), &protocol) || protocol.Code != "53300" {
			t.Fatalf("pressure mapped incorrectly: %v", postgresError(err))
		}
	}
}

func TestSQLCheckpointAttachRejectsUnavailableOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.kitdb")
	db, err := kitdbengine.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Attach(db, Options{CheckpointManager: manager}); !errors.Is(err, kitdbnode.ErrClosed) {
		t.Fatalf("unavailable maintenance owner accepted: %v", err)
	}
	if _, err := db.LastTransaction(); err != nil {
		t.Fatalf("failed attach closed caller's kernel: %v", err)
	}
}

func TestSQLCheckpointBeginAllowsFullCompactionBeforeSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "full.kitdb")
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	options := kitdbengine.OpenOptions{SnapshotMemoryBytes: 256 << 10}
	lease, err := manager.Acquire(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	db := lease.DB()
	engine, err := Attach(db, Options{Kernel: options, CheckpointManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	reader, err := db.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	put := func(value string) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err := tx.Put([]byte("fixture"), []byte(value)); err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	blocked := false
	for generation := 0; generation < 64; generation++ {
		put("fragment")
		_, err := db.Checkpoint()
		if errors.Is(err, kitdbengine.ErrSnapshotsActive) {
			blocked = true
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if !blocked {
		t.Fatal("fixture never required full compaction")
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	put(strings.Repeat("c", 160<<10))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		t.Fatalf("writer's own snapshot blocked full compaction: %v", err)
	}
	defer tx.Rollback()
	stats, err := db.Stats()
	if err != nil || stats.OverlayMutations != 0 || stats.MainSegments > 1 || stats.ActiveSnapshots != 1 {
		t.Fatalf("BEGIN did not checkpoint before capture: %+v err=%v", stats, err)
	}
}
