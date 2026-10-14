package sql

import (
	"strings"
	"testing"
)

func TestParseLocalRoleStatement(t *testing.T) {
	for _, privilege := range []string{"INSERT", "UPDATE", "DELETE"} {
		for _, prefix := range []string{"GRANT ", "REVOKE "} {
			direction := " TO ROLE editor"
			if prefix == "REVOKE " {
				direction = " FROM ROLE editor"
			}
			st, handled, err := ParseLocalRoleStatement(prefix + privilege + " ON TABLE articles" + direction)
			if !handled || err != nil || st.Action != strings.ToLower(privilege) {
				t.Fatalf("%s: %#v %v", privilege, st, err)
			}
		}
	}
	for _, source := range []string{"CREATE ROLE editor", "DROP ROLE editor;", "GRANT SELECT ON TABLE articles TO ROLE editor", "REVOKE SELECT ON TABLE articles FROM ROLE editor", "GRANT ROLE editor ON DATABASE blog TO alice", "REVOKE ROLE editor ON DATABASE blog FROM alice"} {
		if _, handled, err := ParseLocalRoleStatement(source); !handled || err != nil {
			t.Fatalf("%s: %v %v", source, handled, err)
		}
	}
	for _, source := range []string{"CREATE ROLE editor LOGIN", "GRANT SELECT ON TABLE articles TO alice", "GRANT ROLE editor ON DATABASE blog TO alice; SELECT 1", "GRANT SELECT ON TABLE articles TO ROLE $1"} {
		if _, handled, err := ParseLocalRoleStatement(source); !handled || err == nil {
			t.Fatalf("accepted %s", source)
		}
	}
	for _, source := range []string{"CREATE USER alice WITH PASSWORD 'role'", "GRANT CONNECT ON DATABASE blog TO alice", "SELECT 'role'"} {
		if _, handled, _ := ParseLocalRoleStatement(source); handled {
			t.Fatalf("claimed %s", source)
		}
	}
}
