//go:build kitdbfault && kitdb_waldiag

package kitdb

import (
	"errors"
	"os"
	"testing"
)

func TestWindowsWALPublicationSyncCapture(t *testing.T) {
	staging, target := walPhaseFixture(t)
	report := prepareWALPhaseCapture(t, staging)
	t.Setenv("KITDB_IO_FAULT_PATH", target)
	t.Setenv("KITDB_IO_FAULT_POINT", "wal-publish-sync")
	published, err := publishWALFile(staging, target)
	if !published || !errors.Is(err, ErrDurabilityUncertain) {
		t.Fatal("lost post-publication uncertainty", err)
	}
	requireWALPublicationPhase(t, err, "sync-published", true)
	capture := readWALPhaseCapture(t, report)
	if capture.Phase != "sync-published" || !capture.Published || capture.Source.Exists || !capture.Target.Exists || !capture.Handle.Open || capture.Handle.FileID == 0 {
		t.Fatalf("post-publication capture too late: %+v", capture)
	}
	if _, err := os.Stat(staging); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published staging name remains", err)
	}
}
