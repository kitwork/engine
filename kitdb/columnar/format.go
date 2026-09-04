package columnar

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"math/bits"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	formatVersion1       = 1
	formatVersion2       = 2
	formatVersion3       = 3
	columnStatisticsSize = 40
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

type Writer struct {
	output       io.Writer
	fields       []Field
	buffer       []byte
	header       []byte
	textArena    []byte
	textPlans    []textVectorPlan
	textPayloads [][]byte
	lastBlock    int64
}

type textVectorPlan struct {
	dictionary      []string
	dictionaryBytes int
	length          int
}

func NewWriter(output io.Writer, fields []Field) (*Writer, error) {
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	header := make([]byte, 16+5*len(fields))
	copy(header, "KCOL0003")
	binary.LittleEndian.PutUint32(header[8:], uint32(len(fields)))
	for i, field := range fields {
		binary.LittleEndian.PutUint32(header[16+i*5:], field.Tag)
		header[20+i*5] = byte(field.Kind)
	}
	binary.LittleEndian.PutUint32(header[12:], crc32.Checksum(header[16:], crcTable))
	if _, err := output.Write(header); err != nil {
		return nil, err
	}
	return &Writer{
		output:       output,
		fields:       append([]Field(nil), fields...),
		buffer:       make([]byte, BatchRows*9),
		header:       make([]byte, groupHeaderSize(formatVersion3, len(fields))),
		textPlans:    make([]textVectorPlan, len(fields)),
		textPayloads: make([][]byte, len(fields)),
	}, nil
}

func encodeVector(buffer []byte, vector *Vector, rows int) ([]byte, error) {
	if vector != nil && vector.Field.Kind == Text {
		return encodeTextVector(buffer, vector, rows)
	}
	if vector == nil || rows < 0 || rows > BatchRows || len(buffer) < rows*9 || len(vector.Valid) < rows ||
		(vector.Field.Kind == Float && len(vector.Floats) < rows) ||
		(vector.Field.Kind != Float && len(vector.Integers) < rows) {
		return nil, fmt.Errorf("columnar: invalid vector bounds")
	}
	data := buffer[:rows*9]
	copy(data[:rows], vector.Valid[:rows])
	for i := range rows {
		var encoded uint64
		if vector.Valid[i] > 1 {
			return nil, fmt.Errorf("columnar: invalid validity bit")
		}
		if vector.Valid[i] != 0 {
			if vector.Field.Kind == Float {
				if math.IsNaN(vector.Floats[i]) || math.IsInf(vector.Floats[i], 0) {
					return nil, fmt.Errorf("columnar: non-finite float")
				}
				encoded = math.Float64bits(vector.Floats[i])
			} else {
				encoded = uint64(vector.Integers[i])
				if vector.Field.Kind == Boolean && encoded > 1 {
					return nil, fmt.Errorf("columnar: invalid boolean")
				}
			}
		}
		binary.LittleEndian.PutUint64(data[rows+i*8:], encoded)
	}
	return data, nil
}

func encodeTextVector(buffer []byte, vector *Vector, rows int) ([]byte, error) {
	plan, err := prepareTextVector(vector, rows, MaximumGroupPayloadBytes)
	if err != nil {
		return nil, err
	}
	return encodePreparedTextVector(buffer, vector, rows, plan), nil
}

func prepareTextVector(vector *Vector, rows, maximum int) (textVectorPlan, error) {
	if vector == nil || rows < 0 || rows > BatchRows || len(vector.Valid) < rows || len(vector.Texts) < rows {
		return textVectorPlan{}, fmt.Errorf("columnar: invalid text vector bounds")
	}
	minimum := 12 + 4*rows
	if maximum < minimum || maximum > MaximumGroupPayloadBytes {
		return textVectorPlan{}, fmt.Errorf("columnar: text payload exceeds %d bytes", maximum)
	}
	unique := make(map[string]struct{}, rows)
	dictionary := make([]string, 0, rows)
	dictionaryBytes := 0
	for row := range rows {
		if vector.Valid[row] > 1 {
			return textVectorPlan{}, fmt.Errorf("columnar: invalid validity bit")
		}
		if vector.Valid[row] == 0 {
			continue
		}
		value := vector.Texts[row]
		if !utf8.ValidString(value) {
			return textVectorPlan{}, fmt.Errorf("columnar: text is not valid UTF-8")
		}
		if _, exists := unique[value]; exists {
			continue
		}
		nextMinimum := minimum + 4*(len(dictionary)+1)
		if nextMinimum > maximum || dictionaryBytes > maximum-nextMinimum || len(value) > maximum-nextMinimum-dictionaryBytes {
			return textVectorPlan{}, fmt.Errorf("columnar: text payload exceeds %d bytes", maximum)
		}
		unique[value] = struct{}{}
		dictionary = append(dictionary, value)
		dictionaryBytes += len(value)
	}
	sort.Strings(dictionary)
	length := 8 + 4*(len(dictionary)+1) + dictionaryBytes + 4*rows
	return textVectorPlan{dictionary: dictionary, dictionaryBytes: dictionaryBytes, length: length}, nil
}

func encodePreparedTextVector(buffer []byte, vector *Vector, rows int, plan textVectorPlan) []byte {
	length := plan.length
	if cap(buffer) < length {
		buffer = make([]byte, length)
	} else {
		buffer = buffer[:length]
		clear(buffer)
	}
	binary.LittleEndian.PutUint32(buffer, uint32(len(plan.dictionary)))
	binary.LittleEndian.PutUint32(buffer[4:], uint32(plan.dictionaryBytes))
	offsets := 8
	dataOffset := offsets + 4*(len(plan.dictionary)+1)
	position := 0
	codes := make(map[string]uint32, len(plan.dictionary))
	for index, value := range plan.dictionary {
		binary.LittleEndian.PutUint32(buffer[offsets+index*4:], uint32(position))
		copy(buffer[dataOffset+position:], value)
		position += len(value)
		codes[value] = uint32(index + 1)
	}
	binary.LittleEndian.PutUint32(buffer[offsets+len(plan.dictionary)*4:], uint32(position))
	codeOffset := dataOffset + plan.dictionaryBytes
	for row := range rows {
		if vector.Valid[row] != 0 {
			binary.LittleEndian.PutUint32(buffer[codeOffset+row*4:], codes[vector.Texts[row]])
		}
	}
	return buffer
}

func vectorStatistics(vector *Vector, rows int) ColumnStatistics {
	statistics := ColumnStatistics{Field: vector.Field}
	for row := range rows {
		if vector.Valid[row] == 0 {
			statistics.Nulls++
			continue
		}
		switch vector.Field.Kind {
		case Float:
			addFloatStatistic(&statistics, vector.Floats[row])
		case Text:
			statistics.HasValue = true
		default:
			addIntegerStatistic(&statistics, vector.Integers[row])
		}
	}
	return statistics
}

func addIntegerStatistic(statistics *ColumnStatistics, value int64) {
	if !statistics.HasValue {
		statistics.IntegerMin = value
		statistics.IntegerMax = value
	} else {
		statistics.IntegerMin = min(statistics.IntegerMin, value)
		statistics.IntegerMax = max(statistics.IntegerMax, value)
	}
	statistics.HasValue = true
	extension := uint64(0)
	if value < 0 {
		extension = math.MaxUint64
	}
	low, carry := bits.Add64(statistics.IntegerSumLow, uint64(value), 0)
	high, _ := bits.Add64(uint64(statistics.IntegerSumHigh), extension, carry)
	statistics.IntegerSumLow = low
	statistics.IntegerSumHigh = int64(high)
}

func addFloatStatistic(statistics *ColumnStatistics, value float64) {
	if !statistics.HasValue {
		statistics.FloatMin = value
		statistics.FloatMax = value
	} else {
		if value < statistics.FloatMin {
			statistics.FloatMin = value
		}
		if value > statistics.FloatMax {
			statistics.FloatMax = value
		}
	}
	statistics.HasValue = true
}

func encodeColumnStatistics(output []byte, statistics ColumnStatistics) {
	clear(output)
	if statistics.HasValue {
		output[0] = 1
	}
	binary.LittleEndian.PutUint32(output[4:], statistics.Nulls)
	if statistics.Field.Kind == Float {
		binary.LittleEndian.PutUint64(output[8:], math.Float64bits(statistics.FloatMin))
		binary.LittleEndian.PutUint64(output[16:], math.Float64bits(statistics.FloatMax))
		return
	}
	if statistics.Field.Kind == Text {
		return
	}
	binary.LittleEndian.PutUint64(output[8:], uint64(statistics.IntegerMin))
	binary.LittleEndian.PutUint64(output[16:], uint64(statistics.IntegerMax))
	binary.LittleEndian.PutUint64(output[24:], statistics.IntegerSumLow)
	binary.LittleEndian.PutUint64(output[32:], uint64(statistics.IntegerSumHigh))
}

func decodeColumnStatistics(encoded []byte, field Field, rows int) (ColumnStatistics, error) {
	if len(encoded) != columnStatisticsSize || encoded[0]&^byte(1) != 0 || encoded[1] != 0 || encoded[2] != 0 || encoded[3] != 0 {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid block statistics")
	}
	statistics := ColumnStatistics{
		Field:    field,
		Nulls:    binary.LittleEndian.Uint32(encoded[4:]),
		HasValue: encoded[0] != 0,
	}
	if statistics.Nulls > uint32(rows) || statistics.HasValue != (statistics.Nulls < uint32(rows)) {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid block null statistics")
	}
	if !statistics.HasValue {
		for _, value := range encoded[8:] {
			if value != 0 {
				return ColumnStatistics{}, fmt.Errorf("columnar: nonzero empty block statistics")
			}
		}
		return statistics, nil
	}
	if field.Kind == Text {
		for _, value := range encoded[8:] {
			if value != 0 {
				return ColumnStatistics{}, fmt.Errorf("columnar: invalid text block statistics")
			}
		}
		return statistics, nil
	}
	if field.Kind == Float {
		statistics.FloatMin = math.Float64frombits(binary.LittleEndian.Uint64(encoded[8:]))
		statistics.FloatMax = math.Float64frombits(binary.LittleEndian.Uint64(encoded[16:]))
		if math.IsNaN(statistics.FloatMin) || math.IsInf(statistics.FloatMin, 0) ||
			math.IsNaN(statistics.FloatMax) || math.IsInf(statistics.FloatMax, 0) ||
			statistics.FloatMin > statistics.FloatMax || binary.LittleEndian.Uint64(encoded[24:]) != 0 || binary.LittleEndian.Uint64(encoded[32:]) != 0 {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid float block statistics")
		}
		return statistics, nil
	}
	statistics.IntegerMin = int64(binary.LittleEndian.Uint64(encoded[8:]))
	statistics.IntegerMax = int64(binary.LittleEndian.Uint64(encoded[16:]))
	statistics.IntegerSumLow = binary.LittleEndian.Uint64(encoded[24:])
	statistics.IntegerSumHigh = int64(binary.LittleEndian.Uint64(encoded[32:]))
	if statistics.IntegerMin > statistics.IntegerMax {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid integer block statistics")
	}
	if field.Kind == Boolean {
		valid := uint64(rows) - uint64(statistics.Nulls)
		if statistics.IntegerMin < 0 || statistics.IntegerMax > 1 || statistics.IntegerSumHigh != 0 || statistics.IntegerSumLow > valid {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid boolean block statistics")
		}
	}
	return statistics, nil
}

func sameColumnStatistics(left, right ColumnStatistics) bool {
	if left.Field != right.Field || left.Nulls != right.Nulls || left.HasValue != right.HasValue ||
		left.IntegerSumHigh != right.IntegerSumHigh || left.IntegerSumLow != right.IntegerSumLow {
		return false
	}
	if left.Field.Kind == Float {
		return math.Float64bits(left.FloatMin) == math.Float64bits(right.FloatMin) &&
			math.Float64bits(left.FloatMax) == math.Float64bits(right.FloatMax)
	}
	if left.Field.Kind == Text {
		return left.IntegerMin == 0 && left.IntegerMax == 0 && right.IntegerMin == 0 && right.IntegerMax == 0
	}
	return left.IntegerMin == right.IntegerMin && left.IntegerMax == right.IntegerMax
}

func inspectVector(data []byte, field Field, rows int, vector *Vector) (ColumnStatistics, error) {
	if field.Kind == Text {
		return inspectTextVector(data, field, rows, vector)
	}
	if len(data) != rows*9 {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid encoded vector length")
	}
	statistics := ColumnStatistics{Field: field}
	for row := range rows {
		valid := data[row]
		encoded := binary.LittleEndian.Uint64(data[rows+row*8:])
		if valid > 1 || valid == 0 && encoded != 0 {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid validity/value")
		}
		if vector != nil {
			vector.Valid[row] = valid
		}
		if valid == 0 {
			statistics.Nulls++
			if vector != nil {
				if field.Kind == Float {
					vector.Floats[row] = 0
				} else {
					vector.Integers[row] = 0
				}
			}
			continue
		}
		if field.Kind == Float {
			value := math.Float64frombits(encoded)
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return ColumnStatistics{}, fmt.Errorf("columnar: non-finite float")
			}
			if vector != nil {
				vector.Floats[row] = value
			}
			addFloatStatistic(&statistics, value)
			continue
		}
		value := int64(encoded)
		if field.Kind == Boolean && encoded > 1 {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid boolean")
		}
		if vector != nil {
			vector.Integers[row] = value
		}
		addIntegerStatistic(&statistics, value)
	}
	return statistics, nil
}

func inspectTextVector(data []byte, field Field, rows int, vector *Vector) (ColumnStatistics, error) {
	minimum := 8 + 4 + 4*rows
	if rows < 0 || rows > BatchRows || len(data) < minimum || len(data) > MaximumGroupPayloadBytes {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid encoded text vector length")
	}
	count := int(binary.LittleEndian.Uint32(data))
	dictionaryBytes := int(binary.LittleEndian.Uint32(data[4:]))
	if count < 0 || count > rows || dictionaryBytes < 0 {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid text dictionary bounds")
	}
	offsetsLength := 4 * (count + 1)
	dictionaryOffset := 8 + offsetsLength
	if dictionaryOffset > len(data) || dictionaryBytes > len(data)-dictionaryOffset {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid text dictionary length")
	}
	codeOffset := dictionaryOffset + dictionaryBytes
	if codeOffset > len(data) || len(data)-codeOffset != rows*4 {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid text code length")
	}
	if binary.LittleEndian.Uint32(data[8:]) != 0 || int(binary.LittleEndian.Uint32(data[8+count*4:])) != dictionaryBytes {
		return ColumnStatistics{}, fmt.Errorf("columnar: invalid text dictionary offsets")
	}
	dictionaryData := data[dictionaryOffset:codeOffset]
	if !utf8.Valid(dictionaryData) {
		return ColumnStatistics{}, fmt.Errorf("columnar: text dictionary is not valid UTF-8")
	}
	dictionaryText := string(dictionaryData)
	dictionary := make([]string, count)
	previousEnd := 0
	for index := range count {
		start := int(binary.LittleEndian.Uint32(data[8+index*4:]))
		end := int(binary.LittleEndian.Uint32(data[8+(index+1)*4:]))
		if start != previousEnd || start < 0 || end < start || end > dictionaryBytes {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid text dictionary offset")
		}
		value := dictionaryText[start:end]
		if !utf8.ValidString(value) {
			return ColumnStatistics{}, fmt.Errorf("columnar: text dictionary offset splits UTF-8")
		}
		if index != 0 && strings.Compare(dictionary[index-1], value) >= 0 {
			return ColumnStatistics{}, fmt.Errorf("columnar: text dictionary is not strictly ordered")
		}
		dictionary[index] = value
		previousEnd = end
	}
	used := make([]bool, count)
	statistics := ColumnStatistics{Field: field}
	if vector != nil {
		if len(vector.Valid) < rows || len(vector.Texts) < rows {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid text output vector")
		}
		clear(vector.Valid)
		clear(vector.Texts)
	}
	for row := range rows {
		code := int(binary.LittleEndian.Uint32(data[codeOffset+row*4:]))
		if code == 0 {
			statistics.Nulls++
			continue
		}
		if code > count {
			return ColumnStatistics{}, fmt.Errorf("columnar: invalid text dictionary code")
		}
		statistics.HasValue = true
		used[code-1] = true
		if vector != nil {
			vector.Valid[row] = 1
			vector.Texts[row] = dictionary[code-1]
		}
	}
	for _, referenced := range used {
		if !referenced {
			return ColumnStatistics{}, fmt.Errorf("columnar: unreferenced text dictionary value")
		}
	}
	return statistics, nil
}

func (writer *Writer) WriteBatch(batch *Batch) error {
	if batch.Rows < 1 || batch.Rows > BatchRows || len(batch.Columns) != len(writer.fields) {
		return fmt.Errorf("columnar: invalid batch")
	}
	writer.lastBlock = 0
	binary.LittleEndian.PutUint32(writer.header, uint32(batch.Rows))
	totalPayload := 0
	totalTextPayload := 0
	for i := range batch.Columns {
		writer.textPlans[i] = textVectorPlan{}
		writer.textPayloads[i] = nil
		if batch.Columns[i].Field != writer.fields[i] {
			return fmt.Errorf("columnar: batch schema mismatch")
		}
		length := batch.Rows * 9
		if batch.Columns[i].Field.Kind == Text {
			plan, err := prepareTextVector(&batch.Columns[i], batch.Rows, MaximumGroupPayloadBytes-totalPayload)
			if err != nil {
				return err
			}
			writer.textPlans[i] = plan
			length = plan.length
			totalTextPayload += length
		}
		if length > MaximumGroupPayloadBytes-totalPayload {
			return fmt.Errorf("columnar: group payload exceeds %d bytes", MaximumGroupPayloadBytes)
		}
		totalPayload += length
	}
	if cap(writer.textArena) < totalTextPayload {
		writer.textArena = make([]byte, totalTextPayload)
	} else {
		writer.textArena = writer.textArena[:totalTextPayload]
		clear(writer.textArena)
	}
	textOffset := 0
	for i := range batch.Columns {
		data := writer.buffer[:batch.Rows*9]
		var err error
		if batch.Columns[i].Field.Kind == Text {
			plan := writer.textPlans[i]
			data = writer.textArena[textOffset : textOffset+plan.length]
			data = encodePreparedTextVector(data, &batch.Columns[i], batch.Rows, plan)
			writer.textPayloads[i] = data
			textOffset += plan.length
		} else {
			data, err = encodeVector(writer.buffer, &batch.Columns[i], batch.Rows)
			if err != nil {
				return err
			}
		}
		binary.LittleEndian.PutUint32(writer.header[8+i*4:], crc32.Checksum(data, crcTable))
		binary.LittleEndian.PutUint32(writer.header[groupLengthsOffset(len(writer.fields))+i*4:], uint32(len(data)))
		offset := groupStatisticsOffset(formatVersion3, len(writer.fields), i)
		encodeColumnStatistics(writer.header[offset:offset+columnStatisticsSize], vectorStatistics(&batch.Columns[i], batch.Rows))
	}
	binary.LittleEndian.PutUint32(writer.header[4:], groupChecksum(writer.header))
	if _, err := writer.output.Write(writer.header); err != nil {
		return err
	}
	for i := range batch.Columns {
		data := writer.textPayloads[i]
		if batch.Columns[i].Field.Kind != Text {
			var err error
			data, err = encodeVector(writer.buffer, &batch.Columns[i], batch.Rows)
			if err != nil {
				return err
			}
		}
		if _, err := writer.output.Write(data); err != nil {
			return err
		}
	}
	writer.lastBlock = int64(len(writer.header) + totalPayload)
	return nil
}

func (writer *Writer) LastBlockLength() int64 {
	if writer == nil {
		return 0
	}
	return writer.lastBlock
}

func groupChecksum(header []byte) uint32 {
	return crc32.Update(crc32.Checksum(header[:4], crcTable), crcTable, header[8:])
}

func groupHeaderSize(version, fields int) int {
	size := 8 + 4*fields
	if version >= formatVersion3 {
		size += 4 * fields
	}
	if version >= formatVersion2 {
		size += columnStatisticsSize * fields
	}
	return size
}

func groupLengthsOffset(fields int) int {
	return 8 + 4*fields
}

func groupStatisticsOffset(version, fields, position int) int {
	offset := 8 + 4*fields
	if version >= formatVersion3 {
		offset += 4 * fields
	}
	return offset + position*columnStatisticsSize
}

type Reader struct {
	input   *io.SectionReader
	Fields  []Field
	start   int64
	version int
}

func Open(input *io.SectionReader) (*Reader, error) {
	if input == nil {
		return nil, fmt.Errorf("columnar: nil input")
	}
	var header [16]byte
	if _, err := input.ReadAt(header[:], 0); err != nil {
		return nil, err
	}
	version := 0
	switch string(header[:8]) {
	case "KCOL0001":
		version = formatVersion1
	case "KCOL0002":
		version = formatVersion2
	case "KCOL0003":
		version = formatVersion3
	}
	count := binary.LittleEndian.Uint32(header[8:])
	if version == 0 || count == 0 || count > MaximumColumns {
		return nil, fmt.Errorf("columnar: invalid header")
	}
	data := make([]byte, int(count)*5)
	if _, err := input.ReadAt(data, 16); err != nil {
		return nil, err
	}
	if crc32.Checksum(data, crcTable) != binary.LittleEndian.Uint32(header[12:]) {
		return nil, fmt.Errorf("columnar: schema checksum mismatch")
	}
	fields := make([]Field, count)
	for i := range fields {
		fields[i] = Field{Tag: binary.LittleEndian.Uint32(data[i*5:]), Kind: Kind(data[i*5+4])}
	}
	if err := validateFields(fields); err != nil {
		return nil, err
	}
	if version < formatVersion3 {
		for _, field := range fields {
			if field.Kind == Text {
				return nil, fmt.Errorf("columnar: text fields require format version 3")
			}
		}
	}
	return &Reader{input: input, Fields: fields, start: int64(16 + len(data)), version: version}, nil
}

func (reader *Reader) FormatVersion() int { return reader.version }

// BlockLength returns the encoded byte length of one complete block for this
// reader's format. Higher-level manifests can use it to select trusted block
// ranges without reading unrelated block headers first.
func (reader *Reader) BlockLength(rows int) (int64, error) {
	if reader == nil || rows < 1 || rows > BatchRows || len(reader.Fields) == 0 {
		return 0, fmt.Errorf("columnar: invalid block row count")
	}
	length := int64(groupHeaderSize(reader.version, len(reader.Fields)))
	for _, field := range reader.Fields {
		if field.Kind == Text {
			return 0, fmt.Errorf("columnar: variable-width block length requires its verified header")
		}
		length += int64(rows * 9)
	}
	return length, nil
}

func (reader *Reader) validateGroupHeader(header []byte) (int, error) {
	if len(header) != groupHeaderSize(reader.version, len(reader.Fields)) {
		return 0, fmt.Errorf("columnar: invalid group header length")
	}
	rows := int(binary.LittleEndian.Uint32(header))
	if rows < 1 || rows > BatchRows || groupChecksum(header) != binary.LittleEndian.Uint32(header[4:]) {
		return 0, fmt.Errorf("columnar: invalid group header/checksum")
	}
	if reader.version >= formatVersion2 {
		for position, field := range reader.Fields {
			if _, err := reader.columnStatistics(header, position, field, rows); err != nil {
				return 0, err
			}
		}
	}
	return rows, nil
}

func (reader *Reader) columnStatistics(header []byte, position int, field Field, rows int) (ColumnStatistics, error) {
	if reader.version < formatVersion2 || position < 0 || position >= len(reader.Fields) {
		return ColumnStatistics{}, fmt.Errorf("columnar: block statistics unavailable")
	}
	offset := groupStatisticsOffset(reader.version, len(reader.Fields), position)
	return decodeColumnStatistics(header[offset:offset+columnStatisticsSize], field, rows)
}

func (reader *Reader) groupPayloadLengths(header []byte, rows int) ([]int64, int64, error) {
	if reader == nil || len(header) != groupHeaderSize(reader.version, len(reader.Fields)) || rows < 1 || rows > BatchRows {
		return nil, 0, fmt.Errorf("columnar: invalid group payload header")
	}
	lengths := make([]int64, len(reader.Fields))
	var total int64
	for position, field := range reader.Fields {
		length := int64(rows * 9)
		if reader.version >= formatVersion3 {
			length = int64(binary.LittleEndian.Uint32(header[groupLengthsOffset(len(reader.Fields))+position*4:]))
		}
		if field.Kind == Text {
			minimum := int64(12 + 4*rows)
			if reader.version < formatVersion3 || length < minimum || length > MaximumGroupPayloadBytes {
				return nil, 0, fmt.Errorf("columnar: invalid text payload length")
			}
		} else if length != int64(rows*9) {
			return nil, 0, fmt.Errorf("columnar: invalid fixed-width payload length")
		}
		if length > MaximumGroupPayloadBytes-total {
			return nil, 0, fmt.Errorf("columnar: group payload exceeds %d bytes", MaximumGroupPayloadBytes)
		}
		lengths[position] = length
		total += length
	}
	return lengths, total, nil
}

// Scan reads only requested column blocks into reusable typed vectors. Memory
// does not grow with table length. The callback must not retain the batch.
func (reader *Reader) Scan(ctx context.Context, fields []Field, visit func(*Batch) error) (uint64, error) {
	report, err := reader.ScanBlocks(ctx, fields, nil, visit)
	return report.Rows, err
}

// ScanBlocks lets a caller skip a block or consume its verified header
// statistics before any vector payload is read. Callbacks must not retain the
// reusable batch or statistics values.
func (reader *Reader) ScanBlocks(ctx context.Context, fields []Field, decide func(*BlockStatistics) (BlockAction, error), visit func(*Batch) error) (ScanReport, error) {
	return reader.ScanBlockRanges(ctx, fields, []BlockRange{{
		Offset: reader.start,
		Length: reader.input.Size() - reader.start,
	}}, decide, visit)
}

// ScanBlockRanges scans only complete group ranges selected by a higher-level
// planner. Ranges must be ordered and non-overlapping. A forged or stale range
// fails closed when either boundary does not align with a verified group.
func (reader *Reader) ScanBlockRanges(ctx context.Context, fields []Field, ranges []BlockRange, decide func(*BlockStatistics) (BlockAction, error), visit func(*Batch) error) (ScanReport, error) {
	if ctx == nil || visit == nil {
		return ScanReport{}, fmt.Errorf("columnar: nil scan context/callback")
	}
	if err := ctx.Err(); err != nil {
		return ScanReport{}, err
	}
	batch, err := NewBatch(fields)
	if err != nil {
		return ScanReport{}, err
	}
	positions := make([]int, len(fields))
	for i, field := range fields {
		positions[i] = -1
		for j, stored := range reader.Fields {
			if stored == field {
				positions[i] = j
				break
			}
		}
		if positions[i] < 0 {
			return ScanReport{}, fmt.Errorf("columnar: missing field %d", field.Tag)
		}
	}
	header := make([]byte, groupHeaderSize(reader.version, len(reader.Fields)))
	buffers := make([][]byte, len(fields))
	columnOffsets := make([]int64, len(reader.Fields))
	block := BlockStatistics{Columns: make([]ColumnStatistics, len(fields))}
	var report ScanReport
	previousEnd := reader.start
	for _, selected := range ranges {
		if selected.Offset < reader.start || selected.Length < 0 || selected.Offset < previousEnd ||
			selected.Offset > reader.input.Size() || selected.Length > reader.input.Size()-selected.Offset {
			return report, fmt.Errorf("columnar: invalid block range")
		}
		end := selected.Offset + selected.Length
		previousEnd = end
		for offset := selected.Offset; offset < end; {
			if err := ctx.Err(); err != nil {
				return report, err
			}
			if int64(len(header)) > end-offset {
				return report, fmt.Errorf("columnar: block range ends inside a group header")
			}
			if _, err := reader.input.ReadAt(header, offset); err != nil {
				return report, err
			}
			report.BlockHeadersRead++
			report.BlockHeaderBytesRead += uint64(len(header))
			rows, err := reader.validateGroupHeader(header)
			if err != nil {
				return report, err
			}
			payload := offset + int64(len(header))
			lengths, groupLength, err := reader.groupPayloadLengths(header, rows)
			if err != nil {
				return report, err
			}
			columnOffset := payload
			for i, length := range lengths {
				columnOffsets[i] = columnOffset
				columnOffset += length
			}
			if groupLength > end-payload {
				return report, fmt.Errorf("columnar: block range ends inside a group")
			}
			block.Rows = rows
			block.EncodedBytes = int64(len(header)) + groupLength
			action := ScanBlock
			if reader.version >= formatVersion2 {
				for i, position := range positions {
					block.Columns[i], err = reader.columnStatistics(header, position, fields[i], rows)
					if err != nil {
						return report, err
					}
				}
				if decide != nil {
					action, err = decide(&block)
					if err != nil {
						return report, err
					}
				}
			}
			report.Rows += uint64(rows)
			switch action {
			case SkipBlock:
				report.RowsSkipped += uint64(rows)
				report.BlocksSkipped++
			case UseBlockStatistics:
				report.RowsFromStatistics += uint64(rows)
				report.BlocksFromStatistics++
			case ScanBlock:
				batch.Rows = rows
				for i, position := range positions {
					length := lengths[position]
					if cap(buffers[i]) < int(length) {
						buffers[i] = make([]byte, int(length))
					} else {
						buffers[i] = buffers[i][:int(length)]
					}
					data := buffers[i]
					if _, err := reader.input.ReadAt(data, columnOffsets[position]); err != nil {
						return report, err
					}
					report.ColumnPayloadsRead++
					report.ColumnPayloadBytesRead += uint64(len(data))
					if crc32.Checksum(data, crcTable) != binary.LittleEndian.Uint32(header[8+position*4:]) {
						return report, fmt.Errorf("columnar: block checksum mismatch")
					}
					actual, err := inspectVector(data, fields[i], rows, &batch.Columns[i])
					if err != nil {
						return report, err
					}
					if reader.version >= formatVersion2 && !sameColumnStatistics(actual, block.Columns[i]) {
						return report, fmt.Errorf("columnar: block statistics mismatch")
					}
				}
				if err := visit(batch); err != nil {
					return report, err
				}
				report.RowsScanned += uint64(rows)
				report.BlocksScanned++
			default:
				return report, fmt.Errorf("columnar: invalid block action")
			}
			offset = payload + groupLength
		}
	}
	return report, nil
}
