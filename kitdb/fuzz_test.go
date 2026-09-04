package kitdb

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"testing"
)

func FuzzDecodePayloadNeverPanics(f *testing.F) {
	f.Add([]byte{
		1, 0, 0, 0,
		byte(operationPut),
		1, 0, 0, 0,
		1, 0, 0, 0,
		'k', 'v',
	})
	f.Add([]byte{})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, payload []byte) {
		operations, err := decodePayload(payload)
		if err != nil {
			return
		}
		if len(operations) == 0 {
			t.Fatal("successful decode returned no operations")
		}
		for _, operation := range operations {
			if err := validateOperation(operation); err != nil {
				t.Fatalf("decoder accepted invalid operation: %v", err)
			}
		}
	})
}

func FuzzDecodeRowPageNeverPanics(f *testing.F) {
	valid := make([]byte, mainRecordHeaderSize+2)
	binary.LittleEndian.PutUint32(valid[:4], 1)
	binary.LittleEndian.PutUint32(valid[4:8], 1)
	valid[8] = 'k'
	valid[9] = 'v'
	f.Add(valid, uint32(1))
	f.Add([]byte{}, uint32(0))
	f.Add([]byte{0xff}, uint32(mainIndexBlockRecords+1))

	f.Fuzz(func(t *testing.T, data []byte, records uint32) {
		page, err := decodeRowPage("fuzz.kitdb", 0, mainHeaderSize, data, records)
		if err != nil {
			return
		}
		if len(page.rows) != int(records) || page.weight < int64(len(data)) {
			t.Fatalf("decoder returned invalid page: records=%d rows=%d weight=%d data=%d", records, len(page.rows), page.weight, len(data))
		}
	})
}

func FuzzDecodeMutationPageNeverPanics(f *testing.F) {
	valid := make([]byte, operationHeaderSize+2)
	valid[0] = byte(operationPut)
	binary.LittleEndian.PutUint32(valid[1:5], 1)
	binary.LittleEndian.PutUint32(valid[5:9], 1)
	valid[9], valid[10] = 'k', 'v'
	f.Add(valid, uint32(1))
	f.Add([]byte{}, uint32(0))
	f.Add([]byte{byte(operationDelete)}, uint32(1))

	f.Fuzz(func(t *testing.T, data []byte, records uint32) {
		page, err := decodeMutationPage("fuzz.kitdb", 0, generationDataOffset, data, records)
		if err != nil {
			return
		}
		if len(page.rows) != int(records) || page.weight < int64(len(data)) {
			t.Fatalf("mutation decoder returned invalid page: records=%d rows=%d weight=%d data=%d", records, len(page.rows), page.weight, len(data))
		}
	})
}

func FuzzReadReplicaBatchMessageNeverPanics(f *testing.F) {
	batch := replicaWireBatchForTest(f)
	var encoded bytes.Buffer
	if _, err := WriteReplicaBatchMessage(context.Background(), &encoded, batch); err != nil {
		f.Fatalf("WriteReplicaBatchMessage seed: %v", err)
	}
	f.Add(encoded.Bytes())
	f.Add([]byte{})
	f.Add([]byte(replicaBatchMessageMagic))

	f.Fuzz(func(t *testing.T, message []byte) {
		decoded, err := ReadReplicaBatchMessage(
			context.Background(),
			bytes.NewReader(message),
			ReplicaBatchLimits{MaxTransactions: 16, MaxBytes: 1 << 20},
		)
		if err != nil {
			return
		}
		if err := validateReplicaBatchEnvelope(decoded); err != nil {
			t.Fatalf("wire decoder accepted invalid batch: %v", err)
		}
	})
}

func FuzzReadReplicaAcknowledgementMessageNeverPanics(f *testing.F) {
	acknowledgement := replicaAcknowledgement(replicaWireBatchForTest(f))
	var encoded bytes.Buffer
	if _, err := WriteReplicaAcknowledgementMessage(context.Background(), &encoded, acknowledgement); err != nil {
		f.Fatalf("WriteReplicaAcknowledgementMessage seed: %v", err)
	}
	f.Add(encoded.Bytes())
	f.Add([]byte{})
	f.Add([]byte(replicaAckMessageMagic))

	f.Fuzz(func(t *testing.T, message []byte) {
		decoded, err := ReadReplicaAcknowledgementMessage(context.Background(), bytes.NewReader(message))
		if err != nil {
			return
		}
		if err := validateReplicaAcknowledgement(decoded); err != nil {
			t.Fatalf("wire decoder accepted invalid acknowledgement: %v", err)
		}
	})
}

func FuzzReadMainSnapshotNeverPanics(f *testing.F) {
	var generationIdentity [16]byte
	generation := make([]byte, generationDataOffset)
	copy(generation[:generationHeaderSize], encodeGenerationHeader(generationIdentity))
	copy(generation[generationSlotAOffset:generationSlotAOffset+generationSlotSize], encodeGenerationSlot(generationIdentity, generationSlot{
		manifestOffset: generationDataOffset,
		fileEnd:        generationDataOffset,
	}))
	f.Add(generation)

	var identity [16]byte
	header := encodeMainHeader(identity, 0, 0, 0, mainHeaderSize, 0, 0)
	valid := append([]byte(nil), header...)
	valid = append(valid, encodeMainTrailer(crc32.Checksum(header, crc32cTable), uint64(mainHeaderSize+mainTrailerSize))...)
	f.Add(valid)

	record := make([]byte, mainRecordHeaderSize+2)
	binary.LittleEndian.PutUint32(record[:4], 1)
	binary.LittleEndian.PutUint32(record[4:8], 1)
	record[8], record[9] = 'k', 'v'
	directory := make([]byte, mainDirectoryEntryHeaderSize+2)
	binary.LittleEndian.PutUint64(directory[:8], mainHeaderSize)
	binary.LittleEndian.PutUint64(directory[8:16], uint64(len(record)))
	binary.LittleEndian.PutUint32(directory[16:20], 1)
	binary.LittleEndian.PutUint32(directory[20:24], crc32.Checksum(record, crc32cTable))
	binary.LittleEndian.PutUint32(directory[24:28], 1)
	binary.LittleEndian.PutUint32(directory[28:32], 1)
	directory[32], directory[33] = 'k', 'k'
	directoryOffset := mainHeaderSize + len(record)
	pagedHeader := encodeMainHeader(identity, 0, 1, 0, uint64(directoryOffset), uint64(len(directory)), 1)
	metadataChecksum := crc32.New(crc32cTable)
	_, _ = metadataChecksum.Write(pagedHeader)
	_, _ = metadataChecksum.Write(directory)
	paged := append([]byte(nil), pagedHeader...)
	paged = append(paged, record...)
	paged = append(paged, directory...)
	paged = append(paged, encodeMainTrailer(metadataChecksum.Sum32(), uint64(directoryOffset+len(directory)+mainTrailerSize))...)
	f.Add(paged)
	f.Add([]byte{})
	f.Add(make([]byte, mainHeaderSize+mainTrailerSize))

	f.Fuzz(func(t *testing.T, data []byte) {
		reader := bytes.NewReader(data)
		_, _ = decodeMainSnapshot("fuzz.kitdb", reader, int64(len(data)))
	})
}
