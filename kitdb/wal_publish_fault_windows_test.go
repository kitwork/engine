//go:build kitdbfault

package kitdb

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestWindowsWALPublishedSyncFailure(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprintf("history_%t", history), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sync.kitdb")
			options := OpenOptions{RetainHistory: history}
			db, err := OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			commitPut(t, db, "first", "ack-1")
			ack := commitPut(t, db, "second", "ack-2")
			t.Setenv("KITDB_IO_FAULT_PATH", path+".wal")
			t.Setenv("KITDB_IO_FAULT_POINT", "wal-publish-sync")
			if _, err := db.Checkpoint(); !errors.Is(err, ErrDurabilityUncertain) {
				t.Fatalf("post-rename failure did not report uncertainty: %v", err)
			} else {
				requireWALPublicationPhase(t, err, "sync-published", true)
			}
			if db.wal != nil {
				t.Fatal("post-publication failure restored a WAL handle with stale offsets")
			}
			if _, err := db.Begin(); !errors.Is(err, ErrUnavailable) {
				t.Fatal("failed publication admitted a new writer", err)
			}
			if _, _, err := db.Get([]byte("first")); !errors.Is(err, ErrUnavailable) {
				t.Fatal("failed owner admitted a new reader", err)
			}
			reached, err := os.ReadFile(path + ".wal.io-reached")
			if err != nil || string(reached) != "wal-publish-sync\n" {
				t.Fatalf("post-publication injection not reached exactly once: %q %v", reached, err)
			}
			t.Setenv("KITDB_IO_FAULT_POINT", "")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-db.commitWriterDone:
			default:
				t.Fatal("commit writer did not drain")
			}
			reopened, err := OpenWithOptions(path, options)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			if reopened.lastTx != ack || reopened.walBaseTx != ack {
				t.Fatal("published boundary was not recovered exactly")
			}
			if string(requireValue(t, reopened, "first")) != "ack-1" || string(requireValue(t, reopened, "second")) != "ack-2" {
				t.Fatal("acknowledged data lost")
			}
			commitPut(t, reopened, "later", "ack-3")
			if err := reopened.Verify(); err != nil {
				t.Fatal(err)
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWindowsWALPublicationCrashChild(t *testing.T) {
	if os.Getenv("KITDB_WAL_PUBLICATION_CHILD") != "1" {
		t.Skip("fault-lab child only")
	}
	db, err := OpenWithOptions(os.Getenv("KITDB_WAL_PUBLICATION_PATH"), OpenOptions{RetainHistory: os.Getenv("KITDB_WAL_PUBLICATION_HISTORY") == "1"})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	t.Fatal("crash boundary was not reached")
}

func TestWindowsWALPublicationCrash(t *testing.T) {
	for _, history := range []bool{false, true} {
		for _, point := range []string{"wal-publish-renamed", "wal-publish-synced"} {
			t.Run(fmt.Sprintf("history_%t_%s", history, point), func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "crash.kitdb")
				options := OpenOptions{RetainHistory: history}
				db, err := OpenWithOptions(path, options)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				commitPut(t, db, "first", "ack-1")
				ack := commitPut(t, db, "second", "ack-2")
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}
				historyValue := "0"
				if history {
					historyValue = "1"
				}
				t.Setenv("KITDB_WAL_PUBLICATION_CHILD", "1")
				t.Setenv("KITDB_WAL_PUBLICATION_PATH", path)
				t.Setenv("KITDB_WAL_PUBLICATION_HISTORY", historyValue)
				t.Setenv("KITDB_CHECKPOINT_FAULT_PATH", path+".wal")
				t.Setenv("KITDB_CHECKPOINT_FAULT_POINT", point)
				command := exec.Command(os.Args[0], "-test.run=^TestWindowsWALPublicationCrashChild$", "-test.timeout=30s")
				output, err := command.CombinedOutput()
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 73 {
					t.Fatalf("expected hard exit 73, got %v: %s", err, output)
				}
				reached, err := os.ReadFile(path + ".wal.fault-reached")
				if err != nil || string(reached) != point {
					t.Fatalf("wrong crash boundary: %q %v", reached, err)
				}
				t.Setenv("KITDB_CHECKPOINT_FAULT_POINT", "")
				reopened, err := OpenWithOptions(path, options)
				if err != nil {
					t.Fatal(err)
				}
				defer reopened.Close()
				if reopened.lastTx != ack || string(requireValue(t, reopened, "first")) != "ack-1" || string(requireValue(t, reopened, "second")) != "ack-2" {
					t.Fatal("hard exit lost acknowledged data")
				}
				if err := reopened.Verify(); err != nil {
					t.Fatal(err)
				}
				if err := reopened.Close(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}
