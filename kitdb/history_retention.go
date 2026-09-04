package kitdb

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	historyPinsFilename       = "PINS"
	historyPinsMagic          = "KITDBP01"
	historyPinsFormatVersion  = 1
	historyPinsHeaderSize     = 48
	historyPinEntryHeaderSize = 16
	historyPinsTrailerSize    = 4
	maxHistoryPins            = 1024
	maxHistoryPinNameBytes    = 128
	maxHistoryPinsFileSize    = historyPinsHeaderSize + maxHistoryPins*(historyPinEntryHeaderSize+maxHistoryPinNameBytes) + historyPinsTrailerSize
)

// HistoryCursor identifies one exact committed boundary in one database.
type HistoryCursor struct {
	DatabaseID  string
	Transaction uint64
	Checksum    uint32
}

// HistoryPin is one durable named lower bound for retained history. A pin at T
// permits pruning through T while preserving every transaction after T.
type HistoryPin struct {
	Name   string
	Cursor HistoryCursor
}

// HistoryPruneResult describes one explicit whole-segment retention advance.
type HistoryPruneResult struct {
	DatabaseID              string
	RequestedThrough        uint64
	PreviousBaseTransaction uint64
	BaseTransaction         uint64
	BaseChecksum            uint32
	PrunedSegments          int
	PrunedBytes             int64
	CleanupPendingSegments  int
	CleanupPendingBytes     int64
	baseCommitTime          int64
}

// Cursor returns the new exclusive history base after pruning.
func (result HistoryPruneResult) Cursor() HistoryCursor {
	return HistoryCursor{
		DatabaseID:  result.DatabaseID,
		Transaction: result.BaseTransaction,
		Checksum:    result.BaseChecksum,
	}
}

func readHistoryPins(path string, identity [16]byte) (map[string]HistoryCursor, error) {
	pins := make(map[string]HistoryCursor)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return pins, nil
	}
	if err != nil {
		return nil, fmt.Errorf("kitdb: open history pins: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("kitdb: stat history pins: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, corruptFileAt(path, 0, "history pins are not a regular file")
	}
	if info.Size() < historyPinsHeaderSize+historyPinsTrailerSize || info.Size() > maxHistoryPinsFileSize {
		return nil, corruptFileAt(path, 0, "history pins size is %d", info.Size())
	}
	encoded := make([]byte, int(info.Size()))
	if _, err := io.ReadFull(file, encoded); err != nil {
		return nil, fmt.Errorf("kitdb: read history pins: %w", err)
	}
	if string(encoded[:8]) != historyPinsMagic {
		return nil, corruptFileAt(path, 0, "invalid history pins magic")
	}
	if version := binary.LittleEndian.Uint16(encoded[8:10]); version != historyPinsFormatVersion {
		return nil, corruptFileAt(path, 8, "unsupported history pins version %d", version)
	}
	if size := binary.LittleEndian.Uint16(encoded[10:12]); size != historyPinsHeaderSize {
		return nil, corruptFileAt(path, 10, "invalid history pins header size %d", size)
	}
	if flags := binary.LittleEndian.Uint32(encoded[12:16]); flags != 0 {
		return nil, corruptFileAt(path, 12, "unsupported history pins flags %d", flags)
	}
	if string(encoded[16:32]) != string(identity[:]) {
		return nil, corruptFileAt(path, 16, "history pins belong to a different database")
	}
	count := binary.LittleEndian.Uint32(encoded[32:36])
	if count > maxHistoryPins {
		return nil, corruptFileAt(path, 32, "history pin count %d exceeds %d", count, maxHistoryPins)
	}
	payloadSize := binary.LittleEndian.Uint32(encoded[36:40])
	if uint64(payloadSize)+historyPinsHeaderSize+historyPinsTrailerSize != uint64(len(encoded)) {
		return nil, corruptFileAt(path, 36, "history pins payload size %d does not match file size %d", payloadSize, len(encoded))
	}
	for offset := 40; offset < historyPinsHeaderSize; offset++ {
		if encoded[offset] != 0 {
			return nil, corruptFileAt(path, int64(offset), "unsupported history pins reserved value")
		}
	}
	checksumAt := len(encoded) - historyPinsTrailerSize
	declared := binary.LittleEndian.Uint32(encoded[checksumAt:])
	if actual := crc32.Checksum(encoded[:checksumAt], crc32cTable); actual != declared {
		return nil, corruptFileAt(path, int64(checksumAt), "history pins checksum mismatch")
	}

	databaseID := hex.EncodeToString(identity[:])
	offset := historyPinsHeaderSize
	previousName := ""
	for index := uint32(0); index < count; index++ {
		if offset+historyPinEntryHeaderSize > checksumAt {
			return nil, corruptFileAt(path, int64(offset), "history pin entry %d is truncated", index)
		}
		nameSize := int(binary.LittleEndian.Uint16(encoded[offset : offset+2]))
		if reserved := binary.LittleEndian.Uint16(encoded[offset+2 : offset+4]); reserved != 0 {
			return nil, corruptFileAt(path, int64(offset+2), "history pin entry %d has unsupported flags", index)
		}
		checksum := binary.LittleEndian.Uint32(encoded[offset+4 : offset+8])
		transaction := binary.LittleEndian.Uint64(encoded[offset+8 : offset+16])
		offset += historyPinEntryHeaderSize
		if nameSize == 0 || nameSize > maxHistoryPinNameBytes || offset+nameSize > checksumAt {
			return nil, corruptFileAt(path, int64(offset), "history pin entry %d has invalid name size %d", index, nameSize)
		}
		name := string(encoded[offset : offset+nameSize])
		offset += nameSize
		if err := ValidateHistoryPinName(name); err != nil {
			return nil, corruptFileAt(path, int64(offset-nameSize), "history pin entry %d has invalid name: %v", index, err)
		}
		if previousName != "" && name <= previousName {
			return nil, corruptFileAt(path, int64(offset-nameSize), "history pin names are not strictly increasing")
		}
		previousName = name
		pins[name] = HistoryCursor{DatabaseID: databaseID, Transaction: transaction, Checksum: checksum}
	}
	if offset != checksumAt {
		return nil, corruptFileAt(path, int64(offset), "history pins contain %d trailing payload bytes", checksumAt-offset)
	}
	return pins, nil
}

func encodeHistoryPins(identity [16]byte, pins map[string]HistoryCursor) ([]byte, error) {
	if len(pins) > maxHistoryPins {
		return nil, ErrHistoryPinLimit
	}
	names := make([]string, 0, len(pins))
	payloadSize := 0
	databaseID := hex.EncodeToString(identity[:])
	for name := range pins {
		if err := ValidateHistoryPinName(name); err != nil {
			return nil, err
		}
		if payloadSize > math.MaxInt-(historyPinEntryHeaderSize+len(name)) {
			return nil, ErrHistoryPinLimit
		}
		payloadSize += historyPinEntryHeaderSize + len(name)
		if pins[name].DatabaseID != databaseID {
			return nil, errors.Join(ErrHistoryCursor, fmt.Errorf("kitdb: history pin %q belongs to database %q, want %q", name, pins[name].DatabaseID, databaseID))
		}
		names = append(names, name)
	}
	sort.Strings(names)
	encoded := make([]byte, historyPinsHeaderSize+payloadSize+historyPinsTrailerSize)
	copy(encoded[:8], historyPinsMagic)
	binary.LittleEndian.PutUint16(encoded[8:10], historyPinsFormatVersion)
	binary.LittleEndian.PutUint16(encoded[10:12], historyPinsHeaderSize)
	copy(encoded[16:32], identity[:])
	binary.LittleEndian.PutUint32(encoded[32:36], uint32(len(names)))
	binary.LittleEndian.PutUint32(encoded[36:40], uint32(payloadSize))
	offset := historyPinsHeaderSize
	for _, name := range names {
		cursor := pins[name]
		binary.LittleEndian.PutUint16(encoded[offset:offset+2], uint16(len(name)))
		binary.LittleEndian.PutUint32(encoded[offset+4:offset+8], cursor.Checksum)
		binary.LittleEndian.PutUint64(encoded[offset+8:offset+16], cursor.Transaction)
		offset += historyPinEntryHeaderSize
		copy(encoded[offset:offset+len(name)], name)
		offset += len(name)
	}
	binary.LittleEndian.PutUint32(encoded[offset:], crc32.Checksum(encoded[:offset], crc32cTable))
	return encoded, nil
}

// ValidateHistoryPinName validates one durable retention-pin identifier
// without reading or mutating a database.
func ValidateHistoryPinName(name string) error {
	if name == "" || len(name) > maxHistoryPinNameBytes || !utf8.ValidString(name) {
		return fmt.Errorf("kitdb: history pin name must be valid UTF-8 between 1 and %d bytes", maxHistoryPinNameBytes)
	}
	if strings.TrimSpace(name) != name {
		return fmt.Errorf("kitdb: history pin name cannot have surrounding whitespace")
	}
	for _, value := range name {
		if unicode.IsControl(value) {
			return fmt.Errorf("kitdb: history pin name cannot contain control characters")
		}
	}
	return nil
}

func publishHistoryControlFile(path, prefix string, encoded []byte) (published bool, err error) {
	temporary, err := os.CreateTemp(filepath.Dir(path), prefix)
	if err != nil {
		return false, fmt.Errorf("kitdb: create history control staging: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return false, fmt.Errorf("kitdb: set history control permissions: %w", err)
	}
	if _, err := writeAll(temporary, encoded); err != nil {
		return false, fmt.Errorf("kitdb: write history control staging: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return false, fmt.Errorf("kitdb: sync history control staging: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, fmt.Errorf("kitdb: close history control staging: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return false, fmt.Errorf("kitdb: publish history control file: %w", err)
	}
	published = true
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return true, fmt.Errorf("kitdb: sync history control directory: %w", err)
	}
	return true, nil
}

func cloneHistoryCursors(source map[string]HistoryCursor) map[string]HistoryCursor {
	cloned := make(map[string]HistoryCursor, len(source))
	for name, cursor := range source {
		cloned[name] = cursor
	}
	return cloned
}

// HistoryPins returns caller-owned durable retention pins sorted by name.
func (db *DB) HistoryPins() ([]HistoryPin, error) {
	db.historyMu.Lock()
	defer db.historyMu.Unlock()
	db.mu.RLock()
	defer db.mu.RUnlock()
	if err := db.stateErrorLocked(); err != nil {
		return nil, err
	}
	if db.history == nil {
		return nil, ErrHistoryDisabled
	}
	names := make([]string, 0, len(db.history.pins))
	for name := range db.history.pins {
		names = append(names, name)
	}
	sort.Strings(names)
	pins := make([]HistoryPin, 0, len(names))
	for _, name := range names {
		pins = append(pins, HistoryPin{Name: name, Cursor: db.history.pins[name]})
	}
	return pins, nil
}

// SetHistoryPin creates or moves one durable named retention boundary. The
// cursor must still be provable from the retained checksum chain.
func (db *DB) SetHistoryPin(ctx context.Context, name string, cursor HistoryCursor) (HistoryPin, error) {
	if ctx == nil {
		return HistoryPin{}, fmt.Errorf("kitdb: nil history pin context")
	}
	if err := ctx.Err(); err != nil {
		return HistoryPin{}, err
	}
	if err := ValidateHistoryPinName(name); err != nil {
		return HistoryPin{}, err
	}
	db.historyMu.Lock()
	defer db.historyMu.Unlock()

	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return HistoryPin{}, err
	}
	history := db.history
	identity := db.identity
	if history == nil {
		db.mu.RUnlock()
		return HistoryPin{}, ErrHistoryDisabled
	}
	if current, found := history.pins[name]; found && current == cursor {
		db.mu.RUnlock()
		return HistoryPin{Name: name, Cursor: cursor}, nil
	}
	pins := cloneHistoryCursors(history.pins)
	db.mu.RUnlock()

	if err := verifyHistoryCursor(ctx, history.path, identity, cursor); err != nil {
		return HistoryPin{}, err
	}
	pins[name] = cursor
	encoded, err := encodeHistoryPins(identity, pins)
	if err != nil {
		return HistoryPin{}, err
	}
	published, err := publishHistoryControlFile(
		filepath.Join(history.path, historyPinsFilename),
		".kitdb-history-pins-*.tmp",
		encoded,
	)
	if err != nil {
		if published {
			durabilityErr := errors.Join(ErrDurabilityUncertain, err)
			db.markUnavailable(durabilityErr)
			return HistoryPin{}, durabilityErr
		}
		return HistoryPin{}, err
	}
	db.mu.Lock()
	history.pins = pins
	db.mu.Unlock()
	return HistoryPin{Name: name, Cursor: cursor}, nil
}

// ReleaseHistoryPin durably removes one named retention boundary. Releasing a
// missing pin is idempotent.
func (db *DB) ReleaseHistoryPin(ctx context.Context, name string) error {
	if ctx == nil {
		return fmt.Errorf("kitdb: nil history pin context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateHistoryPinName(name); err != nil {
		return err
	}
	db.historyMu.Lock()
	defer db.historyMu.Unlock()

	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return err
	}
	history := db.history
	identity := db.identity
	if history == nil {
		db.mu.RUnlock()
		return ErrHistoryDisabled
	}
	if _, found := history.pins[name]; !found {
		db.mu.RUnlock()
		return nil
	}
	pins := cloneHistoryCursors(history.pins)
	delete(pins, name)
	db.mu.RUnlock()

	encoded, err := encodeHistoryPins(identity, pins)
	if err != nil {
		return err
	}
	published, err := publishHistoryControlFile(
		filepath.Join(history.path, historyPinsFilename),
		".kitdb-history-pins-*.tmp",
		encoded,
	)
	if err != nil {
		if published {
			durabilityErr := errors.Join(ErrDurabilityUncertain, err)
			db.markUnavailable(durabilityErr)
			return durabilityErr
		}
		return err
	}
	db.mu.Lock()
	history.pins = pins
	db.mu.Unlock()
	return nil
}

func verifyHistoryCursor(ctx context.Context, historyPath string, identity [16]byte, cursor HistoryCursor) error {
	databaseID := hex.EncodeToString(identity[:])
	if cursor.DatabaseID != databaseID {
		return errors.Join(ErrHistoryCursor, fmt.Errorf("kitdb: history cursor belongs to database %q, want %q", cursor.DatabaseID, databaseID))
	}
	metadataPath := filepath.Join(historyPath, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		return err
	}
	if metadata.identity != identity {
		return corruptFileAt(metadataPath, 16, "history belongs to a different database")
	}
	if cursor.Transaction < metadata.baseTx {
		return errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history cursor transaction %d precedes retained base %d", cursor.Transaction, metadata.baseTx))
	}
	if cursor.Transaction == metadata.baseTx {
		if cursor.Checksum != metadata.baseChecksum {
			return errors.Join(ErrHistoryCursor, fmt.Errorf("kitdb: history cursor checksum %08x does not match retained base %08x", cursor.Checksum, metadata.baseChecksum))
		}
		return nil
	}
	listed, err := listHistorySegments(historyPath)
	if err != nil {
		return err
	}
	segments, _, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return err
	}
	expectedTx := metadata.baseTx
	expectedChecksum := metadata.baseChecksum
	for _, segment := range segments {
		found := false
		actualChecksum := uint32(0)
		info, err := inspectHistorySegmentFrames(segment.path, historySegmentExpectation{
			identity: identity, baseTx: expectedTx, baseChecksum: expectedChecksum, verifyBaseChecksum: true,
			lastTx: segment.last, bytes: segment.bytes,
		}, func(transaction uint64, checksum uint32, _ int64, _ []operation) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if transaction == cursor.Transaction {
				found = true
				actualChecksum = checksum
			}
			return nil
		})
		if err != nil {
			return err
		}
		expectedTx = info.lastTx
		expectedChecksum = info.lastChecksum
		if cursor.Transaction <= info.lastTx {
			if !found || actualChecksum != cursor.Checksum {
				return errors.Join(ErrHistoryCursor, fmt.Errorf("kitdb: history cursor transaction %d checksum %08x is not retained", cursor.Transaction, cursor.Checksum))
			}
			return nil
		}
	}
	return errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history cursor transaction %d is newer than retained tail %d", cursor.Transaction, expectedTx))
}

// PruneHistory advances retained history through complete segments only. META
// is published before retired files are removed, so a crash can leave debris
// but cannot leave a missing active prefix.
func (db *DB) PruneHistory(ctx context.Context, through uint64) (HistoryPruneResult, error) {
	if ctx == nil {
		return HistoryPruneResult{}, fmt.Errorf("kitdb: nil history prune context")
	}
	if err := ctx.Err(); err != nil {
		return HistoryPruneResult{}, err
	}
	db.commitMu.Lock()
	defer db.commitMu.Unlock()
	db.historyMu.Lock()
	defer db.historyMu.Unlock()
	return db.pruneHistoryLocked(ctx, through)
}

// pruneHistoryLocked shares the one crash-safe history publication path with
// explicit pruning and policy enforcement. Callers hold commitMu then
// historyMu, preserving the database-wide durability lock order.
func (db *DB) pruneHistoryLocked(ctx context.Context, through uint64) (HistoryPruneResult, error) {
	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return HistoryPruneResult{}, err
	}
	history := db.history
	identity := db.identity
	if history == nil {
		db.mu.RUnlock()
		return HistoryPruneResult{}, ErrHistoryDisabled
	}
	pins := cloneHistoryCursors(history.pins)
	db.mu.RUnlock()

	metadataPath := filepath.Join(history.path, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		return HistoryPruneResult{}, err
	}
	if metadata.identity != identity {
		return HistoryPruneResult{}, corruptFileAt(metadataPath, 16, "history belongs to a different database")
	}
	result := HistoryPruneResult{
		DatabaseID:              hex.EncodeToString(identity[:]),
		RequestedThrough:        through,
		PreviousBaseTransaction: metadata.baseTx,
		BaseTransaction:         metadata.baseTx,
		BaseChecksum:            metadata.baseChecksum,
		baseCommitTime:          metadata.baseCommitTime,
	}
	if through <= metadata.baseTx {
		listed, err := listHistorySegments(history.path)
		if err != nil {
			return result, err
		}
		segments, retired, err := activeHistorySegments(metadata.baseTx, listed)
		if err != nil {
			return result, err
		}
		return db.finishHistoryPruneCleanup(ctx, history, result, segments, retired, 0)
	}
	if through > history.tailTx {
		return result, fmt.Errorf("kitdb: history prune transaction %d is newer than retained tail %d", through, history.tailTx)
	}
	if name, cursor, found := oldestHistoryPin(pins); found && through > cursor.Transaction {
		return result, errors.Join(ErrHistoryPinned, fmt.Errorf("kitdb: history pin %q at transaction %d blocks pruning through %d", name, cursor.Transaction, through))
	}

	listed, err := listHistorySegments(history.path)
	if err != nil {
		return result, err
	}
	segments, retired, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return result, err
	}
	pruneCount := 0
	pruneBytes := int64(0)
	for _, segment := range segments {
		if segment.last > through {
			break
		}
		pruneCount++
		pruneBytes += segment.bytes
	}
	if pruneCount != 0 {
		expectedTx := metadata.baseTx
		expectedChecksum := metadata.baseChecksum
		nextBaseCommitTime := metadata.baseCommitTime
		for index, segment := range segments[:pruneCount] {
			info, err := inspectHistorySegmentFrames(segment.path, historySegmentExpectation{
				identity: identity, baseTx: expectedTx, baseChecksum: expectedChecksum, verifyBaseChecksum: true,
				lastTx: segment.last, bytes: segment.bytes,
			}, func(_ uint64, _ uint32, _ int64, _ []operation) error {
				return ctx.Err()
			})
			if err != nil {
				return result, err
			}
			expectedTx = info.lastTx
			expectedChecksum = info.lastChecksum
			if index+1 == pruneCount {
				result.BaseTransaction = info.lastTx
				result.BaseChecksum = info.lastChecksum
				nextBaseCommitTime = info.lastCommitTime
				result.baseCommitTime = info.lastCommitTime
			}
		}
		if pruneCount < len(segments) {
			next := segments[pruneCount]
			nextBaseChecksum, err := readWALBaseChecksum(next.path, identity, expectedTx)
			if err != nil {
				return result, err
			}
			if nextBaseChecksum != expectedChecksum {
				return result, errors.Join(ErrHistoryGap, corruptFileAt(next.path, 40, "history base checksum is %08x, want %08x", nextBaseChecksum, expectedChecksum))
			}
		} else if expectedTx != history.tailTx || expectedChecksum != history.tailChecksum {
			return result, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: verified history tail %d/%08x does not match open state %d/%08x", expectedTx, expectedChecksum, history.tailTx, history.tailChecksum))
		}
		if metadata.baseCommitTime != 0 {
			_, anchorErr := VerifyBackupAnchor(ctx, historyBaseAnchorPath(history.path, metadata.baseTx))
			switch {
			case errors.Is(anchorErr, os.ErrNotExist):
				// A pre-timestamp history directory or a simulated legacy
				// post-META state has no recoverable base image. Preserve
				// transaction retention while explicitly dropping the time claim.
				nextBaseCommitTime = 0
				result.baseCommitTime = 0
			case anchorErr != nil:
				return result, anchorErr
			case nextBaseCommitTime == 0:
				return result, ErrRecoveryTimeUnavailable
			default:
				if err := prepareAdvancedHistoryBaseAnchor(
					ctx, history.path, metadata, result.BaseTransaction,
				); err != nil {
					return result, err
				}
			}
		}
		published, publishErr := publishHistoryControlFile(
			metadataPath,
			".kitdb-history-meta-*.tmp",
			encodeHistoryMetadata(historyMetadata{
				identity:       identity,
				baseTx:         result.BaseTransaction,
				baseChecksum:   result.BaseChecksum,
				baseCommitTime: nextBaseCommitTime,
			}),
		)
		if publishErr != nil {
			if published {
				durabilityErr := errors.Join(ErrDurabilityUncertain, publishErr)
				db.markUnavailable(durabilityErr)
				return result, durabilityErr
			}
			return result, publishErr
		}
		result.PrunedSegments = pruneCount
		result.PrunedBytes = pruneBytes
	}

	return db.finishHistoryPruneCleanup(ctx, history, result, segments, retired, pruneCount)
}

func (db *DB) finishHistoryPruneCleanup(
	ctx context.Context,
	history *historyState,
	result HistoryPruneResult,
	segments []historySegment,
	retired []historySegment,
	pruneCount int,
) (HistoryPruneResult, error) {
	toCleanup := make([]historySegment, 0, len(retired)+pruneCount)
	toCleanup = append(toCleanup, retired...)
	toCleanup = append(toCleanup, segments[:pruneCount]...)
	removedSegments, removedBytes, cleanupErr := cleanupHistorySegments(ctx, history.path, toCleanup)
	pendingSegments := len(toCleanup) - removedSegments
	pendingBytes := historySegmentsBytes(toCleanup) - removedBytes
	activeBytes := historySegmentsBytes(segments[pruneCount:])
	db.mu.Lock()
	history.baseTx = result.BaseTransaction
	history.baseChecksum = result.BaseChecksum
	history.baseCommitTime = result.baseCommitTime
	history.segments = len(segments) - pruneCount
	history.bytes = activeBytes
	history.retiredSegments = pendingSegments
	history.retiredBytes = pendingBytes
	db.mu.Unlock()
	result.CleanupPendingSegments = pendingSegments
	result.CleanupPendingBytes = pendingBytes
	if cleanupErr != nil {
		return result, cleanupErr
	}
	if err := cleanupHistoryBaseAnchors(ctx, history.path, result.BaseTransaction); err != nil {
		return result, err
	}
	return result, nil
}

func oldestHistoryPin(pins map[string]HistoryCursor) (string, HistoryCursor, bool) {
	name := ""
	var oldest HistoryCursor
	found := false
	for candidateName, cursor := range pins {
		if !found || cursor.Transaction < oldest.Transaction ||
			(cursor.Transaction == oldest.Transaction && candidateName < name) {
			name = candidateName
			oldest = cursor
			found = true
		}
	}
	return name, oldest, found
}

func historySegmentsBytes(segments []historySegment) int64 {
	total := int64(0)
	for _, segment := range segments {
		total += segment.bytes
	}
	return total
}

func cleanupHistorySegments(ctx context.Context, directory string, segments []historySegment) (int, int64, error) {
	removed := 0
	removedBytes := int64(0)
	for _, segment := range segments {
		if err := ctx.Err(); err != nil {
			return removed, removedBytes, err
		}
		err := os.Remove(segment.path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return removed, removedBytes, fmt.Errorf("kitdb: remove retired history segment %q: %w", segment.name, err)
		}
		removed++
		removedBytes += segment.bytes
	}
	if removed != 0 {
		if err := syncDirectory(directory); err != nil {
			return removed, removedBytes, fmt.Errorf("kitdb: sync pruned history directory: %w", err)
		}
	}
	return removed, removedBytes, nil
}
