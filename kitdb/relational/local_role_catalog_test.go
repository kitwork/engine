package relational

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/kitdb/managed"
)

func roleCatalogFixture(t *testing.T) (*PostgresNode, *sql.DB, *sql.DB) {
	t.Helper()
	node := sessionTestNode(t, 16)
	admin := setupLocalRoleAdmin(t, node)
	for _, source := range []string{"CREATE TABLE payments(id BIGINT PRIMARY KEY)", "CREATE ROLE article_reader", "CREATE ROLE private_reader", "GRANT SELECT ON TABLE articles TO ROLE article_reader", "GRANT SELECT ON TABLE payments TO ROLE private_reader", "GRANT ROLE article_reader ON DATABASE shop TO reader", "CREATE USER other WITH PASSWORD 'catalog-other-password'", "GRANT CONNECT ON DATABASE shop TO other", "GRANT ROLE private_reader ON DATABASE shop TO other"} {
		if _, err := admin.Exec(source); err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
	reader, err := node.OpenAuthenticatedDatabase(context.Background(), "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); admin.Close() })
	return node, admin, reader
}

func TestLocalRoleCatalogVisibilityAndEffectivePrivileges(t *testing.T) {
	node, admin, reader := roleCatalogFixture(t)
	checkCount := func(db *sql.DB, source string, want int, args ...any) {
		t.Helper()
		var n int
		if err := db.QueryRow(source, args...).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: %d want %d: %v", source, n, want, err)
		}
	}
	checkCount(admin, "SELECT COUNT(*) FROM kitdb_catalog.roles", 2)
	checkCount(reader, "SELECT COUNT(*) FROM kitdb_catalog.roles WHERE role_name='private_reader' OR role_name='article_reader'", 1)
	checkCount(reader, "SELECT COUNT(*) FROM kitdb_catalog.roles WHERE role_name=$1", 0, "private_reader")
	checkCount(reader, "SELECT COUNT(*) FROM kitdb_catalog.role_members WHERE username='other' OR username='reader'", 1)
	checkCount(admin, "SELECT COUNT(*) FROM kitdb_catalog.role_members", 2)
	checkCount(reader, "SELECT COUNT(*) FROM kitdb_catalog.table_grants", 1)
	checkCount(reader, "SELECT COUNT(*) FROM kitdb_catalog.effective_privileges WHERE table_name='payments' OR username='other'", 0)
	var granted bool
	var source string
	if err := reader.QueryRow("SELECT can_select,select_source FROM kitdb_catalog.effective_privileges WHERE policy_scope='session' AND table_name='articles'").Scan(&granted, &source); err != nil || !granted || source != "local_role" {
		t.Fatalf("local effective: %v %q %v", granted, source, err)
	}
	if err := admin.QueryRow("SELECT can_select,select_source FROM kitdb_catalog.effective_privileges WHERE policy_scope='configured' AND username='admin' AND table_name='payments'").Scan(&granted, &source); err != nil || !granted || source != "database" {
		t.Fatalf("database effective: %v %q %v", granted, source, err)
	}
	readOnly, err := admin.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var write, ddl bool
	if err := readOnly.QueryRow("SELECT can_insert,can_schema FROM kitdb_catalog.effective_privileges WHERE policy_scope='session' AND table_name='articles'").Scan(&write, &ddl); err != nil || write || ddl {
		readOnly.Rollback()
		t.Fatal("read-only transaction flags", write, ddl, err)
	}
	if err := readOnly.QueryRow("SELECT can_insert,can_schema FROM kitdb_catalog.effective_privileges WHERE policy_scope='configured' AND username='admin' AND table_name='articles'").Scan(&write, &ddl); err != nil || !write || !ddl {
		readOnly.Rollback()
		t.Fatal("configuration conflated with transaction", write, ddl, err)
	}
	readOnly.Rollback()
	var rootRead bool
	if err := reader.QueryRow("SELECT can_read FROM kitdb_catalog.grants").Scan(&rootRead); err != nil || rootRead {
		t.Fatal("root grant changed", err)
	}
	var name string
	if err := reader.QueryRow("SELECT role_name FROM kitdb_catalog.roles ORDER BY role_name DESC LIMIT 1").Scan(&name); err != nil || name != "article_reader" {
		t.Fatal("ordered view", name, err)
	}
	for _, sql := range []string{"DELETE FROM kitdb_catalog.roles", "DROP TABLE kitdb_catalog.table_grants", "INSERT INTO kitdb_catalog.role_members VALUES('x')", "SELECT password FROM kitdb_catalog.role_members", "SELECT directory FROM kitdb_catalog.roles", "SELECT SUM(can_select) FROM kitdb_catalog.table_grants", "SELECT role_name || 'x' FROM kitdb_catalog.roles", "SELECT * FROM kitdb_catalog.roles UNION ALL SELECT * FROM kitdb_catalog.roles", "SELECT r.role_name FROM kitdb_catalog.roles r JOIN payments p ON r.role_id=p.id", "WITH r AS (SELECT * FROM kitdb_catalog.roles) SELECT * FROM r"} {
		rows, err := reader.Query(sql)
		if rows != nil {
			rows.Close()
		}
		if err == nil {
			t.Fatalf("unsafe view query accepted: %s", sql)
		}
	}
	if isLocalRoleCatalogQuery("SELECT 'kitdb_catalog.roles'") || isLocalRoleCatalogQuery("SELECT * FROM kitdb_catalog.roles_archive") {
		t.Fatal("literal/prefix claimed protected namespace")
	}
	if !isLocalRoleCatalogQuery(`SELECT * FROM "KITDB_CATALOG"."ROLES"`) {
		t.Fatal("quoted view missed")
	}
	node.maximumResults = 1
	if _, err := admin.Query("SELECT * FROM kitdb_catalog.roles"); err == nil {
		t.Fatal("row bound ignored")
	}
	node.maximumResults = 10000
}

func TestLocalRoleCatalogRevalidatesAdministrator(t *testing.T) {
	node, admin, _ := roleCatalogFixture(t)
	if err := node.catalog.SetUser(managed.UserInfo{Name: "backupadmin", Admin: true}, "backup-admin-password"); err != nil {
		t.Fatal(err)
	}
	disabled := true
	if err := node.catalog.AlterAccount(context.Background(), "admin", "admin", nil, &disabled); err != nil {
		t.Fatal(err)
	}
	for _, view := range []string{"roles", "role_members", "table_grants"} {
		var n int
		if err := admin.QueryRow("SELECT COUNT(*) FROM kitdb_catalog." + view).Scan(&n); err != nil || n != 0 {
			t.Fatalf("stale admin %s: %d %v", view, n, err)
		}
	}
	var n int
	if err := admin.QueryRow("SELECT COUNT(*) FROM kitdb_catalog.effective_privileges WHERE username='other' OR username='reader'").Scan(&n); err != nil || n != 0 {
		t.Fatal("stale admin enumerated other users", n, err)
	}
}

func TestLocalRoleCatalogLifecycle(t *testing.T) {
	node, admin, reader := roleCatalogFixture(t)
	var originalID string
	if err := reader.QueryRow("SELECT role_id FROM kitdb_catalog.roles").Scan(&originalID); err != nil {
		t.Fatal(err)
	}
	tx, err := reader.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	renamed := make(chan error, 1)
	go func() { _, err := admin.Exec("ALTER TABLE articles RENAME TO posts"); renamed <- err }()
	select {
	case err := <-renamed:
		t.Fatal("DDL passed an open snapshot", err)
	case <-time.After(20 * time.Millisecond):
	}
	var table string
	if err := tx.QueryRow("SELECT table_name FROM kitdb_catalog.table_grants").Scan(&table); err != nil || table != "articles" {
		t.Fatal("pinned table view", table, err)
	}
	tx.Rollback()
	select {
	case err := <-renamed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("rename did not drain after rollback")
	}
	if err := reader.QueryRow("SELECT table_name FROM kitdb_catalog.table_grants").Scan(&table); err != nil || table != "posts" {
		t.Fatal("rename view", table, err)
	}
	if _, err := admin.Exec("REVOKE ROLE article_reader ON DATABASE shop FROM reader"); err != nil {
		t.Fatal(err)
	}
	var current, configured bool
	if err := reader.QueryRow("SELECT can_select FROM kitdb_catalog.effective_privileges WHERE table_name='posts' AND policy_scope='session'").Scan(&current); err != nil || !current {
		t.Fatal("captured membership", current, err)
	}
	if err := reader.QueryRow("SELECT can_select FROM kitdb_catalog.effective_privileges WHERE table_name='posts' AND policy_scope='configured'").Scan(&configured); err != nil || configured {
		t.Fatal("configured revoke", configured, err)
	}
	var count int
	if err := reader.QueryRow("SELECT COUNT(*) FROM kitdb_catalog.role_members").Scan(&count); err != nil || count != 0 {
		t.Fatal("stale configured membership", count, err)
	}
	fresh, err := node.OpenAuthenticatedDatabase(context.Background(), "reader", "session-test-password", "shop")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	var title string
	if err := fresh.QueryRow("SELECT title FROM posts").Scan(&title); err == nil {
		t.Fatal("new pool inherited revoked mapping")
	}
	for _, sql := range []string{"DROP ROLE article_reader", "CREATE ROLE article_reader", "GRANT SELECT ON TABLE posts TO ROLE article_reader", "GRANT ROLE article_reader ON DATABASE shop TO reader"} {
		if _, err := admin.Exec(sql); err != nil {
			t.Fatal(sql, err)
		}
	}
	var newID string
	if err := reader.QueryRow("SELECT role_id FROM kitdb_catalog.roles").Scan(&newID); err != nil || newID == originalID {
		t.Fatal("role identity reused", newID, err)
	}
	if err := reader.QueryRow("SELECT title FROM posts").Scan(&title); err == nil {
		t.Fatal("old session inherited recreated role")
	}
	if err := reader.QueryRow("SELECT can_select FROM kitdb_catalog.effective_privileges WHERE table_name='posts' AND policy_scope='session'").Scan(&current); err != nil || current {
		t.Fatal("old role effective", current, err)
	}
	if err := reader.QueryRow("SELECT can_select FROM kitdb_catalog.effective_privileges WHERE table_name='posts' AND policy_scope='configured'").Scan(&configured); err != nil || !configured {
		t.Fatal("new role effective", configured, err)
	}
}

func TestLocalRoleCatalogConcurrentGrantReads(t *testing.T) {
	_, admin, reader := roleCatalogFixture(t)
	reader.SetMaxOpenConns(4)
	var wg sync.WaitGroup
	errors := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				var n int
				if err := reader.QueryRow("SELECT COUNT(*) FROM kitdb_catalog.table_grants").Scan(&n); err != nil {
					errors <- err
					return
				}
				if n < 0 || n > 1 {
					t.Error("non-atomic grant view", n)
					return
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		for _, sql := range []string{"REVOKE SELECT ON TABLE articles FROM ROLE article_reader", "GRANT SELECT ON TABLE articles TO ROLE article_reader"} {
			if _, err := admin.Exec(sql); err != nil {
				t.Fatal(err)
			}
		}
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		t.Fatal(err)
	}
}
