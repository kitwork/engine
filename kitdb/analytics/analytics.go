// Package analytics is KCOL on SQLite (docs/KITWORK_KITDB.md mục 6 and 7 of the Kitwork repository): a copy of some
// columns of a table of a KitDB file, laid out by column in blocks of 1,024 rows (kitdb/columnar), so a query that
// filters or aggregates a few columns of every row reads those columns alone instead of the whole file.
//
// The copy's main part is built from one read transaction of the file and names the revision it holds; the rows
// changed since are read from the history (kitwork_history) as the file goes on, so a query reads exactly what the
// rows read at the same moment. The engine decides which tables and columns have a copy; nobody declares one.
//
// The copy is derived data: <file>.analytics/ may be deleted at any time and is built again.
package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kitwork/engine/kitdb/columnar"
)

// ErrUnsupported means a column the copy cannot hold: a type other than an integer, a real or a text, a value of
// another type than the column's, a real that is not finite.
var ErrUnsupported = errors.New("analytics: column not supported")

// manifestFormat is the format of a table's manifest.
const manifestFormat = 1

// rowIDTag is the field of the rowid in a copy; column i of the manifest is field i+2.
const rowIDTag = 1

// Column is one column of a copy: its name and kind.
type Column struct {
	Name string        `json:"name"`
	Kind columnar.Kind `json:"kind"`
}

// Manifest describes the main part of one table's copy.
type Manifest struct {
	Format  int      `json:"format"`
	Table   string   `json:"table"`
	Data    string   `json:"data"` // the file of the main part, in the copy's directory
	Columns []Column `json:"columns"`
	// Revision is the last commit the main part holds: a file without a history holds 0.
	Revision uint64 `json:"revision"`
	// Schema is the file's schema_version when it was built: another one is another table.
	Schema int64  `json:"schema"`
	Rows   uint64 `json:"rows"`
	Bytes  int64  `json:"bytes"`
	// MaxRowID is the largest rowid the main part holds.
	MaxRowID int64 `json:"max_rowid"`
}

// fields are the copy's columnar fields: the rowid, then the columns.
func (manifest *Manifest) fields() []columnar.Field {
	fields := []columnar.Field{{Tag: rowIDTag, Kind: columnar.Integer}}
	for position, column := range manifest.Columns {
		fields = append(fields, columnar.Field{Tag: uint32(position + 2), Kind: column.Kind})
	}
	return fields
}

// position is the index of name among the manifest's columns, -1 when it has none of that name.
func (manifest *Manifest) position(name string) int {
	for position, column := range manifest.Columns {
		if column.Name == name {
			return position
		}
	}
	return -1
}

// Directory is the directory of the copies of the file at path.
func Directory(path string) string { return path + ".analytics" }

func manifestPath(directory, table string) string {
	return filepath.Join(directory, table+".json")
}

// Querier is what reads the file: a *sql.Tx or a *sql.Conn inside a read transaction.
type Querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// columnKinds reads the declared type of each column of table and gives its kind: INTEGER (and a boolean, stored
// 0 or 1) an integer, REAL a real, TEXT a text, by SQLite's rules of affinity. Any other is ErrUnsupported.
func columnKinds(ctx context.Context, source Querier, table string, names []string) ([]Column, error) {
	rows, err := source.QueryContext(ctx, `SELECT name, type, hidden FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return nil, err
	}
	declared := map[string]string{}
	for rows.Next() {
		var name, kind string
		var hidden int
		if err := rows.Scan(&name, &kind, &hidden); err != nil {
			rows.Close()
			return nil, err
		}
		if hidden == 0 {
			declared[name] = kind
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("analytics: table %q does not exist", table)
	}
	columns := make([]Column, 0, len(names))
	seen := map[string]bool{}
	for _, name := range names {
		kind, found := declared[name]
		if !found {
			return nil, fmt.Errorf("analytics: table %q has no column %q", table, name)
		}
		if seen[name] {
			return nil, fmt.Errorf("analytics: column %q twice", name)
		}
		seen[name] = true
		affinity, err := kindOf(kind)
		if err != nil {
			return nil, fmt.Errorf("%w: %s.%s is %q", err, table, name, kind)
		}
		columns = append(columns, Column{Name: name, Kind: affinity})
	}
	return columns, nil
}

// kindOf is SQLite's affinity of a declared type (https://sqlite.org/datatype3.html, 3.1), as a kind of the copy.
func kindOf(declared string) (columnar.Kind, error) {
	upper := strings.ToUpper(declared)
	switch {
	case strings.Contains(upper, "INT"):
		return columnar.Integer, nil
	case strings.Contains(upper, "CHAR"), strings.Contains(upper, "CLOB"), strings.Contains(upper, "TEXT"):
		return columnar.Text, nil
	case strings.Contains(upper, "BLOB"), upper == "", upper == "ANY":
		return 0, ErrUnsupported
	case strings.Contains(upper, "REAL"), strings.Contains(upper, "FLOA"), strings.Contains(upper, "DOUB"):
		return columnar.Float, nil
	}
	return 0, ErrUnsupported // NUMERIC: an integer on one row, a real on the next
}

// snapshotState is the revision and schema_version a read transaction sees.
func snapshotState(ctx context.Context, source Querier) (uint64, int64, error) {
	var schema int64
	if err := source.QueryRowContext(ctx, `PRAGMA schema_version`).Scan(&schema); err != nil {
		return 0, 0, err
	}
	var history int
	if err := source.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'kitwork_history'`).Scan(&history); err != nil {
		return 0, 0, err
	}
	if history == 0 {
		return 0, schema, nil
	}
	var revision int64
	if err := source.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0) FROM kitwork_history`).Scan(&revision); err != nil {
		return 0, 0, err
	}
	return uint64(revision), schema, nil
}

// ReadManifest reads the manifest of table's copy in directory; a copy that is not there is nil and no error.
func ReadManifest(directory, table string) (*Manifest, error) {
	content, err := os.ReadFile(manifestPath(directory, table))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var manifest Manifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return nil, fmt.Errorf("analytics: manifest of %q: %w", table, err)
	}
	if manifest.Format != manifestFormat || manifest.Table != table || manifest.Data != filepath.Base(manifest.Data) {
		return nil, fmt.Errorf("analytics: manifest of %q is not one this engine wrote", table)
	}
	return &manifest, nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
