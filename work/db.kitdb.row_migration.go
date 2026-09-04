package work

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"reflect"
	"sort"
	"strings"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

const (
	kitDBRowMigrationVersion       byte = 1
	kitDBRowMigrationRowLimit           = 2_048
	kitDBPrimaryKeyBuildRowLimit        = 8_192
	kitDBPrimaryKeyVerifyRowLimit       = 65_536
	kitDBPrimaryKeyCleanupKeyLimit      = 8_192
	kitDBRowMigrationMutationLimit      = 16_384
	kitDBRowMigrationByteLimit          = 8 << 20
	kitDBRowMigrationStateLimit         = 16 << 20
	kitDBRowMigrationHeaderSize         = 12
)

const (
	kitDBRowMigrationPhaseRows    = "rows"
	kitDBRowMigrationPhaseVerify  = "verify"
	kitDBRowMigrationPhaseCancel  = "cancel"
	kitDBRowMigrationPhaseCleanup = "cleanup"
)

var (
	kitDBRowMigrationMagic = [4]byte{'K', 'R', 'M', 'S'}
	kitDBRowMigrationCRC   = crc32.MakeTable(crc32.Castagnoli)

	errKitDBRowMigrationPending   = errors.New("kitdb: segmented row migration is pending")
	kitDBSharedRowMigrationDriver kitDBRowMigrationNodeDriver
)

type kitDBRowMigrationNodeDriver struct{}

func (kitDBRowMigrationNodeDriver) RowMigrationDriverID() string {
	return "kitwork/relational-row-migration/v1"
}

func (kitDBRowMigrationNodeDriver) AdvanceRowMigration(
	ctx context.Context,
	database *kitdbengine.DB,
) (kitdbnode.RowMigrationProgress, error) {
	if database == nil {
		return kitdbnode.RowMigrationProgress{}, fmt.Errorf("kitdb: row-migration database is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return kitdbnode.RowMigrationProgress{}, err
	}
	if _, err := checkpointKitDBBackgroundMaintenance(ctx, database); err != nil {
		return kitdbnode.RowMigrationProgress{}, err
	}
	catalog, err := database.Catalog()
	if err != nil {
		return kitdbnode.RowMigrationProgress{}, err
	}
	for _, catalogEntry := range catalog.Structs {
		stored, err := decodeKitDBCatalog(catalogEntry.Definition, catalogEntry.Name)
		if err != nil {
			return kitdbnode.RowMigrationProgress{}, err
		}
		state, found, err := loadKitDBRowMigrationState(database, stored)
		if err != nil {
			return kitdbnode.RowMigrationProgress{}, err
		}
		if !found {
			continue
		}
		if err := advanceKitDBRowMigrationStateChunk(ctx, database, stored, state); err != nil {
			return kitdbnode.RowMigrationProgress{}, err
		}
		transaction, err := database.LastTransaction()
		if err != nil {
			return kitdbnode.RowMigrationProgress{}, err
		}
		pending, err := hasKitDBPendingRowMigration(database)
		if err != nil {
			return kitdbnode.RowMigrationProgress{}, err
		}
		return kitdbnode.RowMigrationProgress{
			Transaction: transaction, Pending: pending, Advanced: true,
		}, nil
	}
	return kitdbnode.RowMigrationProgress{}, nil
}

func advanceKitDBRowMigrationStateChunk(
	ctx context.Context,
	database *kitdbengine.DB,
	stored *StructDef,
	state kitDBRowMigrationState,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	source, target, err := kitDBRowMigrationDefinitions(state, stored.Name)
	if err != nil {
		return err
	}
	activeHash, found, err := database.CatalogStructHashByID(stored.ID)
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("kitdb: struct %q row migration lost its durable catalog", stored.Name)
	}
	switch state.Phase {
	case "", kitDBRowMigrationPhaseRows, kitDBRowMigrationPhaseVerify:
		if activeHash != state.SourceHash || stored.Hash != state.SourceHash {
			return fmt.Errorf("kitdb: struct %q row migration no longer matches its source catalog", stored.Name)
		}
		steps := planKitDBMigration(source, target, true)
		for index := range steps {
			// KRMS exists only after the foreground planner accepted the exact
			// destructive intent. Resume must not depend on an expired request.
			steps[index].WillApply = true
		}
		_, err = advanceKitDBSegmentedMigration(database, state, steps)
		return err
	case kitDBRowMigrationPhaseCancel:
		if activeHash != state.SourceHash || stored.Hash != state.SourceHash {
			return fmt.Errorf("kitdb: struct %q cancellation no longer matches its source catalog", stored.Name)
		}
		_, _, err = advanceKitDBCancelledRowCleanup(database, state)
		return err
	case kitDBRowMigrationPhaseCleanup:
		if activeHash != state.TargetHash || stored.Hash != state.TargetHash {
			return fmt.Errorf("kitdb: struct %q cleanup no longer matches its target catalog", stored.Name)
		}
		_, err = advanceKitDBRetiredRowCleanup(database, state)
		return err
	default:
		return fmt.Errorf("kitdb: struct %q has invalid row-migration phase %q", stored.Name, state.Phase)
	}
}

func hasKitDBPendingRowMigration(database *kitdbengine.DB) (bool, error) {
	catalog, err := database.Catalog()
	if err != nil {
		return false, err
	}
	for _, catalogEntry := range catalog.Structs {
		definition, err := decodeKitDBCatalog(catalogEntry.Definition, catalogEntry.Name)
		if err != nil {
			return false, err
		}
		if _, found, err := loadKitDBRowMigrationState(database, definition); err != nil {
			return false, err
		} else if found {
			return true, nil
		}
	}
	return false, nil
}

// kitDBRowMigrationState is a durable cursor, not another journal. A chunk's
// rewritten rows and its next cursor share one ordinary KitDB WAL commit.
type kitDBRowMigrationState struct {
	StartedAt         uint64 `json:"startedAt"`
	MigrationUnixNano int64  `json:"migrationUnixNano"`
	Rows              uint64 `json:"rows"`
	VerifiedRows      uint64 `json:"verifiedRows,omitempty"`
	CleanupRows       uint64 `json:"cleanupRows,omitempty"`
	// TotalRows is exact for legacy in-place state and an admission lower bound
	// for online shadow state, whose cardinality may change between chunks.
	TotalRows        uint64   `json:"totalRows"`
	Progress         []byte   `json:"progress,omitempty"`
	Phase            string   `json:"phase,omitempty"`
	SourceGeneration uint64   `json:"sourceGeneration,omitempty"`
	TargetGeneration uint64   `json:"targetGeneration,omitempty"`
	RetiredPrefix    []byte   `json:"retiredPrefix,omitempty"`
	PrimaryKeyChange bool     `json:"primaryKeyChange,omitempty"`
	SourceIndexEpoch uint64   `json:"sourceIndexEpoch,omitempty"`
	TargetIndexes    []string `json:"targetIndexes,omitempty"`
	RetiredIndexes   [][]byte `json:"retiredIndexes,omitempty"`
	CleanupIndex     uint64   `json:"cleanupIndex,omitempty"`
	SourceHash       string   `json:"sourceHash"`
	TargetHash       string   `json:"targetHash"`
	SourceDefinition []byte   `json:"sourceDefinition"`
	TargetDefinition []byte   `json:"targetDefinition"`
}

func kitDBRowMigrationStateKey(definition *StructDef) ([]byte, error) {
	if definition == nil {
		return nil, fmt.Errorf("kitdb: row-migration definition is unavailable")
	}
	identity := stableSchemaID("physical", definition.ID+":segmented-row-migration")
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
}

func encodeKitDBRowMigrationState(state kitDBRowMigrationState) ([]byte, error) {
	if state.MigrationUnixNano == 0 || state.SourceHash == "" || state.TargetHash == "" ||
		state.SourceHash == state.TargetHash || len(state.SourceDefinition) == 0 ||
		len(state.TargetDefinition) == 0 {
		return nil, fmt.Errorf("kitdb: invalid segmented row-migration state")
	}
	if state.TargetGeneration == 0 {
		if state.TotalRows <= kitDBAtomicMigrationRows || state.Rows > state.TotalRows ||
			state.Phase != "" || state.SourceGeneration != 0 ||
			state.CleanupRows != 0 || len(state.RetiredPrefix) != 0 {
			return nil, fmt.Errorf("kitdb: invalid legacy segmented row-migration state")
		}
	} else if state.TotalRows <= kitDBAtomicMigrationRows ||
		(state.Phase != kitDBRowMigrationPhaseRows && state.Phase != kitDBRowMigrationPhaseCancel &&
			state.Phase != kitDBRowMigrationPhaseCleanup && state.Phase != kitDBRowMigrationPhaseVerify) ||
		(state.Phase == kitDBRowMigrationPhaseRows && (state.VerifiedRows != 0 || state.CleanupRows != 0)) ||
		state.TargetGeneration == state.SourceGeneration || len(state.RetiredPrefix) == 0 ||
		len(state.RetiredPrefix) > kitDBRowRetiredPrefixLimit {
		return nil, fmt.Errorf("kitdb: invalid shadow row-migration state")
	}
	if !state.PrimaryKeyChange &&
		(state.VerifiedRows != 0 || state.Phase == kitDBRowMigrationPhaseVerify ||
			state.SourceIndexEpoch != 0 || len(state.TargetIndexes) != 0 ||
			len(state.RetiredIndexes) != 0 || state.CleanupIndex != 0) {
		return nil, fmt.Errorf("kitdb: ordinary row migration contains primary-key physical state")
	}
	if state.PrimaryKeyChange {
		cleanupPrefixes := len(state.RetiredIndexes) + 1
		if state.Phase == kitDBRowMigrationPhaseCancel {
			cleanupPrefixes = len(state.TargetIndexes) + 1
		}
		if state.TargetGeneration == 0 || state.CleanupIndex >= uint64(cleanupPrefixes) ||
			((state.Phase == kitDBRowMigrationPhaseRows || state.Phase == kitDBRowMigrationPhaseVerify) &&
				state.CleanupIndex != 0) || state.VerifiedRows > state.Rows ||
			(state.Phase == kitDBRowMigrationPhaseVerify && state.CleanupRows != 0) ||
			(state.Phase == kitDBRowMigrationPhaseCleanup && state.VerifiedRows != state.Rows) {
			return nil, fmt.Errorf("kitdb: invalid primary-key row-migration state")
		}
		for index, signature := range state.TargetIndexes {
			if !validSchemaID(signature) || (index != 0 && signature <= state.TargetIndexes[index-1]) {
				return nil, fmt.Errorf("kitdb: invalid primary-key target-index state")
			}
		}
		for index, prefix := range state.RetiredIndexes {
			if len(prefix) == 0 || len(prefix) > kitDBIndexRetiredPrefixLimit ||
				(index != 0 && bytes.Compare(prefix, state.RetiredIndexes[index-1]) <= 0) {
				return nil, fmt.Errorf("kitdb: invalid primary-key retired-index state")
			}
		}
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return nil, fmt.Errorf("kitdb: encode segmented row-migration state: %w", err)
	}
	if len(payload) > kitDBRowMigrationStateLimit {
		return nil, fmt.Errorf("kitdb: segmented row-migration state exceeds the format limit")
	}
	encoded := make([]byte, kitDBRowMigrationHeaderSize, kitDBRowMigrationHeaderSize+len(payload)+4)
	copy(encoded[:4], kitDBRowMigrationMagic[:])
	encoded[4] = kitDBRowMigrationVersion
	binary.BigEndian.PutUint32(encoded[8:12], uint32(len(payload)))
	encoded = append(encoded, payload...)
	encoded = binary.BigEndian.AppendUint32(encoded, crc32.Checksum(encoded, kitDBRowMigrationCRC))
	return encoded, nil
}

func decodeKitDBRowMigrationState(encoded []byte) (kitDBRowMigrationState, error) {
	if len(encoded) < kitDBRowMigrationHeaderSize+4 ||
		!bytes.Equal(encoded[:4], kitDBRowMigrationMagic[:]) {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: invalid segmented row-migration envelope")
	}
	if encoded[4] != kitDBRowMigrationVersion {
		return kitDBRowMigrationState{}, fmt.Errorf(
			"kitdb: unsupported segmented row-migration version %d", encoded[4],
		)
	}
	if encoded[5] != 0 || encoded[6] != 0 || encoded[7] != 0 {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: nonzero segmented row-migration reserved byte")
	}
	payloadLength := int(binary.BigEndian.Uint32(encoded[8:12]))
	if payloadLength > kitDBRowMigrationStateLimit ||
		payloadLength != len(encoded)-kitDBRowMigrationHeaderSize-4 {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: invalid segmented row-migration payload length")
	}
	storedChecksum := binary.BigEndian.Uint32(encoded[len(encoded)-4:])
	if crc32.Checksum(encoded[:len(encoded)-4], kitDBRowMigrationCRC) != storedChecksum {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: segmented row-migration checksum mismatch")
	}
	var state kitDBRowMigrationState
	if err := json.Unmarshal(encoded[kitDBRowMigrationHeaderSize:len(encoded)-4], &state); err != nil {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: decode segmented row-migration state: %w", err)
	}
	if _, err := encodeKitDBRowMigrationState(state); err != nil {
		return kitDBRowMigrationState{}, err
	}
	return state, nil
}

func loadKitDBRowMigrationState(
	reader kitDBReader,
	definition *StructDef,
) (kitDBRowMigrationState, bool, error) {
	key, err := kitDBRowMigrationStateKey(definition)
	if err != nil {
		return kitDBRowMigrationState{}, false, err
	}
	encoded, found, err := reader.Get(key)
	if err != nil || !found {
		return kitDBRowMigrationState{}, found, err
	}
	state, err := decodeKitDBRowMigrationState(encoded)
	if err != nil {
		return kitDBRowMigrationState{}, false, err
	}
	source, target, err := kitDBRowMigrationDefinitions(state, definition.Name)
	if err != nil {
		return kitDBRowMigrationState{}, false, err
	}
	if source.ID != definition.ID || target.ID != definition.ID {
		return kitDBRowMigrationState{}, false, fmt.Errorf(
			"kitdb: struct %q row-migration identity mismatch", definition.Name,
		)
	}
	return state, true, nil
}

func kitDBRowMigrationDefinitions(
	state kitDBRowMigrationState,
	name string,
) (*StructDef, *StructDef, error) {
	source, err := decodeKitDBCatalog(state.SourceDefinition, name)
	if err != nil {
		return nil, nil, err
	}
	target, err := decodeKitDBCatalog(state.TargetDefinition, name)
	if err != nil {
		return nil, nil, err
	}
	if source.ID != target.ID || source.Hash != state.SourceHash || target.Hash != state.TargetHash {
		return nil, nil, fmt.Errorf("kitdb: segmented row-migration definitions do not match their hashes")
	}
	return source, target, nil
}

func kitDBRowMigrationPendingError(name, sourceHash, targetHash string) error {
	return fmt.Errorf(
		"%w for struct %q (%s -> %s); inspect PRAGMA migration_status(%q); node maintenance continues it in the background when available",
		errKitDBRowMigrationPending,
		name,
		shortSchemaHash(sourceHash),
		shortSchemaHash(targetHash),
		name,
	)
}

func validateKitDBNoPendingRowMigration(reader kitDBReader, definition *StructDef) error {
	state, found, err := loadKitDBRowMigrationState(reader, definition)
	if err != nil || !found {
		return err
	}
	return kitDBRowMigrationPendingError(definition.Name, state.SourceHash, state.TargetHash)
}

func validateKitDBPendingRowMigrations(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	intents map[string]kitDBMigrationIntent,
) error {
	catalog, err := database.Catalog()
	if err != nil {
		return err
	}
	for _, catalogEntry := range catalog.Structs {
		stored, err := decodeKitDBCatalog(catalogEntry.Definition, catalogEntry.Name)
		if err != nil {
			return err
		}
		state, found, err := loadKitDBRowMigrationState(database, stored)
		if err != nil || !found {
			if err != nil {
				return err
			}
			continue
		}
		source, target, err := kitDBRowMigrationDefinitions(state, stored.Name)
		if err != nil {
			return err
		}
		current := definitions[stored.Name]
		if state.TargetGeneration == 0 {
			if stored.Hash == source.Hash && current != nil && current.Hash == target.Hash {
				steps := planKitDBMigrationWithIntent(source, target, true, intents[current.ID])
				entry := &kitDBSchemaPlanEntry{stored: source, current: target, steps: steps}
				if kitDBSegmentedMigrationEligible(entry, definitions) &&
					len(refusedKitDBMigrationSteps(steps)) == 0 {
					continue
				}
			}
			return kitDBRowMigrationPendingError(stored.Name, state.SourceHash, state.TargetHash)
		}
		switch state.Phase {
		case kitDBRowMigrationPhaseRows, kitDBRowMigrationPhaseVerify:
			if stored.Hash != source.Hash || current == nil {
				return kitDBRowMigrationPendingError(stored.Name, state.SourceHash, state.TargetHash)
			}
			if current.Hash == source.Hash {
				continue
			}
			if current.Hash == target.Hash {
				steps := planKitDBMigrationWithIntent(source, target, true, intents[current.ID])
				entry := &kitDBSchemaPlanEntry{stored: source, current: target, steps: steps}
				if kitDBSegmentedMigrationEligible(entry, definitions) &&
					len(refusedKitDBMigrationSteps(steps)) == 0 {
					continue
				}
			}
		case kitDBRowMigrationPhaseCancel:
			if stored.Hash == source.Hash && current != nil && current.Hash == source.Hash {
				continue
			}
		case kitDBRowMigrationPhaseCleanup:
			if stored.Hash == target.Hash && current != nil && current.Hash == target.Hash {
				continue
			}
		default:
			return fmt.Errorf("kitdb: struct %q has invalid row-migration phase %q", stored.Name, state.Phase)
		}
		return kitDBRowMigrationPendingError(stored.Name, state.SourceHash, state.TargetHash)
	}
	return nil
}

func advanceKitDBPendingRowMaintenance(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
) error {
	catalog, err := database.Catalog()
	if err != nil {
		return err
	}
	for _, catalogEntry := range catalog.Structs {
		stored, err := decodeKitDBCatalog(catalogEntry.Definition, catalogEntry.Name)
		if err != nil {
			return err
		}
		state, found, err := loadKitDBRowMigrationState(database, stored)
		if err != nil {
			return err
		}
		if !found || state.TargetGeneration == 0 ||
			(state.Phase != kitDBRowMigrationPhaseCancel && state.Phase != kitDBRowMigrationPhaseCleanup) {
			continue
		}
		current := definitions[stored.Name]
		switch state.Phase {
		case kitDBRowMigrationPhaseCancel:
			if current == nil || current.Hash != state.SourceHash || stored.Hash != state.SourceHash {
				return kitDBRowMigrationPendingError(stored.Name, state.SourceHash, state.TargetHash)
			}
			_, _, err = advanceKitDBCancelledRowCleanup(database, state)
		case kitDBRowMigrationPhaseCleanup:
			if current == nil || current.Hash != state.TargetHash || stored.Hash != state.TargetHash {
				return kitDBRowMigrationPendingError(stored.Name, state.SourceHash, state.TargetHash)
			}
			_, err = advanceKitDBRetiredRowCleanup(database, state)
		}
		return err
	}
	return nil
}

func kitDBSegmentedMigrationCandidate(
	entry *kitDBSchemaPlanEntry,
	definitions map[string]*StructDef,
) (string, string, bool) {
	if entry == nil || entry.stored == nil || entry.current == nil ||
		entry.stored.ID != entry.current.ID || len(entry.steps) == 0 {
		return "", "", false
	}
	fieldID := ""
	action := ""
	for _, step := range entry.steps {
		if !step.WillApply {
			return "", "", false
		}
		switch step.Action {
		case "remove", "field_type":
			if !step.Destructive || step.fieldID == "" || (fieldID != "" && fieldID != step.fieldID) {
				return "", "", false
			}
			fieldID = step.fieldID
			action = step.Action
		case "constraint", "metadata":
			if step.fieldID == "" || (fieldID != "" && fieldID != step.fieldID) {
				return "", "", false
			}
		default:
			return "", "", false
		}
	}
	if fieldID == "" || (action != "remove" && action != "field_type") {
		return "", "", false
	}
	if !equalKitDBUniqueConstraints(entry.stored.UniqueConstraints, entry.current.UniqueConstraints) ||
		!equalKitDBForeignConstraints(entry.stored.ForeignConstraints, entry.current.ForeignConstraints) ||
		!equalKitDBCheckConstraints(entry.stored.CheckConstraints, entry.current.CheckConstraints) {
		return "", "", false
	}
	previous, found := kitDBFieldByID(entry.stored, fieldID)
	if !found || kitDBSegmentedFieldDependency(definitions, entry.stored, previous) != "" {
		return "", "", false
	}
	if current, found := kitDBFieldByID(entry.current, fieldID); found &&
		kitDBSegmentedFieldDependency(definitions, entry.current, current) != "" {
		return "", "", false
	}
	build, retired, ok := kitDBSecondaryIndexTransition(entry.stored, entry.current)
	if !ok || len(build) != 0 || len(retired) != 0 {
		return "", "", false
	}
	return fieldID, action, true
}

func kitDBSegmentedMigrationEligible(
	entry *kitDBSchemaPlanEntry,
	definitions map[string]*StructDef,
) bool {
	if _, _, ok := kitDBSegmentedMigrationCandidate(entry, definitions); ok {
		return true
	}
	return kitDBPrimaryKeyMigrationCandidate(entry, definitions)
}

func kitDBPrimaryKeyMigrationCandidate(
	entry *kitDBSchemaPlanEntry,
	definitions map[string]*StructDef,
) bool {
	if entry == nil || entry.stored == nil || entry.current == nil ||
		entry.stored.ID != entry.current.ID || len(entry.steps) == 0 ||
		sameStructFieldIdentity(entry.stored.primaryFields(), entry.current.primaryFields()) {
		return false
	}
	hasPrimaryStep := false
	for _, step := range entry.steps {
		if !step.WillApply {
			return false
		}
		switch step.Action {
		case "primary":
			hasPrimaryStep = true
		case "constraint", "metadata":
			// A single-column primary key carries implied uniqueness in Schema
			// IR, so demoting it also produces a constraint step.
		default:
			return false
		}
	}
	if !hasPrimaryStep ||
		!equalKitDBUniqueConstraints(entry.stored.UniqueConstraints, entry.current.UniqueConstraints) ||
		!equalKitDBForeignConstraints(entry.stored.ForeignConstraints, entry.current.ForeignConstraints) ||
		!equalKitDBCheckConstraints(entry.stored.CheckConstraints, entry.current.CheckConstraints) {
		return false
	}
	for _, definition := range []*StructDef{entry.stored, entry.current} {
		for _, field := range definition.Fields {
			if (field.Unique && !field.Primary) || field.Reference != nil {
				return false
			}
		}
		if len(definition.UniqueConstraints) != 0 || len(definition.ForeignConstraints) != 0 {
			return false
		}
	}
	// A dependent reference would have to change in the same catalog cutover.
	// Primary-key rekey v1 deliberately admits one isolated table only.
	for _, definition := range definitions {
		if definition == nil || definition.ID == entry.stored.ID {
			continue
		}
		for _, field := range definition.Fields {
			if field.Reference != nil && strings.EqualFold(field.Reference.Struct, entry.stored.Name) {
				return false
			}
		}
		for _, constraint := range definition.ForeignConstraints {
			if constraint.TargetStructID == entry.stored.ID ||
				strings.EqualFold(constraint.TargetStruct, entry.stored.Name) {
				return false
			}
		}
	}
	return true
}

func kitDBPrimaryFieldNames(definition *StructDef) []string {
	fields := definition.primaryFields()
	names := make([]string, len(fields))
	for index, field := range fields {
		names[index] = field.Name
	}
	return names
}

func kitDBFieldByID(definition *StructDef, fieldID string) (StructFieldDef, bool) {
	if definition == nil {
		return StructFieldDef{}, false
	}
	for _, field := range definition.Fields {
		if field.ID == fieldID {
			return field, true
		}
	}
	return StructFieldDef{}, false
}

func kitDBSegmentedFieldDependency(
	definitions map[string]*StructDef,
	definition *StructDef,
	field StructFieldDef,
) string {
	if field.Primary || field.Unique || field.Reference != nil || len(field.Indexes) != 0 {
		return "field constraint or index"
	}
	for _, constraint := range definition.UniqueConstraints {
		if containsKitDBFieldTag(constraint.Fields, field.Tag) {
			return "tuple unique"
		}
	}
	for _, constraint := range definition.ForeignConstraints {
		if containsKitDBFieldTag(constraint.Fields, field.Tag) {
			return "foreign key"
		}
	}
	for _, constraint := range definition.CheckConstraints {
		if structCheckExpressionReferencesField(constraint.Expression, field.Tag) {
			return "check constraint"
		}
	}
	for _, candidate := range definition.Fields {
		for _, member := range candidate.Indexes {
			for _, condition := range member.Filter {
				if strings.EqualFold(condition.Field, field.Name) {
					return "partial index"
				}
			}
		}
	}
	for _, candidate := range definitions {
		if candidate == nil {
			continue
		}
		for _, local := range candidate.Fields {
			if local.Reference == nil || !strings.EqualFold(local.Reference.Struct, definition.Name) {
				continue
			}
			target, found := kitDBField(definition, local.Reference.Field)
			if found && target.ID == field.ID {
				return "incoming foreign key"
			}
		}
		for _, constraint := range candidate.ForeignConstraints {
			if constraint.TargetStructID != definition.ID {
				continue
			}
			for _, targetID := range constraint.TargetFields {
				if targetID == field.ID {
					return "incoming composite foreign key"
				}
			}
		}
	}
	return ""
}

func kitDBMigrationExceedsAtomicRows(
	database *kitdbengine.DB,
	definition *StructDef,
) (bool, error) {
	generation, _, err := loadKitDBActiveRowLayout(database, definition)
	if err != nil {
		return false, err
	}
	prefix, err := kitDBPhysicalRowPrefix(definition, generation)
	if err != nil {
		return false, err
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return false, err
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		return false, err
	}
	defer cursor.Close()
	rows := 0
	for cursor.Next() {
		rows++
		if rows > kitDBAtomicMigrationRows {
			return true, nil
		}
	}
	return false, cursor.Err()
}

func beginKitDBSegmentedMigration(
	database *kitdbengine.DB,
	stored, target *StructDef,
	migrationTime time.Time,
	admissionRows uint64,
) (kitDBRowMigrationState, error) {
	// The caller only has to prove that the atomic row ceiling was crossed.
	// The exact cardinality may change while source-schema CRUD dual-writes the
	// shadow generation, so it is neither required nor treated as an invariant.
	if admissionRows <= kitDBAtomicMigrationRows {
		return kitDBRowMigrationState{}, fmt.Errorf(
			"kitdb: struct %q does not require a segmented row migration", target.Name,
		)
	}
	startedAt, err := database.LastTransaction()
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	if startedAt == ^uint64(0) {
		return kitDBRowMigrationState{}, fmt.Errorf("kitdb: row generation space is exhausted")
	}
	metadata, _, err := loadKitDBRowGenerationMetadata(database, stored)
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	retiredPrefix, err := kitDBPhysicalRowPrefix(stored, metadata.Active)
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	sourceCatalog, err := json.Marshal(stored)
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	targetCatalog, err := json.Marshal(target)
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	state := kitDBRowMigrationState{
		StartedAt: startedAt, MigrationUnixNano: migrationTime.UnixNano(), TotalRows: admissionRows,
		Phase: kitDBRowMigrationPhaseRows, SourceGeneration: metadata.Active,
		TargetGeneration: startedAt + 1, RetiredPrefix: retiredPrefix,
		SourceHash: stored.Hash, TargetHash: target.Hash,
		SourceDefinition: sourceCatalog, TargetDefinition: targetCatalog,
	}
	if !sameStructFieldIdentity(stored.primaryFields(), target.primaryFields()) {
		for _, mode := range []byte{kitDBIndexBuildCodec, kitDBIndexBuildSchema} {
			if _, found, err := loadKitDBIndexBuildState(database, stored, mode); err != nil {
				return kitDBRowMigrationState{}, err
			} else if found {
				return kitDBRowMigrationState{}, fmt.Errorf(
					"kitdb: struct %q must finish index maintenance before changing its primary key",
					stored.Name,
				)
			}
		}
		indexMetadata, _, err := loadKitDBIndexGenerationMetadata(database, stored)
		if err != nil {
			return kitDBRowMigrationState{}, err
		}
		retired := make([][]byte, 0, len(collectKitDBIndexes(stored)))
		for _, index := range collectKitDBIndexes(stored) {
			prefix, err := kitDBIndexBasePrefixForGeneration(
				stored, index, kitDBPhysicalIndexGeneration(indexMetadata, index),
			)
			if err != nil {
				return kitDBRowMigrationState{}, err
			}
			retired = append(retired, prefix)
		}
		sort.Slice(retired, func(left, right int) bool {
			return bytes.Compare(retired[left], retired[right]) < 0
		})
		state.PrimaryKeyChange = true
		state.SourceIndexEpoch = indexMetadata.Epoch
		state.TargetIndexes = kitDBIndexSignatures(collectKitDBIndexes(target))
		state.RetiredIndexes = retired
	}
	encoded, err := encodeKitDBRowMigrationState(state)
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	stateKey, err := kitDBRowMigrationStateKey(stored)
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	tx, err := database.Begin()
	if err != nil {
		return kitDBRowMigrationState{}, err
	}
	if err := tx.Put(stateKey, encoded); err != nil {
		_ = tx.Rollback()
		return kitDBRowMigrationState{}, err
	}
	if _, err := tx.Commit(); err != nil {
		return kitDBRowMigrationState{}, err
	}
	return state, nil
}

func advanceKitDBSegmentedMigration(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
	steps []planStep,
) (bool, error) {
	if state.TargetGeneration == 0 {
		return advanceKitDBLegacySegmentedMigration(database, state, steps)
	}
	if state.PrimaryKeyChange && state.Phase == kitDBRowMigrationPhaseVerify {
		return advanceKitDBPrimaryKeyVerification(database, state, steps)
	}
	return advanceKitDBShadowRowMigration(database, state, steps)
}

func advanceKitDBShadowRowMigration(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
	steps []planStep,
) (bool, error) {
	if state.Phase != kitDBRowMigrationPhaseRows || state.TargetGeneration == 0 {
		return false, fmt.Errorf("kitdb: invalid shadow row-migration phase")
	}
	stored, target, err := kitDBRowMigrationDefinitions(state, "pending")
	if err != nil {
		return false, err
	}
	stateKey, err := kitDBRowMigrationStateKey(stored)
	if err != nil {
		return false, err
	}
	sourcePrefix, err := kitDBPhysicalRowPrefix(stored, state.SourceGeneration)
	if err != nil {
		return false, err
	}
	if !bytes.Equal(sourcePrefix, state.RetiredPrefix) {
		return false, fmt.Errorf("kitdb: struct %q row-migration source prefix changed", stored.Name)
	}
	options := kitdbengine.RangeOptions{Prefix: sourcePrefix}
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
	type rowMutation struct {
		key   []byte
		value []byte
	}
	mutations := make([]rowMutation, 0, kitDBRowMigrationRowLimit)
	processed := 0
	seenTargetRows := make(map[string]struct{})
	var targetIndexes []kitDBPhysicalIndex
	if state.PrimaryKeyChange {
		indexes := collectKitDBIndexes(target)
		if !reflect.DeepEqual(kitDBIndexSignatures(indexes), state.TargetIndexes) {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, fmt.Errorf("kitdb: struct %q primary-key target indexes changed", target.Name)
		}
		generations := make(map[string]uint64, len(indexes))
		for _, index := range indexes {
			generations[kitDBIndexSignature(index)] = state.TargetGeneration
		}
		targetIndexes = kitDBPhysicalIndexes(indexes, generations)
	}
	lastKey := bytes.Clone(state.Progress)
	mutationBytes := len(stateKey) + len(state.SourceDefinition) + len(state.TargetDefinition) + 1024
	more := false
	migrationTime := time.Unix(0, state.MigrationUnixNano).UTC()
	rowLimit := kitDBRowMigrationRowLimit
	if state.PrimaryKeyChange {
		// Rekey already fences table writes, so it can amortize cursor and WAL
		// overhead while retaining the shared byte and mutation ceilings.
		rowLimit = kitDBPrimaryKeyBuildRowLimit
	}
	for cursor.Next() {
		physicalSourceKey := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(physicalSourceKey, state.Progress) {
			continue
		}
		if processed >= rowLimit {
			more = true
			break
		}
		decoded, err := decodeKitDBRow(stored, cursor.Value())
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		migrated, err := transformKitDBMigrationRow(stored, target, decoded.values, migrationTime)
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		if err := validateKitDBRow(target, migrated); err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, fmt.Errorf("kitdb: struct %q migration validation: %w", target.Name, err)
		}
		logicalKey, err := kitDBLogicalRowKey(stored, physicalSourceKey, state.SourceGeneration)
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		targetLogicalKey, err := kitDBRowKeyForRow(target, migrated)
		if err != nil || (!state.PrimaryKeyChange && !bytes.Equal(targetLogicalKey, logicalKey)) {
			_ = cursor.Close()
			_ = snapshot.Close()
			if err != nil {
				return false, err
			}
			return false, fmt.Errorf("kitdb: struct %q migration would change a primary key", target.Name)
		}
		encoded, err := encodeKitDBValidatedRow(target, migrated, decoded.unknown)
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		physicalTargetKey, err := kitDBPhysicalRowKey(target, targetLogicalKey, state.TargetGeneration)
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		rowMutations := []rowMutation{{key: physicalTargetKey, value: encoded}}
		if state.PrimaryKeyChange {
			if _, duplicate := seenTargetRows[string(physicalTargetKey)]; duplicate {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, fmt.Errorf(
					"kitdb: struct %q migration creates duplicate primary key (%s)",
					target.Name, strings.Join(kitDBPrimaryFieldNames(target), ", "),
				)
			}
			entries, err := kitDBSecondaryIndexEntriesForPhysical(
				target, migrated, targetLogicalKey, targetIndexes,
			)
			if err != nil {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, err
			}
			for _, entry := range entries {
				rowMutations = append(rowMutations, rowMutation{key: entry.key, value: entry.value})
			}
		}
		nextBytes := 0
		for _, mutation := range rowMutations {
			nextBytes += len(mutation.key) + len(mutation.value)
		}
		if len(mutations)+len(rowMutations)+1 > kitDBRowMigrationMutationLimit ||
			mutationBytes+nextBytes > kitDBRowMigrationByteLimit {
			if processed == 0 {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, fmt.Errorf(
					"kitdb: struct %q has one row that exceeds the shadow migration chunk budget",
					target.Name,
				)
			}
			more = true
			break
		}
		mutations = append(mutations, rowMutations...)
		seenTargetRows[string(physicalTargetKey)] = struct{}{}
		processed++
		lastKey = bytes.Clone(physicalSourceKey)
		mutationBytes += nextBytes
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
		if errors.Is(cause, kitdbengine.ErrTransactionTooLarge) {
			return false, fmt.Errorf("kitdb: struct %q shadow migration chunk exceeds the transaction limit", target.Name)
		}
		return false, cause
	}
	for _, mutation := range mutations {
		if err := tx.Put(mutation.key, mutation.value); err != nil {
			return rollback(err)
		}
	}
	state.Rows += uint64(processed)
	state.Progress = lastKey
	complete := false
	if more {
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else if state.PrimaryKeyChange {
		// Cross-chunk collisions are proved by a bounded unique-row count over
		// the target generation. Avoiding one miss lookup per source row keeps
		// this build linear as immutable segment count grows.
		state.Phase = kitDBRowMigrationPhaseVerify
		state.VerifiedRows = 0
		state.Progress = nil
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else {
		if err := stageKitDBShadowMigrationCutover(
			database, tx, state, stored, target, steps, migrationTime, stateKey,
		); err != nil {
			return rollback(err)
		}
		complete = true
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return complete, nil
}

func advanceKitDBPrimaryKeyVerification(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
	steps []planStep,
) (bool, error) {
	if !state.PrimaryKeyChange || state.Phase != kitDBRowMigrationPhaseVerify ||
		state.TargetGeneration == 0 {
		return false, fmt.Errorf("kitdb: invalid primary-key verification state")
	}
	stored, target, err := kitDBRowMigrationDefinitions(state, "primary-key verification")
	if err != nil {
		return false, err
	}
	stateKey, err := kitDBRowMigrationStateKey(stored)
	if err != nil {
		return false, err
	}
	targetPrefix, err := kitDBPhysicalRowPrefix(target, state.TargetGeneration)
	if err != nil {
		return false, err
	}
	options := kitdbengine.RangeOptions{Prefix: targetPrefix}
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
	verified := 0
	lastKey := bytes.Clone(state.Progress)
	more := false
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if verified >= kitDBPrimaryKeyVerifyRowLimit {
			more = true
			break
		}
		verified++
		lastKey = bytes.Clone(key)
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close()
		_ = snapshot.Close()
		return false, err
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	nextVerified := state.VerifiedRows + uint64(verified)
	if nextVerified > state.Rows {
		return false, fmt.Errorf("kitdb: struct %q primary-key verification exceeded its source row count", target.Name)
	}
	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	rollback := func(cause error) (bool, error) {
		_ = tx.Rollback()
		return false, cause
	}
	state.VerifiedRows = nextVerified
	state.Progress = lastKey
	if more {
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else {
		if state.VerifiedRows != state.Rows {
			return rollback(fmt.Errorf(
				"kitdb: struct %q migration creates a duplicate primary key (%s): %d source rows produced %d unique target rows",
				target.Name, strings.Join(kitDBPrimaryFieldNames(target), ", "),
				state.Rows, state.VerifiedRows,
			))
		}
		state.Progress = nil
		migrationTime := time.Unix(0, state.MigrationUnixNano).UTC()
		if err := stageKitDBShadowMigrationCutover(
			database, tx, state, stored, target, steps, migrationTime, stateKey,
		); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return !more, nil
}

func stageKitDBShadowMigrationCutover(
	database *kitdbengine.DB,
	tx *kitdbengine.Tx,
	state kitDBRowMigrationState,
	stored, target *StructDef,
	steps []planStep,
	migrationTime time.Time,
	stateKey []byte,
) error {
	metadata, _, err := loadKitDBRowGenerationMetadata(database, target)
	if err != nil {
		return err
	}
	if metadata.Active != state.SourceGeneration {
		return fmt.Errorf("kitdb: struct %q active row generation changed during migration", target.Name)
	}
	if metadata.Epoch == ^uint64(0) {
		return fmt.Errorf("kitdb: row layout epoch space is exhausted")
	}
	metadata.Epoch++
	metadata.Active = state.TargetGeneration
	metadata.Retired = appendKitDBRetiredRowPrefixes(metadata.Retired, [][]byte{state.RetiredPrefix})
	metadataKey, err := kitDBRowGenerationMetadataKey(target)
	if err != nil {
		return err
	}
	encodedMetadata, err := encodeKitDBRowGenerationMetadata(metadata)
	if err != nil {
		return err
	}
	var indexMetadataKey, encodedIndexMetadata []byte
	if state.PrimaryKeyChange {
		indexMetadata, _, err := loadKitDBIndexGenerationMetadata(database, stored)
		if err != nil {
			return err
		}
		if indexMetadata.Epoch != state.SourceIndexEpoch {
			return fmt.Errorf(
				"kitdb: struct %q index layout changed during primary-key migration",
				target.Name,
			)
		}
		currentRetired := make([][]byte, 0, len(collectKitDBIndexes(stored)))
		for _, index := range collectKitDBIndexes(stored) {
			prefix, err := kitDBIndexBasePrefixForGeneration(
				stored, index, kitDBPhysicalIndexGeneration(indexMetadata, index),
			)
			if err != nil {
				return err
			}
			currentRetired = append(currentRetired, prefix)
		}
		sort.Slice(currentRetired, func(left, right int) bool {
			return bytes.Compare(currentRetired[left], currentRetired[right]) < 0
		})
		if !reflect.DeepEqual(currentRetired, state.RetiredIndexes) {
			return fmt.Errorf(
				"kitdb: struct %q source indexes changed during primary-key migration",
				target.Name,
			)
		}
		if indexMetadata.Epoch == ^uint64(0) {
			return fmt.Errorf("kitdb: index layout epoch space is exhausted")
		}
		active := make(map[string]uint64, len(state.TargetIndexes))
		for _, signature := range state.TargetIndexes {
			active[signature] = state.TargetGeneration
		}
		indexMetadata.Epoch++
		indexMetadata.Active = active
		indexMetadata.Retired = appendKitDBRetiredPrefixes(
			indexMetadata.Retired, state.RetiredIndexes,
		)
		indexMetadataKey, err = kitDBIndexGenerationMetadataKey(target)
		if err != nil {
			return err
		}
		encodedIndexMetadata, err = encodeKitDBIndexGenerationMetadata(indexMetadata)
		if err != nil {
			return err
		}
	}
	batch := kitDBMigrationBatchID([]*kitDBSchemaPlanEntry{{
		stored: stored, current: target, from: stored.Hash, steps: steps,
	}})
	catalog, auditKey, audit, err := encodeKitDBMigrationAt(
		target, steps, stored.Hash, batch, migrationTime,
	)
	if err != nil {
		return err
	}
	state.Phase = kitDBRowMigrationPhaseCleanup
	state.CleanupRows = 0
	state.CleanupIndex = 0
	state.Progress = nil
	encodedState, err := encodeKitDBRowMigrationState(state)
	if err != nil {
		return err
	}
	if err := tx.Put(metadataKey, encodedMetadata); err != nil {
		return err
	}
	if len(encodedIndexMetadata) != 0 {
		if err := tx.Put(indexMetadataKey, encodedIndexMetadata); err != nil {
			return err
		}
	}
	if err := tx.DefineStruct(catalog); err != nil {
		return err
	}
	if len(audit) != 0 {
		if err := tx.Put(auditKey, audit); err != nil {
			return err
		}
	}
	return tx.Put(stateKey, encodedState)
}

func advanceKitDBLegacySegmentedMigration(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
	steps []planStep,
) (bool, error) {
	stored, target, err := kitDBRowMigrationDefinitions(state, "pending")
	if err != nil {
		return false, err
	}
	stateKey, err := kitDBRowMigrationStateKey(stored)
	if err != nil {
		return false, err
	}
	prefix, err := kitDBRowPrefix(stored)
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
	type rowMutation struct {
		key   []byte
		value []byte
	}
	mutations := make([]rowMutation, 0, kitDBRowMigrationRowLimit)
	lastKey := bytes.Clone(state.Progress)
	mutationBytes := len(stateKey) + len(state.SourceDefinition) + len(state.TargetDefinition) + 512
	more := false
	migrationTime := time.Unix(0, state.MigrationUnixNano).UTC()
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if len(mutations) >= kitDBRowMigrationRowLimit {
			more = true
			break
		}
		decoded, err := decodeKitDBRow(stored, cursor.Value())
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		migrated, err := transformKitDBMigrationRow(stored, target, decoded.values, migrationTime)
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		if err := validateKitDBRow(target, migrated); err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, fmt.Errorf("kitdb: struct %q migration validation: %w", target.Name, err)
		}
		rowKey, err := kitDBRowKeyForRow(target, migrated)
		if err != nil || !bytes.Equal(rowKey, key) {
			_ = cursor.Close()
			_ = snapshot.Close()
			if err != nil {
				return false, err
			}
			return false, fmt.Errorf("kitdb: struct %q migration would change a primary key", target.Name)
		}
		encoded, err := encodeKitDBValidatedRow(target, migrated, decoded.unknown)
		if err != nil {
			_ = cursor.Close()
			_ = snapshot.Close()
			return false, err
		}
		nextBytes := len(key) + len(encoded)
		if mutationBytes+nextBytes > kitDBRowMigrationByteLimit {
			if len(mutations) == 0 {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, fmt.Errorf(
					"kitdb: struct %q has one row that exceeds the segmented migration chunk budget",
					target.Name,
				)
			}
			more = true
			break
		}
		mutations = append(mutations, rowMutation{key: bytes.Clone(key), value: encoded})
		lastKey = bytes.Clone(key)
		mutationBytes += nextBytes
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close()
		_ = snapshot.Close()
		return false, err
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	nextRows := state.Rows + uint64(len(mutations))
	if !more && nextRows != state.TotalRows {
		return false, fmt.Errorf(
			"kitdb: struct %q segmented migration row count changed from %d to %d",
			target.Name, state.TotalRows, nextRows,
		)
	}
	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	rollback := func(cause error) (bool, error) {
		_ = tx.Rollback()
		if errors.Is(cause, kitdbengine.ErrTransactionTooLarge) {
			return false, fmt.Errorf("kitdb: struct %q segmented migration chunk exceeds the transaction limit", target.Name)
		}
		return false, cause
	}
	for _, mutation := range mutations {
		if err := tx.Put(mutation.key, mutation.value); err != nil {
			return rollback(err)
		}
	}
	state.Rows = nextRows
	state.Progress = lastKey
	if more {
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else {
		batch := kitDBMigrationBatchID([]*kitDBSchemaPlanEntry{{
			stored: stored, current: target, from: stored.Hash, steps: steps,
		}})
		catalog, auditKey, audit, err := encodeKitDBMigrationAt(
			target, steps, stored.Hash, batch, migrationTime,
		)
		if err != nil {
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
		if err := tx.Delete(stateKey); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return !more, nil
}

func beginKitDBRowMigrationCancellation(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
) (kitDBRowMigrationState, error) {
	if state.TargetGeneration == 0 {
		return state, fmt.Errorf("kitdb: legacy in-place row migrations cannot be cancelled safely")
	}
	if state.Phase == kitDBRowMigrationPhaseCancel {
		return state, nil
	}
	if state.Phase != kitDBRowMigrationPhaseRows && state.Phase != kitDBRowMigrationPhaseVerify {
		return state, fmt.Errorf("kitdb: row migration cannot be cancelled after target publication")
	}
	source, target, err := kitDBRowMigrationDefinitions(state, "cancel")
	if err != nil {
		return state, err
	}
	activeHash, found, err := database.CatalogStructHashByID(source.ID)
	if err != nil {
		return state, err
	}
	if !found || activeHash != state.SourceHash {
		return state, fmt.Errorf("kitdb: row migration cancellation lost its active source catalog")
	}
	metadata, _, err := loadKitDBRowGenerationMetadata(database, source)
	if err != nil {
		return state, err
	}
	if metadata.Active != state.SourceGeneration {
		return state, fmt.Errorf("kitdb: row migration cancellation lost its active source generation")
	}
	sourcePrefix, err := kitDBPhysicalRowPrefix(source, state.SourceGeneration)
	if err != nil {
		return state, err
	}
	targetPrefix, err := kitDBPhysicalRowPrefix(target, state.TargetGeneration)
	if err != nil {
		return state, err
	}
	if !bytes.Equal(sourcePrefix, state.RetiredPrefix) || bytes.Equal(sourcePrefix, targetPrefix) {
		return state, fmt.Errorf("kitdb: row migration cancellation has invalid physical generations")
	}
	state.Phase = kitDBRowMigrationPhaseCancel
	state.VerifiedRows = 0
	state.CleanupRows = 0
	state.CleanupIndex = 0
	state.Progress = nil
	encoded, err := encodeKitDBRowMigrationState(state)
	if err != nil {
		return state, err
	}
	stateKey, err := kitDBRowMigrationStateKey(source)
	if err != nil {
		return state, err
	}
	tx, err := database.Begin()
	if err != nil {
		return state, err
	}
	if err := tx.Put(stateKey, encoded); err != nil {
		_ = tx.Rollback()
		return state, err
	}
	if _, err := tx.Commit(); err != nil {
		return state, err
	}
	return state, nil
}

func advanceKitDBCancelledRowCleanup(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
) (bool, int, error) {
	if state.PrimaryKeyChange {
		return advanceKitDBPrimaryKeyMigrationCleanup(database, state, true)
	}
	if state.TargetGeneration == 0 || state.Phase != kitDBRowMigrationPhaseCancel {
		return false, 0, fmt.Errorf("kitdb: invalid cancelled row-migration cleanup state")
	}
	source, target, err := kitDBRowMigrationDefinitions(state, "cancel")
	if err != nil {
		return false, 0, err
	}
	stateKey, err := kitDBRowMigrationStateKey(source)
	if err != nil {
		return false, 0, err
	}
	targetPrefix, err := kitDBPhysicalRowPrefix(target, state.TargetGeneration)
	if err != nil {
		return false, 0, err
	}
	options := kitdbengine.RangeOptions{Prefix: targetPrefix}
	if len(state.Progress) != 0 {
		options.Start = bytes.Clone(state.Progress)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return false, 0, err
	}
	cursor, err := snapshot.Cursor(options)
	if err != nil {
		_ = snapshot.Close()
		return false, 0, err
	}
	keys := make([][]byte, 0, kitDBRowMigrationRowLimit)
	lastKey := bytes.Clone(state.Progress)
	mutationBytes := len(stateKey) + len(state.SourceDefinition) + len(state.TargetDefinition) + 512
	more := false
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if len(keys) >= kitDBRowMigrationRowLimit || mutationBytes+len(key) > kitDBRowMigrationByteLimit {
			if len(keys) == 0 {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, 0, fmt.Errorf(
					"kitdb: struct %q has one shadow key that exceeds the cancellation chunk budget",
					target.Name,
				)
			}
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
		return false, 0, err
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	activeHash, found, err := database.CatalogStructHashByID(source.ID)
	if err != nil {
		return false, 0, err
	}
	if !found || activeHash != state.SourceHash {
		return false, 0, fmt.Errorf("kitdb: cancelled row migration lost its source catalog")
	}
	metadata, _, err := loadKitDBRowGenerationMetadata(database, source)
	if err != nil {
		return false, 0, err
	}
	if metadata.Active != state.SourceGeneration {
		return false, 0, fmt.Errorf("kitdb: cancelled row migration lost its source generation")
	}
	tx, err := database.Begin()
	if err != nil {
		return false, 0, err
	}
	rollback := func(cause error) (bool, int, error) {
		_ = tx.Rollback()
		return false, 0, cause
	}
	for _, key := range keys {
		if err := tx.Delete(key); err != nil {
			return rollback(err)
		}
	}
	state.CleanupRows += uint64(len(keys))
	state.Progress = lastKey
	if more {
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else if err := tx.Delete(stateKey); err != nil {
		return rollback(err)
	}
	if _, err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return !more, len(keys), nil
}

func advanceKitDBRetiredRowCleanup(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
) (bool, error) {
	if state.PrimaryKeyChange {
		complete, _, err := advanceKitDBPrimaryKeyMigrationCleanup(database, state, false)
		return complete, err
	}
	if state.TargetGeneration == 0 || state.Phase != kitDBRowMigrationPhaseCleanup {
		return false, fmt.Errorf("kitdb: invalid retired row cleanup state")
	}
	_, target, err := kitDBRowMigrationDefinitions(state, "cleanup")
	if err != nil {
		return false, err
	}
	stateKey, err := kitDBRowMigrationStateKey(target)
	if err != nil {
		return false, err
	}
	options := kitdbengine.RangeOptions{Prefix: bytes.Clone(state.RetiredPrefix)}
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
	keys := make([][]byte, 0, kitDBRowMigrationRowLimit)
	lastKey := bytes.Clone(state.Progress)
	mutationBytes := len(stateKey) + len(state.SourceDefinition) + len(state.TargetDefinition) + 512
	more := false
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if len(keys) >= kitDBRowMigrationRowLimit || mutationBytes+len(key) > kitDBRowMigrationByteLimit {
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
	state.CleanupRows += uint64(len(keys))
	state.Progress = lastKey
	if more {
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else {
		metadata, found, err := loadKitDBRowGenerationMetadata(database, target)
		if err != nil {
			return rollback(err)
		}
		if !found || metadata.Active != state.TargetGeneration {
			return rollback(fmt.Errorf("kitdb: retired row cleanup lost active generation metadata"))
		}
		retired := make([][]byte, 0, len(metadata.Retired))
		removed := false
		for _, prefix := range metadata.Retired {
			if bytes.Equal(prefix, state.RetiredPrefix) {
				removed = true
				continue
			}
			retired = append(retired, bytes.Clone(prefix))
		}
		if !removed {
			return rollback(fmt.Errorf("kitdb: retired row cleanup prefix is not published"))
		}
		metadata.Retired = retired
		metadataKey, err := kitDBRowGenerationMetadataKey(target)
		if err != nil {
			return rollback(err)
		}
		encodedMetadata, err := encodeKitDBRowGenerationMetadata(metadata)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(metadataKey, encodedMetadata); err != nil {
			return rollback(err)
		}
		if err := tx.Delete(stateKey); err != nil {
			return rollback(err)
		}
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return !more, nil
}

func advanceKitDBPrimaryKeyMigrationCleanup(
	database *kitdbengine.DB,
	state kitDBRowMigrationState,
	cancel bool,
) (bool, int, error) {
	wantedPhase := kitDBRowMigrationPhaseCleanup
	if cancel {
		wantedPhase = kitDBRowMigrationPhaseCancel
	}
	if !state.PrimaryKeyChange || state.TargetGeneration == 0 || state.Phase != wantedPhase {
		return false, 0, fmt.Errorf("kitdb: invalid primary-key migration cleanup state")
	}
	source, target, err := kitDBRowMigrationDefinitions(state, "rekey cleanup")
	if err != nil {
		return false, 0, err
	}
	stateDefinition := target
	if cancel {
		stateDefinition = source
	}
	stateKey, err := kitDBRowMigrationStateKey(stateDefinition)
	if err != nil {
		return false, 0, err
	}

	prefixes := make([][]byte, 0, len(state.RetiredIndexes)+1)
	if cancel {
		rowPrefix, err := kitDBPhysicalRowPrefix(target, state.TargetGeneration)
		if err != nil {
			return false, 0, err
		}
		prefixes = append(prefixes, rowPrefix)
		indexes, ok := kitDBIndexesMatchingSignatures(
			collectKitDBIndexes(target), state.TargetIndexes,
		)
		if !ok {
			return false, 0, fmt.Errorf("kitdb: struct %q primary-key target indexes changed", target.Name)
		}
		indexPrefixes := make([][]byte, 0, len(indexes))
		for _, index := range indexes {
			prefix, err := kitDBIndexBasePrefixForGeneration(target, index, state.TargetGeneration)
			if err != nil {
				return false, 0, err
			}
			indexPrefixes = append(indexPrefixes, prefix)
		}
		sort.Slice(indexPrefixes, func(left, right int) bool {
			return bytes.Compare(indexPrefixes[left], indexPrefixes[right]) < 0
		})
		prefixes = append(prefixes, indexPrefixes...)
	} else {
		prefixes = append(prefixes, bytes.Clone(state.RetiredPrefix))
		prefixes = append(prefixes, cloneKitDBPrefixes(state.RetiredIndexes)...)
	}
	if state.CleanupIndex >= uint64(len(prefixes)) {
		return false, 0, fmt.Errorf("kitdb: primary-key cleanup cursor exceeds its prefix set")
	}
	prefix := prefixes[state.CleanupIndex]
	options := kitdbengine.RangeOptions{Prefix: prefix}
	if len(state.Progress) != 0 {
		options.Start = bytes.Clone(state.Progress)
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return false, 0, err
	}
	cursor, err := snapshot.Cursor(options)
	if err != nil {
		_ = snapshot.Close()
		return false, 0, err
	}
	keys := make([][]byte, 0, kitDBPrimaryKeyCleanupKeyLimit)
	lastKey := bytes.Clone(state.Progress)
	mutationBytes := len(stateKey) + len(state.SourceDefinition) + len(state.TargetDefinition) + 512
	more := false
	for cursor.Next() {
		key := cursor.Key()
		if len(state.Progress) != 0 && bytes.Equal(key, state.Progress) {
			continue
		}
		if len(keys) >= kitDBPrimaryKeyCleanupKeyLimit || mutationBytes+len(key) > kitDBRowMigrationByteLimit {
			if len(keys) == 0 {
				_ = cursor.Close()
				_ = snapshot.Close()
				return false, 0, fmt.Errorf(
					"kitdb: struct %q has one retired key that exceeds the cleanup chunk budget",
					target.Name,
				)
			}
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
		return false, 0, err
	}
	_ = cursor.Close()
	_ = snapshot.Close()

	activeHash, found, err := database.CatalogStructHashByID(source.ID)
	if err != nil {
		return false, 0, err
	}
	wantedHash := state.TargetHash
	if cancel {
		wantedHash = state.SourceHash
	}
	if !found || activeHash != wantedHash {
		return false, 0, fmt.Errorf("kitdb: struct %q primary-key cleanup lost its active catalog", target.Name)
	}
	tx, err := database.Begin()
	if err != nil {
		return false, 0, err
	}
	rollback := func(cause error) (bool, int, error) {
		_ = tx.Rollback()
		return false, 0, cause
	}
	for _, key := range keys {
		if err := tx.Delete(key); err != nil {
			return rollback(err)
		}
	}
	state.CleanupRows += uint64(len(keys))
	state.Progress = lastKey
	finished := false
	if more {
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else if state.CleanupIndex+1 < uint64(len(prefixes)) {
		state.CleanupIndex++
		state.Progress = nil
		encoded, err := encodeKitDBRowMigrationState(state)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(stateKey, encoded); err != nil {
			return rollback(err)
		}
	} else if cancel {
		rowMetadata, _, err := loadKitDBRowGenerationMetadata(database, source)
		if err != nil {
			return rollback(err)
		}
		indexMetadata, _, err := loadKitDBIndexGenerationMetadata(database, source)
		if err != nil {
			return rollback(err)
		}
		if rowMetadata.Active != state.SourceGeneration || indexMetadata.Epoch != state.SourceIndexEpoch {
			return rollback(fmt.Errorf("kitdb: struct %q source layout changed during cancelled primary-key migration", source.Name))
		}
		if err := tx.Delete(stateKey); err != nil {
			return rollback(err)
		}
		finished = true
	} else {
		rowMetadata, found, err := loadKitDBRowGenerationMetadata(database, target)
		if err != nil {
			return rollback(err)
		}
		if !found || rowMetadata.Active != state.TargetGeneration {
			return rollback(fmt.Errorf("kitdb: primary-key cleanup lost active row generation metadata"))
		}
		rowMetadata.Retired = removeKitDBRetiredPrefixes(rowMetadata.Retired, [][]byte{state.RetiredPrefix})
		rowMetadataKey, err := kitDBRowGenerationMetadataKey(target)
		if err != nil {
			return rollback(err)
		}
		encodedRows, err := encodeKitDBRowGenerationMetadata(rowMetadata)
		if err != nil {
			return rollback(err)
		}
		indexMetadata, _, err := loadKitDBIndexGenerationMetadata(database, target)
		if err != nil {
			return rollback(err)
		}
		indexMetadata.Retired = removeKitDBRetiredPrefixes(indexMetadata.Retired, state.RetiredIndexes)
		indexMetadataKey, err := kitDBIndexGenerationMetadataKey(target)
		if err != nil {
			return rollback(err)
		}
		encodedIndexes, err := encodeKitDBIndexGenerationMetadata(indexMetadata)
		if err != nil {
			return rollback(err)
		}
		if err := tx.Put(rowMetadataKey, encodedRows); err != nil {
			return rollback(err)
		}
		if err := tx.Put(indexMetadataKey, encodedIndexes); err != nil {
			return rollback(err)
		}
		if err := tx.Delete(stateKey); err != nil {
			return rollback(err)
		}
		finished = true
	}
	if _, err := tx.Commit(); err != nil {
		return false, 0, err
	}
	return finished, len(keys), nil
}

func cloneKitDBPrefixes(prefixes [][]byte) [][]byte {
	cloned := make([][]byte, len(prefixes))
	for index, prefix := range prefixes {
		cloned[index] = bytes.Clone(prefix)
	}
	return cloned
}

func removeKitDBRetiredPrefixes(current, removed [][]byte) [][]byte {
	wanted := make(map[string]struct{}, len(removed))
	for _, prefix := range removed {
		wanted[string(prefix)] = struct{}{}
	}
	retained := make([][]byte, 0, len(current))
	for _, prefix := range current {
		if _, drop := wanted[string(prefix)]; !drop {
			retained = append(retained, bytes.Clone(prefix))
		}
	}
	return retained
}

func applyKitDBSegmentedMigration(
	database *kitdbengine.DB,
	entry *kitDBSchemaPlanEntry,
	migrationTime time.Time,
	admissionRows uint64,
) error {
	state, found, err := loadKitDBRowMigrationState(database, entry.stored)
	if err != nil {
		return err
	}
	if found {
		if state.SourceHash != entry.stored.Hash || state.TargetHash != entry.current.Hash {
			return kitDBRowMigrationPendingError(entry.current.Name, state.SourceHash, state.TargetHash)
		}
	} else {
		state, err = beginKitDBSegmentedMigration(
			database, entry.stored, entry.current, migrationTime, admissionRows,
		)
		if err != nil {
			return err
		}
	}
	if state.TargetGeneration == 0 {
		for {
			complete, err := advanceKitDBSegmentedMigration(database, state, entry.steps)
			if err != nil || complete {
				return err
			}
			state, found, err = loadKitDBRowMigrationState(database, entry.stored)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("kitdb: struct %q lost its segmented migration cursor", entry.current.Name)
			}
		}
	}
	complete, err := advanceKitDBSegmentedMigration(database, state, entry.steps)
	if err != nil {
		return err
	}
	if !complete {
		return kitDBRowMigrationPendingError(entry.current.Name, state.SourceHash, state.TargetHash)
	}
	return nil
}

func maybeApplyKitDBSegmentedMigrations(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	entries []*kitDBSchemaPlanEntry,
	migrationTime time.Time,
) (bool, error) {
	if len(entries) != 1 {
		return false, nil
	}
	entry := entries[0]
	if entry == nil || entry.stored == nil || entry.current == nil {
		return false, nil
	}
	candidate := kitDBSegmentedMigrationEligible(entry, definitions)
	if state, found, err := loadKitDBRowMigrationState(database, entry.stored); err != nil {
		return true, err
	} else if found {
		if state.SourceHash != entry.stored.Hash || state.TargetHash != entry.current.Hash {
			return true, kitDBRowMigrationPendingError(entry.current.Name, state.SourceHash, state.TargetHash)
		}
		if !candidate {
			return true, fmt.Errorf(
				"%w: resumed target for struct %q is no longer eligible; planned steps: %+v",
				errKitDBRowMigrationPending, entry.current.Name, entry.steps,
			)
		}
		return true, applyKitDBSegmentedMigration(database, entry, migrationTime, 0)
	}
	if !candidate {
		return false, nil
	}
	if kitDBPrimaryKeyMigrationCandidate(entry, definitions) {
		return true, applyKitDBSegmentedMigration(
			database, entry, migrationTime, kitDBAtomicMigrationRows+1,
		)
	}
	exceeds, err := kitDBMigrationExceedsAtomicRows(database, entry.stored)
	if err != nil || !exceeds {
		return false, err
	}
	return true, applyKitDBSegmentedMigration(
		database, entry, migrationTime, kitDBAtomicMigrationRows+1,
	)
}
