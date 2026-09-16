package relational

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/pgwire"
)

const (
	shoppingFleetPassword  = "shopping-fleet-benchmark"
	shoppingFleetNeighbors = 4
	shoppingFleetRows      = 4_096
)

type shoppingFleetMode struct {
	name          string
	path          string
	options       Options
	query         string
	executionPath string
	rows          int
}

// TestShoppingProductionFleetWorkload is an opt-in, execution-backed canary
// for the retained 13M shopping artifacts. It keeps the production database
// read-only and puts mutable disposable tenants behind the same pgwire query
// admission scheduler. The result is a latency distribution, not a unit-test
// timing threshold: shared CI and OS page cache make fixed latency gates false.
func TestShoppingProductionFleetWorkload(t *testing.T) {
	if testing.Short() {
		t.Skip("production fleet workload is disabled by -short")
	}
	duration := shoppingFleetDuration(t)
	modes := []shoppingFleetMode{
		{
			name: "analytics",
			path: strings.TrimSpace(os.Getenv("KITDB_SHOPPING_BENCHMARK")),
			options: Options{
				ExperimentalProjections: true,
			},
			query:         `SELECT SUM(price), AVG(stock), MAX(sold) FROM shopping WHERE category = 4459`,
			executionPath: "kcol-batch",
			rows:          1,
		},
		{
			name: "search",
			path: strings.TrimSpace(os.Getenv("KITDB_SHOPPING_SEARCH_BENCHMARK")),
			options: Options{
				MaximumResultRows:       1_000,
				MaximumSearchResults:    1_000,
				MaximumSearchCandidates: 1_000_000,
			},
			query: `SELECT merchant, id, name, _score FROM shopping WHERE * SEARCH 'ban phim logitech' ORDER BY _score DESC LIMIT 20`,
			rows:  20,
		},
		{
			name: "packed-search",
			path: strings.TrimSpace(os.Getenv("KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK")),
			options: Options{
				ExperimentalProjections: true,
				SearchReaderCacheBytes:  256 << 20,
				MaximumResultRows:       1_000,
				MaximumSearchResults:    1_000,
				MaximumSearchCandidates: 1_000_000,
			},
			query:         `SELECT merchant, id, name, _score FROM shopping WHERE * SEARCH 'ban phim logitech' ORDER BY _score DESC LIMIT 20`,
			executionPath: "search-snapshot",
			rows:          20,
		},
	}
	runs := 0
	for _, mode := range modes {
		if mode.path == "" {
			continue
		}
		runs++
		t.Run(mode.name, func(t *testing.T) {
			runShoppingProductionFleetMode(t, mode, duration)
		})
	}
	if runs == 0 {
		t.Skip("set KITDB_SHOPPING_BENCHMARK, KITDB_SHOPPING_SEARCH_BENCHMARK, and/or KITDB_SHOPPING_PACKED_SEARCH_BENCHMARK")
	}
}

func shoppingFleetDuration(t testing.TB) time.Duration {
	t.Helper()
	source := strings.TrimSpace(os.Getenv("KITDB_SHOPPING_FLEET_DURATION"))
	if source == "" {
		return 10 * time.Second
	}
	duration, err := time.ParseDuration(source)
	if err != nil || duration < 2*time.Second || duration > 10*time.Minute {
		t.Fatalf("KITDB_SHOPPING_FLEET_DURATION must be between 2s and 10m: %q", source)
	}
	return duration
}

func runShoppingProductionFleetMode(t *testing.T, mode shoppingFleetMode, duration time.Duration) {
	t.Helper()
	info, err := os.Stat(mode.path)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("shopping artifact is not a regular file: %s", mode.path)
	}

	runtime.GC()
	memoryBeforeOpen := shoppingFleetMemoryPointNow()
	openStarted := time.Now()
	hotEngine, err := OpenWithOptions(mode.path, mode.options)
	if err != nil {
		t.Fatal(err)
	}
	defer hotEngine.Close()
	openDuration := time.Since(openStarted)
	sourceTransaction, err := hotEngine.database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}

	coldStarted := time.Now()
	if mode.executionPath != "" {
		observed, err := hotEngine.Execute(context.Background(), "EXPLAIN ANALYZE "+mode.query)
		if err != nil {
			t.Fatal(err)
		}
		if observed.Execution == nil || observed.Execution.Path != mode.executionPath {
			t.Fatalf("cold %s path=%+v, want %s", mode.name, observed.Execution, mode.executionPath)
		}
	} else {
		plan, err := hotEngine.Execute(context.Background(), "EXPLAIN "+mode.query)
		if err != nil || !resultContainsOperation(plan, "ranked search") {
			t.Fatalf("cold search plan=%+v error=%v", plan.Rows, err)
		}
		result, err := hotEngine.Execute(context.Background(), mode.query)
		if err != nil || len(result.Rows) != mode.rows {
			t.Fatalf("cold search rows=%d error=%v", len(result.Rows), err)
		}
	}
	coldDuration := time.Since(coldStarted)

	var directWarm boundedLatencyHistogram
	for range 3 {
		started := time.Now()
		result, err := hotEngine.Execute(context.Background(), mode.query)
		if err != nil || len(result.Rows) != mode.rows {
			t.Fatalf("warm %s rows=%d error=%v", mode.name, len(result.Rows), err)
		}
		directWarm.Observe(time.Since(started))
	}
	memoryAfterWarm := shoppingFleetMemoryPointNow()
	projectionCache := hotEngine.ProjectionCacheStats()

	authenticators := make(map[string]pgwire.Authenticator, shoppingFleetNeighbors+1)
	hotAuthenticator, err := hotEngine.PostgresAuthenticator(PostgresOptions{
		Database: "shopping_hot", User: "kitdb", Password: shoppingFleetPassword, ReadOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	authenticators["shopping_hot"] = hotAuthenticator

	neighborEngines := make([]*Engine, 0, shoppingFleetNeighbors)
	for neighbor := 0; neighbor < shoppingFleetNeighbors; neighbor++ {
		name := fmt.Sprintf("neighbor%d", neighbor)
		path := filepath.Join(t.TempDir(), name+".kitdb")
		createPostgresNodeMixedFixture(t, path, false, shoppingFleetRows)
		engine, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		neighborEngines = append(neighborEngines, engine)
		authenticator, err := engine.PostgresAuthenticator(PostgresOptions{
			Database: name, User: "kitdb", Password: shoppingFleetPassword,
		})
		if err != nil {
			t.Fatal(err)
		}
		authenticators[name] = authenticator
	}
	defer func() {
		for _, engine := range neighborEngines {
			if err := engine.Close(); err != nil {
				t.Error(err)
			}
		}
	}()

	queryMetrics := &pgwire.QueryMetrics{}
	address, stopServer := startShoppingFleetServer(
		t,
		shoppingFleetAuthenticator{password: shoppingFleetPassword, databases: authenticators},
		queryMetrics,
	)
	defer stopServer()

	hotClient := openPostgresNodeTestClient(t, address, "shopping_hot", shoppingFleetPassword)
	hotClient.SetMaxOpenConns(2)
	hotClient.SetMaxIdleConns(2)
	defer hotClient.Close()
	neighborClients := make([]*sql.DB, 0, shoppingFleetNeighbors)
	for neighbor := 0; neighbor < shoppingFleetNeighbors; neighbor++ {
		client := openPostgresNodeTestClient(
			t, address, fmt.Sprintf("neighbor%d", neighbor), shoppingFleetPassword,
		)
		client.SetMaxOpenConns(1)
		client.SetMaxIdleConns(1)
		neighborClients = append(neighborClients, client)
		defer client.Close()
	}

	baselineDuration := max(duration/2, 2*time.Second)
	baseline := runShoppingFleetWindow(t, mode, nil, neighborClients, baselineDuration, queryMetrics)
	mixed := runShoppingFleetWindow(t, mode, hotClient, neighborClients, duration, queryMetrics)
	if mixed.query.Peak > 4 || mixed.query.Active != 0 || mixed.query.Queued != 0 {
		t.Fatalf("unbounded or unfinished query admission: %+v", mixed.query)
	}
	finalTransaction, err := hotEngine.database.LastTransaction()
	if err != nil {
		t.Fatal(err)
	}
	if finalTransaction != sourceTransaction {
		t.Fatalf("read-only shopping source transaction changed from %d to %d", sourceTransaction, finalTransaction)
	}

	t.Logf(
		"mode=%s file_bytes=%d source_transaction=%d open_ms=%.3f reader_cold_ms=%.3f direct_warm_p50_ms=%.3f direct_warm_p99_ms=%.3f "+
			"heap_before_open_mib=%.2f heap_after_warm_mib=%.2f rss_before_open_mib=%s rss_after_warm_mib=%s",
		mode.name, info.Size(), sourceTransaction, milliseconds(openDuration), milliseconds(coldDuration),
		directWarm.Quantile(0.50)/1_000, directWarm.Quantile(0.99)/1_000,
		bytesToMiB(memoryBeforeOpen.heap), bytesToMiB(memoryAfterWarm.heap),
		optionalMiB(memoryBeforeOpen.rss), optionalMiB(memoryAfterWarm.rss),
	)
	if mode.executionPath == "search-snapshot" {
		t.Logf(
			"mode=%s search_readers=%d search_file_handles=%d search_resident_mib=%.2f search_capacity_mib=%.2f directory_mib=%.2f",
			mode.name, projectionCache.SearchReaders, projectionCache.SearchFileHandles,
			bytesToMiB(uint64(max(projectionCache.SearchReaderResidentBytes, 0))),
			bytesToMiB(uint64(max(projectionCache.SearchReaderCapacityBytes, 0))),
			bytesToMiB(uint64(max(projectionCache.DirectoryBytes, 0))),
		)
	}
	logShoppingFleetWindow(t, mode.name, "baseline", baseline)
	logShoppingFleetWindow(t, mode.name, "mixed", mixed)
	if baseline.reads.total.Load() > 0 && mixed.reads.total.Load() > 0 {
		baselineP99 := baseline.reads.Quantile(0.99)
		mixedP99 := mixed.reads.Quantile(0.99)
		t.Logf(
			"mode=%s noisy_neighbor_read_p99_ratio=%.3f baseline_us=%.0f mixed_us=%.0f",
			mode.name, mixedP99/max(baselineP99, 1), baselineP99, mixedP99,
		)
	}
}

type shoppingFleetAuthenticator struct {
	password  string
	databases map[string]pgwire.Authenticator
}

func (authenticator shoppingFleetAuthenticator) Authenticate(
	ctx context.Context,
	startup pgwire.Startup,
	password string,
) (pgwire.Session, error) {
	if subtle.ConstantTimeCompare([]byte(password), []byte(authenticator.password)) != 1 {
		return nil, pgwire.NewError("28P01", "password authentication failed for KitDB user")
	}
	name := strings.ToLower(strings.TrimSpace(startup.Database()))
	name = strings.TrimSuffix(name, ".kitdb")
	database := authenticator.databases[name]
	if database == nil {
		return nil, pgwire.NewError("3D000", fmt.Sprintf("KitDB database %q is not served", name))
	}
	return database.Authenticate(ctx, startup, password)
}

func startShoppingFleetServer(
	t testing.TB,
	authenticator pgwire.Authenticator,
	metrics *pgwire.QueryMetrics,
) (string, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- (pgwire.Server{
			Authenticator:        authenticator,
			MaxConnections:       16,
			MaxConcurrentQueries: 4, MaxConcurrentQueriesPerKey: 2,
			MaxQueuedQueries: 64, MaxQueuedQueriesPerKey: 16,
			IdleTimeout: time.Minute, QueryTimeout: 30 * time.Second,
			QueryMetrics: metrics,
		}).Serve(ctx, listener)
	}()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("Serve production fleet: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("production fleet server did not stop")
			}
		})
	}
	return listener.Addr().String(), stop
}

type shoppingFleetWindow struct {
	duration time.Duration
	reads    boundedLatencyHistogram
	updates  boundedLatencyHistogram
	hot      boundedLatencyHistogram
	query    pgwire.QuerySnapshot
	memory   shoppingFleetMemory
}

func runShoppingFleetWindow(
	t testing.TB,
	mode shoppingFleetMode,
	hot *sql.DB,
	neighbors []*sql.DB,
	duration time.Duration,
	metrics *pgwire.QueryMetrics,
) *shoppingFleetWindow {
	t.Helper()
	runtime.GC()
	memory := startShoppingFleetMemorySampler()
	before := metrics.Snapshot()
	window := &shoppingFleetWindow{duration: duration}
	deadline := time.Now().Add(duration)
	var failed atomic.Bool
	var failureOnce sync.Once
	var failure error
	recordFailure := func(err error) {
		failureOnce.Do(func() {
			failure = err
			failed.Store(true)
		})
	}

	var workers sync.WaitGroup
	for neighbor, client := range neighbors {
		workers.Add(1)
		go func(neighbor int, client *sql.DB) {
			defer workers.Done()
			operation := 0
			for !failed.Load() && time.Now().Before(deadline) {
				kind := &window.reads
				started := time.Now()
				var err error
				if operation%4 == 3 {
					kind = &window.updates
					err = runShoppingFleetUpdate(client, neighbor, operation)
				} else {
					err = runShoppingFleetRead(client, neighbor, operation)
				}
				kind.Observe(time.Since(started))
				if err != nil {
					recordFailure(err)
					return
				}
				operation++
			}
		}(neighbor, client)
	}
	if hot != nil {
		for worker := 0; worker < 2; worker++ {
			workers.Add(1)
			go func() {
				defer workers.Done()
				for !failed.Load() && time.Now().Before(deadline) {
					started := time.Now()
					err := runShoppingFleetHotQuery(hot, mode)
					window.hot.Observe(time.Since(started))
					if err != nil {
						recordFailure(err)
						return
					}
				}
			}()
		}
	}
	workers.Wait()
	window.duration = time.Since(deadline.Add(-duration))
	window.memory = memory()
	window.query = shoppingFleetQueryDelta(before, metrics.Snapshot())
	if failure != nil {
		t.Fatal(failure)
	}
	wantQueries := window.reads.total.Load() + window.updates.total.Load() + window.hot.total.Load()
	if window.query.Acquired != wantQueries || window.query.Completed != wantQueries ||
		window.query.Failed != 0 || window.query.Rejected != 0 || window.query.WaitTimeouts != 0 {
		t.Fatalf("query accounting=%+v successful_operations=%d", window.query, wantQueries)
	}
	return window
}

func runShoppingFleetRead(client *sql.DB, neighbor, operation int) error {
	id := (operation + neighbor*97) % shoppingFleetRows
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var amount int64
	if err := client.QueryRowContext(ctx,
		`SELECT amount FROM events WHERE tenant_id = 1 AND id = $1`, id,
	).Scan(&amount); err != nil {
		return fmt.Errorf("neighbor%d point read: %w", neighbor, err)
	}
	return nil
}

func runShoppingFleetUpdate(client *sql.DB, neighbor, operation int) error {
	id := (operation + neighbor*97) % shoppingFleetRows
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := client.ExecContext(ctx,
		`UPDATE events SET amount = $1 WHERE tenant_id = 1 AND id = $2`,
		operation+neighbor, id,
	)
	if err != nil {
		return fmt.Errorf("neighbor%d update: %w", neighbor, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("neighbor%d update result: %w", neighbor, err)
	}
	if affected != 1 {
		return fmt.Errorf("neighbor%d update affected=%d, want 1", neighbor, affected)
	}
	return nil
}

func runShoppingFleetHotQuery(client *sql.DB, mode shoppingFleetMode) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	rows, err := client.QueryContext(ctx, mode.query)
	if err != nil {
		return fmt.Errorf("%s hot query: %w", mode.name, err)
	}
	defer rows.Close()
	count := 0
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != mode.rows {
		return fmt.Errorf("%s hot rows=%d, want %d", mode.name, count, mode.rows)
	}
	return nil
}

func shoppingFleetQueryDelta(before, after pgwire.QuerySnapshot) pgwire.QuerySnapshot {
	return pgwire.QuerySnapshot{
		Active: after.Active, Peak: after.Peak,
		Queued: after.Queued, PeakQueued: after.PeakQueued,
		Acquired: after.Acquired - before.Acquired, Completed: after.Completed - before.Completed,
		Failed:          after.Failed - before.Failed,
		WaitTimeouts:    after.WaitTimeouts - before.WaitTimeouts,
		Rejected:        after.Rejected - before.Rejected,
		WaitNanoseconds: after.WaitNanoseconds - before.WaitNanoseconds,
	}
}

func logShoppingFleetWindow(t testing.TB, mode, stage string, window *shoppingFleetWindow) {
	t.Helper()
	total := window.reads.total.Load() + window.updates.total.Load() + window.hot.total.Load()
	t.Logf(
		"mode=%s stage=%s elapsed_s=%.3f operations=%d throughput_qps=%.2f queue_peak=%d wait_ms=%.3f "+
			"read_n=%d read_p50_ms=%.3f read_p95_ms=%.3f read_p99_ms=%.3f "+
			"update_n=%d update_p50_ms=%.3f update_p95_ms=%.3f update_p99_ms=%.3f "+
			"hot_n=%d hot_p50_ms=%.3f hot_p95_ms=%.3f hot_p99_ms=%.3f "+
			"heap_start_mib=%.2f heap_peak_mib=%.2f heap_end_mib=%.2f rss_start_mib=%s rss_peak_mib=%s rss_end_mib=%s",
		mode, stage, window.duration.Seconds(), total, float64(total)/window.duration.Seconds(),
		window.query.PeakQueued, float64(window.query.WaitNanoseconds)/float64(time.Millisecond),
		window.reads.total.Load(), window.reads.Quantile(0.50)/1_000,
		window.reads.Quantile(0.95)/1_000, window.reads.Quantile(0.99)/1_000,
		window.updates.total.Load(), window.updates.Quantile(0.50)/1_000,
		window.updates.Quantile(0.95)/1_000, window.updates.Quantile(0.99)/1_000,
		window.hot.total.Load(), window.hot.Quantile(0.50)/1_000,
		window.hot.Quantile(0.95)/1_000, window.hot.Quantile(0.99)/1_000,
		bytesToMiB(window.memory.heapStart), bytesToMiB(window.memory.heapPeak), bytesToMiB(window.memory.heapEnd),
		optionalMiB(window.memory.rssStart), optionalMiB(window.memory.rssPeak), optionalMiB(window.memory.rssEnd),
	)
}

type shoppingFleetMemory struct {
	heapStart uint64
	heapPeak  uint64
	heapEnd   uint64
	rssStart  uint64
	rssPeak   uint64
	rssEnd    uint64
}

type shoppingFleetMemoryPoint struct {
	heap uint64
	rss  uint64
}

func shoppingFleetMemoryPointNow() shoppingFleetMemoryPoint {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	rss, _ := shoppingFleetCurrentRSS()
	return shoppingFleetMemoryPoint{heap: stats.HeapAlloc, rss: rss}
}

func startShoppingFleetMemorySampler() func() shoppingFleetMemory {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	memory := shoppingFleetMemory{heapStart: stats.HeapAlloc, heapPeak: stats.HeapAlloc}
	memory.rssStart, _ = shoppingFleetCurrentRSS()
	memory.rssPeak = memory.rssStart
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				runtime.ReadMemStats(&stats)
				memory.heapPeak = max(memory.heapPeak, stats.HeapAlloc)
				if rss, ok := shoppingFleetCurrentRSS(); ok {
					memory.rssPeak = max(memory.rssPeak, rss)
				}
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() shoppingFleetMemory {
		once.Do(func() {
			close(stop)
			<-done
			runtime.ReadMemStats(&stats)
			memory.heapEnd = stats.HeapAlloc
			memory.heapPeak = max(memory.heapPeak, stats.HeapAlloc)
			memory.rssEnd, _ = shoppingFleetCurrentRSS()
			memory.rssPeak = max(memory.rssPeak, memory.rssEnd)
		})
		return memory
	}
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func bytesToMiB(value uint64) float64 {
	return float64(value) / float64(1<<20)
}

func optionalMiB(value uint64) string {
	if value == 0 {
		return "unavailable"
	}
	return strconv.FormatFloat(bytesToMiB(value), 'f', 2, 64)
}
