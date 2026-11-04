package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"time"
)

const (
	historyTable  = "kitwork_history"
	historySchema = `CREATE TABLE IF NOT EXISTS kitwork_history (
	rev INTEGER PRIMARY KEY,
	at INTEGER NOT NULL,
	checksum BLOB NOT NULL,
	changes BLOB NOT NULL
) STRICT`
	changesFormat = 1
	// barrierFormat is format 1 with Barrier among the operations: a commit holding one is written in it, so a
	// reader of format 1 alone refuses it rather than skipping it.
	barrierFormat = 2
	// columnsFormat is format 3 (KITWORK_KITDB.md mục 5, 04/11/2026): an update keeps only the columns it changed,
	// each with its old and new value, and the new values of the columns kept whole (a searchable column) when one of
	// them changed; an insert keeps its image after, a delete its image before, as in format 1.
	columnsFormat = 3
	// columnsBarrierFormat is format 3 with Barrier among the operations, as format 2 is format 1's: Open finds a
	// commit holding one by its first byte alone (Database.unanchored).
	columnsBarrierFormat = 4

	// the kinds of an update's column entry in format 3
	columnChanged = 1 // the old value, then the new
	columnKept    = 2 // the new value alone: the column did not change, it is kept whole for its table's readers
)

// Operation is what one change did to a row, or a schema statement.
type Operation byte

const (
	Insert    Operation = 1
	Update    Operation = 2
	Delete    Operation = 3
	Statement Operation = 4
	// Barrier is a commit of Maintain whose work moved the schema: the history did not follow that work, so replay
	// never crosses it, and a restore after it starts from the anchor written at it. Statement says what moved.
	Barrier Operation = 5
)

// Change is the net effect of one commit on one row: the row as it was before
// the commit (Update, Delete) and as the commit left it (Insert, Update). A
// row changed several times in one commit appears once. A schema statement is
// kept as written and replayed as written.
//
// After is read back from the table when the commit closes and is exact;
// Columns names its values. Before comes from SQLite's pre-update hook, whose
// TEXT values stop at the first NUL character, which TEXT never holds.
//
// Updates is an update's columns: in format 3 the ones it changed, and the
// ones kept whole when one of those changed (Old equal to New), with Before
// and After nil; read from format 1, every column, with Before and After
// whole. A reader of an update reads Updates alone, whatever the format.
type Change struct {
	Operation Operation
	Table     string
	RowID     int64
	Columns   []string
	Before    []any
	After     []any
	Updates   []ColumnUpdate
	Statement string
	By        string
}

// ColumnUpdate is one column of an update: its position in Columns, the value
// the row held before the commit and the value the commit left.
type ColumnUpdate struct {
	Position int
	Old, New any
}

// Commit is one revision of the history.
type Commit struct {
	Revision uint64
	At       time.Time
	Changes  []Change
}

func chainChecksum(previous []byte, revision uint64, at int64, changes []byte) []byte {
	hash := sha256.New()
	hash.Write(previous)
	var numbers [16]byte
	binary.BigEndian.PutUint64(numbers[:8], revision)
	binary.BigEndian.PutUint64(numbers[8:], uint64(at))
	hash.Write(numbers[:])
	hash.Write(changes)
	return hash.Sum(nil)
}

// encodeColumns writes format 3: format 1's name table and columns of every table the commit changed, then each
// change, an update as its column entries alone (columnChanged with the old and the new value, columnKept with the new
// value alone), in the order of their positions.
func encodeColumns(changes []Change) ([]byte, error) {
	names := map[string]uint64{}
	var ordered []string
	name := func(value string) uint64 {
		if index, known := names[value]; known {
			return index
		}
		names[value] = uint64(len(ordered))
		ordered = append(ordered, value)
		return names[value]
	}
	var tables []string
	columns := map[string][]string{}
	for _, change := range changes {
		if change.Operation != Insert && change.Operation != Update && change.Operation != Delete {
			continue
		}
		known, seen := columns[change.Table]
		if !seen {
			columns[change.Table] = change.Columns
			tables = append(tables, change.Table)
		} else if len(known) != len(change.Columns) {
			return nil, fmt.Errorf("kitdb: table %q changed its columns inside one commit", change.Table)
		}
		switch change.Operation {
		case Insert:
			if len(change.After) != len(change.Columns) {
				return nil, fmt.Errorf("kitdb: insert into %q names %d columns for %d values", change.Table, len(change.Columns), len(change.After))
			}
		case Delete:
			if len(change.Before) != len(change.Columns) {
				return nil, fmt.Errorf("kitdb: delete from %q names %d columns for %d values", change.Table, len(change.Columns), len(change.Before))
			}
		case Update:
			last := -1
			for _, update := range change.Updates {
				if update.Position <= last || update.Position >= len(change.Columns) {
					return nil, fmt.Errorf("kitdb: update of %q has column %d out of order or past its %d columns", change.Table, update.Position, len(change.Columns))
				}
				last = update.Position
			}
		}
	}
	var body []byte
	body = binary.AppendUvarint(body, uint64(len(tables)))
	for _, table := range tables {
		body = binary.AppendUvarint(body, name(table))
		body = binary.AppendUvarint(body, uint64(len(columns[table])))
		for _, column := range columns[table] {
			body = binary.AppendUvarint(body, name(column))
		}
	}
	body = binary.AppendUvarint(body, uint64(len(changes)))
	var err error
	for _, change := range changes {
		body = append(body, byte(change.Operation))
		body = binary.AppendUvarint(body, name(change.Table))
		body = binary.AppendUvarint(body, name(change.By))
		switch change.Operation {
		case Statement, Barrier:
			body = binary.AppendUvarint(body, uint64(len(change.Statement)))
			body = append(body, change.Statement...)
		case Insert:
			body = binary.AppendVarint(body, change.RowID)
			body, err = appendValues(body, change.After)
		case Delete:
			body = binary.AppendVarint(body, change.RowID)
			body, err = appendValues(body, change.Before)
		case Update:
			body = binary.AppendVarint(body, change.RowID)
			body = binary.AppendUvarint(body, uint64(len(change.Updates)))
			for _, update := range change.Updates {
				if err != nil {
					break
				}
				body = binary.AppendUvarint(body, uint64(update.Position))
				if sameValue(update.Old, update.New) {
					body = append(body, columnKept)
				} else {
					body = append(body, columnChanged)
					body, err = appendValue(body, update.Old)
				}
				if err == nil {
					body, err = appendValue(body, update.New)
				}
			}
		default:
			return nil, fmt.Errorf("kitdb: unknown change operation %d", change.Operation)
		}
		if err != nil {
			return nil, err
		}
	}
	format := byte(columnsFormat)
	for _, change := range changes {
		if change.Operation == Barrier {
			format = columnsBarrierFormat
		}
	}
	encoded := []byte{format}
	encoded = binary.AppendUvarint(encoded, uint64(len(ordered)))
	for _, value := range ordered {
		encoded = binary.AppendUvarint(encoded, uint64(len(value)))
		encoded = append(encoded, value...)
	}
	return append(encoded, body...), nil
}

// sameValue reports two values of a row the same: the same type and the same bytes, as SQLite stores them.
func sameValue(left, right any) bool {
	switch typed := left.(type) {
	case nil:
		return right == nil
	case int64:
		other, ok := right.(int64)
		return ok && typed == other
	case float64:
		other, ok := right.(float64)
		return ok && math.Float64bits(typed) == math.Float64bits(other)
	case string:
		other, ok := right.(string)
		return ok && typed == other
	case []byte:
		other, ok := right.([]byte)
		return ok && string(typed) == string(other)
	}
	return false
}

func appendValues(buffer []byte, values []any) ([]byte, error) {
	buffer = binary.AppendUvarint(buffer, uint64(len(values)))
	for _, value := range values {
		var err error
		if buffer, err = appendValue(buffer, value); err != nil {
			return nil, err
		}
	}
	return buffer, nil
}

func appendValue(buffer []byte, value any) ([]byte, error) {
	switch typed := value.(type) {
	case nil:
		buffer = append(buffer, 0)
	case int64:
		buffer = append(buffer, 1)
		buffer = binary.AppendVarint(buffer, typed)
	case float64:
		buffer = append(buffer, 2)
		buffer = binary.LittleEndian.AppendUint64(buffer, math.Float64bits(typed))
	case string:
		buffer = append(buffer, 3)
		buffer = binary.AppendUvarint(buffer, uint64(len(typed)))
		buffer = append(buffer, typed...)
	case []byte:
		buffer = append(buffer, 4)
		buffer = binary.AppendUvarint(buffer, uint64(len(typed)))
		buffer = append(buffer, typed...)
	default:
		return nil, fmt.Errorf("kitdb: value of type %T cannot enter the history", value)
	}
	return buffer, nil
}

type changesReader struct {
	data   []byte
	failed bool
}

func (reader *changesReader) take(count uint64) []byte {
	if reader.failed || uint64(len(reader.data)) < count {
		reader.failed = true
		return nil
	}
	value := reader.data[:count]
	reader.data = reader.data[count:]
	return value
}

func (reader *changesReader) uvarint() uint64 {
	value, size := binary.Uvarint(reader.data)
	if reader.failed || size <= 0 {
		reader.failed = true
		return 0
	}
	reader.data = reader.data[size:]
	return value
}

func (reader *changesReader) varint() int64 {
	value, size := binary.Varint(reader.data)
	if reader.failed || size <= 0 {
		reader.failed = true
		return 0
	}
	reader.data = reader.data[size:]
	return value
}

func (reader *changesReader) values() []any {
	count := reader.uvarint()
	if count > uint64(len(reader.data)) {
		reader.failed = true
		return nil
	}
	values := make([]any, count)
	for position := range values {
		values[position] = reader.value()
		if reader.failed {
			return nil
		}
	}
	return values
}

func (reader *changesReader) value() any {
	tag := reader.take(1)
	if reader.failed {
		return nil
	}
	switch tag[0] {
	case 0:
		return nil
	case 1:
		return reader.varint()
	case 2:
		bits := reader.take(8)
		if reader.failed {
			return nil
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(bits))
	case 3:
		return string(reader.take(reader.uvarint()))
	case 4:
		return append([]byte{}, reader.take(reader.uvarint())...)
	}
	reader.failed = true
	return nil
}

func decodeChanges(data []byte) ([]Change, error) {
	reader := &changesReader{data: data}
	format := reader.take(1)
	if reader.failed || format[0] < changesFormat || format[0] > columnsBarrierFormat {
		return nil, fmt.Errorf("%w: unknown changes format", ErrCorruptHistory)
	}
	nameCount := reader.uvarint()
	if nameCount > uint64(len(reader.data)) {
		return nil, fmt.Errorf("%w: name table exceeds the changes", ErrCorruptHistory)
	}
	names := make([]string, nameCount)
	for position := range names {
		names[position] = string(reader.take(reader.uvarint()))
	}
	lookup := func(index uint64) string {
		if index >= uint64(len(names)) {
			reader.failed = true
			return ""
		}
		return names[index]
	}
	tableCount := reader.uvarint()
	if tableCount > uint64(len(reader.data)) {
		return nil, fmt.Errorf("%w: column tables exceed the changes", ErrCorruptHistory)
	}
	columns := map[string][]string{}
	for range tableCount {
		table := lookup(reader.uvarint())
		columnCount := reader.uvarint()
		if reader.failed || columnCount > uint64(len(reader.data)) {
			return nil, fmt.Errorf("%w: column list exceeds the changes", ErrCorruptHistory)
		}
		names := make([]string, columnCount)
		for position := range names {
			names[position] = lookup(reader.uvarint())
		}
		columns[table] = names
	}
	count := reader.uvarint()
	if count > uint64(len(reader.data)) {
		return nil, fmt.Errorf("%w: change count exceeds the changes", ErrCorruptHistory)
	}
	changes := make([]Change, 0, count)
	for range count {
		operation := reader.take(1)
		if reader.failed {
			break
		}
		change := Change{Operation: Operation(operation[0]), Table: lookup(reader.uvarint()), By: lookup(reader.uvarint())}
		switch {
		case change.Operation == Barrier:
			if format[0] != barrierFormat && format[0] != columnsBarrierFormat {
				reader.failed = true
				break
			}
			change.Statement = string(reader.take(reader.uvarint()))
		case change.Operation == Statement:
			change.Statement = string(reader.take(reader.uvarint()))
		case format[0] >= columnsFormat && (change.Operation == Insert || change.Operation == Update || change.Operation == Delete):
			change.RowID = reader.varint()
			change.Columns = columns[change.Table]
			if change.Columns == nil {
				reader.failed = true
				break
			}
			switch change.Operation {
			case Insert:
				change.After = reader.values()
				reader.failed = reader.failed || len(change.After) != len(change.Columns)
			case Delete:
				change.Before = reader.values()
				reader.failed = reader.failed || len(change.Before) != len(change.Columns)
			case Update:
				count := reader.uvarint()
				if count > uint64(len(change.Columns)) {
					reader.failed = true
					break
				}
				change.Updates = make([]ColumnUpdate, 0, count)
				last := -1
				for range count {
					position := reader.uvarint()
					kind := reader.take(1)
					if reader.failed || position >= uint64(len(change.Columns)) || int(position) <= last {
						reader.failed = true
						break
					}
					update := ColumnUpdate{Position: int(position)}
					switch kind[0] {
					case columnChanged:
						update.Old = reader.value()
						update.New = reader.value()
					case columnKept:
						update.New = reader.value()
						update.Old = update.New
					default:
						reader.failed = true
					}
					if reader.failed {
						break
					}
					last = update.Position
					change.Updates = append(change.Updates, update)
				}
			}
		case change.Operation == Insert || change.Operation == Update || change.Operation == Delete:
			change.RowID = reader.varint()
			if change.Operation != Insert {
				change.Before = reader.values()
			}
			if change.Operation == Delete {
				change.Columns = columns[change.Table] // nil for a commit written before 03/11/2026
			}
			if change.Operation != Delete {
				change.After = reader.values()
				change.Columns = columns[change.Table]
				if len(change.Columns) != len(change.After) {
					reader.failed = true
				}
			}
			// an update of format 1 is every column, so a reader of updates reads Updates alone
			if change.Operation == Update && !reader.failed {
				if len(change.Before) != len(change.After) {
					reader.failed = true
					break
				}
				change.Updates = make([]ColumnUpdate, len(change.After))
				for position := range change.After {
					change.Updates[position] = ColumnUpdate{Position: position, Old: change.Before[position], New: change.After[position]}
				}
			}
		default:
			reader.failed = true
		}
		if reader.failed {
			break
		}
		changes = append(changes, change)
	}
	if reader.failed || len(reader.data) != 0 {
		return nil, fmt.Errorf("%w: changes are truncated or malformed", ErrCorruptHistory)
	}
	return changes, nil
}

type historyRow struct {
	revision uint64
	at       int64
	checksum []byte
	changes  []byte
}

type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// readHistory visits the rows after revision, through at (zero: every row), in order.
func readHistory(ctx context.Context, querier rowQuerier, after uint64, through int64, visit func(historyRow) error) error {
	query := `SELECT rev, at, checksum, changes FROM kitwork_history WHERE rev > ?`
	arguments := []any{int64(after)}
	if through != 0 {
		query += ` AND at <= ?`
		arguments = append(arguments, through)
	}
	rows, err := querier.QueryContext(ctx, query+` ORDER BY rev`, arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row historyRow
		var revision int64
		if err := rows.Scan(&revision, &row.at, &row.checksum, &row.changes); err != nil {
			return err
		}
		row.revision = uint64(revision)
		if err := visit(row); err != nil {
			return err
		}
	}
	return rows.Err()
}

// Changes visits every commit after revision, in order.
func (database *Database) Changes(ctx context.Context, after uint64, visit func(Commit) error) error {
	if err := database.ensureOpen(); err != nil {
		return err
	}
	return ReadChanges(ctx, database.readers, after, visit)
}

// ReadChanges visits every commit after revision of the file querier reads, in order, without a Database: what the
// full-text indexes fold in (engine/database/fulltext), on a connection of their own. Inside a read transaction it
// sees exactly the commits that transaction sees.
func ReadChanges(ctx context.Context, querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}, after uint64, visit func(Commit) error) error {
	return readHistory(ctx, querier, after, 0, func(row historyRow) error {
		changes, err := decodeChanges(row.changes)
		if err != nil {
			return fmt.Errorf("revision %d: %w", row.revision, err)
		}
		return visit(Commit{Revision: row.revision, At: time.Unix(0, row.at).UTC(), Changes: changes})
	})
}

// RevisionAt is the last revision committed at or before moment, 0 for a moment before the first. A moment before
// the history Retain kept is ErrPruned: the revision it names is no longer known.
func (database *Database) RevisionAt(ctx context.Context, moment time.Time) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	if err := database.prunedBefore(ctx, moment.UTC().UnixNano()); err != nil {
		return 0, err
	}
	var revision int64
	err := database.readers.QueryRowContext(ctx, `SELECT coalesce(max(rev), 0) FROM kitwork_history WHERE at <= ?`,
		moment.UTC().UnixNano()).Scan(&revision)
	return uint64(revision), err
}

// VerifyHistory checks that revisions are contiguous, that moments only move
// forward, that every checksum chains from the one before it, and that every
// change decodes. The first row's own predecessor is checked when it is
// revision 1.
func (database *Database) VerifyHistory(ctx context.Context) (uint64, error) {
	if err := database.ensureOpen(); err != nil {
		return 0, err
	}
	return verifyHistory(ctx, database.readers)
}

func verifyHistory(ctx context.Context, querier rowQuerier) (uint64, error) {
	var previous historyRow
	first := true
	err := readHistory(ctx, querier, 0, 0, func(row historyRow) error {
		if !first && row.revision != previous.revision+1 {
			return fmt.Errorf("%w: revision %d follows %d", ErrCorruptHistory, row.revision, previous.revision)
		}
		if !first && row.at <= previous.at {
			return fmt.Errorf("%w: revision %d does not move forward in time", ErrCorruptHistory, row.revision)
		}
		if first && row.revision == 1 || !first {
			if want := chainChecksum(previous.checksum, row.revision, row.at, row.changes); string(want) != string(row.checksum) {
				return fmt.Errorf("%w: checksum of revision %d does not chain", ErrCorruptHistory, row.revision)
			}
		}
		if _, err := decodeChanges(row.changes); err != nil {
			return fmt.Errorf("revision %d: %w", row.revision, err)
		}
		previous, first = row, false
		return nil
	})
	return previous.revision, err
}
