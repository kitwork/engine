package managed

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kitwork/engine/kitdb"
)

func TestRootRegistrationRecoveryAndOwnership(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	if _, err := Init(path); err == nil {
		t.Fatal("init overwrote an existing root")
	}
	wantID := createDatabase(t, filepath.Join(path, "physical", "data.kitdb"))
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	other, err := Open(path)
	if err == nil {
		_ = other.Close()
		t.Fatal("two owners acquired one root")
	}
	registered, err := root.Register("shop", "physical")
	if err != nil || registered.DatabaseID != wantID {
		t.Fatalf("register = %+v, %v", registered, err)
	}
	if repeated, err := root.Register("shop", "physical"); err != nil || repeated != registered {
		t.Fatalf("idempotent retry = %+v, %v", repeated, err)
	}
	if _, err := root.Register("alias", "physical"); err == nil {
		t.Fatal("registered the same physical database twice")
	}
	catalog := root.Catalog()
	catalog.Databases[0].Directory = "outside"
	if root.Catalog().Databases[0] != registered {
		t.Fatal("catalog leaked mutable state")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reflect.DeepEqual(reopened.Catalog().Databases, []Database{registered}) {
		t.Fatalf("recovered catalog = %+v", reopened.Catalog())
	}
	// Open canonicalises the root (Abs + EvalSymlinks). On a GitHub Windows runner t.TempDir() comes
	// back in 8.3 form — C:/Users/RUNNER~1/… — and the canonical path is the long form, so the
	// expectation must be canonicalised the same way or the comparison fails only in CI.
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.DatabasePath(registered); err != nil || got != filepath.Join(canonical, "physical", "data.kitdb") {
		t.Fatalf("database path = %q, %v", got, err)
	}
	if _, err := reopened.DatabasePath(Database{Name: "unregistered", Directory: "physical"}); err == nil {
		t.Fatal("accepted an unregistered entry")
	}
}

func TestRootRejectsUnsafeMissingAndRedirectedPaths(t *testing.T) {
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
	for _, name := range []string{"", "..", CatalogDirectory, LegacySystemDirectory, "../shop", "shop/child", `shop\child`, "shop:stream", " shop", "shop."} {
		if _, err := root.Register(name, "shop"); err == nil {
			t.Errorf("accepted logical name %q", name)
		}
		if _, err := root.Register("valid", name); err == nil {
			t.Errorf("accepted directory %q", name)
		}
	}
	if _, err := root.Register("missing", "missing"); err == nil {
		t.Fatal("registered a missing database")
	}
	if _, err := os.Stat(filepath.Join(path, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("register created missing files: %v", err)
	}
	if err := os.Symlink(filepath.Join(path, "shop"), filepath.Join(path, "linked")); err == nil {
		if _, err := root.Register("linked", "linked"); err == nil {
			t.Fatal("registered a symlink directory")
		}
	} else {
		t.Logf("directory symlink unavailable: %v", err)
	}
	if present, err := Present(filepath.Join(path, "shop")); err != nil || present {
		t.Fatalf("searched ancestors for catalog: %t, %v", present, err)
	}
}

func TestRootRejectsCorruptOrIncompleteCatalog(t *testing.T) {
	for _, payload := range []string{"", `{"version":2,"databases":[]}`, `{"version":1,"databases":[],"unknown":1}`, `{"version":1,"databases":[]} {}`} {
		t.Run(payload, func(t *testing.T) {
			path := t.TempDir()
			if _, err := Init(path); err != nil {
				t.Fatal(err)
			}
			store, err := kitdb.Open(filepath.Join(path, CatalogDirectory, "data.kitdb"))
			if err != nil {
				t.Fatal(err)
			}
			tx, err := store.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err := tx.Put([]byte(catalogKey), []byte(payload)); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			if root, err := Open(path); err == nil {
				_ = root.Close()
				t.Fatal("opened invalid catalog")
			}
		})
	}
}

func TestRootReadsLegacySystemCatalogAndRejectsAmbiguousAuthority(t *testing.T) {
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(
		filepath.Join(path, CatalogDirectory),
		filepath.Join(path, LegacySystemDirectory),
	); err != nil {
		t.Fatal(err)
	}
	if present, err := Present(path); err != nil || !present {
		t.Fatalf("legacy catalog present = %t, %v", present, err)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, CatalogDirectory), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Present(path); err == nil {
		t.Fatal("accepted both .catalog and legacy .system")
	}
	if root, err := Open(path); err == nil {
		_ = root.Close()
		t.Fatal("opened a root with ambiguous catalog authority")
	}
}

func TestRootRegistrationHardExitRecovery(t *testing.T) {
	if path := os.Getenv("KITDB_ROOT_CRASH_TEST"); path != "" {
		root, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := root.Register("shop", "shop"); err != nil {
			t.Fatal(err)
		}
		os.Exit(73) // No Close/checkpoint: replay the acknowledged catalog WAL.
	}
	path := t.TempDir()
	if _, err := Init(path); err != nil {
		t.Fatal(err)
	}
	id := createDatabase(t, filepath.Join(path, "shop", "data.kitdb"))
	command := exec.Command(os.Args[0], "-test.run=^TestRootRegistrationHardExitRecovery$")
	command.Env = append(os.Environ(), "KITDB_ROOT_CRASH_TEST="+path)
	output, err := command.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 73 {
		t.Fatalf("hard-exit child = %v: %s", err, output)
	}
	root, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if got := root.Catalog().Databases; len(got) != 1 || got[0].DatabaseID != id {
		t.Fatalf("acknowledged registration lost after hard exit: %+v", got)
	}
}

func createDatabase(t *testing.T, path string) string {
	t.Helper()
	database, err := kitdb.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := database.ID()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return id
}
