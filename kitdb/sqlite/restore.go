package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Restore writes to target the database as it was at moment: the newest anchor
// at or before it (a base and the .pages of its chain, or a whole copy), then
// every later commit whose moment is not after it. The replayed history is
// checked link by link against the anchor's own last checksum. It returns the
// restored revision. target must not exist. A moment before the history Retain
// kept is ErrPruned.
func (database *Database) Restore(ctx context.Context, moment time.Time, target string) (uint64, error) {
	return database.restore(ctx, moment, target, "")
}

// RestoreTable is Restore for a reader of one table (an app's at()): it is refused, ErrPastUnreachable, unless the
// history holds every change of table from the anchor it starts from to the moment. A table the history does not
// record, a moment before where its recording last started (TableHistory's from) and an anchor before that start (a
// Tracked change of the table in the replay) are each refused: rows changed then with no trace, and the table restored
// would not be as it was.
func (database *Database) RestoreTable(ctx context.Context, moment time.Time, target, table string) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	on, from, err := TableHistory(ctx, database.readers, table)
	if err != nil {
		return 0, err
	}
	if !on {
		return 0, fmt.Errorf("%w: the history does not record %q", ErrPastUnreachable, table)
	}
	revision, err := database.RevisionAt(ctx, moment)
	if err != nil {
		return 0, err
	}
	if from > 1 && revision < from {
		return 0, fmt.Errorf("%w: the history records %q from revision %d only", ErrPastUnreachable, table, from)
	}
	return database.restore(ctx, moment, target, table)
}

func (database *Database) restore(ctx context.Context, moment time.Time, target, table string) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if _, err := os.Stat(target); err == nil {
		return 0, fmt.Errorf("kitdb: restore target %q exists", target)
	}
	through := moment.UTC().UnixNano()
	if err := database.prunedBefore(ctx, through); err != nil {
		return 0, err
	}
	// a fold and Retain's removals wait for the anchor files to be read
	database.anchorFiles.RLock()
	anchors, err := database.anchors()
	if err != nil {
		database.anchorFiles.RUnlock()
		return 0, err
	}
	// The newest anchor at or before the moment, or, when its chain does not read back, the one before it: a longer
	// replay, the same rows.
	err = ErrNoAnchor
	var corrupt error
	for position := len(anchors) - 1; position >= 0; position-- {
		if anchors[position].at > through {
			continue
		}
		err = restoreAnchor(anchors, anchors[position], target)
		if !errors.Is(err, ErrCorruptAnchor) {
			break
		}
		corrupt = errors.Join(corrupt, err)
	}
	database.anchorFiles.RUnlock()
	if err != nil {
		return 0, errors.Join(err, corrupt)
	}
	restored, err := sql.Open("sqlite", dataSourceName(target, "_pragma=foreign_keys(0)", "_pragma=journal_mode(WAL)"))
	if err != nil {
		return 0, err
	}
	restored.SetMaxOpenConns(1)
	defer restored.Close()
	var revision int64
	var checksum []byte
	err = restored.QueryRowContext(ctx, `SELECT rev, checksum FROM kitwork_history ORDER BY rev DESC LIMIT 1`).Scan(&revision, &checksum)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	replay := &replayer{target: restored, columns: map[string][]string{}, whole: table}
	tx, err := restored.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	replay.tx = tx
	last := uint64(revision)
	err = readHistory(ctx, database.readers, last, through, func(row historyRow) error {
		if row.revision != last+1 {
			return fmt.Errorf("%w: revision %d follows %d", ErrCorruptHistory, row.revision, last)
		}
		if want := chainChecksum(checksum, row.revision, row.at, row.changes); string(want) != string(row.checksum) {
			return fmt.Errorf("%w: checksum of revision %d does not chain", ErrCorruptHistory, row.revision)
		}
		changes, err := decodeChanges(row.changes)
		if err != nil {
			return fmt.Errorf("revision %d: %w", row.revision, err)
		}
		if err := replay.apply(ctx, changes); err != nil {
			return fmt.Errorf("replay revision %d: %w", row.revision, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO kitwork_history (rev, at, checksum, changes) VALUES (?, ?, ?, ?)`,
			int64(row.revision), row.at, row.checksum, row.changes); err != nil {
			return err
		}
		last, checksum = row.revision, row.checksum
		return nil
	})
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return last, nil
}

type replayer struct {
	target  *sql.DB
	tx      *sql.Tx
	columns map[string][]string
	// whole is the table RestoreTable restores, whose Tracked change the replay may not cross
	whole string
}

func (replay *replayer) tableColumns(ctx context.Context, table string) ([]string, error) {
	if columns, known := replay.columns[table]; known {
		return columns, nil
	}
	rows, err := replay.tx.QueryContext(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("%w: table %q does not exist at this revision", ErrCorruptHistory, table)
	}
	replay.columns[table] = columns
	return columns, nil
}

// afterImages is the row each update of format 3 left, by its table and rowid: format 3 keeps only the columns an
// update changed, so its row as the commit left it is the row as it was before the commit with those columns set.
// Every row is read before any change of the commit runs: a commit that moved a unique value from one row to another
// has the first row's INSERT OR REPLACE delete the second, whose own image is then already in hand.
func (replay *replayer) afterImages(ctx context.Context, changes []Change) (map[rowKey][]any, error) {
	wanted := map[string][]int64{}
	var tables []string
	for _, change := range changes {
		if change.Operation != Update || change.After != nil || change.Light {
			continue // format 1 keeps the whole row after; a light change is not replayed
		}
		if _, seen := wanted[change.Table]; !seen {
			tables = append(tables, change.Table)
		}
		wanted[change.Table] = append(wanted[change.Table], change.RowID)
	}
	images := map[rowKey][]any{}
	for _, table := range tables {
		rowIDs := wanted[table]
		for start := 0; start < len(rowIDs); start += 500 {
			part := rowIDs[start:min(start+500, len(rowIDs))]
			arguments := make([]any, len(part))
			for position, rowID := range part {
				arguments[position] = rowID
			}
			marks := strings.TrimSuffix(strings.Repeat("?, ", len(part)), ", ")
			rows, err := replay.tx.QueryContext(ctx, `SELECT _rowid_, * FROM `+quoteIdentifier(table)+` WHERE _rowid_ IN (`+marks+`)`, arguments...)
			if err != nil {
				return nil, err
			}
			names, err := rows.Columns()
			if err != nil {
				rows.Close()
				return nil, err
			}
			for rows.Next() {
				values := make([]any, len(names))
				pointers := make([]any, len(names))
				for position := range values {
					pointers[position] = &values[position]
				}
				if err := rows.Scan(pointers...); err != nil {
					rows.Close()
					return nil, err
				}
				rowID, _ := values[0].(int64)
				images[rowKey{table, rowID}] = values[1:]
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return nil, err
			}
		}
	}
	for _, change := range changes {
		if change.Operation != Update || change.After != nil {
			continue
		}
		image, found := images[rowKey{change.Table, change.RowID}]
		if !found || len(image) != len(change.Columns) {
			return nil, fmt.Errorf("%w: update of row %d of %q, which the restored copy does not hold as the history says",
				ErrCorruptHistory, change.RowID, change.Table)
		}
		after := append([]any(nil), image...)
		for _, update := range change.Updates {
			after[update.Position] = update.New
		}
		images[rowKey{change.Table, change.RowID}] = after
	}
	return images, nil
}

func (replay *replayer) apply(ctx context.Context, changes []Change) error {
	images, err := replay.afterImages(ctx, changes)
	if err != nil {
		return err
	}
	for _, change := range changes {
		if change.Light {
			// a table followed for its search or its columns copy alone: which row changed, not how; it stays as the
			// anchor holds it, as a table the history does not follow (09/11/2026)
			if replay.whole != "" && strings.EqualFold(change.Table, replay.whole) {
				return fmt.Errorf("%w: the history records which rows of %q changed here, not their values", ErrPastUnreachable, change.Table)
			}
			continue
		}
		if change.Operation == Update && change.After == nil {
			change.After = images[rowKey{change.Table, change.RowID}]
		}
		switch change.Operation {
		case Statement:
			if _, err := replay.tx.ExecContext(ctx, change.Statement); err != nil {
				return err
			}
			clear(replay.columns)
		case Barrier:
			// the anchor Maintain writes at a Barrier starts every restore after it
			return fmt.Errorf("%w: the file changed here outside the history (%s) and no anchor was written at it", ErrNoAnchor, change.Statement)
		case Tracked:
			// the history records a table from here: nothing to replay, unless it is the table RestoreTable restores
			if replay.whole != "" && strings.EqualFold(change.Table, replay.whole) {
				return fmt.Errorf("%w: the history records %q from here only, after the anchor the restore starts from", ErrPastUnreachable, change.Table)
			}
		case Delete:
			if _, err := replay.tx.ExecContext(ctx, `DELETE FROM `+quoteIdentifier(change.Table)+` WHERE _rowid_ = ?`, change.RowID); err != nil {
				return err
			}
		case Insert, Update:
			columns, err := replay.tableColumns(ctx, change.Table)
			if err != nil {
				return err
			}
			if len(columns) != len(change.Columns) {
				return fmt.Errorf("%w: table %q has %d columns, the change %d", ErrCorruptHistory, change.Table, len(columns), len(change.Columns))
			}
			names := make([]string, len(change.Columns))
			for position, column := range change.Columns {
				names[position] = quoteIdentifier(column)
			}
			arguments := append([]any{change.RowID}, change.After...)
			marks := strings.TrimSuffix(strings.Repeat("?, ", len(arguments)), ", ")
			query := `INSERT OR REPLACE INTO ` + quoteIdentifier(change.Table) + ` (_rowid_, ` + strings.Join(names, ", ") + `) VALUES (` + marks + `)`
			if _, err := replay.tx.ExecContext(ctx, query, arguments...); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: operation %d", ErrCorruptHistory, change.Operation)
		}
	}
	return nil
}

func copyFile(source, target string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(output, input); err != nil {
		_ = output.Close()
		_ = os.Remove(target)
		return err
	}
	if err := output.Sync(); err != nil {
		_ = output.Close()
		return err
	}
	return output.Close()
}
