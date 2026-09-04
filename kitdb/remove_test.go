package kitdb

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRemoveDatabaseRequiresExclusiveOwnershipAndRemovesSidecars(t *testing.T) {
	path := filepath.Join(t.TempDir(), "remove.kitdb")
	database, err := OpenWithOptions(path, OpenOptions{RetainHistory: true})
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Put([]byte("product/1"), []byte("before")); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDatabase(path); !errors.Is(err, ErrWriterLocked) {
		t.Fatalf("remove active database error = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("active database main file: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDatabase(path); err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{
		path,
		databaseWALPath(path),
		path + writerLockSuffix,
		databaseHistoryPath(path),
	} {
		if _, err := os.Lstat(removed); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("removed path %q error = %v", removed, err)
		}
	}
	if err := RemoveDatabase(path); !errors.Is(err, ErrDatabaseNotFound) {
		t.Fatalf("second removal error = %v", err)
	}
}
