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
	"github.com/kitwork/engine/value"
)

// at() (KITWORK_DATABASE.md mục 2, "Thời điểm và lịch sử"; KITWORK_KITDB.md mục 5, 03/11/2026): a read as the
// database was at a moment. The file is restored from its history (the newest anchor at or before the moment, then
// every commit not after it) into <file>.at/<revision>.db once, and the statement reads that copy, read-only; the
// next statement at a moment of the same revision reads the same copy. The newest atKept copies are kept. A moment
// before the file's history began has no copy, one before what the retention kept (app.history({ days })) has no
// history left, and one before the table was made has no table: the error says so.

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

// atTable is table read from the file as it was at moment: a copy of the schema table on the restored file, with its
// own connection, closed by close.
func (t *SchemaTable) atTable(moment time.Time) (*SchemaTable, func(), value.Value) {
	database, err := t.tenant.historyDatabase(t.sqliteFile())
	if err != nil {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, err.Error())
	}
	ctx := t.writeContext()
	target, err := atFile(ctx, database, moment)
	if errors.Is(err, kitdbsqlite.ErrPruned) {
		kept, _, keptErr := database.KeptFrom(ctx)
		if keptErr != nil {
			return nil, nil, value.InvalidFailure(value.DatabaseCode, keptErr.Error())
		}
		return nil, nil, value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("at(%s): lịch sử của database chỉ còn từ %s; engine giữ %d ngày (app.history({ days }) trong application.work, mặc định %d)",
				moment.Format(time.RFC3339), kept.In(moment.Location()).Format(time.RFC3339), HistoryDays(), historyDaysDefault))
	}
	if errors.Is(err, kitdbsqlite.ErrNoAnchor) {
		return nil, nil, value.InvalidFailure(value.InvalidArgumentCode,
			fmt.Sprintf("at(%s): database chưa giữ lịch sử từ lúc đó (%v)", moment.Format(time.RFC3339), err))
	}
	if err != nil {
		return nil, nil, value.InvalidFailure(value.DatabaseCode, fmt.Sprintf("at(%s): %v", moment.Format(time.RFC3339), err))
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
	frozen.q, frozen.frozenDB = nil, connection
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
