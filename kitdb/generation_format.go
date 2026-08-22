package kitdb

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
)

const (
	generationHeaderSize         = 64
	generationHeaderChecksumAt   = 60
	generationSlotSize           = 128
	generationSlotChecksumAt     = 124
	generationSlotAOffset        = 64
	generationSlotBOffset        = generationSlotAOffset + generationSlotSize
	generationDataOffset         = 4096
	generationManifestHeaderSize = 64
	generationSegmentSize        = 64
	maxMainSegments              = 32

	generationHeaderMagic   = "KITDBM03"
	generationSlotMagic     = "KITDBS03"
	generationManifestMagic = "KITDBMF3"
)

type generationSlot struct {
	index            int
	generation       uint64
	transaction      uint64
	boundaryChecksum uint32
	manifestOffset   uint64
	manifestSize     uint64
	fileEnd          uint64
	manifestChecksum uint32
	segmentCount     uint32
	records          uint64
}

type generationSegmentDescriptor struct {
	generation        uint64
	transaction       uint64
	dataOffset        uint64
	dataSize          uint64
	directoryOffset   uint64
	directorySize     uint64
	mutations         uint64
	pageCount         uint32
	directoryChecksum uint32
}

func writeEmptyGenerationMain(path string, identity [16]byte) error {
	staging, err := os.CreateTemp(filepath.Dir(path), ".kitdb-main-*.tmp")
	if err != nil {
		return fmt.Errorf("kitdb: create generation main-file staging: %w", err)
	}
	stagingPath := staging.Name()
	ready := false
	defer func() {
		_ = staging.Close()
		if !ready {
			_ = os.Remove(stagingPath)
		}
	}()
	if err := staging.Chmod(0o600); err != nil {
		return fmt.Errorf("kitdb: set generation main-file permissions: %w", err)
	}
	if _, err := writeAll(staging, make([]byte, generationDataOffset)); err != nil {
		return fmt.Errorf("kitdb: reserve generation superblock area: %w", err)
	}
	header := encodeGenerationHeader(identity)
	slot := encodeGenerationSlot(identity, generationSlot{
		index: 0, manifestOffset: generationDataOffset, fileEnd: generationDataOffset,
	})
	if _, err := staging.WriteAt(header, 0); err != nil {
		return fmt.Errorf("kitdb: write generation header: %w", err)
	}
	if _, err := staging.WriteAt(slot, generationSlotAOffset); err != nil {
		return fmt.Errorf("kitdb: write initial generation slot: %w", err)
	}
	if err := staging.Sync(); err != nil {
		return fmt.Errorf("kitdb: sync generation main-file staging: %w", err)
	}
	if err := staging.Close(); err != nil {
		return fmt.Errorf("kitdb: close generation main-file staging: %w", err)
	}
	ready = true
	published, err := publishPreparedMainSnapshot(stagingPath, path)
	if !published {
		_ = os.Remove(stagingPath)
	}
	return err
}

func decodeGenerationMain(path string, file mainReader, size int64) (*mainMetadata, error) {
	if size < generationDataOffset {
		return nil, corruptFileAt(path, 0, "incomplete generation main file")
	}
	header := make([]byte, generationHeaderSize)
	if err := readAt(file, header, 0); err != nil {
		return nil, fmt.Errorf("kitdb: read generation header %q: %w", path, err)
	}
	identity, err := decodeGenerationHeader(path, header)
	if err != nil {
		return nil, err
	}

	var slots [2]generationSlot
	var valid [2]bool
	for index, offset := range []int64{generationSlotAOffset, generationSlotBOffset} {
		data := make([]byte, generationSlotSize)
		if err := readAt(file, data, offset); err != nil {
			return nil, fmt.Errorf("kitdb: read generation slot %d: %w", index, err)
		}
		slot, ok, err := decodeGenerationSlot(path, data, offset, index, identity, uint64(size))
		if err != nil {
			return nil, err
		}
		slots[index], valid[index] = slot, ok
	}
	if !valid[0] && !valid[1] {
		return nil, corruptFileAt(path, generationSlotAOffset, "no valid generation slot")
	}
	selected := 0
	if !valid[0] || valid[1] && slots[1].generation > slots[0].generation {
		selected = 1
	} else if valid[1] && slots[1].generation == slots[0].generation && !sameGenerationSlot(slots[0], slots[1]) {
		return nil, corruptFileAt(path, generationSlotBOffset, "generation slots disagree at generation %d", slots[0].generation)
	}
	slot := slots[selected]
	if slot.segmentCount == 0 && slot.generation == 0 {
		return &mainMetadata{
			formatVersion: mainFormatVersion, identity: identity,
			transaction: slot.transaction, boundaryChecksum: slot.boundaryChecksum,
			records: slot.records, recordEnd: int64(slot.fileEnd),
			generation: slot.generation, activeSlot: selected, fileEnd: int64(slot.fileEnd),
		}, nil
	}

	descriptors, err := decodeGenerationManifest(path, file, identity, slot)
	if err != nil {
		return nil, err
	}
	blocks := make([]mainBlock, 0)
	segments := make([]mainSegment, 0, len(descriptors))
	dataBytes := int64(0)
	directoryBytes := int64(0)
	var previousRegionEnd uint64 = generationDataOffset
	var previousGeneration uint64
	var previousTransaction uint64
	for index, descriptor := range descriptors {
		if descriptor.generation == 0 || descriptor.generation > slot.generation || index > 0 && descriptor.generation <= previousGeneration {
			return nil, corruptFileAt(path, int64(slot.manifestOffset)+generationManifestHeaderSize+int64(index*generationSegmentSize), "segment generations are not strictly increasing")
		}
		if descriptor.transaction == 0 || descriptor.transaction > slot.transaction || index > 0 && descriptor.transaction <= previousTransaction {
			return nil, corruptFileAt(path, int64(slot.manifestOffset)+generationManifestHeaderSize+int64(index*generationSegmentSize)+8, "segment transactions are not strictly increasing")
		}
		if descriptor.dataOffset < previousRegionEnd || descriptor.dataSize == 0 || descriptor.dataOffset > math.MaxUint64-descriptor.dataSize {
			return nil, corruptFileAt(path, int64(slot.manifestOffset)+generationManifestHeaderSize+int64(index*generationSegmentSize)+16, "segment %d has invalid data bounds", index)
		}
		if descriptor.directoryOffset != descriptor.dataOffset+descriptor.dataSize || descriptor.directoryOffset > math.MaxUint64-descriptor.directorySize {
			return nil, corruptFileAt(path, int64(slot.manifestOffset)+generationManifestHeaderSize+int64(index*generationSegmentSize)+32, "segment %d directory does not follow its pages", index)
		}
		directoryEnd := descriptor.directoryOffset + descriptor.directorySize
		if directoryEnd > slot.manifestOffset || descriptor.pageCount == 0 || descriptor.mutations < uint64(descriptor.pageCount) || descriptor.mutations > uint64(descriptor.pageCount)*mainIndexBlockRecords {
			return nil, corruptFileAt(path, int64(slot.manifestOffset)+generationManifestHeaderSize+int64(index*generationSegmentSize)+40, "segment %d descriptor is inconsistent", index)
		}
		segmentBlocks, err := decodeGenerationDirectory(path, file, descriptor)
		if err != nil {
			return nil, err
		}
		firstBlock := len(blocks)
		blocks = append(blocks, segmentBlocks...)
		segments = append(segments, mainSegment{
			generation: descriptor.generation, transaction: descriptor.transaction,
			firstBlock: firstBlock, blockCount: len(segmentBlocks), mutations: descriptor.mutations,
			dataBytes: int64(descriptor.dataSize), directoryBytes: int64(descriptor.directorySize),
			descriptor: descriptor,
		})
		dataBytes += int64(descriptor.dataSize)
		directoryBytes += int64(descriptor.directorySize)
		previousRegionEnd = directoryEnd
		previousGeneration = descriptor.generation
		previousTransaction = descriptor.transaction
	}
	return &mainMetadata{
		formatVersion: mainFormatVersion, identity: identity,
		transaction: slot.transaction, boundaryChecksum: slot.boundaryChecksum,
		records: slot.records, recordEnd: int64(slot.fileEnd), dataBytes: dataBytes,
		directoryBytes: directoryBytes, generation: slot.generation,
		activeSlot: selected, fileEnd: int64(slot.fileEnd), segments: segments, blocks: blocks,
	}, nil
}

func sameGenerationSlot(left, right generationSlot) bool {
	left.index = 0
	right.index = 0
	return left == right
}

func encodeGenerationHeader(identity [16]byte) []byte {
	header := make([]byte, generationHeaderSize)
	copy(header[:8], generationHeaderMagic)
	binary.LittleEndian.PutUint16(header[8:10], mainFormatVersion)
	binary.LittleEndian.PutUint16(header[10:12], generationHeaderSize)
	copy(header[16:32], identity[:])
	binary.LittleEndian.PutUint32(header[generationHeaderChecksumAt:], crc32.Checksum(header[:generationHeaderChecksumAt], crc32cTable))
	return header
}

func decodeGenerationHeader(path string, header []byte) ([16]byte, error) {
	var identity [16]byte
	if string(header[:8]) != generationHeaderMagic {
		return identity, corruptFileAt(path, 0, "invalid generation main-file magic")
	}
	if version := binary.LittleEndian.Uint16(header[8:10]); version != mainFormatVersion {
		return identity, corruptFileAt(path, 8, "unsupported generation main-file version %d", version)
	}
	if size := binary.LittleEndian.Uint16(header[10:12]); size != generationHeaderSize {
		return identity, corruptFileAt(path, 10, "invalid generation header size %d", size)
	}
	if flags := binary.LittleEndian.Uint32(header[12:16]); flags != 0 {
		return identity, corruptFileAt(path, 12, "unsupported generation header flags %d", flags)
	}
	for offset := 32; offset < generationHeaderChecksumAt; offset++ {
		if header[offset] != 0 {
			return identity, corruptFileAt(path, int64(offset), "unsupported generation header value")
		}
	}
	if declared := binary.LittleEndian.Uint32(header[generationHeaderChecksumAt:]); declared != crc32.Checksum(header[:generationHeaderChecksumAt], crc32cTable) {
		return identity, corruptFileAt(path, generationHeaderChecksumAt, "generation header checksum mismatch")
	}
	copy(identity[:], header[16:32])
	return identity, nil
}

func encodeGenerationSlot(identity [16]byte, slot generationSlot) []byte {
	data := make([]byte, generationSlotSize)
	copy(data[:8], generationSlotMagic)
	binary.LittleEndian.PutUint16(data[8:10], mainFormatVersion)
	binary.LittleEndian.PutUint16(data[10:12], generationSlotSize)
	copy(data[16:32], identity[:])
	binary.LittleEndian.PutUint64(data[32:40], slot.generation)
	binary.LittleEndian.PutUint64(data[40:48], slot.transaction)
	binary.LittleEndian.PutUint32(data[48:52], slot.boundaryChecksum)
	binary.LittleEndian.PutUint32(data[52:56], slot.segmentCount)
	binary.LittleEndian.PutUint64(data[56:64], slot.manifestOffset)
	binary.LittleEndian.PutUint64(data[64:72], slot.manifestSize)
	binary.LittleEndian.PutUint64(data[72:80], slot.fileEnd)
	binary.LittleEndian.PutUint32(data[80:84], slot.manifestChecksum)
	binary.LittleEndian.PutUint64(data[88:96], slot.records)
	binary.LittleEndian.PutUint32(data[generationSlotChecksumAt:], crc32.Checksum(data[:generationSlotChecksumAt], crc32cTable))
	return data
}

func decodeGenerationSlot(path string, data []byte, offset int64, index int, identity [16]byte, actualSize uint64) (generationSlot, bool, error) {
	var slot generationSlot
	slot.index = index
	if bytes.Equal(data, make([]byte, len(data))) {
		return slot, false, nil
	}
	if string(data[:8]) != generationSlotMagic {
		return slot, false, nil
	}
	if version := binary.LittleEndian.Uint16(data[8:10]); version != mainFormatVersion {
		return slot, false, nil
	}
	if size := binary.LittleEndian.Uint16(data[10:12]); size != generationSlotSize {
		return slot, false, nil
	}
	if crc32.Checksum(data[:generationSlotChecksumAt], crc32cTable) != binary.LittleEndian.Uint32(data[generationSlotChecksumAt:]) {
		return slot, false, nil
	}
	if flags := binary.LittleEndian.Uint32(data[12:16]); flags != 0 {
		return slot, false, nil
	}
	if !bytes.Equal(data[16:32], identity[:]) {
		return slot, false, nil
	}
	for reserved := 84; reserved < 88; reserved++ {
		if data[reserved] != 0 {
			return slot, false, nil
		}
	}
	for reserved := 96; reserved < generationSlotChecksumAt; reserved++ {
		if data[reserved] != 0 {
			return slot, false, nil
		}
	}
	slot.generation = binary.LittleEndian.Uint64(data[32:40])
	slot.transaction = binary.LittleEndian.Uint64(data[40:48])
	slot.boundaryChecksum = binary.LittleEndian.Uint32(data[48:52])
	slot.segmentCount = binary.LittleEndian.Uint32(data[52:56])
	slot.manifestOffset = binary.LittleEndian.Uint64(data[56:64])
	slot.manifestSize = binary.LittleEndian.Uint64(data[64:72])
	slot.fileEnd = binary.LittleEndian.Uint64(data[72:80])
	slot.manifestChecksum = binary.LittleEndian.Uint32(data[80:84])
	slot.records = binary.LittleEndian.Uint64(data[88:96])
	if slot.segmentCount > maxMainSegments || slot.fileEnd < generationDataOffset || slot.fileEnd > actualSize {
		return slot, false, nil
	}
	if slot.transaction == 0 && slot.boundaryChecksum != 0 {
		return slot, false, nil
	}
	if slot.generation == 0 {
		if slot.segmentCount != 0 || slot.records != 0 || slot.manifestSize != 0 || slot.manifestChecksum != 0 || slot.manifestOffset != generationDataOffset || slot.fileEnd != generationDataOffset || slot.transaction != 0 {
			return slot, false, nil
		}
		return slot, true, nil
	}
	expectedManifestSize := uint64(generationManifestHeaderSize) + uint64(slot.segmentCount)*generationSegmentSize
	if slot.transaction == 0 || slot.segmentCount == 0 && slot.records != 0 || slot.manifestSize != expectedManifestSize || slot.manifestOffset < generationDataOffset || slot.manifestOffset > math.MaxUint64-slot.manifestSize || slot.manifestOffset+slot.manifestSize != slot.fileEnd {
		return slot, false, nil
	}
	return slot, true, nil
}

func decodeGenerationManifest(path string, file mainReader, identity [16]byte, slot generationSlot) ([]generationSegmentDescriptor, error) {
	manifest := make([]byte, int(slot.manifestSize))
	if err := readAt(file, manifest, int64(slot.manifestOffset)); err != nil {
		return nil, fmt.Errorf("kitdb: read generation manifest: %w", err)
	}
	if crc32.Checksum(manifest, crc32cTable) != slot.manifestChecksum {
		return nil, corruptFileAt(path, int64(slot.manifestOffset), "generation manifest checksum mismatch")
	}
	if string(manifest[:8]) != generationManifestMagic || binary.LittleEndian.Uint16(manifest[8:10]) != mainFormatVersion || binary.LittleEndian.Uint16(manifest[10:12]) != generationManifestHeaderSize {
		return nil, corruptFileAt(path, int64(slot.manifestOffset), "invalid generation manifest header")
	}
	if binary.LittleEndian.Uint32(manifest[12:16]) != 0 || !bytes.Equal(manifest[16:32], identity[:]) {
		return nil, corruptFileAt(path, int64(slot.manifestOffset)+12, "generation manifest identity or flags mismatch")
	}
	if binary.LittleEndian.Uint64(manifest[32:40]) != slot.generation || binary.LittleEndian.Uint64(manifest[40:48]) != slot.transaction || binary.LittleEndian.Uint32(manifest[48:52]) != slot.boundaryChecksum || binary.LittleEndian.Uint32(manifest[52:56]) != slot.segmentCount || binary.LittleEndian.Uint64(manifest[56:64]) != slot.records {
		return nil, corruptFileAt(path, int64(slot.manifestOffset)+32, "generation manifest does not match its superblock slot")
	}
	descriptors := make([]generationSegmentDescriptor, slot.segmentCount)
	position := generationManifestHeaderSize
	for index := range descriptors {
		data := manifest[position : position+generationSegmentSize]
		descriptors[index] = generationSegmentDescriptor{
			generation:        binary.LittleEndian.Uint64(data[:8]),
			transaction:       binary.LittleEndian.Uint64(data[8:16]),
			dataOffset:        binary.LittleEndian.Uint64(data[16:24]),
			dataSize:          binary.LittleEndian.Uint64(data[24:32]),
			directoryOffset:   binary.LittleEndian.Uint64(data[32:40]),
			directorySize:     binary.LittleEndian.Uint64(data[40:48]),
			mutations:         binary.LittleEndian.Uint64(data[48:56]),
			pageCount:         binary.LittleEndian.Uint32(data[56:60]),
			directoryChecksum: binary.LittleEndian.Uint32(data[60:64]),
		}
		position += generationSegmentSize
	}
	return descriptors, nil
}

func decodeGenerationDirectory(path string, file mainReader, descriptor generationSegmentDescriptor) ([]mainBlock, error) {
	if descriptor.directorySize < uint64(descriptor.pageCount)*mainDirectoryEntryHeaderSize || descriptor.directorySize > math.MaxInt64 || descriptor.directoryOffset > math.MaxInt64 {
		return nil, corruptFileAt(path, int64(descriptor.directoryOffset), "invalid generation page-directory size")
	}
	if uint64(descriptor.pageCount) > uint64(^uint(0)>>1) {
		return nil, corruptFileAt(path, int64(descriptor.directoryOffset), "generation page count exceeds this platform")
	}
	directorySize := int64(descriptor.directorySize)
	section := io.NewSectionReader(file, int64(descriptor.directoryOffset), directorySize)
	reader := bufio.NewReaderSize(section, minBufferSize(directorySize))
	checksum := crc32.New(crc32cTable)
	blocks := make([]mainBlock, 0, int(descriptor.pageCount))
	consumed := int64(0)
	expectedOffset := int64(descriptor.dataOffset)
	totalRecords := uint64(0)
	var previousLastKey []byte
	for page := uint32(0); page < descriptor.pageCount; page++ {
		entryOffset := int64(descriptor.directoryOffset) + consumed
		if directorySize-consumed < mainDirectoryEntryHeaderSize {
			return nil, corruptFileAt(path, entryOffset, "generation page-directory entry %d is truncated", page)
		}
		var entry [mainDirectoryEntryHeaderSize]byte
		if err := readMainContent(reader, checksum, entry[:]); err != nil {
			return nil, corruptFileAt(path, entryOffset, "generation page-directory entry %d is truncated", page)
		}
		consumed += mainDirectoryEntryHeaderSize
		blockOffsetValue := binary.LittleEndian.Uint64(entry[:8])
		blockLengthValue := binary.LittleEndian.Uint64(entry[8:16])
		records := binary.LittleEndian.Uint32(entry[16:20])
		pageChecksum := binary.LittleEndian.Uint32(entry[20:24])
		firstKeySize := uint64(binary.LittleEndian.Uint32(entry[24:28]))
		lastKeySize := uint64(binary.LittleEndian.Uint32(entry[28:32]))
		if records == 0 || records > mainIndexBlockRecords || firstKeySize == 0 || firstKeySize > maxKeySize || lastKeySize == 0 || lastKeySize > maxKeySize {
			return nil, corruptFileAt(path, entryOffset+16, "generation page %d has invalid metadata", page)
		}
		keyBytes := firstKeySize + lastKeySize
		if keyBytes > uint64(directorySize-consumed) {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "generation page %d key bounds are truncated", page)
		}
		firstKey := make([]byte, int(firstKeySize))
		lastKey := make([]byte, int(lastKeySize))
		if err := readMainContent(reader, checksum, firstKey); err != nil {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "generation page %d first key is truncated", page)
		}
		if err := readMainContent(reader, checksum, lastKey); err != nil {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize+int64(firstKeySize), "generation page %d last key is truncated", page)
		}
		consumed += int64(keyBytes)
		comparison := bytes.Compare(firstKey, lastKey)
		if (records == 1 && comparison != 0) || (records > 1 && comparison >= 0) || previousLastKey != nil && bytes.Compare(previousLastKey, firstKey) >= 0 {
			return nil, corruptFileAt(path, entryOffset+mainDirectoryEntryHeaderSize, "generation page key bounds are inconsistent")
		}
		if blockOffsetValue > math.MaxInt64 || blockLengthValue > math.MaxInt64 {
			return nil, corruptFileAt(path, entryOffset, "generation page %d exceeds supported offsets", page)
		}
		blockOffset := int64(blockOffsetValue)
		blockLength := int64(blockLengthValue)
		if blockOffset != expectedOffset || blockLength < int64(records)*(operationHeaderSize+1) || blockLength > int64(descriptor.directoryOffset)-blockOffset {
			return nil, corruptFileAt(path, entryOffset, "generation page %d has invalid byte bounds", page)
		}
		expectedOffset += blockLength
		totalRecords += uint64(records)
		blocks = append(blocks, mainBlock{
			firstKey: firstKey, lastKey: lastKey, offset: blockOffset,
			length: blockLength, records: records, checksum: pageChecksum, mutations: true,
		})
		previousLastKey = lastKey
	}
	if consumed != directorySize || uint64(totalRecords) != descriptor.mutations || expectedOffset != int64(descriptor.directoryOffset) {
		return nil, corruptFileAt(path, int64(descriptor.directoryOffset)+consumed, "generation page directory does not match its descriptor")
	}
	if checksum.Sum32() != descriptor.directoryChecksum {
		return nil, corruptFileAt(path, int64(descriptor.directoryOffset), "generation page-directory checksum mismatch")
	}
	return blocks, nil
}
