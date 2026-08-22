package kitdb

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"io"
	"math"
	"os"
)

const (
	mainHeaderSize       = 80
	mainHeaderChecksumAt = 76
	mainRecordHeaderSize = 8
	mainTrailerSize      = 24

	mainLegacyFormatVersion = 1
	mainPagedFormatVersion  = 2
	mainFormatVersion       = 3

	mainDirectoryOffsetAt = 52
	mainDirectorySizeAt   = 60
	mainPageCountAt       = 68

	mainMagic       = "KITDBM01"
	mainCommitMagic = "KITDBEND"
)

type mainReader interface {
	io.Reader
	io.ReaderAt
	io.Seeker
}

type mainHeaderMetadata struct {
	formatVersion    uint16
	identity         [16]byte
	transaction      uint64
	records          uint64
	boundaryChecksum uint32
	directoryOffset  uint64
	directorySize    uint64
	pageCount        uint32
}

type mainMetadata struct {
	formatVersion    uint16
	identity         [16]byte
	transaction      uint64
	boundaryChecksum uint32
	records          uint64
	recordEnd        int64
	dataBytes        int64
	directoryBytes   int64
	generation       uint64
	activeSlot       int
	fileEnd          int64
	segments         []mainSegment
	blocks           []mainBlock
}

func loadOrCreateMain(path string, pageCacheBytes int64) (*mainImage, error) {
	image, err := readMainSnapshotWithCache(path, pageCacheBytes)
	if err == nil {
		return image, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		return nil, fmt.Errorf("kitdb: create database identity: %w", err)
	}
	if err := writeEmptyGenerationMain(path, identity); err != nil {
		return nil, err
	}
	return readMainSnapshotWithCache(path, pageCacheBytes)
}

func readMainSnapshot(path string) (*mainImage, error) {
	return readMainSnapshotWithCache(path, defaultMainPageCacheBytes)
}

func readMainSnapshotWithCache(path string, pageCacheBytes int64) (*mainImage, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("kitdb: open main file %q: %w", path, err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("kitdb: stat main file %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, fmt.Errorf("kitdb: main path %q is not a regular file", path)
	}
	metadata, err := decodeMainSnapshot(path, file, info.Size())
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &mainImage{
		path: path, file: file, formatVersion: metadata.formatVersion,
		identity: metadata.identity, transaction: metadata.transaction,
		boundaryChecksum: metadata.boundaryChecksum, records: metadata.records,
		recordEnd: metadata.recordEnd, dataBytes: metadata.dataBytes,
		directoryBytes: metadata.directoryBytes, generation: metadata.generation,
		activeSlot: metadata.activeSlot, fileEnd: metadata.fileEnd,
		segments: metadata.segments, blocks: metadata.blocks,
		cache: newRowPageCache(pageCacheBytes),
	}, nil
}

func decodeMainSnapshot(path string, file mainReader, size int64) (*mainMetadata, error) {
	if size >= 8 {
		var magic [8]byte
		if err := readAt(file, magic[:], 0); err != nil {
			return nil, fmt.Errorf("kitdb: read main-file magic %q: %w", path, err)
		}
		if string(magic[:]) == generationHeaderMagic {
			return decodeGenerationMain(path, file, size)
		}
	}
	if size < mainHeaderSize+mainTrailerSize {
		return nil, corruptFileAt(path, 0, "incomplete main file")
	}

	headerBytes := make([]byte, mainHeaderSize)
	if err := readAt(file, headerBytes, 0); err != nil {
		return nil, fmt.Errorf("kitdb: read main-file header %q: %w", path, err)
	}
	header, err := decodeMainHeader(path, headerBytes)
	if err != nil {
		return nil, err
	}
	trailerOffset := size - mainTrailerSize
	trailer := make([]byte, mainTrailerSize)
	if err := readAt(file, trailer, trailerOffset); err != nil {
		return nil, fmt.Errorf("kitdb: read main-file trailer %q: %w", path, err)
	}
	declaredChecksum, err := decodeMainTrailer(path, trailerOffset, trailer, uint64(size))
	if err != nil {
		return nil, err
	}

	switch header.formatVersion {
	case mainLegacyFormatVersion:
		return decodeLegacyMainSnapshot(path, file, headerBytes, header, trailerOffset, declaredChecksum)
	case mainPagedFormatVersion:
		return decodePagedMainSnapshot(path, file, headerBytes, header, trailerOffset, declaredChecksum)
	default:
		return nil, corruptFileAt(path, 8, "unsupported main-file version %d", header.formatVersion)
	}
}

func decodeLegacyMainSnapshot(path string, file mainReader, headerBytes []byte, header mainHeaderMetadata, trailerOffset int64, declaredChecksum uint32) (*mainMetadata, error) {
	recordBytes := trailerOffset - mainHeaderSize
	minimumRecordSize := int64(mainRecordHeaderSize + 1)
	if header.records > uint64(recordBytes/minimumRecordSize) {
		return nil, corruptFileAt(path, 40, "record count %d cannot fit main file", header.records)
	}
	if _, err := file.Seek(mainHeaderSize, io.SeekStart); err != nil {
		return nil, fmt.Errorf("kitdb: seek legacy main-file records %q: %w", path, err)
	}
	checksum := crc32.New(crc32cTable)
	_, _ = checksum.Write(headerBytes)
	limited := &io.LimitedReader{R: file, N: recordBytes}
	reader := bufio.NewReaderSize(limited, minBufferSize(recordBytes))
	blocks := make([]mainBlock, 0)
	position := int64(mainHeaderSize)
	var previousKey []byte
	for index := uint64(0); index < header.records; index++ {
		recordOffset := position
		var recordHeader [mainRecordHeaderSize]byte
		if err := readMainContent(reader, checksum, recordHeader[:]); err != nil {
			return nil, corruptFileAt(path, position, "record %d header is truncated", index)
		}
		keySize := uint64(binary.LittleEndian.Uint32(recordHeader[:4]))
		valueSize := uint64(binary.LittleEndian.Uint32(recordHeader[4:]))
		position += mainRecordHeaderSize
		if keySize == 0 || keySize > maxKeySize {
			return nil, corruptFileAt(path, position-mainRecordHeaderSize, "record %d has invalid key size %d", index, keySize)
		}
		if valueSize > maxValueSize {
			return nil, corruptFileAt(path, position-mainRecordHeaderSize+4, "record %d has invalid value size %d", index, valueSize)
		}
		if keySize+valueSize > uint64(trailerOffset-position) {
			return nil, corruptFileAt(path, position, "record %d body is truncated", index)
		}
		key := make([]byte, int(keySize))
		if err := readMainContent(reader, checksum, key); err != nil {
			return nil, corruptFileAt(path, position, "record %d key is truncated", index)
		}
		position += int64(keySize)
		if _, err := io.CopyN(checksum, reader, int64(valueSize)); err != nil {
			return nil, corruptFileAt(path, position, "record %d value is truncated", index)
		}
		position += int64(valueSize)
		if previousKey != nil && bytes.Compare(previousKey, key) >= 0 {
			return nil, corruptFileAt(path, position-int64(keySize+valueSize), "record keys are not strictly increasing")
		}
		startBlock := len(blocks) == 0
		if !startBlock {
			current := &blocks[len(blocks)-1]
			startBlock = current.records >= mainIndexBlockRecords || recordOffset-current.offset >= mainIndexBlockBytes
			if startBlock {
				current.length = recordOffset - current.offset
				current.lastKey = bytes.Clone(previousKey)
			}
		}
		if startBlock {
			blocks = append(blocks, mainBlock{firstKey: bytes.Clone(key), offset: recordOffset})
		}
		blocks[len(blocks)-1].records++
		previousKey = key
	}
	if position != trailerOffset {
		return nil, corruptFileAt(path, position, "main file has %d trailing content bytes", trailerOffset-position)
	}
	if actual := checksum.Sum32(); actual != declaredChecksum {
		return nil, corruptFileAt(path, trailerOffset+8, "main-file checksum mismatch")
	}
	if len(blocks) != 0 {
		blocks[len(blocks)-1].length = trailerOffset - blocks[len(blocks)-1].offset
		blocks[len(blocks)-1].lastKey = bytes.Clone(previousKey)
	}
	return &mainMetadata{
		formatVersion: header.formatVersion, identity: header.identity,
		transaction: header.transaction, boundaryChecksum: header.boundaryChecksum,
		records: header.records, recordEnd: trailerOffset,
		dataBytes: trailerOffset - mainHeaderSize,
		fileEnd:   trailerOffset + mainTrailerSize, blocks: blocks,
	}, nil
}

func decodePagedMainSnapshot(path string, file mainReader, headerBytes []byte, header mainHeaderMetadata, trailerOffset int64, declaredChecksum uint32) (*mainMetadata, error) {
	if header.directoryOffset > math.MaxInt64 || header.directorySize > math.MaxInt64 {
		return nil, corruptFileAt(path, mainDirectoryOffsetAt, "main directory exceeds supported file offsets")
	}
	directoryOffset := int64(header.directoryOffset)
	directorySize := int64(header.directorySize)
	if directoryOffset < mainHeaderSize || directoryOffset > trailerOffset {
		return nil, corruptFileAt(path, mainDirectoryOffsetAt, "main directory offset %d is outside the file", header.directoryOffset)
	}
	if directorySize != trailerOffset-directoryOffset {
		return nil, corruptFileAt(path, mainDirectorySizeAt, "main directory size %d does not end at the completion trailer", header.directorySize)
	}
	if header.pageCount == 0 {
		if header.records != 0 || directorySize != 0 || directoryOffset != mainHeaderSize {
			return nil, corruptFileAt(path, mainPageCountAt, "empty page directory does not describe an empty main file")
		}
	} else {
		minimumDirectory := uint64(header.pageCount) * mainDirectoryEntryHeaderSize
		if header.records == 0 || header.records < uint64(header.pageCount) || header.records > uint64(header.pageCount)*mainIndexBlockRecords {
			return nil, corruptFileAt(path, 40, "record count %d is inconsistent with %d pages", header.records, header.pageCount)
		}
		if header.directorySize < minimumDirectory {
			return nil, corruptFileAt(path, mainDirectorySizeAt, "main directory is too small for %d pages", header.pageCount)
		}
	}
	if uint64(header.pageCount) > uint64(^uint(0)>>1) {
		return nil, corruptFileAt(path, mainPageCountAt, "main page count %d exceeds this platform", header.pageCount)
	}

	checksum := crc32.New(crc32cTable)
	_, _ = checksum.Write(headerBytes)
	section := io.NewSectionReader(file, directoryOffset, directorySize)
	reader := bufio.NewReaderSize(section, minBufferSize(directorySize))
	blocks := make([]mainBlock, 0, int(header.pageCount))
	expectedOffset := int64(mainHeaderSize)
	consumed := int64(0)
	totalRecords := uint64(0)
	var previousLastKey []byte
	for page := uint32(0); page < header.pageCount; page++ {
		entryOffset := directoryOffset + consumed
		if directorySize-consumed < mainDirectoryEntryHeaderSize {
			return nil, corruptFileAt(path, entryOffset, "main directory entry %d is truncated", page)
		}
		var entry [mainDirectoryEntryHeaderSize]byte
		if err := readMainContent(reader, checksum, entry[:]); err != nil {
			return nil, corruptFileAt(path, entryOffset, "main directory entry %d is truncated", page)
		}
		consumed += mainDirectoryEntryHeaderSize
		blockOffsetValue := binary.LittleEndian.Uint64(entry[:8])
		blockLengthValue := binary.LittleEndian.Uint64(entry[8:16])
		records := binary.LittleEndian.Uint32(entry[16:20])
		pageChecksum := binary.LittleEndian.Uint32(entry[20:24])
		firstKeySize := uint64(binary.LittleEndian.Uint32(entry[24:28]))
		lastKeySize := uint64(binary.LittleEndian.Uint32(entry[28:32]))
		if records == 0 || records > mainIndexBlockRecords {
			return nil, corruptFileAt(path, entryOffset+16, "main page %d has invalid record count %d", page, records)
		}
		if firstKeySize == 0 || firstKeySize > maxKeySize || lastKeySize == 0 || lastKeySize > maxKeySize {
			return nil, corruptFileAt(path, entryOffset+24, "main page %d has invalid key bounds", page)
		}
		keyBytes := firstKeySize + lastKeySize
		if keyBytes > uint64(directorySize-consumed) {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "main page %d key bounds are truncated", page)
		}
		firstKey := make([]byte, int(firstKeySize))
		lastKey := make([]byte, int(lastKeySize))
		if err := readMainContent(reader, checksum, firstKey); err != nil {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "main page %d first key is truncated", page)
		}
		if err := readMainContent(reader, checksum, lastKey); err != nil {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize+int64(firstKeySize), "main page %d last key is truncated", page)
		}
		consumed += int64(keyBytes)
		comparison := bytes.Compare(firstKey, lastKey)
		if (records == 1 && comparison != 0) || (records > 1 && comparison >= 0) {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "main page %d has inconsistent key bounds", page)
		}
		if previousLastKey != nil && bytes.Compare(previousLastKey, firstKey) >= 0 {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "main page key bounds are not strictly increasing")
		}
		if blockOffsetValue > math.MaxInt64 || blockLengthValue > math.MaxInt64 {
			return nil, corruptFileAt(path, entryOffset, "main page %d exceeds supported file offsets", page)
		}
		blockOffset := int64(blockOffsetValue)
		blockLength := int64(blockLengthValue)
		if blockOffset != expectedOffset {
			return nil, corruptFileAt(path, entryOffset, "main page %d starts at %d instead of %d", page, blockOffset, expectedOffset)
		}
		if blockLength < int64(records)*(mainRecordHeaderSize+1) || blockLength > directoryOffset-blockOffset {
			return nil, corruptFileAt(path, entryOffset+8, "main page %d has invalid length %d", page, blockLength)
		}
		expectedOffset += blockLength
		totalRecords += uint64(records)
		blocks = append(blocks, mainBlock{
			firstKey: firstKey, lastKey: lastKey, offset: blockOffset,
			length: blockLength, records: records, checksum: pageChecksum,
		})
		previousLastKey = lastKey
	}
	if consumed != directorySize {
		return nil, corruptFileAt(path, directoryOffset+consumed, "main directory has %d trailing bytes", directorySize-consumed)
	}
	if expectedOffset != directoryOffset {
		return nil, corruptFileAt(path, mainDirectoryOffsetAt, "main pages end at %d instead of directory offset %d", expectedOffset, directoryOffset)
	}
	if totalRecords != header.records {
		return nil, corruptFileAt(path, 40, "main pages contain %d records instead of %d", totalRecords, header.records)
	}
	if actual := checksum.Sum32(); actual != declaredChecksum {
		return nil, corruptFileAt(path, trailerOffset+8, "main metadata checksum mismatch")
	}
	return &mainMetadata{
		formatVersion: header.formatVersion, identity: header.identity,
		transaction: header.transaction, boundaryChecksum: header.boundaryChecksum,
		records: header.records, recordEnd: directoryOffset,
		dataBytes:      directoryOffset - mainHeaderSize,
		directoryBytes: directorySize,
		fileEnd:        trailerOffset + mainTrailerSize, blocks: blocks,
	}, nil
}

func encodeMainHeader(identity [16]byte, transaction, records uint64, boundaryChecksum uint32, directoryOffset, directorySize uint64, pageCount uint32) []byte {
	header := encodeMainHeaderVersion(mainPagedFormatVersion, identity, transaction, records, boundaryChecksum)
	binary.LittleEndian.PutUint64(header[mainDirectoryOffsetAt:mainDirectorySizeAt], directoryOffset)
	binary.LittleEndian.PutUint64(header[mainDirectorySizeAt:mainPageCountAt], directorySize)
	binary.LittleEndian.PutUint32(header[mainPageCountAt:72], pageCount)
	binary.LittleEndian.PutUint32(header[mainHeaderChecksumAt:], crc32.Checksum(header[:mainHeaderChecksumAt], crc32cTable))
	return header
}

func encodeLegacyMainHeader(identity [16]byte, transaction, records uint64, boundaryChecksum uint32) []byte {
	return encodeMainHeaderVersion(mainLegacyFormatVersion, identity, transaction, records, boundaryChecksum)
}

func encodeMainHeaderVersion(version uint16, identity [16]byte, transaction, records uint64, boundaryChecksum uint32) []byte {
	header := make([]byte, mainHeaderSize)
	copy(header[:8], mainMagic)
	binary.LittleEndian.PutUint16(header[8:10], version)
	binary.LittleEndian.PutUint16(header[10:12], mainHeaderSize)
	copy(header[16:32], identity[:])
	binary.LittleEndian.PutUint64(header[32:40], transaction)
	binary.LittleEndian.PutUint64(header[40:48], records)
	binary.LittleEndian.PutUint32(header[48:52], boundaryChecksum)
	binary.LittleEndian.PutUint32(header[mainHeaderChecksumAt:], crc32.Checksum(header[:mainHeaderChecksumAt], crc32cTable))
	return header
}

func decodeMainHeader(path string, header []byte) (mainHeaderMetadata, error) {
	var metadata mainHeaderMetadata
	if string(header[:8]) != mainMagic {
		return metadata, corruptFileAt(path, 0, "invalid main-file magic")
	}
	metadata.formatVersion = binary.LittleEndian.Uint16(header[8:10])
	if metadata.formatVersion != mainLegacyFormatVersion && metadata.formatVersion != mainPagedFormatVersion {
		return metadata, corruptFileAt(path, 8, "unsupported main-file version %d", metadata.formatVersion)
	}
	if size := binary.LittleEndian.Uint16(header[10:12]); size != mainHeaderSize {
		return metadata, corruptFileAt(path, 10, "invalid main-file header size %d", size)
	}
	if flags := binary.LittleEndian.Uint32(header[12:16]); flags != 0 {
		return metadata, corruptFileAt(path, 12, "unsupported main-file flags %d", flags)
	}
	reservedStart := 52
	if metadata.formatVersion == mainPagedFormatVersion {
		metadata.directoryOffset = binary.LittleEndian.Uint64(header[mainDirectoryOffsetAt:mainDirectorySizeAt])
		metadata.directorySize = binary.LittleEndian.Uint64(header[mainDirectorySizeAt:mainPageCountAt])
		metadata.pageCount = binary.LittleEndian.Uint32(header[mainPageCountAt:72])
		reservedStart = 72
	}
	for offset := reservedStart; offset < mainHeaderChecksumAt; offset++ {
		if header[offset] != 0 {
			return metadata, corruptFileAt(path, int64(offset), "unsupported main-file reserved value")
		}
	}
	declared := binary.LittleEndian.Uint32(header[mainHeaderChecksumAt:])
	if actual := crc32.Checksum(header[:mainHeaderChecksumAt], crc32cTable); actual != declared {
		return metadata, corruptFileAt(path, mainHeaderChecksumAt, "main-file header checksum mismatch")
	}
	copy(metadata.identity[:], header[16:32])
	metadata.transaction = binary.LittleEndian.Uint64(header[32:40])
	metadata.records = binary.LittleEndian.Uint64(header[40:48])
	metadata.boundaryChecksum = binary.LittleEndian.Uint32(header[48:52])
	if metadata.transaction == 0 && metadata.boundaryChecksum != 0 {
		return metadata, corruptFileAt(path, 48, "empty main file has a transaction checksum")
	}
	return metadata, nil
}

func encodeMainTrailer(checksum uint32, fileSize uint64) []byte {
	trailer := make([]byte, mainTrailerSize)
	copy(trailer[:8], mainCommitMagic)
	binary.LittleEndian.PutUint32(trailer[8:12], checksum)
	binary.LittleEndian.PutUint64(trailer[16:24], fileSize)
	return trailer
}

func decodeMainTrailer(path string, offset int64, trailer []byte, actualSize uint64) (uint32, error) {
	if string(trailer[:8]) != mainCommitMagic {
		return 0, corruptFileAt(path, offset, "invalid main-file completion marker")
	}
	if reserved := binary.LittleEndian.Uint32(trailer[12:16]); reserved != 0 {
		return 0, corruptFileAt(path, offset+12, "unsupported main-file trailer value %d", reserved)
	}
	if declared := binary.LittleEndian.Uint64(trailer[16:24]); declared != actualSize {
		return 0, corruptFileAt(path, offset+16, "declared main-file size %d does not match %d", declared, actualSize)
	}
	return binary.LittleEndian.Uint32(trailer[8:12]), nil
}

func readMainContent(reader io.Reader, checksum hash.Hash32, target []byte) error {
	if _, err := io.ReadFull(reader, target); err != nil {
		return err
	}
	_, _ = checksum.Write(target)
	return nil
}
