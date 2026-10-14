package managed

import (
	"context"
	"encoding/hex"
	"slices"
	"strings"
)

// RequireLocalRoleSupport fences older root-auth readers before local ACLs are
// published. A later database-write failure may leave this safe format upgrade.
func (root *Root) RequireLocalRoleSupport(ctx context.Context, actor string) error {
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
	if root.auth.Version == 3 {
		return nil
	}
	updated := *root.auth
	updated.Version = 3
	return root.publishAuth(root.catalog, &updated)
}

// ChangeLocalRole changes only an explicit root mapping. The selected database
// owner must validate role existence before calling this host-trusted method.
func (root *Root) ChangeLocalRole(ctx context.Context, actor, user, database, role string, revoke bool) error {
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
	b, e := hex.DecodeString(role)
	if e != nil || len(b) != 16 || role != strings.ToLower(role) {
		return ErrPermission
	}
	i := slices.IndexFunc(root.auth.Users, func(u authUser) bool { return strings.EqualFold(u.Name, user) })
	if i < 0 {
		return ErrAccountMissing
	}
	info := root.auth.Users[i].UserInfo
	info.Grants = cloneGrants(info.Grants)
	j := slices.IndexFunc(info.Grants, func(g DatabaseGrant) bool { return strings.EqualFold(g.Database, database) })
	if info.Admin || j < 0 {
		return ErrPermission
	}
	g := &info.Grants[j]
	if g.Role != "" || len(g.Permissions) != 1 || g.Permissions[0] != "connect" {
		return ErrPermission
	}
	if revoke {
		g.LocalRoles = slices.DeleteFunc(g.LocalRoles, func(r string) bool { return r == role })
	} else if !slices.Contains(g.LocalRoles, role) {
		g.LocalRoles = append(g.LocalRoles, role)
	}
	updated := *root.auth
	updated.Users = slices.Clone(root.auth.Users)
	updated.Users[i].UserInfo = info
	return root.publishAuth(root.catalog, &updated)
}
