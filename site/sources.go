package site

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type sourceFingerprint struct {
	exists bool
	sum    [sha256.Size]byte
}

// SourceManifest is the immutable set of executable sources, templates, and
// route directories used to prepare one generation.
type SourceManifest struct {
	mu sync.RWMutex

	files        map[string]sourceFingerprint
	directories  map[string]sourceFingerprint
	moduleDirs   map[string]sourceFingerprint
	templateDirs map[string]sourceFingerprint
	frozen       bool
}

func newSourceManifest() *SourceManifest {
	return &SourceManifest{
		files:        make(map[string]sourceFingerprint),
		directories:  make(map[string]sourceFingerprint),
		moduleDirs:   make(map[string]sourceFingerprint),
		templateDirs: make(map[string]sourceFingerprint),
	}
}

// WatchTemplateTree records every HTML template name and content below a site
// root. It detects edits, additions, and removals as one generation boundary.
func (m *SourceManifest) WatchTemplateTree(root string) error {
	if m == nil || root == "" {
		return nil
	}
	fingerprint, err := fingerprintTemplateTree(root)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return fmt.Errorf("site source manifest is frozen")
	}
	m.templateDirs[root] = fingerprint
	return nil
}

// WatchModuleDirectory records the .kitwork.js members of a side-effect
// directory import. Member file contents are watched separately.
func (m *SourceManifest) WatchModuleDirectory(directory string) error {
	if m == nil || directory == "" {
		return nil
	}
	fingerprint, err := fingerprintModuleDirectory(directory)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return fmt.Errorf("site source manifest is frozen")
	}
	m.moduleDirs[directory] = fingerprint
	return nil
}

// WatchFile records the exact content used by an executable source file.
// Missing files are valid entries so a router appearing later is detectable.
func (m *SourceManifest) WatchFile(filename string) error {
	if m == nil || filename == "" {
		return nil
	}
	fingerprint, err := fingerprintFile(filename)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return fmt.Errorf("site source manifest is frozen")
	}
	m.files[filename] = fingerprint
	return nil
}

// WatchDirectory records immediate public child-directory names. This detects
// route creation/removal while ignoring templates, runtime data, private
// folders, and files inside static asset trees.
func (m *SourceManifest) WatchDirectory(directory string) error {
	if m == nil || directory == "" {
		return nil
	}
	fingerprint, err := fingerprintDirectory(directory)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return fmt.Errorf("site source manifest is frozen")
	}
	m.directories[directory] = fingerprint
	return nil
}

func (m *SourceManifest) Freeze() {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.frozen = true
	m.mu.Unlock()
}

func (m *SourceManifest) Frozen() bool {
	if m == nil {
		return true
	}
	m.mu.RLock()
	frozen := m.frozen
	m.mu.RUnlock()
	return frozen
}

// Changed reports whether any executable source changed or any watched route
// directory gained/lost an entry.
func (m *SourceManifest) Changed() (bool, error) {
	if m == nil {
		return false, nil
	}
	m.mu.RLock()
	files := cloneFingerprints(m.files)
	directories := cloneFingerprints(m.directories)
	moduleDirs := cloneFingerprints(m.moduleDirs)
	templateDirs := cloneFingerprints(m.templateDirs)
	m.mu.RUnlock()

	for filename, expected := range files {
		current, err := fingerprintFile(filename)
		if err != nil {
			return false, err
		}
		if current != expected {
			return true, nil
		}
	}
	for directory, expected := range directories {
		current, err := fingerprintDirectory(directory)
		if err != nil {
			return false, err
		}
		if current != expected {
			return true, nil
		}
	}
	for directory, expected := range moduleDirs {
		current, err := fingerprintModuleDirectory(directory)
		if err != nil {
			return false, err
		}
		if current != expected {
			return true, nil
		}
	}
	for root, expected := range templateDirs {
		current, err := fingerprintTemplateTree(root)
		if err != nil {
			return false, err
		}
		if current != expected {
			return true, nil
		}
	}
	return false, nil
}

func cloneFingerprints(source map[string]sourceFingerprint) map[string]sourceFingerprint {
	clone := make(map[string]sourceFingerprint, len(source))
	for filename, fingerprint := range source {
		clone[filename] = fingerprint
	}
	return clone
}

func fingerprintFile(filename string) (sourceFingerprint, error) {
	file, err := os.Open(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return sourceFingerprint{}, nil
		}
		return sourceFingerprint{}, fmt.Errorf("fingerprint source file %q: %w", filename, err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return sourceFingerprint{}, fmt.Errorf("fingerprint source file %q: %w", filename, err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sourceFingerprint{exists: true, sum: sum}, nil
}

func fingerprintDirectory(directory string) (sourceFingerprint, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return sourceFingerprint{}, nil
		}
		return sourceFingerprint{}, fmt.Errorf("fingerprint route directory %q: %w", directory, err)
	}

	hash := sha256.New()
	for _, entry := range entries {
		name := entry.Name()
		if !entry.IsDir() || name == "" || name[0] == '.' || name[0] == '_' {
			continue
		}
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write([]byte{0})
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sourceFingerprint{exists: true, sum: sum}, nil
}

func fingerprintModuleDirectory(directory string) (sourceFingerprint, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return sourceFingerprint{}, nil
		}
		return sourceFingerprint{}, fmt.Errorf("fingerprint module directory %q: %w", directory, err)
	}

	hash := sha256.New()
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) < len(".kitwork.js") ||
			name[len(name)-len(".kitwork.js"):] != ".kitwork.js" {
			continue
		}
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write([]byte{0})
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sourceFingerprint{exists: true, sum: sum}, nil
}

func fingerprintTemplateTree(root string) (sourceFingerprint, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return sourceFingerprint{}, fmt.Errorf("resolve template root %q: %w", root, err)
	}
	if _, err := os.Stat(absolute); err != nil {
		if os.IsNotExist(err) {
			return sourceFingerprint{}, nil
		}
		return sourceFingerprint{}, fmt.Errorf("stat template root %q: %w", absolute, err)
	}

	hash := sha256.New()
	err = filepath.WalkDir(absolute, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename != absolute && entry.IsDir() && strings.HasPrefix(entry.Name(), ".") {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() ||
			!strings.HasSuffix(strings.ToLower(entry.Name()), ".html") {
			return nil
		}
		relative, relErr := filepath.Rel(absolute, filename)
		if relErr != nil {
			return relErr
		}
		fileHash, hashErr := fingerprintFile(filename)
		if hashErr != nil {
			return hashErr
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(fileHash.sum[:])
		_, _ = hash.Write([]byte{0})
		return nil
	})
	if err != nil {
		return sourceFingerprint{}, fmt.Errorf("fingerprint template tree %q: %w", absolute, err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return sourceFingerprint{exists: true, sum: sum}, nil
}
