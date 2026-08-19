//go:build turso

package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	// The sqlite CONTROL phase needs the modernc driver in this test binary (production registers it
	// in package work). The turso driver comes from the tagged turso_driver.go.
	_ "modernc.org/sqlite"
)

// MVCC concurrent-write differential — the ONE thing Turso Database does that plain SQLite cannot,
// and therefore the only technical reason that would justify the backend over modernc.
//
// SQLite (modernc), even in WAL mode, has a SINGLE writer: while one transaction holds the write
// lock, a second writer blocks and then fails with SQLITE_BUSY ("database is locked"). Turso adds
// MVCC via `BEGIN CONCURRENT`: two NON-conflicting write transactions proceed in parallel and both
// commit. This test proves the contrast on the same machine, same code path — measured, not assumed.
//
// Built ONLY with -tags turso. HONEST-FAILURE NOTE: Turso is pre-1.0; if tursogo v0.7.2 has not
// wired BEGIN CONCURRENT yet, the turso phase fails LOUDLY with the engine's actual error. That is a
// real finding to record, not a flaky test.
func TestMVCCConcurrentWriteDifferential(t *testing.T) {
	ctx := context.Background()

	// ---- PHASE 1 (CONTROL): modernc/sqlite serializes writers → the second writer is BUSY. ----
	sqDSN := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "sq.db")) +
		"?_pragma=busy_timeout(300)&_pragma=journal_mode(WAL)"
	sqErr := secondWriterError(ctx, t, "sqlite", sqDSN, "BEGIN IMMEDIATE", "")
	if sqErr == nil {
		t.Fatal("CONTROL BROKEN: modernc let two writers commit concurrently — SQLite must serialize them, so the turso result below would prove nothing")
	}
	if !isLockedErr(sqErr) {
		t.Fatalf("CONTROL: want a locked/busy error from the second sqlite writer, got: %v", sqErr)
	}
	t.Logf("control OK — sqlite rejected the second concurrent writer: %v", sqErr)

	// ---- PHASE 2 (SUBJECT): turso BEGIN CONCURRENT lets both writers commit. ----
	// FINDING: concurrent writes are an OPT-IN in Turso, enabled per-connection with
	// `PRAGMA journal_mode = 'mvcc'` (this replaced the earlier --experimental-mvcc flag, removed in
	// v0.4.0; the feature is still beta). Without it, BEGIN CONCURRENT errors "Concurrent transaction
	// mode is only supported when MVCC is enabled". So this headline capability is neither on by
	// default nor stable yet — a real caveat for adopting Turso.
	tsDSN, err := (&Config{Type: "turso", Name: filepath.Join(t.TempDir(), "ts.db")}).BuildDSN()
	if err != nil {
		t.Fatalf("turso BuildDSN: %v", err)
	}
	tsErr := secondWriterError(ctx, t, "turso", tsDSN, "BEGIN CONCURRENT", "PRAGMA journal_mode = 'mvcc'")
	if tsErr != nil {
		t.Fatalf("SUBJECT: turso rejected a second concurrent writer under BEGIN CONCURRENT — MVCC not delivering, or not yet wired in tursogo v0.7.2: %v", tsErr)
	}
	t.Log("subject OK — turso committed two concurrent writers under BEGIN CONCURRENT (MVCC)")
}

// secondWriterError opens driver/dsn, seeds a table, holds an OPEN write transaction on conn1 (started
// with beginStmt + an INSERT), then starts a SECOND write transaction on conn2 (same beginStmt) and
// tries to INSERT a DIFFERENT (non-conflicting) row. It returns conn2's error, where nil means the
// engine allowed the concurrent writer. On success it COMMITS BOTH and verifies both rows landed, so
// a nil return really means "two writers committed", not "the second silently no-op'd".
func secondWriterError(ctx context.Context, t *testing.T, driver, dsn, beginStmt, setup string) error {
	t.Helper()
	db, err := sql.Open(driver, dsn)
	if err != nil {
		t.Fatalf("[%s] open: %v", driver, err)
	}
	defer db.Close()
	// Must allow >=2 real connections, or database/sql's pool would serialize the writers before the
	// engine ever gets to — which would hide the very property under test.
	db.SetMaxOpenConns(4)

	// setup is an optional per-connection statement (e.g. turso's `PRAGMA journal_mode = 'mvcc'`).
	if setup != "" {
		if _, err := db.ExecContext(ctx, setup); err != nil {
			t.Fatalf("[%s] setup %q: %v", driver, setup, err)
		}
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT)`); err != nil {
		t.Fatalf("[%s] create: %v", driver, err)
	}

	conn1, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("[%s] conn1: %v", driver, err)
	}
	defer conn1.Close()
	conn2, err := db.Conn(ctx)
	if err != nil {
		t.Fatalf("[%s] conn2: %v", driver, err)
	}
	defer conn2.Close()

	// Enable the mode on the two explicit connections too — journal_mode is a per-connection setting
	// for the pinned handles that actually run the concurrent transactions.
	if setup != "" {
		if _, err := conn1.ExecContext(ctx, setup); err != nil {
			t.Fatalf("[%s] conn1 setup %q: %v", driver, setup, err)
		}
		if _, err := conn2.ExecContext(ctx, setup); err != nil {
			t.Fatalf("[%s] conn2 setup %q: %v", driver, setup, err)
		}
	}

	// Writer 1 opens a write transaction and stays OPEN (uncommitted) across writer 2's attempt.
	if _, err := conn1.ExecContext(ctx, beginStmt); err != nil {
		t.Fatalf("[%s] conn1 %q: %v", driver, beginStmt, err)
	}
	if _, err := conn1.ExecContext(ctx, `INSERT INTO t (id, v) VALUES (1, 'one')`); err != nil {
		t.Fatalf("[%s] conn1 insert: %v", driver, err)
	}

	// Writer 2 attempts its own write transaction while writer 1 is still open.
	if _, err := conn2.ExecContext(ctx, beginStmt); err != nil {
		_, _ = conn1.ExecContext(ctx, `ROLLBACK`)
		return err // e.g. SQLite BUSY at BEGIN IMMEDIATE
	}
	if _, err := conn2.ExecContext(ctx, `INSERT INTO t (id, v) VALUES (2, 'two')`); err != nil {
		_, _ = conn1.ExecContext(ctx, `ROLLBACK`)
		_, _ = conn2.ExecContext(ctx, `ROLLBACK`)
		return err
	}

	// Both writers proceeded — commit both and prove both rows survive.
	if _, err := conn1.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatalf("[%s] conn1 commit: %v", driver, err)
	}
	if _, err := conn2.ExecContext(ctx, `COMMIT`); err != nil {
		return err // a commit-time conflict still means the writers were not truly concurrent
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("[%s] count: %v", driver, err)
	}
	if n != 2 {
		t.Fatalf("[%s] both writers committed but only %d row(s) landed", driver, n)
	}
	return nil
}

func isLockedErr(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "locked") || strings.Contains(s, "busy")
}
