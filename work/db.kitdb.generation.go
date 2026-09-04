package work

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"sort"
)

const (
	kitDBIndexGenerationMetadataVersion byte = 1
	kitDBIndexGenerationMetadataLimit        = 16 << 20
	kitDBIndexRetiredPrefixLimit             = 256
)

var (
	kitDBIndexGenerationMetadataMagic = [4]byte{'K', 'I', 'G', 'M'}
	kitDBIndexGenerationMetadataCRC   = crc32.MakeTable(crc32.Castagnoli)
)

// kitDBIndexGenerationMetadata is the physical publication authority for
// secondary indexes. Generation zero is the implicit v2 keyspace, so existing
// databases need no eager rewrite. Retired prefixes remain readable until a
// bounded cleanup transaction removes them; kernel snapshots pin their older
// contents independently of this metadata.
type kitDBIndexGenerationMetadata struct {
	Epoch   uint64
	Active  map[string]uint64
	Retired [][]byte
}

type kitDBPhysicalIndex struct {
	index      indexDef
	generation uint64
}

func kitDBIndexGenerationMetadataKey(definition *StructDef) ([]byte, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb: index-generation definition is unavailable")
	}
	identity := stableSchemaID("physical", definition.ID+":secondary-index-generations")
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
}

func encodeKitDBIndexGenerationMetadata(metadata kitDBIndexGenerationMetadata) ([]byte, error) {
	if len(metadata.Active) > 65_535 || len(metadata.Retired) > 65_535 {
		return nil, fmt.Errorf("kitdb: index-generation metadata exceeds the entry limit")
	}
	encoded := make([]byte, 0, 32+len(metadata.Active)*24+len(metadata.Retired)*48)
	encoded = append(encoded, kitDBIndexGenerationMetadataMagic[:]...)
	encoded = append(encoded, kitDBIndexGenerationMetadataVersion, 0, 0, 0)
	encoded = binary.BigEndian.AppendUint64(encoded, metadata.Epoch)

	signatures := make([]string, 0, len(metadata.Active))
	for signature, generation := range metadata.Active {
		if generation == 0 {
			return nil, fmt.Errorf("kitdb: explicit index generation cannot be zero")
		}
		signatures = append(signatures, signature)
	}
	sort.Strings(signatures)
	encoded = binary.AppendUvarint(encoded, uint64(len(signatures)))
	for _, signature := range signatures {
		identity, err := hex.DecodeString(signature)
		if err != nil || len(identity) != 16 {
			return nil, fmt.Errorf("kitdb: invalid active index signature")
		}
		encoded = append(encoded, identity...)
		encoded = binary.BigEndian.AppendUint64(encoded, metadata.Active[signature])
	}

	retired := make([][]byte, len(metadata.Retired))
	for index, prefix := range metadata.Retired {
		if len(prefix) == 0 || len(prefix) > kitDBIndexRetiredPrefixLimit {
			return nil, fmt.Errorf("kitdb: invalid retired index prefix")
		}
		retired[index] = bytes.Clone(prefix)
	}
	sort.Slice(retired, func(left, right int) bool {
		return bytes.Compare(retired[left], retired[right]) < 0
	})
	encoded = binary.AppendUvarint(encoded, uint64(len(retired)))
	for index, prefix := range retired {
		if index != 0 && bytes.Equal(prefix, retired[index-1]) {
			return nil, fmt.Errorf("kitdb: duplicate retired index prefix")
		}
		encoded = binary.AppendUvarint(encoded, uint64(len(prefix)))
		encoded = append(encoded, prefix...)
	}

	checksum := crc32.Checksum(encoded, kitDBIndexGenerationMetadataCRC)
	encoded = binary.BigEndian.AppendUint32(encoded, checksum)
	if len(encoded) > kitDBIndexGenerationMetadataLimit {
		return nil, fmt.Errorf("kitdb: index-generation metadata exceeds the format limit")
	}
	return encoded, nil
}

func decodeKitDBIndexGenerationMetadata(encoded []byte) (kitDBIndexGenerationMetadata, error) {
	const fixed = 4 + 4 + 8
	epoch, err := decodeKitDBIndexGenerationEpoch(encoded)
	if err != nil {
		return kitDBIndexGenerationMetadata{}, err
	}

	position := fixed
	readUvarint := func(label string) (uint64, error) {
		if position >= len(encoded)-4 {
			return 0, fmt.Errorf("kitdb: missing index-generation %s", label)
		}
		decoded, width := binary.Uvarint(encoded[position : len(encoded)-4])
		if width <= 0 {
			return 0, fmt.Errorf("kitdb: invalid index-generation %s", label)
		}
		var canonical [binary.MaxVarintLen64]byte
		canonicalWidth := binary.PutUvarint(canonical[:], decoded)
		if canonicalWidth != width || !bytes.Equal(canonical[:canonicalWidth], encoded[position:position+width]) {
			return 0, fmt.Errorf("kitdb: non-canonical index-generation %s", label)
		}
		position += width
		return decoded, nil
	}

	metadata := kitDBIndexGenerationMetadata{
		Epoch:  epoch,
		Active: make(map[string]uint64),
	}
	activeCount, err := readUvarint("active count")
	if err != nil {
		return kitDBIndexGenerationMetadata{}, err
	}
	if activeCount > 65_535 || activeCount > uint64((len(encoded)-4-position)/24) {
		return kitDBIndexGenerationMetadata{}, fmt.Errorf("kitdb: invalid active index-generation payload")
	}
	previous := ""
	for range activeCount {
		signature := hex.EncodeToString(encoded[position : position+16])
		position += 16
		generation := binary.BigEndian.Uint64(encoded[position : position+8])
		position += 8
		if signature <= previous || generation == 0 {
			return kitDBIndexGenerationMetadata{}, fmt.Errorf("kitdb: invalid active index-generation ordering")
		}
		metadata.Active[signature] = generation
		previous = signature
	}

	retiredCount, err := readUvarint("retired count")
	if err != nil {
		return kitDBIndexGenerationMetadata{}, err
	}
	if retiredCount > 65_535 {
		return kitDBIndexGenerationMetadata{}, fmt.Errorf("kitdb: invalid retired index-generation count")
	}
	var previousPrefix []byte
	for range retiredCount {
		length, err := readUvarint("retired prefix length")
		if err != nil {
			return kitDBIndexGenerationMetadata{}, err
		}
		if length == 0 || length > kitDBIndexRetiredPrefixLimit || length > uint64(len(encoded)-4-position) {
			return kitDBIndexGenerationMetadata{}, fmt.Errorf("kitdb: invalid retired index-generation prefix")
		}
		prefix := bytes.Clone(encoded[position : position+int(length)])
		position += int(length)
		if previousPrefix != nil && bytes.Compare(prefix, previousPrefix) <= 0 {
			return kitDBIndexGenerationMetadata{}, fmt.Errorf("kitdb: retired index prefixes are not strictly ordered")
		}
		metadata.Retired = append(metadata.Retired, prefix)
		previousPrefix = prefix
	}
	if position != len(encoded)-4 {
		return kitDBIndexGenerationMetadata{}, fmt.Errorf("kitdb: trailing index-generation metadata bytes")
	}
	return metadata, nil
}

func decodeKitDBIndexGenerationEpoch(encoded []byte) (uint64, error) {
	const fixed = 4 + 4 + 8
	if len(encoded) < fixed+4 || !bytes.Equal(encoded[:4], kitDBIndexGenerationMetadataMagic[:]) {
		return 0, fmt.Errorf("kitdb: invalid index-generation metadata envelope")
	}
	if encoded[4] != kitDBIndexGenerationMetadataVersion {
		return 0, fmt.Errorf("kitdb: unsupported index-generation metadata version %d", encoded[4])
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return 0, fmt.Errorf("kitdb: nonzero reserved index-generation metadata byte")
	}
	storedChecksum := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], kitDBIndexGenerationMetadataCRC) != storedChecksum {
		return 0, fmt.Errorf("kitdb: index-generation metadata checksum mismatch")
	}
	return binary.BigEndian.Uint64(encoded[8:16]), nil
}

func loadKitDBIndexGenerationMetadata(
	reader kitDBReader,
	definition *StructDef,
) (kitDBIndexGenerationMetadata, bool, error) {
	key, err := kitDBIndexGenerationMetadataKey(definition)
	if err != nil {
		return kitDBIndexGenerationMetadata{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return kitDBIndexGenerationMetadata{Active: make(map[string]uint64)}, found, err
	}
	metadata, err := decodeKitDBIndexGenerationMetadata(encoded)
	if err != nil {
		return kitDBIndexGenerationMetadata{}, false, err
	}
	return metadata, true, nil
}

func loadKitDBIndexGenerationEpoch(reader kitDBReader, definition *StructDef) (uint64, error) {
	key, err := kitDBIndexGenerationMetadataKey(definition)
	if err != nil {
		return 0, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return 0, err
	}
	return decodeKitDBIndexGenerationEpoch(encoded)
}

func kitDBPhysicalIndexGeneration(metadata kitDBIndexGenerationMetadata, index indexDef) uint64 {
	return metadata.Active[kitDBIndexSignature(index)]
}

func kitDBIndexGenerationMap(indexes []indexDef, metadata kitDBIndexGenerationMetadata) map[string]uint64 {
	generations := make(map[string]uint64, len(indexes))
	for _, index := range indexes {
		signature := kitDBIndexSignature(index)
		if generation := metadata.Active[signature]; generation != 0 {
			generations[signature] = generation
		}
	}
	return generations
}

func kitDBPhysicalIndexes(indexes []indexDef, generations map[string]uint64) []kitDBPhysicalIndex {
	physical := make([]kitDBPhysicalIndex, 0, len(indexes))
	for _, index := range indexes {
		physical = append(physical, kitDBPhysicalIndex{
			index: index, generation: generations[kitDBIndexSignature(index)],
		})
	}
	return physical
}

func appendKitDBRetiredPrefixes(current, additions [][]byte) [][]byte {
	combined := make([][]byte, 0, len(current)+len(additions))
	seen := make(map[string]struct{}, len(current)+len(additions))
	for _, group := range [][][]byte{current, additions} {
		for _, prefix := range group {
			if _, found := seen[string(prefix)]; found {
				continue
			}
			seen[string(prefix)] = struct{}{}
			combined = append(combined, bytes.Clone(prefix))
		}
	}
	sort.Slice(combined, func(left, right int) bool {
		return bytes.Compare(combined[left], combined[right]) < 0
	})
	return combined
}
