//go:build windows

package search

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const moveFileWriteThrough = 0x8

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func publishFile(oldPath, newPath string) error {
	oldName, err := windowsExtendedPath(oldPath)
	if err != nil {
		return err
	}
	newName, err := windowsExtendedPath(newPath)
	if err != nil {
		return err
	}
	oldPointer, err := syscall.UTF16PtrFromString(oldName)
	if err != nil {
		return err
	}
	newPointer, err := syscall.UTF16PtrFromString(newName)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileExW.Call(
		uintptr(unsafe.Pointer(oldPointer)),
		uintptr(unsafe.Pointer(newPointer)),
		moveFileWriteThrough,
	)
	if result != 0 {
		return nil
	}
	if callErr == syscall.Errno(0) {
		callErr = syscall.EINVAL
	}
	return &os.LinkError{Op: "rename", Old: oldPath, New: newPath, Err: callErr}
}

func windowsExtendedPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if strings.HasPrefix(absolute, `\\?\`) {
		return absolute, nil
	}
	if strings.HasPrefix(absolute, `\\`) {
		return `\\?\UNC\` + strings.TrimPrefix(absolute, `\\`), nil
	}
	return `\\?\` + absolute, nil
}
