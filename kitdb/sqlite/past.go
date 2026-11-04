package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrPastUnreachable is PastRelation's word that the table as it was cannot be read going backward: the schema moved
// after the moment, a column is generated, a delete carries no image (one written before 03/11/2026), or more rows
// changed since than the limit. The moment is read from a restored copy instead.
var ErrPastUnreachable = errors.New("kitdb: the moment is not reachable going backward")

// pastConn is what PastRelation works on, a *sql.Conn or a *sql.Tx: it holds a read transaction of the live file and is
// free to write its temporary tables (no query_only).
type pastConn interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	PrepareContext(ctx context.Context, query string) (*sql.Stmt, error)
}

// pastRow is what one row was at the moment, read from the history after it.
type pastRow struct {
	absent bool        // its first change after the moment inserted it
	whole  bool        // a delete after the moment holds its every column
	known  map[int]any // a column's value then: the old value of the first change after the moment that touched it
}

// PastRelation builds, on conn, inside the read transaction its caller opened, table as it was at revision, and names
// the relation that reads it: the table itself when no commit after revision changed it, the temporary view
// kitwork_at otherwise (KITWORK_KITDB.md mục 5).
//
// A row the history after revision changed is, at revision, its columns' old values from the first change after it
// that touched each: a delete touches every column, an update the columns it changed (every column in format 1).
// A row first inserted after revision was not there. A row deleted since is whole from the history and sits in a
// temporary table with the table's declared types and indexes. A row still in the table keeps its other columns
// as they are now: the view reads it from the table joined to the old values (kitwork_at_patch), so no row is read
// ahead, and only the columns some update touched go through a CASE, cast to their declared type so SQLite flattens
// the view and an ORDER BY with a LIMIT merges both sides through their indexes. The rows the history changed are
// limited to limit. It is ErrPastUnreachable when the history after revision cannot be undone (see there).
func PastRelation(ctx context.Context, conn pastConn, table string, revision uint64, limit int) (string, error) {
	var names, types []string
	rows, err := conn.QueryContext(ctx, `SELECT name, type, hidden FROM pragma_table_xinfo(?) ORDER BY cid`, table)
	if err != nil {
		return "", err
	}
	for rows.Next() {
		var name, kind string
		var hidden int
		if err := rows.Scan(&name, &kind, &hidden); err != nil {
			rows.Close()
			return "", err
		}
		if hidden != 0 {
			rows.Close()
			return "", fmt.Errorf("%w: %q has a generated column, which the history's images do not hold", ErrPastUnreachable, table)
		}
		names, types = append(names, name), append(types, kind)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", fmt.Errorf("%w: %q is not in the file now", ErrPastUnreachable, table)
	}
	position := make(map[string]int, len(names))
	for index, name := range names {
		position[name] = index
	}

	states := map[int64]*pastRow{}
	var order []int64
	err = ReadChanges(ctx, conn, revision, func(commit Commit) error {
		for _, change := range commit.Changes {
			if change.Operation == Statement || change.Operation == Barrier {
				return fmt.Errorf("%w: revision %d changed the schema", ErrPastUnreachable, commit.Revision)
			}
			if change.Table != table {
				continue
			}
			state := states[change.RowID]
			if state == nil {
				if len(states) >= limit {
					return fmt.Errorf("%w: more than %d rows of %q changed since", ErrPastUnreachable, limit, table)
				}
				state = &pastRow{known: map[int]any{}, absent: change.Operation == Insert}
				states[change.RowID] = state
				order = append(order, change.RowID)
			}
			if state.absent || state.whole {
				continue // what the row was then is settled
			}
			switch change.Operation {
			case Update:
				for _, update := range change.Updates {
					if update.Position < 0 || update.Position >= len(change.Columns) {
						return fmt.Errorf("%w: update of row %d of %q names column %d of %d", ErrCorruptHistory, change.RowID, table, update.Position, len(change.Columns))
					}
					at, found := position[change.Columns[update.Position]]
					if !found {
						return fmt.Errorf("%w: column %q of %q is gone", ErrPastUnreachable, change.Columns[update.Position], table)
					}
					if _, set := state.known[at]; !set {
						state.known[at] = update.Old
					}
				}
			case Delete:
				if len(change.Columns) == 0 || len(change.Columns) != len(change.Before) {
					return fmt.Errorf("%w: row %d of %q was deleted without its image", ErrPastUnreachable, change.RowID, table)
				}
				for index, column := range change.Columns {
					at, found := position[column]
					if !found {
						return fmt.Errorf("%w: column %q of %q is gone", ErrPastUnreachable, column, table)
					}
					if _, set := state.known[at]; !set {
						state.known[at] = change.Before[index]
					}
				}
				state.whole = true
			case Insert:
				return fmt.Errorf("%w: row %d of %q is inserted while it is there", ErrCorruptHistory, change.RowID, table)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(states) == 0 {
		return table, nil
	}

	// the columns some row keeps an old value of, read through a CASE; the others read straight from the table
	patched := map[int]bool{}
	for _, rowID := range order {
		if state := states[rowID]; !state.absent && !state.whole {
			for at := range state.known {
				patched[at] = true
			}
		}
	}
	var patchedOrder []int
	for at := range names {
		if patched[at] {
			patchedOrder = append(patchedOrder, at)
		}
	}
	quoted := make([]string, len(names))
	declared := make([]string, len(names))
	for at, name := range names {
		quoted[at] = quoteIdentifier(name)
		declared[at] = quoted[at] + " " + types[at]
	}
	patchColumns := []string{"rid INTEGER PRIMARY KEY"}
	for _, at := range patchedOrder {
		patchColumns = append(patchColumns, fmt.Sprintf("v_%d %s", at, types[at]), fmt.Sprintf("k_%d INTEGER", at))
	}
	for _, statement := range []string{
		`CREATE TEMP TABLE kitwork_at_excluded (rid INTEGER PRIMARY KEY)`,
		`CREATE TEMP TABLE kitwork_at_before (` + strings.Join(declared, ", ") + `)`,
		`CREATE TEMP TABLE kitwork_at_patch (` + strings.Join(patchColumns, ", ") + `)`,
	} {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return "", err
		}
	}
	excluded, err := conn.PrepareContext(ctx, `INSERT INTO temp.kitwork_at_excluded (rid) VALUES (?)`)
	if err != nil {
		return "", err
	}
	defer excluded.Close()
	before, err := conn.PrepareContext(ctx, `INSERT INTO temp.kitwork_at_before VALUES (`+strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")+`)`)
	if err != nil {
		return "", err
	}
	defer before.Close()
	patch, err := conn.PrepareContext(ctx, `INSERT INTO temp.kitwork_at_patch VALUES (`+strings.TrimSuffix(strings.Repeat("?, ", 1+2*len(patchedOrder)), ", ")+`)`)
	if err != nil {
		return "", err
	}
	defer patch.Close()
	for _, rowID := range order {
		state := states[rowID]
		switch {
		case state.absent:
			if _, err := excluded.ExecContext(ctx, rowID); err != nil {
				return "", err
			}
		case state.whole:
			if _, err := excluded.ExecContext(ctx, rowID); err != nil {
				return "", err
			}
			values := make([]any, len(names))
			for at := range names {
				values[at] = state.known[at]
			}
			if _, err := before.ExecContext(ctx, values...); err != nil {
				return "", err
			}
		case len(state.known) > 0:
			values := []any{rowID}
			for _, at := range patchedOrder {
				old, set := state.known[at]
				flag := 0
				if set {
					flag = 1
				}
				values = append(values, old, flag)
			}
			if _, err := patch.ExecContext(ctx, values...); err != nil {
				return "", err
			}
		}
		// an update that changed nothing leaves the row as it is now
	}
	indexes, err := tableIndexes(ctx, conn, table)
	if err != nil {
		return "", err
	}
	for number, keys := range indexes {
		keyNames := make([]string, len(keys))
		for index, key := range keys {
			keyNames[index] = quoteIdentifier(key)
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX temp.kitwork_at_index_%d ON kitwork_at_before (%s)`,
			number, strings.Join(keyNames, ", "))); err != nil {
			return "", err
		}
	}
	live := make([]string, len(names))
	for at := range names {
		if !patched[at] {
			live[at] = `m.` + quoted[at] + ` AS ` + quoted[at]
			continue
		}
		value := fmt.Sprintf(`CASE WHEN p.k_%d THEN p.v_%d ELSE m.%s END`, at, at, quoted[at])
		if types[at] != "" {
			value = `CAST(` + value + ` AS ` + types[at] + `)`
		}
		live[at] = value + ` AS ` + quoted[at]
	}
	view := `CREATE TEMP VIEW kitwork_at AS SELECT ` + strings.Join(live, ", ") + ` FROM main.` + quoteIdentifier(table) + ` m`
	if len(patchedOrder) > 0 {
		view += ` LEFT JOIN temp.kitwork_at_patch p ON p.rid = m._rowid_`
	}
	view += ` WHERE m._rowid_ NOT IN (SELECT rid FROM temp.kitwork_at_excluded) UNION ALL SELECT ` + strings.Join(quoted, ", ") +
		` FROM temp.kitwork_at_before`
	if _, err := conn.ExecContext(ctx, view); err != nil {
		return "", err
	}
	return "kitwork_at", nil
}

// tableIndexes lists the key columns of each index of table, one on an expression left out.
func tableIndexes(ctx context.Context, conn pastConn, table string) ([][]string, error) {
	rows, err := conn.QueryContext(ctx, `SELECT name FROM pragma_index_list(?)`, table)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var indexes [][]string
	for _, name := range names {
		keys, err := conn.QueryContext(ctx, `SELECT coalesce(name, '') FROM pragma_index_xinfo(?) WHERE key = 1 ORDER BY seqno`, name)
		if err != nil {
			return nil, err
		}
		var columns []string
		expression := false
		for keys.Next() {
			var column string
			if err := keys.Scan(&column); err != nil {
				keys.Close()
				return nil, err
			}
			expression = expression || column == ""
			columns = append(columns, column)
		}
		keys.Close()
		if err := keys.Err(); err != nil {
			return nil, err
		}
		if !expression && len(columns) > 0 {
			indexes = append(indexes, columns)
		}
	}
	return indexes, nil
}
