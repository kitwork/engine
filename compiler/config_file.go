package compiler

import (
	"os"
	"path/filepath"
)

// The config.work of .shared/queue/ and of .schedule/ is written with calls on the module's namespace, one call
// per line at the file's top level (folder_config.go, 24/10/2026): .shared/queue/config.work calls
// queue.parallel(4) and queue.keep("30d"), .schedule/config.work schedule.keep("7d") (KITWORK_QUEUE.md mục 2,
// KITWORK_SCHEDULE.md mục 2). The const of 17/10 (const parallel = 4) is a compile error naming the call.

// CompileConfigFileInApp compiles a config.work of an app's .shared/queue/ or .schedule/. Its program runs the
// file's calls; the loader reads what each was handed from the module it binds (work/background_config.go).
// A call of the folder written anywhere but as a line of its own at the top level, chained, or twice, and the
// const of 17/10, are compile errors with file:line:column.
func CompileConfigFileInApp(appRoot, path string) (*Bytecode, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, err
	}
	sourceName := filepath.ToSlash(filepath.Base(absolute))
	prog, err := parseProgramSource(string(data), sourceName)
	if err != nil {
		return nil, err
	}
	calls, err := readFolderCalls(prog, folderOf(appRoot, absolute), data)
	if err != nil {
		return nil, err
	}
	files := []string{absolute}
	sources := map[string]string{sourceName: string(data)}
	if hasRelativeImports(prog) {
		var moduleFiles []string
		var moduleSources map[string]string
		prog, moduleFiles, moduleSources, err = nativeBundleWithSources(absolute, prog, appRoot, sources)
		if err != nil {
			return nil, err
		}
		files = append(files, moduleFiles...)
		for name, source := range moduleSources {
			sources[name] = source
		}
	}
	prog.App = appRoot != ""
	prog.FolderCalls = calls
	return compilePreparedFile(prog, files, sources)
}
