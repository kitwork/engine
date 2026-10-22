// Package benchclock supplies high-resolution elapsed time for benchmarks.
package benchclock

import (
	"syscall"
	"time"
	"unsafe"
)

type Stamp int64

var counter = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var frequency = func() int64 {
	var value int64
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	if ok, _, err := proc.Call(uintptr(unsafe.Pointer(&value))); ok == 0 || value <= 0 {
		panic(err)
	}
	return value
}()

// Now avoids Windows interrupt-time quantization of short benchmark phases.
func Now() Stamp {
	var value int64
	if ok, _, err := counter.Call(uintptr(unsafe.Pointer(&value))); ok == 0 {
		panic(err)
	}
	return Stamp(value)
}

func Since(start Stamp) time.Duration {
	return time.Duration(float64(Now()-start) * 1e9 / float64(frequency))
}
