package script

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/kitwork/engine/compiler"
)

// esbuild plugin for virtual "kitwork" module resolution
var kitworkPlugin = api.Plugin{
	Name: "kitwork-virtual",
	Setup: func(build api.PluginBuild) {
		// Resolve imports of "kitwork" or "kitwork/*" to a virtual namespace
		build.OnResolve(api.OnResolveOptions{Filter: `^kitwork(/.*)?$`}, func(args api.OnResolveArgs) (api.OnResolveResult, error) {
			return api.OnResolveResult{
				Path:      args.Path,
				Namespace: "kitwork-ns",
			}, nil
		})

		// Load virtual content when loading from the "kitwork-ns" namespace
		build.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "kitwork-ns"}, func(args api.OnLoadArgs) (api.OnLoadResult, error) {
			var contents string
			switch args.Path {
			case "kitwork":
				contents = `export const { router, log, render, http, database, go } = kitwork();`
			case "kitwork/router":
				contents = `export const router = kitwork().router; export default router;`
			case "kitwork/log":
				contents = `export const log = kitwork().log; export default log;`
			case "kitwork/render":
				contents = `export const render = kitwork().render; export default render;`
			case "kitwork/http":
				contents = `export const http = kitwork().http; export default http;`
			case "kitwork/database":
				contents = `export const database = kitwork().database; export default database;`
			case "kitwork/go":
				contents = `export const go = kitwork().go; export default go;`
			default:
				return api.OnLoadResult{}, fmt.Errorf("unknown virtual module path: %s", args.Path)
			}

			return api.OnLoadResult{
				Contents: &contents,
				Loader:   api.LoaderJS,
			}, nil
		})
	},
}

func bundleJavaScript(entryPath string) (string, error) {
	absPath, err := filepath.Abs(entryPath)
	if err != nil {
		absPath = entryPath
	}

	result := api.Build(api.BuildOptions{
		EntryPoints:       []string{absPath},
		Bundle:            true,
		Write:             false,
		Target:            api.ESNext,
		Format:            api.FormatESModule,
		Plugins:           []api.Plugin{kitworkPlugin},
		ResolveExtensions: []string{".kitwork.js", ".js", ".json"},
	})


	if len(result.Errors) > 0 {
		return "", fmt.Errorf("esbuild error: %s", result.Errors[0].Text)
	}

	if len(result.OutputFiles) == 0 {
		return "", fmt.Errorf("esbuild failed: no output files generated")
	}

	return string(result.OutputFiles[0].Contents), nil
}

func Bytecode(paths ...string) (*compiler.Bytecode, error) {
	if paths == nil {
		return nil, fmt.Errorf("path is required")
	}

	entryPath := filepath.Join(paths...)

	// Read the raw content first
	content, err := readFile(entryPath)
	if err != nil {
		return nil, err
	}

	// Optimize: Only run esbuild bundling if the file actually imports or exports modules.
	// This preserves exact original line numbers for standard single-file scripts.
	hasImports := strings.Contains(content, "import ") || strings.Contains(content, "import{") ||
		strings.Contains(content, "export ") || strings.Contains(content, "export{")
	
	if hasImports {
		content, err = bundleJavaScript(entryPath)
		if err != nil {
			return nil, err
		}
	}

	l := compiler.NewLexer(content)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("assemble error: %s", p.Errors()[0])
	}

	c := compiler.NewCompiler(content)
	if err := c.Compile(prog); err != nil {
		return nil, err
	}
	return c.ByteCodeResult(), nil
}
