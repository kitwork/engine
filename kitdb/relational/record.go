package relational

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"math"
	"sort"
	"time"
	"unicode/utf8"

	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const (
	rowNamespace       byte = kitdbrecord.RowNamespace
	shadowRowNamespace byte = kitdbrecord.ShadowRowNamespace
	uniqueNamespace    byte = kitdbrecord.UniqueNamespace
	physicalNamespace  byte = kitdbrecord.PhysicalNamespace

	binaryRowVersion byte = kitdbrecord.BinaryRowVersion
	rowHeaderSize         = kitdbrecord.RowHeaderSize
	rowFieldLimit         = kitdbrecord.RowFieldLimit
	documentLimit         = 8 << 20
	valueDepthLimit       = 128
	valueNodeLimit        = 100_000
)

const (
	// These IDs are already durable in KROW v2 files. They intentionally
	// start at 4 because the original encoder's iota followed three header
	// constants in one block. Never renumber them.
	valueNil      byte = kitdbrecord.ValueNil
	valueBool     byte = kitdbrecord.ValueBool
	valueInteger  byte = kitdbrecord.ValueInteger
	valueNumber   byte = kitdbrecord.ValueNumber
	valueString   byte = kitdbrecord.ValueString
	valueBytes    byte = kitdbrecord.ValueBytes
	valueTime     byte = kitdbrecord.ValueTime
	valueDuration byte = kitdbrecord.ValueDuration
	valueArray    byte = kitdbrecord.ValueArray
	valueMap      byte = kitdbrecord.ValueMap
)

var (
	rowMagic    = kitdbrecord.RowMagic
	rowCRCTable = kitdbrecord.RowCRC
)

type rawField struct {
	tag     uint32
	kind    byte
	payload []byte
}

type decodedRow struct {
	values  map[string]any
	unknown []rawField
}

type projectedRowDecoder struct {
	schema        kitdbsql.Schema
	byTag         map[uint32]kitdbsql.Field
	projectedTags map[uint32]struct{}
	capacity      int
}

func newProjectedRowDecoder(
	schema kitdbsql.Schema,
	projectedTags map[uint32]struct{},
) *projectedRowDecoder {
	byTag := make(map[uint32]kitdbsql.Field, len(schema.Fields))
	for _, field := range schema.Fields {
		byTag[field.Tag] = field
	}
	capacity := len(schema.Fields)
	if projectedTags != nil {
		capacity = len(projectedTags)
	}
	return &projectedRowDecoder{
		schema: schema, byTag: byTag, projectedTags: projectedTags, capacity: capacity,
	}
}

type encodedField struct {
	tag     uint32
	kind    byte
	payload []byte
}

func encodeRow(schema kitdbsql.Schema, row map[string]any, unknown []rawField) ([]byte, error) {
	byName := make(map[string]kitdbsql.Field, len(schema.Fields))
	for _, field := range schema.Fields {
		byName[field.Name] = field
	}
	used := make(map[uint32]string, len(row)+len(unknown))
	fields := make([]encodedField, 0, len(row)+len(unknown))
	nodes := 0
	for name, item := range row {
		field, found := byName[name]
		if !found {
			return nil, fmt.Errorf("kitdb: struct %q has no field %q", schema.Name, name)
		}
		kind, payload, err := encodeValue(item, 1, &nodes)
		if err != nil {
			return nil, fmt.Errorf("kitdb: encode row field %q: %w", name, err)
		}
		fields = append(fields, encodedField{tag: field.Tag, kind: kind, payload: payload})
		used[field.Tag] = name
	}
	for _, field := range unknown {
		if field.tag == 0 || field.kind == 0 {
			return nil, fmt.Errorf("kitdb: unknown row field has a reserved tag or type")
		}
		if owner := used[field.tag]; owner != "" {
			return nil, fmt.Errorf("kitdb: row field tag %d conflicts with %q", field.tag, owner)
		}
		used[field.tag] = "<unknown>"
		fields = append(fields, encodedField{tag: field.tag, kind: field.kind, payload: bytes.Clone(field.payload)})
	}
	if len(fields) > rowFieldLimit {
		return nil, fmt.Errorf("kitdb: row field count exceeds %d", rowFieldLimit)
	}
	sort.Slice(fields, func(left, right int) bool { return fields[left].tag < fields[right].tag })
	body := make([]byte, 0, len(fields)*12)
	for _, field := range fields {
		body = binary.AppendUvarint(body, uint64(field.tag))
		body = append(body, field.kind)
		body = binary.AppendUvarint(body, uint64(len(field.payload)))
		body = append(body, field.payload...)
		if rowHeaderSize+len(body) > documentLimit {
			return nil, fmt.Errorf("kitdb: row exceeds %d bytes", documentLimit)
		}
	}
	encoded := make([]byte, rowHeaderSize, rowHeaderSize+len(body))
	copy(encoded[:4], rowMagic[:])
	encoded[4] = binaryRowVersion
	binary.LittleEndian.PutUint16(encoded[6:8], rowHeaderSize)
	binary.LittleEndian.PutUint32(encoded[8:12], uint32(len(fields)))
	binary.LittleEndian.PutUint32(encoded[12:16], crc32.Checksum(body, rowCRCTable))
	return append(encoded, body...), nil
}

func decodeRow(schema kitdbsql.Schema, encoded []byte) (decodedRow, error) {
	return decodeProjectedRow(schema, encoded, nil)
}

// decodeProjectedRow validates the complete durable row while materializing
// only selected field tags. A nil projection preserves the CRUD decoder,
// including unknown fields needed by forward-compatible rewrites.
func decodeProjectedRow(
	schema kitdbsql.Schema,
	encoded []byte,
	projectedTags map[uint32]struct{},
) (decodedRow, error) {
	return newProjectedRowDecoder(schema, projectedTags).decode(encoded)
}

func (decoder *projectedRowDecoder) decode(encoded []byte) (decodedRow, error) {
	if len(encoded) > documentLimit {
		return decodedRow{}, fmt.Errorf("kitdb: stored row exceeds %d bytes", documentLimit)
	}
	if len(encoded) < 4 || !bytes.Equal(encoded[:4], rowMagic[:]) {
		decoded, err := decodeLegacyRow(decoder.schema, encoded)
		if err != nil || decoder.projectedTags == nil {
			return decoded, err
		}
		values := make(map[string]any, decoder.capacity)
		for _, field := range decoder.schema.Fields {
			if _, selected := decoder.projectedTags[field.Tag]; !selected {
				continue
			}
			if item, found := decoded.values[field.Name]; found {
				values[field.Name] = item
			}
		}
		return decodedRow{values: values}, nil
	}
	if len(encoded) < rowHeaderSize {
		return decodedRow{}, fmt.Errorf("kitdb: truncated row header")
	}
	if encoded[4] != binaryRowVersion {
		return decodedRow{}, fmt.Errorf("kitdb: unsupported row version %d", encoded[4])
	}
	if encoded[5] != 0 || binary.LittleEndian.Uint16(encoded[6:8]) != rowHeaderSize {
		return decodedRow{}, fmt.Errorf("kitdb: unsupported row header")
	}
	count := binary.LittleEndian.Uint32(encoded[8:12])
	if count > rowFieldLimit {
		return decodedRow{}, fmt.Errorf("kitdb: row field count exceeds %d", rowFieldLimit)
	}
	body := encoded[rowHeaderSize:]
	if crc32.Checksum(body, rowCRCTable) != binary.LittleEndian.Uint32(encoded[12:16]) {
		return decodedRow{}, fmt.Errorf("kitdb: row checksum mismatch")
	}
	result := decodedRow{values: make(map[string]any, decoder.capacity)}
	offset := 0
	var previous uint32
	nodes := 0
	for range count {
		tagValue, err := consumeUvarint(body, &offset)
		if err != nil || tagValue == 0 || tagValue > math.MaxUint32 {
			if err == nil {
				err = fmt.Errorf("invalid field tag %d", tagValue)
			}
			return decodedRow{}, fmt.Errorf("kitdb: decode row: %w", err)
		}
		tag := uint32(tagValue)
		if tag <= previous {
			return decodedRow{}, fmt.Errorf("kitdb: row field tags are not strictly increasing")
		}
		previous = tag
		if offset >= len(body) {
			return decodedRow{}, fmt.Errorf("kitdb: truncated row field type")
		}
		kind := body[offset]
		offset++
		if kind == 0 {
			return decodedRow{}, fmt.Errorf("kitdb: reserved row field type")
		}
		length, err := consumeUvarint(body, &offset)
		if err != nil || length > uint64(len(body)-offset) {
			if err == nil {
				err = fmt.Errorf("field payload exceeds remaining bytes")
			}
			return decodedRow{}, fmt.Errorf("kitdb: decode row: %w", err)
		}
		payload := body[offset : offset+int(length)]
		offset += int(length)
		field, known := decoder.byTag[tag]
		if !known {
			if decoder.projectedTags == nil {
				result.unknown = append(result.unknown, rawField{tag: tag, kind: kind, payload: bytes.Clone(payload)})
			}
			continue
		}
		if decoder.projectedTags != nil {
			if _, selected := decoder.projectedTags[tag]; !selected {
				continue
			}
		}
		item, err := decodeValue(kind, payload, 1, &nodes)
		if err != nil {
			return decodedRow{}, fmt.Errorf("kitdb: decode row field %q: %w", field.Name, err)
		}
		result.values[field.Name] = item
	}
	if offset != len(body) {
		return decodedRow{}, fmt.Errorf("kitdb: row has %d trailing bytes", len(body)-offset)
	}
	return result, nil
}

func decodeLegacyRow(schema kitdbsql.Schema, encoded []byte) (decodedRow, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return decodedRow{}, fmt.Errorf("kitdb: decode legacy row: %w", err)
	}
	values := make(map[string]any, len(object))
	for name, item := range object {
		canonical, _, found := schema.FieldByName(name)
		if !found {
			values[name] = normalizeJSONNumber(item)
			continue
		}
		if _, duplicate := values[canonical]; duplicate {
			return decodedRow{}, fmt.Errorf("kitdb: legacy row contains duplicate field %q", canonical)
		}
		values[canonical] = normalizeJSONNumber(item)
	}
	return decodedRow{values: values}, nil
}

func normalizeJSONNumber(item any) any {
	switch current := item.(type) {
	case json.Number:
		if integer, err := current.Int64(); err == nil {
			return integer
		}
		if number, err := current.Float64(); err == nil {
			return number
		}
		return current.String()
	case []any:
		for index := range current {
			current[index] = normalizeJSONNumber(current[index])
		}
	case map[string]any:
		for name, value := range current {
			current[name] = normalizeJSONNumber(value)
		}
	}
	return item
}

func encodeValue(item any, depth int, nodes *int) (byte, []byte, error) {
	*nodes++
	if *nodes > valueNodeLimit {
		return 0, nil, fmt.Errorf("value exceeds %d nodes", valueNodeLimit)
	}
	if depth > valueDepthLimit {
		return 0, nil, fmt.Errorf("value exceeds depth %d", valueDepthLimit)
	}
	switch current := item.(type) {
	case nil:
		return valueNil, nil, nil
	case bool:
		if current {
			return valueBool, []byte{1}, nil
		}
		return valueBool, []byte{0}, nil
	case int:
		return encodeInteger(int64(current))
	case int8:
		return encodeInteger(int64(current))
	case int16:
		return encodeInteger(int64(current))
	case int32:
		return encodeInteger(int64(current))
	case int64:
		return encodeInteger(current)
	case uint:
		return encodeUnsigned(uint64(current))
	case uint8:
		return encodeInteger(int64(current))
	case uint16:
		return encodeInteger(int64(current))
	case uint32:
		return encodeInteger(int64(current))
	case uint64:
		return encodeUnsigned(current)
	case float32:
		return encodeNumber(float64(current))
	case float64:
		return encodeNumber(current)
	case string:
		if !utf8.ValidString(current) {
			return 0, nil, fmt.Errorf("string is not valid UTF-8")
		}
		return valueString, []byte(current), nil
	case []byte:
		return valueBytes, bytes.Clone(current), nil
	case time.Time:
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint64(payload, math.Float64bits(float64(current.UnixNano())))
		return valueTime, payload, nil
	case time.Duration:
		payload := make([]byte, 8)
		binary.LittleEndian.PutUint64(payload, math.Float64bits(float64(current.Nanoseconds())))
		return valueDuration, payload, nil
	case []any:
		payload := binary.AppendUvarint(nil, uint64(len(current)))
		for _, child := range current {
			kind, encoded, err := encodeValue(child, depth+1, nodes)
			if err != nil {
				return 0, nil, err
			}
			payload = append(payload, kind)
			payload = binary.AppendUvarint(payload, uint64(len(encoded)))
			payload = append(payload, encoded...)
			if len(payload) > documentLimit {
				return 0, nil, fmt.Errorf("value exceeds %d bytes", documentLimit)
			}
		}
		return valueArray, payload, nil
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			if !utf8.ValidString(key) {
				return 0, nil, fmt.Errorf("map key is not valid UTF-8")
			}
			keys = append(keys, key)
		}
		sort.Strings(keys)
		payload := binary.AppendUvarint(nil, uint64(len(keys)))
		for _, key := range keys {
			kind, encoded, err := encodeValue(current[key], depth+1, nodes)
			if err != nil {
				return 0, nil, err
			}
			payload = binary.AppendUvarint(payload, uint64(len(key)))
			payload = append(payload, key...)
			payload = append(payload, kind)
			payload = binary.AppendUvarint(payload, uint64(len(encoded)))
			payload = append(payload, encoded...)
			if len(payload) > documentLimit {
				return 0, nil, fmt.Errorf("value exceeds %d bytes", documentLimit)
			}
		}
		return valueMap, payload, nil
	default:
		return 0, nil, fmt.Errorf("value type %T is not storable", item)
	}
}

func encodeInteger(integer int64) (byte, []byte, error) {
	return valueInteger, binary.AppendVarint(nil, integer), nil
}

func encodeUnsigned(integer uint64) (byte, []byte, error) {
	if integer > math.MaxInt64 {
		return 0, nil, fmt.Errorf("unsigned integer exceeds int64")
	}
	return encodeInteger(int64(integer))
}

func encodeNumber(number float64) (byte, []byte, error) {
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, nil, fmt.Errorf("non-finite number")
	}
	integer := int64(number)
	if float64(integer) == number {
		return encodeInteger(integer)
	}
	payload := make([]byte, 8)
	binary.LittleEndian.PutUint64(payload, math.Float64bits(number))
	return valueNumber, payload, nil
}

func decodeValue(kind byte, payload []byte, depth int, nodes *int) (any, error) {
	*nodes++
	if *nodes > valueNodeLimit {
		return nil, fmt.Errorf("value exceeds %d nodes", valueNodeLimit)
	}
	if depth > valueDepthLimit {
		return nil, fmt.Errorf("value exceeds depth %d", valueDepthLimit)
	}
	switch kind {
	case valueNil:
		if len(payload) != 0 {
			return nil, fmt.Errorf("null payload is not empty")
		}
		return nil, nil
	case valueBool:
		if len(payload) != 1 || payload[0] > 1 {
			return nil, fmt.Errorf("invalid boolean payload")
		}
		return payload[0] == 1, nil
	case valueInteger:
		integer, width := binary.Varint(payload)
		if width <= 0 || width != len(payload) {
			return nil, fmt.Errorf("invalid integer payload")
		}
		var canonical [binary.MaxVarintLen64]byte
		if binary.PutVarint(canonical[:], integer) != width {
			return nil, fmt.Errorf("non-canonical integer payload")
		}
		return integer, nil
	case valueNumber:
		if len(payload) != 8 {
			return nil, fmt.Errorf("invalid number payload")
		}
		number := math.Float64frombits(binary.LittleEndian.Uint64(payload))
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("non-finite number")
		}
		return number, nil
	case valueString:
		if !utf8.Valid(payload) {
			return nil, fmt.Errorf("string is not valid UTF-8")
		}
		return string(payload), nil
	case valueBytes:
		return bytes.Clone(payload), nil
	case valueTime, valueDuration:
		if len(payload) != 8 {
			return nil, fmt.Errorf("invalid temporal payload")
		}
		number := math.Float64frombits(binary.LittleEndian.Uint64(payload))
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, fmt.Errorf("invalid temporal payload")
		}
		if kind == valueTime {
			return time.Unix(0, int64(number)).UTC(), nil
		}
		return time.Duration(int64(number)), nil
	case valueArray:
		return decodeArray(payload, depth, nodes)
	case valueMap:
		return decodeMap(payload, depth, nodes)
	default:
		return nil, fmt.Errorf("unsupported value type %d", kind)
	}
}

func decodeArray(payload []byte, depth int, nodes *int) ([]any, error) {
	offset := 0
	count, err := consumeUvarint(payload, &offset)
	if err != nil || count > valueNodeLimit {
		return nil, fmt.Errorf("invalid array count")
	}
	items := make([]any, 0, int(count))
	for range count {
		child, err := consumeChild(payload, &offset, depth, nodes)
		if err != nil {
			return nil, err
		}
		items = append(items, child)
	}
	if offset != len(payload) {
		return nil, fmt.Errorf("array has trailing bytes")
	}
	return items, nil
}

func decodeMap(payload []byte, depth int, nodes *int) (map[string]any, error) {
	offset := 0
	count, err := consumeUvarint(payload, &offset)
	if err != nil || count > valueNodeLimit {
		return nil, fmt.Errorf("invalid map count")
	}
	fields := make(map[string]any, int(count))
	previous := ""
	for index := uint64(0); index < count; index++ {
		length, err := consumeUvarint(payload, &offset)
		if err != nil || length > uint64(len(payload)-offset) {
			return nil, fmt.Errorf("map key exceeds remaining bytes")
		}
		keyBytes := payload[offset : offset+int(length)]
		offset += int(length)
		if !utf8.Valid(keyBytes) {
			return nil, fmt.Errorf("map key is not valid UTF-8")
		}
		key := string(keyBytes)
		if index != 0 && key <= previous {
			return nil, fmt.Errorf("map keys are not strictly increasing")
		}
		previous = key
		child, err := consumeChild(payload, &offset, depth, nodes)
		if err != nil {
			return nil, err
		}
		fields[key] = child
	}
	if offset != len(payload) {
		return nil, fmt.Errorf("map has trailing bytes")
	}
	return fields, nil
}

func consumeChild(payload []byte, offset *int, depth int, nodes *int) (any, error) {
	if *offset >= len(payload) {
		return nil, fmt.Errorf("truncated nested value type")
	}
	kind := payload[*offset]
	*offset++
	if kind == 0 {
		return nil, fmt.Errorf("reserved nested value type")
	}
	length, err := consumeUvarint(payload, offset)
	if err != nil || length > uint64(len(payload)-*offset) {
		return nil, fmt.Errorf("nested value exceeds remaining bytes")
	}
	encoded := payload[*offset : *offset+int(length)]
	*offset += int(length)
	return decodeValue(kind, encoded, depth+1, nodes)
}

func consumeUvarint(encoded []byte, offset *int) (uint64, error) {
	if *offset < 0 || *offset >= len(encoded) {
		return 0, fmt.Errorf("truncated unsigned integer")
	}
	decoded, width := binary.Uvarint(encoded[*offset:])
	if width == 0 {
		return 0, fmt.Errorf("truncated unsigned integer")
	}
	if width < 0 {
		return 0, fmt.Errorf("unsigned integer overflow")
	}
	var canonical [binary.MaxVarintLen64]byte
	if binary.PutUvarint(canonical[:], decoded) != width {
		return 0, fmt.Errorf("non-canonical unsigned integer")
	}
	*offset += width
	return decoded, nil
}

func fixedKey(namespace byte, structID, childID string) ([]byte, error) {
	return kitdbrecord.FixedKey(namespace, structID, childID)
}

func scalarComponent(item any) ([]byte, error) {
	scalar, err := recordScalar(item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.ScalarComponent(scalar)
}

func fieldScalar(field kitdbsql.Field, item any) (kitdbrecord.Scalar, error) {
	if exactIntegerFieldKind(field.Kind) && item != nil {
		integer, err := integerValue(item)
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarInteger, Integer: integer}, err
	}
	return recordScalar(item)
}

func fieldScalarComponent(field kitdbsql.Field, item any) ([]byte, error) {
	scalar, err := fieldScalar(field, item)
	if err != nil {
		return nil, err
	}
	return kitdbrecord.ScalarComponent(scalar)
}

func recordScalar(item any) (kitdbrecord.Scalar, error) {
	switch current := item.(type) {
	case nil:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNil}, nil
	case bool:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarBool, Bool: current}, nil
	case int:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(current)}, nil
	case int8:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(current)}, nil
	case int16:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(current)}, nil
	case int32:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(current)}, nil
	case int64:
		if current < -(1<<53-1) || current > 1<<53-1 {
			return kitdbrecord.Scalar{}, fmt.Errorf("integer key exceeds the current exact range")
		}
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(current)}, nil
	case uint, uint8, uint16, uint32, uint64:
		integer, err := unsignedValue(current)
		if err != nil || integer > 1<<53-1 {
			return kitdbrecord.Scalar{}, fmt.Errorf("integer key exceeds the current exact range")
		}
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(integer)}, nil
	case float32:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: float64(current)}, nil
	case float64:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarNumber, Number: current}, nil
	case string:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarText, Text: current}, nil
	case []byte:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarBytes, Bytes: current}, nil
	case time.Time:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarTemporal, Number: float64(current.UnixNano())}, nil
	case time.Duration:
		return kitdbrecord.Scalar{Kind: kitdbrecord.ScalarTemporal, Number: float64(current.Nanoseconds())}, nil
	default:
		return kitdbrecord.Scalar{}, fmt.Errorf("value type %T is not scalar", item)
	}
}

func unsignedValue(item any) (uint64, error) {
	switch current := item.(type) {
	case uint:
		return uint64(current), nil
	case uint8:
		return uint64(current), nil
	case uint16:
		return uint64(current), nil
	case uint32:
		return uint64(current), nil
	case uint64:
		return current, nil
	default:
		return 0, fmt.Errorf("not unsigned")
	}
}

func rowPrefix(schema kitdbsql.Schema, generation uint64) ([]byte, error) {
	if generation == 0 {
		return fixedKey(rowNamespace, schema.ID, "")
	}
	prefix, err := fixedKey(shadowRowNamespace, schema.ID, "")
	if err != nil {
		return nil, err
	}
	return binary.BigEndian.AppendUint64(prefix, generation), nil
}

func rowKey(schema kitdbsql.Schema, row map[string]any, generation uint64) ([]byte, error) {
	key, err := rowPrefix(schema, generation)
	if err != nil {
		return nil, err
	}
	primary := schema.PrimaryFields()
	if len(primary) == 0 {
		return nil, fmt.Errorf("kitdb: struct %q has no primary key", schema.Name)
	}
	for _, field := range primary {
		item, found := row[field.Name]
		if !found || item == nil {
			return nil, fmt.Errorf("kitdb: primary field %q is missing", field.Name)
		}
		component, err := fieldScalarComponent(field, item)
		if err != nil {
			return nil, fmt.Errorf("kitdb: primary field %q: %w", field.Name, err)
		}
		key = append(key, component...)
	}
	return key, nil
}

func uniqueKey(schema kitdbsql.Schema, identity string, values []any) ([]byte, bool, error) {
	key, err := fixedKey(uniqueNamespace, schema.ID, identity)
	if err != nil {
		return nil, false, err
	}
	fields, err := uniqueKeyFields(schema, identity)
	if err != nil || len(fields) != len(values) {
		return nil, false, fmt.Errorf("kitdb: invalid unique key %q", identity)
	}
	for position, item := range values {
		if item == nil {
			return nil, false, nil
		}
		component, err := fieldScalarComponent(fields[position], item)
		if err != nil {
			return nil, false, err
		}
		key = append(key, component...)
	}
	return key, true, nil
}

func uniqueKeyFields(schema kitdbsql.Schema, identity string) ([]kitdbsql.Field, error) {
	for _, field := range schema.Fields {
		if field.ID == identity {
			return []kitdbsql.Field{field}, nil
		}
	}
	for _, constraint := range schema.UniqueConstraints {
		if constraint.ID != identity {
			continue
		}
		fields := make([]kitdbsql.Field, len(constraint.Fields))
		for i, tag := range constraint.Fields {
			field, found := fieldByTag(schema, tag)
			if !found {
				return nil, fmt.Errorf("kitdb: unique key has unknown tag %d", tag)
			}
			fields[i] = field
		}
		return fields, nil
	}
	return nil, fmt.Errorf("kitdb: unknown unique key %q", identity)
}
