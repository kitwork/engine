package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
)

const (
	identifierIndexVersion         = uint32(1)
	identifierIndexHeaderSize      = 96
	identifierIndexEntrySize       = 20
	identifierIndexBodyCRCOffset   = 64
	identifierIndexHeaderCRCOffset = 68
)

var identifierIndexMagic = [8]byte{'K', 'W', 'I', 'D', '0', '0', '0', '1'}

type identifierIndexEntry struct {
	hash     [16]byte
	document uint32
}

type identifierIndexInfo struct {
	Path      string
	Documents uint32
	Bytes     int64
}

type identifierIndex struct {
	file       *os.File
	path       string
	documents  uint32
	bodyCRC    uint32
	bodyLength uint64
	closed     atomic.Bool
}

func writeIdentifierIndex(ctx context.Context, path string, schemaHash [32]byte, identifiers []string) (identifierIndexInfo, error) {
	return writeIdentifierIndexEntries(ctx, path, schemaHash, uint32(len(identifiers)), func(document uint32) (string, error) {
		return identifiers[document], nil
	})
}

func writeIdentifierIndexForSegment(ctx context.Context, path string, segment *Segment) (identifierIndexInfo, error) {
	if err := segment.ensureOpen(); err != nil {
		return identifierIndexInfo{}, err
	}
	return writeIdentifierIndexEntries(ctx, path, segment.schema.fingerprint, segment.header.documentN, segment.documentIdentifier)
}

func writeIdentifierIndexEntries(
	ctx context.Context,
	path string,
	schemaHash [32]byte,
	documents uint32,
	identifier func(uint32) (string, error),
) (identifierIndexInfo, error) {
	if ctx == nil {
		return identifierIndexInfo{}, fmt.Errorf("search: identifier index context is nil")
	}
	if err := ctx.Err(); err != nil {
		return identifierIndexInfo{}, err
	}
	if documents == 0 {
		return identifierIndexInfo{}, fmt.Errorf("search: identifier index requires documents")
	}
	if uint64(documents) > uint64(maxIntValue()/identifierIndexEntrySize) {
		return identifierIndexInfo{}, fmt.Errorf("search: identifier index exceeds platform memory range")
	}
	entries := make([]identifierIndexEntry, documents)
	for document := uint32(0); document < documents; document++ {
		if document&8191 == 0 {
			if err := ctx.Err(); err != nil {
				return identifierIndexInfo{}, err
			}
		}
		value, err := identifier(document)
		if err != nil {
			return identifierIndexInfo{}, err
		}
		entries[document] = identifierIndexEntry{hash: identifierHash(value), document: document}
	}
	sort.Slice(entries, func(i, j int) bool {
		if comparison := bytes.Compare(entries[i].hash[:], entries[j].hash[:]); comparison != 0 {
			return comparison < 0
		}
		return entries[i].document < entries[j].document
	})

	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return identifierIndexInfo{}, fmt.Errorf("search: identifier index path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return identifierIndexInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return identifierIndexInfo{}, err
	}
	temporary, err := temporarySegmentPath(path)
	if err != nil {
		return identifierIndexInfo{}, err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return identifierIndexInfo{}, err
	}
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(make([]byte, identifierIndexHeaderSize)); err != nil {
		return identifierIndexInfo{}, err
	}
	hash := crc32.New(crcTable)
	destination := io.MultiWriter(file, hash)
	buffer := make([]byte, 0, 32<<10)
	for position, entry := range entries {
		buffer = append(buffer, entry.hash[:]...)
		buffer = binary.LittleEndian.AppendUint32(buffer, entry.document)
		if len(buffer) >= 32<<10 {
			if _, err := destination.Write(buffer); err != nil {
				return identifierIndexInfo{}, err
			}
			buffer = buffer[:0]
		}
		if position&8191 == 0 {
			if err := ctx.Err(); err != nil {
				return identifierIndexInfo{}, err
			}
		}
	}
	if len(buffer) != 0 {
		if _, err := destination.Write(buffer); err != nil {
			return identifierIndexInfo{}, err
		}
	}
	fileSize, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return identifierIndexInfo{}, err
	}
	header := marshalIdentifierIndexHeader(schemaHash, documents, uint64(fileSize), hash.Sum32())
	if _, err := file.WriteAt(header[:], 0); err != nil {
		return identifierIndexInfo{}, err
	}
	if err := file.Sync(); err != nil {
		return identifierIndexInfo{}, err
	}
	if err := file.Close(); err != nil {
		return identifierIndexInfo{}, err
	}
	if err := publishFile(temporary, path); err != nil {
		return identifierIndexInfo{}, err
	}
	keepTemporary = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return identifierIndexInfo{}, err
	}
	return identifierIndexInfo{Path: path, Documents: documents, Bytes: fileSize}, nil
}

func marshalIdentifierIndexHeader(schemaHash [32]byte, documents uint32, fileSize uint64, bodyCRC uint32) [identifierIndexHeaderSize]byte {
	var data [identifierIndexHeaderSize]byte
	copy(data[:8], identifierIndexMagic[:])
	binary.LittleEndian.PutUint32(data[8:12], identifierIndexVersion)
	binary.LittleEndian.PutUint32(data[12:16], identifierIndexHeaderSize)
	binary.LittleEndian.PutUint64(data[16:24], fileSize)
	copy(data[24:56], schemaHash[:])
	binary.LittleEndian.PutUint32(data[56:60], documents)
	binary.LittleEndian.PutUint32(data[60:64], identifierIndexEntrySize)
	binary.LittleEndian.PutUint32(data[identifierIndexBodyCRCOffset:identifierIndexBodyCRCOffset+4], bodyCRC)
	binary.LittleEndian.PutUint32(data[identifierIndexHeaderCRCOffset:identifierIndexHeaderCRCOffset+4], 0)
	binary.LittleEndian.PutUint32(data[identifierIndexHeaderCRCOffset:identifierIndexHeaderCRCOffset+4], crc32.Checksum(data[:], crcTable))
	return data
}

func openIdentifierIndex(path string, schemaHash [32]byte, documents uint32, expectedBytes uint64) (*identifierIndex, error) {
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
	if !stat.Mode().IsRegular() || stat.Size() < identifierIndexHeaderSize || uint64(stat.Size()) != expectedBytes {
		return nil, corruptIndexf("identifier index has an invalid file size")
	}
	var encoded [identifierIndexHeaderSize]byte
	if err := readAtFull(file, encoded[:], 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded[:8], identifierIndexMagic[:]) {
		return nil, corruptIndexf("identifier index has invalid magic")
	}
	if version := binary.LittleEndian.Uint32(encoded[8:12]); version != identifierIndexVersion {
		return nil, fmt.Errorf("%w: identifier index version %d", ErrUnsupportedVersion, version)
	}
	if size := binary.LittleEndian.Uint32(encoded[12:16]); size != identifierIndexHeaderSize {
		return nil, corruptIndexf("identifier index header size is %d", size)
	}
	if size := binary.LittleEndian.Uint64(encoded[16:24]); size != uint64(stat.Size()) {
		return nil, corruptIndexf("identifier index records %d bytes; actual size is %d", size, stat.Size())
	}
	if !bytes.Equal(encoded[24:56], schemaHash[:]) || binary.LittleEndian.Uint32(encoded[56:60]) != documents {
		return nil, corruptIndexf("identifier index metadata does not match its segment")
	}
	if size := binary.LittleEndian.Uint32(encoded[60:64]); size != identifierIndexEntrySize {
		return nil, corruptIndexf("identifier index entry size is %d", size)
	}
	if !allZero(encoded[72:]) {
		return nil, corruptIndexf("identifier index contains unsupported header flags")
	}
	wantHeaderCRC := binary.LittleEndian.Uint32(encoded[identifierIndexHeaderCRCOffset : identifierIndexHeaderCRCOffset+4])
	header := encoded
	binary.LittleEndian.PutUint32(header[identifierIndexHeaderCRCOffset:identifierIndexHeaderCRCOffset+4], 0)
	if got := crc32.Checksum(header[:], crcTable); got != wantHeaderCRC {
		return nil, corruptIndexf("identifier index header checksum is %08x; expected %08x", got, wantHeaderCRC)
	}
	bodyLength, overflow := multiplyUint64(uint64(documents), identifierIndexEntrySize)
	if overflow || bodyLength != uint64(stat.Size())-identifierIndexHeaderSize {
		return nil, corruptIndexf("identifier index body has an invalid length")
	}
	index := &identifierIndex{
		file: file, path: path, documents: documents,
		bodyCRC:    binary.LittleEndian.Uint32(encoded[identifierIndexBodyCRCOffset : identifierIndexBodyCRCOffset+4]),
		bodyLength: bodyLength,
	}
	closeOnError = false
	return index, nil
}

func (index *identifierIndex) Find(ctx context.Context, segment *Segment, identifier string) (uint32, bool, error) {
	if index == nil || index.closed.Load() {
		return 0, false, ErrClosed
	}
	if ctx == nil {
		return 0, false, fmt.Errorf("search: identifier lookup context is nil")
	}
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	target := identifierHash(identifier)
	low, high := uint32(0), index.documents
	var encoded [identifierIndexEntrySize]byte
	for low < high {
		middle := low + (high-low)/2
		if err := index.readEntry(encoded[:], middle); err != nil {
			return 0, false, err
		}
		if bytes.Compare(encoded[:16], target[:]) < 0 {
			low = middle + 1
		} else {
			high = middle
		}
	}
	for position := low; position < index.documents; position++ {
		if err := index.readEntry(encoded[:], position); err != nil {
			return 0, false, err
		}
		comparison := bytes.Compare(encoded[:16], target[:])
		if comparison != 0 {
			return 0, false, nil
		}
		document := binary.LittleEndian.Uint32(encoded[16:20])
		if document >= index.documents {
			return 0, false, corruptIndexf("identifier index document %d is out of range", document)
		}
		actual, err := segment.documentIdentifier(document)
		if err != nil {
			return 0, false, err
		}
		if actual == identifier {
			return document, true, nil
		}
		if position&31 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, false, err
			}
		}
	}
	return 0, false, nil
}

func (index *identifierIndex) Verify(ctx context.Context, segment *Segment) error {
	if index == nil || index.closed.Load() {
		return ErrClosed
	}
	if ctx == nil {
		return fmt.Errorf("search: identifier verify context is nil")
	}
	if err := verifySectionChecksum(ctx, index.file, sectionDescriptor{
		offset: identifierIndexHeaderSize, length: index.bodyLength, checksum: index.bodyCRC,
	}); err != nil {
		return fmt.Errorf("search: verify identifier index: %w", err)
	}
	var previous identifierIndexEntry
	var encoded [identifierIndexEntrySize]byte
	for position := uint32(0); position < index.documents; position++ {
		if position&8191 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := index.readEntry(encoded[:], position); err != nil {
			return err
		}
		var current identifierIndexEntry
		copy(current.hash[:], encoded[:16])
		current.document = binary.LittleEndian.Uint32(encoded[16:20])
		if current.document >= index.documents || (position > 0 && compareIdentifierIndexEntry(previous, current) >= 0) {
			return corruptIndexf("identifier index entry %d is invalid or unordered", position)
		}
		identifier, err := segment.documentIdentifier(current.document)
		if err != nil {
			return err
		}
		if identifierHash(identifier) != current.hash {
			return corruptIndexf("identifier index entry %d has the wrong hash", position)
		}
		previous = current
	}
	return nil
}

func (index *identifierIndex) readEntry(destination []byte, position uint32) error {
	if position >= index.documents || len(destination) != identifierIndexEntrySize {
		return corruptIndexf("identifier index position %d is out of range", position)
	}
	offset := uint64(identifierIndexHeaderSize) + uint64(position)*identifierIndexEntrySize
	return readAtFull(index.file, destination, offset)
}

func (index *identifierIndex) Close() error {
	if index == nil || !index.closed.CompareAndSwap(false, true) {
		return nil
	}
	return index.file.Close()
}

func identifierHash(identifier string) [16]byte {
	complete := sha256.Sum256([]byte(identifier))
	var result [16]byte
	copy(result[:], complete[:16])
	return result
}

func compareIdentifierIndexEntry(left, right identifierIndexEntry) int {
	if comparison := bytes.Compare(left.hash[:], right.hash[:]); comparison != 0 {
		return comparison
	}
	if left.document < right.document {
		return -1
	}
	if left.document > right.document {
		return 1
	}
	return 0
}

var _ io.Closer = (*identifierIndex)(nil)

func identifierIndexFileSize(documents uint32) (uint64, error) {
	body, overflow := multiplyUint64(uint64(documents), identifierIndexEntrySize)
	if overflow || body > math.MaxUint64-identifierIndexHeaderSize {
		return 0, corruptIndexf("identifier index size overflows")
	}
	return identifierIndexHeaderSize + body, nil
}
