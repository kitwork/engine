package managed_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
)

func TestFolderDetachHardExitRecovery(t *testing.T) {
	if path := os.Getenv("KITDB_FOLDER_DETACH_CRASH"); path != "" {
		root, err := managed.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := root.Detach("shop"); err != nil {
			t.Fatal(err)
		}
		os.Exit(73) // Catalog and grant removals must replay without Close/checkpoint.
	}
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	makeSQLDatabase(t, filepath.Join(path, "shop", "data.kitdb"), "durable")
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if err := root.EnableAuth("admin", "hard-exit-admin-password"); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(managed.UserInfo{Name: "reader", Grants: []managed.DatabaseGrant{{Database: "shop", Role: "reader"}}}, "hard-exit-reader-password"); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestFolderDetachHardExitRecovery$")
	command.Env = append(os.Environ(), "KITDB_FOLDER_DETACH_CRASH="+path)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("child: %v / %s", err, output)
	}
	root, err = managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if len(root.Catalog().Databases) != 0 || len(root.Users()[1].Grants) != 0 {
		t.Fatal("catalog/grants diverged after hard exit")
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if len(root.Users()[1].Grants) != 0 {
		t.Fatal("reattach restored stale grants")
	}
}

func TestFolderMutationRequiresOfflineNode(t *testing.T) {
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	makeSQLDatabase(t, filepath.Join(path, "shop", "data.kitdb"), "active")
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	node, err := relational.OpenPostgresNode(relational.PostgresNodeOptions{Root: path, Password: "node-secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	pool, err := node.OpenNativeDatabase("shop")
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if owner, err := managed.Open(path); err == nil {
		owner.Close()
		t.Fatal("offline mutation acquired serving root")
	}
	var value string
	if err := tx.QueryRow("SELECT value FROM items WHERE id=1").Scan(&value); err != nil || value != "active" {
		t.Fatalf("rejected mutation disrupted session: %s / %v", value, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := pool.Close(); err != nil {
		t.Fatal(err)
	}
	if err := node.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if _, err := root.Detach("shop"); err != nil {
		t.Fatal(err)
	}
}

func TestFolderAttachDetachTransferAndGrants(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, path := range []string{a, b} {
		if _, err := managed.Init(path); err != nil {
			t.Fatal(err)
		}
	}
	makeSQLDatabase(t, filepath.Join(a, "shop", "data.kitdb"), "original")
	root, err := managed.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	db, err := root.Attach("shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if err := root.EnableAuth("admin", "source-admin-password"); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(managed.UserInfo{Name: "reader", Grants: []managed.DatabaseGrant{{Database: "shop", Role: "reader"}}}, "source-reader-password"); err != nil {
		t.Fatal(err)
	}
	busy, err := kitdb.Open(filepath.Join(a, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Detach("shop"); err == nil {
		t.Fatal("detached an independently open database")
	}
	if _, err := root.Attach("shop"); err == nil {
		t.Fatal("attached a busy folder")
	}
	if len(root.Catalog().Databases) != 1 || len(root.Users()[1].Grants) != 1 {
		t.Fatal("busy failure changed catalog or grants")
	}
	if err := busy.Close(); err != nil {
		t.Fatal(err)
	}
	copyClosedFolder(t, filepath.Join(a, "shop"), filepath.Join(a, "duplicate"))
	if _, err := root.Attach("duplicate"); err == nil {
		t.Fatal("duplicate database identity registered")
	}
	if _, err := root.Detach("SHOP"); err != nil {
		t.Fatal(err)
	}
	if len(root.Catalog().Databases) != 0 || len(root.Users()[1].Grants) != 0 {
		t.Fatal("detach did not revoke grants atomically")
	}
	if _, err := os.Stat(filepath.Join(a, "shop", "data.kitdb")); err != nil {
		t.Fatal("detach deleted data")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	root, err = managed.Open(a)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if len(root.Catalog().Databases) != 0 || len(root.Users()[1].Grants) != 0 {
		t.Fatal("detach or grant removal lost on reopen")
	}
	copyClosedFolder(t, filepath.Join(a, "shop"), filepath.Join(b, "shop"))
	target, err := managed.Open(b)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	moved, err := target.Attach("shop")
	if err != nil || moved.DatabaseID != db.DatabaseID || moved.Name != "shop" || moved.Directory != "shop" {
		t.Fatalf("transfer identity/name: %+v / %v", moved, err)
	}
	if err := target.EnableAuth("admin", "target-admin-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := target.Authenticate(context.Background(), "reader", "source-reader-password"); !errors.Is(err, managed.ErrAuthentication) {
		t.Fatalf("source account transferred: %v", err)
	}
	e, err := relational.Open(filepath.Join(b, "shop", "data.kitdb"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := e.Execute(context.Background(), "SELECT value FROM items WHERE id=1")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "original" {
		t.Fatalf("schema/data not portable: %+v / %v", result, err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	// A newly created database at the source's old name receives no old grants.
	if err := os.Rename(filepath.Join(a, "shop"), filepath.Join(a, "saved")); err != nil {
		t.Fatal(err)
	}
	makeSQLDatabase(t, filepath.Join(a, "shop", "data.kitdb"), "replacement")
	replacement, err := root.Attach("shop")
	if err != nil || replacement.DatabaseID == db.DatabaseID {
		t.Fatalf("replacement: %+v / %v", replacement, err)
	}
	reader, err := root.Authenticate(context.Background(), "reader", "source-reader-password")
	if err != nil {
		t.Fatal(err)
	}
	if _, allowed := reader.Access("shop"); allowed {
		t.Fatal("replacement inherited old grants")
	}
}

func makeSQLDatabase(t *testing.T, path, value string) {
	t.Helper()
	e, err := relational.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if _, err := e.Execute(context.Background(), "CREATE TABLE items(id BIGINT PRIMARY KEY, value TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Execute(context.Background(), "INSERT INTO items VALUES(1,$1)", value); err != nil {
		t.Fatal(err)
	}
}

func copyClosedFolder(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0700)
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, output.Close())
	})
	if err != nil {
		t.Fatal(err)
	}
}
