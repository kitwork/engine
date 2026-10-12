package kitsql_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/kitsql"
)

// net.Pipe has no send buffer: a peer that does not read blocks the first write.
type stalledStreamWriter struct {
	net.Conn
	header  http.Header
	started chan struct{}
	once    sync.Once
}

func (writer *stalledStreamWriter) Header() http.Header { return writer.header }
func (*stalledStreamWriter) WriteHeader(int)            {}
func (writer *stalledStreamWriter) Write(payload []byte) (int, error) {
	writer.once.Do(func() { close(writer.started) })
	return writer.Conn.Write(payload)
}
func (*stalledStreamWriter) FlushError() error { return nil }

func kitSQLStreamRequest(ctx context.Context) *http.Request {
	request := httptest.NewRequest(http.MethodPost, kitsql.Path, strings.NewReader(`{"version":1,"database":"shop","mode":"query","sql":"SELECT name FROM products"}`)).WithContext(ctx)
	request.RemoteAddr = "127.0.0.1:12345"
	request.SetBasicAuth("kitdb", "secret")
	request.Header.Set("Content-Type", kitsql.MediaType)
	request.Header.Set("Accept", kitsql.StreamMediaType)
	return request
}

func TestKitSQLBlockedStreamReleasesResources(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			native, closeNative := openKitSQLFixture(t)
			defer closeNative()
			var released atomic.Int32
			timeout := 2 * time.Second
			if mode == "timeout" {
				timeout = 80 * time.Millisecond
			}
			handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
				Open: func(context.Context, string, string, string) (*sql.DB, func() error, error) {
					return native, func() error { released.Add(1); return nil }, nil
				}, AllowInsecureLocal: true, MaximumConcurrent: 1, QueryTimeout: timeout,
			})
			if err != nil {
				t.Fatal(err)
			}
			server, peer := net.Pipe()
			defer server.Close()
			defer peer.Close()
			writer := &stalledStreamWriter{Conn: server, header: make(http.Header), started: make(chan struct{})}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); handler.ServeHTTP(writer, kitSQLStreamRequest(ctx)) }()
			select {
			case <-writer.started:
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not start")
			}
			if mode == "cancel" {
				cancel()
			}
			if mode == "disconnect" {
				_ = peer.Close()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = peer.Close()
				t.Fatal("blocked stream did not stop")
			}
			if released.Load() != 1 || native.Stats().InUse != 0 {
				t.Fatalf("release = %d, in-use connections = %d", released.Load(), native.Stats().InUse)
			}
			// The only admission slot and the database cursor must both be reusable.
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, kitSQLStreamRequest(context.Background()))
			if recorder.Code != 200 || !strings.Contains(recorder.Body.String(), `"type":"complete"`) {
				t.Fatalf("next request = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestKitSQLStreamCountsEncodedBytes(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	if _, err := native.Exec(`UPDATE products SET name = $1`, strings.Repeat("<", 128)); err != nil {
		t.Fatal(err)
	}
	serve := func(limit int64) *httptest.ResponseRecorder {
		handler, err := kitsql.NewHandler(kitsql.HandlerOptions{Open: fixtureOpener(native), AllowInsecureLocal: true, MaximumResultBytes: limit})
		if err != nil {
			t.Fatal(err)
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, kitSQLStreamRequest(context.Background()))
		return recorder
	}
	full := serve(0)
	boundary := int64(full.Body.Len())
	if exact := serve(boundary); exact.Body.String() != full.Body.String() {
		t.Fatal("exact wire-byte boundary rejected")
	}
	for _, limit := range []int64{1, 256, boundary - 1} {
		response := serve(limit)
		decoder := json.NewDecoder(response.Body)
		var resultBytes int64
		var last kitsql.StreamFrame
		for {
			var frame kitsql.StreamFrame
			if err := decoder.Decode(&frame); err == io.EOF {
				break
			} else if err != nil {
				t.Fatal(err)
			}
			if frame.Type != kitsql.FrameError {
				payload, _ := json.Marshal(frame)
				resultBytes += int64(len(payload) + 1)
			}
			last = frame
		}
		if resultBytes > limit || last.Type != kitsql.FrameError || last.Error.Code != "KSQL_RESULT_LIMIT" {
			t.Fatalf("limit %d: bytes %d, terminal %+v", limit, resultBytes, last)
		}
	}
}

func TestKitSQLSlowBodyCannotHoldAdmission(t *testing.T) {
	native, closeNative := openKitSQLFixture(t)
	defer closeNative()
	var opened atomic.Int32
	handler, err := kitsql.NewHandler(kitsql.HandlerOptions{
		Open: func(context.Context, string, string, string) (*sql.DB, func() error, error) {
			opened.Add(1)
			return native, func() error { return nil }, nil
		}, AllowInsecureLocal: true, MaximumConcurrent: 1, QueryTimeout: 80 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	writer := &stalledStreamWriter{Conn: server, header: make(http.Header), started: make(chan struct{})}
	request := kitSQLStreamRequest(context.Background())
	request.Body = io.NopCloser(server)
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(writer, request) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = peer.Close()
		t.Fatal("slow body held admission past its deadline")
	}
	if opened.Load() != 0 {
		t.Fatal("incomplete request opened the database")
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, kitSQLStreamRequest(context.Background()))
	if recorder.Code != 200 || opened.Load() != 1 {
		t.Fatal("admission did not recover")
	}
}
