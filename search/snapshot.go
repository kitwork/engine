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
	manifest, offset, err := readPackedSnapshotManifest(input, schema)
	if err != nil {
		return nil, err
	}
	index := &Index{schema: schema, generation: manifest.Generation, fieldStats: make([]uint64, len(schema.fields))}
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

// InspectSnapshot validates the packed manifest and each fixed-size segment
// header without loading sparse dictionaries or payloads. It is suitable for
// cold database admission; OpenSnapshot and Verify remain the query/deep paths.
func InspectSnapshot(ctx context.Context, input *io.SectionReader, schema Schema) (IndexInfo, error) {
	if ctx == nil {
		return IndexInfo{}, fmt.Errorf("search: nil snapshot context")
	}
	if err := ctx.Err(); err != nil {
		return IndexInfo{}, err
	}
	manifest, offset, err := readPackedSnapshotManifest(input, schema)
	if err != nil {
		return IndexInfo{}, err
	}
	info := IndexInfo{Generation: manifest.Generation, Segments: len(manifest.Lengths), Bytes: input.Size()}
	for position, length := range manifest.Lengths {
		if err := ctx.Err(); err != nil {
			return IndexInfo{}, err
		}
		if length < segmentHeaderSize || length > input.Size()-offset {
			return IndexInfo{}, corruptIndexf("invalid packed segment bounds")
		}
		section := io.NewSectionReader(input, offset, length)
		var headerBytes [segmentHeaderSize]byte
		if err := readAtFull(section, headerBytes[:], 0); err != nil {
			return IndexInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		header, err := parseSegmentHeader(headerBytes[:], length)
		if err != nil {
			return IndexInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		if err := validateSegmentHeaderShape(header, schema); err != nil {
			return IndexInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		if info.Documents > math.MaxUint64-uint64(header.documentN) {
			return IndexInfo{}, corruptIndexf("packed document count overflow")
		}
		info.Documents += uint64(header.documentN)
		offset += length
	}
	if offset != input.Size() {
		return IndexInfo{}, corruptIndexf("trailing packed bytes")
	}
	info.PhysicalDocuments = info.Documents
	return info, nil
}

func readPackedSnapshotManifest(input *io.SectionReader, schema Schema) (packedManifest, int64, error) {
	if input == nil {
		return packedManifest{}, 0, fmt.Errorf("search: nil snapshot input")
	}
	if !schema.valid() {
		return packedManifest{}, 0, fmt.Errorf("search: invalid schema")
	}
	var header [16]byte
	if _, err := input.ReadAt(header[:], 0); err != nil {
		return packedManifest{}, 0, err
	}
	length := binary.LittleEndian.Uint32(header[8:])
	if string(header[:8]) != "KSRCH001" || length > 1<<20 {
		return packedManifest{}, 0, corruptIndexf("invalid packed header")
	}
	data := make([]byte, length)
	if _, err := input.ReadAt(data, 16); err != nil {
		return packedManifest{}, 0, err
	}
	if crc32.Checksum(data, crcTable) != binary.LittleEndian.Uint32(header[12:]) {
		return packedManifest{}, 0, corruptIndexf("packed manifest checksum mismatch")
	}
	var manifest packedManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return packedManifest{}, 0, err
	}
	if manifest.Schema != schema.fingerprint {
		return packedManifest{}, 0, ErrSchemaMismatch
	}
	if len(manifest.Lengths) > manifestMaximumSegments || manifest.Generation == 0 {
		return packedManifest{}, 0, corruptIndexf("invalid packed generation")
	}
	return manifest, int64(16) + int64(length), nil
}
