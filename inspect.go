package engine

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/compiler"
	kitruntime "github.com/kitwork/engine/runtime"
)

// InspectionReport describes one source file after the complete compiler and
// artifact boundary. Inspecting never executes tenant code or starts resources.
type InspectionReport struct {
	File                  string                       `json:"file"`
	Sources               []string                     `json:"sources"`
	ArtifactVersion       uint16                       `json:"artifact_version"`
	ArtifactBytes         int                          `json:"artifact_bytes"`
	CompilerSchemaVersion uint16                       `json:"compiler_schema_version"`
	CompilerFingerprint   string                       `json:"compiler_fingerprint"`
	SourceFingerprint     string                       `json:"source_fingerprint"`
	CacheKey              string                       `json:"cache_key"`
	Program               kitruntime.ProgramInspection `json:"program"`
}

// InspectFile compiles one executable .kitwork.js file through native imports,
// validates and restores its bytecode artifact, then inspects the restored
// immutable Program.
func InspectFile(path string) (InspectionReport, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return InspectionReport{}, fmt.Errorf("inspect: source path is required")
	}
	if !strings.HasSuffix(strings.ToLower(path), ".kitwork.js") {
		return InspectionReport{}, fmt.Errorf("inspect: source must end in .kitwork.js: %q", path)
	}

	compiled, err := compiler.CompileFile(path)
	if err != nil {
		return InspectionReport{}, fmt.Errorf("inspect %s: %w", filepath.ToSlash(path), err)
	}
	if err := compiler.ValidateArtifact(compiled); err != nil {
		return InspectionReport{}, fmt.Errorf("inspect %s: %w", filepath.ToSlash(path), err)
	}
	artifact, err := compiled.MarshalBinary()
	if err != nil {
		return InspectionReport{}, fmt.Errorf("inspect %s: %w", filepath.ToSlash(path), err)
	}
	restored, err := compiler.UnmarshalBytecode(artifact, compiled.SourceFingerprint())
	if err != nil {
		return InspectionReport{}, fmt.Errorf("inspect %s: %w", filepath.ToSlash(path), err)
	}
	reencoded, err := restored.MarshalBinary()
	if err != nil {
		return InspectionReport{}, fmt.Errorf("inspect %s: %w", filepath.ToSlash(path), err)
	}
	if !bytes.Equal(artifact, reencoded) {
		return InspectionReport{}, fmt.Errorf("inspect %s: artifact re-encode changed bytes", filepath.ToSlash(path))
	}
	program, err := kitruntime.InspectProgram(restored.Program)
	if err != nil {
		return InspectionReport{}, fmt.Errorf("inspect %s: %w", filepath.ToSlash(path), err)
	}

	return InspectionReport{
		File:                  filepath.ToSlash(filepath.Clean(path)),
		Sources:               inspectionSourcePaths(path, compiled.Files),
		ArtifactVersion:       compiler.BytecodeArtifactVersion,
		ArtifactBytes:         len(artifact),
		CompilerSchemaVersion: compiler.CompilerSchemaVersion,
		CompilerFingerprint:   compiled.CompilerFingerprint(),
		SourceFingerprint:     compiled.SourceFingerprint(),
		CacheKey:              compiled.CacheKey(),
		Program:               program,
	}, nil
}

func inspectionSourcePaths(entry string, files []string) []string {
	entryAbsolute, err := filepath.Abs(entry)
	if err != nil {
		entryAbsolute = filepath.Clean(entry)
	}
	base := filepath.Dir(entryAbsolute)
	result := make([]string, 0, len(files))
	for _, file := range files {
		relative, relErr := filepath.Rel(base, file)
		if relErr != nil {
			relative = file
		}
		result = append(result, filepath.ToSlash(relative))
	}
	return result
}
