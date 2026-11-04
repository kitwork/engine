package work

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// at() (KITWORK_DATABASE.md mục 2, "Thời điểm và lịch sử"; KITWORK_KITDB.md mục 5): a read as the database was at
// a moment. Since 04/11/2026 it goes backward: the live file, with every row the commits after the moment changed put
// back as it was (atBackward), at a cost that grows with the changes since, not with the file. A moment the history
// after it cannot be undone to (the schema moved since, or more than atBackwardRows rows changed) is read as before
// 04/11: the file is restored from its history (the newest anchor at or before the moment, then every commit not
// after it) into <file>.at/<revision>.db once, and the statement reads that copy, read-only; the newest atKept
// copies are kept. A moment before the file's history began has no copy, one before what the retention kept
// (app.history({ days })) has no history left, and one before the table was made has no table: the error says so.

const atKept = 8

// atMoment reads the moment of at(): a time, an ISO 8601 text with its offset, or milliseconds since 1970.
func atMoment(item value.Value, site string) (time.Time, value.Value) {
	switch item.K {
	case value.Time:
		if moment, ok := item.V.(time.Time); ok {
			return moment, value.Value{}
		}
	case value.Number:
		return time.UnixMilli(int64(item.N)), value.Value{}
	case value.String:
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z07:00"} {
			if moment, err := time.Parse(layout, strings.TrimSpace(item.String())); err == nil {
				return moment, value.Value{}
			}
		}
	}
	return time.Time{}, value.InvalidFailure(value.InvalidArgumentCode,
		fmt.Sprintf("at(…) ở %s nhận một thời điểm: chữ ISO có múi giờ (\"2026-10-16T23:00:00+07:00\"), số mili giây hay một Date", site))
}

// atBackwardRows bounds the rows a read going backward puts back as they were; past it, a restored copy is read.
const atBackwardRows = 1_000_000

// errAtRestored is atBackward's word that the moment is read from a copy restored from the history instead: the
// schema moved after it, or more rows changed since than atBackwardRows.
var errAtRestored = errors.New("at(): read from a restored copy")

// atTable is table read as the file was at moment, with its own connection, closed by close. It goes backward from
// the live file (atBackward, 04/11/2026); a moment the history after it cannot be undone to is read from a copy of the
// file restored from the history (atRestored).
func (t *SchemaTable) atTable(moment time.Time) (*SchemaTable, func(), value.Value) {
	database, err := t.tenant.historyDatabase(t.sqliteFile())
	if err != nil {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, err.Error())
	}
	ctx := t.writeContext()
	revision, err := database.RevisionAt(ctx, moment)
	if err != nil {
		return nil, nil, atFailure(ctx, database, moment, err)
	}
	past, release, err := t.atBackward(ctx, revision)
	if err == nil {
		return past, release, value.Value{}
	}
	if !errors.Is(err, errAtRestored) {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("at(%s): %v", moment.Format(time.RFC3339), err))
	}
	return t.atRestored(ctx, database, moment)
}

// atBackward is table as it was at revision, read on the live file in one read transaction of a connection of its
// own: every row a commit after revision changed is put back as it was then (the Before of its first change after
// it; no row when that change inserted it) in a temporary table, and the statement reads a temporary view of the
// live rows those commits did not touch with them. The temporary table has the columns' declared types, so SQLite
// flattens the view and an ORDER BY with a LIMIT merges the two sides through their indexes (KITWORK_KITDB.md mục 5;
// engine/benchmarks/kitdbsqlite RESULTS_2026-11-04.md). It is errAtRestored when the history after revision holds a
// change of the schema, a row with no image of before, or more than atBackwardRows rows.
func (t *SchemaTable) atBackward(ctx context.Context, revision uint64) (*SchemaTable, func(), error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(t.sqliteFile())+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		_ = db.Close()
		return nil, nil, err
	}
	release := func() {
		// the rollback ends the read and takes the temporary tables and view with it
		_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		_ = conn.Close()
		_ = db.Close()
	}
	relation, err := t.pastRelation(ctx, conn, revision)
	if err != nil {
		release()
		return nil, nil, err
	}
	past := *t
	past.q, past.past = nil, &pastTable{executor: conn, relation: relation}
	return &past, release, nil
}

// pastRelation builds, inside the read transaction it opens on conn, what atBackward reads, and names it: the table
// itself when no commit after revision changed it, the temporary view kitwork_at otherwise.
func (t *SchemaTable) pastRelation(ctx context.Context, conn *sql.Conn, revision uint64) (string, error) {
	if _, err := conn.ExecContext(ctx, "BEGIN"); err != nil {
		return "", err
	}
	var names, types []string
	rows, err := conn.QueryContext(ctx, `SELECT name, type, hidden FROM pragma_table_xinfo(?) ORDER BY cid`, t.table)
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
			return "", errAtRestored // a generated column is not in the history's images
		}
		names, types = append(names, name), append(types, kind)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", errAtRestored // the table is not in the file now
	}
	position := make(map[string]int, len(names))
	for index, name := range names {
		position[name] = index
	}
	changed := map[int64]bool{}
	var before [][]any
	err = kitdbsqlite.ReadChanges(ctx, conn, revision, func(commit kitdbsqlite.Commit) error {
		for _, change := range commit.Changes {
			if change.Operation == kitdbsqlite.Statement || change.Operation == kitdbsqlite.Barrier {
				return errAtRestored
			}
			// only a row's first change after the moment says what it was then
			if change.Table != t.table || changed[change.RowID] {
				continue
			}
			changed[change.RowID] = true
			if len(changed) > atBackwardRows {
				return errAtRestored
			}
			if change.Operation == kitdbsqlite.Insert {
				continue // the row was not there yet
			}
			if len(change.Columns) != len(names) || len(change.Before) != len(names) {
				return errAtRestored // a row deleted before deletes carried their columns (03/11/2026)
			}
			row := make([]any, len(names))
			for index, column := range change.Columns {
				at, known := position[column]
				if !known {
					return errAtRestored
				}
				row[at] = change.Before[index]
			}
			before = append(before, row)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if len(changed) == 0 {
		return t.table, nil
	}
	columns := make([]string, len(names))
	declared := make([]string, len(names))
	for index, name := range names {
		columns[index] = quoteIdentifier(name)
		declared[index] = columns[index] + " " + types[index]
	}
	list := strings.Join(columns, ", ")
	statements := []string{
		`CREATE TEMP TABLE kitwork_at_changed (rid INTEGER PRIMARY KEY)`,
		`CREATE TEMP TABLE kitwork_at_before (` + strings.Join(declared, ", ") + `)`,
	}
	for _, statement := range statements {
		if _, err := conn.ExecContext(ctx, statement); err != nil {
			return "", err
		}
	}
	insertChanged, err := conn.PrepareContext(ctx, `INSERT INTO temp.kitwork_at_changed (rid) VALUES (?)`)
	if err != nil {
		return "", err
	}
	defer insertChanged.Close()
	for rowID := range changed {
		if _, err := insertChanged.ExecContext(ctx, rowID); err != nil {
			return "", err
		}
	}
	insertBefore, err := conn.PrepareContext(ctx, `INSERT INTO temp.kitwork_at_before (`+list+`) VALUES (`+
		strings.TrimSuffix(strings.Repeat("?, ", len(names)), ", ")+`)`)
	if err != nil {
		return "", err
	}
	defer insertBefore.Close()
	for _, row := range before {
		if _, err := insertBefore.ExecContext(ctx, row...); err != nil {
			return "", err
		}
	}
	// the rows as they were get the table's indexes, so a read by key or in key order finds them as on the table
	indexes, err := pastIndexes(ctx, conn, t.table)
	if err != nil {
		return "", err
	}
	for number, keys := range indexes {
		quoted := make([]string, len(keys))
		for index, key := range keys {
			quoted[index] = quoteIdentifier(key)
		}
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`CREATE INDEX temp.kitwork_at_index_%d ON kitwork_at_before (%s)`,
			number, strings.Join(quoted, ", "))); err != nil {
			return "", err
		}
	}
	if _, err := conn.ExecContext(ctx, `CREATE TEMP VIEW kitwork_at AS SELECT `+list+` FROM main.`+quoteIdentifier(t.table)+
		` WHERE _rowid_ NOT IN (SELECT rid FROM temp.kitwork_at_changed) UNION ALL SELECT `+list+` FROM temp.kitwork_at_before`); err != nil {
		return "", err
	}
	return "kitwork_at", nil
}

// quoteIdentifier is name as an SQL identifier.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// pastIndexes lists the key columns of each index of table, one on an expression left out.
func pastIndexes(ctx context.Context, conn *sql.Conn, table string) ([][]string, error) {
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

// pastTable is what a table as it was at a moment reads through: the relation the statement names, on the
// connection that holds it.
type pastTable struct {
	executor query.Executor
	relation string
}

// atFailure is the error of at(moment) for err of the history: the moment is before the history kept, or before the
// file's history began, or the history could not be read.
func atFailure(ctx context.Context, database *kitdbsqlite.Database, moment time.Time, err error) value.Value {
	if errors.Is(err, kitdbsqlite.ErrPruned) {
		kept, _, keptErr := database.KeptFrom(ctx)
		if keptErr != nil {
			return value.InvalidFailure(value.DatabaseCode, keptErr.Error())
		}
		return value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("at(%s): lịch sử của database chỉ còn từ %s; engine giữ %d ngày (app.history({ days }) trong application.work, mặc định %d)",
				moment.Format(time.RFC3339), kept.In(moment.Location()).Format(time.RFC3339), HistoryDays(), historyDaysDefault))
	}
	if errors.Is(err, kitdbsqlite.ErrNoAnchor) {
		return value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("at(%s): database chưa giữ lịch sử từ lúc đó (%v)", moment.Format(time.RFC3339), err))
	}
	return value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("at(%s): %v", moment.Format(time.RFC3339), err))
}

// atRestored is table read from a copy of the file restored as it was at moment, with its own connection, closed by
// close: the moments atBackward cannot reach.
func (t *SchemaTable) atRestored(ctx context.Context, database *kitdbsqlite.Database, moment time.Time) (*SchemaTable, func(), value.Value) {
	target, err := atFile(ctx, database, moment)
	if err != nil {
		return nil, nil, atFailure(ctx, database, moment, err)
	}
	connection, err := sql.Open("sqlite", "file:"+filepath.ToSlash(target)+"?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, err.Error())
	}
	var tables int
	if err := connection.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, t.table).Scan(&tables); err != nil || tables == 0 {
		_ = connection.Close()
		if err != nil {
			return nil, nil, value.InvalidFailure(value.DatabaseCode, err.Error())
		}
		return nil, nil, value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("at(%s): lúc đó database chưa có bảng %s", moment.Format(time.RFC3339), t.table))
	}
	frozen := *t
	frozen.q, frozen.past = nil, &pastTable{executor: connection, relation: t.table}
	return &frozen, func() { _ = connection.Close() }, value.Value{}
}

// atFile is the restored copy of the file at moment, made once per revision.
func atFile(ctx context.Context, database *kitdbsqlite.Database, moment time.Time) (string, error) {
	revision, err := database.RevisionAt(ctx, moment)
	if err != nil {
		return "", err
	}
	directory := database.Path() + ".at"
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(directory, fmt.Sprintf("%020d.db", revision))
	if _, err := os.Stat(target); err == nil {
		return target, nil
	}
	temporary := filepath.Join(directory, fmt.Sprintf("%020d.%d.tmp", revision, time.Now().UnixNano()))
	if _, err := database.Restore(ctx, moment, temporary); err != nil {
		_ = os.Remove(temporary)
		return "", err
	}
	if err := os.Rename(temporary, target); err != nil {
		_ = os.Remove(temporary)
		if _, statErr := os.Stat(target); statErr != nil {
			return "", err
		}
	}
	pruneAtFiles(directory, target)
	return target, nil
}

// dropAtFilesBefore takes out of directory the copies of revisions before revision: the history no longer reaches
// them, so no at() reads them again, and what the retention let go of does not stay on in a copy.
func dropAtFilesBefore(directory string, revision uint64) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		var copied uint64
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		if _, err := fmt.Sscanf(entry.Name(), "%020d.db", &copied); err == nil && copied < revision {
			_ = os.Remove(filepath.Join(directory, entry.Name()))
		}
	}
}

// pruneAtFiles keeps the newest atKept copies of directory, and the one just made.
func pruneAtFiles(directory, keep string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	var copies []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".db") {
			copies = append(copies, filepath.Join(directory, entry.Name()))
		}
	}
	sort.Strings(copies)
	for len(copies) > atKept {
		if copies[0] != keep {
			_ = os.Remove(copies[0])
		}
		copies = copies[1:]
	}
}
