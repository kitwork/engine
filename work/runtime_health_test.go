package work

import (
	"testing"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

func TestRuntimeHealthSnapshot(t *testing.T) {
	health := NewRuntimeHealth()
	program := runtime.EmptyProgram()
	health.Record(program, runtime.VMStats{
		Instructions:   12,
		Energy:         34,
		PeakFrameDepth: 2,
	}, value.New("ok"))
	health.Record(program, runtime.VMStats{
		Instructions:   20,
		Energy:         55,
		PeakFrameDepth: 4,
	}, value.Value{
		K:        value.Invalid,
		ErrorVal: &runtime.Diagnostic{Code: runtime.DiagnosticEnergyLimit},
	})
	// Equivalent immutable Programs share one value identity without retaining
	// either Program pointer.
	health.Record(runtime.EmptyProgram(), runtime.VMStats{}, value.New("ok"))

	snapshot := health.Snapshot()
	if snapshot.Executions != 3 ||
		snapshot.Successes != 2 ||
		snapshot.Failures != 1 ||
		snapshot.Programs != 1 {
		t.Fatalf("execution counters = %+v", snapshot)
	}
	if snapshot.Instructions != 32 ||
		snapshot.Energy != 89 ||
		snapshot.MaxInstructions != 20 ||
		snapshot.MaxEnergy != 55 ||
		snapshot.MaxFrameDepth != 4 {
		t.Fatalf("runtime high-water report = %+v", snapshot)
	}
	if snapshot.Diagnostics[runtime.DiagnosticEnergyLimit] != 1 {
		t.Fatalf("diagnostics = %#v", snapshot.Diagnostics)
	}

	snapshot.Diagnostics[runtime.DiagnosticEnergyLimit] = 99
	if health.Snapshot().Diagnostics[runtime.DiagnosticEnergyLimit] != 1 {
		t.Fatal("snapshot exposed mutable health state")
	}
}
