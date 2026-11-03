//go:build windows

package kitdb

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/kitwork/engine/kitdb/internal/checkpointfault"
	"github.com/kitwork/engine/kitdb/internal/storagetrace"
	"github.com/kitwork/engine/kitdb/internal/waldiag"
)

const (
	walRenameInfoEx       = 22
	walRenameReplace      = 0x1
	walRenamePOSIX        = 0x2
	walSourceDeleteAccess = 0x00010000
	walSourceWriteThrough = 0x80000000
)

type walRenameInformation struct {
	flags    uint32
	root     syscall.Handle
	nameSize uint32
	name     [1]uint16
}

var setWALFileInformation = syscall.NewLazyDLL("kernel32.dll").NewProc("SetFileInformationByHandle")

type walPublicationError struct {
	phase         string
	published     bool
	cause         error
	diagnosticErr error
}

func (err *walPublicationError) Error() string {
	message := fmt.Sprintf("kitdb WAL publication phase=%s published=%t: %v", err.phase, err.published, err.cause)
	if err.diagnosticErr != nil {
		message += fmt.Sprintf(" (diagnostic capture: %v)", err.diagnosticErr)
	}
	return message
}

func (err *walPublicationError) Unwrap() error { return err.cause }

func failWALPublication(phase, staging, target string, published bool, cause error, file *os.File) error {
	return &walPublicationError{phase: phase, published: published, cause: cause,
		diagnosticErr: waldiag.Observe(phase, staging, target, published, cause, file)}
}

// Reserve with the standard temp-file policy, then acquire delete sharing while
// the file is still empty and pinned by the reservation. The writer uses normal
// buffered I/O, not the publication handle's write-through flags.
func createWALRotationStaging(directory, pattern string) (file *os.File, returnErr error) {
	reserved, err := os.CreateTemp(directory, pattern)
	if err != nil {
		return nil, err
	}
	path := reserved.Name()
	defer func() {
		if err := reserved.Close(); err != nil {
			if file != nil {
				_ = file.Close()
				file = nil
			}
			returnErr = errors.Join(returnErr, err)
		}
		if returnErr != nil {
			_ = os.Remove(path)
		}
	}()
	name, err := windowsExtendedPath(path)
	if err != nil {
		return nil, err
	}
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := syscall.CreateFile(pointer, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open rotation staging", Path: path, Err: err}
	}
	file = os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return nil, fmt.Errorf("kitdb: invalid WAL rotation handle")
	}
	return file, nil
}

// The staged header is already synced. Keep its identity pinned through rename
// and a post-publication flush; a failure after rename must not restore old WAL
// offsets onto the new file. No-delete-share readers still refuse publication.
func publishWALFile(staging, target string) (published bool, err error) {
	return publishWALFileUsing(staging, target, renameWALHandle)
}

func publishWALFileUsing(staging, target string, rename func(*os.File, string) error) (published bool, err error) {
	name, err := windowsExtendedPath(staging)
	if err != nil {
		return false, failWALPublication("resolve-source", staging, target, false, err, nil)
	}
	pointer, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return false, failWALPublication("resolve-source", staging, target, false, err, nil)
	}
	openRegion := storagetrace.Start("kitdb.wal.publish.open")
	handle, err := syscall.CreateFile(pointer, syscall.GENERIC_WRITE|walSourceDeleteAccess,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL|walSourceWriteThrough, 0)
	openRegion.End()
	if err != nil {
		return false, failWALPublication("open-source", staging, target, false,
			&os.LinkError{Op: "rename", Old: staging, New: target, Err: err}, nil)
	}
	file := os.NewFile(uintptr(handle), staging)
	if file == nil {
		_ = syscall.CloseHandle(handle)
		return false, failWALPublication("wrap-source", staging, target, false, fmt.Errorf("kitdb: invalid WAL publication handle"), nil)
	}
	defer func() {
		if file != nil {
			closeRegion := storagetrace.Start("kitdb.wal.publish.close")
			closeErr := file.Close()
			closeRegion.End()
			if closeErr != nil {
				err = errors.Join(err, failWALPublication("close-source", staging, target, published, closeErr, file))
				if published {
					err = errors.Join(ErrDurabilityUncertain, err)
				}
			}
		}
	}()
	renameRegion := storagetrace.Start("kitdb.wal.publish.rename")
	renameErr := rename(file, target)
	renameRegion.End()
	if renameErr != nil {
		if !walRenameUnsupported(renameErr) {
			return false, failWALPublication("replace-target", staging, target, false,
				&os.LinkError{Op: "rename", Old: staging, New: target, Err: renameErr}, file)
		}
		// Only unsupported API/filesystem errors use the legacy primitive. An
		// access/sharing denial is returned directly, never retried or bypassed.
		closeErr := file.Close()
		file = nil
		if closeErr != nil {
			return false, failWALPublication("close-for-fallback", staging, target, false, closeErr, nil)
		}
		err := replaceFile(staging, target)
		if err != nil {
			return false, failWALPublication("fallback-replace", staging, target, false, err, nil)
		}
		return true, nil
	}
	published = true
	checkpointfault.Reach(target, "wal-publish-renamed")
	if err := checkpointfault.Error(target, "wal-publish-sync"); err != nil {
		return true, errors.Join(ErrDurabilityUncertain, failWALPublication("sync-published", staging, target, true, err, file))
	}
	syncRegion := storagetrace.Start("kitdb.wal.publish.sync")
	syncErr := file.Sync()
	syncRegion.End()
	if err := syncErr; err != nil {
		return true, errors.Join(ErrDurabilityUncertain, failWALPublication("sync-published", staging, target, true, err, file))
	}
	checkpointfault.Reach(target, "wal-publish-synced")
	return true, nil
}

func renameWALHandle(file *os.File, target string) error {
	if err := setWALFileInformation.Find(); err != nil {
		return err
	}
	name, err := windowsExtendedPath(target)
	if err != nil {
		return err
	}
	encoded, err := syscall.UTF16FromString(name)
	if err != nil {
		return err
	}
	offset := unsafe.Offsetof(walRenameInformation{}.name)
	buffer := make([]byte, int(offset)+len(encoded)*2)
	info := (*walRenameInformation)(unsafe.Pointer(&buffer[0]))
	info.flags = walRenameReplace | walRenamePOSIX
	info.nameSize = uint32((len(encoded) - 1) * 2)
	copy(unsafe.Slice(&info.name[0], len(encoded)), encoded)
	result, _, callErr := setWALFileInformation.Call(file.Fd(), walRenameInfoEx,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	runtime.KeepAlive(buffer)
	runtime.KeepAlive(file)
	if result != 0 {
		return nil
	}
	if callErr == syscall.Errno(0) {
		return syscall.EINVAL
	}
	return callErr
}

func walRenameUnsupported(err error) bool {
	for _, code := range []syscall.Errno{1, 50, 87, 120, 127} {
		if errors.Is(err, code) {
			return true
		}
	}
	return false
}
