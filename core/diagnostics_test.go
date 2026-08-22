package core

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/runtime"
)

func TestEngineDiagnosticsIsBoundedDetachedAndPrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "diagnostic-root-private-sentinel")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTreeTenant(t, root, "diagnostic-private-marker")
	engine := New(root, 123_456, true, "")
	t.Cleanup(engine.Close)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodGet,
		"http://localhost/?token=private-token",
		nil,
	)
	request.Header.Set("X-Diagnostic-Probe", "private-request")
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("diagnostic fixture response = %d %q", response.Code, response.Body.String())
	}
	engine.Hostname = "private.example"

	snapshot := engine.Diagnostics()
	if snapshot.SchemaVersion != DiagnosticSchemaVersion || snapshot.CapturedAt.IsZero() {
		t.Fatalf("diagnostic envelope = %+v", snapshot)
	}
	if !snapshot.Engine.PolicySnapshotAvailable ||
		snapshot.Engine.MaxEnergy != 123_456 ||
		!snapshot.Engine.HotReload ||
		snapshot.Engine.Closed {
		t.Fatalf("engine snapshot = %+v", snapshot.Engine)
	}
	if snapshot.Compatibility.BytecodeVersion != runtime.BytecodeVersion ||
		snapshot.Compatibility.InstructionSetChecksum == "" ||
		snapshot.Compatibility.RuntimeLimits != runtime.Limits() {
		t.Fatalf("compatibility snapshot = %+v", snapshot.Compatibility)
	}
	if snapshot.Process.GoVersion == "" ||
		snapshot.Process.OS == "" ||
		snapshot.Process.Arch == "" ||
		snapshot.Process.CPUs <= 0 ||
		snapshot.Process.Goroutines <= 0 ||
		snapshot.Process.Memory.HeapAllocBytes == 0 {
		t.Fatalf("process snapshot = %+v", snapshot.Process)
	}
	if snapshot.Health.Requests.Started != 1 || snapshot.Health.Requests.Completed != 1 {
		t.Fatalf("request health = %+v", snapshot.Health.Requests)
	}
	if snapshot.Health.VMPool.Active != 0 ||
		snapshot.Health.VMPool.Acquired == 0 ||
		snapshot.Health.VMPool.Released == 0 {
		t.Fatalf("VM pool health = %+v", snapshot.Health.VMPool)
	}

	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(encoded) > 64<<10 {
		t.Fatalf("diagnostic snapshot is unexpectedly large: %d bytes", len(encoded))
	}
	text := string(encoded)
	for _, private := range []string{
		filepath.Base(root),
		"private.example",
		"localhost",
		"private-request",
		"private-token",
		"diagnostic-private-marker",
	} {
		if strings.Contains(text, private) {
			t.Fatalf("diagnostic snapshot exposed private value %q", private)
		}
	}

	snapshot.Health.Latencies.Request.Buckets[0].Count = 99
	if engine.Diagnostics().Health.Latencies.Request.Buckets[0].Count == 99 {
		t.Fatal("diagnostic snapshot retained mutable health storage")
	}
}

func TestEngineDiagnosticsPolicyContentionIsNonblocking(t *testing.T) {
	t.Run("engine ownership", func(t *testing.T) {
		engine := New(t.TempDir(), 789_123, true, "")
		engine.SetBytecodeCache("enabled-cache")
		t.Cleanup(engine.Close)

		finishActivate := engine.runtimeHealth.BeginGenerationActivate()
		finished := false
		locked := false
		t.Cleanup(func() {
			if locked {
				engine.mu.Unlock()
			}
			if !finished {
				finishActivate(false)
			}
		})
		engine.mu.Lock()
		locked = true
		result := make(chan DiagnosticSnapshot, 1)
		go func() {
			result <- engine.Diagnostics()
		}()

		var unavailable DiagnosticSnapshot
		select {
		case unavailable = <-result:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Diagnostics blocked on the engine ownership lock")
		}
		if unavailable.Engine.PolicySnapshotAvailable ||
			unavailable.Engine.UptimeMilliseconds != 0 ||
			unavailable.Engine.MaxEnergy != 0 ||
			unavailable.Engine.IdleTimeoutMillis != 0 ||
			unavailable.Engine.HotReload ||
			unavailable.Engine.BytecodeCacheEnabled ||
			unavailable.Engine.Closed ||
			unavailable.Health.OwnershipSnapshotAvailable ||
			unavailable.Health.Generations.Activating != 1 {
			t.Fatalf("engine-lock diagnostic snapshot = %+v", unavailable)
		}

		finishActivate(true)
		finished = true
		engine.mu.Unlock()
		locked = false
		available := engine.Diagnostics()
		if !available.Engine.PolicySnapshotAvailable ||
			available.Engine.MaxEnergy != 789_123 ||
			!available.Engine.HotReload ||
			!available.Engine.BytecodeCacheEnabled ||
			!available.Health.OwnershipSnapshotAvailable ||
			available.Health.Generations.Activating != 0 {
			t.Fatalf("available diagnostic snapshot = %+v", available)
		}
	})

	t.Run("bytecode policy", func(t *testing.T) {
		engine := New(t.TempDir(), 456_789, true, "")
		engine.SetBytecodeCache("enabled-cache")
		t.Cleanup(engine.Close)

		locked := false
		t.Cleanup(func() {
			if locked {
				engine.bytecodeCacheMu.Unlock()
			}
		})
		engine.bytecodeCacheMu.Lock()
		locked = true
		result := make(chan DiagnosticSnapshot, 1)
		go func() {
			result <- engine.Diagnostics()
		}()

		var unavailable DiagnosticSnapshot
		select {
		case unavailable = <-result:
		case <-time.After(500 * time.Millisecond):
			t.Fatal("Diagnostics blocked on the bytecode policy lock")
		}
		if unavailable.Engine.PolicySnapshotAvailable ||
			unavailable.Engine.UptimeMilliseconds != 0 ||
			unavailable.Engine.MaxEnergy != 0 ||
			unavailable.Engine.IdleTimeoutMillis != 0 ||
			unavailable.Engine.HotReload ||
			unavailable.Engine.BytecodeCacheEnabled ||
			unavailable.Engine.Closed ||
			!unavailable.Health.OwnershipSnapshotAvailable {
			t.Fatalf("bytecode-lock diagnostic snapshot = %+v", unavailable)
		}

		engine.bytecodeCacheMu.Unlock()
		locked = false
		available := engine.Diagnostics()
		if !available.Engine.PolicySnapshotAvailable ||
			available.Engine.MaxEnergy != 456_789 ||
			!available.Engine.HotReload ||
			!available.Engine.BytecodeCacheEnabled ||
			!available.Health.OwnershipSnapshotAvailable {
			t.Fatalf("available bytecode diagnostic snapshot = %+v", available)
		}
	})
}

func TestEngineDiagnosticBundleContainsSnapshotAndOptionalHeapProfile(t *testing.T) {
	engine := New(t.TempDir(), 0, false, "")
	t.Cleanup(engine.Close)

	var snapshotOnly bytes.Buffer
	if err := engine.WriteDiagnosticBundle(
		&snapshotOnly,
		DiagnosticBundleOptions{},
	); err != nil {
		t.Fatal(err)
	}
	plainArchive, err := zip.NewReader(
		bytes.NewReader(snapshotOnly.Bytes()),
		int64(snapshotOnly.Len()),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(plainArchive.File) != 1 || plainArchive.File[0].Name != "diagnostics.json" {
		t.Fatalf("snapshot-only entries = %+v", diagnosticBundleNames(plainArchive.File))
	}

	var output bytes.Buffer
	if err := engine.WriteDiagnosticBundle(
		&output,
		DiagnosticBundleOptions{IncludeHeapProfile: true},
	); err != nil {
		t.Fatal(err)
	}

	archive, err := zip.NewReader(bytes.NewReader(output.Bytes()), int64(output.Len()))
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.File) != 2 ||
		archive.File[0].Name != "diagnostics.json" ||
		archive.File[1].Name != "heap.pprof" {
		t.Fatalf("bundle entries = %+v", diagnosticBundleNames(archive.File))
	}

	diagnostics, err := archive.File[0].Open()
	if err != nil {
		t.Fatal(err)
	}
	var snapshot DiagnosticSnapshot
	decodeErr := json.NewDecoder(diagnostics).Decode(&snapshot)
	closeErr := diagnostics.Close()
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if snapshot.SchemaVersion != DiagnosticSchemaVersion {
		t.Fatalf("bundle schema version = %d", snapshot.SchemaVersion)
	}

	heap, err := archive.File[1].Open()
	if err != nil {
		t.Fatal(err)
	}
	heapBytes, readErr := io.ReadAll(heap)
	closeErr = heap.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if len(heapBytes) == 0 {
		t.Fatal("heap profile entry is empty")
	}
}

func TestEngineDiagnosticBundleRejectsNilWriter(t *testing.T) {
	engine := New(t.TempDir(), 0, false, "")
	t.Cleanup(engine.Close)
	if err := engine.WriteDiagnosticBundle(nil, DiagnosticBundleOptions{}); err == nil {
		t.Fatal("nil diagnostic bundle writer was accepted")
	}
}

func TestClosedEngineDiagnosticsReportsDrainedOwnership(t *testing.T) {
	root := t.TempDir()
	writeTreeTenant(t, root, "healthy")
	engine := New(root, 0, false, "")
	response := httptest.NewRecorder()
	engine.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "http://localhost/", nil),
	)
	engine.Close()

	snapshot := engine.Diagnostics()
	if !snapshot.Engine.PolicySnapshotAvailable ||
		!snapshot.Engine.Closed ||
		!snapshot.Health.OwnershipSnapshotAvailable ||
		snapshot.Health.LoadedApps != 0 ||
		snapshot.Health.LoadedSites != 0 ||
		snapshot.Health.ActiveGenerations != 0 ||
		snapshot.Health.ActiveGenerationLeases != 0 {
		t.Fatalf("closed diagnostic snapshot = %+v", snapshot)
	}
}

func diagnosticBundleNames(files []*zip.File) []string {
	names := make([]string, len(files))
	for index, file := range files {
		names[index] = file.Name
	}
	return names
}
