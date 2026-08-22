package search

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const (
	deletionVersion         = uint32(1)
	deletionHeaderSize      = 96
	deletionBodyCRCOffset   = 72
	deletionHeaderCRCOffset = 76
)

var deletionMagic = [8]byte{'K', 'W', 'D', 'E', 'L', '0', '0', '1'}

type deletedDocuments struct {
	documents   uint32
	ids         []uint32
	bits        []byte
	tokenTotals []uint64
}

type deletionInfo struct {
	Path    string
	Deleted uint32
	Bytes   int64
}

func newDeletedDocuments(documents uint32, fields int, ids []uint32, tokenTotals []uint64) (*deletedDocuments, error) {
	if documents == 0 || fields < 1 || len(tokenTotals) != fields || len(ids) > int(documents) {
		return nil, corruptIndexf("deletion metadata is inconsistent")
	}
	bitsLength := (uint64(documents) + 7) / 8
	if bitsLength > uint64(maxIntValue()) {
		return nil, corruptIndexf("deletion bitset exceeds platform range")
	}
	deleted := &deletedDocuments{
		documents: documents, ids: append([]uint32(nil), ids...),
		bits: make([]byte, int(bitsLength)), tokenTotals: append([]uint64(nil), tokenTotals...),
	}
	for position, document := range deleted.ids {
		if document >= documents || (position > 0 && deleted.ids[position-1] >= document) {
			return nil, corruptIndexf("deleted document IDs are invalid or unordered")
		}
		deleted.bits[document>>3] |= byte(1 << (document & 7))
	}
	return deleted, nil
}

func (deleted *deletedDocuments) Contains(document uint32) bool {
	return deleted != nil && document < deleted.documents && deleted.bits[document>>3]&(1<<(document&7)) != 0
}

func (deleted *deletedDocuments) Count() uint32 {
	if deleted == nil {
		return 0
	}
	return uint32(len(deleted.ids))
}

func (deleted *deletedDocuments) clone() *deletedDocuments {
	if deleted == nil {
		return nil
	}
	return &deletedDocuments{
		documents: deleted.documents,
		ids:       append([]uint32(nil), deleted.ids...), bits: append([]byte(nil), deleted.bits...),
		tokenTotals: append([]uint64(nil), deleted.tokenTotals...),
	}
}

func (deleted *deletedDocuments) Verify(ctx context.Context, segment *Segment) error {
	if deleted == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("search: deletion verify context is nil")
	}
	if err := segment.ensureOpen(); err != nil {
		return err
	}
	if deleted.documents != segment.header.documentN || len(deleted.tokenTotals) != int(segment.header.fieldN) {
		return corruptIndexf("deletion metadata does not match its segment")
	}
	totals := make([]uint64, segment.header.fieldN)
	for field := uint16(0); field < segment.header.fieldN; field++ {
		norms, err := segment.fieldNorms(field)
		if err != nil {
			return err
		}
		for position, document := range deleted.ids {
			if position&8191 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			norm := norms[document]
			if totals[field] > math.MaxUint64-uint64(norm) {
				return corruptIndexf("deletion token total overflows for field %d", field)
			}
			totals[field] += uint64(norm)
		}
	}
	if !equalUint64s(totals, deleted.tokenTotals) {
		return corruptIndexf("deletion token totals do not match their documents")
	}
	return nil
}

func writeDeletionFile(ctx context.Context, path string, schemaHash [32]byte, deleted *deletedDocuments) (deletionInfo, error) {
	if ctx == nil {
		return deletionInfo{}, fmt.Errorf("search: deletion context is nil")
	}
	if err := ctx.Err(); err != nil {
		return deletionInfo{}, err
	}
	if deleted == nil || deleted.Count() == 0 || len(deleted.tokenTotals) == 0 {
		return deletionInfo{}, fmt.Errorf("search: deletion file requires deleted documents")
	}
	bodyLength, overflow := deletionBodyLength(deleted.documents, uint32(len(deleted.ids)), uint16(len(deleted.tokenTotals)))
	if overflow || bodyLength > uint64(maxIntValue()) || bodyLength > math.MaxInt64-deletionHeaderSize {
		return deletionInfo{}, fmt.Errorf("search: deletion file exceeds platform range")
	}
	body := make([]byte, 0, int(bodyLength))
	for _, total := range deleted.tokenTotals {
		body = binary.LittleEndian.AppendUint64(body, total)
	}
	for _, document := range deleted.ids {
		body = binary.LittleEndian.AppendUint32(body, document)
	}
	body = append(body, deleted.bits...)
	if uint64(len(body)) != bodyLength {
		return deletionInfo{}, fmt.Errorf("search: deletion body length is inconsistent")
	}

	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return deletionInfo{}, fmt.Errorf("search: deletion path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return deletionInfo{}, err
	}
	temporary, err := temporarySegmentPath(path)
	if err != nil {
		return deletionInfo{}, err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return deletionInfo{}, err
	}
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporary)
		}
	}()
	fileSize := uint64(deletionHeaderSize) + bodyLength
	header := marshalDeletionHeader(
		schemaHash, deleted.documents, uint32(len(deleted.ids)), uint16(len(deleted.tokenTotals)),
		fileSize, crc32.Checksum(body, crcTable),
	)
	if _, err := file.Write(header[:]); err != nil {
		return deletionInfo{}, err
	}
	written, err := io.Copy(file, bytes.NewReader(body))
	if err != nil {
		return deletionInfo{}, err
	}
	if written != int64(len(body)) {
		return deletionInfo{}, io.ErrShortWrite
	}
	if err := ctx.Err(); err != nil {
		return deletionInfo{}, err
	}
	if err := file.Sync(); err != nil {
		return deletionInfo{}, err
	}
	if err := file.Close(); err != nil {
		return deletionInfo{}, err
	}
	if err := publishFile(temporary, path); err != nil {
		return deletionInfo{}, err
	}
	keepTemporary = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return deletionInfo{}, err
	}
	return deletionInfo{Path: path, Deleted: uint32(len(deleted.ids)), Bytes: int64(fileSize)}, nil
}

func marshalDeletionHeader(
	schemaHash [32]byte,
	documents, deleted uint32,
	fields uint16,
	fileSize uint64,
	bodyCRC uint32,
) [deletionHeaderSize]byte {
	var data [deletionHeaderSize]byte
	copy(data[:8], deletionMagic[:])
	binary.LittleEndian.PutUint32(data[8:12], deletionVersion)
	binary.LittleEndian.PutUint32(data[12:16], deletionHeaderSize)
	binary.LittleEndian.PutUint64(data[16:24], fileSize)
	copy(data[24:56], schemaHash[:])
	binary.LittleEndian.PutUint32(data[56:60], documents)
	binary.LittleEndian.PutUint32(data[60:64], deleted)
	binary.LittleEndian.PutUint16(data[64:66], fields)
	binary.LittleEndian.PutUint32(data[deletionBodyCRCOffset:deletionBodyCRCOffset+4], bodyCRC)
	binary.LittleEndian.PutUint32(data[deletionHeaderCRCOffset:deletionHeaderCRCOffset+4], 0)
	binary.LittleEndian.PutUint32(data[deletionHeaderCRCOffset:deletionHeaderCRCOffset+4], crc32.Checksum(data[:], crcTable))
	return data
}

func openDeletionFile(
	path string,
	schemaHash [32]byte,
	documents, expectedDeleted uint32,
	fields uint16,
	expectedBytes uint64,
) (*deletedDocuments, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !stat.Mode().IsRegular() || stat.Size() < deletionHeaderSize || uint64(stat.Size()) != expectedBytes {
		return nil, corruptIndexf("deletion file has an invalid size")
	}
	var encoded [deletionHeaderSize]byte
	if err := readAtFull(file, encoded[:], 0); err != nil {
		return nil, err
	}
	if !bytes.Equal(encoded[:8], deletionMagic[:]) {
		return nil, corruptIndexf("deletion file has invalid magic")
	}
	if version := binary.LittleEndian.Uint32(encoded[8:12]); version != deletionVersion {
		return nil, fmt.Errorf("%w: deletion version %d", ErrUnsupportedVersion, version)
	}
	if size := binary.LittleEndian.Uint32(encoded[12:16]); size != deletionHeaderSize {
		return nil, corruptIndexf("deletion header size is %d", size)
	}
	if size := binary.LittleEndian.Uint64(encoded[16:24]); size != uint64(stat.Size()) {
		return nil, corruptIndexf("deletion file records %d bytes; actual size is %d", size, stat.Size())
	}
	deleted := binary.LittleEndian.Uint32(encoded[60:64])
	if !bytes.Equal(encoded[24:56], schemaHash[:]) || binary.LittleEndian.Uint32(encoded[56:60]) != documents ||
		deleted != expectedDeleted || binary.LittleEndian.Uint16(encoded[64:66]) != fields ||
		binary.LittleEndian.Uint16(encoded[66:68]) != 0 || binary.LittleEndian.Uint32(encoded[68:72]) != 0 ||
		!allZero(encoded[80:]) {
		return nil, corruptIndexf("deletion metadata does not match its segment")
	}
	wantHeaderCRC := binary.LittleEndian.Uint32(encoded[deletionHeaderCRCOffset : deletionHeaderCRCOffset+4])
	header := encoded
	binary.LittleEndian.PutUint32(header[deletionHeaderCRCOffset:deletionHeaderCRCOffset+4], 0)
	if got := crc32.Checksum(header[:], crcTable); got != wantHeaderCRC {
		return nil, corruptIndexf("deletion header checksum is %08x; expected %08x", got, wantHeaderCRC)
	}
	bodyLength, overflow := deletionBodyLength(documents, deleted, fields)
	if overflow || bodyLength != uint64(stat.Size())-deletionHeaderSize || bodyLength > uint64(maxIntValue()) {
		return nil, corruptIndexf("deletion body has an invalid length")
	}
	body := make([]byte, int(bodyLength))
	if err := readAtFull(file, body, deletionHeaderSize); err != nil {
		return nil, err
	}
	wantBodyCRC := binary.LittleEndian.Uint32(encoded[deletionBodyCRCOffset : deletionBodyCRCOffset+4])
	if got := crc32.Checksum(body, crcTable); got != wantBodyCRC {
		return nil, corruptIndexf("deletion body checksum is %08x; expected %08x", got, wantBodyCRC)
	}
	tokenTotals := make([]uint64, fields)
	for field := range tokenTotals {
		tokenTotals[field] = binary.LittleEndian.Uint64(body[field*8 : field*8+8])
	}
	body = body[int(fields)*8:]
	ids := make([]uint32, deleted)
	for position := range ids {
		ids[position] = binary.LittleEndian.Uint32(body[position*4 : position*4+4])
	}
	body = body[int(deleted)*4:]
	result, err := newDeletedDocuments(documents, int(fields), ids, tokenTotals)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(result.bits, body) {
		return nil, corruptIndexf("deletion bitset does not match its document IDs")
	}
	return result, nil
}

func deletionBodyLength(documents, deleted uint32, fields uint16) (uint64, bool) {
	fieldBytes, overflow := multiplyUint64(uint64(fields), 8)
	if overflow {
		return 0, true
	}
	idBytes, overflow := multiplyUint64(uint64(deleted), 4)
	if overflow || fieldBytes > math.MaxUint64-idBytes {
		return 0, true
	}
	bitsBytes := (uint64(documents) + 7) / 8
	length := fieldBytes + idBytes
	if length > math.MaxUint64-bitsBytes {
		return 0, true
	}
	return length + bitsBytes, false
}

func mergeDeletedDocumentIDs(existing []uint32, additions map[uint32]struct{}) []uint32 {
	orderedAdditions := make([]uint32, 0, len(additions))
	for document := range additions {
		orderedAdditions = append(orderedAdditions, document)
	}
	sort.Slice(orderedAdditions, func(left, right int) bool {
		return orderedAdditions[left] < orderedAdditions[right]
	})
	merged := make([]uint32, 0, len(existing)+len(orderedAdditions))
	left, right := 0, 0
	for left < len(existing) && right < len(orderedAdditions) {
		switch {
		case existing[left] < orderedAdditions[right]:
			merged = append(merged, existing[left])
			left++
		case orderedAdditions[right] < existing[left]:
			merged = append(merged, orderedAdditions[right])
			right++
		default:
			merged = append(merged, existing[left])
			left++
			right++
		}
	}
	merged = append(merged, existing[left:]...)
	merged = append(merged, orderedAdditions[right:]...)
	return merged
}

func equalUint64s(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
