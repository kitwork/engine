// Package record owns KitDB's durable relational record constants. Frontends
// may use different value representations, but they must not redefine these
// bytes because they are part of the on-disk contract.
package record

import "hash/crc32"

const (
	CatalogNamespace   byte = 0x01
	PhysicalNamespace  byte = 0x03
	RowNamespace       byte = 0x10
	ShadowRowNamespace byte = 0x11
	UniqueNamespace    byte = 0x20
	IndexNamespace     byte = 0x30

	IndexCodecV2 byte = 0x02
	IndexCodecV3 byte = 0x03

	BinaryRowVersion byte = 2
	RowHeaderSize         = 16
	RowFieldLimit         = 65_535

	ValueNil      byte = 4
	ValueBool     byte = 5
	ValueInteger  byte = 6
	ValueNumber   byte = 7
	ValueString   byte = 8
	ValueBytes    byte = 9
	ValueTime     byte = 10
	ValueDuration byte = 11
	ValueArray    byte = 12
	ValueMap      byte = 13
)

var (
	RowMagic = [4]byte{'K', 'R', 'O', 'W'}
	RowCRC   = crc32.MakeTable(crc32.Castagnoli)
)
