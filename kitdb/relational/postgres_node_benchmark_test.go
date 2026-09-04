package relational

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	kitdbnode "github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
)

// BenchmarkPostgresNodeMixedWorkload measures the public PostgreSQL path over
// independent KitDB files. Stable databases use KCOL/search snapshots while
// mutable databases exercise KROW point reads and commits.
func BenchmarkPostgresNodeMixedWorkload(b *testing.B) {
	const (
		databaseCount   = 8
		projectedCount  = 4
		rowsPerDatabase = 4_096
	)
	root := b.TempDir()
	for database := 0; database < databaseCount; database++ {
		createPostgresNodeMixedFixture(
			b,
			filepath.Join(root, fmt.Sprintf("tenant%d.kitdb", database)),
			database < projectedCount,
			rowsPerDatabase,
		)
	}

	queryMetrics := &pgwire.QueryMetrics{}
	node, err := OpenPostgresNode(PostgresNodeOptions{
		Root: root, User: "kitdb", Password: "node-secret",
		WarmDatabases:                  []string{"tenant0", "tenant1"},
		MaximumIdleProjectionDatabases: 2,
		DatabaseAcquireTimeout:         time.Second,
		ManagerLimits: kitdbnode.Limits{
			MaxOpenDatabases: databaseCount, MaxPageCacheBytes: databaseCount << 20,
			DefaultPageCacheBytes: 1 << 20, MaxConcurrentOpens: 2,
		},
		Relational: Options{ExperimentalProjections: true},
	})
	if err != nil {
		b.Fatal(err)
	}
	address, stopServer := startPostgresNodeTestServerWithOptions(b, node, PostgresServerOptions{
		MaxConnections:             32,
		MaxConcurrentQueries:       6,
		MaxConcurrentQueriesPerKey: 2,
		MaxQueuedQueries:           32,
		MaxQueuedQueriesPerKey:     8,
		IdleTimeout:                time.Minute,
		QueryTimeout:               30 * time.Second,
		QueryMetrics:               queryMetrics,
	})
	clients := make([]*sql.DB, databaseCount)
	for database := range clients {
		client := openPostgresNodeTestClient(
			b, address, fmt.Sprintf("tenant%d", database), "node-secret",
		)
		client.SetMaxOpenConns(4)
		client.SetMaxIdleConns(4)
		clients[database] = client
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	expectedActiveTotal := mixedFixtureActiveTotal(rowsPerDatabase)
	baseline := queryMetrics.Snapshot()
	latencies := [mixedOperationKinds]boundedLatencyHistogram{}

	var sequence atomic.Uint64
	var failed atomic.Bool
	var failureOnce sync.Once
	var failureMu sync.Mutex
	var failure error
	recordFailure := func(err error) {
		failureOnce.Do(func() {
			failureMu.Lock()
			failure = err
			failureMu.Unlock()
			failed.Store(true)
		})
	}

	b.ReportAllocs()
	b.ResetTimer()
	started := time.Now()
	b.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			if failed.Load() {
				continue
			}
			current := sequence.Add(1) - 1
			database := int(current % databaseCount)
			operation := int(current / databaseCount)
			operationKind := postgresNodeMixedOperationKind(database < projectedCount, operation)
			operationStarted := time.Now()
			if err := runPostgresNodeMixedOperation(
				ctx, clients[database], database < projectedCount, database,
				operation, rowsPerDatabase, expectedActiveTotal,
			); err != nil {
				recordFailure(err)
			}
			latencies[operationKind].Observe(time.Since(operationStarted))
		}
	})
	elapsed := time.Since(started)
	b.StopTimer()
	failureMu.Lock()
	runErr := failure
	failureMu.Unlock()
	if runErr != nil {
		b.Fatal(runErr)
	}
	if ctx.Err() != nil {
		b.Fatal(ctx.Err())
	}

	snapshot := queryMetrics.Snapshot()
	if snapshot.Active != 0 || snapshot.Queued != 0 || snapshot.Peak > 6 ||
		snapshot.Acquired-baseline.Acquired != uint64(b.N) ||
		snapshot.Completed-baseline.Completed != uint64(b.N) ||
		snapshot.Failed != baseline.Failed || snapshot.Rejected != baseline.Rejected ||
		snapshot.WaitTimeouts != baseline.WaitTimeouts {
		b.Fatalf("mixed query metrics baseline=%+v final=%+v", baseline, snapshot)
	}
	b.ReportMetric(float64(b.N)/elapsed.Seconds(), "queries/s")
	b.ReportMetric(float64(snapshot.Peak), "query_peak")
	b.ReportMetric(float64(snapshot.PeakQueued), "queue_peak")
	b.ReportMetric(float64(node.Stats().ProjectionDirectoryBytes)/(1<<20), "projection_MiB")
	for kind, name := range []string{"point", "aggregate", "search", "update"} {
		b.ReportMetric(latencies[kind].Quantile(0.95), name+"_p95_us")
		b.ReportMetric(latencies[kind].Quantile(0.99), name+"_p99_us")
	}

	for _, client := range clients {
		if err := client.Close(); err != nil {
			b.Error(err)
		}
	}
	stopServer()
	if err := node.Close(); err != nil {
		b.Error(err)
	}
}

const (
	mixedOperationPoint = iota
	mixedOperationAggregate
	mixedOperationSearch
	mixedOperationUpdate
	mixedOperationKinds
)

var boundedLatencyBuckets = [...]time.Duration{
	25 * time.Microsecond,
	50 * time.Microsecond,
	100 * time.Microsecond,
	250 * time.Microsecond,
	500 * time.Microsecond,
	time.Millisecond,
	2 * time.Millisecond,
	3 * time.Millisecond,
	4 * time.Millisecond,
	5 * time.Millisecond,
	6 * time.Millisecond,
	8 * time.Millisecond,
	10 * time.Millisecond,
	12 * time.Millisecond,
	16 * time.Millisecond,
	20 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	5 * time.Second,
}

type boundedLatencyHistogram struct {
	counts [len(boundedLatencyBuckets) + 1]atomic.Uint64
	total  atomic.Uint64
	max    atomic.Int64
}

func (histogram *boundedLatencyHistogram) Observe(duration time.Duration) {
	index := len(boundedLatencyBuckets)
	for candidate, upperBound := range boundedLatencyBuckets {
		if duration <= upperBound {
			index = candidate
			break
		}
	}
	histogram.counts[index].Add(1)
	histogram.total.Add(1)
	for {
		current := histogram.max.Load()
		if int64(duration) <= current || histogram.max.CompareAndSwap(current, int64(duration)) {
			return
		}
	}
}

// Quantile returns the observed bucket's upper bound in microseconds. The
// overflow bucket returns the exact maximum rather than pretending it met the
// final finite boundary.
func (histogram *boundedLatencyHistogram) Quantile(quantile float64) float64 {
	total := histogram.total.Load()
	if total == 0 {
		return 0
	}
	target := uint64(float64(total)*quantile + 0.999999999)
	if target < 1 {
		target = 1
	}
	var observed uint64
	for index := range histogram.counts {
		observed += histogram.counts[index].Load()
		if observed < target {
			continue
		}
		if index < len(boundedLatencyBuckets) {
			return float64(boundedLatencyBuckets[index]) / float64(time.Microsecond)
		}
		return float64(histogram.max.Load()) / float64(time.Microsecond)
	}
	return float64(histogram.max.Load()) / float64(time.Microsecond)
}

func postgresNodeMixedOperationKind(projected bool, operation int) int {
	if projected {
		switch operation % 3 {
		case 1:
			return mixedOperationAggregate
		case 2:
			return mixedOperationSearch
		default:
			return mixedOperationPoint
		}
	}
	if operation%2 != 0 {
		return mixedOperationUpdate
	}
	return mixedOperationPoint
}

func TestBoundedLatencyHistogramQuantiles(t *testing.T) {
	var histogram boundedLatencyHistogram
	for range 95 {
		histogram.Observe(900 * time.Microsecond)
	}
	for range 4 {
		histogram.Observe(4_500 * time.Microsecond)
	}
	histogram.Observe(6 * time.Second)
	if got := histogram.Quantile(0.95); got != 1_000 {
		t.Fatalf("p95 = %v us, want 1000", got)
	}
	if got := histogram.Quantile(0.99); got != 5_000 {
		t.Fatalf("p99 = %v us, want 5000", got)
	}
	if got := histogram.Quantile(1); got < 6_000_000 {
		t.Fatalf("overflow maximum = %v us, want at least 6000000", got)
	}
}
