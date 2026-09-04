package kitdb

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

const (
	// ReplicaWireVersion identifies the transport encoding independently of
	// the replica protocol and durable KitDB file formats.
	ReplicaWireVersion uint16 = 1

	replicaBatchMessageMagic       = "KITDRPB1"
	replicaBatchMessageEndMagic    = "KITDRPE1"
	replicaBatchMessageHeaderSize  = 128
	replicaBatchMessageTrailerSize = 64
	replicaBatchMessageChecksumAt  = 124

	replicaAckMessageMagic      = "KITDRPA1"
	replicaAckMessagePrefixSize = 64
	replicaAckMessageSize       = 96
)

const (
	// MaxReplicaBatchMessageBytes is the largest legal encoded batch message.
	MaxReplicaBatchMessageBytes uint64 = replicaBatchMessageHeaderSize + MaxReplicaBatchBytes + replicaBatchMessageTrailerSize
	// ReplicaAcknowledgementMessageBytes is the exact encoded ACK size.
	ReplicaAcknowledgementMessageBytes uint64 = replicaAckMessageSize
)

// WriteReplicaBatchMessage writes one exact replica batch message. The body is
// the canonical WAL-frame v1 encoding, while the envelope adds early header
// validation and a SHA-256 digest over every byte except the digest itself.
// Callers publishing to durable media must write to staging before exposing it.
func WriteReplicaBatchMessage(ctx context.Context, writer io.Writer, batch ReplicaBatch) (uint64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("kitdb: nil replica wire context")
	}
	if writer == nil {
		return 0, fmt.Errorf("kitdb: nil replica wire writer")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateReplicaBatchEnvelope(batch); err != nil {
		return 0, err
	}
	// Validate every source checksum before emitting a partial stream. File
	// transports still stage writes, but generic callers receive the same rule.
	if err := validateReplicaBatchChecksums(ctx, batch); err != nil {
		return 0, err
	}

	header, err := encodeReplicaBatchMessageHeader(batch)
	if err != nil {
		return 0, err
	}
	digest := sha256.New()
	destination := &replicaContextWriter{ctx: ctx, writer: writer}
	hashed := io.MultiWriter(destination, digest)
	if _, err := writeAll(hashed, header); err != nil {
		return 0, fmt.Errorf("kitdb: write replica batch header: %w", err)
	}
	writtenBody := uint64(0)
	for _, event := range batch.Transactions {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		operations, err := referenceReplicaOperations(event.Operations)
		if err != nil {
			return 0, err
		}
		frame, err := encodeFrameAt(event.Transaction, commitTimeUnixNano(event.CommittedAt), operations)
		if err != nil {
			return 0, errors.Join(ErrReplicaProtocol, err)
		}
		if checksum := frameChecksum(frame); checksum != event.Checksum {
			return 0, errors.Join(
				ErrReplicaDiverged,
				fmt.Errorf("kitdb: replica event %d checksum changed while encoding", event.Transaction),
			)
		}
		if _, err := writeAll(hashed, frame); err != nil {
			return 0, fmt.Errorf("kitdb: write replica transaction %d: %w", event.Transaction, err)
		}
		writtenBody += uint64(len(frame))
	}
	if writtenBody != batch.Bytes {
		return 0, errors.Join(
			ErrReplicaProtocol,
			fmt.Errorf("kitdb: wrote %d replica body bytes, want %d", writtenBody, batch.Bytes),
		)
	}

	totalBytes := uint64(replicaBatchMessageHeaderSize) + batch.Bytes + replicaBatchMessageTrailerSize
	trailerPrefix := make([]byte, replicaBatchMessageTrailerSize-sha256.Size)
	copy(trailerPrefix[:8], replicaBatchMessageEndMagic)
	binary.LittleEndian.PutUint16(trailerPrefix[8:10], ReplicaWireVersion)
	binary.LittleEndian.PutUint16(trailerPrefix[10:12], replicaBatchMessageTrailerSize)
	binary.LittleEndian.PutUint64(trailerPrefix[16:24], batch.Bytes)
	binary.LittleEndian.PutUint64(trailerPrefix[24:32], totalBytes)
	if _, err := writeAll(hashed, trailerPrefix); err != nil {
		return 0, fmt.Errorf("kitdb: write replica batch trailer: %w", err)
	}
	if _, err := writeAll(destination, digest.Sum(nil)); err != nil {
		return 0, fmt.Errorf("kitdb: write replica batch digest: %w", err)
	}
	return totalBytes, nil
}

// ReadReplicaBatchMessage decodes exactly one message and requires EOF after
// its digest. Cardinality and byte limits are checked before body allocation.
func ReadReplicaBatchMessage(ctx context.Context, reader io.Reader, limits ReplicaBatchLimits) (ReplicaBatch, error) {
	if ctx == nil {
		return ReplicaBatch{}, fmt.Errorf("kitdb: nil replica wire context")
	}
	if reader == nil {
		return ReplicaBatch{}, fmt.Errorf("kitdb: nil replica wire reader")
	}
	if err := ctx.Err(); err != nil {
		return ReplicaBatch{}, err
	}
	normalized, err := normalizeReplicaBatchLimits(limits)
	if err != nil {
		return ReplicaBatch{}, err
	}

	source := &replicaContextReader{ctx: ctx, reader: reader}
	digest := sha256.New()
	hashed := io.TeeReader(source, digest)
	header := make([]byte, replicaBatchMessageHeaderSize)
	if err := readReplicaMessageFull(hashed, header, "batch header"); err != nil {
		return ReplicaBatch{}, err
	}
	decoded, transactionCount, bodyBytes, totalBytes, err := decodeReplicaBatchMessageHeader(header, normalized)
	if err != nil {
		return ReplicaBatch{}, err
	}

	decoded.Transactions = make([]CommitEvent, 0, int(transactionCount))
	decoded.Bytes = bodyBytes
	remaining := bodyBytes
	lastTransaction := decoded.From.Transaction
	offset := uint64(replicaBatchMessageHeaderSize)
	for index := uint32(0); index < transactionCount; index++ {
		if remaining < frameHeaderSize+payloadPrefixSize+operationHeaderSize+1+frameTrailerSize {
			return ReplicaBatch{}, replicaMessageError("transaction %d cannot fit in %d remaining body bytes", index, remaining)
		}
		frameHeader := make([]byte, frameHeaderSize)
		if err := readReplicaMessageFull(hashed, frameHeader, fmt.Sprintf("transaction %d header", index)); err != nil {
			return ReplicaBatch{}, err
		}
		transaction, payloadBytes, flags, frameErr := decodeFrameHeaderFor("replica batch message", int64(offset), frameHeader, lastTransaction)
		if frameErr != nil {
			return ReplicaBatch{}, replicaMessageError("invalid transaction %d header: %v", index, frameErr)
		}
		frameBytes := uint64(frameHeaderSize) + payloadBytes + frameTrailerSize
		if frameBytes > remaining {
			return ReplicaBatch{}, replicaMessageError("transaction %d declares %d bytes with %d remaining", index, frameBytes, remaining)
		}
		body := make([]byte, int(payloadBytes)+frameTrailerSize)
		if err := readReplicaMessageFull(hashed, body, fmt.Sprintf("transaction %d body", index)); err != nil {
			return ReplicaBatch{}, err
		}
		payload := body[:int(payloadBytes)]
		trailer := body[int(payloadBytes):]
		if frameErr := validateTrailerFor(
			"replica batch message",
			int64(offset),
			frameHeader,
			payload,
			trailer,
			transaction,
			uint32(frameBytes),
		); frameErr != nil {
			return ReplicaBatch{}, replicaMessageError("invalid transaction %d trailer: %v", index, frameErr)
		}
		operations, committedAt, decodeErr := decodeFramePayload(payload, flags)
		if decodeErr != nil {
			return ReplicaBatch{}, replicaMessageError("invalid transaction %d payload: %v", index, decodeErr)
		}
		decoded.Transactions = append(decoded.Transactions, CommitEvent{
			Transaction: transaction,
			Checksum:    binary.LittleEndian.Uint32(trailer[16:20]),
			CommittedAt: commitTimeValue(committedAt),
			Operations:  exposeReplicaOperations(operations),
		})
		lastTransaction = transaction
		remaining -= frameBytes
		offset += frameBytes
	}
	if remaining != 0 {
		return ReplicaBatch{}, replicaMessageError("batch body has %d trailing bytes", remaining)
	}

	trailerPrefix := make([]byte, replicaBatchMessageTrailerSize-sha256.Size)
	if err := readReplicaMessageFull(hashed, trailerPrefix, "batch trailer"); err != nil {
		return ReplicaBatch{}, err
	}
	if err := validateReplicaBatchMessageTrailer(trailerPrefix, bodyBytes, totalBytes); err != nil {
		return ReplicaBatch{}, err
	}
	declaredDigest := make([]byte, sha256.Size)
	if err := readReplicaMessageFull(source, declaredDigest, "batch digest"); err != nil {
		return ReplicaBatch{}, err
	}
	if subtle.ConstantTimeCompare(declaredDigest, digest.Sum(nil)) != 1 {
		return ReplicaBatch{}, replicaMessageError("batch SHA-256 digest mismatch")
	}
	if err := requireReplicaMessageEOF(source); err != nil {
		return ReplicaBatch{}, err
	}
	if err := validateReplicaBatchEnvelope(decoded); err != nil {
		return ReplicaBatch{}, err
	}
	return decoded, nil
}

// WriteReplicaAcknowledgementMessage writes the fixed-size wire form of one
// exact durable target cursor.
func WriteReplicaAcknowledgementMessage(
	ctx context.Context,
	writer io.Writer,
	acknowledgement ReplicaAcknowledgement,
) (uint64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("kitdb: nil replica wire context")
	}
	if writer == nil {
		return 0, fmt.Errorf("kitdb: nil replica wire writer")
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := validateReplicaAcknowledgement(acknowledgement); err != nil {
		return 0, err
	}
	identity, err := decodeReplicaMessageIdentity(acknowledgement.Cursor.DatabaseID)
	if err != nil {
		return 0, err
	}
	encoded := make([]byte, replicaAckMessageSize)
	copy(encoded[:8], replicaAckMessageMagic)
	binary.LittleEndian.PutUint16(encoded[8:10], ReplicaWireVersion)
	binary.LittleEndian.PutUint16(encoded[10:12], replicaAckMessageSize)
	copy(encoded[16:32], identity[:])
	binary.LittleEndian.PutUint64(encoded[32:40], acknowledgement.Cursor.Transaction)
	binary.LittleEndian.PutUint32(encoded[40:44], acknowledgement.Cursor.Checksum)
	binary.LittleEndian.PutUint16(encoded[44:46], acknowledgement.Version)
	digest := sha256.Sum256(encoded[:replicaAckMessagePrefixSize])
	copy(encoded[replicaAckMessagePrefixSize:], digest[:])
	if _, err := writeAll(&replicaContextWriter{ctx: ctx, writer: writer}, encoded); err != nil {
		return 0, fmt.Errorf("kitdb: write replica acknowledgement: %w", err)
	}
	return replicaAckMessageSize, nil
}

// ReadReplicaAcknowledgementMessage decodes one fixed-size ACK and rejects
// trailing bytes.
func ReadReplicaAcknowledgementMessage(ctx context.Context, reader io.Reader) (ReplicaAcknowledgement, error) {
	if ctx == nil {
		return ReplicaAcknowledgement{}, fmt.Errorf("kitdb: nil replica wire context")
	}
	if reader == nil {
		return ReplicaAcknowledgement{}, fmt.Errorf("kitdb: nil replica wire reader")
	}
	if err := ctx.Err(); err != nil {
		return ReplicaAcknowledgement{}, err
	}
	source := &replicaContextReader{ctx: ctx, reader: reader}
	encoded := make([]byte, replicaAckMessageSize)
	if err := readReplicaMessageFull(source, encoded, "acknowledgement"); err != nil {
		return ReplicaAcknowledgement{}, err
	}
	if err := requireReplicaMessageEOF(source); err != nil {
		return ReplicaAcknowledgement{}, err
	}
	if string(encoded[:8]) != replicaAckMessageMagic {
		return ReplicaAcknowledgement{}, replicaMessageError("invalid acknowledgement magic")
	}
	if version := binary.LittleEndian.Uint16(encoded[8:10]); version != ReplicaWireVersion {
		return ReplicaAcknowledgement{}, replicaMessageError("unsupported acknowledgement wire version %d", version)
	}
	if size := binary.LittleEndian.Uint16(encoded[10:12]); size != replicaAckMessageSize {
		return ReplicaAcknowledgement{}, replicaMessageError("invalid acknowledgement size %d", size)
	}
	if flags := binary.LittleEndian.Uint32(encoded[12:16]); flags != 0 {
		return ReplicaAcknowledgement{}, replicaMessageError("unsupported acknowledgement flags %d", flags)
	}
	for offset := 46; offset < replicaAckMessagePrefixSize; offset++ {
		if encoded[offset] != 0 {
			return ReplicaAcknowledgement{}, replicaMessageError("unsupported acknowledgement reserved value at byte %d", offset)
		}
	}
	digest := sha256.Sum256(encoded[:replicaAckMessagePrefixSize])
	if subtle.ConstantTimeCompare(encoded[replicaAckMessagePrefixSize:], digest[:]) != 1 {
		return ReplicaAcknowledgement{}, replicaMessageError("acknowledgement SHA-256 digest mismatch")
	}
	acknowledgement := ReplicaAcknowledgement{
		Version: binary.LittleEndian.Uint16(encoded[44:46]),
		Cursor: HistoryCursor{
			DatabaseID:  hex.EncodeToString(encoded[16:32]),
			Transaction: binary.LittleEndian.Uint64(encoded[32:40]),
			Checksum:    binary.LittleEndian.Uint32(encoded[40:44]),
		},
	}
	if err := validateReplicaAcknowledgement(acknowledgement); err != nil {
		return ReplicaAcknowledgement{}, err
	}
	return acknowledgement, nil
}

func encodeReplicaBatchMessageHeader(batch ReplicaBatch) ([]byte, error) {
	identity, err := decodeReplicaMessageIdentity(batch.DatabaseID)
	if err != nil {
		return nil, err
	}
	totalBytes := uint64(replicaBatchMessageHeaderSize) + batch.Bytes + replicaBatchMessageTrailerSize
	if totalBytes > MaxReplicaBatchMessageBytes {
		return nil, errors.Join(ErrReplicaBatchLimit, fmt.Errorf("kitdb: replica message requires %d bytes", totalBytes))
	}
	header := make([]byte, replicaBatchMessageHeaderSize)
	copy(header[:8], replicaBatchMessageMagic)
	binary.LittleEndian.PutUint16(header[8:10], ReplicaWireVersion)
	binary.LittleEndian.PutUint16(header[10:12], replicaBatchMessageHeaderSize)
	copy(header[16:32], identity[:])
	encodeReplicaMessageCursor(header[32:48], batch.From)
	encodeReplicaMessageCursor(header[48:64], batch.To)
	encodeReplicaMessageCursor(header[64:80], batch.SourceBoundary)
	binary.LittleEndian.PutUint32(header[80:84], uint32(len(batch.Transactions)))
	binary.LittleEndian.PutUint16(header[84:86], batch.Version)
	binary.LittleEndian.PutUint16(header[86:88], frameFormatVersion)
	binary.LittleEndian.PutUint64(header[88:96], batch.Bytes)
	binary.LittleEndian.PutUint64(header[96:104], totalBytes)
	binary.LittleEndian.PutUint32(
		header[replicaBatchMessageChecksumAt:],
		crc32.Checksum(header[:replicaBatchMessageChecksumAt], crc32cTable),
	)
	return header, nil
}

func decodeReplicaBatchMessageHeader(
	header []byte,
	limits ReplicaBatchLimits,
) (ReplicaBatch, uint32, uint64, uint64, error) {
	if string(header[:8]) != replicaBatchMessageMagic {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("invalid batch magic")
	}
	if version := binary.LittleEndian.Uint16(header[8:10]); version != ReplicaWireVersion {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("unsupported batch wire version %d", version)
	}
	if size := binary.LittleEndian.Uint16(header[10:12]); size != replicaBatchMessageHeaderSize {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("invalid batch header size %d", size)
	}
	if flags := binary.LittleEndian.Uint32(header[12:16]); flags != 0 {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("unsupported batch flags %d", flags)
	}
	declaredHeaderChecksum := binary.LittleEndian.Uint32(header[replicaBatchMessageChecksumAt:])
	actualHeaderChecksum := crc32.Checksum(header[:replicaBatchMessageChecksumAt], crc32cTable)
	if declaredHeaderChecksum != actualHeaderChecksum {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("batch header checksum mismatch")
	}
	for offset := 104; offset < replicaBatchMessageChecksumAt; offset++ {
		if header[offset] != 0 {
			return ReplicaBatch{}, 0, 0, 0, replicaMessageError("unsupported batch reserved value at byte %d", offset)
		}
	}
	for _, offset := range []int{44, 45, 46, 47, 60, 61, 62, 63, 76, 77, 78, 79} {
		if header[offset] != 0 {
			return ReplicaBatch{}, 0, 0, 0, replicaMessageError("unsupported cursor reserved value at byte %d", offset)
		}
	}
	transactionCount := binary.LittleEndian.Uint32(header[80:84])
	if transactionCount > limits.MaxTransactions {
		return ReplicaBatch{}, 0, 0, 0, errors.Join(
			ErrReplicaBatchLimit,
			fmt.Errorf("kitdb: replica message declares %d transactions, limit is %d", transactionCount, limits.MaxTransactions),
		)
	}
	bodyBytes := binary.LittleEndian.Uint64(header[88:96])
	if bodyBytes > limits.MaxBytes {
		return ReplicaBatch{}, 0, 0, 0, errors.Join(
			ErrReplicaBatchLimit,
			fmt.Errorf("kitdb: replica message declares %d body bytes, limit is %d", bodyBytes, limits.MaxBytes),
		)
	}
	minimumFrameBytes := uint64(frameHeaderSize + payloadPrefixSize + operationHeaderSize + 1 + frameTrailerSize)
	if uint64(transactionCount) > bodyBytes/minimumFrameBytes {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("%d transactions cannot fit in %d body bytes", transactionCount, bodyBytes)
	}
	totalBytes := binary.LittleEndian.Uint64(header[96:104])
	expectedTotal := uint64(replicaBatchMessageHeaderSize) + bodyBytes + replicaBatchMessageTrailerSize
	if totalBytes != expectedTotal || totalBytes > MaxReplicaBatchMessageBytes {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("batch total size is %d, want %d", totalBytes, expectedTotal)
	}
	protocolVersion := binary.LittleEndian.Uint16(header[84:86])
	if protocolVersion != ReplicaProtocolVersion {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("unsupported replica protocol version %d", protocolVersion)
	}
	if frameVersion := binary.LittleEndian.Uint16(header[86:88]); frameVersion != frameFormatVersion {
		return ReplicaBatch{}, 0, 0, 0, replicaMessageError("unsupported replica frame version %d", frameVersion)
	}
	databaseID := hex.EncodeToString(header[16:32])
	decoded := ReplicaBatch{
		Version:        protocolVersion,
		DatabaseID:     databaseID,
		From:           decodeReplicaMessageCursor(databaseID, header[32:48]),
		To:             decodeReplicaMessageCursor(databaseID, header[48:64]),
		SourceBoundary: decodeReplicaMessageCursor(databaseID, header[64:80]),
	}
	return decoded, transactionCount, bodyBytes, totalBytes, nil
}

func validateReplicaBatchMessageTrailer(prefix []byte, bodyBytes, totalBytes uint64) error {
	if string(prefix[:8]) != replicaBatchMessageEndMagic {
		return replicaMessageError("invalid batch trailer magic")
	}
	if version := binary.LittleEndian.Uint16(prefix[8:10]); version != ReplicaWireVersion {
		return replicaMessageError("unsupported batch trailer version %d", version)
	}
	if size := binary.LittleEndian.Uint16(prefix[10:12]); size != replicaBatchMessageTrailerSize {
		return replicaMessageError("invalid batch trailer size %d", size)
	}
	if flags := binary.LittleEndian.Uint32(prefix[12:16]); flags != 0 {
		return replicaMessageError("unsupported batch trailer flags %d", flags)
	}
	if repeated := binary.LittleEndian.Uint64(prefix[16:24]); repeated != bodyBytes {
		return replicaMessageError("batch trailer repeats %d body bytes, want %d", repeated, bodyBytes)
	}
	if repeated := binary.LittleEndian.Uint64(prefix[24:32]); repeated != totalBytes {
		return replicaMessageError("batch trailer repeats %d total bytes, want %d", repeated, totalBytes)
	}
	return nil
}

func encodeReplicaMessageCursor(destination []byte, cursor HistoryCursor) {
	binary.LittleEndian.PutUint64(destination[:8], cursor.Transaction)
	binary.LittleEndian.PutUint32(destination[8:12], cursor.Checksum)
}

func decodeReplicaMessageCursor(databaseID string, source []byte) HistoryCursor {
	return HistoryCursor{
		DatabaseID:  databaseID,
		Transaction: binary.LittleEndian.Uint64(source[:8]),
		Checksum:    binary.LittleEndian.Uint32(source[8:12]),
	}
}

func decodeReplicaMessageIdentity(databaseID string) ([16]byte, error) {
	var identity [16]byte
	if err := validateReplicaDatabaseID(databaseID); err != nil {
		return identity, err
	}
	decoded, err := hex.DecodeString(databaseID)
	if err != nil {
		return identity, errors.Join(ErrReplicaProtocol, err)
	}
	copy(identity[:], decoded)
	return identity, nil
}

func exposeReplicaOperations(operations []operation) []CommitOperation {
	exposed := make([]CommitOperation, len(operations))
	for index, item := range operations {
		kind := CommitOperationDelete
		if item.kind == operationPut {
			kind = CommitOperationPut
		}
		exposed[index] = CommitOperation{Kind: kind, Key: item.key, Value: item.value}
	}
	return exposed
}

func readReplicaMessageFull(reader io.Reader, destination []byte, part string) error {
	if _, err := io.ReadFull(reader, destination); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.ErrNoProgress) {
			return replicaMessageError("read %s: %v", part, err)
		}
		return fmt.Errorf("kitdb: read replica %s: %w", part, err)
	}
	return nil
}

func requireReplicaMessageEOF(reader io.Reader) error {
	var trailing [1]byte
	read, err := io.ReadFull(reader, trailing[:])
	if read != 0 {
		return replicaMessageError("message has trailing bytes")
	}
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return fmt.Errorf("kitdb: finish replica message: %w", err)
	}
	return replicaMessageError("message reader made no progress at EOF")
}

func replicaMessageError(format string, arguments ...any) error {
	return errors.Join(ErrReplicaProtocol, fmt.Errorf("kitdb: replica wire: "+format, arguments...))
}

type replicaContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *replicaContextReader) Read(destination []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(destination)
}

type replicaContextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer *replicaContextWriter) Write(source []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	return writer.writer.Write(source)
}
