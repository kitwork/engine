package search

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"sort"
)

const (
	postingBlockDocuments     = 128
	postingBlockHeaderSizeV1  = 24
	postingBlockHeaderSize    = 32
	postingBlockHeaderCRC     = 28
	postingBlockFlagPositions = 1 << 0
	postingBlockPayloadMax    = math.MaxUint32
)

type posting struct {
	document  uint32
	frequency uint32
	norm      uint32
	positions []uint32
}

type postingBlockHeader struct {
	count         uint16
	flags         uint16
	firstDocument uint32
	lastDocument  uint32
	maximumTF     uint32
	minimumNorm   uint32
	payloadLength uint32
	payloadCRC    uint32
}

type postingListInfo struct {
	bytes       uint64
	maximumTF   uint32
	minimumNorm uint32
}

func writePostingList(destination io.Writer, postings []posting, version uint32, includePositions bool) (postingListInfo, error) {
	if len(postings) == 0 {
		return postingListInfo{}, fmt.Errorf("search: cannot write an empty posting list")
	}
	headerSize, err := postingBlockHeaderBytes(version)
	if err != nil {
		return postingListInfo{}, err
	}
	info := postingListInfo{minimumNorm: math.MaxUint32}
	for start := 0; start < len(postings); start += postingBlockDocuments {
		end := min(start+postingBlockDocuments, len(postings))
		block := postings[start:end]
		payload := make([]byte, 0, len(block)*8)
		first := block[0].document
		previous := first
		maximumTF := uint32(0)
		minimumNorm := uint32(math.MaxUint32)
		for index, item := range block {
			if item.frequency == 0 || (index > 0 && item.document <= previous) {
				return postingListInfo{}, fmt.Errorf("search: postings are not strictly ordered")
			}
			if version >= segmentVersionV2 && (item.norm == 0 || item.frequency > item.norm) {
				return postingListInfo{}, fmt.Errorf("search: posting norm is smaller than term frequency")
			}
			gap := uint64(0)
			if index > 0 {
				gap = uint64(item.document - previous)
			}
			payload = binary.AppendUvarint(payload, gap)
			payload = binary.AppendUvarint(payload, uint64(item.frequency))
			if version >= segmentVersion && includePositions {
				if uint32(len(item.positions)) != item.frequency {
					return postingListInfo{}, fmt.Errorf("search: posting positions do not match term frequency")
				}
				previousPosition := uint32(0)
				for positionIndex, position := range item.positions {
					if positionIndex == 0 {
						previousPosition = position
						payload = binary.AppendUvarint(payload, uint64(position))
						continue
					}
					if position <= previousPosition {
						return postingListInfo{}, fmt.Errorf("search: posting positions are not strictly increasing")
					}
					payload = binary.AppendUvarint(payload, uint64(position-previousPosition))
					previousPosition = position
				}
			}
			previous = item.document
			maximumTF = max(maximumTF, item.frequency)
			minimumNorm = min(minimumNorm, item.norm)
		}
		if len(payload) > postingBlockPayloadMax {
			return postingListInfo{}, fmt.Errorf("search: posting block payload exceeds %d bytes", postingBlockPayloadMax)
		}
		var header [postingBlockHeaderSize]byte
		binary.LittleEndian.PutUint16(header[:2], uint16(len(block)))
		binary.LittleEndian.PutUint32(header[4:8], first)
		binary.LittleEndian.PutUint32(header[8:12], block[len(block)-1].document)
		binary.LittleEndian.PutUint32(header[12:16], maximumTF)
		if version == segmentVersionV1 {
			binary.LittleEndian.PutUint32(header[16:20], uint32(len(payload)))
			binary.LittleEndian.PutUint32(header[20:24], crc32.Checksum(payload, crcTable))
		} else {
			if version >= segmentVersion && includePositions {
				binary.LittleEndian.PutUint16(header[2:4], postingBlockFlagPositions)
			}
			binary.LittleEndian.PutUint32(header[16:20], minimumNorm)
			binary.LittleEndian.PutUint32(header[20:24], uint32(len(payload)))
			binary.LittleEndian.PutUint32(header[24:28], crc32.Checksum(payload, crcTable))
			binary.LittleEndian.PutUint32(header[postingBlockHeaderCRC:postingBlockHeaderCRC+4], 0)
			binary.LittleEndian.PutUint32(
				header[postingBlockHeaderCRC:postingBlockHeaderCRC+4],
				crc32.Checksum(header[:], crcTable),
			)
		}
		if _, err := destination.Write(header[:headerSize]); err != nil {
			return postingListInfo{}, err
		}
		if _, err := destination.Write(payload); err != nil {
			return postingListInfo{}, err
		}
		info.bytes += uint64(headerSize + len(payload))
		info.maximumTF = max(info.maximumTF, maximumTF)
		info.minimumNorm = min(info.minimumNorm, minimumNorm)
	}
	return info, nil
}

func postingBlockHeaderBytes(version uint32) (int, error) {
	switch version {
	case segmentVersionV1:
		return postingBlockHeaderSizeV1, nil
	case segmentVersionV2, segmentVersion:
		return postingBlockHeaderSize, nil
	default:
		return 0, fmt.Errorf("%w: posting version %d", ErrUnsupportedVersion, version)
	}
}

type postingIterator struct {
	file              *os.File
	version           uint32
	headerSize        uint64
	position          uint64
	end               uint64
	wantPositions     bool
	documentFrequency uint32
	documentCount     uint32
	expectedMaximumTF uint32
	expectedMinimum   uint32
	maximumTF         uint32
	minimumNorm       uint32
	seen              uint32
	previousBlockLast uint32
	hasPreviousBlock  bool

	documents   [postingBlockDocuments]uint32
	frequencies [postingBlockDocuments]uint32
	headerBytes [postingBlockHeaderSize]byte
	blockCount  int
	blockIndex  int
	hasCurrent  bool
	payload     []byte
	positions   [][]uint32
	norms       []uint32
}

func newPostingIterator(
	file *os.File,
	version uint32,
	record termRecord,
	documentCount uint32,
	postingsSection sectionDescriptor,
	wantPositions bool,
) (*postingIterator, error) {
	iterator := &postingIterator{}
	if err := resetPostingIterator(iterator, file, version, record, documentCount, postingsSection, wantPositions); err != nil {
		return nil, err
	}
	return iterator, nil
}

func resetPostingIterator(
	iterator *postingIterator,
	file *os.File,
	version uint32,
	record termRecord,
	documentCount uint32,
	postingsSection sectionDescriptor,
	wantPositions bool,
) error {
	if record.documentFreq == 0 || record.documentFreq > documentCount {
		return corruptf("posting document frequency %d exceeds segment size %d", record.documentFreq, documentCount)
	}
	headerSize, err := postingBlockHeaderBytes(version)
	if err != nil {
		return err
	}
	if version >= segmentVersionV2 && (record.maximumTF == 0 || record.minimumNorm == 0) {
		return corruptf("posting list has no score bounds")
	}
	sectionEnd := postingsSection.offset + postingsSection.length
	if record.postingsOffset < postingsSection.offset || record.postingsOffset > sectionEnd || record.postingsLength > sectionEnd-record.postingsOffset {
		return corruptf("posting list exceeds postings section")
	}
	iterator.file = file
	iterator.version = version
	iterator.headerSize = uint64(headerSize)
	iterator.position = record.postingsOffset
	iterator.end = record.postingsOffset + record.postingsLength
	iterator.wantPositions = wantPositions
	iterator.documentFrequency = record.documentFreq
	iterator.documentCount = documentCount
	iterator.expectedMaximumTF = record.maximumTF
	iterator.expectedMinimum = record.minimumNorm
	iterator.maximumTF = 0
	iterator.minimumNorm = math.MaxUint32
	iterator.seen = 0
	iterator.previousBlockLast = 0
	iterator.hasPreviousBlock = false
	iterator.blockCount = 0
	iterator.blockIndex = -1
	iterator.hasCurrent = false
	iterator.payload = nil
	iterator.positions = nil
	iterator.norms = nil
	return nil
}

func (iterator *postingIterator) Current() (uint32, uint32, bool) {
	if !iterator.hasCurrent {
		return 0, 0, false
	}
	return iterator.documents[iterator.blockIndex], iterator.frequencies[iterator.blockIndex], true
}

func (iterator *postingIterator) CurrentPositions() []uint32 {
	if !iterator.hasCurrent || !iterator.wantPositions || iterator.blockIndex < 0 || iterator.blockIndex >= len(iterator.positions) {
		return nil
	}
	return iterator.positions[iterator.blockIndex]
}

func (iterator *postingIterator) Next() (bool, error) {
	if iterator.hasCurrent && iterator.blockIndex+1 < iterator.blockCount {
		iterator.blockIndex++
		return true, nil
	}
	return iterator.loadNextBlock()
}

func (iterator *postingIterator) nextBlockHeader() (postingBlockHeader, bool, error) {
	if iterator.hasCurrent && iterator.blockIndex+1 < iterator.blockCount {
		return postingBlockHeader{}, false, corruptf("posting iterator left a decoded block unfinished")
	}
	iterator.hasCurrent = false
	if iterator.position >= iterator.end {
		return postingBlockHeader{}, false, iterator.validateEnd()
	}
	header, err := iterator.readBlockHeader()
	if err != nil {
		return postingBlockHeader{}, false, err
	}
	return header, true, nil
}

func (iterator *postingIterator) decodeNextBlock(header postingBlockHeader) (bool, error) {
	if _, err := iterator.decodeBlock(header); err != nil {
		return false, err
	}
	iterator.blockIndex = 0
	iterator.hasCurrent = true
	return true, nil
}

func (iterator *postingIterator) skipNextBlock(header postingBlockHeader) error {
	iterator.hasCurrent = false
	iterator.consumeBlockHeader(header)
	iterator.position += iterator.headerSize + uint64(header.payloadLength)
	if iterator.position == iterator.end {
		return iterator.validateEnd()
	}
	return nil
}

func (iterator *postingIterator) Advance(target uint32) (bool, error) {
	if iterator.hasCurrent {
		if iterator.documents[iterator.blockIndex] >= target {
			return true, nil
		}
		remaining := iterator.documents[iterator.blockIndex+1 : iterator.blockCount]
		offset := sort.Search(len(remaining), func(index int) bool { return remaining[index] >= target })
		if offset < len(remaining) {
			iterator.blockIndex += offset + 1
			return true, nil
		}
	}

	iterator.hasCurrent = false
	for iterator.position < iterator.end {
		header, err := iterator.readBlockHeader()
		if err != nil {
			return false, err
		}
		if header.lastDocument < target {
			iterator.consumeBlockHeader(header)
			iterator.position += iterator.headerSize + uint64(header.payloadLength)
			continue
		}
		if _, err := iterator.decodeBlock(header); err != nil {
			return false, err
		}
		offset := sort.Search(iterator.blockCount, func(index int) bool {
			return iterator.documents[index] >= target
		})
		if offset < iterator.blockCount {
			iterator.blockIndex = offset
			iterator.hasCurrent = true
			return true, nil
		}
	}
	return false, iterator.validateEnd()
}

func (iterator *postingIterator) loadNextBlock() (bool, error) {
	header, more, err := iterator.nextBlockHeader()
	if err != nil {
		return false, err
	}
	if !more {
		return false, nil
	}
	return iterator.decodeNextBlock(header)
}

func (iterator *postingIterator) readBlockHeader() (postingBlockHeader, error) {
	if iterator.position > iterator.end || iterator.end-iterator.position < iterator.headerSize {
		return postingBlockHeader{}, corruptf("posting block header is truncated")
	}
	encoded := iterator.headerBytes[:iterator.headerSize]
	if err := readAtFull(iterator.file, encoded, iterator.position); err != nil {
		return postingBlockHeader{}, err
	}
	header := postingBlockHeader{
		count:         binary.LittleEndian.Uint16(encoded[:2]),
		firstDocument: binary.LittleEndian.Uint32(encoded[4:8]),
		lastDocument:  binary.LittleEndian.Uint32(encoded[8:12]),
		maximumTF:     binary.LittleEndian.Uint32(encoded[12:16]),
	}
	if iterator.version == segmentVersionV1 {
		header.payloadLength = binary.LittleEndian.Uint32(encoded[16:20])
		header.payloadCRC = binary.LittleEndian.Uint32(encoded[20:24])
	} else {
		header.flags = binary.LittleEndian.Uint16(encoded[2:4])
		if header.flags&^postingBlockFlagPositions != 0 {
			return postingBlockHeader{}, corruptf("posting block flags are not zero")
		}
		wantCRC := binary.LittleEndian.Uint32(encoded[postingBlockHeaderCRC : postingBlockHeaderCRC+4])
		copyForCRC := iterator.headerBytes
		binary.LittleEndian.PutUint32(copyForCRC[postingBlockHeaderCRC:postingBlockHeaderCRC+4], 0)
		if got := crc32.Checksum(copyForCRC[:iterator.headerSize], crcTable); got != wantCRC {
			return postingBlockHeader{}, corruptf("posting block header checksum is %08x; expected %08x", got, wantCRC)
		}
		header.minimumNorm = binary.LittleEndian.Uint32(encoded[16:20])
		header.payloadLength = binary.LittleEndian.Uint32(encoded[20:24])
		header.payloadCRC = binary.LittleEndian.Uint32(encoded[24:28])
	}
	if header.count == 0 || header.count > postingBlockDocuments {
		return postingBlockHeader{}, corruptf("posting block count is %d", header.count)
	}
	if header.firstDocument > header.lastDocument || header.lastDocument >= iterator.documentCount {
		return postingBlockHeader{}, corruptf("posting block document bounds are invalid")
	}
	if iterator.hasPreviousBlock && header.firstDocument <= iterator.previousBlockLast {
		return postingBlockHeader{}, corruptf("posting blocks are not strictly ordered")
	}
	if header.maximumTF == 0 || header.payloadLength == 0 || header.payloadLength > postingBlockPayloadMax ||
		(iterator.version >= segmentVersionV2 && header.minimumNorm == 0) {
		return postingBlockHeader{}, corruptf("posting block metadata is invalid")
	}
	blockLength := iterator.headerSize + uint64(header.payloadLength)
	if blockLength > iterator.end-iterator.position {
		return postingBlockHeader{}, corruptf("posting block payload is truncated")
	}
	if iterator.seen+uint32(header.count) > iterator.documentFrequency {
		return postingBlockHeader{}, corruptf("posting list contains too many documents")
	}
	return header, nil
}

func (iterator *postingIterator) decodeBlock(header postingBlockHeader) (int, error) {
	if cap(iterator.payload) < int(header.payloadLength) {
		iterator.payload = make([]byte, int(header.payloadLength))
	} else {
		iterator.payload = iterator.payload[:header.payloadLength]
	}
	payload := iterator.payload
	if err := readAtFull(iterator.file, payload, iterator.position+iterator.headerSize); err != nil {
		return 0, err
	}
	if got := crc32.Checksum(payload, crcTable); got != header.payloadCRC {
		return 0, corruptf("posting block checksum is %08x; expected %08x", got, header.payloadCRC)
	}

	remaining := payload
	previous := header.firstDocument
	maximumTF := uint32(0)
	minimumNorm := uint32(math.MaxUint32)
	wantPositions := iterator.wantPositions
	hasPositions := header.flags&postingBlockFlagPositions != 0
	if wantPositions && !hasPositions {
		return 0, corruptf("posting block does not store phrase positions")
	}
	if wantPositions {
		iterator.positions = iterator.positions[:0]
	}
	if hasPositions && wantPositions {
		if cap(iterator.positions) < int(header.count) {
			iterator.positions = make([][]uint32, int(header.count))
		} else {
			iterator.positions = iterator.positions[:header.count]
		}
	}
	for index := 0; index < int(header.count); index++ {
		gap, read := binary.Uvarint(remaining)
		if read <= 0 {
			return 0, corruptf("posting document gap is malformed")
		}
		remaining = remaining[read:]
		frequency, read := binary.Uvarint(remaining)
		if read <= 0 || frequency == 0 || frequency > math.MaxUint32 {
			return 0, corruptf("posting term frequency is malformed")
		}
		remaining = remaining[read:]
		if index == 0 && gap != 0 {
			return 0, corruptf("first posting in a block has a non-zero gap")
		}
		if index > 0 {
			if gap == 0 || gap > uint64(math.MaxUint32-previous) {
				return 0, corruptf("posting document gap exceeds range")
			}
			previous += uint32(gap)
		}
		iterator.documents[index] = previous
		iterator.frequencies[index] = uint32(frequency)
		if hasPositions {
			if wantPositions {
				if cap(iterator.positions[index]) < int(frequency) {
					iterator.positions[index] = make([]uint32, 0, int(frequency))
				} else {
					iterator.positions[index] = iterator.positions[index][:0]
				}
			}
			previousPosition := uint32(0)
			for positionIndex := 0; positionIndex < int(frequency); positionIndex++ {
				gap, read := binary.Uvarint(remaining)
				if read <= 0 {
					return 0, corruptf("posting position gap is malformed")
				}
				remaining = remaining[read:]
				var position uint32
				if positionIndex == 0 {
					if gap > math.MaxUint32 {
						return 0, corruptf("posting position exceeds range")
					}
					position = uint32(gap)
				} else {
					if gap == 0 || gap > uint64(math.MaxUint32-previousPosition) {
						return 0, corruptf("posting position gap exceeds range")
					}
					position = previousPosition + uint32(gap)
				}
				if wantPositions {
					iterator.positions[index] = append(iterator.positions[index], position)
				}
				previousPosition = position
			}
		}
		maximumTF = max(maximumTF, uint32(frequency))
		if len(iterator.norms) != 0 {
			norm := iterator.norms[previous]
			if norm == 0 || uint32(frequency) > norm {
				return 0, corruptf("posting term frequency exceeds its field norm")
			}
			minimumNorm = min(minimumNorm, norm)
		}
	}
	if len(remaining) != 0 || previous != header.lastDocument || maximumTF != header.maximumTF ||
		(iterator.version >= segmentVersionV2 && len(iterator.norms) != 0 && minimumNorm != header.minimumNorm) {
		return 0, corruptf("posting block payload does not match its header")
	}
	iterator.blockCount = int(header.count)
	iterator.consumeBlockHeader(header)
	iterator.position += iterator.headerSize + uint64(header.payloadLength)
	return iterator.blockCount, nil
}

func (iterator *postingIterator) consumeBlockHeader(header postingBlockHeader) {
	iterator.seen += uint32(header.count)
	iterator.previousBlockLast = header.lastDocument
	iterator.hasPreviousBlock = true
	iterator.maximumTF = max(iterator.maximumTF, header.maximumTF)
	if iterator.version >= segmentVersionV2 {
		iterator.minimumNorm = min(iterator.minimumNorm, header.minimumNorm)
	}
}

func (iterator *postingIterator) validateEnd() error {
	if iterator.position != iterator.end {
		return corruptf("posting iterator stopped before list end")
	}
	if iterator.seen != iterator.documentFrequency {
		return corruptf("posting list contains %d documents; expected %d", iterator.seen, iterator.documentFrequency)
	}
	if iterator.version >= segmentVersionV2 &&
		(iterator.maximumTF != iterator.expectedMaximumTF || iterator.minimumNorm != iterator.expectedMinimum) {
		return corruptf("posting list score bounds do not match its dictionary entry")
	}
	return nil
}
