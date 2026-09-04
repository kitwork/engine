package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sync"
	"sync/atomic"
)

type normCache struct {
	once sync.Once
	data []uint32
	err  error
}

// Segment is a concurrent read-only view of one immutable segment file.
// Close must run only after in-flight searches have drained.
type Segment struct {
	file       io.ReaderAt
	closer     io.Closer
	path       string
	schema     Schema
	header     segmentHeader
	fieldStats []uint64
	dictionary []dictionaryBlockIndex
	norms      []normCache
	closed     atomic.Bool
}

// OpenSegment validates the header, schema, field statistics, and sparse term
// index. Verify performs the optional full-file checksum and semantic scan.
func OpenSegment(path string, schema Schema) (*Segment, error) {
	if !schema.valid() {
		return nil, fmt.Errorf("search: invalid schema")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = file.Close()
		}
	}()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() < segmentHeaderSize {
		return nil, corruptf("segment is not a regular file or is too small")
	}
	segment, err := openSegmentReader(path, file, stat.Size(), schema)
	if err != nil {
		return nil, err
	}
	segment.closer = file
	closeOnError = false
	return segment, nil
}

func openSegmentReader(path string, file io.ReaderAt, size int64, schema Schema) (*Segment, error) {
	headerBytes := make([]byte, segmentHeaderSize)
	if err := readAtFull(file, headerBytes, 0); err != nil {
		return nil, err
	}
	header, err := parseSegmentHeader(headerBytes, size)
	if err != nil {
		return nil, err
	}
	if header.schemaHash != schema.fingerprint {
		return nil, ErrSchemaMismatch
	}
	if int(header.fieldN) != len(schema.fields) {
		return nil, corruptf("segment has %d fields; schema has %d", header.fieldN, len(schema.fields))
	}

	statsSection := header.sections[sectionFieldStats]
	expectedStatsLength := uint64(header.fieldN) * 8
	if statsSection.length != expectedStatsLength {
		return nil, corruptf("field statistics length is %d; expected %d", statsSection.length, expectedStatsLength)
	}
	statsData, err := readCheckedSection(file, statsSection, expectedStatsLength)
	if err != nil {
		return nil, err
	}
	fieldStats := make([]uint64, header.fieldN)
	for index := range fieldStats {
		fieldStats[index] = binary.LittleEndian.Uint64(statsData[index*8 : index*8+8])
	}

	indexSection := header.sections[sectionDictionaryIndex]
	indexData, err := readCheckedSection(file, indexSection, dictionaryIndexMaxBytes)
	if err != nil {
		return nil, err
	}
	dictionary, err := decodeDictionaryIndex(indexData)
	if err != nil {
		return nil, err
	}
	blocksSection := header.sections[sectionDictionaryBlocks]
	blocksEnd := blocksSection.offset + blocksSection.length
	expectedBlockOffset := blocksSection.offset
	for index, block := range dictionary {
		if block.field >= header.fieldN {
			return nil, corruptf("dictionary block %d references field %d", index, block.field)
		}
		if block.blockOffset != expectedBlockOffset || block.blockOffset > blocksEnd || uint64(block.blockLength) > blocksEnd-block.blockOffset {
			return nil, corruptf("dictionary block %d exceeds or leaves a gap in its section", index)
		}
		expectedBlockOffset += uint64(block.blockLength)
	}
	if expectedBlockOffset != blocksEnd {
		return nil, corruptf("dictionary blocks do not cover their section")
	}

	normsLength, overflow := multiplyUint64(uint64(header.documentN), uint64(header.fieldN), 4)
	if overflow || header.sections[sectionNorms].length != normsLength {
		return nil, corruptf("norm section has invalid length")
	}
	offsetsLength, overflow := multiplyUint64(uint64(header.documentN)+1, 8)
	if overflow || header.sections[sectionStoredOffsets].length != offsetsLength {
		return nil, corruptf("stored offset section has invalid length")
	}

	segment := &Segment{
		file: file, path: path, schema: schema, header: header,
		fieldStats: fieldStats, dictionary: dictionary, norms: make([]normCache, header.fieldN),
	}
	return segment, nil
}

// Info reports stable segment metadata.
func (segment *Segment) Info() SegmentInfo {
	return SegmentInfo{
		Path: segment.path, Documents: segment.header.documentN,
		Fields: segment.header.fieldN, Bytes: int64(segment.header.fileSize),
	}
}

// Close releases the file handle. The caller must drain searches first.
func (segment *Segment) Close() error {
	if segment == nil || !segment.closed.CompareAndSwap(false, true) {
		return nil
	}
	if segment.closer != nil {
		return segment.closer.Close()
	}
	return nil
}

func (segment *Segment) ensureOpen() error {
	if segment == nil || segment.closed.Load() {
		return ErrClosed
	}
	return nil
}

func (segment *Segment) lookupTerm(field uint16, term string) (termRecord, bool, error) {
	var buffer []byte
	return segment.lookupTermBuffered(field, term, &buffer)
}

func (segment *Segment) lookupTermBuffered(field uint16, term string, buffer *[]byte) (termRecord, bool, error) {
	if err := segment.ensureOpen(); err != nil {
		return termRecord{}, false, err
	}
	block, exists := findDictionaryBlock(segment.dictionary, field, term)
	if !exists {
		return termRecord{}, false, nil
	}
	if cap(*buffer) < int(block.blockLength) {
		*buffer = make([]byte, block.blockLength)
	} else {
		*buffer = (*buffer)[:block.blockLength]
	}
	data := *buffer
	if err := readAtFull(segment.file, data, block.blockOffset); err != nil {
		return termRecord{}, false, err
	}
	if got := dictionaryBlockChecksum(data); got != block.blockCRC {
		return termRecord{}, false, corruptf("dictionary block checksum is %08x; expected %08x", got, block.blockCRC)
	}
	record, found, err := lookupDictionaryBlock(data, segment.header.version, term)
	if err != nil || !found {
		return termRecord{}, found, err
	}
	record.key.field = field
	postings := segment.header.sections[sectionPostings]
	postingsEnd := postings.offset + postings.length
	if record.postingsOffset < postings.offset || record.postingsOffset > postingsEnd || record.postingsLength > postingsEnd-record.postingsOffset {
		return termRecord{}, false, corruptf("term %q postings exceed their section", term)
	}
	return record, true, nil
}

func (segment *Segment) fieldNorms(field uint16) ([]uint32, error) {
	if err := segment.ensureOpen(); err != nil {
		return nil, err
	}
	if field >= segment.header.fieldN {
		return nil, fmt.Errorf("search: field %d is out of range", field)
	}
	cache := &segment.norms[field]
	cache.once.Do(func() {
		length := uint64(segment.header.documentN) * 4
		if length > uint64(maxIntValue()) {
			cache.err = corruptf("field norm data exceeds platform range")
			return
		}
		encoded := make([]byte, int(length))
		offset := segment.header.sections[sectionNorms].offset + uint64(field)*length
		if err := readAtFull(segment.file, encoded, offset); err != nil {
			cache.err = err
			return
		}
		cache.data = make([]uint32, segment.header.documentN)
		for index := range cache.data {
			cache.data[index] = binary.LittleEndian.Uint32(encoded[index*4 : index*4+4])
		}
	})
	return cache.data, cache.err
}

func (segment *Segment) fieldNorm(field uint16, document uint32) (uint32, error) {
	if err := segment.ensureOpen(); err != nil {
		return 0, err
	}
	if field >= segment.header.fieldN || document >= segment.header.documentN {
		return 0, corruptf("field norm position (%d, %d) is out of range", field, document)
	}
	var encoded [4]byte
	offset := segment.header.sections[sectionNorms].offset +
		(uint64(field)*uint64(segment.header.documentN)+uint64(document))*4
	if err := readAtFull(segment.file, encoded[:], offset); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(encoded[:]), nil
}

func (segment *Segment) documentIdentifier(document uint32) (string, error) {
	if err := segment.ensureOpen(); err != nil {
		return "", err
	}
	if document >= segment.header.documentN {
		return "", corruptf("document %d exceeds segment bounds", document)
	}
	section := segment.header.sections[sectionStoredOffsets]
	var encoded [16]byte
	if err := readAtFull(segment.file, encoded[:], section.offset+uint64(document)*8); err != nil {
		return "", err
	}
	start := binary.LittleEndian.Uint64(encoded[:8])
	end := binary.LittleEndian.Uint64(encoded[8:])
	dataSection := segment.header.sections[sectionStoredData]
	if start > end || end > dataSection.length || end-start > uint64(maxIntValue()) {
		return "", corruptf("document %d has invalid stored offsets", document)
	}
	data := make([]byte, int(end-start))
	if err := readAtFull(segment.file, data, dataSection.offset+start); err != nil {
		return "", err
	}
	return string(data), nil
}

func (segment *Segment) documentIdentifierHasPrefix(
	document uint32,
	prefix string,
	buffer *[]byte,
) (bool, error) {
	if prefix == "" {
		return true, nil
	}
	if err := segment.ensureOpen(); err != nil {
		return false, err
	}
	if document >= segment.header.documentN {
		return false, corruptf("document %d exceeds segment bounds", document)
	}
	section := segment.header.sections[sectionStoredOffsets]
	var encoded [16]byte
	if err := readAtFull(segment.file, encoded[:], section.offset+uint64(document)*8); err != nil {
		return false, err
	}
	start := binary.LittleEndian.Uint64(encoded[:8])
	end := binary.LittleEndian.Uint64(encoded[8:])
	dataSection := segment.header.sections[sectionStoredData]
	if start > end || end > dataSection.length || end-start > uint64(maxIntValue()) {
		return false, corruptf("document %d has invalid stored offsets", document)
	}
	if end-start < uint64(len(prefix)) {
		return false, nil
	}
	if cap(*buffer) < len(prefix) {
		*buffer = make([]byte, len(prefix))
	} else {
		*buffer = (*buffer)[:len(prefix)]
	}
	if err := readAtFull(segment.file, *buffer, dataSection.offset+start); err != nil {
		return false, err
	}
	return bytes.Equal(*buffer, []byte(prefix)), nil
}

// Verify reads every section, dictionary block, posting list, norm, and stored
// offset. It is intended for tooling and backup validation, not request paths.
func (segment *Segment) Verify(ctx context.Context) error {
	if err := segment.ensureOpen(); err != nil {
		return err
	}
	if ctx == nil {
		return fmt.Errorf("search: verify context is nil")
	}
	for index, section := range segment.header.sections {
		if err := verifySectionChecksum(ctx, segment.file, section); err != nil {
			return fmt.Errorf("search: verify section %d: %w", index, err)
		}
	}

	expectedPostingsOffset := segment.header.sections[sectionPostings].offset
	for blockIndex, block := range segment.dictionary {
		if blockIndex&63 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		data := make([]byte, block.blockLength)
		if err := readAtFull(segment.file, data, block.blockOffset); err != nil {
			return err
		}
		if got := dictionaryBlockChecksum(data); got != block.blockCRC {
			return corruptf("dictionary block checksum is %08x; expected %08x", got, block.blockCRC)
		}
		first := true
		var verifyErr error
		decodeErr := decodeDictionaryBlock(data, segment.header.version, func(record termRecord) bool {
			if first && record.key.term != block.firstTerm {
				verifyErr = corruptf("dictionary block %d first term does not match its index", blockIndex)
				return false
			}
			first = false
			record.key.field = block.field
			if record.postingsOffset != expectedPostingsOffset {
				verifyErr = corruptf("term %q leaves a gap in postings", record.key.term)
				return false
			}
			expectedPostingsOffset += record.postingsLength
			iterator, iteratorErr := newPostingIterator(
				segment.file, segment.header.version, record, segment.header.documentN,
				segment.header.sections[sectionPostings], false,
			)
			if iteratorErr != nil {
				verifyErr = iteratorErr
				return false
			}
			norms, normErr := segment.fieldNorms(record.key.field)
			if normErr != nil {
				verifyErr = normErr
				return false
			}
			iterator.norms = norms
			for {
				more, iteratorErr := iterator.Next()
				if iteratorErr != nil {
					verifyErr = iteratorErr
					return false
				}
				if !more {
					break
				}
			}
			return true
		})
		if decodeErr != nil {
			return decodeErr
		}
		if verifyErr != nil {
			return verifyErr
		}
		if first {
			return corruptf("dictionary block %d first term does not match its index", blockIndex)
		}
	}
	if expectedPostingsOffset != segment.header.sections[sectionPostings].offset+segment.header.sections[sectionPostings].length {
		return corruptf("dictionary does not cover postings section")
	}

	var storedOffset uint64
	var encoded [8]byte
	offsets := segment.header.sections[sectionStoredOffsets]
	for document := uint64(0); document <= uint64(segment.header.documentN); document++ {
		if document&8191 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := readAtFull(segment.file, encoded[:], offsets.offset+document*8); err != nil {
			return err
		}
		current := binary.LittleEndian.Uint64(encoded[:])
		if current < storedOffset || current > segment.header.sections[sectionStoredData].length {
			return corruptf("stored offsets are not monotonic")
		}
		storedOffset = current
	}
	if storedOffset != segment.header.sections[sectionStoredData].length {
		return corruptf("stored offsets do not cover stored data")
	}
	return segment.verifyNormTotals(ctx)
}

func (segment *Segment) verifyNormTotals(ctx context.Context) error {
	for field := uint16(0); field < segment.header.fieldN; field++ {
		norms, err := segment.fieldNorms(field)
		if err != nil {
			return err
		}
		var total uint64
		for index, norm := range norms {
			total += uint64(norm)
			if index&8191 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
		}
		if total != segment.fieldStats[field] {
			return corruptf("field %d token total is %d; expected %d", field, total, segment.fieldStats[field])
		}
	}
	return nil
}

func verifySectionChecksum(ctx context.Context, file io.ReaderAt, section sectionDescriptor) error {
	hash := crc32.New(crcTable)
	buffer := make([]byte, 64<<10)
	remaining := section.length
	offset := section.offset
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := min(uint64(len(buffer)), remaining)
		if err := readAtFull(file, buffer[:chunk], offset); err != nil {
			return err
		}
		_, _ = hash.Write(buffer[:chunk])
		offset += chunk
		remaining -= chunk
	}
	if got := hash.Sum32(); got != section.checksum {
		return corruptf("section checksum is %08x; expected %08x", got, section.checksum)
	}
	return nil
}

func multiplyUint64(values ...uint64) (uint64, bool) {
	result := uint64(1)
	for _, value := range values {
		if value != 0 && result > math.MaxUint64/value {
			return 0, true
		}
		result *= value
	}
	return result, false
}

var _ io.Closer = (*Segment)(nil)
