package managed

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRootGranularPermissionPersistence(t *testing.T) {
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
	const password = "permission-local-password"
	if err := root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	info := UserInfo{Name: "custom", Grants: []DatabaseGrant{{Database: "shop", Permissions: []string{"connect", "schema"}}}}
	if err := root.SetUser(info, password); err != nil {
		t.Fatal(err)
	}
	info.Grants[0].Permissions[0] = "write"
	user, err := root.Authenticate(context.Background(), "custom", password)
	if err != nil {
		t.Fatal(err)
	}
	p := user.Permissions("SHOP")
	if !p.Connect || !p.Schema || p.Read || p.Write {
		t.Fatalf("incorrect permissions: %+v", p)
	}
	user.Grants[0].Permissions[0] = "read"
	for _, grant := range []DatabaseGrant{
		{Database: "shop", Role: "writer", Permissions: []string{"schema"}},
		{Database: "shop", Permissions: []string{"backup"}},
		{Database: "shop", Permissions: []string{"read", "read"}},
		{Database: "shop"},
	} {
		if err := root.SetUser(UserInfo{Name: "invalid", Grants: []DatabaseGrant{grant}}, password); err == nil {
			t.Fatalf("accepted invalid grant: %+v", grant)
		}
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	user, err = root.Authenticate(context.Background(), "custom", password)
	if err != nil || !user.Permissions("shop").Connect {
		t.Fatalf("grant lost on reopen: %v", err)
	}
	// Legacy v1 writers retain their former DDL authority as explicit developer.
	root.auth.Version = 1
	root.auth.Users[1].Grants = []DatabaseGrant{{Database: "shop", Role: "writer"}}
	// Write the old encoding through the catalog's own transaction for migration evidence.
	data, err := json.Marshal(root.auth)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := root.store.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := tx.Put([]byte(authKey), data); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Commit(); err != nil {
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
	user, err = root.Authenticate(context.Background(), "custom", password)
	if err != nil || !user.Permissions("shop").Schema || user.Grants[0].Role != "developer" {
		t.Fatalf("legacy grant changed: %+v / %v", user, err)
	}
	if err := root.SetUser(UserInfo{Name: "custom", Grants: []DatabaseGrant{{Database: "shop", Role: "writer"}}}, ""); err != nil {
		t.Fatal(err)
	}
	if root.auth.Version != 2 || root.Users()[1].Permissions("shop").Schema {
		t.Fatal("explicit downgrade to DML writer failed")
	}
}
