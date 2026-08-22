package search

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	dictionaryBlockMaxEntries   = 64
	dictionaryBlockMaxBytes     = 1 << 20
	dictionaryIndexMaxBytes     = 64 << 20
	dictionaryEntryFixedBytesV1 = 24
	dictionaryEntryFixedBytes   = 32
)

var dictionaryIndexMagic = [4]byte{'K', 'D', 'I', '1'}

type termKey struct {
	field uint16
	term  string
}

type termRecord struct {
	key            termKey
	documentFreq   uint32
	postingsOffset uint64
	postingsLength uint64
	maximumTF      uint32
	minimumNorm    uint32
}

type dictionaryBlockIndex struct {
	field       uint16
	firstTerm   string
	blockOffset uint64
	blockLength uint32
	blockCRC    uint32
}

func encodeDictionaryBlock(records []termRecord, version uint32) ([]byte, error) {
	if len(records) == 0 || len(records) > dictionaryBlockMaxEntries {
		return nil, fmt.Errorf("search: dictionary block has %d entries", len(records))
	}
	fixedBytes, err := dictionaryEntryBytes(version)
	if err != nil {
		return nil, err
	}
	field := records[0].key.field
	estimated := 4
	for _, record := range records {
		if record.key.field != field {
			return nil, fmt.Errorf("search: dictionary block crosses field boundary")
		}
		if version >= segmentVersionV2 && (record.maximumTF == 0 || record.minimumNorm == 0) {
			return nil, fmt.Errorf("search: dictionary term %q has no score bounds", record.key.term)
		}
		estimated += fixedBytes + len(record.key.term)
	}
	data := make([]byte, 4, estimated)
	binary.LittleEndian.PutUint16(data[:2], uint16(len(records)))
	previous := ""
	for index, record := range records {
		prefix := commonPrefixBytes(previous, record.key.term)
		if index == 0 {
			prefix = 0
		}
		suffix := record.key.term[prefix:]
		if prefix > 1<<16-1 || len(suffix) > 1<<16-1 {
			return nil, fmt.Errorf("search: dictionary term %q is too large", record.key.term)
		}
		base := len(data)
		data = append(data, make([]byte, fixedBytes)...)
		binary.LittleEndian.PutUint16(data[base:base+2], uint16(prefix))
		binary.LittleEndian.PutUint16(data[base+2:base+4], uint16(len(suffix)))
		binary.LittleEndian.PutUint32(data[base+4:base+8], record.documentFreq)
		binary.LittleEndian.PutUint64(data[base+8:base+16], record.postingsOffset)
		binary.LittleEndian.PutUint64(data[base+16:base+24], record.postingsLength)
		if version >= segmentVersionV2 {
			binary.LittleEndian.PutUint32(data[base+24:base+28], record.maximumTF)
			binary.LittleEndian.PutUint32(data[base+28:base+32], record.minimumNorm)
		}
		data = append(data, suffix...)
		previous = record.key.term
	}
	if len(data) > dictionaryBlockMaxBytes {
		return nil, fmt.Errorf("search: dictionary block exceeds %d bytes", dictionaryBlockMaxBytes)
	}
	return data, nil
}

func decodeDictionaryBlock(data []byte, version uint32, visit func(termRecord) bool) error {
	if len(data) < 4 {
		return corruptf("dictionary block is truncated")
	}
	fixedBytes, err := dictionaryEntryBytes(version)
	if err != nil {
		return err
	}
	count := int(binary.LittleEndian.Uint16(data[:2]))
	if count == 0 || count > dictionaryBlockMaxEntries {
		return corruptf("dictionary block entry count is %d", count)
	}
	data = data[4:]
	previous := ""
	for index := 0; index < count; index++ {
		if len(data) < fixedBytes {
			return corruptf("dictionary entry %d is truncated", index)
		}
		prefix := int(binary.LittleEndian.Uint16(data[:2]))
		suffixLength := int(binary.LittleEndian.Uint16(data[2:4]))
		documentFreq := binary.LittleEndian.Uint32(data[4:8])
		postingsOffset := binary.LittleEndian.Uint64(data[8:16])
		postingsLength := binary.LittleEndian.Uint64(data[16:24])
		var maximumTF, minimumNorm uint32
		if version >= segmentVersionV2 {
			maximumTF = binary.LittleEndian.Uint32(data[24:28])
			minimumNorm = binary.LittleEndian.Uint32(data[28:32])
		}
		data = data[fixedBytes:]
		if prefix > len(previous) || suffixLength > len(data) {
			return corruptf("dictionary entry %d has invalid prefix or suffix length", index)
		}
		if index == 0 && prefix != 0 {
			return corruptf("first dictionary entry has a shared prefix")
		}
		termBytes := make([]byte, prefix+suffixLength)
		copy(termBytes, previous[:prefix])
		copy(termBytes[prefix:], data[:suffixLength])
		data = data[suffixLength:]
		if len(termBytes) == 0 || !utf8.Valid(termBytes) {
			return corruptf("dictionary entry %d has an invalid term", index)
		}
		term := string(termBytes)
		if previous != "" && term <= previous {
			return corruptf("dictionary terms are not strictly increasing")
		}
		if documentFreq == 0 || postingsLength == 0 ||
			(version >= segmentVersionV2 && (maximumTF == 0 || minimumNorm == 0)) {
			return corruptf("dictionary term %q has empty postings metadata", term)
		}
		previous = term
		if visit != nil && !visit(termRecord{
			key:            termKey{term: term},
			documentFreq:   documentFreq,
			postingsOffset: postingsOffset,
			postingsLength: postingsLength,
			maximumTF:      maximumTF,
			minimumNorm:    minimumNorm,
		}) {
			return nil
		}
	}
	if len(data) != 0 {
		return corruptf("dictionary block has %d trailing bytes", len(data))
	}
	return nil
}

func lookupDictionaryBlock(data []byte, version uint32, target string) (termRecord, bool, error) {
	if len(data) < 4 {
		return termRecord{}, false, corruptf("dictionary block is truncated")
	}
	fixedBytes, err := dictionaryEntryBytes(version)
	if err != nil {
		return termRecord{}, false, err
	}
	count := int(binary.LittleEndian.Uint16(data[:2]))
	if count == 0 || count > dictionaryBlockMaxEntries {
		return termRecord{}, false, corruptf("dictionary block entry count is %d", count)
	}
	data = data[4:]
	previous := make([]byte, 0, 64)
	current := make([]byte, 0, 64)
	for index := 0; index < count; index++ {
		if len(data) < fixedBytes {
			return termRecord{}, false, corruptf("dictionary entry %d is truncated", index)
		}
		prefix := int(binary.LittleEndian.Uint16(data[:2]))
		suffixLength := int(binary.LittleEndian.Uint16(data[2:4]))
		documentFrequency := binary.LittleEndian.Uint32(data[4:8])
		postingsOffset := binary.LittleEndian.Uint64(data[8:16])
		postingsLength := binary.LittleEndian.Uint64(data[16:24])
		var maximumTF, minimumNorm uint32
		if version >= segmentVersionV2 {
			maximumTF = binary.LittleEndian.Uint32(data[24:28])
			minimumNorm = binary.LittleEndian.Uint32(data[28:32])
		}
		data = data[fixedBytes:]
		if prefix > len(previous) || suffixLength > len(data) || (index == 0 && prefix != 0) {
			return termRecord{}, false, corruptf("dictionary entry %d has invalid prefix or suffix length", index)
		}
		current = append(current[:0], previous[:prefix]...)
		current = append(current, data[:suffixLength]...)
		data = data[suffixLength:]
		if len(current) == 0 || !utf8.Valid(current) || (index > 0 && bytes.Compare(current, previous) <= 0) {
			return termRecord{}, false, corruptf("dictionary entry %d has an invalid or unordered term", index)
		}
		if documentFrequency == 0 || postingsLength == 0 ||
			(version >= segmentVersionV2 && (maximumTF == 0 || minimumNorm == 0)) {
			return termRecord{}, false, corruptf("dictionary term has empty postings metadata")
		}
		switch compareBytesString(current, target) {
		case 0:
			return termRecord{
				key: termKey{term: target}, documentFreq: documentFrequency,
				postingsOffset: postingsOffset, postingsLength: postingsLength,
				maximumTF: maximumTF, minimumNorm: minimumNorm,
			}, true, nil
		case 1:
			return termRecord{}, false, nil
		}
		previous, current = current, previous
	}
	if len(data) != 0 {
		return termRecord{}, false, corruptf("dictionary block has %d trailing bytes", len(data))
	}
	return termRecord{}, false, nil
}

func dictionaryEntryBytes(version uint32) (int, error) {
	switch version {
	case segmentVersionV1:
		return dictionaryEntryFixedBytesV1, nil
	case segmentVersionV2, segmentVersion:
		return dictionaryEntryFixedBytes, nil
	default:
		return 0, fmt.Errorf("%w: dictionary version %d", ErrUnsupportedVersion, version)
	}
}

func encodeDictionaryIndex(entries []dictionaryBlockIndex) ([]byte, error) {
	data := make([]byte, 8, 8+len(entries)*32)
	copy(data[:4], dictionaryIndexMagic[:])
	binary.LittleEndian.PutUint32(data[4:8], uint32(len(entries)))
	for _, entry := range entries {
		if entry.firstTerm == "" || len(entry.firstTerm) > 1<<16-1 {
			return nil, fmt.Errorf("search: invalid dictionary index term")
		}
		base := len(data)
		data = append(data, make([]byte, 20)...)
		binary.LittleEndian.PutUint16(data[base:base+2], entry.field)
		binary.LittleEndian.PutUint16(data[base+2:base+4], uint16(len(entry.firstTerm)))
		binary.LittleEndian.PutUint64(data[base+4:base+12], entry.blockOffset)
		binary.LittleEndian.PutUint32(data[base+12:base+16], entry.blockLength)
		binary.LittleEndian.PutUint32(data[base+16:base+20], entry.blockCRC)
		data = append(data, entry.firstTerm...)
	}
	return data, nil
}

func decodeDictionaryIndex(data []byte) ([]dictionaryBlockIndex, error) {
	if len(data) < 8 || !bytes.Equal(data[:4], dictionaryIndexMagic[:]) {
		return nil, corruptf("invalid dictionary index header")
	}
	count := uint64(binary.LittleEndian.Uint32(data[4:8]))
	data = data[8:]
	if count > uint64(len(data)/20) || count > uint64(maxIntValue()) {
		return nil, corruptf("dictionary index count %d exceeds its section", count)
	}
	entries := make([]dictionaryBlockIndex, 0, int(count))
	for index := 0; index < int(count); index++ {
		if len(data) < 20 {
			return nil, corruptf("dictionary index entry %d is truncated", index)
		}
		termLength := int(binary.LittleEndian.Uint16(data[2:4]))
		entry := dictionaryBlockIndex{
			field:       binary.LittleEndian.Uint16(data[:2]),
			blockOffset: binary.LittleEndian.Uint64(data[4:12]),
			blockLength: binary.LittleEndian.Uint32(data[12:16]),
			blockCRC:    binary.LittleEndian.Uint32(data[16:20]),
		}
		data = data[20:]
		if termLength == 0 || termLength > len(data) {
			return nil, corruptf("dictionary index entry %d has invalid term length", index)
		}
		entry.firstTerm = string(data[:termLength])
		data = data[termLength:]
		if !utf8.ValidString(entry.firstTerm) || entry.blockLength == 0 || entry.blockLength > dictionaryBlockMaxBytes {
			return nil, corruptf("dictionary index entry %d is invalid", index)
		}
		if len(entries) > 0 && compareDictionaryIndex(entries[len(entries)-1], entry) >= 0 {
			return nil, corruptf("dictionary index is not strictly increasing")
		}
		entries = append(entries, entry)
	}
	if len(data) != 0 {
		return nil, corruptf("dictionary index has %d trailing bytes", len(data))
	}
	return entries, nil
}

func findDictionaryBlock(entries []dictionaryBlockIndex, field uint16, term string) (dictionaryBlockIndex, bool) {
	position := sort.Search(len(entries), func(index int) bool {
		entry := entries[index]
		return entry.field > field || (entry.field == field && entry.firstTerm > term)
	}) - 1
	if position < 0 || entries[position].field != field {
		return dictionaryBlockIndex{}, false
	}
	return entries[position], true
}

func compareDictionaryIndex(left, right dictionaryBlockIndex) int {
	if left.field < right.field {
		return -1
	}
	if left.field > right.field {
		return 1
	}
	return stringsCompare(left.firstTerm, right.firstTerm)
}

func stringsCompare(left, right string) int {
	return strings.Compare(left, right)
}

func compareBytesString(left []byte, right string) int {
	maximum := min(len(left), len(right))
	for index := 0; index < maximum; index++ {
		if left[index] < right[index] {
			return -1
		}
		if left[index] > right[index] {
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func commonPrefixBytes(left, right string) int {
	maximum := min(len(left), len(right))
	index := 0
	for index < maximum && left[index] == right[index] {
		index++
	}
	return index
}

func dictionaryBlockChecksum(data []byte) uint32 {
	return crc32.Checksum(data, crcTable)
}
