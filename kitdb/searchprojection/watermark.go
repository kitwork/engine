// Package searchprojection owns the durable boundary shared by KitDB row
// storage and rebuildable full-text projections. It deliberately contains no
// Kitwork runtime or SQL adapter state.
package searchprojection

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/crc32"

	kitdb "github.com/kitwork/engine/kitdb"
)

const (
	WatermarkVersion = uint32(1)
	WatermarkSize    = 84
)

type IdentifierLayout uint32

const (
	IdentifierLogicalRowKey IdentifierLayout = 1
	IdentifierTextInteger   IdentifierLayout = 2
)

var (
	watermarkMagic = [8]byte{'K', 'S', 'W', 'A', 'T', '0', '0', '1'}
	watermarkCRC   = crc32.MakeTable(crc32.Castagnoli)

	ErrInvalidWatermark = errors.New("invalid KitDB search watermark")
)

// Watermark identifies the exact source boundary fully represented by one
// committed search generation.
type Watermark struct {
	Cursor           kitdb.HistoryCursor
	StructID         string
	IdentifierLayout IdentifierLayout
	RowGeneration    uint64
	RowEpoch         uint64
}

func ValidIdentifierLayout(layout IdentifierLayout) bool {
	return layout == IdentifierLogicalRowKey || layout == IdentifierTextInteger
}

func EncodeWatermark(watermark Watermark) ([]byte, error) {
	databaseID, err := hex.DecodeString(watermark.Cursor.DatabaseID)
	if err != nil || len(databaseID) != 16 {
		return nil, fmt.Errorf("%w: invalid database identity", ErrInvalidWatermark)
	}
	structID, err := hex.DecodeString(watermark.StructID)
	if err != nil || len(structID) != 16 {
		return nil, fmt.Errorf("%w: invalid struct identity", ErrInvalidWatermark)
	}
	if !ValidIdentifierLayout(watermark.IdentifierLayout) {
		return nil, fmt.Errorf("%w: invalid identifier layout", ErrInvalidWatermark)
	}
	encoded := make([]byte, WatermarkSize)
	copy(encoded[:8], watermarkMagic[:])
	binary.LittleEndian.PutUint32(encoded[8:12], WatermarkVersion)
	binary.LittleEndian.PutUint32(encoded[12:16], WatermarkSize)
	copy(encoded[16:32], databaseID)
	copy(encoded[32:48], structID)
	binary.LittleEndian.PutUint64(encoded[48:56], watermark.Cursor.Transaction)
	binary.LittleEndian.PutUint32(encoded[56:60], watermark.Cursor.Checksum)
	binary.LittleEndian.PutUint32(encoded[60:64], uint32(watermark.IdentifierLayout))
	binary.LittleEndian.PutUint64(encoded[64:72], watermark.RowGeneration)
	binary.LittleEndian.PutUint64(encoded[72:80], watermark.RowEpoch)
	binary.LittleEndian.PutUint32(encoded[80:84], crc32.Checksum(encoded[:80], watermarkCRC))
	return encoded, nil
}

func DecodeWatermark(encoded []byte) (Watermark, error) {
	if len(encoded) != WatermarkSize {
		return Watermark{}, fmt.Errorf("%w: size %d", ErrInvalidWatermark, len(encoded))
	}
	if string(encoded[:8]) != string(watermarkMagic[:]) ||
		binary.LittleEndian.Uint32(encoded[8:12]) != WatermarkVersion ||
		binary.LittleEndian.Uint32(encoded[12:16]) != WatermarkSize {
		return Watermark{}, fmt.Errorf("%w: unsupported envelope", ErrInvalidWatermark)
	}
	layout := IdentifierLayout(binary.LittleEndian.Uint32(encoded[60:64]))
	if !ValidIdentifierLayout(layout) {
		return Watermark{}, fmt.Errorf("%w: unsupported identifier layout", ErrInvalidWatermark)
	}
	want := binary.LittleEndian.Uint32(encoded[80:84])
	if got := crc32.Checksum(encoded[:80], watermarkCRC); got != want {
		return Watermark{}, fmt.Errorf(
			"%w: checksum %08x, want %08x", ErrInvalidWatermark, got, want,
		)
	}
	return Watermark{
		Cursor: kitdb.HistoryCursor{
			DatabaseID:  hex.EncodeToString(encoded[16:32]),
			Transaction: binary.LittleEndian.Uint64(encoded[48:56]),
			Checksum:    binary.LittleEndian.Uint32(encoded[56:60]),
		},
		StructID:         hex.EncodeToString(encoded[32:48]),
		IdentifierLayout: layout,
		RowGeneration:    binary.LittleEndian.Uint64(encoded[64:72]),
		RowEpoch:         binary.LittleEndian.Uint64(encoded[72:80]),
	}, nil
}
