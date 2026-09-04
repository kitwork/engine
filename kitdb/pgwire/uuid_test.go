package pgwire

import "testing"

func TestUUIDBinaryRoundTrip(t *testing.T) {
	want := "a0eebc99-9c0b-4ef8-bb6d-6bb9bd380a11"
	encoded, err := EncodeUUIDBinary("{A0EEBC99-9C0B-4EF8-BB6D-6BB9BD380A11}")
	if err != nil || len(encoded) != 16 {
		t.Fatalf("EncodeUUIDBinary = %x, %v", encoded, err)
	}
	decoded, err := DecodeUUIDBinary(encoded)
	if err != nil || decoded != want {
		t.Fatalf("DecodeUUIDBinary = %q, %v; want %q", decoded, err, want)
	}
}

func TestUUIDBinaryRejectsInvalidValues(t *testing.T) {
	if _, err := EncodeUUIDBinary("not-a-uuid"); err == nil {
		t.Fatal("invalid UUID text was accepted")
	}
	if _, err := DecodeUUIDBinary(make([]byte, 15)); err == nil {
		t.Fatal("short UUID binary value was accepted")
	}
}
