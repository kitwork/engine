//go:build !windows

package work

import (
	"os"
	"syscall"
)

// dbLockHandle keeps the locked file open for the lifetime of the lock (flock is tied to the open fd).
type dbLockHandle struct{ f *os.File }

// lockFile takes a non-blocking exclusive flock on the lock file. A second flock on the same file —
// from this or any process — fails immediately with EWOULDBLOCK. Closing the file (or the process
// exiting) releases the lock.
func lockFile(path string) (*dbLockHandle, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, err
	}
	return &dbLockHandle{f: f}, nil
}

func (l *dbLockHandle) close() {
	if l != nil && l.f != nil {
		_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
		_ = l.f.Close()
		l.f = nil
	}
}
