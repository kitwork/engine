package kitsql_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/node"
)

type sqlCanaryProgress struct {
	count uint64
	last  time.Time
}

func (p *sqlCanaryProgress) observe(now time.Time, count uint64, window time.Duration) error {
	if count < p.count {
		return errors.New("ACK counter moved backwards")
	}
	if count > p.count {
		p.count, p.last = count, now
	}
	if now.Sub(p.last) >= window {
		return errors.New("adapter has no ACK progress within its window")
	}
	return nil
}

func TestSQLCanaryProgress(t *testing.T) {
	start := time.Unix(0, 0)
	p := sqlCanaryProgress{last: start}
	if err := p.observe(start.Add(29*time.Second), 0, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if p.observe(start.Add(30*time.Second), 0, 30*time.Second) == nil {
		t.Fatal("stall falsely passed")
	}
	if err := p.observe(start.Add(31*time.Second), 1, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	if p.observe(start.Add(32*time.Second), 0, 30*time.Second) == nil {
		t.Fatal("counter regression falsely passed")
	}
	other := sqlCanaryProgress{last: start}
	if other.observe(start.Add(31*time.Second), 0, 30*time.Second) == nil {
		t.Fatal("another adapter masked stall")
	}
}

type sqlCanaryPressureSample struct {
	ElapsedMS          int64
	ACKs               [3]uint64
	HeapBytes          uint64
	Snapshots, Waiters int
	Generation         uint64
	Segments           int
}

type sqlCanaryPressureResult struct {
	Mode                                          string
	RequestedMS, WorkloadMS                       int64
	ACKs, Reads, Conflicts, Busy                  [3]uint64
	SlowReads, SlowBusy, Checkpoints, Compactions uint64
	RecoveryACKs                                  [3]uint64
	RecoveryMS                                    int64
	PeakHeapBytes                                 uint64
	PeakGoroutines, PeakWaiters                   int
	Samples                                       []sqlCanaryPressureSample
	Success                                       bool
}

// Controlled mode accelerates the segment boundary with explicit jobs. Policy
// mode has no checkpoint worker and observes only SQL-triggered generations.
func runSQLCanaryPressure(ctx context.Context, pools []*sql.DB, expected []int64, kernel *kitdb.DB, manager *node.Manager, path string, options kitdb.OpenOptions) (sqlCanaryPressureResult, error) {
	policy := os.Getenv("KITDB_SQL_CANARY_POLICY") == "1"
	if policy && os.Getenv("KITDB_SQL_CANARY_PRESSURE") == "1" {
		return sqlCanaryPressureResult{}, errors.New("choose controlled pressure or automatic policy, not both")
	}
	seconds := 6
	if policy {
		seconds = 30
	}
	if source := os.Getenv("KITDB_SQL_CANARY_PRESSURE_SECONDS"); source != "" {
		value, err := strconv.Atoi(source)
		if err != nil || value < 6 || value > 30 {
			return sqlCanaryPressureResult{}, errors.New("pressure seconds must be 6..30")
		}
		seconds = value
	}
	r := sqlCanaryPressureResult{Mode: "controlled-checkpoint-pressure; not automatic policy qualification", RequestedMS: int64(seconds) * 1000}
	if policy {
		r.Mode = "automatic-checkpoint-policy; no checkpoint worker"
	}
	if len(pools) != 3 || len(expected) != sqlCanaryRows+1 {
		return r, errors.New("invalid pressure fixture")
	}
	var acks, reads, conflicts, busy [3]atomic.Uint64
	var slow, slowBusy, checkpoints, compactions atomic.Uint64
	stopped := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopped) }) }
	defer stop()
	failures := make(chan error, 1)
	fail := func(err error) {
		select {
		case failures <- err:
		default:
		}
		stop()
	}
	paused := func(duration time.Duration) bool {
		timer := time.NewTimer(duration)
		defer timer.Stop()
		select {
		case <-stopped:
			return false
		case <-ctx.Done():
			return false
		case <-timer.C:
			return true
		}
	}
	start := time.Now()
	baseline, err := kernel.Stats()
	if err != nil {
		return r, err
	}
	lastGeneration, lastSegments := baseline.MainGeneration, baseline.MainSegments
	var group sync.WaitGroup
	for adapter, pool := range pools {
		group.Add(1)
		go func(adapter int, pool *sql.DB) {
			defer group.Done()
			id := adapter + 4
			sequence := 0
			for paused(10 * time.Millisecond) {
				if policy {
					keys := (sqlCanaryRows-(adapter+4))/3 + 1
					id = adapter + 4 + 3*(sequence%keys)
					sequence++
				}
				op, cancel := context.WithTimeout(ctx, 5*time.Second)
				result, err := pool.ExecContext(op, "UPDATE items SET version=version+1 WHERE id=$1", id)
				if err == nil {
					count, countErr := result.RowsAffected()
					if countErr != nil || count != 1 {
						cancel()
						fail(fmt.Errorf("pressure affected rows: %d / %v", count, countErr))
						return
					}
					expected[id]++
					acks[adapter].Add(1)
				} else {
					switch sqlCanaryRejection(err) {
					case "conflict":
						conflicts[adapter].Add(1)
					case "busy":
						busy[adapter].Add(1)
					default:
						cancel()
						fail(sqlCanaryFailure(fmt.Sprintf("pressure adapter %d write outcome", adapter), err))
						return
					}
				}
				var version int64
				err = pool.QueryRowContext(op, "SELECT version FROM items WHERE id=$1", id).Scan(&version)
				cancel()
				if err != nil {
					if sqlCanaryRejection(err) == "busy" {
						busy[adapter].Add(1)
						continue
					}
					fail(sqlCanaryFailure(fmt.Sprintf("pressure adapter %d read", adapter), err))
					return
				}
				if version != expected[id] {
					fail(fmt.Errorf("pressure ACK mismatch for %d", id))
					return
				}
				reads[adapter].Add(1)
			}
		}(adapter, pool)
	}
	group.Add(1)
	go func() {
		defer group.Done()
		for paused(10 * time.Millisecond) {
			op, cancel := context.WithTimeout(ctx, 5*time.Second)
			tx, err := pools[0].BeginTx(op, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				cancel()
				if policy && sqlCanaryRejection(err) == "busy" {
					slowBusy.Add(1)
					continue
				}
				fail(err)
				return
			}
			var count int64
			err = tx.QueryRowContext(op, "SELECT COUNT(*) FROM items").Scan(&count)
			if err == nil && count != sqlCanaryRows {
				err = fmt.Errorf("slow reader count=%d", count)
			}
			var before, after int64
			if err == nil {
				err = tx.QueryRowContext(op, "SELECT version FROM items WHERE id=4").Scan(&before)
			}
			if err == nil {
				paused(150 * time.Millisecond)
				err = tx.QueryRowContext(op, "SELECT version FROM items WHERE id=4").Scan(&after)
				if err == nil && before != after {
					err = errors.New("slow reader lost its fixed snapshot")
				}
				if err == nil {
					slow.Add(1)
				}
			}
			rollbackErr := tx.Rollback()
			cancel()
			if err != nil && sqlCanaryRejection(err) != "busy" {
				fail(err)
				return
			}
			if err != nil {
				slowBusy.Add(1)
			}
			if rollbackErr != nil {
				fail(rollbackErr)
				return
			}
		}
	}()
	if !policy {
		group.Add(1)
		go func() {
			defer group.Done()
			for paused(40 * time.Millisecond) {
				before, err := kernel.Stats()
				if err != nil {
					fail(err)
					return
				}
				op, cancel := context.WithTimeout(ctx, 5*time.Second)
				ticket, err := manager.ScheduleCheckpoint(op, path, options, node.MaintenanceUrgent)
				if err == nil {
					_, err = ticket.Wait(op)
				}
				cancel()
				if err != nil {
					fail(fmt.Errorf("controlled checkpoint: %w", err))
					return
				}
				after, err := kernel.Stats()
				if err != nil {
					fail(err)
					return
				}
				if after.MainGeneration > before.MainGeneration {
					checkpoints.Add(1)
					if after.MainSegments < before.MainSegments {
						select {
						case <-stopped:
						default:
							compactions.Add(1)
						}
					}
				}
			}
		}()
	}
	progress := [3]sqlCanaryProgress{{last: start}, {last: start}, {last: start}}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	duration := time.NewTimer(time.Duration(seconds) * time.Second)
	defer duration.Stop()
	nextSample := start
	lastTick := start
	var phaseErr error
monitor:
	for {
		select {
		case <-stopped:
			break monitor
		case <-ctx.Done():
			phaseErr = ctx.Err()
			break monitor
		case <-duration.C:
			break monitor
		case now := <-ticker.C:
			if now.Sub(lastTick) > 2*time.Second {
				phaseErr = errors.New("pressure monitoring gap exceeds two seconds")
				break monitor
			}
			lastTick = now
			stats, err := kernel.Stats()
			if err != nil {
				phaseErr = err
				break monitor
			}
			if policy && stats.MainGeneration > lastGeneration {
				checkpoints.Add(stats.MainGeneration - lastGeneration)
				if stats.MainSegments < lastSegments {
					compactions.Add(1)
				}
				lastGeneration, lastSegments = stats.MainGeneration, stats.MainSegments
			}
			var memory runtime.MemStats
			runtime.ReadMemStats(&memory)
			r.PeakHeapBytes = max(r.PeakHeapBytes, memory.HeapAlloc)
			r.PeakGoroutines = max(r.PeakGoroutines, runtime.NumGoroutine())
			r.PeakWaiters = max(r.PeakWaiters, stats.SnapshotWaiters)
			sample := sqlCanaryPressureSample{ElapsedMS: now.Sub(start).Milliseconds(), HeapBytes: memory.HeapAlloc, Snapshots: stats.ActiveSnapshots, Waiters: stats.SnapshotWaiters, Generation: stats.MainGeneration, Segments: stats.MainSegments}
			for i := range acks {
				sample.ACKs[i] = acks[i].Load()
				if err := progress[i].observe(now, sample.ACKs[i], 30*time.Second); err != nil {
					phaseErr = fmt.Errorf("adapter %d: %w", i, err)
					break monitor
				}
			}
			if !now.Before(nextSample) {
				r.Samples = append(r.Samples, sample)
				nextSample = now.Add(time.Second)
			}
		}
	}
	// Stop between operations; do not intentionally cancel submitted writes.
	r.WorkloadMS = time.Since(start).Milliseconds()
	stop()
	group.Wait()
	select {
	case err := <-failures:
		phaseErr = errors.Join(phaseErr, err)
	default:
	}
	for i := range acks {
		r.ACKs[i], r.Reads[i], r.Conflicts[i], r.Busy[i] = acks[i].Load(), reads[i].Load(), conflicts[i].Load(), busy[i].Load()
		if r.ACKs[i] == 0 || r.Reads[i] == 0 {
			phaseErr = errors.Join(phaseErr, fmt.Errorf("adapter %d made no write/read progress", i))
		}
	}
	r.SlowReads, r.SlowBusy, r.Checkpoints, r.Compactions = slow.Load(), slowBusy.Load(), checkpoints.Load(), compactions.Load()
	if r.SlowReads == 0 || r.Compactions == 0 {
		phaseErr = errors.Join(phaseErr, errors.New("missing slow-reader/full-compaction coverage"))
	}
	if r.WorkloadMS < r.RequestedMS {
		phaseErr = errors.Join(phaseErr, errors.New("pressure workload ended early"))
	}
	if phaseErr == nil {
		recoveryStart := time.Now()
		recovery, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		for adapter, pool := range pools {
			id := adapter + 4
			result, err := pool.ExecContext(recovery, "UPDATE items SET version=version+1 WHERE id=$1", id)
			if err != nil {
				phaseErr = fmt.Errorf("recovery adapter %d write: %w", adapter, err)
				break
			}
			count, err := result.RowsAffected()
			if err != nil || count != 1 {
				phaseErr = fmt.Errorf("recovery affected rows: %d / %v", count, err)
				break
			}
			expected[id]++
			r.RecoveryACKs[adapter]++
		}
		r.RecoveryMS = time.Since(recoveryStart).Milliseconds()
	}
	r.Success = phaseErr == nil
	return r, phaseErr
}
