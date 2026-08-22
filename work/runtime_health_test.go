package work

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/site"
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

func TestRuntimeHealthGenerationLifecycleGauges(t *testing.T) {
	health := NewRuntimeHealth()

	finishPrepare := health.BeginGenerationPrepare()
	finishActivate := health.BeginGenerationActivate()
	snapshot := health.Snapshot()
	if snapshot.Generations.Preparing != 1 || snapshot.Generations.Activating != 1 {
		t.Fatalf("in-progress generation gauges = %+v", snapshot.Generations)
	}

	finishPrepare(false)
	finishPrepare(true)
	finishActivate(true)
	finishActivate(false)
	snapshot = health.Snapshot()
	if snapshot.Generations.Preparing != 0 ||
		snapshot.Generations.Activating != 0 ||
		snapshot.Generations.PrepareFailures != 1 ||
		snapshot.Generations.Activated != 1 ||
		snapshot.Latencies.GenerationPrepare.Count != 1 ||
		snapshot.Latencies.GenerationActivate.Count != 1 {
		t.Fatalf("completed generation gauges = %+v latencies=%+v", snapshot.Generations, snapshot.Latencies)
	}

	siteRuntime := site.NewRuntime(nil, "", "")
	generation, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	firstLease, firstOK := generation.Acquire()
	secondLease, secondOK := generation.Acquire()
	if !firstOK || !secondOK {
		t.Fatal("could not acquire generation health test leases")
	}
	finishDrain := health.BeginGenerationDrain(generation)
	finishDuplicateDrain := health.BeginGenerationDrain(generation)
	time.Sleep(time.Millisecond)
	snapshot = health.Snapshot()
	if snapshot.Generations.Draining != 1 ||
		snapshot.Generations.DrainingLeases != 2 ||
		snapshot.Generations.OldestDrainNanoseconds == 0 ||
		snapshot.Generations.MaxOldestDrainNanoseconds < snapshot.Generations.OldestDrainNanoseconds {
		t.Fatalf("draining generation gauges = %+v", snapshot.Generations)
	}

	firstLease.Release()
	secondLease.Release()
	finishDrain(true)
	finishDrain(false)
	finishDuplicateDrain(true)
	generation.Retire()
	snapshot = health.Snapshot()
	if snapshot.Generations.Draining != 0 ||
		snapshot.Generations.DrainingLeases != 0 ||
		snapshot.Generations.OldestDrainNanoseconds != 0 ||
		snapshot.Generations.Drained != 1 ||
		snapshot.Generations.DrainFailures != 0 ||
		snapshot.Latencies.GenerationDrain.Count != 1 ||
		snapshot.Generations.MaxOldestDrainNanoseconds == 0 {
		t.Fatalf("completed drain gauges = %+v latency=%+v", snapshot.Generations, snapshot.Latencies.GenerationDrain)
	}

	failedGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	finishFailedDrain := health.BeginGenerationDrain(failedGeneration)
	finishFailedDrain(false)
	finishFailedDrain(true)
	failedGeneration.Retire()
	snapshot = health.Snapshot()
	if snapshot.Generations.Drained != 1 ||
		snapshot.Generations.DrainFailures != 1 ||
		snapshot.Latencies.GenerationDrain.Count != 2 {
		t.Fatalf("idempotent failed drain outcome = %+v latency=%+v", snapshot.Generations, snapshot.Latencies.GenerationDrain)
	}

	reobservedGeneration, err := siteRuntime.PrepareGeneration()
	if err != nil {
		t.Fatal(err)
	}
	health.BeginGenerationDrain(reobservedGeneration)(true)
	health.BeginGenerationDrain(reobservedGeneration)(true)
	reobservedGeneration.Retire()
	snapshot = health.Snapshot()
	if snapshot.Generations.Drained != 3 ||
		snapshot.Generations.DrainFailures != 1 ||
		snapshot.Latencies.GenerationDrain.Count != 4 {
		t.Fatalf("reobserved drain attempts = %+v latency=%+v", snapshot.Generations, snapshot.Latencies.GenerationDrain)
	}
}

func TestRuntimeHealthCapsObservedProgramIdentities(t *testing.T) {
	health := NewRuntimeHealth()
	for index := 0; index < maxObservedPrograms; index++ {
		var digest [32]byte
		binary.LittleEndian.PutUint64(digest[:], uint64(index+1))
		health.programs[digest] = struct{}{}
	}

	health.Record(runtime.EmptyProgram(), runtime.VMStats{}, value.New("ok"))
	snapshot := health.Snapshot()
	if snapshot.Programs != maxObservedPrograms || snapshot.ProgramsDropped != 1 {
		t.Fatalf("bounded program identities = %+v", snapshot)
	}
}

func TestRuntimeHealthOperationalSignalsAreBoundedSnapshots(t *testing.T) {
	health := NewRuntimeHealth()

	health.RequestStarted()
	health.RequestCompleted(200, 80*time.Microsecond)
	health.RequestStarted()
	health.RequestCompleted(404, 2*time.Millisecond)
	health.RequestStarted()
	health.RequestCompleted(503, 300*time.Millisecond)
	health.RecordResolve(120 * time.Microsecond)
	health.RecordVMLatency(300 * time.Microsecond)
	health.RecordRender(600*time.Microsecond, true)
	health.RecordRender(3*time.Millisecond, false)
	health.RecordResponseCache(true)
	health.RecordResponseCache(false)
	health.RecordGenerationPrepare(6*time.Millisecond, true)
	health.RecordGenerationPrepare(7*time.Millisecond, false)
	health.RecordGenerationActivate(20*time.Microsecond, true)
	health.RecordGenerationActivate(30*time.Microsecond, false)
	health.RecordGenerationDrain(9 * time.Millisecond)

	snapshot := health.Snapshot()
	if snapshot.Requests.Started != 3 ||
		snapshot.Requests.Completed != 3 ||
		snapshot.Requests.Successful != 1 ||
		snapshot.Requests.ClientErrors != 1 ||
		snapshot.Requests.ServerErrors != 1 ||
		snapshot.Requests.Inflight != 0 ||
		snapshot.Requests.MaxInflight != 1 {
		t.Fatalf("request health = %+v", snapshot.Requests)
	}
	if snapshot.Presentation.PreparedRenders != 1 || snapshot.Presentation.FallbackRenders != 1 {
		t.Fatalf("presentation health = %+v", snapshot.Presentation)
	}
	if snapshot.ResponseCache.Hits != 1 || snapshot.ResponseCache.Misses != 1 {
		t.Fatalf("response cache health = %+v", snapshot.ResponseCache)
	}
	if snapshot.Generations.Prepared != 1 ||
		snapshot.Generations.PrepareFailures != 1 ||
		snapshot.Generations.Activated != 1 ||
		snapshot.Generations.ActivateFailures != 1 ||
		snapshot.Generations.Drained != 1 ||
		snapshot.Generations.DrainFailures != 0 {
		t.Fatalf("generation health = %+v", snapshot.Generations)
	}
	if snapshot.Latencies.Request.Count != 3 ||
		snapshot.Latencies.Request.Overflow != 1 ||
		snapshot.Latencies.Resolve.Count != 1 ||
		snapshot.Latencies.VM.Count != 1 ||
		snapshot.Latencies.Render.Count != 2 {
		t.Fatalf("latency health = %+v", snapshot.Latencies)
	}
	if len(snapshot.Latencies.Request.Buckets) != latencyBucketCount {
		t.Fatalf("request bucket count = %d", len(snapshot.Latencies.Request.Buckets))
	}

	snapshot.Latencies.Request.Buckets[0].Count = 999
	if health.Snapshot().Latencies.Request.Buckets[0].Count == 999 {
		t.Fatal("latency snapshot exposed mutable health state")
	}
}
