package search

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
)

const (
	segmentVersionV1  = uint32(1)
	segmentVersionV2  = uint32(2)
	segmentVersion    = uint32(3)
	segmentHeaderSize = 256
	sectionCount      = 7
	headerCRCOffset   = 232
)

const (
	sectionFieldStats = iota
	sectionPostings
	sectionDictionaryBlocks
	sectionDictionaryIndex
	sectionNorms
	sectionStoredOffsets
	sectionStoredData
)

var (
	segmentMagic = [8]byte{'K', 'W', 'S', 'E', 'G', '0', '0', '1'}
	crcTable     = crc32.MakeTable(crc32.Castagnoli)
)

type sectionDescriptor struct {
	offset   uint64
	length   uint64
	checksum uint32
}

type segmentHeader struct {
	version     uint32
	fileSize    uint64
	schemaHash  [32]byte
	documentN   uint32
	fieldN      uint16
	sections    [sectionCount]sectionDescriptor
	headerBytes [segmentHeaderSize]byte
}

func marshalSegmentHeader(header segmentHeader) [segmentHeaderSize]byte {
	var data [segmentHeaderSize]byte
	copy(data[:8], segmentMagic[:])
	version := header.version
	if version == 0 {
		version = segmentVersion
	}
	binary.LittleEndian.PutUint32(data[8:12], version)
	binary.LittleEndian.PutUint32(data[12:16], segmentHeaderSize)
	binary.LittleEndian.PutUint64(data[16:24], header.fileSize)
	copy(data[24:56], header.schemaHash[:])
	binary.LittleEndian.PutUint32(data[56:60], header.documentN)
	binary.LittleEndian.PutUint16(data[60:62], header.fieldN)

	for i, section := range header.sections {
		base := 64 + i*24
		binary.LittleEndian.PutUint64(data[base:base+8], section.offset)
		binary.LittleEndian.PutUint64(data[base+8:base+16], section.length)
		binary.LittleEndian.PutUint32(data[base+16:base+20], section.checksum)
	}
	binary.LittleEndian.PutUint32(data[headerCRCOffset:headerCRCOffset+4], 0)
	checksum := crc32.Checksum(data[:], crcTable)
	binary.LittleEndian.PutUint32(data[headerCRCOffset:headerCRCOffset+4], checksum)
	return data
}

func parseSegmentHeader(data []byte, actualSize int64) (segmentHeader, error) {
	if len(data) != segmentHeaderSize {
		return segmentHeader{}, corruptf("header has %d bytes; expected %d", len(data), segmentHeaderSize)
	}
	if !bytes.Equal(data[:8], segmentMagic[:]) {
		return segmentHeader{}, corruptf("invalid segment magic")
	}
	version := binary.LittleEndian.Uint32(data[8:12])
	if version != segmentVersionV1 && version != segmentVersionV2 && version != segmentVersion {
		return segmentHeader{}, fmt.Errorf("%w: got %d, support %d through %d", ErrUnsupportedVersion, version, segmentVersionV1, segmentVersion)
	}
	if size := binary.LittleEndian.Uint32(data[12:16]); size != segmentHeaderSize {
		return segmentHeader{}, corruptf("header size is %d; expected %d", size, segmentHeaderSize)
	}

	wantCRC := binary.LittleEndian.Uint32(data[headerCRCOffset : headerCRCOffset+4])
	copyForCRC := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(copyForCRC[headerCRCOffset:headerCRCOffset+4], 0)
	if got := crc32.Checksum(copyForCRC, crcTable); got != wantCRC {
		return segmentHeader{}, corruptf("header checksum is %08x; expected %08x", got, wantCRC)
	}

	header := segmentHeader{
		version:   version,
		fileSize:  binary.LittleEndian.Uint64(data[16:24]),
		documentN: binary.LittleEndian.Uint32(data[56:60]),
		fieldN:    binary.LittleEndian.Uint16(data[60:62]),
	}
	copy(header.schemaHash[:], data[24:56])
	copy(header.headerBytes[:], data)
	if actualSize < 0 || header.fileSize != uint64(actualSize) {
		return segmentHeader{}, corruptf("recorded file size is %d; actual size is %d", header.fileSize, actualSize)
	}
	for i := range header.sections {
		base := 64 + i*24
		header.sections[i] = sectionDescriptor{
			offset:   binary.LittleEndian.Uint64(data[base : base+8]),
			length:   binary.LittleEndian.Uint64(data[base+8 : base+16]),
			checksum: binary.LittleEndian.Uint32(data[base+16 : base+20]),
		}
	}
	if err := validateSections(header); err != nil {
		return segmentHeader{}, err
	}
	return header, nil
}

func validateSections(header segmentHeader) error {
	previousEnd := uint64(segmentHeaderSize)
	for index, section := range header.sections {
		if section.offset < previousEnd {
			return corruptf("section %d overlaps preceding data", index)
		}
		if section.offset > header.fileSize || section.length > header.fileSize-section.offset {
			return corruptf("section %d exceeds file bounds", index)
		}
		previousEnd = section.offset + section.length
	}
	if previousEnd != header.fileSize {
		return corruptf("last section ends at %d; file ends at %d", previousEnd, header.fileSize)
	}
	return nil
}

func readCheckedSection(file *os.File, section sectionDescriptor, maximum uint64) ([]byte, error) {
	if section.length > maximum || section.length > uint64(maxIntValue()) {
		return nil, corruptf("section length %d exceeds limit %d", section.length, maximum)
	}
	data := make([]byte, int(section.length))
	if err := readAtFull(file, data, section.offset); err != nil {
		return nil, err
	}
	if got := crc32.Checksum(data, crcTable); got != section.checksum {
		return nil, corruptf("section checksum is %08x; expected %08x", got, section.checksum)
	}
	return data, nil
}

func readAtFull(file *os.File, destination []byte, offset uint64) error {
	if offset > math.MaxInt64 {
		return corruptf("file offset %d exceeds platform range", offset)
	}
	if len(destination) == 0 {
		return nil
	}
	read, err := file.ReadAt(destination, int64(offset))
	if err != nil && err != io.EOF {
		return err
	}
	if read != len(destination) {
		return corruptf("short read at offset %d: got %d bytes, expected %d", offset, read, len(destination))
	}
	return nil
}

func corruptf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrCorruptSegment, fmt.Sprintf(format, arguments...))
}

func maxIntValue() int {
	return int(^uint(0) >> 1)
}

func allZero(data []byte) bool {
	for _, value := range data {
		if value != 0 {
			return false
		}
	}
	return true
}
