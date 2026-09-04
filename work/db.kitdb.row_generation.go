package work

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sort"
	"time"

	"github.com/kitwork/engine/value"
)

const (
	kitDBShadowRowNamespace           byte = 0x11
	kitDBRowGenerationMetadataVersion      = 1
	kitDBRowGenerationMetadataLimit        = 16 << 20
	kitDBRowRetiredPrefixLimit             = 256
)

var (
	kitDBRowGenerationMetadataMagic = [4]byte{'K', 'R', 'G', 'M'}
	kitDBRowGenerationMetadataCRC   = crc32.MakeTable(crc32.Castagnoli)
)

// kitDBRowGenerationMetadata is the publication authority for row storage.
// Generation zero is the original 0x10 keyspace and remains implicit so every
// existing database opens without an eager rewrite.
type kitDBRowGenerationMetadata struct {
	Epoch   uint64
	Active  uint64
	Retired [][]byte
}

type kitDBPhysicalRow struct {
	definition        *StructDef
	generation        uint64
	migrationUnixNano int64
}

func kitDBRowGenerationMetadataKey(definition *StructDef) ([]byte, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb: row-generation definition is unavailable")
	}
	identity := stableSchemaID("physical", definition.ID+":row-generations")
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
}

func encodeKitDBRowGenerationMetadata(metadata kitDBRowGenerationMetadata) ([]byte, error) {
	if len(metadata.Retired) > 65_535 {
		return nil, fmt.Errorf("kitdb: row-generation metadata exceeds the entry limit")
	}
	encoded := make([]byte, 0, 32+len(metadata.Retired)*48)
	encoded = append(encoded, kitDBRowGenerationMetadataMagic[:]...)
	encoded = append(encoded, kitDBRowGenerationMetadataVersion, 0, 0, 0)
	encoded = binary.BigEndian.AppendUint64(encoded, metadata.Epoch)
	encoded = binary.BigEndian.AppendUint64(encoded, metadata.Active)

	retired := make([][]byte, len(metadata.Retired))
	for index, prefix := range metadata.Retired {
		if len(prefix) == 0 || len(prefix) > kitDBRowRetiredPrefixLimit {
			return nil, fmt.Errorf("kitdb: invalid retired row prefix")
		}
		retired[index] = bytes.Clone(prefix)
	}
	sort.Slice(retired, func(left, right int) bool {
		return bytes.Compare(retired[left], retired[right]) < 0
	})
	encoded = binary.AppendUvarint(encoded, uint64(len(retired)))
	for index, prefix := range retired {
		if index != 0 && bytes.Equal(prefix, retired[index-1]) {
			return nil, fmt.Errorf("kitdb: duplicate retired row prefix")
		}
		encoded = binary.AppendUvarint(encoded, uint64(len(prefix)))
		encoded = append(encoded, prefix...)
	}
	encoded = binary.BigEndian.AppendUint32(
		encoded,
		crc32.Checksum(encoded, kitDBRowGenerationMetadataCRC),
	)
	if len(encoded) > kitDBRowGenerationMetadataLimit {
		return nil, fmt.Errorf("kitdb: row-generation metadata exceeds the format limit")
	}
	return encoded, nil
}

func decodeKitDBRowGenerationMetadata(encoded []byte) (kitDBRowGenerationMetadata, error) {
	const fixed = 4 + 4 + 8 + 8
	metadata, err := decodeKitDBRowGenerationHeader(encoded)
	if err != nil {
		return kitDBRowGenerationMetadata{}, err
	}
	position := fixed
	readUvarint := func(label string) (uint64, error) {
		if position >= len(encoded)-4 {
			return 0, fmt.Errorf("kitdb: missing row-generation %s", label)
		}
		decoded, width := binary.Uvarint(encoded[position : len(encoded)-4])
		if width <= 0 {
			return 0, fmt.Errorf("kitdb: invalid row-generation %s", label)
		}
		var canonical [binary.MaxVarintLen64]byte
		canonicalWidth := binary.PutUvarint(canonical[:], decoded)
		if canonicalWidth != width || !bytes.Equal(canonical[:canonicalWidth], encoded[position:position+width]) {
			return 0, fmt.Errorf("kitdb: non-canonical row-generation %s", label)
		}
		position += width
		return decoded, nil
	}

	retiredCount, err := readUvarint("retired count")
	if err != nil {
		return kitDBRowGenerationMetadata{}, err
	}
	if retiredCount > 65_535 {
		return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: invalid retired row-generation count")
	}
	var previous []byte
	for range retiredCount {
		length, err := readUvarint("retired prefix length")
		if err != nil {
			return kitDBRowGenerationMetadata{}, err
		}
		if length == 0 || length > kitDBRowRetiredPrefixLimit || length > uint64(len(encoded)-4-position) {
			return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: invalid retired row-generation prefix")
		}
		prefix := bytes.Clone(encoded[position : position+int(length)])
		position += int(length)
		if previous != nil && bytes.Compare(prefix, previous) <= 0 {
			return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: retired row prefixes are not strictly ordered")
		}
		metadata.Retired = append(metadata.Retired, prefix)
		previous = prefix
	}
	if position != len(encoded)-4 {
		return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: trailing row-generation metadata bytes")
	}
	return metadata, nil
}

func decodeKitDBRowGenerationHeader(encoded []byte) (kitDBRowGenerationMetadata, error) {
	const fixed = 4 + 4 + 8 + 8
	if len(encoded) < fixed+1+4 || len(encoded) > kitDBRowGenerationMetadataLimit ||
		!bytes.Equal(encoded[:4], kitDBRowGenerationMetadataMagic[:]) {
		return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: invalid row-generation metadata envelope")
	}
	if encoded[4] != kitDBRowGenerationMetadataVersion {
		return kitDBRowGenerationMetadata{}, fmt.Errorf(
			"kitdb: unsupported row-generation metadata version %d", encoded[4],
		)
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: nonzero reserved row-generation metadata byte")
	}
	storedChecksum := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], kitDBRowGenerationMetadataCRC) != storedChecksum {
		return kitDBRowGenerationMetadata{}, fmt.Errorf("kitdb: row-generation metadata checksum mismatch")
	}
	return kitDBRowGenerationMetadata{
		Epoch:  binary.BigEndian.Uint64(encoded[8:16]),
		Active: binary.BigEndian.Uint64(encoded[16:24]),
	}, nil
}

func loadKitDBRowGenerationMetadata(
	reader kitDBReader,
	definition *StructDef,
) (kitDBRowGenerationMetadata, bool, error) {
	key, err := kitDBRowGenerationMetadataKey(definition)
	if err != nil {
		return kitDBRowGenerationMetadata{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return kitDBRowGenerationMetadata{}, found, err
	}
	metadata, err := decodeKitDBRowGenerationMetadata(encoded)
	return metadata, err == nil, err
}

func loadKitDBRowGenerationEpoch(reader kitDBReader, definition *StructDef) (uint64, error) {
	key, err := kitDBRowGenerationMetadataKey(definition)
	if err != nil {
		return 0, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return 0, err
	}
	metadata, err := decodeKitDBRowGenerationHeader(encoded)
	return metadata.Epoch, err
}

func loadKitDBActiveRowLayout(reader kitDBReader, definition *StructDef) (uint64, uint64, error) {
	metadata, _, err := loadKitDBRowGenerationMetadata(reader, definition)
	if err != nil {
		return 0, 0, err
	}
	return metadata.Active, metadata.Epoch, nil
}

func loadKitDBRowLayout(
	reader kitDBReader,
	definition *StructDef,
) (uint64, []kitDBPhysicalRow, uint64, error) {
	metadata, _, err := loadKitDBRowGenerationMetadata(reader, definition)
	if err != nil {
		return 0, nil, 0, err
	}
	writes := []kitDBPhysicalRow{{definition: definition, generation: metadata.Active}}
	state, found, err := loadKitDBRowMigrationState(reader, definition)
	if err != nil || !found || state.TargetGeneration == 0 || state.Phase == kitDBRowMigrationPhaseCleanup {
		return metadata.Active, writes, metadata.Epoch, err
	}
	if state.Phase != kitDBRowMigrationPhaseRows && state.Phase != kitDBRowMigrationPhaseCancel {
		return 0, nil, 0, fmt.Errorf("kitdb: struct %q has invalid row-migration phase %q", definition.Name, state.Phase)
	}
	source, target, err := kitDBRowMigrationDefinitions(state, definition.Name)
	if err != nil {
		return 0, nil, 0, err
	}
	if metadata.Active != state.SourceGeneration {
		return 0, nil, 0, fmt.Errorf("kitdb: struct %q row-migration source generation is no longer active", definition.Name)
	}
	if definition.Hash != source.Hash {
		return 0, nil, 0, fmt.Errorf(
			"kitdb: struct %q target schema %s is not readable before row cutover",
			definition.Name,
			shortSchemaHash(definition.Hash),
		)
	}
	if state.Phase == kitDBRowMigrationPhaseCancel {
		return metadata.Active, writes, metadata.Epoch, nil
	}
	writes = []kitDBPhysicalRow{
		{definition: source, generation: state.SourceGeneration},
		{definition: target, generation: state.TargetGeneration, migrationUnixNano: state.MigrationUnixNano},
	}
	return metadata.Active, writes, metadata.Epoch, nil
}

func validateKitDBRowLayoutEpoch(reader kitDBReader, definition *StructDef, expected uint64) error {
	epoch, err := loadKitDBRowGenerationEpoch(reader, definition)
	if err != nil {
		return err
	}
	if epoch != expected {
		return fmt.Errorf(
			"kitdb: struct %q row %w from epoch %d to %d; retry the read",
			definition.Name,
			errKitDBLayoutAdvanced,
			expected,
			epoch,
		)
	}
	return nil
}

func kitDBPhysicalRowPrefix(definition *StructDef, generation uint64) ([]byte, error) {
	if generation == 0 {
		return kitDBRowPrefix(definition)
	}
	prefix, err := kitDBFixedKey(kitDBShadowRowNamespace, definition.ID, "")
	if err != nil {
		return nil, err
	}
	return binary.BigEndian.AppendUint64(prefix, generation), nil
}

func kitDBPhysicalRowKey(
	definition *StructDef,
	logicalKey []byte,
	generation uint64,
) ([]byte, error) {
	logicalPrefix, err := kitDBRowPrefix(definition)
	if err != nil {
		return nil, err
	}
	if len(logicalKey) <= len(logicalPrefix) || !bytes.HasPrefix(logicalKey, logicalPrefix) {
		return nil, fmt.Errorf("kitdb: struct %q has an invalid logical row key", definition.Name)
	}
	if generation == 0 {
		return bytes.Clone(logicalKey), nil
	}
	physicalPrefix, err := kitDBPhysicalRowPrefix(definition, generation)
	if err != nil {
		return nil, err
	}
	return append(physicalPrefix, logicalKey[len(logicalPrefix):]...), nil
}

func kitDBLogicalRowKey(
	definition *StructDef,
	physicalKey []byte,
	generation uint64,
) ([]byte, error) {
	physicalPrefix, err := kitDBPhysicalRowPrefix(definition, generation)
	if err != nil {
		return nil, err
	}
	if len(physicalKey) <= len(physicalPrefix) || !bytes.HasPrefix(physicalKey, physicalPrefix) {
		return nil, fmt.Errorf("kitdb: struct %q has an invalid physical row key", definition.Name)
	}
	if generation == 0 {
		return bytes.Clone(physicalKey), nil
	}
	logicalPrefix, err := kitDBRowPrefix(definition)
	if err != nil {
		return nil, err
	}
	return append(logicalPrefix, physicalKey[len(physicalPrefix):]...), nil
}

func (table *SchemaTable) kitDBPhysicalRowKey(logicalKey []byte) ([]byte, error) {
	if table == nil || table.definition == nil {
		return nil, fmt.Errorf("kitdb: row layout is unavailable")
	}
	return kitDBPhysicalRowKey(table.definition, logicalKey, table.rowGeneration)
}

func (table *SchemaTable) getKitDBRow(
	reader kitDBReader,
	logicalKey []byte,
) ([]byte, bool, error) {
	physicalKey, err := table.kitDBPhysicalRowKey(logicalKey)
	if err != nil {
		return nil, false, err
	}
	return reader.Get(physicalKey)
}

func (table *SchemaTable) putKitDBRow(
	transaction *kitDBRecordTransaction,
	logicalKey []byte,
	row map[string]value.Value,
	unknown []kitDBRawField,
) error {
	layouts := table.writeRows
	if len(layouts) == 0 {
		layouts = []kitDBPhysicalRow{{definition: table.definition, generation: table.rowGeneration}}
	}
	if len(layouts) > 1 {
		transaction.wakeRowMigration()
	}
	for _, layout := range layouts {
		if layout.definition == nil {
			return fmt.Errorf("kitdb: row write layout is unavailable")
		}
		values := row
		if layout.definition.Hash != table.definition.Hash {
			var err error
			values, err = transformKitDBMigrationRow(
				table.definition,
				layout.definition,
				row,
				time.Unix(0, layout.migrationUnixNano).UTC(),
			)
			if err != nil {
				return err
			}
			if err := validateKitDBRow(layout.definition, values); err != nil {
				return fmt.Errorf("kitdb: struct %q shadow row validation: %w", layout.definition.Name, err)
			}
		}
		encoded, err := encodeKitDBValidatedRow(layout.definition, values, unknown)
		if err != nil {
			return err
		}
		physicalKey, err := kitDBPhysicalRowKey(layout.definition, logicalKey, layout.generation)
		if err != nil {
			return err
		}
		if err := transaction.Put(physicalKey, encoded); err != nil {
			return err
		}
	}
	return nil
}

func (table *SchemaTable) deleteKitDBRow(
	transaction *kitDBRecordTransaction,
	logicalKey []byte,
) error {
	layouts := table.writeRows
	if len(layouts) == 0 {
		layouts = []kitDBPhysicalRow{{definition: table.definition, generation: table.rowGeneration}}
	}
	if len(layouts) > 1 {
		transaction.wakeRowMigration()
	}
	for _, layout := range layouts {
		physicalKey, err := kitDBPhysicalRowKey(layout.definition, logicalKey, layout.generation)
		if err != nil {
			return err
		}
		if err := transaction.Delete(physicalKey); err != nil {
			return err
		}
	}
	return nil
}

func getKitDBLogicalRow(
	reader kitDBReader,
	definition *StructDef,
	logicalKey []byte,
) ([]byte, bool, error) {
	generation, _, err := loadKitDBActiveRowLayout(reader, definition)
	if err != nil {
		return nil, false, err
	}
	physicalKey, err := kitDBPhysicalRowKey(definition, logicalKey, generation)
	if err != nil {
		return nil, false, err
	}
	return reader.Get(physicalKey)
}

func appendKitDBRetiredRowPrefixes(current, additions [][]byte) [][]byte {
	combined := make([][]byte, 0, len(current)+len(additions))
	seen := make(map[string]struct{}, len(current)+len(additions))
	for _, group := range [][][]byte{current, additions} {
		for _, prefix := range group {
			if _, found := seen[string(prefix)]; found {
				continue
			}
			seen[string(prefix)] = struct{}{}
			combined = append(combined, bytes.Clone(prefix))
		}
	}
	sort.Slice(combined, func(left, right int) bool {
		return bytes.Compare(combined[left], combined[right]) < 0
	})
	return combined
}
