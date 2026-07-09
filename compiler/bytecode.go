package compiler

import (
	"fmt"
	"os"
	"path/filepath"
)

// parseProgram lexes + parses source into an AST, returning the first parser
// error (if any).
func parseProgram(content string) (*Program, error) {
	l := NewLexer(content)
	p := NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("assemble error: %s", p.Errors()[0])
	}
	return prog, nil
}

// Bytecode compiles a tenant entry file to bytecode. import/export are FULLY
// native, no esbuild:
//   - `import … from "kitwork"` / `import x from "kitwork/sub"` → kitwork() bindings
//   - `import { a as b } from …` → member bindings (`const b = ….a`)
//   - relative modules (`./x`, `../x`) → resolved + IIFE-wrapped by nativeBundle
//   - `export …` / `export default …` → tracked for the bundler
//
// The hand-written parser is the single source of truth for the Kitwork language
// subset: anything it can't express is a compile error (by design), not silently
// normalized by a bundler.
func CompileFile(paths ...string) (*Bytecode, error) {
	if paths == nil {
		return nil, fmt.Errorf("path is required")
	}

	entryPath := filepath.Join(paths...)

	data, err := os.ReadFile(entryPath)
	if err != nil {
		return nil, err
	}
	content := string(data)
	if err != nil {
		return nil, err
	}

	prog, err := parseProgram(content)
	if err != nil {
		return nil, err
	}
	files := []string{entryPath}
	if hasRelativeImports(prog) {
		var moduleFiles []string
		prog, moduleFiles, err = nativeBundle(entryPath, prog)
		if err != nil {
			return nil, err
		}
		files = append(files, moduleFiles...)
	}

	c := NewCompiler(content)
	if err := c.Compile(prog); err != nil {
		return nil, err
	}
	bc := c.ByteCodeResult()
	bc.Files = files
	return bc, nil
}
