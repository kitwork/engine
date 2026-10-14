package sql

import (
	"strings"
	"testing"
)

func TestAccountStatementParser(t *testing.T) {
	for _, source := range []string{
		"CREATE USER alice WITH PASSWORD 'a-long-password'",
		"/* comment */ CREATE USER alice WITH PASSWORD $1;",
		"GRANT CONNECT, READ ON DATABASE shop TO alice",
		"REVOKE WRITE ON DATABASE shop FROM alice",
		"ALTER USER alice WITH PASSWORD $1",
		"ALTER USER alice NOLOGIN",
		"ALTER USER alice WITH LOGIN PASSWORD 'another-password'",
		"DROP USER alice",
		"DROP USER IF EXISTS alice;",
	} {
		if _, handled, err := ParseAccountStatement(source); !handled || err != nil {
			t.Fatalf("%s: %v/%v", source, handled, err)
		}
	}
	for _, source := range []string{"SELECT * FROM users", "CREATE TABLE users(id BIGINT)", "ALTER TABLE users ADD COLUMN value TEXT", "DROP TABLE users", "SELECT 'grant'"} {
		if _, handled, err := ParseAccountStatement(source); handled || err != nil {
			t.Fatalf("claimed data statement %s", source)
		}
	}
	const secret = "not-for-errors-secret"
	for _, source := range []string{
		"CREATE USER x WITH PASSWORD " + secret,
		"CREATE USER x WITH PASSWORD '" + secret,
		"CREATE USER x WITH PASSWORD '" + secret + "' SUPERUSER",
		"CREATE USER x WITH PASSWORD '" + secret + "'; DROP TABLE items",
		"CREATE USER x WITH PASSWORD $0",
		"GRANT READ,READ ON DATABASE shop TO x",
		"GRANT ADMIN ON DATABASE shop TO x",
		"ALTER USER x",
		"ALTER USER x LOGIN NOLOGIN",
		"ALTER USER x PASSWORD '" + secret + "' PASSWORD '" + secret + "'",
		"ALTER USER x PASSWORD NULL",
		"DROP USER x CASCADE",
	} {
		if _, handled, err := ParseAccountStatement(source); !handled || err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("unsafe parser failure: handled=%v err=%v", handled, err)
		}
		if strings.Contains(AccountTraceSource(source), secret) {
			t.Fatal("password remained in trace")
		}
	}
	statement, _, err := ParseAccountStatement("CREATE USER x WITH PASSWORD 'escaped''password'")
	if err != nil || statement.Password != "escaped'password" {
		t.Fatalf("password literal: %+v/%v", statement, err)
	}
	statement, _, err = ParseAccountStatement("ALTER USER x WITH PASSWORD $1 NOLOGIN")
	if err != nil || !statement.PasswordSpecified || statement.PasswordParameter != 1 || statement.Disabled == nil || !*statement.Disabled {
		t.Fatal("combined ALTER options lost")
	}
}
