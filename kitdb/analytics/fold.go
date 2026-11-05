package analytics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/kitwork/engine/internal/snapshotfile"
	"github.com/kitwork/engine/kitdb/columnar"
)

// errFoldMoved means the table's copy is no longer the one the view read (a build or another fold came first), or the
// view holds nothing past it: there is nothing to fold.
var errFoldMoved = errors.New("analytics: the copy moved since the view")

// errFoldSpread means the changed rows are spread over many blocks while they are still few: folding them would write
// much of the table for little, so they wait until there are more (deferredRows).
var errFoldSpread = errors.New("analytics: the changed rows are spread over many blocks and still few")

// spreadBlocks is the share of a table's blocks (1/16) past which a fold of few changed rows waits.
const spreadBlocks = 16

// deferredRows is how many changed rows a fold waits for when they are spread: a 256th of the table's rows, so a
// table written again whole for them is written again once for that many changes.
func deferredRows(rows uint64) int { return int(rows / 256) }

// maxSectionExtents bounds how many ranges of the file a fold leaves a table's section made of. Each one is a line of
// every generation's directory; past it, the fold writes the table whole.
const maxSectionExtents = 1024

// Fold makes the rows view reads from its changed rows part of the main part, as table's section of a new generation
// at the view's revision (KITWORK_KITDB.md mục 6). The blocks holding no changed row are not written again: the new
// section refers to their bytes where they are; a block holding one is read, merged with the changed rows of its
// rowids and written anew, split in even blocks when it grew past 1,024 rows. The rows past the last block go with it.
// When the blocks are half empty on average, or the section would be made of more than 1,024 ranges of the file,
// every block is written again in full blocks instead.
func (f *File) Fold(ctx context.Context, view *View) (*Manifest, error) {
	manifest := view.table.copy.Manifest
	return f.fold(ctx, manifest.Table, manifest.Section, view.changed, view.Revision, true)
}

// fold is Fold of changes, the changed rows at revision of the main part in section; the caller keeps no table open.
// Unless eager, changed rows in more than a sixteenth of the blocks and fewer than deferredRows are errFoldSpread,
// nothing written.
func (f *File) fold(ctx context.Context, table, section string, changes *changed, revision uint64, eager bool) (*Manifest, error) {
	f.publish.Lock()
	defer f.publish.Unlock()
	f.mutex.Lock()
	var old *Manifest
	if f.current != nil {
		old = f.current.metadata.Tables[table]
	}
	f.mutex.Unlock()
	if old == nil || old.Section != section || revision <= old.Revision {
		return nil, errFoldMoved
	}
	manifest, err := f.publishLocked(ctx, table, func(output io.Writer, base *generation) (*Manifest, error) {
		return foldSection(ctx, base, old, changes, revision, eager, output)
	})
	if err != nil {
		return nil, err
	}
	f.moved()
	return manifest, nil
}

// blockSpan is one block of a section: where it is, its rows, and the first and last rowid it holds.
type blockSpan struct {
	offset, length int64
	rows           int
	low, high      int64
}

// blockSpans reads where each block of reader is from the blocks' headers alone (their checksums verified).
func blockSpans(ctx context.Context, reader *columnar.Reader, rowID columnar.Field) ([]blockSpan, error) {
	var spans []blockSpan
	offset := reader.DataOffset()
	_, err := reader.ScanBlocks(ctx, []columnar.Field{rowID}, func(statistics *columnar.BlockStatistics) (columnar.BlockAction, error) {
		ids := statistics.Columns[0]
		if !ids.HasValue || ids.Nulls != 0 || len(spans) > 0 && ids.IntegerMin <= spans[len(spans)-1].high {
			return 0, fmt.Errorf("analytics: the blocks of the copy are not in rowid order at %d", offset)
		}
		spans = append(spans, blockSpan{offset: offset, length: statistics.EncodedBytes, rows: statistics.Rows, low: ids.IntegerMin, high: ids.IntegerMax})
		offset += statistics.EncodedBytes
		return columnar.SkipBlock, nil
	}, func(*columnar.Batch) error { return nil })
	return spans, err
}

// rowSource is one row of a merged block: the row at index of the block read, or (index -1) a changed row.
type rowSource struct {
	index int
	rowID int64
}

// foldSection writes old's section of base with changes merged into output, and returns its manifest.
func foldSection(ctx context.Context, base *generation, old *Manifest, changes *changed, revision uint64, eager bool, output io.Writer) (*Manifest, error) {
	section, err := base.reader.Section(old.Section)
	if err != nil {
		return nil, err
	}
	reader, err := columnar.Open(section)
	if err != nil {
		return nil, err
	}
	fields := old.fields()
	spans, err := blockSpans(ctx, reader, fields[0])
	if err != nil {
		return nil, err
	}

	// the changed rows of each block: past the last rowid of the block before, up to its own (the last block takes the
	// rows past every block)
	rowIDs := changes.rowIDs
	mine := make([][]int64, len(spans))
	touched := make([]bool, len(spans))
	count, next := 0, 0
	for position, span := range spans {
		end := len(rowIDs)
		if position < len(spans)-1 {
			end = next + sort.Search(len(rowIDs)-next, func(k int) bool { return rowIDs[next+k] > span.high })
		}
		mine[position] = rowIDs[next:end]
		next = end
		for _, rowID := range mine[position] {
			if changes.images[rowID] != nil || rowID >= span.low && rowID <= span.high {
				touched[position] = true
				count++
				break
			}
		}
	}
	if !eager && count*spreadBlocks > len(spans) && len(rowIDs) < deferredRows(old.Rows) {
		return nil, errFoldSpread
	}

	// the rows and blocks the section will hold, counted from the rowids of the blocks changed
	rows, blocks := 0, 0
	var merged []rowSource
	for position, span := range spans {
		if !touched[position] {
			rows, blocks = rows+span.rows, blocks+1
			continue
		}
		_, err := reader.ScanBlockRanges(ctx, fields[:1], []columnar.BlockRange{{Offset: span.offset, Length: span.length}}, nil, func(in *columnar.Batch) error {
			merged = mergeRows(merged[:0], in, mine[position], changes.images)
			return nil
		})
		if err != nil {
			return nil, err
		}
		rows += len(merged)
		blocks += (len(merged) + columnar.BatchRows - 1) / columnar.BatchRows
	}
	refill := blocks > 1 && rows < blocks*columnar.BatchRows/2 || base.reader.SectionExtents(old.Section)+2*count+2 > maxSectionExtents

	manifest := *old
	manifest.Revision, manifest.Rows, manifest.Blocks, manifest.MaxRowID, manifest.Folds = revision, 0, 0, 0, old.Folds+1
	counted := &countingWriter{output: output}
	buffered := bufio.NewWriterSize(counted, 1<<20)
	writer, err := columnar.NewWriter(buffered, fields)
	if err != nil {
		return nil, err
	}
	out, err := columnar.NewBatch(fields)
	if err != nil {
		return nil, err
	}
	emit := func() error {
		if out.Rows == 0 {
			return nil
		}
		if err := writer.WriteBatch(out); err != nil {
			return err
		}
		manifest.Blocks++
		manifest.Rows += uint64(out.Rows)
		manifest.MaxRowID = out.Columns[0].Integers[out.Rows-1]
		out.Reset()
		return nil
	}
	appendRow := func(in *columnar.Batch, source rowSource) error {
		row := out.Rows
		if source.index >= 0 {
			for column := range out.Columns {
				to, from := &out.Columns[column], &in.Columns[column]
				to.Valid[row] = from.Valid[source.index]
				switch to.Field.Kind {
				case columnar.Float:
					to.Floats[row] = from.Floats[source.index]
				case columnar.Text:
					to.Texts[row] = from.Texts[source.index]
				default:
					to.Integers[row] = from.Integers[source.index]
				}
			}
		} else {
			out.Columns[0].Integers[row], out.Columns[0].Valid[row] = source.rowID, 1
			image := changes.images[source.rowID]
			for position, column := range old.Columns {
				if err := setCell(&out.Columns[position+1], row, image[position]); err != nil {
					return fmt.Errorf("%w: %s.%s, rowid %d: %v", ErrUnsupported, old.Table, column.Name, source.rowID, err)
				}
			}
		}
		out.Rows++
		return nil
	}
	// write puts the merged rows of a block in blocks of their own, as even as 1,024 rows at most allow; when the
	// section is written whole, they fill the blocks in turn instead.
	write := func(in *columnar.Batch, merged []rowSource) error {
		size := columnar.BatchRows
		if !refill && len(merged) > 0 {
			pieces := (len(merged) + columnar.BatchRows - 1) / columnar.BatchRows
			size = (len(merged) + pieces - 1) / pieces
		}
		for _, source := range merged {
			if err := appendRow(in, source); err != nil {
				return err
			}
			if out.Rows == size {
				if err := emit(); err != nil {
					return err
				}
			}
		}
		if !refill {
			return emit()
		}
		return nil
	}

	var referenced, runStart, runLength int64
	reference := func() error {
		if runLength == 0 {
			return nil
		}
		if err := buffered.Flush(); err != nil {
			return err
		}
		if err := snapshotfile.Reference(output, base.reader, old.Section, runStart, runLength); err != nil {
			return err
		}
		referenced += runLength
		runLength = 0
		return nil
	}
	for position, span := range spans {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !touched[position] && !refill {
			if runLength == 0 {
				runStart = span.offset
			}
			runLength += span.length
			manifest.Blocks++
			manifest.Rows += uint64(span.rows)
			manifest.MaxRowID = span.high
			continue
		}
		if err := reference(); err != nil {
			return nil, err
		}
		_, err := reader.ScanBlockRanges(ctx, fields, []columnar.BlockRange{{Offset: span.offset, Length: span.length}}, nil, func(in *columnar.Batch) error {
			merged = mergeRows(merged[:0], in, mine[position], changes.images)
			return write(in, merged)
		})
		if err != nil {
			return nil, err
		}
	}
	if err := reference(); err != nil {
		return nil, err
	}
	if len(spans) == 0 {
		var added []rowSource
		for _, rowID := range rowIDs {
			if changes.images[rowID] != nil {
				added = append(added, rowSource{index: -1, rowID: rowID})
			}
		}
		refill = true
		if err := write(nil, added); err != nil {
			return nil, err
		}
	}
	if err := emit(); err != nil {
		return nil, err
	}
	if err := buffered.Flush(); err != nil {
		return nil, err
	}
	manifest.Bytes = counted.written + referenced
	return &manifest, nil
}

// mergeRows is the rows of the block in, by rowid, with rowIDs merged in: a changed row's image in place of the row
// of its rowid, or nothing for a row deleted since.
func mergeRows(merged []rowSource, in *columnar.Batch, rowIDs []int64, images map[int64][]any) []rowSource {
	ids := in.Columns[0].Integers[:in.Rows]
	i, j := 0, 0
	for i < len(ids) || j < len(rowIDs) {
		if j == len(rowIDs) || i < len(ids) && ids[i] < rowIDs[j] {
			merged = append(merged, rowSource{index: i, rowID: ids[i]})
			i++
			continue
		}
		if i < len(ids) && ids[i] == rowIDs[j] {
			i++
		}
		if images[rowIDs[j]] != nil {
			merged = append(merged, rowSource{index: -1, rowID: rowIDs[j]})
		}
		j++
	}
	return merged
}
