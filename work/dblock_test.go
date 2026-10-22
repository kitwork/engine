package work

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/app"
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

// acquireDBLock takes the OS lock once and counts its holders within a process (re-opening across
// hot-reloads must not self-deadlock, and every app pool on a shared file holds it): the lock stays while
// any holder is left, and a fresh lockFile on the same path is blocked until the last release.
func TestAcquireDBLockCountsItsHolders(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "data.db")

	if err := acquireDBLock(dbPath); err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	if err := acquireDBLock(dbPath); err != nil {
		t.Fatalf("re-acquire in the same process must be a hold, not an error, got: %v", err)
	}
	// A fresh OS lock on the same lock file must be blocked while this process holds it.
	if h, err := lockFile(lockFilePath(dbPath)); err == nil {
		h.close()
		t.Fatal("acquireDBLock did not actually hold the OS lock")
	}
	releaseDBLock(dbPath)
	if h, err := lockFile(lockFilePath(dbPath)); err == nil {
		h.close()
		t.Fatal("the first release let the OS lock go while a second holder is left")
	}
	releaseDBLock(dbPath)
	// After the last release the path is free again.
	h, err := lockFile(lockFilePath(dbPath))
	if err != nil {
		t.Fatalf("the last release must free the OS lock: %v", err)
	}
	h.close()
	if err := acquireDBLock(dbPath); err != nil {
		t.Fatalf("acquire after release should succeed: %v", err)
	}
	releaseDBLock(dbPath)
}

// A host's SQLite file of connection("tên") is opened by every app that names it, each in its own pool:
// the single-process lock stays until the last of those apps closes, so a second kitwork process cannot
// open the file under an app still writing to it. Opening it again in one app is no new hold.
func TestSharedSQLiteFileKeepsItsLockUntilTheLastAppCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "warehouse.db")
	config := &database.Config{Type: "sqlite", Name: path}
	lockPath, guarded := lockableDBPath(config)
	if !guarded {
		t.Fatal("a SQLite file must be guarded")
	}
	first := &Tenant{appRuntime: app.NewRuntime("first")}
	second := &Tenant{appRuntime: app.NewRuntime("second")}
	defer first.appRuntime.Close()
	defer second.appRuntime.Close()
	for _, tenant := range []*Tenant{first, first, second} {
		if _, err := tenant.openDatabase(config); err != nil {
			t.Fatal(err)
		}
	}
	held := func() bool {
		h, err := lockFile(lockFilePath(lockPath))
		if err != nil {
			return true
		}
		h.close()
		return false
	}
	if !held() {
		t.Fatal("the file is not locked while two apps hold it")
	}
	first.appRuntime.Close()
	if !held() {
		t.Fatal("the first app to close released the lock while the second still has the file open")
	}
	second.appRuntime.Close()
	if held() {
		t.Fatal("the lock outlived the last app on the file")
	}

	// a file that does not open gives its hold back: no pool is kept, so no cleanup would ever release it
	blocker := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	broken := &database.Config{Type: "sqlite", Name: filepath.Join(blocker, "warehouse.db")}
	third := &Tenant{appRuntime: app.NewRuntime("third")}
	defer third.appRuntime.Close()
	if _, err := third.openDatabase(broken); err == nil {
		t.Fatal("a SQLite file under a regular file must not open")
	}
	brokenLock, _ := lockableDBPath(broken)
	h, err := lockFile(lockFilePath(brokenLock))
	if err != nil {
		t.Fatalf("a file that did not open kept its lock: %v", err)
	}
	h.close()
}

// Only local file backends are guarded; :memory: and network backends must be exempt.
func TestLockableDBPathClassification(t *testing.T) {
	cases := []struct {
		name   string
		config *database.Config
		want   bool
	}{
		{"sqlite file", &database.Config{Type: "sqlite", Name: "data/app.db"}, true},
		{"sqlite memory", &database.Config{Type: "sqlite", Name: ":memory:"}, false},
		{"postgres", &database.Config{Type: "postgres", Name: "kitwork", Host: "db"}, false},
	}
	for _, c := range cases {
		if _, ok := lockableDBPath(c.config); ok != c.want {
			t.Errorf("%s: lockableDBPath ok = %v, want %v", c.name, ok, c.want)
		}
	}
}
