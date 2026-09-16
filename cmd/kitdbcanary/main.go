// Command kitdbcanary runs a bounded long-lived KitDB storage probe.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"math/rand"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/buildinfo"
	"github.com/kitwork/engine/kitdb/node"
)

const canaryReportSchemaVersion = 1

type canaryConfig struct {
	Root            string
	Report          string
	Duration        time.Duration
	Interval        time.Duration
	Tenants         int
	Workers         int
	MaxOpen         int
	Keyspace        uint64
	CheckpointEvery uint64
	VerifyEvery     uint64
	HistoryBytes    int64
	Seed            int64
	Quiet           bool
}

type latencySummary struct {
	P50Micros uint64 `json:"p50_micros"`
	P95Micros uint64 `json:"p95_micros"`
	P99Micros uint64 `json:"p99_micros"`
	MaxMicros uint64 `json:"max_micros"`
}

type canaryReport struct {
	SchemaVersion       int                        `json:"schema_version"`
	Build               buildinfo.Info             `json:"build"`
	RequestedWorkloadMS int64                      `json:"requested_workload_ms"`
	WorkloadMS          int64                      `json:"workload_ms"`
	WorkloadCompleted   bool                       `json:"workload_completed"`
	Contract            kitdb.CompatibilityProfile `json:"contract"`
	StartedAt           time.Time                  `json:"started_at"`
	FinishedAt          time.Time                  `json:"finished_at"`
	DurationMS          int64                      `json:"duration_ms"`
	GoVersion           string                     `json:"go_version"`
	OS                  string                     `json:"os"`
	Arch                string                     `json:"arch"`
	Seed                int64                      `json:"seed"`
	Tenants             int                        `json:"tenants"`
	Workers             int                        `json:"workers"`
	MaxOpen             int                        `json:"max_open"`
	Keyspace            uint64                     `json:"keyspace"`
	Operations          uint64                     `json:"operations"`
	Commits             uint64                     `json:"commits"`
	Checkpoints         uint64                     `json:"checkpoints"`
	Verifications       uint64                     `json:"verifications"`
	Backups             uint64                     `json:"backups"`
	Restores            uint64                     `json:"restores"`
	Latency             latencySummary             `json:"commit_latency"`
	Node                node.Stats                 `json:"node"`
	Success             bool                       `json:"success"`
	Failure             string                     `json:"failure,omitempty"`
}

type modelValue struct {
	transaction uint64
	value       string
}

type tenantState struct {
	path     string
	sequence atomic.Uint64
	mu       sync.Mutex
	model    map[string]modelValue
}

type latencyHistogram struct {
	buckets [64]atomic.Uint64
	count   atomic.Uint64
	max     atomic.Uint64
}

func main() {
	version := flag.Bool("version", false, "print binary build information and exit")
	config := canaryConfig{}
	flag.StringVar(&config.Root, "root", "", "parent directory for canary files; empty uses a temporary directory")
	flag.StringVar(&config.Report, "json", "", "optional final JSON report path")
	flag.DurationVar(&config.Duration, "duration", time.Minute, "total write workload duration")
	flag.DurationVar(&config.Interval, "interval", 5*time.Millisecond, "minimum delay between writes per worker")
	flag.IntVar(&config.Tenants, "tenants", 16, "number of isolated KitDB files")
	flag.IntVar(&config.Workers, "workers", 4, "concurrent workload workers")
	flag.IntVar(&config.MaxOpen, "max-open", 8, "node handle ceiling")
	flag.Uint64Var(&config.Keyspace, "keyspace", 1024, "bounded keys reused per tenant")
	flag.Uint64Var(&config.CheckpointEvery, "checkpoint-every", 256, "tenant writes between checkpoints")
	flag.Uint64Var(&config.VerifyEvery, "verify-every", 2048, "tenant writes between full verification")
	flag.Int64Var(&config.HistoryBytes, "history-bytes", 8<<20, "retained-history byte target per tenant")
	flag.Int64Var(&config.Seed, "seed", 1, "deterministic tenant-selection seed")
	flag.BoolVar(&config.Quiet, "quiet", false, "suppress the final human-readable summary")
	flag.Parse()
	if *version {
		if err := json.NewEncoder(os.Stdout).Encode(buildinfo.Current()); err != nil {
			fmt.Fprintln(os.Stderr, "kitdbcanary:", err)
			os.Exit(1)
		}
		return
	}

	if err := config.validate(); err != nil {
		fmt.Fprintln(os.Stderr, "kitdbcanary:", err)
		os.Exit(2)
	}
	signalContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSignals()

	report, err := executeCanary(signalContext, config)
	if reportErr := writeCanaryReport(config.Report, report); reportErr != nil && err == nil {
		err = reportErr
	}
	if !config.Quiet {
		fmt.Printf(
			"kitdbcanary: success=%t tenants=%d commits=%d p99=%dus checkpoints=%d verifies=%d backups=%d restores=%d\n",
			report.Success, report.Tenants, report.Commits, report.Latency.P99Micros,
			report.Checkpoints, report.Verifications, report.Backups, report.Restores,
		)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "kitdbcanary:", err)
		os.Exit(1)
	}
}

func (config canaryConfig) validate() error {
	switch {
	case config.Duration < 100*time.Millisecond || config.Duration > 7*24*time.Hour:
		return fmt.Errorf("duration must be between 100ms and 168h")
	case config.Interval < 0 || config.Interval > time.Minute:
		return fmt.Errorf("interval must be between 0 and 1m")
	case config.Tenants < 1 || config.Tenants > 1000:
		return fmt.Errorf("tenants must be between 1 and 1000")
	case config.Workers < 1 || config.Workers > 128:
		return fmt.Errorf("workers must be between 1 and 128")
	case config.MaxOpen < 1 || config.MaxOpen > config.Tenants:
		return fmt.Errorf("max-open must be between 1 and tenants")
	case config.Keyspace < 1 || config.Keyspace > 1_000_000:
		return fmt.Errorf("keyspace must be between 1 and 1000000")
	case config.CheckpointEvery < 1 || config.CheckpointEvery > 1_000_000:
		return fmt.Errorf("checkpoint-every must be between 1 and 1000000")
	case config.VerifyEvery < config.CheckpointEvery || config.VerifyEvery > 10_000_000:
		return fmt.Errorf("verify-every must be between checkpoint-every and 10000000")
	case config.HistoryBytes < 1 || config.HistoryBytes > 1<<40:
		return fmt.Errorf("history-bytes must be between 1 and 1 TiB")
	default:
		return nil
	}
}

func executeCanary(ctx context.Context, config canaryConfig) (canaryReport, error) {
	startedAt := time.Now().UTC()
	report := canaryReport{
		SchemaVersion:       canaryReportSchemaVersion,
		Build:               buildinfo.Current(),
		RequestedWorkloadMS: config.Duration.Milliseconds(),
		Contract:            kitdb.CurrentCompatibility(),
		StartedAt:           startedAt,
		GoVersion:           runtime.Version(),
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		Seed:                config.Seed,
		Tenants:             config.Tenants,
		Workers:             config.Workers,
		MaxOpen:             config.MaxOpen,
		Keyspace:            config.Keyspace,
	}
	if ctx == nil {
		return finishCanaryReport(report, startedAt, nil, fmt.Errorf("nil canary context"))
	}
	if err := config.validate(); err != nil {
		return finishCanaryReport(report, startedAt, nil, err)
	}

	root, cleanup, err := prepareCanaryRoot(config.Root)
	if err != nil {
		return finishCanaryReport(report, startedAt, nil, err)
	}
	defer cleanup()
	manager, err := node.NewManager(node.Limits{
		MaxOpenDatabases:      config.MaxOpen,
		MaxPageCacheBytes:     int64(config.MaxOpen) << 20,
		DefaultPageCacheBytes: 1 << 20,
		MaxConcurrentOpens:    min(config.Workers, config.MaxOpen),
	})
	if err != nil {
		return finishCanaryReport(report, startedAt, nil, err)
	}
	options := kitdb.OpenOptions{
		PageCacheBytes: 1 << 20,
		RetainHistory:  true,
		HistoryRetention: kitdb.HistoryRetentionPolicy{
			MaxBytes: config.HistoryBytes,
		},
		CommitQueueSize:  128,
		MaxCommitBatch:   16,
		GroupCommitDelay: 200 * time.Microsecond,
	}
	tenants := make([]*tenantState, config.Tenants)
	for index := range tenants {
		tenants[index] = &tenantState{
			path:  filepath.Join(root, fmt.Sprintf("tenant-%04d.kitdb", index)),
			model: make(map[string]modelValue, min(int(config.Keyspace), 4096)),
		}
	}

	var operations atomic.Uint64
	var commits atomic.Uint64
	var checkpoints atomic.Uint64
	var verifications atomic.Uint64
	var latency latencyHistogram
	workContext, cancelWork := context.WithCancel(ctx)
	defer cancelWork()
	failures := make(chan error, 1)
	fail := func(err error) {
		if err == nil {
			return
		}
		select {
		case failures <- err:
			cancelWork()
		default:
		}
	}

	for index, tenant := range tenants {
		if err := writeCanaryRecord(
			workContext, manager, options, tenant, index, config,
			&operations, &commits, &checkpoints, &verifications, &latency,
		); err != nil {
			closeErr := manager.Close()
			return finishCanaryReport(report, startedAt, &latency, errors.Join(err, closeErr))
		}
	}

	workloadStartedAt := time.Now()
	var workers sync.WaitGroup
	workers.Add(config.Workers)
	for worker := range config.Workers {
		go func(worker int) {
			defer workers.Done()
			random := rand.New(rand.NewSource(config.Seed + int64(worker+1)*7919))
			for workContext.Err() == nil {
				index := random.Intn(len(tenants))
				if err := writeCanaryRecord(
					workContext, manager, options, tenants[index], index, config,
					&operations, &commits, &checkpoints, &verifications, &latency,
				); err != nil {
					if workContext.Err() == nil || (!errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)) {
						fail(fmt.Errorf("worker %d tenant %d: %w", worker, index, err))
					}
					return
				}
				if config.Interval > 0 {
					timer := time.NewTimer(config.Interval)
					select {
					case <-workContext.Done():
						if !timer.Stop() {
							<-timer.C
						}
						return
					case <-timer.C:
					}
				}
			}
		}(worker)
	}

	var workloadErr error
	workloadTimer := time.NewTimer(config.Duration)
	select {
	case <-ctx.Done():
		workloadErr = ctx.Err()
		cancelWork()
	case <-workloadTimer.C:
		report.WorkloadCompleted = true
		cancelWork()
	case workloadErr = <-failures:
	}
	if !workloadTimer.Stop() {
		select {
		case <-workloadTimer.C:
		default:
		}
	}
	workers.Wait()
	report.WorkloadMS = time.Since(workloadStartedAt).Milliseconds()
	if err := ctx.Err(); err != nil {
		report.WorkloadCompleted = false
		workloadErr = errors.Join(workloadErr, err)
	}
	// A worker can fail concurrently with timer expiry. Draining after Wait
	// prevents a successful final restore from masking that workload failure.
	select {
	case failure := <-failures:
		workloadErr = errors.Join(workloadErr, failure)
	default:
	}
	cancelWork()
	report.Operations = operations.Load()
	report.Commits = commits.Load()
	report.Checkpoints = checkpoints.Load()
	report.Verifications = verifications.Load()

	finalContext, cancelFinal := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancelFinal()
	if workloadErr == nil {
		workloadErr = finalizeCanary(
			finalContext, root, manager, options, tenants, &report,
		)
	}
	closeErr := manager.CloseContext(finalContext)
	report.Node = manager.Stats()
	if report.Node.ActiveLeases != 0 {
		closeErr = errors.Join(closeErr, fmt.Errorf("node retained %d active leases", report.Node.ActiveLeases))
	}
	return finishCanaryReport(report, startedAt, &latency, errors.Join(workloadErr, closeErr))
}

func writeCanaryRecord(
	ctx context.Context,
	manager *node.Manager,
	options kitdb.OpenOptions,
	tenant *tenantState,
	tenantIndex int,
	config canaryConfig,
	operations, commits, checkpoints, verifications *atomic.Uint64,
	latency *latencyHistogram,
) error {
	operations.Add(1)
	sequence := tenant.sequence.Add(1)
	slot := (sequence - 1) % config.Keyspace
	key := fmt.Sprintf("record/%020d", slot)
	value := fmt.Sprintf("tenant=%d sequence=%d", tenantIndex, sequence)
	startedAt := time.Now()
	lease, err := manager.Acquire(ctx, tenant.path, options)
	if err != nil {
		return err
	}
	transaction, err := lease.DB().Begin()
	if err == nil {
		err = transaction.Put([]byte(key), []byte(value))
	}
	var committed uint64
	if err == nil {
		committed, err = transaction.Commit()
	} else if transaction != nil {
		err = errors.Join(err, transaction.Rollback())
	}
	if err == nil {
		stored, found, getErr := lease.DB().Get([]byte(key))
		if getErr != nil || !found || string(stored) != value {
			err = fmt.Errorf("read committed key %q = (%q, %t, %v)", key, stored, found, getErr)
		}
	}
	releaseErr := lease.Release()
	if err != nil || releaseErr != nil {
		return errors.Join(err, releaseErr)
	}
	latency.observe(time.Since(startedAt))
	commits.Add(1)
	tenant.mu.Lock()
	if previous := tenant.model[key]; committed > previous.transaction {
		tenant.model[key] = modelValue{transaction: committed, value: value}
	}
	tenant.mu.Unlock()

	if sequence%config.CheckpointEvery == 0 {
		if err := runCanaryMaintenance(ctx, manager, tenant.path, options, true); err != nil {
			return err
		}
		checkpoints.Add(1)
	}
	if sequence%config.VerifyEvery == 0 {
		if err := runCanaryMaintenance(ctx, manager, tenant.path, options, false); err != nil {
			return err
		}
		verifications.Add(1)
	}
	return nil
}

func runCanaryMaintenance(
	ctx context.Context,
	manager *node.Manager,
	path string,
	options kitdb.OpenOptions,
	checkpoint bool,
) error {
	var ticket *node.MaintenanceTicket
	var err error
	if checkpoint {
		ticket, err = manager.ScheduleCheckpoint(ctx, path, options, node.MaintenanceBackground)
	} else {
		ticket, err = manager.ScheduleVerify(ctx, path, options, node.MaintenanceBackground)
	}
	if err != nil {
		return err
	}
	_, err = ticket.Wait(ctx)
	return err
}

func finalizeCanary(
	ctx context.Context,
	root string,
	manager *node.Manager,
	options kitdb.OpenOptions,
	tenants []*tenantState,
	report *canaryReport,
) error {
	backupRoot := filepath.Join(root, "backups")
	restoreRoot := filepath.Join(root, "restores")
	if err := os.MkdirAll(backupRoot, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(restoreRoot, 0o700); err != nil {
		return err
	}
	for index, tenant := range tenants {
		if err := runCanaryMaintenance(ctx, manager, tenant.path, options, true); err != nil {
			return fmt.Errorf("final checkpoint tenant %d: %w", index, err)
		}
		report.Checkpoints++
		if err := runCanaryMaintenance(ctx, manager, tenant.path, options, false); err != nil {
			return fmt.Errorf("final verify tenant %d: %w", index, err)
		}
		report.Verifications++

		lease, err := manager.Acquire(ctx, tenant.path, options)
		if err != nil {
			return err
		}
		model := tenant.modelSnapshot()
		if err := verifyCanaryModel(lease.DB(), model); err != nil {
			_ = lease.Release()
			return fmt.Errorf("source model tenant %d: %w", index, err)
		}
		stats, err := lease.DB().Stats()
		if err != nil || stats.MainRecords != uint64(len(model)) {
			_ = lease.Release()
			return fmt.Errorf("source stats tenant %d = (%#v, %v), model=%d", index, stats, err, len(model))
		}
		backupPath := filepath.Join(backupRoot, fmt.Sprintf("tenant-%04d.kitdb", index))
		anchor, backupErr := lease.DB().CreateBackupAnchor(ctx, backupPath)
		releaseErr := lease.Release()
		if backupErr != nil || releaseErr != nil {
			return errors.Join(backupErr, releaseErr)
		}
		verified, err := kitdb.VerifyBackupAnchor(ctx, backupPath)
		if err != nil || verified != anchor {
			return fmt.Errorf("backup verify tenant %d = (%#v, %v), want %#v", index, verified, err, anchor)
		}
		report.Backups++

		restorePath := filepath.Join(restoreRoot, fmt.Sprintf("tenant-%04d.kitdb", index))
		restored, err := kitdb.RestoreToTransaction(ctx, backupPath, "", restorePath, anchor.Transaction)
		if err != nil || restored.DatabaseID != anchor.DatabaseID || restored.Transaction != anchor.Transaction {
			return fmt.Errorf("restore tenant %d = (%#v, %v)", index, restored, err)
		}
		reopened, err := kitdb.OpenWithOptions(restorePath, kitdb.OpenOptions{VerifyOnOpen: true, PageCacheBytes: -1})
		if err != nil {
			return err
		}
		modelErr := verifyCanaryModel(reopened, model)
		closeErr := reopened.Close()
		if modelErr != nil || closeErr != nil {
			return errors.Join(modelErr, closeErr)
		}
		report.Restores++
	}
	return nil
}

func (tenant *tenantState) modelSnapshot() map[string]modelValue {
	tenant.mu.Lock()
	defer tenant.mu.Unlock()
	result := make(map[string]modelValue, len(tenant.model))
	for key, value := range tenant.model {
		result[key] = value
	}
	return result
}

func verifyCanaryModel(database *kitdb.DB, model map[string]modelValue) error {
	keys := make([]string, 0, len(model))
	for key := range model {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value, found, err := database.Get([]byte(key))
		if err != nil || !found || string(value) != model[key].value {
			return fmt.Errorf("key %q = (%q, %t, %v), want %q", key, value, found, err, model[key].value)
		}
	}
	return nil
}

func prepareCanaryRoot(parent string) (string, func(), error) {
	if parent == "" {
		root, err := os.MkdirTemp("", "kitdb-canary-*")
		return root, func() { _ = os.RemoveAll(root) }, err
	}
	absolute, err := filepath.Abs(parent)
	if err != nil {
		return "", func() {}, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", func() {}, err
	}
	root := filepath.Join(absolute, fmt.Sprintf("kitdb-canary-%d-%d", time.Now().UTC().UnixNano(), os.Getpid()))
	if err := os.Mkdir(root, 0o700); err != nil {
		return "", func() {}, err
	}
	return root, func() {}, nil
}

func (histogram *latencyHistogram) observe(duration time.Duration) {
	nanoseconds := uint64(max(duration.Nanoseconds(), 1))
	bucket := bits.Len64(nanoseconds) - 1
	histogram.buckets[bucket].Add(1)
	histogram.count.Add(1)
	for current := histogram.max.Load(); nanoseconds > current; current = histogram.max.Load() {
		if histogram.max.CompareAndSwap(current, nanoseconds) {
			break
		}
	}
}

func (histogram *latencyHistogram) summary() latencySummary {
	return latencySummary{
		P50Micros: histogram.percentile(50) / 1000,
		P95Micros: histogram.percentile(95) / 1000,
		P99Micros: histogram.percentile(99) / 1000,
		MaxMicros: histogram.max.Load() / 1000,
	}
}

func (histogram *latencyHistogram) percentile(percent uint64) uint64 {
	count := histogram.count.Load()
	if count == 0 {
		return 0
	}
	target := (count*percent + 99) / 100
	seen := uint64(0)
	for index := range histogram.buckets {
		seen += histogram.buckets[index].Load()
		if seen >= target {
			return uint64(1) << index
		}
	}
	return histogram.max.Load()
}

func finishCanaryReport(
	report canaryReport,
	startedAt time.Time,
	latency *latencyHistogram,
	err error,
) (canaryReport, error) {
	report.FinishedAt = time.Now().UTC()
	report.DurationMS = report.FinishedAt.Sub(startedAt).Milliseconds()
	if latency != nil {
		report.Latency = latency.summary()
	}
	report.Success = err == nil
	if err != nil {
		report.Failure = err.Error()
	}
	return report, err
}

func writeCanaryReport(path string, report canaryReport) error {
	if path == "" {
		return nil
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.CreateTemp(filepath.Dir(absolute), ".kitdb-canary-report-*.tmp")
	if err != nil {
		return err
	}
	temporary := file.Name()
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := io.Copy(file, bytes.NewReader(data)); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, absolute); err != nil {
		return err
	}
	remove = false
	return nil
}
