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
// TEXT values stop at the first NUL character; replay never needs Before.
type Change struct {
	Operation Operation
	Table     string
	RowID     int64
	Columns   []string
	Before    []any
	After     []any
	Statement string
	By        string
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

// encodeChanges writes format 1: the format byte, a table of the names the
// changes use (tables, columns, identities), the columns of every table whose
// rows the commit left, then each change. A commit holding a Barrier is
// written in format 2, which is format 1 with that operation.
func encodeChanges(changes []Change) ([]byte, error) {
	format := byte(changesFormat)
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
		switch {
		case change.Operation == Delete && change.Columns != nil:
		case change.Operation != Insert && change.Operation != Update:
			continue
		}
		known, seen := columns[change.Table]
		if !seen {
			columns[change.Table] = change.Columns
			tables = append(tables, change.Table)
		} else if len(known) != len(change.Columns) {
			return nil, fmt.Errorf("kitdb: table %q changed its columns inside one commit", change.Table)
		}
		if change.Operation != Delete && len(change.Columns) != len(change.After) {
			return nil, fmt.Errorf("kitdb: change of %q names %d columns for %d values", change.Table, len(change.Columns), len(change.After))
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
	for _, change := range changes {
		body = append(body, byte(change.Operation))
		body = binary.AppendUvarint(body, name(change.Table))
		body = binary.AppendUvarint(body, name(change.By))
		switch change.Operation {
		case Statement, Barrier:
			if change.Operation == Barrier {
				format = barrierFormat
			}
			body = binary.AppendUvarint(body, uint64(len(change.Statement)))
			body = append(body, change.Statement...)
			continue
		case Insert, Update, Delete:
		default:
			return nil, fmt.Errorf("kitdb: unknown change operation %d", change.Operation)
		}
		body = binary.AppendVarint(body, change.RowID)
		if change.Operation != Insert {
			var err error
			if body, err = appendValues(body, change.Before); err != nil {
				return nil, err
			}
		}
		if change.Operation != Delete {
			var err error
			if body, err = appendValues(body, change.After); err != nil {
				return nil, err
			}
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

func appendValues(buffer []byte, values []any) ([]byte, error) {
	buffer = binary.AppendUvarint(buffer, uint64(len(values)))
	for _, value := range values {
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
		tag := reader.take(1)
		if reader.failed {
			return nil
		}
		switch tag[0] {
		case 0:
		case 1:
			values[position] = reader.varint()
		case 2:
			values[position] = math.Float64frombits(binary.LittleEndian.Uint64(reader.take(8)))
		case 3:
			values[position] = string(reader.take(reader.uvarint()))
		case 4:
			values[position] = append([]byte{}, reader.take(reader.uvarint())...)
		default:
			reader.failed = true
		}
	}
	return values
}

func decodeChanges(data []byte) ([]Change, error) {
	reader := &changesReader{data: data}
	format := reader.take(1)
	if reader.failed || format[0] != changesFormat && format[0] != barrierFormat {
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
		switch change.Operation {
		case Barrier:
			if format[0] != barrierFormat {
				reader.failed = true
				break
			}
			change.Statement = string(reader.take(reader.uvarint()))
		case Statement:
			change.Statement = string(reader.take(reader.uvarint()))
		case Insert, Update, Delete:
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
