package relational

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kitwork/engine/kitdb/managed"
)

func (node *PostgresNode) authenticateAccount(ctx context.Context, user, password string) (managed.UserInfo, error) {
	if err := ctx.Err(); err != nil {
		return managed.UserInfo{}, err
	}
	if node.catalog != nil && node.catalog.AuthEnabled() {
		return node.catalog.Authenticate(ctx, user, password)
	}
	if subtle.ConstantTimeCompare([]byte(user), []byte(node.user)) != 1 || subtle.ConstantTimeCompare([]byte(password), []byte(node.password)) != 1 {
		return managed.UserInfo{}, managed.ErrAuthentication
	}
	return managed.UserInfo{Name: node.user, Admin: true}, nil
}

// OpenAuthenticatedDatabase is the shared node entrance for in-process clients
// and a KitSQL HandlerOptions.Open callback. The pool never opens a side owner.
// Grants are captured at logical pool creation; recreate the pool after SQL
// grants change. Offline operator commands still require stopping the node.
func (node *PostgresNode) OpenAuthenticatedDatabase(ctx context.Context, user, password, name string) (*sql.DB, error) {
	return node.openAuthenticatedDatabase(ctx, user, password, name, "native")
}

// OpenKitSQLDatabase labels request-scoped HTTP connections without granting
// additional authority or creating a second database owner.
func (node *PostgresNode) OpenKitSQLDatabase(ctx context.Context, user, password, name string) (*sql.DB, error) {
	return node.openAuthenticatedDatabase(ctx, user, password, name, "kitsql")
}

func (node *PostgresNode) openAuthenticatedDatabase(ctx context.Context, user, password, name, adapter string) (*sql.DB, error) {
	if node == nil {
		return nil, managed.ErrAuthentication
	}
	account, err := node.authenticateAccount(ctx, user, password)
	if err != nil {
		return nil, err
	}
	name = postgresLogicalDatabaseName(name)
	if err := validatePostgresLogicalDatabaseName(name); err != nil {
		return nil, err
	}
	if strings.EqualFold(name, node.maintenanceDatabase) {
		return nil, fmt.Errorf("kitdb native node: maintenance database has no user structs")
	}
	readOnly, allowed := account.Access(name)
	if !allowed {
		return nil, managed.ErrPermission
	}
	databases, err := node.discoverDatabases()
	if err != nil {
		return nil, err
	}
	database, found := findPostgresNodeDatabase(databases, name)
	if !found {
		return nil, fmt.Errorf("kitdb native node: database does not exist")
	}
	permissions := account.Permissions(name)
	return sql.OpenDB(nativeNodeSQLConnector{node: node, database: database.name, readOnly: readOnly, permissions: &permissions, account: &account, adapter: adapter}), nil
}

func (session *postgresSession) visibleDatabases() ([]string, error) {
	names, err := session.node.Databases()
	if err != nil {
		return nil, err
	}
	if session.node.catalog == nil || !session.node.catalog.AuthEnabled() {
		return names, nil
	}
	var visible []string
	for _, name := range names {
		_, allowed := session.account.Access(name)
		if name == session.node.maintenanceDatabase || allowed {
			visible = append(visible, name)
		}
	}
	return visible, nil
}
