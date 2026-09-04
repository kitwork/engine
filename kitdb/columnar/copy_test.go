package columnar

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"testing"
)

func groupCopyFixture(t *testing.T) ([]byte, int64) {
	t.Helper()
	fields := []Field{{1, Integer}, {2, Float}, {3, Boolean}}
	batch, err := NewBatch(fields)
	if err != nil {
		t.Fatal(err)
	}
	batch.Rows = 3
	for i := range 3 {
		for j := range fields {
			batch.Columns[j].Valid[i] = 1
		}
		batch.Columns[0].Integers[i] = int64(i - 1)
		batch.Columns[1].Floats[i] = float64(i) / 4
		batch.Columns[2].Integers[i] = int64(i % 2)
	}
	var output bytes.Buffer
	writer, err := NewWriter(&output, fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	firstEnd := int64(output.Len())
	batch.Rows = 2
	if err := writer.WriteBatch(batch); err != nil {
		t.Fatal(err)
	}
	return output.Bytes(), firstEnd
}

func TestColumnarCopyGroupsPreservesBytesAndBoundaries(t *testing.T) {
	data, firstEnd := groupCopyFixture(t)
	reader, err := Open(io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		start, end int64
		rows       uint64
	}{
		{reader.DataOffset(), int64(len(data)), 5},
		{reader.DataOffset(), firstEnd, 3},
		{firstEnd, int64(len(data)), 2},
		{firstEnd, firstEnd, 0},
	} {
		var output bytes.Buffer
		rows, err := reader.CopyGroups(context.Background(), &output, test.start, test.end-test.start)
		if err != nil || rows != test.rows || !bytes.Equal(output.Bytes(), data[test.start:test.end]) {
			t.Fatalf("copy %d..%d: rows=%d error=%v", test.start, test.end, rows, err)
		}
	}
	for _, bounds := range [][2]int64{{0, 10}, {reader.DataOffset(), -1}, {reader.DataOffset(), int64(len(data))}, {reader.DataOffset() + 1, 10}, {reader.DataOffset(), firstEnd - reader.DataOffset() - 1}} {
		if _, err := reader.CopyGroups(context.Background(), io.Discard, bounds[0], bounds[1]); err == nil {
			t.Fatalf("accepted invalid bounds %v", bounds)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.CopyGroups(ctx, io.Discard, reader.DataOffset(), firstEnd-reader.DataOffset()); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled copy: %v", err)
	}
	if _, err := reader.CopyGroups(context.Background(), shortGroupWriter{}, reader.DataOffset(), firstEnd-reader.DataOffset()); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short copy: %v", err)
	}
}

type shortGroupWriter struct{}

func (shortGroupWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }

func TestColumnarCopyGroupsVerifiesEveryColumnAndValue(t *testing.T) {
	for _, kind := range []string{"checksum", "statistics", "validity", "null-value", "float", "boolean"} {
		t.Run(kind, func(t *testing.T) {
			data, firstEnd := groupCopyFixture(t)
			reader, err := Open(io.NewSectionReader(bytes.NewReader(data), 0, int64(len(data))))
			if err != nil {
				t.Fatal(err)
			}
			headerSize := groupHeaderSize(reader.version, len(reader.Fields))
			header := data[reader.DataOffset() : reader.DataOffset()+int64(headerSize)]
			column := 0
			if kind == "float" {
				column = 1
			} else if kind == "boolean" || kind == "checksum" {
				column = 2
			}
			start := reader.DataOffset() + int64(headerSize) + int64(column*27)
			block := data[start : start+27]
			switch kind {
			case "checksum":
				block[len(block)-1] ^= 1
			case "statistics":
				statistics := groupStatisticsOffset(reader.version, len(reader.Fields), column)
				minimum := binary.LittleEndian.Uint64(header[statistics+8:])
				binary.LittleEndian.PutUint64(header[statistics+8:], minimum+1)
				binary.LittleEndian.PutUint32(header[4:], groupChecksum(header))
			case "validity":
				block[0] = 2
			case "null-value":
				block[0] = 0
			case "float":
				binary.LittleEndian.PutUint64(block[3:], math.Float64bits(math.NaN()))
			case "boolean":
				binary.LittleEndian.PutUint64(block[3:], 2)
			}
			if kind != "checksum" && kind != "statistics" {
				binary.LittleEndian.PutUint32(header[8+column*4:], crc32.Checksum(block, crcTable))
				binary.LittleEndian.PutUint32(header[4:], groupChecksum(header))
			}
			if _, err := reader.CopyGroups(context.Background(), io.Discard, reader.DataOffset(), firstEnd-reader.DataOffset()); err == nil {
				t.Fatal("accepted corrupt or noncanonical column")
			}
		})
	}
}
