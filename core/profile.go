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

// ProfileProgram describes one executable router, schedule, or queue Program.
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
		compile := compiler.CompileFileInApp
		if isBackgroundModule(file) {
			// a job file or a schedule file runs as the module its importers and the loader see
			// (work/queue_load.go, work/schedule_load.go)
			compile = func(appRoot string, paths ...string) (*compiler.Bytecode, error) {
				return compiler.CompileModuleFileInApp(appRoot, filepath.Join(paths...))
			}
		}
		bytecode, compileErr := compile(profileAppRoot(root, layout, file), file)
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

		if isRouterMarker(path, entry.Name()) {
			files = append(files, path)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// isRouterMarker reports whether a walked file is the router that marks its folder: router.kitwork.js,
// or router.work for a Kitwork UI site — only the one work.RouterMarker chooses when both exist, so
// profile and check compile the router the host would run. Until 15/10/2026 only the first name was
// an entrypoint, and a .ui site had no program in the profile at all.
func isRouterMarker(path, name string) bool {
	if name != work.RouterFileName && name != work.UIRouterFileName {
		return false
	}
	return work.RouterMarker(filepath.Dir(path)) == path
}

func profileAppEntrypoints(root string) ([]string, error) {
	var files []string
	for _, folder := range []struct{ path, suffix string }{
		{".schedule", ".work"},
		{filepath.Join(".shared", "queue"), ".work"},
	} {
		directory := filepath.Join(root, folder.path)
		entries, err := os.ReadDir(directory)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || entry.Name() == "config.work" ||
				!strings.HasSuffix(strings.ToLower(entry.Name()), folder.suffix) {
				continue
			}
			files = append(files, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(files)
	return files, nil
}

// isQueueJobFile reports a job file of an app's .shared/queue/ (KITWORK_QUEUE.md mục 2).
func isQueueJobFile(file string) bool {
	return filepath.Base(filepath.Dir(file)) == "queue" &&
		filepath.Base(filepath.Dir(filepath.Dir(file))) == ".shared" &&
		strings.HasSuffix(file, ".work") && filepath.Base(file) != "config.work"
}

// isScheduleFile reports a file of an app's .schedule/ (KITWORK_SCHEDULE.md mục 2).
func isScheduleFile(file string) bool {
	return filepath.Base(filepath.Dir(file)) == ".schedule" &&
		strings.HasSuffix(file, ".work") && filepath.Base(file) != "config.work"
}

// isBackgroundModule reports a file the app's loaders run as a module: a job file, a schedule file.
func isBackgroundModule(file string) bool {
	return isQueueJobFile(file) || isScheduleFile(file)
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
			if name == ".shared" || name == ".schedule" {
				// the app's jobs, .shared/queue/*.work, and its schedules, .schedule/*.work, are entrypoints;
				// the rest of .shared/ is imported
				background, err := profileAppEntrypoints(filepath.Dir(path))
				if err != nil {
					return err
				}
				for _, file := range background {
					if (name == ".shared" && isQueueJobFile(file)) || (name == ".schedule" && isScheduleFile(file)) {
						files = append(files, file)
					}
				}
				return filepath.SkipDir
			}
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
		if isRouterMarker(path, name) {
			files = append(files, path)
			return nil
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

// profileAppRoot is the folder of the app file belongs to, where its `app/…` imports resolve, as the
// tenant that runs it finds it (resolveApp): the root itself for one app (app/), apps/<identity>/ for
// many. The legacy walk has no declared shape and takes the first folder under the root: the identity of
// <identity>/<domain>/, the site of a flat <domain>/ (sites/<domain>/ for the sites folder).
func profileAppRoot(root string, layout work.RootLayout, file string) string {
	if layout.IsSingleApp() {
		return root
	}
	relative, err := filepath.Rel(root, file)
	if err != nil {
		return root
	}
	first, rest, nested := strings.Cut(filepath.ToSlash(relative), "/")
	if !nested || first == ".." {
		return root
	}
	if layout == work.RootLayoutAuto && first == work.SitesDirName {
		if domain, _, deeper := strings.Cut(rest, "/"); deeper {
			return filepath.Join(root, first, domain)
		}
	}
	return filepath.Join(root, first)
}

func profileRelativePath(root, file string) string {
	relative, err := filepath.Rel(root, file)
	if err != nil {
		return filepath.ToSlash(file)
	}
	return filepath.ToSlash(relative)
}
