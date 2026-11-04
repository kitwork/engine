package sqlite

import (
	"encoding/binary"
	"fmt"
)

// The writer writes format 3 since 04/11/2026; a file written before holds format 1 (and 2), which every reader still
// reads until app.history({ days }) lets it go. The tests write format 1 with the encoder the writer had.

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
