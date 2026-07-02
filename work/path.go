package work

import (
	"path"
	"strings"

	"github.com/kitwork/engine/value"
)

// PathUtil provides standard, sandboxed path manipulation utilities
// inspired by Node.js path module, executed purely in the VM.
type PathUtil struct{}

func (w *KitWork) Path() *PathUtil {
	return &PathUtil{}
}

// Join joins all given path segments together and cleans the resulting path.
func (pu *PathUtil) Join(args ...value.Value) value.Value {
	var parts []string
	for _, arg := range args {
		parts = append(parts, arg.String())
	}
	return value.New(path.Join(parts...))
}

// Clean lexical cleans a path, resolving .., . and duplicate slashes.
func (pu *PathUtil) Clean(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.New("")
	}
	return value.New(path.Clean(args[0].String()))
}

// Dirname returns the directory name of a path.
func (pu *PathUtil) Dirname(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.New("")
	}
	return value.New(path.Dir(args[0].String()))
}

// Basename returns the last portion of a path.
// If a second argument is passed, it will be stripped from the result if it matches the suffix.
func (pu *PathUtil) Basename(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.New("")
	}
	base := path.Base(args[0].String())
	if len(args) > 1 {
		suffix := args[1].String()
		if strings.HasSuffix(base, suffix) {
			base = strings.TrimSuffix(base, suffix)
		}
	}
	return value.New(base)
}

// Extname returns the extension of the path (from the last '.' to the end of the string).
func (pu *PathUtil) Extname(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.New("")
	}
	return value.New(path.Ext(args[0].String()))
}

// Resolve resolves path segments into an absolute-looking clean path relative to virtual root.
func (pu *PathUtil) Resolve(args ...value.Value) value.Value {
	var parts []string
	for _, arg := range args {
		parts = append(parts, arg.String())
	}
	joined := path.Join(parts...)
	if !strings.HasPrefix(joined, "/") {
		joined = "/" + joined
	}
	return value.New(path.Clean(joined))
}
