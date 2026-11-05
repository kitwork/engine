//go:build !windows

package main

import (
	"errors"
	"time"
)

// fineTimer is Windows' timer resolution; elsewhere a sleep is not rounded up to a tick of it.
func fineTimer() (func(), error) {
	return nil, errors.New("not Windows: nothing to set")
}

// lockSteps is the Windows file locks SQLite takes; elsewhere they are fcntl locks, not measured here.
func lockSteps(string, uint32, <-chan struct{}) ([]time.Duration, error) {
	return nil, errors.New("not Windows: not measured")
}
