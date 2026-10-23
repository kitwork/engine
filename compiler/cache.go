package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileCache is an opt-in, directory-scoped bytecode artifact cache.
// A caller owns its directory and lifecycle. Cache read/write failures never
// prevent source compilation; a malformed or stale artifact under the current
// name is replaced. An artifact of another compiler fingerprint (an older schema
// or bytecode version) or of an earlier source lives under another name: it is
// never read, replaced or removed, so the directory grows with each schema bump
// and each source edit until its owner clears it (15/10/2026).
type FileCache struct {
	directory string
	mu        sync.Mutex
}

func NewFileCache(directory string) *FileCache {
	return &FileCache{directory: directory}
}

// CompileFile loads a compatible artifact or compiles and replaces it. Source
// parsing and native import discovery still run first so the cache key covers
// the exact current dependency contents.
func (c *FileCache) CompileFile(paths ...string) (*Bytecode, error) {
	return c.CompileFileInApp("", paths...)
}

// CompileFileInApp is CompileFile for a file of the app whose folder is appRoot
// (CompileFileInApp of the package). The artifact's name carries the app's
// folder: one cache directory serves every app of a host, and two apps never
// share what their `app/…` imports resolved to.
func (c *FileCache) CompileFileInApp(appRoot string, paths ...string) (*Bytecode, error) {
	if c == nil || c.directory == "" {
		return CompileFileInApp(appRoot, paths...)
	}

	prog, files, sources, err := prepareFile(appRoot, paths...)
	if err != nil {
		return nil, err
	}
	sourceFingerprint := fingerprintSources(sources)
	filename := filepath.Join(c.directory, cacheKeyForApp(appRoot, sourceFingerprint)+".kwbc")

	c.mu.Lock()
	defer c.mu.Unlock()

	if data, readErr := os.ReadFile(filename); readErr == nil {
		if cached, decodeErr := UnmarshalBytecode(data, sourceFingerprint); decodeErr == nil {
			cached.Files = append([]string(nil), files...)
			// what the boot checks of environment is read off the program again: an artifact holds code
			// alone, and a .env changed since it was written must still be checked (environment.go)
			scopes, err := newCompilerWithSources(sources).resolveProgram(prog)
			if err != nil {
				return nil, err
			}
			cached.EnvironmentRequires = scopes.requires
			cached.EnvironmentServerOnly = environmentServerOnly(scopes)
			return cached, nil
		}
	}

	bytecode, err := compilePreparedFile(prog, files, sources)
	if err != nil {
		return nil, err
	}
	data, encodeErr := bytecode.MarshalBinary()
	if encodeErr == nil {
		_ = writeCacheArtifact(filename, data)
	}
	return bytecode, nil
}

func writeCacheArtifact(filename string, data []byte) error {
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(directory, ".kitwork-bytecode-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, filename); err == nil {
		return nil
	}

	// Windows cannot atomically replace an existing file. This cache is
	// reconstructible, and FileCache serializes access within the process.
	if err := os.Remove(filename); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("replace bytecode cache: %w", err)
	}
	if err := os.Rename(tempName, filename); err != nil {
		return fmt.Errorf("publish bytecode cache: %w", err)
	}
	return nil
}
