package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// anchorDirectory holds full copies of the database at chosen revisions,
// named <revision>-<moment>.db: zero-padded so they sort.
func (database *Database) anchorDirectory() string { return database.path + ".history" }

type anchor struct {
	path     string
	revision uint64
	at       int64
}

// Anchor writes a full copy of the database as of the latest commit
// (VACUUM INTO on a reader) and returns its revision. Restore starts from
// the newest anchor at or before the moment it is asked for, so anchors bound
// how much history a restore replays.
func (database *Database) Anchor(ctx context.Context) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	directory := database.anchorDirectory()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return 0, err
	}
	temporary := filepath.Join(directory, fmt.Sprintf("anchor-%d.tmp", time.Now().UnixNano()))
	// A reader cannot run VACUUM INTO: query_only refuses even a write to another file.
	source, err := sql.Open("sqlite", dataSourceName(database.path, "mode=ro"))
	if err != nil {
		return 0, err
	}
	defer source.Close()
	if _, err := source.ExecContext(ctx, `VACUUM INTO ?`, filepath.ToSlash(temporary)); err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	copied, err := sql.Open("sqlite", dataSourceName(temporary, "mode=ro"))
	if err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	var revision, at int64
	err = copied.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0), coalesce(max(at), 0) FROM kitwork_history`).Scan(&revision, &at)
	_ = copied.Close()
	if err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	final := filepath.Join(directory, fmt.Sprintf("%020d-%020d.db", revision, at))
	if _, err := os.Stat(final); err == nil {
		_ = os.Remove(temporary)
		return uint64(revision), nil
	}
	if err := os.Rename(temporary, final); err != nil {
		_ = os.Remove(temporary)
		return 0, err
	}
	return uint64(revision), nil
}

func (database *Database) anchors() ([]anchor, error) {
	entries, err := os.ReadDir(database.anchorDirectory())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var anchors []anchor
	for _, entry := range entries {
		var revision uint64
		var at int64
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		if _, err := fmt.Sscanf(entry.Name(), "%020d-%020d.db", &revision, &at); err != nil {
			continue
		}
		anchors = append(anchors, anchor{path: filepath.Join(database.anchorDirectory(), entry.Name()), revision: revision, at: at})
	}
	sort.Slice(anchors, func(i, j int) bool { return anchors[i].revision < anchors[j].revision })
	return anchors, nil
}

// Restore writes to target the database as it was at moment: the newest anchor
// at or before it, then every later commit whose moment is not after it. The
// replayed history is checked link by link against the anchor's own last
// checksum. It returns the restored revision. target must not exist.
func (database *Database) Restore(ctx context.Context, moment time.Time, target string) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if _, err := os.Stat(target); err == nil {
		return 0, fmt.Errorf("kitdb: restore target %q exists", target)
	}
	anchors, err := database.anchors()
	if err != nil {
		return 0, err
	}
	through := moment.UTC().UnixNano()
	var start *anchor
	for position := range anchors {
		if anchors[position].at <= through {
			start = &anchors[position]
		}
	}
	if start == nil {
		return 0, ErrNoAnchor
	}
	if err := copyFile(start.path, target); err != nil {
		return 0, err
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
	replay := &replayer{target: restored, columns: map[string][]string{}}
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

func (replay *replayer) apply(ctx context.Context, changes []Change) error {
	for _, change := range changes {
		switch change.Operation {
		case Statement:
			if _, err := replay.tx.ExecContext(ctx, change.Statement); err != nil {
				return err
			}
			clear(replay.columns)
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
