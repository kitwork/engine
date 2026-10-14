package relational

import (
	"testing"

	"github.com/kitwork/engine/kitdb/managed"
)

func TestDatabaseStatementPermissions(t *testing.T) {
	cases := []struct {
		sql        string
		permission string
	}{
		{"SELECT id FROM items", "read"},
		{"EXPLAIN SELECT id FROM items", "read"},
		{"SELECT datname FROM pg_catalog.pg_database", "read"},
		{"INSERT INTO items VALUES(1)", "write"},
		{"UPDATE items SET id=2 WHERE id=1", "write"},
		{"DELETE FROM items WHERE id=1", "write"},
		{"UPDATE pg_class SET id=2 WHERE id=1", "write"},
		{"DELETE FROM pg_class WHERE id=1", "write"},
		{"CREATE TABLE items(id BIGINT PRIMARY KEY)", "schema"},
		{"DROP TABLE items", "schema"},
		{"DROP TABLE pg_class", "schema"},
		{"ANALYZE items", "schema"},
		{"REINDEX TABLE items", "schema"},
	}
	for _, item := range cases {
		for _, permission := range []string{"read", "write", "schema"} {
			p := managed.UserInfo{Grants: []managed.DatabaseGrant{{Database: "shop", Permissions: []string{"connect", permission}}}}.Permissions("shop")
			err := checkDatabaseStatement(&p, item.sql)
			if (err == nil) != (permission == item.permission) {
				t.Fatalf("%s under %s: %v", item.sql, permission, err)
			}
		}
	}
	writer := managed.DatabasePermissions{Connect: true, Write: true}
	for _, query := range []string{
		"UPDATE items SET id=2 WHERE id=1 RETURNING id",
		"DELETE FROM items WHERE id=1 RETURNING id",
		"INSERT INTO items VALUES(1) RETURNING id",
		"INSERT INTO items SELECT id FROM other",
	} {
		if err := checkDatabaseStatement(&writer, query); err == nil {
			t.Fatalf("write-only read via %s", query)
		}
	}
	schemaReader := managed.DatabasePermissions{Connect: true, Read: true, Schema: true}
	for _, query := range []string{"SELECT nextval('s')", "SELECT setval('s', 1)"} {
		if err := checkDatabaseStatement(&schemaReader, query); err == nil {
			t.Fatalf("sequence mutation via %s", query)
		}
	}
	if err := checkDatabaseStatement(&schemaReader, "SELECT currval('s')"); err != nil {
		t.Fatal(err)
	}
}
