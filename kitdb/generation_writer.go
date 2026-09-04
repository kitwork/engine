package kitdb

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
)

type generationSegmentWriter struct {
	file        *os.File
	writer      *bufio.Writer
	start       int64
	position    int64
	mutations   uint64
	blocks      []mainBlock
	current     *pendingMainPage
	previousKey []byte
}

type generationAppendResult struct {
	generation       uint64
	transaction      uint64
	boundaryChecksum uint32
	logicalRecords   uint64
	activeSlot       int
	fileEnd          int64
	descriptor       generationSegmentDescriptor
	blocks           []mainBlock
}

func newGenerationSegmentWriter(file *os.File, offset int64) (*generationSegmentWriter, error) {
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, fmt.Errorf("kitdb: seek generation segment: %w", err)
	}
	return &generationSegmentWriter{
		file: file, writer: bufio.NewWriterSize(file, mainIndexBlockBytes),
		start: offset, position: offset,
	}, nil
}

func (writer *generationSegmentWriter) append(key []byte, mutation rowMutation) error {
	if err := validateKey(key); err != nil {
		return fmt.Errorf("kitdb: generation segment has invalid key: %w", err)
	}
	if len(mutation.value) > maxValueSize {
		return fmt.Errorf("kitdb: generation segment has invalid value: %w", ErrValueTooLarge)
	}
	if writer.previousKey != nil && bytes.Compare(writer.previousKey, key) >= 0 {
		return fmt.Errorf("kitdb: generation segment keys are not strictly increasing")
	}
	value := mutation.value
	if mutation.deleted {
		value = nil
	}
	recordSize := int64(operationHeaderSize + len(key) + len(value))
	if recordSize > math.MaxInt64-writer.position {
		return fmt.Errorf("kitdb: generation segment size overflow")
	}
	if writer.current == nil || writer.current.block.records >= mainIndexBlockRecords || writer.current.block.length >= mainIndexBlockBytes {
		writer.finishPage()
		writer.current = &pendingMainPage{
			block:    mainBlock{firstKey: bytes.Clone(key), offset: writer.position, mutations: true},
			checksum: crc32.New(crc32cTable),
		}
	}
	var header [operationHeaderSize]byte
	header[0] = byte(operationPut)
	if mutation.deleted {
		header[0] = byte(operationDelete)
	}
	binary.LittleEndian.PutUint32(header[1:5], uint32(len(key)))
	binary.LittleEndian.PutUint32(header[5:9], uint32(len(value)))
	if err := writer.writePageBytes(header[:]); err != nil {
		return fmt.Errorf("kitdb: write generation mutation header: %w", err)
	}
	if err := writer.writePageBytes(key); err != nil {
		return fmt.Errorf("kitdb: write generation mutation key: %w", err)
	}
	if err := writer.writePageBytes(value); err != nil {
		return fmt.Errorf("kitdb: write generation mutation value: %w", err)
	}
	keyCopy := bytes.Clone(key)
	writer.current.block.lastKey = keyCopy
	writer.current.block.records++
	writer.previousKey = keyCopy
	if writer.mutations == math.MaxUint64 {
		return fmt.Errorf("kitdb: generation mutation count overflow")
	}
	writer.mutations++
	return nil
}

func (writer *generationSegmentWriter) writePageBytes(data []byte) error {
	if _, err := writeAll(writer.writer, data); err != nil {
		return err
	}
	_, _ = writer.current.checksum.Write(data)
	length := int64(len(data))
	writer.current.block.length += length
	writer.position += length
	return nil
}

func (writer *generationSegmentWriter) finishPage() {
	if writer.current == nil {
		return
	}
	writer.current.block.checksum = writer.current.checksum.Sum32()
	writer.blocks = append(writer.blocks, writer.current.block)
	writer.current = nil
}

func (writer *generationSegmentWriter) finish(generation, transaction uint64) (generationSegmentDescriptor, error) {
	writer.finishPage()
	if writer.mutations == 0 || len(writer.blocks) == 0 {
		return generationSegmentDescriptor{}, fmt.Errorf("kitdb: cannot persist an empty generation segment")
	}
	if len(writer.blocks) > math.MaxUint32 {
		return generationSegmentDescriptor{}, fmt.Errorf("kitdb: generation segment exceeds page-directory limit")
	}
	if err := writer.writer.Flush(); err != nil {
		return generationSegmentDescriptor{}, fmt.Errorf("kitdb: flush generation pages: %w", err)
	}
	directoryOffset := writer.position
	directorySize := int64(0)
	for _, block := range writer.blocks {
		entrySize := int64(mainDirectoryEntryHeaderSize + len(block.firstKey) + len(block.lastKey))
		if entrySize > math.MaxInt64-directorySize {
			return generationSegmentDescriptor{}, fmt.Errorf("kitdb: generation directory size overflow")
		}
		directorySize += entrySize
	}
	directoryChecksum := crc32.New(crc32cTable)
	directoryWriter := writer.writer
	directoryWriter.Reset(io.MultiWriter(writer.file, directoryChecksum))
	for _, block := range writer.blocks {
		var entry [mainDirectoryEntryHeaderSize]byte
		binary.LittleEndian.PutUint64(entry[:8], uint64(block.offset))
		binary.LittleEndian.PutUint64(entry[8:16], uint64(block.length))
		binary.LittleEndian.PutUint32(entry[16:20], block.records)
		binary.LittleEndian.PutUint32(entry[20:24], block.checksum)
		binary.LittleEndian.PutUint32(entry[24:28], uint32(len(block.firstKey)))
		binary.LittleEndian.PutUint32(entry[28:32], uint32(len(block.lastKey)))
		if _, err := writeAll(directoryWriter, entry[:]); err != nil {
			return generationSegmentDescriptor{}, fmt.Errorf("kitdb: write generation directory entry: %w", err)
		}
		if _, err := writeAll(directoryWriter, block.firstKey); err != nil {
			return generationSegmentDescriptor{}, fmt.Errorf("kitdb: write generation first key: %w", err)
		}
		if _, err := writeAll(directoryWriter, block.lastKey); err != nil {
			return generationSegmentDescriptor{}, fmt.Errorf("kitdb: write generation last key: %w", err)
		}
	}
	if err := directoryWriter.Flush(); err != nil {
		return generationSegmentDescriptor{}, fmt.Errorf("kitdb: flush generation directory: %w", err)
	}
	writer.position += directorySize
	return generationSegmentDescriptor{
		generation: generation, transaction: transaction,
		dataOffset: uint64(writer.start), dataSize: uint64(directoryOffset - writer.start),
		directoryOffset: uint64(directoryOffset), directorySize: uint64(directorySize),
		mutations: writer.mutations, pageCount: uint32(len(writer.blocks)),
		directoryChecksum: directoryChecksum.Sum32(),
	}, nil
}

func appendIncrementalGeneration(path string, main *mainImage, overlay map[string]rowMutation, transaction uint64, boundaryChecksum uint32, logicalRecords uint64) (*generationAppendResult, error) {
	if main == nil || main.formatVersion != mainFormatVersion || len(overlay) == 0 {
		return nil, fmt.Errorf("kitdb: incremental checkpoint requires a generation main file and mutations")
	}
	if main.generation == math.MaxUint64 {
		return nil, fmt.Errorf("kitdb: generation ID overflow")
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("kitdb: open generation main file for append: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("kitdb: stat generation main file for append: %w", err)
	}
	if info.Size() < main.fileEnd {
		return nil, corruptFileAt(path, info.Size(), "generation main file is shorter than its active boundary")
	}
	if info.Size() != main.fileEnd {
		if err := file.Truncate(main.fileEnd); err != nil {
			return nil, fmt.Errorf("kitdb: truncate abandoned generation tail: %w", err)
		}
	}
	generation := main.generation + 1
	segmentWriter, err := newGenerationSegmentWriter(file, main.fileEnd)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := segmentWriter.append([]byte(key), overlay[key]); err != nil {
			return nil, err
		}
	}
	descriptor, err := segmentWriter.finish(generation, transaction)
	if err != nil {
		return nil, err
	}
	descriptors := make([]generationSegmentDescriptor, 0, len(main.segments)+1)
	for _, segment := range main.segments {
		descriptors = append(descriptors, segment.descriptor)
	}
	descriptors = append(descriptors, descriptor)
	manifestOffset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, fmt.Errorf("kitdb: locate generation manifest: %w", err)
	}
	manifest := encodeGenerationManifest(main.identity, generation, transaction, boundaryChecksum, logicalRecords, descriptors)
	if _, err := writeAll(file, manifest); err != nil {
		return nil, fmt.Errorf("kitdb: write generation manifest: %w", err)
	}
	fileEnd := manifestOffset + int64(len(manifest))
	if err := file.Sync(); err != nil {
		return nil, fmt.Errorf("kitdb: sync generation data before publication: %w", err)
	}
	slotIndex := 1 - main.activeSlot
	slot := encodeGenerationSlot(main.identity, generationSlot{
		index: slotIndex, generation: generation, transaction: transaction,
		boundaryChecksum: boundaryChecksum, manifestOffset: uint64(manifestOffset),
		manifestSize: uint64(len(manifest)), fileEnd: uint64(fileEnd),
		manifestChecksum: crc32.Checksum(manifest, crc32cTable),
		segmentCount:     uint32(len(descriptors)), records: logicalRecords,
	})
	slotOffset := int64(generationSlotAOffset + slotIndex*generationSlotSize)
	if err := writeAllAt(file, slot, slotOffset); err != nil {
		return nil, errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: publish generation slot: %w", err))
	}
	if err := file.Sync(); err != nil {
		return nil, errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync generation slot: %w", err))
	}
	return &generationAppendResult{
		generation: generation, transaction: transaction,
		boundaryChecksum: boundaryChecksum, logicalRecords: logicalRecords,
		activeSlot: slotIndex, fileEnd: fileEnd, descriptor: descriptor,
		blocks: segmentWriter.blocks,
	}, nil
}

func openAppendedGeneration(path string, previous *mainImage, result *generationAppendResult, pageCacheBytes int64) (*mainImage, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("kitdb: open appended generation reader: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("kitdb: stat appended generation reader: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() < result.fileEnd {
		_ = file.Close()
		return nil, corruptFileAt(path, info.Size(), "appended generation file is incomplete")
	}
	blocks := make([]mainBlock, len(previous.blocks)+len(result.blocks))
	copy(blocks, previous.blocks)
	copy(blocks[len(previous.blocks):], result.blocks)
	segments := make([]mainSegment, len(previous.segments)+1)
	copy(segments, previous.segments)
	segments[len(previous.segments)] = mainSegment{
		generation: result.descriptor.generation, transaction: result.descriptor.transaction,
		firstBlock: len(previous.blocks), blockCount: len(result.blocks), mutations: result.descriptor.mutations,
		dataBytes: int64(result.descriptor.dataSize), directoryBytes: int64(result.descriptor.directorySize),
		descriptor: result.descriptor,
	}
	return &mainImage{
		path: path, file: file, formatVersion: mainFormatVersion,
		identity: previous.identity, transaction: result.transaction,
		boundaryChecksum: result.boundaryChecksum, records: result.logicalRecords,
		recordEnd: result.fileEnd, dataBytes: previous.dataBytes + int64(result.descriptor.dataSize),
		directoryBytes: previous.directoryBytes + int64(result.descriptor.directorySize),
		generation:     result.generation, activeSlot: result.activeSlot, fileEnd: result.fileEnd,
		segments: segments, blocks: blocks, cache: newRowPageCache(pageCacheBytes),
	}, nil
}

func prepareCompactedGeneration(path string, identity [16]byte, transaction uint64, boundaryChecksum uint32, generation uint64, walk func(emit func(key, value []byte) error) error) (stagingPath string, returnErr error) {
	file, err := os.CreateTemp(filepath.Dir(path), ".kitdb-main-*.tmp")
	if err != nil {
		return "", fmt.Errorf("kitdb: create compacted generation staging: %w", err)
	}
	temporaryPath := file.Name()
	stagingPath = temporaryPath
	ready := false
	defer func() {
		closeErr := file.Close()
		if !ready {
			returnErr = errors.Join(returnErr, closeErr, os.Remove(temporaryPath))
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("kitdb: set compacted generation permissions: %w", err)
	}
	if _, err := writeAll(file, make([]byte, generationDataOffset)); err != nil {
		return "", fmt.Errorf("kitdb: reserve compacted generation superblocks: %w", err)
	}
	if _, err := file.WriteAt(encodeGenerationHeader(identity), 0); err != nil {
		return "", fmt.Errorf("kitdb: write compacted generation header: %w", err)
	}
	segmentWriter, err := newGenerationSegmentWriter(file, generationDataOffset)
	if err != nil {
		return "", err
	}
	records := uint64(0)
	if err := walk(func(key, value []byte) error {
		if err := segmentWriter.append(key, rowMutation{value: value}); err != nil {
			return err
		}
		records++
		return nil
	}); err != nil {
		return "", err
	}
	descriptors := make([]generationSegmentDescriptor, 0, 1)
	if records != 0 {
		descriptor, err := segmentWriter.finish(generation, transaction)
		if err != nil {
			return "", err
		}
		descriptors = append(descriptors, descriptor)
	} else if err := segmentWriter.writer.Flush(); err != nil {
		return "", fmt.Errorf("kitdb: flush empty compacted generation: %w", err)
	}
	if records == 0 && transaction == 0 {
		if boundaryChecksum != 0 {
			return "", fmt.Errorf("kitdb: empty compacted generation has a transaction checksum")
		}
		if err := file.Truncate(generationDataOffset); err != nil {
			return "", fmt.Errorf("kitdb: truncate empty compacted generation: %w", err)
		}
		slot := encodeGenerationSlot(identity, generationSlot{
			index: 0, manifestOffset: generationDataOffset, fileEnd: generationDataOffset,
		})
		if _, err := file.WriteAt(slot, generationSlotAOffset); err != nil {
			return "", fmt.Errorf("kitdb: write empty compacted generation slot: %w", err)
		}
		if err := file.Sync(); err != nil {
			return "", fmt.Errorf("kitdb: sync empty compacted generation staging: %w", err)
		}
		if err := file.Close(); err != nil {
			return "", fmt.Errorf("kitdb: close empty compacted generation staging: %w", err)
		}
		ready = true
		return stagingPath, nil
	}
	manifestOffset, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return "", fmt.Errorf("kitdb: locate compacted generation manifest: %w", err)
	}
	manifest := encodeGenerationManifest(identity, generation, transaction, boundaryChecksum, records, descriptors)
	if _, err := writeAll(file, manifest); err != nil {
		return "", fmt.Errorf("kitdb: write compacted generation manifest: %w", err)
	}
	fileEnd := manifestOffset + int64(len(manifest))
	slot := encodeGenerationSlot(identity, generationSlot{
		index: 0, generation: generation, transaction: transaction,
		boundaryChecksum: boundaryChecksum, manifestOffset: uint64(manifestOffset),
		manifestSize: uint64(len(manifest)), fileEnd: uint64(fileEnd),
		manifestChecksum: crc32.Checksum(manifest, crc32cTable),
		segmentCount:     uint32(len(descriptors)), records: records,
	})
	if _, err := file.WriteAt(slot, generationSlotAOffset); err != nil {
		return "", fmt.Errorf("kitdb: write compacted generation slot: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("kitdb: sync compacted generation staging: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("kitdb: close compacted generation staging: %w", err)
	}
	ready = true
	return stagingPath, nil
}

func encodeGenerationManifest(identity [16]byte, generation, transaction uint64, boundaryChecksum uint32, records uint64, descriptors []generationSegmentDescriptor) []byte {
	manifest := make([]byte, generationManifestHeaderSize+len(descriptors)*generationSegmentSize)
	copy(manifest[:8], generationManifestMagic)
	binary.LittleEndian.PutUint16(manifest[8:10], mainFormatVersion)
	binary.LittleEndian.PutUint16(manifest[10:12], generationManifestHeaderSize)
	copy(manifest[16:32], identity[:])
	binary.LittleEndian.PutUint64(manifest[32:40], generation)
	binary.LittleEndian.PutUint64(manifest[40:48], transaction)
	binary.LittleEndian.PutUint32(manifest[48:52], boundaryChecksum)
	binary.LittleEndian.PutUint32(manifest[52:56], uint32(len(descriptors)))
	binary.LittleEndian.PutUint64(manifest[56:64], records)
	position := generationManifestHeaderSize
	for _, descriptor := range descriptors {
		data := manifest[position : position+generationSegmentSize]
		binary.LittleEndian.PutUint64(data[:8], descriptor.generation)
		binary.LittleEndian.PutUint64(data[8:16], descriptor.transaction)
		binary.LittleEndian.PutUint64(data[16:24], descriptor.dataOffset)
		binary.LittleEndian.PutUint64(data[24:32], descriptor.dataSize)
		binary.LittleEndian.PutUint64(data[32:40], descriptor.directoryOffset)
		binary.LittleEndian.PutUint64(data[40:48], descriptor.directorySize)
		binary.LittleEndian.PutUint64(data[48:56], descriptor.mutations)
		binary.LittleEndian.PutUint32(data[56:60], descriptor.pageCount)
		binary.LittleEndian.PutUint32(data[60:64], descriptor.directoryChecksum)
		position += generationSegmentSize
	}
	return manifest
}

func writeAllAt(file *os.File, data []byte, offset int64) error {
	written := 0
	for written < len(data) {
		count, err := file.WriteAt(data[written:], offset+int64(written))
		written += count
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}
