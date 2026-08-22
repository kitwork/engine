package kitdb

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"os"
	"sort"
)

const (
	mainIndexBlockRecords = 128
	mainIndexBlockBytes   = 64 << 10
	cachedRowOverhead     = 48
)

type mainBlock struct {
	firstKey  []byte
	lastKey   []byte
	offset    int64
	length    int64
	records   uint32
	checksum  uint32
	mutations bool
}

type mainSegment struct {
	generation     uint64
	transaction    uint64
	firstBlock     int
	blockCount     int
	mutations      uint64
	dataBytes      int64
	directoryBytes int64
	descriptor     generationSegmentDescriptor
}

type mainImage struct {
	path             string
	file             *os.File
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
	cache            *rowPageCache
}

func (main *mainImage) close() error {
	if main == nil || main.file == nil {
		return nil
	}
	err := main.file.Close()
	main.file = nil
	return err
}

func (main *mainImage) get(key []byte) ([]byte, bool, error) {
	if main == nil || main.file == nil || len(main.blocks) == 0 {
		return nil, false, nil
	}
	if main.formatVersion == mainFormatVersion {
		for segmentIndex := len(main.segments) - 1; segmentIndex >= 0; segmentIndex-- {
			segment := main.segments[segmentIndex]
			value, found, deleted, err := main.getFromBlocks(key, segment.firstBlock, segment.blockCount)
			if err != nil {
				return nil, false, err
			}
			if found {
				if deleted {
					return nil, false, nil
				}
				return value, true, nil
			}
		}
		return nil, false, nil
	}
	value, found, _, err := main.getFromBlocks(key, 0, len(main.blocks))
	return value, found, err
}

func (main *mainImage) getFromBlocks(key []byte, firstBlock, blockCount int) ([]byte, bool, bool, error) {
	if blockCount == 0 {
		return nil, false, false, nil
	}
	block := sort.Search(blockCount, func(index int) bool {
		return bytes.Compare(main.blocks[firstBlock+index].firstKey, key) > 0
	}) - 1
	if block < 0 {
		return nil, false, false, nil
	}
	block += firstBlock
	if len(main.blocks[block].lastKey) != 0 && bytes.Compare(key, main.blocks[block].lastKey) > 0 {
		return nil, false, false, nil
	}
	page, found := main.cache.get(block)
	if !found {
		decoded, err := main.readPage(block)
		if err != nil {
			return nil, false, false, err
		}
		page = main.cache.addOrGet(decoded)
	}
	position := sort.Search(len(page.rows), func(index int) bool {
		return bytes.Compare(page.rows[index].key, key) >= 0
	})
	if position == len(page.rows) || !bytes.Equal(page.rows[position].key, key) {
		return nil, false, false, nil
	}
	row := page.rows[position]
	return bytes.Clone(row.value), true, row.deleted, nil
}

func (main *mainImage) readPage(blockIndex int) (*rowPage, error) {
	if blockIndex < 0 || blockIndex >= len(main.blocks) {
		return nil, fmt.Errorf("kitdb: main block %d is outside the sparse index", blockIndex)
	}
	block := main.blocks[blockIndex]
	data := make([]byte, int(block.length))
	if err := readAt(main.file, data, block.offset); err != nil {
		return nil, fmt.Errorf("kitdb: read main block %d: %w", blockIndex, err)
	}
	if main.formatVersion >= mainPagedFormatVersion {
		actual := crc32.Checksum(data, crc32cTable)
		if actual != block.checksum {
			return nil, corruptFileAt(main.path, block.offset, "main page %d checksum mismatch", blockIndex)
		}
	}
	var page *rowPage
	var err error
	if block.mutations {
		page, err = decodeMutationPage(main.path, blockIndex, block.offset, data, block.records)
	} else {
		page, err = decodeRowPage(main.path, blockIndex, block.offset, data, block.records)
	}
	if err != nil {
		return nil, err
	}
	if len(page.rows) == 0 || !bytes.Equal(page.rows[0].key, block.firstKey) {
		return nil, corruptFileAt(main.path, block.offset, "main page %d first key does not match its directory entry", blockIndex)
	}
	if len(block.lastKey) != 0 && !bytes.Equal(page.rows[len(page.rows)-1].key, block.lastKey) {
		return nil, corruptFileAt(main.path, block.offset, "main page %d last key does not match its directory entry", blockIndex)
	}
	return page, nil
}

func decodeMutationPage(path string, blockIndex int, blockOffset int64, data []byte, records uint32) (*rowPage, error) {
	if records > mainIndexBlockRecords {
		return nil, corruptFileAt(path, blockOffset, "main mutation page has %d records above limit %d", records, mainIndexBlockRecords)
	}
	rows := make([]mainRow, 0, records)
	position := 0
	var previous []byte
	for index := uint32(0); index < records; index++ {
		recordOffset := position
		if len(data)-position < operationHeaderSize {
			return nil, corruptFileAt(path, blockOffset+int64(position), "main mutation record header is truncated")
		}
		kind := operationKind(data[position])
		keySize := int(binary.LittleEndian.Uint32(data[position+1 : position+5]))
		valueSize := int(binary.LittleEndian.Uint32(data[position+5 : position+9]))
		position += operationHeaderSize
		if kind != operationPut && kind != operationDelete {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset), "main mutation record has invalid kind %d", kind)
		}
		if kind == operationDelete && valueSize != 0 {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset+5), "main delete mutation has value size %d", valueSize)
		}
		if keySize == 0 || keySize > maxKeySize {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset+1), "main mutation record has invalid key size %d", keySize)
		}
		if valueSize > maxValueSize || keySize > len(data)-position || valueSize > len(data)-position-keySize {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset), "main mutation record body is invalid or truncated")
		}
		key := data[position : position+keySize]
		position += keySize
		value := data[position : position+valueSize]
		position += valueSize
		if previous != nil && bytes.Compare(previous, key) >= 0 {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset), "main mutation page keys are not strictly increasing")
		}
		rows = append(rows, mainRow{key: key, value: value, deleted: kind == operationDelete})
		previous = key
	}
	if position != len(data) {
		return nil, corruptFileAt(path, blockOffset+int64(position), "main mutation page has %d trailing bytes", len(data)-position)
	}
	weight := int64(len(data)) + int64(cap(rows))*cachedRowOverhead
	return &rowPage{block: blockIndex, data: data, rows: rows, weight: weight}, nil
}

func decodeRowPage(path string, blockIndex int, blockOffset int64, data []byte, records uint32) (*rowPage, error) {
	if records > mainIndexBlockRecords {
		return nil, corruptFileAt(path, blockOffset, "main block has %d records above limit %d", records, mainIndexBlockRecords)
	}
	rows := make([]mainRow, 0, records)
	position := 0
	var previous []byte
	for index := uint32(0); index < records; index++ {
		recordOffset := position
		if len(data)-position < mainRecordHeaderSize {
			return nil, corruptFileAt(path, blockOffset+int64(position), "cached main record header is truncated")
		}
		keySize := int(binary.LittleEndian.Uint32(data[position : position+4]))
		valueSize := int(binary.LittleEndian.Uint32(data[position+4 : position+8]))
		position += mainRecordHeaderSize
		if keySize == 0 || keySize > maxKeySize {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset), "cached main record has invalid key size %d", keySize)
		}
		if valueSize > maxValueSize || keySize > len(data)-position || valueSize > len(data)-position-keySize {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset), "cached main record body is invalid or truncated")
		}
		key := data[position : position+keySize]
		position += keySize
		value := data[position : position+valueSize]
		position += valueSize
		if previous != nil && bytes.Compare(previous, key) >= 0 {
			return nil, corruptFileAt(path, blockOffset+int64(recordOffset), "cached main block keys are not strictly increasing")
		}
		rows = append(rows, mainRow{key: key, value: value})
		previous = key
	}
	if position != len(data) {
		return nil, corruptFileAt(path, blockOffset+int64(position), "main block has %d trailing bytes", len(data)-position)
	}
	weight := int64(len(data)) + int64(cap(rows))*cachedRowOverhead
	return &rowPage{block: blockIndex, data: data, rows: rows, weight: weight}, nil
}

func minBufferSize(length int64) int {
	const maximum = 256 << 10
	if length <= 0 {
		return 1
	}
	if length < maximum {
		return int(length)
	}
	return maximum
}
