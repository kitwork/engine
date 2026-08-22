package kitdb

import (
	"bytes"
	"container/heap"
	"sort"
)

type mainRowIterator struct {
	main     *mainImage
	physical *physicalSegmentIterator
	merged   *generationMergeIterator
	emitted  uint64
	validate bool
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

func (iterator *mainRowIterator) next() ([]byte, []byte, bool, error) {
	var row mainRow
	var found bool
	var err error
	if iterator.merged != nil {
		row, found, err = iterator.merged.next()
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

type physicalSegmentIterator struct {
	main     *mainImage
	block    int
	blockEnd int
	row      int
	page     *rowPage
	seek     []byte
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
		seek: bytes.Clone(start),
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
		iterator.page = page
		iterator.row = 0
		iterator.block++
		if len(iterator.seek) != 0 {
			iterator.row = sort.Search(len(page.rows), func(index int) bool {
				return bytes.Compare(page.rows[index].key, iterator.seek) >= 0
			})
			iterator.seek = nil
		}
	}
	row := iterator.page.rows[iterator.row]
	iterator.row++
	return row, true, nil
}

type generationCursor struct {
	iterator *physicalSegmentIterator
	row      mainRow
}

type generationHeapItem struct {
	key     []byte
	segment int
}

type generationMergeHeap []generationHeapItem

func (items generationMergeHeap) Len() int { return len(items) }

func (items generationMergeHeap) Less(left, right int) bool {
	comparison := bytes.Compare(items[left].key, items[right].key)
	if comparison != 0 {
		return comparison < 0
	}
	return items[left].segment > items[right].segment
}

func (items generationMergeHeap) Swap(left, right int) {
	items[left], items[right] = items[right], items[left]
}

func (items *generationMergeHeap) Push(value any) {
	*items = append(*items, value.(generationHeapItem))
}

func (items *generationMergeHeap) Pop() any {
	old := *items
	last := len(old) - 1
	item := old[last]
	old[last] = generationHeapItem{}
	*items = old[:last]
	return item
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
			heap.Push(&iterator.items, generationHeapItem{key: row.key, segment: segmentIndex})
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
	for iterator.items.Len() != 0 {
		first := heap.Pop(&iterator.items).(generationHeapItem)
		key := first.key
		var duplicates [maxMainSegments]generationHeapItem
		duplicates[0] = first
		duplicateCount := 1
		for iterator.items.Len() != 0 && bytes.Equal(iterator.items[0].key, key) {
			duplicates[duplicateCount] = heap.Pop(&iterator.items).(generationHeapItem)
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
				heap.Push(&iterator.items, generationHeapItem{key: row.key, segment: item.segment})
			}
		}
		if chosen.deleted {
			continue
		}
		return chosen, true, nil
	}
	return mainRow{}, false, nil
}
