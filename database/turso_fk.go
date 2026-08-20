package database

import (
	"context"
	"database/sql"
	"database/sql/driver"

	turso "turso.tech/database/tursogo"
)

// Turso reads its DSN as a plain file path and, unlike modernc, will not accept modernc's
// `?_pragma=foreign_keys(1)` query string — so foreign-key enforcement (which is OFF by default and is
// a per-connection setting) must be turned on when each physical connection is opened. We wrap tursogo's
// connector and run `PRAGMA foreign_keys=ON` on every new connection, so REFERENCES constraints and
// ON DELETE CASCADE actually fire.

type foreignKeyConnector struct{ inner driver.Connector }

func (c foreignKeyConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	if err := enableForeignKeys(ctx, conn); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return conn, nil
}

func (c foreignKeyConnector) Driver() driver.Driver { return c.inner.Driver() }

func enableForeignKeys(ctx context.Context, conn driver.Conn) error {
	const pragma = "PRAGMA foreign_keys=ON"
	if ex, ok := conn.(driver.ExecerContext); ok {
		_, err := ex.ExecContext(ctx, pragma, nil)
		return err
	}
	stmt, err := conn.Prepare(pragma)
	if err != nil {
		return err
	}
	defer stmt.Close()
	_, err = stmt.Exec(nil)
	return err
}

// openTurso opens a turso database whose every connection has foreign keys enabled.
func openTurso(dsn string) (*sql.DB, error) {
	connector, err := turso.NewConnector(dsn)
	if err != nil {
		return nil, err
	}
	return sql.OpenDB(foreignKeyConnector{inner: connector}), nil
}
