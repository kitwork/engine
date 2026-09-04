//go:build !windows

package main

import "time"

func benchmarkNow() time.Time                      { return time.Now() }
func benchmarkSince(start time.Time) time.Duration { return time.Since(start) }
