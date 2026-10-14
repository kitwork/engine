package managed

import (
	"context"
	"errors"
	"testing"
)

func TestSessionAccountIncarnation(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	const password = "session-identity-password"
	if err = root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	if err = root.SetUser(UserInfo{Name: "backup", Admin: true}, password); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	old, err := root.Authenticate(ctx, "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	changed := "session-identity-rotated"
	if err = root.AlterAccount(ctx, "backup", "admin", &changed, nil); err != nil {
		t.Fatal(err)
	}
	current, err := root.Authenticate(ctx, "admin", changed)
	if err != nil {
		t.Fatal(err)
	}
	if !old.SameAccount(current) {
		t.Fatal("password rotation changed account incarnation")
	}
	if err = root.DropAccount(ctx, "backup", "admin", false); err != nil {
		t.Fatal(err)
	}
	if err = root.SetUser(UserInfo{Name: "admin", Admin: true}, password); err != nil {
		t.Fatal(err)
	}
	current, err = root.Authenticate(ctx, "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if old.SameAccount(current) || current.SameAccount(old) {
		t.Fatal("recreated account inherited incarnation")
	}
	if err = root.CreateAccount(WithSessionAccount(ctx, old), "admin", "forbidden", password); !errors.Is(err, ErrPermission) {
		t.Fatal("stale captured actor authorized", err)
	}
	if err = root.CreateAccount(WithSessionAccount(ctx, current), "admin", "allowed", password); err != nil {
		t.Fatal(err)
	}
	ordinary, err := root.Authenticate(ctx, "allowed", password)
	if err != nil {
		t.Fatal(err)
	}
	if err = root.SetUser(UserInfo{Name: "allowed", Admin: true}, ""); err != nil {
		t.Fatal(err)
	}
	if err = root.CreateAccount(WithSessionAccount(ctx, ordinary), "allowed", "promoted_old_login", password); !errors.Is(err, ErrPermission) {
		t.Fatal("captured ordinary login gained administration", err)
	}
	if err = root.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.Authenticate(ctx, "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	if current.SameAccount(after) {
		t.Fatal("account stamp survived owner replacement")
	}
}
