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
//
// A config.work of the app's folder, of a site or of a branch is the rules of the branch split out of
// router.work (KITWORK_ROUTER.md mục 4, 24/10/2026): it is read by the rules of the router (router_rules.go),
// where a door is a compile error, and its program runs the router calls it writes.
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
	module := folderOf(appRoot, absolute)
	calls, err := readFolderCalls(prog, module, data)
	if err != nil {
		return nil, err
	}
	// the config.work of the app's folder, a site or a branch is the router's (router_rules.go), as prepareFile
	// reads it; the one of .shared/queue/ or .schedule/, or of a folder that is no route (.shared/, _drafts/),
	// has no router
	routerKind := routerFileOf(appRoot, absolute)
	var routerCalls *RouterCalls
	if routerKind == routerConfigFile {
		if routerCalls, err = readRouterFile(prog, routerConfigFile, routerDepth(appRoot, absolute), data, routerPlace{appRoot: appRoot, file: absolute}); err != nil {
			return nil, err
		}
	} else if err := refuseRouterImport(prog, data); err != nil {
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
	prog.RouterConfig = routerKind == routerConfigFile
	prog.RouterCalls = routerCalls
	return compilePreparedFile(prog, files, sources)
}
