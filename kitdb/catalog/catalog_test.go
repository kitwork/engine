package catalog

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/kitdb/pgwire"
	"github.com/lib/pq/scram"
)

var hostAdmin = Admin{User: "kitwork", Password: "host-admin-password"}

func openTest(t *testing.T, admin Admin) (*Catalog, string) {
	t.Helper()
	cluster := t.TempDir()
	catalog, err := Open(context.Background(), cluster, admin)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { catalog.Close() })
	return catalog, cluster
}

func mustRun(t *testing.T, catalog *Catalog, actor, database, text, want string) {
	t.Helper()
	tag, handled, err := catalog.Command(context.Background(), actor, database, text)
	if err != nil || !handled || tag != want {
		t.Fatalf("%s: tag %q, handled %v, error %v; want tag %q", text, tag, handled, err, want)
	}
}

func mustFail(t *testing.T, catalog *Catalog, actor, database, text, code string) {
	t.Helper()
	_, handled, err := catalog.Command(context.Background(), actor, database, text)
	var protocolError *pgwire.Error
	if !handled || !errors.As(err, &protocolError) || protocolError.Code != code {
		t.Fatalf("%s: handled %v, error %v; want SQLSTATE %s", text, handled, err, code)
	}
}

func canConnect(t *testing.T, catalog *Catalog, user, database string) bool {
	t.Helper()
	allowed, err := catalog.CanConnect(context.Background(), user, database)
	if err != nil {
		t.Fatal(err)
	}
	return allowed
}

func canSelect(t *testing.T, catalog *Catalog, user, database, schema, table string) bool {
	t.Helper()
	allowed, err := catalog.CanSelect(context.Background(), user, database, schema, table)
	if err != nil {
		t.Fatal(err)
	}
	return allowed
}

func stampOf(t *testing.T, catalog *Catalog, user string) uint64 {
	t.Helper()
	stamp, err := catalog.Stamp(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return stamp
}

func verifierOf(t *testing.T, catalog *Catalog, user string) (pgwire.SCRAMVerifier, bool) {
	t.Helper()
	verifier, found, err := catalog.Verifier(context.Background(), user)
	if err != nil {
		t.Fatal(err)
	}
	return verifier, found
}

// scramExchange runs lib/pq's SCRAM-SHA-256 client against a server side that knows only verifier, and returns the
// first failure: the server refusing the client's proof, or the client refusing the server's signature.
func scramExchange(verifier pgwire.SCRAMVerifier, user, password string) error {
	client := scram.NewClient(sha256.New, user, password)
	client.Step(nil)
	clientFirstBare := strings.TrimPrefix(string(client.Out()), "n,,")
	_, clientNonce, _ := strings.Cut(clientFirstBare, ",r=")
	serverFirst := fmt.Sprintf("r=%sserver-nonce,s=%s,i=%d", clientNonce, base64.StdEncoding.EncodeToString(verifier.Salt), verifier.Iterations)
	client.Step([]byte(serverFirst))
	if err := client.Err(); err != nil {
		return err
	}
	withoutProof, proofText, found := strings.Cut(string(client.Out()), ",p=")
	if !found {
		return errors.New("no proof in the client's final message")
	}
	proof, err := base64.StdEncoding.DecodeString(proofText)
	if err != nil || len(proof) != sha256.Size {
		return errors.New("malformed proof")
	}
	authMessage := clientFirstBare + "," + serverFirst + "," + withoutProof
	signature := hmac.New(sha256.New, verifier.StoredKey[:])
	signature.Write([]byte(authMessage))
	clientKey := signature.Sum(nil)
	for index := range clientKey {
		clientKey[index] ^= proof[index]
	}
	if sha256.Sum256(clientKey) != verifier.StoredKey {
		return errors.New("the server refused the client's proof")
	}
	serverSignature := hmac.New(sha256.New, verifier.ServerKey[:])
	serverSignature.Write([]byte(authMessage))
	client.Step([]byte("v=" + base64.StdEncoding.EncodeToString(serverSignature.Sum(nil))))
	return client.Err()
}

// TestCreateUserVerifierAcceptsSCRAM: CREATE USER keeps a verifier lib/pq's SCRAM client logs in with, and refuses
// a wrong password.
// CONTROL: storing verifier.ServerKey as stored_key in create fails with
// "a lib/pq client cannot log in as alice: the server refused the client's proof".
func TestCreateUserVerifierAcceptsSCRAM(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	mustRun(t, catalog, hostAdmin.User, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	verifier, found := verifierOf(t, catalog, "alice")
	if !found || verifier.Iterations != PasswordIterations || len(verifier.Salt) != saltBytes {
		t.Fatalf("verifier of alice: found %v, iterations %d, salt %d bytes", found, verifier.Iterations, len(verifier.Salt))
	}
	if err := scramExchange(verifier, "alice", "alice-password-2026"); err != nil {
		t.Fatalf("a lib/pq client cannot log in as alice: %v", err)
	}
	if err := scramExchange(verifier, "alice", "alice-password-2027"); err == nil {
		t.Fatal("a wrong password logged in")
	}
	if _, found := verifierOf(t, catalog, "nobody"); found {
		t.Fatal("an unknown user has a verifier")
	}
}

// TestPasswordNeverStoredInClear: neither the password nor SCRAM's SaltedPassword is in the catalog's files.
// CONTROL: inserting []byte(*parsed.password) as the salt in create fails with
// "the password is in clear in kitwork.db".
func TestPasswordNeverStoredInClear(t *testing.T) {
	t.Parallel()
	catalog, cluster := openTest(t, hostAdmin)
	const password = "Plain-Text-Password-Marker-2026"
	mustRun(t, catalog, hostAdmin.User, "", `CREATE USER alice PASSWORD '`+password+`'`, "CREATE ROLE")
	verifier, _ := verifierOf(t, catalog, "alice")
	salted, err := pbkdf2.Key(sha256.New, password, verifier.Salt, verifier.Iterations, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(cluster, Directory))
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, entry := range entries {
		data, err := os.ReadFile(filepath.Join(cluster, Directory, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		read += len(data)
		if bytes.Contains(data, []byte(password)) || bytes.Contains(bytes.ToLower(data), bytes.ToLower([]byte(password))) {
			t.Fatalf("the password is in clear in %s", entry.Name())
		}
		if bytes.Contains(data, salted) {
			t.Fatalf("the salted password (a SCRAM login) is in %s", entry.Name())
		}
		if bytes.Contains(data, []byte(hostAdmin.Password)) {
			t.Fatalf("the host admin's password is in %s", entry.Name())
		}
	}
	if read == 0 {
		t.Fatal("no catalog file was read")
	}
}

// TestHostAdminIsNeverStored: the host admin logs in, is an admin, may connect and select everywhere, keeps one
// stamp, and its name cannot be created, altered or dropped.
// CONTROL: removing the host admin's check at the top of create fails with
// "CREATE USER kitwork WITH PASSWORD 'another-password-2026': handled true, error <nil>; want SQLSTATE 42710".
func TestHostAdminIsNeverStored(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	verifier, found := verifierOf(t, catalog, hostAdmin.User)
	if !found {
		t.Fatal("the host admin has no verifier")
	}
	if err := scramExchange(verifier, hostAdmin.User, hostAdmin.Password); err != nil {
		t.Fatalf("the host admin cannot log in: %v", err)
	}
	if !catalog.IsAdmin(hostAdmin.User) || catalog.IsAdmin("someone") || catalog.IsAdmin("") {
		t.Fatal("IsAdmin is wrong")
	}
	if !canConnect(t, catalog, hostAdmin.User, "shopping") || !canSelect(t, catalog, hostAdmin.User, "shopping", "billing", "invoices") {
		t.Fatal("the host admin may not read everything")
	}
	if stampOf(t, catalog, hostAdmin.User) != hostAdminStamp {
		t.Fatal("the host admin's stamp is not constant")
	}
	mustFail(t, catalog, hostAdmin.User, "", `CREATE USER kitwork WITH PASSWORD 'another-password-2026'`, "42710")
	mustFail(t, catalog, hostAdmin.User, "", `CREATE ROLE kitwork`, "42710")
	mustFail(t, catalog, hostAdmin.User, "", `ALTER USER kitwork WITH PASSWORD 'another-password-2026'`, "0A000")
	mustFail(t, catalog, hostAdmin.User, "", `DROP USER kitwork`, "55006")
	var stored int
	if err := catalog.database.QueryRow(`SELECT count(*) FROM users`).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("users stored: %d, %v", stored, err)
	}
	if _, err := Open(context.Background(), t.TempDir(), Admin{User: "kitwork"}); err == nil {
		t.Fatal("a host admin without a password was accepted")
	}
}

// TestOnlyAdminsRunAccountStatements: a stored user, an unknown name and the empty name get 42501 for every statement,
// and nothing changes.
// CONTROL: making administrator return true for every user fails with
// "CREATE USER bob WITH PASSWORD 'bob-password-2026': handled true, error <nil>; want SQLSTATE 42501".
func TestOnlyAdminsRunAccountStatements(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	mustRun(t, catalog, hostAdmin.User, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, hostAdmin.User, "", `CREATE ROLE readers`, "CREATE ROLE")
	before := stampOf(t, catalog, "alice")
	for _, actor := range []string{"alice", "stranger", ""} {
		for _, text := range []string{
			`CREATE USER bob WITH PASSWORD 'bob-password-2026'`,
			`CREATE ROLE writers`,
			`ALTER USER alice WITH PASSWORD 'alice-password-2027'`,
			`DROP ROLE readers`,
			`GRANT CONNECT ON DATABASE shop TO alice`,
			`GRANT SELECT ON users TO alice`,
			`GRANT readers TO alice`,
			`REVOKE CONNECT ON DATABASE shop FROM alice`,
		} {
			mustFail(t, catalog, actor, "shop", text, "42501")
		}
	}
	if catalog.IsAdmin("alice") || canConnect(t, catalog, "alice", "shop") || stampOf(t, catalog, "alice") != before {
		t.Fatal("a refused statement changed something")
	}
	if _, found := verifierOf(t, catalog, "bob"); found {
		t.Fatal("a refused CREATE USER made bob")
	}
}

// TestStatementsAndTags: every account statement, its spellings, and Postgres's command tags.
// CONTROL: returning "CREATE USER" from tag for actionCreateUser fails with
// "CREATE USER alice WITH PASSWORD 'alice-password-2026': tag "CREATE USER", handled true, error <nil>; want tag "CREATE ROLE"".
func TestStatementsAndTags(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", "  /* a /* nested */ comment */ create user Bob password 'it''s-bob-2026' ; -- done", "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE ROLE readers`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `create role "Writers" with nologin;`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `ALTER USER alice WITH PASSWORD 'alice-password-2027'`, "ALTER ROLE")
	mustRun(t, catalog, admin, "", `ALTER ROLE alice NOLOGIN`, "ALTER ROLE")
	mustRun(t, catalog, admin, "", `ALTER USER alice LOGIN ENCRYPTED PASSWORD 'alice-password-2028'`, "ALTER ROLE")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE shop TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE "demo.kitwork.io/application" TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `REVOKE CONNECT ON DATABASE demo.kitwork.io/application FROM alice`, "REVOKE")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON TABLE billing.invoices TO alice`, "GRANT")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON users TO readers`, "GRANT")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON ALL TABLES IN SCHEMA reports TO alice`, "GRANT")
	mustRun(t, catalog, admin, "shop", `REVOKE SELECT ON ALL TABLES IN SCHEMA reports FROM alice`, "REVOKE")
	mustRun(t, catalog, admin, "shop", `REVOKE SELECT ON billing.invoices FROM alice CASCADE`, "REVOKE")
	mustRun(t, catalog, admin, "", `GRANT readers TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT readers ON DATABASE shop TO bob`, "GRANT")
	mustRun(t, catalog, admin, "", `REVOKE readers FROM alice`, "REVOKE")
	mustRun(t, catalog, admin, "", `DROP ROLE "Writers"`, "DROP ROLE")
	mustRun(t, catalog, admin, "", `DROP USER IF EXISTS nobody`, "DROP ROLE")
	mustRun(t, catalog, admin, "", `DROP ROLE IF EXISTS nobody`, "DROP ROLE")
	mustRun(t, catalog, admin, "", `DROP USER bob`, "DROP ROLE")
	mustRun(t, catalog, admin, "", `DROP ROLE readers`, "DROP ROLE")
	if _, found := verifierOf(t, catalog, "bob"); found {
		t.Fatal("bob survived DROP USER")
	}
	verifier, found := verifierOf(t, catalog, "alice")
	if !found {
		t.Fatal("alice cannot log in after ALTER USER … LOGIN")
	}
	if err := scramExchange(verifier, "alice", "alice-password-2028"); err != nil {
		t.Fatalf("alice's new password does not log in: %v", err)
	}
}

// TestRefusedStatements: duplicates, missing names, syntax and what the catalog does not do, each with its SQLSTATE;
// statements that are not account statements are not handled.
// CONTROL: making the duplicate check in create ignore an existing kind (kind != "" → false) fails with
// "CREATE USER alice WITH PASSWORD 'alice-password-2026': handled true, error catalog: write: constraint failed: UNIQUE constraint failed: users.name (1555); want SQLSTATE 42710".
func TestRefusedStatements(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE ROLE readers`, "CREATE ROLE")
	for _, refused := range []struct{ text, code string }{
		{`CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "42710"},
		{`CREATE ROLE alice`, "42710"},
		{`CREATE ROLE readers`, "42710"},
		{`CREATE ROLE ReAdErS`, "42710"},
		{`ALTER USER nobody PASSWORD 'nobody-password-2026'`, "42704"},
		{`DROP USER nobody`, "42704"},
		{`GRANT CONNECT ON DATABASE shop TO nobody`, "42704"},
		{`GRANT nobody TO alice`, "42704"},
		{`GRANT readers TO nobody`, "42704"},
		{`CREATE USER bob PASSWORD 'short'`, "22023"},
		{`CREATE USER bob PASSWORD 'md5` + strings.Repeat("ab", 16) + `'`, "0A000"},
		{`CREATE USER bob`, "42601"},
		{`CREATE USER bob WITH PASSWORD bob-password-2026`, "42601"},
		{`CREATE USER bob WITH PASSWORD 'unterminated`, "42601"},
		{`CREATE USER bob WITH PASSWORD 'a-password-2026' PASSWORD 'b-password-2026'`, "42601"},
		{`CREATE USER bob WITH PASSWORD 'a-password-2026' SUPERUSER`, "0A000"},
		{`CREATE USER bob WITH PASSWORD 'a-password-2026'; DROP USER alice`, "42601"},
		{`CREATE ROLE loud LOGIN`, "0A000"},
		{`CREATE ROLE loud PASSWORD 'loud-password-2026'`, "0A000"},
		{`ALTER USER alice`, "42601"},
		{`ALTER ROLE readers PASSWORD 'readers-password-2026'`, "0A000"},
		{`ALTER USER alice RENAME TO alicia`, "0A000"},
		{`GRANT INSERT ON users TO alice`, "0A000"},
		{`GRANT ALL ON DATABASE shop TO alice`, "0A000"},
		{`GRANT CONNECT, SELECT ON DATABASE shop TO alice`, "0A000"},
		{`GRANT CONNECT ON DATABASE shop TO PUBLIC`, "0A000"},
		{`GRANT CONNECT ON DATABASE shop TO alice WITH GRANT OPTION`, "0A000"},
		{`GRANT CONNECT ON SCHEMA public TO alice`, "0A000"},
		{`GRANT SELECT ON SEQUENCE counter TO alice`, "0A000"},
		{`GRANT SELECT ON shop.public.users TO alice`, "0A000"},
		{`GRANT SELECT ON users TO alice`, "3D000"},
		{`GRANT CONNECT ON DATABASE TO alice`, "42601"},
		{`GRANT CONNECT ON DATABASE shop alice`, "42601"},
		{`GRANT alice TO readers`, "0A000"},
		{`GRANT readers TO readers`, "0A000"},
		{`REVOKE GRANT OPTION FOR SELECT ON users FROM alice`, "0A000"},
		{`DROP USER alice, readers`, "0A000"},
		{`DROP USER IF alice`, "42601"},
	} {
		mustFail(t, catalog, admin, "", refused.text, refused.code)
	}
	for _, text := range []string{
		`SELECT 1`,
		`CREATE TABLE users (id INTEGER)`,
		`CREATE USER MAPPING FOR alice SERVER remote`,
		`DROP USER MAPPING IF EXISTS FOR alice SERVER remote`,
		`DROP TABLE users`,
		`-- only a comment`,
		``,
		`ALTER TABLE users ADD COLUMN x INTEGER`,
		`GRANTED`,
	} {
		tag, handled, err := catalog.Command(context.Background(), "stranger", "", text)
		if handled || err != nil || tag != "" {
			t.Fatalf("%q: tag %q, handled %v, error %v; want not handled", text, tag, handled, err)
		}
	}
	_, _, err := catalog.Command(context.Background(), admin, "", `CREATE USER bob WITH PASSWORD 'secret-in-a-message`)
	if err == nil || strings.Contains(err.Error(), "secret-in-a-message") {
		t.Fatalf("an error echoes the password: %v", err)
	}
}

// TestConnectGrants: CONNECT by database, revoked, and refused to a disabled or unknown user.
// CONTROL: removing "AND g.database_name = ?2" from CanConnect's query fails with "alice connects to blog".
func TestConnectGrants(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	if canConnect(t, catalog, "alice", "shop") {
		t.Fatal("a new user connects without a grant")
	}
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE shop TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE Demo.Kitwork.IO/application TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE "Archive" TO alice`, "GRANT")
	if !canConnect(t, catalog, "alice", "shop") || !canConnect(t, catalog, "alice", "demo.kitwork.io/application") ||
		!canConnect(t, catalog, "alice", "Archive") {
		t.Fatal("alice cannot connect where she was granted")
	}
	if canConnect(t, catalog, "alice", "blog") {
		t.Fatal("alice connects to blog")
	}
	if canConnect(t, catalog, "alice", "archive") || canConnect(t, catalog, "Alice", "shop") || canConnect(t, catalog, "nobody", "shop") {
		t.Fatal("a name matched another case, or an unknown user connects")
	}
	mustRun(t, catalog, admin, "", `ALTER USER alice NOLOGIN`, "ALTER ROLE")
	if canConnect(t, catalog, "alice", "shop") {
		t.Fatal("a disabled user connects")
	}
	if _, found := verifierOf(t, catalog, "alice"); found {
		t.Fatal("a disabled user has a verifier")
	}
	mustRun(t, catalog, admin, "", `ALTER USER alice LOGIN`, "ALTER ROLE")
	mustRun(t, catalog, admin, "", `REVOKE CONNECT ON DATABASE shop FROM alice`, "REVOKE")
	if canConnect(t, catalog, "alice", "shop") || !canConnect(t, catalog, "alice", "demo.kitwork.io/application") {
		t.Fatal("REVOKE CONNECT did not take shop alone")
	}
}

// TestSelectGrants: SELECT by table and by schema, in the session's database only, schema and table names without
// case as SQLite's, revoked; a revoke a schema-wide grant still covers is refused.
// CONTROL: replacing the empty table_name clause of CanSelect, "(g.table_name = ?4 OR g.table_name = …)", with
// "(g.table_name = ?4 OR 1)" fails with "alice selects public.orders".
func TestSelectGrants(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON users TO alice`, "GRANT")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON TABLE billing."Invoices" TO alice`, "GRANT")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON ALL TABLES IN SCHEMA reports TO alice`, "GRANT")
	for _, allowed := range [][2]string{{"public", "users"}, {"PUBLIC", "Users"}, {"billing", "invoices"}, {"Billing", "INVOICES"},
		{"reports", "daily"}, {"reports", "anything"}} {
		if !canSelect(t, catalog, "alice", "shop", allowed[0], allowed[1]) {
			t.Fatalf("alice cannot select %s.%s", allowed[0], allowed[1])
		}
	}
	if canSelect(t, catalog, "alice", "shop", "public", "orders") {
		t.Fatal("alice selects public.orders")
	}
	if canSelect(t, catalog, "alice", "shop", "billing", "users") || canSelect(t, catalog, "alice", "blog", "public", "users") ||
		canSelect(t, catalog, "alice", "shop", "", "users") || canSelect(t, catalog, "nobody", "shop", "public", "users") {
		t.Fatal("a grant reached another schema, another database or another user")
	}
	mustRun(t, catalog, admin, "shop", `REVOKE SELECT ON public.users FROM alice`, "REVOKE")
	if canSelect(t, catalog, "alice", "shop", "public", "users") {
		t.Fatal("alice still selects users after REVOKE")
	}
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON reports.daily TO alice`, "GRANT")
	mustFail(t, catalog, admin, "shop", `REVOKE SELECT ON reports.daily FROM alice`, "0A000")
	if !canSelect(t, catalog, "alice", "shop", "reports", "daily") {
		t.Fatal("a refused REVOKE changed alice's grants")
	}
	mustRun(t, catalog, admin, "shop", `REVOKE SELECT ON ALL TABLES IN SCHEMA reports FROM alice`, "REVOKE")
	if canSelect(t, catalog, "alice", "shop", "reports", "daily") || canSelect(t, catalog, "alice", "shop", "reports", "anything") {
		t.Fatal("REVOKE … ALL TABLES IN SCHEMA left a grant of that schema")
	}
	mustRun(t, catalog, admin, "shop", `ALTER USER alice NOLOGIN`, "ALTER ROLE")
	if canSelect(t, catalog, "alice", "shop", "billing", "invoices") {
		t.Fatal("a disabled user selects")
	}
}

// TestRoles: grants reach users through roles held for every database or for one; revoked and dropped roles take
// them back; a role cannot log in.
// CONTROL: replacing "g.grantee = u.name OR g.grantee IN (" with "g.grantee = u.name OR 0 AND g.grantee IN (" in
// CanSelect fails with "alice does not select users through readers".
func TestRoles(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE USER bob WITH PASSWORD 'bob-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE ROLE readers`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE shop TO readers`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE blog TO readers`, "GRANT")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON users TO readers`, "GRANT")
	mustRun(t, catalog, admin, "blog", `GRANT SELECT ON posts TO readers`, "GRANT")
	if _, found := verifierOf(t, catalog, "readers"); found {
		t.Fatal("a role has a verifier")
	}
	mustRun(t, catalog, admin, "", `GRANT readers TO alice`, "GRANT")
	if !canSelect(t, catalog, "alice", "shop", "public", "users") {
		t.Fatal("alice does not select users through readers")
	}
	if !canConnect(t, catalog, "alice", "shop") || !canConnect(t, catalog, "alice", "blog") || !canSelect(t, catalog, "alice", "blog", "public", "posts") {
		t.Fatal("alice does not hold readers for every database")
	}
	if canConnect(t, catalog, "bob", "shop") || canSelect(t, catalog, "bob", "shop", "public", "users") {
		t.Fatal("bob reads through a role he does not hold")
	}
	mustRun(t, catalog, admin, "", `GRANT readers ON DATABASE shop TO bob`, "GRANT")
	if !canConnect(t, catalog, "bob", "shop") || !canSelect(t, catalog, "bob", "shop", "public", "users") {
		t.Fatal("bob does not hold readers for shop")
	}
	if canConnect(t, catalog, "bob", "blog") || canSelect(t, catalog, "bob", "blog", "public", "posts") {
		t.Fatal("readers held for shop reached blog")
	}
	mustRun(t, catalog, admin, "", `GRANT readers TO bob`, "GRANT")
	mustFail(t, catalog, admin, "", `REVOKE readers ON DATABASE shop FROM bob`, "0A000")
	mustRun(t, catalog, admin, "", `REVOKE readers FROM bob`, "REVOKE")
	if canConnect(t, catalog, "bob", "shop") || canConnect(t, catalog, "bob", "blog") {
		t.Fatal("REVOKE role FROM user left a membership")
	}
	mustRun(t, catalog, admin, "", `REVOKE readers FROM alice`, "REVOKE")
	if canConnect(t, catalog, "alice", "shop") || canSelect(t, catalog, "alice", "shop", "public", "users") {
		t.Fatal("alice reads after REVOKE readers")
	}
	mustRun(t, catalog, admin, "", `GRANT readers TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `DROP ROLE readers`, "DROP ROLE")
	if canConnect(t, catalog, "alice", "shop") || canSelect(t, catalog, "alice", "shop", "public", "users") {
		t.Fatal("alice reads after DROP ROLE readers")
	}
	mustRun(t, catalog, admin, "", `CREATE ROLE readers`, "CREATE ROLE")
	if canConnect(t, catalog, "alice", "shop") {
		t.Fatal("a role made again brought back the old one's members or grants")
	}
}

// TestDroppedUserLosesEverything: DROP USER takes the user's verifier, grants, memberships and stamp; a user made again
// with the same name has none of them and a stamp never given before.
// CONTROL: removing "DELETE FROM grants WHERE grantee = OLD.name;" from the users_forget trigger fails with
// "alice made again connects with the old grant".
func TestDroppedUserLosesEverything(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE ROLE readers`, "CREATE ROLE")
	mustRun(t, catalog, admin, "shop", `GRANT SELECT ON users TO readers`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT readers TO alice`, "GRANT")
	mustRun(t, catalog, admin, "", `GRANT CONNECT ON DATABASE shop TO alice`, "GRANT")
	before := stampOf(t, catalog, "alice")
	mustRun(t, catalog, admin, "", `DROP USER alice`, "DROP ROLE")
	if _, found := verifierOf(t, catalog, "alice"); found || stampOf(t, catalog, "alice") != 0 || canConnect(t, catalog, "alice", "shop") {
		t.Fatal("a dropped user still logs in, has a stamp or connects")
	}
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2027'`, "CREATE ROLE")
	if canConnect(t, catalog, "alice", "shop") {
		t.Fatal("alice made again connects with the old grant")
	}
	if canSelect(t, catalog, "alice", "shop", "public", "users") {
		t.Fatal("alice made again holds the old role")
	}
	if after := stampOf(t, catalog, "alice"); after == 0 || after == before {
		t.Fatalf("alice made again has stamp %d, the old one was %d", after, before)
	}
	var left int
	if err := catalog.database.QueryRow(`SELECT (SELECT count(*) FROM grants WHERE grantee = 'alice') + (SELECT count(*) FROM memberships WHERE member = 'alice')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows of the old alice left: %d, %v", left, err)
	}
}

// TestStampChanges: a user's stamp changes with its password, login, grants, roles and its roles' grants, and only
// then.
// CONTROL: making touch return nil before writing fails with
// "the stamp did not change after ALTER USER alice WITH PASSWORD 'alice-password-2027'".
func TestStampChanges(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE USER bob WITH PASSWORD 'bob-password-2026'`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE ROLE readers`, "CREATE ROLE")
	mustRun(t, catalog, admin, "", `CREATE ROLE others`, "CREATE ROLE")
	last := stampOf(t, catalog, "alice")
	if last == 0 {
		t.Fatal("a new user has no stamp")
	}
	changes := func(database, text, tag string) {
		t.Helper()
		mustRun(t, catalog, admin, database, text, tag)
		stamp := stampOf(t, catalog, "alice")
		if stamp <= last {
			t.Fatalf("the stamp did not change after %s", text)
		}
		last = stamp
	}
	keeps := func(database, text, tag string) {
		t.Helper()
		mustRun(t, catalog, admin, database, text, tag)
		if stamp := stampOf(t, catalog, "alice"); stamp != last {
			t.Fatalf("the stamp changed after %s", text)
		}
	}
	changes("", `ALTER USER alice WITH PASSWORD 'alice-password-2027'`, "ALTER ROLE")
	changes("", `ALTER USER alice NOLOGIN`, "ALTER ROLE")
	changes("", `ALTER USER alice LOGIN`, "ALTER ROLE")
	changes("", `GRANT CONNECT ON DATABASE shop TO alice`, "GRANT")
	keeps("", `GRANT CONNECT ON DATABASE shop TO alice`, "GRANT")
	changes("shop", `GRANT SELECT ON users TO alice`, "GRANT")
	changes("shop", `REVOKE SELECT ON users FROM alice`, "REVOKE")
	keeps("shop", `REVOKE SELECT ON users FROM alice`, "REVOKE")
	changes("", `GRANT readers TO alice`, "GRANT")
	changes("shop", `GRANT SELECT ON orders TO readers`, "GRANT")
	changes("", `GRANT CONNECT ON DATABASE blog TO readers`, "GRANT")
	keeps("shop", `GRANT SELECT ON orders TO others`, "GRANT")
	keeps("", `GRANT CONNECT ON DATABASE shop TO bob`, "GRANT")
	keeps("", `ALTER USER bob NOLOGIN`, "ALTER ROLE")
	changes("", `REVOKE CONNECT ON DATABASE shop FROM alice`, "REVOKE")
	changes("", `DROP ROLE readers`, "DROP ROLE")
	changes("", `GRANT others ON DATABASE shop TO alice`, "GRANT")
	changes("", `REVOKE others ON DATABASE shop FROM alice`, "REVOKE")
}

// TestIdentifierRules: unquoted names fold to lower case (ASCII only), quoted names are kept, and empty, NUL, too long,
// reserved and Postgres's own names are refused.
// CONTROL: replacing "len(name) > maximumIdentifierBytes" with "len(name) > 1000" in checkName fails with
// "CREATE ROLE aaa…a (64 of them): handled true, error <nil>; want SQLSTATE 42622".
func TestIdentifierRules(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	admin := hostAdmin.User
	mustRun(t, catalog, admin, "", `CREATE USER Alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	if _, found := verifierOf(t, catalog, "alice"); !found {
		t.Fatal("an unquoted name was not folded to lower case")
	}
	if _, found := verifierOf(t, catalog, "Alice"); found {
		t.Fatal("a startup name was folded")
	}
	for _, text := range []string{
		`CREATE ROLE "Mixed"`,
		`CREATE ROLE mixed`,
		`CREATE ROLE "with"`,
		`CREATE ROLE "a""b"`,
		`CREATE ROLE ÂBc_1$`,
		`CREATE ROLE ` + strings.Repeat("a", 63),
		`CREATE ROLE "` + strings.Repeat("é", 31) + `"`,
	} {
		mustRun(t, catalog, admin, "", text, "CREATE ROLE")
	}
	rows, err := catalog.database.Query(`SELECT name FROM roles ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	rows.Close()
	want := []string{"Mixed", "a\"b", strings.Repeat("a", 63), "mixed", "with", "Âbc_1$", strings.Repeat("é", 31)}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Fatalf("roles stored as %q; want %q", names, want)
	}
	mustFail(t, catalog, admin, "", `CREATE ROLE MIXED`, "42710")
	mustFail(t, catalog, admin, "", `CREATE ROLE `+strings.Repeat("a", 64), "42622")
	mustFail(t, catalog, admin, "", `CREATE ROLE "`+strings.Repeat("é", 32)+`"`, "42622")
	mustFail(t, catalog, admin, "", `CREATE ROLE ""`, "42601")
	mustFail(t, catalog, admin, "", "CREATE ROLE \"a\x00b\"", "42602")
	mustFail(t, catalog, admin, "", `CREATE ROLE with`, "42601")
	mustFail(t, catalog, admin, "", `CREATE ROLE user`, "42601")
	mustFail(t, catalog, admin, "", `CREATE ROLE public`, "42939")
	mustFail(t, catalog, admin, "", `CREATE ROLE "none"`, "42939")
	mustFail(t, catalog, admin, "", `CREATE ROLE pg_monitor`, "42939")
	mustFail(t, catalog, admin, "", `CREATE ROLE "unterminated`, "42601")
	mustFail(t, catalog, admin, "", `CREATE ROLE 1abc`, "42601")
	mustFail(t, catalog, admin, "", `GRANT CONNECT ON DATABASE "`+strings.Repeat("d", 513)+`" TO alice`, "42622")
}

// TestLastStoredAdmin: without a host admin, the last enabled stored admin cannot be dropped or disabled, and no admin
// drops itself; with a host admin, a stored admin may go.
// CONTROL: making protectLastAdmin return nil at once fails with
// "ALTER USER root NOLOGIN: handled true, error <nil>; want SQLSTATE 55000".
func TestLastStoredAdmin(t *testing.T) {
	t.Parallel()
	catalog, cluster := openTest(t, Admin{})
	insertAdmin := func(name string) {
		t.Helper()
		verifier, err := derivePassword(name + "-password-2026")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := catalog.database.Exec(`INSERT INTO users (name, salt, iterations, stored_key, server_key, admin, disabled, stamp)
			VALUES (?, ?, ?, ?, ?, 1, 0, 2)`, name, verifier.Salt, verifier.Iterations, verifier.StoredKey[:], verifier.ServerKey[:]); err != nil {
			t.Fatal(err)
		}
	}
	insertAdmin("root")
	insertAdmin("second")
	if !catalog.IsAdmin("root") || catalog.IsAdmin("kitwork") {
		t.Fatal("IsAdmin is wrong without a host admin")
	}
	if !canConnect(t, catalog, "root", "shop") || !canSelect(t, catalog, "root", "shop", "billing", "invoices") {
		t.Fatal("a stored admin may not read everything")
	}
	mustRun(t, catalog, "root", "", `CREATE ROLE staff`, "CREATE ROLE")
	mustFail(t, catalog, "root", "", `DROP USER root`, "55006")
	mustRun(t, catalog, "root", "", `DROP USER second`, "DROP ROLE")
	mustFail(t, catalog, "root", "", `ALTER USER root NOLOGIN`, "55000")
	insertAdmin("third")
	mustRun(t, catalog, "root", "", `ALTER USER root NOLOGIN`, "ALTER ROLE")
	mustFail(t, catalog, "root", "", `CREATE ROLE others`, "42501")
	mustRun(t, catalog, "third", "", `DROP USER root`, "DROP ROLE")
	mustFail(t, catalog, "third", "", `ALTER USER third NOLOGIN`, "55000")
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	catalog, err := Open(context.Background(), cluster, hostAdmin)
	if err != nil {
		t.Fatal(err)
	}
	defer catalog.Close()
	mustRun(t, catalog, hostAdmin.User, "", `DROP USER third`, "DROP ROLE")
}

// TestPsqlVerifierKeptAsSent: ALTER USER … PASSWORD with Postgres's SCRAM-SHA-256 text (psql's \password) keeps that
// verifier, and the password behind it logs in.
// CONTROL: making passwordVerifier skip parseVerifier (ok && false) fails with "ALTER USER alice PASSWORD
// 'SCRAM-SHA-256$4096:…': tag "", handled true, error a malformed SCRAM-SHA-256 verifier (…); want tag "ALTER ROLE"".
func TestPsqlVerifierKeptAsSent(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	mustRun(t, catalog, hostAdmin.User, "", `CREATE USER alice WITH PASSWORD 'alice-password-2026'`, "CREATE ROLE")
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	salted, err := pbkdf2.Key(sha256.New, "psql-chosen-password", salt, 4096, sha256.Size)
	if err != nil {
		t.Fatal(err)
	}
	sent := pgwire.SCRAMVerifierFromSaltedPassword(salt, 4096, salted)
	text := fmt.Sprintf("SCRAM-SHA-256$4096:%s$%s:%s", base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(sent.StoredKey[:]), base64.StdEncoding.EncodeToString(sent.ServerKey[:]))
	mustRun(t, catalog, hostAdmin.User, "", `ALTER USER alice PASSWORD '`+text+`'`, "ALTER ROLE")
	kept, _ := verifierOf(t, catalog, "alice")
	if kept.Iterations != 4096 || !bytes.Equal(kept.Salt, salt) || kept.StoredKey != sent.StoredKey || kept.ServerKey != sent.ServerKey {
		t.Fatal("the verifier was not kept as sent")
	}
	if err := scramExchange(kept, "alice", "psql-chosen-password"); err != nil {
		t.Fatalf("the password behind the verifier does not log in: %v", err)
	}
	mustFail(t, catalog, hostAdmin.User, "", `ALTER USER alice PASSWORD 'SCRAM-SHA-256$1:`+base64.StdEncoding.EncodeToString(salt)+`$x:y'`, "22023")
}

// TestOpenRefusesOtherFiles: Open makes the catalog once and opens it again, and refuses a cluster that does not
// exist, a SQLite file that is not a catalog, and a catalog of a newer schema.
// CONTROL: removing "if objects != 0 {…}" in prepare fails with "a SQLite file that is not a catalog was opened".
func TestOpenRefusesOtherFiles(t *testing.T) {
	t.Parallel()
	catalog, cluster := openTest(t, hostAdmin)
	mustRun(t, catalog, hostAdmin.User, "", `CREATE ROLE readers`, "CREATE ROLE")
	if err := catalog.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := Open(context.Background(), cluster, hostAdmin)
	if err != nil {
		t.Fatal(err)
	}
	mustFail(t, again, hostAdmin.User, "", `CREATE ROLE readers`, "42710")
	again.Close()
	if _, err := Open(context.Background(), filepath.Join(cluster, "missing"), hostAdmin); err == nil {
		t.Fatal("a missing cluster was opened")
	}
	foreign := t.TempDir()
	if err := os.Mkdir(filepath.Join(foreign, Directory), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", dataSourceName(filepath.Join(foreign, Directory, File)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE notes (text TEXT)`); err != nil {
		t.Fatal(err)
	}
	database.Close()
	if opened, err := Open(context.Background(), foreign, hostAdmin); err == nil {
		opened.Close()
		t.Fatal("a SQLite file that is not a catalog was opened")
	}
	newer := t.TempDir()
	if err := os.Mkdir(filepath.Join(newer, Directory), 0o700); err != nil {
		t.Fatal(err)
	}
	database, err = sql.Open("sqlite", dataSourceName(filepath.Join(newer, Directory, File)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 2`); err != nil {
		t.Fatal(err)
	}
	database.Close()
	if opened, err := Open(context.Background(), newer, hostAdmin); err == nil {
		opened.Close()
		t.Fatal("a catalog of a newer schema was opened")
	}
}

// TestConcurrentCommands: account statements from many goroutines, with readers beside them, all succeed and all land.
// CONTROL: removing the writer mutex in write and "&_txlock=immediate" from dataSourceName fails with
// "CREATE ROLE role_12_0: catalog: write: database is locked (5) (SQLITE_BUSY)" (the role varies): deferred transactions upgrading to
// write collide.
func TestConcurrentCommands(t *testing.T) {
	t.Parallel()
	catalog, _ := openTest(t, hostAdmin)
	const writers, each = 16, 10
	var group sync.WaitGroup
	failures := make(chan error, writers*each*2)
	for writer := range writers {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range each {
				name := fmt.Sprintf("role_%d_%d", writer, index)
				for _, text := range []string{`CREATE ROLE ` + name, `GRANT CONNECT ON DATABASE shop TO ` + name} {
					if _, _, err := catalog.Command(context.Background(), hostAdmin.User, "", text); err != nil {
						failures <- fmt.Errorf("%s: %w", text, err)
					}
				}
				if _, err := catalog.CanConnect(context.Background(), name, "shop"); err != nil {
					failures <- err
				}
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var roles, grants int
	if err := catalog.database.QueryRow(`SELECT (SELECT count(*) FROM roles), (SELECT count(*) FROM grants)`).Scan(&roles, &grants); err != nil {
		t.Fatal(err)
	}
	if roles != writers*each || grants != writers*each {
		t.Fatalf("%d roles and %d grants; want %d each", roles, grants, writers*each)
	}
}
