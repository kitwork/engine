package analytics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/kitwork/engine/kitdb/columnar"
)

// Build writes the main part of the copy of columns of table, as the read transaction source sees the file it
// attaches as schema ("main": its own), as table's section of a new generation of f, and returns its manifest. The
// rows go in rowid order, blocks of 1,024. A reader of the copy before keeps its generation; the section before stays
// in the file, unread, until the file is compacted.
func (f *File) Build(ctx context.Context, source Querier, schema, table string, columns []string) (*Manifest, error) {
	if len(columns) == 0 || len(columns)+1 > columnar.MaximumColumns {
		return nil, fmt.Errorf("analytics: a copy holds 1..%d columns", columnar.MaximumColumns-1)
	}
	revision, version, _, err := snapshotState(ctx, source, schema)
	if err != nil {
		return nil, err
	}
	kinds, err := columnKinds(ctx, source, schema, table, columns)
	if err != nil {
		return nil, err
	}
	f.publish.Lock()
	defer f.publish.Unlock()
	manifest, err := f.publishLocked(ctx, table, func(output io.Writer, _ *generation) (*Manifest, error) {
		manifest := &Manifest{Columns: kinds, Revision: revision, Schema: version}
		return manifest, writeRows(ctx, source, schema, table, manifest, output)
	})
	if err != nil {
		return nil, err
	}
	f.moved()
	return manifest, nil
}

// writeRows writes the rows of table as source holds them into output, KCOL blocks of 1,024, counting them into
// manifest.
func writeRows(ctx context.Context, source Querier, schema, table string, manifest *Manifest, output io.Writer) error {
	counted := &countingWriter{output: output}
	buffered := bufio.NewWriterSize(counted, 4<<20)
	fields := manifest.fields()
	writer, err := columnar.NewWriter(buffered, fields)
	if err != nil {
		return err
	}
	batch, err := columnar.NewBatch(fields)
	if err != nil {
		return err
	}
	names := make([]string, len(manifest.Columns))
	for position, column := range manifest.Columns {
		names[position] = quoteIdentifier(column.Name)
	}
	rows, err := source.QueryContext(ctx, `SELECT _rowid_, `+strings.Join(names, ", ")+` FROM `+quoteIdentifier(schema)+"."+
		quoteIdentifier(table)+` ORDER BY _rowid_`)
	if err != nil {
		return err
	}
	defer rows.Close()
	values := make([]any, len(names)+1)
	pointers := make([]any, len(values))
	for position := range values {
		pointers[position] = &values[position]
	}
	flush := func() error {
		if batch.Rows == 0 {
			return nil
		}
		if err := writer.WriteBatch(batch); err != nil {
			return err
		}
		manifest.Blocks++
		batch.Reset()
		return nil
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return err
		}
		row := batch.Rows
		rowID, isInteger := values[0].(int64)
		if !isInteger {
			return fmt.Errorf("analytics: %q gave a rowid of %T", table, values[0])
		}
		batch.Columns[0].Integers[row] = rowID
		batch.Columns[0].Valid[row] = 1
		for position, column := range manifest.Columns {
			if err := setCell(&batch.Columns[position+1], row, values[position+1]); err != nil {
				return fmt.Errorf("%w: %s.%s, rowid %d: %v", ErrUnsupported, table, column.Name, rowID, err)
			}
		}
		batch.Rows++
		manifest.Rows++
		manifest.MaxRowID = rowID
		if batch.Rows == columnar.BatchRows {
			if err := flush(); err != nil {
				return err
			}
			if manifest.Rows%(64*columnar.BatchRows) == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if err := flush(); err != nil {
		return err
	}
	if err := buffered.Flush(); err != nil {
		return err
	}
	manifest.Bytes = counted.written
	return nil
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	output  io.Writer
	written int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	n, err := w.output.Write(data)
	w.written += int64(n)
	return n, err
}

// setCell puts value at row of vector: a NULL as no value; an integer, a real or a text of the vector's kind.
func setCell(vector *columnar.Vector, row int, value any) error {
	if value == nil {
		vector.Valid[row] = 0
		return nil
	}
	switch vector.Field.Kind {
	case columnar.Integer:
		number, ok := value.(int64)
		if !ok {
			return fmt.Errorf("a value of %T in an integer column", value)
		}
		vector.Integers[row] = number
	case columnar.Float:
		number, ok := value.(float64)
		if !ok {
			return fmt.Errorf("a value of %T in a real column", value)
		}
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return errors.New("a real that is not finite")
		}
		vector.Floats[row] = number
	case columnar.Text:
		switch text := value.(type) {
		case string:
			vector.Texts[row] = text
		case []byte:
			vector.Texts[row] = string(text)
		default:
			return fmt.Errorf("a value of %T in a text column", value)
		}
	}
	vector.Valid[row] = 1
	return nil
}
