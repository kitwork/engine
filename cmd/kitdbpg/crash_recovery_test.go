package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
	"github.com/lib/pq"
)

const crashPassword = "Crash-lab-only-2026!"

type processCrashReport struct {
	SchemaVersion int                `json:"schema_version"`
	OS            string             `json:"os"`
	Arch          string             `json:"arch"`
	BinarySHA256  string             `json:"binary_sha256"`
	ChildRace     bool               `json:"child_race"`
	Cases         []processCrashCase `json:"cases"`
	Success       bool               `json:"success"`
}

type processCrashCase struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
}

// This opt-in campaign builds and kills the real server, never the test runner
// or a server discovered by port/PID. No production fault hooks are needed.
func TestStandaloneRestrictedWriteCrashRecovery(t *testing.T) {
	if os.Getenv("KITDB_PROCESS_CRASH") != "1" {
		t.Skip("set KITDB_PROCESS_CRASH=1 to build and hard-stop standalone servers")
	}
	report := processCrashReport{SchemaVersion: 1, OS: runtime.GOOS, Arch: runtime.GOARCH}
	if path := os.Getenv("KITDB_PROCESS_CRASH_REPORT"); path != "" {
		t.Cleanup(func() {
			report.Success = !t.Failed() && len(report.Cases) == 6
			path = filepath.Join("..", "..", path)
			data, err := json.MarshalIndent(report, "", "  ")
			if err == nil {
				err = os.MkdirAll(filepath.Dir(path), 0700)
			}
			if err == nil {
				err = os.WriteFile(path, append(data, '\n'), 0600)
			}
			if err != nil {
				t.Errorf("write process crash evidence: %v", err)
			}
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	executable := filepath.Join(t.TempDir(), "kitdbpg-crash.exe")
	buildArgs := []string{"build", "-o", executable}
	childRace := os.Getenv("KITDB_PROCESS_CRASH_RACE") == "1"
	report.ChildRace = childRace
	if childRace {
		buildArgs = append(buildArgs, "-race")
	}
	buildArgs = append(buildArgs, "./cmd/kitdbpg")
	build := exec.CommandContext(ctx, "go", buildArgs...)
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	f, err := os.Open(executable)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("standalone executable SHA256=%x child_race=%t (current worktree, not a clean commit)", hash.Sum(nil), childRace)
	report.BinarySHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	for _, scenario := range []string{"commit_ack", "staged_batch", "grant_ack", "revoke_ack", "commit_not_forwarded", "commit_reply_withheld"} {
		passed := t.Run(scenario, func(t *testing.T) {
			root := crashRoot(t)
			server := startCrashServer(t, ctx, executable, root)
			admin := crashClient(t, ctx, server.address, "admin")
			for _, q := range []string{
				"CREATE TABLE receipts(id BIGINT PRIMARY KEY, amount BIGINT)",
				"CREATE TABLE effects(id BIGINT PRIMARY KEY, amount BIGINT)",
				"CREATE USER writer WITH PASSWORD '" + crashPassword + "'",
				"GRANT CONNECT ON DATABASE shop TO writer",
				"CREATE ROLE publisher",
				"GRANT SELECT ON TABLE receipts TO ROLE publisher",
				"GRANT INSERT ON TABLE receipts TO ROLE publisher",
				"GRANT SELECT ON TABLE effects TO ROLE publisher",
				"GRANT INSERT ON TABLE effects TO ROLE publisher",
				"GRANT ROLE publisher ON DATABASE shop TO writer",
			} {
				crashExec(t, ctx, admin, q)
			}
			var roleID string
			if err := admin.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='publisher'").Scan(&roleID); err != nil {
				t.Fatal(err)
			}
			ledger := newCrashLedger(t, root)
			wantRows := 0
			switch scenario {
			case "grant_ack", "revoke_ack":
				verb := "GRANT UPDATE ON TABLE effects TO ROLE publisher"
				if scenario == "revoke_ack" {
					crashExec(t, ctx, admin, verb)
					verb = "REVOKE UPDATE ON TABLE effects FROM ROLE publisher"
				}
				crashExec(t, ctx, admin, verb)
				ledger("acl_ack", scenario)
				server.kill(t)
			case "commit_ack", "staged_batch":
				writer := crashClient(t, ctx, server.address, "writer")
				tx := crashBatch(t, ctx, writer)
				if scenario == "commit_ack" {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					ledger("commit_ack", scenario)
					wantRows = 1
				} else {
					ledger("staged_only", scenario)
				}
				server.kill(t)
				tx.Rollback()
				writer.Close()
			default:
				gate := newCommitGate(t, ctx, server.address, scenario)
				writer := crashClient(t, ctx, gate.address, "writer")
				tx := crashBatch(t, ctx, writer)
				finished := make(chan error, 1)
				go func() { finished <- tx.Commit() }()
				select {
				case <-gate.reached:
				case err := <-finished:
					t.Fatalf("commit completed before fault gate: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(15 * time.Second):
					t.Fatal("commit fault gate not reached")
				}
				ledger("commit_unknown", scenario)
				// In reply-withheld mode C(COMMIT) has arrived from the server,
				// but no commit response has been forwarded to the client.
				server.kill(t)
				gate.close(t)
				select {
				case err := <-finished:
					var sqlErr *pq.Error
					if err == nil || errors.As(err, &sqlErr) {
						t.Fatalf("expected transport-unknown commit, got %v", err)
					}
				case <-time.After(10 * time.Second):
					t.Fatal("commit client did not terminate")
				}
				writer.Close()
				if scenario == "commit_reply_withheld" {
					wantRows = 1
				}
			}
			admin.Close()
			// A fresh OS process owns recovery; no in-process owner reuses state.
			recovered := startCrashServer(t, ctx, executable, root)
			reader := crashClient(t, ctx, recovered.address, "writer")
			receiptRows := 0
			for _, table := range []string{"receipts", "effects"} {
				count := crashCount(t, ctx, reader, "SELECT COUNT(*) FROM "+table, wantRows)
				if table == "receipts" {
					receiptRows = count
				}
				if wantRows == 1 {
					var amount int
					if err := reader.QueryRowContext(ctx, "SELECT amount FROM "+table+" WHERE id=1").Scan(&amount); err != nil || amount != 17 {
						t.Fatalf("recovered %s amount=%d err=%v", table, amount, err)
					}
				}
			}
			var recoveredID string
			if err := reader.QueryRowContext(ctx, "SELECT role_id FROM kitdb_catalog.roles WHERE role_name='publisher'").Scan(&recoveredID); err != nil || recoveredID != roleID {
				t.Fatalf("role identity changed: %q %q %v", roleID, recoveredID, err)
			}
			_, updateErr := reader.ExecContext(ctx, "UPDATE effects SET amount=18 WHERE id=99")
			if scenario == "grant_ack" {
				if updateErr != nil {
					t.Fatalf("acknowledged grant lost: %v", updateErr)
				}
			} else {
				var denied *pq.Error
				if !errors.As(updateErr, &denied) || string(denied.Code) != "42501" {
					t.Fatalf("missing UPDATE must deny even zero rows: %v", updateErr)
				}
			}
			if strings.HasPrefix(scenario, "commit_") && scenario != "commit_ack" {
				ledger("reconciled", fmt.Sprintf("rows=%d", receiptRows))
				// The receipt and the effect share a transaction. Retry only when
				// the authoritative receipt is absent, using the same operation ID.
				if receiptRows == 0 {
					tx := crashBatch(t, ctx, reader)
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					ledger("retry_commit_ack", scenario)
				}
				for _, table := range []string{"receipts", "effects"} {
					crashCount(t, ctx, reader, "SELECT COUNT(*) FROM "+table, 1)
				}
			}
			reader.Close()
			recovered.kill(t)
			if scenario == "commit_not_forwarded" || scenario == "commit_reply_withheld" {
				// Reconciliation is not complete until its result survives another
				// hard stop. Never confuse retry's statement ACK with commit ACK.
				final := startCrashServer(t, ctx, executable, root)
				client := crashClient(t, ctx, final.address, "writer")
				for _, table := range []string{"receipts", "effects"} {
					crashCount(t, ctx, client, "SELECT COUNT(*) FROM "+table, 1)
					var amount int
					if err := client.QueryRowContext(ctx, "SELECT amount FROM "+table+" WHERE id=1").Scan(&amount); err != nil || amount != 17 {
						t.Fatalf("reconciled %s amount=%d err=%v", table, amount, err)
					}
				}
				client.Close()
				final.kill(t)
			}
			ledger("verified", scenario)
			t.Logf("PASS fault=%s recovered_rows=%d role_identity_preserved=true client_ledger_synced=true child_processes_reaped=true", scenario, wantRows)
		})
		report.Cases = append(report.Cases, processCrashCase{Name: scenario, Success: passed})
	}
}

func crashRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := managed.Init(root); err != nil {
		t.Fatal(err)
	}
	e, err := relational.Open(filepath.Join(root, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err := managed.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	if _, err = catalog.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if err = catalog.EnableAuth("admin", crashPassword); err != nil {
		t.Fatal(err)
	}
	return root
}

type crashServer struct {
	command *exec.Cmd
	log     *os.File
	address string
}

func startCrashServer(t *testing.T, ctx context.Context, executable, root string) *crashServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	log, err := os.CreateTemp(root, "server-*.log")
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, executable, "-root", root, "-listen", address, "-verify-on-open")
	command.Stdout, command.Stderr = log, log
	if err := command.Start(); err != nil {
		log.Close()
		t.Fatal(err)
	}
	server := &crashServer{command: command, log: log, address: address}
	t.Cleanup(func() { server.kill(t) })
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && ctx.Err() == nil {
		db, err := sql.Open("postgres", crashDSN(address, "admin"))
		if err == nil {
			err = db.PingContext(ctx)
			db.Close()
			if err == nil {
				return server
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	server.kill(t)
	contents, _ := os.ReadFile(log.Name())
	t.Fatalf("standalone server not ready: %v\n%s", ctx.Err(), contents)
	return nil
}

func (s *crashServer) kill(t *testing.T) {
	t.Helper()
	if s.command == nil {
		return
	}
	if err := s.command.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("kill owned child: %v", err)
	}
	s.command.Wait() // A forced termination is expected, and must be reaped.
	s.command = nil
	if err := s.log.Close(); err != nil {
		t.Errorf("close child log: %v", err)
	}
	// A forced stop bypasses the race detector's normal exit status. Its
	// diagnostic must still fail the parent instead of disappearing in stderr.
	contents, err := os.ReadFile(s.log.Name())
	if err != nil {
		t.Errorf("read child log: %v", err)
	} else if bytes.Contains(contents, []byte("WARNING: DATA RACE")) {
		t.Errorf("child server reported a race:\n%s", contents)
	}
}

func crashDSN(address, user string) string {
	// Authentication is deliberately expensive, especially in a race build.
	// This correctness campaign does not impose a two-second login benchmark.
	u := url.URL{Scheme: "postgres", Host: address, Path: "/shop", User: url.UserPassword(user, crashPassword), RawQuery: "sslmode=disable&connect_timeout=15"}
	return u.String()
}

func crashClient(t *testing.T, ctx context.Context, address, user string) *sql.DB {
	t.Helper()
	db, err := sql.Open("postgres", crashDSN(address, user))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if err := db.PingContext(ctx); err != nil {
		t.Fatal(err)
	}
	return db
}

func crashExec(t *testing.T, ctx context.Context, db *sql.DB, q string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, q); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func crashCount(t *testing.T, ctx context.Context, db *sql.DB, q string, want int) int {
	t.Helper()
	var count int
	if err := db.QueryRowContext(ctx, q).Scan(&count); err != nil || count != want {
		t.Fatalf("%s count=%d want=%d err=%v", q, count, want, err)
	}
	return count
}

func crashBatch(t *testing.T, ctx context.Context, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Rollback() })
	for _, table := range []string{"receipts", "effects"} {
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" VALUES(1,17)"); err != nil {
			t.Fatal(err)
		}
	}
	return tx
}

func newCrashLedger(t *testing.T, root string) func(string, string) {
	t.Helper()
	f, err := os.OpenFile(filepath.Join(root, "client-ledger.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	return func(state, detail string) {
		t.Helper()
		if err := json.NewEncoder(f).Encode(map[string]any{"operation_id": 1, "state": state, "detail": detail}); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
	}
}

// A single-connection, bounded-frame test bridge. Before-forward mode stops Q
// COMMIT; reply-withheld mode stops C COMMIT before the client sees any ACK.
type commitGate struct {
	address  string
	listener net.Listener
	reached  chan struct{}
	done     chan struct{}
	stop     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	client   net.Conn
	upstream net.Conn
}

func newCommitGate(t *testing.T, ctx context.Context, upstream, mode string) *commitGate {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	g := &commitGate{address: ln.Addr().String(), listener: ln, reached: make(chan struct{}), done: make(chan struct{}), stop: make(chan struct{})}
	t.Cleanup(func() { g.close(t) })
	go func() {
		defer close(g.done)
		client, err := ln.Accept()
		if err != nil {
			return
		}
		defer client.Close()
		server, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", upstream)
		if err != nil {
			return
		}
		defer server.Close()
		g.mu.Lock()
		g.client, g.upstream = client, server
		g.mu.Unlock()
		// close may have raced connection establishment.
		select {
		case <-g.stop:
			return
		default:
		}
		if err := relayStartup(server, client); err != nil {
			return
		}
		var workers sync.WaitGroup
		workers.Add(2)
		relay := func(dst, src net.Conn, frontend bool) {
			defer workers.Done()
			defer client.Close()
			defer server.Close()
			for {
				frame, err := readCrashFrame(src)
				if err != nil {
					return
				}
				payload := strings.TrimSpace(strings.TrimSuffix(string(frame[5:]), "\x00"))
				intercept := frontend && mode == "commit_not_forwarded" && frame[0] == 'Q' && strings.EqualFold(payload, "COMMIT")
				intercept = intercept || !frontend && mode == "commit_reply_withheld" && frame[0] == 'C' && payload == "COMMIT"
				if intercept {
					close(g.reached)
					<-g.stop
					return
				}
				if _, err := io.Copy(dst, bytes.NewReader(frame)); err != nil {
					return
				}
			}
		}
		go relay(server, client, true)
		go relay(client, server, false)
		workers.Wait()
	}()
	return g
}

func (g *commitGate) close(t *testing.T) {
	t.Helper()
	g.once.Do(func() {
		close(g.stop)
		g.listener.Close()
		g.mu.Lock()
		if g.client != nil {
			g.client.Close()
			g.upstream.Close()
		}
		g.mu.Unlock()
	})
	select {
	case <-g.done:
	case <-time.After(10 * time.Second):
		t.Error("pgwire fault bridge did not drain")
	}
}

func relayStartup(dst io.Writer, src io.Reader) error {
	var header [4]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n < 8 || n > 1<<20 {
		return fmt.Errorf("invalid startup length %d", n)
	}
	if _, err := dst.Write(header[:]); err != nil {
		return err
	}
	_, err := io.CopyN(dst, src, int64(n)-4)
	return err
}

func readCrashFrame(src io.Reader) ([]byte, error) {
	var header [5]byte
	if _, err := io.ReadFull(src, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[1:])
	if n < 4 || n > 1<<20 {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	frame := make([]byte, int(n)+1)
	copy(frame, header[:])
	_, err := io.ReadFull(src, frame[5:])
	return frame, err
}

func TestCrashBridgeFrameBounds(t *testing.T) {
	valid := []byte{'C', 0, 0, 0, 11, 'C', 'O', 'M', 'M', 'I', 'T', 0}
	for _, tc := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"empty", nil, false},
		{"short_header", []byte{'C', 0}, false},
		{"short_length", []byte{'C', 0, 0, 0, 3}, false},
		{"oversized", []byte{'C', 0, 16, 0, 1}, false},
		{"truncated_body", valid[:6], false},
		{"commit_complete", valid, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame, err := readCrashFrame(bytes.NewReader(tc.data))
			if (err == nil) != tc.ok || tc.ok && !bytes.Equal(frame, tc.data) {
				t.Fatalf("frame=%x err=%v want_success=%t", frame, err, tc.ok)
			}
		})
	}
	for _, n := range []uint32{0, 7, 1<<20 + 1} {
		var header [4]byte
		binary.BigEndian.PutUint32(header[:], n)
		if err := relayStartup(io.Discard, bytes.NewReader(header[:])); err == nil {
			t.Fatalf("accepted startup length %d", n)
		}
	}
	startup := []byte{0, 0, 0, 8, 0, 3, 0, 0}
	var copied bytes.Buffer
	if err := relayStartup(&copied, bytes.NewReader(startup)); err != nil || !bytes.Equal(copied.Bytes(), startup) {
		t.Fatalf("startup relay: %x %v", copied.Bytes(), err)
	}
}
