//go:build windows && amd64

package relational

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

// PSS is a documented, current-process-only diagnostic. No VA clone, foreign
// process handle, handle duplication, or database capability is introduced.
var censusKernel = syscall.NewLazyDLL("kernel32.dll")
var censusCapture = censusKernel.NewProc("PssCaptureSnapshot")
var censusFree = censusKernel.NewProc("PssFreeSnapshot")
var censusMarkerCreate = censusKernel.NewProc("PssWalkMarkerCreate")
var censusMarkerFree = censusKernel.NewProc("PssWalkMarkerFree")
var censusWalk = censusKernel.NewProc("PssWalkSnapshot")
var censusQueryObject = syscall.NewLazyDLL("ntdll.dll").NewProc("NtQueryObject")
var censusFinalPath = censusKernel.NewProc("GetFinalPathNameByHandleW")

type censusHandleEntry struct {
	Handle                        uintptr
	Flags, ObjectType             uint32
	CaptureTime                   syscall.Filetime
	Attributes, GrantedAccess     uint32
	HandleCount, PointerCount     uint32
	PagedPoolCharge, NonPagedPool uint32
	CreationTime                  syscall.Filetime
	TypeNameLength                uint16
	TypeName                      *uint16
	ObjectNameLength              uint16
	ObjectName                    *uint16
	Specific                      [6]uintptr
}

type censusThreadEntry struct {
	ExitStatus             uint32
	TEB                    uintptr
	ProcessID, ThreadID    uint32
	Affinity               uintptr
	Priority, BasePriority int32
	Start                  uintptr
}

type handleCensus struct {
	Types               map[string]int     `json:"types"`
	ThreadStarts        map[string]int     `json:"thread_start_functions"`
	FixtureFiles        []string           `json:"fixture_file_names"`
	Total               int                `json:"captured_handles"`
	UnnamedFiles        int                `json:"unnamed_file_handles"`
	Unclassified        int                `json:"unclassified_handles"`
	LiveTypeQueries     int                `json:"live_type_queries"`
	UnresolvedDiskFiles int                `json:"unresolved_disk_file_paths"`
	FileIssues          []string           `json:"unresolved_file_details,omitempty"`
	ProcessBefore       uint32             `json:"process_count_before_capture"`
	ProcessAfter        uint32             `json:"process_count_after_free"`
	Resources           lifecycleResources `json:"after_gc"`
}

func censusString(pointer *uint16, bytes uint16) (string, error) {
	if bytes%2 != 0 || (bytes != 0 && pointer == nil) {
		return "", fmt.Errorf("invalid PSS string")
	}
	if bytes == 0 {
		return "", nil
	}
	return strings.TrimRight(string(utf16.Decode(unsafe.Slice(pointer, int(bytes)/2))), "\x00"), nil
}

// PSS can omit type metadata. These bounded live queries are counted because
// handle reuse means they are not atomic snapshot ownership evidence. Never
// query object names on pipes or inspect another process.
func censusObjectString(handle, class uintptr) string {
	var buffer [65536]byte
	var size uint32
	status, _, _ := censusQueryObject.Call(handle, class, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), uintptr(unsafe.Pointer(&size)))
	if status != 0 {
		return ""
	}
	type unicodeString struct {
		Length, Maximum uint16
		Pointer         *uint16
	}
	name := (*unicodeString)(unsafe.Pointer(&buffer[0]))
	start, end := uintptr(unsafe.Pointer(&buffer[0])), uintptr(unsafe.Pointer(&buffer[0]))+uintptr(len(buffer))
	pointer := uintptr(unsafe.Pointer(name.Pointer))
	if pointer < start || pointer > end || uintptr(name.Length) > end-pointer {
		return ""
	}
	text, _ := censusString(name.Pointer, name.Length)
	runtime.KeepAlive(&buffer)
	return text
}

func censusDiskPath(handle uintptr) (name string, disk, resolved bool, detail string) {
	kind, err := syscall.GetFileType(syscall.Handle(handle))
	if err != nil {
		return "", true, false, fmt.Sprintf("GetFileType(%#x): %v", handle, err)
	}
	if kind != syscall.FILE_TYPE_DISK {
		return "", false, true, ""
	}
	var buffer [32768]uint16
	length, _, callErr := censusFinalPath.Call(handle, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)), 0)
	if length == 0 || length >= uintptr(len(buffer)) {
		// Disk device handles (for example a volume manager) need not have a
		// Win32 final file path. Query names only for this disk classification.
		name := censusObjectString(handle, 1)
		return name, true, name != "", fmt.Sprintf("GetFinalPathName(%#x): %v", handle, callErr)
	}
	return string(utf16.Decode(buffer[:int(length)])), true, true, ""
}

func censusStatus(name string, status uintptr) error {
	if status != 0 {
		return fmt.Errorf("%s: %w", name, syscall.Errno(status))
	}
	return nil
}

func captureHandleCensus(root string) (report handleCensus, resultErr error) {
	if !filepath.IsAbs(root) || filepath.VolumeName(root) == "" {
		return report, fmt.Errorf("census requires an absolute fixture root")
	}
	for _, proc := range []*syscall.LazyProc{censusCapture, censusFree, censusMarkerCreate, censusMarkerFree, censusWalk, censusQueryObject, censusFinalPath} {
		if err := proc.Find(); err != nil {
			return report, err
		}
	}
	var err error
	report.Resources, err = sampleLifecycleResources()
	if err != nil {
		return report, err
	}
	var available bool
	report.ProcessBefore, available = mixedProcessHandles()
	if !available {
		return report, fmt.Errorf("handle count unavailable")
	}
	report.Types, report.ThreadStarts = make(map[string]int), make(map[string]int)
	process := ^uintptr(0) // GetCurrentProcess pseudo-handle; never close it.
	var snapshot, marker uintptr
	// Handles, names, basic and supported type-specific information only.
	status, _, _ := censusCapture.Call(process, 0x04|0x08|0x10|0x20, 0, uintptr(unsafe.Pointer(&snapshot)))
	if err := censusStatus("PssCaptureSnapshot", status); err != nil {
		return report, err
	}
	defer func() {
		status, _, _ := censusFree.Call(process, snapshot)
		resultErr = errors.Join(resultErr, censusStatus("PssFreeSnapshot", status))
		report.ProcessAfter, available = mixedProcessHandles()
		if !available {
			resultErr = errors.Join(resultErr, fmt.Errorf("post-free handle count unavailable"))
		}
	}()
	status, _, _ = censusMarkerCreate.Call(0, uintptr(unsafe.Pointer(&marker)))
	if err := censusStatus("PssWalkMarkerCreate", status); err != nil {
		return report, err
	}
	defer func() {
		status, _, _ := censusMarkerFree.Call(marker)
		resultErr = errors.Join(resultErr, censusStatus("PssWalkMarkerFree", status))
	}()
	suffix := strings.ToLower(filepath.Clean(root)[len(filepath.VolumeName(root)):]) + `\`
	for scanned := 0; scanned < 16384; scanned++ {
		var entry censusHandleEntry
		status, _, _ := censusWalk.Call(snapshot, 2, marker, uintptr(unsafe.Pointer(&entry)), unsafe.Sizeof(entry))
		if status == 259 { // ERROR_NO_MORE_ITEMS
			return report, nil
		}
		if err := censusStatus("PssWalkSnapshot", status); err != nil {
			return report, err
		}
		kind, err := censusString(entry.TypeName, entry.TypeNameLength)
		if err != nil {
			return report, err
		}
		if kind == "" {
			report.LiveTypeQueries++
			kind = censusObjectString(entry.Handle, 2)
		}
		if kind == "" {
			kind = "Unknown"
			report.Unclassified++
		}
		report.Total++
		report.Types[kind]++
		if kind == "File" {
			name, err := censusString(entry.ObjectName, entry.ObjectNameLength)
			if err != nil {
				return report, err
			}
			if entry.Flags&2 == 0 || name == "" {
				report.UnnamedFiles++
				live, disk, resolved, detail := censusDiskPath(entry.Handle)
				if disk && !resolved {
					report.UnresolvedDiskFiles++
					report.FileIssues = append(report.FileIssues, detail)
				}
				if resolved {
					name = live
				}
			}
			if strings.Contains(strings.ToLower(name), suffix) {
				report.FixtureFiles = append(report.FixtureFiles, name)
			}
		}
		if kind == "Thread" && entry.Flags&8 != 0 {
			thread := (*censusThreadEntry)(unsafe.Pointer(&entry.Specific[0]))
			name := "unresolved"
			if function := runtime.FuncForPC(thread.Start); function != nil {
				name = function.Name()
			}
			report.ThreadStarts[name]++
		}
	}
	return report, fmt.Errorf("PSS census exceeds bounded handle count")
}

func TestWindowsHandleCensusControl(t *testing.T) {
	if unsafe.Sizeof(censusHandleEntry{}) != 136 || unsafe.Offsetof(censusHandleEntry{}.Specific) != 88 || unsafe.Sizeof(censusThreadEntry{}) != 48 {
		t.Fatal("PSS x64 ABI layout mismatch")
	}
	root := t.TempDir()
	baseline, err := captureHandleCensus(root)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "held-control.bin"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	open, err := captureHandleCensus(root)
	if err != nil || len(open.FixtureFiles) != 1 || open.Unclassified != 0 || open.Types["File"] != baseline.Types["File"]+1 || !slices.Equal(open.FileIssues, baseline.FileIssues) {
		t.Fatalf("held file not detected: census=%+v error=%v", open, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	for range 4 {
		closed, err := captureHandleCensus(root)
		if err != nil || len(closed.FixtureFiles) != 0 || closed.Unclassified != 0 || closed.Types["File"] != baseline.Types["File"] || !slices.Equal(closed.FileIssues, baseline.FileIssues) {
			t.Fatalf("closed file still detected: census=%+v error=%v", closed, err)
		}
	}
}

// Creates runtime OS-thread pressure without opening KitDB. It is a positive
// control for thread-associated handle growth, not an identical I/O workload.
func censusRuntimePressure() {
	var ready, done sync.WaitGroup
	ready.Add(24)
	done.Add(24)
	release := make(chan struct{})
	for range 24 {
		go func() {
			defer done.Done()
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			ready.Done()
			<-release
		}()
	}
	ready.Wait()
	close(release)
	done.Wait()
}

func TestWindowsHandleCensusCampaign(t *testing.T) {
	if os.Getenv("KITDB_HANDLE_CENSUS") != "1" {
		t.Skip("set KITDB_HANDLE_CENSUS=1 for bounded current-process handle attribution")
	}
	mode := os.Getenv("KITDB_HANDLE_MODE")
	if mode != "runtime" && mode != "database" {
		t.Fatal("KITDB_HANDLE_MODE must be runtime or database")
	}
	file, err := os.OpenFile(os.Getenv("KITDB_HANDLE_REPORT"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			t.Error(err)
		}
	})
	encoder := json.NewEncoder(file)
	root := t.TempDir()
	var baselineIssues []string
	sample := func(phase string, round int) {
		t.Helper()
		census, err := captureHandleCensus(root)
		entry := struct {
			Mode   string       `json:"mode"`
			Phase  string       `json:"phase"`
			Round  int          `json:"round"`
			Census handleCensus `json:"census"`
			Error  string       `json:"error,omitempty"`
		}{Mode: mode, Phase: phase, Round: round, Census: census}
		if err != nil {
			entry.Error = err.Error()
		}
		if writeErr := encoder.Encode(entry); writeErr != nil {
			t.Fatal(writeErr)
		}
		if phase == "initial" {
			baselineIssues = slices.Clone(census.FileIssues)
		}
		if err != nil || len(census.FixtureFiles) != 0 || census.Unclassified != 0 || !slices.Equal(census.FileIssues, baselineIssues) {
			t.Fatalf("incomplete census or retained fixture handle: %+v error=%v", census, err)
		}
		t.Logf("phase=%s round=%d types=%v starts=%v unnamed_files=%d", phase, round, census.Types, census.ThreadStarts, census.UnnamedFiles)
	}
	sample("initial", -1)
	for round := 0; round < 6; round++ {
		if mode == "runtime" {
			censusRuntimePressure()
		} else {
			report, err := runMixedWindow(filepath.Join(root, fmt.Sprintf("round_%d", round)), mixedConfig{Name: "census", Databases: 16, Noisy: round%2 == 1, Duration: 2 * time.Second})
			if err := encoder.Encode(struct {
				Round  int         `json:"round"`
				Window mixedWindow `json:"window"`
			}{round, report}); err != nil {
				t.Fatal(err)
			}
			if err != nil || !report.Success {
				t.Fatalf("database workload: %v", err)
			}
		}
		sample("after_close", round)
	}
	time.Sleep(time.Second)
	sample("idle_1s", 6)
}
