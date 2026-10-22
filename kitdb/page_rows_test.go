package kitdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func encodePositionTestPage(rows []mainRow, mutations bool) []byte {
	var data []byte
	for _, row := range rows {
		header := make([]byte, mainRecordHeaderSize)
		if mutations {
			header = make([]byte, operationHeaderSize)
			header[0] = byte(operationPut)
			if row.deleted {
				header[0] = byte(operationDelete)
			}
			binary.LittleEndian.PutUint32(header[1:5], uint32(len(row.key)))
			binary.LittleEndian.PutUint32(header[5:9], uint32(len(row.value)))
		} else {
			binary.LittleEndian.PutUint32(header[:4], uint32(len(row.key)))
			binary.LittleEndian.PutUint32(header[4:8], uint32(len(row.value)))
		}
		data = append(data, header...)
		data = append(data, row.key...)
		data = append(data, row.value...)
	}
	return data
}

func TestPageRowPositions(t *testing.T) {
	for _, mutations := range []bool{false, true} {
		t.Run(fmt.Sprintf("mutations=%t", mutations), func(t *testing.T) {
			expected := make([]mainRow, mainIndexBlockRecords)
			for i := range expected {
				expected[i] = mainRow{key: []byte{0, byte(i), 255}, value: bytes.Repeat([]byte{byte(i)}, i*3)}
				if mutations && i%7 == 0 {
					expected[i].value = nil
					expected[i].deleted = true
				}
			}
			expected[len(expected)-1].value = bytes.Repeat([]byte{254}, mainIndexBlockBytes+17)
			data := encodePositionTestPage(expected, mutations)
			decode := decodeRowPage
			if mutations {
				decode = decodeMutationPage
			}
			page, err := decode("positions", 4, 37, data, uint32(len(expected)))
			if err != nil {
				t.Fatal(err)
			}
			if len(page.rows) != len(expected) || page.block != 4 || page.weight != int64(len(data))+int64(cap(page.rows))*cachedRowOverhead {
				t.Fatalf("page metadata differs: rows=%d block=%d weight=%d", len(page.rows), page.block, page.weight)
			}
			for i, want := range expected {
				got := page.row(i)
				if !bytes.Equal(got.key, want.key) || !bytes.Equal(got.value, want.value) || got.deleted != want.deleted || !bytes.Equal(page.key(i), want.key) {
					t.Fatalf("row %d differs: %+v", i, got)
				}
				if len(got.value) != 0 && &got.value[0] != &data[page.rows[i].valueStart] {
					t.Fatal("page row unexpectedly copied its value")
				}
			}
			// Reuse only the metadata storage with another independently owned
			// data buffer; returned positions must refer to that new buffer.
			replacement := []mainRow{{key: []byte("z"), value: []byte("new")}}
			nextData := encodePositionTestPage(replacement, mutations)
			decodeRows := decodeRowPageRows
			if mutations {
				decodeRows = decodeMutationPageRows
			}
			rows, err := decodeRows("positions", 0, nextData, 1, page.rows)
			if err != nil || len(rows) != 1 || cap(rows) != cap(page.rows) || &rows[0] != &page.rows[0] {
				t.Fatalf("metadata reuse failed: %v", err)
			}
			next := &rowPage{data: nextData, rows: rows}
			if row := next.row(0); string(row.key) != "z" || string(row.value) != "new" || row.deleted {
				t.Fatalf("reused row=%+v", row)
			}
		})
	}
}

func TestPagePositionsRejectCorruption(t *testing.T) {
	for _, mutations := range []bool{false, true} {
		t.Run(fmt.Sprintf("mutations=%t", mutations), func(t *testing.T) {
			rows := []mainRow{{key: []byte("a"), value: []byte("one")}, {key: []byte("z"), value: []byte("two")}}
			original := encodePositionTestPage(rows, mutations)
			header, keyAt, valueAt := mainRecordHeaderSize, 0, 4
			if mutations {
				header, keyAt, valueAt = operationHeaderSize, 1, 5
			}
			type corruptionCase struct {
				name   string
				mutate func([]byte) []byte
				count  uint32
			}
			cases := []corruptionCase{
				{"header", func(d []byte) []byte { return d[:header-1] }, 2},
				{"body", func(d []byte) []byte { return d[:len(d)-1] }, 2},
				{"zero_key", func(d []byte) []byte { binary.LittleEndian.PutUint32(d[keyAt:], 0); return d }, 2},
				{"oversize_key", func(d []byte) []byte { binary.LittleEndian.PutUint32(d[keyAt:], maxKeySize+1); return d }, 2},
				{"oversize_value", func(d []byte) []byte { binary.LittleEndian.PutUint32(d[valueAt:], maxValueSize+1); return d }, 2},
				{"trailing", func(d []byte) []byte { return append(d, 0) }, 2},
				{"duplicate", func(d []byte) []byte { d[header+4+header] = 'a'; return d }, 2},
				{"descending", func(d []byte) []byte { d[header+4+header] = '0'; return d }, 2},
				{"missing_record", func(d []byte) []byte { return d }, 3},
				{"above_limit", func(d []byte) []byte { return d }, mainIndexBlockRecords + 1},
				{"zero_records", func(d []byte) []byte { return d }, 0},
			}
			if mutations {
				cases = append(cases,
					corruptionCase{"invalid_kind", func(d []byte) []byte { d[0] = 255; return d }, 2},
					corruptionCase{"delete_value", func(d []byte) []byte { d[0] = byte(operationDelete); return d }, 2})
			}
			for _, tc := range cases {
				t.Run(tc.name, func(t *testing.T) {
					data := tc.mutate(bytes.Clone(original))
					main := positionTestImage(t, data, tc.count, mutations)
					if _, err := main.readPage(0); err == nil {
						t.Fatal("corrupt page accepted")
					}
					// A lookup of the valid first row must still validate a bad
					// later row, even when the page checksum itself matches.
					if _, _, err := main.lookupFromBlocks([]byte("a"), 0, 1, nil); err == nil {
						t.Fatal("point lookup skipped malformed page data")
					}
				})
			}
			for _, field := range []string{"checksum", "first", "last"} {
				t.Run(field, func(t *testing.T) {
					main := positionTestImage(t, original, 2, mutations)
					switch field {
					case "checksum":
						main.blocks[0].checksum++
					case "first":
						main.blocks[0].firstKey = []byte("bad")
					case "last":
						main.blocks[0].lastKey = []byte("bad")
					}
					if _, err := main.readPage(0); err == nil {
						t.Fatal("directory/checksum mismatch accepted")
					}
				})
			}
		})
	}
}

func positionTestImage(t *testing.T, data []byte, count uint32, mutations bool) *mainImage {
	t.Helper()
	path := filepath.Join(t.TempDir(), "page.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Error(err)
		}
	})
	return &mainImage{path: path, file: file, formatVersion: mainPagedFormatVersion,
		blocks: []mainBlock{{firstKey: []byte("a"), lastKey: []byte("z"), length: int64(len(data)),
			records: count, checksum: crc32.Checksum(data, crc32cTable), mutations: mutations}}}
}

func TestPagePositionsCallerOwnership(t *testing.T) {
	for _, cache := range []int64{-1, 256 << 10} {
		t.Run(fmt.Sprintf("cache=%d", cache), func(t *testing.T) {
			db, err := OpenWithOptions(filepath.Join(t.TempDir(), "owner.kitdb"), OpenOptions{PageCacheBytes: cache})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			tx := mustBegin(t, db)
			for i := range 300 {
				if err := tx.Put([]byte(fmt.Sprintf("row/%04d", i)), bytes.Repeat([]byte{byte(i)}, 1024)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			snapshot, err := db.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			for _, reverse := range []bool{false, true} {
				cursor, err := snapshot.Cursor(RangeOptions{Reverse: reverse})
				if err != nil {
					t.Fatal(err)
				}
				var saved [][]byte
				for cursor.Next() {
					saved = append(saved, cursor.Value())
				}
				if err := cursor.Err(); err != nil || len(saved) != 300 {
					t.Fatalf("cursor count=%d err=%v", len(saved), err)
				}
				for i, value := range saved {
					id := i
					if reverse {
						id = 299 - i
					}
					if !bytes.Equal(value, bytes.Repeat([]byte{byte(id)}, 1024)) {
						t.Fatalf("retained value changed at %d", i)
					}
					value[0] ^= 255
				}
			}
			value, found, err := snapshot.Get([]byte("row/0001"))
			if err != nil || !found || !bytes.Equal(value, bytes.Repeat([]byte{1}, 1024)) {
				t.Fatalf("owned read differs: %v", err)
			}
			value[0] = 255
			commitPut(t, db, "row/0001", "new")
			if _, err := db.Checkpoint(); err != nil {
				t.Fatal(err)
			}
			value, found, err = snapshot.Get([]byte("row/0001"))
			if err != nil || !found || !bytes.Equal(value, bytes.Repeat([]byte{1}, 1024)) {
				t.Fatalf("old snapshot changed: %v", err)
			}
			if err := db.Verify(); err != nil {
				t.Fatal(err)
			}
			if err := snapshot.Close(); err != nil {
				t.Fatal(err)
			}
			if _, _, err := snapshot.Get([]byte("row/0001")); !errors.Is(err, ErrSnapshotClosed) {
				t.Fatalf("closed snapshot read: %v", err)
			}
		})
	}
}
