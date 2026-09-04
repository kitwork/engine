package main

import (
	"syscall"
	"time"
	"unsafe"
)

var performanceCounter = syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceCounter")
var performanceFrequency = func() int64 {
	var frequency int64
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("QueryPerformanceFrequency")
	if ok, _, err := proc.Call(uintptr(unsafe.Pointer(&frequency))); ok == 0 || frequency <= 0 {
		panic(err)
	}
	return frequency
}()

// Go's Windows interrupt-time clock can quantize sub-millisecond queries to zero.
// Use the same high-resolution clock family as Python's perf_counter_ns.
func benchmarkNow() int64 {
	var value int64
	if ok, _, err := performanceCounter.Call(uintptr(unsafe.Pointer(&value))); ok == 0 {
		panic(err)
	}
	return value
}

func benchmarkSince(start int64) time.Duration {
	return time.Duration(float64(benchmarkNow()-start) * 1e9 / float64(performanceFrequency))
}
