// Package pgimport streams resumable CSV and JSONL imports through KitDB's
// bounded PostgreSQL COPY profile.
package pgimport

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxSourceRecordBytes = 4 << 20

type hashField struct {
	kind byte
	data []byte
}

type sourceRecord struct {
	values []any
	fields []hashField
	end    uint64
}

type importSource interface {
	Columns() []string
	Offset() uint64
	Next() (sourceRecord, error)
	Close() error
}

type csvSource struct {
	file       *os.File
	reader     *csv.Reader
	columns    []string
	null       string
	nullSet    bool
	offset     uint64
	lastOffset uint64
}

func openCSVSource(path string, columns []string, header bool, delimiter rune, null string, nullSet bool) (*csvSource, error) {
	if delimiter == 0 {
		delimiter = ','
	}
	if delimiter == '\r' || delimiter == '\n' || delimiter == 0 || delimiter == utf8.RuneError {
		return nil, fmt.Errorf("pgimport: CSV delimiter must be one valid non-newline rune")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	reader := csv.NewReader(file)
	reader.Comma = delimiter
	reader.FieldsPerRecord = -1
	reader.ReuseRecord = false
	source := &csvSource{file: file, reader: reader, columns: append([]string(nil), columns...), null: null, nullSet: nullSet}
	if header {
		headerColumns, err := reader.Read()
		if err != nil {
			_ = file.Close()
			if errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("pgimport: CSV header is missing")
			}
			return nil, fmt.Errorf("pgimport: read CSV header: %w", err)
		}
		source.offset = uint64(reader.InputOffset())
		source.lastOffset = source.offset
		for index := range headerColumns {
			headerColumns[index] = strings.TrimSpace(headerColumns[index])
		}
		if len(source.columns) == 0 {
			source.columns = headerColumns
		} else if !equalColumnNames(source.columns, headerColumns) {
			_ = file.Close()
			return nil, fmt.Errorf("pgimport: CSV header does not match -columns")
		}
	}
	if err := validateColumns(source.columns); err != nil {
		_ = file.Close()
		return nil, err
	}
	return source, nil
}

func (source *csvSource) Columns() []string { return append([]string(nil), source.columns...) }
func (source *csvSource) Offset() uint64    { return source.offset }
func (source *csvSource) Close() error      { return source.file.Close() }

func (source *csvSource) Next() (sourceRecord, error) {
	fields, err := source.reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			source.offset = uint64(source.reader.InputOffset())
		}
		return sourceRecord{}, err
	}
	end := uint64(source.reader.InputOffset())
	if end < source.lastOffset || end-source.lastOffset > maxSourceRecordBytes {
		return sourceRecord{}, fmt.Errorf("pgimport: CSV record exceeds %d bytes", maxSourceRecordBytes)
	}
	source.lastOffset = end
	source.offset = end
	if len(fields) != len(source.columns) {
		return sourceRecord{}, fmt.Errorf(
			"pgimport: CSV row ending at byte %d has %d fields; expected %d",
			end, len(fields), len(source.columns),
		)
	}
	record := sourceRecord{values: make([]any, len(fields)), fields: make([]hashField, len(fields)), end: end}
	for index, field := range fields {
		if source.nullSet && field == source.null {
			record.fields[index] = hashField{kind: 0}
			continue
		}
		record.values[index] = field
		record.fields[index] = hashField{kind: 1, data: []byte(field)}
	}
	return record, nil
}

type jsonlSource struct {
	file       *os.File
	reader     *bufio.Reader
	columns    []string
	offset     uint64
	lastOffset uint64
}

func openJSONLSource(path string, columns []string) (*jsonlSource, error) {
	if err := validateColumns(columns); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return &jsonlSource{
		file: file, reader: bufio.NewReaderSize(file, 64<<10), columns: append([]string(nil), columns...),
	}, nil
}

func (source *jsonlSource) Columns() []string { return append([]string(nil), source.columns...) }
func (source *jsonlSource) Offset() uint64    { return source.offset }
func (source *jsonlSource) Close() error      { return source.file.Close() }

func (source *jsonlSource) Next() (sourceRecord, error) {
	for {
		line, consumed, err := readBoundedJSONLLine(source.reader)
		if consumed != 0 {
			source.offset += consumed
		}
		if err != nil {
			return sourceRecord{}, err
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if source.offset < source.lastOffset || source.offset-source.lastOffset > maxSourceRecordBytes {
			return sourceRecord{}, fmt.Errorf("pgimport: JSONL record exceeds %d bytes", maxSourceRecordBytes)
		}
		source.lastOffset = source.offset
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			return sourceRecord{}, fmt.Errorf("pgimport: invalid JSON object ending at byte %d: %w", source.offset, err)
		}
		if object == nil {
			return sourceRecord{}, fmt.Errorf("pgimport: JSONL value ending at byte %d is not an object", source.offset)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			if err == nil {
				return sourceRecord{}, fmt.Errorf("pgimport: JSONL line ending at byte %d has multiple values", source.offset)
			}
			return sourceRecord{}, fmt.Errorf("pgimport: invalid JSONL line ending at byte %d: %w", source.offset, err)
		}
		record := sourceRecord{
			values: make([]any, len(source.columns)), fields: make([]hashField, len(source.columns)), end: source.offset,
		}
		for index, column := range source.columns {
			value, found := object[column]
			if !found {
				value = nil
			}
			driverValue, field, err := canonicalJSONLValue(value)
			if err != nil {
				return sourceRecord{}, fmt.Errorf("pgimport: JSONL column %q ending at byte %d: %w", column, source.offset, err)
			}
			record.values[index] = driverValue
			record.fields[index] = field
		}
		return record, nil
	}
}

func readBoundedJSONLLine(reader *bufio.Reader) ([]byte, uint64, error) {
	line := make([]byte, 0, 1024)
	var consumed uint64
	for {
		fragment, err := reader.ReadSlice('\n')
		consumed += uint64(len(fragment))
		if len(line)+len(fragment) > maxSourceRecordBytes+2 {
			return nil, consumed, fmt.Errorf("pgimport: JSONL record exceeds %d bytes", maxSourceRecordBytes)
		}
		line = append(line, fragment...)
		switch {
		case err == nil:
			line = bytes.TrimSuffix(line, []byte{'\n'})
			line = bytes.TrimSuffix(line, []byte{'\r'})
			return line, consumed, nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF) && len(line) != 0:
			return bytes.TrimSuffix(line, []byte{'\r'}), consumed, nil
		case errors.Is(err, io.EOF):
			return nil, consumed, io.EOF
		default:
			return nil, consumed, err
		}
	}
}

func canonicalJSONLValue(value any) (any, hashField, error) {
	switch item := value.(type) {
	case nil:
		return nil, hashField{kind: 0}, nil
	case string:
		return item, hashField{kind: 1, data: []byte(item)}, nil
	case bool:
		data := []byte("false")
		if item {
			data = []byte("true")
		}
		return item, hashField{kind: 2, data: data}, nil
	case json.Number:
		if _, err := strconv.ParseFloat(item.String(), 64); err != nil {
			return nil, hashField{}, fmt.Errorf("invalid number %q", item)
		}
		return item.String(), hashField{kind: 3, data: []byte(item.String())}, nil
	case []any, map[string]any:
		encoded, err := json.Marshal(item)
		if err != nil {
			return nil, hashField{}, err
		}
		return string(encoded), hashField{kind: 4, data: encoded}, nil
	default:
		return nil, hashField{}, fmt.Errorf("unsupported value type %T", value)
	}
}

func validateColumns(columns []string) error {
	if len(columns) == 0 {
		return fmt.Errorf("pgimport: columns are required (or use a CSV header)")
	}
	seen := make(map[string]struct{}, len(columns))
	for index, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" || !utf8.ValidString(column) || strings.ContainsRune(column, 0) {
			return fmt.Errorf("pgimport: column %d is invalid", index)
		}
		key := strings.ToLower(column)
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("pgimport: duplicate column %q", column)
		}
		seen[key] = struct{}{}
		columns[index] = column
	}
	return nil
}

func equalColumnNames(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if !strings.EqualFold(strings.TrimSpace(left[index]), strings.TrimSpace(right[index])) {
			return false
		}
	}
	return true
}

func importSeed(id, source, format, table string, columns []string) [sha256.Size]byte {
	buffer := make([]byte, 0, 256)
	buffer = append(buffer, "KITDB-IMPORT-SHA256-V1"...)
	for _, part := range append([]string{id, source, format, table}, columns...) {
		buffer = binary.AppendUvarint(buffer, uint64(len(part)))
		buffer = append(buffer, part...)
	}
	return sha256.Sum256(buffer)
}

func advanceImportChecksum(previous [sha256.Size]byte, fields []hashField) [sha256.Size]byte {
	buffer := make([]byte, 0, sha256.Size+len(fields)*16)
	buffer = append(buffer, previous[:]...)
	buffer = binary.AppendUvarint(buffer, uint64(len(fields)))
	for _, field := range fields {
		buffer = append(buffer, field.kind)
		buffer = binary.AppendUvarint(buffer, uint64(len(field.data)))
		buffer = append(buffer, field.data...)
	}
	return sha256.Sum256(buffer)
}

func parseImportChecksum(checksum string) ([sha256.Size]byte, error) {
	var parsed [sha256.Size]byte
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != len(parsed) {
		return parsed, fmt.Errorf("pgimport: invalid stored SHA-256 checksum")
	}
	copy(parsed[:], decoded)
	return parsed, nil
}
