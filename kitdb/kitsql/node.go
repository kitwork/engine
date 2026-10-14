package kitsql

import (
	"context"
	"database/sql"
	"errors"

	"github.com/kitwork/engine/kitdb/managed"
	"github.com/kitwork/engine/kitdb/relational"
)

// OpenNodeDatabase binds a handler to the same authenticated root and managed
// relational owners as pgwire/native. Each HTTP request retires its logical pool.
func OpenNodeDatabase(node *relational.PostgresNode) OpenDatabase {
	return func(ctx context.Context, user, password, name string) (*sql.DB, func() error, error) {
		db, err := node.OpenKitSQLDatabase(ctx, user, password, name)
		if errors.Is(err, managed.ErrAuthentication) {
			return nil, nil, ErrUnauthorized
		}
		if errors.Is(err, managed.ErrPermission) {
			return nil, nil, ErrForbidden
		}
		if err != nil {
			return nil, nil, err
		}
		return db, db.Close, nil
	}
}
