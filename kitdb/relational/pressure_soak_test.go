package relational

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/lib/pq"
)

type sqlPressureSoakRound struct {
	Seconds                                                                            float64
	Writes, Reads, Busy, Conflicts, CancelAttempts, ObservedCancellations, SlowReaders int64
	MaxOperationMicros                                                                 int64
	RecoverySeconds                                                                    float64
	RecoveryRejections                                                                 int64
	RecoveryWrites                                                                     int64
	MaxNoWriteSeconds                                                                  float64
	ZeroWriteWindows                                                                   int
	HeapPeak, HeapAfterDrain                                                           uint64
	GoroutinesAfterDrain                                                               int
	PeakSnapshotBytes, PeakTransactionBytes                                            int64
	PeakWaiters, PeakQueuedMaintenance                                                 int
	Checkpoints                                                                        uint64
	Drained                                                                            kitdbengine.Stats
}

func sqlPressureExpected(err error) bool {
	var pg *pq.Error
	return errors.Is(err, kitdbnode.ErrCheckpointBackoff) || errors.Is(err, kitdbnode.ErrMaintenanceQueueFull) ||
		errors.Is(err, kitdbengine.ErrSnapshotsActive) || errors.Is(err, kitdbengine.ErrSnapshotAdmissionTimeout) ||
		errors.Is(err, kitdbengine.ErrSnapshotAdmissionFull) || errors.Is(err, kitdbengine.ErrSnapshotMemory) ||
		errors.Is(err, kitdbengine.ErrActiveTransactionMemory) || errors.Is(err, kitdbengine.ErrTooManySnapshots) ||
		errors.Is(err, kitdbengine.ErrTransactionConflict) || (errors.As(err, &pg) && (pg.Code == "53300" || pg.Code == "40001"))
}

func TestSQLPressureSoak(t *testing.T) {
	if os.Getenv("KITDB_SQL_SOAK") != "1" {
		t.Skip("set KITDB_SQL_SOAK=1 for mixed native/pgwire soak")
	}
	seconds := 180
	if source := os.Getenv("KITDB_SQL_SOAK_SECONDS"); source != "" {
		var err error
		seconds, err = strconv.Atoi(source)
		if err != nil || seconds < 9 || seconds > 1800 || seconds%3 != 0 {
			t.Fatal("KITDB_SQL_SOAK_SECONDS must be 9..1800 and divisible by 3")
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "soak.kitdb")
	seed, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	options := kitdbengine.OpenOptions{SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	node, err := OpenPostgresNode(PostgresNodeOptions{Root: root, User: "kitdb", Password: "node-secret", Relational: Options{Kernel: options}})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	address, stop := startPostgresNodeTestServerWithOptions(t, node, PostgresServerOptions{MaxConnections: 32, QueryTimeout: 30 * time.Second, IdleTimeout: time.Minute})
	defer stop()
	remote := openPostgresNodeTestClient(t, address, "soak", "node-secret")
	defer remote.Close()
	remote.SetMaxOpenConns(12)
	remote.SetMaxIdleConns(12)
	native, err := node.OpenNativeDatabase("soak")
	if err != nil {
		t.Fatal(err)
	}
	defer native.Close()
	native.SetMaxOpenConns(12)
	native.SetMaxIdleConns(12)
	ctx := context.Background()
	for _, source := range []string{"CREATE TABLE items(id BIGINT PRIMARY KEY, version BIGINT NOT NULL, payload TEXT NOT NULL)", "CREATE INDEX items_version ON items(version)"} {
		if _, err := native.ExecContext(ctx, source); err != nil {
			t.Fatal(err)
		}
	}
	payload := strings.Repeat("soak", 512)
	const rows = 256
	for id := 1; id <= rows; id++ {
		if _, err := native.ExecContext(ctx, "INSERT INTO items VALUES($1,0,$2)", id, payload); err != nil {
			t.Fatal(err)
		}
	}
	lease, err := node.manager.Acquire(ctx, path, options)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	db := lease.DB()
	checkpoint, err := node.manager.ScheduleCheckpoint(ctx, path, options, kitdbnode.MaintenanceUrgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	var expected [rows + 1]atomic.Int64
	var rounds []sqlPressureSoakRound
	var initial runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&initial)
	checkData := func(pool *sql.DB) {
		t.Helper()
		result, err := pool.QueryContext(ctx, "SELECT id,version,payload FROM items")
		if err != nil {
			t.Fatal(err)
		}
		defer result.Close()
		seen := make(map[int64]bool, rows)
		for result.Next() {
			var id, version int64
			var value string
			if err := result.Scan(&id, &version, &value); err != nil {
				t.Fatal(err)
			}
			if id < 1 || id > rows || seen[id] || version != expected[id].Load() || value != payload {
				t.Fatalf("row/acknowledgement mismatch id=%d version=%d", id, version)
			}
			seen[id] = true
		}
		if err := result.Err(); err != nil {
			t.Fatal(err)
		}
		if len(seen) != rows {
			t.Fatalf("missing rows: %d", len(seen))
		}
		if err := result.Close(); err != nil {
			t.Fatal(err)
		}
		groups := make(map[int64]int)
		for id := 1; id <= rows; id++ {
			groups[expected[id].Load()]++
		}
		for version, want := range groups {
			indexed, err := pool.QueryContext(ctx, "SELECT id FROM items WHERE version=$1", version)
			if err != nil {
				t.Fatal(err)
			}
			seen := make(map[int64]bool)
			for indexed.Next() {
				var id int64
				if err := indexed.Scan(&id); err != nil {
					_ = indexed.Close()
					t.Fatal(err)
				}
				if id < 1 || id > rows || seen[id] || expected[id].Load() != version {
					_ = indexed.Close()
					t.Fatalf("incorrect secondary index id=%d version=%d", id, version)
				}
				seen[id] = true
			}
			if err := indexed.Err(); err != nil {
				_ = indexed.Close()
				t.Fatal(err)
			}
			_ = indexed.Close()
			if len(seen) != want {
				t.Fatalf("index version=%d count=%d want=%d", version, len(seen), want)
			}
		}
	}
	for round := 0; round < 3; round++ {
		started := time.Now()
		baseline := node.manager.Stats().CheckpointCompletions
		phase, cancel := context.WithTimeout(ctx, time.Duration(seconds/3)*time.Second)
		var group sync.WaitGroup
		var writes, reads, busy, conflicts, cancels, observed, slow, maxLatency atomic.Int64
		failures := make(chan error, 1)
		record := func(start time.Time, err error, read bool) {
			latency := time.Since(start).Microseconds()
			for old := maxLatency.Load(); latency > old && !maxLatency.CompareAndSwap(old, latency); old = maxLatency.Load() {
			}
			if err == nil {
				return
			}
			var canceledPG *pq.Error
			if read && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
				(phase.Err() != nil && errors.As(err, &canceledPG) && canceledPG.Code == "57014")) {
				return
			}
			if sqlPressureExpected(err) {
				var pg *pq.Error
				if errors.Is(err, kitdbengine.ErrTransactionConflict) || errors.Is(err, ErrTransactionConflict) || (errors.As(err, &pg) && pg.Code == "40001") {
					conflicts.Add(1)
				} else {
					busy.Add(1)
				}
				return
			}
			select {
			case failures <- err:
			default:
			}
			cancel()
		}
		for worker := 0; worker < 8; worker++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				for phase.Err() == nil {
					start := time.Now()
					// Do not intentionally cancel writes at the phase boundary.
					operation, done := context.WithTimeout(ctx, 30*time.Second)
					var err error
					if worker%2 == 0 {
						var tx *sql.Tx
						tx, err = native.BeginTx(operation, nil)
						if err == nil {
							_, err = tx.ExecContext(operation, "UPDATE items SET version=version+1 WHERE id=$1", worker+1)
							if err == nil {
								err = tx.Commit()
							}
							_ = tx.Rollback()
						}
					} else {
						_, err = remote.ExecContext(operation, "UPDATE items SET version=version+1 WHERE id=$1", worker+1)
					}
					if err == nil {
						expected[worker+1].Add(1)
						writes.Add(1)
					}
					done()
					record(start, err, false)
					if err != nil {
						time.Sleep(time.Millisecond)
					}
				}
			}(worker)
		}
		for worker := 0; worker < 4; worker++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				pool := native
				if worker%2 != 0 {
					pool = remote
				}
				for phase.Err() == nil {
					start := time.Now()
					var value string
					err := pool.QueryRowContext(phase, "SELECT payload FROM items WHERE id=$1", 16+worker).Scan(&value)
					if err == nil && value != payload {
						err = fmt.Errorf("read payload mismatch")
					}
					if err == nil {
						reads.Add(1)
					}
					record(start, err, true)
				}
			}(worker)
		}
		group.Add(2)
		go func() {
			defer group.Done()
			for phase.Err() == nil {
				start := time.Now()
				tx, err := native.BeginTx(phase, &sql.TxOptions{ReadOnly: true})
				if err == nil {
					var count int64
					err = tx.QueryRowContext(phase, "SELECT COUNT(*) FROM items").Scan(&count)
					if err == nil && count != rows {
						err = fmt.Errorf("slow reader count=%d", count)
					}
					if err == nil {
						slow.Add(1)
						select {
						case <-phase.Done():
						case <-time.After(200 * time.Millisecond):
						}
					}
					_ = tx.Rollback()
				}
				record(start, err, true)
			}
		}()
		go func() {
			defer group.Done()
			for phase.Err() == nil {
				start := time.Now()
				query, cancelQuery := context.WithCancel(phase)
				result, err := native.QueryContext(query, "SELECT id,payload FROM items")
				if err == nil {
					_ = result.Next()
					cancels.Add(1)
					cancelQuery()
					for result.Next() {
					}
					if errors.Is(result.Err(), context.Canceled) {
						observed.Add(1)
					}
					iterationError := result.Err()
					err = result.Close()
					if iterationError != nil && !errors.Is(iterationError, context.Canceled) {
						err = iterationError
					}
				}
				cancelQuery()
				record(start, err, true)
				time.Sleep(5 * time.Millisecond)
			}
		}()
		measurement := sqlPressureSoakRound{}
		lastProgress := started
		nextLog := started.Add(30 * time.Second)
		var lastWrites, loggedWrites int64
		// Samples are bounded by configured duration, with no per-query retention.
		for phase.Err() == nil {
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			measurement.HeapPeak = max(measurement.HeapPeak, memory.HeapAlloc)
			stats, err := db.Stats()
			if err != nil {
				record(time.Now(), err, false)
				break
			}
			measurement.PeakSnapshotBytes = max(measurement.PeakSnapshotBytes, stats.SnapshotBytes+stats.SnapshotReservedBytes)
			measurement.PeakTransactionBytes = max(measurement.PeakTransactionBytes, stats.FrontendTransactionBytes+int64(stats.ActiveTransactionBytes))
			measurement.PeakWaiters = max(measurement.PeakWaiters, stats.SnapshotWaiters)
			measurement.PeakQueuedMaintenance = max(measurement.PeakQueuedMaintenance, node.manager.Stats().QueuedMaintenance)
			now := time.Now()
			currentWrites := writes.Load()
			if currentWrites != lastWrites {
				lastProgress = now
				lastWrites = currentWrites
			}
			measurement.MaxNoWriteSeconds = max(measurement.MaxNoWriteSeconds, now.Sub(lastProgress).Seconds())
			if !now.Before(nextLog) {
				if currentWrites == loggedWrites {
					measurement.ZeroWriteWindows++
				}
				t.Logf("round %d progress %.0fs: writes=%d delta=%d reads=%d busy=%d checkpoints=%d heap=%d main_segments=%d", round+1, now.Sub(started).Seconds(), currentWrites, currentWrites-loggedWrites, reads.Load(), busy.Load(), node.manager.Stats().CheckpointCompletions-baseline, memory.HeapAlloc, stats.MainSegments)
				loggedWrites = currentWrites
				nextLog = now.Add(30 * time.Second)
			}
			if stats.SnapshotBytes+stats.SnapshotReservedBytes > stats.SnapshotMemoryLimitBytes || stats.FrontendTransactionBytes+int64(stats.ActiveTransactionBytes) > stats.TransactionMemoryLimitBytes {
				record(time.Now(), fmt.Errorf("kernel resource budget exceeded"), false)
			}
			select {
			case <-phase.Done():
			case <-time.After(time.Second):
			}
		}
		cancel()
		group.Wait()
		select {
		case err := <-failures:
			t.Fatalf("round %d unexpected error: %v", round+1, err)
		default:
		}
		deadline := time.Now().Add(10 * time.Second)
		for node.manager.Stats().RunningMaintenance != 0 || node.manager.Stats().QueuedMaintenance != 0 {
			if time.Now().After(deadline) {
				t.Fatal("maintenance did not drain")
			}
			time.Sleep(10 * time.Millisecond)
		}
		// All load readers are now stopped. Prove that pressure admission
		// resumes on a new acknowledged operation, not a forced checkpoint.
		recoveryStart := time.Now()
		recoveryCtx, stopRecovery := context.WithTimeout(ctx, 10*time.Second)
		for {
			_, err := native.ExecContext(recoveryCtx, "UPDATE items SET version=version+1 WHERE id=1")
			if err == nil {
				expected[1].Add(1)
				measurement.RecoveryWrites = 1
				break
			}
			if !sqlPressureExpected(err) || recoveryCtx.Err() != nil {
				stopRecovery()
				t.Fatalf("quiescent recovery failed in round %d: %v", round+1, err)
			}
			measurement.RecoveryRejections++
			time.Sleep(10 * time.Millisecond)
		}
		stopRecovery()
		measurement.RecoverySeconds = time.Since(recoveryStart).Seconds()
		stats, err := db.Stats()
		if err != nil {
			t.Fatal(err)
		}
		if stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 || stats.SnapshotWaiters != 0 || stats.SnapshotBytes != 0 || stats.SnapshotReservedBytes != 0 || stats.ActiveTransactions != 0 || stats.FrontendTransactionBytes != 0 || stats.ActiveTransactionBytes != 0 || stats.PendingCommits != 0 || native.Stats().InUse != 0 || remote.Stats().InUse != 0 {
			t.Fatalf("round %d retained ownership: %+v", round+1, stats)
		}
		checkData(native)
		runtime.GC()
		var memory runtime.MemStats
		runtime.ReadMemStats(&memory)
		measurement.Seconds = time.Since(started).Seconds()
		measurement.Writes, measurement.Reads, measurement.Busy, measurement.Conflicts = writes.Load(), reads.Load(), busy.Load(), conflicts.Load()
		measurement.CancelAttempts, measurement.ObservedCancellations, measurement.SlowReaders = cancels.Load(), observed.Load(), slow.Load()
		measurement.MaxOperationMicros = maxLatency.Load()
		measurement.HeapAfterDrain = memory.HeapAlloc
		measurement.GoroutinesAfterDrain = runtime.NumGoroutine()
		measurement.Checkpoints = node.manager.Stats().CheckpointCompletions - baseline
		measurement.Drained = stats
		if measurement.Writes == 0 || measurement.Reads == 0 || measurement.CancelAttempts == 0 || measurement.ObservedCancellations == 0 || measurement.SlowReaders == 0 {
			t.Fatalf("round %d missed required workload: %+v", round+1, measurement)
		}
		rounds = append(rounds, measurement)
		t.Logf("round %d: writes=%d reads=%d busy=%d checkpoints=%d heap after drain=%d", round+1, measurement.Writes, measurement.Reads, measurement.Busy, measurement.Checkpoints, measurement.HeapAfterDrain)
	}
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	_ = remote.Close()
	_ = native.Close()
	stop()
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	pool, err := OpenNativeSQL(reopened)
	if err != nil {
		t.Fatal(err)
	}
	checkData(pool)
	if err := reopened.database.Verify(); err != nil {
		t.Fatal(err)
	}
	_ = pool.Close()
	_ = reopened.Close()
	runtime.GC()
	var final runtime.MemStats
	runtime.ReadMemStats(&final)
	var acknowledged int64
	for id := 1; id <= rows; id++ {
		acknowledged += expected[id].Load()
	}
	writeProgressVerified := true
	for _, round := range rounds {
		if round.MaxNoWriteSeconds > 30 || round.ZeroWriteWindows != 0 {
			writeProgressVerified = false
		}
	}
	report := struct {
		Format, Platform, GoVersion                                       string
		RequestedSeconds                                                  int
		AcknowledgedWrites                                                int64
		HeapBefore, HeapAfterClose                                        uint64
		Rounds                                                            []sqlPressureSoakRound
		ExactDataVerified, ReopenVerified, WriteProgressVerified, Success bool
	}{"kitdb-sql-pressure-soak/v2", runtime.GOOS + "/" + runtime.GOARCH, runtime.Version(), seconds, acknowledged, initial.HeapAlloc, final.HeapAlloc, rounds, true, true, writeProgressVerified, writeProgressVerified}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if destination := os.Getenv("KITDB_SQL_SOAK_REPORT"); destination != "" {
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(destination, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("acknowledged writes=%d; heap before/after close=%d/%d; exact data/index/reopen verified", acknowledged, initial.HeapAlloc, final.HeapAlloc)
	if !writeProgressVerified {
		t.Fatal("continuous workload stalled: a zero-write window or more than 30 seconds without write progress")
	}
}
