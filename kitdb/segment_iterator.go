package kitdb

import (
	"bytes"
	"sort"
)

type mainRowIterator struct {
	main            *mainImage
	physical        *physicalSegmentIterator
	merged          *generationMergeIterator
	reversePhysical *reversePhysicalSegmentIterator
	reverseMerged   *reverseGenerationMergeIterator
	emitted         uint64
	validate        bool
}

func (main *mainImage) iterator() *mainRowIterator {
	return main.iteratorFrom(nil)
}

func (main *mainImage) iteratorFrom(start []byte) *mainRowIterator {
	iterator := &mainRowIterator{main: main, validate: len(start) == 0}
	if main.formatVersion == mainFormatVersion {
		iterator.merged = newGenerationMergeIteratorFrom(main, start)
	} else {
		iterator.physical = newPhysicalSegmentIteratorFrom(main, 0, len(main.blocks), start)
	}
	return iterator
}

func (main *mainImage) reverseIteratorFrom(end []byte) *mainRowIterator {
	iterator := &mainRowIterator{main: main, validate: len(end) == 0}
	if main.formatVersion == mainFormatVersion {
		iterator.reverseMerged = newReverseGenerationMergeIteratorFrom(main, end)
	} else {
		iterator.reversePhysical = newReversePhysicalSegmentIteratorFrom(main, 0, len(main.blocks), end)
	}
	return iterator
}

func (iterator *mainRowIterator) next() ([]byte, []byte, bool, error) {
	var row mainRow
	var found bool
	var err error
	if iterator.merged != nil {
		row, found, err = iterator.merged.next()
	} else if iterator.reverseMerged != nil {
		row, found, err = iterator.reverseMerged.next()
	} else if iterator.reversePhysical != nil {
		row, found, err = iterator.reversePhysical.next()
	} else {
		row, found, err = iterator.physical.next()
	}
	if err != nil {
		return nil, nil, false, err
	}
	if !found {
		if iterator.validate && iterator.emitted != iterator.main.records {
			return nil, nil, false, corruptFileAt(iterator.main.path, iterator.main.recordEnd, "main iterator produced %d records instead of %d", iterator.emitted, iterator.main.records)
		}
		return nil, nil, false, nil
	}
	if row.deleted {
		return nil, nil, false, corruptFileAt(iterator.main.path, iterator.main.recordEnd, "logical main iterator exposed a tombstone")
	}
	iterator.emitted++
	return row.key, row.value, true, nil
}

func (iterator *mainRowIterator) stats() CursorStats {
	if iterator == nil {
		return CursorStats{}
	}
	switch {
	case iterator.merged != nil:
		return iterator.merged.stats()
	case iterator.reverseMerged != nil:
		return iterator.reverseMerged.stats()
	case iterator.reversePhysical != nil:
		return iterator.reversePhysical.stats()
	case iterator.physical != nil:
		return iterator.physical.stats()
	default:
		return CursorStats{}
	}
}

type physicalSegmentIterator struct {
	main         *mainImage
	block        int
	blockEnd     int
	initialBlock int
	initialRow   int
	row          int
	page         *rowPage
	seek         []byte
	started      bool
}

func newPhysicalSegmentIterator(main *mainImage, firstBlock, blockCount int) *physicalSegmentIterator {
	return newPhysicalSegmentIteratorFrom(main, firstBlock, blockCount, nil)
}

func newPhysicalSegmentIteratorFrom(main *mainImage, firstBlock, blockCount int, start []byte) *physicalSegmentIterator {
	blockOffset := 0
	if len(start) != 0 {
		blockOffset = sort.Search(blockCount, func(index int) bool {
			lastKey := main.blocks[firstBlock+index].lastKey
			return len(lastKey) == 0 || bytes.Compare(lastKey, start) >= 0
		})
	}
	return &physicalSegmentIterator{
		main: main, block: firstBlock + blockOffset, blockEnd: firstBlock + blockCount,
		initialBlock: firstBlock + blockOffset, seek: bytes.Clone(start),
	}
}

func (iterator *physicalSegmentIterator) next() (mainRow, bool, error) {
	for iterator.page == nil || iterator.row == len(iterator.page.rows) {
		if iterator.block >= iterator.blockEnd {
			return mainRow{}, false, nil
		}
		page, err := iterator.main.readPage(iterator.block)
		if err != nil {
			return mainRow{}, false, err
		}
		firstPage := !iterator.started
		iterator.page = page
		iterator.row = 0
		iterator.block++
		if len(iterator.seek) != 0 {
			iterator.row = sort.Search(len(page.rows), func(index int) bool {
				return bytes.Compare(page.rows[index].key, iterator.seek) >= 0
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
	return row, true, nil
}

func (iterator *physicalSegmentIterator) stats() CursorStats {
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
		consumed := iterator.row - iterator.initialRow
		if consumed > 0 {
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
	if iterator.row > 0 {
		stats.GenerationEntriesVisited += uint64(iterator.row)
	}
	return stats
}

type reversePhysicalSegmentIterator struct {
	main         *mainImage
	block        int
	blockStart   int
	initialBlock int
	initialRow   int
	row          int
	page         *rowPage
	seek         []byte
	started      bool
}

func newReversePhysicalSegmentIteratorFrom(
	main *mainImage,
	firstBlock, blockCount int,
	end []byte,
) *reversePhysicalSegmentIterator {
	blockOffset := blockCount - 1
	if len(end) != 0 {
		blockOffset = sort.Search(blockCount, func(index int) bool {
			return bytes.Compare(main.blocks[firstBlock+index].firstKey, end) >= 0
		}) - 1
	}
	return &reversePhysicalSegmentIterator{
		main: main, block: firstBlock + blockOffset, blockStart: firstBlock,
		initialBlock: firstBlock + blockOffset, row: -1, seek: bytes.Clone(end),
	}
}

func (iterator *reversePhysicalSegmentIterator) next() (mainRow, bool, error) {
	for iterator.page == nil || iterator.row < 0 {
		if iterator.block < iterator.blockStart {
			return mainRow{}, false, nil
		}
		page, err := iterator.main.readPage(iterator.block)
		if err != nil {
			return mainRow{}, false, err
		}
		firstPage := !iterator.started
		iterator.page = page
		iterator.row = len(page.rows) - 1
		iterator.block--
		if len(iterator.seek) != 0 {
			iterator.row = sort.Search(len(page.rows), func(index int) bool {
				return bytes.Compare(page.rows[index].key, iterator.seek) >= 0
			}) - 1
			iterator.seek = nil
		}
		if firstPage {
			iterator.initialRow = iterator.row
			iterator.started = true
		}
	}
	row := iterator.page.rows[iterator.row]
	iterator.row--
	return row, true, nil
}

func (iterator *reversePhysicalSegmentIterator) stats() CursorStats {
	if iterator == nil || !iterator.started || iterator.main == nil {
		return CursorStats{}
	}
	loadedStart := max(iterator.block+1, iterator.blockStart)
	if loadedStart > iterator.initialBlock {
		return CursorStats{}
	}
	stats := CursorStats{}
	for block := iterator.initialBlock; block >= loadedStart; block-- {
		directory := iterator.main.blocks[block]
		stats.PagesRead++
		stats.PageBytesRead += uint64(directory.length)
		stats.PageRecordsDecoded += uint64(directory.records)
	}
	stats.PageAccesses = stats.PagesRead
	stats.PageCacheBypasses = stats.PagesRead
	currentBlock := loadedStart
	if currentBlock == iterator.initialBlock {
		consumed := iterator.initialRow - iterator.row
		if consumed > 0 {
			stats.GenerationEntriesVisited = uint64(consumed)
		}
		return stats
	}
	if iterator.initialRow >= 0 {
		stats.GenerationEntriesVisited += uint64(iterator.initialRow + 1)
	}
	for block := iterator.initialBlock - 1; block > currentBlock; block-- {
		stats.GenerationEntriesVisited += uint64(iterator.main.blocks[block].records)
	}
	currentRecords := int(iterator.main.blocks[currentBlock].records)
	consumed := currentRecords - 1 - iterator.row
	if consumed > 0 {
		stats.GenerationEntriesVisited += uint64(consumed)
	}
	return stats
}

type generationCursor struct {
	iterator *physicalSegmentIterator
	row      mainRow
}

type generationHeapItem struct {
	key     []byte
	segment int
}

type generationMergeHeap struct {
	items   []generationHeapItem
	reverse bool
}

func (items *generationMergeHeap) len() int { return len(items.items) }

func (items *generationMergeHeap) less(left, right generationHeapItem) bool {
	comparison := bytes.Compare(left.key, right.key)
	if comparison != 0 {
		if items.reverse {
			return comparison > 0
		}
		return comparison < 0
	}
	return left.segment > right.segment
}

func (items *generationMergeHeap) first() generationHeapItem {
	return items.items[0]
}

func (items *generationMergeHeap) push(item generationHeapItem) {
	items.items = append(items.items, item)
	child := len(items.items) - 1
	for child > 0 {
		parent := (child - 1) / 2
		if !items.less(items.items[child], items.items[parent]) {
			break
		}
		items.items[parent], items.items[child] = items.items[child], items.items[parent]
		child = parent
	}
}

func (items *generationMergeHeap) pop() generationHeapItem {
	last := len(items.items) - 1
	first := items.items[0]
	tail := items.items[last]
	items.items[last] = generationHeapItem{}
	items.items = items.items[:last]
	if last == 0 {
		return first
	}
	items.items[0] = tail
	parent := 0
	for {
		left := 2*parent + 1
		if left >= len(items.items) {
			break
		}
		child := left
		right := left + 1
		if right < len(items.items) && items.less(items.items[right], items.items[left]) {
			child = right
		}
		if !items.less(items.items[child], items.items[parent]) {
			break
		}
		items.items[parent], items.items[child] = items.items[child], items.items[parent]
		parent = child
	}
	return first
}

type generationMergeIterator struct {
	cursors    []generationCursor
	items      generationMergeHeap
	initialErr error
}

func newGenerationMergeIterator(main *mainImage) *generationMergeIterator {
	return newGenerationMergeIteratorFrom(main, nil)
}

func newGenerationMergeIteratorFrom(main *mainImage, start []byte) *generationMergeIterator {
	iterator := &generationMergeIterator{cursors: make([]generationCursor, len(main.segments))}
	for segmentIndex, segment := range main.segments {
		cursor := generationCursor{iterator: newPhysicalSegmentIteratorFrom(main, segment.firstBlock, segment.blockCount, start)}
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

func (iterator *generationMergeIterator) next() (mainRow, bool, error) {
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
		return chosen, true, nil
	}
	return mainRow{}, false, nil
}

func (iterator *generationMergeIterator) stats() CursorStats {
	stats := CursorStats{}
	if iterator == nil {
		return stats
	}
	for index := range iterator.cursors {
		stats.Add(iterator.cursors[index].iterator.stats())
	}
	return stats
}

type reverseGenerationCursor struct {
	iterator *reversePhysicalSegmentIterator
	row      mainRow
}

type reverseGenerationMergeIterator struct {
	cursors    []reverseGenerationCursor
	items      generationMergeHeap
	initialErr error
}

func newReverseGenerationMergeIteratorFrom(main *mainImage, end []byte) *reverseGenerationMergeIterator {
	iterator := &reverseGenerationMergeIterator{
		cursors: make([]reverseGenerationCursor, len(main.segments)),
		items:   generationMergeHeap{reverse: true},
	}
	for segmentIndex, segment := range main.segments {
		cursor := reverseGenerationCursor{iterator: newReversePhysicalSegmentIteratorFrom(
			main, segment.firstBlock, segment.blockCount, end,
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

func (iterator *reverseGenerationMergeIterator) next() (mainRow, bool, error) {
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
		return chosen, true, nil
	}
	return mainRow{}, false, nil
}

func (iterator *reverseGenerationMergeIterator) stats() CursorStats {
	stats := CursorStats{}
	if iterator == nil {
		return stats
	}
	for index := range iterator.cursors {
		stats.Add(iterator.cursors[index].iterator.stats())
	}
	return stats
}
