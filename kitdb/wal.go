package kitdb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

const (
	walFilename = "WAL"
	walSuffix   = ".wal"

	walHeaderSize       = 64
	walHeaderChecksumAt = 60
	frameHeaderSize     = 32
	frameTrailerSize    = 24
	payloadPrefixSize   = 4
	timedPayloadPrefixSize = payloadPrefixSize + 8
	operationHeaderSize = 9

	walFormatVersion   = 1
	frameFormatVersion = 1

	maxKeySize     = 64 << 10
	maxValueSize   = 32 << 20
	maxPayloadSize = 64 << 20
	maxOperations  = 1 << 18
)

const frameFlagCommittedAt uint32 = 1 << 0

const (
	walMagic    = "KITDBW02"
	frameMagic  = "KITDBTX1"
	commitMagic = "KITDBCMT"
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

type walHeader struct {
	identity     [16]byte
	baseTx       uint64
	baseChecksum uint32
}

type walRecovery struct {
	file        *os.File
	identity    [16]byte
	overlay     map[string]rowMutation
	lastTx      uint64
	walEnd      int64
	walChecksum uint32
	walBaseTx   uint64
	lastCommitTime int64
}

func databaseWALPath(databasePath string) string {
	return databasePath + walSuffix
}

func openAndRecoverWAL(databasePath string, main *mainImage, history *historyState) (*walRecovery, error) {
	path := databaseWALPath(databasePath)
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if errors.Is(err, os.ErrNotExist) {
		if err := createWAL(path, main.identity, main.transaction, main.boundaryChecksum); err != nil {
			return nil, err
		}
		file, err = os.OpenFile(path, os.O_RDWR, 0o600)
	}
	if err != nil {
		return nil, fmt.Errorf("kitdb: open WAL: %w", err)
	}
	fail := func(err error) (*walRecovery, error) {
		_ = file.Close()
		return nil, err
	}

	info, err := file.Stat()
	if err != nil {
		return fail(fmt.Errorf("kitdb: stat WAL: %w", err))
	}
	header, err := readWALHeader(path, file, info.Size())
	if err != nil {
		return fail(err)
	}
	if !bytes.Equal(header.identity[:], main.identity[:]) {
		return fail(corruptFileAt(path, 16, "WAL belongs to a different database"))
	}
	if header.baseTx > main.transaction {
		return fail(corruptFileAt(path, 32, "WAL base transaction %d is newer than main transaction %d", header.baseTx, main.transaction))
	}
	if header.baseTx == main.transaction && header.baseChecksum != main.boundaryChecksum {
		return fail(corruptFileAt(path, 40, "WAL base checksum does not match the main snapshot"))
	}
	overlay := make(map[string]rowMutation)

	baseCommitTime := int64(0)
	if history != nil && history.tailTx == header.baseTx {
		baseCommitTime = history.tailCommitTime
	}
	lastTx, validSize, walChecksum, lastCommitTime, truncated, err := replayWAL(
		file,
		info.Size(),
		overlay,
		walHeaderSize,
		header.baseTx,
		header.baseChecksum,
		main.transaction,
		main.boundaryChecksum,
		baseCommitTime,
	)
	if err != nil {
		return fail(err)
	}
	if truncated {
		if err := file.Truncate(validSize); err != nil {
			return fail(fmt.Errorf("kitdb: truncate incomplete WAL tail: %w", err))
		}
		if err := file.Sync(); err != nil {
			return fail(fmt.Errorf("kitdb: sync recovered WAL: %w", err))
		}
	}

	// A crash can publish the main file immediately before WAL rotation. Once
	// recovery has proved the old WAL through that boundary, finish the
	// rotation before admitting new commits.
	if header.baseTx < main.transaction && lastTx == main.transaction {
		if history != nil {
			published, bytes, err := sealHistoryWAL(
				history,
				path,
				main.identity,
				validSize,
				header.baseTx,
				lastTx,
				walChecksum,
			)
			if err != nil {
				return fail(err)
			}
			if published {
				history.segments++
				history.bytes += bytes
				history.tailTx = lastTx
				history.tailChecksum = walChecksum
				history.tailCommitTime = lastCommitTime
			}
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("kitdb: close stale WAL: %w", err)
		}
		if err := createWAL(path, main.identity, main.transaction, main.boundaryChecksum); err != nil {
			return nil, err
		}
		file, err = os.OpenFile(path, os.O_RDWR, 0o600)
		if err != nil {
			return nil, fmt.Errorf("kitdb: reopen rotated WAL: %w", err)
		}
		header.baseTx = main.transaction
		header.baseChecksum = main.boundaryChecksum
		validSize = walHeaderSize
		walChecksum = main.boundaryChecksum
	}
	if history != nil {
		historyAtMain := history.tailTx == main.transaction && history.tailChecksum == main.boundaryChecksum
		walBridgesMain := header.baseTx == history.tailTx &&
			header.baseChecksum == history.tailChecksum &&
			header.baseTx < main.transaction &&
			lastTx > main.transaction
		if !historyAtMain && !walBridgesMain {
			return fail(errors.Join(
				ErrHistoryGap,
				fmt.Errorf(
					"kitdb: retained history ends at transaction %d checksum %08x without a WAL bridge to main transaction %d checksum %08x",
					history.tailTx,
					history.tailChecksum,
					main.transaction,
					main.boundaryChecksum,
				),
			))
		}
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return fail(fmt.Errorf("kitdb: seek recovered WAL: %w", err))
	}
	return &walRecovery{
		file: file, identity: main.identity, overlay: overlay,
		lastTx: lastTx, walEnd: validSize, walChecksum: walChecksum,
		walBaseTx: header.baseTx, lastCommitTime: lastCommitTime,
	}, nil
}

func createWAL(path string, identity [16]byte, baseTx uint64, baseChecksum uint32) error {
	stagingPath, err := prepareWAL(path, identity, baseTx, baseChecksum)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(stagingPath)
		}
	}()
	if err := replaceFile(stagingPath, path); err != nil {
		return fmt.Errorf("kitdb: publish WAL: %w", err)
	}
	published = true
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		return fmt.Errorf("kitdb: sync WAL directory entry: %w", err)
	}
	return nil
}

func prepareWAL(path string, identity [16]byte, baseTx uint64, baseChecksum uint32) (string, error) {
	if baseTx == 0 && baseChecksum != 0 {
		return "", fmt.Errorf("kitdb: empty WAL base has a transaction checksum")
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".kitdb-wal-*.tmp")
	if err != nil {
		return "", fmt.Errorf("kitdb: create WAL staging: %w", err)
	}
	stagingPath := file.Name()
	cleanup := true
	defer func() {
		_ = file.Close()
		if cleanup {
			_ = os.Remove(stagingPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return "", fmt.Errorf("kitdb: set WAL permissions: %w", err)
	}
	header := encodeWALHeader(identity, baseTx, baseChecksum)
	if _, err := writeAll(file, header); err != nil {
		return "", fmt.Errorf("kitdb: write WAL header: %w", err)
	}
	if err := file.Sync(); err != nil {
		return "", fmt.Errorf("kitdb: sync WAL header: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("kitdb: close WAL staging: %w", err)
	}
	cleanup = false
	return stagingPath, nil
}

func encodeWALHeader(identity [16]byte, baseTx uint64, baseChecksum uint32) []byte {
	header := make([]byte, walHeaderSize)
	copy(header[:8], walMagic)
	binary.LittleEndian.PutUint16(header[8:10], walFormatVersion)
	binary.LittleEndian.PutUint16(header[10:12], walHeaderSize)
	copy(header[16:32], identity[:])
	binary.LittleEndian.PutUint64(header[32:40], baseTx)
	binary.LittleEndian.PutUint32(header[40:44], baseChecksum)
	binary.LittleEndian.PutUint32(header[walHeaderChecksumAt:], crc32.Checksum(header[:walHeaderChecksumAt], crc32cTable))
	return header
}

func readWALHeader(path string, file *os.File, size int64) (walHeader, error) {
	var decoded walHeader
	if size < walHeaderSize {
		return decoded, corruptFileAt(path, 0, "incomplete WAL header")
	}
	header := make([]byte, walHeaderSize)
	if err := readAt(file, header, 0); err != nil {
		return decoded, fmt.Errorf("kitdb: read WAL header: %w", err)
	}
	if string(header[:8]) != walMagic {
		return decoded, corruptFileAt(path, 0, "invalid WAL magic")
	}
	if version := binary.LittleEndian.Uint16(header[8:10]); version != walFormatVersion {
		return decoded, corruptFileAt(path, 8, "unsupported WAL version %d", version)
	}
	if headerSize := binary.LittleEndian.Uint16(header[10:12]); headerSize != walHeaderSize {
		return decoded, corruptFileAt(path, 10, "invalid WAL header size %d", headerSize)
	}
	if flags := binary.LittleEndian.Uint32(header[12:16]); flags != 0 {
		return decoded, corruptFileAt(path, 12, "unsupported WAL flags %d", flags)
	}
	for offset := 44; offset < walHeaderChecksumAt; offset++ {
		if header[offset] != 0 {
			return decoded, corruptFileAt(path, int64(offset), "unsupported WAL reserved value")
		}
	}
	declared := binary.LittleEndian.Uint32(header[walHeaderChecksumAt:])
	if actual := crc32.Checksum(header[:walHeaderChecksumAt], crc32cTable); declared != actual {
		return decoded, corruptFileAt(path, walHeaderChecksumAt, "WAL header checksum mismatch")
	}
	copy(decoded.identity[:], header[16:32])
	decoded.baseTx = binary.LittleEndian.Uint64(header[32:40])
	decoded.baseChecksum = binary.LittleEndian.Uint32(header[40:44])
	if decoded.baseTx == 0 && decoded.baseChecksum != 0 {
		return walHeader{}, corruptFileAt(path, 40, "empty WAL base has a transaction checksum")
	}
	return decoded, nil
}

// rotateWALLocked requires commitMu. It prepares the replacement before
// closing the active WAL, so pre-publication failures leave commits available.
func (db *DB) rotateWALLocked(baseTx uint64, baseChecksum uint32) error {
	path := databaseWALPath(db.path)
	stagingPath, err := prepareWAL(path, db.identity, baseTx, baseChecksum)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(stagingPath)
		}
	}()

	db.mu.Lock()
	wal := db.wal
	db.wal = nil
	db.mu.Unlock()
	if wal == nil {
		err := fmt.Errorf("kitdb: active WAL is unavailable during rotation")
		db.markUnavailable(err)
		return errors.Join(ErrUnavailable, err)
	}
	if err := wal.Close(); err != nil {
		return db.restoreWALAfterRotationFailure(path, fmt.Errorf("kitdb: close WAL for rotation: %w", err))
	}
	if err := replaceFile(stagingPath, path); err != nil {
		return db.restoreWALAfterRotationFailure(path, fmt.Errorf("kitdb: publish rotated WAL: %w", err))
	}
	published = true
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		durableErr := errors.Join(ErrDurabilityUncertain, fmt.Errorf("kitdb: sync rotated WAL directory: %w", err))
		db.markUnavailable(durableErr)
		return durableErr
	}

	file, err := openWALForAppend(path)
	if err != nil {
		db.markUnavailable(err)
		return errors.Join(ErrUnavailable, err)
	}
	db.mu.Lock()
	db.wal = file
	db.walEnd = walHeaderSize
	db.walBaseTx = baseTx
	db.walChecksum = baseChecksum
	db.mu.Unlock()
	return nil
}

func (db *DB) restoreWALAfterRotationFailure(path string, rotationErr error) error {
	file, err := openWALForAppend(path)
	if err != nil {
		db.markUnavailable(errors.Join(rotationErr, err))
		return errors.Join(ErrUnavailable, rotationErr, err)
	}
	db.mu.Lock()
	db.wal = file
	db.mu.Unlock()
	return rotationErr
}

func openWALForAppend(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("kitdb: reopen WAL: %w", err)
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("kitdb: seek reopened WAL: %w", err)
	}
	return file, nil
}

func encodeFrame(transaction uint64, operations []operation) ([]byte, error) {
	return encodeFrameAt(transaction, 0, operations)
}

func encodeFrameAt(transaction uint64, committedAt int64, operations []operation) ([]byte, error) {
	if len(operations) == 0 {
		return nil, ErrEmptyTransaction
	}
	if len(operations) > maxOperations {
		return nil, ErrTransactionTooLarge
	}
	prefixSize := payloadPrefixSize
	flags := uint32(0)
	if committedAt != 0 {
		if committedAt < 0 {
			return nil, fmt.Errorf("kitdb: commit timestamp must be positive")
		}
		prefixSize = timedPayloadPrefixSize
		flags = frameFlagCommittedAt
	}
	payloadSize := prefixSize
	for _, op := range operations {
		if err := validateOperation(op); err != nil {
			return nil, err
		}
		size := operationHeaderSize + len(op.key) + len(op.value)
		if size > maxPayloadSize-payloadSize {
			return nil, ErrTransactionTooLarge
		}
		payloadSize += size
	}
	totalSize := frameHeaderSize + payloadSize + frameTrailerSize
	frame := make([]byte, totalSize)
	copy(frame[:8], frameMagic)
	binary.LittleEndian.PutUint16(frame[8:10], frameFormatVersion)
	binary.LittleEndian.PutUint16(frame[10:12], frameHeaderSize)
	binary.LittleEndian.PutUint32(frame[12:16], flags)
	binary.LittleEndian.PutUint64(frame[16:24], transaction)
	binary.LittleEndian.PutUint64(frame[24:32], uint64(payloadSize))

	position := frameHeaderSize
	binary.LittleEndian.PutUint32(frame[position:position+4], uint32(len(operations)))
	if committedAt != 0 {
		binary.LittleEndian.PutUint64(frame[position+payloadPrefixSize:position+timedPayloadPrefixSize], uint64(committedAt))
	}
	position += prefixSize
	for _, op := range operations {
		frame[position] = byte(op.kind)
		binary.LittleEndian.PutUint32(frame[position+1:position+5], uint32(len(op.key)))
		binary.LittleEndian.PutUint32(frame[position+5:position+9], uint32(len(op.value)))
		position += operationHeaderSize
		position += copy(frame[position:], op.key)
		position += copy(frame[position:], op.value)
	}

	trailer := frame[frameHeaderSize+payloadSize:]
	copy(trailer[:8], commitMagic)
	binary.LittleEndian.PutUint64(trailer[8:16], transaction)
	binary.LittleEndian.PutUint32(trailer[16:20], crc32.Checksum(frame[:frameHeaderSize+payloadSize], crc32cTable))
	binary.LittleEndian.PutUint32(trailer[20:24], uint32(totalSize))
	return frame, nil
}

func frameChecksum(frame []byte) uint32 {
	return binary.LittleEndian.Uint32(frame[len(frame)-frameTrailerSize+16 : len(frame)-frameTrailerSize+20])
}

func replayWAL(file *os.File, size int64, overlay map[string]rowMutation, offset int64, baseTx uint64, baseChecksum uint32, mainTx uint64, mainChecksum uint32, baseCommitTime int64) (uint64, int64, uint32, int64, bool, error) {
	lastTx := baseTx
	lastChecksum := baseChecksum
	lastCommitTime := baseCommitTime
	for offset < size {
		remaining := size - offset
		if remaining < frameHeaderSize {
			if lastTx < mainTx {
				return 0, 0, 0, 0, false, corruptAt(offset, "WAL is truncated before main transaction %d", mainTx)
			}
			return lastTx, offset, lastChecksum, lastCommitTime, true, nil
		}

		header := make([]byte, frameHeaderSize)
		if err := readAt(file, header, offset); err != nil {
			return 0, 0, 0, 0, false, fmt.Errorf("kitdb: read frame header at %d: %w", offset, err)
		}
		transaction, payloadSize, flags, err := decodeFrameHeader(offset, header, lastTx)
		if err != nil {
			return 0, 0, 0, 0, false, err
		}
		frameSize := int64(frameHeaderSize) + int64(payloadSize) + int64(frameTrailerSize)
		if frameSize > remaining {
			if lastTx < mainTx {
				return 0, 0, 0, 0, false, corruptAt(offset, "WAL frame is truncated before main transaction %d", mainTx)
			}
			return lastTx, offset, lastChecksum, lastCommitTime, true, nil
		}

		body := make([]byte, int(payloadSize)+frameTrailerSize)
		if err := readAt(file, body, offset+frameHeaderSize); err != nil {
			return 0, 0, 0, 0, false, fmt.Errorf("kitdb: read frame body at %d: %w", offset, err)
		}
		payload := body[:payloadSize]
		trailer := body[payloadSize:]
		if err := validateTrailer(offset, header, payload, trailer, transaction, uint32(frameSize)); err != nil {
			return 0, 0, 0, 0, false, err
		}
		operations, committedAt, err := decodeFramePayload(payload, flags)
		if err != nil {
			return 0, 0, 0, 0, false, corruptAt(offset+frameHeaderSize, "invalid transaction payload: %v", err)
		}
		if committedAt != 0 && lastCommitTime != 0 && committedAt <= lastCommitTime {
			return 0, 0, 0, 0, false, corruptAt(offset+frameHeaderSize+payloadPrefixSize, "commit timestamp %d does not follow %d", committedAt, lastCommitTime)
		}
		lastChecksum = binary.LittleEndian.Uint32(trailer[16:20])
		if transaction == mainTx && lastChecksum != mainChecksum {
			return 0, 0, 0, 0, false, corruptAt(offset, "WAL transaction %d does not match the main boundary checksum", transaction)
		}
		if transaction > mainTx {
			applyOperations(overlay, operations)
		}
		lastTx = transaction
		if committedAt != 0 {
			lastCommitTime = committedAt
		}
		offset += frameSize
	}
	if lastTx < mainTx {
		return 0, 0, 0, 0, false, corruptAt(offset, "WAL ends at transaction %d before main transaction %d", lastTx, mainTx)
	}
	return lastTx, offset, lastChecksum, lastCommitTime, false, nil
}

func decodeFrameHeader(offset int64, header []byte, lastTx uint64) (uint64, uint64, uint32, error) {
	return decodeFrameHeaderFor(walFilename, offset, header, lastTx)
}

func decodeFrameHeaderFor(path string, offset int64, header []byte, lastTx uint64) (uint64, uint64, uint32, error) {
	if string(header[:8]) != frameMagic {
		return 0, 0, 0, corruptFileAt(path, offset, "invalid transaction frame magic")
	}
	if version := binary.LittleEndian.Uint16(header[8:10]); version != frameFormatVersion {
		return 0, 0, 0, corruptFileAt(path, offset+8, "unsupported transaction frame version %d", version)
	}
	if size := binary.LittleEndian.Uint16(header[10:12]); size != frameHeaderSize {
		return 0, 0, 0, corruptFileAt(path, offset+10, "invalid transaction frame header size %d", size)
	}
	flags := binary.LittleEndian.Uint32(header[12:16])
	if flags & ^frameFlagCommittedAt != 0 {
		return 0, 0, 0, corruptFileAt(path, offset+12, "unsupported transaction frame flags %d", flags)
	}
	transaction := binary.LittleEndian.Uint64(header[16:24])
	if transaction != lastTx+1 || transaction == 0 {
		return 0, 0, 0, corruptFileAt(path, offset+16, "transaction ID %d does not follow %d", transaction, lastTx)
	}
	payloadSize := binary.LittleEndian.Uint64(header[24:32])
	minimumPayload := uint64(payloadPrefixSize)
	if flags&frameFlagCommittedAt != 0 {
		minimumPayload = timedPayloadPrefixSize
	}
	if payloadSize < minimumPayload || payloadSize > maxPayloadSize {
		return 0, 0, 0, corruptFileAt(path, offset+24, "invalid transaction payload size %d", payloadSize)
	}
	return transaction, payloadSize, flags, nil
}

func validateTrailer(offset int64, header, payload, trailer []byte, transaction uint64, frameSize uint32) error {
	return validateTrailerFor(walFilename, offset, header, payload, trailer, transaction, frameSize)
}

func validateTrailerFor(path string, offset int64, header, payload, trailer []byte, transaction uint64, frameSize uint32) error {
	trailerOffset := offset + int64(len(header)) + int64(len(payload))
	if string(trailer[:8]) != commitMagic {
		return corruptFileAt(path, trailerOffset, "invalid commit marker")
	}
	if repeated := binary.LittleEndian.Uint64(trailer[8:16]); repeated != transaction {
		return corruptFileAt(path, trailerOffset+8, "commit transaction ID %d does not match %d", repeated, transaction)
	}
	if declared := binary.LittleEndian.Uint32(trailer[20:24]); declared != frameSize {
		return corruptFileAt(path, trailerOffset+20, "declared frame size %d does not match %d", declared, frameSize)
	}
	checksum := crc32.New(crc32cTable)
	_, _ = checksum.Write(header)
	_, _ = checksum.Write(payload)
	if declared := binary.LittleEndian.Uint32(trailer[16:20]); declared != checksum.Sum32() {
		return corruptFileAt(path, trailerOffset+16, "transaction checksum mismatch")
	}
	return nil
}

func decodePayload(payload []byte) ([]operation, error) {
	operations, _, err := decodeFramePayload(payload, 0)
	return operations, err
}

func decodeFramePayload(payload []byte, flags uint32) ([]operation, int64, error) {
	prefixSize := payloadPrefixSize
	committedAt := int64(0)
	if flags&^frameFlagCommittedAt != 0 {
		return nil, 0, fmt.Errorf("unsupported transaction frame flags %d", flags)
	}
	if flags&frameFlagCommittedAt != 0 {
		prefixSize = timedPayloadPrefixSize
	}
	if len(payload) < prefixSize {
		return nil, 0, fmt.Errorf("missing operation count")
	}
	if flags&frameFlagCommittedAt != 0 {
		committedAt = int64(binary.LittleEndian.Uint64(payload[payloadPrefixSize:timedPayloadPrefixSize]))
		if committedAt <= 0 {
			return nil, 0, fmt.Errorf("invalid commit timestamp %d", committedAt)
		}
	}
	count := uint64(binary.LittleEndian.Uint32(payload[:4]))
	if count == 0 {
		return nil, 0, fmt.Errorf("empty committed transaction")
	}
	if count > maxOperations {
		return nil, 0, fmt.Errorf("operation count %d exceeds limit", count)
	}
	minimumOperationSize := operationHeaderSize + 1
	if count > uint64(len(payload)-prefixSize)/uint64(minimumOperationSize) {
		return nil, 0, fmt.Errorf("operation count %d cannot fit payload", count)
	}
	operations := make([]operation, 0, int(count))
	position := prefixSize
	for index := uint64(0); index < count; index++ {
		if len(payload)-position < operationHeaderSize {
			return nil, 0, fmt.Errorf("operation %d header is truncated", index)
		}
		kind := operationKind(payload[position])
		keySize := uint64(binary.LittleEndian.Uint32(payload[position+1 : position+5]))
		valueSize := uint64(binary.LittleEndian.Uint32(payload[position+5 : position+9]))
		position += operationHeaderSize
		if keySize == 0 || keySize > maxKeySize {
			return nil, 0, fmt.Errorf("operation %d has invalid key size %d", index, keySize)
		}
		if valueSize > maxValueSize {
			return nil, 0, fmt.Errorf("operation %d has invalid value size %d", index, valueSize)
		}
		if kind == operationDelete && valueSize != 0 {
			return nil, 0, fmt.Errorf("delete operation %d has a value", index)
		}
		if kind != operationPut && kind != operationDelete {
			return nil, 0, fmt.Errorf("operation %d has unknown kind %d", index, kind)
		}
		bodySize := keySize + valueSize
		if bodySize > uint64(len(payload)-position) {
			return nil, 0, fmt.Errorf("operation %d body is truncated", index)
		}
		keyEnd := position + int(keySize)
		valueEnd := keyEnd + int(valueSize)
		operations = append(operations, operation{kind: kind, key: payload[position:keyEnd], value: payload[keyEnd:valueEnd]})
		position = valueEnd
	}
	if position != len(payload) {
		return nil, 0, fmt.Errorf("payload has %d trailing bytes", len(payload)-position)
	}
	return operations, committedAt, nil
}

func validateOperation(op operation) error {
	if err := validateKey(op.key); err != nil {
		return err
	}
	if len(op.value) > maxValueSize {
		return ErrValueTooLarge
	}
	switch op.kind {
	case operationPut:
		return nil
	case operationDelete:
		if len(op.value) != 0 {
			return fmt.Errorf("kitdb: delete operation contains a value")
		}
		return nil
	default:
		return fmt.Errorf("kitdb: unknown operation kind %d", op.kind)
	}
}

func readAt(reader io.ReaderAt, buffer []byte, offset int64) error {
	n, err := reader.ReadAt(buffer, offset)
	if n == len(buffer) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return err
}
