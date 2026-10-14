package relational

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRestrictedWriteCampaignSeeded(t *testing.T) {
	const seed int64 = 20261014
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	node := sessionTestNode(t, 32)
	openNative := authorizationClientFactory(t, node, "native")
	admin := openNative("admin")
	run := func(q string) {
		t.Helper()
		if _, err := admin.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	run("REVOKE READ ON DATABASE shop FROM reader")
	for _, q := range []string{
		"CREATE TABLE totals(id BIGINT PRIMARY KEY,label TEXT UNIQUE,n BIGINT)",
		"CREATE TABLE source_rows(id BIGINT PRIMARY KEY,n BIGINT)",
		"CREATE TABLE copied(id BIGINT PRIMARY KEY,n BIGINT)",
		"CREATE TABLE discarded(id BIGINT PRIMARY KEY,n BIGINT)",
		"INSERT INTO totals VALUES(1,'primary',0),(2,'unique',0)",
		"CREATE ROLE campaign", "GRANT ROLE campaign ON DATABASE shop TO reader",
	} {
		run(q)
	}
	for _, table := range []string{"totals", "copied", "discarded"} {
		for _, p := range []string{"SELECT", "INSERT"} {
			run("GRANT " + p + " ON TABLE " + table + " TO ROLE campaign")
		}
	}
	run("GRANT UPDATE ON TABLE totals TO ROLE campaign")
	run("GRANT SELECT ON TABLE source_rows TO ROLE campaign")
	for i := 0; i < 8; i++ {
		if _, err := admin.ExecContext(ctx, "INSERT INTO source_rows VALUES($1,0)", i); err != nil {
			t.Fatal(err)
		}
	}
	var roleID string
	if err := admin.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='campaign'").Scan(&roleID); err != nil {
		t.Fatal(err)
	}
	address, stop := portableRoleListener(t, node)
	openPG := func(user string) *sql.DB {
		u := url.URL{Scheme: "postgres", Host: address, Path: "/shop", User: url.UserPassword(user, "session-test-password"), RawQuery: "sslmode=disable&connect_timeout=15"}
		db, err := sql.Open("postgres", u.String())
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		t.Cleanup(func() { db.Close() })
		return db
	}
	clients := []*sql.DB{openNative("reader"), openPG("reader"), openNative("reader"), openPG("reader")}
	for _, db := range clients {
		if err := db.PingContext(ctx); err != nil {
			t.Fatal(err)
		}
	}
	type operation struct{ bucket, delta int }
	random := rand.New(rand.NewSource(seed))
	workload := make([][]operation, len(clients))
	var expected [2]int64
	for w := range clients {
		for i := 0; i < 12; i++ {
			op := operation{random.Intn(2), 1 + random.Intn(5)}
			workload[w] = append(workload[w], op)
			expected[op.bucket] += int64(op.delta)
		}
	}
	var acknowledged, conflicts atomic.Int64
	var wg sync.WaitGroup
	failures := make(chan error, len(clients))
	start := make(chan struct{})
	for w, db := range clients {
		wg.Add(1)
		go func(w int, db *sql.DB) {
			defer wg.Done()
			<-start
			for i, op := range workload[w] {
				q := "INSERT INTO totals VALUES(1,'primary',$1) ON CONFLICT(id) DO UPDATE SET n=totals.n+excluded.n"
				args := []any{op.delta}
				if op.bucket == 1 {
					q = "INSERT INTO totals VALUES($1,'unique',$2) ON CONFLICT(label) DO UPDATE SET n=totals.n+excluded.n"
					args = []any{100 + w*100 + i, op.delta}
				}
				success := false
				for attempt := 0; attempt < 64; attempt++ {
					_, err := db.ExecContext(ctx, q, args...)
					if err == nil {
						acknowledged.Add(1)
						success = true
						break
					}
					if !errors.Is(err, ErrTransactionConflict) && authorizationState(err) != "40001" {
						failures <- fmt.Errorf("worker %d operation %d: %w", w, i, err)
						return
					}
					conflicts.Add(1)
					select {
					case <-ctx.Done():
						failures <- ctx.Err()
						return
					case <-time.After(time.Duration(1+attempt%4) * time.Millisecond):
					}
				}
				if !success {
					failures <- fmt.Errorf("worker %d operation %d exhausted bounded conflict retries", w, i)
					return
				}
			}
		}(w, db)
	}
	close(start)
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if acknowledged.Load() != 48 {
		t.Fatal("missing commit acknowledgements", acknowledged.Load())
	}
	check := func(db *sql.DB, q string, want int64) {
		t.Helper()
		var got int64
		if err := db.QueryRowContext(ctx, q).Scan(&got); err != nil || got != want {
			t.Fatalf("%s: got %d want %d, %v", q, got, want, err)
		}
	}
	verifyTotals := func(db *sql.DB) {
		check(db, "SELECT COUNT(*) FROM totals", 2)
		check(db, "SELECT n FROM totals WHERE id=1", expected[0])
		check(db, "SELECT n FROM totals WHERE id=2", expected[1])
		check(db, "SELECT id FROM totals WHERE label='unique'", 2)
	}
	verifyTotals(admin)
	t.Logf("seed=%d workers=4 operations=48 acknowledged=%d conflicts=%d ledger=%v", seed, acknowledged.Load(), conflicts.Load(), expected)

	// Source commit occurs after snapshot acquisition but before source reads.
	tx, err := clients[0].BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	changed := make(chan error, 1)
	go func() { _, err := admin.ExecContext(ctx, "UPDATE source_rows SET n=n+1"); changed <- err }()
	if err = <-changed; err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO copied SELECT id,n FROM source_rows"); err != nil {
		t.Fatal(err)
	}
	copySum := int64(0)
	err = tx.Commit()
	sourceConflict := err != nil
	if err != nil {
		if !errors.Is(err, ErrTransactionConflict) && authorizationState(err) != "40001" {
			t.Fatal("source change outcome", err)
		}
		check(admin, "SELECT COUNT(*) FROM copied", 0)
		if _, err = clients[0].ExecContext(ctx, "INSERT INTO copied SELECT id,n FROM source_rows"); err != nil {
			t.Fatal(err)
		}
		copySum = 8
	}
	check(admin, "SELECT COUNT(*) FROM copied", 8)
	check(admin, "SELECT SUM(n) FROM copied", copySum)
	t.Logf("source-copy conflict=%v snapshot-sum=%d", sourceConflict, copySum)

	tx, err = clients[1].BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, "INSERT INTO totals VALUES(1,'primary',7) ON CONFLICT(id) DO UPDATE SET n=totals.n+excluded.n"); err != nil {
		t.Fatal(err)
	}
	run("REVOKE UPDATE ON TABLE totals FROM ROLE campaign")
	denied := make(chan error, 1)
	go func() {
		_, err := clients[2].ExecContext(ctx, "INSERT INTO totals VALUES(1,'primary',999) ON CONFLICT(id) DO UPDATE SET n=excluded.n")
		denied <- err
	}()
	if err = <-denied; authorizationState(err) != "42501" {
		t.Fatal("fresh write after revoke", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal("pinned policy commit", err)
	}
	expected[0] += 7
	verifyTotals(admin)
	run("GRANT UPDATE ON TABLE totals TO ROLE campaign")

	for index, mode := range []string{"cancel", "kill"} {
		batchCtx, batchCancel := context.WithCancel(ctx)
		staged := make(chan int64, 1)
		proceed := make(chan struct{})
		finished := make(chan error, 1)
		go func(db *sql.DB) {
			var result error
			defer func() { finished <- result }()
			conn, err := db.Conn(batchCtx)
			if err != nil {
				result = err
				return
			}
			defer conn.Close()
			batch, err := conn.BeginTx(batchCtx, nil)
			if err != nil {
				result = err
				return
			}
			defer batch.Rollback()
			var id int64
			if err = batch.QueryRowContext(batchCtx, "SELECT session_id FROM kitdb_catalog.sessions WHERE transaction_state='transaction'").Scan(&id); err != nil {
				result = err
				return
			}
			for _, q := range []string{"INSERT INTO totals VALUES(1,'primary',10000) ON CONFLICT(id) DO UPDATE SET n=totals.n+excluded.n", "INSERT INTO discarded SELECT id,n FROM source_rows"} {
				if _, err = batch.ExecContext(batchCtx, q); err != nil {
					result = err
					return
				}
			}
			staged <- id
			select {
			case <-proceed:
			case <-batchCtx.Done():
			}
			result = batch.Commit()
		}(clients[index])
		select {
		case id := <-staged:
			if mode == "cancel" {
				batchCancel()
			} else {
				if _, err = admin.ExecContext(ctx, fmt.Sprintf("KILL SESSION %d", id)); err != nil {
					batchCancel()
					t.Fatal(err)
				}
				waitSessionGone(t, node, id)
			}
			close(proceed)
		case err := <-finished:
			batchCancel()
			t.Fatal("batch failed before cancellation barrier", err)
		case <-ctx.Done():
			batchCancel()
			t.Fatal(ctx.Err())
		}
		if err = <-finished; err == nil {
			batchCancel()
			t.Fatal(mode, "batch committed after termination")
		}
		batchCancel()
		verifyTotals(admin)
		check(admin, "SELECT COUNT(*) FROM discarded", 0)
	}
	for _, db := range clients {
		db.Close()
	}
	admin.Close()
	stop()
	if err = node.Close(); err != nil {
		t.Fatal(err)
	}
	node.mu.Lock()
	remaining := len(node.sessionRegistry)
	node.mu.Unlock()
	stats := node.manager.Stats()
	if remaining != 0 || stats.ActiveLeases != 0 || stats.OpenDatabases != 0 || stats.OpeningDatabases != 0 || stats.ClosingDatabases != 0 || stats.WaitingAcquisitions != 0 || stats.ReservedPageCacheBytes != 0 || stats.QueuedMaintenance != 0 || stats.RunningMaintenance != 0 || !stats.Closed {
		t.Fatalf("drain sessions=%d stats=%+v", remaining, stats)
	}
	reopened, err := OpenPostgresNode(PostgresNodeOptions{Root: node.root})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	verifyTotals(recovered)
	check(recovered, "SELECT COUNT(*) FROM copied", 8)
	check(recovered, "SELECT SUM(n) FROM copied", copySum)
	check(recovered, "SELECT COUNT(*) FROM discarded", 0)
	var recoveredRole string
	if err = recovered.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='campaign'").Scan(&recoveredRole); err != nil || recoveredRole != roleID {
		t.Fatal("role recovery", recoveredRole, err)
	}
	check(recovered, "SELECT COUNT(*) FROM kitdb_catalog.table_grants WHERE table_name='totals' AND can_select=true AND can_insert=true AND can_update=true", 1)
	recovered.Close()
	if err = reopened.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := reopened.manager.Stats(); stats.ActiveLeases != 0 || stats.OpenDatabases != 0 || stats.OpeningDatabases != 0 || stats.ClosingDatabases != 0 || stats.WaitingAcquisitions != 0 || stats.ReservedPageCacheBytes != 0 || stats.QueuedMaintenance != 0 || stats.RunningMaintenance != 0 || !stats.Closed {
		t.Fatalf("reopened drain: %+v", stats)
	}
	t.Logf("restart verified ledger=%v copied=%d discarded=0; owners drained", expected, copySum)
}
