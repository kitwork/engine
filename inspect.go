package engine

import (
	"bytes"
	"fmt"
	"os"
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

	appRoot := inspectAppRoot(path)
	compiled, err := compiler.CompileFileInApp(appRoot, path)
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
		CacheKey:              compiled.CacheKeyInApp(appRoot),
		Program:               program,
	}, nil
}

// inspectAppRoot is the folder of the app the inspected file belongs to, where its `app/…` imports
// resolve (22/10/2026), found by the names the host finds its root by when application.work names none
// (resolveRootConfigAt): the first folder app/ (one app), or apps/<identity>/ under the first folder apps/
// (many apps). It is counted from the current folder (the host's) when the file is inside it, so a folder
// app or apps above the host is never taken, then from the top of the file's volume. "" when there is
// neither: the file is no app's, and an `app/…` import says so.
func inspectAppRoot(path string) string {
	file, err := filepath.Abs(path)
	if err != nil {
		return ""
	}
	if working, err := os.Getwd(); err == nil {
		if found := appRootBelow(working, file); found != "" {
			return found
		}
	}
	return appRootBelow(filepath.VolumeName(file)+string(filepath.Separator), file)
}

// appRootBelow is inspectAppRoot counted from base, "" when file is not inside base or names no app.
func appRootBelow(base, file string) string {
	relative, err := filepath.Rel(base, filepath.Dir(file))
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	segments := strings.Split(filepath.ToSlash(relative), "/")
	for i, segment := range segments {
		switch {
		case strings.EqualFold(segment, "app"):
			return filepath.Join(base, filepath.Join(segments[:i+1]...))
		case strings.EqualFold(segment, "apps") && i+1 < len(segments):
			return filepath.Join(base, filepath.Join(segments[:i+2]...))
		}
	}
	return ""
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
