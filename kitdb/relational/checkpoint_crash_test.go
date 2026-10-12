//go:build kitdbfault

package relational

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

const checkpointCrashPayload = "acknowledged-sql-row"

func TestSQLCheckpointCrashHelper(t *testing.T) {
	root := os.Getenv("KITDB_CHECKPOINT_CRASH_CHILD")
	if root == "" {
		t.Skip("tagged subprocess helper")
	}
	point := os.Getenv("KITDB_CHECKPOINT_CRASH_POINT")
	full := os.Getenv("KITDB_CHECKPOINT_CRASH_FULL") == "1"
	reader := os.Getenv("KITDB_CHECKPOINT_CRASH_READER") == "1"
	t.Setenv("KITDB_CHECKPOINT_FAULT_POINT", "")
	path := filepath.Join(root, "publication.kitdb")
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	lease, err := manager.Acquire(context.Background(), path, kitdbengine.OpenOptions{})
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
	ctx := context.Background()
	stats := func() kitdbengine.Stats {
		t.Helper()
		value, err := lease.DB().Stats()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	execute := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	execute("CREATE TABLE items(id BIGINT PRIMARY KEY, batch BIGINT NOT NULL, payload TEXT NOT NULL)")
	execute("CREATE INDEX items_batch ON items(batch)")
	execute("CREATE TABLE preparation(id BIGINT PRIMARY KEY)")
	if _, err := lease.DB().Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if full {
		for attempt := 1; stats().MainSegments < 32 && attempt <= 64; attempt++ {
			execute("INSERT INTO preparation VALUES($1)", attempt)
			if _, err := lease.DB().Checkpoint(); err != nil {
				t.Fatal(err)
			}
		}
		if stats().MainSegments != 32 {
			t.Fatal("fixture failed to reach full-compaction boundary")
		}
	}
	journal, err := os.Create(filepath.Join(root, "ack.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	for batch := 1; batch <= 32; batch++ {
		execute("INSERT INTO items VALUES($1,$2,$3),($4,$2,$3)", batch*2-1, batch, checkpointCrashPayload, batch*2)
		if err := json.NewEncoder(journal).Encode(batch); err != nil {
			t.Fatal(err)
		}
		if err := journal.Sync(); err != nil {
			t.Fatal(err)
		}
	}
	if reader {
		tx, err := pool.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM items").Scan(&count); err != nil || count != 64 {
			t.Fatalf("reader count=%d err=%v", count, err)
		}
		if stats().ActiveSnapshots == 0 {
			t.Fatal("reader did not retain a kernel snapshot")
		}
	}
	state, err := os.Create(filepath.Join(root, "before.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	if err := json.NewEncoder(state).Encode(stats()); err != nil {
		t.Fatal(err)
	}
	if err := state.Sync(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KITDB_CHECKPOINT_FAULT_PATH", path)
	t.Setenv("KITDB_CHECKPOINT_FAULT_POINT", point)
	ticket, err := manager.ScheduleCheckpoint(ctx, path, kitdbengine.OpenOptions{}, kitdbnode.MaintenanceUrgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ticket.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	t.Fatal("checkpoint completed without reaching requested process-exit point")
}

func TestSQLCheckpointPublicationCrash(t *testing.T) {
	cases := []struct {
		point string
		full  bool
	}{
		{"append-body-written", false}, {"append-body-synced", false},
		{"superblock-written", false}, {"superblock-synced", false},
		{"compact-staging-synced", true}, {"compact-main-replaced", true},
		{"wal-staging-synced", false}, {"wal-replaced", false},
		{"wal-staging-synced", true}, {"wal-replaced", true},
	}
	for _, test := range cases {
		name := test.point
		if test.full {
			name += "-full"
		} else {
			name += "-reader"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "publication.kitdb")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			child := exec.CommandContext(ctx, executable, "-test.run=^TestSQLCheckpointCrashHelper$", "-test.timeout=25s")
			child.Env = append(os.Environ(), "KITDB_CHECKPOINT_CRASH_CHILD="+root, "KITDB_CHECKPOINT_CRASH_POINT="+test.point,
				fmt.Sprintf("KITDB_CHECKPOINT_CRASH_FULL=%d", boolInt(test.full)), fmt.Sprintf("KITDB_CHECKPOINT_CRASH_READER=%d", boolInt(!test.full)))
			output, err := child.CombinedOutput()
			if child.ProcessState == nil || child.ProcessState.ExitCode() != 73 || ctx.Err() != nil {
				t.Fatalf("exact-point hard exit failed: %v\n%s", err, output)
			}
			marker, err := os.ReadFile(path + ".fault-reached")
			if err != nil || string(marker) != test.point {
				t.Fatalf("wrong reached point: %q %v", marker, err)
			}
			journal, err := os.Open(filepath.Join(root, "ack.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			defer journal.Close()
			decoder := json.NewDecoder(journal)
			for batch := 1; batch <= 32; batch++ {
				var ack int
				if err := decoder.Decode(&ack); err != nil || ack != batch {
					t.Fatalf("ACK journal batch=%d err=%v", ack, err)
				}
			}
			if strings.Contains(test.point, "staging") {
				pattern := ".kitdb-wal-*.tmp"
				if test.full && test.point == "compact-staging-synced" {
					pattern = ".kitdb-main-*.tmp"
				}
				staging, err := filepath.Glob(filepath.Join(root, pattern))
				if err != nil || len(staging) != 1 {
					t.Fatalf("expected actual abandoned staging file: %v %v", staging, err)
				}
			}
			// Recovery ignores abandoned staging. Do not delete it before proving
			// that a fresh owner can read, write, checkpoint and reopen again.
			recovered, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer recovered.Close()
			if err := recovered.database.Verify(); err != nil {
				t.Fatal(err)
			}
			beforeBytes, err := os.ReadFile(filepath.Join(root, "before.json"))
			if err != nil {
				t.Fatal(err)
			}
			var before kitdbengine.Stats
			if err := json.Unmarshal(beforeBytes, &before); err != nil {
				t.Fatal(err)
			}
			after, err := recovered.database.Stats()
			if err != nil || after.LastTransaction != before.LastTransaction {
				t.Fatalf("recovery transaction mismatch: before=%+v after=%+v err=%v", before, after, err)
			}
			unpublished := test.point == "append-body-written" || test.point == "append-body-synced" || test.point == "compact-staging-synced"
			wantCheckpoint := before.LastTransaction
			if unpublished {
				wantCheckpoint = before.CheckpointTransaction
			}
			if after.CheckpointTransaction != wantCheckpoint {
				t.Fatalf("publication boundary selected wrong main: checkpoint=%d want=%d", after.CheckpointTransaction, wantCheckpoint)
			}
			if test.full && !unpublished && after.MainSegments != 1 {
				t.Fatalf("published full compaction has %d segments", after.MainSegments)
			}
			checkCheckpointCrashRows(t, recovered, 32)
			if _, err := recovered.Execute(ctx, "INSERT INTO items VALUES(65,33,$1),(66,33,$1)", checkpointCrashPayload); err != nil {
				t.Fatal(err)
			}
			if _, err := recovered.database.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			if err := recovered.database.Verify(); err != nil {
				t.Fatal(err)
			}
			if err := recovered.Close(); err != nil {
				t.Fatal(err)
			}
			second, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer second.Close()
			checkCheckpointCrashRows(t, second, 33)
			t.Logf("point=%s full=%t confirmed=32 recovery_write_and_checkpoint=ok", test.point, test.full)
		})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func checkCheckpointCrashRows(t *testing.T, engine *Engine, batches int) {
	t.Helper()
	ctx := context.Background()
	result, err := engine.Execute(ctx, "SELECT id,batch,payload FROM items ORDER BY id")
	if err != nil || len(result.Rows) != batches*2 {
		t.Fatalf("recovered rows=%d err=%v", len(result.Rows), err)
	}
	for row, values := range result.Rows {
		if fmt.Sprint(values) != fmt.Sprint([]any{row + 1, row/2 + 1, checkpointCrashPayload}) {
			t.Fatalf("row %d mismatched: %v", row, values)
		}
	}
	for batch := 1; batch <= batches; batch++ {
		indexed, err := engine.Execute(ctx, "EXPLAIN ANALYZE SELECT id,payload FROM items WHERE batch=$1 ORDER BY id", batch)
		if err != nil || indexed.Execution == nil || indexed.Execution.Path != "index-scan" || indexed.Execution.IndexEntriesScanned != 2 {
			t.Fatalf("secondary index batch=%d stats=%+v err=%v", batch, indexed.Execution, err)
		}
		rows, err := engine.Execute(ctx, "SELECT id,payload FROM items WHERE batch=$1 ORDER BY id", batch)
		if err != nil || len(rows.Rows) != 2 || fmt.Sprint(rows.Rows) != fmt.Sprint([][]any{{batch*2 - 1, checkpointCrashPayload}, {batch * 2, checkpointCrashPayload}}) {
			t.Fatalf("secondary index batch=%d rows=%v err=%v", batch, rows.Rows, err)
		}
	}
}
