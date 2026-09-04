package kitdb

import (
	"bytes"
	"sort"
)

// physicalKeySegmentIterator owns one reusable page buffer. It is used only
// by callback-scoped key scans, so no row value or page escapes an iteration.
type physicalKeySegmentIterator struct {
	main         *mainImage
	block        int
	blockEnd     int
	initialBlock int
	initialRow   int
	row          int
	page         rowPage
	seek         []byte
	started      bool
}

func newPhysicalKeySegmentIteratorFrom(
	main *mainImage,
	firstBlock, blockCount int,
	start []byte,
) *physicalKeySegmentIterator {
	blockOffset := 0
	if len(start) != 0 {
		blockOffset = sort.Search(blockCount, func(index int) bool {
			lastKey := main.blocks[firstBlock+index].lastKey
			return len(lastKey) == 0 || bytes.Compare(lastKey, start) >= 0
		})
	}
	return &physicalKeySegmentIterator{
		main: main, block: firstBlock + blockOffset, blockEnd: firstBlock + blockCount,
		initialBlock: firstBlock + blockOffset, seek: bytes.Clone(start),
	}
}

func (iterator *physicalKeySegmentIterator) next() (mainRow, bool, error) {
	for len(iterator.page.rows) == 0 || iterator.row == len(iterator.page.rows) {
		if iterator.block >= iterator.blockEnd {
			return mainRow{}, false, nil
		}
		if err := iterator.main.readPageInto(iterator.block, &iterator.page); err != nil {
			return mainRow{}, false, err
		}
		firstPage := !iterator.started
		iterator.row = 0
		iterator.block++
		if len(iterator.seek) != 0 {
			iterator.row = sort.Search(len(iterator.page.rows), func(index int) bool {
				return bytes.Compare(iterator.page.rows[index].key, iterator.seek) >= 0
			})
			iterator.seek = nil
		}
		if firstPage {
			iterator.initialRow = iterator.row
			iterator.started = true
		}
	}
	row := iterator.page.rows[iterator.row]
	iterator.row++
	return mainRow{key: row.key, deleted: row.deleted}, true, nil
}

func (iterator *physicalKeySegmentIterator) stats() CursorStats {
	if iterator == nil || !iterator.started || iterator.main == nil {
		return CursorStats{}
	}
	loadedEnd := min(iterator.block, iterator.blockEnd)
	if loadedEnd <= iterator.initialBlock {
		return CursorStats{}
	}
	stats := CursorStats{}
	for block := iterator.initialBlock; block < loadedEnd; block++ {
		directory := iterator.main.blocks[block]
		stats.PagesRead++
		stats.PageBytesRead += uint64(directory.length)
		stats.PageRecordsDecoded += uint64(directory.records)
	}
	stats.PageAccesses = stats.PagesRead
	stats.PageCacheBypasses = stats.PagesRead
	currentBlock := loadedEnd - 1
	if currentBlock == iterator.initialBlock {
		if consumed := iterator.row - iterator.initialRow; consumed > 0 {
			stats.GenerationEntriesVisited = uint64(consumed)
		}
		return stats
	}
	firstRecords := int(iterator.main.blocks[iterator.initialBlock].records)
	if iterator.initialRow < firstRecords {
		stats.GenerationEntriesVisited += uint64(firstRecords - iterator.initialRow)
	}
	for block := iterator.initialBlock + 1; block < currentBlock; block++ {
		stats.GenerationEntriesVisited += uint64(iterator.main.blocks[block].records)
	}
	stats.GenerationEntriesVisited += uint64(iterator.row)
	return stats
}

type generationKeyCursor struct {
	iterator *physicalKeySegmentIterator
	row      mainRow
}

type generationKeyMergeIterator struct {
	cursors    []generationKeyCursor
	items      generationMergeHeap
	output     []byte
	initialErr error
}

func newGenerationKeyMergeIteratorFrom(main *mainImage, start []byte) *generationKeyMergeIterator {
	iterator := &generationKeyMergeIterator{cursors: make([]generationKeyCursor, len(main.segments))}
	for segmentIndex, segment := range main.segments {
		cursor := generationKeyCursor{iterator: newPhysicalKeySegmentIteratorFrom(
			main, segment.firstBlock, segment.blockCount, start,
		)}
		row, found, err := cursor.iterator.next()
		if err != nil {
			iterator.initialErr = err
			return iterator
		}
		if found {
			cursor.row = row
			iterator.items.push(generationHeapItem{key: row.key, segment: segmentIndex})
		}
		iterator.cursors[segmentIndex] = cursor
	}
	return iterator
}

func (iterator *generationKeyMergeIterator) next() (mainRow, bool, error) {
	if iterator.initialErr != nil {
		err := iterator.initialErr
		iterator.initialErr = nil
		return mainRow{}, false, err
	}
	for iterator.items.len() != 0 {
		first := iterator.items.pop()
		key := first.key
		var duplicates [maxMainSegments]generationHeapItem
		duplicates[0] = first
		duplicateCount := 1
		for iterator.items.len() != 0 && bytes.Equal(iterator.items.first().key, key) {
			duplicates[duplicateCount] = iterator.items.pop()
			duplicateCount++
		}
		newest := duplicates[0].segment
		for _, item := range duplicates[1:duplicateCount] {
			if item.segment > newest {
				newest = item.segment
			}
		}
		chosen := iterator.cursors[newest].row
		iterator.output = append(iterator.output[:0], chosen.key...)
		for _, item := range duplicates[:duplicateCount] {
			cursor := &iterator.cursors[item.segment]
			row, found, err := cursor.iterator.next()
			if err != nil {
				return mainRow{}, false, err
			}
			if found {
				cursor.row = row
				iterator.items.push(generationHeapItem{key: row.key, segment: item.segment})
			}
		}
		if chosen.deleted {
			continue
		}
		return mainRow{key: iterator.output}, true, nil
	}
	return mainRow{}, false, nil
}

func (iterator *generationKeyMergeIterator) stats() CursorStats {
	stats := CursorStats{}
	if iterator == nil {
		return stats
	}
	for index := range iterator.cursors {
		stats.Add(iterator.cursors[index].iterator.stats())
	}
	return stats
}

type mainKeyIterator struct {
	main       *mainImage
	physical   *physicalKeySegmentIterator
	merged     *generationKeyMergeIterator
	emitted    uint64
	validate   bool
	pendingErr error
}

func newMainKeyIteratorFrom(main *mainImage, start []byte) *mainKeyIterator {
	iterator := &mainKeyIterator{main: main, validate: len(start) == 0}
	if main.formatVersion == mainFormatVersion {
		iterator.merged = newGenerationKeyMergeIteratorFrom(main, start)
	} else {
		iterator.physical = newPhysicalKeySegmentIteratorFrom(main, 0, len(main.blocks), start)
	}
	return iterator
}

func (iterator *mainKeyIterator) next() ([]byte, bool, error) {
	if iterator.pendingErr != nil {
		err := iterator.pendingErr
		iterator.pendingErr = nil
		return nil, false, err
	}
	var row mainRow
	var found bool
	var err error
	if iterator.merged != nil {
		row, found, err = iterator.merged.next()
	} else {
		row, found, err = iterator.physical.next()
	}
	if err != nil {
		return nil, false, err
	}
	if !found {
		if iterator.validate && iterator.emitted != iterator.main.records {
			return nil, false, corruptFileAt(
				iterator.main.path, iterator.main.recordEnd,
				"main key iterator produced %d records instead of %d", iterator.emitted, iterator.main.records,
			)
		}
		return nil, false, nil
	}
	if row.deleted {
		return nil, false, corruptFileAt(
			iterator.main.path, iterator.main.recordEnd, "logical main key iterator exposed a tombstone",
		)
	}
	iterator.emitted++
	return row.key, true, nil
}

func (iterator *mainKeyIterator) stats() CursorStats {
	if iterator == nil {
		return CursorStats{}
	}
	if iterator.merged != nil {
		return iterator.merged.stats()
	}
	return iterator.physical.stats()
}

type logicalKeyIterator struct {
	main        *mainKeyIterator
	overlay     map[string]rowMutation
	keys        []string
	change      int
	mainKey     []byte
	hasMain     bool
	initialized bool
	pendingErr  error
	output      []byte
}

func newLogicalKeyIterator(main *mainImage, overlay map[string]rowMutation, start []byte) *logicalKeyIterator {
	keys := make([]string, 0, len(overlay))
	bound := string(start)
	for key := range overlay {
		if len(start) == 0 || key >= bound {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return &logicalKeyIterator{
		main: newMainKeyIteratorFrom(main, start), overlay: overlay, keys: keys,
	}
}

func (iterator *logicalKeyIterator) next() ([]byte, bool, error) {
	if iterator.pendingErr != nil {
		err := iterator.pendingErr
		iterator.pendingErr = nil
		return nil, false, err
	}
	if !iterator.initialized {
		iterator.initialized = true
		if err := iterator.advanceMain(); err != nil {
			return nil, false, err
		}
	}
	for iterator.hasMain || iterator.change < len(iterator.keys) {
		if !iterator.hasMain {
			key := iterator.keys[iterator.change]
			mutation := iterator.overlay[key]
			iterator.change++
			if !mutation.deleted {
				iterator.output = append(iterator.output[:0], key...)
				return iterator.output, true, nil
			}
			continue
		}
		if iterator.change == len(iterator.keys) {
			iterator.output = append(iterator.output[:0], iterator.mainKey...)
			if err := iterator.advanceMain(); err != nil {
				iterator.pendingErr = err
			}
			return iterator.output, true, nil
		}

		changeKey := iterator.keys[iterator.change]
		comparison := bytes.Compare(iterator.mainKey, []byte(changeKey))
		switch {
		case comparison < 0:
			iterator.output = append(iterator.output[:0], iterator.mainKey...)
			if err := iterator.advanceMain(); err != nil {
				iterator.pendingErr = err
			}
			return iterator.output, true, nil
		case comparison > 0:
			mutation := iterator.overlay[changeKey]
			iterator.change++
			if !mutation.deleted {
				iterator.output = append(iterator.output[:0], changeKey...)
				return iterator.output, true, nil
			}
		default:
			mutation := iterator.overlay[changeKey]
			iterator.change++
			iterator.output = append(iterator.output[:0], changeKey...)
			err := iterator.advanceMain()
			if mutation.deleted {
				if err != nil {
					return nil, false, err
				}
				continue
			}
			if err != nil {
				iterator.pendingErr = err
			}
			return iterator.output, true, nil
		}
	}
	return nil, false, nil
}

func (iterator *logicalKeyIterator) advanceMain() error {
	key, found, err := iterator.main.next()
	if err != nil {
		return err
	}
	iterator.mainKey = key
	iterator.hasMain = found
	return nil
}

func (iterator *logicalKeyIterator) stats() CursorStats {
	if iterator == nil {
		return CursorStats{}
	}
	stats := iterator.main.stats()
	stats.OverlayEntriesVisited += uint64(iterator.change)
	return stats
}
