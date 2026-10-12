package kitsql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/bits"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/relational"
)

const networkLoadRows = 10_000

type networkLoadPhase struct {
	Name              string   `json:"name"`
	Workers           int      `json:"workers"`
	Requests          uint64   `json:"requests"`
	Success           uint64   `json:"success"`
	Conflicts         uint64   `json:"conflicts"`
	Busy              uint64   `json:"busy"`
	Unexpected        uint64   `json:"unexpected"`
	Seconds           float64  `json:"seconds"`
	RequestsPerSecond float64  `json:"requests_per_second"`
	P50UpperMicros    uint64   `json:"p50_upper_micros"`
	P95UpperMicros    uint64   `json:"p95_upper_micros"`
	P99UpperMicros    uint64   `json:"p99_upper_micros"`
	MaxMicros         uint64   `json:"max_micros"`
	Errors            []string `json:"errors,omitempty"`
}

type networkLoadMetrics struct {
	requests, success, conflicts, busy, unexpected atomic.Uint64
	buckets                                        [32]atomic.Uint64
	maximum                                        atomic.Uint64
	mu                                             sync.Mutex
	errors                                         []string
}

func (m *networkLoadMetrics) observe(start time.Time, err error) {
	micros := uint64(time.Since(start).Microseconds())
	m.requests.Add(1)
	bucket := bits.Len64(micros)
	if bucket >= len(m.buckets) {
		bucket = len(m.buckets) - 1
	}
	m.buckets[bucket].Add(1)
	for old := m.maximum.Load(); micros > old; old = m.maximum.Load() {
		if m.maximum.CompareAndSwap(old, micros) {
			break
		}
	}
	switch {
	case err == nil:
		m.success.Add(1)
	case strings.Contains(err.Error(), "transaction conflict"):
		m.conflicts.Add(1)
	case strings.Contains(err.Error(), "KSQL_BUSY"), strings.Contains(err.Error(), "KSQL_ADMISSION_TIMEOUT"):
		m.busy.Add(1)
	default:
		m.unexpected.Add(1)
		m.mu.Lock()
		if len(m.errors) < 8 {
			m.errors = append(m.errors, err.Error())
		}
		m.mu.Unlock()
	}
}

func (m *networkLoadMetrics) percentile(percent uint64) uint64 {
	target := (m.requests.Load()*percent + 99) / 100
	var seen uint64
	for i := range m.buckets {
		seen += m.buckets[i].Load()
		if seen >= target {
			return (uint64(1) << i) - 1
		}
	}
	return m.maximum.Load()
}

// Opt-in real TCP campaign. Ordinary unit tests never start a long load run.
func TestKitSQLNetworkLoadCampaign(t *testing.T) {
	if os.Getenv("KITDB_NETWORK_LOAD") != "1" {
		t.Skip("set KITDB_NETWORK_LOAD=1 for real TCP load")
	}
	seconds := 12
	if source := os.Getenv("KITDB_NETWORK_SECONDS"); source != "" {
		var err error
		seconds, err = strconv.Atoi(source)
		if err != nil || seconds < 1 || seconds > 120 {
			t.Fatal("KITDB_NETWORK_SECONDS must be 1..120")
		}
	}
	port := os.Getenv("KITDB_NETWORK_PORT")
	if port == "" {
		port = "18195"
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	path := filepath.Join(t.TempDir(), "network-load.kitdb")
	manager, err := node.NewManager(node.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	kernel := lease.DB()
	t.Cleanup(func() { _ = lease.Release() })
	engine, err := relational.Attach(kernel, relational.Options{MaximumResultRows: networkLoadRows, CheckpointManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	native, err := relational.OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	// Exceed the snapshot ceiling deliberately: kernel admission owns queuing.
	native.SetMaxOpenConns(64)
	native.SetMaxIdleConns(64)
	ctx := context.Background()
	for _, query := range []string{
		"CREATE TABLE items(id BIGINT PRIMARY KEY, value BIGINT NOT NULL, payload TEXT NOT NULL)",
		"CREATE INDEX items_value ON items(value)",
	} {
		if _, err := native.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	payloadRepeats := 8
	if source := os.Getenv("KITDB_NETWORK_PAYLOAD_REPEATS"); source != "" {
		payloadRepeats, err = strconv.Atoi(source)
		if err != nil || payloadRepeats < 1 || payloadRepeats > 64 {
			t.Fatal("KITDB_NETWORK_PAYLOAD_REPEATS must be 1..64")
		}
	}
	payload := strings.Repeat("network-load-", payloadRepeats)
	for first := 1; first <= networkLoadRows; first += 100 {
		var query strings.Builder
		query.WriteString("INSERT INTO items VALUES ")
		for id := first; id < first+100; id++ {
			if id != first {
				query.WriteByte(',')
			}
			fmt.Fprintf(&query, "(%d, 0, '%s')", id, payload)
		}
		if _, err := native.ExecContext(ctx, query.String()); err != nil {
			t.Fatal(err)
		}
	}
	seedTicket, err := manager.ScheduleCheckpoint(ctx, path, kitdb.OpenOptions{}, node.MaintenanceUrgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedTicket.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	checkpointBaseline := manager.Stats().CheckpointCompletions
	var active atomic.Int64
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Open: func(_ context.Context, user, password, database string) (*sql.DB, func() error, error) {
			if user != "kitdb" || password != "local-load-only" {
				return nil, nil, kitsql.ErrUnauthorized
			}
			if database != "load" {
				return nil, nil, kitsql.ErrDatabaseNotFound
			}
			return native, func() error { return nil }, nil
		},
		AllowInsecureLocal: true, MaximumConcurrent: 64, MaximumRows: networkLoadRows,
		QueryTimeout: 20 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		handler.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	transport := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 128, MaxConnsPerHost: 128}
	t.Cleanup(transport.CloseIdleConnections)
	remote, err := kitsql.OpenWithClient("kitsql://kitdb:local-load-only@"+listener.Addr().String()+"/load?sslmode=disable", &http.Client{Transport: transport, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	remote.SetMaxOpenConns(128)
	remote.SetMaxIdleConns(128)
	if err := remote.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	t.Logf("real TCP KitSQL listener: %s; rows=%d; %ds per phase", listener.Addr(), networkLoadRows, seconds)
	bad, err := kitsql.OpenWithClient("kitsql://kitdb:wrong@"+listener.Addr().String()+"/load?sslmode=disable", &http.Client{Transport: transport, Timeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	authErr := bad.PingContext(ctx)
	_ = bad.Close()
	if authErr == nil || !strings.Contains(authErr.Error(), "KSQL_AUTH") {
		t.Fatalf("authentication not rejected: %v", authErr)
	}
	var expected [networkLoadRows + 1]atomic.Int64
	var heapPeak, goroutinePeak atomic.Uint64
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	monitorDone := make(chan struct{})
	monitorStopped := make(chan struct{})
	go func() {
		defer close(monitorStopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-monitorDone:
				return
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapAlloc > heapPeak.Load() {
					heapPeak.Store(stats.HeapAlloc)
				}
				if n := uint64(runtime.NumGoroutine()); n > goroutinePeak.Load() {
					goroutinePeak.Store(n)
				}
			}
		}
	}()
	var phases []networkLoadPhase
	for _, phase := range []struct {
		name    string
		workers int
	}{
		{"point-read", 64}, {"mixed-independent", 64}, {"hot-row", 64}, {"large-stream", 8}, {"cancel-overload", 128},
	} {
		t.Logf("starting %s (%d workers)", phase.name, phase.workers)
		m := &networkLoadMetrics{}
		start := time.Now()
		deadline := start.Add(time.Duration(seconds) * time.Second)
		var group sync.WaitGroup
		for worker := 0; worker < phase.workers; worker++ {
			group.Add(1)
			go func(worker int) {
				defer group.Done()
				for sequence := 0; time.Now().Before(deadline); sequence++ {
					started := time.Now()
					var err error
					switch {
					case phase.name == "hot-row" || phase.name == "mixed-independent" && worker%2 == 0:
						id := int64(worker + 1)
						if phase.name == "hot-row" {
							id = 1
						}
						var result sql.Result
						result, err = remote.ExecContext(ctx, "UPDATE items SET value=value+1 WHERE id=$1", id)
						if err == nil {
							var count int64
							count, err = result.RowsAffected()
							if err == nil && count != 1 {
								err = fmt.Errorf("update affected %d", count)
							}
							if err == nil {
								expected[id].Add(1)
							}
						}
					case phase.name == "large-stream" || phase.name == "cancel-overload":
						queryCtx, cancel := context.WithCancel(ctx)
						var rows *sql.Rows
						rows, err = remote.QueryContext(queryCtx, "SELECT id, payload FROM items LIMIT 10000")
						if err == nil {
							count := 0
							for rows.Next() {
								var id int64
								var text string
								if err = rows.Scan(&id, &text); err != nil {
									break
								}
								if id < 1 || id > networkLoadRows || text != payload {
									err = errors.New("invalid streamed row")
									break
								}
								count++
								if phase.name == "cancel-overload" {
									cancel()
									break
								}
							}
							if phase.name == "large-stream" && err == nil {
								err = rows.Err()
								if err == nil && count != networkLoadRows {
									err = fmt.Errorf("stream rows=%d", count)
								}
							}
							if phase.name == "cancel-overload" && count == 0 && err == nil {
								err = rows.Err()
								if err == nil {
									err = errors.New("cancel stream returned no row")
								}
							}
							_ = rows.Close()
						}
						cancel()
					default:
						id := int64((worker*97+sequence)%networkLoadRows + 1)
						var value int64
						var text string
						var rowID int64
						err = remote.QueryRowContext(ctx, "SELECT id, value, payload FROM items WHERE id=$1", id).Scan(&rowID, &value, &text)
						if err == nil && (rowID != id || value < 0 || text != payload) {
							err = errors.New("invalid point row")
						}
					}
					m.observe(started, err)
				}
			}(worker)
		}
		group.Wait()
		elapsed := time.Since(start).Seconds()
		result := networkLoadPhase{Name: phase.name, Workers: phase.workers, Requests: m.requests.Load(), Success: m.success.Load(), Conflicts: m.conflicts.Load(), Busy: m.busy.Load(), Unexpected: m.unexpected.Load(), Seconds: elapsed, RequestsPerSecond: float64(m.requests.Load()) / elapsed, P50UpperMicros: m.percentile(50), P95UpperMicros: m.percentile(95), P99UpperMicros: m.percentile(99), MaxMicros: m.maximum.Load(), Errors: m.errors}
		phases = append(phases, result)
		t.Logf("%s: requests=%d success=%d conflicts=%d busy=%d unexpected=%d rate=%.1f/s p99<=%dus", result.Name, result.Requests, result.Success, result.Conflicts, result.Busy, result.Unexpected, result.RequestsPerSecond, result.P99UpperMicros)
	}
	close(monitorDone)
	<-monitorStopped
	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = server.Shutdown(shutdownCtx)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if err := <-serveDone; !errors.Is(err, http.ErrServerClosed) {
		t.Fatalf("serve: %v", err)
	}
	_ = remote.Close()
	transport.CloseIdleConnections()
	// No checkpoint timer: only the SQL engine's real pressure policy runs
	// during traffic. Drain its bounded jobs before inspecting ownership.
	drainDeadline := time.Now().Add(20 * time.Second)
	for manager.Stats().QueuedMaintenance != 0 || manager.Stats().RunningMaintenance != 0 {
		if time.Now().After(drainDeadline) {
			t.Fatal("checkpoint maintenance did not drain")
		}
		time.Sleep(10 * time.Millisecond)
	}
	checkpoints := manager.Stats().CheckpointCompletions - checkpointBaseline
	check := func(database *sql.DB) {
		t.Helper()
		rows, err := database.QueryContext(ctx, "SELECT id, value FROM items")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		seen := make([]bool, networkLoadRows+1)
		count := 0
		for rows.Next() {
			var id, value int64
			if err := rows.Scan(&id, &value); err != nil {
				t.Fatal(err)
			}
			if id < 1 || id > networkLoadRows || seen[id] {
				t.Fatalf("invalid/duplicate id=%d", id)
			}
			seen[id] = true
			count++
			if value != expected[id].Load() {
				t.Fatalf("id=%d stored=%d acknowledged=%d", id, value, expected[id].Load())
			}
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if count != networkLoadRows {
			t.Fatalf("missing rows: %d", count)
		}
	}
	checkIndex := func(database *sql.DB) {
		t.Helper()
		groups := make(map[int64]int)
		for id := 1; id <= networkLoadRows; id++ {
			groups[expected[id].Load()]++
		}
		for value, want := range groups {
			rows, err := database.QueryContext(ctx, "SELECT id FROM items WHERE value=$1 LIMIT 10000", value)
			if err != nil {
				t.Fatal(err)
			}
			seen := make(map[int64]bool)
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					_ = rows.Close()
					t.Fatal(err)
				}
				if id < 1 || id > networkLoadRows || seen[id] || expected[id].Load() != value {
					_ = rows.Close()
					t.Fatalf("incorrect index entry id=%d value=%d", id, value)
				}
				seen[id] = true
			}
			if err := rows.Err(); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			_ = rows.Close()
			if len(seen) != want {
				t.Fatalf("index value=%d rows=%d want=%d", value, len(seen), want)
			}
		}
	}
	check(native)
	checkIndex(native)
	stats, err := kernel.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if active.Load() != 0 || stats.ActiveSnapshots != 0 || stats.SnapshotReservations != 0 || stats.SnapshotWaiters != 0 || stats.SnapshotBytes != 0 || stats.SnapshotReservedBytes != 0 || stats.FrontendTransactionBytes != 0 || stats.ActiveTransactionBytes != 0 || stats.ActiveTransactions != 0 || stats.PendingCommits != 0 || native.Stats().InUse != 0 {
		t.Fatalf("resources retained: active=%d stats=%+v native=%+v", active.Load(), stats, native.Stats())
	}
	if err := kernel.Verify(); err != nil {
		t.Fatal(err)
	}
	if _, err := kernel.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := kernel.Verify(); err != nil {
		t.Fatal(err)
	}
	if err := native.Close(); err != nil {
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
	reopened, err := relational.OpenWithOptions(path, relational.Options{MaximumResultRows: networkLoadRows})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	reopenedSQL, err := relational.OpenNativeSQL(reopened)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopenedSQL.Close() })
	check(reopenedSQL)
	checkIndex(reopenedSQL)
	_ = reopenedSQL.Close()
	_ = reopened.Close()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	var writes int64
	for i := range expected {
		writes += expected[i].Load()
	}
	success := true
	for _, phase := range phases {
		if phase.Unexpected != 0 || phase.Success == 0 {
			success = false
		}
	}
	report := struct {
		Format, Platform, GoVersion, Listen        string
		Rows                                       int
		PayloadBytes                               int
		AcknowledgedWrites                         int64
		HeapBefore, HeapPeak, HeapAfter            uint64
		PeakGoroutines                             uint64
		Stats                                      kitdb.Stats
		ConcurrentCheckpoints                      uint64
		Phases                                     []networkLoadPhase
		ExactDataVerified, ReopenVerified, Success bool
	}{"kitdb-network-load/v2-pressure", runtime.GOOS + "/" + runtime.GOARCH, runtime.Version(), listener.Addr().String(), networkLoadRows, len(payload), writes, before.HeapAlloc, heapPeak.Load(), after.HeapAlloc, goroutinePeak.Load(), stats, checkpoints, phases, true, true, success}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if reportPath := os.Getenv("KITDB_NETWORK_REPORT"); reportPath != "" {
		if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(reportPath, encoded, 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("writes=%d exact data + reopen verified; heap before/peak/after=%d/%d/%d; goroutines peak=%d", writes, before.HeapAlloc, heapPeak.Load(), after.HeapAlloc, goroutinePeak.Load())
	if !success {
		t.Fatalf("campaign unexpected failures: %s", encoded)
	}
}
