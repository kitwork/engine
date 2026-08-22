package runtime_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	kitruntime "github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

const (
	valuePressureReportSchemaVersion = 1
	valuePressureCampaignEnv         = "KITWORK_VALUE_PRESSURE"
	valuePressureReportEnv           = "KITWORK_VALUE_PRESSURE_REPORT"

	defaultValuePressureRounds        = 48
	defaultValuePressureWarmupRounds  = 6
	defaultValuePressureCheckpoints   = 6
	defaultValuePressureArrayItems    = 4_096
	defaultValuePressureObjectNodes   = 2_048
	defaultValuePressureNativeBytes   = 512 << 10
	defaultValuePressureNativeRecords = 1_024

	valuePressureHeapGrowthAllowance      = 8 << 20
	valuePressureObjectGrowthAllowance    = 25_000
	valuePressureGoroutineGrowthAllowance = 4
)

type valuePressureConfig struct {
	Rounds        int `json:"rounds"`
	WarmupRounds  int `json:"warmup_rounds"`
	Checkpoints   int `json:"checkpoints"`
	ArrayItems    int `json:"array_items"`
	ObjectNodes   int `json:"object_nodes"`
	NativeBytes   int `json:"native_bytes"`
	NativeRecords int `json:"native_records"`
}

type valuePressureGrowth struct {
	HeapGrowthBytes      uint64  `json:"heap_growth_bytes"`
	HeapObjectsGrowth    uint64  `json:"heap_objects_growth"`
	GoroutineGrowth      int     `json:"goroutine_growth"`
	SlopeBytesPerRound   float64 `json:"slope_bytes_per_round"`
	ProjectedGrowthBytes uint64  `json:"projected_growth_bytes"`
}

type valuePressureAllowances struct {
	HeapGrowthBytes           uint64  `json:"heap_growth_bytes"`
	HeapObjectsGrowth         uint64  `json:"heap_objects_growth"`
	GoroutineGrowth           int     `json:"goroutine_growth"`
	SlopeBytesPerRound        float64 `json:"slope_bytes_per_round"`
	SlopeProjectedGrowthBytes uint64  `json:"slope_projected_growth_bytes"`
}

type valuePressureHeapSample struct {
	Round       int    `json:"round"`
	HeapAlloc   uint64 `json:"heap_alloc_bytes"`
	HeapInuse   uint64 `json:"heap_inuse_bytes"`
	HeapObjects uint64 `json:"heap_objects"`
	Goroutines  int    `json:"goroutines"`
}

type valuePressureWorkloadReport struct {
	Name                     string `json:"name"`
	Executions               int    `json:"executions"`
	InputItems               int    `json:"input_items,omitempty"`
	InputBytes               int    `json:"input_bytes,omitempty"`
	ResultKind               string `json:"result_kind"`
	RetainedKind             string `json:"retained_kind"`
	RetainedSize             int    `json:"retained_size"`
	VerifiedMaxStackDepth    int    `json:"verified_max_stack_depth"`
	PeakStackDepth           int    `json:"peak_stack_depth"`
	InstructionsPerExecution uint64 `json:"instructions_per_execution"`
	EnergyPerExecution       uint64 `json:"energy_per_execution"`
	PeakFrameDepth           int    `json:"peak_frame_depth"`
	MaxStackCapacity         int    `json:"max_stack_capacity"`
	TotalDurationNS          int64  `json:"total_duration_ns"`
	TotalAllocatedBytes      uint64 `json:"total_allocated_bytes"`
	TotalMallocs             uint64 `json:"total_mallocs"`
}

type valuePressureReport struct {
	SchemaVersion uint16                        `json:"schema_version"`
	GeneratedAt   time.Time                     `json:"generated_at"`
	GoVersion     string                        `json:"go_version"`
	OS            string                        `json:"os"`
	Arch          string                        `json:"arch"`
	Config        valuePressureConfig           `json:"config"`
	Growth        valuePressureGrowth           `json:"growth"`
	Allowances    valuePressureAllowances       `json:"allowances"`
	Workloads     []valuePressureWorkloadReport `json:"workloads"`
	HeapSamples   []valuePressureHeapSample     `json:"heap_samples"`
	Pool          app.PoolStats                 `json:"pool"`
	Success       bool                          `json:"success"`
	Failure       string                        `json:"failure,omitempty"`
}

type valuePressureObservation struct {
	ResultKind   string
	RetainedKind string
	RetainedSize int
}

type valuePressureWorkload struct {
	name       string
	program    *kitruntime.Program
	globals    func() map[string]value.Value
	validate   func(*kitruntime.VM) (valuePressureObservation, error)
	inputItems int
	inputBytes int
}

type valuePressureExecution struct {
	observation valuePressureObservation
	stats       kitruntime.VMStats
	duration    time.Duration
	allocBytes  uint64
	mallocs     uint64
}

func TestVMValuePressureSmoke(t *testing.T) {
	config := valuePressureConfig{
		Rounds:        4,
		WarmupRounds:  1,
		Checkpoints:   2,
		ArrayItems:    128,
		ObjectNodes:   64,
		NativeBytes:   16 << 10,
		NativeRecords: 32,
	}
	report, err := runVMValuePressure(t, config)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Success || report.Pool.Active != 0 {
		t.Fatalf("value-pressure smoke ended unhealthy: %+v", report.Pool)
	}
}

func TestVMValuePressureCampaign(t *testing.T) {
	if os.Getenv(valuePressureCampaignEnv) != "1" {
		t.Skip("set KITWORK_VALUE_PRESSURE=1 to run the VM value-pressure campaign")
	}

	config := valuePressureConfig{
		Rounds: valuePressureBoundedEnv(
			t, "KITWORK_VALUE_PRESSURE_ROUNDS", defaultValuePressureRounds, 8, 512,
		),
		WarmupRounds: defaultValuePressureWarmupRounds,
		Checkpoints:  defaultValuePressureCheckpoints,
		ArrayItems: valuePressureBoundedEnv(
			t, "KITWORK_VALUE_PRESSURE_ARRAY_ITEMS", defaultValuePressureArrayItems, 64, 32_768,
		),
		ObjectNodes: valuePressureBoundedEnv(
			t, "KITWORK_VALUE_PRESSURE_OBJECT_NODES", defaultValuePressureObjectNodes, 64, 8_192,
		),
		NativeBytes: valuePressureBoundedEnv(
			t, "KITWORK_VALUE_PRESSURE_NATIVE_BYTES", defaultValuePressureNativeBytes, 4<<10, 4<<20,
		),
		NativeRecords: valuePressureBoundedEnv(
			t, "KITWORK_VALUE_PRESSURE_NATIVE_RECORDS", defaultValuePressureNativeRecords, 16, 8_192,
		),
	}

	report, campaignErr := runVMValuePressure(t, config)
	if campaignErr != nil {
		report.Success = false
		report.Failure = campaignErr.Error()
	}
	reportPath := strings.TrimSpace(os.Getenv(valuePressureReportEnv))
	if reportPath == "" {
		reportPath = ".artifacts/value-pressure.json"
	}
	if err := writeValuePressureReport(valuePressureCampaignReportPath(reportPath), report); err != nil {
		if campaignErr != nil {
			t.Fatalf("%v; write value-pressure report: %v", campaignErr, err)
		}
		t.Fatalf("write value-pressure report: %v", err)
	}
	t.Logf("value-pressure evidence written to %s", reportPath)
	if campaignErr != nil {
		t.Fatal(campaignErr)
	}
}

func TestVMValuePressureAllocationEnvelope(t *testing.T) {
	config := valuePressureConfig{
		ArrayItems:    128,
		ObjectNodes:   64,
		NativeBytes:   16 << 10,
		NativeRecords: 32,
	}
	budgets := map[string]float64{
		"array-growth":      24,
		"object-chain":      160,
		"native-buffers":    24,
		"native-collection": 340,
	}

	for _, workload := range compileValuePressureWorkloads(t, config) {
		pool := app.NewPool()
		if _, err := executeValuePressureWorkload(workload, pool, true, false); err != nil {
			t.Fatalf("preflight %s: %v", workload.name, err)
		}
		var runErr error
		allocations := testing.AllocsPerRun(25, func() {
			if runErr != nil {
				return
			}
			_, runErr = executeValuePressureWorkload(workload, pool, false, false)
		})
		if runErr != nil {
			t.Fatalf("measure %s: %v", workload.name, runErr)
		}
		budget := budgets[workload.name]
		if allocations > budget {
			t.Fatalf(
				"%s allocations/op = %.0f, budget %.0f",
				workload.name,
				allocations,
				budget,
			)
		}
		t.Logf("%s allocations/op=%.0f budget=%.0f", workload.name, allocations, budget)
	}
}

func BenchmarkVMValuePressure(b *testing.B) {
	config := valuePressureConfig{
		ArrayItems:    128,
		ObjectNodes:   64,
		NativeBytes:   16 << 10,
		NativeRecords: 32,
	}
	workloads := compileValuePressureWorkloads(b, config)

	for _, workload := range workloads {
		workload := workload
		b.Run(workload.name, func(b *testing.B) {
			pool := app.NewPool()
			if _, err := executeValuePressureWorkload(workload, pool, true, false); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			if workload.inputItems > 0 {
				b.ReportMetric(float64(workload.inputItems), "input_items/op")
			}
			if workload.inputBytes > 0 {
				b.ReportMetric(float64(workload.inputBytes), "input_bytes/op")
			}
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				if _, err := executeValuePressureWorkload(workload, pool, false, false); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func runVMValuePressure(t testing.TB, config valuePressureConfig) (valuePressureReport, error) {
	t.Helper()
	workloads := compileValuePressureWorkloads(t, config)
	pool := app.NewPool()
	report := valuePressureReport{
		SchemaVersion: valuePressureReportSchemaVersion,
		GeneratedAt:   time.Now().UTC(),
		GoVersion:     goruntime.Version(),
		OS:            goruntime.GOOS,
		Arch:          goruntime.GOARCH,
		Config:        config,
		Allowances: valuePressureAllowances{
			HeapGrowthBytes:           valuePressureHeapGrowthAllowance,
			HeapObjectsGrowth:         valuePressureObjectGrowthAllowance,
			GoroutineGrowth:           valuePressureGoroutineGrowthAllowance,
			SlopeBytesPerRound:        64 << 10,
			SlopeProjectedGrowthBytes: 4 << 20,
		},
		Workloads: make([]valuePressureWorkloadReport, len(workloads)),
	}

	for round := 0; round < config.WarmupRounds; round++ {
		for _, workload := range workloads {
			if _, err := executeValuePressureWorkload(workload, pool, true, false); err != nil {
				return report, fmt.Errorf("warmup %d %s: %w", round, workload.name, err)
			}
		}
	}

	report.HeapSamples = append(report.HeapSamples, measureValuePressureHeap(0))
	checkpointEvery := config.Rounds / config.Checkpoints
	if checkpointEvery < 1 {
		checkpointEvery = 1
	}

	for round := 1; round <= config.Rounds; round++ {
		for index, workload := range workloads {
			execution, err := executeValuePressureWorkload(workload, pool, true, true)
			if err != nil {
				return report, fmt.Errorf("round %d %s: %w", round, workload.name, err)
			}
			if err := accumulateValuePressureExecution(
				&report.Workloads[index], workload, execution,
			); err != nil {
				return report, fmt.Errorf("round %d %s: %w", round, workload.name, err)
			}
		}
		if round%checkpointEvery == 0 || round == config.Rounds {
			report.HeapSamples = append(
				report.HeapSamples,
				measureValuePressureHeap(round),
			)
		}
	}

	report.Pool = pool.Stats()
	if report.Pool.Active != 0 {
		return report, fmt.Errorf("pool retained %d active VM leases", report.Pool.Active)
	}
	if report.Pool.Acquired != report.Pool.Released {
		return report, fmt.Errorf(
			"pool lifecycle mismatch: acquired=%d released=%d",
			report.Pool.Acquired,
			report.Pool.Released,
		)
	}
	if report.Pool.Created >= report.Pool.Acquired {
		return report, fmt.Errorf(
			"pool reuse missing: created=%d acquired=%d",
			report.Pool.Created,
			report.Pool.Acquired,
		)
	}

	report.Growth = measureValuePressureGrowth(report.HeapSamples)
	if err := assertValuePressurePlateau(report.Growth); err != nil {
		return report, err
	}
	report.Success = true
	return report, nil
}

func compileValuePressureWorkloads(
	t testing.TB,
	config valuePressureConfig,
) []valuePressureWorkload {
	t.Helper()
	compile := func(name, source string) *kitruntime.Program {
		t.Helper()
		bytecode, err := compiler.CompileSource(source)
		if err != nil {
			t.Fatalf("compile value-pressure workload %s: %v", name, err)
		}
		return bytecode.Program
	}

	arrayProgram := compile("array-growth", fmt.Sprintf(`
var retained = [];
for (let index = 0; index < %d; index++) {
	retained.push(index);
}
var result = retained.length;
`, config.ArrayItems))
	objectProgram := compile("object-chain", fmt.Sprintf(`
var retained = {};
for (let index = 0; index < %d; index++) {
	retained = { index: index, previous: retained };
}
var result = %d;
`, config.ObjectNodes, config.ObjectNodes))
	nativeBufferProgram := compile("native-buffers", `
var retainedText = loadText();
var retainedBytes = loadBytes();
var result = retainedText.length + retainedBytes.length;
`)
	nativeCollectionProgram := compile("native-collection", `
var retained = loadRows();
var result = retained.length;
`)

	return []valuePressureWorkload{
		{
			name:       "array-growth",
			program:    arrayProgram,
			inputItems: config.ArrayItems,
			validate: func(vm *kitruntime.VM) (valuePressureObservation, error) {
				retained := vm.Vars["retained"]
				return validateValuePressureResult(
					vm,
					value.Array,
					retained.Len(),
					config.ArrayItems,
				)
			},
		},
		{
			name:       "object-chain",
			program:    objectProgram,
			inputItems: config.ObjectNodes,
			validate: func(vm *kitruntime.VM) (valuePressureObservation, error) {
				cursor := vm.Vars["retained"]
				for depth := config.ObjectNodes - 1; depth >= 0; depth-- {
					if cursor.K != value.Map || cursor.Get("index").Int() != depth {
						return valuePressureObservation{}, fmt.Errorf(
							"object chain broke at depth %d", depth,
						)
					}
					cursor = cursor.Get("previous")
				}
				return validateValuePressureResult(
					vm,
					value.Map,
					config.ObjectNodes,
					config.ObjectNodes,
				)
			},
		},
		{
			name:       "native-buffers",
			program:    nativeBufferProgram,
			inputBytes: config.NativeBytes * 2,
			globals: func() map[string]value.Value {
				return map[string]value.Value{
					"loadText": value.NewFunc(func(...value.Value) value.Value {
						return value.New(strings.Repeat("x", config.NativeBytes))
					}),
					"loadBytes": value.NewFunc(func(...value.Value) value.Value {
						return value.New(bytes.Repeat([]byte{'x'}, config.NativeBytes))
					}),
				}
			},
			validate: func(vm *kitruntime.VM) (valuePressureObservation, error) {
				text := vm.Vars["retainedText"]
				binary := vm.Vars["retainedBytes"]
				if text.K != value.String || text.Len() != config.NativeBytes {
					return valuePressureObservation{}, fmt.Errorf(
						"native text kind=%s size=%d", text.K, text.Len(),
					)
				}
				if binary.K != value.Bytes || binary.Len() != config.NativeBytes {
					return valuePressureObservation{}, fmt.Errorf(
						"native bytes kind=%s size=%d", binary.K, binary.Len(),
					)
				}
				return validateValuePressureResult(
					vm,
					value.Bytes,
					config.NativeBytes*2,
					config.NativeBytes*2,
				)
			},
		},
		{
			name:       "native-collection",
			program:    nativeCollectionProgram,
			inputItems: config.NativeRecords,
			globals: func() map[string]value.Value {
				return map[string]value.Value{
					"loadRows": value.NewFunc(func(...value.Value) value.Value {
						rows := make([]map[string]any, config.NativeRecords)
						for index := range rows {
							rows[index] = map[string]any{
								"index":  index,
								"active": index%2 == 0,
							}
						}
						return value.New(rows)
					}),
				}
			},
			validate: func(vm *kitruntime.VM) (valuePressureObservation, error) {
				retained := vm.Vars["retained"]
				if retained.K != value.Array || retained.Len() != config.NativeRecords {
					return valuePressureObservation{}, fmt.Errorf(
						"native collection kind=%s size=%d",
						retained.K,
						retained.Len(),
					)
				}
				if config.NativeRecords > 0 {
					last := retained.Index(config.NativeRecords - 1)
					if last.K != value.Map || last.Get("index").Int() != config.NativeRecords-1 {
						return valuePressureObservation{}, fmt.Errorf("native collection tail was not normalized")
					}
				}
				return validateValuePressureResult(
					vm,
					value.Array,
					retained.Len(),
					config.NativeRecords,
				)
			},
		},
	}
}

func validateValuePressureResult(
	vm *kitruntime.VM,
	retainedKind value.Kind,
	retainedSize int,
	want int,
) (valuePressureObservation, error) {
	result := vm.Vars["result"]
	if result.K != value.Number || result.Int() != want {
		return valuePressureObservation{}, fmt.Errorf(
			"result kind=%s value=%s, want number %d",
			result.K,
			result.Text(),
			want,
		)
	}
	return valuePressureObservation{
		ResultKind:   result.K.String(),
		RetainedKind: retainedKind.String(),
		RetainedSize: retainedSize,
	}, nil
}

func executeValuePressureWorkload(
	workload valuePressureWorkload,
	pool *app.Pool,
	validate bool,
	measure bool,
) (valuePressureExecution, error) {
	var before goruntime.MemStats
	if measure {
		goruntime.ReadMemStats(&before)
	}
	startedAt := time.Now()
	vm := pool.Acquire()
	globals := map[string]value.Value(nil)
	if workload.globals != nil {
		globals = workload.globals()
	}
	vm.PrepareHostState(globals, nil)
	vm.FastResetPrepared(workload.program)
	vm.MaxEnergy = kitruntime.Limits().DefaultMaxEnergy

	result := vm.Run()
	stats := vm.Stats()
	var observation valuePressureObservation
	var executionErr error
	if result.K == value.Invalid {
		executionErr = fmt.Errorf("VM failed: %s", result.Text())
	} else if validate {
		observation, executionErr = workload.validate(vm)
	}
	pool.Release(vm)
	if ownerErr := valuePressureReleasedOwners(vm); executionErr == nil && ownerErr != nil {
		executionErr = ownerErr
	}
	duration := time.Since(startedAt)

	var allocBytes, mallocs uint64
	if measure {
		var after goruntime.MemStats
		goruntime.ReadMemStats(&after)
		allocBytes = after.TotalAlloc - before.TotalAlloc
		mallocs = after.Mallocs - before.Mallocs
	}
	return valuePressureExecution{
		observation: observation,
		stats:       stats,
		duration:    duration,
		allocBytes:  allocBytes,
		mallocs:     mallocs,
	}, executionErr
}

func valuePressureReleasedOwners(vm *kitruntime.VM) error {
	if vm.Program() != nil {
		return fmt.Errorf("pooled VM retained its Program")
	}
	if vm.Context != nil || vm.Globals != nil || vm.Builtins != nil || vm.Spawner != nil {
		return fmt.Errorf("pooled VM retained request or host ownership")
	}
	if len(vm.Stack) != 0 || len(vm.Vars) != 0 {
		return fmt.Errorf("pooled VM retained stack or root variables")
	}
	for index, item := range vm.Stack[:cap(vm.Stack)] {
		if item.K != value.Invalid || item.N != 0 || item.V != nil ||
			item.IsError || item.ErrorVal != nil || item.Raw {
			return fmt.Errorf("pooled VM stack storage retained slot %d", index)
		}
	}
	if vm.FrameIdx != 0 {
		return fmt.Errorf("pooled VM frame index=%d, want 0", vm.FrameIdx)
	}
	for index := range vm.Frames {
		frame := &vm.Frames[index]
		if frame.Fn != nil || len(frame.Vars) != 0 || len(frame.Defers) != 0 {
			return fmt.Errorf("frame %d retained execution owners", index)
		}
	}
	return nil
}

func accumulateValuePressureExecution(
	report *valuePressureWorkloadReport,
	workload valuePressureWorkload,
	execution valuePressureExecution,
) error {
	if report.Executions == 0 {
		profile := workload.program.Profile()
		report.Name = workload.name
		report.InputItems = workload.inputItems
		report.InputBytes = workload.inputBytes
		report.ResultKind = execution.observation.ResultKind
		report.RetainedKind = execution.observation.RetainedKind
		report.RetainedSize = execution.observation.RetainedSize
		report.VerifiedMaxStackDepth = profile.MaxStackDepth
		report.InstructionsPerExecution = execution.stats.Instructions
		report.EnergyPerExecution = execution.stats.Energy
	} else {
		if report.InstructionsPerExecution != execution.stats.Instructions {
			return fmt.Errorf(
				"instruction count changed from %d to %d",
				report.InstructionsPerExecution,
				execution.stats.Instructions,
			)
		}
		if report.EnergyPerExecution != execution.stats.Energy {
			return fmt.Errorf(
				"energy changed from %d to %d",
				report.EnergyPerExecution,
				execution.stats.Energy,
			)
		}
		if report.ResultKind != execution.observation.ResultKind ||
			report.RetainedKind != execution.observation.RetainedKind ||
			report.RetainedSize != execution.observation.RetainedSize {
			return fmt.Errorf("result shape changed across executions")
		}
	}

	report.Executions++
	report.TotalDurationNS += execution.duration.Nanoseconds()
	report.TotalAllocatedBytes += execution.allocBytes
	report.TotalMallocs += execution.mallocs
	if execution.stats.PeakFrameDepth > report.PeakFrameDepth {
		report.PeakFrameDepth = execution.stats.PeakFrameDepth
	}
	if execution.stats.PeakStackDepth > report.PeakStackDepth {
		report.PeakStackDepth = execution.stats.PeakStackDepth
	}
	if execution.stats.StackCapacity > report.MaxStackCapacity {
		report.MaxStackCapacity = execution.stats.StackCapacity
	}
	return nil
}

func measureValuePressureHeap(round int) valuePressureHeapSample {
	goruntime.GC()
	goruntime.GC()
	debug.FreeOSMemory()
	var stats goruntime.MemStats
	goruntime.ReadMemStats(&stats)
	return valuePressureHeapSample{
		Round:       round,
		HeapAlloc:   stats.HeapAlloc,
		HeapInuse:   stats.HeapInuse,
		HeapObjects: stats.HeapObjects,
		Goroutines:  goruntime.NumGoroutine(),
	}
}

func measureValuePressureGrowth(samples []valuePressureHeapSample) valuePressureGrowth {
	if len(samples) < 2 {
		return valuePressureGrowth{}
	}
	first := samples[0]
	last := samples[len(samples)-1]
	slope := valuePressureSlope(samples)
	projected := slope * float64(last.Round-first.Round)
	if projected < 0 {
		projected = 0
	}
	return valuePressureGrowth{
		HeapGrowthBytes:      positiveValuePressureDelta(last.HeapAlloc, first.HeapAlloc),
		HeapObjectsGrowth:    positiveValuePressureDelta(last.HeapObjects, first.HeapObjects),
		GoroutineGrowth:      last.Goroutines - first.Goroutines,
		SlopeBytesPerRound:   slope,
		ProjectedGrowthBytes: uint64(projected),
	}
}

func assertValuePressurePlateau(growth valuePressureGrowth) error {
	if growth.HeapGrowthBytes > valuePressureHeapGrowthAllowance {
		return fmt.Errorf(
			"post-GC heap grew by %d bytes; allowance is %d",
			growth.HeapGrowthBytes,
			valuePressureHeapGrowthAllowance,
		)
	}
	if growth.HeapObjectsGrowth > valuePressureObjectGrowthAllowance {
		return fmt.Errorf(
			"post-GC heap objects grew by %d; allowance is %d",
			growth.HeapObjectsGrowth,
			valuePressureObjectGrowthAllowance,
		)
	}
	if growth.SlopeBytesPerRound > 64<<10 && growth.ProjectedGrowthBytes > 4<<20 {
		return fmt.Errorf(
			"post-GC heap slope is %.2f KiB/round with %d projected bytes",
			growth.SlopeBytesPerRound/(1<<10),
			growth.ProjectedGrowthBytes,
		)
	}
	if growth.GoroutineGrowth > valuePressureGoroutineGrowthAllowance {
		return fmt.Errorf(
			"goroutines grew by %d; allowance is %d",
			growth.GoroutineGrowth,
			valuePressureGoroutineGrowthAllowance,
		)
	}
	return nil
}

func valuePressureSlope(samples []valuePressureHeapSample) float64 {
	if len(samples) < 2 {
		return 0
	}
	var sumX, sumY float64
	for _, sample := range samples {
		sumX += float64(sample.Round)
		sumY += float64(sample.HeapAlloc)
	}
	meanX := sumX / float64(len(samples))
	meanY := sumY / float64(len(samples))
	var numerator, denominator float64
	for _, sample := range samples {
		x := float64(sample.Round) - meanX
		numerator += x * (float64(sample.HeapAlloc) - meanY)
		denominator += x * x
	}
	if denominator == 0 {
		return 0
	}
	return numerator / denominator
}

func positiveValuePressureDelta(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func valuePressureBoundedEnv(
	t testing.TB,
	name string,
	fallback, minimum, maximum int,
) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil || parsed < minimum || parsed > maximum {
		t.Fatalf("%s must be between %d and %d, got %q", name, minimum, maximum, raw)
	}
	return parsed
}

func writeValuePressureReport(path string, report valuePressureReport) error {
	if directory := filepath.Dir(path); directory != "." && directory != "" {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func valuePressureCampaignReportPath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	_, sourceFile, _, ok := goruntime.Caller(0)
	if !ok {
		return path
	}
	return filepath.Join(filepath.Dir(filepath.Dir(sourceFile)), path)
}
