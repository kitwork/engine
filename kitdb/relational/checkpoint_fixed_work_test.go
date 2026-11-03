package relational

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/internal/benchclock"
	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

type fixedWorkConfig struct {
	Databases, Writes, Reads, CheckpointEvery int
	ProfileMode, ProfilePrefix                string
	Workload                                  string
	LockProfile                               bool
}
type fixedCheckpointSample struct {
	Database    int    `json:"database"`
	Transaction uint64 `json:"transaction"`
	TotalNS     int64  `json:"qpc_total_ns"`
	QueueNS     int64  `json:"coarse_queue_ns"`
	ServiceNS   int64  `json:"coarse_service_ns"`
}
type fixedWorkReport struct {
	Config             fixedWorkConfig         `json:"config"`
	ElapsedNS          int64                   `json:"workload_elapsed_ns"`
	ACKs               uint64                  `json:"acknowledged_commits"`
	Points             int                     `json:"point_reads"`
	Indexes            int                     `json:"index_reads"`
	Samples            []fixedCheckpointSample `json:"checkpoints"`
	Allocated, Mallocs uint64
	GCCycles           uint32            `json:"gc_cycles"`
	GCPauseNS          uint64            `json:"gc_pause_ns"`
	HeapBefore         uint64            `json:"heap_before_bytes"`
	HeapAfter          uint64            `json:"heap_after_bytes"`
	GOGC               uint64            `json:"gogc_percent"`
	GoMemoryLimit      uint64            `json:"go_memory_limit_bytes"`
	Manager            kitdbnode.Stats   `json:"manager_after_close"`
	Verified           int               `json:"reopened_verified_databases"`
	Success            bool              `json:"success"`
	Failure            string            `json:"failure,omitempty"`
	LockProfiles       []json.RawMessage `json:"lock_profiles,omitempty"`
}

func validateFixedWork(c fixedWorkConfig) error {
	if c.Databases < 1 || c.Databases > 16 || c.Writes < 0 || c.Writes > 128 || c.Reads < 0 || c.Reads > 512 {
		return fmt.Errorf("invalid bounded fixed-work configuration")
	}
	switch c.Workload {
	case "", "mixed":
		if c.Writes < 4 || c.Reads < 1 {
			return fmt.Errorf("mixed workload requires writes and reads")
		}
	case "write":
		if c.Writes < 4 || c.Reads != 0 {
			return fmt.Errorf("write workload requires only writes")
		}
	case "read":
		if c.Writes != 0 || c.Reads < 1 || c.CheckpointEvery != 0 {
			return fmt.Errorf("read workload requires only reads")
		}
	default:
		return fmt.Errorf("unknown fixed workload")
	}
	if c.Writes > 0 && (c.CheckpointEvery < 1 || c.Writes%c.CheckpointEvery != 0) {
		return fmt.Errorf("invalid checkpoint cadence")
	}
	if (c.ProfileMode == "") != (c.ProfilePrefix == "") {
		return fmt.Errorf("profile requires both mode and prefix")
	}
	return nil
}

// Fixed counts, one writer and one independent reader per database. Setup,
// validation, close/reopen and profiling flush stay outside workload timings.
func runFixedWork(root string, config fixedWorkConfig) (report fixedWorkReport, resultErr error) {
	report.Config = config
	if err := validateFixedWork(config); err != nil {
		return report, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	options := kitdbengine.OpenOptions{PageCacheBytes: 256 << 10, SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	manager, err := kitdbnode.NewManager(kitdbnode.Limits{MaxOpenDatabases: config.Databases, MaxPageCacheBytes: int64(config.Databases) * options.PageCacheBytes, DefaultPageCacheBytes: options.PageCacheBytes, MaxConcurrentOpens: min(2, config.Databases), MaxConcurrentMaintenance: min(2, config.Databases), MaxQueuedMaintenance: config.Databases * 2, MaxMaintenancePerDatabase: 1})
	if err != nil {
		return report, err
	}
	tenants := make([]*mixedTenant, 0, config.Databases)
	defer func() {
		resultErr = errors.Join(resultErr, closeMixedOwners(tenants, manager))
		report.Manager = manager.Stats()
		if resultErr != nil {
			report.Success = false
			report.Failure = resultErr.Error()
		}
	}()
	for id := 0; id < config.Databases; id++ {
		tenant := &mixedTenant{path: filepath.Join(root, fmt.Sprintf("db_%02d.kitdb", id)), expected: make([]int64, mixedRows)}
		tenants = append(tenants, tenant)
		tenant.lease, err = manager.Acquire(ctx, tenant.path, options)
		if err != nil {
			return report, err
		}
		tenant.engine, err = Attach(tenant.lease.DB(), Options{Kernel: options})
		if err != nil {
			return report, err
		}
		if err = seedMixedTenant(ctx, tenant, id); err != nil {
			return report, err
		}
		stats, err := tenant.lease.DB().Stats()
		if err != nil {
			return report, err
		}
		tenant.initialSyncs = stats.CommitSyncs
	}
	perDatabase := make([][]fixedCheckpointSample, config.Databases)
	readCounts := make([]int, config.Databases)
	var workers sync.WaitGroup
	var failureMu sync.Mutex
	var failure error
	start := make(chan struct{})
	launch := func(call func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			if err := call(); err != nil {
				failureMu.Lock()
				failure = errors.Join(failure, err)
				failureMu.Unlock()
				cancel()
			}
		}()
	}
	for id, tenant := range tenants {
		if config.Writes > 0 {
			perDatabase[id] = make([]fixedCheckpointSample, 0, config.Writes/config.CheckpointEvery)
			launch(func() error {
				for step := 0; step < config.Writes; step++ {
					if err := writeMixedTenant(ctx, tenant, 1); err != nil {
						return err
					}
					if (step+1)%config.CheckpointEvery != 0 {
						continue
					}
					at := benchclock.Now()
					ticket, err := manager.ScheduleCheckpoint(ctx, tenant.path, options, kitdbnode.MaintenanceNormal)
					if err != nil {
						return err
					}
					out, err := ticket.Wait(ctx)
					if err != nil {
						return err
					}
					perDatabase[id] = append(perDatabase[id], fixedCheckpointSample{id, out.Transaction, benchclock.Since(at).Nanoseconds(), out.StartedAt.Sub(out.QueuedAt).Nanoseconds(), out.Duration.Nanoseconds()})
				}
				return nil
			})
		}
		if config.Reads > 0 {
			launch(func() error {
				for step := 0; step < config.Reads; step++ {
					requested := (step * 37) % mixedRows
					point, err := tenant.engine.Execute(ctx, "SELECT id,version,bucket,payload FROM items WHERE id=$1", requested)
					if err != nil {
						return err
					}
					if len(point.Rows) != 1 || len(point.Rows[0]) != 4 || point.Rows[0][0] != int64(requested) {
						return fmt.Errorf("fixed point identity mismatch")
					}
					if err = checkMixedVisible(point.Rows, tenant.payload, -1); err != nil {
						return err
					}
					bucket := int64(step % 64)
					index, err := tenant.engine.Execute(ctx, "SELECT id,version,bucket,payload FROM items WHERE bucket=$1 ORDER BY id LIMIT 16", bucket)
					if err != nil {
						return err
					}
					if err = checkMixedVisible(index.Rows, tenant.payload, bucket); err != nil {
						return err
					}
					readCounts[id]++
				}
				return nil
			})
		}
	}
	if err := startFixedLockProfile(tenants, config.LockProfile); err != nil {
		cancel()
		close(start)
		workers.Wait()
		return report, err
	}
	runtime.GC()
	finishProfile, err := startMixedProfile(config.ProfileMode, config.ProfilePrefix)
	if err != nil {
		cancel()
		close(start)
		workers.Wait()
		return report, err
	}
	var before, after runtime.MemStats
	settings := []metrics.Sample{{Name: "/gc/gogc:percent"}, {Name: "/gc/gomemlimit:bytes"}}
	metrics.Read(settings)
	report.GOGC, report.GoMemoryLimit = settings[0].Value.Uint64(), settings[1].Value.Uint64()
	runtime.ReadMemStats(&before)
	at := benchclock.Now()
	close(start)
	workers.Wait()
	report.ElapsedNS = benchclock.Since(at).Nanoseconds()
	runtime.ReadMemStats(&after)
	report.Allocated, report.Mallocs = after.TotalAlloc-before.TotalAlloc, after.Mallocs-before.Mallocs
	report.GCCycles, report.GCPauseNS = after.NumGC-before.NumGC, after.PauseTotalNs-before.PauseTotalNs
	report.HeapBefore, report.HeapAfter = before.HeapAlloc, after.HeapAlloc
	if err := finishProfile(); err != nil {
		return report, errors.Join(failure, err)
	}
	if failure != nil {
		return report, failure
	}
	report.LockProfiles, err = finishFixedLockProfile(tenants, config.LockProfile)
	if err != nil {
		return report, err
	}
	for id, tenant := range tenants {
		stats, err := tenant.lease.DB().Stats()
		if err != nil {
			return report, err
		}
		checkpoints := 0
		if config.Writes > 0 {
			checkpoints = config.Writes / config.CheckpointEvery
		}
		if mixedDrainOf(stats) != (mixedDrain{}) || stats.CommitSyncs-tenant.initialSyncs != tenant.acknowledged || tenant.acknowledged != uint64(config.Writes) || readCounts[id] != config.Reads || len(perDatabase[id]) != checkpoints {
			return report, fmt.Errorf("fixed work count, sync or drain mismatch for database %d", id)
		}
		for step, sample := range perDatabase[id] {
			if sample.Transaction != stats.LastTransaction-uint64(config.Writes)+uint64((step+1)*config.CheckpointEvery) {
				return report, fmt.Errorf("unexpected checkpoint boundary")
			}
		}
		report.ACKs += tenant.acknowledged
		report.Points += readCounts[id]
		report.Indexes += readCounts[id]
		report.Samples = append(report.Samples, perDatabase[id]...)
	}
	if err := closeMixedOwners(tenants, manager); err != nil {
		return report, err
	}
	report.Manager = manager.Stats()
	if !mixedManagerDrained(report.Manager) {
		return report, fmt.Errorf("fixed manager did not drain")
	}
	for _, tenant := range tenants {
		if err := verifyMixedReopen(ctx, tenant, options); err != nil {
			return report, err
		}
		report.Verified++
	}
	report.Success = true
	return report, nil
}

func TestCheckpointFixedWorkSmoke(t *testing.T) {
	report, err := runFixedWork(t.TempDir(), fixedWorkConfig{Databases: 2, Writes: 4, Reads: 4, CheckpointEvery: 2})
	if err != nil || !report.Success || report.ACKs != 8 || report.Points != 8 || len(report.Samples) != 4 || report.Verified != 2 {
		t.Fatalf("report=%+v error=%v", report, err)
	}
}

func TestCheckpointFixedWorkBounds(t *testing.T) {
	valid := fixedWorkConfig{Databases: 16, Writes: 64, Reads: 192, CheckpointEvery: 4}
	if err := validateFixedWork(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*fixedWorkConfig){func(c *fixedWorkConfig) { c.Databases = 17 }, func(c *fixedWorkConfig) { c.Writes = 129 }, func(c *fixedWorkConfig) { c.Reads = 513 }, func(c *fixedWorkConfig) { c.CheckpointEvery = 3 }, func(c *fixedWorkConfig) { c.ProfileMode = "trace" }} {
		c := valid
		mutate(&c)
		if validateFixedWork(c) == nil {
			t.Fatal("unbounded fixed-work accepted")
		}
	}
}

func TestCheckpointFixedWorkCampaign(t *testing.T) {
	if os.Getenv("KITDB_FIXED_WORK") != "1" {
		t.Skip("set KITDB_FIXED_WORK=1 for fixed-count native SQL checkpoint comparison")
	}
	file, err := os.OpenFile(os.Getenv("KITDB_FIXED_REPORT"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			t.Error(err)
		}
	})
	report, err := runFixedWork(t.TempDir(), fixedWorkConfig{Databases: 16, Writes: 64, Reads: 192, CheckpointEvery: 4, ProfileMode: os.Getenv("KITDB_FIXED_PROFILE"), ProfilePrefix: os.Getenv("KITDB_FIXED_PREFIX")})
	if writeErr := json.NewEncoder(file).Encode(report); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
}
