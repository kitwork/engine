package kitdb

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

const mainDirectoryEntryHeaderSize = 32

type pendingMainPage struct {
	block    mainBlock
	checksum hash.Hash32
}

type mainSnapshotWriter struct {
	path        string
	file        *os.File
	writer      *bufio.Writer
	position    int64
	records     uint64
	blocks      []mainBlock
	current     *pendingMainPage
	previousKey []byte
}

func newMainSnapshotWriter(path string, file *os.File) (*mainSnapshotWriter, error) {
	if _, err := writeAll(file, make([]byte, mainHeaderSize)); err != nil {
		return nil, fmt.Errorf("kitdb: reserve main-file header: %w", err)
	}
	return &mainSnapshotWriter{
		path: path, file: file, writer: bufio.NewWriterSize(file, 256<<10),
		position: mainHeaderSize,
	}, nil
}

func (writer *mainSnapshotWriter) append(key, value []byte) error {
	if err := validateKey(key); err != nil {
		return fmt.Errorf("kitdb: main snapshot has invalid key: %w", err)
	}
	if len(value) > maxValueSize {
		return fmt.Errorf("kitdb: main snapshot has invalid value: %w", ErrValueTooLarge)
	}
	if writer.previousKey != nil && bytes.Compare(writer.previousKey, key) >= 0 {
		return fmt.Errorf("kitdb: main snapshot keys are not strictly increasing")
	}
	recordSize := int64(mainRecordHeaderSize + len(key) + len(value))
	if recordSize > math.MaxInt64-writer.position-mainTrailerSize {
		return fmt.Errorf("kitdb: main snapshot size overflow")
	}
	if writer.current == nil || writer.current.block.records >= mainIndexBlockRecords || writer.current.block.length >= mainIndexBlockBytes {
		writer.finishPage()
		writer.current = &pendingMainPage{
			block:    mainBlock{firstKey: bytes.Clone(key), offset: writer.position},
			checksum: crc32.New(crc32cTable),
		}
	}

	var header [mainRecordHeaderSize]byte
	binary.LittleEndian.PutUint32(header[:4], uint32(len(key)))
	binary.LittleEndian.PutUint32(header[4:], uint32(len(value)))
	if err := writer.writePageBytes(header[:]); err != nil {
		return fmt.Errorf("kitdb: write main record header: %w", err)
	}
	if err := writer.writePageBytes(key); err != nil {
		return fmt.Errorf("kitdb: write main record key: %w", err)
	}
	if err := writer.writePageBytes(value); err != nil {
		return fmt.Errorf("kitdb: write main record value: %w", err)
	}
	keyCopy := bytes.Clone(key)
	writer.current.block.lastKey = keyCopy
	writer.current.block.records++
	writer.previousKey = keyCopy
	if writer.records == math.MaxUint64 {
		return fmt.Errorf("kitdb: main snapshot record count overflow")
	}
	writer.records++
	return nil
}

func (writer *mainSnapshotWriter) writePageBytes(data []byte) error {
	if _, err := writeAll(writer.writer, data); err != nil {
		return err
	}
	_, _ = writer.current.checksum.Write(data)
	length := int64(len(data))
	writer.current.block.length += length
	writer.position += length
	return nil
}

func (writer *mainSnapshotWriter) finishPage() {
	if writer.current == nil {
		return
	}
	writer.current.block.checksum = writer.current.checksum.Sum32()
	writer.blocks = append(writer.blocks, writer.current.block)
	writer.current = nil
}

func (writer *mainSnapshotWriter) finish(identity [16]byte, transaction uint64, boundaryChecksum uint32) error {
	writer.finishPage()
	if len(writer.blocks) > math.MaxUint32 {
		return fmt.Errorf("kitdb: main snapshot exceeds the page-directory limit")
	}
	if err := writer.writer.Flush(); err != nil {
		return fmt.Errorf("kitdb: flush main-file pages: %w", err)
	}

	directoryOffset := writer.position
	directorySize := int64(0)
	for _, block := range writer.blocks {
		entrySize := int64(mainDirectoryEntryHeaderSize + len(block.firstKey) + len(block.lastKey))
		if entrySize > math.MaxInt64-directorySize-mainTrailerSize {
			return fmt.Errorf("kitdb: main page directory size overflow")
		}
		directorySize += entrySize
	}
	if directorySize > math.MaxInt64-directoryOffset-mainTrailerSize {
		return fmt.Errorf("kitdb: main snapshot size overflow")
	}
	fileSize := directoryOffset + directorySize + mainTrailerSize
	header := encodeMainHeader(
		identity, transaction, writer.records, boundaryChecksum,
		uint64(directoryOffset), uint64(directorySize), uint32(len(writer.blocks)),
	)
	if _, err := writer.file.WriteAt(header, 0); err != nil {
		return fmt.Errorf("kitdb: finalize main-file header: %w", err)
	}
	if _, err := writer.file.Seek(directoryOffset, io.SeekStart); err != nil {
		return fmt.Errorf("kitdb: seek main-file directory: %w", err)
	}

	metadataChecksum := crc32.New(crc32cTable)
	_, _ = metadataChecksum.Write(header)
	directoryWriter := bufio.NewWriterSize(io.MultiWriter(writer.file, metadataChecksum), 256<<10)
	for _, block := range writer.blocks {
		var entry [mainDirectoryEntryHeaderSize]byte
		binary.LittleEndian.PutUint64(entry[:8], uint64(block.offset))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(block.length))
		binary.LittleEndian.PutUint32(entry[16:20], block.records)
		binary.LittleEndian.PutUint32(entry[20:24], block.checksum)
		binary.LittleEndian.PutUint32(entry[24:28], uint32(len(block.firstKey)))
		binary.LittleEndian.PutUint32(entry[28:32], uint32(len(block.lastKey)))
		if _, err := writeAll(directoryWriter, entry[:]); err != nil {
			return fmt.Errorf("kitdb: write main page-directory entry: %w", err)
		}
		if _, err := writeAll(directoryWriter, block.firstKey); err != nil {
			return fmt.Errorf("kitdb: write main page first key: %w", err)
		}
		if _, err := writeAll(directoryWriter, block.lastKey); err != nil {
			return fmt.Errorf("kitdb: write main page last key: %w", err)
		}
	}
	if err := directoryWriter.Flush(); err != nil {
		return fmt.Errorf("kitdb: flush main page directory: %w", err)
	}
	if _, err := writeAll(writer.file, encodeMainTrailer(metadataChecksum.Sum32(), uint64(fileSize))); err != nil {
		return fmt.Errorf("kitdb: write main-file trailer: %w", err)
	}
	return nil
}

func prepareMainSnapshot(path string, identity [16]byte, transaction uint64, boundaryChecksum uint32, walk func(emit func(key, value []byte) error) error) (stagingPath string, returnErr error) {
	if transaction == 0 && boundaryChecksum != 0 {
		return "", fmt.Errorf("kitdb: empty main snapshot has a transaction checksum")
	}
	directory := filepath.Dir(path)
	staging, err := os.CreateTemp(directory, ".kitdb-main-*.tmp")
	if err != nil {
		return "", fmt.Errorf("kitdb: create main-file staging: %w", err)
	}
	stagingPath = staging.Name()
	ready := false
	defer func() {
		_ = staging.Close()
		if !ready {
			_ = os.Remove(stagingPath)
		}
	}()
	if err := staging.Chmod(0o600); err != nil {
		return "", fmt.Errorf("kitdb: set main-file permissions: %w", err)
	}
	writer, err := newMainSnapshotWriter(path, staging)
	if err != nil {
		return "", err
	}
	if err := walk(writer.append); err != nil {
		return "", err
	}
	if err := writer.finish(identity, transaction, boundaryChecksum); err != nil {
		return "", err
	}
	if err := staging.Sync(); err != nil {
		return "", fmt.Errorf("kitdb: sync main-file staging: %w", err)
	}
	if err := staging.Close(); err != nil {
		return "", fmt.Errorf("kitdb: close main-file staging: %w", err)
	}
	ready = true
	return stagingPath, nil
}

func writeMainSnapshot(path string, identity [16]byte, transaction uint64, boundaryChecksum uint32, state map[string][]byte) error {
	keys := make([]string, 0, len(state))
	for key := range state {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	stagingPath, err := prepareMainSnapshot(path, identity, transaction, boundaryChecksum, func(emit func(key, value []byte) error) error {
		for _, key := range keys {
			if err := emit([]byte(key), state[key]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	defer os.Remove(stagingPath)
	_, err = publishPreparedMainSnapshot(stagingPath, path)
	return err
}
