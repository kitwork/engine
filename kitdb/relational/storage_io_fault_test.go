//go:build kitdbfault

package relational

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func TestSQLStorageIOFaultMatrix(t *testing.T) {
	cases := []struct {
		point, kind             string
		full, commit, uncertain bool
	}{
		{"wal-write", "short", false, true, true},
		{"wal-write", "space", false, true, true},
		{"wal-sync", "io", false, true, true},
		{"append-write", "short", false, false, false},
		{"append-sync", "io", false, false, false},
		{"slot-write", "short", false, false, true},
		{"slot-sync", "io", false, false, true},
		{"compact-write", "short", true, false, false},
		{"compact-sync", "io", true, false, false},
		{"compact-replace", "io", true, false, false},
		{"compact-directory-sync", "io", true, false, true},
		{"rotate-prepare", "io", false, false, false},
		{"rotate-replace", "io", false, false, false},
		{"rotate-directory-sync", "io", false, false, true},
		{"rotate-prepare", "io", true, false, false},
		{"rotate-replace", "io", true, false, false},
		{"rotate-directory-sync", "io", true, false, true},
	}
	for _, test := range cases {
		name := test.point + "-" + test.kind
		if test.full {
			name += "-full"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "io.kitdb")
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			manager, err := kitdbnode.NewManager(kitdbnode.Limits{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			lease, err := manager.Acquire(ctx, path, kitdbengine.OpenOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Release()
			engine, err := Attach(lease.DB(), Options{CheckpointManager: manager})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			pool, err := OpenNativeSQL(engine)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			execute := func(query string, args ...any) {
				t.Helper()
				if _, err := pool.ExecContext(ctx, query, args...); err != nil {
					t.Fatal(err)
				}
			}
			execute("CREATE TABLE items(id BIGINT PRIMARY KEY, batch BIGINT NOT NULL, payload TEXT NOT NULL)")
			execute("CREATE INDEX items_batch ON items(batch)")
			if _, err := lease.DB().Checkpoint(); err != nil {
				t.Fatal(err)
			}
			if test.full {
				for batch := 1; batch <= 31; batch++ {
					execute("INSERT INTO items VALUES($1,$2,$3),($4,$2,$3)", batch*2-1, batch, checkpointCrashPayload, batch*2)
					if _, err := lease.DB().Checkpoint(); err != nil {
						t.Fatal(err)
					}
				}
				stats, err := lease.DB().Stats()
				if err != nil || stats.MainSegments != 32 {
					t.Fatalf("full fixture stats=%+v err=%v", stats, err)
				}
			} else {
				for batch := 1; batch <= 31; batch++ {
					execute("INSERT INTO items VALUES($1,$2,$3),($4,$2,$3)", batch*2-1, batch, checkpointCrashPayload, batch*2)
				}
			}
			if !test.commit {
				execute("INSERT INTO items VALUES(63,32,$1),(64,32,$1)", checkpointCrashPayload)
			}
			t.Setenv("KITDB_IO_FAULT_PATH", path)
			t.Setenv("KITDB_IO_FAULT_POINT", test.point)
			t.Setenv("KITDB_IO_FAULT_KIND", test.kind)
			if test.commit {
				_, err = pool.ExecContext(ctx, "INSERT INTO items VALUES(63,32,$1),(64,32,$1)", checkpointCrashPayload)
			} else {
				var ticket *kitdbnode.MaintenanceTicket
				ticket, err = manager.ScheduleCheckpoint(ctx, path, kitdbengine.OpenOptions{}, kitdbnode.MaintenanceUrgent)
				if err == nil {
					_, err = ticket.Wait(ctx)
				}
			}
			if err == nil || errors.Is(err, kitdbengine.ErrDurabilityUncertain) != test.uncertain {
				t.Fatalf("wrong failure classification: uncertain=%t err=%v", test.uncertain, err)
			}
			wantCause := error(syscall.EIO)
			if test.kind == "short" {
				wantCause = io.ErrShortWrite
			}
			if test.kind == "space" {
				wantCause = syscall.ENOSPC
			}
			if !errors.Is(err, wantCause) {
				t.Fatalf("injected cause lost: %v", err)
			}
			marker, err := os.ReadFile(path + ".io-reached")
			if err != nil || string(marker) != test.point+"\n" {
				t.Fatalf("fault not reached exactly once: %q %v", marker, err)
			}
			if pool.Stats().InUse != 0 {
				t.Fatal("SQL connection leaked after failure")
			}
			if test.uncertain {
				if _, err := pool.ExecContext(ctx, "INSERT INTO items VALUES(67,34,$1),(68,34,$1)", checkpointCrashPayload); !errors.Is(err, kitdbengine.ErrUnavailable) {
					t.Fatalf("uncertain owner accepted another write: %v", err)
				}
				if _, err := lease.DB().Checkpoint(); !errors.Is(err, kitdbengine.ErrUnavailable) {
					t.Fatalf("uncertain owner accepted checkpoint: %v", err)
				}
				marker, err := os.ReadFile(path + ".io-reached")
				if err != nil || strings.Count(string(marker), "\n") != 1 {
					t.Fatal("failure was automatically retried")
				}
			} else {
				checkCheckpointCrashRows(t, engine, 32)
				stats, err := lease.DB().Stats()
				if err != nil || stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 || stats.FrontendTransactionBytes != 0 || stats.ActiveTransactions != 0 || stats.PendingCommits != 0 {
					t.Fatalf("resources did not drain: %+v %v", stats, err)
				}
				t.Setenv("KITDB_IO_FAULT_POINT", "")
				execute("INSERT INTO items VALUES(65,33,$1),(66,33,$1)", checkpointCrashPayload)
				if _, err := lease.DB().Checkpoint(); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("KITDB_IO_FAULT_POINT", "")
			if err := pool.Close(); err != nil {
				t.Fatal(err)
			}
			if err := engine.Close(); err != nil {
				t.Fatal(err)
			}
			if err := lease.Release(); err != nil {
				t.Fatal(err)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
			if stats := manager.Stats(); stats.ActiveLeases != 0 || stats.RunningMaintenance != 0 || stats.QueuedMaintenance != 0 {
				t.Fatalf("owner did not drain: %+v", stats)
			}
			recovered, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err := recovered.database.Verify(); err != nil {
				t.Fatal(err)
			}
			batches := 32
			if !test.uncertain {
				batches = 33
			}
			if test.commit && test.point == "wal-write" {
				batches = 31
			}
			// A WAL Sync failure is ambiguous. A complete frame may be present
			// despite no successful commit response; never replay that insert.
			if test.point == "wal-sync" {
				result, err := recovered.Execute(ctx, "SELECT COUNT(*) FROM items")
				if err != nil {
					t.Fatal(err)
				}
				count := result.Rows[0][0]
				if count == int64(62) {
					batches = 31
				} else if count != int64(64) {
					t.Fatalf("partial uncertain transaction: %v", count)
				}
			}
			checkCheckpointCrashRows(t, recovered, batches)
			if _, err := recovered.Execute(ctx, "UPDATE items SET payload=$1 WHERE id=1", checkpointCrashPayload); err != nil {
				t.Fatal(err)
			}
			if _, err := recovered.database.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			checkCheckpointCrashRows(t, recovered, batches)
		})
	}
}
