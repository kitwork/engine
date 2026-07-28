package render

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// Source is the immutable file view used while assembling templates.
type Source interface {
	ReadFile(string) ([]byte, error)
	Exists(string) bool
}

// Snapshot is a generation-scoped copy of every HTML template below one site
// root. Paths are stored as cleaned absolute filenames.
type Snapshot struct {
	root string

	mu     sync.RWMutex
	files  map[string][]byte
	closed bool
}

// NewSnapshot reads all HTML templates below root. Hidden runtime directories
// and symlinks are excluded.
func NewSnapshot(root string) (*Snapshot, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve template root: %w", err)
	}
	absolute = filepath.Clean(absolute)
	snapshot := &Snapshot{
		root:  absolute,
		files: make(map[string][]byte),
	}
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
		content, readErr := os.ReadFile(filename)
		if readErr != nil {
			return readErr
		}
		snapshot.files[filepath.Clean(filename)] = append([]byte(nil), content...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("snapshot templates below %q: %w", absolute, err)
	}
	return snapshot, nil
}

func (s *Snapshot) inside(filename string) (string, bool) {
	if s == nil {
		return "", false
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", false
	}
	absolute = filepath.Clean(absolute)
	relative, err := filepath.Rel(s.root, absolute)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	return absolute, true
}

func (s *Snapshot) ReadFile(filename string) ([]byte, error) {
	absolute, ok := s.inside(filename)
	if !ok {
		return nil, os.ErrNotExist
	}
	s.mu.RLock()
	content, exists := s.files[absolute]
	closed := s.closed
	s.mu.RUnlock()
	if closed || !exists {
		return nil, os.ErrNotExist
	}
	return append([]byte(nil), content...), nil
}

func (s *Snapshot) Exists(filename string) bool {
	absolute, ok := s.inside(filename)
	if !ok {
		return false
	}
	s.mu.RLock()
	_, exists := s.files[absolute]
	closed := s.closed
	s.mu.RUnlock()
	return exists && !closed
}

// Files returns the immutable template filenames represented by this snapshot.
func (s *Snapshot) Files() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	files := make([]string, 0, len(s.files))
	if !s.closed {
		for filename := range s.files {
			files = append(files, filename)
		}
	}
	s.mu.RUnlock()
	return files
}

func (s *Snapshot) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.files = nil
	s.mu.Unlock()
}
