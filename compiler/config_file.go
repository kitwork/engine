package compiler

import (
	"os"
	"path/filepath"
)

// A config.work is written with const (KITWORK_UI.md mục 4): the engine reads the names the file declares
// at its top level, not what it exports. .shared/queue/config.work declares const parallel = 2 and
// const keep = "7d" (KITWORK_QUEUE.md mục 2).

// CompileConfigFileInApp compiles a config.work of an app: its program returns an object of every name the
// file declares at its top level, by that name; names gives them in the order written. A name an import
// binds is the import's, not a setting, and is left out.
func CompileConfigFileInApp(appRoot, path string) (*Bytecode, []string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = path
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return nil, nil, err
	}
	sourceName := filepath.ToSlash(filepath.Base(absolute))
	prog, err := parseProgramSource(string(data), sourceName)
	if err != nil {
		return nil, nil, err
	}
	var names []string
	for _, statement := range prog.Statements {
		binding, ok := statement.(*VarStatement)
		if !ok || fromKitwork(binding.Value) {
			continue
		}
		for _, name := range binding.Names {
			if name.Value != DefaultExportName {
				names = append(names, name.Value)
			}
		}
	}
	files := []string{absolute}
	sources := map[string]string{sourceName: string(data)}
	if hasRelativeImports(prog) {
		var moduleFiles []string
		var moduleSources map[string]string
		prog, moduleFiles, moduleSources, err = nativeBundleWithSources(absolute, prog, appRoot, sources)
		if err != nil {
			return nil, nil, err
		}
		files = append(files, moduleFiles...)
		for name, source := range moduleSources {
			sources[name] = source
		}
	}
	prog.App = appRoot != ""
	origin := Token{Source: sourceName, Position: int32(len(data))}
	entries := make([]ObjectEntry, 0, len(names))
	for _, name := range names {
		entries = append(entries, ObjectEntry{Key: ident(name, origin), Value: ident(name, origin)})
	}
	prog.Statements = append(prog.Statements, &ReturnStatement{
		Token:       tokenFrom(origin, Return),
		ReturnValue: &ObjectLiteral{Token: tokenFrom(origin, LeftBrace), Entries: entries},
	})
	bytecode, err := compilePreparedFile(prog, files, sources)
	return bytecode, names, err
}

// fromKitwork reports an expression read off kitwork(), what an import of a Kitwork module lowers to
// (import * as queue from "kitwork/queue" is const queue = kitwork().queue).
func fromKitwork(expression Expression) bool {
	for {
		switch node := expression.(type) {
		case *MemberExpression:
			expression = node.Object
		case *CallExpression:
			function, ok := node.Function.(*Identifier)
			return ok && function.Value == "kitwork"
		default:
			return false
		}
	}
}
