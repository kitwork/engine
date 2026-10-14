package managed

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/kitwork/engine/kitdb"
)

// Detach is offline, never deletes files, and atomically removes every grant
// for the registration. Missing files may be unregistered; an existing file
// must be exclusively acquired before publication. A replacement identity can
// be detached to repair a stale registration, but is never adopted implicitly.
func (root *Root) Detach(name string) (result Database, err error) {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil {
		return result, fmt.Errorf("kitdb root: closed")
	}
	if !safeName(name) {
		return result, fmt.Errorf("kitdb root: invalid database name")
	}
	index := slices.IndexFunc(root.catalog.Databases, func(db Database) bool { return strings.EqualFold(db.Name, name) })
	if index < 0 {
		return result, fmt.Errorf("kitdb root: database is not registered")
	}
	result = root.catalog.Databases[index]
	if err := checkFile(root.path, result.Directory); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Database{}, err
		}
	} else {
		db, err := kitdb.OpenWithOptions(filepath.Join(root.path, result.Directory, "data.kitdb"), kitdb.OpenOptions{PageCacheBytes: -1})
		if err != nil {
			return Database{}, fmt.Errorf("kitdb root: database is busy or unavailable: %w", err)
		}
		defer func() { err = errors.Join(err, db.Close()) }()
	}
	updated := root.catalog
	updated.Databases = append(slices.Clone(root.catalog.Databases[:index]), root.catalog.Databases[index+1:]...)
	if err := validateCatalog(updated); err != nil {
		return Database{}, err
	}
	if root.auth != nil {
		auth := *root.auth
		auth.Users = slices.Clone(root.auth.Users)
		for i := range auth.Users {
			auth.Users[i].Grants = slices.DeleteFunc(cloneGrants(auth.Users[i].Grants), func(grant DatabaseGrant) bool {
				return strings.EqualFold(grant.Database, result.Name)
			})
		}
		if err := root.publishAuth(updated, &auth); err != nil {
			return Database{}, err
		}
		return result, nil
	}
	if err := writeCatalog(root.store, updated); err != nil {
		closeErr := root.store.Close()
		root.store = nil
		return Database{}, errors.Join(err, closeErr)
	}
	root.catalog = updated
	return result, nil
}
