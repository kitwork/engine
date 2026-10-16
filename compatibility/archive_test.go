package compatibility

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/runtime"
)

// currentArchive is the archive of this runtime's bytecode version (testdata/v3 since 14/10/2026,
// testdata/v4 since 15/10/2026).
var currentArchive = filepath.Join("testdata", "v4", "manifest.json")

func TestVMCompatibilityArchive(t *testing.T) {
	if err := VerifyArchive(currentArchive); err != nil {
		t.Fatal(err)
	}
}

// Bytecode 4 refuses bytecode 2's and bytecode 3's Programs by their version before anything reads their
// code: a runtime takes only the version it is. The v2 and v3 archives stay as those versions' evidence.
func TestVMCompatibilityArchiveOfBytecode2IsRefusedByItsVersion(t *testing.T) {
	refusedByItsVersion(t, 2)
}

func TestVMCompatibilityArchiveOfBytecode3IsRefusedByItsVersion(t *testing.T) {
	refusedByItsVersion(t, 3)
}

func refusedByItsVersion(t *testing.T, version int) {
	t.Helper()
	path := filepath.Join("testdata", fmt.Sprintf("v%d", version), "manifest.json")
	if err := VerifyArchive(path); err == nil ||
		!strings.Contains(err.Error(), fmt.Sprintf(`archive = "vm-v%d", want vm-v%d`, version, runtime.BytecodeVersion)) {
		t.Fatalf("v%d archive error = %v", version, err)
	}
	manifest, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range manifest.Cases {
		encoded, err := os.ReadFile(filepath.Join(filepath.Dir(path), filepath.FromSlash(fixture.Program)))
		if err != nil {
			t.Fatal(err)
		}
		if digest(encoded) != fixture.ProgramSHA256 {
			t.Fatalf("%s: the v%d evidence changed", fixture.Name, version)
		}
		if _, err := runtime.UnmarshalProgram(encoded); err == nil ||
			!strings.Contains(err.Error(), fmt.Sprintf("bytecode version %d is incompatible with %d", version, runtime.BytecodeVersion)) {
			t.Fatalf("%s: decode error = %v", fixture.Name, err)
		}
	}
}

// STEP costs what the PUSH 1; ADD it replaced cost (bytecode 4, 15/10/2026): every case the v3 archive
// holds spends, compiled by bytecode 4, the energy it spent there, and runs no more instructions. The
// evidence of both archives says it. CONTROL: the case only v4 has runs STEP.
func TestBytecode4SpendsTheEnergyBytecode3Spent(t *testing.T) {
	v3, err := ReadManifest(filepath.Join("testdata", "v3", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	v4, err := ReadManifest(currentArchive)
	if err != nil {
		t.Fatal(err)
	}
	now := map[string]Case{}
	for _, fixture := range v4.Cases {
		now[fixture.Name] = fixture
	}
	stepped := 0
	for _, before := range v3.Cases {
		after, ok := now[before.Name]
		if !ok {
			t.Fatalf("%s: the v4 archive lacks a case of v3", before.Name)
		}
		if after.Execution.Energy != before.Execution.Energy || after.Execution.Instructions > before.Execution.Instructions {
			t.Errorf("%s: v4 spends %d energy in %d instructions, v3 %d in %d", before.Name,
				after.Execution.Energy, after.Execution.Instructions, before.Execution.Energy, before.Execution.Instructions)
		}
		if after.Execution.Instructions < before.Execution.Instructions {
			stepped++
		}
	}
	if stepped == 0 {
		t.Error("no case of v3 runs fewer instructions in v4: none steps a counter")
	}
	runsStep := false
	for _, opcode := range now["step"].Profile.Opcodes {
		runsStep = runsStep || opcode.Name == "STEP"
	}
	if !runsStep {
		t.Error("CONTROL: the step case holds no STEP")
	}
}

func TestVMCompatibilityArchiveAcrossReusedAndPooledVMs(t *testing.T) {
	path := currentArchive
	manifest, err := ReadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(path)
	if err := validateManifest(root, manifest, true); err != nil {
		t.Fatal(err)
	}

	var reused *runtime.VM
	pool := app.NewPool()
	for _, fixture := range manifest.Cases {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			program, baseline, err := verifyCase(root, fixture)
			if err != nil {
				t.Fatal(err)
			}
			if reused == nil {
				reused = runtime.New(program)
			} else {
				reused.FastReset(program, nil)
			}
			reused.MaxEnergy = fixture.MaxEnergy
			got, err := executeLoadedVM(reused)
			if err != nil {
				t.Fatal(err)
			}
			if err := compareExecution(baseline, got); err != nil {
				t.Fatalf("reused VM: %v", err)
			}

			lease := pool.Acquire()
			lease.FastReset(program, nil)
			lease.MaxEnergy = fixture.MaxEnergy
			got, err = executeLoadedVM(lease)
			pool.Release(lease)
			if err != nil {
				t.Fatal(err)
			}
			if err := compareExecution(baseline, got); err != nil {
				t.Fatalf("pooled VM: %v", err)
			}
		})
	}
	if active := pool.Active(); active != 0 {
		t.Fatalf("compatibility archive retained %d active VM leases", active)
	}
}

func TestVMCompatibilityArchiveRejectsTamperedProgram(t *testing.T) {
	manifestPath := currentArchive
	manifest, err := ReadManifest(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	fixture := manifest.Cases[0]
	root := filepath.Dir(manifestPath)
	programPath, err := resolvePath(root, fixture.Program)
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(programPath)
	if err != nil {
		t.Fatal(err)
	}
	program[len(program)-1] ^= 0xff
	temporaryRoot := t.TempDir()
	fixture.Program = "tampered.kwpb"
	fixture.ProgramSHA256 = digest(program)
	if err := os.WriteFile(filepath.Join(temporaryRoot, fixture.Program), program, 0o644); err != nil {
		t.Fatal(err)
	}
	fixture.Sources = nil
	if _, _, err := verifyCase(temporaryRoot, fixture); err == nil ||
		!strings.Contains(err.Error(), "decode Program") {
		t.Fatalf("tampered Program error = %v", err)
	}
}

func TestVMCompatibilityManifestRejectsPathEscape(t *testing.T) {
	manifest, err := ReadManifest(currentArchive)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Cases[0].Program = "../escape.kwpb"
	if err := validateManifest(t.TempDir(), manifest, true); err == nil ||
		!strings.Contains(err.Error(), "escapes archive root") {
		t.Fatalf("path escape error = %v", err)
	}
}

func executeLoadedVM(vm *runtime.VM) (ExecutionSnapshot, error) {
	returned := vm.Run()
	return snapshotExecutedVM(vm, returned)
}
