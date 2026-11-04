package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"

	"github.com/kitwork/engine/kitdb/internal/checkpointfault"
)

func publishPreparedMainSnapshot(stagingPath, path string) (bool, error) {
	if err := storageReplace(path, "compact-replace", stagingPath, path); err != nil {
		return false, fmt.Errorf("kitdb: publish main file: %w", err)
	}
	checkpointfault.Reach(path, "compact-main-replaced")
	if err := storageSyncDirectory(path, "compact-directory-sync", filepath.Dir(path)); err != nil {
		return true, errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync main-file directory: %w", err))
	}
	return true, nil
}

func walkMergedRows(main *mainImage, overlay map[string]rowMutation, emit func(key, value []byte) error) error {
	iterator := newLogicalRowIterator(main, overlay, nil)
	for {
		key, value, found, err := iterator.next()
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if err := emit(key, value); err != nil {
			return err
		}
	}
}

type logicalRowIterator struct {
	main        *mainRowIterator
	overlay     map[string]rowMutation
	keys        []string
	change      int
	mainKey     []byte
	mainValue   []byte
	hasMain     bool
	initialized bool
	pendingErr  error
	reverse     bool
	borrowed    bool
	outputKey   []byte
	outputValue []byte
}

func newScanRowIteratorRange(main *mainImage, overlay map[string]rowMutation, start, end []byte) *logicalRowIterator {
	iterator := newDirectionalLogicalRowIterator(main, overlay, start, end, false)
	iterator.borrowed = true
	if merged := iterator.main.merged; merged != nil {
		merged.borrowed = true
		for index := range merged.cursors {
			physical := merged.cursors[index].iterator
			if physical == nil {
				continue // An initial page error leaves later cursors uninitialized.
			}
			physical.reusable = physical.page
			if physical.reusable == nil {
				physical.reusable = &rowPage{}
			}
		}
	} else {
		iterator.main.physical.reusable = &rowPage{}
	}
	return iterator
}

func (iterator *logicalRowIterator) currentMain() ([]byte, []byte) {
	if !iterator.borrowed {
		return iterator.mainKey, iterator.mainValue
	}
	// The main merge advances before returning the logical row.
	iterator.outputKey = append(iterator.outputKey[:0], iterator.mainKey...)
	iterator.outputValue = append(iterator.outputValue[:0], iterator.mainValue...)
	return iterator.outputKey, iterator.outputValue
}

func newLogicalRowIterator(main *mainImage, overlay map[string]rowMutation, start []byte) *logicalRowIterator {
	return newDirectionalLogicalRowIterator(main, overlay, start, nil, false)
}

func newDirectionalLogicalRowIterator(
	main *mainImage,
	overlay map[string]rowMutation,
	start, end []byte,
	reverse bool,
) *logicalRowIterator {
	keys := make([]string, 0, len(overlay))
	startKey, endKey := string(start), string(end)
	for key := range overlay {
		if len(start) != 0 && key < startKey || len(end) != 0 && key >= endKey {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if reverse {
		sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	}
	mainIterator := main.rangeIterator(start, end, reverse)
	return &logicalRowIterator{
		main: mainIterator, overlay: overlay, keys: keys, reverse: reverse,
	}
}

func (iterator *logicalRowIterator) next() ([]byte, []byte, bool, error) {
	if iterator.pendingErr != nil {
		err := iterator.pendingErr
		iterator.pendingErr = nil
		return nil, nil, false, err
	}
	if !iterator.initialized {
		iterator.initialized = true
		if err := iterator.advanceMain(); err != nil {
			return nil, nil, false, err
		}
	}
	for iterator.hasMain || iterator.change < len(iterator.keys) {
		if !iterator.hasMain {
			key := iterator.keys[iterator.change]
			mutation := iterator.overlay[key]
			iterator.change++
			if !mutation.deleted {
				return []byte(key), mutation.value, true, nil
			}
			continue
		}
		if iterator.change == len(iterator.keys) {
			key, value := iterator.currentMain()
			if err := iterator.advanceMain(); err != nil {
				iterator.pendingErr = err
			}
			return key, value, true, nil
		}

		changeKey := []byte(iterator.keys[iterator.change])
		switch comparison := bytes.Compare(iterator.mainKey, changeKey); {
		case comparison < 0 && !iterator.reverse, comparison > 0 && iterator.reverse:
			key, value := iterator.currentMain()
			if err := iterator.advanceMain(); err != nil {
				iterator.pendingErr = err
			}
			return key, value, true, nil
		case comparison > 0 && !iterator.reverse, comparison < 0 && iterator.reverse:
			mutation := iterator.overlay[iterator.keys[iterator.change]]
			iterator.change++
			if !mutation.deleted {
				return changeKey, mutation.value, true, nil
			}
		default:
			mutation := iterator.overlay[iterator.keys[iterator.change]]
			iterator.change++
			err := iterator.advanceMain()
			if mutation.deleted {
				if err != nil {
					return nil, nil, false, err
				}
				continue
			}
			if err != nil {
				iterator.pendingErr = err
			}
			return changeKey, mutation.value, true, nil
		}
	}
	return nil, nil, false, nil
}

func (iterator *logicalRowIterator) stats() CursorStats {
	if iterator == nil {
		return CursorStats{}
	}
	stats := iterator.main.stats()
	stats.OverlayEntriesVisited += uint64(iterator.change)
	return stats
}

func (iterator *logicalRowIterator) advanceMain() error {
	key, value, found, err := iterator.main.next()
	if err != nil {
		return err
	}
	iterator.mainKey = key
	iterator.mainValue = value
	iterator.hasMain = found
	return nil
}
