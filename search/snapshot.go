package search

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"unsafe"

	"github.com/kitwork/engine/internal/snapshotfile"
)

type packedManifest struct {
	Schema     [32]byte
	Generation uint64
	Lengths    []int64
}

// SnapshotInfo describes bounded admission metadata for one packed snapshot.
// ReaderCapacityBytes is a conservative reservation for immutable reader
// structures plus every field-norm vector, not file payload or query memory.
type SnapshotInfo struct {
	Generation          uint64
	Segments            int
	Documents           uint64
	Bytes               int64
	ReaderCapacityBytes int64
}

// WriteSnapshot leases one immutable managed generation and streams it into
// the packed read-only format. Concurrent commits may publish a newer
// generation, but cannot change or close the leased source until this export
// returns.
func (manager *Manager) WriteSnapshot(
	ctx context.Context,
	key string,
	schema Schema,
	output io.Writer,
) (IndexInfo, error) {
	if ctx == nil || output == nil {
		return IndexInfo{}, fmt.Errorf("search: nil snapshot context/output")
	}
	managed, releaseManaged, err := manager.managed(ctx, key, schema)
	if err != nil {
		return IndexInfo{}, err
	}
	defer releaseManaged()
	linked, releaseContext := linkContexts(ctx, manager.ctx, managed.ctx)
	defer releaseContext()
	snapshot, releaseSnapshot, err := managed.acquireSnapshot()
	if err != nil {
		return IndexInfo{}, err
	}
	if snapshot == nil {
		return IndexInfo{}, ErrIndexNotFound
	}
	defer releaseSnapshot()
	info := snapshot.index.Info()
	if err := snapshot.index.WriteSnapshot(linked, output); err != nil {
		return IndexInfo{}, err
	}
	return info, nil
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
	return OpenSnapshotContext(context.Background(), input, schema)
}

// OpenSnapshotContext opens a packed snapshot while honoring cancellation
// between its bounded immutable segments.
func OpenSnapshotContext(ctx context.Context, input *io.SectionReader, schema Schema) (*Index, error) {
	if ctx == nil {
		return nil, fmt.Errorf("search: nil snapshot context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manifest, offset, err := readPackedSnapshotManifest(input, schema)
	if err != nil {
		return nil, err
	}
	segments := len(manifest.Lengths)
	index := &Index{
		schema: schema, generation: manifest.Generation,
		entries:    make([]manifestSegment, 0, segments),
		segments:   make([]*Segment, 0, segments),
		deletions:  make([]*deletedDocuments, 0, segments),
		bases:      make([]uint64, 0, segments),
		fieldStats: make([]uint64, len(schema.fields)),
	}
	for _, length := range manifest.Lengths {
		if err := ctx.Err(); err != nil {
			_ = index.Close()
			return nil, err
		}
		if length < segmentHeaderSize || length > input.Size()-offset {
			_ = index.Close()
			return nil, corruptIndexf("invalid packed segment bounds")
		}
		segment, err := openSegmentReader("<packed>", io.NewSectionReader(input, offset, length), length, schema)
		if err != nil {
			_ = index.Close()
			return nil, err
		}
		index.bases = append(index.bases, index.documents)
		if index.documents > math.MaxUint64-uint64(segment.header.documentN) {
			_ = index.Close()
			return nil, corruptIndexf("packed document count overflow")
		}
		index.documents += uint64(segment.header.documentN)
		index.segments = append(index.segments, segment)
		index.deletions = append(index.deletions, nil)
		index.entries = append(index.entries, manifestSegment{documents: segment.header.documentN, bytes: uint64(length)})
		for i, total := range segment.fieldStats {
			if total > math.MaxUint64-index.fieldStats[i] {
				_ = index.Close()
				return nil, corruptIndexf("packed field statistics overflow")
			}
			index.fieldStats[i] += total
		}
		offset += length
	}
	if offset != input.Size() {
		_ = index.Close()
		return nil, corruptIndexf("trailing packed bytes")
	}
	index.physical, index.bytes = index.documents, input.Size()
	return index, nil
}

// InspectSnapshot validates the packed manifest, each fixed-size segment
// header, and sparse-directory prefix without loading dictionary entries or
// payloads. It is suitable for cold database admission; OpenSnapshot and Verify
// remain the query/deep paths.
func InspectSnapshot(ctx context.Context, input *io.SectionReader, schema Schema) (SnapshotInfo, error) {
	if ctx == nil {
		return SnapshotInfo{}, fmt.Errorf("search: nil snapshot context")
	}
	if err := ctx.Err(); err != nil {
		return SnapshotInfo{}, err
	}
	manifest, offset, err := readPackedSnapshotManifest(input, schema)
	if err != nil {
		return SnapshotInfo{}, err
	}
	info := SnapshotInfo{Generation: manifest.Generation, Segments: len(manifest.Lengths), Bytes: input.Size()}
	capacity := uint64(unsafe.Sizeof(Index{})) + uint64(len(schema.fields))*8 +
		maximumMultiFrequencyReaderCapacity()
	perSegment := uint64(unsafe.Sizeof(manifestSegment{})) +
		2*uint64(unsafe.Sizeof((*Segment)(nil))) + 8
	if uint64(len(manifest.Lengths)) > (math.MaxUint64-capacity)/perSegment {
		return SnapshotInfo{}, corruptIndexf("packed reader capacity overflow")
	}
	capacity += uint64(len(manifest.Lengths)) * perSegment
	for position, length := range manifest.Lengths {
		if err := ctx.Err(); err != nil {
			return SnapshotInfo{}, err
		}
		if length < segmentHeaderSize || length > input.Size()-offset {
			return SnapshotInfo{}, corruptIndexf("invalid packed segment bounds")
		}
		section := io.NewSectionReader(input, offset, length)
		var headerBytes [segmentHeaderSize]byte
		if err := readAtFull(section, headerBytes[:], 0); err != nil {
			return SnapshotInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		header, err := parseSegmentHeader(headerBytes[:], length)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		if err := validateSegmentHeaderShape(header, schema); err != nil {
			return SnapshotInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		segmentCapacity, err := inspectSegmentReaderCapacity(section, header)
		if err != nil {
			return SnapshotInfo{}, fmt.Errorf("search: inspect packed segment %d: %w", position, err)
		}
		if capacity > math.MaxUint64-segmentCapacity {
			return SnapshotInfo{}, corruptIndexf("packed reader capacity overflow")
		}
		capacity += segmentCapacity
		if info.Documents > math.MaxUint64-uint64(header.documentN) {
			return SnapshotInfo{}, corruptIndexf("packed document count overflow")
		}
		info.Documents += uint64(header.documentN)
		offset += length
	}
	if offset != input.Size() {
		return SnapshotInfo{}, corruptIndexf("trailing packed bytes")
	}
	if capacity > math.MaxInt64 {
		return SnapshotInfo{}, corruptIndexf("packed reader capacity exceeds platform range")
	}
	info.ReaderCapacityBytes = int64(capacity)
	return info, nil
}

func maximumMultiFrequencyReaderCapacity() uint64 {
	const maximumKeyBytes = maximumMultiFrequencyCacheTerm + maximumQueryFields*2 + 1
	return maximumMultiFrequencyCacheEntries *
		(uint64(unsafe.Sizeof("")) + 40 + maximumKeyBytes)
}

func inspectSegmentReaderCapacity(input io.ReaderAt, header segmentHeader) (uint64, error) {
	indexSection := header.sections[sectionDictionaryIndex]
	if indexSection.length < 8 {
		return 0, corruptf("dictionary index is truncated")
	}
	var prefix [8]byte
	if err := readAtFull(input, prefix[:], indexSection.offset); err != nil {
		return 0, err
	}
	if string(prefix[:4]) != string(dictionaryIndexMagic[:]) {
		return 0, corruptf("invalid dictionary index header")
	}
	count := uint64(binary.LittleEndian.Uint32(prefix[4:]))
	if count > (indexSection.length-8)/20 {
		return 0, corruptf("dictionary index count exceeds its section")
	}
	termBytes := indexSection.length - 8 - count*20
	capacity := uint64(unsafe.Sizeof(Segment{})) + uint64(header.fieldN)*8 +
		uint64(header.fieldN)*uint64(unsafe.Sizeof(normCache{})) + header.sections[sectionNorms].length
	entryBytes := uint64(unsafe.Sizeof(dictionaryBlockIndex{}))
	if count > (math.MaxUint64-capacity)/entryBytes {
		return 0, corruptf("dictionary reader capacity overflow")
	}
	capacity += count * entryBytes
	if capacity > math.MaxUint64-termBytes {
		return 0, corruptf("dictionary reader capacity overflow")
	}
	return capacity + termBytes, nil
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
