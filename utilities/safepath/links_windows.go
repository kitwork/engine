package safepath

import (
	"fmt"
	"io/fs"
	"strings"
	"syscall"
	"unsafe"
)

var getFinalPathNameByHandle = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFinalPathNameByHandleW")

// resolveLinks is the path the system reaches for name: every link on the way followed, a junction (a
// folder's mount point, which mklink /J makes without the privilege a symlink needs) as much as a symlink,
// and each name read in the case and long form the disk holds. filepath.EvalSymlinks has not followed a
// junction since Go 1.23 (it reads one as a folder), so a path through a junction that leads out of a
// boundary read as inside it. Here the system resolves it: name is opened without reading it, a folder
// too, and the open handle's final path is read back.
func resolveLinks(name string) (string, error) {
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return "", &fs.PathError{Op: "open", Path: name, Err: err}
	}
	handle, err := syscall.CreateFile(pointer, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		// a name that is not there, or a link that leads nowhere, is missing: canonical walks up from it
		return "", &fs.PathError{Op: "open", Path: name, Err: err}
	}
	defer syscall.CloseHandle(handle)

	buffer := make([]uint16, syscall.MAX_PATH)
	for {
		// flags 0: FILE_NAME_NORMALIZED, VOLUME_NAME_DOS
		n, _, callErr := getFinalPathNameByHandle.Call(uintptr(handle), uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
		if n == 0 {
			// never a missing name, the file is open: a volume with no drive letter, or a driver that cannot
			// tell its path, fails here, and the path is refused rather than read as written
			return "", fmt.Errorf("final path of %s: %v", name, callErr)
		}
		if n < uintptr(len(buffer)) {
			return dosPath(syscall.UTF16ToString(buffer[:n]))
		}
		buffer = make([]uint16, n) // too small: n is the length it needs
	}
}

// dosPath is a final path without the \\?\ the system puts before it: C:\… for a drive, \\server\share\…
// for a share.
func dosPath(final string) (string, error) {
	if rest, ok := strings.CutPrefix(final, `\\?\UNC\`); ok {
		return `\\` + rest, nil
	}
	if rest, ok := strings.CutPrefix(final, `\\?\`); ok && len(rest) >= 2 && rest[1] == ':' {
		return rest, nil
	}
	return "", fmt.Errorf("unexpected final path %s", final)
}
