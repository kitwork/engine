package record

import (
	"bytes"
	"math"
	"reflect"
	"testing"
)

func TestOrderedScalarComponentDecode(t *testing.T) {
	values := []Scalar{
		{Kind: ScalarNil},
		{Kind: ScalarBool},
		{Kind: ScalarBool, Bool: true},
		{Kind: ScalarNumber, Number: -123.5},
		{Kind: ScalarNumber, Number: 0},
		{Kind: ScalarNumber, Number: 987.25},
		{Kind: ScalarInteger, Integer: math.MinInt64},
		{Kind: ScalarInteger, Integer: math.MaxInt64},
		{Kind: ScalarText, Text: "merchant\x00name"},
		{Kind: ScalarBytes, Bytes: []byte{0, 1, 2, 0xff}},
		{Kind: ScalarTemporal, Number: 1_725_000_000},
	}
	for _, value := range values {
		encoded, err := OrderedScalarComponent(value)
		if err != nil {
			t.Fatalf("encode %+v: %v", value, err)
		}
		joined := append(append([]byte(nil), encoded...), 0x7f)
		size, err := OrderedScalarComponentSize(joined)
		if err != nil || size != len(encoded) {
			t.Fatalf("size %+v = %d, %v; want %d", value, size, err, len(encoded))
		}
		decoded, consumed, err := DecodeOrderedScalarComponent(joined)
		if err != nil || consumed != len(encoded) || !reflect.DeepEqual(decoded, value) {
			t.Fatalf("decode %+v = %+v, %d, %v", value, decoded, consumed, err)
		}
	}
}

func TestOrderedScalarComponentDecodeRejectsMalformed(t *testing.T) {
	negativeZero := make([]byte, 9)
	negativeZero[0] = byte(ScalarNumber)
	copy(negativeZero[1:], []byte{0x7f, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	malformed := [][]byte{
		nil,
		{0xff},
		{byte(ScalarBool)},
		{byte(ScalarBool), 2},
		{byte(ScalarInteger), 1},
		{byte(ScalarText), 'a'},
		{byte(ScalarText), 0},
		{byte(ScalarText), 0, 1},
		negativeZero,
	}
	for _, encoded := range malformed {
		if size, err := OrderedScalarComponentSize(encoded); err == nil {
			t.Fatalf("malformed component accepted: %x size=%d", encoded, size)
		}
		if value, size, err := DecodeOrderedScalarComponent(encoded); err == nil {
			t.Fatalf("malformed component decoded: %x %+v size=%d", encoded, value, size)
		}
	}

	encoded, err := OrderedScalarComponent(Scalar{Kind: ScalarText, Text: "a\x00b"})
	if err != nil || !bytes.Contains(encoded, []byte{0, 0xff}) {
		t.Fatalf("zero escape = %x, %v", encoded, err)
	}
}
