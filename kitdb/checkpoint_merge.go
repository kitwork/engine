package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
)

func prepareMergedMainSnapshot(path string, identity [16]byte, transaction uint64, boundaryChecksum uint32, main *mainImage, overlay map[string]rowMutation) (stagingPath string, returnErr error) {
	if main == nil || main.file == nil {
		return "", fmt.Errorf("kitdb: main snapshot is unavailable")
	}
	return prepareMainSnapshot(path, identity, transaction, boundaryChecksum, func(emit func(key, value []byte) error) error {
		return walkMergedRows(main, overlay, emit)
	})
}

func publishPreparedMainSnapshot(stagingPath, path string) (bool, error) {
	if err := replaceFile(stagingPath, path); err != nil {
		return false, fmt.Errorf("kitdb: publish main file: %w", err)
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
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
}

func newLogicalRowIterator(main *mainImage, overlay map[string]rowMutation, start []byte) *logicalRowIterator {
	keys := make([]string, 0, len(overlay))
	for key := range overlay {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	change := 0
	if len(start) != 0 {
		change = sort.SearchStrings(keys, string(start))
	}
	return &logicalRowIterator{
		main: main.iteratorFrom(start), overlay: overlay, keys: keys, change: change,
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
			key, value := iterator.mainKey, iterator.mainValue
			if err := iterator.advanceMain(); err != nil {
				iterator.pendingErr = err
			}
			return key, value, true, nil
		}

		changeKey := []byte(iterator.keys[iterator.change])
		switch comparison := bytes.Compare(iterator.mainKey, changeKey); {
		case comparison < 0:
			key, value := iterator.mainKey, iterator.mainValue
			if err := iterator.advanceMain(); err != nil {
				iterator.pendingErr = err
			}
			return key, value, true, nil
		case comparison > 0:
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
