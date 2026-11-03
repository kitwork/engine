// The reader of an old KitDB file (.kitdb), what `kitwork migrate kitdb` (kitdb_copy.go) needs to read the
// catalogue and the rows of a file the engine wrote before 29/10/2026. KROW, the engine that wrote it, is gone
// (KITWORK_KITDB.md câu 13.4); what is here only reads.
package work

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/value"
)

func decodeKitDBCatalog(encoded []byte, name string) (*StructDef, error) {
	var stored StructDef
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, fmt.Errorf("kitdb: decode struct catalog %q: %w", name, err)
	}
	if stored.ID == "" || stored.Name == "" || len(stored.Fields) == 0 {
		return nil, fmt.Errorf("kitdb: struct catalog %q is incomplete", name)
	}
	stored.catalogHash = stored.Hash
	switch stored.Version {
	case 1:
		if err := normalizeLegacyKitDBFieldTags(&stored); err != nil {
			return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
		}
		stored.Version = structIRVersion
		refreshStructHash(&stored)
		stored.catalogNeedsUpgrade = true
	case structIRVersion, structIRPartitionVersion:
		if err := validateKitDBFieldTags(&stored); err != nil {
			return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
		}
		claimedHash := stored.Hash
		refreshStructHash(&stored)
		if stored.Hash != claimedHash {
			return nil, fmt.Errorf("kitdb: struct catalog %q hash mismatch", name)
		}
	default:
		return nil, fmt.Errorf("kitdb: struct catalog %q uses unsupported schema IR version %d", name, stored.Version)
	}
	hydrateKitDBStructColumns(&stored)
	if err := validateStructUniqueConstraints(&stored); err != nil {
		return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
	}
	if err := validateStructForeignConstraintShape(&stored); err != nil {
		return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
	}
	return &stored, nil
}

// kitDBDecimal reads a stored decimal exactly: its text (validKitDBDecimal's form), or a number of the VM
// by the text a write stores it as (numText).
func kitDBDecimal(item value.Value) (kitDBDecimalNumber, bool) {
	switch item.K {
	case value.String:
		return kitDBParseDecimal(item.String())
	case value.Number:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return kitDBDecimalNumber{}, false
		}
		return kitDBParseDecimal(strconv.FormatFloat(item.N, 'g', -1, 64))
	}
	return kitDBDecimalNumber{}, false
}

func decodeKitDBRow(definition *StructDef, encoded []byte) (kitDBDecodedRow, error) {
	return decodeProjectedKitDBRow(definition, encoded, nil)
}

func loadKitDBActiveRowLayout(reader kitDBReader, definition *StructDef) (uint64, uint64, error) {
	metadata, _, err := loadKitDBRowGenerationMetadata(reader, definition)
	if err != nil {
		return 0, 0, err
	}
	return metadata.Active, metadata.Epoch, nil
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

const kitDBRowMigrationPhaseCleanup = "cleanup"

func loadKitDBRowMigrationState(
	reader kitDBReader,
	definition *StructDef,
) (kitDBRowMigrationState, bool, error) {
	key, err := kitDBRowMigrationStateKey(definition)
	if err != nil {
		return kitDBRowMigrationState{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return kitDBRowMigrationState{}, found, err
	}
	state, err := decodeKitDBRowMigrationState(encoded)
	if err != nil {
		return kitDBRowMigrationState{}, false, err
	}
	source, target, err := kitDBRowMigrationDefinitions(state, definition.Name)
	if err != nil {
		return kitDBRowMigrationState{}, false, err
	}
	if source.ID != definition.ID || target.ID != definition.ID {
		return kitDBRowMigrationState{}, false, fmt.Errorf(
			"kitdb: struct %q row-migration identity mismatch", definition.Name,
		)
	}
	return state, true, nil
}

func normalizeLegacyKitDBFieldTags(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("field tags are unavailable")
	}
	order := make([]int, len(definition.Fields))
	for index := range definition.Fields {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		leftField := definition.Fields[order[left]]
		rightField := definition.Fields[order[right]]
		if leftField.Position != rightField.Position {
			return leftField.Position < rightField.Position
		}
		return leftField.Name < rightField.Name
	})
	for position, index := range order {
		definition.Fields[index].Tag = uint32(position + 1)
	}
	definition.NextFieldTag = uint32(len(definition.Fields) + 1)
	return validateKitDBFieldTags(definition)
}

func validateKitDBFieldTags(definition *StructDef) error {
	if definition == nil || len(definition.Fields) == 0 {
		return fmt.Errorf("field tags are unavailable")
	}
	if len(definition.Fields) > kitDBRowFieldLimit {
		return fmt.Errorf("field count exceeds %d", kitDBRowFieldLimit)
	}
	used := make(map[uint32]string, len(definition.Fields))
	owners := make(map[string]string, len(definition.Fields)*2)
	for _, field := range definition.Fields {
		if previous, duplicate := owners[field.Name]; duplicate && previous != field.ID {
			return fmt.Errorf("fields share name %q", field.Name)
		}
		owners[field.Name] = field.ID
	}
	var maximum uint32
	for _, field := range definition.Fields {
		if field.ID == "" || field.Name == "" {
			return fmt.Errorf("field identity is incomplete")
		}
		if typeInfo, found := kitdbsql.LookupKind(field.Kind); !found || typeInfo.Kind != field.Kind {
			return fmt.Errorf("field %q has unsupported kind %q", field.Name, field.Kind)
		}
		if field.Kind == "bigint" {
			return fmt.Errorf("field %q uses standalone BIGINT; the Kitwork VM bridge does not yet preserve full int64 values", field.Name)
		}
		if field.Tag == 0 {
			return fmt.Errorf("field %q has reserved tag 0", field.Name)
		}
		if previous, found := used[field.Tag]; found {
			return fmt.Errorf("fields %q and %q share tag %d", previous, field.Name, field.Tag)
		}
		used[field.Tag] = field.Name
		if field.Tag > maximum {
			maximum = field.Tag
		}
		previousAlias := ""
		for index, alias := range field.Aliases {
			if alias == "" || alias == field.Name {
				return fmt.Errorf("field %q has invalid alias %q", field.Name, alias)
			}
			if index != 0 && alias <= previousAlias {
				return fmt.Errorf("field %q aliases are not strictly ordered", field.Name)
			}
			previousAlias = alias
			if owner, duplicate := owners[alias]; duplicate && owner != field.ID {
				return fmt.Errorf("alias %q belongs to multiple fields", alias)
			}
			owners[alias] = field.ID
		}
	}
	if definition.NextFieldTag == 0 || definition.NextFieldTag <= maximum {
		return fmt.Errorf("next field tag %d must be greater than %d", definition.NextFieldTag, maximum)
	}
	if err := validateKitDBPartition(definition); err != nil {
		return err
	}
	return nil
}

func hydrateKitDBStructColumns(definition *StructDef) {
	if definition == nil {
		return
	}
	definition.columns = make(map[string]*ColumnSpec, len(definition.Fields))
	for _, field := range definition.Fields {
		spec := &ColumnSpec{
			kind: field.Kind, primary: field.Primary, primaryOrder: field.PrimaryOrder, notNull: field.NotNull,
			unique: field.Unique, hasDefault: field.HasDefault, def: field.Default,
			defaultNow: field.DefaultNow, touch: field.Updated,
			enumVals: append([]string(nil), field.Enum...), searchable: field.Searchable,
			searchWt: field.SearchWeight, analytics: field.Analytics, seq: uint64(field.Position + 1),
		}
		for _, member := range field.Indexes {
			index := colIndexRef{name: member.Name, id: member.ID, pos: member.Order}
			for _, condition := range member.Filter {
				index.filter = append(index.filter, indexCond{col: condition.Field, val: condition.Value})
			}
			spec.indexes = append(spec.indexes, index)
		}
		if field.Reference != nil {
			spec.fk = &fkRef{
				table: field.Reference.Struct, column: field.Reference.Field,
				onDelete: field.Reference.OnDelete, onUpdate: field.Reference.OnUpdate,
			}
		}
		definition.columns[field.Name] = spec
	}
	fieldsByTag := make(map[uint32]string, len(definition.Fields))
	for _, field := range definition.Fields {
		fieldsByTag[field.Tag] = field.Name
	}
	for _, constraint := range definition.UniqueConstraints {
		for position, tag := range constraint.Fields {
			if spec := definition.columns[fieldsByTag[tag]]; spec != nil {
				spec.uniques = append(spec.uniques, colUniqueRef{name: constraint.Name, pos: position + 1})
			}
		}
	}
	for _, constraint := range definition.ForeignConstraints {
		for position, tag := range constraint.Fields {
			if spec := definition.columns[fieldsByTag[tag]]; spec != nil {
				spec.fk = &fkRef{
					name: constraint.Name, pos: position + 1,
					table: constraint.TargetStruct, targetStructID: constraint.TargetStructID,
					targetFieldID: constraint.TargetFields[position],
					onDelete:      constraint.OnDelete, onUpdate: constraint.OnUpdate,
				}
			}
		}
	}
	if definition.Partition != nil {
		if spec := definition.columns[fieldsByTag[definition.Partition.Field]]; spec != nil {
			spec.partition = definition.Partition.Strategy
		}
	}
}

type kitDBReader interface {
	Get(key []byte) ([]byte, bool, error)
}

func kitDBRowPrefix(definition *StructDef) ([]byte, error) {
	return kitDBFixedKey(kitDBRowNamespace, definition.ID, "")
}

func kitDBFixedKey(namespace byte, structID, childID string) ([]byte, error) {
	return kitdbrecord.FixedKey(namespace, structID, childID)
}

// kitDBDecimalNumber is a decimal read from its text: sign × 0.digits × 10^exponent, its digits without a
// zero in front or behind ("35000" is 0.35 × 10^5, "0.05" is 0.5 × 10^-1); zero has sign 0 and no digits.
type kitDBDecimalNumber struct {
	sign     int
	digits   string
	exponent int64
}

// kitDBParseDecimal reads [+-]digits[.digits][e[+-]digits], with a digit on at least one side of the point.
func kitDBParseDecimal(text string) (kitDBDecimalNumber, bool) {
	position, sign := 0, 1
	if position < len(text) && (text[position] == '+' || text[position] == '-') {
		if text[position] == '-' {
			sign = -1
		}
		position++
	}
	integerStart := position
	for position < len(text) && text[position] >= '0' && text[position] <= '9' {
		position++
	}
	integer := text[integerStart:position]
	fraction := ""
	if position < len(text) && text[position] == '.' {
		position++
		fractionStart := position
		for position < len(text) && text[position] >= '0' && text[position] <= '9' {
			position++
		}
		fraction = text[fractionStart:position]
	}
	if integer == "" && fraction == "" {
		return kitDBDecimalNumber{}, false
	}
	var exponent int64
	if position < len(text) && (text[position] == 'e' || text[position] == 'E') {
		position++
		exponentSign := int64(1)
		if position < len(text) && (text[position] == '+' || text[position] == '-') {
			if text[position] == '-' {
				exponentSign = -1
			}
			position++
		}
		exponentStart := position
		for position < len(text) && text[position] >= '0' && text[position] <= '9' {
			if exponent > kitDBDecimalExponentLimit {
				return kitDBDecimalNumber{}, false
			}
			exponent = exponent*10 + int64(text[position]-'0')
			position++
		}
		if position == exponentStart {
			return kitDBDecimalNumber{}, false
		}
		exponent *= exponentSign
	}
	if position != len(text) {
		return kitDBDecimalNumber{}, false
	}
	// the digits of both sides without the zeros in front; the exponent counts the integer's digits left
	leading := len(integer) - len(strings.TrimLeft(integer, "0"))
	digits := integer[leading:] + fraction
	if len(digits) == len(fraction) {
		trimmed := strings.TrimLeft(fraction, "0")
		leading += len(fraction) - len(trimmed)
		digits = trimmed
	}
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return kitDBDecimalNumber{}, true
	}
	return kitDBDecimalNumber{sign: sign, digits: digits, exponent: int64(len(integer)-leading) + exponent}, true
}

type kitDBDecodedRow struct {
	values  map[string]value.Value
	unknown []kitDBRawField
	legacy  bool
}

// decodeProjectedKitDBRow validates the complete row envelope while only
// materializing selected known field tags. A nil projection preserves the
// full CRUD decoder behavior, including unknown fields for forward writes.
func decodeProjectedKitDBRow(
	definition *StructDef,
	encoded []byte,
	projectedTags map[uint32]struct{},
) (kitDBDecodedRow, error) {
	if err := checkKitDBRowDefinition(definition); err != nil {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: %w", err)
	}
	if len(encoded) > kitDBDocumentLimit {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: stored row exceeds %d bytes", kitDBDocumentLimit)
	}
	if len(encoded) < len(kitDBRowMagic) || !bytes.Equal(encoded[:4], kitDBRowMagic[:]) {
		decoded, err := decodeLegacyKitDBRow(definition, encoded)
		if err != nil || projectedTags == nil {
			return decoded, err
		}
		values := make(map[string]value.Value, len(projectedTags))
		for tag := range projectedTags {
			fieldIndex, known := definition.byTag[tag]
			if !known {
				continue
			}
			name := definition.Fields[fieldIndex].Name
			if item, exists := decoded.values[name]; exists {
				values[name] = item
			}
		}
		return kitDBDecodedRow{values: values, legacy: true}, nil
	}
	if len(encoded) < kitDBRowHeaderSize {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: truncated binary header")
	}
	if encoded[4] != kitDBBinaryRowVersion {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: unsupported binary version %d", encoded[4])
	}
	if encoded[5] != 0 {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: unsupported flags 0x%02x", encoded[5])
	}
	if size := binary.LittleEndian.Uint16(encoded[6:8]); size != kitDBRowHeaderSize {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: unsupported header size %d", size)
	}
	fieldCount := binary.LittleEndian.Uint32(encoded[8:12])
	if fieldCount > kitDBRowFieldLimit {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: field count exceeds %d", kitDBRowFieldLimit)
	}
	body := encoded[kitDBRowHeaderSize:]
	if got, want := crc32.Checksum(body, kitDBRowCRCTable), binary.LittleEndian.Uint32(encoded[12:16]); got != want {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: checksum mismatch")
	}

	valueCapacity := len(definition.Fields)
	if projectedTags != nil {
		valueCapacity = len(projectedTags)
	}
	decoded := kitDBDecodedRow{values: make(map[string]value.Value, valueCapacity)}
	offset := 0
	var previousTag uint32
	nodes := 0
	for index := uint32(0); index < fieldCount; index++ {
		tagValue, err := consumeKitDBUvarint(body, &offset)
		if err != nil || tagValue == 0 || tagValue > math.MaxUint32 {
			if err == nil {
				err = fmt.Errorf("invalid field tag %d", tagValue)
			}
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: %w", err)
		}
		tag := uint32(tagValue)
		if tag <= previousTag {
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: field tags are not strictly increasing")
		}
		previousTag = tag
		if offset >= len(body) {
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: truncated field type")
		}
		kind := body[offset]
		offset++
		if kind == 0 {
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: reserved field type 0")
		}
		length, err := consumeKitDBUvarint(body, &offset)
		if err != nil || length > uint64(len(body)-offset) {
			if err == nil {
				err = fmt.Errorf("field payload length %d exceeds remaining bytes", length)
			}
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: %w", err)
		}
		payload := body[offset : offset+int(length)]
		offset += int(length)
		fieldIndex, known := definition.byTag[tag]
		if !known {
			if projectedTags == nil {
				decoded.unknown = append(decoded.unknown, kitDBRawField{
					tag: tag, kind: kind, payload: bytes.Clone(payload),
				})
			}
			continue
		}
		if projectedTags != nil {
			if _, selected := projectedTags[tag]; !selected {
				continue
			}
		}
		field := definition.Fields[fieldIndex]
		item, err := decodeKitDBBinaryValue(kind, payload, 1, &nodes)
		if err != nil {
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row field %q: %w", field.Name, err)
		}
		decoded.values[field.Name] = item
	}
	if offset != len(body) {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: decode row: %d trailing bytes", len(body)-offset)
	}
	return decoded, nil
}

const kitDBShadowRowNamespace byte = 0x11

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

// kitDBRowMigrationState is a durable cursor, not another journal. A chunk's
// rewritten rows and its next cursor share one ordinary KitDB WAL commit.
type kitDBRowMigrationState struct {
	StartedAt         uint64 `json:"startedAt"`
	MigrationUnixNano int64  `json:"migrationUnixNano"`
	Rows              uint64 `json:"rows"`
	VerifiedRows      uint64 `json:"verifiedRows,omitempty"`
	CleanupRows       uint64 `json:"cleanupRows,omitempty"`
	// TotalRows is exact for legacy in-place state and an admission lower bound
	// for online shadow state, whose cardinality may change between chunks.
	TotalRows        uint64   `json:"totalRows"`
	Progress         []byte   `json:"progress,omitempty"`
	Phase            string   `json:"phase,omitempty"`
	SourceGeneration uint64   `json:"sourceGeneration,omitempty"`
	TargetGeneration uint64   `json:"targetGeneration,omitempty"`
	RetiredPrefix    []byte   `json:"retiredPrefix,omitempty"`
	PrimaryKeyChange bool     `json:"primaryKeyChange,omitempty"`
	SourceIndexEpoch uint64   `json:"sourceIndexEpoch,omitempty"`
	TargetIndexes    []string `json:"targetIndexes,omitempty"`
	RetiredIndexes   [][]byte `json:"retiredIndexes,omitempty"`
	CleanupIndex     uint64   `json:"cleanupIndex,omitempty"`
	SourceHash       string   `json:"sourceHash"`
	TargetHash       string   `json:"targetHash"`
	SourceDefinition []byte   `json:"sourceDefinition"`
	TargetDefinition []byte   `json:"targetDefinition"`
}

func kitDBRowMigrationStateKey(definition *StructDef) ([]byte, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb: row-migration definition is unavailable")
	}
	identity := stableSchemaID("physical", definition.ID+":segmented-row-migration")
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
}

func decodeKitDBRowMigrationState(encoded []byte) (kitDBRowMigrationState, error) {
	if len(encoded) < kitDBRowMigrationHeaderSize+4 ||
		!bytes.Equal(encoded[:4], kitDBRowMigrationMagic[:]) {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: invalid segmented row-migration envelope")
	}
	if encoded[4] != kitDBRowMigrationVersion {
		return kitDBRowMigrationState{}, fmt.Errorf(
			"kitdb: unsupported segmented row-migration version %d", encoded[4],
		)
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: nonzero segmented row-migration reserved byte")
	}
	payloadLength := int(binary.BigEndian.Uint32(encoded[8:12]))
	if payloadLength > kitDBRowMigrationStateLimit ||
		payloadLength != len(encoded)-kitDBRowMigrationHeaderSize-4 {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: invalid segmented row-migration payload length")
	}
	storedChecksum := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], kitDBRowMigrationCRC) != storedChecksum {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: segmented row-migration checksum mismatch")
	}
	var state kitDBRowMigrationState
	if err := json.Unmarshal(encoded[kitDBRowMigrationHeaderSize:len(encoded)-4], &state); err != nil {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: decode segmented row-migration state: %w", err)
	}
	if _, err := encodeKitDBRowMigrationState(state); err != nil {
		return kitDBRowMigrationState{}, err
	}
	return state, nil
}

func kitDBRowMigrationDefinitions(
	state kitDBRowMigrationState,
	name string,
) (*StructDef, *StructDef, error) {
	source, err := decodeKitDBCatalog(state.SourceDefinition, name)
	if err != nil {
		return nil, nil, err
	}
	target, err := decodeKitDBCatalog(state.TargetDefinition, name)
	if err != nil {
		return nil, nil, err
	}
	if source.ID != target.ID || source.Hash != state.SourceHash || target.Hash != state.TargetHash {
		return nil, nil, fmt.Errorf("kitdb: segmented row-migration definitions do not match their hashes")
	}
	return source, target, nil
}

const kitDBDocumentLimit = 8 << 20

const kitDBPhysicalNamespace byte = 0x03

func validateKitDBPartition(definition *StructDef) error {
	if definition == nil || definition.Partition == nil {
		return nil
	}
	partition := definition.Partition
	if definition.Version < structIRPartitionVersion || partition.Version != kitdbsql.PartitionVersion1 {
		return fmt.Errorf("partition policy uses an unsupported version")
	}
	var field *StructFieldDef
	for index := range definition.Fields {
		if definition.Fields[index].Tag == partition.Field {
			field = &definition.Fields[index]
			break
		}
	}
	if field == nil {
		return fmt.Errorf("partition policy references missing field tag %d", partition.Field)
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found || typeInfo.Family != kitdbsql.FamilyInteger {
		return fmt.Errorf("partition field %q must be an integer", field.Name)
	}
	switch partition.Strategy {
	case "hash":
		if partition.Buckets != kitdbsql.PartitionHashBuckets {
			return fmt.Errorf("HASH partition needs %d buckets", kitdbsql.PartitionHashBuckets)
		}
	case "range":
		if partition.Buckets != 0 {
			return fmt.Errorf("RANGE partition cannot declare hash buckets")
		}
	default:
		return fmt.Errorf("unsupported partition strategy %q", partition.Strategy)
	}
	return nil
}

const kitDBRowNamespace byte = kitdbrecord.RowNamespace

// kitDBDecimalExponentLimit bounds the exponent a decimal text is read with: past it the text is not read
// as a number and compares as text.
const kitDBDecimalExponentLimit = 1 << 40

const kitDBBinaryRowVersion byte = kitdbrecord.BinaryRowVersion

const kitDBRowHeaderSize = kitdbrecord.RowHeaderSize

const kitDBRowFieldLimit = kitdbrecord.RowFieldLimit

var kitDBRowMagic = kitdbrecord.RowMagic

var kitDBRowCRCTable = kitdbrecord.RowCRC

type kitDBRawField struct {
	tag     uint32
	kind    byte
	payload []byte
}

func decodeLegacyKitDBRow(definition *StructDef, encoded []byte) (kitDBDecodedRow, error) {
	decoded := decodeKitDBValue(encoded)
	if decoded.K == value.Invalid {
		return kitDBDecodedRow{}, fmt.Errorf("%s", decoded.Text())
	}
	if decoded.K != value.Map {
		return kitDBDecodedRow{}, fmt.Errorf("kitdb: stored row is not an object")
	}
	row := make(map[string]value.Value, len(decoded.Map()))
	for name, item := range decoded.Map() {
		fieldIndex, known := definition.byAlias[name]
		if !known {
			row[name] = item
			continue
		}
		field := definition.Fields[fieldIndex]
		if existing, duplicate := row[field.Name]; duplicate && !reflect.DeepEqual(existing, item) {
			return kitDBDecodedRow{}, fmt.Errorf("kitdb: legacy row contains conflicting values for field %q", field.Name)
		}
		row[field.Name] = item
	}
	return kitDBDecodedRow{values: row, legacy: true}, nil
}

func checkKitDBRowDefinition(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("struct definition is unavailable")
	}
	if len(definition.Fields) == 0 || len(definition.byName) != len(definition.Fields) ||
		len(definition.byTag) != len(definition.Fields) || definition.byAlias == nil {
		return fmt.Errorf("struct %q row codec is not prepared", definition.Name)
	}
	return nil
}

func decodeKitDBBinaryValue(kind byte, payload []byte, depth int, nodes *int) (value.Value, error) {
	*nodes++
	if *nodes > kitDBJSONNodeLimit {
		return value.Value{}, fmt.Errorf("value exceeds %d nodes", kitDBJSONNodeLimit)
	}
	if depth > kitDBJSONDepthLimit {
		return value.Value{}, fmt.Errorf("value exceeds depth %d", kitDBJSONDepthLimit)
	}
	switch kind {
	case kitDBValueNil:
		if len(payload) != 0 {
			return value.Value{}, fmt.Errorf("null payload is not empty")
		}
		return value.NewNil(), nil
	case kitDBValueBool:
		if len(payload) != 1 || payload[0] > 1 {
			return value.Value{}, fmt.Errorf("invalid boolean payload")
		}
		return value.New(payload[0] == 1), nil
	case kitDBValueNumber:
		if len(payload) != 8 {
			return value.Value{}, fmt.Errorf("invalid number payload length %d", len(payload))
		}
		number := math.Float64frombits(binary.LittleEndian.Uint64(payload))
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return value.Value{}, fmt.Errorf("non-finite number")
		}
		return value.New(number), nil
	case kitDBValueInteger:
		integer, size := binary.Varint(payload)
		if size <= 0 || size != len(payload) {
			return value.Value{}, fmt.Errorf("invalid integer payload")
		}
		var canonical [binary.MaxVarintLen64]byte
		if binary.PutVarint(canonical[:], integer) != size {
			return value.Value{}, fmt.Errorf("non-canonical integer payload")
		}
		return value.New(integer), nil
	case kitDBValueString:
		if !utf8.Valid(payload) {
			return value.Value{}, fmt.Errorf("string is not valid UTF-8")
		}
		return value.New(string(payload)), nil
	case kitDBValueBytes:
		return value.New(bytes.Clone(payload)), nil
	case kitDBValueTime:
		if len(payload) != 8 {
			return value.Value{}, fmt.Errorf("invalid time payload length %d", len(payload))
		}
		number := math.Float64frombits(binary.LittleEndian.Uint64(payload))
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return value.Value{}, fmt.Errorf("invalid time payload")
		}
		return value.Value{K: value.Time, N: number}, nil
	case kitDBValueDuration:
		if len(payload) != 8 {
			return value.Value{}, fmt.Errorf("invalid duration payload length %d", len(payload))
		}
		number := math.Float64frombits(binary.LittleEndian.Uint64(payload))
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return value.Value{}, fmt.Errorf("invalid duration payload")
		}
		return value.Value{K: value.Duration, N: number}, nil
	case kitDBValueArray:
		return decodeKitDBBinaryArray(payload, depth, nodes)
	case kitDBValueMap:
		return decodeKitDBBinaryMap(payload, depth, nodes)
	default:
		return value.Value{}, fmt.Errorf("unsupported value type %d", kind)
	}
}

func consumeKitDBUvarint(encoded []byte, offset *int) (uint64, error) {
	if *offset < 0 || *offset >= len(encoded) {
		return 0, fmt.Errorf("truncated unsigned integer")
	}
	value, size := binary.Uvarint(encoded[*offset:])
	if size == 0 {
		return 0, fmt.Errorf("truncated unsigned integer")
	}
	if size < 0 {
		return 0, fmt.Errorf("unsigned integer overflow")
	}
	var canonical [binary.MaxVarintLen64]byte
	if binary.PutUvarint(canonical[:], value) != size {
		return 0, fmt.Errorf("non-canonical unsigned integer")
	}
	*offset += size
	return value, nil
}

// kitDBRowGenerationMetadata is the publication authority for row storage.
// Generation zero is the original 0x10 keyspace and remains implicit so every
// existing database opens without an eager rewrite.
type kitDBRowGenerationMetadata struct {
	Epoch   uint64
	Active  uint64
	Retired [][]byte
}

func kitDBRowGenerationMetadataKey(definition *StructDef) ([]byte, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb: row-generation definition is unavailable")
	}
	identity := stableSchemaID("physical", definition.ID+":row-generations")
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
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

const kitDBRowMigrationVersion byte = 1

const kitDBRowMigrationStateLimit = 16 << 20

const kitDBRowMigrationHeaderSize = 12

var kitDBRowMigrationMagic = [4]byte{'K', 'R', 'M', 'S'}

var kitDBRowMigrationCRC = crc32.MakeTable(crc32.Castagnoli)

func encodeKitDBRowMigrationState(state kitDBRowMigrationState) ([]byte, error) {
	if state.MigrationUnixNano == 0 || state.SourceHash == "" || state.TargetHash == "" ||
		state.SourceHash == state.TargetHash || len(state.SourceDefinition) == 0 ||
		len(state.TargetDefinition) == 0 {
		return nil, fmt.Errorf("kitdb: invalid segmented row-migration state")
	}
	if state.TargetGeneration == 0 {
		if state.TotalRows <= kitDBAtomicMigrationRows || state.Rows > state.TotalRows ||
			state.Phase != "" || state.SourceGeneration != 0 ||
			state.CleanupRows != 0 || len(state.RetiredPrefix) != 0 {
			return nil, fmt.Errorf("kitdb: invalid legacy segmented row-migration state")
		}
	} else if state.TotalRows <= kitDBAtomicMigrationRows ||
		(state.Phase != kitDBRowMigrationPhaseRows && state.Phase != kitDBRowMigrationPhaseCancel &&
			state.Phase != kitDBRowMigrationPhaseCleanup && state.Phase != kitDBRowMigrationPhaseVerify) ||
		(state.Phase == kitDBRowMigrationPhaseRows && (state.VerifiedRows != 0 || state.CleanupRows != 0)) ||
		state.TargetGeneration == state.SourceGeneration || len(state.RetiredPrefix) == 0 ||
		len(state.RetiredPrefix) > kitDBRowRetiredPrefixLimit {
		return nil, fmt.Errorf("kitdb: invalid shadow row-migration state")
	}
	if !state.PrimaryKeyChange &&
		(state.VerifiedRows != 0 || state.Phase == kitDBRowMigrationPhaseVerify ||
			state.SourceIndexEpoch != 0 || len(state.TargetIndexes) != 0 ||
			len(state.RetiredIndexes) != 0 || state.CleanupIndex != 0) {
		return nil, fmt.Errorf("kitdb: ordinary row migration contains primary-key physical state")
	}
	if state.PrimaryKeyChange {
		cleanupPrefixes := len(state.RetiredIndexes) + 1
		if state.Phase == kitDBRowMigrationPhaseCancel {
			cleanupPrefixes = len(state.TargetIndexes) + 1
		}
		if state.TargetGeneration == 0 || state.CleanupIndex >= uint64(cleanupPrefixes) ||
			((state.Phase == kitDBRowMigrationPhaseRows || state.Phase == kitDBRowMigrationPhaseVerify) &&
				state.CleanupIndex != 0) || state.VerifiedRows > state.Rows ||
			(state.Phase == kitDBRowMigrationPhaseVerify && state.CleanupRows != 0) ||
			(state.Phase == kitDBRowMigrationPhaseCleanup && state.VerifiedRows != state.Rows) {
			return nil, fmt.Errorf("kitdb: invalid primary-key row-migration state")
		}
		for index, signature := range state.TargetIndexes {
			if !validSchemaID(signature) || (index != 0 && signature <= state.TargetIndexes[index-1]) {
				return nil, fmt.Errorf("kitdb: invalid primary-key target-index state")
			}
		}
		for index, prefix := range state.RetiredIndexes {
			if len(prefix) == 0 || len(prefix) > kitDBIndexRetiredPrefixLimit ||
				(index != 0 && bytes.Compare(prefix, state.RetiredIndexes[index-1]) <= 0) {
				return nil, fmt.Errorf("kitdb: invalid primary-key retired-index state")
			}
		}
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("kitdb: encode segmented row-migration state: %w", err)
	}
	if len(payload) > kitDBRowMigrationStateLimit {
		return nil, fmt.Errorf("kitdb: segmented row-migration state exceeds the format limit")
	}
	encoded := make([]byte, kitDBRowMigrationHeaderSize, kitDBRowMigrationHeaderSize+len(payload)+4)
	copy(encoded[:4], kitDBRowMigrationMagic[:])
	encoded[4] = kitDBRowMigrationVersion
	binary.BigEndian.PutUint32(encoded[8:12], uint32(len(payload)))
	encoded = append(encoded, payload...)
	encoded = binary.BigEndian.AppendUint32(encoded, crc32.Checksum(encoded, kitDBRowMigrationCRC))
	return encoded, nil
}

const kitDBIndexRetiredPrefixLimit = 256

const kitDBJSONDepthLimit = 128

const kitDBJSONNodeLimit = 100_000

func decodeKitDBValue(encoded []byte) value.Value {
	var decoded value.Value
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return value.InvalidFailure(value.DatabaseCode, "kitdb: decode stored JSON: "+err.Error())
	}
	return decoded
}

const kitDBAtomicMigrationRows = 10_000

// The kinds of a stored value, part of KROW v2's durable format. The historical iota expression started
// after three header constants, so the first value ID is 4; the values are explicit.
const (
	kitDBValueNil      byte = kitdbrecord.ValueNil
	kitDBValueBool     byte = kitdbrecord.ValueBool
	kitDBValueInteger  byte = kitdbrecord.ValueInteger
	kitDBValueNumber   byte = kitdbrecord.ValueNumber
	kitDBValueString   byte = kitdbrecord.ValueString
	kitDBValueBytes    byte = kitdbrecord.ValueBytes
	kitDBValueTime     byte = kitdbrecord.ValueTime
	kitDBValueDuration byte = kitdbrecord.ValueDuration
	kitDBValueArray    byte = kitdbrecord.ValueArray
	kitDBValueMap      byte = kitdbrecord.ValueMap
)

func decodeKitDBBinaryArray(payload []byte, depth int, nodes *int) (value.Value, error) {
	offset := 0
	count, err := consumeKitDBUvarint(payload, &offset)
	if err != nil || count > kitDBJSONNodeLimit {
		if err == nil {
			err = fmt.Errorf("array count exceeds %d", kitDBJSONNodeLimit)
		}
		return value.Value{}, err
	}
	items := make([]value.Value, 0, int(count))
	for index := uint64(0); index < count; index++ {
		kind, child, err := consumeKitDBBinaryChild(payload, &offset, depth, nodes)
		if err != nil {
			return value.Value{}, err
		}
		_ = kind
		items = append(items, child)
	}
	if offset != len(payload) {
		return value.Value{}, fmt.Errorf("array has %d trailing bytes", len(payload)-offset)
	}
	return value.New(items), nil
}

func decodeKitDBBinaryMap(payload []byte, depth int, nodes *int) (value.Value, error) {
	offset := 0
	count, err := consumeKitDBUvarint(payload, &offset)
	if err != nil || count > kitDBJSONNodeLimit {
		if err == nil {
			err = fmt.Errorf("map count exceeds %d", kitDBJSONNodeLimit)
		}
		return value.Value{}, err
	}
	fields := make(map[string]value.Value, int(count))
	previousKey := ""
	for index := uint64(0); index < count; index++ {
		length, err := consumeKitDBUvarint(payload, &offset)
		if err != nil || length > uint64(len(payload)-offset) {
			if err == nil {
				err = fmt.Errorf("map key length exceeds remaining bytes")
			}
			return value.Value{}, err
		}
		keyBytes := payload[offset : offset+int(length)]
		offset += int(length)
		if !utf8.Valid(keyBytes) {
			return value.Value{}, fmt.Errorf("map key is not valid UTF-8")
		}
		key := string(keyBytes)
		if index != 0 && key <= previousKey {
			return value.Value{}, fmt.Errorf("map keys are not strictly increasing")
		}
		previousKey = key
		_, child, err := consumeKitDBBinaryChild(payload, &offset, depth, nodes)
		if err != nil {
			return value.Value{}, err
		}
		fields[key] = child
	}
	if offset != len(payload) {
		return value.Value{}, fmt.Errorf("map has %d trailing bytes", len(payload)-offset)
	}
	return value.New(fields), nil
}

const kitDBRowRetiredPrefixLimit = 256

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

const kitDBRowMigrationPhaseRows = "rows"

const kitDBRowMigrationPhaseVerify = "verify"

const kitDBRowMigrationPhaseCancel = "cancel"

func consumeKitDBBinaryChild(payload []byte, offset *int, depth int, nodes *int) (byte, value.Value, error) {
	if *offset >= len(payload) {
		return 0, value.Value{}, fmt.Errorf("truncated nested value type")
	}
	kind := payload[*offset]
	*offset++
	if kind == 0 {
		return 0, value.Value{}, fmt.Errorf("reserved nested value type 0")
	}
	length, err := consumeKitDBUvarint(payload, offset)
	if err != nil || length > uint64(len(payload)-*offset) {
		if err == nil {
			err = fmt.Errorf("nested value length exceeds remaining bytes")
		}
		return 0, value.Value{}, err
	}
	childPayload := payload[*offset : *offset+int(length)]
	*offset += int(length)
	child, err := decodeKitDBBinaryValue(kind, childPayload, depth+1, nodes)
	return kind, child, err
}

const kitDBRowGenerationMetadataVersion = 1

const kitDBRowGenerationMetadataLimit = 16 << 20

var kitDBRowGenerationMetadataMagic = [4]byte{'K', 'R', 'G', 'M'}

var kitDBRowGenerationMetadataCRC = crc32.MakeTable(crc32.Castagnoli)
