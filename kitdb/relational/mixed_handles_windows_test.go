package relational

import (
	"syscall"
	"unsafe"
)

var mixedGetProcessHandleCount = syscall.NewLazyDLL("kernel32.dll").NewProc("GetProcessHandleCount")

func mixedProcessHandles() (uint32, bool) {
	var count uint32
	// GetCurrentProcess() is the -1 pseudo-handle, not an owned OS resource.
	ok, _, _ := mixedGetProcessHandleCount.Call(^uintptr(0), uintptr(unsafe.Pointer(&count)))
	return count, ok != 0
}
