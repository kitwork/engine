package managed

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

var ErrAccountExists = errors.New("kitdb root: account already exists")
var ErrAccountMissing = errors.New("kitdb root: account does not exist")
var ErrLastAdministrator = errors.New("kitdb root: cannot remove or disable the last enabled administrator")
var ErrAccountChanged = errors.New("kitdb root: account changed during update; retry")

func (root *Root) protectAdministratorLocked(index int) error {
	user := root.auth.Users[index]
	if !user.Admin || user.Disabled {
		return nil
	}
	for i, other := range root.auth.Users {
		if i != index && other.Admin && !other.Disabled {
			return nil
		}
	}
	return ErrLastAdministrator
}

// AlterAccount hashes outside the catalog mutex, then merges into the latest
// record under ownership so concurrent grants are not overwritten.
func (root *Root) AlterAccount(ctx context.Context, actor, name string, password *string, disabled *bool) error {
	if ctx == nil {
		return ErrAuthentication
	}
	root.mu.Lock()
	err := root.administratorForContextLocked(ctx, actor)
	var expected authUser
	if err == nil {
		index := slices.IndexFunc(root.auth.Users, func(user authUser) bool { return strings.EqualFold(user.Name, name) })
		if index < 0 {
			err = ErrAccountMissing
		} else {
			expected = root.auth.Users[index]
		}
	}
	root.mu.Unlock()
	if err != nil {
		return err
	}
	if password == nil && disabled == nil {
		return fmt.Errorf("kitdb root: account update is empty")
	}
	var credentials authUser
	if password != nil {
		select {
		case root.authSlots <- struct{}{}:
			defer func() { <-root.authSlots }()
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		credentials, err = root.newAuthUser(UserInfo{Name: name}, *password, nil)
		if err != nil {
			return err
		}
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if err := root.administratorForContextLocked(ctx, actor); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	index := slices.IndexFunc(root.auth.Users, func(user authUser) bool { return strings.EqualFold(user.Name, name) })
	if index < 0 {
		return ErrAccountMissing
	}
	if root.auth.Users[index].Salt != expected.Salt || root.auth.Users[index].Verifier != expected.Verifier {
		return ErrAccountChanged
	}
	if disabled != nil && *disabled {
		if err := root.protectAdministratorLocked(index); err != nil {
			return err
		}
	}
	updated := *root.auth
	updated.Users = slices.Clone(root.auth.Users)
	if password != nil {
		updated.Users[index].Salt = credentials.Salt
		updated.Users[index].Verifier = credentials.Verifier
	}
	if disabled != nil {
		updated.Users[index].Disabled = *disabled
	}
	return root.publishAuth(root.catalog, &updated)
}

// DropAccount removes the complete account, including grants, in one catalog
// transaction. Existing data sessions retain their snapshot until reconnect.
func (root *Root) DropAccount(ctx context.Context, actor, name string, ifExists bool) error {
	if ctx == nil {
		return ErrAuthentication
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if err := root.administratorForContextLocked(ctx, actor); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	index := slices.IndexFunc(root.auth.Users, func(user authUser) bool { return strings.EqualFold(user.Name, name) })
	if index < 0 {
		if ifExists {
			return nil
		}
		return ErrAccountMissing
	}
	if err := root.protectAdministratorLocked(index); err != nil {
		return err
	}
	updated := *root.auth
	updated.Users = slices.Delete(slices.Clone(root.auth.Users), index, index+1)
	return root.publishAuth(root.catalog, &updated)
}

func (root *Root) administratorLocked(actor string) error {
	if root.store == nil || root.auth == nil {
		return ErrAuthentication
	}
	for _, user := range root.auth.Users {
		if strings.EqualFold(user.Name, actor) && user.Admin && !user.Disabled {
			return nil
		}
	}
	return ErrPermission
}

// CreateAccount is a root-owned autocommit operation. actor must be the
// authenticated session identity; caller code is host-trusted.
func (root *Root) CreateAccount(ctx context.Context, actor, name, password string) error {
	if ctx == nil {
		return ErrAuthentication
	}
	root.mu.Lock()
	err := root.administratorForContextLocked(ctx, actor)
	root.mu.Unlock()
	if err != nil {
		return err
	}
	select {
	case root.authSlots <- struct{}{}:
		defer func() { <-root.authSlots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	user, err := root.newAuthUser(UserInfo{Name: name}, password, nil)
	if err != nil {
		return err
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if err := root.administratorForContextLocked(ctx, actor); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if slices.ContainsFunc(root.auth.Users, func(existing authUser) bool { return strings.EqualFold(existing.Name, name) }) {
		return ErrAccountExists
	}
	updated := *root.auth
	updated.Users = append(slices.Clone(root.auth.Users), user)
	return root.publishAuth(root.catalog, &updated)
}

// ChangePermissions preserves the password/status and modifies only one
// database grant, atomically under catalog ownership. No session is revoked.
func (root *Root) ChangePermissions(ctx context.Context, actor, name, database string, permissions []string, revoke bool) error {
	if ctx == nil {
		return ErrAuthentication
	}
	root.mu.Lock()
	defer root.mu.Unlock()
	if err := root.administratorForContextLocked(ctx, actor); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(permissions) < 1 || len(permissions) > 4 {
		return ErrPermission
	}
	seen := map[string]bool{}
	for _, p := range permissions {
		if seen[p] || (p != "connect" && p != "read" && p != "write" && p != "schema") {
			return ErrPermission
		}
		seen[p] = true
	}
	index := slices.IndexFunc(root.auth.Users, func(user authUser) bool { return strings.EqualFold(user.Name, name) })
	if index < 0 {
		return ErrAccountMissing
	}
	if root.auth.Users[index].Admin {
		return fmt.Errorf("kitdb root: administrator rights cannot be changed with database grants")
	}
	dbIndex := slices.IndexFunc(root.catalog.Databases, func(db Database) bool { return strings.EqualFold(db.Name, database) })
	if dbIndex < 0 {
		return fmt.Errorf("kitdb root: database is not registered")
	}
	database = root.catalog.Databases[dbIndex].Name
	info := root.auth.Users[index].UserInfo
	info.Grants = cloneGrants(info.Grants)
	enabled := info
	enabled.Disabled = false
	current := enabled.Permissions(database)
	var localRoles []string
	for _, old := range info.Grants {
		if strings.EqualFold(old.Database, database) {
			localRoles = slices.Clone(old.LocalRoles)
		}
	}
	set := map[string]bool{"connect": current.Connect, "read": current.Read, "write": current.Write, "schema": current.Schema}
	for _, permission := range permissions {
		set[permission] = !revoke
	}
	info.Grants = slices.DeleteFunc(info.Grants, func(grant DatabaseGrant) bool { return strings.EqualFold(grant.Database, database) })
	grant := DatabaseGrant{Database: database}
	if set["connect"] {
		grant.LocalRoles = localRoles
	}
	for _, permission := range []string{"connect", "read", "write", "schema"} {
		if set[permission] {
			grant.Permissions = append(grant.Permissions, permission)
		}
	}
	if len(grant.Permissions) != 0 {
		info.Grants = append(info.Grants, grant)
	}
	updated := *root.auth
	updated.Users = slices.Clone(root.auth.Users)
	updated.Users[index].UserInfo = info
	if err := ctx.Err(); err != nil {
		return err
	}
	return root.publishAuth(root.catalog, &updated)
}
