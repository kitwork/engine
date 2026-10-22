package relational

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

const mixedRows = 1024

type mixedConfig struct {
	Name           string
	Databases      int
	Noisy          bool
	Duration       time.Duration
	ProfilePrefix  string
	ProfileMode    string
	ObserveFailure func(int, string, error) // Opt-in test diagnostics, called before owner cleanup.
}

type mixedLatency struct {
	Count      uint64  `json:"count"`
	PerSecond  float64 `json:"per_second"`
	P50UpperUS float64 `json:"p50_upper_us"`
	P95UpperUS float64 `json:"p95_upper_us"`
	P99UpperUS float64 `json:"p99_upper_us"`
	MaximumUS  float64 `json:"maximum_us"`
	FewSamples bool    `json:"fewer_than_100_samples"`
}

type mixedOperations struct {
	Point      mixedLatency `json:"point"`
	Index      mixedLatency `json:"index"`
	Write      mixedLatency `json:"write_including_capture_and_stage"`
	Commit     mixedLatency `json:"commit_only"`
	Checkpoint mixedLatency `json:"checkpoint_including_queue_and_drain"`
	Published  mixedLatency `json:"checkpoint_with_new_transaction"`
	Queue      mixedLatency `json:"maintenance_scheduler_wait_coarse"`
	Service    mixedLatency `json:"maintenance_service_including_handle_acquisition"`
}

type mixedMetrics struct {
	point, index, write, commit, checkpoint, published boundedLatencyHistogram
	queue, service                                     boundedLatencyHistogram
}

func (metrics *mixedMetrics) summary(seconds float64) mixedOperations {
	return mixedOperations{
		Point: mixedLatencyOf(&metrics.point, seconds), Index: mixedLatencyOf(&metrics.index, seconds),
		Write: mixedLatencyOf(&metrics.write, seconds), Commit: mixedLatencyOf(&metrics.commit, seconds),
		Checkpoint: mixedLatencyOf(&metrics.checkpoint, seconds), Published: mixedLatencyOf(&metrics.published, seconds),
		Queue: mixedLatencyOf(&metrics.queue, seconds), Service: mixedLatencyOf(&metrics.service, seconds),
	}
}

func mixedLatencyOf(histogram *boundedLatencyHistogram, seconds float64) mixedLatency {
	count := histogram.total.Load()
	return mixedLatency{
		Count: count, PerSecond: float64(count) / seconds,
		P50UpperUS: histogram.Quantile(.50), P95UpperUS: histogram.Quantile(.95), P99UpperUS: histogram.Quantile(.99),
		MaximumUS: float64(histogram.max.Load()) / float64(time.Microsecond), FewSamples: count < 100,
	}
}

func (metrics *mixedMetrics) merge(source *mixedMetrics) {
	targets := []*boundedLatencyHistogram{&metrics.point, &metrics.index, &metrics.write, &metrics.commit, &metrics.checkpoint, &metrics.published, &metrics.queue, &metrics.service}
	sources := []*boundedLatencyHistogram{&source.point, &source.index, &source.write, &source.commit, &source.checkpoint, &source.published, &source.queue, &source.service}
	for index, target := range targets {
		other := sources[index]
		for bucket := range target.counts {
			target.counts[bucket].Add(other.counts[bucket].Load())
		}
		target.total.Add(other.total.Load())
		target.max.Store(max(target.max.Load(), other.max.Load()))
	}
}

type mixedDrain struct {
	Snapshots        int   `json:"snapshots"`
	Reservations     int   `json:"reservations"`
	Waiters          int   `json:"waiters"`
	SnapshotBytes    int64 `json:"snapshot_bytes"`
	ReservedBytes    int64 `json:"reserved_bytes"`
	FrontendBytes    int64 `json:"frontend_bytes"`
	Transactions     int   `json:"transactions"`
	TransactionBytes int   `json:"transaction_bytes"`
	Commits          int   `json:"pending_commits"`
}

func mixedDrainOf(stats kitdbengine.Stats) mixedDrain {
	return mixedDrain{stats.ActiveSnapshots, stats.SnapshotReservations, stats.SnapshotWaiters,
		stats.SnapshotBytes, stats.SnapshotReservedBytes, stats.FrontendTransactionBytes,
		stats.ActiveTransactions, stats.ActiveTransactionBytes, stats.PendingCommits}
}

type mixedTenant struct {
	path, payload string
	engine        *Engine
	lease         *kitdbnode.Lease
	metrics       mixedMetrics
	expected      []int64 // Only its single writer mutates this oracle.
	acknowledged  uint64
	changedRows   uint64
	compactions   uint64
	initialSyncs  uint64
}

type mixedTenantReport struct {
	ID             int             `json:"id"`
	Role           string          `json:"role"`
	Operations     mixedOperations `json:"operations"`
	Acknowledged   uint64          `json:"acknowledged_transactions"`
	ChangedRows    uint64          `json:"acknowledged_row_updates"`
	CommitSyncs    uint64          `json:"commit_syncs"`
	Compactions    uint64          `json:"compactions_observed"`
	Drain          mixedDrain      `json:"drain"`
	ReopenVerified bool            `json:"reopen_rows_and_index_verified"`
}

type mixedMemory struct {
	HeapBeforeOpen   uint64 `json:"heap_before_open_bytes"`
	RSSBeforeOpen    uint64 `json:"rss_before_open_bytes"`
	HeapAtStart      uint64 `json:"heap_at_workload_start_bytes"`
	HeapPeak         uint64 `json:"sampled_heap_peak_bytes"`
	HeapAtEnd        uint64 `json:"heap_at_workload_end_bytes"`
	RSSAtStart       uint64 `json:"rss_at_workload_start_bytes"`
	RSSPeak          uint64 `json:"sampled_rss_peak_bytes"`
	RSSAtEnd         uint64 `json:"rss_at_workload_end_bytes"`
	Allocated        uint64 `json:"workload_total_allocated_bytes"`
	Mallocs          uint64 `json:"workload_mallocs"`
	HeapAfterCloseGC uint64 `json:"heap_after_close_gc_bytes"`
	RSSAfterCloseGC  uint64 `json:"rss_after_close_gc_bytes"`
	GoroutinesBefore int    `json:"goroutines_before_open"`
	GoroutinesAfter  int    `json:"goroutines_after_close_gc"`
	HandlesAvailable bool   `json:"process_handle_counts_available"`
	HandlesBefore    uint32 `json:"process_handles_before_open"`
	HandlesAfter     uint32 `json:"process_handles_after_close_gc"`
}

type mixedWindow struct {
	Name               string              `json:"name"`
	Databases          int                 `json:"databases"`
	Noisy              bool                `json:"noisy_database_zero"`
	MaintenanceWorkers int                 `json:"maintenance_workers"`
	RequestedMS        int64               `json:"requested_ms"`
	ElapsedMS          float64             `json:"elapsed_with_inflight_drain_ms"`
	Success            bool                `json:"success"`
	Failure            string              `json:"failure,omitempty"`
	Errors             []string            `json:"errors,omitempty"`
	All                mixedOperations     `json:"all_databases"`
	Neighbors          mixedOperations     `json:"databases_1_and_above"`
	Tenants            []mixedTenantReport `json:"tenants"`
	Memory             mixedMemory         `json:"process_memory"`
	ManagerClosed      bool                `json:"manager_closed"`
	OpenDatabases      int                 `json:"open_databases_after_close"`
	ActiveLeases       int                 `json:"leases_after_close"`
	Queued             int                 `json:"maintenance_queued_after_close"`
	Running            int                 `json:"maintenance_running_after_close"`
	ProfileMode        string              `json:"profile_mode,omitempty"`
	ManagerDrain       *kitdbnode.Stats    `json:"manager_drain,omitempty"`
	PostCloseDrain     *kitdbnode.Stats    `json:"post_close_drain,omitempty"`
}

// Opt-in bounded native SQL campaign. Use Go's subtest filter to run each case
// in a fresh process; ordinary tests only run the short smoke below.
func TestKitDBMixedCampaign(t *testing.T) {
	if os.Getenv("KITDB_MIXED_CAMPAIGN") != "1" {
		t.Skip("set KITDB_MIXED_CAMPAIGN=1 for the synthetic mixed workload")
	}
	seconds := 8
	if source := os.Getenv("KITDB_MIXED_SECONDS"); source != "" {
		var err error
		seconds, err = strconv.Atoi(source)
		if err != nil || seconds < 1 || seconds > 30 {
			t.Fatal("KITDB_MIXED_SECONDS must be 1..30")
		}
	}
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	repeats, err := mixedCampaignRepeats(os.Getenv("KITDB_MIXED_REPEATS"))
	if err != nil {
		t.Fatal(err)
	}
	report := struct {
		Schema                 int           `json:"schema"`
		OS                     string        `json:"os"`
		Arch                   string        `json:"arch"`
		Go                     string        `json:"go"`
		GOMAXPROCS             int           `json:"gomaxprocs"`
		Rows                   int           `json:"rows_per_database"`
		PayloadBytes           int           `json:"payload_bytes"`
		QueryTimeoutMS         int           `json:"query_timeout_ms"`
		CheckpointTimeoutMS    int           `json:"checkpoint_wait_timeout_ms"`
		ReadIntervalMS         int           `json:"per_reader_interval_ms"`
		WriteIntervalMS        int           `json:"normal_writer_interval_ms"`
		CheckpointIntervalMS   int           `json:"checkpoint_interval_ms"`
		PageCacheBytes         int           `json:"page_cache_bytes_per_database"`
		SnapshotBudgetBytes    int           `json:"snapshot_budget_bytes_per_database"`
		TransactionBudgetBytes int           `json:"transaction_budget_bytes_per_database"`
		MaintenanceWorkers     int           `json:"maximum_maintenance_workers"`
		Mode                   string        `json:"mode"`
		Repeats                int           `json:"repeats_in_same_process"`
		Cases                  []mixedWindow `json:"cases"`
	}{Schema: 2, OS: runtime.GOOS, Arch: runtime.GOARCH, Go: runtime.Version(), GOMAXPROCS: 4,
		Rows: mixedRows, PayloadBytes: 512, QueryTimeoutMS: 3000, CheckpointTimeoutMS: 5000,
		ReadIntervalMS: 10, WriteIntervalMS: 20, CheckpointIntervalMS: 100,
		PageCacheBytes: 256 << 10, SnapshotBudgetBytes: 8 << 20, TransactionBudgetBytes: 8 << 20,
		MaintenanceWorkers: 2, Repeats: repeats,
		Mode: "native relational SQL; query result cache off; paced closed-loop readers/normal writers; one unpaced 32-row writer in noisy mode; explicit managed checkpoints; no automatic checkpoint evaluator; histogram upper bounds; 25ms process memory sampling"}
	var output *os.File
	if path := os.Getenv("KITDB_MIXED_REPORT"); path != "" {
		var err error
		output, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		defer func() {
			encoder := json.NewEncoder(output)
			encoder.SetIndent("", "  ")
			if err := encoder.Encode(report); err != nil {
				t.Error(err)
			}
			if err := output.Sync(); err != nil {
				t.Error(err)
			}
		}()
	}
	for _, config := range []mixedConfig{
		{Name: "balanced_1", Databases: 1, Duration: time.Duration(seconds) * time.Second},
		{Name: "balanced_4", Databases: 4, Duration: time.Duration(seconds) * time.Second},
		{Name: "noisy_4", Databases: 4, Noisy: true, Duration: time.Duration(seconds) * time.Second},
		{Name: "balanced_16", Databases: 16, Duration: time.Duration(seconds) * time.Second},
		{Name: "noisy_16", Databases: 16, Noisy: true, Duration: time.Duration(seconds) * time.Second},
	} {
		t.Run(config.Name, func(t *testing.T) {
			for repeat := 1; repeat <= repeats; repeat++ {
				current := config
				current.ProfileMode = os.Getenv("KITDB_MIXED_PROFILE_MODE")
				if prefix := os.Getenv("KITDB_MIXED_PROFILE_PREFIX"); prefix != "" {
					current.ProfilePrefix = fmt.Sprintf("%s_%s_%d", prefix, config.Name, repeat)
				}
				if repeats > 1 {
					current.Name = fmt.Sprintf("%s_repeat_%d", config.Name, repeat)
				}
				window, err := runMixedWindow(t.TempDir(), current)
				if err != nil {
					window.Success, window.Failure = false, err.Error()
				}
				report.Cases = append(report.Cases, window)
				if err != nil {
					t.Fatal(err)
				}
				t.Logf("case=%s point_p99<=%.0fus index_p99<=%.0fus commit_p99<=%.0fus checkpoint_p99<=%.0fus ack=%d handles=%d->%d",
					current.Name, window.All.Point.P99UpperUS, window.All.Index.P99UpperUS,
					window.All.Commit.P99UpperUS, window.All.Checkpoint.P99UpperUS, window.All.Commit.Count,
					window.Memory.HandlesBefore, window.Memory.HandlesAfter)
			}
		})
	}
}

func mixedCampaignRepeats(source string) (int, error) {
	if source == "" {
		return 1, nil
	}
	repeats, err := strconv.Atoi(source)
	if err != nil || repeats < 1 || repeats > 3 {
		return 0, fmt.Errorf("KITDB_MIXED_REPEATS must be 1..3")
	}
	return repeats, nil
}

func runMixedWindow(root string, config mixedConfig) (report mixedWindow, resultErr error) {
	report.Name, report.Databases, report.Noisy, report.RequestedMS = config.Name, config.Databases, config.Noisy, config.Duration.Milliseconds()
	if config.Databases < 1 || config.Databases > 16 || config.Duration <= 0 || config.Duration > 30*time.Second || (config.Noisy && config.Databases < 2) {
		return report, fmt.Errorf("invalid mixed-workload configuration")
	}
	report.MaintenanceWorkers = min(2, config.Databases)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runtime.GC()
	baseline := shoppingFleetMemoryPointNow()
	report.Memory.HeapBeforeOpen, report.Memory.RSSBeforeOpen = baseline.heap, baseline.rss
	report.Memory.GoroutinesBefore = runtime.NumGoroutine()
	report.Memory.HandlesBefore, report.Memory.HandlesAvailable = mixedProcessHandles()
	options := kitdbengine.OpenOptions{PageCacheBytes: 256 << 10, SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{
		MaxOpenDatabases: config.Databases, MaxPageCacheBytes: int64(config.Databases) * options.PageCacheBytes,
		DefaultPageCacheBytes: options.PageCacheBytes, MaxConcurrentOpens: min(2, config.Databases),
		MaxConcurrentMaintenance: report.MaintenanceWorkers, MaxQueuedMaintenance: config.Databases * 2, MaxMaintenancePerDatabase: 1,
	})
	if err != nil {
		return report, err
	}
	var tenants []*mixedTenant
	defer func() {
		resultErr = errors.Join(resultErr, closeMixedOwners(tenants, manager))
		if manager != nil {
			stats := manager.Stats()
			report.PostCloseDrain = &stats
		} else {
			report.PostCloseDrain = report.ManagerDrain
		}
		if resultErr != nil {
			report.Success, report.Failure = false, resultErr.Error()
		}
	}()
	for id := 0; id < config.Databases; id++ {
		tenant := &mixedTenant{path: filepath.Join(root, fmt.Sprintf("tenant_%02d.kitdb", id)), expected: make([]int64, mixedRows)}
		tenants = append(tenants, tenant)
		tenant.lease, err = manager.Acquire(ctx, tenant.path, options)
		if err != nil {
			return report, err
		}
		tenant.engine, err = Attach(tenant.lease.DB(), Options{Kernel: options})
		if err != nil {
			return report, err
		}
		if tenant.engine.queryCache != nil {
			return report, fmt.Errorf("mixed workload must not use the query result cache")
		}
		if err := seedMixedTenant(ctx, tenant, id); err != nil {
			return report, err
		}
		stats, err := tenant.lease.DB().Stats()
		if err != nil {
			return report, err
		}
		tenant.initialSyncs = stats.CommitSyncs
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	stopWork := func() { stopOnce.Do(func() { close(stop) }) }
	var failureMu sync.Mutex
	var observeOnce sync.Once
	recordFailure := func(id int, operation string, err error) {
		failureMu.Lock()
		if len(report.Errors) < 8 {
			report.Errors = append(report.Errors, fmt.Sprintf("database %d %s: %v", id, operation, err))
		}
		failureMu.Unlock()
		stopWork()
		if config.ObserveFailure != nil {
			observeOnce.Do(func() { config.ObserveFailure(id, operation, err) })
		}
	}
	var workers sync.WaitGroup
	start := make(chan struct{})
	launch := func(id int, operation string, interval time.Duration, call func(context.Context) error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			var ticker *time.Ticker
			if interval != 0 {
				ticker = time.NewTicker(interval)
				defer ticker.Stop()
			}
			for {
				select {
				case <-stop:
					return
				default:
				}
				if err := call(ctx); err != nil {
					recordFailure(id, operation, err)
					return
				}
				if ticker != nil {
					select {
					case <-ticker.C:
					case <-stop:
						return
					}
				}
			}
		}()
	}
	for id, tenant := range tenants {
		pointSequence, indexSequence := 0, 0
		launch(id, "point", 10*time.Millisecond, func(parent context.Context) error {
			queryCtx, done := context.WithTimeout(parent, 3*time.Second)
			defer done()
			requestedID := (pointSequence * 37) % mixedRows
			at := benchclock.Now()
			result, err := tenant.engine.Execute(queryCtx, "SELECT id,version,bucket,payload FROM items WHERE id=$1", requestedID)
			tenant.metrics.point.Observe(benchclock.Since(at))
			pointSequence++
			if err != nil {
				return err
			}
			if len(result.Rows) != 1 {
				return fmt.Errorf("point returned %d rows", len(result.Rows))
			}
			if len(result.Rows[0]) != 4 || result.Rows[0][0] != int64(requestedID) {
				return fmt.Errorf("point returned the wrong identity")
			}
			return checkMixedVisible(result.Rows, tenant.payload, -1)
		})
		launch(id, "index", 10*time.Millisecond, func(parent context.Context) error {
			queryCtx, done := context.WithTimeout(parent, 3*time.Second)
			defer done()
			bucket := indexSequence % 64
			at := benchclock.Now()
			result, err := tenant.engine.Execute(queryCtx, "SELECT id,version,bucket,payload FROM items WHERE bucket=$1 ORDER BY id LIMIT 16", bucket)
			tenant.metrics.index.Observe(benchclock.Since(at))
			indexSequence++
			if err != nil {
				return err
			}
			return checkMixedVisible(result.Rows, tenant.payload, int64(bucket))
		})
		batch, interval := 1, 20*time.Millisecond
		if config.Noisy && id == 0 {
			batch, interval = 32, 0
		}
		launch(id, "write", interval, func(parent context.Context) error {
			return writeMixedTenant(parent, tenant, batch)
		})
		stats, _ := tenant.lease.DB().Stats()
		lastTx, lastSegments := stats.CheckpointTransaction, stats.MainSegments
		launch(id, "checkpoint", 100*time.Millisecond, func(parent context.Context) error {
			checkpointCtx, done := context.WithTimeout(parent, 5*time.Second)
			defer done()
			at := benchclock.Now()
			ticket, err := manager.ScheduleCheckpoint(checkpointCtx, tenant.path, options, kitdbnode.MaintenanceNormal)
			var result kitdbnode.MaintenanceResult
			if err == nil {
				result, err = ticket.Wait(checkpointCtx)
			}
			elapsed := benchclock.Since(at)
			tenant.metrics.checkpoint.Observe(elapsed)
			if err != nil {
				return err
			}
			tenant.metrics.queue.Observe(result.StartedAt.Sub(result.QueuedAt))
			tenant.metrics.service.Observe(result.Duration)
			if result.Transaction > lastTx {
				tenant.metrics.published.Observe(elapsed)
			}
			stats, err := tenant.lease.DB().Stats()
			if err != nil {
				return err
			}
			if stats.MainSegments < lastSegments {
				tenant.compactions++
			}
			lastTx, lastSegments = result.Transaction, stats.MainSegments
			return nil
		})
	}
	runtime.GC()
	finishProfile, err := startMixedProfile(config.ProfileMode, config.ProfilePrefix)
	if err != nil {
		stopWork()
		close(start)
		workers.Wait()
		return report, err
	}
	report.ProfileMode = config.ProfileMode
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	stopMemory := startShoppingFleetMemorySampler()
	at := benchclock.Now()
	close(start)
	if err := waitMixedWindow(ctx, stop, config.Duration, func() time.Duration { return benchclock.Since(at) }); err != nil {
		recordFailure(-1, "window wait", err)
	}
	stopWork()
	workers.Wait() // Do not cancel admitted operations just because the window ended.
	report.ElapsedMS = milliseconds(benchclock.Since(at))
	memory := stopMemory()
	runtime.ReadMemStats(&after)
	report.Memory.HeapAtStart, report.Memory.HeapPeak, report.Memory.HeapAtEnd = memory.heapStart, memory.heapPeak, memory.heapEnd
	report.Memory.RSSAtStart, report.Memory.RSSPeak, report.Memory.RSSAtEnd = memory.rssStart, memory.rssPeak, memory.rssEnd
	report.Memory.Allocated, report.Memory.Mallocs = after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
	if err := finishProfile(); err != nil {
		return report, err
	}
	var all, neighbors mixedMetrics
	for id, tenant := range tenants {
		stats, err := tenant.lease.DB().Stats()
		if err != nil {
			return report, err
		}
		drain := mixedDrainOf(stats)
		if drain != (mixedDrain{}) {
			return report, fmt.Errorf("database %d retained resources: %+v", id, drain)
		}
		if stats.CommitSyncs-tenant.initialSyncs != tenant.acknowledged {
			return report, fmt.Errorf("database %d lost commit/sync accounting", id)
		}
		if tenant.acknowledged == 0 || tenant.metrics.point.total.Load() == 0 || tenant.metrics.index.total.Load() == 0 || tenant.metrics.published.total.Load() == 0 {
			return report, fmt.Errorf("database %d made no required progress", id)
		}
		all.merge(&tenant.metrics)
		if id != 0 {
			neighbors.merge(&tenant.metrics)
		}
		role := "normal"
		if config.Noisy && id == 0 {
			role = "noisy"
		}
		report.Tenants = append(report.Tenants, mixedTenantReport{ID: id, Role: role, Operations: tenant.metrics.summary(report.ElapsedMS / 1000),
			Acknowledged: tenant.acknowledged, ChangedRows: tenant.changedRows, CommitSyncs: stats.CommitSyncs - tenant.initialSyncs,
			Compactions: tenant.compactions, Drain: drain})
	}
	report.All, report.Neighbors = all.summary(report.ElapsedMS/1000), neighbors.summary(report.ElapsedMS/1000)
	managerStats := manager.Stats()
	if managerStats.QueuedMaintenance != 0 || managerStats.RunningMaintenance != 0 || managerStats.MaintenanceFailures != 0 {
		err := fmt.Errorf("maintenance did not drain cleanly: queued=%d running=%d failed=%d", managerStats.QueuedMaintenance, managerStats.RunningMaintenance, managerStats.MaintenanceFailures)
		if len(report.Errors) != 0 {
			err = errors.Join(err, errors.New(strings.Join(report.Errors, "; ")))
		}
		return report, err
	}
	// Release every managed handle before independently reopening any file.
	if err := closeMixedOwners(tenants, manager); err != nil {
		return report, err
	}
	managerStats = manager.Stats()
	report.ManagerDrain = &managerStats
	report.ManagerClosed, report.OpenDatabases, report.ActiveLeases = managerStats.Closed, managerStats.OpenDatabases, managerStats.ActiveLeases
	report.Queued, report.Running = managerStats.QueuedMaintenance, managerStats.RunningMaintenance
	if !mixedManagerDrained(managerStats) {
		return report, fmt.Errorf("manager did not release owned resources")
	}
	for id, tenant := range tenants {
		if err := verifyMixedReopen(ctx, tenant, options); err != nil {
			return report, fmt.Errorf("database %d reopen: %w", id, err)
		}
		report.Tenants[id].ReopenVerified = true
	}
	tenants, manager = nil, nil
	runtime.GC()
	runtime.GC()
	closed := shoppingFleetMemoryPointNow()
	report.Memory.HeapAfterCloseGC, report.Memory.RSSAfterCloseGC = closed.heap, closed.rss
	report.Memory.GoroutinesAfter = runtime.NumGoroutine()
	var handlesOK bool
	report.Memory.HandlesAfter, handlesOK = mixedProcessHandles()
	report.Memory.HandlesAvailable = report.Memory.HandlesAvailable && handlesOK
	if len(report.Errors) != 0 {
		return report, errors.New(strings.Join(report.Errors, "; "))
	}
	report.Success = true
	return report, nil
}

// A Windows timer wakeup can precede the QPC deadline by a fraction of a
// millisecond. Keep the requested minimum in the measurement clock, not by
// weakening the elapsed-time oracle or rounding its reported value upward.
func waitMixedWindow(ctx context.Context, stop <-chan struct{}, duration time.Duration, elapsed func() time.Duration) error {
	timer := time.NewTimer(max(0, duration-elapsed()))
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			if remaining := duration - elapsed(); remaining > 0 {
				timer.Reset(remaining)
				continue
			}
			return nil
		case <-stop:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func TestMixedWindowDeadline(t *testing.T) {
	const duration = 2 * time.Millisecond
	calls := 0
	elapsed := func() time.Duration {
		calls++
		switch calls {
		case 1:
			return 0
		case 2:
			return duration - time.Millisecond
		default:
			return duration
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := waitMixedWindow(ctx, make(chan struct{}), duration, elapsed); err != nil || calls != 3 {
		t.Fatalf("early wakeup ended window: clock calls=%d error=%v", calls, err)
	}
	stopped := make(chan struct{})
	close(stopped)
	if err := waitMixedWindow(ctx, stopped, time.Hour, func() time.Duration { return 0 }); err != nil {
		t.Fatal(err)
	}
	cancelled, cancelNow := context.WithCancel(context.Background())
	cancelNow()
	if err := waitMixedWindow(cancelled, make(chan struct{}), time.Hour, func() time.Duration { return 0 }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func mixedManagerDrained(stats kitdbnode.Stats) bool {
	return mixedManagerReleased(stats) && stats.OpenFailures == 0 && stats.MaintenanceFailures == 0 && stats.LastCloseError == ""
}

func mixedManagerReleased(stats kitdbnode.Stats) bool {
	return stats.Closed && stats.ManagedDatabases == 0 && stats.OpenDatabases == 0 &&
		stats.OpeningDatabases == 0 && stats.ClosingDatabases == 0 && stats.ActiveLeases == 0 &&
		stats.WaitingAcquisitions == 0 && stats.ReservedPageCacheBytes == 0 &&
		stats.WarmReservedPageCacheBytes == 0 && stats.QueuedMaintenance == 0 &&
		stats.RunningMaintenance == 0 && !stats.MultiDatabaseMaintenanceRunning
}

func closeMixedOwners(tenants []*mixedTenant, manager *kitdbnode.Manager) error {
	var result error
	for _, tenant := range tenants {
		if tenant.engine != nil {
			result = errors.Join(result, tenant.engine.Close())
		}
		if tenant.lease != nil {
			result = errors.Join(result, tenant.lease.Release())
		}
	}
	if manager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		result = errors.Join(result, manager.CloseContext(ctx))
	}
	return result
}

func seedMixedTenant(ctx context.Context, tenant *mixedTenant, id int) error {
	tenant.payload = fmt.Sprintf("tenant-%02d:", id) + strings.Repeat("x", 502)
	for _, query := range []string{
		"CREATE TABLE items(id BIGINT PRIMARY KEY,version BIGINT NOT NULL,bucket BIGINT NOT NULL,payload TEXT NOT NULL)",
		"CREATE INDEX items_bucket ON items(bucket)",
	} {
		if _, err := tenant.engine.Execute(ctx, query); err != nil {
			return err
		}
	}
	for start := 0; start < mixedRows; start += 128 {
		var query strings.Builder
		query.WriteString("INSERT INTO items VALUES ")
		for row := start; row < start+128; row++ {
			if row != start {
				query.WriteByte(',')
			}
			tenant.expected[row] = int64(row % 64)
			fmt.Fprintf(&query, "(%d,%d,%d,'%s')", row, row%64, row%64, tenant.payload)
		}
		if _, err := tenant.engine.Execute(ctx, query.String()); err != nil {
			return err
		}
	}
	if _, err := tenant.lease.DB().Checkpoint(); err != nil {
		return err
	}
	for _, query := range []string{
		"EXPLAIN ANALYZE SELECT id,version,bucket,payload FROM items WHERE id=0",
		"EXPLAIN ANALYZE SELECT id,version,bucket,payload FROM items WHERE bucket=0 ORDER BY id LIMIT 16",
	} {
		result, err := tenant.engine.Execute(ctx, query)
		if err != nil {
			return err
		}
		if result.Execution == nil || (result.Execution.Path != "primary-lookup" && !strings.HasPrefix(result.Execution.Path, "index")) {
			return fmt.Errorf("required lookup/index path missing: %+v", result.Execution)
		}
	}
	return nil
}

func writeMixedTenant(parent context.Context, tenant *mixedTenant, batch int) error {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	sequence := int64(tenant.acknowledged) + 65
	start := int(tenant.acknowledged*uint64(batch)) % mixedRows
	at := benchclock.Now()
	tx, err := tenant.engine.BeginTransaction(ctx, TransactionOptions{})
	if err != nil {
		tenant.metrics.write.Observe(benchclock.Since(at))
		return err
	}
	defer tx.Rollback()
	result, err := tx.Execute(ctx, "UPDATE items SET version=$1,bucket=$2 WHERE id>=$3 AND id<$4", sequence, sequence%64, start, start+batch)
	if err != nil || result.Affected != int64(batch) {
		tenant.metrics.write.Observe(benchclock.Since(at))
		return fmt.Errorf("update affected=%d: %w", result.Affected, err)
	}
	commitAt := benchclock.Now()
	_, err = tx.Commit(ctx)
	tenant.metrics.commit.Observe(benchclock.Since(commitAt))
	tenant.metrics.write.Observe(benchclock.Since(at))
	if err != nil {
		return err
	}
	for row := start; row < start+batch; row++ {
		tenant.expected[row] = sequence
	}
	tenant.acknowledged++
	tenant.changedRows += uint64(batch)
	return nil
}

func checkMixedVisible(rows [][]any, payload string, bucket int64) error {
	previous := int64(-1)
	for _, row := range rows {
		if len(row) != 4 {
			return fmt.Errorf("unexpected row width %d", len(row))
		}
		id, idOK := row[0].(int64)
		version, versionOK := row[1].(int64)
		actualBucket, bucketOK := row[2].(int64)
		if !idOK || !versionOK || !bucketOK || id < 0 || id >= mixedRows || id <= previous || version < 0 || actualBucket != version%64 || row[3] != payload || (bucket >= 0 && bucket != actualBucket) {
			return fmt.Errorf("inconsistent row id=%v version=%v bucket=%v", row[0], row[1], row[2])
		}
		previous = id
	}
	return nil
}

func verifyMixedReopen(ctx context.Context, tenant *mixedTenant, options kitdbengine.OpenOptions) (resultErr error) {
	engine, err := OpenWithOptions(tenant.path, Options{Kernel: options})
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, engine.Close()) }()
	if err := engine.database.Verify(); err != nil {
		return err
	}
	result, err := engine.Execute(ctx, "SELECT id,version,bucket,payload FROM items ORDER BY id")
	if err != nil {
		return err
	}
	if len(result.Rows) != mixedRows {
		return fmt.Errorf("row count=%d, want %d", len(result.Rows), mixedRows)
	}
	if err := checkMixedVisible(result.Rows, tenant.payload, -1); err != nil {
		return err
	}
	for id, row := range result.Rows {
		if row[0] != int64(id) || row[1] != tenant.expected[id] {
			return fmt.Errorf("acknowledged row %d changed: got=%v want_version=%d", id, row[:3], tenant.expected[id])
		}
	}
	for bucket := int64(0); bucket < 64; bucket++ {
		result, err := engine.Execute(ctx, "SELECT id,version,bucket,payload FROM items WHERE bucket=$1 ORDER BY id", bucket)
		if err != nil {
			return err
		}
		if err := checkMixedVisible(result.Rows, tenant.payload, bucket); err != nil {
			return err
		}
		position := 0
		for id, version := range tenant.expected {
			if version%64 != bucket {
				continue
			}
			if position >= len(result.Rows) || result.Rows[position][0] != int64(id) || result.Rows[position][1] != version {
				return fmt.Errorf("secondary index lost acknowledged row %d", id)
			}
			position++
		}
		if position != len(result.Rows) {
			return fmt.Errorf("secondary index has extra rows for bucket %d", bucket)
		}
	}
	return nil
}

func TestMixedCampaignSmoke(t *testing.T) {
	for _, config := range []mixedConfig{{Name: "one", Databases: 1, Duration: 350 * time.Millisecond}, {Name: "noisy", Databases: 2, Noisy: true, Duration: 350 * time.Millisecond}} {
		t.Run(config.Name, func(t *testing.T) {
			report, err := runMixedWindow(t.TempDir(), config)
			if err != nil {
				t.Fatal(err)
			}
			if !report.Success || len(report.Tenants) != config.Databases || !report.ManagerClosed || report.All.Commit.Count == 0 {
				t.Fatalf("incomplete smoke report: %+v", report)
			}
			if config.Databases > 1 && report.Neighbors.Point.Count == 0 {
				t.Fatal("neighbor made no progress")
			}
		})
	}
}

func TestMixedCampaignRejectsInvalidConfig(t *testing.T) {
	for _, config := range []mixedConfig{{Databases: 0, Duration: time.Second}, {Databases: 17, Duration: time.Second}, {Databases: 1, Noisy: true, Duration: time.Second}, {Databases: 1}} {
		if _, err := runMixedWindow(t.TempDir(), config); err == nil {
			t.Fatal("invalid configuration accepted")
		}
	}
}

func TestMixedVisibleOracleRejectsCorruption(t *testing.T) {
	for _, rows := range [][][]any{
		{{int64(1), int64(3), int64(2), "tenant"}},
		{{int64(1), int64(3), int64(3), "other-tenant"}},
		{{int64(1), int64(3), int64(3), "tenant"}, {int64(1), int64(3), int64(3), "tenant"}},
	} {
		if err := checkMixedVisible(rows, "tenant", -1); err == nil {
			t.Fatal("corrupt row accepted")
		}
	}
}

func TestMixedMetricsMergeUsesSamplesNotPercentileAverages(t *testing.T) {
	var first, second, merged mixedMetrics
	first.point.Observe(10 * time.Microsecond)
	first.point.Observe(20 * time.Microsecond)
	second.point.Observe(3 * time.Millisecond)
	merged.merge(&first)
	merged.merge(&second)
	result := merged.summary(2)
	if result.Point.Count != 3 || result.Point.PerSecond != 1.5 || result.Point.P50UpperUS != 25 || result.Point.P99UpperUS != 3000 || result.Point.MaximumUS != 3000 || !result.Point.FewSamples {
		t.Fatalf("incorrect merged distribution: %+v", result.Point)
	}
	if result.Commit.Count != 0 || result.Commit.P99UpperUS != 0 {
		t.Fatalf("empty distribution: %+v", result.Commit)
	}
}

func TestMixedCampaignRepeatBound(t *testing.T) {
	for _, source := range []string{"", "1", "2", "3"} {
		if count, err := mixedCampaignRepeats(source); err != nil || count < 1 || count > 3 {
			t.Fatalf("repeat %q=%d: %v", source, count, err)
		}
	}
	for _, source := range []string{"0", "4", "-1", "forever"} {
		if _, err := mixedCampaignRepeats(source); err == nil {
			t.Fatalf("unbounded repeat %q accepted", source)
		}
	}
}
