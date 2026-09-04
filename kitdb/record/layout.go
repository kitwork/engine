package record

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
)

type ScalarKind uint8

const (
	ScalarNil ScalarKind = iota
	ScalarBool
	ScalarNumber
	ScalarText
	ScalarBytes
	ScalarTemporal
	ScalarInteger
)

type Scalar struct {
	Kind    ScalarKind
	Bool    bool
	Number  float64
	Integer int64
	Text    string
	Bytes   []byte
}

func FixedKey(namespace byte, structureID, childID string) ([]byte, error) {
	structure, err := hex.DecodeString(structureID)
	if err != nil || len(structure) != 16 {
		return nil, fmt.Errorf("kitdb: invalid struct identity")
	}
	key := make([]byte, 1, 33)
	key[0] = namespace
	key = append(key, structure...)
	if childID != "" {
		child, err := hex.DecodeString(childID)
		if err != nil || len(child) != 16 {
			return nil, fmt.Errorf("kitdb: invalid child identity")
		}
		key = append(key, child...)
	}
	return key, nil
}

// ScalarComponent is the historical self-delimiting row/unique-key codec.
func ScalarComponent(item Scalar) ([]byte, error) {
	payload := make([]byte, 1, 17)
	switch item.Kind {
	case ScalarNil:
		payload[0] = 0
	case ScalarBool:
		payload[0] = 1
		if item.Bool {
			payload = append(payload, 1)
		} else {
			payload = append(payload, 0)
		}
	case ScalarNumber:
		if math.IsNaN(item.Number) || math.IsInf(item.Number, 0) {
			return nil, fmt.Errorf("non-finite numbers cannot be keys")
		}
		payload[0] = 2
		payload = appendOrderedNumber(payload, item.Number)
	case ScalarInteger:
		payload[0] = 6
		payload = binary.BigEndian.AppendUint64(payload, uint64(item.Integer)^(uint64(1)<<63))
	case ScalarText:
		payload[0] = 3
		payload = append(payload, item.Text...)
	case ScalarBytes:
		payload[0] = 4
		payload = append(payload, item.Bytes...)
	case ScalarTemporal:
		if math.IsNaN(item.Number) || math.IsInf(item.Number, 0) {
			return nil, fmt.Errorf("non-finite times cannot be keys")
		}
		payload[0] = 5
		var encoded [8]byte
		binary.BigEndian.PutUint64(encoded[:], math.Float64bits(item.Number))
		payload = append(payload, encoded[:]...)
	default:
		return nil, fmt.Errorf("invalid scalar kind %d", item.Kind)
	}
	component := make([]byte, binary.MaxVarintLen64, binary.MaxVarintLen64+len(payload))
	width := binary.PutUvarint(component, uint64(len(payload)))
	component = component[:width]
	return append(component, payload...), nil
}

// OrderedScalarComponent is the zero-escaped codec used by secondary index
// keys. Lexicographic byte order matches KitDB scalar comparison order.
func OrderedScalarComponent(item Scalar) ([]byte, error) {
	component := make([]byte, 1, 18)
	switch item.Kind {
	case ScalarNil:
		component[0] = 0
	case ScalarBool:
		component[0] = 1
		if item.Bool {
			component = append(component, 1)
		} else {
			component = append(component, 0)
		}
	case ScalarNumber:
		if math.IsNaN(item.Number) || math.IsInf(item.Number, 0) {
			return nil, fmt.Errorf("non-finite numbers cannot be keys")
		}
		component[0] = 2
		component = appendOrderedNumber(component, item.Number)
	case ScalarInteger:
		component[0] = 6
		component = binary.BigEndian.AppendUint64(component, uint64(item.Integer)^(uint64(1)<<63))
	case ScalarText:
		component[0] = 3
		component = appendEscapedBytes(component, []byte(item.Text))
	case ScalarBytes:
		component[0] = 4
		component = appendEscapedBytes(component, item.Bytes)
	case ScalarTemporal:
		if math.IsNaN(item.Number) || math.IsInf(item.Number, 0) {
			return nil, fmt.Errorf("non-finite times cannot be keys")
		}
		component[0] = 5
		component = appendOrderedNumber(component, item.Number)
	default:
		return nil, fmt.Errorf("invalid scalar kind %d", item.Kind)
	}
	return component, nil
}

// OrderedScalarComponentSize validates one leading ordered scalar and returns
// its encoded size. It does not allocate, so index executors can compare a
// composite-key prefix before decoding the value of a new group.
func OrderedScalarComponentSize(encoded []byte) (int, error) {
	if len(encoded) == 0 {
		return 0, fmt.Errorf("truncated ordered scalar")
	}
	switch ScalarKind(encoded[0]) {
	case ScalarNil:
		return 1, nil
	case ScalarBool:
		if len(encoded) < 2 || encoded[1] > 1 {
			return 0, fmt.Errorf("invalid ordered boolean")
		}
		return 2, nil
	case ScalarNumber, ScalarTemporal:
		if len(encoded) < 9 {
			return 0, fmt.Errorf("truncated ordered number")
		}
		if _, err := decodeOrderedNumber(encoded[1:9]); err != nil {
			return 0, err
		}
		return 9, nil
	case ScalarInteger:
		if len(encoded) < 9 {
			return 0, fmt.Errorf("truncated ordered integer")
		}
		return 9, nil
	case ScalarText, ScalarBytes:
		for position := 1; position < len(encoded); position++ {
			if encoded[position] != 0 {
				continue
			}
			if position+1 >= len(encoded) {
				return 0, fmt.Errorf("truncated ordered byte escape")
			}
			switch encoded[position+1] {
			case 0:
				return position + 2, nil
			case 0xff:
				position++
			default:
				return 0, fmt.Errorf("invalid ordered byte escape")
			}
		}
		return 0, fmt.Errorf("unterminated ordered bytes")
	default:
		return 0, fmt.Errorf("invalid ordered scalar kind %d", encoded[0])
	}
}

// DecodeOrderedScalarComponent decodes one canonical leading component and
// returns the number of bytes consumed. Trailing bytes belong to the next
// component in a composite index key.
func DecodeOrderedScalarComponent(encoded []byte) (Scalar, int, error) {
	size, err := OrderedScalarComponentSize(encoded)
	if err != nil {
		return Scalar{}, 0, err
	}
	kind := ScalarKind(encoded[0])
	switch kind {
	case ScalarNil:
		return Scalar{Kind: kind}, size, nil
	case ScalarBool:
		return Scalar{Kind: kind, Bool: encoded[1] != 0}, size, nil
	case ScalarNumber, ScalarTemporal:
		number, err := decodeOrderedNumber(encoded[1:9])
		if err != nil {
			return Scalar{}, 0, err
		}
		return Scalar{Kind: kind, Number: number}, size, nil
	case ScalarInteger:
		integer := int64(binary.BigEndian.Uint64(encoded[1:9]) ^ (uint64(1) << 63))
		return Scalar{Kind: kind, Integer: integer}, size, nil
	case ScalarText, ScalarBytes:
		decoded := make([]byte, 0, size-3)
		for position := 1; position < size-2; position++ {
			if encoded[position] == 0 {
				decoded = append(decoded, 0)
				position++
				continue
			}
			decoded = append(decoded, encoded[position])
		}
		if kind == ScalarText {
			return Scalar{Kind: kind, Text: string(decoded)}, size, nil
		}
		return Scalar{Kind: kind, Bytes: decoded}, size, nil
	default:
		return Scalar{}, 0, fmt.Errorf("invalid ordered scalar kind %d", kind)
	}
}

func PrefixEnd(prefix []byte) []byte {
	if len(prefix) == 0 {
		return nil
	}
	end := append([]byte(nil), prefix...)
	for position := len(end) - 1; position >= 0; position-- {
		if end[position] != 0xff {
			end[position]++
			return end[:position+1]
		}
	}
	return nil
}

func appendOrderedNumber(target []byte, number float64) []byte {
	if number == 0 {
		number = 0
	}
	bits := math.Float64bits(number)
	if bits&(uint64(1)<<63) != 0 {
		bits = ^bits
	} else {
		bits ^= uint64(1) << 63
	}
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], bits)
	return append(target, encoded[:]...)
}

func decodeOrderedNumber(encoded []byte) (float64, error) {
	if len(encoded) != 8 {
		return 0, fmt.Errorf("invalid ordered number length")
	}
	bits := binary.BigEndian.Uint64(encoded)
	if bits&(uint64(1)<<63) != 0 {
		bits ^= uint64(1) << 63
	} else {
		bits = ^bits
	}
	number := math.Float64frombits(bits)
	if math.IsNaN(number) || math.IsInf(number, 0) || number == 0 && bits != 0 {
		return 0, fmt.Errorf("invalid ordered number")
	}
	return number, nil
}

func appendEscapedBytes(target, source []byte) []byte {
	for _, next := range source {
		if next == 0 {
			target = append(target, 0, 0xff)
			continue
		}
		target = append(target, next)
	}
	return append(target, 0, 0)
}
