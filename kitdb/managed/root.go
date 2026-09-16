// Package managed owns the durable catalog for one standalone KitDB root.
// Tenant placement and application identity remain the hosting platform's job.
package managed

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/kitwork/engine/kitdb"
)

const MaximumDatabases = 4096
const maximumCatalogBytes = 1 << 20
const catalogKey = "kitdb/managed-root/catalog/v1"

const (
	CatalogDirectory      = ".catalog"
	LegacySystemDirectory = ".system"
)

type Database struct {
	Name       string `json:"name"`
	Directory  string `json:"directory"`
	DatabaseID string `json:"database_id"`
}

type Catalog struct {
	Version   int        `json:"version"`
	Databases []Database `json:"databases"`
}

// Root holds the exclusive .catalog database lock until Close. User databases
// are not opened by Open or Catalog. Registration is an offline operation.
type Root struct {
	mu      sync.Mutex
	path    string
	store   *kitdb.DB
	catalog Catalog
}

// Init creates a fresh catalog without adopting any existing database files.
// An existing catalog is never overwritten, including an interrupted init.
func Init(path string) (result Catalog, err error) {
	absolute, err := filepath.Abs(path)
	if err != nil || strings.TrimSpace(path) == "" {
		return result, fmt.Errorf("kitdb root: invalid root path %q", path)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return result, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return result, err
	}
	present, err := Present(absolute)
	if err != nil {
		return result, err
	}
	if present {
		return result, fmt.Errorf("kitdb root: catalog already exists")
	}
	if err := os.Mkdir(filepath.Join(absolute, CatalogDirectory), 0o700); err != nil {
		return result, fmt.Errorf("kitdb root: create fresh %s (never overwritten): %w", CatalogDirectory, err)
	}
	store, err := kitdb.OpenWithOptions(filepath.Join(absolute, CatalogDirectory, "data.kitdb"), catalogOptions())
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	result = Catalog{Version: 1, Databases: []Database{}}
	err = writeCatalog(store, result)
	return result, err
}

// Present only checks the explicitly selected root, never its ancestors.
// A malformed catalog still counts as present so callers fail closed at Open.
func Present(path string) (bool, error) {
	_, present, err := catalogDirectory(path)
	return present, err
}

func Open(path string) (*Root, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("kitdb root: root path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	directory, present, err := catalogDirectory(absolute)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, fmt.Errorf("kitdb root: %s is not initialized", CatalogDirectory)
	}
	if err := checkFile(absolute, directory); err != nil {
		return nil, err
	}
	store, err := kitdb.OpenWithOptions(filepath.Join(absolute, directory, "data.kitdb"), catalogOptions())
	if err != nil {
		return nil, fmt.Errorf("kitdb root: open %s: %w", directory, err)
	}
	catalog, err := readCatalog(store)
	if err != nil {
		return nil, errors.Join(err, store.Close())
	}
	return &Root{path: absolute, store: store, catalog: catalog}, nil
}

func catalogOptions() kitdb.OpenOptions {
	return kitdb.OpenOptions{PageCacheBytes: 256 << 10, CommitQueueSize: 4, MaxCommitBatch: 1}
}

func catalogDirectory(path string) (directory string, present bool, err error) {
	current, currentErr := directoryPresent(path, CatalogDirectory)
	legacy, legacyErr := directoryPresent(path, LegacySystemDirectory)
	if currentErr != nil {
		return "", false, currentErr
	}
	if legacyErr != nil {
		return "", false, legacyErr
	}
	if current && legacy {
		return "", false, fmt.Errorf(
			"kitdb root: both %s and legacy %s exist; catalog authority is ambiguous",
			CatalogDirectory,
			LegacySystemDirectory,
		)
	}
	if current {
		return CatalogDirectory, true, nil
	}
	if legacy {
		return LegacySystemDirectory, true, nil
	}
	return "", false, nil
}

func directoryPresent(path, name string) (bool, error) {
	_, err := os.Lstat(filepath.Join(path, name))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (root *Root) Catalog() Catalog {
	root.mu.Lock()
	defer root.mu.Unlock()
	return Catalog{Version: root.catalog.Version, Databases: slices.Clone(root.catalog.Databases)}
}

// Register publishes an already-created database with its verified identity.
// Retrying the same registration is idempotent. It never creates, renames or
// deletes the user's files, so publication is one ordinary catalog transaction.
func (root *Root) Register(name, directory string) (result Database, err error) {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil {
		return result, fmt.Errorf("kitdb root: closed")
	}
	if !safeName(name) || !safeName(directory) {
		return result, fmt.Errorf("kitdb root: name and directory must be safe ASCII basenames (1-63 characters)")
	}
	if err := checkFile(root.path, directory); err != nil {
		return result, err
	}
	database, err := kitdb.OpenWithOptions(filepath.Join(root.path, directory, "data.kitdb"), kitdb.OpenOptions{PageCacheBytes: -1})
	if err != nil {
		return result, err
	}
	result = Database{Name: name, Directory: directory, DatabaseID: database.ID()}
	if err := database.Close(); err != nil {
		return result, err
	}
	for _, existing := range root.catalog.Databases {
		if existing == result {
			return result, nil
		}
	}
	updated := Catalog{Version: 1, Databases: append(slices.Clone(root.catalog.Databases), result)}
	if err := validateCatalog(updated); err != nil {
		return Database{}, err
	}
	slices.SortFunc(updated.Databases, func(a, b Database) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	if err := writeCatalog(root.store, updated); err != nil {
		// An uncertain commit requires a fresh recovery before another mutation.
		closeErr := root.store.Close()
		root.store = nil
		return Database{}, errors.Join(err, closeErr)
	}
	root.catalog = updated
	return result, nil
}

// DatabasePath validates filesystem shape before lazy acquisition. Identity
// must additionally be compared against the handle returned by the kernel.
func (root *Root) DatabasePath(database Database) (string, error) {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil {
		return "", fmt.Errorf("kitdb root: closed")
	}
	if !slices.Contains(root.catalog.Databases, database) {
		return "", fmt.Errorf("kitdb root: database is not registered")
	}
	if err := checkFile(root.path, database.Directory); err != nil {
		return "", err
	}
	return filepath.Join(root.path, database.Directory, "data.kitdb"), nil
}

func (root *Root) Close() error {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil {
		return nil
	}
	err := root.store.Close()
	root.store = nil
	return err
}

func checkFile(root, directory string) error {
	dir := filepath.Join(root, directory)
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("kitdb root: inspect database directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("kitdb root: database directory must be a real directory")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(dir) {
		return fmt.Errorf("kitdb root: redirected database directory is not allowed")
	}
	for _, suffix := range []string{"", ".wal", ".lock"} {
		info, err := os.Lstat(filepath.Join(dir, "data.kitdb"+suffix))
		if suffix != "" && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("kitdb root: inspect database file: %w", err)
		}
		if !info.Mode().IsRegular() || (suffix == "" && info.Size() < 4096) {
			return fmt.Errorf("kitdb root: database must be an existing regular KitDB file")
		}
	}
	return nil
}

func safeName(name string) bool {
	if len(name) < 1 || len(name) > 63 {
		return false
	}
	for i, ch := range []byte(name) {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' ||
			(i > 0 && ((ch >= '0' && ch <= '9') || ch == '-')) {
			continue
		}
		return false
	}
	return true
}

func validateCatalog(catalog Catalog) error {
	if catalog.Version != 1 || len(catalog.Databases) > MaximumDatabases {
		return fmt.Errorf("kitdb root: unsupported catalog version or database limit exceeded")
	}
	names, paths, ids := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, entry := range catalog.Databases {
		identity, err := hex.DecodeString(entry.DatabaseID)
		if !safeName(entry.Name) || !safeName(entry.Directory) || err != nil || len(identity) != 16 ||
			entry.DatabaseID != strings.ToLower(entry.DatabaseID) {
			return fmt.Errorf("kitdb root: invalid catalog entry")
		}
		name, path := strings.ToLower(entry.Name), strings.ToLower(entry.Directory)
		if names[name] || paths[path] || ids[entry.DatabaseID] {
			return fmt.Errorf("kitdb root: duplicate database name, directory or identity")
		}
		names[name], paths[path], ids[entry.DatabaseID] = true, true, true
	}
	return nil
}

func readCatalog(store *kitdb.DB) (Catalog, error) {
	data, found, err := store.Get([]byte(catalogKey))
	if err != nil {
		return Catalog{}, err
	}
	if !found || len(data) > maximumCatalogBytes {
		return Catalog{}, fmt.Errorf("kitdb root: missing or oversized catalog; catalog is not initialized")
	}
	var catalog Catalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("kitdb root: invalid catalog: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Catalog{}, fmt.Errorf("kitdb root: trailing catalog data")
	}
	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func writeCatalog(store *kitdb.DB, catalog Catalog) error {
	data, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	if len(data) > maximumCatalogBytes {
		return fmt.Errorf("kitdb root: catalog exceeds bounded size")
	}
	tx, err := store.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Put([]byte(catalogKey), data); err != nil {
		return err
	}
	_, err = tx.Commit()
	return err
}
