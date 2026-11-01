package catalog

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/pgwire"
)

// The keys and shapes of kitdb/managed's catalog (engine/kitdb/managed: root.go, auth.go), read here without
// importing that package.
const (
	kitdbAuthKey     = "kitdb/managed-root/auth/v1"
	kitdbCatalogKey  = "kitdb/managed-root/catalog/v1"
	kitdbImport      = "kitdb"
	maximumKitDBData = 1 << 20
)

type kitdbGrant struct {
	LocalRoles  []string `json:"local_roles,omitempty"`
	Database    string   `json:"database"`
	Role        string   `json:"role,omitempty"`
	Permissions []string `json:"permissions,omitempty"`
}

type kitdbUser struct {
	Name     string       `json:"name"`
	Admin    bool         `json:"admin"`
	Disabled bool         `json:"disabled"`
	Grants   []kitdbGrant `json:"grants"`
	Salt     string       `json:"salt"`
	Verifier string       `json:"verifier"`
}

type kitdbAuth struct {
	Version    int         `json:"version"`
	Iterations int         `json:"iterations"`
	Users      []kitdbUser `json:"users"`
}

type kitdbDatabase struct {
	Name       string `json:"name"`
	Directory  string `json:"directory"`
	DatabaseID string `json:"database_id"`
}

type kitdbCatalog struct {
	Version      int             `json:"version"`
	Databases    []kitdbDatabase `json:"databases"`
	AuthRequired bool            `json:"auth_required,omitempty"`
}

// ImportResult is what an import of the old KitDB catalog did. It never holds a password, salt or verifier.
type ImportResult struct {
	// Imported counts the accounts written.
	Imported int
	// AlreadyImported is true when the catalog had imported before: nothing was read.
	AlreadyImported bool
	// Notes says, one line each, what the import could not carry or chose (the port is read-only; a name taken).
	Notes []string
}

// ImportKitDB copies the accounts of the old KitDB catalog (<cluster>/.catalog/data.kitdb, kitdb/managed's JSON blob
// "kitdb/managed-root/auth/v1") into this catalog, verifiers unchanged, once (a mark row); the KitDB file is read from
// a private read-only copy and never written. Each note of ImportKitDBReport is logged.
func (c *Catalog) ImportKitDB(ctx context.Context, path string) (imported int, err error) {
	result, err := c.ImportKitDBReport(ctx, path)
	for _, note := range result.Notes {
		log.Printf("kitdb catalog: import of %s: %s", path, note)
	}
	return result.Imported, err
}

// ImportKitDBReport is ImportKitDB, saying what it did instead of logging it.
//
// The file and every file beside it sharing its name (its .wal and the like, not its .lock) are copied into a new
// temporary folder, opened there with kitdb.OpenReplica, read, and removed. Accounts are written in one transaction
// with the mark; a second import finds the mark and reads nothing. A missing file is nothing to import, without a
// mark. Each KitDB account becomes a user with the same name and the same verifier (the password works as before):
//   - an administrator stays an admin of this cluster (account statements, every database and table);
//   - reader, writer and developer on a database, or the permission connect, give CONNECT on it; reader, writer,
//     developer, or read give SELECT on every table of its schema public (a KitDB database had no other schema);
//     writes and schema changes are not carried: the port is read-only;
//   - a disabled account stays disabled; database-local roles stay in the KitDB database and are not carried;
//   - an account named as the host admin, or as a user or role already in this catalog, is skipped.
func (c *Catalog) ImportKitDBReport(ctx context.Context, path string) (ImportResult, error) {
	var result ImportResult
	if ctx == nil {
		return result, errors.New("catalog: nil context")
	}
	var marked bool
	if err := c.database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM imports WHERE source = ?)`, kitdbImport).Scan(&marked); err != nil {
		return result, fmt.Errorf("catalog: read imports: %w", err)
	}
	if marked {
		result.AlreadyImported = true
		return result, nil
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		result.Notes = append(result.Notes, "no KitDB catalog there: nothing to import")
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("catalog: import: %w", err)
	}
	if !info.Mode().IsRegular() {
		return result, fmt.Errorf("catalog: import: %s is not a regular file", path)
	}
	catalog, auth, err := readKitDB(path)
	if err != nil {
		return result, err
	}
	err = c.write(ctx, func(work *transaction) error {
		marked, err := work.exists(`SELECT 1 FROM imports WHERE source = ?`, kitdbImport)
		if err != nil {
			return err
		}
		if marked {
			result.AlreadyImported = true
			return nil
		}
		if auth == nil {
			result.Notes = append(result.Notes, "the KitDB catalog has no accounts (its auth was never enabled)")
		} else {
			for _, user := range auth.Users {
				written, err := c.importUser(work, catalog, auth, user, &result)
				if err != nil {
					return err
				}
				if written {
					result.Imported++
				}
			}
		}
		_, err = work.exec(`INSERT INTO imports (source, users, imported_at) VALUES (?, ?, ?)`,
			kitdbImport, result.Imported, time.Now().UTC().Format(time.RFC3339))
		return err
	})
	if err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

// readKitDB reads the catalog and the accounts (nil when auth was never enabled) of the KitDB catalog at path, from a
// private copy.
func readKitDB(path string) (*kitdbCatalog, *kitdbAuth, error) {
	temporary, err := os.MkdirTemp("", "kitwork-catalog-import-*")
	if err != nil {
		return nil, nil, fmt.Errorf("catalog: import: %w", err)
	}
	defer os.RemoveAll(temporary)
	copied, err := copyKitDB(path, temporary)
	if err != nil {
		return nil, nil, err
	}
	store, err := kitdb.OpenReplica(copied, kitdb.OpenOptions{PageCacheBytes: 256 << 10})
	if err != nil {
		return nil, nil, fmt.Errorf("catalog: import: open the copy of %s: %w", path, err)
	}
	catalogData, catalogFound, catalogErr := store.Get([]byte(kitdbCatalogKey))
	authData, authFound, authErr := store.Get([]byte(kitdbAuthKey))
	if err := errors.Join(catalogErr, authErr, store.Close()); err != nil {
		return nil, nil, fmt.Errorf("catalog: import: read %s: %w", path, err)
	}
	if !catalogFound {
		return nil, nil, fmt.Errorf("catalog: import: %s holds no KitDB catalog", path)
	}
	var catalog kitdbCatalog
	if err := decodeKitDB(catalogData, &catalog); err != nil {
		return nil, nil, fmt.Errorf("catalog: import: %s: catalog: %w", path, err)
	}
	if !authFound {
		return &catalog, nil, nil
	}
	var auth kitdbAuth
	if err := decodeKitDB(authData, &auth); err != nil {
		return nil, nil, fmt.Errorf("catalog: import: %s: accounts: %w", path, err)
	}
	if auth.Version < 1 || auth.Version > 3 || auth.Iterations < minimumIterations || auth.Iterations > maximumIterations {
		return nil, nil, fmt.Errorf("catalog: import: %s: accounts of version %d with %d iterations are not known", path, auth.Version, auth.Iterations)
	}
	return &catalog, &auth, nil
}

func decodeKitDB(data []byte, target any) error {
	if len(data) > maximumKitDBData {
		return errors.New("over 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return errors.New("not in KitDB's encoding")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

// copyKitDB copies path and the regular files beside it sharing its name, its .lock aside, into destination, and
// returns the copy of path.
func copyKitDB(path, destination string) (string, error) {
	directory, base := filepath.Split(filepath.Clean(path))
	if directory == "" {
		directory = "."
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", fmt.Errorf("catalog: import: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if (name != base && !strings.HasPrefix(name, base+".")) || name == base+".lock" {
			continue
		}
		info, err := os.Lstat(filepath.Join(directory, name))
		if err != nil {
			return "", fmt.Errorf("catalog: import: %w", err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := copyFile(filepath.Join(directory, name), filepath.Join(destination, name)); err != nil {
			return "", fmt.Errorf("catalog: import: copy %s: %w", name, err)
		}
	}
	return filepath.Join(destination, base), nil
}

func copyFile(source, destination string) (err error) {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, output.Close()) }()
	_, err = io.Copy(output, input)
	return err
}

// importUser writes one KitDB account; written is false when it was skipped (a note says why).
func (c *Catalog) importUser(work *transaction, catalog *kitdbCatalog, auth *kitdbAuth, user kitdbUser, result *ImportResult) (written bool, err error) {
	note := func(format string, arguments ...any) {
		result.Notes = append(result.Notes, fmt.Sprintf("%q: ", user.Name)+fmt.Sprintf(format, arguments...))
	}
	salt, saltErr := hex.DecodeString(user.Salt)
	salted, saltedErr := hex.DecodeString(user.Verifier)
	if saltErr != nil || saltedErr != nil || len(salt) < minimumSaltBytes || len(salt) > maximumSaltBytes || len(salted) != 32 {
		return false, fmt.Errorf("catalog: import: account %q has a malformed verifier", user.Name)
	}
	if err := checkNewName(user.Name); err != nil {
		note("skipped: %v", err)
		return false, nil
	}
	if c.admin != "" && user.Name == c.admin {
		note("skipped: the host admin has this name")
		return false, nil
	}
	kind, err := work.kind(user.Name)
	if err != nil {
		return false, err
	}
	if kind != "" {
		note("skipped: a %s of this catalog has this name", kind)
		return false, nil
	}
	verifier := pgwire.SCRAMVerifierFromSaltedPassword(salt, auth.Iterations, salted)
	clear(salted)
	stamp, err := work.nextStamp()
	if err != nil {
		return false, err
	}
	if _, err := work.exec(`INSERT INTO users (name, salt, iterations, stored_key, server_key, admin, disabled, stamp)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		user.Name, verifier.Salt, verifier.Iterations, verifier.StoredKey[:], verifier.ServerKey[:], user.Admin, user.Disabled, stamp); err != nil {
		return false, err
	}
	if user.Admin {
		note("a KitDB administrator, stays an admin of this cluster")
	}
	if user.Disabled {
		note("disabled in KitDB, stays disabled")
	}
	for _, grant := range user.Grants {
		database := grant.Database
		if index := slices.IndexFunc(catalog.Databases, func(registered kitdbDatabase) bool {
			return strings.EqualFold(registered.Name, grant.Database)
		}); index >= 0 {
			database = catalog.Databases[index].Name
		}
		connect, read := false, false
		switch grant.Role {
		case "reader":
			connect, read = true, true
		case "writer", "developer":
			connect, read = true, true
			note("%s on %q becomes CONNECT and SELECT on every table of schema public: the port is read-only", grant.Role, database)
		case "":
			var dropped []string
			for _, permission := range grant.Permissions {
				switch permission {
				case "connect":
					connect = true
				case "read":
					read = true
				default:
					dropped = append(dropped, permission)
				}
			}
			if len(dropped) > 0 {
				note("%s on %q not carried: the port is read-only", strings.Join(dropped, ", "), database)
			}
		default:
			note("unknown KitDB role %q on %q not carried", grant.Role, database)
		}
		if len(grant.LocalRoles) > 0 {
			note("%d database-local role(s) of %q stay in KitDB: grant its tables again with GRANT SELECT", len(grant.LocalRoles), database)
		}
		if connect {
			if _, err := work.exec(`INSERT OR IGNORE INTO grants (grantee, privilege, database_name) VALUES (?, 'connect', ?)`,
				user.Name, database); err != nil {
				return false, err
			}
		}
		if read {
			if _, err := work.exec(`INSERT OR IGNORE INTO grants (grantee, privilege, database_name, schema_name, table_name)
				VALUES (?, 'select', ?, 'public', '')`, user.Name, database); err != nil {
				return false, err
			}
		}
	}
	return true, nil
}
