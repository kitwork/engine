package compatibility

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
)

type pendingProgram struct {
	path string
	data []byte
}

// UpdateArchive compiles the manually authored cases and writes deterministic
// Program binaries. Existing evidence is immutable unless replace is explicit.
func UpdateArchive(path string, replace bool) error {
	manifest, err := ReadManifest(path)
	if err != nil {
		return err
	}
	root := filepath.Dir(path)
	if err := validateManifest(root, manifest, false); err != nil {
		return err
	}
	existingGenerated := manifest.BytecodeVersion != 0 ||
		manifest.ProgramEncodingVersion != 0 ||
		manifest.InstructionSetChecksum != "" ||
		manifest.Producer.CompilerFingerprint != ""

	candidate := manifest
	candidate.BytecodeVersion = runtime.BytecodeVersion
	candidate.ProgramEncodingVersion = runtime.ProgramEncodingVersion
	candidate.InstructionSetChecksum = runtime.InstructionSetChecksum()
	candidate.Producer = ProducerContract{
		CompilerSchemaVersion: compiler.CompilerSchemaVersion,
		CompilerFingerprint:   compiler.Fingerprint(),
	}

	pending := make([]pendingProgram, 0, len(candidate.Cases))
	for index := range candidate.Cases {
		fixture := &candidate.Cases[index]
		entry, err := resolvePath(root, fixture.Entry)
		if err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		compiled, err := compiler.CompileFile(entry)
		if err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		if err := compiler.ValidateArtifact(compiled); err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		programData, err := compiled.Program.MarshalBinary()
		if err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		program, err := runtime.UnmarshalProgram(programData)
		if err != nil {
			return fmt.Errorf("generate case %q: restore Program: %w", fixture.Name, err)
		}
		snapshot, err := executeProgram(program, fixture.MaxEnergy)
		if err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		if err := verifyExpectation(*fixture, snapshot); err != nil {
			return fmt.Errorf("generate case %q: authored expectation failed: %w", fixture.Name, err)
		}
		sources, err := sourceRecords(root, compiled.Files)
		if err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		fixture.Sources = sources
		fixture.SourceFingerprint = compiled.SourceFingerprint()
		fixture.ProgramChecksum = program.Checksum()
		fixture.ProgramSHA256 = digest(programData)
		fixture.Profile = program.Profile()
		fixture.Execution = snapshot

		programPath, err := resolvePath(root, fixture.Program)
		if err != nil {
			return fmt.Errorf("generate case %q: %w", fixture.Name, err)
		}
		pending = append(pending, pendingProgram{path: programPath, data: programData})
	}
	if err := validateManifest(root, candidate, true); err != nil {
		return fmt.Errorf("validate generated VM compatibility archive: %w", err)
	}
	if existingGenerated && !replace {
		equal, err := equalManifest(manifest, candidate)
		if err != nil {
			return err
		}
		if !equal {
			return fmt.Errorf("VM compatibility archive would change; rerun with --replace after a version decision")
		}
	}
	for _, program := range pending {
		if existing, readErr := os.ReadFile(program.path); readErr == nil {
			if !bytes.Equal(existing, program.data) && !replace {
				return fmt.Errorf("Program %s would change; rerun with --replace after a version decision", program.path)
			}
		} else if !os.IsNotExist(readErr) {
			return fmt.Errorf("read existing Program %s: %w", program.path, readErr)
		}
	}

	for _, program := range pending {
		if err := os.MkdirAll(filepath.Dir(program.path), 0o755); err != nil {
			return fmt.Errorf("create Program directory: %w", err)
		}
		if err := os.WriteFile(program.path, program.data, 0o644); err != nil {
			return fmt.Errorf("write Program %s: %w", program.path, err)
		}
	}
	encoded, err := json.MarshalIndent(candidate, "", "  ")
	if err != nil {
		return fmt.Errorf("encode VM compatibility manifest: %w", err)
	}
	encoded = append(encoded, '\n')
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return fmt.Errorf("write VM compatibility manifest: %w", err)
	}
	return nil
}

func sourceRecords(root string, files []string) ([]SourceRecord, error) {
	rootAbsolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	result := make([]SourceRecord, 0, len(files))
	for _, file := range files {
		absolute, err := filepath.Abs(file)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(rootAbsolute, absolute)
		if err != nil || relative == ".." || filepath.IsAbs(relative) ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("compiled source escapes archive root: %s", file)
		}
		data, err := os.ReadFile(absolute)
		if err != nil {
			return nil, err
		}
		result = append(result, SourceRecord{
			Path:   filepath.ToSlash(relative),
			SHA256: digest(data),
		})
	}
	sort.Slice(result, func(left, right int) bool {
		return result[left].Path < result[right].Path
	})
	return result, nil
}

func equalManifest(left, right Manifest) (bool, error) {
	leftJSON, err := json.Marshal(left)
	if err != nil {
		return false, err
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		return false, err
	}
	var leftValue any
	if err := json.Unmarshal(leftJSON, &leftValue); err != nil {
		return false, err
	}
	var rightValue any
	if err := json.Unmarshal(rightJSON, &rightValue); err != nil {
		return false, err
	}
	return reflect.DeepEqual(leftValue, rightValue), nil
}
