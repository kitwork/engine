package kitdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestGenerationMainOpenReadsOnlyMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedRows(t, db, 4096, 1024)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	directoryBytes := db.main.directoryBytes
	stats, err := db.Stats()
	if err != nil {
		t.Fatal(err)
	}
	if stats.MainFormatVersion != mainFormatVersion || stats.MainDirectoryBytes != directoryBytes || stats.MainDataBytes <= 0 {
		t.Fatalf("paged main Stats = %+v", stats)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	counting := &countingMainReader{File: file}
	metadata, err := decodeMainSnapshot(path, counting, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	manifestBytes := int64(generationManifestHeaderSize + len(metadata.segments)*generationSegmentSize)
	wantRead := int64(8+generationHeaderSize+2*generationSlotSize) + manifestBytes + directoryBytes
	if counting.bytesRead != wantRead {
		t.Fatalf("paged Open read %d bytes, want metadata-only %d", counting.bytesRead, wantRead)
	}
	if counting.bytesRead >= info.Size()/4 {
		t.Fatalf("paged Open read %d of %d bytes; row payload was not lazy", counting.bytesRead, info.Size())
	}
	if metadata.formatVersion != mainFormatVersion || metadata.records != 4096 {
		t.Fatalf("paged metadata = version %d records %d", metadata.formatVersion, metadata.records)
	}
}

func TestMainDirectoryCorruptionIsRejectedOnOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedRows(t, db, 256, 128)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	directoryOffset := db.main.segments[0].descriptor.directoryOffset
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	checksumOffset := directoryOffset + 20
	if checksumOffset >= uint64(len(data)) {
		t.Fatalf("invalid test directory offset %d", directoryOffset)
	}
	data[checksumOffset] ^= 0xff
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open with corrupted directory error = %v, want ErrCorrupt", err)
	}
}

func TestLegacyMainReadsAndCheckpointUpgradesToPagedFormat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	writeLegacyMainForTest(t, path, map[string]string{
		"alpha":  "one",
		"middle": "two",
		"zulu":   "three",
	})

	db := mustOpen(t, path)
	if db.main.formatVersion != mainLegacyFormatVersion {
		t.Fatalf("legacy format = %d, want %d", db.main.formatVersion, mainLegacyFormatVersion)
	}
	if got := requireValue(t, db, "middle"); string(got) != "two" {
		t.Fatalf("legacy middle = %q", got)
	}
	if err := db.Verify(); err != nil {
		t.Fatalf("legacy Verify: %v", err)
	}
	if transaction, err := db.Checkpoint(); err != nil || transaction != 7 {
		t.Fatalf("legacy upgrade Checkpoint = (%d, %v), want (7, nil)", transaction, err)
	}
	if db.main.formatVersion != mainFormatVersion || db.main.directoryBytes == 0 {
		t.Fatalf("upgraded format/directory = %d/%d", db.main.formatVersion, db.main.directoryBytes)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := OpenWithOptions(path, OpenOptions{VerifyOnOpen: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := requireValue(t, db, "alpha"); string(got) != "one" {
		t.Fatalf("upgraded alpha = %q", got)
	}
	if got := requireValue(t, db, "zulu"); string(got) != "three" {
		t.Fatalf("upgraded zulu = %q", got)
	}
}

func TestVerifyScansPagesWithoutPopulatingCache(t *testing.T) {
	db := mustOpen(t, filepath.Join(t.TempDir(), "tenant.kitdb"))
	defer db.Close()
	seedRows(t, db, 1024, 512)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Verify(); err != nil {
		t.Fatal(err)
	}
	if used, pages := db.main.cache.usage(); used != 0 || pages != 0 {
		t.Fatalf("Verify populated page cache: %d bytes in %d pages", used, pages)
	}
}

func TestIncrementalCheckpointDoesNotHideCorruptedOlderPage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tenant.kitdb")
	db := mustOpen(t, path)
	seedRows(t, db, 64, 256)
	if _, err := db.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	pageOffset := db.main.blocks[0].offset
	firstKeySize := len(db.main.blocks[0].firstKey)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	valueOffset := pageOffset + operationHeaderSize + int64(firstKeySize)
	var valueByte [1]byte
	if _, err := file.ReadAt(valueByte[:], valueOffset); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	valueByte[0] ^= 0xff
	if _, err := file.WriteAt(valueByte[:], valueOffset); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	db = mustOpen(t, path)
	defer db.Close()
	if err := db.Verify(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify before incremental checkpoint = %v, want ErrCorrupt", err)
	}
	commitPut(t, db, "wal-tail", "still-authoritative")
	if _, err := db.Checkpoint(); err != nil {
		t.Fatalf("incremental Checkpoint: %v", err)
	}
	if got := requireValue(t, db, "wal-tail"); string(got) != "still-authoritative" {
		t.Fatalf("WAL tail after rejected checkpoint = %q", got)
	}
	if _, _, err := db.Get([]byte(rowKey(0))); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Get from corrupted older page = %v, want ErrCorrupt", err)
	}
	if err := db.Verify(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Verify after incremental checkpoint = %v, want ErrCorrupt", err)
	}
}

type countingMainReader struct {
	*os.File
	bytesRead int64
}

func (reader *countingMainReader) Read(target []byte) (int, error) {
	read, err := reader.File.Read(target)
	reader.bytesRead += int64(read)
	return read, err
}

func (reader *countingMainReader) ReadAt(target []byte, offset int64) (int, error) {
	read, err := reader.File.ReadAt(target, offset)
	reader.bytesRead += int64(read)
	return read, err
}

func writeLegacyMainForTest(t *testing.T, path string, rows map[string]string) {
	t.Helper()
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var identity [16]byte
	copy(identity[:], "legacy-kitdb-v1")
	header := encodeLegacyMainHeader(identity, 7, uint64(len(keys)), 0x12345678)
	checksum := crc32.New(crc32cTable)
	var content bytes.Buffer
	write := func(data []byte) {
		t.Helper()
		if _, err := content.Write(data); err != nil {
			t.Fatal(err)
		}
		_, _ = checksum.Write(data)
	}
	write(header)
	for _, key := range keys {
		value := rows[key]
		var recordHeader [mainRecordHeaderSize]byte
		binary.LittleEndian.PutUint32(recordHeader[:4], uint32(len(key)))
		binary.LittleEndian.PutUint32(recordHeader[4:], uint32(len(value)))
		write(recordHeader[:])
		write([]byte(key))
		write([]byte(value))
	}
	fileSize := uint64(content.Len() + mainTrailerSize)
	if _, err := content.Write(encodeMainTrailer(checksum.Sum32(), fileSize)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
