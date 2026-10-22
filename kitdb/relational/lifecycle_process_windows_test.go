package relational

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func lifecycleProcessThreads() (count uint32, resultErr error) {
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0, err
	}
	defer func() { resultErr = errors.Join(resultErr, syscall.CloseHandle(snapshot)) }()
	entry := syscall.ProcessEntry32{Size: uint32(unsafe.Sizeof(syscall.ProcessEntry32{}))}
	if err := syscall.Process32First(snapshot, &entry); err != nil {
		return 0, err
	}
	for scanned := 0; scanned < 65536; scanned++ {
		if entry.ProcessID == uint32(os.Getpid()) {
			return entry.Threads, nil
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		if err := syscall.Process32Next(snapshot, &entry); err != nil {
			return 0, fmt.Errorf("find current process thread count: %w", err)
		}
	}
	return 0, fmt.Errorf("process snapshot exceeds bounded scan")
}
