//go:build !windows

package benchclock

import "time"

type Stamp = time.Time

func Now() Stamp                      { return time.Now() }
func Since(start Stamp) time.Duration { return time.Since(start) }
