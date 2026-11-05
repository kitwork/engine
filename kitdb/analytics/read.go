package analytics

import (
	"context"
	"fmt"
	"sync"

	"github.com/kitwork/engine/kitdb/columnar"
)

// Copy is the open main part of one table's copy: its section of a generation of the File, which it keeps readable
// until closed.
type Copy struct {
	Manifest   *Manifest
	generation *generation
	reader     *columnar.Reader
	close      sync.Once
}

// openCopy opens manifest's section of g; the caller has acquired g for it.
func openCopy(g *generation, manifest *Manifest) (*Copy, error) {
	section, err := g.reader.Section(manifest.Section)
	if err != nil {
		return nil, err
	}
	if section.Size() != manifest.Bytes {
		return nil, fmt.Errorf("analytics: the section of %q holds %d bytes, its manifest says %d", manifest.Table, section.Size(), manifest.Bytes)
	}
	reader, err := columnar.Open(section)
	if err != nil {
		return nil, err
	}
	if fmt.Sprint(reader.Fields) != fmt.Sprint(manifest.fields()) {
		return nil, fmt.Errorf("analytics: the section of %q holds other columns than its manifest", manifest.Table)
	}
	return &Copy{Manifest: manifest, generation: g, reader: reader}, nil
}

// Close lets the copy's generation go.
func (c *Copy) Close() error {
	c.close.Do(c.generation.release)
	return nil
}

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
