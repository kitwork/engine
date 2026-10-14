package managed

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/kitwork/engine/kitdb"
)

const authKey = "kitdb/managed-root/auth/v1"
const passwordIterations = 600000
const MaximumUsers = 256
const maximumAuthBytes = 1 << 20

var ErrAuthentication = errors.New("kitdb root: authentication failed")
var ErrPermission = errors.New("kitdb root: permission denied for database")

type DatabaseGrant struct {
	LocalRoles  []string `json:"local_roles,omitempty"` // Explicit mappings, never carried by a copied database.
	Database    string   `json:"database"`
	Role        string   `json:"role,omitempty"`        // reader, writer (DML), or developer (DML + DDL).
	Permissions []string `json:"permissions,omitempty"` // Alternative to Role; explicit, no implied permissions.
}

type DatabasePermissions struct {
	Connect, Read, Write, Schema bool
}

func (user UserInfo) Permissions(database string) DatabasePermissions {
	if user.Disabled {
		return DatabasePermissions{}
	}
	if user.Admin {
		return DatabasePermissions{true, true, true, true}
	}
	for _, grant := range user.Grants {
		if !strings.EqualFold(grant.Database, database) {
			continue
		}
		if grant.Role != "" && len(grant.Permissions) != 0 {
			return DatabasePermissions{}
		}
		switch grant.Role {
		case "reader":
			return DatabasePermissions{Connect: true, Read: true}
		case "writer":
			return DatabasePermissions{Connect: true, Read: true, Write: true}
		case "developer":
			return DatabasePermissions{true, true, true, true}
		case "":
			var result DatabasePermissions
			for _, permission := range grant.Permissions {
				switch permission {
				case "connect":
					result.Connect = true
				case "read":
					result.Read = true
				case "write":
					result.Write = true
				case "schema":
					result.Schema = true
				default:
					return DatabasePermissions{}
				}
			}
			return result
		default:
			return DatabasePermissions{}
		}
	}
	return DatabasePermissions{}
}

func cloneGrants(grants []DatabaseGrant) []DatabaseGrant {
	result := slices.Clone(grants)
	for i := range result {
		result[i].Permissions = slices.Clone(result[i].Permissions)
		result[i].LocalRoles = slices.Clone(result[i].LocalRoles)
	}
	return result
}

// UserInfo deliberately excludes password verifiers. These are connection
// accounts for this root, not website users. Management uses the root owner.
type UserInfo struct {
	Name             string          `json:"name"`
	Admin            bool            `json:"admin"`
	Disabled         bool            `json:"disabled"`
	Grants           []DatabaseGrant `json:"grants"`
	identityRoot     *Root
	identityInstance uint64
}

// SameAccount compares captured login incarnations. Unstamped values are
// host-trusted metadata, not credentials; managed login results are stamped.
func (user UserInfo) SameAccount(other UserInfo) bool {
	if !strings.EqualFold(user.Name, other.Name) {
		return false
	}
	if user.identityRoot == nil {
		return true
	}
	return user.identityRoot == other.identityRoot && user.identityInstance == other.identityInstance
}

func (root *Root) stampAccountLocked(info UserInfo) UserInfo {
	if root.accountInstances == nil {
		root.accountInstances = make(map[string]uint64)
	}
	key := strings.ToLower(info.Name)
	id := root.accountInstances[key]
	if id == 0 {
		root.nextAccountInstance++
		id = root.nextAccountInstance
		root.accountInstances[key] = id
	}
	info.identityRoot, info.identityInstance = root, id
	return info
}

type authUser struct {
	UserInfo
	Salt     string `json:"salt"`
	Verifier string `json:"verifier"`
}

type authCatalog struct {
	Version    int        `json:"version"`
	Iterations int        `json:"iterations"`
	Users      []authUser `json:"users"`
}

func (user UserInfo) Access(database string) (readOnly bool, allowed bool) {
	p := user.Permissions(database)
	return !p.Write && !p.Schema, p.Connect
}

func (root *Root) AuthEnabled() bool {
	root.mu.Lock()
	defer root.mu.Unlock()
	return root.catalog.AuthRequired
}

func (root *Root) Users() []UserInfo {
	root.mu.Lock()
	defer root.mu.Unlock()
	return root.usersLocked(false)
}

// SessionAccounts returns owner-stamped snapshots for live-session checks.
// The stamps are process-local and never part of persisted or JSON metadata.
func (root *Root) SessionAccounts() []UserInfo {
	root.mu.Lock()
	defer root.mu.Unlock()
	return root.usersLocked(true)
}

func (root *Root) usersLocked(stamp bool) []UserInfo {
	var result []UserInfo
	if root.auth != nil {
		for _, user := range root.auth.Users {
			info := user.UserInfo
			info.identityRoot, info.identityInstance = nil, 0
			if stamp {
				info = root.stampAccountLocked(info)
			}
			info.Grants = cloneGrants(info.Grants)
			result = append(result, info)
		}
	}
	return result
}

// EnableAuth atomically upgrades the authority marker and publishes the first
// administrator. Older readers reject catalog v2 rather than bypass its auth.
func (root *Root) EnableAuth(name, password string) error {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil {
		return ErrAuthentication
	}
	if root.catalog.AuthRequired {
		return fmt.Errorf("kitdb root: auth already enabled")
	}
	user, err := root.newAuthUser(UserInfo{Name: name, Admin: true}, password, nil)
	if err != nil {
		return err
	}
	auth := &authCatalog{Version: 2, Iterations: passwordIterations, Users: []authUser{user}}
	catalog := root.catalog
	catalog.Version = 2
	catalog.AuthRequired = true
	return root.publishAuth(catalog, auth)
}

// SetUser replaces grants/status. An empty password retains the existing verifier
// on update only. Disabled accounts are not deleted or silently re-enabled.
func (root *Root) SetUser(info UserInfo, password string) error {
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil || root.auth == nil {
		return ErrAuthentication
	}
	updated := *root.auth
	updated.Users = slices.Clone(root.auth.Users)
	index := -1
	for i := range updated.Users {
		if strings.EqualFold(updated.Users[i].Name, info.Name) {
			index = i
			break
		}
	}
	var existing *authUser
	if index >= 0 {
		existing = &updated.Users[index]
	}
	user, err := root.newAuthUser(info, password, existing)
	if err != nil {
		return err
	}
	if index < 0 {
		updated.Users = append(updated.Users, user)
	} else {
		updated.Users[index] = user
	}
	if err := validateAuth(&updated, root.catalog); err != nil {
		return err
	}
	return root.publishAuth(root.catalog, &updated)
}

func (root *Root) newAuthUser(info UserInfo, password string, existing *authUser) (authUser, error) {
	info.identityRoot, info.identityInstance = nil, 0
	info.Name = strings.ToLower(info.Name)
	info.Grants = cloneGrants(info.Grants)
	user := authUser{UserInfo: info}
	if !safeName(info.Name) {
		return user, fmt.Errorf("kitdb root: invalid account name")
	}
	if password == "" && existing != nil {
		user.Salt, user.Verifier = existing.Salt, existing.Verifier
		return user, nil
	}
	if len(password) < 12 || len(password) > 1024 {
		return user, fmt.Errorf("kitdb root: password must contain 12..1024 bytes")
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return user, err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return user, err
	}
	user.Salt, user.Verifier = hex.EncodeToString(salt), hex.EncodeToString(key)
	return user, nil
}

func (root *Root) Authenticate(ctx context.Context, name, password string) (UserInfo, error) {
	if ctx == nil || !safeName(name) || len(password) > 1024 {
		return UserInfo{}, ErrAuthentication
	}
	select {
	case root.authSlots <- struct{}{}:
		defer func() { <-root.authSlots }()
	case <-ctx.Done():
		return UserInfo{}, ctx.Err()
	}
	root.mu.Lock()
	if root.store == nil || root.auth == nil {
		root.mu.Unlock()
		return UserInfo{}, ErrAuthentication
	}
	var selected authUser
	found := false
	for _, user := range root.auth.Users {
		if strings.EqualFold(user.Name, name) {
			selected = user
			found = true
			break
		}
	}
	root.mu.Unlock()
	// Missing/disabled accounts still perform the bounded KDF, avoiding a cheap
	// username-existence oracle. Stored KDF parameters are validated at Open.
	salt := make([]byte, 16)
	verifier := make([]byte, 32)
	if found {
		salt, _ = hex.DecodeString(selected.Salt)
		verifier, _ = hex.DecodeString(selected.Verifier)
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, passwordIterations, 32)
	if err != nil {
		return UserInfo{}, ErrAuthentication
	}
	if err := ctx.Err(); err != nil {
		return UserInfo{}, err
	}
	if subtle.ConstantTimeCompare(key, verifier) != 1 || !found || selected.Disabled {
		return UserInfo{}, ErrAuthentication
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if root.store == nil || root.auth == nil {
		return UserInfo{}, ErrAuthentication
	}
	current := false
	for _, user := range root.auth.Users {
		if user.Name == selected.Name && user.Salt == selected.Salt && user.Verifier == selected.Verifier && !user.Disabled {
			selected = user
			current = true
			break
		}
	}
	if !current {
		return UserInfo{}, ErrAuthentication
	}
	selected.Grants = cloneGrants(selected.Grants)
	return root.stampAccountLocked(selected.UserInfo), nil
}

func validateAuth(auth *authCatalog, catalog Catalog) error {
	if (auth.Version != 2 && auth.Version != 3) || auth.Iterations != passwordIterations || len(auth.Users) < 1 || len(auth.Users) > MaximumUsers {
		return fmt.Errorf("kitdb root: invalid auth version, KDF or account limit")
	}
	names := map[string]bool{}
	admins := 0
	for _, user := range auth.Users {
		name := strings.ToLower(user.Name)
		salt, se := hex.DecodeString(user.Salt)
		verifier, ve := hex.DecodeString(user.Verifier)
		if !safeName(name) || user.Name != name || names[name] || se != nil || ve != nil || len(salt) != 16 || len(verifier) != 32 {
			return fmt.Errorf("kitdb root: invalid auth record")
		}
		names[name] = true
		if user.Admin && !user.Disabled {
			admins++
		}
		if user.Admin && len(user.Grants) != 0 {
			return fmt.Errorf("kitdb root: administrator does not need per-database grants")
		}
		grants := map[string]bool{}
		for _, grant := range user.Grants {
			if len(grant.LocalRoles) > 0 {
				if auth.Version != 3 {
					return fmt.Errorf("kitdb root: local mappings require auth version 3")
				}
				if grant.Role != "" || len(grant.Permissions) != 1 || grant.Permissions[0] != "connect" || len(grant.LocalRoles) > 32 {
					return fmt.Errorf("kitdb root: local roles require connect-only grants")
				}
				seen := map[string]bool{}
				for _, role := range grant.LocalRoles {
					b, e := hex.DecodeString(role)
					if e != nil || len(b) != 16 || role != strings.ToLower(role) || seen[role] {
						return fmt.Errorf("kitdb root: invalid local role mapping")
					}
					seen[role] = true
				}
			}
			key := strings.ToLower(grant.Database)
			registered := slices.ContainsFunc(catalog.Databases, func(db Database) bool { return strings.EqualFold(db.Name, grant.Database) })
			if !registered || grants[key] {
				return fmt.Errorf("kitdb root: grant must name one registered database")
			}
			if grant.Role != "" {
				if len(grant.Permissions) != 0 || (grant.Role != "reader" && grant.Role != "writer" && grant.Role != "developer") {
					return fmt.Errorf("kitdb root: invalid or mixed grant role")
				}
			} else {
				if len(grant.Permissions) == 0 || len(grant.Permissions) > 4 {
					return fmt.Errorf("kitdb root: invalid permission count")
				}
				seen := map[string]bool{}
				for _, permission := range grant.Permissions {
					if seen[permission] || (permission != "connect" && permission != "read" && permission != "write" && permission != "schema") {
						return fmt.Errorf("kitdb root: invalid or duplicate permission")
					}
					seen[permission] = true
				}
			}
			grants[key] = true
		}
	}
	if admins == 0 {
		return ErrLastAdministrator
	}
	return nil
}

func readAuth(store *kitdb.DB, catalog Catalog) (*authCatalog, error) {
	data, found, err := store.Get([]byte(authKey))
	if err != nil {
		return nil, err
	}
	if !catalog.AuthRequired {
		if found {
			return nil, fmt.Errorf("kitdb root: auth exists without authority marker")
		}
		return nil, nil
	}
	if !found || len(data) > maximumAuthBytes {
		return nil, fmt.Errorf("kitdb root: missing or oversized required auth")
	}
	var auth authCatalog
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&auth); err != nil {
		return nil, fmt.Errorf("kitdb root: invalid auth encoding")
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, fmt.Errorf("kitdb root: trailing auth data")
	}
	// Preserve legacy writer capabilities explicitly. New writer grants are DML-only.
	// A subsequent operator update publishes v2, which older auth readers reject.
	if auth.Version == 1 {
		for i := range auth.Users {
			for j := range auth.Users[i].Grants {
				grant := &auth.Users[i].Grants[j]
				if len(grant.Permissions) != 0 || (grant.Role != "reader" && grant.Role != "writer") {
					return nil, fmt.Errorf("kitdb root: invalid legacy grant")
				}
				if grant.Role == "writer" {
					grant.Role = "developer"
				}
			}
		}
		auth.Version = 2
	}
	if err := validateAuth(&auth, catalog); err != nil {
		return nil, err
	}
	return &auth, nil
}

func (root *Root) publishAuth(catalog Catalog, auth *authCatalog) error {
	for _, user := range auth.Users {
		for _, grant := range user.Grants {
			if len(grant.LocalRoles) > 0 {
				auth.Version = 3
			}
		}
	}
	if err := validateAuth(auth, catalog); err != nil {
		return err
	}
	authData, err := json.Marshal(auth)
	if err != nil {
		return err
	}
	if len(authData) > maximumAuthBytes {
		return fmt.Errorf("kitdb root: auth exceeds size bound")
	}
	catalogData, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	tx, err := root.store.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.Put([]byte(authKey), authData); err != nil {
		return err
	}
	if err := tx.Put([]byte(catalogKey), catalogData); err != nil {
		return err
	}
	if _, err := tx.Commit(); err != nil {
		closeErr := root.store.Close()
		root.store = nil
		return errors.Join(err, closeErr)
	}
	root.catalog, root.auth = catalog, auth
	for name := range root.accountInstances {
		if !slices.ContainsFunc(auth.Users, func(u authUser) bool { return u.Name == name }) {
			delete(root.accountInstances, name)
		}
	}
	return nil
}
