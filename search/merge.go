package search

import (
	"container/heap"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
)

type mergeSource struct {
	segment *Segment
	deleted *deletedDocuments
	remap   []uint32
}

func writeMergedSegment(
	ctx context.Context,
	path string,
	schema Schema,
	segments []*Segment,
	deletions []*deletedDocuments,
) (SegmentInfo, error) {
	if ctx == nil {
		return SegmentInfo{}, fmt.Errorf("search: merge context is nil")
	}
	if len(segments) == 0 || len(segments) != len(deletions) {
		return SegmentInfo{}, fmt.Errorf("search: merge requires aligned segments and deletions")
	}
	sources, documents, fieldStats, err := prepareMergeSources(ctx, schema, segments, deletions)
	if err != nil {
		return SegmentInfo{}, err
	}
	if documents == 0 {
		return SegmentInfo{}, fmt.Errorf("search: cannot write an empty merged segment")
	}

	path = filepath.Clean(path)
	if _, err := os.Stat(path); err == nil {
		return SegmentInfo{}, fmt.Errorf("search: segment path already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return SegmentInfo{}, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return SegmentInfo{}, err
	}
	temporary, err := temporarySegmentPath(path)
	if err != nil {
		return SegmentInfo{}, err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return SegmentInfo{}, err
	}
	keepTemporary := true
	defer func() {
		_ = file.Close()
		if keepTemporary {
			_ = os.Remove(temporary)
		}
	}()
	if _, err := file.Write(make([]byte, segmentHeaderSize)); err != nil {
		return SegmentInfo{}, err
	}

	var sections [sectionCount]sectionDescriptor
	sections[sectionFieldStats], err = writeSection(file, func(destination io.Writer) error {
		var encoded [8]byte
		for _, total := range fieldStats {
			binary.LittleEndian.PutUint64(encoded[:], total)
			if _, err := destination.Write(encoded[:]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	var records []termRecord
	sections[sectionPostings], err = writeSection(file, func(destination io.Writer) error {
		sectionStart, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return seekErr
		}
		var mergeErr error
		records, _, mergeErr = mergePostings(ctx, destination, uint64(sectionStart), sources)
		return mergeErr
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	indexes := make([]dictionaryBlockIndex, 0, (len(records)+dictionaryBlockMaxEntries-1)/dictionaryBlockMaxEntries)
	sections[sectionDictionaryBlocks], err = writeSection(file, func(destination io.Writer) error {
		sectionStart, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr != nil {
			return seekErr
		}
		var relative uint64
		for start := 0; start < len(records); {
			end := min(start+dictionaryBlockMaxEntries, len(records))
			for end > start+1 && records[end-1].key.field != records[start].key.field {
				end--
			}
			block, err := encodeDictionaryBlock(records[start:end], segmentVersion)
			if err != nil {
				return err
			}
			if _, err := destination.Write(block); err != nil {
				return err
			}
			indexes = append(indexes, dictionaryBlockIndex{
				field: records[start].key.field, firstTerm: records[start].key.term,
				blockOffset: uint64(sectionStart) + relative, blockLength: uint32(len(block)),
				blockCRC: dictionaryBlockChecksum(block),
			})
			relative += uint64(len(block))
			start = end
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}
	sections[sectionDictionaryIndex], err = writeSection(file, func(destination io.Writer) error {
		encoded, err := encodeDictionaryIndex(indexes)
		if err != nil {
			return err
		}
		_, err = destination.Write(encoded)
		return err
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	sections[sectionNorms], err = writeSection(file, func(destination io.Writer) error {
		buffer := make([]byte, 0, 32<<10)
		for field := uint16(0); field < uint16(len(schema.fields)); field++ {
			for _, source := range sources {
				norms, err := source.segment.fieldNorms(field)
				if err != nil {
					return err
				}
				for document, remapped := range source.remap {
					if remapped == math.MaxUint32 {
						continue
					}
					buffer = binary.LittleEndian.AppendUint32(buffer, norms[document])
					if len(buffer) >= 32<<10 {
						if _, err := destination.Write(buffer); err != nil {
							return err
						}
						buffer = buffer[:0]
					}
					if document&8191 == 0 {
						if err := ctx.Err(); err != nil {
							return err
						}
					}
				}
			}
		}
		if len(buffer) != 0 {
			_, err := destination.Write(buffer)
			return err
		}
		return nil
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	sections[sectionStoredOffsets], err = writeSection(file, func(destination io.Writer) error {
		var encoded [8]byte
		var offset uint64
		if _, err := destination.Write(encoded[:]); err != nil {
			return err
		}
		return visitMergedDocuments(ctx, sources, func(segment *Segment, document uint32) error {
			identifier, err := segment.documentIdentifier(document)
			if err != nil {
				return err
			}
			if offset > math.MaxUint64-uint64(len(identifier)) {
				return corruptIndexf("merged identifier offsets overflow")
			}
			offset += uint64(len(identifier))
			binary.LittleEndian.PutUint64(encoded[:], offset)
			_, err = destination.Write(encoded[:])
			return err
		})
	})
	if err != nil {
		return SegmentInfo{}, err
	}
	sections[sectionStoredData], err = writeSection(file, func(destination io.Writer) error {
		return visitMergedDocuments(ctx, sources, func(segment *Segment, document uint32) error {
			identifier, err := segment.documentIdentifier(document)
			if err != nil {
				return err
			}
			_, err = io.WriteString(destination, identifier)
			return err
		})
	})
	if err != nil {
		return SegmentInfo{}, err
	}

	fileSize, err := file.Seek(0, io.SeekCurrent)
	if err != nil {
		return SegmentInfo{}, err
	}
	header := marshalSegmentHeader(segmentHeader{
		version: segmentVersion, fileSize: uint64(fileSize), schemaHash: schema.fingerprint,
		documentN: documents, fieldN: uint16(len(schema.fields)), sections: sections,
	})
	if _, err := file.WriteAt(header[:], 0); err != nil {
		return SegmentInfo{}, err
	}
	if err := ctx.Err(); err != nil {
		return SegmentInfo{}, err
	}
	if err := file.Sync(); err != nil {
		return SegmentInfo{}, err
	}
	if err := file.Close(); err != nil {
		return SegmentInfo{}, err
	}
	if _, err := os.Stat(path); err == nil {
		return SegmentInfo{}, fmt.Errorf("search: segment path appeared while merging: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return SegmentInfo{}, err
	}
	if err := publishFile(temporary, path); err != nil {
		return SegmentInfo{}, err
	}
	keepTemporary = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return SegmentInfo{}, err
	}
	return SegmentInfo{Path: path, Documents: documents, Fields: uint16(len(schema.fields)), Bytes: fileSize}, nil
}

func prepareMergeSources(
	ctx context.Context,
	schema Schema,
	segments []*Segment,
	deletions []*deletedDocuments,
) ([]mergeSource, uint32, []uint64, error) {
	sources := make([]mergeSource, len(segments))
	fieldStats := make([]uint64, len(schema.fields))
	var next uint64
	for position, segment := range segments {
		if err := ctx.Err(); err != nil {
			return nil, 0, nil, err
		}
		if segment.schema.fingerprint != schema.fingerprint {
			return nil, 0, nil, ErrSchemaMismatch
		}
		deleted := deletions[position]
		if deleted != nil && deleted.documents != segment.header.documentN {
			return nil, 0, nil, corruptIndexf("merge deletion metadata does not match segment %d", position)
		}
		if uint64(segment.header.documentN) > uint64(maxIntValue()/4) {
			return nil, 0, nil, fmt.Errorf("search: merge remap exceeds platform memory range")
		}
		live := uint64(segment.header.documentN)
		if deleted != nil {
			live -= uint64(deleted.Count())
		}
		if next > math.MaxUint32-live {
			return nil, 0, nil, fmt.Errorf("search: merged segment exceeds %d documents", uint64(math.MaxUint32))
		}
		remap := make([]uint32, segment.header.documentN)
		for document := range remap {
			if deleted.Contains(uint32(document)) {
				remap[document] = math.MaxUint32
				continue
			}
			remap[document] = uint32(next)
			next++
		}
		for field, total := range segment.fieldStats {
			deletedTotal := uint64(0)
			if deleted != nil {
				deletedTotal = deleted.tokenTotals[field]
			}
			if deletedTotal > total || fieldStats[field] > math.MaxUint64-(total-deletedTotal) {
				return nil, 0, nil, corruptIndexf("merge field statistics overflow at segment %d", position)
			}
			fieldStats[field] += total - deletedTotal
		}
		sources[position] = mergeSource{segment: segment, deleted: deleted, remap: remap}
	}
	return sources, uint32(next), fieldStats, nil
}

type segmentTermIterator struct {
	segment  *Segment
	block    int
	records  []termRecord
	position int
	buffer   []byte
}

func (iterator *segmentTermIterator) Next(ctx context.Context) (termRecord, bool, error) {
	for {
		if iterator.position < len(iterator.records) {
			record := iterator.records[iterator.position]
			iterator.position++
			return record, true, nil
		}
		if iterator.block >= len(iterator.segment.dictionary) {
			return termRecord{}, false, nil
		}
		if err := ctx.Err(); err != nil {
			return termRecord{}, false, err
		}
		block := iterator.segment.dictionary[iterator.block]
		iterator.block++
		if cap(iterator.buffer) < int(block.blockLength) {
			iterator.buffer = make([]byte, block.blockLength)
		} else {
			iterator.buffer = iterator.buffer[:block.blockLength]
		}
		data := iterator.buffer
		if err := readAtFull(iterator.segment.file, data, block.blockOffset); err != nil {
			return termRecord{}, false, err
		}
		if got := dictionaryBlockChecksum(data); got != block.blockCRC {
			return termRecord{}, false, corruptf("dictionary block checksum is %08x; expected %08x", got, block.blockCRC)
		}
		iterator.records = iterator.records[:0]
		err := decodeDictionaryBlock(data, iterator.segment.header.version, func(record termRecord) bool {
			record.key.field = block.field
			iterator.records = append(iterator.records, record)
			return true
		})
		if err != nil {
			return termRecord{}, false, err
		}
		if len(iterator.records) == 0 || iterator.records[0].key.term != block.firstTerm {
			return termRecord{}, false, corruptf("dictionary block first term does not match its index")
		}
		iterator.position = 0
	}
}

type mergeTermCursor struct {
	source   int
	iterator segmentTermIterator
	record   termRecord
}

type mergeTermHeap []*mergeTermCursor

func (items mergeTermHeap) Len() int { return len(items) }
func (items mergeTermHeap) Less(left, right int) bool {
	if comparison := compareTermKeys(items[left].record.key, items[right].record.key); comparison != 0 {
		return comparison < 0
	}
	return items[left].source < items[right].source
}
func (items mergeTermHeap) Swap(left, right int) {
	items[left], items[right] = items[right], items[left]
}
func (items *mergeTermHeap) Push(value any) { *items = append(*items, value.(*mergeTermCursor)) }
func (items *mergeTermHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func mergePostings(
	ctx context.Context,
	destination io.Writer,
	sectionStart uint64,
	sources []mergeSource,
) ([]termRecord, uint64, error) {
	cursors := make(mergeTermHeap, 0, len(sources))
	for source := range sources {
		cursor := &mergeTermCursor{source: source, iterator: segmentTermIterator{segment: sources[source].segment}}
		record, found, err := cursor.iterator.Next(ctx)
		if err != nil {
			return nil, 0, err
		}
		if found {
			cursor.record = record
			heap.Push(&cursors, cursor)
		}
	}
	records := make([]termRecord, 0, 1024)
	group := make([]*mergeTermCursor, 0, len(sources))
	var relative uint64
	for cursors.Len() != 0 {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		first := heap.Pop(&cursors).(*mergeTermCursor)
		key := first.record.key
		group = append(group[:0], first)
		for cursors.Len() != 0 && compareTermKeys(cursors[0].record.key, key) == 0 {
			group = append(group, heap.Pop(&cursors).(*mergeTermCursor))
		}

		listInfo, documentFrequency, err := writeMergedPostingList(ctx, destination, sources, group)
		if err != nil {
			return nil, 0, err
		}
		if documentFrequency != 0 {
			records = append(records, termRecord{
				key: key, documentFreq: documentFrequency,
				postingsOffset: sectionStart + relative, postingsLength: listInfo.bytes,
				maximumTF: listInfo.maximumTF, minimumNorm: listInfo.minimumNorm,
			})
			relative += listInfo.bytes
		}
		for _, cursor := range group {
			record, found, err := cursor.iterator.Next(ctx)
			if err != nil {
				return nil, 0, err
			}
			if found {
				cursor.record = record
				heap.Push(&cursors, cursor)
			}
		}
	}
	return records, relative, nil
}

func writeMergedPostingList(
	ctx context.Context,
	destination io.Writer,
	sources []mergeSource,
	cursors []*mergeTermCursor,
) (postingListInfo, uint32, error) {
	if len(cursors) == 0 {
		return postingListInfo{}, 0, fmt.Errorf("search: merge posting list has no cursors")
	}
	buffer := make([]posting, 0, postingBlockDocuments)
	info := postingListInfo{minimumNorm: math.MaxUint32}
	var frequency uint32
	var previous uint32
	var visited uint64
	hasPrevious := false
	includePositions := true
	for _, source := range sources {
		if source.segment.header.version < segmentVersion {
			includePositions = false
			break
		}
	}
	flush := func() error {
		if len(buffer) == 0 {
			return nil
		}
		chunk, err := writePostingList(destination, buffer, segmentVersion, includePositions)
		if err != nil {
			return err
		}
		info.bytes += chunk.bytes
		info.maximumTF = max(info.maximumTF, chunk.maximumTF)
		info.minimumNorm = min(info.minimumNorm, chunk.minimumNorm)
		buffer = buffer[:0]
		return nil
	}
	field := cursors[0].record.key.field
	for _, cursor := range cursors {
		source := sources[cursor.source]
		norms, err := source.segment.fieldNorms(field)
		if err != nil {
			return postingListInfo{}, 0, err
		}
		iterator, err := newPostingIterator(
			source.segment.file, source.segment.header.version, cursor.record, source.segment.header.documentN,
			source.segment.header.sections[sectionPostings], includePositions && source.segment.header.version >= segmentVersion,
		)
		if err != nil {
			return postingListInfo{}, 0, err
		}
		for {
			if visited&8191 == 0 {
				if err := ctx.Err(); err != nil {
					return postingListInfo{}, 0, err
				}
			}
			more, err := iterator.Next()
			if err != nil {
				return postingListInfo{}, 0, err
			}
			if !more {
				break
			}
			visited++
			document, termFrequency, _ := iterator.Current()
			remapped := source.remap[document]
			if remapped == math.MaxUint32 {
				continue
			}
			if hasPrevious && remapped <= previous {
				return postingListInfo{}, 0, corruptIndexf("merged postings are not strictly ordered")
			}
			mergedPosting := posting{
				document: remapped, frequency: termFrequency, norm: norms[document],
			}
			if includePositions {
				positions := iterator.CurrentPositions()
				if len(positions) != int(termFrequency) {
					return postingListInfo{}, 0, corruptIndexf("merged posting positions do not match term frequency")
				}
				mergedPosting.positions = append([]uint32(nil), positions...)
			}
			buffer = append(buffer, mergedPosting)
			previous, hasPrevious = remapped, true
			frequency++
			if len(buffer) == postingBlockDocuments {
				if err := flush(); err != nil {
					return postingListInfo{}, 0, err
				}
			}
		}
	}
	if err := flush(); err != nil {
		return postingListInfo{}, 0, err
	}
	return info, frequency, nil
}

func visitMergedDocuments(
	ctx context.Context,
	sources []mergeSource,
	visit func(*Segment, uint32) error,
) error {
	var visited uint64
	for _, source := range sources {
		for document, remapped := range source.remap {
			if remapped == math.MaxUint32 {
				continue
			}
			if visited&8191 == 0 {
				if err := ctx.Err(); err != nil {
					return err
				}
			}
			if err := visit(source.segment, uint32(document)); err != nil {
				return err
			}
			visited++
		}
	}
	return nil
}

func compareTermKeys(left, right termKey) int {
	if left.field < right.field {
		return -1
	}
	if left.field > right.field {
		return 1
	}
	return stringsCompare(left.term, right.term)
}
