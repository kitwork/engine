package pgwire

import (
	"encoding/binary"
	"testing"
)

func TestPostgreSQLTemporalBinaryEpochsAndRoundTrips(t *testing.T) {
	date, err := EncodeDateBinary("2000-01-02")
	if err != nil || int32(binary.BigEndian.Uint32(date)) != 1 {
		t.Fatalf("DATE binary = %x, %v", date, err)
	}
	if decoded, err := DecodeDateBinary(date); err != nil || decoded != "2000-01-02" {
		t.Fatalf("DATE round trip = %q, %v", decoded, err)
	}

	clock, err := EncodeTimeBinary("01:02:03.4")
	wantClock := int64(3_723_400_000)
	if err != nil || int64(binary.BigEndian.Uint64(clock)) != wantClock {
		t.Fatalf("TIME binary = %x, %v", clock, err)
	}
	if decoded, err := DecodeTimeBinary(clock); err != nil || decoded != "01:02:03.4" {
		t.Fatalf("TIME round trip = %q, %v", decoded, err)
	}

	for _, test := range []struct {
		name         string
		text         string
		withTimezone bool
		want         string
	}{
		{"timestamp", "2000-01-01 00:00:00", false, "2000-01-01T00:00:00"},
		{"timestamptz", "2000-01-01T07:00:00+07:00", true, "2000-01-01T00:00:00Z"},
		{"fraction", "1999-12-31 23:59:59.123456", false, "1999-12-31T23:59:59.123456"},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := EncodeTimestampBinary(test.text, test.withTimezone)
			if err != nil {
				t.Fatal(err)
			}
			if test.name != "fraction" && int64(binary.BigEndian.Uint64(encoded)) != 0 {
				t.Fatalf("timestamp epoch offset = %d", int64(binary.BigEndian.Uint64(encoded)))
			}
			decoded, err := DecodeTimestampBinary(encoded, test.withTimezone)
			if err != nil || decoded != test.want {
				t.Fatalf("timestamp round trip = %q, %v; want %q", decoded, err, test.want)
			}
		})
	}

	interval, err := EncodeIntervalBinary("14 mons 3 days 04:05:06.7")
	if err != nil {
		t.Fatal(err)
	}
	if len(interval) != 16 || int32(binary.BigEndian.Uint32(interval[8:12])) != 3 ||
		int32(binary.BigEndian.Uint32(interval[12:16])) != 14 {
		t.Fatalf("INTERVAL binary = %x", interval)
	}
	if decoded, err := DecodeIntervalBinary(interval); err != nil || decoded != "14 mons 3 days 04:05:06.7" {
		t.Fatalf("INTERVAL round trip = %q, %v", decoded, err)
	}
}

func TestPostgreSQLTemporalBinaryRejectsMalformedValues(t *testing.T) {
	for _, test := range []struct {
		name string
		call func() error
	}{
		{"date", func() error { _, err := EncodeDateBinary("2026-02-30"); return err }},
		{"time", func() error { _, err := EncodeTimeBinary("25:00:00"); return err }},
		{"timestamp-zone", func() error { _, err := EncodeTimestampBinary("2026-01-01+07:00", false); return err }},
		{"date-length", func() error { _, err := DecodeDateBinary([]byte{1}); return err }},
		{"interval-length", func() error { _, err := DecodeIntervalBinary(make([]byte, 8)); return err }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.call(); err == nil {
				t.Fatal("malformed temporal value was accepted")
			}
		})
	}
}

func TestPostgreSQLIntervalBinaryRoundTripsSignedExtrema(t *testing.T) {
	encoded := make([]byte, 16)
	binary.BigEndian.PutUint64(encoded[0:8], uint64(1)<<63)
	binary.BigEndian.PutUint32(encoded[8:12], uint32(1)<<31)
	binary.BigEndian.PutUint32(encoded[12:16], uint32(1)<<31)
	text, err := DecodeIntervalBinary(encoded)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip, err := EncodeIntervalBinary(text)
	if err != nil || string(roundTrip) != string(encoded) {
		t.Fatalf("signed extrema round trip = %q %x, %v; want %x", text, roundTrip, err, encoded)
	}
}
