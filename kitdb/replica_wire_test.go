package kitdb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func TestReplicaWireBatchAndAcknowledgementRoundTripDeterministically(t *testing.T) {
	batch := replicaWireBatchForTest(t)
	var first bytes.Buffer
	written, err := WriteReplicaBatchMessage(context.Background(), &first, batch)
	if err != nil {
		t.Fatalf("WriteReplicaBatchMessage: %v", err)
	}
	if written != uint64(first.Len()) || written != uint64(replicaBatchMessageHeaderSize)+batch.Bytes+replicaBatchMessageTrailerSize {
		t.Fatalf("batch wire bytes = %d, buffer = %d, body = %d", written, first.Len(), batch.Bytes)
	}
	if got := string(first.Bytes()[:8]); got != replicaBatchMessageMagic {
		t.Fatalf("batch magic = %q", got)
	}
	if got := string(first.Bytes()[replicaBatchMessageHeaderSize : replicaBatchMessageHeaderSize+8]); got != frameMagic {
		t.Fatalf("first body frame magic = %q", got)
	}

	var second bytes.Buffer
	if _, err := WriteReplicaBatchMessage(context.Background(), &second, batch); err != nil {
		t.Fatalf("second WriteReplicaBatchMessage: %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatal("replica batch encoding is not deterministic")
	}
	decoded, err := ReadReplicaBatchMessage(context.Background(), bytes.NewReader(first.Bytes()), ReplicaBatchLimits{})
	if err != nil {
		t.Fatalf("ReadReplicaBatchMessage: %v", err)
	}
	if !replicaBatchesEqual(decoded, batch) {
		t.Fatalf("decoded batch = %+v, want %+v", decoded, batch)
	}

	acknowledgement := replicaAcknowledgement(batch)
	var acknowledgementBytes bytes.Buffer
	ackWritten, err := WriteReplicaAcknowledgementMessage(context.Background(), &acknowledgementBytes, acknowledgement)
	if err != nil {
		t.Fatalf("WriteReplicaAcknowledgementMessage: %v", err)
	}
	if ackWritten != ReplicaAcknowledgementMessageBytes || acknowledgementBytes.Len() != replicaAckMessageSize {
		t.Fatalf("acknowledgement bytes = %d/%d", ackWritten, acknowledgementBytes.Len())
	}
	decodedAcknowledgement, err := ReadReplicaAcknowledgementMessage(
		context.Background(), bytes.NewReader(acknowledgementBytes.Bytes()),
	)
	if err != nil || decodedAcknowledgement != acknowledgement {
		t.Fatalf("ReadReplicaAcknowledgementMessage = (%+v, %v)", decodedAcknowledgement, err)
	}
}

func TestReplicaWireRejectsEveryTruncationTrailingBytesAndTampering(t *testing.T) {
	batch := replicaWireBatchForTest(t)
	var encoded bytes.Buffer
	if _, err := WriteReplicaBatchMessage(context.Background(), &encoded, batch); err != nil {
		t.Fatalf("WriteReplicaBatchMessage: %v", err)
	}
	message := encoded.Bytes()
	for cut := 0; cut < len(message); cut++ {
		if _, err := ReadReplicaBatchMessage(
			context.Background(), bytes.NewReader(message[:cut]), ReplicaBatchLimits{},
		); !errors.Is(err, ErrReplicaProtocol) {
			t.Fatalf("truncation at %d error = %v", cut, err)
		}
	}
	withTrailingByte := append(append([]byte(nil), message...), 0)
	if _, err := ReadReplicaBatchMessage(
		context.Background(), bytes.NewReader(withTrailingByte), ReplicaBatchLimits{},
	); !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("trailing byte error = %v", err)
	}

	badDigest := append([]byte(nil), message...)
	badDigest[len(badDigest)-1] ^= 1
	if _, err := ReadReplicaBatchMessage(
		context.Background(), bytes.NewReader(badDigest), ReplicaBatchLimits{},
	); !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("bad digest error = %v", err)
	}

	badFrame := append([]byte(nil), message...)
	badFrame[replicaBatchMessageHeaderSize+frameHeaderSize+payloadPrefixSize+operationHeaderSize] ^= 1
	rehashReplicaBatchMessageForTest(badFrame)
	if _, err := ReadReplicaBatchMessage(
		context.Background(), bytes.NewReader(badFrame), ReplicaBatchLimits{},
	); !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("bad frame error = %v", err)
	}
}

func TestReplicaWireRejectsHeaderLimitsBeforeBodyAllocation(t *testing.T) {
	batch := replicaWireBatchForTest(t)
	var encoded bytes.Buffer
	if _, err := WriteReplicaBatchMessage(context.Background(), &encoded, batch); err != nil {
		t.Fatalf("WriteReplicaBatchMessage: %v", err)
	}

	tests := []struct {
		name   string
		mutate func([]byte)
		limit  bool
	}{
		{name: "magic", mutate: func(message []byte) { message[0] ^= 1 }},
		{name: "wire version", mutate: func(message []byte) { binary.LittleEndian.PutUint16(message[8:10], ReplicaWireVersion+1) }},
		{name: "flags", mutate: func(message []byte) { binary.LittleEndian.PutUint32(message[12:16], 1) }},
		{name: "header checksum", mutate: func(message []byte) { message[replicaBatchMessageChecksumAt] ^= 1 }},
		{name: "reserved", mutate: func(message []byte) {
			message[104] = 1
			rechecksumReplicaBatchHeaderForTest(message)
		}},
		{name: "cursor reserved", mutate: func(message []byte) {
			message[44] = 1
			rechecksumReplicaBatchHeaderForTest(message)
		}},
		{name: "transaction count", limit: true, mutate: func(message []byte) {
			binary.LittleEndian.PutUint32(message[80:84], MaxReplicaBatchTransactions+1)
			rechecksumReplicaBatchHeaderForTest(message)
		}},
		{name: "body bytes", limit: true, mutate: func(message []byte) {
			binary.LittleEndian.PutUint64(message[88:96], MaxReplicaBatchBytes+1)
			binary.LittleEndian.PutUint64(
				message[96:104],
				uint64(replicaBatchMessageHeaderSize)+MaxReplicaBatchBytes+1+replicaBatchMessageTrailerSize,
			)
			rechecksumReplicaBatchHeaderForTest(message)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			corrupt := append([]byte(nil), encoded.Bytes()...)
			test.mutate(corrupt)
			_, err := ReadReplicaBatchMessage(context.Background(), bytes.NewReader(corrupt), ReplicaBatchLimits{
				MaxTransactions: MaxReplicaBatchTransactions,
				MaxBytes:        MaxReplicaBatchBytes,
			})
			if test.limit {
				if !errors.Is(err, ErrReplicaBatchLimit) {
					t.Fatalf("error = %v", err)
				}
				return
			}
			if !errors.Is(err, ErrReplicaProtocol) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReplicaWireAcknowledgementRejectsCorruptionAndTrailingBytes(t *testing.T) {
	acknowledgement := replicaAcknowledgement(replicaWireBatchForTest(t))
	var encoded bytes.Buffer
	if _, err := WriteReplicaAcknowledgementMessage(context.Background(), &encoded, acknowledgement); err != nil {
		t.Fatalf("WriteReplicaAcknowledgementMessage: %v", err)
	}
	message := encoded.Bytes()
	for cut := 0; cut < len(message); cut++ {
		if _, err := ReadReplicaAcknowledgementMessage(
			context.Background(), bytes.NewReader(message[:cut]),
		); !errors.Is(err, ErrReplicaProtocol) {
			t.Fatalf("truncation at %d error = %v", cut, err)
		}
	}
	badDigest := append([]byte(nil), message...)
	badDigest[len(badDigest)-1] ^= 1
	if _, err := ReadReplicaAcknowledgementMessage(
		context.Background(), bytes.NewReader(badDigest),
	); !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("bad ACK digest error = %v", err)
	}
	withTrailingByte := append(append([]byte(nil), message...), 0)
	if _, err := ReadReplicaAcknowledgementMessage(
		context.Background(), bytes.NewReader(withTrailingByte),
	); !errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("trailing ACK byte error = %v", err)
	}
}

func TestReplicaWireHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WriteReplicaBatchMessage(ctx, &bytes.Buffer{}, replicaWireBatchForTest(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled WriteReplicaBatchMessage error = %v", err)
	}
	if _, err := ReadReplicaBatchMessage(ctx, bytes.NewReader(nil), ReplicaBatchLimits{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ReadReplicaBatchMessage error = %v", err)
	}
}

func TestReplicaWirePreservesTransportReadErrors(t *testing.T) {
	transportErr := errors.New("transport read failed")
	if _, err := ReadReplicaBatchMessage(
		context.Background(), replicaWireFailingReader{err: transportErr}, ReplicaBatchLimits{},
	); !errors.Is(err, transportErr) || errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("batch transport error = %v", err)
	}
	if _, err := ReadReplicaAcknowledgementMessage(
		context.Background(), replicaWireFailingReader{err: transportErr},
	); !errors.Is(err, transportErr) || errors.Is(err, ErrReplicaProtocol) {
		t.Fatalf("acknowledgement transport error = %v", err)
	}
}

func replicaWireBatchForTest(t testing.TB) ReplicaBatch {
	t.Helper()
	databaseID := "00112233445566778899aabbccddeeff"
	from := HistoryCursor{DatabaseID: databaseID, Transaction: 7, Checksum: 0x11223344}
	events := []CommitEvent{
		{
			Transaction: 8,
			Operations: []CommitOperation{
				{Kind: CommitOperationPut, Key: []byte("product/1"), Value: []byte("ao thun cotton")},
			},
		},
		{
			Transaction: 9,
			Operations: []CommitOperation{
				{Kind: CommitOperationPut, Key: []byte("product/2"), Value: []byte("giay chay bo")},
				{Kind: CommitOperationDelete, Key: []byte("product/old")},
			},
		},
	}
	batch := ReplicaBatch{
		Version: ReplicaProtocolVersion, DatabaseID: databaseID,
		From: from, To: from,
	}
	for index := range events {
		operations, err := referenceReplicaOperations(events[index].Operations)
		if err != nil {
			t.Fatalf("referenceReplicaOperations: %v", err)
		}
		frame, err := encodeFrame(events[index].Transaction, operations)
		if err != nil {
			t.Fatalf("encodeFrame: %v", err)
		}
		events[index].Checksum = frameChecksum(frame)
		batch.Transactions = append(batch.Transactions, events[index])
		batch.Bytes += uint64(len(frame))
		batch.To = HistoryCursor{
			DatabaseID: databaseID, Transaction: events[index].Transaction, Checksum: events[index].Checksum,
		}
	}
	batch.SourceBoundary = batch.To
	return batch
}

func rechecksumReplicaBatchHeaderForTest(message []byte) {
	binary.LittleEndian.PutUint32(
		message[replicaBatchMessageChecksumAt:replicaBatchMessageHeaderSize],
		crc32.Checksum(message[:replicaBatchMessageChecksumAt], crc32cTable),
	)
}

func rehashReplicaBatchMessageForTest(message []byte) {
	digest := sha256.Sum256(message[:len(message)-sha256.Size])
	copy(message[len(message)-sha256.Size:], digest[:])
}

type replicaWireFailingReader struct {
	err error
}

func (reader replicaWireFailingReader) Read([]byte) (int, error) {
	return 0, reader.err
}
