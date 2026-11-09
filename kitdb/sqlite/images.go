package sqlite

import "fmt"

// LiveRows reads rows of table as they are now, by rowid: the table's columns in its order, and each row's values in
// that order. A rowid with no row is left out.
type LiveRows func(table string, rowIDs []int64) (columns []string, rows map[int64][]any, err error)

// WholeImages makes Before and After whole for every update of changes that keeps only its changed columns (format 3,
// After nil): each such row is walked back from the row as it is now (live) or from a later delete's image, every
// update before it setting its columns' old values. changes are one table's, oldest first, and for every row they touch
// they hold every change from the row's first change among them up to now; inserts and deletes are whole already, and
// so are format 1's updates. The images follow each change's own Columns, so a walk crosses a column added since by
// name. It is ErrCorruptHistory when the history and the rows disagree: an update of a row gone after it, or a column
// an older change names that no newer image holds.
func WholeImages(changes []Change, table string, live LiveRows) error {
	order := map[int64][]int{}
	var rowIDs []int64
	for index, change := range changes {
		if change.Operation != Insert && change.Operation != Update && change.Operation != Delete {
			continue
		}
		if change.Light {
			return fmt.Errorf("%w: the history records which row %d of %q changed, not its values", ErrPastUnreachable, change.RowID, table)
		}
		if _, seen := order[change.RowID]; !seen {
			rowIDs = append(rowIDs, change.RowID)
		}
		order[change.RowID] = append(order[change.RowID], index)
	}
	// the rows a walk starts from the row as it is now: some update of theirs is partial, and their last change left
	// a row
	var wanted []int64
	for _, rowID := range rowIDs {
		indexes := order[rowID]
		partial := false
		for _, index := range indexes {
			partial = partial || changes[index].Operation == Update && changes[index].After == nil
		}
		if partial && changes[indexes[len(indexes)-1]].Operation != Delete {
			wanted = append(wanted, rowID)
		}
	}
	var liveColumns []string
	var liveRows map[int64][]any
	if len(wanted) > 0 {
		var err error
		if liveColumns, liveRows, err = live(table, wanted); err != nil {
			return err
		}
	}
	for _, rowID := range rowIDs {
		indexes := order[rowID]
		partial := false
		for _, index := range indexes {
			partial = partial || changes[index].Operation == Update && changes[index].After == nil
		}
		if !partial {
			continue
		}
		// current is the row after the change the walk is at: nil when there is none
		var current *rowImage
		if last := changes[indexes[len(indexes)-1]]; last.Operation != Delete {
			values, found := liveRows[rowID]
			if !found {
				return fmt.Errorf("%w: row %d of %q is changed last by an %s, and the table does not hold it", ErrCorruptHistory,
					rowID, table, operationName(last.Operation))
			}
			current = &rowImage{columns: liveColumns, values: values}
		}
		for position := len(indexes) - 1; position >= 0; position-- {
			change := &changes[indexes[position]]
			switch change.Operation {
			case Insert:
				current = nil
			case Delete:
				current = &rowImage{columns: change.Columns, values: change.Before}
			case Update:
				if change.After != nil {
					current = &rowImage{columns: change.Columns, values: change.Before}
					continue
				}
				if current == nil {
					return fmt.Errorf("%w: row %d of %q is updated, and gone after the update", ErrCorruptHistory, rowID, table)
				}
				after, err := current.as(change.Columns, table)
				if err != nil {
					return err
				}
				before := append([]any(nil), after...)
				for _, update := range change.Updates {
					before[update.Position] = update.Old
				}
				change.After, change.Before = after, before
				current = &rowImage{columns: change.Columns, values: before}
			}
		}
	}
	return nil
}

// rowImage is a row's values with the columns they follow.
type rowImage struct {
	columns []string
	values  []any
}

// as is the image's values in the order of columns, by name.
func (image *rowImage) as(columns []string, table string) ([]any, error) {
	if len(columns) == len(image.columns) {
		same := true
		for position := range columns {
			same = same && columns[position] == image.columns[position]
		}
		if same {
			return append([]any(nil), image.values...), nil
		}
	}
	positions := make(map[string]int, len(image.columns))
	for position, column := range image.columns {
		positions[column] = position
	}
	values := make([]any, len(columns))
	for position, column := range columns {
		at, found := positions[column]
		if !found {
			return nil, fmt.Errorf("%w: column %q of %q is in an older change and in no image after it", ErrCorruptHistory, column, table)
		}
		values[position] = image.values[at]
	}
	return values, nil
}

func operationName(operation Operation) string {
	switch operation {
	case Insert:
		return "insert"
	case Update:
		return "update"
	case Delete:
		return "delete"
	}
	return fmt.Sprintf("operation %d", operation)
}
