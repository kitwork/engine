package core

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"
	"runtime/pprof"
	"strconv"
	"time"

	"github.com/kitwork/engine/compiler"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

const DiagnosticSchemaVersion uint16 = 1

// DiagnosticSnapshot is a bounded, detached host snapshot. It deliberately
// excludes roots, hostnames, routes, request identifiers, arguments, source
// text, environment values, and pointers to runtime owners.
type DiagnosticSnapshot struct {
	SchemaVersion uint16                          `json:"schema_version"`
	CapturedAt    time.Time                       `json:"captured_at"`
	Engine        DiagnosticEngineSnapshot        `json:"engine"`
	Compatibility DiagnosticCompatibilitySnapshot `json:"compatibility"`
	Process       DiagnosticProcessSnapshot       `json:"process"`
	Health        work.RuntimeHealthSnapshot      `json:"health"`
}

type DiagnosticEngineSnapshot struct {
	UptimeMilliseconds      uint64 `json:"uptime_ms"`
	MaxEnergy               uint64 `json:"max_energy"`
	IdleTimeoutMillis       int64  `json:"idle_timeout_ms"`
	HotReload               bool   `json:"hot_reload"`
	BytecodeCacheEnabled    bool   `json:"bytecode_cache_enabled"`
	Closed                  bool   `json:"closed"`
	PolicySnapshotAvailable bool   `json:"policy_snapshot_available"`
}

type DiagnosticCompatibilitySnapshot struct {
	BytecodeVersion        uint16                    `json:"bytecode_version"`
	ProgramEncodingVersion uint16                    `json:"program_encoding_version"`
	ArtifactVersion        uint16                    `json:"artifact_version"`
	CompilerSchemaVersion  uint16                    `json:"compiler_schema_version"`
	CompilerFingerprint    string                    `json:"compiler_fingerprint"`
	InstructionSetChecksum string                    `json:"instruction_set_checksum"`
	RuntimeLimits          kitruntime.LimitsSnapshot `json:"runtime_limits"`
}

type DiagnosticProcessSnapshot struct {
	GoVersion  string                   `json:"go_version"`
	OS         string                   `json:"os"`
	Arch       string                   `json:"arch"`
	CPUs       int                      `json:"cpus"`
	MaxProcs   int                      `json:"max_procs"`
	Goroutines int                      `json:"goroutines"`
	Build      DiagnosticBuildSnapshot  `json:"build"`
	Memory     DiagnosticMemorySnapshot `json:"memory"`
	GC         DiagnosticGCSnapshot     `json:"gc"`
}

type DiagnosticBuildSnapshot struct {
	ModulePath    string     `json:"module_path,omitempty"`
	ModuleVersion string     `json:"module_version,omitempty"`
	Revision      string     `json:"revision,omitempty"`
	RevisionTime  *time.Time `json:"revision_time,omitempty"`
	Modified      bool       `json:"modified"`
}

type DiagnosticMemorySnapshot struct {
	AllocBytes        uint64 `json:"alloc_bytes"`
	TotalAllocBytes   uint64 `json:"total_alloc_bytes"`
	SystemBytes       uint64 `json:"system_bytes"`
	HeapAllocBytes    uint64 `json:"heap_alloc_bytes"`
	HeapInuseBytes    uint64 `json:"heap_inuse_bytes"`
	HeapIdleBytes     uint64 `json:"heap_idle_bytes"`
	HeapReleasedBytes uint64 `json:"heap_released_bytes"`
	HeapObjects       uint64 `json:"heap_objects"`
	StackInuseBytes   uint64 `json:"stack_inuse_bytes"`
}

type DiagnosticGCSnapshot struct {
	NextTargetBytes       uint64     `json:"next_target_bytes"`
	LastCollection        *time.Time `json:"last_collection,omitempty"`
	Collections           uint32     `json:"collections"`
	ForcedCollections     uint32     `json:"forced_collections"`
	PauseTotalNanoseconds uint64     `json:"pause_total_ns"`
	LastPauseNanoseconds  uint64     `json:"last_pause_ns"`
	CPUFraction           float64    `json:"cpu_fraction"`
}

type DiagnosticBundleOptions struct {
	// IncludeHeapProfile adds heap.pprof. Generating it is explicit because a
	// heap profile may expose build paths and briefly pauses the process.
	IncludeHeapProfile bool
}

// Diagnostics captures one point-in-time report without forcing a GC.
func (e *Engine) Diagnostics() DiagnosticSnapshot {
	capturedAt := time.Now().UTC()
	snapshot := DiagnosticSnapshot{
		SchemaVersion: DiagnosticSchemaVersion,
		CapturedAt:    capturedAt,
		Compatibility: DiagnosticCompatibilitySnapshot{
			BytecodeVersion:        kitruntime.BytecodeVersion,
			ProgramEncodingVersion: kitruntime.ProgramEncodingVersion,
			ArtifactVersion:        compiler.BytecodeArtifactVersion,
			CompilerSchemaVersion:  compiler.CompilerSchemaVersion,
			CompilerFingerprint:    compiler.Fingerprint(),
			InstructionSetChecksum: kitruntime.InstructionSetChecksum(),
			RuntimeLimits:          kitruntime.Limits(),
		},
		Process: captureDiagnosticProcess(),
	}

	if e == nil {
		snapshot.Engine.PolicySnapshotAvailable = true
		snapshot.Engine.Closed = true
		return snapshot
	}

	snapshot.Health = e.Health()
	policy := DiagnosticEngineSnapshot{}
	enginePolicyAvailable := e.mu.TryRLock()
	if enginePolicyAvailable {
		startedAt := e.startedAt
		policy.MaxEnergy = e.maxEnergy
		policy.IdleTimeoutMillis = e.idleTimeout.Milliseconds()
		policy.HotReload = e.hotReload
		policy.Closed = e.closed
		if !startedAt.IsZero() && !capturedAt.Before(startedAt) {
			policy.UptimeMilliseconds = uint64(capturedAt.Sub(startedAt).Milliseconds())
		}
		e.mu.RUnlock()
	}

	bytecodePolicyAvailable := e.bytecodeCacheMu.TryRLock()
	if bytecodePolicyAvailable {
		policy.BytecodeCacheEnabled = e.bytecodeCacheDir != ""
		e.bytecodeCacheMu.RUnlock()
	}

	if enginePolicyAvailable && bytecodePolicyAvailable {
		policy.PolicySnapshotAvailable = true
		snapshot.Engine = policy
	}
	return snapshot
}

func captureDiagnosticProcess() DiagnosticProcessSnapshot {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)

	lastPause := uint64(0)
	if memory.NumGC > 0 {
		lastPause = memory.PauseNs[(memory.NumGC-1)%uint32(len(memory.PauseNs))]
	}
	var lastCollection *time.Time
	if memory.LastGC != 0 {
		collectedAt := time.Unix(0, int64(memory.LastGC)).UTC()
		lastCollection = &collectedAt
	}

	return DiagnosticProcessSnapshot{
		GoVersion:  runtime.Version(),
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		CPUs:       runtime.NumCPU(),
		MaxProcs:   runtime.GOMAXPROCS(0),
		Goroutines: runtime.NumGoroutine(),
		Build:      captureDiagnosticBuild(),
		Memory: DiagnosticMemorySnapshot{
			AllocBytes:        memory.Alloc,
			TotalAllocBytes:   memory.TotalAlloc,
			SystemBytes:       memory.Sys,
			HeapAllocBytes:    memory.HeapAlloc,
			HeapInuseBytes:    memory.HeapInuse,
			HeapIdleBytes:     memory.HeapIdle,
			HeapReleasedBytes: memory.HeapReleased,
			HeapObjects:       memory.HeapObjects,
			StackInuseBytes:   memory.StackInuse,
		},
		GC: DiagnosticGCSnapshot{
			NextTargetBytes:       memory.NextGC,
			LastCollection:        lastCollection,
			Collections:           memory.NumGC,
			ForcedCollections:     memory.NumForcedGC,
			PauseTotalNanoseconds: memory.PauseTotalNs,
			LastPauseNanoseconds:  lastPause,
			CPUFraction:           memory.GCCPUFraction,
		},
	}
}

func captureDiagnosticBuild() DiagnosticBuildSnapshot {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return DiagnosticBuildSnapshot{}
	}
	build := DiagnosticBuildSnapshot{
		ModulePath:    info.Main.Path,
		ModuleVersion: info.Main.Version,
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			build.Revision = setting.Value
		case "vcs.time":
			if parsed, err := time.Parse(time.RFC3339, setting.Value); err == nil {
				parsed = parsed.UTC()
				build.RevisionTime = &parsed
			}
		case "vcs.modified":
			build.Modified, _ = strconv.ParseBool(setting.Value)
		}
	}
	return build
}

// WriteDiagnosticBundle writes a ZIP archive containing diagnostics.json and,
// only when requested, heap.pprof. The caller owns access control and storage.
func (e *Engine) WriteDiagnosticBundle(
	writer io.Writer,
	options DiagnosticBundleOptions,
) error {
	if writer == nil {
		return fmt.Errorf("diagnostic bundle writer is nil")
	}

	bundle := zip.NewWriter(writer)
	diagnostics, err := bundle.CreateHeader(&zip.FileHeader{
		Name:   "diagnostics.json",
		Method: zip.Deflate,
	})
	if err != nil {
		_ = bundle.Close()
		return fmt.Errorf("create diagnostic snapshot entry: %w", err)
	}
	encoder := json.NewEncoder(diagnostics)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(e.Diagnostics()); err != nil {
		_ = bundle.Close()
		return fmt.Errorf("encode diagnostic snapshot: %w", err)
	}

	if options.IncludeHeapProfile {
		heap, err := bundle.CreateHeader(&zip.FileHeader{
			Name:   "heap.pprof",
			Method: zip.Store,
		})
		if err != nil {
			_ = bundle.Close()
			return fmt.Errorf("create heap profile entry: %w", err)
		}
		if err := pprof.WriteHeapProfile(heap); err != nil {
			_ = bundle.Close()
			return fmt.Errorf("write heap profile: %w", err)
		}
	}

	if err := bundle.Close(); err != nil {
		return fmt.Errorf("close diagnostic bundle: %w", err)
	}
	return nil
}
