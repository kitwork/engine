package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestDropDatabaseRejectsActiveLeaseThenRemovesIdleDatabase(t *testing.T) {
	manager, err := NewManager(Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	path := filepath.Join(t.TempDir(), "drop.kitdb")
	lease, err := manager.Acquire(context.Background(), path, kitdb.OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := lease.DB().Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("product/1"), []byte("before")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := manager.DropDatabase(context.Background(), path); !errors.Is(err, ErrDatabaseBusy) {
		t.Fatalf("active drop error = %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := manager.DropDatabase(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dropped database stat error = %v", err)
	}
}
