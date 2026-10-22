package relational

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

type lifecycleRMProcess struct {
	PID                                uint32
	Started                            syscall.Filetime
	Application                        [256]uint16
	Service                            [64]uint16
	Type, Status, Session, Restartable uint32
}

type lifecycleLockUser struct {
	PID           uint32 `json:"pid"`
	StartFiletime uint64 `json:"start_filetime_100ns"`
	Application   string `json:"application"`
	Service       string `json:"service"`
}

type lifecycleLockObservation struct {
	At            time.Time           `json:"observed_utc"`
	ElapsedMS     int64               `json:"elapsed_ms"`
	Paths         []string            `json:"paths"`
	Users         []lifecycleLockUser `json:"resource_users"`
	Error         string              `json:"error,omitempty"`
	RebootReasons uint32              `json:"restart_manager_reboot_reasons"`
	Limitation    string              `json:"limitation"`
}

// Diagnostic queries only: never RmShutdown/RmRestart. Restart Manager reports
// resource users, not per-handle share flags or a complete kernel lock inventory.
// https://learn.microsoft.com/en-us/windows/win32/api/restartmanager/nf-restartmanager-rmgetlist
func lifecycleLockUsers(paths []string) (observation lifecycleLockObservation) {
	at := time.Now()
	observation.At, observation.Paths = at.UTC(), append([]string(nil), paths...)
	observation.Limitation = "Best-effort resource users after failure, not proof of blocking share flags; empty results do not exclude transient or kernel locks. No shutdown/restart. Native API calls are synchronous; list retries capped at three, users capped at 256."
	var resultErr error
	defer func() {
		observation.ElapsedMS = time.Since(at).Milliseconds()
		if resultErr != nil {
			observation.Error = resultErr.Error()
		}
	}()
	if len(paths) < 1 || len(paths) > 2 {
		resultErr = fmt.Errorf("lock query requires 1..2 file paths")
		return
	}
	names := make([]*uint16, len(paths))
	for i, path := range paths {
		if !filepath.IsAbs(path) {
			resultErr = fmt.Errorf("lock query requires absolute paths")
			return
		}
		var err error
		names[i], err = syscall.UTF16PtrFromString(path)
		if err != nil {
			resultErr = err
			return
		}
	}
	dll := syscall.NewLazyDLL("rstrtmgr.dll")
	start, end, register, list := dll.NewProc("RmStartSession"), dll.NewProc("RmEndSession"), dll.NewProc("RmRegisterResources"), dll.NewProc("RmGetList")
	for _, proc := range []*syscall.LazyProc{start, end, register, list} {
		if err := proc.Find(); err != nil {
			resultErr = err
			return
		}
	}
	var session uint32
	var key [33]uint16
	code, _, _ := start.Call(uintptr(unsafe.Pointer(&session)), 0, uintptr(unsafe.Pointer(&key[0])))
	if code != 0 {
		resultErr = fmt.Errorf("RmStartSession: %w", syscall.Errno(code))
		return
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
		resultErr = fmt.Errorf("RmRegisterResources: %w", syscall.Errno(code))
		return
	}
	var processes [256]lifecycleRMProcess
	for range 3 {
		var needed uint32
		count := uint32(len(processes))
		code, _, _ = list.Call(uintptr(session), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&processes[0])), uintptr(unsafe.Pointer(&observation.RebootReasons)))
		if code == 234 && needed <= 256 {
			continue
		}
		if code != 0 {
			resultErr = fmt.Errorf("RmGetList (needed=%d): %w", needed, syscall.Errno(code))
			return
		}
		if count > 256 {
			resultErr = fmt.Errorf("invalid process count %d", count)
			return
		}
		for _, process := range processes[:count] {
			observation.Users = append(observation.Users, lifecycleLockUser{process.PID, uint64(process.Started.HighDateTime)<<32 | uint64(process.Started.LowDateTime), syscall.UTF16ToString(process.Application[:]), syscall.UTF16ToString(process.Service[:])})
		}
		return
	}
	resultErr = fmt.Errorf("Restart Manager list kept changing after three queries")
	return
}

func TestLifecycleWindowsLockCapture(t *testing.T) {
	if os.Getenv("KITDB_WAL_LOCK_CAPTURE") != "1" {
		t.Skip("set KITDB_WAL_LOCK_CAPTURE=1 for the diagnostic Restart Manager probe")
	}
	if unsafe.Sizeof(lifecycleRMProcess{}) != 668 {
		t.Fatal("Restart Manager ABI mismatch")
	}
	path := filepath.Join(t.TempDir(), "held.wal")
	if err := os.WriteFile(path, []byte("diagnostic fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := syscall.CreateFile(name, syscall.GENERIC_READ, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE, nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(blocker)
	observation := lifecycleLockUsers([]string{path})
	if observation.Error != "" {
		t.Fatal(observation.Error)
	}
	found := false
	for _, user := range observation.Users {
		if user.PID == uint32(os.Getpid()) && user.StartFiletime != 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("known resource user not reported: %+v", observation)
	}
	for _, paths := range [][]string{nil, {"relative.wal"}, {path, path, path}} {
		if lifecycleLockUsers(paths).Error == "" {
			t.Fatal("invalid lock query accepted")
		}
	}
}
