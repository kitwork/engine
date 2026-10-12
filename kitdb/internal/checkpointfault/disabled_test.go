//go:build !kitdbfault

package checkpointfault

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestOrdinaryBuildCannotArmCheckpointFault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ordinary.kitdb")
	t.Setenv("KITDB_CHECKPOINT_FAULT_PATH", path)
	t.Setenv("KITDB_CHECKPOINT_FAULT_POINT", "superblock-synced")
	Reach(path, "superblock-synced")
	t.Setenv("KITDB_IO_FAULT_PATH", path)
	t.Setenv("KITDB_IO_FAULT_POINT", "wal-write")
	t.Setenv("KITDB_IO_FAULT_KIND", "short")
	if err := Error(path, "wal-write"); err != nil {
		t.Fatalf("ordinary build armed an I/O error: %v", err)
	}
	if err := WriteError(path, "wal-write", nil, nil, -1); err != nil {
		t.Fatalf("ordinary build armed a partial write: %v", err)
	}
	if _, err := os.Stat(path + ".io-reached"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary build created I/O fault marker: %v", err)
	}
	if _, err := os.Stat(path + ".fault-reached"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ordinary build created fault marker: %v", err)
	}
}
