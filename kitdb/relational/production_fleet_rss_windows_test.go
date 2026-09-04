//go:build windows

package relational

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

var shoppingFleetGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

type shoppingFleetProcessMemoryCounters struct {
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

func shoppingFleetCurrentRSS() (uint64, bool) {
	counters := shoppingFleetProcessMemoryCounters{
		size: uint32(unsafe.Sizeof(shoppingFleetProcessMemoryCounters{})),
	}
	result, _, callErr := shoppingFleetGetProcessMemoryInfo.Call(
		uintptr(windows.CurrentProcess()),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.size),
	)
	if result == 0 {
		if callErr == nil || errors.Is(callErr, windows.ERROR_SUCCESS) {
			callErr = errors.New("GetProcessMemoryInfo failed")
		}
		return 0, false
	}
	return uint64(counters.workingSetSize), true
}
