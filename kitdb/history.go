package kitdb

import (
	"bytes"
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
	"strconv"
	"strings"
	"time"
)

const (
	historySuffix                            = ".history"
	historyMetadataFilename                  = "META"
	historySegmentSuffix                     = ".khist"
	historyMetadataSize                      = 64
	historyMetadataCRCAt                     = 60
	historyFormatVersion                     = 1
	historyMagic                             = "KITDBH01"
	maxHistorySegments                       = 1 << 16
	historyMetadataFlagBaseCommitTime uint32 = 1 << 0
)

type historyState struct {
	path            string
	baseTx          uint64
	baseChecksum    uint32
	baseCommitTime  int64
	tailTx          uint64
	tailChecksum    uint32
	tailCommitTime  int64
	segments        int
	bytes           int64
	retiredSegments int
	retiredBytes    int64
	pins            map[string]HistoryCursor
}

type historyMetadata struct {
	identity       [16]byte
	baseTx         uint64
	baseChecksum   uint32
	baseCommitTime int64
}

type historySegment struct {
	path  string
	name  string
	first uint64
	last  uint64
	bytes int64
}

type historySegmentInfo struct {
	baseTx         uint64
	baseChecksum   uint32
	lastTx         uint64
	lastChecksum   uint32
	lastCommitTime int64
	bytes          int64
}

type historySegmentExpectation struct {
	identity           [16]byte
	baseTx             uint64
	baseChecksum       uint32
	verifyBaseChecksum bool
	lastTx             uint64
	lastChecksum       uint32
	verifyLastChecksum bool
	bytes              int64
}

// HistoryRange describes the contiguous retained transaction interval. The
// base transaction is an exclusive anchor already represented by the database
// when retention started; events begin at BaseTransaction+1.
type HistoryRange struct {
	DatabaseID      string
	BaseTransaction uint64
	BaseChecksum    uint32
	LastTransaction uint64
	LastChecksum    uint32
	Segments        int
	Bytes           int64
}

// Cursor returns the verified inclusive boundary reached by this history walk.
func (history HistoryRange) Cursor() HistoryCursor {
	return HistoryCursor{
		DatabaseID:  history.DatabaseID,
		Transaction: history.LastTransaction,
		Checksum:    history.LastChecksum,
	}
}

// HistoryVisitor consumes one committed transaction. A consumer must advance
// its durable watermark only after WalkHistory returns nil.
type HistoryVisitor func(CommitEvent) error

type historyFrameVisitor func(transaction uint64, checksum uint32, committedAt int64, operations []operation) error

func databaseHistoryPath(databasePath string) string {
	return databasePath + historySuffix
}

// prepareHistory enables retention when requested or when a valid history
// directory already exists. This sticky behavior prevents an accidental open
// with default options from silently creating a permanent transaction gap.
func prepareHistory(databasePath string, main *mainImage, requested, replica bool, minimumBaseCommitTime int64) (*historyState, error) {
	path := databaseHistoryPath(databasePath)
	info, err := os.Stat(path)
	switch {
	case err == nil && !info.IsDir():
		return nil, fmt.Errorf("kitdb: history path %q is not a directory", path)
	case err == nil:
	case errors.Is(err, os.ErrNotExist) && !requested:
		return nil, nil
	case errors.Is(err, os.ErrNotExist):
		if err := os.Mkdir(path, 0o700); err != nil {
			return nil, fmt.Errorf("kitdb: create history directory: %w", err)
		}
		if err := syncDirectory(filepath.Dir(path)); err != nil {
			return nil, fmt.Errorf("kitdb: sync history parent directory: %w", err)
		}
	default:
		return nil, fmt.Errorf("kitdb: inspect history directory: %w", err)
	}

	metadataPath := filepath.Join(path, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if errors.Is(err, os.ErrNotExist) {
		segments, listErr := listHistorySegments(path)
		if listErr != nil {
			return nil, listErr
		}
		if len(segments) != 0 {
			return nil, corruptFileAt(metadataPath, 0, "history metadata is missing for %d segments", len(segments))
		}
		metadata = historyMetadata{
			identity: main.identity, baseTx: main.transaction, baseChecksum: main.boundaryChecksum,
		}
		if !replica {
			metadata.baseCommitTime = time.Now().UTC().UnixNano()
			if minimumBaseCommitTime > metadata.baseCommitTime {
				metadata.baseCommitTime = minimumBaseCommitTime
			}
		}
		if err := ensureHistoryBaseAnchor(context.Background(), path, main, metadata); err != nil {
			return nil, err
		}
		if err := createHistoryMetadata(metadataPath, metadata); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	if !bytes.Equal(metadata.identity[:], main.identity[:]) {
		return nil, corruptFileAt(metadataPath, 16, "history belongs to a different database")
	}
	if metadata.baseTx > main.transaction {
		return nil, corruptFileAt(metadataPath, 32, "history base transaction %d is newer than main transaction %d", metadata.baseTx, main.transaction)
	}

	listed, err := listHistorySegments(path)
	if err != nil {
		return nil, err
	}
	segments, retired, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return nil, err
	}
	tailTx, err := validateHistoryNames(metadata.baseTx, segments)
	if err != nil {
		return nil, err
	}
	if tailTx > main.transaction {
		return nil, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: retained history ends at transaction %d after main transaction %d", tailTx, main.transaction))
	}
	state := &historyState{
		path: path, baseTx: metadata.baseTx, baseChecksum: metadata.baseChecksum,
		baseCommitTime: metadata.baseCommitTime,
		tailTx:         metadata.baseTx, tailChecksum: metadata.baseChecksum,
		tailCommitTime: metadata.baseCommitTime,
		pins:           make(map[string]HistoryCursor),
	}
	for _, segment := range segments {
		if segment.bytes > math.MaxInt64-state.bytes {
			return nil, errors.Join(ErrHistoryLimit, fmt.Errorf("kitdb: retained history byte count overflows int64"))
		}
		state.segments++
		state.bytes += segment.bytes
	}
	for _, segment := range retired {
		if segment.bytes > math.MaxInt64-state.retiredBytes {
			return nil, errors.Join(ErrHistoryLimit, fmt.Errorf("kitdb: retired history byte count overflows int64"))
		}
		state.retiredSegments++
		state.retiredBytes += segment.bytes
	}
	if len(segments) != 0 {
		last := segments[len(segments)-1]
		info, err := inspectHistorySegment(last.path, historySegmentExpectation{
			identity: main.identity, baseTx: last.first - 1, lastTx: last.last, bytes: last.bytes,
		}, nil)
		if err != nil {
			return nil, err
		}
		state.tailTx = info.lastTx
		state.tailChecksum = info.lastChecksum
		state.tailCommitTime = info.lastCommitTime
	}
	pins, err := readHistoryPins(filepath.Join(path, historyPinsFilename), metadata.identity)
	if err != nil {
		return nil, err
	}
	for name, cursor := range pins {
		if cursor.Transaction < metadata.baseTx || cursor.Transaction > state.tailTx {
			return nil, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history pin %q at transaction %d is outside retained range %d..%d", name, cursor.Transaction, metadata.baseTx, state.tailTx))
		}
		if cursor.Transaction == metadata.baseTx && cursor.Checksum != metadata.baseChecksum {
			return nil, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history pin %q checksum does not match retained base", name))
		}
		state.pins[name] = cursor
	}
	return state, nil
}

func createHistoryMetadata(path string, metadata historyMetadata) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kitdb-history-meta-*.tmp")
	if err != nil {
		return fmt.Errorf("kitdb: create history metadata staging: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("kitdb: set history metadata permissions: %w", err)
	}
	encoded := encodeHistoryMetadata(metadata)
	if _, err := writeAll(temporary, encoded); err != nil {
		return fmt.Errorf("kitdb: write history metadata: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("kitdb: sync history metadata: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("kitdb: close history metadata staging: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("kitdb: publish history metadata: %w", err)
	}
	cleanup = false
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("kitdb: sync history metadata directory: %w", err)
	}
	return nil
}

func encodeHistoryMetadata(metadata historyMetadata) []byte {
	encoded := make([]byte, historyMetadataSize)
	copy(encoded[:8], historyMagic)
	binary.LittleEndian.PutUint16(encoded[8:10], historyFormatVersion)
	binary.LittleEndian.PutUint16(encoded[10:12], historyMetadataSize)
	if metadata.baseCommitTime != 0 {
		binary.LittleEndian.PutUint32(encoded[12:16], historyMetadataFlagBaseCommitTime)
	}
	copy(encoded[16:32], metadata.identity[:])
	binary.LittleEndian.PutUint64(encoded[32:40], metadata.baseTx)
	binary.LittleEndian.PutUint32(encoded[40:44], metadata.baseChecksum)
	if metadata.baseCommitTime != 0 {
		binary.LittleEndian.PutUint64(encoded[44:52], uint64(metadata.baseCommitTime))
	}
	binary.LittleEndian.PutUint32(encoded[historyMetadataCRCAt:], crc32.Checksum(encoded[:historyMetadataCRCAt], crc32cTable))
	return encoded
}

func readHistoryMetadata(path string) (historyMetadata, error) {
	var metadata historyMetadata
	file, err := os.Open(path)
	if err != nil {
		return metadata, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return metadata, fmt.Errorf("kitdb: stat history metadata: %w", err)
	}
	if !info.Mode().IsRegular() {
		return metadata, corruptFileAt(path, 0, "history metadata is not a regular file")
	}
	if info.Size() != historyMetadataSize {
		return metadata, corruptFileAt(path, 0, "history metadata size is %d, want %d", info.Size(), historyMetadataSize)
	}
	encoded := make([]byte, historyMetadataSize)
	if err := readAt(file, encoded, 0); err != nil {
		return metadata, fmt.Errorf("kitdb: read history metadata: %w", err)
	}
	if string(encoded[:8]) != historyMagic {
		return metadata, corruptFileAt(path, 0, "invalid history metadata magic")
	}
	if version := binary.LittleEndian.Uint16(encoded[8:10]); version != historyFormatVersion {
		return metadata, corruptFileAt(path, 8, "unsupported history metadata version %d", version)
	}
	if size := binary.LittleEndian.Uint16(encoded[10:12]); size != historyMetadataSize {
		return metadata, corruptFileAt(path, 10, "invalid history metadata header size %d", size)
	}
	flags := binary.LittleEndian.Uint32(encoded[12:16])
	if flags & ^historyMetadataFlagBaseCommitTime != 0 {
		return metadata, corruptFileAt(path, 12, "unsupported history metadata flags %d", flags)
	}
	reservedStart := 44
	if flags&historyMetadataFlagBaseCommitTime != 0 {
		reservedStart = 52
	}
	for offset := reservedStart; offset < historyMetadataCRCAt; offset++ {
		if encoded[offset] != 0 {
			return metadata, corruptFileAt(path, int64(offset), "unsupported history metadata reserved value")
		}
	}
	declared := binary.LittleEndian.Uint32(encoded[historyMetadataCRCAt:])
	if actual := crc32.Checksum(encoded[:historyMetadataCRCAt], crc32cTable); actual != declared {
		return metadata, corruptFileAt(path, historyMetadataCRCAt, "history metadata checksum mismatch")
	}
	copy(metadata.identity[:], encoded[16:32])
	metadata.baseTx = binary.LittleEndian.Uint64(encoded[32:40])
	metadata.baseChecksum = binary.LittleEndian.Uint32(encoded[40:44])
	if flags&historyMetadataFlagBaseCommitTime != 0 {
		metadata.baseCommitTime = int64(binary.LittleEndian.Uint64(encoded[44:52]))
		if metadata.baseCommitTime <= 0 {
			return historyMetadata{}, corruptFileAt(path, 44, "invalid history base commit timestamp %d", metadata.baseCommitTime)
		}
	}
	if metadata.baseTx == 0 && metadata.baseChecksum != 0 {
		return historyMetadata{}, corruptFileAt(path, 40, "empty history base has a transaction checksum")
	}
	return metadata, nil
}

func historySegmentName(first, last uint64) string {
	return fmt.Sprintf("%020d-%020d%s", first, last, historySegmentSuffix)
}

func parseHistorySegmentName(name string) (uint64, uint64, error) {
	if !strings.HasSuffix(name, historySegmentSuffix) {
		return 0, 0, fmt.Errorf("not a history segment")
	}
	base := strings.TrimSuffix(name, historySegmentSuffix)
	if len(base) != 41 || base[20] != '-' {
		return 0, 0, fmt.Errorf("invalid history segment name %q", name)
	}
	first, err := strconv.ParseUint(base[:20], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid history segment first transaction in %q", name)
	}
	last, err := strconv.ParseUint(base[21:], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid history segment last transaction in %q", name)
	}
	if first == 0 || last < first || historySegmentName(first, last) != name {
		return 0, 0, fmt.Errorf("invalid history segment range in %q", name)
	}
	return first, last, nil
}

func listHistorySegments(path string) ([]historySegment, error) {
	directory, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("kitdb: read history directory: %w", err)
	}
	defer directory.Close()
	segments := make([]historySegment, 0)
	for {
		entries, readErr := directory.ReadDir(256)
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasSuffix(name, historySegmentSuffix) {
				continue
			}
			if len(segments) == maxHistorySegments {
				return nil, ErrHistoryLimit
			}
			first, last, err := parseHistorySegmentName(name)
			if err != nil {
				return nil, corruptFileAt(filepath.Join(path, name), 0, "%v", err)
			}
			info, err := entry.Info()
			if err != nil {
				return nil, fmt.Errorf("kitdb: inspect history segment %q: %w", name, err)
			}
			if !info.Mode().IsRegular() {
				return nil, corruptFileAt(filepath.Join(path, name), 0, "history segment is not a regular file")
			}
			segments = append(segments, historySegment{
				path: filepath.Join(path, name), name: name, first: first, last: last, bytes: info.Size(),
			})
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("kitdb: read history directory entries: %w", readErr)
		}
	}
	sort.Slice(segments, func(left, right int) bool {
		if segments[left].first == segments[right].first {
			return segments[left].last < segments[right].last
		}
		return segments[left].first < segments[right].first
	})
	return segments, nil
}

func validateHistoryNames(base uint64, segments []historySegment) (uint64, error) {
	last := base
	for _, segment := range segments {
		if last == math.MaxUint64 || segment.first != last+1 {
			return last, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q starts at %d after %d", segment.name, segment.first, last))
		}
		last = segment.last
	}
	return last, nil
}

// activeHistorySegments permits fully retired prefix files after a crash or a
// failed cleanup. META is authoritative; a segment may never straddle its base.
func activeHistorySegments(base uint64, segments []historySegment) ([]historySegment, []historySegment, error) {
	active := make([]historySegment, 0, len(segments))
	retired := make([]historySegment, 0)
	for _, segment := range segments {
		switch {
		case segment.last <= base:
			retired = append(retired, segment)
		case segment.first <= base:
			return nil, nil, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q straddles retained base %d", segment.name, base))
		default:
			active = append(active, segment)
		}
	}
	if _, err := validateHistoryNames(base, active); err != nil {
		return nil, nil, err
	}
	return active, retired, nil
}

// sealHistoryLocked requires commitMu. It archives the exact durable WAL bytes
// only after the matching main transaction boundary has been published.
func (db *DB) sealHistoryLocked(walEnd int64, baseTx, lastTx uint64, lastChecksum uint32, lastCommitTime int64) error {
	if db.history == nil {
		return nil
	}
	published, bytes, err := sealHistoryWAL(
		db.history,
		databaseWALPath(db.path),
		db.identity,
		walEnd,
		baseTx,
		lastTx,
		lastChecksum,
	)
	if err != nil {
		return err
	}
	db.mu.Lock()
	if published {
		db.history.segments++
		db.history.bytes += bytes
		db.history.tailTx = lastTx
		db.history.tailChecksum = lastChecksum
		db.history.tailCommitTime = lastCommitTime
	}
	db.mu.Unlock()
	return nil
}

func sealHistoryWAL(state *historyState, sourcePath string, identity [16]byte, sourceSize int64, baseTx, lastTx uint64, lastChecksum uint32) (bool, int64, error) {
	if lastTx <= state.baseTx {
		return false, 0, nil
	}
	if baseTx < state.baseTx || lastTx <= baseTx || baseTx == math.MaxUint64 {
		return false, 0, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: WAL range %d..%d does not follow history base %d", baseTx, lastTx, state.baseTx))
	}
	sourceBaseChecksum, err := readWALBaseChecksum(sourcePath, identity, baseTx)
	if err != nil {
		return false, 0, err
	}

	name := historySegmentName(baseTx+1, lastTx)
	destination := filepath.Join(state.path, name)
	if _, err := os.Stat(destination); err == nil {
		expected := historySegmentExpectation{
			identity: identity, baseTx: baseTx, baseChecksum: sourceBaseChecksum, verifyBaseChecksum: true,
			lastTx: lastTx, lastChecksum: lastChecksum, verifyLastChecksum: true, bytes: sourceSize,
		}
		if _, err := inspectHistorySegment(destination, expected, nil); err != nil {
			return false, 0, err
		}
		if err := syncDirectory(state.path); err != nil {
			return false, 0, fmt.Errorf("kitdb: sync existing history directory: %w", err)
		}
		switch {
		case state.tailTx == lastTx && state.tailChecksum == lastChecksum:
			return false, 0, nil
		case state.tailTx == baseTx && state.tailChecksum == sourceBaseChecksum:
			return true, sourceSize, nil
		default:
			return false, 0, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: retained history state ends at transaction %d checksum %08x before existing segment %q", state.tailTx, state.tailChecksum, name))
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, 0, fmt.Errorf("kitdb: inspect retained history segment: %w", err)
	}
	if state.tailTx != baseTx || state.tailChecksum != sourceBaseChecksum {
		return false, 0, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: retained history ends at transaction %d checksum %08x, WAL starts at %d", state.tailTx, state.tailChecksum, baseTx))
	}
	physicalBytes := state.bytes
	if state.retiredBytes > math.MaxInt64-physicalBytes {
		return false, 0, ErrHistoryLimit
	}
	physicalBytes += state.retiredBytes
	if state.segments+state.retiredSegments >= maxHistorySegments || sourceSize > math.MaxInt64-physicalBytes {
		return false, 0, ErrHistoryLimit
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return false, 0, fmt.Errorf("kitdb: open WAL for history retention: %w", err)
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return false, 0, fmt.Errorf("kitdb: stat WAL for history retention: %w", err)
	}
	if info.Size() != sourceSize {
		return false, 0, fmt.Errorf("kitdb: WAL size changed from %d to %d during history retention", sourceSize, info.Size())
	}

	temporary, err := os.CreateTemp(state.path, ".kitdb-history-segment-*.tmp")
	if err != nil {
		return false, 0, fmt.Errorf("kitdb: create history segment staging: %w", err)
	}
	temporaryPath := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return false, 0, fmt.Errorf("kitdb: set history segment permissions: %w", err)
	}
	if _, err := io.CopyN(temporary, source, sourceSize); err != nil {
		return false, 0, fmt.Errorf("kitdb: copy WAL into history segment: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return false, 0, fmt.Errorf("kitdb: sync history segment: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return false, 0, fmt.Errorf("kitdb: close history segment staging: %w", err)
	}
	expected := historySegmentExpectation{
		identity: identity, baseTx: baseTx, baseChecksum: sourceBaseChecksum, verifyBaseChecksum: true,
		lastTx: lastTx, lastChecksum: lastChecksum, verifyLastChecksum: true, bytes: sourceSize,
	}
	if _, err := inspectHistorySegment(temporaryPath, expected, nil); err != nil {
		return false, 0, err
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return false, 0, fmt.Errorf("kitdb: publish history segment: %w", err)
	}
	cleanup = false
	if err := syncDirectory(state.path); err != nil {
		return false, 0, fmt.Errorf("kitdb: sync history directory: %w", err)
	}
	return true, sourceSize, nil
}

func readWALBaseChecksum(path string, identity [16]byte, baseTx uint64) (uint32, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("kitdb: open WAL history base: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, fmt.Errorf("kitdb: stat WAL history base: %w", err)
	}
	header, err := readWALHeader(path, file, info.Size())
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(header.identity[:], identity[:]) {
		return 0, corruptFileAt(path, 16, "WAL history base belongs to a different database")
	}
	if header.baseTx != baseTx {
		return 0, errors.Join(ErrHistoryGap, corruptFileAt(path, 32, "WAL history base is %d, want %d", header.baseTx, baseTx))
	}
	return header.baseChecksum, nil
}

func inspectHistorySegment(path string, expected historySegmentExpectation, visit func(uint64, []operation) error) (historySegmentInfo, error) {
	var frameVisitor historyFrameVisitor
	if visit != nil {
		frameVisitor = func(transaction uint64, _ uint32, _ int64, operations []operation) error {
			return visit(transaction, operations)
		}
	}
	return inspectHistorySegmentFrames(path, expected, frameVisitor)
}

func inspectHistorySegmentFrames(path string, expected historySegmentExpectation, visit historyFrameVisitor) (historySegmentInfo, error) {
	var result historySegmentInfo
	file, err := os.Open(path)
	if err != nil {
		return result, fmt.Errorf("kitdb: open history segment: %w", err)
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return result, fmt.Errorf("kitdb: stat history segment: %w", err)
	}
	if !stat.Mode().IsRegular() {
		return result, corruptFileAt(path, 0, "history segment is not a regular file")
	}
	if expected.bytes >= 0 && stat.Size() != expected.bytes {
		return result, corruptFileAt(path, 0, "history segment size is %d, want %d", stat.Size(), expected.bytes)
	}
	header, err := readWALHeader(path, file, stat.Size())
	if err != nil {
		return result, err
	}
	if !bytes.Equal(header.identity[:], expected.identity[:]) {
		return result, corruptFileAt(path, 16, "history segment belongs to a different database")
	}
	if header.baseTx != expected.baseTx {
		return result, errors.Join(ErrHistoryGap, corruptFileAt(path, 32, "history segment base transaction is %d, want %d", header.baseTx, expected.baseTx))
	}
	if expected.verifyBaseChecksum && header.baseChecksum != expected.baseChecksum {
		return result, errors.Join(ErrHistoryGap, corruptFileAt(path, 40, "history base checksum is %08x, want %08x", header.baseChecksum, expected.baseChecksum))
	}
	lastTx, lastChecksum, lastCommitTime, err := walkCompleteWALFrames(path, file, stat.Size(), header, visit)
	if err != nil {
		return result, err
	}
	if lastTx == header.baseTx {
		return result, corruptFileAt(path, walHeaderSize, "history segment contains no transaction frames")
	}
	if lastTx != expected.lastTx {
		return result, errors.Join(ErrHistoryGap, corruptFileAt(path, walHeaderSize, "history segment ends at transaction %d, want %d", lastTx, expected.lastTx))
	}
	if expected.verifyLastChecksum && lastChecksum != expected.lastChecksum {
		return result, corruptFileAt(path, stat.Size()-frameTrailerSize+16, "history boundary checksum is %08x, want %08x", lastChecksum, expected.lastChecksum)
	}
	return historySegmentInfo{
		baseTx: header.baseTx, baseChecksum: header.baseChecksum,
		lastTx: lastTx, lastChecksum: lastChecksum, lastCommitTime: lastCommitTime,
		bytes: stat.Size(),
	}, nil
}

func walkCompleteWAL(path string, file *os.File, size int64, header walHeader, visit func(uint64, []operation) error) (uint64, uint32, error) {
	var frameVisitor historyFrameVisitor
	if visit != nil {
		frameVisitor = func(transaction uint64, _ uint32, _ int64, operations []operation) error {
			return visit(transaction, operations)
		}
	}
	lastTx, lastChecksum, _, err := walkCompleteWALFrames(path, file, size, header, frameVisitor)
	return lastTx, lastChecksum, err
}

func walkCompleteWALFrames(path string, file *os.File, size int64, header walHeader, visit historyFrameVisitor) (uint64, uint32, int64, error) {
	lastTx := header.baseTx
	lastChecksum := header.baseChecksum
	lastCommitTime := int64(0)
	for offset := int64(walHeaderSize); offset < size; {
		remaining := size - offset
		if remaining < frameHeaderSize {
			return 0, 0, 0, corruptFileAt(path, offset, "history frame header is truncated")
		}
		frameHeader := make([]byte, frameHeaderSize)
		if err := readAt(file, frameHeader, offset); err != nil {
			return 0, 0, 0, fmt.Errorf("kitdb: read history frame header at %d: %w", offset, err)
		}
		transaction, payloadSize, flags, err := decodeFrameHeaderFor(path, offset, frameHeader, lastTx)
		if err != nil {
			return 0, 0, 0, err
		}
		frameSize := int64(frameHeaderSize) + int64(payloadSize) + int64(frameTrailerSize)
		if frameSize > remaining {
			return 0, 0, 0, corruptFileAt(path, offset, "history transaction %d is truncated", transaction)
		}
		body := make([]byte, int(payloadSize)+frameTrailerSize)
		if err := readAt(file, body, offset+frameHeaderSize); err != nil {
			return 0, 0, 0, fmt.Errorf("kitdb: read history frame body at %d: %w", offset, err)
		}
		payload := body[:int(payloadSize)]
		trailer := body[int(payloadSize):]
		if err := validateTrailerFor(path, offset, frameHeader, payload, trailer, transaction, uint32(frameSize)); err != nil {
			return 0, 0, 0, err
		}
		operations, committedAt, err := decodeFramePayload(payload, flags)
		if err != nil {
			return 0, 0, 0, corruptFileAt(path, offset+frameHeaderSize, "invalid transaction payload: %v", err)
		}
		if committedAt != 0 && lastCommitTime != 0 && committedAt <= lastCommitTime {
			return 0, 0, 0, corruptFileAt(path, offset+frameHeaderSize+payloadPrefixSize, "commit timestamp %d does not follow %d", committedAt, lastCommitTime)
		}
		checksum := binary.LittleEndian.Uint32(trailer[16:20])
		if visit != nil {
			if err := visit(transaction, checksum, committedAt, operations); err != nil {
				return 0, 0, 0, err
			}
		}
		lastTx = transaction
		lastChecksum = checksum
		if committedAt != 0 {
			lastCommitTime = committedAt
		}
		offset += frameSize
	}
	return lastTx, lastChecksum, lastCommitTime, nil
}

// WalkHistory establishes a fixed durable boundary with a checkpoint, then
// streams retained transactions after the exclusive after transaction through
// that boundary. Commits published after the boundary are not included.
func (db *DB) WalkHistory(ctx context.Context, after uint64, visit HistoryVisitor) (HistoryRange, error) {
	if ctx == nil {
		return HistoryRange{}, fmt.Errorf("kitdb: nil history context")
	}
	if visit == nil {
		return HistoryRange{}, fmt.Errorf("kitdb: nil history visitor")
	}
	if err := ctx.Err(); err != nil {
		return HistoryRange{}, err
	}
	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return HistoryRange{}, err
	}
	history := db.history
	identity := db.identity
	db.mu.RUnlock()
	if history == nil {
		return HistoryRange{}, ErrHistoryDisabled
	}
	boundary, err := db.Checkpoint()
	if err != nil {
		return HistoryRange{}, err
	}
	metadata, err := readHistoryMetadata(filepath.Join(history.path, historyMetadataFilename))
	if err != nil {
		return HistoryRange{}, err
	}
	if !bytes.Equal(metadata.identity[:], identity[:]) {
		return HistoryRange{}, corruptFileAt(filepath.Join(history.path, historyMetadataFilename), 16, "history belongs to a different database")
	}
	rangeInfo := HistoryRange{
		DatabaseID:      hex.EncodeToString(identity[:]),
		BaseTransaction: metadata.baseTx,
		BaseChecksum:    metadata.baseChecksum,
		LastTransaction: boundary,
	}
	if after < metadata.baseTx {
		return rangeInfo, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: requested transaction %d precedes retained base %d", after, metadata.baseTx))
	}
	if after > boundary {
		return rangeInfo, fmt.Errorf("kitdb: requested transaction %d is newer than history boundary %d", after, boundary)
	}
	if err := ctx.Err(); err != nil {
		return rangeInfo, err
	}

	listed, err := listHistorySegments(history.path)
	if err != nil {
		return rangeInfo, err
	}
	segments, _, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return rangeInfo, err
	}
	expectedTx := metadata.baseTx
	expectedChecksum := metadata.baseChecksum
	for _, segment := range segments {
		if segment.first > boundary {
			break
		}
		if expectedTx == math.MaxUint64 || segment.first != expectedTx+1 {
			return rangeInfo, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: history segment %q starts after transaction %d", segment.name, expectedTx))
		}
		if segment.last > boundary {
			return rangeInfo, corruptFileAt(segment.path, 0, "history segment crosses fixed boundary %d", boundary)
		}
		info, err := inspectHistorySegmentFrames(
			segment.path,
			historySegmentExpectation{
				identity: identity, baseTx: expectedTx, baseChecksum: expectedChecksum, verifyBaseChecksum: true,
				lastTx: segment.last, bytes: segment.bytes,
			},
			func(transaction uint64, checksum uint32, committedAt int64, operations []operation) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if transaction <= after {
					return nil
				}
				return visit(CommitEvent{
					Transaction: transaction,
					Checksum:    checksum,
					CommittedAt: commitTimeValue(committedAt),
					Operations:  cloneCommitOperations(operations),
				})
			},
		)
		if err != nil {
			return rangeInfo, err
		}
		expectedTx = info.lastTx
		expectedChecksum = info.lastChecksum
		rangeInfo.Segments++
		if info.bytes > math.MaxInt64-rangeInfo.Bytes {
			return rangeInfo, ErrHistoryLimit
		}
		rangeInfo.Bytes += info.bytes
	}
	if expectedTx != boundary {
		return rangeInfo, errors.Join(ErrHistoryGap, fmt.Errorf("kitdb: retained history ends at transaction %d before boundary %d", expectedTx, boundary))
	}
	rangeInfo.LastChecksum = expectedChecksum
	return rangeInfo, nil
}

// walkReplicaHistory proves both exact cursors and streams only their ordered
// interior. Unlike WalkHistory it never chooses a newer boundary itself.
func (db *DB) walkReplicaHistory(
	ctx context.Context,
	from HistoryCursor,
	boundary HistoryCursor,
	visit HistoryVisitor,
) (HistoryRange, error) {
	if ctx == nil {
		return HistoryRange{}, fmt.Errorf("kitdb: nil replica history context")
	}
	if visit == nil {
		return HistoryRange{}, fmt.Errorf("kitdb: nil replica history visitor")
	}
	if err := ctx.Err(); err != nil {
		return HistoryRange{}, err
	}
	db.mu.RLock()
	if err := db.stateErrorLocked(); err != nil {
		db.mu.RUnlock()
		return HistoryRange{}, err
	}
	history := db.history
	identity := db.identity
	db.mu.RUnlock()
	if history == nil {
		return HistoryRange{}, ErrHistoryDisabled
	}
	databaseID := hex.EncodeToString(identity[:])
	if from.DatabaseID != databaseID || boundary.DatabaseID != databaseID {
		return HistoryRange{}, errors.Join(
			ErrHistoryCursor,
			fmt.Errorf("kitdb: replica cursors do not belong to database %q", databaseID),
		)
	}
	if from.Transaction > boundary.Transaction {
		return HistoryRange{}, errors.Join(
			ErrHistoryCursor,
			fmt.Errorf("kitdb: replica cursor %d is newer than source boundary %d", from.Transaction, boundary.Transaction),
		)
	}

	metadataPath := filepath.Join(history.path, historyMetadataFilename)
	metadata, err := readHistoryMetadata(metadataPath)
	if err != nil {
		return HistoryRange{}, err
	}
	if !bytes.Equal(metadata.identity[:], identity[:]) {
		return HistoryRange{}, corruptFileAt(metadataPath, 16, "history belongs to a different database")
	}
	rangeInfo := HistoryRange{
		DatabaseID:      databaseID,
		BaseTransaction: metadata.baseTx,
		BaseChecksum:    metadata.baseChecksum,
		LastTransaction: boundary.Transaction,
		LastChecksum:    boundary.Checksum,
	}
	if from.Transaction < metadata.baseTx {
		return rangeInfo, errors.Join(
			ErrHistoryGap,
			fmt.Errorf("kitdb: replica cursor %d precedes retained base %d", from.Transaction, metadata.baseTx),
		)
	}
	if boundary.Transaction < metadata.baseTx {
		return rangeInfo, errors.Join(
			ErrHistoryGap,
			fmt.Errorf("kitdb: replica boundary %d precedes retained base %d", boundary.Transaction, metadata.baseTx),
		)
	}

	listed, err := listHistorySegments(history.path)
	if err != nil {
		return rangeInfo, err
	}
	segments, _, err := activeHistorySegments(metadata.baseTx, listed)
	if err != nil {
		return rangeInfo, err
	}
	foundFrom := false
	if from.Transaction == metadata.baseTx {
		if from.Checksum != metadata.baseChecksum {
			return rangeInfo, errors.Join(
				ErrHistoryCursor,
				fmt.Errorf("kitdb: replica cursor checksum %08x does not match retained base %08x", from.Checksum, metadata.baseChecksum),
			)
		}
		foundFrom = true
	}
	expectedTx := metadata.baseTx
	expectedChecksum := metadata.baseChecksum
	for _, segment := range segments {
		if segment.first > boundary.Transaction {
			break
		}
		if expectedTx == math.MaxUint64 || segment.first != expectedTx+1 {
			return rangeInfo, errors.Join(
				ErrHistoryGap,
				fmt.Errorf("kitdb: history segment %q starts after transaction %d", segment.name, expectedTx),
			)
		}
		if segment.last > boundary.Transaction {
			return rangeInfo, errors.Join(
				ErrHistoryCursor,
				fmt.Errorf("kitdb: replica boundary %d is not a sealed history boundary", boundary.Transaction),
			)
		}
		info, err := inspectHistorySegmentFrames(
			segment.path,
			historySegmentExpectation{
				identity: identity, baseTx: expectedTx, baseChecksum: expectedChecksum, verifyBaseChecksum: true,
				lastTx: segment.last, bytes: segment.bytes,
			},
			func(transaction uint64, checksum uint32, committedAt int64, operations []operation) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if transaction == from.Transaction {
					if checksum != from.Checksum {
						return errors.Join(
							ErrHistoryCursor,
							fmt.Errorf("kitdb: replica cursor transaction %d checksum %08x is not retained", from.Transaction, from.Checksum),
						)
					}
					foundFrom = true
				}
				if transaction <= from.Transaction {
					return nil
				}
				return visit(CommitEvent{
					Transaction: transaction,
					Checksum:    checksum,
					CommittedAt: commitTimeValue(committedAt),
					Operations:  cloneCommitOperations(operations),
				})
			},
		)
		if err != nil {
			return rangeInfo, err
		}
		expectedTx = info.lastTx
		expectedChecksum = info.lastChecksum
		rangeInfo.Segments++
		if info.bytes > math.MaxInt64-rangeInfo.Bytes {
			return rangeInfo, ErrHistoryLimit
		}
		rangeInfo.Bytes += info.bytes
	}
	if !foundFrom {
		return rangeInfo, errors.Join(
			ErrHistoryGap,
			fmt.Errorf("kitdb: replica cursor transaction %d is not retained", from.Transaction),
		)
	}
	if expectedTx != boundary.Transaction {
		return rangeInfo, errors.Join(
			ErrHistoryGap,
			fmt.Errorf("kitdb: retained history ends at transaction %d before replica boundary %d", expectedTx, boundary.Transaction),
		)
	}
	if expectedChecksum != boundary.Checksum {
		return rangeInfo, errors.Join(
			ErrHistoryCursor,
			fmt.Errorf("kitdb: replica boundary checksum %08x does not match retained checksum %08x", boundary.Checksum, expectedChecksum),
		)
	}
	return rangeInfo, nil
}
