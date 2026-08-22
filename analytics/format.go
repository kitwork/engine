package analytics

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"
)

const (
	segmentFileVersion    = uint32(1)
	segmentFileHeaderSize = 256
	segmentFileCRCOffset  = 120
)

var (
	segmentFileMagic = [8]byte{'K', 'A', 'N', 'A', 'L', 'Y', 'Z', '1'}
	crcTable         = crc32.MakeTable(crc32.Castagnoli)
)

type segmentSection struct {
	offset   uint64
	length   uint64
	checksum uint32
}

type segmentFileHeader struct {
	version    uint32
	fileSize   uint64
	schemaHash [32]byte
	rows       uint64
	columns    uint32
	sections   [2]segmentSection
}

func marshalSegmentFileHeader(header segmentFileHeader) [segmentFileHeaderSize]byte {
	var data [segmentFileHeaderSize]byte
	copy(data[:8], segmentFileMagic[:])
	binary.LittleEndian.PutUint32(data[8:12], header.version)
	binary.LittleEndian.PutUint32(data[12:16], segmentFileHeaderSize)
	binary.LittleEndian.PutUint64(data[16:24], header.fileSize)
	copy(data[24:56], header.schemaHash[:])
	binary.LittleEndian.PutUint64(data[56:64], header.rows)
	binary.LittleEndian.PutUint32(data[64:68], header.columns)
	for index, section := range header.sections {
		base := 72 + index*24
		binary.LittleEndian.PutUint64(data[base:base+8], section.offset)
		binary.LittleEndian.PutUint64(data[base+8:base+16], section.length)
		binary.LittleEndian.PutUint32(data[base+16:base+20], section.checksum)
	}
	binary.LittleEndian.PutUint32(data[segmentFileCRCOffset:segmentFileCRCOffset+4], 0)
	checksum := crc32.Checksum(data[:], crcTable)
	binary.LittleEndian.PutUint32(data[segmentFileCRCOffset:segmentFileCRCOffset+4], checksum)
	return data
}

func parseSegmentFileHeader(data []byte, actualSize int64) (segmentFileHeader, error) {
	if len(data) != segmentFileHeaderSize {
		return segmentFileHeader{}, corruptf("header has %d bytes; expected %d", len(data), segmentFileHeaderSize)
	}
	if !bytes.Equal(data[:8], segmentFileMagic[:]) {
		return segmentFileHeader{}, corruptf("invalid analytics segment magic")
	}
	version := binary.LittleEndian.Uint32(data[8:12])
	if version != segmentFileVersion {
		return segmentFileHeader{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, version, segmentFileVersion)
	}
	if size := binary.LittleEndian.Uint32(data[12:16]); size != segmentFileHeaderSize {
		return segmentFileHeader{}, corruptf("header size is %d; expected %d", size, segmentFileHeaderSize)
	}
	wantCRC := binary.LittleEndian.Uint32(data[segmentFileCRCOffset : segmentFileCRCOffset+4])
	copyForCRC := append([]byte(nil), data...)
	binary.LittleEndian.PutUint32(copyForCRC[segmentFileCRCOffset:segmentFileCRCOffset+4], 0)
	if got := crc32.Checksum(copyForCRC, crcTable); got != wantCRC {
		return segmentFileHeader{}, corruptf("header checksum is %08x; expected %08x", got, wantCRC)
	}
	header := segmentFileHeader{
		version:  version,
		fileSize: binary.LittleEndian.Uint64(data[16:24]),
		rows:     binary.LittleEndian.Uint64(data[56:64]),
		columns:  binary.LittleEndian.Uint32(data[64:68]),
	}
	copy(header.schemaHash[:], data[24:56])
	for index := range header.sections {
		base := 72 + index*24
		header.sections[index] = segmentSection{
			offset:   binary.LittleEndian.Uint64(data[base : base+8]),
			length:   binary.LittleEndian.Uint64(data[base+8 : base+16]),
			checksum: binary.LittleEndian.Uint32(data[base+16 : base+20]),
		}
	}
	if actualSize < 0 || header.fileSize != uint64(actualSize) {
		return segmentFileHeader{}, corruptf("recorded file size is %d; actual size is %d", header.fileSize, actualSize)
	}
	if err := validateSegmentSections(header); err != nil {
		return segmentFileHeader{}, err
	}
	return header, nil
}

func validateSegmentSections(header segmentFileHeader) error {
	previousEnd := uint64(segmentFileHeaderSize)
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

func encodeSchemaSection(schema Schema) []byte {
	buffer := bytes.NewBuffer(nil)
	var scratch [8]byte
	binary.LittleEndian.PutUint32(scratch[:4], uint32(len(schema.Columns)))
	_, _ = buffer.Write(scratch[:4])
	for _, column := range schema.Columns {
		binary.LittleEndian.PutUint16(scratch[:2], uint16(len(column.Name)))
		_, _ = buffer.Write(scratch[:2])
		_, _ = buffer.WriteString(column.Name)
		scratch[0] = byte(column.Kind)
		if column.Nullable {
			scratch[1] = 1
		} else {
			scratch[1] = 0
		}
		_, _ = buffer.Write(scratch[:2])
	}
	return buffer.Bytes()
}

func decodeSchemaSection(data []byte) (Schema, error) {
	reader := bytes.NewReader(data)
	var count32 uint32
	if err := binary.Read(reader, binary.LittleEndian, &count32); err != nil {
		return Schema{}, fmt.Errorf("analytics: decode schema count: %w", err)
	}
	columns := make([]Column, 0, count32)
	for position := uint32(0); position < count32; position++ {
		var nameLength uint16
		if err := binary.Read(reader, binary.LittleEndian, &nameLength); err != nil {
			return Schema{}, fmt.Errorf("analytics: decode schema name length: %w", err)
		}
		name := make([]byte, nameLength)
		if _, err := io.ReadFull(reader, name); err != nil {
			return Schema{}, fmt.Errorf("analytics: decode schema name: %w", err)
		}
		var kindByte [1]byte
		if _, err := io.ReadFull(reader, kindByte[:]); err != nil {
			return Schema{}, fmt.Errorf("analytics: decode schema kind: %w", err)
		}
		var nullableByte [1]byte
		if _, err := io.ReadFull(reader, nullableByte[:]); err != nil {
			return Schema{}, fmt.Errorf("analytics: decode schema nullable flag: %w", err)
		}
		columns = append(columns, Column{
			Name:     string(name),
			Kind:     Kind(kindByte[0]),
			Nullable: nullableByte[0] != 0,
		})
	}
	if reader.Len() != 0 {
		return Schema{}, fmt.Errorf("analytics: schema section has %d trailing bytes", reader.Len())
	}
	return NewSchema(columns...)
}

func encodeValue(kind Kind, value any) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	switch kind {
	case KindBool:
		if value == nil {
			buffer.WriteByte(0)
			return buffer.Bytes(), nil
		}
		buffer.WriteByte(1)
		if value.(bool) {
			buffer.WriteByte(1)
		} else {
			buffer.WriteByte(0)
		}
	case KindInt64:
		if value == nil {
			buffer.WriteByte(0)
			return buffer.Bytes(), nil
		}
		buffer.WriteByte(2)
		var scratch [8]byte
		binary.LittleEndian.PutUint64(scratch[:], uint64(value.(int64)))
		_, _ = buffer.Write(scratch[:])
	case KindFloat64:
		if value == nil {
			buffer.WriteByte(0)
			return buffer.Bytes(), nil
		}
		buffer.WriteByte(3)
		var scratch [8]byte
		binary.LittleEndian.PutUint64(scratch[:], math.Float64bits(value.(float64)))
		_, _ = buffer.Write(scratch[:])
	case KindText:
		if value == nil {
			buffer.WriteByte(0)
			return buffer.Bytes(), nil
		}
		buffer.WriteByte(4)
		text := []byte(value.(string))
		var scratch [4]byte
		binary.LittleEndian.PutUint32(scratch[:], uint32(len(text)))
		_, _ = buffer.Write(scratch[:])
		_, _ = buffer.Write(text)
	case KindTime:
		if value == nil {
			buffer.WriteByte(0)
			return buffer.Bytes(), nil
		}
		buffer.WriteByte(5)
		var scratch [8]byte
		binary.LittleEndian.PutUint64(scratch[:], uint64(value.(time.Time).UTC().UnixNano()))
		_, _ = buffer.Write(scratch[:])
	default:
		return nil, fmt.Errorf("%w: unsupported column kind %s", ErrTypeMismatch, kind)
	}
	return buffer.Bytes(), nil
}

func decodeValue(kind Kind, reader *bytes.Reader) (any, error) {
	tag, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if tag == 0 {
		return nil, nil
	}
	switch kind {
	case KindBool:
		if tag != 1 {
			return nil, corruptf("bool tag is %d", tag)
		}
		value, err := reader.ReadByte()
		if err != nil {
			return nil, err
		}
		return value != 0, nil
	case KindInt64:
		if tag != 2 {
			return nil, corruptf("int64 tag is %d", tag)
		}
		var scratch [8]byte
		if _, err := io.ReadFull(reader, scratch[:]); err != nil {
			return nil, err
		}
		return int64(binary.LittleEndian.Uint64(scratch[:])), nil
	case KindFloat64:
		if tag != 3 {
			return nil, corruptf("float64 tag is %d", tag)
		}
		var scratch [8]byte
		if _, err := io.ReadFull(reader, scratch[:]); err != nil {
			return nil, err
		}
		return math.Float64frombits(binary.LittleEndian.Uint64(scratch[:])), nil
	case KindText:
		if tag != 4 {
			return nil, corruptf("text tag is %d", tag)
		}
		var length32 uint32
		if err := binary.Read(reader, binary.LittleEndian, &length32); err != nil {
			return nil, err
		}
		text := make([]byte, length32)
		if _, err := io.ReadFull(reader, text); err != nil {
			return nil, err
		}
		return string(text), nil
	case KindTime:
		if tag != 5 {
			return nil, corruptf("time tag is %d", tag)
		}
		var scratch [8]byte
		if _, err := io.ReadFull(reader, scratch[:]); err != nil {
			return nil, err
		}
		return time.Unix(0, int64(binary.LittleEndian.Uint64(scratch[:]))).UTC(), nil
	default:
		return nil, fmt.Errorf("%w: unsupported column kind %s", ErrTypeMismatch, kind)
	}
}

func writeSegmentFile(path string, segment *Segment) error {
	if segment == nil {
		return fmt.Errorf("analytics: nil segment")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()

	schemaSection := encodeSchemaSection(segment.schema)
	dataSection, err := encodeSegmentRows(segment)
	if err != nil {
		return err
	}

	var header segmentFileHeader
	header.version = segmentFileVersion
	header.rows = uint64(segment.rows)
	header.columns = uint32(len(segment.schema.Columns))
	header.schemaHash = segment.schema.Fingerprint()
	header.sections[0] = segmentSection{
		offset:   segmentFileHeaderSize,
		length:   uint64(len(schemaSection)),
		checksum: crc32.Checksum(schemaSection, crcTable),
	}
	header.sections[1] = segmentSection{
		offset:   segmentFileHeaderSize + uint64(len(schemaSection)),
		length:   uint64(len(dataSection)),
		checksum: crc32.Checksum(dataSection, crcTable),
	}
	header.fileSize = header.sections[1].offset + header.sections[1].length
	head := marshalSegmentFileHeader(header)

	if _, err := temporary.Write(head[:]); err != nil {
		return err
	}
	if _, err := temporary.Write(schemaSection); err != nil {
		return err
	}
	if _, err := temporary.Write(dataSection); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return nil
}

func encodeSegmentRows(segment *Segment) ([]byte, error) {
	buffer := bytes.NewBuffer(nil)
	var scratch [8]byte
	binary.LittleEndian.PutUint64(scratch[:], uint64(segment.rows))
	_, _ = buffer.Write(scratch[:])
	for row := 0; row < segment.rows; row++ {
		for index, column := range segment.schema.Columns {
			value := segment.cols[index].values[row]
			encoded, err := encodeValue(column.Kind, value)
			if err != nil {
				return nil, err
			}
			_, _ = buffer.Write(encoded)
		}
	}
	return buffer.Bytes(), nil
}

func loadSegmentFile(path string, expectedSchema Schema) (*Segment, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	headerBytes := make([]byte, segmentFileHeaderSize)
	if _, err := io.ReadFull(file, headerBytes); err != nil {
		return nil, err
	}
	header, err := parseSegmentFileHeader(headerBytes, stat.Size())
	if err != nil {
		return nil, err
	}
	if header.schemaHash != expectedSchema.Fingerprint() {
		return nil, fmt.Errorf("%w: schema fingerprint mismatch for %s", ErrInvalidSchema, path)
	}

	schemaSection := make([]byte, header.sections[0].length)
	if _, err := file.ReadAt(schemaSection, int64(header.sections[0].offset)); err != nil {
		return nil, err
	}
	if got := crc32.Checksum(schemaSection, crcTable); got != header.sections[0].checksum {
		return nil, corruptf("schema section checksum is %08x; expected %08x", got, header.sections[0].checksum)
	}
	decodedSchema, err := decodeSchemaSection(schemaSection)
	if err != nil {
		return nil, err
	}
	if decodedSchema.Fingerprint() != expectedSchema.Fingerprint() {
		return nil, fmt.Errorf("%w: decoded schema mismatch for %s", ErrInvalidSchema, path)
	}

	dataSection := make([]byte, header.sections[1].length)
	if _, err := file.ReadAt(dataSection, int64(header.sections[1].offset)); err != nil {
		return nil, err
	}
	if got := crc32.Checksum(dataSection, crcTable); got != header.sections[1].checksum {
		return nil, corruptf("data section checksum is %08x; expected %08x", got, header.sections[1].checksum)
	}

	reader := bytes.NewReader(dataSection)
	var rowCount uint64
	if err := binary.Read(reader, binary.LittleEndian, &rowCount); err != nil {
		return nil, err
	}
	if rowCount != header.rows {
		return nil, corruptf("row count is %d; expected %d", rowCount, header.rows)
	}

	builder := NewBuilder(decodedSchema)
	for row := uint64(0); row < rowCount; row++ {
		current := make(Row, len(decodedSchema.Columns))
		for _, column := range decodedSchema.Columns {
			value, err := decodeValue(column.Kind, reader)
			if err != nil {
				return nil, err
			}
			current[column.Name] = value
		}
		if err := builder.Add(current); err != nil {
			return nil, err
		}
	}
	if reader.Len() != 0 {
		return nil, corruptf("data section has %d trailing bytes", reader.Len())
	}
	return builder.Build()
}
