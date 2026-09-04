package columnar

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"math"
	"testing"
)

func TestColumnarBlockStatisticsAndActions(t *testing.T) {
	fields := []Field{{1, Integer}, {2, Float}, {3, Boolean}}
	batch, err := NewBatch(fields)
	if err != nil {
		t.Fatal(err)
	}
	batch.Rows = 4
	integers := []int64{math.MaxInt64, math.MaxInt64, 0, -1}
	floats := []float64{-0.0, 2.5, 0, -3}
	booleans := []int64{1, 0, 0, 1}
	for row := range batch.Rows {
		if row != 2 {
			for column := range fields {
				batch.Columns[column].Valid[row] = 1
			}
		}
		batch.Columns[0].Integers[row] = integers[row]
		batch.Columns[1].Floats[row] = floats[row]
		batch.Columns[2].Integers[row] = booleans[row]
	}
	var output bytes.Buffer
	writer, err := NewWriter(&output, fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(io.NewSectionReader(bytes.NewReader(output.Bytes()), 0, int64(output.Len())))
	if err != nil || reader.FormatVersion() != formatVersion3 {
		t.Fatal(reader, err)
	}
	report, err := reader.ScanBlocks(context.Background(), fields, func(block *BlockStatistics) (BlockAction, error) {
		if block.Rows != 4 || block.Columns[0].Nulls != 1 || block.Columns[0].IntegerMin != -1 || block.Columns[0].IntegerMax != math.MaxInt64 ||
			block.Columns[0].IntegerSumHigh != 0 || block.Columns[0].IntegerSumLow != math.MaxUint64-2 ||
			block.Columns[1].FloatMin != -3 || block.Columns[1].FloatMax != 2.5 ||
			block.Columns[2].IntegerMin != 0 || block.Columns[2].IntegerMax != 1 || block.Columns[2].IntegerSumLow != 2 {
			t.Fatalf("statistics: %+v", block)
		}
		return UseBlockStatistics, nil
	}, func(*Batch) error {
		t.Fatal("statistics-only block decoded vectors")
		return nil
	})
	headerBytes := uint64(groupHeaderSize(formatVersion3, len(fields)))
	if err != nil || report.Rows != 4 || report.RowsFromStatistics != 4 || report.BlocksFromStatistics != 1 || report.RowsScanned != 0 ||
		report.BlockHeadersRead != 1 || report.BlockHeaderBytesRead != headerBytes ||
		report.ColumnPayloadsRead != 0 || report.ColumnPayloadBytesRead != 0 {
		t.Fatalf("statistics report: %+v, %v", report, err)
	}
	report, err = reader.ScanBlocks(context.Background(), fields[:1], func(*BlockStatistics) (BlockAction, error) {
		return SkipBlock, nil
	}, func(*Batch) error {
		t.Fatal("skipped block decoded vectors")
		return nil
	})
	if err != nil || report.RowsSkipped != 4 || report.BlocksSkipped != 1 ||
		report.BlockHeadersRead != 1 || report.BlockHeaderBytesRead != headerBytes ||
		report.ColumnPayloadsRead != 0 || report.ColumnPayloadBytesRead != 0 {
		t.Fatalf("skip report: %+v, %v", report, err)
	}
}

func TestColumnarVersionOneScansWithoutStatistics(t *testing.T) {
	fields := []Field{{1, Integer}}
	batch, _ := NewBatch(fields)
	batch.Rows = 2
	batch.Columns[0].Valid[0], batch.Columns[0].Valid[1] = 1, 1
	batch.Columns[0].Integers[0], batch.Columns[0].Integers[1] = 4, 9
	var output bytes.Buffer
	header := make([]byte, 21)
	copy(header, "KCOL0001")
	binary.LittleEndian.PutUint32(header[8:], 1)
	binary.LittleEndian.PutUint32(header[16:], fields[0].Tag)
	header[20] = byte(fields[0].Kind)
	binary.LittleEndian.PutUint32(header[12:], crc32.Checksum(header[16:], crcTable))
	output.Write(header)
	group := make([]byte, groupHeaderSize(formatVersion1, 1))
	binary.LittleEndian.PutUint32(group, uint32(batch.Rows))
	data, err := encodeVector(make([]byte, BatchRows*9), &batch.Columns[0], batch.Rows)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(group[8:], crc32.Checksum(data, crcTable))
	binary.LittleEndian.PutUint32(group[4:], groupChecksum(group))
	output.Write(group)
	output.Write(data)
	reader, err := Open(io.NewSectionReader(bytes.NewReader(output.Bytes()), 0, int64(output.Len())))
	if err != nil || reader.FormatVersion() != formatVersion1 {
		t.Fatal(reader, err)
	}
	decided, visited := false, false
	report, err := reader.ScanBlocks(context.Background(), fields, func(*BlockStatistics) (BlockAction, error) {
		decided = true
		return SkipBlock, nil
	}, func(batch *Batch) error {
		visited = batch.Rows == 2 && batch.Columns[0].Integers[1] == 9
		return nil
	})
	if err != nil || decided || !visited || report.RowsScanned != 2 {
		t.Fatalf("v1 scan: decided=%t visited=%t report=%+v error=%v", decided, visited, report, err)
	}
}

func TestColumnarProjectionReadsOnlyRequestedColumn(t *testing.T) {
	fields := []Field{{Tag: 1, Kind: Integer}, {Tag: 2, Kind: Float}}
	batch, err := NewBatch(fields)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	writer, err := NewWriter(&output, fields)
	if err != nil {
		t.Fatal(err)
	}
	batch.Rows = 3
	batch.Columns[0].Valid[0], batch.Columns[0].Valid[2] = 1, 1
	batch.Columns[0].Integers[0], batch.Columns[0].Integers[2] = 10, -5
	for i := range 3 {
		batch.Columns[1].Valid[i] = 1
		batch.Columns[1].Floats[i] = float64(i) / 4
	}
	if err := writer.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	// Corrupt the unrequested float block. Reading integer-only remains valid;
	// reading all columns detects the damage.
	data[len(data)-1] ^= 1
	reader, err := Open(io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := reader.Scan(context.Background(), fields[:1], func(b *Batch) error {
		if b.Rows != 3 || len(b.Columns) != 1 || b.Columns[0].Valid[1] != 0 || b.Columns[0].Integers[2] != -5 {
			t.Fatalf("batch: %+v", b)
		}
		return nil
	})
	if err != nil || rows != 3 {
		t.Fatal(rows, err)
	}
	if _, err := reader.Scan(context.Background(), fields, func(*Batch) error { return nil }); err == nil {
		t.Fatal("corrupt selected column accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.Scan(ctx, fields[:1], func(*Batch) error { return nil }); err == nil {
		t.Fatal("canceled scan accepted")
	}
}

func TestColumnarDictionaryTextRoundTripAndCorruption(t *testing.T) {
	fields := []Field{{Tag: 1, Kind: Integer}, {Tag: 2, Kind: Text}}
	batch, err := NewBatch(fields)
	if err != nil {
		t.Fatal(err)
	}
	values := []string{"bàn phím", "", "bàn phím", "chuột", ""}
	batch.Rows = len(values)
	for row, value := range values {
		batch.Columns[0].Valid[row] = 1
		batch.Columns[0].Integers[row] = int64(row + 1)
		if row != 4 {
			batch.Columns[1].Valid[row] = 1
			batch.Columns[1].Texts[row] = value
		}
	}
	plan, err := prepareTextVector(&batch.Columns[1], batch.Rows, MaximumGroupPayloadBytes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareTextVector(&batch.Columns[1], batch.Rows, plan.length-1); err == nil {
		t.Fatal("text vector exceeded its pre-allocation payload budget")
	}
	var output bytes.Buffer
	writer, err := NewWriter(&output, fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	reader, err := Open(io.NewSectionReader(bytes.NewReader(output.Bytes()), 0, int64(output.Len())))
	if err != nil || reader.FormatVersion() != formatVersion3 {
		t.Fatal(reader, err)
	}
	if _, err := reader.BlockLength(batch.Rows); err == nil {
		t.Fatal("variable-width text block reported a synthetic fixed length")
	}
	report, err := reader.ScanBlocks(context.Background(), fields[1:], func(block *BlockStatistics) (BlockAction, error) {
		if block.Rows != len(values) || block.EncodedBytes != writer.LastBlockLength() ||
			block.Columns[0].Nulls != 1 || !block.Columns[0].HasValue {
			t.Fatalf("text block statistics = %+v", block)
		}
		return ScanBlock, nil
	}, func(projected *Batch) error {
		if projected.Rows != len(values) || projected.Columns[0].Valid[4] != 0 {
			t.Fatalf("projected text batch = %+v", projected)
		}
		for row := 0; row < 4; row++ {
			if projected.Columns[0].Texts[row] != values[row] {
				t.Fatalf("text row %d = %q, want %q", row, projected.Columns[0].Texts[row], values[row])
			}
		}
		return nil
	})
	if err != nil || report.ColumnPayloadsRead != 1 {
		t.Fatalf("text scan report = %+v, %v", report, err)
	}

	corrupt := bytes.Clone(output.Bytes())
	headerOffset := int(reader.DataOffset())
	headerSize := groupHeaderSize(formatVersion3, len(fields))
	header := corrupt[headerOffset : headerOffset+headerSize]
	integerLength := int(binary.LittleEndian.Uint32(header[groupLengthsOffset(len(fields)) : groupLengthsOffset(len(fields))+4]))
	textLength := int(binary.LittleEndian.Uint32(header[groupLengthsOffset(len(fields))+4 : groupLengthsOffset(len(fields))+8]))
	textPayload := corrupt[headerOffset+headerSize+integerLength : headerOffset+headerSize+integerLength+textLength]
	binary.LittleEndian.PutUint32(textPayload[len(textPayload)-4:], math.MaxUint32)
	binary.LittleEndian.PutUint32(header[12:], crc32.Checksum(textPayload, crcTable))
	binary.LittleEndian.PutUint32(header[4:], groupChecksum(header))
	reader, err = Open(io.NewSectionReader(bytes.NewReader(corrupt), 0, int64(len(corrupt))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Scan(context.Background(), fields[1:], func(*Batch) error { return nil }); err == nil {
		t.Fatal("out-of-range dictionary code was accepted")
	}

	batch.Reset()
	if batch.Columns[1].Texts[0] != "" || batch.Columns[1].Valid[0] != 0 {
		t.Fatal("batch reset retained text")
	}
}

func TestColumnarScanBlockRangesReadsOnlyCompleteSelectedGroups(t *testing.T) {
	fields := []Field{{Tag: 1, Kind: Integer}}
	var output bytes.Buffer
	writer, err := NewWriter(&output, fields)
	if err != nil {
		t.Fatal(err)
	}
	for group := range 3 {
		batch, err := NewBatch(fields)
		if err != nil {
			t.Fatal(err)
		}
		batch.Rows = 2
		for row := range batch.Rows {
			batch.Columns[0].Valid[row] = 1
			batch.Columns[0].Integers[row] = int64(group*10 + row)
		}
		if err := writer.WriteBatch(batch); err != nil {
			t.Fatal(err)
		}
	}
	reader, err := Open(io.NewSectionReader(bytes.NewReader(output.Bytes()), 0, int64(output.Len())))
	if err != nil {
		t.Fatal(err)
	}
	groupLength := int64(groupHeaderSize(formatVersion3, len(fields)) + 2*9*len(fields))
	if got, err := reader.BlockLength(2); err != nil || got != groupLength {
		t.Fatalf("block length = %d, %v; want %d", got, err, groupLength)
	}
	if _, err := reader.BlockLength(0); err == nil {
		t.Fatal("zero-row block length unexpectedly succeeded")
	}
	selected := BlockRange{Offset: reader.DataOffset() + groupLength, Length: groupLength}
	var values []int64
	report, err := reader.ScanBlockRanges(context.Background(), fields, []BlockRange{selected}, nil, func(batch *Batch) error {
		values = append(values, batch.Columns[0].Integers[:batch.Rows]...)
		return nil
	})
	if err != nil || report.Rows != 2 || report.RowsScanned != 2 ||
		report.BlockHeadersRead != 1 || report.BlockHeaderBytesRead != uint64(groupHeaderSize(formatVersion3, len(fields))) ||
		report.ColumnPayloadsRead != 1 || report.ColumnPayloadBytesRead != 2*9 ||
		!bytes.Equal(int64Bytes(values), int64Bytes([]int64{10, 11})) {
		t.Fatalf("selected range: values=%v report=%+v error=%v", values, report, err)
	}
	invalid := []BlockRange{{Offset: selected.Offset + 1, Length: selected.Length - 1}}
	if _, err := reader.ScanBlockRanges(context.Background(), fields, invalid, nil, func(*Batch) error { return nil }); err == nil {
		t.Fatal("misaligned block range unexpectedly succeeded")
	}
	unordered := []BlockRange{selected, {Offset: reader.DataOffset(), Length: groupLength}}
	if _, err := reader.ScanBlockRanges(context.Background(), fields, unordered, nil, func(*Batch) error { return nil }); err == nil {
		t.Fatal("unordered block ranges unexpectedly succeeded")
	}
}

func int64Bytes(values []int64) []byte {
	data := make([]byte, len(values)*8)
	for index, value := range values {
		binary.LittleEndian.PutUint64(data[index*8:], uint64(value))
	}
	return data
}

func TestColumnarRejectsTruncationAndInvalidSchemas(t *testing.T) {
	for _, fields := range [][]Field{nil, {{1, Integer}, {1, Float}}, {{0, Float}}, {{1, Kind(99)}}} {
		if _, err := NewBatch(fields); err == nil {
			t.Fatal("invalid schema accepted")
		}
	}
	batch, _ := NewBatch([]Field{{1, Integer}})
	batch.Rows = BatchRows
	var output bytes.Buffer
	w, err := NewWriter(&output, []Field{{1, Integer}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	for _, size := range []int{0, 7, 20, len(data) - 1} {
		r, err := Open(io.NewSectionReader(bytes.NewReader(data[:size]), 0, int64(size)))
		if err == nil {
			_, err = r.Scan(context.Background(), []Field{{1, Integer}}, func(*Batch) error { return nil })
		}
		if err == nil {
			t.Fatalf("accepted truncation at %d", size)
		}
	}
}
