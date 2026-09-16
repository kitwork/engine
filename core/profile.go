package core

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/kitwork/engine/compiler"
	"github.com/kitwork/engine/runtime"
	"github.com/kitwork/engine/work"
)

// ProfileIssue is a source that could not be compiled for static VM analysis.
type ProfileIssue struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// ProfileProgram describes one executable router, cron, or queue Program.
type ProfileProgram struct {
	File     string                 `json:"file"`
	Checksum string                 `json:"checksum"`
	Profile  runtime.ProgramProfile `json:"profile"`
}

// ProfileReport aggregates immutable Program profiles from one apps root.
type ProfileReport struct {
	Root                   string                  `json:"root"`
	Layout                 string                  `json:"layout"`
	BytecodeVersion        uint16                  `json:"bytecode_version"`
	ProgramEncodingVersion uint16                  `json:"program_encoding_version"`
	ArtifactVersion        uint16                  `json:"artifact_version"`
	CompilerSchemaVersion  uint16                  `json:"compiler_schema_version"`
	CompilerFingerprint    string                  `json:"compiler_fingerprint"`
	InstructionSetChecksum string                  `json:"instruction_set_checksum"`
	Entrypoints            int                     `json:"entrypoints"`
	ProgramCount           int                     `json:"program_count"`
	CompatiblePrograms     int                     `json:"compatible_programs"`
	BytecodeBytes          int                     `json:"bytecode_bytes"`
	Constants              int                     `json:"constants"`
	Instructions           int                     `json:"instructions"`
	EntryPoints            int                     `json:"program_entry_points"`
	MaxStackDepth          int                     `json:"max_stack_depth"`
	EncodedEnergy          uint64                  `json:"encoded_energy"`
	Opcodes                []runtime.OpcodeProfile `json:"opcodes"`
	Programs               []ProfileProgram        `json:"programs"`
	Issues                 []ProfileIssue          `json:"issues,omitempty"`
}

func (r ProfileReport) OK() bool {
	return len(r.Issues) == 0
}

// Profile discovers executable source entrypoints and compiles each through
// the native bundler. It does not execute tenant code or start app resources.
func Profile(root string) ProfileReport {
	return ProfileWithLayout(root, work.RootLayoutAuto)
}

// ProfileWithLayout profiles only entrypoints that can execute under the
// filesystem contract selected by host bootstrap. Profile retains the legacy
// mixed-root walk for direct engine users.
func ProfileWithLayout(root string, layout work.RootLayout) ProfileReport {
	report := ProfileReport{
		Root:                   root,
		Layout:                 layout.String(),
		BytecodeVersion:        runtime.BytecodeVersion,
		ProgramEncodingVersion: runtime.ProgramEncodingVersion,
		ArtifactVersion:        compiler.BytecodeArtifactVersion,
		CompilerSchemaVersion:  compiler.CompilerSchemaVersion,
		CompilerFingerprint:    compiler.Fingerprint(),
		InstructionSetChecksum: runtime.InstructionSetChecksum(),
	}
	files, err := profileEntrypoints(root, layout)
	if err != nil {
		report.Issues = append(report.Issues, ProfileIssue{
			File:    root,
			Message: err.Error(),
		})
		return report
	}
	report.Entrypoints = len(files)

	var opcodeCounts [256]int
	var opcodeEnergy [256]uint64
	var opcodeNames [256]string
	for _, file := range files {
		bytecode, compileErr := compiler.CompileFile(file)
		relative := profileRelativePath(root, file)
		if compileErr != nil {
			report.Issues = append(report.Issues, ProfileIssue{
				File:    relative,
				Message: compileErr.Error(),
			})
			continue
		}
		if compatibilityErr := compiler.ValidateArtifact(bytecode); compatibilityErr != nil {
			report.Issues = append(report.Issues, ProfileIssue{
				File:    relative,
				Message: "bytecode compatibility: " + compatibilityErr.Error(),
			})
			continue
		}

		profile := bytecode.Program.Profile()
		report.ProgramCount++
		report.CompatiblePrograms++
		report.BytecodeBytes += profile.BytecodeBytes
		report.Constants += profile.Constants
		report.Instructions += profile.Instructions
		report.EntryPoints += profile.EntryPoints
		report.EncodedEnergy += profile.EncodedEnergy
		if profile.MaxStackDepth > report.MaxStackDepth {
			report.MaxStackDepth = profile.MaxStackDepth
		}
		for _, opcode := range profile.Opcodes {
			index := uint8(opcode.Opcode)
			opcodeCounts[index] += opcode.Count
			opcodeEnergy[index] += opcode.EncodedEnergy
			opcodeNames[index] = opcode.Name
		}
		report.Programs = append(report.Programs, ProfileProgram{
			File:     relative,
			Checksum: bytecode.Program.Checksum(),
			Profile:  profile,
		})
	}

	for rawOpcode, count := range opcodeCounts {
		if count == 0 {
			continue
		}
		report.Opcodes = append(report.Opcodes, runtime.OpcodeProfile{
			Opcode:        runtime.Opcode(rawOpcode),
			Name:          opcodeNames[rawOpcode],
			Count:         count,
			EncodedEnergy: opcodeEnergy[rawOpcode],
		})
	}
	sort.Slice(report.Opcodes, func(i, j int) bool {
		if report.Opcodes[i].Count != report.Opcodes[j].Count {
			return report.Opcodes[i].Count > report.Opcodes[j].Count
		}
		return report.Opcodes[i].Opcode < report.Opcodes[j].Opcode
	})
	sort.Slice(report.Programs, func(i, j int) bool {
		left := report.Programs[i].Profile
		right := report.Programs[j].Profile
		if left.BytecodeBytes != right.BytecodeBytes {
			return left.BytecodeBytes > right.BytecodeBytes
		}
		return report.Programs[i].File < report.Programs[j].File
	})
	sort.Slice(report.Issues, func(i, j int) bool {
		return report.Issues[i].File < report.Issues[j].File
	})
	return report
}

func profileEntrypoints(root string, layout work.RootLayout) ([]string, error) {
	if layout == work.RootLayoutAuto {
		return profileLegacyEntrypoints(root)
	}
	if !layout.Valid() {
		return nil, &os.PathError{Op: "profile root layout", Path: root, Err: fs.ErrInvalid}
	}
	if _, err := os.ReadDir(root); err != nil {
		return nil, err
	}

	var files []string
	addApp := func(appRoot string) error {
		background, err := profileAppEntrypoints(appRoot)
		if err != nil {
			return err
		}
		files = append(files, background...)
		return nil
	}
	addSite := func(siteRoot string) error {
		routes, err := profileRouteEntrypoints(siteRoot)
		if err != nil {
			return err
		}
		files = append(files, routes...)
		return nil
	}

	switch layout {
	case work.RootLayoutSingle:
		if err := addApp(root); err != nil {
			return nil, err
		}
		if err := addSite(root); err != nil {
			return nil, err
		}
	case work.RootLayoutMultiDomain:
		if err := addApp(root); err != nil {
			return nil, err
		}
		sites, err := work.DiscoverAppSites(root)
		if err != nil {
			return nil, err
		}
		for _, site := range sites {
			if err := addSite(site.Directory); err != nil {
				return nil, err
			}
		}
	case work.RootLayoutMultiTenant:
		sites, err := work.DiscoverTenantSites(root)
		if err != nil {
			return nil, err
		}
		for _, identity := range work.DiscoverAppIdentities(root) {
			if err := addApp(filepath.Join(root, identity)); err != nil {
				return nil, err
			}
		}
		for _, site := range sites {
			if err := addSite(site.Directory); err != nil {
				return nil, err
			}
		}
	}

	sort.Strings(files)
	return compactProfileEntrypoints(files), nil
}

func profileRouteEntrypoints(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		name := entry.Name()
		if name == work.RouterFileName {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func profileAppEntrypoints(root string) ([]string, error) {
	var files []string
	for _, folder := range []string{"_cron", "_queue"} {
		directory := filepath.Join(root, folder)
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
				!strings.HasSuffix(strings.ToLower(entry.Name()), ".kitwork.js") {
				continue
			}
			files = append(files, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

func compactProfileEntrypoints(files []string) []string {
	if len(files) < 2 {
		return files
	}
	write := 1
	for read := 1; read < len(files); read++ {
		if files[read] == files[write-1] {
			continue
		}
		files[write] = files[read]
		write++
	}
	return files[:write]
}

func profileLegacyEntrypoints(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path == root {
				return nil
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" {
				return filepath.SkipDir
			}
			relative, err := filepath.Rel(root, path)
			if err == nil {
				first, _, _ := strings.Cut(filepath.ToSlash(relative), "/")
				if first == "test" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}

		name := entry.Name()
		if name == work.RouterFileName {
			files = append(files, path)
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(name), ".kitwork.js") {
			return nil
		}
		parent := filepath.Base(filepath.Dir(path))
		if parent == "_cron" || parent == "_queue" {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func profileRelativePath(root, file string) string {
	relative, err := filepath.Rel(root, file)
	if err != nil {
		return filepath.ToSlash(file)
	}
	return filepath.ToSlash(relative)
}
