package kitdb

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RemoveDatabase permanently removes one closed KitDB database and its
// engine-owned sidecars. The writer lock proves that no other process owns the
// file; callers must separately prevent new opens while removal is in flight.
func RemoveDatabase(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("kitdb: remove: empty database path")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("kitdb: resolve database path: %w", err)
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Stat(absolute)
	switch {
	case err == nil && !info.Mode().IsRegular():
		return fmt.Errorf("kitdb: database path %q is not a regular file", absolute)
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return ErrDatabaseNotFound
	default:
		return fmt.Errorf("kitdb: inspect database file: %w", err)
	}

	lock, err := acquireWriterLock(absolute)
	if err != nil {
		return err
	}
	lockPath := absolute + writerLockSuffix
	closeLock := func() error {
		if lock == nil {
			return nil
		}
		err := lock.close()
		lock = nil
		return err
	}

	if err := os.Remove(absolute); err != nil {
		return errors.Join(fmt.Errorf("kitdb: remove main file: %w", err), closeLock())
	}
	walErr := removeDatabaseFile(databaseWALPath(absolute), "WAL")
	historyErr := removeDatabaseDirectory(databaseHistoryPath(absolute), "history")
	closeErr := closeLock()
	lockErr := removeDatabaseFile(lockPath, "writer lock")
	syncErr := syncDirectory(filepath.Dir(absolute))
	return errors.Join(walErr, historyErr, closeErr, lockErr, syncErr)
}

func removeDatabaseFile(path, label string) error {
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("kitdb: remove %s: %w", label, err)
}

func removeDatabaseDirectory(path, label string) error {
	_, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("kitdb: inspect %s: %w", label, err)
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("kitdb: remove %s: %w", label, err)
	}
	return nil
}
