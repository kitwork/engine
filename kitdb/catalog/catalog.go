// Package catalog keeps one cluster's accounts, roles and grants for the read-only PostgreSQL wire port on SQLite,
// in <cluster>/.catalog/kitwork.db: a plain SQLite file through database/sql and modernc.org/sqlite, one writer at a
// time inside the process (a mutex around BEGIN IMMEDIATE transactions), readers on their own connections.
//
// # Accounts
//
// The host's admin (application.work's app.database user and password) is an admin of every cluster and is never
// stored: Open derives its SCRAM verifier once, in memory, from a fresh salt. A stored account cannot take its name;
// when the file holds one by that name from before (the admin renamed in application.work), the host admin wins.
//
// A user (CREATE USER, Postgres's CREATE ROLE … LOGIN) logs in; a role (CREATE ROLE) is a group that cannot log in.
// Users and roles share one namespace, as Postgres's roles do. A role is granted to users only (no role inside a
// role), for every database of the cluster (GRANT role TO user) or for one (GRANT role ON DATABASE db TO user, a
// KitDB form Postgres does not have). Grants are CONNECT on a database, and SELECT, INSERT, UPDATE and DELETE on a
// table (schema.table) or on every table of a schema, present and future (GRANT … ON ALL TABLES IN SCHEMA s), one or
// a list of them, or ALL [PRIVILEGES] for the four (the port writes since 05/11/2026; it read only before). A grant
// goes to a user or a role; a user may do what it was granted and what its roles were granted.
// Revoking a table that a schema-wide grant still covers, or a role for one database that the user still holds for
// every database, is refused rather than done while the access stays.
//
// Stored admins exist only from ImportKitDB, which brought the accounts of an old KitDB catalog over until the KROW
// reader left the engine (05/11/2026): a KitDB administrator stays an administrator (it may run account statements,
// connect to and select from everything). No statement makes or unmakes one. When the catalog has no
// host admin, the last enabled stored admin cannot be dropped or disabled (kitdb/managed's rule); with a host admin
// there is always an admin, so a stored one may go.
//
// Names follow PostgreSQL identifiers: unquoted folded to lower case (ASCII only), "quoted" kept, compared exactly;
// empty, NUL and over 63 bytes are refused. A database is named as clients name it on the port ("shopping",
// "demo.kitwork.io/application"), quoted or unquoted (letters, digits, _ $ . - /), compared exactly, at most 512
// bytes; the catalog does not check that it exists. Schema and table names compare without ASCII case, as SQLite's
// do.
//
// Passwords are never stored in clear, nor as the salted password: CREATE USER and ALTER USER derive SCRAM's
// SaltedPassword (PBKDF2-HMAC-SHA256, 600000 iterations, 16-byte random salt, as kitdb/managed) and keep only the
// salt, the iteration count, StoredKey and ServerKey (RFC 5802), as Postgres does: the file lets no one log in. A
// password must be 12 to 1024 bytes (kitdb/managed's rule), or a SCRAM-SHA-256 verifier in Postgres's text form
// (what psql's \password sends), kept as it is. Only an admin runs account statements; others get SQLSTATE 42501.
//
// The password is PBKDF2's input byte for byte, without SASLprep (as kitdb/managed and lib/pq). The host admin's
// password needs only 1 to 1024 bytes: it is application.work's, not set over the port.
//
// Each user has a stamp, a value of one sequence for the whole catalog, taken again in the same transaction as any
// change to the user's password, login, grants or roles, or to the grants of a role it belongs to. A value is never
// given twice, so a user dropped and made again does not take an old stamp; a missing user's stamp is 0, the host
// admin's is constant.
//
// # Statements
//
// One statement each, key words in any case, -- and /* */ comments, an optional final semicolon; a password is a
// string literal (two single quotes stand for one) and never appears in an error. Command tags are Postgres's.
//
//	CREATE USER name [WITH] PASSWORD '…' [LOGIN | NOLOGIN]              CREATE ROLE (ENCRYPTED PASSWORD too)
//	CREATE ROLE name [[WITH] NOLOGIN]                                   CREATE ROLE
//	ALTER USER | ALTER ROLE name [WITH] PASSWORD '…' | LOGIN | NOLOGIN …  ALTER ROLE (users only)
//	DROP USER | DROP ROLE [IF EXISTS] name                              DROP ROLE (either kind, with its grants)
//	GRANT | REVOKE CONNECT ON DATABASE database TO | FROM name          GRANT | REVOKE
//	GRANT | REVOKE privileges ON [TABLE] [schema.]table TO | FROM name      GRANT | REVOKE (the session's database)
//	GRANT | REVOKE privileges ON ALL TABLES IN SCHEMA schema TO | FROM name GRANT | REVOKE (the session's database)
//
// privileges is SELECT, INSERT, UPDATE or DELETE, a list of them separated by commas, or ALL [PRIVILEGES].
//
//	GRANT | REVOKE role [ON DATABASE database] TO | FROM user           GRANT | REVOKE
//
// REVOKE takes RESTRICT or CASCADE and ignores them; REVOKE role FROM user takes the role for every database it was
// held for. Other privileges, column privileges, role options, lists of grantees or tables, PUBLIC, WITH GRANT
// OPTION and cross-database names are 0A000.
//
// # Schema
//
// PRAGMA user_version is 2 (1 until 05/11/2026, whose grants were CONNECT and SELECT only: Open moves such a file to
// 2). Tables are STRICT.
//
//	users       (name PRIMARY KEY, salt, iterations, stored_key, server_key, admin, disabled, stamp)
//	roles       (name PRIMARY KEY)
//	memberships (role → roles, member → users, database_name; '' = every database)
//	grants      (grantee, privilege 'connect' | 'select' | 'insert' | 'update' | 'delete', database_name,
//	             schema_name, table_name; table '' = every table of the schema; schema and table COLLATE NOCASE)
//	sequence    (name PRIMARY KEY, value)            'stamp' → the last stamp given
//	imports     (source PRIMARY KEY, users, imported_at)   'kitdb' once ImportKitDB ran (until 05/11/2026)
//
// Triggers keep a name in users or roles, never both; refuse a grant to a name in neither; and delete a name's
// grants and memberships when it is deleted, so a name made again never finds old rights.
package catalog

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/kitwork/engine/kitdb/pgwire"
	_ "modernc.org/sqlite"
)

const (
	// Directory is the folder of a cluster that holds its catalog.
	Directory = ".catalog"
	// File is the catalog's SQLite file inside Directory.
	File = "kitwork.db"
	// PasswordIterations is the PBKDF2-HMAC-SHA256 iteration count of a password the catalog derives.
	PasswordIterations = 600000

	schemaVersion            = 2
	saltBytes                = 16
	minimumPasswordBytes     = 12
	maximumPasswordBytes     = 1024
	maximumIdentifierBytes   = 63
	maximumDatabaseNameBytes = 512
	minimumIterations        = 4096
	maximumIterations        = 10000000
	minimumSaltBytes         = 8
	maximumSaltBytes         = 64
	hostAdminStamp           = 1
)

// Admin is the host's admin of every cluster (application.work's app.database user/password), never stored.
type Admin struct{ User, Password string }

// Catalog is one cluster's accounts, roles and grants in <cluster>/.catalog/kitwork.db (plain SQLite through
// database/sql and modernc; one writer at a time inside the process).
type Catalog struct {
	path          string
	database      *sql.DB
	writer        sync.Mutex
	admin         string
	adminVerifier pgwire.SCRAMVerifier
}

// grantsTable and grantsGranteeTrigger are the grants and the trigger that keeps them to existing grantees.
const (
	grantsTable = `CREATE TABLE grants (
		grantee       TEXT NOT NULL,
		privilege     TEXT NOT NULL CHECK (privilege IN ('connect', 'select', 'insert', 'update', 'delete')),
		database_name TEXT NOT NULL,
		schema_name   TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
		table_name    TEXT NOT NULL DEFAULT '' COLLATE NOCASE,
		PRIMARY KEY (grantee, privilege, database_name, schema_name, table_name),
		CHECK ((privilege = 'connect' AND schema_name = '' AND table_name = '') OR (privilege <> 'connect' AND schema_name <> ''))
	) STRICT`
	grantsGranteeTrigger = `CREATE TRIGGER grants_grantee_exists BEFORE INSERT ON grants
	WHEN NOT EXISTS (SELECT 1 FROM users WHERE name = NEW.grantee) AND NOT EXISTS (SELECT 1 FROM roles WHERE name = NEW.grantee)
	BEGIN SELECT RAISE(ABORT, 'the grantee does not exist'); END`
)

var schemaStatements = []string{
	`CREATE TABLE users (
		name       TEXT PRIMARY KEY NOT NULL,
		salt       BLOB NOT NULL CHECK (length(salt) BETWEEN 8 AND 64),
		iterations INTEGER NOT NULL CHECK (iterations BETWEEN 4096 AND 10000000),
		stored_key BLOB NOT NULL CHECK (length(stored_key) = 32),
		server_key BLOB NOT NULL CHECK (length(server_key) = 32),
		admin      INTEGER NOT NULL DEFAULT 0 CHECK (admin IN (0, 1)),
		disabled   INTEGER NOT NULL DEFAULT 0 CHECK (disabled IN (0, 1)),
		stamp      INTEGER NOT NULL
	) STRICT`,
	`CREATE TABLE roles (
		name TEXT PRIMARY KEY NOT NULL
	) STRICT`,
	`CREATE TABLE memberships (
		role          TEXT NOT NULL REFERENCES roles (name) ON DELETE CASCADE,
		member        TEXT NOT NULL REFERENCES users (name) ON DELETE CASCADE,
		database_name TEXT NOT NULL,
		PRIMARY KEY (role, member, database_name)
	) STRICT`,
	`CREATE INDEX memberships_member ON memberships (member)`,
	grantsTable,
	`CREATE TABLE sequence (
		name  TEXT PRIMARY KEY NOT NULL,
		value INTEGER NOT NULL
	) STRICT`,
	`INSERT INTO sequence (name, value) VALUES ('stamp', 1)`,
	`CREATE TABLE imports (
		source      TEXT PRIMARY KEY NOT NULL,
		users       INTEGER NOT NULL,
		imported_at TEXT NOT NULL
	) STRICT`,
	`CREATE TRIGGER users_name_free BEFORE INSERT ON users
	WHEN EXISTS (SELECT 1 FROM roles WHERE name = NEW.name)
	BEGIN SELECT RAISE(ABORT, 'a role has this name'); END`,
	`CREATE TRIGGER roles_name_free BEFORE INSERT ON roles
	WHEN EXISTS (SELECT 1 FROM users WHERE name = NEW.name)
	BEGIN SELECT RAISE(ABORT, 'a user has this name'); END`,
	grantsGranteeTrigger,
	`CREATE TRIGGER users_forget AFTER DELETE ON users
	BEGIN
		DELETE FROM grants WHERE grantee = OLD.name;
		DELETE FROM memberships WHERE member = OLD.name;
	END`,
	`CREATE TRIGGER roles_forget AFTER DELETE ON roles
	BEGIN
		DELETE FROM grants WHERE grantee = OLD.name;
		DELETE FROM memberships WHERE role = OLD.name;
	END`,
}

// Open opens the catalog of cluster, an existing directory, making .catalog/kitwork.db when missing. admin may be
// empty (no host admin); otherwise both its user and password are required.
func Open(ctx context.Context, cluster string, admin Admin) (*Catalog, error) {
	if ctx == nil {
		return nil, errors.New("catalog: nil context")
	}
	if strings.TrimSpace(cluster) == "" {
		return nil, errors.New("catalog: the cluster directory is required")
	}
	if (admin.User == "") != (admin.Password == "") {
		return nil, errors.New("catalog: the host admin needs both a user and a password")
	}
	catalog := &Catalog{admin: admin.User}
	if admin.User != "" {
		if err := checkName(admin.User); err != nil {
			return nil, fmt.Errorf("catalog: host admin: %w", err)
		}
		if len(admin.Password) > maximumPasswordBytes || strings.IndexByte(admin.Password, 0) >= 0 {
			return nil, errors.New("catalog: the host admin's password must be at most 1024 bytes, without NUL")
		}
		verifier, err := derivePassword(admin.Password)
		if err != nil {
			return nil, fmt.Errorf("catalog: host admin: %w", err)
		}
		catalog.adminVerifier = verifier
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absolute, err := filepath.Abs(cluster)
	if err != nil {
		return nil, fmt.Errorf("catalog: resolve cluster: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("catalog: cluster: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("catalog: cluster %s is not a directory", absolute)
	}
	directory := filepath.Join(absolute, Directory)
	info, err = os.Lstat(directory)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("catalog: create %s: %w", directory, err)
		}
	case err != nil:
		return nil, fmt.Errorf("catalog: inspect %s: %w", directory, err)
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("catalog: %s must be a real directory", directory)
	}
	catalog.path = filepath.Join(directory, File)
	info, err = os.Lstat(catalog.path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Made empty and private first, so SQLite's -wal and -shm files take its permissions.
		file, err := os.OpenFile(catalog.path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil && !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("catalog: create %s: %w", catalog.path, err)
		}
		if err == nil {
			if err := file.Close(); err != nil {
				return nil, fmt.Errorf("catalog: create %s: %w", catalog.path, err)
			}
		}
	case err != nil:
		return nil, fmt.Errorf("catalog: inspect %s: %w", catalog.path, err)
	case !info.Mode().IsRegular():
		return nil, fmt.Errorf("catalog: %s must be a regular file", catalog.path)
	}
	catalog.database, err = sql.Open("sqlite", dataSourceName(catalog.path))
	if err != nil {
		return nil, fmt.Errorf("catalog: open %s: %w", catalog.path, err)
	}
	catalog.database.SetMaxOpenConns(8)
	if err := catalog.prepare(ctx); err != nil {
		return nil, errors.Join(err, catalog.database.Close())
	}
	return catalog, nil
}

func dataSourceName(path string) string {
	escaped := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(filepath.ToSlash(path))
	return "file:" + escaped + "?_pragma=busy_timeout(10000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(FULL)&_txlock=immediate"
}

// prepare makes the schema in a new file and checks the version of an existing one.
func (c *Catalog) prepare(ctx context.Context) error {
	c.writer.Lock()
	defer c.writer.Unlock()
	transaction, err := c.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: open %s: %w", c.path, err)
	}
	defer transaction.Rollback()
	var version int
	if err := transaction.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("catalog: read %s: %w", c.path, err)
	}
	switch version {
	case schemaVersion:
		return transaction.Commit()
	case 1:
		// version 1's grants took CONNECT and SELECT alone: the table is made again with the privileges that write,
		// its rows kept. Its own trigger goes with it, and the two that delete from it would stop the rename (SQLite
		// checks every trigger then): all three are made again after.
		moves := []string{
			`DROP TRIGGER users_forget`,
			`DROP TRIGGER roles_forget`,
			strings.Replace(grantsTable, "CREATE TABLE grants (", "CREATE TABLE grants_2 (", 1),
			`INSERT INTO grants_2 SELECT grantee, privilege, database_name, schema_name, table_name FROM grants`,
			`DROP TABLE grants`,
			`ALTER TABLE grants_2 RENAME TO grants`,
			grantsGranteeTrigger,
		}
		for _, statement := range schemaStatements {
			if strings.HasPrefix(statement, "CREATE TRIGGER users_forget ") || strings.HasPrefix(statement, "CREATE TRIGGER roles_forget ") {
				moves = append(moves, statement)
			}
		}
		if len(moves) != 9 {
			return fmt.Errorf("catalog: the triggers of the grants are not where the move from version 1 looks")
		}
		for _, statement := range append(moves, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)) {
			if _, err := transaction.ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("catalog: move %s to schema version %d: %w", c.path, schemaVersion, err)
			}
		}
		return transaction.Commit()
	case 0:
	default:
		return fmt.Errorf("catalog: %s has schema version %d; this engine knows %d", c.path, version, schemaVersion)
	}
	var objects int
	if err := transaction.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		return fmt.Errorf("catalog: read %s: %w", c.path, err)
	}
	if objects != 0 {
		return fmt.Errorf("catalog: %s is not a Kitwork catalog", c.path)
	}
	for _, statement := range schemaStatements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("catalog: make %s: %w", c.path, err)
		}
	}
	if _, err := transaction.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("catalog: make %s: %w", c.path, err)
	}
	return transaction.Commit()
}

// Close closes the catalog's file.
func (c *Catalog) Close() error {
	c.writer.Lock()
	defer c.writer.Unlock()
	return c.database.Close()
}

// derivePassword makes a new SCRAM-SHA-256 verifier of password: a random 16-byte salt and PBKDF2-HMAC-SHA256 with
// PasswordIterations; the salted password itself is cleared once the two keys are derived.
func derivePassword(password string) (pgwire.SCRAMVerifier, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return pgwire.SCRAMVerifier{}, err
	}
	salted, err := pbkdf2.Key(sha256.New, password, salt, PasswordIterations, sha256.Size)
	if err != nil {
		return pgwire.SCRAMVerifier{}, err
	}
	verifier := pgwire.SCRAMVerifierFromSaltedPassword(salt, PasswordIterations, salted)
	clear(salted)
	return verifier, nil
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryRowContext(ctx context.Context, query string, arguments ...any) *sql.Row
}

// Verifier of a user for SCRAM; the admin's is derived from its password once at Open (fresh salt, 600000
// iterations). found is false for an unknown or disabled user and for a role.
func (c *Catalog) Verifier(ctx context.Context, user string) (pgwire.SCRAMVerifier, bool, error) {
	if ctx == nil {
		return pgwire.SCRAMVerifier{}, false, errors.New("catalog: nil context")
	}
	if c.admin != "" && user == c.admin {
		verifier := c.adminVerifier
		verifier.Salt = append([]byte(nil), verifier.Salt...)
		return verifier, true, nil
	}
	var salt, storedKey, serverKey []byte
	var iterations int
	err := c.database.QueryRowContext(ctx, `SELECT salt, iterations, stored_key, server_key FROM users WHERE name = ? AND disabled = 0`, user).
		Scan(&salt, &iterations, &storedKey, &serverKey)
	if errors.Is(err, sql.ErrNoRows) {
		return pgwire.SCRAMVerifier{}, false, nil
	}
	if err != nil {
		return pgwire.SCRAMVerifier{}, false, fmt.Errorf("catalog: read user: %w", err)
	}
	if len(storedKey) != sha256.Size || len(serverKey) != sha256.Size {
		return pgwire.SCRAMVerifier{}, false, errors.New("catalog: a stored verifier has the wrong length")
	}
	verifier := pgwire.SCRAMVerifier{Salt: salt, Iterations: iterations}
	copy(verifier.StoredKey[:], storedKey)
	copy(verifier.ServerKey[:], serverKey)
	return verifier, true, nil
}

// IsAdmin reports whether user is the host admin or an enabled stored admin. A failure to read the file is false.
func (c *Catalog) IsAdmin(user string) bool {
	admin, err := c.administrator(context.Background(), c.database, user)
	return err == nil && admin
}

func (c *Catalog) administrator(ctx context.Context, from querier, user string) (bool, error) {
	if user == "" {
		return false, nil
	}
	if c.admin != "" && user == c.admin {
		return true, nil
	}
	var admin bool
	err := from.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE name = ? AND admin = 1 AND disabled = 0)`, user).Scan(&admin)
	if err != nil {
		return false, fmt.Errorf("catalog: read user: %w", err)
	}
	return admin, nil
}

// CanConnect reports whether user may connect to database: an admin, or CONNECT granted to the user or to one of its
// roles (a role held for every database or for this one). A disabled or unknown user may not.
func (c *Catalog) CanConnect(ctx context.Context, user, database string) (bool, error) {
	if ctx == nil {
		return false, errors.New("catalog: nil context")
	}
	if c.admin != "" && user == c.admin {
		return true, nil
	}
	if user == "" || database == "" {
		return false, nil
	}
	var allowed bool
	err := c.database.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM users AS u WHERE u.name = ?1 AND u.disabled = 0 AND (
			u.admin = 1 OR EXISTS (
				SELECT 1 FROM grants AS g
				WHERE g.privilege = 'connect' AND g.database_name = ?2 AND (
					g.grantee = u.name OR g.grantee IN (
						SELECT m.role FROM memberships AS m WHERE m.member = u.name AND m.database_name IN ('', ?2))))))`,
		user, database).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("catalog: read grants: %w", err)
	}
	return allowed, nil
}

// CanSelect reports whether user may read table of schema in database: an admin, or SELECT on that table or on
// every table of that schema, granted to the user or to one of its roles. CONNECT is CanConnect's question.
func (c *Catalog) CanSelect(ctx context.Context, user, database, schema, table string) (bool, error) {
	return c.Can(ctx, user, database, schema, table, "select")
}

// Can reports whether user holds privilege ("select", "insert", "update" or "delete") on table of schema in database:
// an admin, or the privilege on that table or on every table of that schema, granted to the user or to one of its
// roles.
func (c *Catalog) Can(ctx context.Context, user, database, schema, table, privilege string) (bool, error) {
	if ctx == nil {
		return false, errors.New("catalog: nil context")
	}
	if privilege != "select" && privilege != "insert" && privilege != "update" && privilege != "delete" {
		return false, fmt.Errorf("catalog: %q is no table privilege", privilege)
	}
	if c.admin != "" && user == c.admin {
		return true, nil
	}
	if user == "" || database == "" || schema == "" || table == "" {
		return false, nil
	}
	var allowed bool
	err := c.database.QueryRowContext(ctx, `SELECT EXISTS (
		SELECT 1 FROM users AS u WHERE u.name = ?1 AND u.disabled = 0 AND (
			u.admin = 1 OR EXISTS (
				SELECT 1 FROM grants AS g
				WHERE g.privilege = ?5 AND g.database_name = ?2 AND g.schema_name = ?3
					AND (g.table_name = ?4 OR g.table_name = '') AND (
					g.grantee = u.name OR g.grantee IN (
						SELECT m.role FROM memberships AS m WHERE m.member = u.name AND m.database_name IN ('', ?2))))))`,
		user, database, schema, table, privilege).Scan(&allowed)
	if err != nil {
		return false, fmt.Errorf("catalog: read grants: %w", err)
	}
	return allowed, nil
}

// Stamp changes whenever the user's password, grants or roles change: a session compares it before each statement
// and ends when it changed (revocation). It is 0 for a user that does not exist, and constant for the host admin.
func (c *Catalog) Stamp(ctx context.Context, user string) (uint64, error) {
	if ctx == nil {
		return 0, errors.New("catalog: nil context")
	}
	if c.admin != "" && user == c.admin {
		return hostAdminStamp, nil
	}
	var stamp int64
	err := c.database.QueryRowContext(ctx, `SELECT stamp FROM users WHERE name = ?`, user).Scan(&stamp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("catalog: read user: %w", err)
	}
	return uint64(stamp), nil
}
