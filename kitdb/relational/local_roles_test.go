package relational

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kitwork/engine/kitdb/managed"
)

func setupLocalRoleAdmin(t *testing.T, node *PostgresNode) *sql.DB {
	t.Helper()
	db, err := node.OpenAuthenticatedDatabase(context.Background(), "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"CREATE TABLE articles(id BIGINT PRIMARY KEY, title TEXT)", "INSERT INTO articles VALUES(1,'public')", "REVOKE READ ON DATABASE shop FROM reader"} {
		if _, err := db.Exec(source); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestLocalRolesPortableDatabaseRequiresMapping(t *testing.T) {
	ctx := context.Background()
	node := sessionTestNode(t, 16)
	admin := setupLocalRoleAdmin(t, node)
	defer admin.Close()
	for _, source := range []string{"CREATE ROLE article_reader", "GRANT SELECT ON TABLE articles TO ROLE article_reader", "GRANT INSERT ON TABLE articles TO ROLE article_reader", "GRANT UPDATE ON TABLE articles TO ROLE article_reader", "GRANT DELETE ON TABLE articles TO ROLE article_reader", "GRANT ROLE article_reader ON DATABASE shop TO reader"} {
		if _, err := admin.Exec(source); err != nil {
			t.Fatal(err)
		}
	}
	// Borrow the owner's DB, never open a second handle to the source file.
	node.mu.Lock()
	var owner *Engine
	for _, entry := range node.engines {
		if entry.database.name == "shop" {
			owner = entry.engine
		}
	}
	node.mu.Unlock()
	if owner == nil {
		t.Fatal("missing owner")
	}
	path := t.TempDir()
	if _, err := managed.Init(path); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "shop"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.database.CreateBackupAnchor(ctx, filepath.Join(path, "shop", "data.kitdb")); err != nil {
		t.Fatal(err)
	}
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := root.Attach("shop"); err != nil {
		t.Fatal(err)
	}
	if err := root.EnableAuth("admin", "session-test-password"); err != nil {
		t.Fatal(err)
	}
	if err := root.SetUser(managed.UserInfo{Name: "reader", Grants: []managed.DatabaseGrant{{Database: "shop", Permissions: []string{"connect"}}}}, "session-test-password"); err != nil {
		t.Fatal(err)
	}
	root.Close()
	dest, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	reader, err := dest.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	var title string
	if err := reader.QueryRow("SELECT title FROM articles").Scan(&title); err == nil {
		t.Fatal("same username inherited copied ACL")
	}
	reader.Close()
	operator, err := dest.OpenAuthenticatedDatabase(ctx, "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := operator.Exec("GRANT ROLE article_reader ON DATABASE shop TO reader"); err != nil {
		t.Fatal("copied local role missing", err)
	}
	operator.Close()
	reader, err = dest.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.QueryRow("SELECT title FROM articles").Scan(&title); err != nil || title != "public" {
		t.Fatalf("explicit mapping %q %v", title, err)
	}
	if _, err := reader.Exec("INSERT INTO articles VALUES(2,'copied')"); err != nil {
		t.Fatal("copied INSERT grant", err)
	}
	if _, err := reader.Exec("UPDATE articles SET title='edited' WHERE id=2"); err != nil {
		t.Fatal("copied UPDATE grant", err)
	}
	if _, err := reader.Exec("DELETE FROM articles WHERE id=2"); err != nil {
		t.Fatal("copied DELETE grant", err)
	}
	reader.Close()
	dest.Close()
	reopened, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	reader, err = reopened.OpenAuthenticatedDatabase(ctx, "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := reader.QueryRow("SELECT title FROM articles").Scan(&title); err != nil {
		t.Fatal("mapping did not persist", err)
	}
}

func TestLocalRoleHardExitRecovery(t *testing.T) {
	for _, stage := range []string{"create", "grant", "map", "drop", "writegrant", "writerevoke"} {
		t.Run(stage, func(t *testing.T) {
			node := sessionTestNode(t, 16)
			admin := setupLocalRoleAdmin(t, node)
			if stage != "create" {
				if _, err := admin.Exec("CREATE ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "map" || stage == "drop" {
				if _, err := admin.Exec("GRANT SELECT ON TABLE articles TO ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "drop" {
				if _, err := admin.Exec("GRANT ROLE article_reader ON DATABASE shop TO reader"); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "writerevoke" {
				if _, err := admin.Exec("GRANT INSERT ON TABLE articles TO ROLE article_reader"); err != nil {
					t.Fatal(err)
				}
			}
			admin.Close()
			node.Close()
			command := exec.Command(os.Args[0], "-test.run=^TestLocalRoleHardExitChild$")
			command.Env = append(os.Environ(), "KITDB_LOCAL_ROLE_CRASH_ROOT="+node.root, "KITDB_LOCAL_ROLE_CRASH_STAGE="+stage)
			output, err := command.CombinedOutput()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 73 {
				t.Fatalf("child exit %v: %s", err, output)
			}
			fresh, err := OpenPostgresNode(PostgresNodeOptions{Root: node.root})
			if err != nil {
				t.Fatal(err)
			}
			defer fresh.Close()
			db, err := fresh.OpenAuthenticatedDatabase(context.Background(), "admin", "session-test-password", "shop")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			switch stage {
			case "writegrant", "writerevoke":
				if _, err := db.Exec("GRANT ROLE article_reader ON DATABASE shop TO reader"); err != nil {
					t.Fatal(err)
				}
				reader, err := fresh.OpenAuthenticatedDatabase(context.Background(), "reader", "session-test-password", "shop")
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				_, err = reader.Exec("INSERT INTO articles VALUES(2,'recovered')")
				if stage == "writegrant" && err != nil {
					t.Fatal("write grant ACK lost", err)
				}
				if stage == "writerevoke" && err == nil {
					t.Fatal("write revoke ACK lost")
				}
			case "create":
				if _, err := db.Exec("CREATE ROLE article_reader"); err == nil {
					t.Fatal("role ACK lost")
				}
			case "grant", "map":
				if stage == "grant" {
					if _, err := db.Exec("GRANT ROLE article_reader ON DATABASE shop TO reader"); err != nil {
						t.Fatal(err)
					}
				}
				reader, err := fresh.OpenAuthenticatedDatabase(context.Background(), "reader", "session-test-password", "shop")
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				var title string
				if err := reader.QueryRow("SELECT title FROM articles").Scan(&title); err != nil {
					t.Fatal("grant/mapping ACK lost", err)
				}
			case "drop":
				if _, err := db.Exec("CREATE TABLE articles(id BIGINT PRIMARY KEY, title TEXT)"); err != nil {
					t.Fatal("drop ACK lost", err)
				}
				reader, err := fresh.OpenAuthenticatedDatabase(context.Background(), "reader", "session-test-password", "shop")
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				var n int
				if err := reader.QueryRow("SELECT COUNT(*) FROM articles").Scan(&n); err == nil {
					t.Fatal("atomic drop ACL cleanup lost")
				}
			}
		})
	}
}

func TestLocalRoleHardExitChild(t *testing.T) {
	path := os.Getenv("KITDB_LOCAL_ROLE_CRASH_ROOT")
	if path == "" {
		t.Skip("crash child")
	}
	node, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	db, err := node.OpenAuthenticatedDatabase(context.Background(), "admin", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	commands := map[string]string{"create": "CREATE ROLE article_reader", "grant": "GRANT SELECT ON TABLE articles TO ROLE article_reader", "map": "GRANT ROLE article_reader ON DATABASE shop TO reader", "drop": "DROP TABLE articles", "writegrant": "GRANT INSERT ON TABLE articles TO ROLE article_reader", "writerevoke": "REVOKE INSERT ON TABLE articles FROM ROLE article_reader"}
	if _, err := db.Exec(commands[os.Getenv("KITDB_LOCAL_ROLE_CRASH_STAGE")]); err != nil {
		t.Fatal(err)
	}
	os.Exit(73) // No DB/pool/root cleanup after the acknowledged WAL transaction.
}
