package kitdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
	"testing"
)

type latencyPageFixture struct {
	data     []byte
	keys     [][]byte
	expected []mainRow
	digest   uint64
}

func newLatencyPageFixture() latencyPageFixture {
	fixture := latencyPageFixture{}
	for i := range mainIndexBlockRecords {
		key := []byte(fmt.Sprintf("row/%04d", i))
		value := bytes.Repeat([]byte{byte(i)}, 384)
		deleted := i%11 == 0
		if deleted {
			value = nil
		}
		header := make([]byte, operationHeaderSize)
		header[0] = byte(operationPut)
		if deleted {
			header[0] = byte(operationDelete)
		}
		binary.LittleEndian.PutUint32(header[1:5], uint32(len(key)))
		binary.LittleEndian.PutUint32(header[5:9], uint32(len(value)))
		fixture.data = append(fixture.data, header...)
		fixture.data = append(fixture.data, key...)
		fixture.data = append(fixture.data, value...)
		fixture.keys = append(fixture.keys, key)
		row := mainRow{key: key, value: value, deleted: deleted}
		fixture.expected = append(fixture.expected, row)
		fixture.digest += latencyRowDigest(row)
	}
	return fixture
}

func latencyRowDigest(row mainRow) uint64 {
	digest := uint64(len(row.key))*257 + uint64(row.key[0])*31 + uint64(row.key[len(row.key)-1])
	digest += uint64(len(row.value)) * 65537
	if len(row.value) != 0 {
		digest += uint64(row.value[0])*17 + uint64(row.value[len(row.value)-1])*7
	}
	if row.deleted {
		digest += 1000000007
	}
	return digest
}

func checkLatencyPage(tb testing.TB, page *rowPage, fixture latencyPageFixture) {
	tb.Helper()
	if len(page.rows) != len(fixture.expected) {
		tb.Fatalf("rows=%d want=%d", len(page.rows), len(fixture.expected))
	}
	for i, want := range fixture.expected {
		got := latencyPageRow(page, i)
		if !bytes.Equal(got.key, want.key) || !bytes.Equal(got.value, want.value) || got.deleted != want.deleted || !bytes.Equal(latencyPageKey(page, i), want.key) {
			tb.Fatalf("row %d differs from fixture", i)
		}
	}
}

func TestPageLatencyFixture(t *testing.T) {
	fixture := newLatencyPageFixture()
	page, err := decodeMutationPage("latency", 0, 0, fixture.data, mainIndexBlockRecords)
	if err != nil {
		t.Fatal(err)
	}
	checkLatencyPage(t, page, fixture)
	var digest uint64
	for i := range page.rows {
		digest += latencyRowDigest(latencyPageRow(page, i))
	}
	if digest != fixture.digest {
		t.Fatalf("digest=%d want=%d", digest, fixture.digest)
	}
}

// The identical benchmark body is compiled against both row representations.
// Test-only access shims inline each representation's actual row/key access.
func BenchmarkPageLatency(b *testing.B) {
	fixture := newLatencyPageFixture()
	checksum := crc32.Checksum(fixture.data, crc32cTable)
	for _, phase := range []string{"checksum", "decode_reuse", "rows_visit", "keys_seek", "decode_visit"} {
		b.Run(phase, func(b *testing.B) {
			page, err := decodeMutationPage("latency", 0, 0, fixture.data, mainIndexBlockRecords)
			if err != nil {
				b.Fatal(err)
			}
			checkLatencyPage(b, page, fixture)
			b.ReportAllocs()
			b.ResetTimer()
			for operation := range b.N {
				switch phase {
				case "checksum":
					if crc32.Checksum(fixture.data, crc32cTable) != checksum {
						b.Fatal("checksum differs")
					}
				case "decode_reuse", "decode_visit":
					rows, err := decodeMutationPageRows("latency", 0, fixture.data, mainIndexBlockRecords, page.rows)
					if err != nil || len(rows) != mainIndexBlockRecords {
						b.Fatalf("decode: %v", err)
					}
					page.rows = rows
				}
				switch phase {
				case "rows_visit", "decode_visit":
					var digest uint64
					for i := range page.rows {
						digest += latencyRowDigest(latencyPageRow(page, i))
					}
					if digest != fixture.digest {
						b.Fatal("row digest differs")
					}
				case "keys_seek":
					want := (operation * 37) % mainIndexBlockRecords
					position := sort.Search(len(page.rows), func(i int) bool {
						return bytes.Compare(latencyPageKey(page, i), fixture.keys[want]) >= 0
					})
					if position != want {
						b.Fatalf("seek=%d want=%d", position, want)
					}
				}
			}
			b.StopTimer()
			checkLatencyPage(b, page, fixture)
		})
	}
}
