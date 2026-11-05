package analytics

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/kitwork/engine/kitdb/columnar"
)

// Copy is the open main part of one table's copy.
type Copy struct {
	Manifest *Manifest
	file     *os.File
	reader   *columnar.Reader
}

// Open opens the main part of table's copy in directory: nil and no error when there is none.
func Open(directory, table string) (*Copy, error) {
	manifest, err := ReadManifest(directory, table)
	if err != nil || manifest == nil {
		return nil, err
	}
	file, err := os.Open(filepath.Join(directory, manifest.Data))
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	if info.Size() != manifest.Bytes {
		file.Close()
		return nil, fmt.Errorf("analytics: %s holds %d bytes, its manifest says %d", manifest.Data, info.Size(), manifest.Bytes)
	}
	reader, err := columnar.Open(io.NewSectionReader(file, 0, info.Size()))
	if err != nil {
		file.Close()
		return nil, err
	}
	if fmt.Sprint(reader.Fields) != fmt.Sprint(manifest.fields()) {
		file.Close()
		return nil, fmt.Errorf("analytics: %s holds other columns than its manifest", manifest.Data)
	}
	return &Copy{Manifest: manifest, file: file, reader: reader}, nil
}

// Close closes the main part's file.
func (c *Copy) Close() error { return c.file.Close() }

// fieldsOf are the fields of the rowid and of the named columns, in that order.
func (c *Copy) fieldsOf(columns []string) ([]columnar.Field, error) {
	all := c.Manifest.fields()
	fields := []columnar.Field{all[0]}
	for _, name := range columns {
		position := c.Manifest.position(name)
		if position < 0 {
			return nil, fmt.Errorf("analytics: the copy of %q has no column %q", c.Manifest.Table, name)
		}
		fields = append(fields, all[position+1])
	}
	return fields, nil
}

// Scan visits each block of the main part, in rowid order, with the rowid (column 0 of the batch) and the named
// columns after it. decide, when not nil, is given each block's statistics in the same order first. visit must not
// keep the batch.
func (c *Copy) Scan(ctx context.Context, columns []string, decide func(*columnar.BlockStatistics) (columnar.BlockAction, error),
	visit func(*columnar.Batch) error) (columnar.ScanReport, error) {
	fields, err := c.fieldsOf(columns)
	if err != nil {
		return columnar.ScanReport{}, err
	}
	return c.reader.ScanBlocks(ctx, fields, decide, visit)
}
