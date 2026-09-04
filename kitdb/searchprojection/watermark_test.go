package searchprojection

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestWatermarkDecodesAndReencodesExistingKitworkProjection(t *testing.T) {
	encoded, err := hex.DecodeString(
		"4B535741543030310100000054000000" +
			"BFBFAE56DD2B7D9237E4523E56B950C3" +
			"06E7BA3E4ECA73CBF8476E60E178BBA6" +
			"FD83000000000000593A878102000000" +
			"714F0000000000000200000000000000" +
			"EE166632",
	)
	if err != nil {
		t.Fatal(err)
	}
	watermark, err := DecodeWatermark(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if watermark.Cursor.DatabaseID != "bfbfae56dd2b7d9237e4523e56b950c3" ||
		watermark.StructID != "06e7ba3e4eca73cbf8476e60e178bba6" ||
		watermark.Cursor.Transaction != 33789 || watermark.Cursor.Checksum != 0x81873a59 ||
		watermark.IdentifierLayout != IdentifierTextInteger ||
		watermark.RowGeneration != 20337 || watermark.RowEpoch != 2 {
		t.Fatalf("decoded watermark = %#v", watermark)
	}
	roundTrip, err := EncodeWatermark(watermark)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(roundTrip, encoded) {
		t.Fatalf("round-trip watermark differs\n got %x\nwant %x", roundTrip, encoded)
	}
	corrupt := bytes.Clone(encoded)
	corrupt[50] ^= 1
	if _, err := DecodeWatermark(corrupt); !errors.Is(err, ErrInvalidWatermark) {
		t.Fatalf("corrupt watermark error = %v", err)
	}
}
