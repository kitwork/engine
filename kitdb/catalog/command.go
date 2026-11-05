package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// Command runs an account statement (CREATE USER, ALTER USER, DROP USER, CREATE ROLE, DROP ROLE, GRANT, REVOKE) for
// actor on database; handled=false when the statement is not an account statement. Only an admin may run them.
// Returns the command tag ("CREATE ROLE" for CREATE USER, as Postgres does). database is the database the session is
// connected to, the one GRANT SELECT and REVOKE SELECT apply to. Errors are *pgwire.Error with Postgres's SQLSTATE
// when the code means something to a client: 42601 syntax, 42501 not an admin, 42710 the name exists, 42704 it does
// not, 0A000 not supported, 22023 a password refused.
func (c *Catalog) Command(ctx context.Context, actor, database, statementText string) (tag string, handled bool, err error) {
	if ctx == nil {
		return "", false, errors.New("catalog: nil context")
	}
	parsed, handled, err := parseStatement(statementText)
	if !handled {
		return "", false, nil
	}
	if err != nil {
		return "", true, err
	}
	denied := pgwire.NewError("42501", "permission denied to "+strings.ToLower(parsed.tag())+": only an admin manages accounts and grants")
	admin, err := c.administrator(ctx, c.database, actor)
	if err != nil {
		return "", true, err
	}
	if !admin {
		return "", true, denied
	}
	var verifier *pgwire.SCRAMVerifier
	if parsed.password != nil {
		derived, err := passwordVerifier(*parsed.password)
		if err != nil {
			return "", true, err
		}
		verifier = &derived
		if err := ctx.Err(); err != nil {
			return "", true, err
		}
	}
	err = c.write(ctx, func(work *transaction) error {
		admin, err := c.administrator(ctx, work.sql, actor)
		if err != nil {
			return err
		}
		if !admin {
			return denied
		}
		return c.apply(work, actor, database, parsed, verifier)
	})
	if err != nil {
		return "", true, err
	}
	return parsed.tag(), true, nil
}

// passwordVerifier is the verifier a CREATE USER or ALTER USER password gives: a SCRAM-SHA-256 verifier in Postgres's
// text form kept as it is, or a password of 12 to 1024 bytes derived with a fresh salt. Text that starts like a
// verifier but is not one is refused (Postgres would take it as a password), and so is an MD5 hash.
func passwordVerifier(password string) (pgwire.SCRAMVerifier, error) {
	if verifier, ok := parseVerifier(password); ok {
		return verifier, nil
	}
	if strings.HasPrefix(password, "SCRAM-SHA-256$") {
		return pgwire.SCRAMVerifier{}, pgwire.NewError("22023", "a malformed SCRAM-SHA-256 verifier (iterations 4096 to 10000000, a salt of 8 to 64 bytes, two 32-byte keys)")
	}
	if md5Hash(password) {
		return pgwire.SCRAMVerifier{}, unsupported("MD5 password hashes are not supported: send the password or a SCRAM-SHA-256 verifier")
	}
	if strings.IndexByte(password, 0) >= 0 {
		return pgwire.SCRAMVerifier{}, pgwire.NewError("22023", "a password cannot contain NUL")
	}
	if len(password) < minimumPasswordBytes || len(password) > maximumPasswordBytes {
		return pgwire.SCRAMVerifier{}, pgwire.NewError("22023", "a password must be 12 to 1024 bytes")
	}
	verifier, err := derivePassword(password)
	if err != nil {
		return pgwire.SCRAMVerifier{}, fmt.Errorf("catalog: derive password: %w", err)
	}
	return verifier, nil
}

// transaction is one write: a BEGIN IMMEDIATE transaction under the catalog's writer mutex, and the stamp it gives
// the users it changes (taken from the sequence once, when first needed).
type transaction struct {
	context context.Context
	sql     *sql.Tx
	stamp   int64
}

// write runs apply in one transaction, committed when apply returns nil.
func (c *Catalog) write(ctx context.Context, apply func(*transaction) error) error {
	c.writer.Lock()
	defer c.writer.Unlock()
	begun, err := c.database.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("catalog: begin: %w", err)
	}
	defer begun.Rollback()
	if err := apply(&transaction{context: ctx, sql: begun}); err != nil {
		return err
	}
	if err := begun.Commit(); err != nil {
		return fmt.Errorf("catalog: commit: %w", err)
	}
	return nil
}

func (work *transaction) exec(query string, arguments ...any) (int64, error) {
	result, err := work.sql.ExecContext(work.context, query, arguments...)
	if err != nil {
		return 0, fmt.Errorf("catalog: write: %w", err)
	}
	return result.RowsAffected()
}

func (work *transaction) exists(query string, arguments ...any) (bool, error) {
	var found bool
	if err := work.sql.QueryRowContext(work.context, `SELECT EXISTS (`+query+`)`, arguments...).Scan(&found); err != nil {
		return false, fmt.Errorf("catalog: read: %w", err)
	}
	return found, nil
}

// nextStamp is this transaction's stamp, a value of the sequence no user had before.
func (work *transaction) nextStamp() (int64, error) {
	if work.stamp == 0 {
		err := work.sql.QueryRowContext(work.context, `UPDATE sequence SET value = value + 1 WHERE name = 'stamp' RETURNING value`).Scan(&work.stamp)
		if err != nil {
			return 0, fmt.Errorf("catalog: take a stamp: %w", err)
		}
	}
	return work.stamp, nil
}

// touch gives a new stamp to the user name, or to every member of the role name.
func (work *transaction) touch(name string) error {
	stamp, err := work.nextStamp()
	if err != nil {
		return err
	}
	_, err = work.exec(`UPDATE users SET stamp = ?1 WHERE name = ?2 OR name IN (SELECT member FROM memberships WHERE role = ?2)`, stamp, name)
	return err
}

// kind is "user", "role", or "" for a name that is neither.
func (work *transaction) kind(name string) (string, error) {
	var kind string
	err := work.sql.QueryRowContext(work.context, `SELECT CASE
		WHEN EXISTS (SELECT 1 FROM users WHERE name = ?1) THEN 'user'
		WHEN EXISTS (SELECT 1 FROM roles WHERE name = ?1) THEN 'role'
		ELSE '' END`, name).Scan(&kind)
	if err != nil {
		return "", fmt.Errorf("catalog: read: %w", err)
	}
	return kind, nil
}

func roleExists(name string) error {
	return pgwire.NewError("42710", fmt.Sprintf("role %q already exists", name))
}

func roleMissing(name string) error {
	return pgwire.NewError("42704", fmt.Sprintf("role %q does not exist", name))
}

func (c *Catalog) apply(work *transaction, actor, database string, parsed *statement, verifier *pgwire.SCRAMVerifier) error {
	switch parsed.action {
	case actionCreateUser, actionCreateRole:
		return c.create(work, parsed, verifier)
	case actionAlterUser:
		return c.alter(work, parsed, verifier)
	case actionDrop:
		return c.drop(work, actor, parsed)
	case actionGrantConnect:
		return c.grantConnect(work, parsed)
	case actionGrantTable:
		return c.grantTable(work, database, parsed)
	case actionGrantRole:
		return c.grantRole(work, parsed)
	}
	return fmt.Errorf("catalog: unknown action %q", parsed.action)
}

func (c *Catalog) create(work *transaction, parsed *statement, verifier *pgwire.SCRAMVerifier) error {
	if c.admin != "" && parsed.name == c.admin {
		return roleExists(parsed.name)
	}
	kind, err := work.kind(parsed.name)
	if err != nil {
		return err
	}
	if kind != "" {
		return roleExists(parsed.name)
	}
	if parsed.action == actionCreateRole {
		_, err := work.exec(`INSERT INTO roles (name) VALUES (?)`, parsed.name)
		return err
	}
	stamp, err := work.nextStamp()
	if err != nil {
		return err
	}
	disabled := parsed.login != nil && !*parsed.login
	_, err = work.exec(`INSERT INTO users (name, salt, iterations, stored_key, server_key, admin, disabled, stamp)
		VALUES (?, ?, ?, ?, ?, 0, ?, ?)`,
		parsed.name, verifier.Salt, verifier.Iterations, verifier.StoredKey[:], verifier.ServerKey[:], disabled, stamp)
	return err
}

func (c *Catalog) alter(work *transaction, parsed *statement, verifier *pgwire.SCRAMVerifier) error {
	if c.admin != "" && parsed.name == c.admin {
		return unsupported(fmt.Sprintf("role %q is the host admin: its password is set in application.work", parsed.name))
	}
	kind, err := work.kind(parsed.name)
	if err != nil {
		return err
	}
	switch kind {
	case "":
		return roleMissing(parsed.name)
	case "role":
		return unsupported(fmt.Sprintf("role %q cannot log in: it is a group made by CREATE ROLE", parsed.name))
	}
	if parsed.login != nil && !*parsed.login {
		if err := c.protectLastAdmin(work, parsed.name); err != nil {
			return err
		}
	}
	if verifier != nil {
		if _, err := work.exec(`UPDATE users SET salt = ?, iterations = ?, stored_key = ?, server_key = ? WHERE name = ?`,
			verifier.Salt, verifier.Iterations, verifier.StoredKey[:], verifier.ServerKey[:], parsed.name); err != nil {
			return err
		}
	}
	if parsed.login != nil {
		if _, err := work.exec(`UPDATE users SET disabled = ? WHERE name = ?`, !*parsed.login, parsed.name); err != nil {
			return err
		}
	}
	return work.touch(parsed.name)
}

// protectLastAdmin refuses to drop or disable the last enabled stored admin of a catalog without a host admin.
func (c *Catalog) protectLastAdmin(work *transaction, name string) error {
	if c.admin != "" {
		return nil
	}
	last, err := work.exists(`SELECT 1 FROM users WHERE name = ?1 AND admin = 1 AND disabled = 0
		AND NOT EXISTS (SELECT 1 FROM users WHERE name <> ?1 AND admin = 1 AND disabled = 0)`, name)
	if err != nil {
		return err
	}
	if last {
		return pgwire.NewError("55000", "cannot drop or disable the last enabled admin")
	}
	return nil
}

func (c *Catalog) drop(work *transaction, actor string, parsed *statement) error {
	if c.admin != "" && parsed.name == c.admin {
		return pgwire.NewError("55006", fmt.Sprintf("cannot drop role %q: it is the host admin, set in application.work", parsed.name))
	}
	if parsed.name == actor {
		return pgwire.NewError("55006", "current user cannot be dropped")
	}
	kind, err := work.kind(parsed.name)
	if err != nil {
		return err
	}
	switch kind {
	case "":
		if parsed.ifExists {
			return nil
		}
		return roleMissing(parsed.name)
	case "user":
		if err := c.protectLastAdmin(work, parsed.name); err != nil {
			return err
		}
		_, err := work.exec(`DELETE FROM users WHERE name = ?`, parsed.name)
		return err
	}
	if err := work.touch(parsed.name); err != nil {
		return err
	}
	_, err = work.exec(`DELETE FROM roles WHERE name = ?`, parsed.name)
	return err
}

// grantee checks the user or role a grant goes to; skip is true for the host admin, who holds every grant already.
func (c *Catalog) grantee(work *transaction, name string) (skip bool, err error) {
	if c.admin != "" && name == c.admin {
		return true, nil
	}
	kind, err := work.kind(name)
	if err != nil {
		return false, err
	}
	if kind == "" {
		return false, roleMissing(name)
	}
	return false, nil
}

func (c *Catalog) grantConnect(work *transaction, parsed *statement) error {
	skip, err := c.grantee(work, parsed.name)
	if err != nil || skip {
		return err
	}
	var changed int64
	if parsed.revoke {
		changed, err = work.exec(`DELETE FROM grants WHERE grantee = ? AND privilege = 'connect' AND database_name = ?`, parsed.name, parsed.database)
	} else {
		changed, err = work.exec(`INSERT OR IGNORE INTO grants (grantee, privilege, database_name) VALUES (?, 'connect', ?)`, parsed.name, parsed.database)
	}
	if err != nil || changed == 0 {
		return err
	}
	return work.touch(parsed.name)
}

func (c *Catalog) grantTable(work *transaction, database string, parsed *statement) error {
	if database == "" {
		return pgwire.NewError("3D000", "GRANT on a table applies to the database the session is connected to: connect to one")
	}
	skip, err := c.grantee(work, parsed.name)
	if err != nil || skip {
		return err
	}
	var changed int64
	for _, privilege := range parsed.privileges {
		var count int64
		switch {
		case !parsed.revoke:
			count, err = work.exec(`INSERT OR IGNORE INTO grants (grantee, privilege, database_name, schema_name, table_name)
				VALUES (?, ?, ?, ?, ?)`, parsed.name, privilege, database, parsed.schema, parsed.table)
		case parsed.table == "":
			count, err = work.exec(`DELETE FROM grants WHERE grantee = ? AND privilege = ? AND database_name = ? AND schema_name = ?`,
				parsed.name, privilege, database, parsed.schema)
		default:
			everyTable, err := work.exists(`SELECT 1 FROM grants WHERE grantee = ? AND privilege = ? AND database_name = ?
				AND schema_name = ? AND table_name = ''`, parsed.name, privilege, database, parsed.schema)
			if err != nil {
				return err
			}
			if everyTable {
				word := strings.ToUpper(privilege)
				return unsupported(fmt.Sprintf("%s on every table of schema %q stays granted to %q: REVOKE %s ON ALL TABLES IN SCHEMA %s FROM %s first",
					word, parsed.schema, parsed.name, word, parsed.schema, parsed.name))
			}
			count, err = work.exec(`DELETE FROM grants WHERE grantee = ? AND privilege = ? AND database_name = ? AND schema_name = ?
				AND table_name = ?`, parsed.name, privilege, database, parsed.schema, parsed.table)
		}
		if err != nil {
			return err
		}
		changed += count
	}
	if changed == 0 {
		return nil
	}
	return work.touch(parsed.name)
}

func (c *Catalog) grantRole(work *transaction, parsed *statement) error {
	if c.admin != "" && parsed.role == c.admin {
		return unsupported(fmt.Sprintf("role %q is the host admin and cannot be granted", parsed.role))
	}
	kind, err := work.kind(parsed.role)
	if err != nil {
		return err
	}
	switch kind {
	case "":
		return roleMissing(parsed.role)
	case "user":
		return unsupported(fmt.Sprintf("role %q is a user: only a role made by CREATE ROLE is granted", parsed.role))
	}
	skip, err := c.grantee(work, parsed.name)
	if err != nil || skip {
		return err
	}
	if kind, err := work.kind(parsed.name); err != nil {
		return err
	} else if kind == "role" {
		return unsupported(fmt.Sprintf("role %q is a group: roles are granted to users only", parsed.name))
	}
	var changed int64
	switch {
	case !parsed.revoke:
		changed, err = work.exec(`INSERT OR IGNORE INTO memberships (role, member, database_name) VALUES (?, ?, ?)`,
			parsed.role, parsed.name, parsed.database)
	case parsed.database == "":
		changed, err = work.exec(`DELETE FROM memberships WHERE role = ? AND member = ?`, parsed.role, parsed.name)
	default:
		changed, err = work.exec(`DELETE FROM memberships WHERE role = ? AND member = ? AND database_name = ?`,
			parsed.role, parsed.name, parsed.database)
		if err != nil {
			return err
		}
		everyDatabase, err := work.exists(`SELECT 1 FROM memberships WHERE role = ? AND member = ? AND database_name = ''`,
			parsed.role, parsed.name)
		if err != nil {
			return err
		}
		if everyDatabase {
			return unsupported(fmt.Sprintf("role %q stays granted to %q for every database: REVOKE %s FROM %s first",
				parsed.role, parsed.name, parsed.role, parsed.name))
		}
	}
	if err != nil || changed == 0 {
		return err
	}
	return work.touch(parsed.name)
}
