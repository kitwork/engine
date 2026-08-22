//go:build scale && windows

package search

import (
	"errors"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"
)

var getSegmentScaleProcessMemoryInfo = syscall.NewLazyDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type segmentScaleProcessMemoryCounters struct {
	size                       uint32
	pageFaultCount             uint32
	peakWorkingSetSize         uintptr
	workingSetSize             uintptr
	quotaPeakPagedPoolUsage    uintptr
	quotaPagedPoolUsage        uintptr
	quotaPeakNonPagedPoolUsage uintptr
	quotaNonPagedPoolUsage     uintptr
	pagefileUsage              uintptr
	peakPagefileUsage          uintptr
}

func currentSegmentScaleRSS() (uint64, error) {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return 0, err
	}
	counters := segmentScaleProcessMemoryCounters{size: uint32(unsafe.Sizeof(segmentScaleProcessMemoryCounters{}))}
	result, _, callErr := getSegmentScaleProcessMemoryInfo.Call(
		uintptr(handle),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.size),
	)
	if result == 0 {
		if callErr == nil || errors.Is(callErr, syscall.Errno(0)) {
			callErr = errors.New("GetProcessMemoryInfo failed")
		}
		return 0, callErr
	}
	return uint64(counters.workingSetSize), nil
}

func sampleSegmentScaleRSS(peak *atomic.Uint64, stop <-chan struct{}, stopped chan<- struct{}) {
	defer close(stopped)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if rss, err := currentSegmentScaleRSS(); err == nil {
				updateSegmentScalePeak(peak, rss)
			}
		case <-stop:
			return
		}
	}
}
