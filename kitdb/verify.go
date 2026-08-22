package kitdb

import (
	"bytes"
	"errors"
	"fmt"
	"os"
)

// Verify checks every persisted main-file page. WAL frames are already
// checksummed during recovery and before becoming visible, so a successful
// call verifies the complete durable state represented by this handle.
func (db *DB) Verify() error {
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return err
	}
	return db.main.verify()
}

func (main *mainImage) verify() error {
	if main == nil || main.file == nil {
		return ErrUnavailable
	}
	if main.formatVersion == mainLegacyFormatVersion {
		file, err := os.Open(main.path)
		if err != nil {
			return fmt.Errorf("kitdb: open legacy main file for verification: %w", err)
		}
		info, statErr := file.Stat()
		if statErr == nil {
			_, statErr = decodeMainSnapshot(main.path, file, info.Size())
		}
		return errors.Join(statErr, file.Close())
	}
	if main.formatVersion == mainFormatVersion {
		for _, segment := range main.segments {
			var mutations uint64
			var previousLastKey []byte
			for blockIndex := segment.firstBlock; blockIndex < segment.firstBlock+segment.blockCount; blockIndex++ {
				page, err := main.readPage(blockIndex)
				if err != nil {
					return err
				}
				if previousLastKey != nil && bytes.Compare(previousLastKey, page.rows[0].key) >= 0 {
					return corruptFileAt(main.path, main.blocks[blockIndex].offset, "generation segment page keys are not strictly increasing")
				}
				previousLastKey = page.rows[len(page.rows)-1].key
				mutations += uint64(len(page.rows))
			}
			if mutations != segment.mutations {
				return corruptFileAt(main.path, main.recordEnd, "generation segment contains %d mutations instead of %d", mutations, segment.mutations)
			}
		}
		iterator := main.iterator()
		for {
			_, _, found, err := iterator.next()
			if err != nil {
				return err
			}
			if !found {
				return nil
			}
		}
	}
	var records uint64
	var previousLastKey []byte
	for blockIndex := range main.blocks {
		page, err := main.readPage(blockIndex)
		if err != nil {
			return err
		}
		if previousLastKey != nil && bytes.Compare(previousLastKey, page.rows[0].key) >= 0 {
			return corruptFileAt(main.path, main.blocks[blockIndex].offset, "main page keys are not strictly increasing")
		}
		previousLastKey = page.rows[len(page.rows)-1].key
		records += uint64(len(page.rows))
	}
	if records != main.records {
		return corruptFileAt(main.path, main.recordEnd, "main pages contain %d records instead of %d", records, main.records)
	}
	return nil
}
