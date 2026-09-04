//go:build windows

package search

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const (
	moveFileReplaceExisting = 0x1
	moveFileWriteThrough    = 0x8
)

var moveFileExW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileExW")

func publishFile(oldPath, newPath string) error {
	return moveFile(oldPath, newPath, moveFileWriteThrough)
}

func replaceFile(oldPath, newPath string) error {
	return moveFile(oldPath, newPath, moveFileReplaceExisting|moveFileWriteThrough)
}

func moveFile(oldPath, newPath string, flags uintptr) error {
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
		flags,
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
