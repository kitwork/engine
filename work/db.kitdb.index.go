package work

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"reflect"
	"sort"
	"strings"

	kitdbengine "github.com/kitwork/engine/kitdb"
	"github.com/kitwork/engine/value"
)

const (
	kitDBIndexBuildLegacyVersion byte = 1
	kitDBIndexBuildVersion       byte = 2

	kitDBIndexBuildCodec  byte = 1
	kitDBIndexBuildSchema byte = 2

	kitDBIndexBuildPhaseRows    byte = 1
	kitDBIndexBuildPhaseCleanup byte = 2

	kitDBIndexBuildRowLimit      = 2_048
	kitDBIndexCleanupScanLimit   = 4_096
	kitDBIndexBuildMutationLimit = 16_384
	kitDBIndexBuildByteLimit     = 8 << 20
	kitDBIndexBuildStateLimit    = 16 << 20
)

var (
	kitDBIndexBuildMagic = [4]byte{'K', 'I', 'B', 'S'}
	kitDBIndexBuildCRC   = crc32.MakeTable(crc32.Castagnoli)
)

// kitDBIndexBuildState is durable progress, not a second journal. Every chunk
// writes its derived entries and the next cursor in one ordinary KitDB commit.
type kitDBIndexBuildState struct {
	FormatVersion    byte
	Mode             byte
	Phase            byte
	StartedAt        uint64
	Rows             uint64
	SourceHash       string
	TargetHash       string
	Progress         []byte
	SourceDefinition []byte
	TargetDefinition []byte
	Indexes          []string
	Generations      []uint64
	RetiredPrefixes  [][]byte
	CleanupIndex     uint64
}

type kitDBIndexBuildStatus struct {
	Mode               string
	Phase              string
	ProcessedRows      uint64
	IndexCount         int
	HasCursor          bool
	CleanupIndex       uint64
	CleanupTotal       int
	StartedTransaction uint64
	SourceHash         string
	TargetHash         string
	TargetPublished    bool
}

func kitDBIndexSignature(index indexDef) string {
	if index.implicitPrimary {
		// The ordered primary access path is a physical kernel contract, not a
		// Schema IR member. Its identity must survive table and field renames.
		return stableSchemaID("index-signature", "implicit-primary:"+index.id)
	}
	type filterValue struct {
		Field string      `json:"field"`
		Value value.Value `json:"value"`
	}
	type signature struct {
		Name    string        `json:"name"`
		Columns []string      `json:"columns"`
		Filter  []filterValue `json:"filter,omitempty"`
	}
	filters := make([]indexCond, len(index.filter))
	copy(filters, index.filter)
	sort.Slice(filters, func(left, right int) bool {
		if filters[left].col != filters[right].col {
			return filters[left].col < filters[right].col
		}
		leftValue, _ := json.Marshal(filters[left].val)
		rightValue, _ := json.Marshal(filters[right].val)
		return bytes.Compare(leftValue, rightValue) < 0
	})
	descriptor := signature{Name: index.name, Columns: append([]string(nil), index.columns...)}
	for _, filter := range filters {
		descriptor.Filter = append(descriptor.Filter, filterValue{Field: filter.col, Value: filter.val})
	}
	encoded, _ := json.Marshal(descriptor)
	return stableSchemaID("index-signature", string(encoded))
}

// collectKitDBIndexes returns the physical ordered indexes maintained by the
// KitDB adapter. A primary key is logically unique but its row-key codec is not
// order preserving, so KitDB adds one hidden ordered access path unless an
// ordinary non-partial index already starts with the primary field.
//
// The implicit member never enters Struct IR or SQL catalogs. This keeps schema
// hashes stable while making ORDER BY primary_key a core storage guarantee.
func collectKitDBIndexes(definition *StructDef) []indexDef {
	if definition == nil {
		return nil
	}
	indexes := collectIndexes(definition.Name, definition.columns)
	implicit, needed := kitDBImplicitPrimaryIndex(definition, indexes)
	if !needed {
		return indexes
	}
	indexes = append(indexes, implicit)
	sort.Slice(indexes, func(left, right int) bool { return indexes[left].name < indexes[right].name })
	return indexes
}

func kitDBImplicitPrimaryIndexes(definition *StructDef) []indexDef {
	if definition == nil {
		return nil
	}
	implicit, needed := kitDBImplicitPrimaryIndex(
		definition,
		collectIndexes(definition.Name, definition.columns),
	)
	if !needed {
		return nil
	}
	return []indexDef{implicit}
}

func kitDBImplicitPrimaryIndex(definition *StructDef, indexes []indexDef) (indexDef, bool) {
	primary := definition.primaryFields()
	if len(primary) == 0 {
		return indexDef{}, false
	}
	columns := make([]string, len(primary))
	identities := make([]string, len(primary))
	for position, field := range primary {
		if !kitDBSortableIndexKind(field.Kind) {
			return indexDef{}, false
		}
		columns[position] = field.Name
		identities[position] = field.ID
	}
	for _, index := range indexes {
		if len(index.filter) != 0 || len(index.columns) < len(columns) {
			continue
		}
		matched := true
		for position := range columns {
			if index.columns[position] != columns[position] {
				matched = false
				break
			}
		}
		if matched {
			return indexDef{}, false
		}
	}
	identity := definition.ID + ":implicit-primary-order"
	if len(primary) > 1 {
		identity += ":" + strings.Join(identities, ",")
	}
	return indexDef{
		name:            definition.Name + "_pkey",
		id:              stableSchemaID("index", identity),
		columns:         columns,
		implicitPrimary: true,
	}, true
}

func kitDBIndexSignatures(indexes []indexDef) []string {
	signatures := make([]string, 0, len(indexes))
	for _, index := range indexes {
		signatures = append(signatures, kitDBIndexSignature(index))
	}
	sort.Strings(signatures)
	return signatures
}

func kitDBIndexesMatchingSignatures(indexes []indexDef, signatures []string) ([]indexDef, bool) {
	wanted := make(map[string]struct{}, len(signatures))
	for _, signature := range signatures {
		wanted[signature] = struct{}{}
	}
	selected := make([]indexDef, 0, len(signatures))
	for _, index := range indexes {
		if _, found := wanted[kitDBIndexSignature(index)]; found {
			selected = append(selected, index)
		}
	}
	return selected, reflect.DeepEqual(kitDBIndexSignatures(selected), signatures)
}

// kitDBSecondaryIndexTransition separates logical catalog change from physical
// work. A reused name, changed column order, or changed partial predicate is a
// new signature and therefore receives a disjoint physical generation.
func kitDBSecondaryIndexTransition(stored, target *StructDef) (build, retire []indexDef, ok bool) {
	if stored == nil || target == nil || stored.ID != target.ID {
		return nil, nil, false
	}
	source := collectKitDBIndexes(stored)
	wanted := collectKitDBIndexes(target)
	sourceSignatures := make(map[string]struct{}, len(source))
	targetSignatures := make(map[string]struct{}, len(wanted))
	for _, index := range source {
		signature := kitDBIndexSignature(index)
		if _, duplicate := sourceSignatures[signature]; duplicate {
			return nil, nil, false
		}
		sourceSignatures[signature] = struct{}{}
	}
	for _, index := range wanted {
		signature := kitDBIndexSignature(index)
		if _, duplicate := targetSignatures[signature]; duplicate {
			return nil, nil, false
		}
		targetSignatures[signature] = struct{}{}
		if _, retained := sourceSignatures[signature]; !retained {
			build = append(build, index)
		}
	}
	for _, index := range source {
		if _, retained := targetSignatures[kitDBIndexSignature(index)]; !retained {
			retire = append(retire, index)
		}
	}
	return build, retire, true
}

func kitDBOnlyChangesSecondaryIndexes(
	steps []planStep,
	stored, target *StructDef,
) (build, retire []indexDef, ok bool) {
	if len(steps) == 0 {
		return nil, nil, false
	}
	hasIndex := false
	for _, step := range steps {
		if step.Destructive || !step.WillApply {
			return nil, nil, false
		}
		switch step.Action {
		case "index":
			hasIndex = true
		case "metadata":
			// Defaults, aliases, field order, and search declarations live in
			// the catalog. They do not change existing row bytes, so they can
			// be published atomically with an online secondary-index cutover.
		default:
			return nil, nil, false
		}
	}
	if !hasIndex || kitDBMigrationNeedsValidation(stored, target) {
		return nil, nil, false
	}
	return kitDBSecondaryIndexTransition(stored, target)
}

func kitDBIndexBuildStateKey(definition *StructDef, mode byte) ([]byte, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb: index-build definition is unavailable")
	}
	name := "secondary-index-schema-build"
	if mode == kitDBIndexBuildCodec {
		name = "secondary-index-codec-build"
	} else if mode != kitDBIndexBuildSchema {
		return nil, fmt.Errorf("kitdb: unknown index-build mode %d", mode)
	}
	identity := stableSchemaID("physical", definition.ID+":"+name)
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
}

func encodeKitDBIndexBuildState(state kitDBIndexBuildState) ([]byte, error) {
	if state.FormatVersion == kitDBIndexBuildLegacyVersion {
		return encodeLegacyKitDBIndexBuildState(state)
	}
	if state.Mode != kitDBIndexBuildCodec && state.Mode != kitDBIndexBuildSchema {
		return nil, fmt.Errorf("kitdb: invalid index-build mode %d", state.Mode)
	}
	if state.Phase != kitDBIndexBuildPhaseRows && state.Phase != kitDBIndexBuildPhaseCleanup {
		return nil, fmt.Errorf("kitdb: invalid index-build phase %d", state.Phase)
	}
	source, err := hex.DecodeString(state.SourceHash)
	if err != nil || len(source) != 32 {
		return nil, fmt.Errorf("kitdb: invalid index-build source hash")
	}
	target, err := hex.DecodeString(state.TargetHash)
	if err != nil || len(target) != 32 {
		return nil, fmt.Errorf("kitdb: invalid index-build target hash")
	}
	if len(state.Progress) > kitDBIndexBuildStateLimit ||
		len(state.SourceDefinition) > kitDBIndexBuildStateLimit ||
		len(state.TargetDefinition) > kitDBIndexBuildStateLimit {
		return nil, fmt.Errorf("kitdb: index-build state exceeds the format limit")
	}
	if len(state.Indexes) != len(state.Generations) {
		return nil, fmt.Errorf("kitdb: index-build generations do not match signatures")
	}
	if state.Mode == kitDBIndexBuildSchema && len(state.SourceDefinition) == 0 {
		return nil, fmt.Errorf("kitdb: schema index build has no source definition")
	}
	if state.Phase == kitDBIndexBuildPhaseRows && state.CleanupIndex != 0 {
		return nil, fmt.Errorf("kitdb: row build has a cleanup cursor")
	}
	if state.Mode == kitDBIndexBuildSchema && state.Phase == kitDBIndexBuildPhaseCleanup &&
		(state.CleanupIndex >= uint64(len(state.RetiredPrefixes)) || len(state.RetiredPrefixes) == 0) {
		return nil, fmt.Errorf("kitdb: schema index cleanup has no current retired generation")
	}
	encoded := make(
		[]byte,
		0,
		112+len(state.Progress)+len(state.SourceDefinition)+len(state.TargetDefinition)+len(state.Indexes)*24,
	)
	encoded = append(encoded, kitDBIndexBuildMagic[:]...)
	encoded = append(encoded, kitDBIndexBuildVersion, state.Mode, state.Phase, 0)
	encoded = binary.BigEndian.AppendUint64(encoded, state.StartedAt)
	encoded = binary.BigEndian.AppendUint64(encoded, state.Rows)
	encoded = append(encoded, source...)
	encoded = append(encoded, target...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.Progress)))
	encoded = append(encoded, state.Progress...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.SourceDefinition)))
	encoded = append(encoded, state.SourceDefinition...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.TargetDefinition)))
	encoded = append(encoded, state.TargetDefinition...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.Indexes)))
	previous := ""
	for index, signature := range state.Indexes {
		if previous != "" && signature <= previous {
			return nil, fmt.Errorf("kitdb: index-build signatures are not strictly ordered")
		}
		identity, err := hex.DecodeString(signature)
		if err != nil || len(identity) != 16 {
			return nil, fmt.Errorf("kitdb: invalid index-build index signature")
		}
		encoded = append(encoded, identity...)
		generation := state.Generations[index]
		if state.Mode == kitDBIndexBuildCodec && generation != 0 {
			return nil, fmt.Errorf("kitdb: codec build cannot name a physical generation")
		}
		if state.Mode == kitDBIndexBuildSchema && generation == 0 {
			return nil, fmt.Errorf("kitdb: schema shadow index has reserved generation zero")
		}
		encoded = binary.BigEndian.AppendUint64(encoded, generation)
		previous = signature
	}
	encoded = binary.AppendUvarint(encoded, uint64(len(state.RetiredPrefixes)))
	var previousPrefix []byte
	for _, prefix := range state.RetiredPrefixes {
		if len(prefix) == 0 || len(prefix) > kitDBIndexRetiredPrefixLimit {
			return nil, fmt.Errorf("kitdb: invalid retired index-build prefix")
		}
		if previousPrefix != nil && bytes.Compare(prefix, previousPrefix) <= 0 {
			return nil, fmt.Errorf("kitdb: retired index-build prefixes are not strictly ordered")
		}
		encoded = binary.AppendUvarint(encoded, uint64(len(prefix)))
		encoded = append(encoded, prefix...)
		previousPrefix = prefix
	}
	encoded = binary.AppendUvarint(encoded, state.CleanupIndex)
	checksum := crc32.Checksum(encoded, kitDBIndexBuildCRC)
	encoded = binary.BigEndian.AppendUint32(encoded, checksum)
	if len(encoded) > kitDBIndexBuildStateLimit {
		return nil, fmt.Errorf("kitdb: index-build state exceeds the format limit")
	}
	return encoded, nil
}

func encodeLegacyKitDBIndexBuildState(state kitDBIndexBuildState) ([]byte, error) {
	if state.Mode != kitDBIndexBuildCodec && state.Mode != kitDBIndexBuildSchema {
		return nil, fmt.Errorf("kitdb: invalid legacy index-build mode %d", state.Mode)
	}
	if state.Phase != kitDBIndexBuildPhaseRows && state.Phase != kitDBIndexBuildPhaseCleanup {
		return nil, fmt.Errorf("kitdb: invalid legacy index-build phase %d", state.Phase)
	}
	if len(state.SourceDefinition) != 0 || len(state.RetiredPrefixes) != 0 || state.CleanupIndex != 0 {
		return nil, fmt.Errorf("kitdb: legacy index-build state contains v2 fields")
	}
	if len(state.Generations) != 0 && len(state.Generations) != len(state.Indexes) {
		return nil, fmt.Errorf("kitdb: legacy index-build generations do not match signatures")
	}
	for _, generation := range state.Generations {
		if generation != 0 {
			return nil, fmt.Errorf("kitdb: legacy index-build state names a physical generation")
		}
	}
	source, err := hex.DecodeString(state.SourceHash)
	if err != nil || len(source) != 32 {
		return nil, fmt.Errorf("kitdb: invalid legacy index-build source hash")
	}
	target, err := hex.DecodeString(state.TargetHash)
	if err != nil || len(target) != 32 {
		return nil, fmt.Errorf("kitdb: invalid legacy index-build target hash")
	}
	if len(state.Progress) > kitDBIndexBuildStateLimit || len(state.TargetDefinition) > kitDBIndexBuildStateLimit {
		return nil, fmt.Errorf("kitdb: legacy index-build state exceeds the format limit")
	}
	encoded := make([]byte, 0, 96+len(state.Progress)+len(state.TargetDefinition)+len(state.Indexes)*16)
	encoded = append(encoded, kitDBIndexBuildMagic[:]...)
	encoded = append(encoded, kitDBIndexBuildLegacyVersion, state.Mode, state.Phase, 0)
	encoded = binary.BigEndian.AppendUint64(encoded, state.StartedAt)
	encoded = binary.BigEndian.AppendUint64(encoded, state.Rows)
	encoded = append(encoded, source...)
	encoded = append(encoded, target...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.Progress)))
	encoded = append(encoded, state.Progress...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.TargetDefinition)))
	encoded = append(encoded, state.TargetDefinition...)
	encoded = binary.AppendUvarint(encoded, uint64(len(state.Indexes)))
	previous := ""
	for _, signature := range state.Indexes {
		if previous != "" && signature <= previous {
			return nil, fmt.Errorf("kitdb: legacy index-build signatures are not strictly ordered")
		}
		identity, err := hex.DecodeString(signature)
		if err != nil || len(identity) != 16 {
			return nil, fmt.Errorf("kitdb: invalid legacy index-build index signature")
		}
		encoded = append(encoded, identity...)
		previous = signature
	}
	checksum := crc32.Checksum(encoded, kitDBIndexBuildCRC)
	encoded = binary.BigEndian.AppendUint32(encoded, checksum)
	if len(encoded) > kitDBIndexBuildStateLimit {
		return nil, fmt.Errorf("kitdb: legacy index-build state exceeds the format limit")
	}
	return encoded, nil
}

func decodeKitDBIndexBuildState(encoded []byte) (kitDBIndexBuildState, error) {
	const fixed = 4 + 4 + 8 + 8 + 32 + 32
	if len(encoded) < fixed+4 || !bytes.Equal(encoded[:4], kitDBIndexBuildMagic[:]) {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: invalid index-build state envelope")
	}
	if encoded[4] != kitDBIndexBuildLegacyVersion && encoded[4] != kitDBIndexBuildVersion {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: unsupported index-build state version %d", encoded[4])
	}
	if encoded[7] != 0 {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: nonzero reserved index-build state byte")
	}
	storedChecksum := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], kitDBIndexBuildCRC) != storedChecksum {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: index-build state checksum mismatch")
	}
	state := kitDBIndexBuildState{
		FormatVersion: encoded[4],
		Mode:          encoded[5],
		Phase:         encoded[6],
		StartedAt:     binary.BigEndian.Uint64(encoded[8:16]),
		Rows:          binary.BigEndian.Uint64(encoded[16:24]),
		SourceHash:    hex.EncodeToString(encoded[24:56]),
		TargetHash:    hex.EncodeToString(encoded[56:88]),
	}
	if state.Mode != kitDBIndexBuildCodec && state.Mode != kitDBIndexBuildSchema {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: invalid index-build mode %d", state.Mode)
	}
	if state.Phase != kitDBIndexBuildPhaseRows && state.Phase != kitDBIndexBuildPhaseCleanup {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: invalid index-build phase %d", state.Phase)
	}
	position := fixed
	readUvarint := func(label string) (uint64, error) {
		if position >= len(encoded)-4 {
			return 0, fmt.Errorf("kitdb: missing index-build %s", label)
		}
		decoded, width := binary.Uvarint(encoded[position : len(encoded)-4])
		if width <= 0 {
			return 0, fmt.Errorf("kitdb: invalid index-build %s", label)
		}
		var canonical [binary.MaxVarintLen64]byte
		canonicalWidth := binary.PutUvarint(canonical[:], decoded)
		if canonicalWidth != width || !bytes.Equal(canonical[:canonicalWidth], encoded[position:position+width]) {
			return 0, fmt.Errorf("kitdb: non-canonical index-build %s", label)
		}
		position += width
		return decoded, nil
	}
	readBytes := func(label string) ([]byte, error) {
		length, err := readUvarint(label + " length")
		if err != nil {
			return nil, err
		}
		if length > uint64(len(encoded)-4-position) {
			return nil, fmt.Errorf("kitdb: truncated index-build %s", label)
		}
		value := bytes.Clone(encoded[position : position+int(length)])
		position += int(length)
		return value, nil
	}
	var err error
	state.Progress, err = readBytes("progress")
	if err != nil {
		return kitDBIndexBuildState{}, err
	}
	if state.FormatVersion >= kitDBIndexBuildVersion {
		state.SourceDefinition, err = readBytes("source definition")
		if err != nil {
			return kitDBIndexBuildState{}, err
		}
	}
	state.TargetDefinition, err = readBytes("target definition")
	if err != nil {
		return kitDBIndexBuildState{}, err
	}
	count, err := readUvarint("signature count")
	if err != nil {
		return kitDBIndexBuildState{}, err
	}
	recordWidth := 16
	if state.FormatVersion >= kitDBIndexBuildVersion {
		recordWidth = 24
	}
	if count > uint64((len(encoded)-4-position)/recordWidth) || count > 65_535 {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: invalid index-build signature payload")
	}
	state.Indexes = make([]string, 0, int(count))
	previous := ""
	for range count {
		signature := hex.EncodeToString(encoded[position : position+16])
		if previous != "" && signature <= previous {
			return kitDBIndexBuildState{}, fmt.Errorf("kitdb: index-build signatures are not strictly ordered")
		}
		state.Indexes = append(state.Indexes, signature)
		position += 16
		generation := uint64(0)
		if state.FormatVersion >= kitDBIndexBuildVersion {
			generation = binary.BigEndian.Uint64(encoded[position : position+8])
			position += 8
		}
		state.Generations = append(state.Generations, generation)
		previous = signature
	}
	if state.FormatVersion >= kitDBIndexBuildVersion {
		retiredCount, err := readUvarint("retired prefix count")
		if err != nil {
			return kitDBIndexBuildState{}, err
		}
		if retiredCount > 65_535 {
			return kitDBIndexBuildState{}, fmt.Errorf("kitdb: invalid retired index-build count")
		}
		var previousPrefix []byte
		for range retiredCount {
			prefix, err := readBytes("retired prefix")
			if err != nil {
				return kitDBIndexBuildState{}, err
			}
			if len(prefix) == 0 || len(prefix) > kitDBIndexRetiredPrefixLimit ||
				(previousPrefix != nil && bytes.Compare(prefix, previousPrefix) <= 0) {
				return kitDBIndexBuildState{}, fmt.Errorf("kitdb: invalid retired index-build prefix ordering")
			}
			state.RetiredPrefixes = append(state.RetiredPrefixes, prefix)
			previousPrefix = prefix
		}
		state.CleanupIndex, err = readUvarint("cleanup index")
		if err != nil {
			return kitDBIndexBuildState{}, err
		}
	}
	if position != len(encoded)-4 {
		return kitDBIndexBuildState{}, fmt.Errorf("kitdb: trailing index-build state bytes")
	}
	if state.Mode == kitDBIndexBuildCodec {
		for _, generation := range state.Generations {
			if generation != 0 {
				return kitDBIndexBuildState{}, fmt.Errorf("kitdb: codec build names a physical generation")
			}
		}
	}
	if state.Mode == kitDBIndexBuildSchema && state.FormatVersion >= kitDBIndexBuildVersion {
		if len(state.SourceDefinition) == 0 {
			return kitDBIndexBuildState{}, fmt.Errorf("kitdb: schema index build has no source definition")
		}
		for _, generation := range state.Generations {
			if generation == 0 {
				return kitDBIndexBuildState{}, fmt.Errorf("kitdb: schema shadow index has reserved generation zero")
			}
		}
		if state.Phase == kitDBIndexBuildPhaseCleanup &&
			(state.CleanupIndex >= uint64(len(state.RetiredPrefixes)) || len(state.RetiredPrefixes) == 0) {
			return kitDBIndexBuildState{}, fmt.Errorf("kitdb: schema index cleanup has no current retired generation")
		}
	}
	return state, nil
}

func loadKitDBIndexBuildState(
	reader kitDBReader,
	definition *StructDef,
	mode byte,
) (kitDBIndexBuildState, bool, error) {
	key, err := kitDBIndexBuildStateKey(definition, mode)
	if err != nil {
		return kitDBIndexBuildState{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return kitDBIndexBuildState{}, found, err
	}
	state, err := decodeKitDBIndexBuildState(encoded)
	if err != nil {
		return kitDBIndexBuildState{}, false, err
	}
	if state.Mode != mode {
		return kitDBIndexBuildState{}, false, fmt.Errorf("kitdb: index-build state mode mismatch")
	}
	target, err := decodeKitDBCatalog(state.TargetDefinition, definition.Name)
	if err != nil {
		return kitDBIndexBuildState{}, false, err
	}
	if target.ID != definition.ID || target.Hash != state.TargetHash {
		return kitDBIndexBuildState{}, false, fmt.Errorf("kitdb: index-build target does not match its durable state")
	}
	if len(state.SourceDefinition) != 0 {
		source, err := decodeKitDBCatalog(state.SourceDefinition, definition.Name)
		if err != nil {
			return kitDBIndexBuildState{}, false, err
		}
		if source.ID != definition.ID || source.Hash != state.SourceHash {
			return kitDBIndexBuildState{}, false, fmt.Errorf("kitdb: index-build source does not match its durable state")
		}
	}
	if mode == kitDBIndexBuildCodec && state.SourceHash != state.TargetHash {
		return kitDBIndexBuildState{}, false, fmt.Errorf("kitdb: codec build changed the logical schema")
	}
	return state, true, nil
}

func loadKitDBIndexBuildStatuses(
	reader kitDBReader,
	definition *StructDef,
) ([]kitDBIndexBuildStatus, error) {
	statuses := make([]kitDBIndexBuildStatus, 0, 2)
	for _, candidate := range []struct {
		mode byte
		name string
	}{
		{mode: kitDBIndexBuildCodec, name: "codec"},
		{mode: kitDBIndexBuildSchema, name: "schema"},
	} {
		state, found, err := loadKitDBIndexBuildState(reader, definition, candidate.mode)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		phase := "building"
		if state.Phase == kitDBIndexBuildPhaseCleanup {
			phase = "cleanup"
		}
		startedTransaction := state.StartedAt
		if startedTransaction != ^uint64(0) {
			startedTransaction++
		}
		statuses = append(statuses, kitDBIndexBuildStatus{
			Mode:               candidate.name,
			Phase:              phase,
			ProcessedRows:      state.Rows,
			IndexCount:         len(state.Indexes),
			HasCursor:          len(state.Progress) != 0,
			CleanupIndex:       state.CleanupIndex,
			CleanupTotal:       len(state.RetiredPrefixes),
			StartedTransaction: startedTransaction,
			SourceHash:         state.SourceHash,
			TargetHash:         state.TargetHash,
			TargetPublished:    state.Phase == kitDBIndexBuildPhaseCleanup,
		})
	}
	return statuses, nil
}

func loadKitDBInactiveIndexes(reader kitDBReader, definition *StructDef) (map[string]struct{}, error) {
	inactive := make(map[string]struct{})
	if definition == nil {
		return inactive, nil
	}
	markerKey, err := kitDBSecondaryIndexCodecMarkerKey(definition)
	if err != nil {
		return nil, err
	}
	marker, markerFound, err := reader.Get(markerKey)
	if err != nil {
		return nil, err
	}
	if markerFound && !kitDBKnownSecondaryIndexCodecMarker(marker) {
		return nil, fmt.Errorf("kitdb: struct %q has an unsupported secondary-index codec marker", definition.Name)
	}
	currentCodec := markerFound && bytes.Equal(marker, kitDBSecondaryIndexCodecMarker)
	if !currentCodec {
		for _, index := range kitDBImplicitPrimaryIndexes(definition) {
			inactive[kitDBIndexSignature(index)] = struct{}{}
		}
	}
	codec, found, err := loadKitDBIndexBuildState(reader, definition, kitDBIndexBuildCodec)
	if err != nil {
		return nil, err
	}
	if found && codec.Phase == kitDBIndexBuildPhaseRows {
		// With no marker this is the original v1->v2 build, so every
		// admitted signature is incomplete. A v2 marker means its declared
		// indexes are already readable and only the hidden primary path waits
		// for the v3 build.
		if !markerFound {
			for _, signature := range codec.Indexes {
				inactive[signature] = struct{}{}
			}
		}
	}
	schema, found, err := loadKitDBIndexBuildState(reader, definition, kitDBIndexBuildSchema)
	if err != nil {
		return nil, err
	}
	if found && schema.Phase == kitDBIndexBuildPhaseRows {
		for _, signature := range schema.Indexes {
			inactive[signature] = struct{}{}
		}
	}
	return inactive, nil
}

func kitDBIndexBuildGenerations(state kitDBIndexBuildState) map[string]uint64 {
	generations := make(map[string]uint64, len(state.Indexes))
	for index, signature := range state.Indexes {
		if index < len(state.Generations) {
			generations[signature] = state.Generations[index]
		}
	}
	return generations
}

// loadKitDBIndexLayout returns both sides of the physical contract. Planner
// generations describe only indexes declared by definition. Write indexes may
// additionally include source-only generations during an online replacement,
// keeping pre-cutover snapshot readers complete until publication.
func loadKitDBIndexLayout(
	reader kitDBReader,
	definition *StructDef,
) (map[string]uint64, []kitDBPhysicalIndex, uint64, error) {
	metadata, _, err := loadKitDBIndexGenerationMetadata(reader, definition)
	if err != nil {
		return nil, nil, 0, err
	}
	indexes := collectKitDBIndexes(definition)
	generations := kitDBIndexGenerationMap(indexes, metadata)
	writes := kitDBPhysicalIndexes(indexes, generations)

	state, found, err := loadKitDBIndexBuildState(reader, definition, kitDBIndexBuildSchema)
	if err != nil || !found || state.Phase != kitDBIndexBuildPhaseRows {
		return generations, writes, metadata.Epoch, err
	}
	target, err := decodeKitDBCatalog(state.TargetDefinition, definition.Name)
	if err != nil {
		return nil, nil, 0, err
	}
	if definition.Hash == state.TargetHash {
		building := kitDBIndexBuildGenerations(state)
		for signature, generation := range building {
			generations[signature] = generation
		}
		targetIndexes := collectKitDBIndexes(target)
		writes = kitDBPhysicalIndexes(targetIndexes, generations)

		if len(state.SourceDefinition) != 0 {
			source, err := decodeKitDBCatalog(state.SourceDefinition, definition.Name)
			if err != nil {
				return nil, nil, 0, err
			}
			targetSignatures := make(map[string]struct{}, len(targetIndexes))
			for _, index := range targetIndexes {
				targetSignatures[kitDBIndexSignature(index)] = struct{}{}
			}
			for _, index := range collectKitDBIndexes(source) {
				signature := kitDBIndexSignature(index)
				if _, retained := targetSignatures[signature]; retained {
					continue
				}
				writes = append(writes, kitDBPhysicalIndex{
					index: index, generation: metadata.Active[signature],
				})
			}
		}
		return generations, writes, metadata.Epoch, nil
	}
	if definition.Hash == state.SourceHash {
		return generations, writes, metadata.Epoch, nil
	}
	return nil, nil, 0, fmt.Errorf(
		"kitdb: struct %q index layout does not admit schema %s",
		definition.Name,
		shortSchemaHash(definition.Hash),
	)
}

func validateKitDBIndexLayoutEpoch(reader kitDBReader, definition *StructDef, expected uint64) error {
	epoch, err := loadKitDBIndexGenerationEpoch(reader, definition)
	if err != nil {
		return err
	}
	if epoch != expected {
		return fmt.Errorf(
			"kitdb: struct %q index %w from epoch %d to %d; retry the read",
			definition.Name,
			errKitDBLayoutAdvanced,
			expected,
			epoch,
		)
	}
	return nil
}

func kitDBPendingIndexDefinition(reader kitDBReader, stored *StructDef) (*StructDef, bool, error) {
	state, found, err := loadKitDBIndexBuildState(reader, stored, kitDBIndexBuildSchema)
	if err != nil || !found {
		return nil, found, err
	}
	if state.Phase != kitDBIndexBuildPhaseRows {
		return nil, false, nil
	}
	if state.SourceHash != stored.Hash {
		return nil, false, fmt.Errorf("kitdb: struct %q has inconsistent pending index metadata", stored.Name)
	}
	target, err := decodeKitDBCatalog(state.TargetDefinition, stored.Name)
	if err != nil {
		return nil, false, err
	}
	if target.Hash != state.TargetHash || target.ID != stored.ID {
		return nil, false, fmt.Errorf("kitdb: struct %q pending index target does not match its durable state", stored.Name)
	}
	return target, true, nil
}

// validateKitDBWriteDefinition closes the gap between ready() and write-gate
// admission. A request from an older app generation may have prepared against
// the source catalog before another request durably starts or publishes a
// migration; it must not commit without maintaining the accepted target index.
func validateKitDBWriteDefinition(database *kitdbengine.DB, definition *StructDef) error {
	if database == nil || definition == nil {
		return fmt.Errorf("kitdb: write schema is unavailable")
	}
	activeHash, found, err := database.CatalogStructHashByID(definition.ID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("kitdb: struct %q has no durable catalog", definition.Name)
	}
	allowed := activeHash
	rowState, rowBuilding, err := loadKitDBRowMigrationState(database, definition)
	if err != nil {
		return err
	}
	if rowBuilding {
		if rowState.TargetGeneration == 0 {
			return kitDBRowMigrationPendingError(definition.Name, rowState.SourceHash, rowState.TargetHash)
		}
		switch rowState.Phase {
		case kitDBRowMigrationPhaseRows, kitDBRowMigrationPhaseVerify:
			if activeHash != rowState.SourceHash {
				return fmt.Errorf("kitdb: struct %q row-migration source no longer matches the active catalog", definition.Name)
			}
			if rowState.PrimaryKeyChange {
				return fmt.Errorf(
					"kitdb: writes to struct %q are paused while its primary key is rebuilt; reads remain available and PRAGMA migration_status(%q) reports durable progress",
					definition.Name, definition.Name,
				)
			}
			allowed = rowState.SourceHash
		case kitDBRowMigrationPhaseCancel:
			if activeHash != rowState.SourceHash {
				return fmt.Errorf("kitdb: struct %q row-migration cancellation no longer matches the active catalog", definition.Name)
			}
			allowed = rowState.SourceHash
		case kitDBRowMigrationPhaseCleanup:
			if activeHash != rowState.TargetHash {
				return fmt.Errorf("kitdb: struct %q row cleanup no longer matches the active catalog", definition.Name)
			}
			allowed = rowState.TargetHash
		default:
			return fmt.Errorf("kitdb: struct %q has invalid row-migration phase %q", definition.Name, rowState.Phase)
		}
	}
	state, building, err := loadKitDBIndexBuildState(database, definition, kitDBIndexBuildSchema)
	if err != nil {
		return err
	}
	if building && state.Phase == kitDBIndexBuildPhaseRows {
		if state.SourceHash != activeHash {
			return fmt.Errorf("kitdb: struct %q index-build source no longer matches the active catalog", definition.Name)
		}
		allowed = state.TargetHash
	} else if building && state.Phase == kitDBIndexBuildPhaseCleanup && state.TargetHash != activeHash {
		return fmt.Errorf("kitdb: struct %q index cleanup no longer matches the active catalog", definition.Name)
	}
	if definition.Hash != allowed {
		return fmt.Errorf(
			"kitdb: struct %q write uses %w %s; active write schema is %s",
			definition.Name, errKitDBStaleSchema,
			shortSchemaHash(definition.Hash), shortSchemaHash(allowed),
		)
	}
	return nil
}

func kitDBIndexBuildCatalogs(
	stored, target *StructDef,
) (sourceCatalog, targetCatalog []byte, err error) {
	targetCatalog, err = json.Marshal(target)
	if err != nil {
		return nil, nil, fmt.Errorf("kitdb: encode index-build target %q: %w", target.Name, err)
	}
	sourceCatalog, err = json.Marshal(stored)
	if err != nil {
		return nil, nil, fmt.Errorf("kitdb: encode index-build source %q: %w", stored.Name, err)
	}
	return sourceCatalog, targetCatalog, nil
}

func validateKitDBIndexBuildContract(
	state kitDBIndexBuildState,
	stored, target *StructDef,
	mode byte,
	indexes []indexDef,
	sourceCatalog, targetCatalog []byte,
) error {
	if state.Mode != mode || state.Phase != kitDBIndexBuildPhaseRows ||
		state.SourceHash != stored.Hash || state.TargetHash != target.Hash ||
		!bytes.Equal(state.TargetDefinition, targetCatalog) ||
		(state.FormatVersion >= kitDBIndexBuildVersion && !bytes.Equal(state.SourceDefinition, sourceCatalog)) ||
		!reflect.DeepEqual(state.Indexes, kitDBIndexSignatures(indexes)) {
		return fmt.Errorf(
			"kitdb: struct %q has a different resumable index build in progress (%s -> %s)",
			target.Name, shortSchemaHash(state.SourceHash), shortSchemaHash(state.TargetHash),
		)
	}
	if state.FormatVersion >= kitDBIndexBuildVersion && mode == kitDBIndexBuildSchema {
		if state.StartedAt == ^uint64(0) {
			return fmt.Errorf("kitdb: index generation space is exhausted")
		}
		for _, generation := range state.Generations {
			if generation != state.StartedAt+1 {
				return fmt.Errorf("kitdb: struct %q has an inconsistent shadow index generation", target.Name)
			}
		}
	}
	return nil
}

// admitKitDBIndexBuild publishes only the durable dual-write contract. It does
// not open a row cursor or derive one index entry, so DDL admission is bounded
// by schema/index metadata rather than table cardinality. The node-owned worker
// performs every subsequent row, cutover, and cleanup chunk.
func admitKitDBIndexBuild(
	database *kitdbengine.DB,
	stored, target *StructDef,
	mode byte,
	indexes []indexDef,
	retired []indexDef,
) (bool, error) {
	stateKey, err := kitDBIndexBuildStateKey(target, mode)
	if err != nil {
		return false, err
	}
	state, found, err := loadKitDBIndexBuildState(database, target, mode)
	if err != nil {
		return false, err
	}
	sourceCatalog, targetCatalog, err := kitDBIndexBuildCatalogs(stored, target)
	if err != nil {
		return false, err
	}
	if found {
		if err := validateKitDBIndexBuildContract(
			state, stored, target, mode, indexes, sourceCatalog, targetCatalog,
		); err != nil {
			return false, err
		}
		return false, nil
	}

	startedAt, err := database.LastTransaction()
	if err != nil {
		return false, err
	}
	if startedAt == ^uint64(0) {
		return false, fmt.Errorf("kitdb: index generation space is exhausted")
	}
	metadata, _, err := loadKitDBIndexGenerationMetadata(database, stored)
	if err != nil {
		return false, err
	}
	signatures := kitDBIndexSignatures(indexes)
	generations := make([]uint64, len(signatures))
	if mode == kitDBIndexBuildSchema {
		for index := range generations {
			generations[index] = startedAt + 1
		}
	}
	retiredPrefixes := make([][]byte, 0, len(retired))
	for _, index := range retired {
		prefix, err := kitDBIndexBasePrefixForGeneration(
			stored,
			index,
			kitDBPhysicalIndexGeneration(metadata, index),
		)
		if err != nil {
			return false, err
		}
		retiredPrefixes = append(retiredPrefixes, prefix)
	}
	sort.Slice(retiredPrefixes, func(left, right int) bool {
		return bytes.Compare(retiredPrefixes[left], retiredPrefixes[right]) < 0
	})
	state = kitDBIndexBuildState{
		FormatVersion: kitDBIndexBuildVersion,
		Mode:          mode, Phase: kitDBIndexBuildPhaseRows, StartedAt: startedAt,
		SourceHash: stored.Hash, TargetHash: target.Hash,
		SourceDefinition: sourceCatalog, TargetDefinition: targetCatalog,
		Indexes: signatures, Generations: generations, RetiredPrefixes: retiredPrefixes,
	}
	encoded, err := encodeKitDBIndexBuildState(state)
	if err != nil {
		return false, err
	}
	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	if err := tx.Put(stateKey, encoded); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func advanceKitDBIndexRows(
	database *kitdbengine.DB,
	stored, target *StructDef,
	mode byte,
	indexes []indexDef,
	retired []indexDef,
	steps []planStep,
) (bool, error) {
	stateKey, err := kitDBIndexBuildStateKey(target, mode)
	if err != nil {
		return false, err
	}
	state, found, err := loadKitDBIndexBuildState(database, target, mode)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("kitdb: struct %q has no durable index-build intent", target.Name)
	}
	sourceCatalog, targetCatalog, err := kitDBIndexBuildCatalogs(stored, target)
	if err != nil {
		return false, err
	}
	if err := validateKitDBIndexBuildContract(
		state, stored, target, mode, indexes, sourceCatalog, targetCatalog,
	); err != nil {
		return false, err
	}

	entries := make([]kitDBIndexEntry, 0)
	lastKey := bytes.Clone(state.Progress)
	processed := 0
	mutationBytes := len(stateKey) + len(state.SourceDefinition) + len(state.TargetDefinition) + 256
	more := false
	if len(indexes) != 0 {
		rowGeneration, _, err := loadKitDBActiveRowLayout(database, target)
		if err != nil {
			return false, err
		}
		rowPrefix, err := kitDBPhysicalRowPrefix(target, rowGeneration)
		if err != nil {
			return false, err
		}
		options := kitdbengine.RangeOptions{Prefix: rowPrefix}
		if len(state.Progress) != 0 {
			options.Start = bytes.Clone(state.Progress)
		}
		snapshot, err := database.Snapshot()
		if err != nil {
			return false, err
		}
		cursor, err := snapshot.Cursor(options)
		if err != nil {
			_ = snapshot.Close()
			return false, err
		}
		buildGenerations := kitDBIndexBuildGenerations(state)
		for cursor.Next() {
			key := cursor.Key()
			if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
				continue
			}
			if processed >= kitDBIndexBuildRowLimit {
				more = true
				break
			}
			decoded, err := decodeKitDBRow(target, cursor.Value())
			if err != nil {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, err
			}
			rowKey, err := kitDBLogicalRowKey(target, key, rowGeneration)
			if err != nil {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, err
			}
			rowEntries, err := kitDBSecondaryIndexEntriesForGenerations(
				target,
				decoded.values,
				rowKey,
				indexes,
				buildGenerations,
			)
			if err != nil {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, err
			}
			nextBytes := 0
			for _, entry := range rowEntries {
				nextBytes += len(entry.key) + len(entry.value)
			}
			if len(entries)+len(rowEntries)+2 > kitDBIndexBuildMutationLimit ||
				mutationBytes+nextBytes > kitDBIndexBuildByteLimit {
				if processed == 0 {
					_ = cursor.Close()
					_ = snapshot.Close()
					return false, fmt.Errorf(
						"kitdb: struct %q has one row whose derived indexes exceed the chunk budget",
						target.Name,
					)
				}
				more = true
				break
			}
			entries = append(entries, rowEntries...)
			mutationBytes += nextBytes
			lastKey = key
			processed++
		}
		if err := cursor.Err(); err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		_ = cursor.Close()
		_ = snapshot.Close()
	}

	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	rollback := func(cause error) (bool, error) {
		_ = tx.Rollback()
		return false, cause
	}
	for _, entry := range entries {
		if err := tx.Put(entry.key, entry.value); err != nil {
			return rollback(err)
		}
	}
	state.Rows += uint64(processed)
	state.Progress = bytes.Clone(lastKey)
	if more {
		encoded, err := encodeKitDBIndexBuildState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else if mode == kitDBIndexBuildCodec {
		markerKey, err := kitDBSecondaryIndexCodecMarkerKey(target)
		if err != nil {
			return rollback(err)
		}
		state.Phase = kitDBIndexBuildPhaseCleanup
		state.Progress = nil
		encoded, err := encodeKitDBIndexBuildState(state)
		if err != nil {
			return rollback(err)
		}
		marker := kitDBSecondaryIndexCodecMarkerV2
		if reflect.DeepEqual(state.Indexes, kitDBIndexSignatures(collectKitDBIndexes(target))) {
			marker = kitDBSecondaryIndexCodecMarker
		}
		if err := tx.Put(markerKey, marker); err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else {
		metadata, _, err := loadKitDBIndexGenerationMetadata(database, target)
		if err != nil {
			return rollback(err)
		}
		if metadata.Epoch == ^uint64(0) {
			return rollback(fmt.Errorf("kitdb: index layout epoch space is exhausted"))
		}
		building := kitDBIndexBuildGenerations(state)
		active := make(map[string]uint64)
		for _, index := range collectKitDBIndexes(target) {
			signature := kitDBIndexSignature(index)
			generation := building[signature]
			if generation == 0 {
				generation = metadata.Active[signature]
			}
			if generation != 0 {
				active[signature] = generation
			}
		}
		metadata.Epoch++
		metadata.Active = active
		metadata.Retired = appendKitDBRetiredPrefixes(metadata.Retired, state.RetiredPrefixes)
		metadataKey, err := kitDBIndexGenerationMetadataKey(target)
		if err != nil {
			return rollback(err)
		}
		encodedMetadata, err := encodeKitDBIndexGenerationMetadata(metadata)
		if err != nil {
			return rollback(err)
		}
		catalog, auditKey, audit, err := encodeKitDBMigration(target, steps, stored.Hash)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(metadataKey, encodedMetadata); err != nil {
			return rollback(err)
		}
		if err := tx.DefineStruct(catalog); err != nil {
			return rollback(err)
		}
		if len(audit) != 0 {
			if err := tx.Put(auditKey, audit); err != nil {
				return rollback(err)
			}
		}
		if len(state.RetiredPrefixes) != 0 {
			state.Phase = kitDBIndexBuildPhaseCleanup
			state.Progress = nil
			state.CleanupIndex = 0
			encoded, err := encodeKitDBIndexBuildState(state)
			if err != nil {
				return rollback(err)
			}
			if err := tx.Put(stateKey, encoded); err != nil {
				return rollback(err)
			}
		} else if err := tx.Delete(stateKey); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return !more, nil
}

func advanceKitDBLegacyIndexCleanup(
	database *kitdbengine.DB,
	definition *StructDef,
	state kitDBIndexBuildState,
) (bool, error) {
	if state.Mode != kitDBIndexBuildCodec || state.Phase != kitDBIndexBuildPhaseCleanup {
		return false, fmt.Errorf("kitdb: invalid legacy-index cleanup state")
	}
	stateKey, err := kitDBIndexBuildStateKey(definition, kitDBIndexBuildCodec)
	if err != nil {
		return false, err
	}
	prefix, err := kitDBFixedKey(kitDBIndexNamespace, definition.ID, "")
	if err != nil {
		return false, err
	}
	options := kitdbengine.RangeOptions{Prefix: prefix}
	if len(state.Progress) != 0 {
		options.Start = bytes.Clone(state.Progress)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return false, err
	}
	cursor, err := snapshot.Cursor(options)
	if err != nil {
		_ = snapshot.Close()
		return false, err
	}
	keys := make([][]byte, 0)
	lastKey := bytes.Clone(state.Progress)
	scanned := 0
	more := false
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if scanned >= kitDBIndexCleanupScanLimit {
			more = true
			break
		}
		lastKey = key
		scanned++
		if len(key) > 33 && key[33] != 0 {
			keys = append(keys, key)
		}
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close()
		_ = snapshot.Close()
		return false, err
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	for _, key := range keys {
		if err := tx.Delete(key); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	}
	if more {
		state.Progress = bytes.Clone(lastKey)
		encoded, err := encodeKitDBIndexBuildState(state)
		if err != nil {
			_ = tx.Rollback()
			return false, err
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			_ = tx.Rollback()
			return false, err
		}
	} else if err := tx.Delete(stateKey); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return !more, nil
}

func advanceKitDBRetiredIndexCleanup(
	database *kitdbengine.DB,
	definition *StructDef,
	state kitDBIndexBuildState,
) (bool, error) {
	if state.Mode != kitDBIndexBuildSchema || state.Phase != kitDBIndexBuildPhaseCleanup ||
		state.CleanupIndex >= uint64(len(state.RetiredPrefixes)) {
		return false, fmt.Errorf("kitdb: invalid retired index cleanup state")
	}
	stateKey, err := kitDBIndexBuildStateKey(definition, kitDBIndexBuildSchema)
	if err != nil {
		return false, err
	}
	prefix := state.RetiredPrefixes[state.CleanupIndex]
	options := kitdbengine.RangeOptions{Prefix: prefix}
	if len(state.Progress) != 0 {
		options.Start = bytes.Clone(state.Progress)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return false, err
	}
	cursor, err := snapshot.Cursor(options)
	if err != nil {
		_ = snapshot.Close()
		return false, err
	}
	keys := make([][]byte, 0, kitDBIndexCleanupScanLimit)
	lastKey := bytes.Clone(state.Progress)
	mutationBytes := len(stateKey) + len(state.TargetDefinition) + 256
	more := false
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if len(keys) >= kitDBIndexCleanupScanLimit ||
			mutationBytes+len(key) > kitDBIndexBuildByteLimit {
			more = true
			break
		}
		keys = append(keys, bytes.Clone(key))
		lastKey = bytes.Clone(key)
		mutationBytes += len(key)
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close()
		_ = snapshot.Close()
		return false, err
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	rollback := func(cause error) (bool, error) {
		_ = tx.Rollback()
		return false, cause
	}
	for _, key := range keys {
		if err := tx.Delete(key); err != nil {
			return rollback(err)
		}
	}
	if more {
		state.Progress = lastKey
		encoded, err := encodeKitDBIndexBuildState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else {
		metadata, found, err := loadKitDBIndexGenerationMetadata(database, definition)
		if err != nil {
			return rollback(err)
		}
		if !found {
			return rollback(fmt.Errorf("kitdb: retired index cleanup lost generation metadata"))
		}
		retained := make([][]byte, 0, len(metadata.Retired))
		removed := false
		for _, candidate := range metadata.Retired {
			if bytes.Equal(candidate, prefix) {
				removed = true
				continue
			}
			retained = append(retained, candidate)
		}
		if !removed {
			return rollback(fmt.Errorf("kitdb: retired index cleanup prefix is not published"))
		}
		metadata.Retired = retained
		metadataKey, err := kitDBIndexGenerationMetadataKey(definition)
		if err != nil {
			return rollback(err)
		}
		encodedMetadata, err := encodeKitDBIndexGenerationMetadata(metadata)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(metadataKey, encodedMetadata); err != nil {
			return rollback(err)
		}

		state.CleanupIndex++
		state.Progress = nil
		if state.CleanupIndex < uint64(len(state.RetiredPrefixes)) {
			encoded, err := encodeKitDBIndexBuildState(state)
			if err != nil {
				return rollback(err)
			}
			if err := tx.Put(stateKey, encoded); err != nil {
				return rollback(err)
			}
		} else if err := tx.Delete(stateKey); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return !more && state.CleanupIndex >= uint64(len(state.RetiredPrefixes)), nil
}
