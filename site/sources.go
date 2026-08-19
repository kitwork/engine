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

	"github.com/kitwork/engine/utilities/safepath"
)

type sourceFingerprint struct {
	exists bool
	sum    [sha256.Size]byte
}

type confinedSourceFingerprint struct {
	boundary *safepath.Boundary
	root     string
	relative string
	rootInfo os.FileInfo
	resolved string
	sourceFingerprint
}

// SourceManifest is the immutable set of executable sources, templates, and
// route directories used to prepare one generation.
type SourceManifest struct {
	mu sync.RWMutex

	files        map[string]sourceFingerprint
	confined     map[string]confinedSourceFingerprint
	directories  map[string]sourceFingerprint
	moduleDirs   map[string]sourceFingerprint
	templateDirs map[string]sourceFingerprint
	frozen       bool
}

func newSourceManifest() *SourceManifest {
	return &SourceManifest{
		files:        make(map[string]sourceFingerprint),
		confined:     make(map[string]confinedSourceFingerprint),
		directories:  make(map[string]sourceFingerprint),
		moduleDirs:   make(map[string]sourceFingerprint),
		templateDirs: make(map[string]sourceFingerprint),
	}
}

// WatchConfinedFileContent records bytes already consumed through an authored
// path while retaining the canonical site boundary and target selected during
// preparation. Changed re-resolves and verifies that boundary before opening
// the file, so a symlink, junction, or parent reparse-point retarget cannot
// make the hot-reload poller hash bytes outside the owning site.
func (m *SourceManifest) WatchConfinedFileContent(filename, root, resolved string, content []byte) error {
	if m == nil || filename == "" || root == "" || resolved == "" {
		return fmt.Errorf("site confined source watch requires filename, root, and resolved target")
	}
	boundary, err := safepath.NewBoundary(root)
	if err != nil {
		return fmt.Errorf("prepare confined source boundary %q: %w", root, err)
	}
	inside, err := boundary.Contains(resolved)
	if err != nil {
		return fmt.Errorf("verify confined source %q: %w", filename, err)
	}
	if !inside {
		return fmt.Errorf("site source %q escapes its prepared boundary", filename)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve confined source root %q: %w", root, err)
	}
	canonicalRoot, err = filepath.Abs(canonicalRoot)
	if err != nil {
		return fmt.Errorf("resolve confined source root %q: %w", root, err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)
	rootInfo, err := os.Stat(canonicalRoot)
	if err != nil {
		return fmt.Errorf("stat confined source root %q: %w", root, err)
	}
	if !rootInfo.IsDir() {
		return fmt.Errorf("confined source root %q is not a directory", root)
	}
	preparedRoot, err := os.OpenRoot(canonicalRoot)
	if err != nil {
		return fmt.Errorf("open confined source root %q: %w", root, err)
	}
	defer preparedRoot.Close()
	openedRootInfo, err := preparedRoot.Stat(".")
	if err != nil || !openedRootInfo.IsDir() || !os.SameFile(rootInfo, openedRootInfo) {
		return fmt.Errorf("confined source root %q changed while generation was being prepared", root)
	}
	inside, err = boundary.Contains(canonicalRoot)
	if err != nil || !inside {
		return fmt.Errorf("confined source root %q changed while generation was being prepared", root)
	}
	currentRootInfo, err := os.Stat(canonicalRoot)
	if err != nil || !os.SameFile(openedRootInfo, currentRootInfo) {
		return fmt.Errorf("confined source root %q changed while generation was being prepared", root)
	}
	rootInfo = openedRootInfo
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return fmt.Errorf("resolve confined source %q: %w", filename, err)
	}
	canonical, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("resolve confined source target %q: %w", resolved, err)
	}
	canonical = filepath.Clean(canonical)
	relative, err := filepath.Rel(canonicalRoot, canonical)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("site source %q cannot be opened through its prepared boundary", filename)
	}
	watch := confinedSourceFingerprint{
		boundary:          boundary,
		root:              canonicalRoot,
		relative:          relative,
		rootInfo:          rootInfo,
		resolved:          canonical,
		sourceFingerprint: sourceFingerprint{exists: true, sum: sha256.Sum256(content)},
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return fmt.Errorf("site source manifest is frozen")
	}
	absolute = filepath.Clean(absolute)
	if prior, exists := m.confined[absolute]; exists {
		if prior.root != watch.root || prior.relative != watch.relative || prior.resolved != watch.resolved ||
			prior.sourceFingerprint != watch.sourceFingerprint || !os.SameFile(prior.rootInfo, watch.rootInfo) {
			return fmt.Errorf("site source %q changed while generation was being prepared", absolute)
		}
	}
	m.confined[absolute] = watch
	return nil
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

// WatchFileContent records the exact bytes already consumed during generation
// preparation. It avoids a second read changing the manifest identity between
// snapshotting a tenant component and retaining its browser artifact.
func (m *SourceManifest) WatchFileContent(filename string, content []byte) error {
	if m == nil || filename == "" {
		return nil
	}
	fingerprint := sourceFingerprint{exists: true, sum: sha256.Sum256(content)}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frozen {
		return fmt.Errorf("site source manifest is frozen")
	}
	if prior, exists := m.files[filename]; exists && prior != fingerprint {
		return fmt.Errorf("site source %q changed while generation was being prepared", filename)
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
	confined := cloneConfinedFingerprints(m.confined)
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
	for filename, expected := range confined {
		current, changed, err := fingerprintConfinedFile(filename, expected)
		if err != nil {
			return false, err
		}
		if changed || current.resolved != expected.resolved || current.sourceFingerprint != expected.sourceFingerprint {
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

func cloneConfinedFingerprints(source map[string]confinedSourceFingerprint) map[string]confinedSourceFingerprint {
	clone := make(map[string]confinedSourceFingerprint, len(source))
	for filename, fingerprint := range source {
		clone[filename] = fingerprint
	}
	return clone
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

// fingerprintConfinedFile never opens a component through its authored path.
// It re-resolves that path only to prove that the prepared canonical target is
// unchanged, then opens the prepared relative target through an os.Root whose
// directory identity matches the prepared site root. os.Root rejects a
// symlink, junction, or reparse-point escape atomically during open. A second
// authored-path check and root-relative stat happen before hashing the handle.
func fingerprintConfinedFile(filename string, expected confinedSourceFingerprint) (confinedSourceFingerprint, bool, error) {
	resolved, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	resolved = filepath.Clean(resolved)
	inside, err := expected.boundary.Contains(resolved)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	if !inside || resolved != expected.resolved {
		return confinedSourceFingerprint{}, true, nil
	}

	root, err := os.OpenRoot(expected.root)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	defer root.Close()
	currentRootInfo, err := root.Stat(".")
	if err != nil || !os.SameFile(expected.rootInfo, currentRootInfo) {
		return confinedSourceFingerprint{}, true, nil
	}

	file, err := root.Open(expected.relative)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	if !openedInfo.Mode().IsRegular() {
		return confinedSourceFingerprint{}, true, nil
	}

	currentResolved, err := filepath.EvalSymlinks(filename)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	currentResolved, err = filepath.Abs(currentResolved)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	currentResolved = filepath.Clean(currentResolved)
	inside, err = expected.boundary.Contains(currentResolved)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	if !inside || currentResolved != expected.resolved {
		return confinedSourceFingerprint{}, true, nil
	}
	currentInfo, err := root.Stat(expected.relative)
	if err != nil {
		return confinedSourceFingerprint{}, true, nil
	}
	if !os.SameFile(openedInfo, currentInfo) {
		return confinedSourceFingerprint{}, true, nil
	}

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return confinedSourceFingerprint{}, false, fmt.Errorf("fingerprint confined source %q: %w", filename, err)
	}
	var sum [sha256.Size]byte
	copy(sum[:], hash.Sum(nil))
	return confinedSourceFingerprint{
		boundary:          expected.boundary,
		root:              expected.root,
		relative:          expected.relative,
		rootInfo:          currentRootInfo,
		resolved:          expected.resolved,
		sourceFingerprint: sourceFingerprint{exists: true, sum: sum},
	}, false, nil
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
