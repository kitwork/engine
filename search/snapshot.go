package search

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"

	"github.com/kitwork/engine/internal/snapshotfile"
)

type packedManifest struct {
	Schema     [32]byte
	Generation uint64
	Lengths    []int64
}

// WriteSnapshot packs a deletion-free immutable index. Segment bytes are not
// re-encoded. This experimental read-only format is not an IndexWriter store.
func (index *Index) WriteSnapshot(ctx context.Context, output io.Writer) error {
	if ctx == nil || output == nil {
		return fmt.Errorf("search: nil snapshot context/output")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := index.ensureOpen(); err != nil {
		return err
	}
	if index.deleted != 0 {
		return fmt.Errorf("search: snapshot requires a fresh replacement without tombstones")
	}
	manifest := packedManifest{Schema: index.schema.fingerprint, Generation: index.generation}
	for _, segment := range index.segments {
		manifest.Lengths = append(manifest.Lengths, segment.Info().Bytes)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("search: snapshot manifest too large")
	}
	var header [16]byte
	copy(header[:], "KSRCH001")
	binary.LittleEndian.PutUint32(header[8:], uint32(len(data)))
	binary.LittleEndian.PutUint32(header[12:], crc32.Checksum(data, crcTable))
	if _, err := output.Write(header[:]); err != nil {
		return err
	}
	if _, err := output.Write(data); err != nil {
		return err
	}
	for _, segment := range index.segments {
		if err := segment.Verify(ctx); err != nil {
			return err
		}
		if err := snapshotfile.Copy(ctx, output, io.NewSectionReader(segment.file, 0, segment.Info().Bytes)); err != nil {
			return err
		}
	}
	return nil
}

// OpenSnapshot borrows a single container handle. Queries read segments through
// section readers, without extracting files. The caller owns input's lifetime.
func OpenSnapshot(input *io.SectionReader, schema Schema) (*Index, error) {
	if input == nil {
		return nil, fmt.Errorf("search: nil snapshot input")
	}
	if !schema.valid() {
		return nil, fmt.Errorf("search: invalid schema")
	}
	var header [16]byte
	if _, err := input.ReadAt(header[:], 0); err != nil {
		return nil, err
	}
	length := binary.LittleEndian.Uint32(header[8:])
	if string(header[:8]) != "KSRCH001" || length > 1<<20 {
		return nil, corruptIndexf("invalid packed header")
	}
	data := make([]byte, length)
	if _, err := input.ReadAt(data, 16); err != nil {
		return nil, err
	}
	if crc32.Checksum(data, crcTable) != binary.LittleEndian.Uint32(header[12:]) {
		return nil, corruptIndexf("packed manifest checksum mismatch")
	}
	var manifest packedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, err
	}
	if manifest.Schema != schema.fingerprint {
		return nil, ErrSchemaMismatch
	}
	if len(manifest.Lengths) > manifestMaximumSegments || manifest.Generation == 0 {
		return nil, corruptIndexf("invalid packed generation")
	}
	index := &Index{schema: schema, generation: manifest.Generation, fieldStats: make([]uint64, len(schema.fields))}
	offset := int64(16 + length)
	for _, length := range manifest.Lengths {
		if length < segmentHeaderSize || length > input.Size()-offset {
			return nil, corruptIndexf("invalid packed segment bounds")
		}
		segment, err := openSegmentReader("<packed>", io.NewSectionReader(input, offset, length), length, schema)
		if err != nil {
			return nil, err
		}
		index.bases = append(index.bases, index.documents)
		if index.documents > math.MaxUint64-uint64(segment.header.documentN) {
			return nil, corruptIndexf("packed document count overflow")
		}
		index.documents += uint64(segment.header.documentN)
		index.segments = append(index.segments, segment)
		index.deletions = append(index.deletions, nil)
		index.entries = append(index.entries, manifestSegment{documents: segment.header.documentN, bytes: uint64(length)})
		for i, total := range segment.fieldStats {
			if total > math.MaxUint64-index.fieldStats[i] {
				return nil, corruptIndexf("packed field statistics overflow")
			}
			index.fieldStats[i] += total
		}
		offset += length
	}
	if offset != input.Size() {
		return nil, corruptIndexf("trailing packed bytes")
	}
	index.physical, index.bytes = index.documents, input.Size()
	return index, nil
}
