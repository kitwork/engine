package gateway

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestTemporaryDirectoriesOfRunningProcessesStay: only the empty directory of a process no longer running is removed;
// a running process's, empty most of the time, stays; and the process's own directory is made again when something
// removed it.
//
// CONTROL (05/11/2026): with every empty directory removed (05/11 morning's rule), "the directory of a running
// process was removed"; without the directory made again, "the process's directory stays gone".
func TestTemporaryDirectoriesOfRunningProcessesStay(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"kitwork-sqlite-100", "kitwork-sqlite-200", "kitwork-sqlite-300", "kitwork-sqlite-own", "kitwork-sqlite-x"} {
		if err := os.Mkdir(filepath.Join(parent, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(parent, "kitwork-sqlite-300", "etilqs_1"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	removeStaleTemporaryDirectories(parent, "kitwork-sqlite-own", func(pid int) bool { return pid == 100 })
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(parent, name))
		return err == nil
	}
	if !exists("kitwork-sqlite-100") {
		t.Fatal("the directory of a running process was removed")
	}
	if exists("kitwork-sqlite-200") {
		t.Fatal("the empty directory of a process gone stays")
	}
	if !exists("kitwork-sqlite-300") || !exists("kitwork-sqlite-own") || !exists("kitwork-sqlite-x") {
		t.Fatal("a directory holding files, the process's own, or one of another name was removed")
	}
	if !processRunning(os.Getpid()) {
		t.Fatal("the test's own process is not found running")
	}

	prepareTemporaryFiles(context.Background())
	own := temporaryDirectory.path
	if own == "" {
		t.Skip("the process has no directory of its own for SQLite's temporary files")
	}
	if err := os.Remove(own); err != nil {
		t.Fatalf("remove the process's directory: %v", err)
	}
	prepareTemporaryFiles(context.Background())
	if _, err := os.Stat(own); err != nil {
		t.Fatal("the process's directory stays gone")
	}
}
