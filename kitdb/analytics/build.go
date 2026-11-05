package analytics

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kitwork/engine/kitdb/columnar"
)

// Build writes the main part of the copy of columns of table, as the read transaction source sees the file, into
// directory, and returns its manifest. The rows go in rowid order, blocks of 1,024. The data file takes a name of
// its own and the manifest names it last, so a reader of the copy before keeps its file; the files no manifest names
// any more are removed when nothing holds them.
func Build(ctx context.Context, source Querier, directory, table string, columns []string) (*Manifest, error) {
	if len(columns) == 0 || len(columns)+1 > columnar.MaximumColumns {
		return nil, fmt.Errorf("analytics: a copy holds 1..%d columns", columnar.MaximumColumns-1)
	}
	revision, schema, err := snapshotState(ctx, source)
	if err != nil {
		return nil, err
	}
	kinds, err := columnKinds(ctx, source, table, columns)
	if err != nil {
		return nil, err
	}
	manifest := &Manifest{Format: manifestFormat, Table: table, Columns: kinds, Revision: revision, Schema: schema}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	stamp := time.Now().UnixNano()
	temporary := filepath.Join(directory, fmt.Sprintf("%s-%d.tmp", table, stamp))
	written := false
	defer func() {
		if !written {
			_ = os.Remove(temporary)
		}
	}()
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
	}()
	buffered := bufio.NewWriterSize(file, 4<<20)
	fields := manifest.fields()
	writer, err := columnar.NewWriter(buffered, fields)
	if err != nil {
		return nil, err
	}
	batch, err := columnar.NewBatch(fields)
	if err != nil {
		return nil, err
	}

	names := make([]string, len(kinds))
	for position, column := range kinds {
		names[position] = quoteIdentifier(column.Name)
	}
	rows, err := source.QueryContext(ctx, `SELECT _rowid_, `+strings.Join(names, ", ")+` FROM `+quoteIdentifier(table)+` ORDER BY _rowid_`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]any, len(kinds)+1)
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
		batch.Reset()
		return nil
	}
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := batch.Rows
		rowID, isInteger := values[0].(int64)
		if !isInteger {
			return nil, fmt.Errorf("analytics: %q gave a rowid of %T", table, values[0])
		}
		batch.Columns[0].Integers[row] = rowID
		batch.Columns[0].Valid[row] = 1
		for position, column := range kinds {
			if err := setCell(&batch.Columns[position+1], row, values[position+1]); err != nil {
				return nil, fmt.Errorf("%w: %s.%s, rowid %d: %v", ErrUnsupported, table, column.Name, rowID, err)
			}
		}
		batch.Rows++
		manifest.Rows++
		manifest.MaxRowID = rowID
		if batch.Rows == columnar.BatchRows {
			if err := flush(); err != nil {
				return nil, err
			}
			if manifest.Rows%(64*columnar.BatchRows) == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := flush(); err != nil {
		return nil, err
	}
	if err := buffered.Flush(); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	manifest.Bytes = info.Size()
	closed = true
	if err := file.Close(); err != nil {
		return nil, err
	}
	manifest.Data = fmt.Sprintf("%s-%d-%d.kcol", table, revision, stamp)
	if err := os.Rename(temporary, filepath.Join(directory, manifest.Data)); err != nil {
		return nil, err
	}
	written = true
	if err := writeManifest(directory, manifest); err != nil {
		return nil, err
	}
	removeUnnamed(directory, table, manifest.Data)
	return manifest, nil
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

// writeManifest replaces table's manifest in directory.
func writeManifest(directory string, manifest *Manifest) error {
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	temporary := filepath.Join(directory, fmt.Sprintf("%s-%d.json.tmp", manifest.Table, time.Now().UnixNano()))
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = file.Write(content)
	if err == nil {
		err = file.Sync()
	}
	if err = errors.Join(err, file.Close()); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	if err := os.Rename(temporary, manifestPath(directory, manifest.Table)); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

// ownFile is a file of a table's copy after its name and a dash: a main part (<revision>-<stamp>.kcol), what a build
// or a manifest cut short left (<stamp>.tmp, <stamp>.json.tmp). Another table whose name begins with this one's
// and a dash never matches.
var ownFile = regexp.MustCompile(`^[0-9]+(-[0-9]+\.kcol|\.tmp|\.json\.tmp)$`)

// removeUnnamed removes the data files of table the manifest no longer names, and what a build cut short left; a
// file a reader still holds stays, for the next build to remove.
func removeUnnamed(directory, table, named string) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == named || !strings.HasPrefix(name, table+"-") || !ownFile.MatchString(name[len(table)+1:]) {
			continue
		}
		_ = os.Remove(filepath.Join(directory, name))
	}
}
