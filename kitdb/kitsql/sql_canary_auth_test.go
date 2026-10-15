package kitsql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/node"
	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/kitwork/engine/kitdb/relational"
	"github.com/lib/pq"
)

const sqlAuthPassword = "Authorization-canary-only!"
const sqlAuthRows = 4

var sqlAuthAdapters = [...]string{"native", "pg", "http"}

func sqlAuthConfig(seconds, rounds string) (time.Duration, int, error) {
	s, n := 6, 1
	var err error
	if seconds != "" {
		s, err = strconv.Atoi(seconds)
	}
	if err != nil || s < 2 || s > 30 {
		return 0, 0, errors.New("auth phase seconds must be 2..30")
	}
	if rounds != "" {
		n, err = strconv.Atoi(rounds)
	}
	if err != nil || n < 1 || n > 4 {
		return 0, 0, errors.New("auth rounds must be 1..4")
	}
	return time.Duration(s) * time.Second, n, nil
}

func sqlAuthDenied(err error) bool {
	if errors.Is(err, kitdb.ErrDurabilityUncertain) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var local *pgwire.Error
	var pg *pq.Error
	var remote *kitsql.ProtocolError
	return errors.As(err, &local) && local != nil && local.Code == "42501" ||
		errors.As(err, &pg) && pg != nil && pg.Code == "42501" ||
		errors.As(err, &remote) && remote != nil && remote.Status == http.StatusForbidden && remote.Code == "KSQL_FORBIDDEN"
}

func TestSQLAuthCanaryOracles(t *testing.T) {
	for _, cfg := range [][2]string{{"", ""}, {"2", "1"}, {"30", "4"}} {
		if _, _, err := sqlAuthConfig(cfg[0], cfg[1]); err != nil {
			t.Fatal(cfg, err)
		}
	}
	for _, cfg := range [][2]string{{"1", "1"}, {"31", "1"}, {"bad", "1"}, {"6", "0"}, {"6", "5"}, {"6", "bad"}} {
		if _, _, err := sqlAuthConfig(cfg[0], cfg[1]); err == nil {
			t.Fatal("accepted unbounded config", cfg)
		}
	}
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{pgwire.NewError("42501", "denied"), true},
		{fmt.Errorf("wrapped: %w", &pq.Error{Code: "42501"}), true},
		{&kitsql.ProtocolError{Status: 403, Code: "KSQL_FORBIDDEN"}, true},
		{&kitsql.ProtocolError{Status: 400, Code: "KSQL_FORBIDDEN"}, false},
		{&kitsql.ProtocolError{Status: 400, Code: "KSQL_EXECUTION", Message: "permission denied"}, false},
		{pgwire.NewError("42601", "permission denied"), false},
		{errors.New("42501 permission denied"), false},
		{errors.Join(kitdb.ErrDurabilityUncertain, pgwire.NewError("42501", "denied")), false},
	} {
		if sqlAuthDenied(tc.err) != tc.want {
			t.Fatal("permission classifier", tc.err)
		}
	}
}

type sqlAuthSample struct {
	ElapsedMS        int64
	ACKs, Denied     [3]uint64
	Sessions, Leases int
	HeapBytes        uint64
	Checkpoints      uint64
}

type sqlAuthPhase struct {
	Name                                 string
	RequestedMS, WorkloadMS, DrainMS     int64
	WriteAllowed                         [3]bool
	ACKs, Reads, Denied, Conflicts, Busy [3]uint64
	Checkpoints                          uint64
	PinnedRead                           bool
	Samples                              []sqlAuthSample
	Success                              bool
}

type sqlAuthReport struct {
	Format, Scope, OS, Arch                                       string
	GoVersion                                                     string
	StartedAt                                                     time.Time
	DurationMS                                                    int64
	PhaseSeconds, Rounds                                          int
	Phases                                                        []sqlAuthPhase
	PinnedCommits, MappingChecks                                  int
	ExactData, ExactIndex, OfflineVerify, Reopen, Closed, Success bool
	BeforeClose, AfterClose, ReopenedClose                        relational.PostgresNodeStats
}

// Managed-root companion to the existing SQL canary. Reuse its ACK/index
// oracles and typed rejection classifier; do not expose a new production hook.
func TestSQLCanaryAuthorization(t *testing.T) {
	if os.Getenv("KITDB_SQL_CANARY_AUTH") != "1" {
		t.Skip("set KITDB_SQL_CANARY_AUTH=1 for managed authorization canary")
	}
	duration, rounds, err := sqlAuthConfig(os.Getenv("KITDB_SQL_AUTH_PHASE_SECONDS"), os.Getenv("KITDB_SQL_AUTH_ROUNDS"))
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	r := sqlAuthReport{Format: "kitdb-sql-authorization/v1", Scope: "bounded managed SQL phases; not release or power-loss qualification", OS: runtime.GOOS, Arch: runtime.GOARCH, GoVersion: runtime.Version(), StartedAt: started.UTC(), PhaseSeconds: int(duration / time.Second), Rounds: rounds}
	t.Cleanup(func() {
		r.DurationMS = time.Since(started).Milliseconds()
		r.Success = !t.Failed() && r.ExactData && r.ExactIndex && r.OfflineVerify && r.Reopen && r.Closed && len(r.Phases) == 3*rounds
		data, err := json.MarshalIndent(r, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if path := os.Getenv("KITDB_SQL_AUTH_REPORT"); path != "" {
			if !filepath.IsAbs(path) {
				t.Error("auth report path must be absolute")
				return
			}
			if err := os.WriteFile(path, append(data, '\n'), 0600); err != nil {
				t.Error(err)
				r.Success = false
				data, _ = json.MarshalIndent(r, "", "  ")
			}
		}
		t.Log(string(data))
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(rounds)*3*duration+5*time.Minute)
	t.Cleanup(cancel)
	root := t.TempDir()
	payload := strings.Repeat("auth-row-", 1024)
	seedSQLAuth(t, ctx, root, payload)
	f := startSQLAuth(t, ctx, root)
	exec := func(q string) {
		t.Helper()
		if _, err := f.admin.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	exec("CREATE ROLE auditor")
	for _, adapter := range sqlAuthAdapters {
		exec("CREATE ROLE writer_" + adapter)
		exec("GRANT ROLE writer_" + adapter + " ON DATABASE shop TO writer_" + adapter)
		for _, privilege := range []string{"SELECT", "UPDATE"} {
			exec("GRANT " + privilege + " ON TABLE items_" + adapter + " TO ROLE writer_" + adapter)
		}
		exec("GRANT SELECT ON TABLE items_" + adapter + " TO ROLE auditor")
	}
	exec("GRANT ROLE auditor ON DATABASE shop TO auditor")
	audit := f.open(t, "native", "auditor")
	clients := [3]*sql.DB{}
	for i, adapter := range sqlAuthAdapters {
		clients[i] = f.open(t, adapter, "writer_"+adapter)
	}
	var roleIDs [3]string
	for i, adapter := range sqlAuthAdapters {
		if err := f.admin.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='writer_"+adapter+"'").Scan(&roleIDs[i]); err != nil {
			t.Fatal(err)
		}
	}
	model := [3][]int64{}
	for i := range model {
		model[i] = make([]int64, sqlAuthRows+1)
	}
	check := func() {
		t.Helper()
		for i, adapter := range sqlAuthAdapters {
			if err := checkSQLAuth(ctx, clients[i], "items_"+adapter, model[i], payload); err != nil {
				t.Fatal(adapter, err)
			}
			if err := checkSQLAuth(ctx, audit, "items_"+adapter, model[i], payload); err != nil {
				t.Fatal("auditor", adapter, err)
			}
		}
		var value int
		if err := f.admin.QueryRowContext(ctx, "SELECT version FROM secret WHERE id=1").Scan(&value); err != nil || value != 0 {
			t.Fatal("secret changed", value, err)
		}
	}
	deny := func(db *sql.DB, q string) {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if rows != nil {
			for rows.Next() {
			}
			err = errors.Join(err, rows.Err(), rows.Close())
		}
		if !sqlAuthDenied(err) {
			t.Fatalf("expected typed permission denial: %s: %v", q, err)
		}
	}
	for i := range clients {
		deny(clients[i], "SELECT * FROM secret")
		deny(clients[i], "UPDATE items_"+sqlAuthAdapters[(i+1)%3]+" SET version=999 WHERE id=99")
		deny(audit, "UPDATE items_"+sqlAuthAdapters[i]+" SET version=999 WHERE id=99")
	}
	for round := 0; round < rounds; round++ {
		pin, err := clients[0].BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		var old int64
		if err := pin.QueryRowContext(ctx, "SELECT version FROM items_native WHERE id=1").Scan(&old); err != nil {
			pin.Rollback()
			t.Fatal(err)
		}
		phase, err := runSQLAuthPhase(ctx, f, clients, &model, duration, [3]bool{true, true, true}, pin, old)
		phase.Name = fmt.Sprintf("round%d/allowed", round+1)
		r.Phases = append(r.Phases, phase)
		if err != nil {
			t.Fatal(err)
		}
		check()
		// Pin/stage before revoke, with writers stopped at the phase barrier.
		tx, err := clients[1].BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		if _, err := tx.ExecContext(ctx, "UPDATE items_pg SET version=version+1 WHERE id=1"); err != nil {
			t.Fatal(err)
		}
		for _, adapter := range []string{"pg", "http"} {
			exec("REVOKE UPDATE ON TABLE items_" + adapter + " FROM ROLE writer_" + adapter)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal("pinned commit after revoke", err)
		}
		model[1][1]++
		r.PinnedCommits++
		phase, err = runSQLAuthPhase(ctx, f, clients, &model, duration, [3]bool{true, false, false}, nil, 0)
		phase.Name = fmt.Sprintf("round%d/revoked", round+1)
		r.Phases = append(r.Phases, phase)
		if err != nil {
			t.Fatal(err)
		}
		check()
		for _, adapter := range []string{"pg", "http"} {
			exec("GRANT UPDATE ON TABLE items_" + adapter + " TO ROLE writer_" + adapter)
		}
		exec("REVOKE ROLE writer_native ON DATABASE shop FROM writer_native")
		if _, err := clients[0].ExecContext(ctx, "UPDATE items_native SET version=version+1 WHERE id=1"); err != nil {
			t.Fatal("captured native mapping lost", err)
		}
		model[0][1]++
		for _, adapter := range sqlAuthAdapters {
			fresh := f.open(t, adapter, "writer_native")
			deny(fresh, "SELECT * FROM items_native")
			deny(fresh, "UPDATE items_native SET version=999 WHERE id=99")
			if err := fresh.Close(); err != nil {
				t.Fatal(err)
			}
			r.MappingChecks++
		}
		exec("GRANT ROLE writer_native ON DATABASE shop TO writer_native")
		// Retire every logical pool. New pools must use the restored mappings.
		for i, adapter := range sqlAuthAdapters {
			clients[i].Close()
			clients[i] = f.open(t, adapter, "writer_"+adapter)
		}
		phase, err = runSQLAuthPhase(ctx, f, clients, &model, duration, [3]bool{true, true, true}, nil, 0)
		phase.Name = fmt.Sprintf("round%d/restored", round+1)
		r.Phases = append(r.Phases, phase)
		if err != nil {
			t.Fatal(err)
		}
		check()
	}
	r.ExactData, r.ExactIndex = true, true
	checkpoints := uint64(0)
	for _, phase := range r.Phases {
		checkpoints += phase.Checkpoints
	}
	if checkpoints == 0 {
		t.Fatal("no automatic checkpoint observed during workload; coverage missing")
	}
	// Leave a revoke durable so recovery checks an actual denied state too.
	exec("REVOKE UPDATE ON TABLE items_http FROM ROLE writer_http")
	r.BeforeClose = f.owner.Stats()
	if err := f.close(); err != nil {
		t.Fatal(err)
	}
	r.AfterClose = f.owner.Stats()
	if err := sqlAuthClosed(r.AfterClose); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "shop", "data.kitdb")
	kernel, err := kitdb.OpenWithOptions(path, kitdb.OpenOptions{VerifyOnOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	verifyErr := kernel.Verify()
	closeErr := kernel.Close()
	if err := errors.Join(verifyErr, closeErr); err != nil {
		t.Fatal(err)
	}
	r.OfflineVerify = true
	f = startSQLAuth(t, ctx, root)
	audit = f.open(t, "native", "auditor")
	for i, adapter := range sqlAuthAdapters {
		clients[i] = f.open(t, adapter, "writer_"+adapter)
		var roleID string
		if err := f.admin.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='writer_"+adapter+"'").Scan(&roleID); err != nil || roleID != roleIDs[i] {
			t.Fatal("role identity changed", roleID, err)
		}
	}
	check()
	deny(clients[2], "UPDATE items_http SET version=999 WHERE id=99")
	r.Reopen = true
	if err := f.close(); err != nil {
		t.Fatal(err)
	}
	r.ReopenedClose = f.owner.Stats()
	if err := sqlAuthClosed(r.ReopenedClose); err != nil {
		t.Fatal(err)
	}
	if r.PinnedCommits != rounds || r.MappingChecks != 3*rounds || len(r.Phases) != 3*rounds {
		t.Fatal("missing authorization lifecycle coverage")
	}
	r.Closed = true
}

type sqlAuthFixture struct {
	ctx       context.Context
	owner     *relational.PostgresNode
	admin     *sql.DB
	address   string
	http      *httptest.Server
	transport *http.Transport
	active    atomic.Int64
	pools     []*sql.DB
	cancel    context.CancelFunc
	done      chan error
	once      sync.Once
	err       error
}

func seedSQLAuth(t *testing.T, ctx context.Context, root, payload string) {
	t.Helper()
	if _, err := managed.Init(root); err != nil {
		t.Fatal(err)
	}
	e, err := relational.Open(filepath.Join(root, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	for _, table := range append([]string{"secret"}, "items_native", "items_pg", "items_http") {
		if _, err := e.Execute(ctx, "CREATE TABLE "+table+"(id BIGINT PRIMARY KEY,version BIGINT NOT NULL,payload TEXT NOT NULL)"); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Execute(ctx, "CREATE INDEX "+table+"_version ON "+table+"(version)"); err != nil {
			t.Fatal(err)
		}
		for id := 1; id <= sqlAuthRows; id++ {
			if _, err := e.Execute(ctx, "INSERT INTO "+table+" VALUES($1,0,$2)", id, payload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err := managed.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if _, err := catalog.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if err := catalog.EnableAuth("admin", sqlAuthPassword); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"writer_native", "writer_pg", "writer_http", "auditor"} {
		if err := catalog.SetUser(managed.UserInfo{Name: name, Grants: []managed.DatabaseGrant{{Database: "shop", Permissions: []string{"connect"}}}}, sqlAuthPassword); err != nil {
			t.Fatal(err)
		}
	}
}

func startSQLAuth(t *testing.T, ctx context.Context, root string) *sqlAuthFixture {
	t.Helper()
	owner, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{Root: root, MaximumSessions: 16, ManagerLimits: node.Limits{MaxOpenDatabases: 2, DefaultPageCacheBytes: 1 << 20, MaxPageCacheBytes: 2 << 20}, Relational: relational.Options{MaximumResultRows: 32, Kernel: kitdb.OpenOptions{VerifyOnOpen: true, SnapshotMemoryBytes: 1 << 20, TransactionMemoryBytes: 8 << 20}}})
	if err != nil {
		t.Fatal(err)
	}
	f := &sqlAuthFixture{ctx: ctx, owner: owner}
	t.Cleanup(func() {
		if err := f.close(); err != nil {
			t.Error(err)
		}
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f.address = ln.Addr().String()
	serverCtx, cancel := context.WithCancel(ctx)
	f.cancel, f.done = cancel, make(chan error, 1)
	go func() {
		f.done <- owner.ServePostgres(serverCtx, ln, relational.PostgresServerOptions{MaxConnections: 16, QueryTimeout: 15 * time.Second})
	}()
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: kitsql.OpenNodeDatabase(owner), AllowInsecureLocal: true, MaximumRows: 32, MaximumConcurrent: 4, QueryTimeout: 15 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	f.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.active.Add(1)
		defer f.active.Add(-1)
		handler.ServeHTTP(w, r)
	}))
	f.transport = &http.Transport{MaxConnsPerHost: 2, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, ResponseHeaderTimeout: 15 * time.Second}
	f.admin = f.open(t, "native", "admin")
	return f
}

func (f *sqlAuthFixture) open(t *testing.T, adapter, user string) *sql.DB {
	t.Helper()
	var db *sql.DB
	var err error
	u := url.URL{Scheme: "postgres", Host: f.address, Path: "/shop", User: url.UserPassword(user, sqlAuthPassword), RawQuery: "sslmode=disable&connect_timeout=15"}
	switch adapter {
	case "native":
		db, err = f.owner.OpenAuthenticatedDatabase(f.ctx, user, sqlAuthPassword, "shop")
	case "pg":
		db, err = sql.Open("postgres", u.String())
	case "http":
		u.Scheme = "kitsql"
		u.Host = strings.TrimPrefix(f.http.URL, "http://")
		u.RawQuery = "sslmode=disable"
		db, err = kitsql.OpenWithClient(u.String(), &http.Client{Transport: f.transport, Timeout: 20 * time.Second})
	}
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(2)
	db.SetMaxIdleConns(2)
	f.pools = append(f.pools, db)
	if err := db.PingContext(f.ctx); err != nil {
		t.Fatal(adapter, user, err)
	}
	return db
}

func (f *sqlAuthFixture) close() error {
	f.once.Do(func() {
		for _, db := range f.pools {
			f.err = errors.Join(f.err, db.Close())
			if db.Stats().InUse != 0 {
				f.err = errors.Join(f.err, errors.New("native/client pool still in use"))
			}
		}
		if f.http != nil {
			f.http.Close()
		}
		if f.transport != nil {
			f.transport.CloseIdleConnections()
		}
		if f.cancel != nil {
			f.cancel()
			select {
			case err := <-f.done:
				f.err = errors.Join(f.err, err)
			case <-time.After(10 * time.Second):
				f.err = errors.Join(f.err, errors.New("pgwire server did not drain"))
			}
		}
		if f.active.Load() != 0 {
			f.err = errors.Join(f.err, errors.New("HTTP requests did not drain"))
		}
		f.err = errors.Join(f.err, f.owner.Close())
	})
	return f.err
}

func sqlAuthClosed(s relational.PostgresNodeStats) error {
	m := s.Manager
	if !s.Closed || s.RegisteredSessions != 0 || s.Sessions != 0 || s.ManagedEngines != 0 || s.OpeningEngines != 0 || s.ProjectionActiveLeases != 0 || s.ProjectionSearchFileHandles != 0 || !m.Closed || m.OpenDatabases != 0 || m.ActiveLeases != 0 || m.OpeningDatabases != 0 || m.ClosingDatabases != 0 || m.WaitingAcquisitions != 0 || m.ReservedPageCacheBytes != 0 || m.QueuedMaintenance != 0 || m.RunningMaintenance != 0 {
		return fmt.Errorf("node retained resources: %+v", s)
	}
	return nil
}

func checkSQLAuth(ctx context.Context, db *sql.DB, table string, model []int64, payload string) error {
	rows, err := db.QueryContext(ctx, "SELECT id,version,payload FROM "+table)
	if err != nil {
		return err
	}
	var got []sqlCanaryRow
	for rows.Next() {
		var row sqlCanaryRow
		if err := rows.Scan(&row.ID, &row.Version, &row.Payload); err != nil {
			rows.Close()
			return err
		}
		got = append(got, row)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	if err := sqlCanaryCheckRows(got, model, payload); err != nil {
		return err
	}
	versions := map[int64]bool{}
	for _, v := range model[1:] {
		versions[v] = true
	}
	for v := range versions {
		rows, err := db.QueryContext(ctx, "SELECT id FROM "+table+" WHERE version=$1", v)
		if err != nil {
			return err
		}
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			ids = append(ids, id)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if err := sqlCanaryCheckIndex(ids, v, model); err != nil {
			return err
		}
	}
	return nil
}

func runSQLAuthPhase(ctx context.Context, f *sqlAuthFixture, clients [3]*sql.DB, model *[3][]int64, duration time.Duration, allowed [3]bool, pin *sql.Tx, old int64) (sqlAuthPhase, error) {
	r := sqlAuthPhase{RequestedMS: duration.Milliseconds(), WriteAllowed: allowed}
	var acks, reads, denied, conflicts, busy [3]atomic.Uint64
	var pinned atomic.Bool
	stopped := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(stopped) }) }
	failures := make(chan error, 4)
	var group sync.WaitGroup
	start := time.Now()
	base := f.owner.Stats().Manager.CheckpointCompletions
	for i, db := range clients {
		group.Add(1)
		go func(i int, db *sql.DB) {
			defer group.Done()
			for op := 0; ; op++ {
				select {
				case <-stopped:
					return
				case <-ctx.Done():
					failures <- ctx.Err()
					stop()
					return
				default:
				}
				id := 1 + op%sqlAuthRows
				result, err := db.ExecContext(ctx, "UPDATE items_"+sqlAuthAdapters[i]+" SET version=version+1 WHERE id=$1", id)
				if err == nil {
					if !allowed[i] {
						failures <- fmt.Errorf("adapter %d bypassed revoke", i)
						stop()
						return
					}
					n, e := result.RowsAffected()
					if e != nil || n != 1 {
						failures <- fmt.Errorf("affected %d: %v", n, e)
						stop()
						return
					}
					model[i][id]++
					acks[i].Add(1)
				} else if !allowed[i] && sqlAuthDenied(err) {
					denied[i].Add(1)
				} else if kind := sqlCanaryRejection(err); kind == "conflict" {
					conflicts[i].Add(1)
				} else if kind == "busy" {
					busy[i].Add(1)
				} else {
					failures <- fmt.Errorf("adapter %d unexpected write outcome: %w", i, err)
					stop()
					return
				}
				var version int64
				if err := db.QueryRowContext(ctx, "SELECT version FROM items_"+sqlAuthAdapters[i]+" WHERE id=$1", id).Scan(&version); err != nil {
					if sqlCanaryRejection(err) == "busy" {
						busy[i].Add(1)
						continue
					}
					failures <- fmt.Errorf("adapter %d read: %w", i, err)
					stop()
					return
				}
				if version != model[i][id] {
					failures <- fmt.Errorf("adapter %d ACK model mismatch", i)
					stop()
					return
				}
				reads[i].Add(1)
				select {
				case <-stopped:
					return
				case <-ctx.Done():
					failures <- ctx.Err()
					stop()
					return
				case <-time.After(25 * time.Millisecond):
				}
			}
		}(i, db)
	}
	if pin != nil {
		group.Add(1)
		go func() {
			defer group.Done()
			defer pin.Rollback()
			deadline := time.NewTimer(15 * time.Second)
			defer deadline.Stop()
			for acks[0].Load() == 0 {
				select {
				case <-stopped:
					failures <- errors.New("pinned read missing write progress")
					return
				case <-deadline.C:
					failures <- errors.New("pinned read wait expired")
					stop()
					return
				case <-time.After(10 * time.Millisecond):
				}
			}
			time.Sleep(150 * time.Millisecond)
			var version int64
			if err := pin.QueryRowContext(ctx, "SELECT version FROM items_native WHERE id=1").Scan(&version); err != nil || version != old {
				failures <- fmt.Errorf("pinned snapshot mismatch %d/%d: %v", version, old, err)
				stop()
				return
			}
			pinned.Store(true)
		}()
	}
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	var phaseErr error
	progress := [3]sqlCanaryProgress{{last: start}, {last: start}, {last: start}}
	lastTick, nextSample := start, start
monitor:
	for {
		select {
		case <-stopped:
			break monitor
		case <-ctx.Done():
			phaseErr = ctx.Err()
			break monitor
		case <-deadline.C:
			break monitor
		case <-tick.C:
			now := time.Now()
			if now.Sub(lastTick) > 3*time.Second {
				phaseErr = errors.New("authorization monitor gap exceeds 3 seconds")
				break monitor
			}
			lastTick = now
			s := f.owner.Stats()
			if s.Closed || s.Manager.Closed || s.Manager.CheckpointCompletions < base {
				phaseErr = errors.New("authorization owner closed or checkpoint counter regressed")
				break monitor
			}
			if s.RegisteredSessions > 16 || s.Manager.OpenDatabases > 2 || s.Manager.ReservedPageCacheBytes > 2<<20 {
				phaseErr = errors.New("authorization owner exceeded configured resource ceiling")
				break monitor
			}
			sample := sqlAuthSample{ElapsedMS: now.Sub(start).Milliseconds(), Sessions: s.RegisteredSessions, Leases: s.Manager.ActiveLeases, Checkpoints: s.Manager.CheckpointCompletions - base}
			for i := range acks {
				sample.ACKs[i] = acks[i].Load()
				sample.Denied[i] = denied[i].Load()
				count := sample.ACKs[i]
				if !allowed[i] {
					count = sample.Denied[i]
				}
				if err := progress[i].observe(now, count, 30*time.Second); err != nil {
					phaseErr = fmt.Errorf("adapter %d progress: %w", i, err)
					break monitor
				}
			}
			if sample.ACKs[0]+sample.ACKs[1]+sample.ACKs[2] > 0 {
				r.Checkpoints = max(r.Checkpoints, sample.Checkpoints)
			}
			if !now.Before(nextSample) {
				if len(r.Samples) >= int(duration/time.Second)+1 {
					phaseErr = errors.New("authorization sample bound exceeded")
					break monitor
				}
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				sample.HeapBytes = memory.HeapAlloc
				r.Samples = append(r.Samples, sample)
				nextSample = now.Add(time.Second)
			}
		}
	}
	if time.Since(lastTick) > 3*time.Second {
		phaseErr = errors.Join(phaseErr, errors.New("authorization final monitoring gap exceeds 3 seconds"))
	}
	r.WorkloadMS = time.Since(start).Milliseconds()
	stop()
	drain := time.Now()
	group.Wait()
	r.DrainMS = time.Since(drain).Milliseconds()
	close(failures)
	for err := range failures {
		phaseErr = errors.Join(phaseErr, err)
	}
	for i := range acks {
		r.ACKs[i] = acks[i].Load()
		r.Reads[i] = reads[i].Load()
		r.Denied[i] = denied[i].Load()
		r.Conflicts[i] = conflicts[i].Load()
		r.Busy[i] = busy[i].Load()
		if r.Reads[i] == 0 || allowed[i] && r.ACKs[i] == 0 || !allowed[i] && (r.Denied[i] == 0 || r.ACKs[i] != 0) {
			phaseErr = errors.Join(phaseErr, fmt.Errorf("adapter %d missing required progress", i))
		}
	}
	r.PinnedRead = pinned.Load()
	if pin != nil && !r.PinnedRead {
		phaseErr = errors.Join(phaseErr, errors.New("pinned reader did not complete"))
	}
	if r.WorkloadMS < r.RequestedMS {
		phaseErr = errors.Join(phaseErr, errors.New("authorization workload ended early"))
	}
	r.Success = phaseErr == nil
	return r, phaseErr
}
