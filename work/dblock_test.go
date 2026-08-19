package work

import (
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/database"
)

// The OS lock must exclude a SECOND holder of the same file (that second lockFile call stands in for a
// second kitwork process). This is the guard that turns "two processes silently corrupt the db" into a
// loud, early failure.
func TestFileLockExcludesSecondHolder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db.lock")

	first, err := lockFile(path)
	if err != nil {
		t.Fatalf("first lock should succeed: %v", err)
	}

	// A second independent lock on the same file must FAIL while the first is held.
	if second, err := lockFile(path); err == nil {
		second.close()
		t.Fatal("second lock succeeded while the first was held — the guard does not exclude a second holder")
	}

	// After releasing the first, the file can be locked again (no stale lock).
	first.close()
	third, err := lockFile(path)
	if err != nil {
		t.Fatalf("lock should be re-acquirable after release: %v", err)
	}
	third.close()
}

// acquireDBLock is idempotent within a process (re-opening across hot-reloads must not self-deadlock),
// and it actually holds the lock so a fresh lockFile on the same path is blocked.
func TestAcquireDBLockIdempotentAndHeld(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")

	if err := acquireDBLock(dbPath); err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	if err := acquireDBLock(dbPath); err != nil {
		t.Fatalf("re-acquire in the same process must be a no-op, got: %v", err)
	}
	// A fresh OS lock on the same lock file must be blocked while this process holds it.
	if h, err := lockFile(lockFilePath(dbPath)); err == nil {
		h.close()
		t.Fatal("acquireDBLock did not actually hold the OS lock")
	}
	releaseDBLock(dbPath)
	// After release the path is free again.
	if err := acquireDBLock(dbPath); err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}
	releaseDBLock(dbPath)
}

// Only local file backends are guarded; :memory: and network backends must be exempt.
func TestLockableDBPathClassification(t *testing.T) {
	cases := []struct {
		name   string
		config *database.Config
		want   bool
	}{
		{"turso file", &database.Config{Type: "turso", Name: "app.db"}, true},
		{"sqlite file", &database.Config{Type: "sqlite", Name: "data/app.db"}, true},
		{"sqlite memory", &database.Config{Type: "sqlite", Name: ":memory:"}, false},
		{"postgres", &database.Config{Type: "postgres", Name: "kitwork", Host: "db"}, false},
		{"empty", &database.Config{Type: "turso"}, false},
	}
	for _, c := range cases {
		if _, ok := lockableDBPath(c.config); ok != c.want {
			t.Errorf("%s: lockableDBPath ok = %v, want %v", c.name, ok, c.want)
		}
	}
}
