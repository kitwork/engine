package relational

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"hash/crc32"

	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

var rowGenerationCRC = crc32.MakeTable(crc32.Castagnoli)

type recordReader interface {
	Get(key []byte) ([]byte, bool, error)
}

func activeRowGeneration(reader recordReader, schema kitdbsql.Schema) (uint64, error) {
	generation, _, err := activeRowLayout(reader, schema)
	return generation, err
}

func activeRowLayout(reader recordReader, schema kitdbsql.Schema) (uint64, uint64, error) {
	identity := kitdbsql.StableSchemaID("physical", schema.ID+":row-generations")
	key, err := fixedKey(physicalNamespace, schema.ID, identity)
	if err != nil {
		return 0, 0, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return 0, 0, err
	}
	const fixed = 4 + 4 + 8 + 8
	if len(encoded) < fixed+1+4 || len(encoded) > 16<<20 || !bytes.Equal(encoded[:4], []byte("KRGM")) {
		return 0, 0, fmt.Errorf("kitdb: invalid row-generation metadata")
	}
	if encoded[4] != 1 || encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return 0, 0, fmt.Errorf("kitdb: unsupported row-generation metadata")
	}
	stored := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], rowGenerationCRC) != stored {
		return 0, 0, fmt.Errorf("kitdb: row-generation metadata checksum mismatch")
	}
	position := fixed
	count, err := consumeUvarint(encoded[:len(encoded)-4], &position)
	if err != nil || count > 65_535 {
		return 0, 0, fmt.Errorf("kitdb: invalid retired row-generation count")
	}
	var previous []byte
	for range count {
		length, err := consumeUvarint(encoded[:len(encoded)-4], &position)
		if err != nil || length == 0 || length > 256 || length > uint64(len(encoded)-4-position) {
			return 0, 0, fmt.Errorf("kitdb: invalid retired row-generation prefix")
		}
		prefix := encoded[position : position+int(length)]
		position += int(length)
		if previous != nil && bytes.Compare(prefix, previous) <= 0 {
			return 0, 0, fmt.Errorf("kitdb: retired row-generation prefixes are not ordered")
		}
		previous = prefix
	}
	if position != len(encoded)-4 {
		return 0, 0, fmt.Errorf("kitdb: trailing row-generation metadata")
	}
	return binary.BigEndian.Uint64(encoded[16:24]), binary.BigEndian.Uint64(encoded[8:16]), nil
}

func logicalRowKey(schema kitdbsql.Schema, physical []byte, generation uint64) ([]byte, error) {
	physicalPrefix, err := rowPrefix(schema, generation)
	if err != nil {
		return nil, err
	}
	if len(physical) <= len(physicalPrefix) || !bytes.HasPrefix(physical, physicalPrefix) {
		return nil, fmt.Errorf("kitdb: table %q has an invalid physical row key", schema.Name)
	}
	if generation == 0 {
		return bytes.Clone(physical), nil
	}
	logicalPrefix, err := rowPrefix(schema, 0)
	if err != nil {
		return nil, err
	}
	return append(logicalPrefix, physical[len(physicalPrefix):]...), nil
}

func physicalRowKey(schema kitdbsql.Schema, logical []byte, generation uint64) ([]byte, error) {
	logicalPrefix, err := rowPrefix(schema, 0)
	if err != nil {
		return nil, err
	}
	if len(logical) <= len(logicalPrefix) || !bytes.HasPrefix(logical, logicalPrefix) {
		return nil, fmt.Errorf("kitdb: table %q has an invalid logical row key", schema.Name)
	}
	if generation == 0 {
		return bytes.Clone(logical), nil
	}
	physicalPrefix, err := rowPrefix(schema, generation)
	if err != nil {
		return nil, err
	}
	return append(physicalPrefix, logical[len(logicalPrefix):]...), nil
}
