package work

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"

	searchprojection "github.com/kitwork/engine/kitdb/searchprojection"
	"github.com/kitwork/engine/value"
)

type kitDBSearchIdentifierLayout = searchprojection.IdentifierLayout

const (
	kitDBSearchIdentifierLayoutLogicalRowKey = searchprojection.IdentifierLogicalRowKey
	kitDBSearchIdentifierLayoutTextInteger   = searchprojection.IdentifierTextInteger

	kitDBSearchLogicalSignaturePrefix     = "id:logical-row-key-v1|"
	kitDBSearchTextIntegerSignaturePrefix = "id:text-integer-v1|"
)

func validKitDBSearchIdentifierLayout(layout kitDBSearchIdentifierLayout) bool {
	return searchprojection.ValidIdentifierLayout(layout)
}

func (t *SchemaTable) proveKitDBSearchSignature(signature string) string {
	prefix := kitDBSearchLogicalSignaturePrefix
	if t.kitDBSearchIdentifierLayout() == kitDBSearchIdentifierLayoutTextInteger {
		prefix = kitDBSearchTextIntegerSignaturePrefix
	}
	if strings.HasPrefix(signature, prefix) {
		return signature
	}
	return prefix + signature
}

func (t *SchemaTable) verifyKitDBSearchSignatureProof(signature string) (string, bool) {
	prefix := kitDBSearchLogicalSignaturePrefix
	if t.kitDBSearchIdentifierLayout() == kitDBSearchIdentifierLayoutTextInteger {
		prefix = kitDBSearchTextIntegerSignaturePrefix
	}
	boundary, found := strings.CutPrefix(signature, prefix)
	return boundary, found && boundary != ""
}

func (t *SchemaTable) kitDBSearchIdentifierLayout() kitDBSearchIdentifierLayout {
	if t != nil && t.definition != nil {
		primary := t.definition.primaryFields()
		if len(primary) == 2 && primary[0].Kind == "text" &&
			(primary[1].Kind == "integer" || primary[1].Kind == "serial") {
			return kitDBSearchIdentifierLayoutTextInteger
		}
	}
	return kitDBSearchIdentifierLayoutLogicalRowKey
}

func (t *SchemaTable) kitDBSearchDocumentID(row kitDBStoredRow) (string, error) {
	if len(row.key) == 0 {
		return "", fmt.Errorf("search source row has an empty logical key")
	}
	if t.kitDBSearchIdentifierLayout() == kitDBSearchIdentifierLayoutTextInteger {
		text, integer, err := t.decodeKitDBTextIntegerSearchKey(row.key)
		if err != nil {
			return "", err
		}
		return encodeKitDBTextIntegerSearchIdentifier(text, integer), nil
	}
	return "k:" + hex.EncodeToString(row.key), nil
}

func (t *SchemaTable) kitDBSearchRowKey(identifier string) ([]byte, error) {
	if t.kitDBSearchIdentifierLayout() == kitDBSearchIdentifierLayoutTextInteger {
		text, integer, err := decodeKitDBTextIntegerSearchIdentifier(identifier)
		if err != nil {
			return nil, err
		}
		primary := t.definition.primaryFields()
		rowKey, err := kitDBRowKeyForRow(t.definition, map[string]value.Value{
			primary[0].Name: value.New(text),
			primary[1].Name: value.New(integer),
		})
		if err != nil {
			return nil, err
		}
		if canonical := encodeKitDBTextIntegerSearchIdentifier(text, integer); identifier != canonical {
			return nil, fmt.Errorf("KitDB search identifier is not canonical")
		}
		return rowKey, nil
	}

	if !strings.HasPrefix(identifier, "k:") {
		return nil, fmt.Errorf("KitDB search identifier has an invalid prefix")
	}
	rowKey, err := hex.DecodeString(strings.TrimPrefix(identifier, "k:"))
	if err != nil {
		return nil, fmt.Errorf("decode KitDB search identifier: %w", err)
	}
	prefix, err := kitDBRowPrefix(t.definition)
	if err != nil {
		return nil, err
	}
	if len(rowKey) <= len(prefix) || !bytes.HasPrefix(rowKey, prefix) {
		return nil, fmt.Errorf("KitDB search identifier is outside struct %q", t.table)
	}
	return rowKey, nil
}

func (t *SchemaTable) decodeKitDBTextIntegerSearchKey(rowKey []byte) (string, int64, error) {
	prefix, err := kitDBRowPrefix(t.definition)
	if err != nil {
		return "", 0, err
	}
	if len(rowKey) <= len(prefix) || !bytes.HasPrefix(rowKey, prefix) {
		return "", 0, fmt.Errorf("KitDB search row key is outside struct %q", t.table)
	}
	remaining := rowKey[len(prefix):]
	textPayload, remaining, err := consumeKitDBSearchKeyComponent(remaining)
	if err != nil {
		return "", 0, err
	}
	integerPayload, remaining, err := consumeKitDBSearchKeyComponent(remaining)
	if err != nil {
		return "", 0, err
	}
	if len(remaining) != 0 || len(textPayload) == 0 || textPayload[0] != 3 ||
		len(integerPayload) != 9 || integerPayload[0] != 2 {
		return "", 0, fmt.Errorf("KitDB search row key has an invalid text/integer composite identity")
	}
	ordered := binary.BigEndian.Uint64(integerPayload[1:])
	bits := ordered ^ uint64(1<<63)
	if ordered&uint64(1<<63) == 0 {
		bits = ^ordered
	}
	number := math.Float64frombits(bits)
	if math.IsNaN(number) || math.IsInf(number, 0) || math.Trunc(number) != number ||
		number < -9223372036854775808 || number >= 9223372036854775808 {
		return "", 0, fmt.Errorf("KitDB search row key integer is outside int64")
	}
	integer := int64(number)
	encoded, err := kitDBScalarComponent(value.New(integer))
	if err != nil || !bytes.Equal(encoded, appendKitDBSearchComponent(nil, integerPayload)) {
		return "", 0, fmt.Errorf("KitDB search row key integer is not canonical")
	}
	return string(textPayload[1:]), integer, nil
}

func consumeKitDBSearchKeyComponent(encoded []byte) (payload, remaining []byte, err error) {
	length, size := binary.Uvarint(encoded)
	if size <= 0 || length == 0 || length > uint64(len(encoded)-size) {
		return nil, nil, fmt.Errorf("KitDB search row key has an invalid scalar component")
	}
	end := size + int(length)
	return encoded[size:end], encoded[end:], nil
}

func appendKitDBSearchComponent(target, payload []byte) []byte {
	target = binary.AppendUvarint(target, uint64(len(payload)))
	return append(target, payload...)
}

func encodeKitDBTextIntegerSearchIdentifier(text string, integer int64) string {
	var ordered [8]byte
	binary.BigEndian.PutUint64(ordered[:], uint64(integer)^uint64(1<<63))
	return hex.EncodeToString([]byte(text)) + ":" + hex.EncodeToString(ordered[:])
}

func (t *SchemaTable) kitDBLeadingTextSearchIdentifierPrefix(field, text string) (string, bool) {
	if t == nil || t.definition == nil ||
		t.kitDBSearchIdentifierLayout() != kitDBSearchIdentifierLayoutTextInteger {
		return "", false
	}
	primary := t.definition.primaryFields()
	if len(primary) != 2 || !strings.EqualFold(primary[0].Name, field) {
		return "", false
	}
	return hex.EncodeToString([]byte(text)) + ":", true
}

func decodeKitDBTextIntegerSearchIdentifier(identifier string) (string, int64, error) {
	separator := len(identifier) - 17
	if separator < 0 || identifier[separator] != ':' || separator&1 != 0 {
		return "", 0, fmt.Errorf("KitDB search identifier has an invalid text/integer layout")
	}
	text, err := hex.DecodeString(identifier[:separator])
	if err != nil {
		return "", 0, fmt.Errorf("decode KitDB search identifier text: %w", err)
	}
	ordered, err := strconv.ParseUint(identifier[separator+1:], 16, 64)
	if err != nil {
		return "", 0, fmt.Errorf("decode KitDB search identifier integer: %w", err)
	}
	return string(text), int64(ordered ^ uint64(1<<63)), nil
}
