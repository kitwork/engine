package conformance

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kitwork/engine/app"
	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

const (
	corpusSchemaVersion uint16 = 1
	defaultMaxEnergy           = 1_000_000
)

type corpusManifest struct {
	SchemaVersion uint16       `json:"schema_version"`
	Cases         []corpusCase `json:"cases"`
}

type corpusCase struct {
	Name          string          `json:"name"`
	Category      string          `json:"category"`
	File          string          `json:"file"`
	Kind          string          `json:"kind"`
	Expected      json.RawMessage `json:"expected,omitempty"`
	Diagnostic    string          `json:"diagnostic,omitempty"`
	ErrorContains string          `json:"error_contains,omitempty"`
	MaxEnergy     uint64          `json:"max_energy,omitempty"`
}

type executionStats struct {
	Instructions   uint64
	Energy         uint64
	StackDepth     int
	PeakStackDepth int
	FrameDepth     int
	PeakFrameDepth int
}

type executionSnapshot struct {
	ReturnedKind value.Kind
	Variables    string
	Result       string
	HasResult    bool
	Diagnostic   *runtime.Diagnostic
	Stats        executionStats
}

func TestLanguageConformanceCorpus(t *testing.T) {
	root := filepath.Join("testdata")
	manifest := readCorpusManifest(t, filepath.Join(root, "corpus.json"))
	validateCorpusManifest(t, root, manifest)

	dirtyBytecode, err := compiler.CompileSource(`
const make = (base) => (number) => base + number;
let staleResult = [1, 2, 3].map((number) => make(40)(number));
`)
	if err != nil {
		t.Fatalf("compile dirty VM fixture: %v", err)
	}
	pool := app.NewPool()

	for _, fixture := range manifest.Cases {
		fixture := fixture
		t.Run(fixture.Category+"/"+fixture.Name, func(t *testing.T) {
			path := filepath.Join(root, filepath.FromSlash(fixture.File))
			compiled, compileErr := compiler.CompileFile(path)
			if fixture.Kind == "reject" {
				assertRejectedFixture(t, fixture, compileErr)
				return
			}
			if compileErr != nil {
				t.Fatalf("compile accepted fixture: %v", compileErr)
			}

			restored := roundTripArtifact(t, compiled)
			maxEnergy := fixture.MaxEnergy
			if maxEnergy == 0 {
				maxEnergy = defaultMaxEnergy
			}

			fresh := runtime.New(restored.Program)
			fresh.MaxEnergy = maxEnergy
			baseline := snapshotExecution(t, fresh)
			assertFixtureExpectation(t, fixture, baseline)

			reused := runtime.New(dirtyBytecode.Program)
			reused.MaxEnergy = defaultMaxEnergy
			_ = reused.Run()
			reused.FastReset(restored.Program, nil)
			reused.MaxEnergy = maxEnergy
			assertSameExecution(t, "reused VM", baseline, snapshotExecution(t, reused))

			dirtyLease := pool.Acquire()
			dirtyLease.FastReset(dirtyBytecode.Program, nil)
			dirtyLease.MaxEnergy = defaultMaxEnergy
			_ = dirtyLease.Run()
			pool.Release(dirtyLease)

			lease := pool.Acquire()
			lease.FastReset(restored.Program, nil)
			lease.MaxEnergy = maxEnergy
			pooled := snapshotExecution(t, lease)
			pool.Release(lease)
			assertSameExecution(t, "pooled VM", baseline, pooled)
		})
	}

	if active := pool.Active(); active != 0 {
		t.Fatalf("conformance corpus retained %d active VM leases", active)
	}
}

func readCorpusManifest(t testing.TB, path string) corpusManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest corpusManifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		t.Fatalf("decode corpus manifest: %v", err)
	}
	if manifest.SchemaVersion != corpusSchemaVersion {
		t.Fatalf("corpus schema = %d, want %d", manifest.SchemaVersion, corpusSchemaVersion)
	}
	return manifest
}

func validateCorpusManifest(t testing.TB, root string, manifest corpusManifest) {
	t.Helper()
	if len(manifest.Cases) < 12 {
		t.Fatalf("corpus has only %d cases", len(manifest.Cases))
	}
	seenNames := make(map[string]struct{}, len(manifest.Cases))
	seenFiles := make(map[string]struct{}, len(manifest.Cases))
	for index, fixture := range manifest.Cases {
		label := fmt.Sprintf("case %d", index)
		if fixture.Name == "" || fixture.Category == "" || fixture.File == "" {
			t.Fatalf("%s has an empty name, category, or file", label)
		}
		fullName := fixture.Category + "/" + fixture.Name
		if _, exists := seenNames[fullName]; exists {
			t.Fatalf("duplicate corpus name %q", fullName)
		}
		seenNames[fullName] = struct{}{}

		clean := filepath.Clean(filepath.FromSlash(fixture.File))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			t.Fatalf("%s escapes testdata: %q", fullName, fixture.File)
		}
		if strings.ToLower(filepath.Ext(clean)) != ".js" || !strings.HasSuffix(strings.ToLower(clean), ".kitwork.js") {
			t.Fatalf("%s is not a .kitwork.js fixture: %q", fullName, fixture.File)
		}
		if _, exists := seenFiles[filepath.ToSlash(clean)]; exists {
			t.Fatalf("duplicate corpus file %q", fixture.File)
		}
		seenFiles[filepath.ToSlash(clean)] = struct{}{}
		if info, err := os.Stat(filepath.Join(root, clean)); err != nil || info.IsDir() {
			t.Fatalf("%s source %q is unavailable: %v", fullName, fixture.File, err)
		}

		switch fixture.Kind {
		case "execute":
			if len(fixture.Expected) == 0 || fixture.Diagnostic != "" || fixture.ErrorContains != "" {
				t.Fatalf("%s has an invalid execute expectation", fullName)
			}
		case "diagnostic":
			if fixture.Diagnostic == "" || len(fixture.Expected) != 0 || fixture.ErrorContains != "" {
				t.Fatalf("%s has an invalid diagnostic expectation", fullName)
			}
		case "reject":
			if fixture.ErrorContains == "" || len(fixture.Expected) != 0 || fixture.Diagnostic != "" {
				t.Fatalf("%s has an invalid rejection expectation", fullName)
			}
		default:
			t.Fatalf("%s has unknown kind %q", fullName, fixture.Kind)
		}
	}

}

func roundTripArtifact(t testing.TB, compiled *compiler.Bytecode) *compiler.Bytecode {
	t.Helper()
	if err := compiler.ValidateArtifact(compiled); err != nil {
		t.Fatalf("validate artifact: %v", err)
	}
	encoded, err := compiled.MarshalBinary()
	if err != nil {
		t.Fatalf("encode artifact: %v", err)
	}
	restored, err := compiler.UnmarshalBytecode(encoded, compiled.SourceFingerprint())
	if err != nil {
		t.Fatalf("restore artifact: %v", err)
	}
	reencoded, err := restored.MarshalBinary()
	if err != nil {
		t.Fatalf("re-encode artifact: %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatal("artifact changed after deterministic round-trip")
	}
	if restored.Program == compiled.Program {
		t.Fatal("artifact restoration reused the compiler-owned Program")
	}
	if restored.Program.Checksum() != compiled.Program.Checksum() {
		t.Fatalf("restored checksum = %q, want %q", restored.Program.Checksum(), compiled.Program.Checksum())
	}
	return restored
}

func snapshotExecution(t testing.TB, vm *runtime.VM) executionSnapshot {
	t.Helper()
	returned := vm.Run()
	variables, err := json.Marshal(vm.Vars)
	if err != nil {
		t.Fatalf("encode VM variables: %v", err)
	}
	result, hasResult := vm.Vars["result"]
	resultJSON := ""
	if hasResult {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			t.Fatalf("encode result: %v", marshalErr)
		}
		resultJSON = string(data)
	}
	stats := vm.Stats()
	snapshot := executionSnapshot{
		ReturnedKind: returned.K,
		Variables:    string(variables),
		Result:       resultJSON,
		HasResult:    hasResult,
		Stats: executionStats{
			Instructions:   stats.Instructions,
			Energy:         stats.Energy,
			StackDepth:     stats.StackDepth,
			PeakStackDepth: stats.PeakStackDepth,
			FrameDepth:     stats.FrameDepth,
			PeakFrameDepth: stats.PeakFrameDepth,
		},
	}
	if diagnostic, ok := runtime.DiagnosticFrom(returned); ok {
		snapshot.Diagnostic = diagnostic
	}
	return snapshot
}

func assertFixtureExpectation(t testing.TB, fixture corpusCase, snapshot executionSnapshot) {
	t.Helper()
	switch fixture.Kind {
	case "execute":
		if snapshot.Diagnostic != nil || snapshot.ReturnedKind == value.Invalid {
			t.Fatalf("execution returned diagnostic: %#v", snapshot.Diagnostic)
		}
		if !snapshot.HasResult {
			t.Fatal("fixture did not publish top-level result")
		}
		if normalizeJSON(t, snapshot.Result) != normalizeJSON(t, string(fixture.Expected)) {
			t.Fatalf("result = %s, want %s", snapshot.Result, fixture.Expected)
		}
	case "diagnostic":
		if snapshot.Diagnostic == nil {
			t.Fatalf("execution returned kind %v without diagnostic", snapshot.ReturnedKind)
		}
		if string(snapshot.Diagnostic.Code) != fixture.Diagnostic {
			t.Fatalf("diagnostic = %q, want %q", snapshot.Diagnostic.Code, fixture.Diagnostic)
		}
	}
}

func assertRejectedFixture(t testing.TB, fixture corpusCase, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("rejected fixture compiled successfully")
	}
	if !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(fixture.ErrorContains)) {
		t.Fatalf("compile error %q does not contain %q", err, fixture.ErrorContains)
	}
}

func assertSameExecution(t testing.TB, owner string, want, got executionSnapshot) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s changed execution\nwant: %#v\n got: %#v", owner, want, got)
	}
}

func normalizeJSON(t testing.TB, input string) string {
	t.Helper()
	var decoded any
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		t.Fatalf("invalid expected JSON %q: %v", input, err)
	}
	data, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("normalize JSON: %v", err)
	}
	return string(data)
}
