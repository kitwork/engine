//go:build windows && kitdb_waldiag

package waldiag

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const markerName = ".kitdb-wal-diagnostic-fixture"
const markerBody = "kitdb-wal-diagnostic-v1\n"

type fileObservation struct {
	Exists     bool   `json:"exists"`
	Bytes      int64  `json:"bytes"`
	Attributes uint32 `json:"windows_attributes"`
	Error      string `json:"error,omitempty"`
}

type handleObservation struct {
	Present bool   `json:"present"`
	Open    bool   `json:"open"`
	Value   string `json:"current_process_handle,omitempty"`
	Volume  uint32 `json:"volume_serial"`
	FileID  uint64 `json:"file_id"`
	Error   string `json:"error,omitempty"`
}

type resourceUser struct {
	PID         uint32 `json:"pid"`
	Started     uint64 `json:"start_filetime_100ns"`
	Application string `json:"application"`
	Service     string `json:"service"`
}

type observation struct {
	Version       int               `json:"version"`
	At            time.Time         `json:"observed_utc"`
	PID           int               `json:"observer_pid"`
	Phase         string            `json:"phase"`
	Published     bool              `json:"published"`
	Staging       string            `json:"staging"`
	Target        string            `json:"target"`
	Errno         uint32            `json:"windows_errno"`
	Cause         string            `json:"cause"`
	SourceState   fileObservation   `json:"source_state"`
	TargetState   fileObservation   `json:"target_state"`
	Handle        handleObservation `json:"publication_handle"`
	Users         []resourceUser    `json:"resource_users"`
	ResourceError string            `json:"resource_error,omitempty"`
	Limit         string            `json:"limitation"`
}

func beneath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validate(root, report string) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(report) || filepath.Clean(root) == filepath.VolumeName(root)+string(filepath.Separator) {
		return fmt.Errorf("WAL diagnostics require an explicit fixture root and absolute report")
	}
	if filepath.Clean(root) == filepath.Clean(report) || beneath(root, report) {
		return fmt.Errorf("WAL diagnostic report must be outside fixture root")
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("WAL diagnostic root must be a plain directory")
	}
	return nil
}

// Prepare is a host-test operation, never an app capability. The marker prevents
// accidental observation of an ordinary application database with a tagged host.
func Prepare(root, report string) error {
	if err := validate(root, report); err != nil {
		return err
	}
	if _, err := os.Lstat(report); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("WAL diagnostic report must be fresh: %v", err)
	}
	file, err := os.OpenFile(filepath.Join(root, markerName), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.WriteString(markerBody)
	return errors.Join(err, file.Close())
}

func statPath(path string) fileObservation {
	info, err := os.Lstat(path)
	if err != nil {
		return fileObservation{Error: err.Error()}
	}
	result := fileObservation{Exists: true, Bytes: info.Size()}
	if data, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		result.Attributes = data.FileAttributes
	}
	return result
}

func statHandle(file *os.File) handleObservation {
	if file == nil {
		return handleObservation{}
	}
	result := handleObservation{Present: true}
	handle := syscall.Handle(file.Fd())
	result.Value = fmt.Sprintf("%#x", handle)
	var data syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &data); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Open = true
	result.Volume = data.VolumeSerialNumber
	result.FileID = uint64(data.FileIndexHigh)<<32 | uint64(data.FileIndexLow)
	runtime.KeepAlive(file)
	return result
}

// Observe runs inside the publisher's failure branch, before its cleanup defers
// and before rotateWALLocked can restore the old WAL. Only the first O_EXCL
// report reservation wins; no mutable process-global observer is installed.
func Observe(phase, staging, target string, published bool, cause error, file *os.File) error {
	root, report := os.Getenv("KITDB_WAL_DIAG_ROOT"), os.Getenv("KITDB_WAL_DIAG_REPORT")
	if root == "" && report == "" {
		return nil
	}
	if err := validate(root, report); err != nil {
		return err
	}
	if !beneath(root, staging) || !beneath(root, target) {
		return fmt.Errorf("WAL diagnostic paths escape fixture root")
	}
	marker := filepath.Join(root, markerName)
	info, err := os.Lstat(marker)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() != int64(len(markerBody)) {
		return fmt.Errorf("invalid WAL diagnostic fixture marker")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != markerBody {
		return fmt.Errorf("invalid WAL diagnostic marker: %v", err)
	}
	output, err := os.OpenFile(report, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	result := observation{Version: 1, At: time.Now().UTC(), PID: os.Getpid(), Phase: phase, Published: published,
		Staging: staging, Target: target, Cause: fmt.Sprint(cause), SourceState: statPath(staging), TargetState: statPath(target), Handle: statHandle(file),
		Limit: "Diagnostic-build-only failure observation before publisher cleanup/recovery, not atomic with the failed OS call. First report reservation wins. Resource users and this process's publication-handle identity do not reveal blocking share flags or identify a driver. No retry, shutdown/restart, foreign-handle access or file-content read. Native queries are synchronous; resource-list retries <=3 and users <=256."}
	var errno syscall.Errno
	if errors.As(cause, &errno) {
		result.Errno = uint32(errno)
	}
	result.Users, err = users([]string{staging, target})
	if err != nil {
		result.ResourceError = err.Error()
	}
	return errors.Join(json.NewEncoder(output).Encode(result), output.Sync(), output.Close())
}

type rmProcess struct {
	PID                                uint32
	Started                            syscall.Filetime
	Application                        [256]uint16
	Service                            [64]uint16
	Type, Status, Session, Restartable uint32
}

// Restart Manager is observation only. Even an empty list cannot exclude
// transient/kernel users, and our PID can own non-blocking staging handles.
func users(paths []string) (result []resourceUser, resultErr error) {
	if unsafe.Sizeof(rmProcess{}) != 668 {
		return nil, fmt.Errorf("Restart Manager ABI mismatch")
	}
	names := make([]*uint16, len(paths))
	for i, path := range paths {
		pointer, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			return nil, err
		}
		names[i] = pointer
	}
	dll := syscall.NewLazyDLL("rstrtmgr.dll")
	start, end, register, list := dll.NewProc("RmStartSession"), dll.NewProc("RmEndSession"), dll.NewProc("RmRegisterResources"), dll.NewProc("RmGetList")
	for _, proc := range []*syscall.LazyProc{start, end, register, list} {
		if err := proc.Find(); err != nil {
			return nil, err
		}
	}
	var session uint32
	var key [33]uint16
	code, _, _ := start.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0])))
	if code != 0 {
		return nil, fmt.Errorf("RmStartSession: %w", syscall.Errno(code))
	}
	defer func() {
		code, _, _ := end.Call(uintptr(session))
		if code != 0 {
			resultErr = errors.Join(resultErr, fmt.Errorf("RmEndSession: %w", syscall.Errno(code)))
		}
	}()
	code, _, _ = register.Call(uintptr(session), uintptr(len(names)), uintptr(unsafe.Pointer(&names[0])), 0, 0, 0, 0)
	runtime.KeepAlive(names)
	if code != 0 {
		return nil, fmt.Errorf("RmRegisterResources: %w", syscall.Errno(code))
	}
	var entries [256]rmProcess
	for range 3 {
		var needed, reasons uint32
		count := uint32(len(entries))
		code, _, _ = list.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&entries[0])), uintptr(unsafe.Pointer(&reasons)))
		if code == 234 && needed <= uint32(len(entries)) {
			continue
		}
		if code != 0 {
			return nil, fmt.Errorf("RmGetList (needed=%d): %w", needed, syscall.Errno(code))
		}
		if count > uint32(len(entries)) {
			return nil, fmt.Errorf("invalid resource-user count")
		}
		for _, entry := range entries[:count] {
			result = append(result, resourceUser{entry.PID, uint64(entry.Started.HighDateTime)<<32 | uint64(entry.Started.LowDateTime), syscall.UTF16ToString(entry.Application[:]), syscall.UTF16ToString(entry.Service[:])})
		}
		return result, nil
	}
	return nil, fmt.Errorf("Restart Manager list changed after three attempts")
}
