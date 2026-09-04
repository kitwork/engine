package work

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	kitDBImportNamespace         byte = 0x05
	kitDBImportStateMagic             = "KIMP"
	kitDBImportStateVersion      byte = 1
	kitDBImportStatePayloadLimit      = 64 << 10
	kitDBImportIDLimit                = 256
	kitDBImportSourceLimit            = 1024
	kitDBImportColumnLimit            = 1024
)

var kitDBImportCRCTable = crc32.MakeTable(crc32.Castagnoli)

// kitDBImportState is adapter metadata, not a user row. One KIMP value is
// advanced in the same record transaction as the rows in its COPY chunk.
type kitDBImportState struct {
	Version      uint8    `json:"version"`
	ID           string   `json:"id"`
	Source       string   `json:"source"`
	SourceFormat string   `json:"source_format"`
	TableID      string   `json:"table_id"`
	Table        string   `json:"table"`
	FieldIDs     []string `json:"field_ids"`
	Columns      []string `json:"columns"`
	Seed         string   `json:"seed"`
	Checksum     string   `json:"checksum"`
	Chunk        uint64   `json:"chunk"`
	Rows         uint64   `json:"rows"`
	Offset       uint64   `json:"offset"`
	Complete     bool     `json:"complete"`
	Cancelled    bool     `json:"cancelled,omitempty"`
	Transaction  uint64   `json:"transaction"`
}

func kitDBImportStateKey(id string) ([]byte, error) {
	if err := validateKitDBImportLabel("id", id, kitDBImportIDLimit); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(append([]byte("kitdb-import-state\x00"), id...))
	key := make([]byte, 1, 1+len(digest))
	key[0] = kitDBImportNamespace
	return append(key, digest[:]...), nil
}

func validateKitDBImportLabel(name, label string, limit int) error {
	if label == "" || len(label) > limit || !utf8.ValidString(label) || strings.ContainsRune(label, 0) {
		return fmt.Errorf("kitdb import %s must be valid UTF-8 between 1 and %d bytes", name, limit)
	}
	for _, current := range label {
		if unicode.IsControl(current) {
			return fmt.Errorf("kitdb import %s contains a control character", name)
		}
	}
	return nil
}

func normalizeKitDBImportChecksum(name, checksum string) (string, error) {
	if len(checksum) != sha256.Size*2 {
		return "", fmt.Errorf("kitdb import %s must be a 64-character SHA-256 checksum", name)
	}
	decoded, err := hex.DecodeString(checksum)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("kitdb import %s must be a hexadecimal SHA-256 checksum", name)
	}
	return hex.EncodeToString(decoded), nil
}

func encodeKitDBImportState(state kitDBImportState) ([]byte, error) {
	if err := validateKitDBImportState(state); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("kitdb import encode state: %w", err)
	}
	if len(payload) > kitDBImportStatePayloadLimit {
		return nil, fmt.Errorf("kitdb import state exceeds %d bytes", kitDBImportStatePayloadLimit)
	}
	encoded := make([]byte, 12, 12+len(payload)+4)
	copy(encoded[:4], kitDBImportStateMagic)
	encoded[4] = kitDBImportStateVersion
	binary.LittleEndian.PutUint32(encoded[8:12], uint32(len(payload)))
	encoded = append(encoded, payload...)
	encoded = binary.LittleEndian.AppendUint32(encoded, crc32.Checksum(encoded, kitDBImportCRCTable))
	return encoded, nil
}

func decodeKitDBImportState(encoded []byte) (kitDBImportState, error) {
	if len(encoded) < 16 || string(encoded[:4]) != kitDBImportStateMagic {
		return kitDBImportState{}, fmt.Errorf("kitdb import state has an invalid KIMP envelope")
	}
	if encoded[4] != kitDBImportStateVersion {
		return kitDBImportState{}, fmt.Errorf("kitdb import state version %d is not supported", encoded[4])
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return kitDBImportState{}, fmt.Errorf("kitdb import state has nonzero reserved bytes")
	}
	payloadSize := int(binary.LittleEndian.Uint32(encoded[8:12]))
	if payloadSize > kitDBImportStatePayloadLimit || payloadSize != len(encoded)-16 {
		return kitDBImportState{}, fmt.Errorf("kitdb import state has an invalid payload size")
	}
	wantCRC := binary.LittleEndian.Uint32(encoded[len(encoded)-4:])
	if gotCRC := crc32.Checksum(encoded[:len(encoded)-4], kitDBImportCRCTable); gotCRC != wantCRC {
		return kitDBImportState{}, fmt.Errorf("kitdb import state checksum mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded[12 : len(encoded)-4]))
	decoder.DisallowUnknownFields()
	var state kitDBImportState
	if err := decoder.Decode(&state); err != nil {
		return kitDBImportState{}, fmt.Errorf("kitdb import decode state: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return kitDBImportState{}, fmt.Errorf("kitdb import state has trailing JSON data")
	} else if err != io.EOF {
		return kitDBImportState{}, fmt.Errorf("kitdb import decode trailing state: %w", err)
	}
	if err := validateKitDBImportState(state); err != nil {
		return kitDBImportState{}, err
	}
	return state, nil
}

func validateKitDBImportState(state kitDBImportState) error {
	if state.Version != kitDBImportStateVersion {
		return fmt.Errorf("kitdb import payload version %d is not supported", state.Version)
	}
	if err := validateKitDBImportLabel("id", state.ID, kitDBImportIDLimit); err != nil {
		return err
	}
	if err := validateKitDBImportLabel("source", state.Source, kitDBImportSourceLimit); err != nil {
		return err
	}
	if state.SourceFormat != "csv" && state.SourceFormat != "jsonl" {
		return fmt.Errorf("kitdb import source format %q is not supported", state.SourceFormat)
	}
	if state.TableID == "" || state.Table == "" || len(state.FieldIDs) == 0 ||
		len(state.FieldIDs) != len(state.Columns) || len(state.FieldIDs) > kitDBImportColumnLimit {
		return fmt.Errorf("kitdb import state has an invalid table or column identity")
	}
	if _, err := decodeStableKitDBImportID(state.TableID); err != nil {
		return fmt.Errorf("kitdb import state table identity: %w", err)
	}
	for index, fieldID := range state.FieldIDs {
		if _, err := decodeStableKitDBImportID(fieldID); err != nil {
			return fmt.Errorf("kitdb import state field %d identity: %w", index, err)
		}
		if state.Columns[index] == "" {
			return fmt.Errorf("kitdb import state column %d is empty", index)
		}
	}
	if _, err := normalizeKitDBImportChecksum("seed", state.Seed); err != nil {
		return err
	}
	if _, err := normalizeKitDBImportChecksum("checksum", state.Checksum); err != nil {
		return err
	}
	if state.Chunk == 0 || state.Transaction == 0 {
		return fmt.Errorf("kitdb import state has an invalid chunk or transaction watermark")
	}
	if state.Complete && state.Cancelled {
		return fmt.Errorf("kitdb import state cannot be complete and cancelled")
	}
	return nil
}

func decodeStableKitDBImportID(id string) ([]byte, error) {
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != 16 {
		return nil, fmt.Errorf("expected a 16-byte hexadecimal identity")
	}
	return decoded, nil
}

func loadKitDBImportState(reader kitDBReader, id string) (kitDBImportState, bool, error) {
	key, err := kitDBImportStateKey(id)
	if err != nil {
		return kitDBImportState{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return kitDBImportState{}, found, err
	}
	state, err := decodeKitDBImportState(encoded)
	if err != nil {
		return kitDBImportState{}, false, err
	}
	if state.ID != id {
		return kitDBImportState{}, false, fmt.Errorf("kitdb import state key collision for %q", id)
	}
	return state, true, nil
}

func prepareKitDBImportState(
	transaction *kitDBRecordTransaction,
	definition *StructDef,
	fields []StructFieldDef,
	progress *kitDBPostgresCopyProgress,
) (*kitDBImportState, error) {
	if progress == nil {
		return nil, nil
	}
	if transaction == nil || definition == nil {
		return nil, fmt.Errorf("kitdb import transaction or struct is unavailable")
	}
	if transaction.base == math.MaxUint64 {
		return nil, fmt.Errorf("kitdb import transaction watermark overflow")
	}
	existing, found, err := loadKitDBImportState(transaction, progress.id)
	if err != nil {
		return nil, err
	}
	fieldIDs := make([]string, len(fields))
	columns := make([]string, len(fields))
	for index, field := range fields {
		fieldIDs[index] = field.ID
		columns[index] = field.Name
	}
	seed := progress.previous
	totalRows := progress.rows
	if found {
		if existing.Complete {
			return nil, fmt.Errorf("kitdb import %q is already complete", progress.id)
		}
		if existing.Cancelled {
			return nil, fmt.Errorf("kitdb import %q is cancelled", progress.id)
		}
		if existing.Source != progress.source || existing.SourceFormat != progress.sourceFormat ||
			existing.TableID != definition.ID || !equalKitDBImportStrings(existing.FieldIDs, fieldIDs) {
			return nil, fmt.Errorf("kitdb import %q source, table, or column identity changed", progress.id)
		}
		if existing.Chunk == math.MaxUint64 {
			return nil, fmt.Errorf("kitdb import %q chunk watermark overflow", progress.id)
		}
		if progress.chunk != existing.Chunk+1 || progress.start != existing.Offset ||
			progress.previous != existing.Checksum {
			return nil, fmt.Errorf(
				"kitdb import %q expected chunk %d at offset %d after checksum %s",
				progress.id, existing.Chunk+1, existing.Offset, existing.Checksum,
			)
		}
		if progress.rows > math.MaxUint64-existing.Rows {
			return nil, fmt.Errorf("kitdb import %q row watermark overflow", progress.id)
		}
		seed = existing.Seed
		totalRows += existing.Rows
	} else if progress.chunk != 1 {
		return nil, fmt.Errorf("kitdb import %q must begin at chunk 1", progress.id)
	}
	state := &kitDBImportState{
		Version: kitDBImportStateVersion, ID: progress.id,
		Source: progress.source, SourceFormat: progress.sourceFormat,
		TableID: definition.ID, Table: definition.Name,
		FieldIDs: fieldIDs, Columns: columns,
		Seed: seed, Checksum: progress.checksum,
		Chunk: progress.chunk, Rows: totalRows, Offset: progress.end,
		Complete: progress.complete, Transaction: transaction.base + 1,
	}
	if err := validateKitDBImportState(*state); err != nil {
		return nil, err
	}
	return state, nil
}

func putKitDBImportState(transaction *kitDBRecordTransaction, state *kitDBImportState) error {
	if state == nil {
		return nil
	}
	key, err := kitDBImportStateKey(state.ID)
	if err != nil {
		return err
	}
	encoded, err := encodeKitDBImportState(*state)
	if err != nil {
		return err
	}
	return transaction.Put(key, encoded)
}

func equalKitDBImportStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
