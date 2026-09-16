package main

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lib/pq"
)

// Development opt-in only. The manifest-qualified journey calls the same drill.
func TestKitDBApplicationNativeJourney(t *testing.T) {
	dir := os.Getenv("KITDB_APPLICATION_BINARY_DIRECTORY")
	if dir == "" {
		t.Skip("set KITDB_APPLICATION_BINARY_DIRECTORY to standalone development binaries")
	}
	exerciseApplicationBinaries(t, nativeApplicationBinary(dir))
}

func nativeApplicationBinary(dir string) func(string) string {
	return func(name string) string {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		return filepath.Join(dir, name)
	}
}

type applicationServer struct {
	client  *sql.DB
	command *exec.Cmd
	stopped bool
}

func startApplicationServer(t *testing.T, ctx context.Context, server, database string) *applicationServer {
	t.Helper()
	command := exec.CommandContext(ctx, server, "-file", database, "-database", "application",
		"-listen", "127.0.0.1:0", "-retain-history", "-verify-on-open",
		"-max-connections", "8", "-max-concurrent-queries", "4",
		"-max-concurrent-queries-per-database", "4", "-max-queued-queries", "8",
		"-max-queued-queries-per-database", "8", "-query-timeout", "10s")
	command.Dir = filepath.Dir(database)
	command.Env = buildEnvironment(os.Environ(), map[string]string{"KITDB_TOKEN": "application-fixture"})
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	process := &applicationServer{command: command}
	t.Cleanup(process.stop)
	ready := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, _ := reader.ReadString('\n')
		ready <- line
		_, _ = io.Copy(io.Discard, reader)
	}()
	var address string
	select {
	case line := <-ready:
		if _, err := fmt.Sscanf(line, "KitDB standalone PostgreSQL profile listening on %s", &address); err != nil {
			t.Fatalf("application server readiness: %q: %v", line, err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(10 * time.Second):
		t.Fatal("application server readiness timeout")
	}
	endpoint := (&url.URL{Scheme: "postgres", User: url.UserPassword("kitdb", "application-fixture"),
		Host: address, Path: "/application", RawQuery: "sslmode=disable&connect_timeout=5"}).String()
	process.client, err = sql.Open("postgres", endpoint)
	if err != nil {
		t.Fatal(err)
	}
	process.client.SetMaxOpenConns(6)
	process.client.SetMaxIdleConns(6)
	if err := process.client.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return process
}

// Kill before closing clients: an open transaction must not get a clean rollback
// from server shutdown. This is process-crash evidence, not a power-loss test.
func (server *applicationServer) stop() {
	if server.stopped {
		return
	}
	server.stopped = true
	_ = server.command.Process.Kill()
	_ = server.command.Wait()
	if server.client != nil {
		_ = server.client.Close()
	}
}

const applicationOrders = 80

func applicationAmount(id int) int64 {
	amount := int64((id%2 + 1) * 10)
	if id%4 == 0 {
		amount++
	}
	return amount
}

func applicationRetains(id int) bool { return id%5 != 0 && id%7 != 0 }

func applicationSnapshot(ctx context.Context, client *sql.DB) error {
	var committed, total, matched int64
	var actual sql.NullInt64
	err := client.QueryRowContext(ctx, `SELECT c.orders, c.total, COUNT(o.id), SUM(o.amount)
		FROM counters c LEFT JOIN orders o ON o.counter_id = c.id GROUP BY c.orders, c.total`).
		Scan(&committed, &total, &matched, &actual)
	if err != nil {
		return err
	}
	if committed != matched || total != actual.Int64 || (matched > 0 && !actual.Valid) {
		return fmt.Errorf("torn application snapshot: counter=(%d,%d) orders=(%d,%+v)", committed, total, matched, actual)
	}
	return nil
}

func applicationSearch(ctx context.Context, client *sql.DB) error {
	var count, total int64
	err := client.QueryRowContext(ctx, `SELECT COUNT(*), SUM(price) FROM products WHERE * SEARCH 'blue widget'`).Scan(&count, &total)
	if err != nil {
		return err
	}
	if count != 2 || total != 30 {
		return fmt.Errorf("SEARCH count=%d sum=%d, want 2/30", count, total)
	}
	return nil
}

func applicationJoin(ctx context.Context, client *sql.DB) error {
	rows, err := client.QueryContext(ctx, `SELECT o.id, o.amount, p.price FROM orders o
		JOIN products p ON o.merchant = p.merchant AND o.product_id = p.id ORDER BY o.id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	previous := 0
	for rows.Next() {
		var id int
		var amount, price int64
		if err := rows.Scan(&id, &amount, &price); err != nil {
			return err
		}
		if id <= previous || id > applicationOrders || !applicationRetains(id) || amount != applicationAmount(id) || price != int64((id%2+1)*10) {
			return fmt.Errorf("unexpected joined order: id=%d amount=%d price=%d", id, amount, price)
		}
		previous = id
	}
	return rows.Err()
}

func verifyApplication(ctx context.Context, client *sql.DB) error {
	rows, err := client.QueryContext(ctx, `SELECT id, merchant, product_id, counter_id, amount FROM orders ORDER BY id`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var count, total int64
	for id := 1; id <= applicationOrders; id++ {
		if !applicationRetains(id) {
			continue
		}
		if !rows.Next() {
			return fmt.Errorf("missing order %d: %v", id, rows.Err())
		}
		var actual, product, counter int
		var amount int64
		var merchant string
		if err := rows.Scan(&actual, &merchant, &product, &counter, &amount); err != nil {
			return err
		}
		if actual != id || merchant != "shop" || product != id%2+1 || counter != 1 || amount != applicationAmount(id) {
			return fmt.Errorf("order %d disagrees with independent model: %d/%s/%d/%d/%d", id, actual, merchant, product, counter, amount)
		}
		count++
		total += amount
	}
	if rows.Next() {
		return fmt.Errorf("unexpected additional order")
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	var actualCount, actualTotal int64
	if err := client.QueryRowContext(ctx, `SELECT orders, total FROM counters WHERE id = 1`).Scan(&actualCount, &actualTotal); err != nil {
		return err
	}
	if actualCount != count || actualTotal != total {
		return fmt.Errorf("counter=%d/%d, independent model=%d/%d", actualCount, actualTotal, count, total)
	}
	return errors.Join(applicationSnapshot(ctx, client), applicationJoin(ctx, client), applicationSearch(ctx, client))
}

func writeApplicationOrder(ctx context.Context, client *sql.DB, id int, prepared func() error) error {
	transaction, err := client.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if _, err := transaction.ExecContext(ctx, `INSERT INTO orders (id, merchant, product_id, counter_id, amount)
		VALUES ($1, 'shop', $2, 1, $3)`, id, id%2+1, (id%2+1)*10); err != nil {
		return err
	}
	if id%4 == 0 {
		if _, err := transaction.ExecContext(ctx, `UPDATE orders SET amount = amount + 1 WHERE id = $1`, id); err != nil {
			return err
		}
	}
	if id%7 == 0 {
		if _, err := transaction.ExecContext(ctx, `DELETE FROM orders WHERE id = $1`, id); err != nil {
			return err
		}
	} else if _, err := transaction.ExecContext(ctx, `UPDATE counters SET orders = orders + 1, total = total + $1 WHERE id = 1`, applicationAmount(id)); err != nil {
		return err
	}
	if err := prepared(); err != nil {
		return err
	}
	if id%5 == 0 {
		return transaction.Rollback()
	}
	return transaction.Commit()
}

func exerciseApplicationConcurrency(ctx context.Context, client *sql.DB) ([3]int, int, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	var writers sync.WaitGroup
	failures := make(chan error, 5)
	fail := func(err error) { failures <- err; cancel() }
	prepared := make(chan struct{}, 2)
	firstReads := make(chan struct{}, 3)
	release := make(chan struct{})
	done := make(chan struct{})
	for worker := 0; worker < 2; worker++ {
		workers.Add(1)
		writers.Add(1)
		go func(worker int) {
			defer workers.Done()
			defer writers.Done()
			var first sync.Once
			for id := worker + 1; id <= applicationOrders; id += 2 {
				for attempt := 0; ; attempt++ {
					err := writeApplicationOrder(ctx, client, id, func() error {
						first.Do(func() { prepared <- struct{}{} })
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					})
					if err == nil {
						break
					}
					// Retry only an explicit aborted-transaction conflict, never an
					// ambiguous COMMIT transport error that might already be durable.
					var conflict *pq.Error
					if !errors.As(err, &conflict) || conflict.Code != "40001" || attempt >= 31 {
						fail(fmt.Errorf("writer order %d: %w", id, err))
						return
					}
					select {
					case <-ctx.Done():
						fail(ctx.Err())
						return
					case <-time.After(time.Duration(attempt+1) * time.Millisecond):
					}
				}
			}
		}(worker)
	}
	go func() { writers.Wait(); close(done) }()
	// Readers must observe both open, uncommitted transactions before either
	// writer is allowed to commit. Subsequent loops run freely and concurrently.
	for i := 0; i < 2; i++ {
		select {
		case <-prepared:
		case <-ctx.Done():
		}
	}
	var reads [3]int
	searchRetries := 0
	searchRead := func(ctx context.Context, client *sql.DB) error {
		for attempt := 0; ; attempt++ {
			err := applicationSearch(ctx, client)
			var transient *pq.Error
			if !errors.As(err, &transient) || transient.Code != "XX000" || attempt >= 31 ||
				(transient.Message != "kitdb: search projection could not reach a stable source boundary; retry" &&
					transient.Message != "kitdb: SEARCH source or projection changed repeatedly; retry") {
				return err
			}
			// These two existing freshness errors explicitly ask for a read retry.
			// Count them as availability evidence; never mask arbitrary SQL errors.
			searchRetries++
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * time.Millisecond):
			}
		}
	}
	for i, query := range []func(context.Context, *sql.DB) error{applicationSnapshot, applicationJoin, searchRead} {
		workers.Add(1)
		go func(i int, query func(context.Context, *sql.DB) error) {
			defer workers.Done()
			for {
				if err := query(ctx, client); err != nil {
					fail(fmt.Errorf("reader %d: %w", i, err))
					return
				}
				reads[i]++
				if reads[i] == 1 {
					firstReads <- struct{}{}
				}
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-time.After(time.Millisecond):
				}
			}
		}(i, query)
	}
	for i := 0; i < 3; i++ {
		select {
		case <-firstReads:
		case <-ctx.Done():
		}
	}
	close(release)
	workers.Wait()
	close(failures)
	var err error
	for failure := range failures {
		err = errors.Join(err, failure)
	}
	return reads, searchRetries, err
}

func exerciseApplicationBinaries(t *testing.T, binary func(string) string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root := t.TempDir()
	source := filepath.Join(root, "application.kitdb")
	server := startApplicationServer(t, ctx, binary("kitdbpg"), source)
	execute := func(statement string) {
		t.Helper()
		if _, err := server.client.ExecContext(ctx, statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	for _, statement := range []string{
		`CREATE TABLE products (merchant TEXT NOT NULL, id BIGINT NOT NULL, name TEXT NOT NULL SEARCHABLE, price BIGINT NOT NULL, PRIMARY KEY (merchant, id))`,
		`CREATE TABLE orders (id BIGINT PRIMARY KEY, merchant TEXT NOT NULL, product_id BIGINT NOT NULL, counter_id BIGINT NOT NULL, amount BIGINT NOT NULL)`,
		`CREATE INDEX orders_counter ON orders (counter_id)`,
		`CREATE TABLE counters (id BIGINT PRIMARY KEY, orders BIGINT NOT NULL, total BIGINT NOT NULL)`,
		`INSERT INTO counters (id, orders, total) VALUES (1, 0, 0)`,
		`INSERT INTO products (merchant, id, name, price) VALUES ('shop', 1, 'blue widget small', 10), ('shop', 2, 'blue widget large', 20)`,
	} {
		execute(statement)
	}
	if err := applicationSearch(ctx, server.client); err != nil {
		t.Fatal(err)
	}
	reads, searchRetries, err := exerciseApplicationConcurrency(ctx, server.client)
	if err != nil {
		t.Fatal(err)
	}
	for i, count := range reads {
		if count == 0 {
			t.Fatalf("reader %d did not run with pending writers", i)
		}
	}
	if err := verifyApplication(ctx, server.client); err != nil {
		t.Fatal(err)
	}
	pending, err := server.client.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pending.ExecContext(ctx, `INSERT INTO orders (id, merchant, product_id, counter_id, amount) VALUES (999, 'shop', 1, 1, 999)`); err != nil {
		_ = pending.Rollback()
		t.Fatal(err)
	}
	if _, err := pending.ExecContext(ctx, `UPDATE counters SET orders = orders + 1, total = total + 999 WHERE id = 1`); err != nil {
		_ = pending.Rollback()
		t.Fatal(err)
	}
	server.stop()
	_ = pending.Rollback()
	server = startApplicationServer(t, ctx, binary("kitdbpg"), source)
	if err := verifyApplication(ctx, server.client); err != nil {
		t.Fatalf("after process crash: %v", err)
	}
	recoveryTime := time.Now().UTC().Format(time.RFC3339Nano)
	server.stop()
	cli := func(args ...string) {
		t.Helper()
		if _, err := run(ctx, root, nil, binary("kitdb"), args...); err != nil {
			t.Fatal(err)
		}
	}
	anchor := filepath.Join(root, "anchor.kitdb")
	restored := filepath.Join(root, "restored.kitdb")
	cli("backup", source, anchor)
	cli("restore", anchor, restored)
	backup := startApplicationServer(t, ctx, binary("kitdbpg"), restored)
	if err := verifyApplication(ctx, backup.client); err != nil {
		t.Fatalf("independent backup restore: %v", err)
	}
	backup.stop()
	server = startApplicationServer(t, ctx, binary("kitdbpg"), source)
	execute(`UPDATE orders SET amount = 0`)
	execute(`DELETE FROM orders WHERE id = 1`)
	if err := verifyApplication(ctx, server.client); err == nil || !strings.Contains(err.Error(), "order 1 disagrees with independent model") {
		t.Fatalf("independent oracle did not identify deliberately damaged order 1: %v", err)
	}
	server.stop()
	recovered := filepath.Join(root, "recovered.kitdb")
	cli("restore-time", "--at", recoveryTime, source, recovered)
	server = startApplicationServer(t, ctx, binary("kitdbpg"), recovered)
	if err := verifyApplication(ctx, server.client); err != nil {
		t.Fatalf("point-in-time recovery: %v", err)
	}
	server.stop()
	cli("verify", recovered)
	t.Logf("native application: %d transactions, concurrent snapshot/JOIN/SEARCH reads=%v, SEARCH freshness retries=%d; independent row model, rollback/delete, hard-process restart, backup/restore and PITR passed", applicationOrders, reads, searchRetries)
}
