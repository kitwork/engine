package managed

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestRootLiveAccountCommands(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	createDatabase(t, filepath.Join(path, "shop", "data.kitdb"))
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	const password = "live-account-password"
	if err := root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := root.CreateAccount(ctx, "stranger", "alice", password); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	if err := root.CreateAccount(ctx, "admin", "alice", password); err != nil {
		t.Fatal(err)
	}
	if err := root.CreateAccount(ctx, "admin", "alice", "different-password"); !errors.Is(err, ErrAccountExists) {
		t.Fatal("duplicate account overwritten")
	}
	user, err := root.Authenticate(ctx, "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed := user.Access("shop"); allowed {
		t.Fatal("new user received implicit grants")
	}
	if err := root.ChangePermissions(ctx, "admin", "alice", "shop", []string{"connect", "read", "write"}, false); err != nil {
		t.Fatal(err)
	}
	if err := root.ChangePermissions(ctx, "alice", "alice", "shop", []string{"schema"}, false); !errors.Is(err, ErrPermission) {
		t.Fatal("user escalated privileges")
	}
	if err := root.ChangePermissions(ctx, "admin", "alice", "shop", []string{"write"}, true); err != nil {
		t.Fatal(err)
	}
	user, err = root.Authenticate(ctx, "alice", password)
	if err != nil || user.Permissions("shop").Write || !user.Permissions("shop").Read {
		t.Fatalf("revoke: %+v/%v", user, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := root.CreateAccount(canceled, "admin", "canceled", password); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := root.ChangePermissions(canceled, "admin", "alice", "shop", []string{"schema"}, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	user, err = root.Authenticate(ctx, "alice", password)
	if err != nil || !user.Permissions("shop").Read || user.Permissions("shop").Write {
		t.Fatalf("restart: %+v/%v", user, err)
	}
	if err := root.SetUser(UserInfo{Name: "otheradmin", Admin: true}, password); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(UserInfo{Name: "admin", Disabled: true}, ""); err != nil {
		t.Fatal(err)
	}
	if err := root.CreateAccount(ctx, "admin", "stale", password); !errors.Is(err, ErrPermission) {
		t.Fatal("stale admin authority")
	}
}

func TestRootAlterDropAccountsAndAdminProtection(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	createDatabase(t, filepath.Join(path, "shop", "data.kitdb"))
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	const oldPassword = "original-account-password"
	const newPassword = "rotated-account-password"
	ctx := context.Background()
	if err := root.EnableAuth("admin", oldPassword); err != nil {
		t.Fatal(err)
	}
	if err := root.CreateAccount(ctx, "admin", "alice", oldPassword); err != nil {
		t.Fatal(err)
	}
	if err := root.ChangePermissions(ctx, "admin", "alice", "shop", []string{"connect", "read"}, false); err != nil {
		t.Fatal(err)
	}
	if err := root.AlterAccount(ctx, "alice", "alice", nil, ptrBool(true)); !errors.Is(err, ErrPermission) {
		t.Fatal("non-admin changed account")
	}
	if err := root.AlterAccount(ctx, "admin", "alice", ptrString(newPassword), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Authenticate(ctx, "alice", oldPassword); !errors.Is(err, ErrAuthentication) {
		t.Fatal("old password retained")
	}
	user, err := root.Authenticate(ctx, "alice", newPassword)
	if err != nil || !user.Permissions("shop").Read {
		t.Fatal("rotation changed grants")
	}
	if err := root.AlterAccount(ctx, "admin", "alice", nil, ptrBool(true)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Authenticate(ctx, "alice", newPassword); !errors.Is(err, ErrAuthentication) {
		t.Fatal("disabled account authenticated")
	}
	if err := root.AlterAccount(ctx, "admin", "alice", nil, ptrBool(false)); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Authenticate(ctx, "alice", newPassword); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func() error{
		func() error { return root.AlterAccount(ctx, "admin", "admin", nil, ptrBool(true)) },
		func() error { return root.AlterAccount(ctx, "admin", "admin", ptrString(newPassword), ptrBool(true)) },
		func() error { return root.DropAccount(ctx, "admin", "admin", false) },
	} {
		if err := operation(); !errors.Is(err, ErrLastAdministrator) {
			t.Fatalf("last admin: %v", err)
		}
	}
	if _, err := root.Authenticate(ctx, "admin", oldPassword); err != nil {
		t.Fatal("failed NOLOGIN changed administrator password")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := root.DropAccount(canceled, "admin", "alice", false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := root.DropAccount(ctx, "alice", "alice", false); !errors.Is(err, ErrPermission) {
		t.Fatal(err)
	}
	if err := root.DropAccount(ctx, "admin", "alice", false); err != nil {
		t.Fatal(err)
	}
	if err := root.DropAccount(ctx, "admin", "alice", false); !errors.Is(err, ErrAccountMissing) {
		t.Fatal(err)
	}
	if err := root.DropAccount(ctx, "admin", "alice", true); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if len(root.Users()) != 1 || root.Users()[0].Name != "admin" {
		t.Fatal("drop/grant removal lost on restart")
	}
	if err := root.SetUser(UserInfo{Name: "standby", Admin: true, Disabled: true}, oldPassword); err != nil {
		t.Fatal(err)
	}
	if err := root.DropAccount(ctx, "admin", "admin", false); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("disabled admin counted as surviving authority")
	}
	if err := root.AlterAccount(ctx, "admin", "standby", nil, ptrBool(false)); err != nil {
		t.Fatal(err)
	}
	if err := root.DropAccount(ctx, "admin", "admin", false); err != nil {
		t.Fatal("could not drop admin with an enabled survivor", err)
	}
	if err := root.CreateAccount(ctx, "admin", "stale", oldPassword); !errors.Is(err, ErrPermission) {
		t.Fatal("dropped actor kept root authority")
	}
	if err := root.DropAccount(ctx, "standby", "standby", true); !errors.Is(err, ErrLastAdministrator) {
		t.Fatal("IF EXISTS bypassed last-admin protection")
	}
}

func ptrBool(value bool) *bool       { return &value }
func ptrString(value string) *string { return &value }
