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

const identifierReadAheadBytes = 4 << 10

type sectionReadAhead struct {
	data   []byte
	offset uint64
	valid  bool
}

// documentIdentifierPrefixReader keeps the two monotonically accessed stored
// sections query-local. It avoids one positioned read for every candidate
// without retaining document identifiers in the immutable segment reader.
type documentIdentifierPrefixReader struct {
	ctx     context.Context
	segment *Segment
	offsets sectionReadAhead
	data    sectionReadAhead
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
	normBytes  atomic.Int64
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
	if err := validateSegmentHeaderShape(header, schema); err != nil {
		return nil, err
	}

	statsSection := header.sections[sectionFieldStats]
	expectedStatsLength := uint64(header.fieldN) * 8
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

	segment := &Segment{
		file: file, path: path, schema: schema, header: header,
		fieldStats: fieldStats, dictionary: dictionary, norms: make([]normCache, header.fieldN),
	}
	return segment, nil
}

// validateSegmentHeaderShape performs the fixed-size validation shared by a
// query reader and the bounded packed-snapshot inspector. It deliberately does
// not read payload sections or allocate the sparse term dictionary.
func validateSegmentHeaderShape(header segmentHeader, schema Schema) error {
	if header.schemaHash != schema.fingerprint {
		return ErrSchemaMismatch
	}
	if int(header.fieldN) != len(schema.fields) {
		return corruptf("segment has %d fields; schema has %d", header.fieldN, len(schema.fields))
	}

	statsSection := header.sections[sectionFieldStats]
	expectedStatsLength := uint64(header.fieldN) * 8
	if statsSection.length != expectedStatsLength {
		return corruptf("field statistics length is %d; expected %d", statsSection.length, expectedStatsLength)
	}

	normsLength, overflow := multiplyUint64(uint64(header.documentN), uint64(header.fieldN), 4)
	if overflow || header.sections[sectionNorms].length != normsLength {
		return corruptf("norm section has invalid length")
	}
	offsetsLength, overflow := multiplyUint64(uint64(header.documentN)+1, 8)
	if overflow || header.sections[sectionStoredOffsets].length != offsetsLength {
		return corruptf("stored offset section has invalid length")
	}
	return nil
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
		segment.normBytes.Add(int64(len(cache.data)) * 4)
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

func newDocumentIdentifierPrefixReader(
	ctx context.Context,
	segment *Segment,
) documentIdentifierPrefixReader {
	reader := documentIdentifierPrefixReader{}
	reader.reset(ctx, segment)
	return reader
}

func (reader *documentIdentifierPrefixReader) reset(ctx context.Context, segment *Segment) {
	reader.ctx = ctx
	reader.segment = segment
	reader.offsets.reset()
	reader.data.reset()
}

func (reader *sectionReadAhead) reset() {
	reader.data = reader.data[:0]
	reader.offset = 0
	reader.valid = false
}

func (reader *documentIdentifierPrefixReader) hasPrefix(document uint32, prefix string) (bool, error) {
	if prefix == "" {
		return true, nil
	}
	if reader == nil || reader.ctx == nil || reader.segment == nil {
		return false, fmt.Errorf("search: invalid identifier prefix reader")
	}
	segment := reader.segment
	if err := segment.ensureOpen(); err != nil {
		return false, err
	}
	if err := reader.ctx.Err(); err != nil {
		return false, err
	}
	if document >= segment.header.documentN {
		return false, corruptf("document %d exceeds segment bounds", document)
	}
	offsetSection := segment.header.sections[sectionStoredOffsets]
	encoded, err := reader.offsets.read(
		reader.ctx, segment.file, offsetSection, uint64(document)*8, 16,
	)
	if err != nil {
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
	data, err := reader.data.read(reader.ctx, segment.file, dataSection, start, uint64(len(prefix)))
	if err != nil {
		return false, err
	}
	return bytes.Equal(data, []byte(prefix)), nil
}

func (reader *sectionReadAhead) read(
	ctx context.Context,
	file io.ReaderAt,
	section sectionDescriptor,
	offset uint64,
	length uint64,
) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if length == 0 {
		return nil, nil
	}
	if offset > section.length || length > section.length-offset || length > uint64(maxIntValue()) {
		return nil, corruptf("read-ahead range exceeds its section")
	}
	if reader.valid && offset >= reader.offset {
		relative := offset - reader.offset
		if relative <= uint64(len(reader.data)) && length <= uint64(len(reader.data))-relative {
			return reader.data[relative : relative+length], nil
		}
	}
	remaining := section.length - offset
	window := min(uint64(identifierReadAheadBytes), remaining)
	if window < length {
		window = length
	}
	if window > uint64(maxIntValue()) {
		return nil, corruptf("read-ahead window exceeds platform range")
	}
	if cap(reader.data) < int(window) {
		reader.data = make([]byte, int(window))
	} else {
		reader.data = reader.data[:int(window)]
	}
	reader.valid = false
	if err := readAtFull(file, reader.data, section.offset+offset); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	reader.offset = offset
	reader.valid = true
	return reader.data[:length], nil
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
				ctx, segment.file, segment.header.version, record, segment.header.documentN,
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
