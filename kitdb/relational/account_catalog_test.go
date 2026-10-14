package relational

import (
	"testing"

	"github.com/kitwork/engine/kitdb/managed"
)

func TestAccountCatalogScopeAndBounds(t *testing.T) {
	path := createManagedNodeRoot(t, "shop")
	root, err := managed.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	const password = "catalog-view-password"
	if err := root.EnableAuth("admin", password); err != nil {
		t.Fatal(err)
	}
	for _, user := range []managed.UserInfo{
		{Name: "connectonly", Grants: []managed.DatabaseGrant{{Database: "shop", Permissions: []string{"connect"}}}},
		{Name: "disabled", Disabled: true, Grants: []managed.DatabaseGrant{{Database: "shop", Role: "writer"}}},
	} {
		if err := root.SetUser(user, password); err != nil {
			t.Fatal(err)
		}
	}
	users := root.Users()
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	node, err := OpenPostgresNode(PostgresNodeOptions{Root: path})
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	query := func(user managed.UserInfo, source string, args ...any) Result {
		t.Helper()
		result, handled, err := node.accountCatalogQuery(user, source, args)
		if err != nil || !handled {
			t.Fatalf("%s: handled=%v err=%v", source, handled, err)
		}
		return result
	}
	if got := query(users[0], "SELECT username FROM kitdb_catalog.users ORDER BY username DESC LIMIT 1 OFFSET 1"); len(got.Rows) != 1 || got.Rows[0][0] != "connectonly" {
		t.Fatalf("ordering/pagination: %+v", got)
	}
	if got := query(users[1], "SELECT COUNT(*) FROM kitdb_catalog.users WHERE username=$1 OR is_admin=true", "connectonly"); len(got.Rows) != 1 || got.Rows[0][0] != int64(1) {
		t.Fatalf("scope: %+v", got)
	}
	if got := query(users[0], "SELECT can_connect,can_read,can_write,can_schema FROM kitdb_catalog.grants WHERE username='disabled'"); len(got.Rows) != 1 || got.Rows[0][0] != false || got.Rows[0][1] != false || got.Rows[0][2] != false || got.Rows[0][3] != false {
		t.Fatalf("disabled effective grants: %+v", got)
	}
	if got := query(users[0], "SELECT COUNT(*) FROM kitdb_catalog.users LIMIT 0"); len(got.Rows) != 0 {
		t.Fatal("COUNT pagination ignored")
	}
	for _, sql := range []string{
		"SELECT verifier FROM kitdb_catalog.users",
		"SELECT salt FROM kitdb_catalog.users",
		"SELECT directory FROM kitdb_catalog.grants",
		"DELETE FROM kitdb_catalog.users",
		"DROP TABLE kitdb_catalog.grants",
		"CREATE TABLE kitdb_catalog.users(id BIGINT PRIMARY KEY)",
		"SELECT u.username FROM kitdb_catalog.users u JOIN items i ON u.username=i.value",
		"SELECT username FROM kitdb_catalog.users UNION ALL SELECT value FROM items",
	} {
		if _, handled, err := node.accountCatalogQuery(users[0], sql, nil); !handled || err == nil {
			t.Fatalf("unsafe query accepted: %s", sql)
		}
	}
	node.maximumResults = 1
	if _, _, err := node.accountCatalogQuery(users[0], "SELECT * FROM kitdb_catalog.users", nil); err == nil {
		t.Fatal("unbounded catalog result")
	}
	if err := checkDatabaseStatement(&managed.DatabasePermissions{Connect: true}, "SELECT * FROM kitdb_catalog.users"); err != nil {
		t.Fatal(err)
	}
	if isAccountCatalogQuery("SELECT 'kitdb_catalog.users'") {
		t.Fatal("string literal claimed system namespace")
	}
	if isAccountCatalogQuery("SELECT * FROM kitdb_catalog.users_archive") {
		t.Fatal("prefix claimed system view")
	}
	if !isAccountCatalogQuery(`SELECT * FROM "KITDB_CATALOG"."USERS"`) {
		t.Fatal("quoted system view missed")
	}
}
