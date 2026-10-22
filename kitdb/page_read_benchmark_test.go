package kitdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

// Each phase uses the same fully populated mutation page. File-read timings
// are warm OS-cache reads, not a cold-device or durability benchmark.
func BenchmarkPageReadPhases(b *testing.B) {
	var data []byte
	for i := range mainIndexBlockRecords {
		key := []byte(fmt.Sprintf("row/%04d", i))
		header := make([]byte, operationHeaderSize)
		header[0] = byte(operationPut)
		binary.LittleEndian.PutUint32(header[1:5], uint32(len(key)))
		binary.LittleEndian.PutUint32(header[5:9], 384)
		data = append(data, header...)
		data = append(data, key...)
		data = append(data, bytes.Repeat([]byte{byte(i)}, 384)...)
	}
	path := filepath.Join(b.TempDir(), "page.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		b.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := file.Close(); err != nil {
			b.Error(err)
		}
	})
	checksum := crc32.Checksum(data, crc32cTable)
	main := &mainImage{path: path, file: file, formatVersion: mainFormatVersion,
		blocks: []mainBlock{{firstKey: []byte("row/0000"), lastKey: []byte("row/0127"),
			length: int64(len(data)), records: mainIndexBlockRecords, checksum: checksum, mutations: true}}}
	for _, phase := range []string{"read", "checksum", "decode", "read_decode", "read_decode_reuse"} {
		b.Run(phase, func(b *testing.B) {
			scratch := make([]byte, len(data))
			var page rowPage
			if err := main.readPageInto(0, &page); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(data)))
			b.ResetTimer()
			for range b.N {
				switch phase {
				case "read":
					if err := readAt(file, scratch, 0); err != nil {
						b.Fatal(err)
					}
				case "checksum":
					if crc32.Checksum(data, crc32cTable) != checksum {
						b.Fatal("checksum differs")
					}
				case "decode":
					decoded, err := decodeMutationPage(path, 0, 0, data, mainIndexBlockRecords)
					if err != nil || len(decoded.rows) != mainIndexBlockRecords {
						b.Fatalf("decode: %v", err)
					}
				case "read_decode":
					decoded, err := main.readPage(0)
					if err != nil || len(decoded.rows) != mainIndexBlockRecords {
						b.Fatalf("read/decode: %v", err)
					}
				case "read_decode_reuse":
					if err := main.readPageInto(0, &page); err != nil || len(page.rows) != mainIndexBlockRecords {
						b.Fatalf("reuse: %v", err)
					}
				}
			}
			b.StopTimer()
			if phase == "read" && !bytes.Equal(scratch, data) {
				b.Fatal("read bytes differ")
			}
		})
	}
}
