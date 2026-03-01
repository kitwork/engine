package script

import (
	"fmt"
	"path/filepath"

	"github.com/kitwork/engine/compiler"
)

func Bytecode(paths ...string) (*compiler.Bytecode, error) {
	if paths == nil {
		return nil, fmt.Errorf("path is required")
	}

	content, err := readFile(filepath.Join(paths...))
	if err != nil {
		return nil, err
	}

	l := compiler.NewLexer(content)
	p := compiler.NewParser(l)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("assemble error: %s", p.Errors()[0])
	}

	c := compiler.NewCompiler()
	if err := c.Compile(prog); err != nil {
		return nil, err
	}
	return c.ByteCodeResult(), nil
}
