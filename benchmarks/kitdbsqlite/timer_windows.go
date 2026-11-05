package main

import (
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

// fineTimer asks Windows for a 1 ms timer resolution (timeBeginPeriod) and returns what gives it back.
func fineTimer() (func(), error) {
	winmm := syscall.NewLazyDLL("winmm.dll")
	begin, end := winmm.NewProc("timeBeginPeriod"), winmm.NewProc("timeEndPeriod")
	if result, _, _ := begin.Call(1); result != 0 {
		return nil, fmt.Errorf("timeBeginPeriod(1) returned %d", result)
	}
	return func() { _, _, _ = end.Call(1) }, nil
}

// lockSteps times taking and giving back a shared lock on one byte of a file (LockFileEx, UnlockFileEx), what
// SQLite's Windows VFS does on the -shm and the database file at the start and end of every read transaction, until
// stop is closed.
func lockSteps(path string, offset uint32, stop <-chan struct{}) ([]time.Duration, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	kernel := syscall.NewLazyDLL("kernel32.dll")
	lock, unlock := kernel.NewProc("LockFileEx"), kernel.NewProc("UnlockFileEx")
	var steps []time.Duration
	for {
		select {
		case <-stop:
			return steps, nil
		default:
		}
		overlapped := syscall.Overlapped{Offset: offset}
		start := time.Now()
		// LOCKFILE_FAIL_IMMEDIATELY (1), shared
		if result, _, err := lock.Call(file.Fd(), 1, 0, 1, 0, uintptr(unsafe.Pointer(&overlapped))); result == 0 {
			return nil, fmt.Errorf("LockFileEx: %v", err)
		}
		if result, _, err := unlock.Call(file.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped))); result == 0 {
			return nil, fmt.Errorf("UnlockFileEx: %v", err)
		}
		steps = append(steps, time.Since(start))
	}
}
