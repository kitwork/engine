package gateway

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kitwork/engine/kitdb/pgwire"
)

// schemaMap is one attached schema's b-trees by root page, read from its sqlite_schema at a schema version.
type schemaMap struct {
	loaded  bool
	version int64
	tables  map[int64]string // root page → the table it belongs to (an index → its table)
}

// operation is one row of EXPLAIN: one opcode of the statement's program.
type operation struct {
	address int64
	opcode  string
	p1      int64
	p2      int64
	p3      int64
	p4      string
	p5      int64
}

// p2IsRegister is OPFLAG_P2ISREG: p2 of an Open opcode names a register holding the root page, not the page.
const p2IsRegister = 0x10

// schemaRootPage is the root page of sqlite_schema in every database file.
const schemaRootPage = 1

// temporaryDatabase is SQLite's index of the TEMP database (p3 of an opcode), where only the catalog lives: the
// session is read-only, so no statement can make a TEMP table of its own.
const temporaryDatabase = 1

// writingOpcodes change a database, a schema or the connection; a read statement has none of them.
var writingOpcodes = map[string]bool{
	"OpenWrite": true, "Clear": true, "Destroy": true, "CreateBtree": true, "SqlExec": true, "ParseSchema": true,
	"LoadAnalysis": true, "DropTable": true, "DropIndex": true, "DropTrigger": true, "Vacuum": true,
	"IncrVacuum": true, "AutoCommit": true, "Savepoint": true, "SetCookie": true, "JournalMode": true,
	"Checkpoint": true, "VUpdate": true, "VCreate": true, "VDestroy": true, "VRename": true, "VBegin": true,
	"Expire": true,
}

// refreshSchemas runs inside the statement's read transaction, before EXPLAIN. Reading each schema's version starts
// the transaction's snapshot of that file; a schema whose version moved since its root pages were read is read again.
//
// That second read is also what keeps EXPLAIN honest. EXPLAIN never runs the program it lists, so it never makes
// SQLite check the schema cookie: it lists the program of SQLite's cached copy of the schema, while the statement
// itself, when it runs, finds the cookie changed and runs a program prepared again from the new schema (another
// connection redefined a view: EXPLAIN shows the old tables, the statement reads the new ones). Reading
// sqlite_schema is a statement that does check the cookie and reloads SQLite's copy. When the version read now
// equals the one the root pages were read at, SQLite's copy is that version too: it was refreshed by that same read,
// can only have moved forward since, and cannot be ahead of the snapshot.
func (session *Session) refreshSchemas(ctx context.Context) error {
	for _, name := range session.order {
		var version int64
		if err := session.connection.QueryRowContext(ctx, "PRAGMA "+quoteIdentifier(name)+".schema_version").Scan(&version); err != nil {
			return sqliteError(err)
		}
		schema := session.schemas[name]
		if schema.loaded && schema.version == version {
			continue
		}
		tables, err := session.loadSchema(ctx, name)
		if err != nil {
			return err
		}
		schema.tables, schema.version, schema.loaded = tables, version, true
	}
	return nil
}

func (session *Session) loadSchema(ctx context.Context, name string) (map[int64]string, error) {
	rows, err := session.connection.QueryContext(ctx,
		"SELECT tbl_name, rootpage FROM "+quoteIdentifier(name)+".sqlite_schema WHERE rootpage > 0")
	if err != nil {
		return nil, sqliteError(err)
	}
	defer rows.Close()
	tables := map[int64]string{}
	for rows.Next() {
		var table string
		var root int64
		if err := rows.Scan(&table, &root); err != nil {
			return nil, sqliteError(err)
		}
		tables[root] = table
	}
	if err := rows.Err(); err != nil {
		return nil, sqliteError(err)
	}
	return tables, nil
}

// explain returns the program of text, prepared with arguments bound as the statement itself will be.
func (session *Session) explain(ctx context.Context, text string, arguments []any) ([]operation, error) {
	rows, err := session.connection.QueryContext(ctx, "EXPLAIN "+text, arguments...)
	if err != nil {
		return nil, sqliteError(err)
	}
	defer rows.Close()
	var operations []operation
	for rows.Next() {
		var current operation
		var p4, comment sql.NullString
		if err := rows.Scan(&current.address, &current.opcode, &current.p1, &current.p2, &current.p3, &p4, &current.p5, &comment); err != nil {
			return nil, sqliteError(err)
		}
		current.p4 = p4.String
		operations = append(operations, current)
	}
	if err := rows.Err(); err != nil {
		return nil, sqliteError(err)
	}
	return operations, nil
}

// authorize checks the program of a read statement before it runs: no opcode may write, every b-tree it opens must
// belong to a table the user may select, and the only virtual tables are json_each and json_tree.
func (session *Session) authorize(ctx context.Context, parsed statement, arguments []any) error {
	operations, err := session.explain(ctx, parsed.authorized, arguments)
	if err != nil {
		return err
	}
	type table struct{ schema, name string }
	var tables []table
	seen := map[table]bool{}
	for _, current := range operations {
		switch {
		case writingOpcodes[current.opcode] || (current.opcode == "Transaction" && current.p2 != 0):
			return readOnlyError(parsed.verb)
		case current.opcode == "OpenRead" || current.opcode == "ReopenIdx":
			if current.p5&p2IsRegister != 0 {
				return pgwire.NewError("42501", "permission denied: the statement opens a table SQLite chooses only while it runs")
			}
			schema, attached := session.databases[current.p3]
			if current.p2 == schemaRootPage {
				continue // sqlite_schema: names and definitions only
			}
			if current.p3 == temporaryDatabase {
				continue // the catalog's TEMP tables (catalog.go): names and definitions only
			}
			if !attached {
				return pgwire.NewError("42501", "permission denied: the statement reads a table outside the database's schemas")
			}
			name, known := session.schemas[schema].tables[current.p2]
			if !known {
				return pgwire.NewError("42501", fmt.Sprintf(
					"permission denied: the statement reads a table of schema %s the session cannot name", schema))
			}
			candidate := table{schema: schema, name: name}
			if !seen[candidate] {
				seen[candidate] = true
				tables = append(tables, candidate)
			}
		case current.opcode == "VOpen":
			if _, allowed := session.tableFunctions[current.p4]; !allowed {
				return pgwire.NewError("42501",
					"permission denied: virtual tables and table-valued functions other than json_each and json_tree are not available")
			}
		}
	}
	for _, candidate := range tables {
		allowed, err := session.access.CanSelect(ctx, candidate.schema, candidate.name)
		if err != nil {
			return protocolError(err, fmt.Sprintf("checking the permission on table %s.%s", candidate.schema, candidate.name))
		}
		if !allowed {
			return pgwire.NewError("42501", fmt.Sprintf("permission denied for table %s.%s", candidate.schema, candidate.name))
		}
	}
	return nil
}
