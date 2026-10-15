package compatibility

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/runtime"
)

// currentArchive is the archive of this runtime's bytecode version (testdata/v3 since 14/10/2026).
var currentArchive = filepath.Join("testdata", "v3", "manifest.json")

func TestVMCompatibilityArchive(t *testing.T) {
	if err := VerifyArchive(currentArchive); err != nil {
		t.Fatal(err)
	}
}

// Bytecode 3 refuses bytecode 2's Programs by their version before anything reads their code: a
// runtime takes only the version it is. The v2 archive stays as that version's evidence.
func TestVMCompatibilityArchiveOfBytecode2IsRefusedByItsVersion(t *testing.T) {
	path := filepath.Join("testdata", "v2", "manifest.json")
	if err := VerifyArchive(path); err == nil ||
		!strings.Contains(err.Error(), `archive = "vm-v2", want vm-v3`) {
		t.Fatalf("v2 archive error = %v", err)
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
			t.Fatalf("%s: the v2 evidence changed", fixture.Name)
		}
		if _, err := runtime.UnmarshalProgram(encoded); err == nil ||
			!strings.Contains(err.Error(), "bytecode version 2 is incompatible with 3") {
			t.Fatalf("%s: decode error = %v", fixture.Name, err)
		}
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
