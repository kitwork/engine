package kitsql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/kitsql"
	"github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/relational"
	"github.com/lib/pq"
)

// This smoke harness is deliberately finite. It is not a 24-hour qualification.
const sqlCanaryRows = 32

type sqlCanaryRow struct {
	ID, Version int64
	Payload     string
}

func sqlCanaryCheckRows(rows []sqlCanaryRow, expected []int64, payload string) error {
	seen := make([]bool, len(expected))
	for _, row := range rows {
		if row.ID < 1 || row.ID >= int64(len(expected)) || seen[row.ID] {
			return fmt.Errorf("invalid or duplicate row %d", row.ID)
		}
		seen[row.ID] = true
		if row.Version != expected[row.ID] || row.Payload != payload {
			return fmt.Errorf("ACK/payload mismatch for row %d", row.ID)
		}
	}
	if len(rows) != len(expected)-1 {
		return fmt.Errorf("missing rows: got %d want %d", len(rows), len(expected)-1)
	}
	return nil
}

func sqlCanaryCheckIndex(ids []int64, version int64, expected []int64) error {
	seen := make([]bool, len(expected))
	for _, id := range ids {
		if id < 1 || id >= int64(len(expected)) || seen[id] || expected[id] != version {
			return fmt.Errorf("incorrect index membership: id=%d version=%d", id, version)
		}
		seen[id] = true
	}
	for id := 1; id < len(expected); id++ {
		if (expected[id] == version) != seen[id] {
			return fmt.Errorf("missing index member %d", id)
		}
	}
	return nil
}

// Only explicit rejection codes are nonfatal. Unknown write outcomes are never
// counted as ACKs or automatically replayed by the harness.
func sqlCanaryRejection(err error) string {
	if errors.Is(err, kitdb.ErrDurabilityUncertain) {
		return ""
	}
	var protocol *kitsql.ProtocolError
	if errors.As(err, &protocol) {
		switch {
		case protocol.Code == "KSQL_CONFLICT" && protocol.Status == http.StatusConflict:
			return "conflict"
		case (protocol.Code == "KSQL_BUSY" || protocol.Code == "KSQL_ADMISSION_TIMEOUT") && protocol.Status == http.StatusServiceUnavailable:
			return "busy"
		default:
			return ""
		}
	}
	var pg *pq.Error
	if errors.As(err, &pg) {
		switch pg.Code {
		case "40001":
			return "conflict"
		case "53300":
			return "busy"
		default:
			return ""
		}
	}
	if errors.Is(err, kitdb.ErrTransactionConflict) {
		return "conflict"
	}
	for _, known := range []error{kitdb.ErrSnapshotAdmissionFull, kitdb.ErrSnapshotAdmissionTimeout,
		kitdb.ErrSnapshotMemory, kitdb.ErrTooManySnapshots, kitdb.ErrTooManyTransactions,
		kitdb.ErrActiveTransactionMemory, kitdb.ErrSnapshotsActive, node.ErrCheckpointBackoff, node.ErrMaintenanceQueueFull} {
		if errors.Is(err, known) {
			return "busy"
		}
	}
	return ""
}

func TestSQLCanaryOracles(t *testing.T) {
	expected := []int64{0, 1, 2}
	valid := []sqlCanaryRow{{1, 1, "fixture"}, {2, 2, "fixture"}}
	if err := sqlCanaryCheckRows(valid, expected, "fixture"); err != nil {
		t.Fatal(err)
	}
	for name, rows := range map[string][]sqlCanaryRow{
		"missing": valid[:1], "duplicate": {valid[0], valid[0]},
		"version": {{1, 2, "fixture"}, valid[1]},
		"payload": {{1, 1, "wrong"}, valid[1]}, "outside": {{3, 1, "fixture"}, valid[1]},
	} {
		t.Run(name, func(t *testing.T) {
			if sqlCanaryCheckRows(rows, expected, "fixture") == nil {
				t.Fatal("false pass")
			}
		})
	}
	if err := sqlCanaryCheckIndex([]int64{2}, 2, expected); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]int64{nil, {1}, {2, 2}, {3}} {
		if sqlCanaryCheckIndex(ids, 2, expected) == nil {
			t.Fatalf("false index pass: %v", ids)
		}
	}
}

func sqlCanaryFailure(stage string, err error) error {
	var pg *pq.Error
	if errors.As(err, &pg) {
		return fmt.Errorf("%s SQLSTATE=%s: %w", stage, pg.Code, err)
	}
	var protocol *kitsql.ProtocolError
	if errors.As(err, &protocol) {
		return fmt.Errorf("%s HTTP=%d code=%s: %w", stage, protocol.Status, protocol.Code, err)
	}
	return fmt.Errorf("%s: %w", stage, err)
}

func TestSQLCanaryErrorClassification(t *testing.T) {
	for _, test := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("wrapped: %w", kitdb.ErrTransactionConflict), "conflict"},
		{kitdb.ErrSnapshotMemory, "busy"},
		{&pq.Error{Code: "XX000", Message: "kitdb: snapshot memory budget reached"}, ""},
		{&pq.Error{Code: "40001"}, "conflict"}, {&pq.Error{Code: "53300"}, "busy"},
		{&kitsql.ProtocolError{Status: 409, Code: "KSQL_CONFLICT"}, "conflict"},
		{&kitsql.ProtocolError{Status: 503, Code: "KSQL_BUSY"}, "busy"},
		{&kitsql.ProtocolError{Status: 500, Code: "KSQL_BUSY"}, ""},
		{kitdb.ErrDurabilityUncertain, ""}, {context.DeadlineExceeded, ""},
		{&kitsql.ProtocolError{Status: 500, Code: "KSQL_DURABILITY"}, ""},
		{errors.New("connection lost after submitting a write"), ""},
		{errors.New("KSQL_BUSY transaction conflict"), ""},
	} {
		if got := sqlCanaryRejection(test.err); got != test.want {
			t.Fatalf("%v: got %q want %q", test.err, got, test.want)
		}
	}
}

type sqlCanaryAdapterResult struct {
	Name                                             string
	ACKs, Reads, Conflicts, Busy, EarlyClosedStreams uint64
}

func TestSQLCanarySmoke(t *testing.T) {
	if os.Getenv("KITDB_SQL_CANARY_SMOKE") != "1" {
		t.Skip("set KITDB_SQL_CANARY_SMOKE=1 for finite real-loopback smoke")
	}
	testDeadline := 45 * time.Second
	if os.Getenv("KITDB_SQL_CANARY_POLICY") == "1" {
		testDeadline = 70 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
	defer cancel()
	report := struct {
		Format, Scope                                                  string
		Success, ExactData, ExactIndex, Reopen, Drained, ManagerClosed bool
		Adapters                                                       []sqlCanaryAdapterResult
		Kernel                                                         kitdb.Stats
		Manager                                                        node.Stats
		Pressure                                                       *sqlCanaryPressureResult `json:",omitempty"`
		Rows, PayloadBytes                                             int
		SnapshotMemoryBytes, TransactionMemoryBytes                    int64
	}{Format: "kitdb-sql-canary-smoke/v1", Scope: "finite Windows-compatible smoke; not long-running qualification"}
	defer func() {
		report.Success = !t.Failed() && report.ExactData && report.ExactIndex && report.Reopen && report.Drained && report.ManagerClosed
		encoded, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if path := os.Getenv("KITDB_SQL_CANARY_REPORT"); path != "" {
			if !filepath.IsAbs(path) {
				t.Error("report path must be absolute")
				return
			}
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Error(err)
			}
		}
		t.Log(string(encoded))
	}()
	path := filepath.Join(t.TempDir(), "smoke.kitdb")
	manager, err := node.NewManager(node.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	openOptions := kitdb.OpenOptions{SnapshotMemoryBytes: 8 << 20, TransactionMemoryBytes: 8 << 20}
	policy := os.Getenv("KITDB_SQL_CANARY_POLICY") == "1"
	if policy {
		openOptions.SnapshotMemoryBytes = 512 << 10
	}
	report.SnapshotMemoryBytes, report.TransactionMemoryBytes = openOptions.SnapshotMemoryBytes, openOptions.TransactionMemoryBytes
	lease, err := manager.Acquire(ctx, path, openOptions)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lease.Release() })
	kernel := lease.DB()
	engine, err := relational.Attach(kernel, relational.Options{Kernel: openOptions, MaximumResultRows: sqlCanaryRows, CheckpointManager: manager})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	native, err := relational.OpenNativeSQL(engine)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = native.Close() })
	native.SetMaxOpenConns(4)
	native.SetMaxIdleConns(4)
	for _, query := range []string{
		"CREATE TABLE items(id BIGINT PRIMARY KEY, version BIGINT NOT NULL, payload TEXT NOT NULL)",
		"CREATE INDEX items_version ON items(version)",
	} {
		if _, err := native.ExecContext(ctx, query); err != nil {
			t.Fatal(err)
		}
	}
	payload := "sql-canary-fixture"
	if policy {
		payload = strings.Repeat("fixture-", 1024)
	}
	report.Rows, report.PayloadBytes = sqlCanaryRows, len(payload)
	for id := 1; id <= sqlCanaryRows; id++ {
		if _, err := native.ExecContext(ctx, "INSERT INTO items VALUES($1,0,$2)", id, payload); err != nil {
			t.Fatal(err)
		}
	}
	pgListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pgListener.Close() })
	pgCtx, pgCancel := context.WithCancel(ctx)
	t.Cleanup(pgCancel)
	pgDone := make(chan error, 1)
	go func() {
		pgDone <- engine.ServePostgres(pgCtx, pgListener, relational.PostgresServerOptions{
			PostgresOptions: relational.PostgresOptions{Database: "smoke", User: "kitdb", Password: "smoke-only"},
			MaxConnections:  4, QueryTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second,
		})
	}()
	pg, err := sql.Open("postgres", "postgres://kitdb:smoke-only@"+pgListener.Addr().String()+"/smoke?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Close() })
	pg.SetMaxOpenConns(2)
	pg.SetMaxIdleConns(2)
	if err := pg.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Open: func(_ context.Context, user, password, database string) (*sql.DB, func() error, error) {
			if user != "kitdb" || password != "smoke-only" {
				return nil, nil, kitsql.ErrUnauthorized
			}
			if database != "smoke" {
				return nil, nil, kitsql.ErrDatabaseNotFound
			}
			return native, func() error { return nil }, nil
		}, AllowInsecureLocal: true, MaximumConcurrent: 4, MaximumRows: sqlCanaryRows, QueryTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	var active atomic.Int64
	httpServer := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		active.Add(1)
		defer active.Add(-1)
		handler.ServeHTTP(w, r)
	}), ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second}
	httpListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = httpListener.Close() })
	t.Cleanup(func() { _ = httpServer.Close() })
	httpDone := make(chan error, 1)
	go func() { httpDone <- httpServer.Serve(httpListener) }()
	transport := &http.Transport{MaxConnsPerHost: 2, MaxIdleConns: 2, MaxIdleConnsPerHost: 2}
	t.Cleanup(transport.CloseIdleConnections)
	remote, err := kitsql.OpenWithClient("kitsql://kitdb:smoke-only@"+httpListener.Addr().String()+"/smoke?sslmode=disable", &http.Client{Transport: transport, Timeout: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = remote.Close() })
	remote.SetMaxOpenConns(2)
	remote.SetMaxIdleConns(2)
	pools := []*sql.DB{native, pg, remote}
	report.Adapters = []sqlCanaryAdapterResult{{Name: "native"}, {Name: "pgwire"}, {Name: "kitsql"}}
	expected := make([]int64, sqlCanaryRows+1)
	// One writer per key: the ACK model needs no response-arrival ordering.
	var workers sync.WaitGroup
	failures := make(chan error, len(pools))
	for adapter, pool := range pools {
		workers.Add(1)
		go func(adapter int, pool *sql.DB) {
			defer workers.Done()
			result := &report.Adapters[adapter]
			id := adapter + 1
			for operation := 0; operation < 12; operation++ {
				var writeErr error
				var affected int64
				if adapter == 0 {
					tx, err := pool.BeginTx(ctx, nil)
					if err != nil {
						if policy && sqlCanaryRejection(err) == "busy" {
							result.Busy++
							continue
						}
						failures <- fmt.Errorf("%s begin: %w", result.Name, err)
						return
					}
					res, err := tx.ExecContext(ctx, "UPDATE items SET version=version+1 WHERE id=$1", id)
					if err == nil {
						affected, err = res.RowsAffected()
					}
					if err == nil && affected != 1 {
						err = fmt.Errorf("affected %d", affected)
					}
					if err == nil {
						writeErr = tx.Commit()
					} else {
						_ = tx.Rollback()
						writeErr = err
					}
				} else {
					res, err := pool.ExecContext(ctx, "UPDATE items SET version=version+1 WHERE id=$1", id)
					writeErr = err
					if err == nil {
						affected, writeErr = res.RowsAffected()
						if writeErr == nil && affected != 1 {
							writeErr = fmt.Errorf("affected %d", affected)
						}
					}
				}
				if writeErr == nil {
					expected[id]++
					result.ACKs++
				} else {
					switch sqlCanaryRejection(writeErr) {
					case "conflict":
						result.Conflicts++
					case "busy":
						result.Busy++
					default:
						failures <- fmt.Errorf("%s write outcome: %w", result.Name, writeErr)
						return
					}
				}
				var version int64
				var text string
				if err := pool.QueryRowContext(ctx, "SELECT version,payload FROM items WHERE id=$1", id).Scan(&version, &text); err != nil {
					if policy && sqlCanaryRejection(err) == "busy" {
						result.Busy++
						continue
					}
					failures <- fmt.Errorf("%s read: %w", result.Name, err)
					return
				}
				if version != expected[id] || text != payload {
					failures <- fmt.Errorf("%s read mismatch", result.Name)
					return
				}
				result.Reads++
			}
		}(adapter, pool)
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if t.Failed() {
		return
	}
	if os.Getenv("KITDB_SQL_CANARY_PRESSURE") == "1" || policy {
		pressure, err := runSQLCanaryPressure(ctx, pools, expected, kernel, manager, path, openOptions)
		report.Pressure = &pressure
		if err != nil {
			t.Fatal(err)
		}
	}
	check := func(pool *sql.DB) {
		t.Helper()
		rows, err := pool.QueryContext(ctx, "SELECT id,version,payload FROM items")
		if err != nil {
			t.Fatal(err)
		}
		var got []sqlCanaryRow
		for rows.Next() {
			var row sqlCanaryRow
			if err := rows.Scan(&row.ID, &row.Version, &row.Payload); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			got = append(got, row)
		}
		err = rows.Err()
		closeErr := rows.Close()
		if err != nil || closeErr != nil {
			t.Fatal(errors.Join(err, closeErr))
		}
		if err := sqlCanaryCheckRows(got, expected, payload); err != nil {
			t.Fatal(err)
		}
		versions := make(map[int64]bool)
		for id := 1; id < len(expected); id++ {
			versions[expected[id]] = true
		}
		for version := range versions {
			indexed, err := pool.QueryContext(ctx, "SELECT id FROM items WHERE version=$1", version)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for indexed.Next() {
				var id int64
				if err := indexed.Scan(&id); err != nil {
					_ = indexed.Close()
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			err = indexed.Err()
			closeErr = indexed.Close()
			if err != nil || closeErr != nil {
				t.Fatal(errors.Join(err, closeErr))
			}
			if err := sqlCanaryCheckIndex(ids, version, expected); err != nil {
				t.Fatal(err)
			}
		}
	}
	for adapter, pool := range pools {
		if report.Adapters[adapter].ACKs == 0 {
			t.Fatalf("no write progress: %s", report.Adapters[adapter].Name)
		}
		check(pool)
		readCtx, readCancel := context.WithCancel(ctx)
		rows, err := pool.QueryContext(readCtx, "SELECT id,payload FROM items")
		if err != nil {
			readCancel()
			t.Fatal(err)
		}
		if !rows.Next() {
			_ = rows.Close()
			readCancel()
			t.Fatal("early-close stream has no row")
		}
		readCancel()
		_ = rows.Close()
		report.Adapters[adapter].EarlyClosedStreams++
	}
	report.ExactData, report.ExactIndex = true, true
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	if err := pg.Close(); err != nil {
		t.Fatal(err)
	}
	if err := httpServer.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-httpDone; !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	transport.CloseIdleConnections()
	pgCancel()
	if err := <-pgDone; err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		stats, err := kernel.Stats()
		if err != nil {
			t.Fatal(err)
		}
		ms := manager.Stats()
		if active.Load() == 0 && native.Stats().InUse == 0 && stats.ActiveSnapshots == 0 && stats.SnapshotReservations == 0 && stats.SnapshotWaiters == 0 && stats.SnapshotBytes == 0 && stats.SnapshotReservedBytes == 0 && stats.FrontendTransactionBytes == 0 && stats.ActiveTransactionBytes == 0 && stats.ActiveTransactions == 0 && stats.PendingCommits == 0 && ms.QueuedMaintenance == 0 && ms.RunningMaintenance == 0 {
			report.Kernel = stats
			report.Drained = true
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("resources did not drain: %+v / %+v", stats, ms)
		}
		time.Sleep(10 * time.Millisecond)
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
	report.Manager = manager.Stats()
	if !report.Manager.Closed || report.Manager.OpenDatabases != 0 || report.Manager.ActiveLeases != 0 || report.Manager.WaitingAcquisitions != 0 || report.Manager.QueuedMaintenance != 0 || report.Manager.RunningMaintenance != 0 {
		t.Fatalf("manager retained resources: %+v", report.Manager)
	}
	report.ManagerClosed = true
	// Only reopen after all adapters and the original owner have closed.
	reopened, err := relational.OpenWithOptions(path, relational.Options{MaximumResultRows: sqlCanaryRows, Kernel: kitdb.OpenOptions{VerifyOnOpen: true}})
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
	if err := reopenedSQL.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	report.Reopen = true
}
