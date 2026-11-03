//go:build windows && kitdb_waldiag

package waldiag

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestWALDiagnosticFixtureConfinement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "fixture")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	report := filepath.Join(parent, "capture.json")
	for _, paths := range [][2]string{{"relative", report}, {root, "relative"}, {root, filepath.Join(root, "capture.json")}, {root, root}} {
		if err := Prepare(paths[0], paths[1]); err == nil {
			t.Fatalf("accepted unsafe configuration %v", paths)
		}
	}
	t.Setenv("KITDB_WAL_DIAG_ROOT", root)
	t.Setenv("KITDB_WAL_DIAG_REPORT", report)
	staging, target := filepath.Join(root, "new.tmp"), filepath.Join(root, "old.wal")
	if err := Observe("open-source", staging, target, false, os.ErrPermission, nil); err == nil {
		t.Fatal("captured an unmarked root")
	}
	if err := Prepare(root, report); err != nil {
		t.Fatal(err)
	}
	if err := Observe("open-source", filepath.Join(parent, "outside"), target, false, os.ErrPermission, nil); err == nil {
		t.Fatal("captured outside fixture root")
	}
	if _, err := os.Stat(report); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected configuration wrote capture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, markerName), []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Observe("open-source", staging, target, false, os.ErrPermission, nil); err == nil {
		t.Fatal("accepted corrupt fixture marker")
	}
	if err := os.WriteFile(report, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root, report); err == nil {
		t.Fatal("accepted existing report")
	}
}
