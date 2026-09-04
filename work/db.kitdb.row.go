package work

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"math"
	"reflect"
	"sort"
	"unicode/utf8"

	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	"github.com/kitwork/engine/value"
)

const (
	kitDBBinaryRowVersion byte = kitdbrecord.BinaryRowVersion
	kitDBRowHeaderSize         = kitdbrecord.RowHeaderSize
	kitDBRowFieldLimit         = kitdbrecord.RowFieldLimit

	// These IDs are part of KROW v2's durable format. The historical iota
	// expression started after three header constants, so the first value ID
	// is 4. Keep the values explicit to make reordering harmless.
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

var (
	kitDBRowMagic    = kitdbrecord.RowMagic
	kitDBRowCRCTable = kitdbrecord.RowCRC
)

type kitDBRawField struct {
	tag     uint32
	kind    byte
	payload []byte
}

type kitDBDecodedRow struct {
	values  map[string]value.Value
	unknown []kitDBRawField
	legacy  bool
}

type kitDBEncodedField struct {
	tag     uint32
	kind    byte
	payload []byte
}

func encodeKitDBRow(
	definition *StructDef,
	row map[string]value.Value,
	unknown []kitDBRawField,
) ([]byte, error) {
	if err := validateKitDBJSON(value.New(row)); err != nil {
		return nil, fmt.Errorf("kitdb: encode row: %w", err)
	}
	return encodeKitDBValidatedRow(definition, row, unknown)
}

func encodeKitDBValidatedRow(
	definition *StructDef,
	row map[string]value.Value,
	unknown []kitDBRawField,
) ([]byte, error) {
	if err := checkKitDBRowDefinition(definition); err != nil {
		return nil, fmt.Errorf("kitdb: encode row: %w", err)
	}

	usedTags := make(map[uint32]string, len(definition.Fields)+len(unknown))
	fields := make([]kitDBEncodedField, 0, len(row)+len(unknown))
	nodes := 0
	for name, item := range row {
		fieldIndex, found := definition.byName[name]
		if !found {
			return nil, fmt.Errorf("kitdb: encode row: struct %q has no field %q", definition.Name, name)
		}
		field := definition.Fields[fieldIndex]
		kind, payload, err := encodeKitDBBinaryValue(item, 1, &nodes)
		if err != nil {
			return nil, fmt.Errorf("kitdb: encode row field %q: %w", name, err)
		}
		fields = append(fields, kitDBEncodedField{tag: field.Tag, kind: kind, payload: payload})
		usedTags[field.Tag] = name
	}
	for _, raw := range unknown {
		if raw.tag == 0 || raw.kind == 0 {
			return nil, fmt.Errorf("kitdb: encode row: unknown field has a reserved tag or type")
		}
		if owner, exists := usedTags[raw.tag]; exists {
			return nil, fmt.Errorf("kitdb: encode row: field tag %d conflicts with %q", raw.tag, owner)
		}
		usedTags[raw.tag] = "<unknown>"
		fields = append(fields, kitDBEncodedField{
			tag: raw.tag, kind: raw.kind, payload: bytes.Clone(raw.payload),
		})
	}
	if len(fields) > kitDBRowFieldLimit {
		return nil, fmt.Errorf("kitdb: encode row: field count exceeds %d", kitDBRowFieldLimit)
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left].tag < fields[right].tag })

	body := make([]byte, 0, min(kitDBDocumentLimit, len(fields)*12))
	for _, field := range fields {
		body = binary.AppendUvarint(body, uint64(field.tag))
		body = append(body, field.kind)
		body = binary.AppendUvarint(body, uint64(len(field.payload)))
		body = append(body, field.payload...)
		if kitDBRowHeaderSize+len(body) > kitDBDocumentLimit {
			return nil, fmt.Errorf("kitdb: row exceeds %d bytes", kitDBDocumentLimit)
		}
	}

	encoded := make([]byte, kitDBRowHeaderSize, kitDBRowHeaderSize+len(body))
	copy(encoded[:4], kitDBRowMagic[:])
	encoded[4] = kitDBBinaryRowVersion
	encoded[5] = 0
	binary.LittleEndian.PutUint16(encoded[6:8], kitDBRowHeaderSize)
	binary.LittleEndian.PutUint32(encoded[8:12], uint32(len(fields)))
	binary.LittleEndian.PutUint32(encoded[12:16], crc32.Checksum(body, kitDBRowCRCTable))
	encoded = append(encoded, body...)
	return encoded, nil
}

func decodeKitDBRow(definition *StructDef, encoded []byte) (kitDBDecodedRow, error) {
	return decodeProjectedKitDBRow(definition, encoded, nil)
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

func encodeKitDBBinaryValue(item value.Value, depth int, nodes *int) (byte, []byte, error) {
	*nodes++
	if *nodes > kitDBJSONNodeLimit {
		return 0, nil, fmt.Errorf("value exceeds %d nodes", kitDBJSONNodeLimit)
	}
	if depth > kitDBJSONDepthLimit {
		return 0, nil, fmt.Errorf("value exceeds depth %d", kitDBJSONDepthLimit)
	}
	switch item.K {
	case value.Nil:
		return kitDBValueNil, nil, nil
	case value.Bool:
		flag := byte(0)
		if item.N != 0 {
			flag = 1
		}
		return kitDBValueBool, []byte{flag}, nil
	case value.Number:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return 0, nil, fmt.Errorf("non-finite number")
		}
		integer := int64(item.N)
		if float64(integer) == item.N {
			return kitDBValueInteger, binary.AppendVarint(nil, integer), nil
		}
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint64(payload, math.Float64bits(item.N))
		return kitDBValueNumber, payload, nil
	case value.String:
		if !utf8.ValidString(item.String()) {
			return 0, nil, fmt.Errorf("string is not valid UTF-8")
		}
		return kitDBValueString, []byte(item.String()), nil
	case value.Bytes:
		return kitDBValueBytes, bytes.Clone(item.Bytes()), nil
	case value.Time:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return 0, nil, fmt.Errorf("invalid time value")
		}
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint64(payload, math.Float64bits(item.N))
		return kitDBValueTime, payload, nil
	case value.Duration:
		if math.IsNaN(item.N) || math.IsInf(item.N, 0) {
			return 0, nil, fmt.Errorf("invalid duration value")
		}
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint64(payload, math.Float64bits(item.N))
		return kitDBValueDuration, payload, nil
	case value.Array:
		items := item.Array()
		payload := binary.AppendUvarint(nil, uint64(len(items)))
		for _, child := range items {
			kind, childPayload, err := encodeKitDBBinaryValue(child, depth+1, nodes)
			if err != nil {
				return 0, nil, err
			}
			payload = append(payload, kind)
			payload = binary.AppendUvarint(payload, uint64(len(childPayload)))
			payload = append(payload, childPayload...)
			if len(payload) > kitDBDocumentLimit {
				return 0, nil, fmt.Errorf("value exceeds %d bytes", kitDBDocumentLimit)
			}
		}
		return kitDBValueArray, payload, nil
	case value.Map:
		fields := item.Map()
		keys := make([]string, 0, len(fields))
		for key := range fields {
			if !utf8.ValidString(key) {
				return 0, nil, fmt.Errorf("map key is not valid UTF-8")
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		payload := binary.AppendUvarint(nil, uint64(len(keys)))
		for _, key := range keys {
			kind, childPayload, err := encodeKitDBBinaryValue(fields[key], depth+1, nodes)
			if err != nil {
				return 0, nil, err
			}
			payload = binary.AppendUvarint(payload, uint64(len(key)))
			payload = append(payload, key...)
			payload = append(payload, kind)
			payload = binary.AppendUvarint(payload, uint64(len(childPayload)))
			payload = append(payload, childPayload...)
			if len(payload) > kitDBDocumentLimit {
				return 0, nil, fmt.Errorf("value exceeds %d bytes", kitDBDocumentLimit)
			}
		}
		return kitDBValueMap, payload, nil
	default:
		return 0, nil, fmt.Errorf("value kind %s is not storable", item.K)
	}
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

func cloneKitDBRawFields(fields []kitDBRawField) []kitDBRawField {
	cloned := make([]kitDBRawField, len(fields))
	for index, field := range fields {
		cloned[index] = kitDBRawField{tag: field.tag, kind: field.kind, payload: bytes.Clone(field.payload)}
	}
	return cloned
}
