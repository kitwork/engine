package pgwire

import (
	"encoding/hex"
	"testing"
)

func TestPostgresNumericBinaryKnownFormsAndRoundTrip(t *testing.T) {
	tests := []struct {
		text    string
		typmod  int32
		encoded string
		decoded string
	}{
		{"12345.67", -1, "0003000100000002000109291a2c", "12345.67"},
		{"-0.0012", -1, "0001ffff40000004000c", "-0.0012"},
		{"0", -1, "0000000000000000", "0"},
		{"1.2", int32((5<<16)|2) + 4, "0002000000000002000107d0", "1.20"},
	}
	for _, test := range tests {
		t.Run(test.text, func(t *testing.T) {
			encoded, err := EncodeNumericBinary(test.text, test.typmod)
			if err != nil {
				t.Fatal(err)
			}
			if got := hex.EncodeToString(encoded); got != test.encoded {
				t.Fatalf("encoded = %s, want %s", got, test.encoded)
			}
			decoded, err := DecodeNumericBinary(encoded)
			if err != nil || decoded != test.decoded {
				t.Fatalf("decoded = %q, %v; want %q", decoded, err, test.decoded)
			}
		})
	}
}

func TestPostgresNumericBinaryRejectsInvalidInput(t *testing.T) {
	for _, source := range []string{"", "NaN", "1e3", "1.2.3"} {
		if _, err := EncodeNumericBinary(source, -1); err == nil {
			t.Fatalf("encoded invalid numeric %q", source)
		}
	}
	for _, data := range [][]byte{
		{},
		{0, 1, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0xc0, 0, 0, 0},
	} {
		if _, err := DecodeNumericBinary(data); err == nil {
			t.Fatalf("decoded invalid numeric %x", data)
		}
	}
}
