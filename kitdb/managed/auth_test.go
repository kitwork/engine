package managed

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestRootAuthPersistenceScopeAndSafety(t *testing.T) {
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
	if _, err := root.Register("shop", "shop"); err != nil {
		t.Fatal(err)
	}
	const password = "local-admin-password"
	if err := root.EnableAuth("Admin", password); err != nil {
		t.Fatal(err)
	}
	if root.Catalog().Version != 2 || !root.Catalog().AuthRequired {
		t.Fatal("missing downgrade guard")
	}
	if err := root.SetUser(UserInfo{Name: "reader", Grants: []DatabaseGrant{{Database: "shop", Role: "reader"}}}, "reader-local-password"); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(UserInfo{Name: "admin", Disabled: true, Admin: true}, ""); err == nil {
		t.Fatal("disabled last admin")
	}
	if err := root.SetUser(UserInfo{Name: "invalid", Grants: []DatabaseGrant{{Database: "other", Role: "writer"}}}, "invalid-local-password"); err == nil {
		t.Fatal("granted unregistered database")
	}
	ctx := context.Background()
	user, err := root.Authenticate(ctx, "READER", "reader-local-password")
	if err != nil {
		t.Fatal(err)
	}
	if ro, ok := user.Access("shop"); !ro || !ok {
		t.Fatal("reader permission lost")
	}
	if _, ok := user.Access("other"); ok {
		t.Fatal("grant escaped root")
	}
	user.Grants[0].Role = "writer"
	if root.Users()[1].Grants[0].Role != "reader" {
		t.Fatal("mutable grant leaked")
	}
	data, _, _ := root.store.Get([]byte(authKey))
	if bytes.Contains(data, []byte(password)) || bytes.Contains(data, []byte("reader-local-password")) {
		t.Fatal("plaintext password persisted")
	}
	if _, err := root.Authenticate(ctx, "reader", "wrong-password"); !errors.Is(err, ErrAuthentication) {
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
	if _, err := root.Authenticate(ctx, "admin", password); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(UserInfo{Name: "reader", Disabled: true, Grants: []DatabaseGrant{{Database: "shop", Role: "reader"}}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Authenticate(ctx, "reader", "reader-local-password"); !errors.Is(err, ErrAuthentication) {
		t.Fatal("disabled account admitted")
	}
	other := t.TempDir()
	if _, err := Init(other); err != nil {
		t.Fatal(err)
	}
	sibling, err := Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer sibling.Close()
	if _, err := sibling.Authenticate(ctx, "admin", password); !errors.Is(err, ErrAuthentication) {
		t.Fatal("credentials escaped to sibling root")
	}
}

func TestRootRequiredAuthCannotDisappear(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.EnableAuth("admin", "required-local-password"); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := kitdb.OpenWithOptions(filepath.Join(path, CatalogDirectory, "data.kitdb"), catalogOptions())
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Delete([]byte(authKey)); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if broken, err := Open(path); err == nil {
		broken.Close()
		t.Fatal("missing auth downgraded to config credentials")
	}
}

func TestRootAuthCancellationAndInvalidRole(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for range cap(root.authSlots) {
		root.authSlots <- struct{}{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := root.Authenticate(ctx, "admin", "password"); !errors.Is(err, context.Canceled) {
		t.Fatalf("auth admission did not cancel: %v", err)
	}
	for range cap(root.authSlots) {
		<-root.authSlots
	}
	user := UserInfo{Name: "bad", Grants: []DatabaseGrant{{Database: "shop", Role: "unknown"}}}
	if _, allowed := user.Access("shop"); allowed {
		t.Fatal("unknown role granted access")
	}
}
