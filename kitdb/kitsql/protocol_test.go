package kitsql

import (
	"bytes"
	"database/sql/driver"
	"math"
	"testing"
	"time"
)

func TestValueCodecPreservesDatabaseTypes(t *testing.T) {
	now := time.Date(2026, time.September, 14, 9, 30, 1, 234, time.FixedZone("ICT", 7*60*60))
	tests := []struct {
		name   string
		source any
		check  func(driver.Value) bool
	}{
		{"null", nil, func(value driver.Value) bool { return value == nil }},
		{"boolean", true, func(value driver.Value) bool { return value == true }},
		{"integer", int64(math.MaxInt64), func(value driver.Value) bool { return value == int64(math.MaxInt64) }},
		{"float", 1.25, func(value driver.Value) bool { return value == 1.25 }},
		{"text", "bàn phím", func(value driver.Value) bool { return value == "bàn phím" }},
		{"bytes", []byte{0, 1, 255}, func(value driver.Value) bool { return bytes.Equal(value.([]byte), []byte{0, 1, 255}) }},
		{"timestamp", now, func(value driver.Value) bool { return value.(time.Time).Equal(now) }},
		{"duration", 3*time.Second + 2*time.Millisecond, func(value driver.Value) bool { return value == "3.002s" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := encodeValue(test.source)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := encoded.decode()
			if err != nil {
				t.Fatal(err)
			}
			if !test.check(decoded) {
				t.Fatalf("round trip = %#v", decoded)
			}
		})
	}
}

func TestValueCodecRejectsUnsafeUnsignedInteger(t *testing.T) {
	if _, err := encodeValue(uint64(math.MaxInt64) + 1); err == nil {
		t.Fatal("unsafe unsigned integer was accepted")
	}
}
