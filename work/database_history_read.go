package work

import (
	"context"
	"fmt"
	"sort"
	"strings"

	kitdbsqlite "github.com/kitwork/engine/kitdb/sqlite"
	"github.com/kitwork/engine/utilities/query"
	"github.com/kitwork/engine/value"
)

// history() (KITWORK_DATABASE.md mục 2, "Thời điểm và lịch sử"; KITWORK_KITDB.md mục 5, 03/11/2026): the changes of
// the rows of a table its condition keeps, read from the file's kitwork_history, oldest first, each
// { at, before, after, by }: at the moment of the commit in milliseconds, before and after the row as it was and as
// the commit left it (null for an insert's before and a delete's after), by the authenticated subject the commit
// names, null for none. A change is kept when the row before it or after it meets the condition, so a row since
// deleted has its history too. The condition runs as SQL on the images, in batches, so it means what it means in
// list: SQLite's comparison, a decimal against its stored integer.
//
// historyLimit bounds the changes one history() gives: past it, the error says to narrow the condition.
const historyLimit = 10_000

// historyImage is one row image of a change, by the change's index.
type historyImage struct {
	change int
	values map[string]any
}

func (m *ManagedDatabase) runHistory(prepared *preparedStatement, binder *statementBinder) value.Value {
	s := prepared.statement
	table, failure := m.table(s.Table)
	if table == nil {
		return failure
	}
	if !table.keepsHistory() {
		return value.InvalidFailure(value.UnsupportedCode, fmt.Sprintf("history() đọc lịch sử của database, %s không giữ lịch sử", s.Table))
	}
	if failure, ok := table.ready(); !ok {
		return failure
	}
	predicate, truth, failure := binder.predicate(s.Where)
	if failure.Failed() {
		return failure
	}
	if truth == bindFalse {
		return value.New([]value.Value{})
	}
	if predicate != nil {
		if message := sqliteWritePredicate(table.table, table.columns, predicate); message != "" {
			return value.InvalidFailure(value.InvalidArgumentCode, message)
		}
	}
	db := table.source().db()
	if db == nil {
		return value.InvalidFailure(value.UnavailableCode, "history(): connection unavailable")
	}
	ctx := table.writeContext()
	var changes []kitdbsqlite.Change
	var moments []int64
	err := kitdbsqlite.ReadChanges(ctx, db, 0, func(commit kitdbsqlite.Commit) error {
		for _, change := range commit.Changes {
			switch change.Operation {
			case kitdbsqlite.Insert, kitdbsqlite.Update, kitdbsqlite.Delete:
				if strings.EqualFold(change.Table, table.table) {
					changes = append(changes, change)
					moments = append(moments, commit.At.UnixMilli())
				}
			}
		}
		return nil
	})
	if err != nil {
		return query.DatabaseFailure("history()", err)
	}
	images := make([][2]map[string]any, len(changes))
	var all []historyImage
	for index, change := range changes {
		if change.Operation != kitdbsqlite.Insert {
			images[index][0] = namedImage(change.Columns, change.Before)
		}
		if change.Operation != kitdbsqlite.Delete {
			images[index][1] = namedImage(change.Columns, change.After)
		}
		for _, image := range images[index] {
			if image != nil {
				all = append(all, historyImage{change: index, values: image})
			}
		}
	}
	kept := map[int]bool{}
	if truth == bindTrue || predicate == nil {
		for index := range changes {
			kept[index] = true
		}
	} else if err := matchImages(ctx, table, *predicate, all, kept); err != nil {
		return query.DatabaseFailure("history()", err)
	}
	if len(kept) > historyLimit {
		return value.InvalidFailure(value.InvalidArgumentCode, fmt.Sprintf("history() của %s có %d thay đổi, quá %d: thu hẹp điều kiện", table.table, len(kept), historyLimit))
	}
	indexes := make([]int, 0, len(kept))
	for index := range kept {
		indexes = append(indexes, index)
	}
	sort.Ints(indexes)
	out := make([]value.Value, 0, len(indexes))
	for _, index := range indexes {
		entry := map[string]value.Value{"at": value.New(moments[index]), "before": value.NewNil(), "after": value.NewNil(), "by": value.NewNil()}
		for position, name := range []string{"before", "after"} {
			if image := images[index][position]; image != nil {
				row := make(map[string]value.Value, len(image))
				for column, item := range image {
					if _, declared := table.columns[column]; declared {
						row[column] = value.New(item)
					}
				}
				entry[name] = sqliteReadResult(table.columns, coerceResult(table.columns, value.New(row)))
			}
		}
		if by := changes[index].By; by != "" {
			entry["by"] = value.New(by)
		}
		out = append(out, value.New(entry))
	}
	return value.New(out)
}

// namedImage is a row image by its columns' names; nil when the change does not name them (a delete written before
// 03/11/2026).
func namedImage(columns []string, values []any) map[string]any {
	if columns == nil || len(columns) != len(values) {
		return nil
	}
	image := make(map[string]any, len(columns))
	for position, column := range columns {
		image[column] = values[position]
	}
	return image
}

// matchImages runs the condition on the images, in batches of one VALUES each, and marks the changes whose image
// meets it.
func matchImages(ctx context.Context, table *SchemaTable, predicate query.Predicate, images []historyImage, kept map[int]bool) error {
	columns := orderedColumns(table.columns)
	quoted := make([]string, len(columns))
	for position, column := range columns {
		quoted[position] = quoteSearchIdentifier(column)
	}
	db := table.source().db()
	batch := max(1, 30_000/(len(columns)+1)-1)
	for start := 0; start < len(images); start += batch {
		part := images[start:min(start+batch, len(images))]
		var args []any
		rows := make([]string, len(part))
		for row, image := range part {
			marks := make([]string, 0, len(columns)+1)
			args = append(args, image.change)
			marks = append(marks, fmt.Sprintf("$%d", len(args)))
			for _, column := range columns {
				args = append(args, image.values[column])
				marks = append(marks, fmt.Sprintf("$%d", len(args)))
			}
			rows[row] = "(" + strings.Join(marks, ", ") + ")"
		}
		condition, conditionArgs := query.PredicateSQL(predicate, len(args)+1)
		args = append(args, conditionArgs...)
		text := "WITH images (kitwork_change, " + strings.Join(quoted, ", ") + ") AS (VALUES " + strings.Join(rows, ", ") +
			") SELECT DISTINCT kitwork_change FROM images WHERE " + condition
		result, err := db.QueryContext(ctx, text, args...)
		if err != nil {
			return err
		}
		for result.Next() {
			var index int
			if err := result.Scan(&index); err != nil {
				result.Close()
				return err
			}
			kept[index] = true
		}
		err = result.Err()
		result.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
