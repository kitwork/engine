// Package compatibility owns Kitwork's immutable VM compatibility evidence.
package compatibility

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/value"
)

const (
	ManifestSchemaVersion uint16 = 1
	DefaultManifestPath          = "compatibility/testdata/v2/manifest.json"
)

// Manifest records immutable Program binaries and the contract that produced
// them. Producer metadata is provenance; Program and instruction versions are
// the compatibility boundary enforced by normal tests.
type Manifest struct {
	SchemaVersion          uint16           `json:"schema_version"`
	Archive                string           `json:"archive"`
	BytecodeVersion        uint16           `json:"bytecode_version"`
	ProgramEncodingVersion uint16           `json:"program_encoding_version"`
	InstructionSetChecksum string           `json:"instruction_set_checksum"`
	Producer               ProducerContract `json:"producer"`
	Cases                  []Case           `json:"cases"`
}

type ProducerContract struct {
	CompilerSchemaVersion uint16 `json:"compiler_schema_version"`
	CompilerFingerprint   string `json:"compiler_fingerprint"`
}

// Case contains manually authored semantics plus generated provenance and
// execution evidence. Expected and Diagnostic are the only semantic inputs to
// the updater; the remaining identity fields are derived from the compiler.
type Case struct {
	Name              string                 `json:"name"`
	Entry             string                 `json:"entry"`
	Sources           []SourceRecord         `json:"sources,omitempty"`
	Program           string                 `json:"program"`
	Kind              string                 `json:"kind"`
	Expected          json.RawMessage        `json:"expected,omitempty"`
	Diagnostic        string                 `json:"diagnostic,omitempty"`
	MaxEnergy         uint64                 `json:"max_energy"`
	SourceFingerprint string                 `json:"source_fingerprint,omitempty"`
	ProgramChecksum   string                 `json:"program_checksum,omitempty"`
	ProgramSHA256     string                 `json:"program_sha256,omitempty"`
	Profile           runtime.ProgramProfile `json:"profile"`
	Execution         ExecutionSnapshot      `json:"execution"`
}

type SourceRecord struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// ExecutionSnapshot deliberately excludes allocation and stack-capacity
// details. Those are performance implementation details, not VM semantics.
type ExecutionSnapshot struct {
	ReturnedKind   string          `json:"returned_kind"`
	HasResult      bool            `json:"has_result"`
	Result         json.RawMessage `json:"result,omitempty"`
	Variables      json.RawMessage `json:"variables"`
	Diagnostic     string          `json:"diagnostic,omitempty"`
	Instructions   uint64          `json:"instructions"`
	Energy         uint64          `json:"energy"`
	StackDepth     int             `json:"stack_depth"`
	FrameDepth     int             `json:"frame_depth"`
	PeakFrameDepth int             `json:"peak_frame_depth"`
}

// ReadManifest decodes one strict archive manifest. Unknown fields and trailing
// JSON values are rejected so a typo cannot silently weaken the contract.
func ReadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read VM compatibility manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode VM compatibility manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Manifest{}, fmt.Errorf("decode VM compatibility manifest: trailing JSON value")
		}
		return Manifest{}, fmt.Errorf("decode VM compatibility manifest: %w", err)
	}
	return manifest, nil
}

// VerifyArchive proves that every frozen Program can be loaded, verified,
// executed, and encoded again without invoking the compiler.
func VerifyArchive(path string) error {
	manifest, err := ReadManifest(path)
	if err != nil {
		return err
	}
	root := filepath.Dir(path)
	if err := validateManifest(root, manifest, true); err != nil {
		return err
	}
	for _, fixture := range manifest.Cases {
		if _, _, err := verifyCase(root, fixture); err != nil {
			return fmt.Errorf("verify VM compatibility case %q: %w", fixture.Name, err)
		}
	}
	return nil
}

func verifyCase(root string, fixture Case) (*runtime.Program, ExecutionSnapshot, error) {
	for _, source := range fixture.Sources {
		path, err := resolvePath(root, source.Path)
		if err != nil {
			return nil, ExecutionSnapshot{}, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, ExecutionSnapshot{}, fmt.Errorf("read source %s: %w", source.Path, err)
		}
		if digest(data) != source.SHA256 {
			return nil, ExecutionSnapshot{}, fmt.Errorf("source %s SHA-256 mismatch", source.Path)
		}
	}

	programPath, err := resolvePath(root, fixture.Program)
	if err != nil {
		return nil, ExecutionSnapshot{}, err
	}
	encoded, err := os.ReadFile(programPath)
	if err != nil {
		return nil, ExecutionSnapshot{}, fmt.Errorf("read Program %s: %w", fixture.Program, err)
	}
	if digest(encoded) != fixture.ProgramSHA256 {
		return nil, ExecutionSnapshot{}, fmt.Errorf("Program %s SHA-256 mismatch", fixture.Program)
	}
	program, err := runtime.UnmarshalProgram(encoded)
	if err != nil {
		return nil, ExecutionSnapshot{}, fmt.Errorf("decode Program %s: %w", fixture.Program, err)
	}
	if program.ProgramVersion() != runtime.BytecodeVersion {
		return nil, ExecutionSnapshot{}, fmt.Errorf(
			"Program bytecode version = %d, want %d",
			program.ProgramVersion(),
			runtime.BytecodeVersion,
		)
	}
	if program.Checksum() != fixture.ProgramChecksum {
		return nil, ExecutionSnapshot{}, fmt.Errorf(
			"Program checksum = %s, want %s",
			program.Checksum(),
			fixture.ProgramChecksum,
		)
	}
	if profile := program.Profile(); !reflect.DeepEqual(profile, fixture.Profile) {
		return nil, ExecutionSnapshot{}, fmt.Errorf("Program profile changed\nwant: %#v\n got: %#v", fixture.Profile, profile)
	}
	reencoded, err := program.MarshalBinary()
	if err != nil {
		return nil, ExecutionSnapshot{}, fmt.Errorf("re-encode Program: %w", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		return nil, ExecutionSnapshot{}, fmt.Errorf("Program changed after deterministic round trip")
	}

	snapshot, err := executeProgram(program, fixture.MaxEnergy)
	if err != nil {
		return nil, ExecutionSnapshot{}, err
	}
	if err := verifyExpectation(fixture, snapshot); err != nil {
		return nil, ExecutionSnapshot{}, err
	}
	if err := compareExecution(fixture.Execution, snapshot); err != nil {
		return nil, ExecutionSnapshot{}, err
	}
	return program, snapshot, nil
}

func executeProgram(program *runtime.Program, maxEnergy uint64) (ExecutionSnapshot, error) {
	vm := runtime.New(program)
	vm.MaxEnergy = maxEnergy
	returned := vm.Run()
	return snapshotExecutedVM(vm, returned)
}

func snapshotExecutedVM(vm *runtime.VM, returned value.Value) (ExecutionSnapshot, error) {
	variables, err := json.Marshal(vm.Vars)
	if err != nil {
		return ExecutionSnapshot{}, fmt.Errorf("encode VM variables: %w", err)
	}
	result, hasResult := vm.Vars["result"]
	var resultJSON json.RawMessage
	if hasResult {
		resultJSON, err = json.Marshal(result)
		if err != nil {
			return ExecutionSnapshot{}, fmt.Errorf("encode VM result: %w", err)
		}
	}
	stats := vm.Stats()
	snapshot := ExecutionSnapshot{
		ReturnedKind:   returned.K.String(),
		HasResult:      hasResult,
		Result:         resultJSON,
		Variables:      variables,
		Instructions:   stats.Instructions,
		Energy:         stats.Energy,
		StackDepth:     stats.StackDepth,
		FrameDepth:     stats.FrameDepth,
		PeakFrameDepth: stats.PeakFrameDepth,
	}
	if diagnostic, ok := runtime.DiagnosticFrom(returned); ok {
		snapshot.Diagnostic = string(diagnostic.Code)
	}
	return snapshot, nil
}

func verifyExpectation(fixture Case, snapshot ExecutionSnapshot) error {
	switch fixture.Kind {
	case "execute":
		if snapshot.Diagnostic != "" {
			return fmt.Errorf("execution returned diagnostic %s", snapshot.Diagnostic)
		}
		if !snapshot.HasResult {
			return fmt.Errorf("execution did not publish top-level result")
		}
		if equal, err := equalJSON(fixture.Expected, snapshot.Result); err != nil {
			return err
		} else if !equal {
			return fmt.Errorf("result = %s, want %s", snapshot.Result, fixture.Expected)
		}
	case "diagnostic":
		if snapshot.Diagnostic != fixture.Diagnostic {
			return fmt.Errorf("diagnostic = %q, want %q", snapshot.Diagnostic, fixture.Diagnostic)
		}
	default:
		return fmt.Errorf("unsupported case kind %q", fixture.Kind)
	}
	return nil
}

func compareExecution(want, got ExecutionSnapshot) error {
	if want.ReturnedKind != got.ReturnedKind ||
		want.HasResult != got.HasResult ||
		want.Diagnostic != got.Diagnostic ||
		want.Instructions != got.Instructions ||
		want.Energy != got.Energy ||
		want.StackDepth != got.StackDepth ||
		want.FrameDepth != got.FrameDepth ||
		want.PeakFrameDepth != got.PeakFrameDepth {
		return fmt.Errorf("execution snapshot changed\nwant: %#v\n got: %#v", want, got)
	}
	for name, pair := range map[string][2]json.RawMessage{
		"result":    {want.Result, got.Result},
		"variables": {want.Variables, got.Variables},
	} {
		equal, err := equalJSON(pair[0], pair[1])
		if err != nil {
			return fmt.Errorf("compare %s: %w", name, err)
		}
		if !equal {
			return fmt.Errorf("%s changed\nwant: %s\n got: %s", name, pair[0], pair[1])
		}
	}
	return nil
}

func validateManifest(root string, manifest Manifest, generated bool) error {
	if manifest.SchemaVersion != ManifestSchemaVersion {
		return fmt.Errorf(
			"VM compatibility schema = %d, want %d",
			manifest.SchemaVersion,
			ManifestSchemaVersion,
		)
	}
	if manifest.Archive != "vm-v2" {
		return fmt.Errorf("VM compatibility archive = %q, want vm-v2", manifest.Archive)
	}
	if len(manifest.Cases) < 5 {
		return fmt.Errorf("VM compatibility archive has only %d cases", len(manifest.Cases))
	}
	if generated {
		if manifest.BytecodeVersion != runtime.BytecodeVersion {
			return fmt.Errorf(
				"archived bytecode version = %d, current = %d",
				manifest.BytecodeVersion,
				runtime.BytecodeVersion,
			)
		}
		if manifest.ProgramEncodingVersion != runtime.ProgramEncodingVersion {
			return fmt.Errorf(
				"archived Program encoding = %d, current = %d",
				manifest.ProgramEncodingVersion,
				runtime.ProgramEncodingVersion,
			)
		}
		if manifest.InstructionSetChecksum != runtime.InstructionSetChecksum() {
			return fmt.Errorf("archived instruction-set checksum differs from the current VM")
		}
		if manifest.Producer.CompilerSchemaVersion == 0 || manifest.Producer.CompilerFingerprint == "" {
			return fmt.Errorf("VM compatibility producer metadata is incomplete")
		}
	}

	seen := make(map[string]struct{}, len(manifest.Cases))
	for _, fixture := range manifest.Cases {
		if fixture.Name == "" || fixture.Entry == "" || fixture.Program == "" {
			return fmt.Errorf("VM compatibility case has an empty name, entry, or Program path")
		}
		if _, exists := seen[fixture.Name]; exists {
			return fmt.Errorf("duplicate VM compatibility case %q", fixture.Name)
		}
		seen[fixture.Name] = struct{}{}
		if _, err := resolvePath(root, fixture.Entry); err != nil {
			return fmt.Errorf("case %q entry: %w", fixture.Name, err)
		}
		if !strings.HasSuffix(strings.ToLower(fixture.Entry), ".kitwork.js") {
			return fmt.Errorf("case %q entry is not a .kitwork.js source", fixture.Name)
		}
		if _, err := resolvePath(root, fixture.Program); err != nil {
			return fmt.Errorf("case %q Program: %w", fixture.Name, err)
		}
		if strings.ToLower(filepath.Ext(fixture.Program)) != ".kwpb" {
			return fmt.Errorf("case %q Program does not use the .kwpb archive extension", fixture.Name)
		}
		if fixture.MaxEnergy == 0 {
			return fmt.Errorf("case %q max_energy must be positive", fixture.Name)
		}
		switch fixture.Kind {
		case "execute":
			if len(fixture.Expected) == 0 || fixture.Diagnostic != "" {
				return fmt.Errorf("case %q has an invalid execute expectation", fixture.Name)
			}
		case "diagnostic":
			if fixture.Diagnostic == "" || len(fixture.Expected) != 0 {
				return fmt.Errorf("case %q has an invalid diagnostic expectation", fixture.Name)
			}
		default:
			return fmt.Errorf("case %q has unknown kind %q", fixture.Name, fixture.Kind)
		}
		if !generated {
			continue
		}
		if len(fixture.Sources) == 0 || fixture.SourceFingerprint == "" ||
			fixture.ProgramChecksum == "" || fixture.ProgramSHA256 == "" ||
			len(fixture.Execution.Variables) == 0 {
			return fmt.Errorf("case %q generated metadata is incomplete", fixture.Name)
		}
		entryFound := false
		for _, source := range fixture.Sources {
			if _, err := resolvePath(root, source.Path); err != nil {
				return fmt.Errorf("case %q source: %w", fixture.Name, err)
			}
			if source.Path == filepath.ToSlash(filepath.Clean(filepath.FromSlash(fixture.Entry))) {
				entryFound = true
			}
			if source.SHA256 == "" {
				return fmt.Errorf("case %q source %q has no SHA-256", fixture.Name, source.Path)
			}
		}
		if !entryFound {
			return fmt.Errorf("case %q sources omit entry %q", fixture.Name, fixture.Entry)
		}
	}
	return nil
}

func resolvePath(root, relative string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimSpace(relative)))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes archive root: %q", relative)
	}
	return filepath.Join(root, clean), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func equalJSON(left, right []byte) (bool, error) {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right), nil
	}
	var leftValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		return false, fmt.Errorf("invalid archived JSON %q: %w", left, err)
	}
	var rightValue any
	if err := json.Unmarshal(right, &rightValue); err != nil {
		return false, fmt.Errorf("invalid current JSON %q: %w", right, err)
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}
