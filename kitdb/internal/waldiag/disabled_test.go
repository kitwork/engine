//go:build !windows || !kitdb_waldiag

package waldiag

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOrdinaryBuildCannotCaptureWALDiagnostics(t *testing.T) {
	report := filepath.Join(t.TempDir(), "capture.json")
	t.Setenv("KITDB_WAL_DIAG_ROOT", t.TempDir())
	t.Setenv("KITDB_WAL_DIAG_REPORT", report)
	if err := Observe("replace-target", "bad\x00path", "", false, os.ErrPermission, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary build wrote diagnostics: %v", err)
	}
	if err := Prepare("", ""); err == nil {
		t.Fatal("ordinary build accepted diagnostic setup")
	}
	if allocations := testing.AllocsPerRun(20, func() { _ = Observe("replace-target", "", "", false, os.ErrPermission, nil) }); allocations != 0 {
		t.Fatalf("ordinary diagnostic stub allocated %.0f objects", allocations)
	}
}
