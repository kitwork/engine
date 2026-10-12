package kitsql_test

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/kitsql"
)

// Pause before the first row, while preserving ResponseController access.
type pausedRowWriter struct {
	http.ResponseWriter
	ctx     context.Context
	entered chan struct{}
	resume  chan struct{}
	writes  int
}

func (w *pausedRowWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *pausedRowWriter) Write(payload []byte) (int, error) {
	w.writes++
	if w.writes == 2 {
		close(w.entered)
		select {
		case <-w.resume:
		case <-w.ctx.Done():
			return 0, w.ctx.Err()
		}
	}
	return w.ResponseWriter.Write(payload)
}

func TestKitSQLNetworkSnapshotAndShutdown(t *testing.T) {
	for _, mode := range []string{"snapshot", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			native, closeNative := openKitSQLFixture(t)
			t.Cleanup(closeNative)
			if _, err := native.Exec("INSERT INTO products VALUES (2, 'mouse', 75), (3, 'screen', 200), (4, 'cable', 20)"); err != nil {
				t.Fatal(err)
			}
			entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var requests atomic.Int32
			handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: fixtureOpener(native), QueryTimeout: 5 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			remote, server := networkDatabase(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if requests.Add(1) == 1 {
					defer close(done)
					handler.ServeHTTP(&pausedRowWriter{ResponseWriter: w, ctx: r.Context(), entered: entered, resume: resume}, r)
					return
				}
				handler.ServeHTTP(w, r)
			}), true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			rows, err := remote.QueryContext(ctx, "SELECT id, name FROM products ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("first row never reached transport")
			}
			if mode == "snapshot" {
				if _, err := native.Exec("UPDATE products SET name = 'changed' WHERE id = 4"); err != nil {
					t.Fatal(err)
				}
				if _, err := native.Exec("INSERT INTO products VALUES (5, 'new', 1)"); err != nil {
					t.Fatal(err)
				}
				close(resume)
				var names []string
				for rows.Next() {
					var id int64
					var name string
					if err := rows.Scan(&id, &name); err != nil {
						t.Fatal(err)
					}
					names = append(names, name)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(names, []string{"keyboard", "mouse", "screen", "cable"}) {
					t.Fatalf("snapshot changed under concurrent writes: %v", names)
				}
				var current string
				if err := remote.QueryRow("SELECT name FROM products WHERE id = 4").Scan(&current); err != nil || current != "changed" {
					t.Fatalf("next snapshot: %q %v", current, err)
				}
			} else {
				shutdownCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
				err := server.Config.Shutdown(shutdownCtx)
				stop()
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("shutdown must wait for the active stream: %v", err)
				}
				cancel()
				_ = rows.Close()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("snapshot lease not released")
			}
			if native.Stats().InUse != 0 {
				t.Fatal("native cursor remained in use")
			}
			shutdownCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
			defer stop()
			if err := server.Config.Shutdown(shutdownCtx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func networkDatabase(t testing.TB, handler http.Handler, secure bool) (*sql.DB, *httptest.Server) {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.Config.ConnContext = func(ctx context.Context, conn net.Conn) context.Context {
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetWriteBuffer(1024)
		}
		return ctx
	}
	if secure {
		server.StartTLS()
	} else {
		server.Start()
	}
	t.Cleanup(server.Close)
	client := server.Client()
	transport := client.Transport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
		if tcp, ok := conn.(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(1024)
		}
		return conn, err
	}
	client.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	parsed, _ := url.Parse(server.URL)
	dsn := "kitsql://kitdb:secret@" + parsed.Host + "/shop"
	if !secure {
		dsn += "?sslmode=disable"
	}
	database, err := kitsql.OpenWithClient(dsn, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, server
}

func TestKitSQLNetworkStreamLifecycle(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	tx, err := native.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for id := 2; id <= 257; id++ {
		if _, err := tx.Exec("INSERT INTO products VALUES ($1, $2, $3)", id, strings.Repeat("x", 16384), id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, secure := range []bool{false, true} {
		label := "http"
		if secure {
			label = "tls"
		}
		for _, mode := range []string{"close", "cancel", "slow-reader"} {
			t.Run(label+"/"+mode, func(t *testing.T) {
				var releases atomic.Int32
				done := make(chan struct{}, 4)
				handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
					Open: func(ctx context.Context, user, password, database string) (*sql.DB, func() error, error) {
						return native, func() error { releases.Add(1); return nil }, nil
					}, AllowInsecureLocal: true, MaximumConcurrent: 1, QueryTimeout: time.Second,
				})
				if err != nil {
					t.Fatal(err)
				}
				remote, _ := networkDatabase(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer func() { done <- struct{}{} }()
					handler.ServeHTTP(w, r)
				}), secure)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				rows, err := remote.QueryContext(ctx, "SELECT name FROM products")
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				// Four MiB cannot drain into the deliberately small socket buffers.
				select {
				case <-done:
					t.Fatal("stream finished before the client consumed its rows")
				default:
				}
				switch mode {
				case "close":
					_ = rows.Close()
				case "cancel":
					cancel()
				}
				select {
				case <-done:
				// crypto/tls may spend another five seconds sending close_notify
				// after a write timeout; this is bounded transport teardown.
				case <-time.After(8 * time.Second):
					t.Fatal("network stream retained its handler")
				}
				_ = rows.Close()
				if releases.Load() != 1 || native.Stats().InUse != 0 {
					t.Fatalf("releases=%d in-use=%d", releases.Load(), native.Stats().InUse)
				}
				var price int64
				if err := remote.QueryRow("SELECT price FROM products WHERE id = 1").Scan(&price); err != nil || price != 100 {
					t.Fatalf("admission/cursor not reusable: price=%d err=%v", price, err)
				}
			})
		}
	}
}

func TestKitSQLNativeResultParity(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	if _, err := native.Exec("CREATE TABLE samples (id BIGINT PRIMARY KEY, label TEXT, amount DOUBLE PRECISION, enabled BOOLEAN, payload BLOB, happened TIMESTAMPTZ)"); err != nil {
		t.Fatal(err)
	}
	for _, values := range [][]any{
		{int64(9007199254740993), "quotes\" <tag>\nTi\u1ebfng Vi\u1ec7t", 1.25, true, []byte{0, 255, 42}, time.Date(2026, 10, 10, 12, 30, 0, 123456789, time.UTC)},
		{int64(2), nil, nil, false, nil, nil},
	} {
		if _, err := native.Exec("INSERT INTO samples VALUES ($1, $2, $3, $4, $5, $6)", values...); err != nil {
			t.Fatal(err)
		}
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: fixtureOpener(native)})
	if err != nil {
		t.Fatal(err)
	}
	remote, _ := networkDatabase(t, handler, true)
	for _, query := range []string{
		"SELECT * FROM samples ORDER BY id",
		"SELECT label, amount, enabled, payload FROM samples ORDER BY label",
		"SELECT count(*) FROM samples",
		"SELECT DISTINCT enabled FROM samples ORDER BY enabled",
		"SELECT * FROM samples WHERE id = 0",
	} {
		t.Run(query, func(t *testing.T) {
			left, leftTypes := collectSQLRows(t, native, query)
			right, rightTypes := collectSQLRows(t, remote, query)
			if !reflect.DeepEqual(left, right) || !reflect.DeepEqual(leftTypes, rightTypes) {
				t.Fatalf("native=%#v %v remote=%#v %v", left, leftTypes, right, rightTypes)
			}
		})
	}
	for _, query := range []string{"SELECT * FROM missing_table", "SELECT FROM", "INSERT INTO samples (id) VALUES (2)"} {
		_, nativeErr := native.Exec(query)
		_, remoteErr := remote.Exec(query)
		if nativeErr == nil || remoteErr == nil {
			t.Fatalf("error parity for %q: native=%v remote=%v", query, nativeErr, remoteErr)
		}
	}
}

func collectSQLRows(t testing.TB, database *sql.DB, query string) ([][]any, []string) {
	t.Helper()
	rows, err := database.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	types := make([]string, len(columns))
	for i, column := range columns {
		types[i] = column.Name() + ":" + column.DatabaseTypeName()
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = append([]byte(nil), bytes...)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result, types
}

func BenchmarkKitSQLNetworkStream(b *testing.B) {
	native, closeNative := openKitSQLFixture(b)
	b.Cleanup(closeNative)
	tx, err := native.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	for id := 2; id <= 256; id++ {
		if _, err := tx.Exec("INSERT INTO products VALUES ($1, $2, $3)", id, strings.Repeat("x", 128), id); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: fixtureOpener(native)})
	if err != nil {
		b.Fatal(err)
	}
	remote, _ := networkDatabase(b, handler, true)
	for _, adapter := range []struct {
		name     string
		database *sql.DB
	}{{"native", native}, {"tls", remote}} {
		for _, firstOnly := range []bool{false, true} {
			mode := "all"
			if firstOnly {
				mode = "first"
			}
			b.Run(adapter.name+"/"+mode, func(b *testing.B) {
				b.ReportAllocs()
				var firstLatency time.Duration
				for i := 0; i < b.N; i++ {
					started := time.Now()
					rows, err := adapter.database.Query("SELECT id, name FROM products ORDER BY id")
					if err != nil {
						b.Fatal(err)
					}
					if !rows.Next() {
						b.Fatalf("missing first row: %v", rows.Err())
					}
					firstLatency += time.Since(started)
					count := 1
					if !firstOnly {
						for rows.Next() {
							count++
						}
						if err := rows.Err(); err != nil {
							b.Fatal(err)
						}
						if count != 256 {
							b.Fatalf("row count = %d", count)
						}
					}
					if err := rows.Close(); err != nil && !errors.Is(err, io.EOF) {
						b.Fatal(err)
					}
				}
				b.ReportMetric(float64(firstLatency.Nanoseconds())/float64(b.N), "first-row-ns/op")
			})
		}
	}
}
