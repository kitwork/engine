package gateway

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// endlessSort sorts rows of a WITH RECURSIVE that never ends, 100 KB each: it reads no table, so any account runs it,
// and its sort spills to SQLite's temporary files until something stops it.
const endlessSort = `WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c) SELECT x, zeroblob(100000) FROM c ORDER BY x DESC`

// TestTemporaryFilesStopAStatement: a statement whose sort spills more than the session's MaxTemporaryBytes is
// canceled with 53400, as PostgreSQL cancels one over temp_file_limit, long before the QueryTimeout; the session
// goes on, and a statement that spills nothing runs.
//
// CONTROL (05/11/2026): with the watcher never canceling, "the endless sort: 57014 canceling statement due to user
// request after 16.1s, want 53400"; with Execute not turning the cause into 53400, "the endless sort: 57014 canceling
// statement due to user request after 300ms, want 53400"; with the temporary directory set on the session's own
// connection (as first written), "the endless sort: 42P01 no such table: temp.pg_namespace after 0s, want 53400":
// setting it closes that connection's TEMP database, where the catalog lives.
func TestTemporaryFilesStopAStatement(t *testing.T) {
	if !temporarySupported {
		t.Skip("SQLite's temporary files are not measured on this system")
	}
	fixture := newFixture(t)
	session := openSession(t, fixture, defaultAccess(), Options{MaxTemporaryBytes: 64 << 20})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	_, err := session.Execute(ctx, endlessSort, nil)
	took := time.Since(start).Round(100 * time.Millisecond)
	if codeOf(err) != "53400" || !strings.Contains(err.Error(), "temporary file size exceeds the port's limit (64 MB)") {
		t.Fatalf("the endless sort: %s %v after %s, want 53400", codeOf(err), err, took)
	}
	if took > 10*time.Second {
		t.Fatalf("the endless sort was stopped after %s", took)
	}
	if got := cells(mustRun(t, session, "SELECT name FROM users WHERE id = 1")); got != "An" {
		t.Fatalf("the session after the stop = %q, want An", got)
	}
	if got := cells(mustRun(t, session, "SELECT count(*) FROM (SELECT x FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x + 1 FROM c WHERE x < 1000) SELECT x FROM c) ORDER BY x DESC)")); got != "1000" {
		t.Fatalf("a sort that fits in memory = %q, want 1000", got)
	}
	temporaryFiles.mutex.Lock()
	running := len(temporaryFiles.running)
	temporaryFiles.mutex.Unlock()
	if running != 0 {
		t.Fatalf("%d statements still watched after they ended", running)
	}
}

// TestTemporaryGuardCancelsOnlyWhatPassesItsLimit: the watcher cancels each running statement whose limit the
// measured total passes, with a *temporaryLimit, leaves the others, and stops once none runs.
//
// CONTROL (05/11/2026): with every running statement canceled, "the statement of limit 100 was canceled".
func TestTemporaryGuardCancelsOnlyWhatPassesItsLimit(t *testing.T) {
	if !temporarySupported {
		t.Skip("SQLite's temporary files are not measured on this system")
	}
	guard := &temporaryGuard{running: map[*guardedStatement]struct{}{}, every: time.Millisecond,
		measure: func() (int64, bool) { return 50, true }}
	small, cancelSmall := context.WithCancelCause(context.Background())
	large, cancelLarge := context.WithCancelCause(context.Background())
	defer cancelLarge(nil)
	unwatchSmall := guard.watch(10, cancelSmall)
	unwatchLarge := guard.watch(100, cancelLarge)
	select {
	case <-small.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the statement of limit 10 was not canceled")
	}
	var limit *temporaryLimit
	if !errors.As(context.Cause(small), &limit) || limit.used != 50 || limit.limit != 10 {
		t.Fatalf("the cause = %v, want 50 of 10", context.Cause(small))
	}
	if code := codeOf(temporaryCause(small, context.Canceled)); code != "53400" {
		t.Fatalf("the statement's error = %s, want 53400", code)
	}
	time.Sleep(20 * time.Millisecond)
	if large.Err() != nil {
		t.Fatal("the statement of limit 100 was canceled")
	}
	unwatchSmall()
	unwatchLarge()
	deadline := time.Now().Add(5 * time.Second)
	for {
		guard.mutex.Lock()
		watching := guard.watching
		guard.mutex.Unlock()
		if !watching {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the watcher goes on with no statement running")
		}
		time.Sleep(time.Millisecond)
	}
	if code := codeOf(temporaryCause(large, context.Canceled)); code != "" {
		t.Fatalf("an error of a statement not stopped for its files became %s", code)
	}
}
