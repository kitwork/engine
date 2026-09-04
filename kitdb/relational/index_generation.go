package relational

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"sort"

	kitdbrecord "github.com/kitwork/engine/kitdb/record"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
)

const indexGenerationMetadataLimit = 16 << 20

var (
	indexGenerationMetadataMagic = [4]byte{'K', 'I', 'G', 'M'}
	indexGenerationMetadataCRC   = crc32.MakeTable(crc32.Castagnoli)
)

type indexGenerationMetadata struct {
	Epoch  uint64
	Active map[string]uint64
}

func indexGenerationMetadataKey(schema kitdbsql.Schema) ([]byte, error) {
	identity := kitdbsql.StableSchemaID("physical", schema.ID+":secondary-index-generations")
	return fixedKey(kitdbrecord.PhysicalNamespace, schema.ID, identity)
}

func loadIndexGenerationMetadata(
	reader recordReader,
	schema kitdbsql.Schema,
) (indexGenerationMetadata, bool, error) {
	key, err := indexGenerationMetadataKey(schema)
	if err != nil {
		return indexGenerationMetadata{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return indexGenerationMetadata{Active: make(map[string]uint64)}, found, err
	}
	metadata, err := decodeIndexGenerationMetadata(encoded)
	if err != nil {
		return indexGenerationMetadata{}, false, err
	}
	return metadata, true, nil
}

func decodeIndexGenerationMetadata(encoded []byte) (indexGenerationMetadata, error) {
	const fixed = 4 + 4 + 8
	if len(encoded) < fixed+1+1+4 || len(encoded) > indexGenerationMetadataLimit ||
		!bytes.Equal(encoded[:4], indexGenerationMetadataMagic[:]) {
		return indexGenerationMetadata{}, fmt.Errorf("kitdb: invalid index-generation metadata envelope")
	}
	if encoded[4] != 1 {
		return indexGenerationMetadata{}, fmt.Errorf(
			"kitdb: unsupported index-generation metadata version %d", encoded[4],
		)
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return indexGenerationMetadata{}, fmt.Errorf("kitdb: nonzero reserved index-generation metadata byte")
	}
	storedChecksum := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], indexGenerationMetadataCRC) != storedChecksum {
		return indexGenerationMetadata{}, fmt.Errorf("kitdb: index-generation metadata checksum mismatch")
	}

	payload := encoded[:len(encoded)-4]
	position := fixed
	activeCount, err := consumeUvarint(payload, &position)
	if err != nil || activeCount > 65_535 || activeCount > uint64((len(payload)-position)/24) {
		return indexGenerationMetadata{}, fmt.Errorf("kitdb: invalid active index-generation payload")
	}
	metadata := indexGenerationMetadata{
		Epoch:  binary.BigEndian.Uint64(encoded[8:16]),
		Active: make(map[string]uint64, int(activeCount)),
	}
	previous := ""
	for range activeCount {
		if len(payload)-position < 24 {
			return indexGenerationMetadata{}, fmt.Errorf("kitdb: truncated active index-generation payload")
		}
		signature := hex.EncodeToString(payload[position : position+16])
		position += 16
		generation := binary.BigEndian.Uint64(payload[position : position+8])
		position += 8
		if signature <= previous || generation == 0 {
			return indexGenerationMetadata{}, fmt.Errorf("kitdb: invalid active index-generation ordering")
		}
		metadata.Active[signature] = generation
		previous = signature
	}

	retiredCount, err := consumeUvarint(payload, &position)
	if err != nil || retiredCount > 65_535 {
		return indexGenerationMetadata{}, fmt.Errorf("kitdb: invalid retired index-generation count")
	}
	var previousPrefix []byte
	for range retiredCount {
		length, err := consumeUvarint(payload, &position)
		if err != nil || length == 0 || length > 256 || length > uint64(len(payload)-position) {
			return indexGenerationMetadata{}, fmt.Errorf("kitdb: invalid retired index-generation prefix")
		}
		prefix := payload[position : position+int(length)]
		position += int(length)
		if previousPrefix != nil && bytes.Compare(prefix, previousPrefix) <= 0 {
			return indexGenerationMetadata{}, fmt.Errorf("kitdb: retired index prefixes are not strictly ordered")
		}
		previousPrefix = prefix
	}
	if position != len(payload) {
		return indexGenerationMetadata{}, fmt.Errorf("kitdb: trailing index-generation metadata bytes")
	}
	return metadata, nil
}

func secondaryIndexSignature(index secondaryIndex) string {
	if index.implicit {
		return kitdbsql.StableSchemaID("index-signature", "implicit-primary:"+index.id)
	}
	type signatureFilter struct {
		Field string          `json:"field"`
		Value json.RawMessage `json:"value"`
	}
	type signatureDescriptor struct {
		Name    string            `json:"name"`
		Columns []string          `json:"columns"`
		Filter  []signatureFilter `json:"filter,omitempty"`
	}
	filters := append([]kitdbsql.IndexCondition(nil), index.filter...)
	sort.Slice(filters, func(left, right int) bool {
		if filters[left].Field != filters[right].Field {
			return filters[left].Field < filters[right].Field
		}
		return bytes.Compare(filters[left].Value, filters[right].Value) < 0
	})
	descriptor := signatureDescriptor{
		Name:    index.name,
		Columns: make([]string, len(index.fields)),
	}
	for position, field := range index.fields {
		descriptor.Columns[position] = field.Name
	}
	for _, filter := range filters {
		value := bytes.Clone(filter.Value)
		if len(value) == 0 {
			value = []byte("null")
		}
		descriptor.Filter = append(descriptor.Filter, signatureFilter{
			Field: filter.Field,
			Value: value,
		})
	}
	encoded, _ := json.Marshal(descriptor)
	return kitdbsql.StableSchemaID("index-signature", string(encoded))
}

func secondaryIndexBasePrefixForGeneration(
	schema kitdbsql.Schema,
	index secondaryIndex,
	generation uint64,
) ([]byte, error) {
	prefix, err := fixedKey(kitdbrecord.IndexNamespace, schema.ID, index.id)
	if err != nil {
		return nil, err
	}
	if generation == 0 {
		return append(prefix, 0, kitdbrecord.IndexCodecV2), nil
	}
	prefix = append(prefix, 0, kitdbrecord.IndexCodecV3)
	return binary.BigEndian.AppendUint64(prefix, generation), nil
}

func secondaryIndexPhysicalLayout(
	reader recordReader,
	schema kitdbsql.Schema,
	index secondaryIndex,
) (uint64, bool, error) {
	metadata, _, err := loadIndexGenerationMetadata(reader, schema)
	if err != nil {
		return 0, false, err
	}
	if generation := metadata.Active[secondaryIndexSignature(index)]; generation != 0 {
		return generation, true, nil
	}
	key, err := indexReadyKey(schema, index)
	if err != nil {
		return 0, false, err
	}
	encoded, found, err := reader.Get(key)
	return 0, found && bytes.Equal(encoded, indexReadyValue(schema)), err
}
