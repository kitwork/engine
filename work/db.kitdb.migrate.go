package work

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbsql "github.com/kitwork/engine/kitdb/sql"
	"github.com/kitwork/engine/value"
)

const (
	kitDBMigrationNamespace byte = 0x02
	kitDBPhysicalNamespace  byte = 0x03

	kitDBAtomicMigrationRows = 10_000
	kitDBMaximumExactInteger = float64(1<<53 - 1)
)

var (
	kitDBSecondaryIndexCodecMarkerV2 = []byte("KITDB-SECONDARY-INDEX\x02")
	kitDBSecondaryIndexCodecMarker   = []byte("KITDB-SECONDARY-INDEX\x03")
)

func kitDBKnownSecondaryIndexCodecMarker(marker []byte) bool {
	return bytes.Equal(marker, kitDBSecondaryIndexCodecMarkerV2) ||
		bytes.Equal(marker, kitDBSecondaryIndexCodecMarker)
}

// ensureKitDBSecondaryIndexCodec publishes an empty durable build intent and
// leaves all row/cleanup progress to the node worker. Empty databases can
// publish the marker directly because there is no legacy data to translate.
// The caller serializes this function with relational writes through writeMu.
func ensureKitDBSecondaryIndexCodec(database *kitdbengine.DB, declared *StructDef) (bool, error) {
	if database == nil || declared == nil {
		return false, fmt.Errorf("kitdb: secondary-index codec upgrade is unavailable")
	}
	markerKey, err := kitDBSecondaryIndexCodecMarkerKey(declared)
	if err != nil {
		return false, err
	}
	marker, found, err := database.Get(markerKey)
	if err != nil {
		return false, err
	}
	state, stateFound, err := loadKitDBIndexBuildState(database, declared, kitDBIndexBuildCodec)
	if err != nil {
		return false, err
	}
	if found {
		if !kitDBKnownSecondaryIndexCodecMarker(marker) {
			return false, fmt.Errorf("kitdb: struct %q has an unsupported secondary-index codec marker", declared.Name)
		}
		if bytes.Equal(marker, kitDBSecondaryIndexCodecMarker) {
			if stateFound && state.Phase != kitDBIndexBuildPhaseCleanup {
				return false, fmt.Errorf("kitdb: struct %q published its index codec before row build completion", declared.Name)
			}
			return true, nil
		}
		if stateFound {
			// A v2 marker remains authoritative while either its old cleanup
			// or the v3 ordered-primary build owns the one codec state key.
			return false, nil
		}
	}
	if stateFound && state.Phase == kitDBIndexBuildPhaseCleanup {
		return false, fmt.Errorf("kitdb: struct %q lost its published secondary-index codec marker", declared.Name)
	}
	if stateFound {
		// Resume exactly the signatures admitted by the older process. The
		// worker will publish v2 or v3 according to the completed contract.
		return false, nil
	}

	entry, catalogFound, err := database.CatalogStructByID(declared.ID)
	if err != nil || !catalogFound {
		return true, err
	}
	stored, err := decodeKitDBCatalog(entry.Definition, declared.Name)
	if err != nil {
		return false, err
	}
	indexes := collectKitDBIndexes(stored)
	if found && len(kitDBImplicitPrimaryIndexes(stored)) == 0 {
		return publishKitDBSecondaryIndexCodecMarker(database, markerKey)
	}
	publishMarker := len(indexes) == 0
	if !publishMarker {
		hasRows, err := kitDBHasRows(database, stored)
		if err != nil {
			return false, err
		}
		publishMarker = !hasRows
	}
	if publishMarker {
		return publishKitDBSecondaryIndexCodecMarker(database, markerKey)
	}
	_, err = admitKitDBIndexBuild(
		database, stored, stored, kitDBIndexBuildCodec, indexes, nil,
	)
	return false, err
}

func publishKitDBSecondaryIndexCodecMarker(
	database *kitdbengine.DB,
	markerKey []byte,
) (bool, error) {
	tx, err := database.Begin()
	if err != nil {
		return false, err
	}
	if err := tx.Put(markerKey, kitDBSecondaryIndexCodecMarker); err != nil {
		_ = tx.Rollback()
		return false, err
	}
	if _, err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func kitDBSecondaryIndexCodecMarkerKey(definition *StructDef) ([]byte, error) {
	identity := stableSchemaID("physical", definition.ID+":secondary-index-codec")
	return kitDBFixedKey(kitDBPhysicalNamespace, definition.ID, identity)
}

type kitDBMigrationAudit struct {
	Version   int                       `json:"version"`
	Batch     string                    `json:"batch,omitempty"`
	StructID  string                    `json:"structId"`
	Struct    string                    `json:"struct"`
	From      string                    `json:"from"`
	To        string                    `json:"to"`
	Steps     []kitDBMigrationAuditStep `json:"steps"`
	AppliedAt string                    `json:"appliedAt"`
}

type kitDBMigrationAuditStep struct {
	Action string `json:"action"`
	Field  string `json:"field,omitempty"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
}

type kitDBSchemaPlanMode byte

const (
	kitDBSchemaPlanAtomic kitDBSchemaPlanMode = iota + 1
	kitDBSchemaPlanIndexBuild
	kitDBSchemaPlanIndexCleanup
)

type kitDBSchemaPlanEntry struct {
	mode     kitDBSchemaPlanMode
	stored   *StructDef
	current  *StructDef
	steps    []planStep
	from     string
	indexes  []indexDef
	retired  []indexDef
	build    kitDBIndexBuildState
	rewrite  bool
	validate bool
	rows     []kitDBMigrationRow

	metadata          kitDBIndexGenerationMetadata
	layoutChanged     bool
	sourcePhysical    []kitDBPhysicalIndex
	targetPhysical    []kitDBPhysicalIndex
	targetGenerations map[string]uint64
	rowGeneration     uint64
}

// kitDBMigrationIntent grants one destructive field transition that was
// requested explicitly through ALTER TABLE. Source schema drift never creates
// an intent, so an omitted or retyped struct() field remains fail-closed.
type kitDBMigrationIntent struct {
	actions map[string]string
}

func (intent kitDBMigrationIntent) allows(fieldID, action string) bool {
	return intent.actions != nil && intent.actions[fieldID] == action
}

type kitDBMigrationMutation struct {
	key     []byte
	value   []byte
	deleted bool
}

type kitDBMigrationMutationSet struct {
	byKey map[string]kitDBMigrationMutation
}

func newKitDBMigrationMutationSet() *kitDBMigrationMutationSet {
	return &kitDBMigrationMutationSet{byKey: make(map[string]kitDBMigrationMutation)}
}

func (set *kitDBMigrationMutationSet) Put(key, encoded []byte) error {
	set.byKey[string(key)] = kitDBMigrationMutation{key: bytes.Clone(key), value: bytes.Clone(encoded)}
	return nil
}

func (set *kitDBMigrationMutationSet) Delete(key []byte) error {
	set.byKey[string(key)] = kitDBMigrationMutation{key: bytes.Clone(key), deleted: true}
	return nil
}

func (set *kitDBMigrationMutationSet) ordered() []kitDBMigrationMutation {
	mutations := make([]kitDBMigrationMutation, 0, len(set.byKey))
	for _, mutation := range set.byKey {
		mutations = append(mutations, mutation)
	}
	sort.Slice(mutations, func(left, right int) bool {
		return bytes.Compare(mutations[left].key, mutations[right].key) < 0
	})
	return mutations
}

type kitDBMigrationOverlayReader struct {
	base      kitDBReader
	mutations *kitDBMigrationMutationSet
}

func (reader kitDBMigrationOverlayReader) Get(key []byte) ([]byte, bool, error) {
	if reader.mutations != nil {
		if mutation, found := reader.mutations.byKey[string(key)]; found {
			if mutation.deleted {
				return nil, false, nil
			}
			return bytes.Clone(mutation.value), true, nil
		}
	}
	return reader.base.Get(key)
}

// ensureKitDBSchema is the source-declared schema boundary. The caller holds
// the per-file relational writer gate, so planning observes one catalog and
// every bounded logical change can publish through one ordinary WAL frame.
func ensureKitDBSchema(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	migrate bool,
	checkpoint func() error,
) error {
	return ensureKitDBSchemaWithIntents(database, definitions, migrate, checkpoint, nil)
}

func ensureKitDBSchemaWithIntents(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	migrate bool,
	checkpoint func() error,
	intents map[string]kitDBMigrationIntent,
) error {
	if database == nil || len(definitions) == 0 {
		return fmt.Errorf("kitdb: schema is unavailable")
	}
	names := sortedKitDBDefinitionNames(definitions)
	for _, name := range names {
		if err := validateKitDBStruct(definitions[name], definitions); err != nil {
			return err
		}
	}
	for _, name := range names {
		if err := reconcileStoredKitDBIdentity(database, definitions[name]); err != nil {
			return err
		}
	}
	// Reconciliation restores persisted field identities. Cross-struct
	// constraints must be checked again against that final target graph.
	for _, name := range names {
		if err := validateKitDBStruct(definitions[name], definitions); err != nil {
			return err
		}
	}
	if err := validateKitDBPendingRowMigrations(database, definitions, intents); err != nil {
		return err
	}
	if err := advanceKitDBPendingRowMaintenance(database, definitions); err != nil {
		return err
	}

	codecReady := make(map[string]bool, len(names))
	for _, name := range names {
		definition := definitions[name]
		ready, err := ensureKitDBSecondaryIndexCodec(database, definition)
		if err != nil {
			return err
		}
		codecReady[definition.ID] = ready
		if ready {
			continue
		}
		activeHash, found, err := database.CatalogStructHashByID(definition.ID)
		if err != nil {
			return err
		}
		if found && activeHash != definition.Hash {
			return fmt.Errorf(
				"kitdb: struct %q must finish its resumable codec upgrade before schema migration %s -> %s",
				definition.Name, shortSchemaHash(activeHash), shortSchemaHash(definition.Hash),
			)
		}
	}

	entries, err := planKitDBSchemaWithIntents(database, definitions, migrate, intents)
	if err != nil {
		return err
	}
	atomic := make([]*kitDBSchemaPlanEntry, 0, len(entries))
	indexBuilds := make([]*kitDBSchemaPlanEntry, 0, 1)
	for _, entry := range entries {
		switch entry.mode {
		case kitDBSchemaPlanAtomic:
			atomic = append(atomic, entry)
		case kitDBSchemaPlanIndexBuild:
			indexBuilds = append(indexBuilds, entry)
		case kitDBSchemaPlanIndexCleanup:
			// The durable cleanup cursor belongs to the node worker.
		}
	}
	if len(indexBuilds) != 0 && len(atomic)+len(indexBuilds) != 1 {
		changed := make([]string, 0, len(atomic)+len(indexBuilds))
		for _, entry := range append(append([]*kitDBSchemaPlanEntry(nil), atomic...), indexBuilds...) {
			changed = append(changed, entry.current.Name)
		}
		sort.Strings(changed)
		return fmt.Errorf(
			"kitdb: atomic schema migration for structs (%s) includes a resumable secondary-index build; deploy that index change separately before the remaining schema changes",
			strings.Join(changed, ", "),
		)
	}
	if len(indexBuilds) == 1 {
		if checkpoint == nil {
			return fmt.Errorf("kitdb: migration checkpoint scheduler is unavailable")
		}
		if err := checkpoint(); err != nil {
			return err
		}
		entry := indexBuilds[0]
		if _, err := admitKitDBIndexBuild(
			database, entry.stored, entry.current, kitDBIndexBuildSchema,
			entry.indexes, entry.retired,
		); err != nil {
			return err
		}
	} else if len(atomic) != 0 {
		if kitDBSchemaPlanNeedsRows(atomic) {
			if checkpoint == nil {
				return fmt.Errorf("kitdb: migration checkpoint scheduler is unavailable")
			}
			if err := checkpoint(); err != nil {
				return err
			}
		}
		if err := applyKitDBMigrations(database, definitions, atomic); err != nil {
			return err
		}
	}

	// A fresh catalog had no definition during the first codec pass. Publish
	// its marker only after the complete logical schema transaction is durable.
	for _, name := range names {
		definition := definitions[name]
		if !codecReady[definition.ID] {
			continue
		}
		if _, err := ensureKitDBSecondaryIndexCodec(database, definition); err != nil {
			return err
		}
	}
	return nil
}

func sortedKitDBDefinitionNames(definitions map[string]*StructDef) []string {
	names := make([]string, 0, len(definitions))
	for name := range definitions {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func planKitDBSchema(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	migrate bool,
) ([]*kitDBSchemaPlanEntry, error) {
	return planKitDBSchemaWithIntents(database, definitions, migrate, nil)
}

func planKitDBSchemaWithIntents(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	migrate bool,
	intents map[string]kitDBMigrationIntent,
) ([]*kitDBSchemaPlanEntry, error) {
	entries := make([]*kitDBSchemaPlanEntry, 0, len(definitions))
	for _, name := range sortedKitDBDefinitionNames(definitions) {
		current := definitions[name]
		entry, err := planKitDBSchemaEntryWithIntent(database, current, migrate, intents[current.ID])
		if err != nil {
			return nil, err
		}
		if entry != nil {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func planKitDBSchemaEntry(
	database *kitdbengine.DB,
	current *StructDef,
	migrate bool,
) (*kitDBSchemaPlanEntry, error) {
	return planKitDBSchemaEntryWithIntent(database, current, migrate, kitDBMigrationIntent{})
}

func planKitDBSchemaEntryWithIntent(
	database *kitdbengine.DB,
	current *StructDef,
	migrate bool,
	intent kitDBMigrationIntent,
) (*kitDBSchemaPlanEntry, error) {
	catalog, found, err := database.CatalogStructByID(current.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		if current.catalogOwned {
			// Dropped since this copy was taken. The copy follows the catalog; it
			// does not bring the struct back.
			return nil, nil
		}
		return &kitDBSchemaPlanEntry{mode: kitDBSchemaPlanAtomic, current: current}, nil
	}
	stored, err := decodeKitDBCatalog(catalog.Definition, current.Name)
	if err != nil {
		return nil, err
	}
	if current.catalogOwned && stored.Hash != current.Hash {
		// The catalog moved on after this copy was taken — a concurrent DDL
		// committed. Planning the copy would migrate the struct back to what this
		// reader last saw; on an empty table that applies without a migrate flag
		// and silently drops the other statement's column. The copy is stale, not
		// authoritative: leave the catalog alone and let the reader refresh.
		return nil, nil
	}
	if state, building, err := loadKitDBIndexBuildState(database, stored, kitDBIndexBuildSchema); err != nil {
		return nil, err
	} else if building {
		target, err := decodeKitDBCatalog(state.TargetDefinition, current.Name)
		if err != nil {
			return nil, err
		}
		if state.Phase == kitDBIndexBuildPhaseCleanup {
			if state.TargetHash != stored.Hash || current.Hash != stored.Hash {
				return nil, fmt.Errorf(
					"kitdb: struct %q must finish retired index cleanup for %s before another schema change",
					current.Name, shortSchemaHash(state.TargetHash),
				)
			}
			return &kitDBSchemaPlanEntry{
				mode: kitDBSchemaPlanIndexCleanup, stored: stored, current: current, build: state,
			}, nil
		}
		if state.SourceHash != stored.Hash || state.TargetHash != target.Hash || target.Hash != current.Hash {
			return nil, fmt.Errorf(
				"kitdb: struct %q must finish its pending index build %s -> %s before another schema change",
				current.Name, shortSchemaHash(state.SourceHash), shortSchemaHash(state.TargetHash),
			)
		}
		steps := planKitDBMigration(stored, target, true)
		indexes, retired, ok := kitDBOnlyChangesSecondaryIndexes(steps, stored, target)
		if !ok || !reflect.DeepEqual(kitDBIndexSignatures(indexes), state.Indexes) {
			return nil, fmt.Errorf("kitdb: struct %q pending index build no longer matches its catalog transition", current.Name)
		}
		return &kitDBSchemaPlanEntry{
			mode: kitDBSchemaPlanIndexBuild, stored: stored, current: current,
			steps: steps, from: stored.Hash, indexes: indexes, retired: retired, build: state,
		}, nil
	}
	if stored.Hash == current.Hash {
		if !stored.catalogNeedsUpgrade {
			return nil, nil
		}
		return &kitDBSchemaPlanEntry{
			mode: kitDBSchemaPlanAtomic, stored: stored, current: current,
			from:  stored.catalogHash,
			steps: []planStep{{Action: "catalog_upgrade", From: stored.catalogHash, To: current.Hash, WillApply: true}},
		}, nil
	}

	steps := planKitDBMigrationWithIntent(stored, current, migrate, intent)
	hasRows, err := kitDBHasRows(database, stored)
	if err != nil {
		return nil, err
	}
	if !hasRows {
		for index := range steps {
			steps[index].WillApply = true
		}
		return &kitDBSchemaPlanEntry{
			mode: kitDBSchemaPlanAtomic, stored: stored, current: current,
			from: stored.Hash, steps: steps,
		}, nil
	}
	if !migrate {
		return nil, fmt.Errorf(
			"kitdb: struct %q changed from %s to %s; explicit migration is required (review plan(), then pass { migrate: true })",
			current.Name, shortSchemaHash(stored.Hash), shortSchemaHash(current.Hash),
		)
	}
	if refused := refusedKitDBMigrationSteps(steps); len(refused) != 0 {
		return nil, fmt.Errorf("kitdb: struct %q migration refused: %s", current.Name, strings.Join(refused, "; "))
	}
	if indexes, retired, ok := kitDBOnlyChangesSecondaryIndexes(steps, stored, current); ok {
		return &kitDBSchemaPlanEntry{
			mode: kitDBSchemaPlanIndexBuild, stored: stored, current: current,
			from: stored.Hash, steps: steps, indexes: indexes, retired: retired,
		}, nil
	}
	return &kitDBSchemaPlanEntry{
		mode: kitDBSchemaPlanAtomic, stored: stored, current: current,
		from: stored.Hash, steps: steps,
	}, nil
}

func kitDBSchemaPlanNeedsRows(entries []*kitDBSchemaPlanEntry) bool {
	for _, entry := range entries {
		if entry.stored != nil &&
			(kitDBMigrationNeedsRewrite(entry.stored, entry.current) || kitDBMigrationNeedsValidation(entry.stored, entry.current)) {
			return true
		}
	}
	return false
}

func reconcileStoredKitDBIdentity(database *kitdbengine.DB, definition *StructDef) error {
	entry, found, err := database.CatalogStructByID(definition.ID)
	if err != nil || !found {
		return err
	}
	stored, err := decodeKitDBCatalog(entry.Definition, definition.Name)
	if err != nil {
		return err
	}
	if state, building, err := loadKitDBIndexBuildState(database, stored, kitDBIndexBuildSchema); err != nil {
		return err
	} else if building {
		target, err := decodeKitDBCatalog(state.TargetDefinition, definition.Name)
		if err != nil {
			return err
		}
		if state.Phase == kitDBIndexBuildPhaseCleanup {
			if state.TargetHash != stored.Hash || definition.Hash != stored.Hash {
				return fmt.Errorf(
					"kitdb: struct %q must finish retired index cleanup for %s before another schema change",
					definition.Name,
					shortSchemaHash(state.TargetHash),
				)
			}
			return nil
		}
		if state.SourceHash != stored.Hash || state.TargetHash != target.Hash || target.Hash != definition.Hash {
			return fmt.Errorf(
				"kitdb: struct %q must finish its pending index build %s -> %s before another schema change",
				definition.Name, shortSchemaHash(state.SourceHash), shortSchemaHash(state.TargetHash),
			)
		}
		return nil
	}
	if stored.Hash == definition.Hash {
		return nil
	}
	return reconcileKitDBDefinition(stored, definition)
}

func ensureKitDBCatalog(
	database *kitdbengine.DB,
	definition *StructDef,
	definitions map[string]*StructDef,
	migrate bool,
	checkpoint func() error,
) error {
	entry, found, err := database.CatalogStructByID(definition.ID)
	if err != nil {
		return err
	}
	if !found {
		return commitKitDBCatalog(database, definition, nil, "")
	}

	stored, err := decodeKitDBCatalog(entry.Definition, definition.Name)
	if err != nil {
		return err
	}
	if state, building, err := loadKitDBIndexBuildState(database, stored, kitDBIndexBuildSchema); err != nil {
		return err
	} else if building {
		target, err := decodeKitDBCatalog(state.TargetDefinition, definition.Name)
		if err != nil {
			return err
		}
		if state.Phase == kitDBIndexBuildPhaseCleanup {
			if state.TargetHash != stored.Hash || definition.Hash != stored.Hash {
				return fmt.Errorf(
					"kitdb: struct %q must finish retired index cleanup for %s before another schema change",
					definition.Name,
					shortSchemaHash(state.TargetHash),
				)
			}
			return nil
		}
		if state.SourceHash != stored.Hash || state.TargetHash != target.Hash || target.Hash != definition.Hash {
			return fmt.Errorf(
				"kitdb: struct %q must finish its pending index build %s -> %s before another schema change",
				definition.Name, shortSchemaHash(state.SourceHash), shortSchemaHash(state.TargetHash),
			)
		}
		steps := planKitDBMigration(stored, target, true)
		indexes, _, ok := kitDBOnlyChangesSecondaryIndexes(steps, stored, target)
		if !ok || !reflect.DeepEqual(kitDBIndexSignatures(indexes), state.Indexes) {
			return fmt.Errorf("kitdb: struct %q pending index build no longer matches its catalog transition", definition.Name)
		}
		return nil
	}
	if stored.Hash == definition.Hash {
		if stored.catalogNeedsUpgrade {
			steps := []planStep{{Action: "catalog_upgrade", From: stored.catalogHash, To: definition.Hash, WillApply: true}}
			return commitKitDBCatalog(database, definition, steps, stored.catalogHash)
		}
		return nil
	}
	if err := reconcileKitDBDefinition(stored, definition); err != nil {
		return err
	}
	if stored.Hash == definition.Hash {
		return nil
	}

	steps := planKitDBMigration(stored, definition, migrate)
	hasRows, err := kitDBHasRows(database, stored)
	if err != nil {
		return err
	}
	if !hasRows {
		return commitKitDBCatalog(database, definition, steps, stored.Hash)
	}
	if !migrate {
		return fmt.Errorf(
			"kitdb: struct %q changed from %s to %s; explicit migration is required (review plan(), then pass { migrate: true })",
			definition.Name, shortSchemaHash(stored.Hash), shortSchemaHash(definition.Hash),
		)
	}
	if refused := refusedKitDBMigrationSteps(steps); len(refused) != 0 {
		return fmt.Errorf("kitdb: struct %q migration refused: %s", definition.Name, strings.Join(refused, "; "))
	}
	if checkpoint == nil {
		return fmt.Errorf("kitdb: migration checkpoint scheduler is unavailable")
	}
	if err := checkpoint(); err != nil {
		return err
	}
	if indexes, retired, ok := kitDBOnlyChangesSecondaryIndexes(steps, stored, definition); ok {
		_, err := admitKitDBIndexBuild(
			database, stored, definition, kitDBIndexBuildSchema, indexes, retired,
		)
		return err
	}
	return applyKitDBMigration(database, stored, definition, definitions, steps)
}

func planKitDBCatalog(database *kitdbengine.DB, definition *StructDef, migrate bool) ([]planStep, error) {
	entry, found, err := database.CatalogStructByID(definition.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return []planStep{{Action: "create", WillApply: true}}, nil
	}
	stored, err := decodeKitDBCatalog(entry.Definition, definition.Name)
	if err != nil {
		return nil, err
	}
	if state, building, err := loadKitDBIndexBuildState(database, stored, kitDBIndexBuildSchema); err != nil {
		return nil, err
	} else if building {
		if state.Phase == kitDBIndexBuildPhaseCleanup {
			if state.TargetHash != stored.Hash || definition.Hash != stored.Hash {
				return nil, fmt.Errorf(
					"kitdb: struct %q has retired index cleanup for %s",
					definition.Name,
					shortSchemaHash(state.TargetHash),
				)
			}
			return []planStep{{
				Action: "index_cleanup", Column: definition.Name,
				From: fmt.Sprintf("generation %d", state.CleanupIndex+1),
				To:   fmt.Sprintf("%d generations", len(state.RetiredPrefixes)), WillApply: true,
			}}, nil
		}
		if state.TargetHash != definition.Hash {
			return nil, fmt.Errorf(
				"kitdb: struct %q has pending index target %s, not %s",
				definition.Name, shortSchemaHash(state.TargetHash), shortSchemaHash(definition.Hash),
			)
		}
		return []planStep{{
			Action: "index_build", Column: definition.Name,
			From: fmt.Sprintf("%d rows", state.Rows), To: shortSchemaHash(state.TargetHash), WillApply: true,
		}}, nil
	}
	if stored.Hash == definition.Hash {
		if stored.catalogNeedsUpgrade {
			return []planStep{{Action: "catalog_upgrade", From: stored.catalogHash, To: definition.Hash, WillApply: true}}, nil
		}
		return nil, nil
	}
	if err := reconcileKitDBDefinition(stored, definition); err != nil {
		return nil, err
	}
	if stored.Hash == definition.Hash {
		return nil, nil
	}
	steps := planKitDBMigration(stored, definition, migrate)
	hasRows, err := kitDBHasRows(database, stored)
	if err != nil {
		return nil, err
	}
	if !hasRows {
		for index := range steps {
			steps[index].WillApply = true
		}
	}
	return steps, nil
}

func decodeKitDBCatalog(encoded []byte, name string) (*StructDef, error) {
	var stored StructDef
	if err := json.Unmarshal(encoded, &stored); err != nil {
		return nil, fmt.Errorf("kitdb: decode struct catalog %q: %w", name, err)
	}
	if stored.ID == "" || stored.Name == "" || len(stored.Fields) == 0 {
		return nil, fmt.Errorf("kitdb: struct catalog %q is incomplete", name)
	}
	stored.catalogHash = stored.Hash
	switch stored.Version {
	case 1:
		if err := normalizeLegacyKitDBFieldTags(&stored); err != nil {
			return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
		}
		stored.Version = structIRVersion
		refreshStructHash(&stored)
		stored.catalogNeedsUpgrade = true
	case structIRVersion, structIRPartitionVersion:
		if err := validateKitDBFieldTags(&stored); err != nil {
			return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
		}
		claimedHash := stored.Hash
		refreshStructHash(&stored)
		if stored.Hash != claimedHash {
			return nil, fmt.Errorf("kitdb: struct catalog %q hash mismatch", name)
		}
	default:
		return nil, fmt.Errorf("kitdb: struct catalog %q uses unsupported schema IR version %d", name, stored.Version)
	}
	hydrateKitDBStructColumns(&stored)
	if err := prepareStructCheckConstraints(&stored); err != nil {
		return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
	}
	if err := validateStructUniqueConstraints(&stored); err != nil {
		return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
	}
	if err := validateStructForeignConstraintShape(&stored); err != nil {
		return nil, fmt.Errorf("kitdb: struct catalog %q: %w", name, err)
	}
	return &stored, nil
}

func normalizeLegacyKitDBFieldTags(definition *StructDef) error {
	if definition == nil {
		return fmt.Errorf("field tags are unavailable")
	}
	order := make([]int, len(definition.Fields))
	for index := range definition.Fields {
		order[index] = index
	}
	sort.SliceStable(order, func(left, right int) bool {
		leftField := definition.Fields[order[left]]
		rightField := definition.Fields[order[right]]
		if leftField.Position != rightField.Position {
			return leftField.Position < rightField.Position
		}
		return leftField.Name < rightField.Name
	})
	for position, index := range order {
		definition.Fields[index].Tag = uint32(position + 1)
	}
	definition.NextFieldTag = uint32(len(definition.Fields) + 1)
	return validateKitDBFieldTags(definition)
}

func validateKitDBFieldTags(definition *StructDef) error {
	if definition == nil || len(definition.Fields) == 0 {
		return fmt.Errorf("field tags are unavailable")
	}
	if len(definition.Fields) > kitDBRowFieldLimit {
		return fmt.Errorf("field count exceeds %d", kitDBRowFieldLimit)
	}
	used := make(map[uint32]string, len(definition.Fields))
	owners := make(map[string]string, len(definition.Fields)*2)
	for _, field := range definition.Fields {
		if previous, duplicate := owners[field.Name]; duplicate && previous != field.ID {
			return fmt.Errorf("fields share name %q", field.Name)
		}
		owners[field.Name] = field.ID
	}
	var maximum uint32
	for _, field := range definition.Fields {
		if field.ID == "" || field.Name == "" {
			return fmt.Errorf("field identity is incomplete")
		}
		if typeInfo, found := kitdbsql.LookupKind(field.Kind); !found || typeInfo.Kind != field.Kind {
			return fmt.Errorf("field %q has unsupported kind %q", field.Name, field.Kind)
		}
		if field.Kind == "bigint" {
			return fmt.Errorf("field %q uses standalone BIGINT; the Kitwork VM bridge does not yet preserve full int64 values", field.Name)
		}
		if field.Tag == 0 {
			return fmt.Errorf("field %q has reserved tag 0", field.Name)
		}
		if previous, found := used[field.Tag]; found {
			return fmt.Errorf("fields %q and %q share tag %d", previous, field.Name, field.Tag)
		}
		used[field.Tag] = field.Name
		if field.Tag > maximum {
			maximum = field.Tag
		}
		previousAlias := ""
		for index, alias := range field.Aliases {
			if alias == "" || alias == field.Name {
				return fmt.Errorf("field %q has invalid alias %q", field.Name, alias)
			}
			if index != 0 && alias <= previousAlias {
				return fmt.Errorf("field %q aliases are not strictly ordered", field.Name)
			}
			previousAlias = alias
			if owner, duplicate := owners[alias]; duplicate && owner != field.ID {
				return fmt.Errorf("alias %q belongs to multiple fields", alias)
			}
			owners[alias] = field.ID
		}
	}
	if definition.NextFieldTag == 0 || definition.NextFieldTag <= maximum {
		return fmt.Errorf("next field tag %d must be greater than %d", definition.NextFieldTag, maximum)
	}
	if err := validateKitDBPartition(definition); err != nil {
		return err
	}
	return nil
}

func validateKitDBPartition(definition *StructDef) error {
	if definition == nil || definition.Partition == nil {
		return nil
	}
	partition := definition.Partition
	if definition.Version < structIRPartitionVersion || partition.Version != kitdbsql.PartitionVersion1 {
		return fmt.Errorf("partition policy uses an unsupported version")
	}
	var field *StructFieldDef
	for index := range definition.Fields {
		if definition.Fields[index].Tag == partition.Field {
			field = &definition.Fields[index]
			break
		}
	}
	if field == nil {
		return fmt.Errorf("partition policy references missing field tag %d", partition.Field)
	}
	typeInfo, found := kitdbsql.LookupKind(field.Kind)
	if !found || typeInfo.Family != kitdbsql.FamilyInteger {
		return fmt.Errorf("partition field %q must be an integer", field.Name)
	}
	switch partition.Strategy {
	case "hash":
		if partition.Buckets != kitdbsql.PartitionHashBuckets {
			return fmt.Errorf("HASH partition needs %d buckets", kitdbsql.PartitionHashBuckets)
		}
	case "range":
		if partition.Buckets != 0 {
			return fmt.Errorf("RANGE partition cannot declare hash buckets")
		}
	default:
		return fmt.Errorf("unsupported partition strategy %q", partition.Strategy)
	}
	return nil
}

// reconcileKitDBDefinition replaces declaration-derived field IDs with the
// identities already persisted in the catalog. A .from("old") hint performs
// the same reconciliation across a deliberate rename.
func reconcileKitDBDefinition(stored, current *StructDef) error {
	if stored == nil || current == nil || stored.ID != current.ID || stored.Name != current.Name {
		return fmt.Errorf("kitdb: struct identity changed; refresh the catalog after a table rename")
	}
	declaredNamesByTag := make(map[uint32]string, len(current.Fields))
	for _, field := range current.Fields {
		declaredNamesByTag[field.Tag] = field.Name
	}
	byName := make(map[string]StructFieldDef, len(stored.Fields))
	for _, field := range stored.Fields {
		byName[field.Name] = field
		for _, alias := range field.Aliases {
			if previous, exists := byName[alias]; exists && previous.ID != field.ID {
				return fmt.Errorf("kitdb: struct %q alias %q is ambiguous", stored.Name, alias)
			}
			byName[alias] = field
		}
	}
	claimed := make(map[string]string, len(current.Fields))
	usedTags := make(map[uint32]string, len(stored.Fields)+len(current.Fields))
	for _, field := range stored.Fields {
		usedTags[field.Tag] = field.Name
	}
	nextTag := stored.NextFieldTag
	for index := range current.Fields {
		field := &current.Fields[index]
		previous, found := byName[field.Name]
		if !found && field.From != "" && field.From != field.Name {
			previous, found = byName[field.From]
		}
		if !found {
			if field.From != "" {
				return fmt.Errorf("kitdb: struct %q field %q cannot migrate from missing field %q", current.Name, field.Name, field.From)
			}
			for {
				if nextTag == 0 {
					return fmt.Errorf("kitdb: struct %q exhausted field tags", current.Name)
				}
				if _, used := usedTags[nextTag]; !used {
					break
				}
				nextTag++
			}
			field.Tag = nextTag
			usedTags[nextTag] = field.Name
			nextTag++
			continue
		}
		if field.From != "" && field.From != field.Name {
			if source, sourceFound := byName[field.From]; sourceFound && source.ID != previous.ID {
				return fmt.Errorf("kitdb: struct %q field %q cannot migrate from %q because both names already exist in catalog %v", current.Name, field.Name, field.From, kitDBStructFieldNames(stored))
			}
		}
		if owner, used := claimed[previous.ID]; used {
			return fmt.Errorf("kitdb: struct %q fields %q and %q claim the same persisted identity", current.Name, owner, field.Name)
		}
		claimed[previous.ID] = field.Name
		field.ID = previous.ID
		field.Tag = previous.Tag
		field.Aliases = removeKitDBFieldAlias(previous.Aliases, field.Name)
		if previous.Name != field.Name {
			field.Aliases = appendKitDBFieldAlias(field.Aliases, previous.Name, field.Name)
		}
	}
	currentTagsByName := make(map[string]uint32, len(current.Fields))
	for _, field := range current.Fields {
		currentTagsByName[field.Name] = field.Tag
	}
	for constraintIndex := range current.UniqueConstraints {
		constraint := &current.UniqueConstraints[constraintIndex]
		for fieldIndex, declaredTag := range constraint.Fields {
			name := declaredNamesByTag[declaredTag]
			tag, found := currentTagsByName[name]
			if name == "" || !found {
				return fmt.Errorf(
					"kitdb: struct %q tuple-unique %q lost field tag %d during reconciliation",
					current.Name, constraint.Name, declaredTag,
				)
			}
			constraint.Fields[fieldIndex] = tag
		}
	}
	for constraintIndex := range current.ForeignConstraints {
		constraint := &current.ForeignConstraints[constraintIndex]
		for fieldIndex, declaredTag := range constraint.Fields {
			name := declaredNamesByTag[declaredTag]
			tag, found := currentTagsByName[name]
			if name == "" || !found {
				return fmt.Errorf(
					"kitdb: struct %q composite foreign key %q lost field tag %d during reconciliation",
					current.Name, constraint.Name, declaredTag,
				)
			}
			constraint.Fields[fieldIndex] = tag
		}
	}
	for constraintIndex := range current.CheckConstraints {
		if err := remapStructCheckFieldTags(
			&current.CheckConstraints[constraintIndex].Expression,
			declaredNamesByTag,
			currentTagsByName,
		); err != nil {
			return fmt.Errorf("kitdb: struct %q check %q: %w", current.Name, current.CheckConstraints[constraintIndex].Name, err)
		}
	}
	if current.Partition != nil {
		name := declaredNamesByTag[current.Partition.Field]
		tag, found := currentTagsByName[name]
		if name == "" || !found {
			return fmt.Errorf(
				"kitdb: struct %q partition lost field tag %d during reconciliation",
				current.Name, current.Partition.Field,
			)
		}
		current.Partition.Field = tag
		current.Version = structIRPartitionVersion
	} else if current.Version == structIRPartitionVersion {
		current.Version = structIRVersion
	}
	current.NextFieldTag = nextTag
	refreshStructLookups(current)
	if err := validateKitDBFieldTags(current); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", current.Name, err)
	}
	if err := validateStructUniqueConstraints(current); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", current.Name, err)
	}
	if err := validateStructForeignConstraintShape(current); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", current.Name, err)
	}
	if err := prepareStructCheckConstraints(current); err != nil {
		return fmt.Errorf("kitdb: struct %q: %w", current.Name, err)
	}
	refreshStructHash(current)
	return nil
}

func removeKitDBFieldAlias(aliases []string, current string) []string {
	var filtered []string
	for _, alias := range aliases {
		if alias != current {
			filtered = append(filtered, alias)
		}
	}
	return filtered
}

func appendKitDBFieldAlias(aliases []string, alias, current string) []string {
	if alias == "" || alias == current {
		return aliases
	}
	for _, existing := range aliases {
		if existing == alias {
			return aliases
		}
	}
	aliases = append(aliases, alias)
	sort.Strings(aliases)
	return aliases
}

func kitDBStructFieldNames(definition *StructDef) []string {
	names := make([]string, 0, len(definition.Fields))
	for _, field := range definition.Fields {
		names = append(names, field.Name)
	}
	sort.Strings(names)
	return names
}

func hydrateKitDBStructColumns(definition *StructDef) {
	if definition == nil {
		return
	}
	definition.columns = make(map[string]*ColumnSpec, len(definition.Fields))
	for _, field := range definition.Fields {
		spec := &ColumnSpec{
			kind: field.Kind, primary: field.Primary, primaryOrder: field.PrimaryOrder, notNull: field.NotNull,
			unique: field.Unique, hasDefault: field.HasDefault, def: field.Default,
			defaultNow: field.DefaultNow, touch: field.Updated,
			enumVals: append([]string(nil), field.Enum...), searchable: field.Searchable,
			searchWt: field.SearchWeight, analytics: field.Analytics, seq: uint64(field.Position + 1),
		}
		for _, member := range field.Indexes {
			index := colIndexRef{name: member.Name, id: member.ID, pos: member.Order}
			for _, condition := range member.Filter {
				index.filter = append(index.filter, indexCond{col: condition.Field, val: condition.Value})
			}
			spec.indexes = append(spec.indexes, index)
		}
		if field.Reference != nil {
			spec.fk = &fkRef{
				table: field.Reference.Struct, column: field.Reference.Field,
				onDelete: field.Reference.OnDelete, onUpdate: field.Reference.OnUpdate,
			}
		}
		definition.columns[field.Name] = spec
	}
	fieldsByTag := make(map[uint32]string, len(definition.Fields))
	for _, field := range definition.Fields {
		fieldsByTag[field.Tag] = field.Name
	}
	for _, constraint := range definition.UniqueConstraints {
		for position, tag := range constraint.Fields {
			if spec := definition.columns[fieldsByTag[tag]]; spec != nil {
				spec.uniques = append(spec.uniques, colUniqueRef{name: constraint.Name, pos: position + 1})
			}
		}
	}
	for _, constraint := range definition.ForeignConstraints {
		for position, tag := range constraint.Fields {
			if spec := definition.columns[fieldsByTag[tag]]; spec != nil {
				spec.fk = &fkRef{
					name: constraint.Name, pos: position + 1,
					table: constraint.TargetStruct, targetStructID: constraint.TargetStructID,
					targetFieldID: constraint.TargetFields[position],
					onDelete:      constraint.OnDelete, onUpdate: constraint.OnUpdate,
				}
			}
		}
	}
	if definition.Partition != nil {
		if spec := definition.columns[fieldsByTag[definition.Partition.Field]]; spec != nil {
			spec.partition = definition.Partition.Strategy
		}
	}
}

func planKitDBMigration(stored, current *StructDef, apply bool) []planStep {
	return planKitDBMigrationWithIntent(stored, current, apply, kitDBMigrationIntent{})
}

func planKitDBMigrationWithIntent(
	stored, current *StructDef,
	apply bool,
	intent kitDBMigrationIntent,
) []planStep {
	oldByID := make(map[string]StructFieldDef, len(stored.Fields))
	newByID := make(map[string]StructFieldDef, len(current.Fields))
	for _, field := range stored.Fields {
		oldByID[field.ID] = field
	}
	for _, field := range current.Fields {
		newByID[field.ID] = field
	}

	steps := make([]planStep, 0)
	appendStep := func(action, field, fieldID, from, to string, destructive bool) {
		willApply := apply && !destructive
		if destructive &&
			((intent.allows(fieldID, "drop") && action == "remove") ||
				(intent.allows(fieldID, "type") && action == "field_type") ||
				(intent.allows(fieldID, "reference") && action == "reference") ||
				(intent.allows(fieldID, "primary") && action == "primary")) {
			willApply = apply
		}
		steps = append(steps, planStep{
			Action: action, Column: field, From: from, To: to,
			Destructive: destructive, WillApply: willApply, fieldID: fieldID,
		})
	}
	for _, field := range current.Fields {
		previous, found := oldByID[field.ID]
		if !found {
			spec := current.columns[field.Name]
			unsafe := field.Primary || (field.NotNull && !kitDBMigrationCanFill(spec))
			if field.Reference != nil && kitDBMigrationCanFill(spec) {
				unsafe = true
			}
			appendStep("field_add", field.Name, field.ID, "", field.Kind, unsafe)
			continue
		}
		if previous.Name != field.Name {
			appendStep("rename", field.Name, field.ID, previous.Name, field.Name, false)
		}
		if previous.Kind != field.Kind {
			appendStep("field_type", field.Name, field.ID, previous.Kind, field.Kind, true)
		}
		if previous.Primary != field.Primary || previous.PrimaryOrder != field.PrimaryOrder {
			appendStep(
				"primary", field.Name, field.ID,
				fmt.Sprintf("%t/%d", previous.Primary, previous.PrimaryOrder),
				fmt.Sprintf("%t/%d", field.Primary, field.PrimaryOrder), true,
			)
		}
		if !reflect.DeepEqual(previous.Reference, field.Reference) {
			appendStep("reference", field.Name, field.ID, formatKitDBReference(previous.Reference), formatKitDBReference(field.Reference), true)
		}
		if previous.NotNull != field.NotNull || previous.Unique != field.Unique || !reflect.DeepEqual(previous.Enum, field.Enum) {
			appendStep("constraint", field.Name, field.ID, kitDBConstraintSummary(previous), kitDBConstraintSummary(field), false)
		}
		if !reflect.DeepEqual(previous.Indexes, field.Indexes) {
			appendStep("index", field.Name, field.ID, fmt.Sprint(len(previous.Indexes)), fmt.Sprint(len(field.Indexes)), false)
		}
		if kitDBFieldMetadataChanged(previous, field) {
			appendStep("metadata", field.Name, field.ID, "", "", false)
		}
	}
	for _, field := range stored.Fields {
		if _, found := newByID[field.ID]; !found {
			appendStep("remove", field.Name, field.ID, field.Kind, "", true)
		}
	}
	if !equalKitDBUniqueConstraints(stored.UniqueConstraints, current.UniqueConstraints) {
		appendStep(
			"unique", current.Name, "",
			formatStructUniqueConstraints(stored), formatStructUniqueConstraints(current), false,
		)
	}
	if !equalKitDBForeignConstraints(stored.ForeignConstraints, current.ForeignConstraints) {
		appendStep(
			"foreign", current.Name, "",
			formatStructForeignConstraints(stored), formatStructForeignConstraints(current), false,
		)
	}
	if !equalKitDBCheckConstraints(stored.CheckConstraints, current.CheckConstraints) {
		appendStep(
			"check", current.Name, "",
			formatStructCheckConstraints(stored), formatStructCheckConstraints(current), false,
		)
	}
	if !reflect.DeepEqual(stored.Partition, current.Partition) {
		appendStep(
			"partition", current.Name, "",
			formatKitDBPartition(stored), formatKitDBPartition(current), false,
		)
	}
	if len(steps) == 0 && stored.Hash != current.Hash {
		appendStep("metadata", current.Name, "", stored.Hash, current.Hash, false)
	}
	return steps
}

func formatKitDBPartition(definition *StructDef) string {
	if definition == nil || definition.Partition == nil {
		return ""
	}
	field := fmt.Sprintf("tag:%d", definition.Partition.Field)
	for _, candidate := range definition.Fields {
		if candidate.Tag == definition.Partition.Field {
			field = candidate.Name
			break
		}
	}
	return strings.ToUpper(definition.Partition.Strategy) + "(" + field + ")"
}

func kitDBMigrationCanFill(spec *ColumnSpec) bool {
	if spec == nil {
		return false
	}
	return spec.hasDefault || spec.defaultNow || spec.kind == "kitid" || spec.kind == "uuid" ||
		spec.kind == "year" || spec.kind == "month" || spec.kind == "day"
}

func equalKitDBUniqueConstraints(left, right []StructUniqueConstraint) bool {
	return len(left) == len(right) && (len(left) == 0 || reflect.DeepEqual(left, right))
}

func equalKitDBForeignConstraints(left, right []StructForeignConstraint) bool {
	return len(left) == len(right) && (len(left) == 0 || reflect.DeepEqual(left, right))
}

func equalKitDBCheckConstraints(left, right []StructCheckConstraint) bool {
	return len(left) == len(right) && (len(left) == 0 || reflect.DeepEqual(left, right))
}

func formatKitDBReference(reference *StructReferenceDef) string {
	if reference == nil {
		return ""
	}
	return reference.Struct + "." + reference.Field
}

func kitDBConstraintSummary(field StructFieldDef) string {
	return fmt.Sprintf("notNull=%t unique=%t choice=%v", field.NotNull, field.Unique, field.Enum)
}

func formatStructUniqueConstraints(definition *StructDef) string {
	if definition == nil || len(definition.UniqueConstraints) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(definition.UniqueConstraints))
	fieldsByTag := make(map[uint32]string, len(definition.Fields))
	for _, field := range definition.Fields {
		fieldsByTag[field.Tag] = field.Name
	}
	for _, constraint := range definition.UniqueConstraints {
		fields := make([]string, len(constraint.Fields))
		for index, tag := range constraint.Fields {
			fields[index] = fieldsByTag[tag]
		}
		parts = append(parts, constraint.Name+"("+strings.Join(fields, ",")+")")
	}
	return "[" + strings.Join(parts, ";") + "]"
}

func formatStructForeignConstraints(definition *StructDef) string {
	if definition == nil || len(definition.ForeignConstraints) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(definition.ForeignConstraints))
	fieldsByTag := make(map[uint32]string, len(definition.Fields))
	for _, field := range definition.Fields {
		fieldsByTag[field.Tag] = field.Name
	}
	for _, constraint := range definition.ForeignConstraints {
		fields := make([]string, len(constraint.Fields))
		for index, tag := range constraint.Fields {
			fields[index] = fieldsByTag[tag]
		}
		parts = append(parts, constraint.Name+"("+strings.Join(fields, ",")+")->"+constraint.TargetStruct)
	}
	return "[" + strings.Join(parts, ";") + "]"
}

func kitDBFieldMetadataChanged(previous, current StructFieldDef) bool {
	if previous.Position != current.Position || previous.HasDefault != current.HasDefault ||
		previous.DefaultNow != current.DefaultNow || previous.Updated != current.Updated ||
		previous.Searchable != current.Searchable || previous.SearchWeight != current.SearchWeight ||
		previous.Analytics != current.Analytics ||
		!reflect.DeepEqual(previous.Aliases, current.Aliases) {
		return true
	}
	return previous.HasDefault && !reflect.DeepEqual(previous.Default, current.Default)
}

func refusedKitDBMigrationSteps(steps []planStep) []string {
	refused := make([]string, 0)
	for _, step := range steps {
		if !step.WillApply {
			refused = append(refused, step.describe())
		}
	}
	return refused
}

func applyKitDBMigration(
	database *kitdbengine.DB,
	stored, current *StructDef,
	definitions map[string]*StructDef,
	steps []planStep,
) error {
	return applyKitDBMigrations(database, definitions, []*kitDBSchemaPlanEntry{{
		mode: kitDBSchemaPlanAtomic, stored: stored, current: current,
		from: stored.Hash, steps: steps,
	}})
}

func applyKitDBMigrations(
	database *kitdbengine.DB,
	definitions map[string]*StructDef,
	entries []*kitDBSchemaPlanEntry,
) error {
	if len(entries) == 0 {
		return nil
	}
	migrationTime := time.Now().UTC()
	if handled, err := maybeApplyKitDBSegmentedMigrations(
		database, definitions, entries, migrationTime,
	); handled || err != nil {
		return err
	}
	lastTransaction, err := database.LastTransaction()
	if err != nil {
		return err
	}
	hasNextGeneration := lastTransaction != ^uint64(0)
	nextGeneration := lastTransaction + 1
	totalRows := 0
	for _, entry := range entries {
		if err := prepareKitDBSchemaPlanEntry(
			database, entry, nextGeneration, hasNextGeneration, migrationTime,
		); err != nil {
			return err
		}
		totalRows += len(entry.rows)
		if totalRows > kitDBAtomicMigrationRows {
			return fmt.Errorf(
				"kitdb: atomic schema migration for structs (%s) needs to inspect or rewrite more than %d total rows; split the deployment or use an online migration",
				strings.Join(kitDBSchemaPlanNames(entries), ", "), kitDBAtomicMigrationRows,
			)
		}
	}

	mutations := newKitDBMigrationMutationSet()
	for _, entry := range entries {
		if err := stageKitDBSchemaPlanEntry(mutations, entry); err != nil {
			return err
		}
	}
	reader := kitDBMigrationOverlayReader{base: database, mutations: mutations}
	for _, entry := range entries {
		if len(entry.rows) == 0 {
			continue
		}
		table := &SchemaTable{
			engine: "kitdb", table: entry.current.Name, columns: entry.current.columns,
			definition: entry.current, definitions: definitions,
		}
		for _, row := range entry.rows {
			if err := validateKitDBMigrationReferences(table, reader, row.values); err != nil {
				return err
			}
		}
	}

	type catalogWrite struct {
		catalog  []byte
		auditKey []byte
		audit    []byte
	}
	writes := make([]catalogWrite, 0, len(entries))
	batch := kitDBMigrationBatchID(entries)
	for _, entry := range entries {
		catalog, auditKey, audit, err := encodeKitDBMigrationAt(
			entry.current, entry.steps, entry.from, batch, migrationTime,
		)
		if err != nil {
			return err
		}
		writes = append(writes, catalogWrite{catalog: catalog, auditKey: auditKey, audit: audit})
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		_ = tx.Rollback()
		if errors.Is(cause, kitdbengine.ErrTransactionTooLarge) {
			return fmt.Errorf(
				"kitdb: atomic schema migration for structs (%s) exceeds the transaction format limit; split the deployment or use an online migration",
				strings.Join(kitDBSchemaPlanNames(entries), ", "),
			)
		}
		return cause
	}
	for _, mutation := range mutations.ordered() {
		if mutation.deleted {
			if err := tx.Delete(mutation.key); err != nil {
				return rollback(err)
			}
			continue
		}
		if err := tx.Put(mutation.key, mutation.value); err != nil {
			return rollback(err)
		}
	}
	for _, write := range writes {
		if err := tx.DefineStruct(write.catalog); err != nil {
			return rollback(err)
		}
		if len(write.audit) != 0 {
			if err := tx.Put(write.auditKey, write.audit); err != nil {
				return rollback(err)
			}
		}
	}
	if _, err := tx.Commit(); err != nil {
		if errors.Is(err, kitdbengine.ErrTransactionTooLarge) {
			return fmt.Errorf(
				"kitdb: atomic schema migration for structs (%s) exceeds the transaction format limit; split the deployment or use an online migration",
				strings.Join(kitDBSchemaPlanNames(entries), ", "),
			)
		}
		return err
	}
	return nil
}

func prepareKitDBSchemaPlanEntry(
	database *kitdbengine.DB,
	entry *kitDBSchemaPlanEntry,
	nextGeneration uint64,
	hasNextGeneration bool,
	migrationTime time.Time,
) error {
	if entry == nil || entry.current == nil {
		return fmt.Errorf("kitdb: invalid schema migration entry")
	}
	if entry.stored == nil {
		return nil
	}
	rowGeneration, _, err := loadKitDBActiveRowLayout(database, entry.stored)
	if err != nil {
		return err
	}
	entry.rowGeneration = rowGeneration
	entry.rewrite = kitDBMigrationNeedsRewrite(entry.stored, entry.current)
	entry.validate = kitDBMigrationNeedsValidation(entry.stored, entry.current)
	if entry.rewrite || entry.validate {
		rows, err := prepareKitDBMigrationRows(
			database, entry.stored, entry.current, true, migrationTime,
		)
		if err != nil {
			return err
		}
		entry.rows = rows
	}
	if !entry.rewrite {
		return nil
	}

	metadata, _, err := loadKitDBIndexGenerationMetadata(database, entry.stored)
	if err != nil {
		return err
	}
	sourceIndexes := collectKitDBIndexes(entry.stored)
	targetIndexes := collectKitDBIndexes(entry.current)
	buildIndexes, retiredIndexes, transitionOK := kitDBSecondaryIndexTransition(entry.stored, entry.current)
	if !transitionOK {
		return fmt.Errorf("kitdb: struct %q has an invalid physical index transition", entry.current.Name)
	}
	entry.layoutChanged = len(buildIndexes) != 0 || len(retiredIndexes) != 0
	entry.targetGenerations = kitDBIndexGenerationMap(targetIndexes, metadata)
	if entry.layoutChanged {
		if !hasNextGeneration {
			return fmt.Errorf("kitdb: index generation space is exhausted")
		}
		for _, index := range buildIndexes {
			entry.targetGenerations[kitDBIndexSignature(index)] = nextGeneration
		}
	}
	entry.metadata = metadata
	entry.sourcePhysical = kitDBPhysicalIndexes(
		sourceIndexes, kitDBIndexGenerationMap(sourceIndexes, metadata),
	)
	entry.targetPhysical = kitDBPhysicalIndexes(targetIndexes, entry.targetGenerations)
	return nil
}

func stageKitDBSchemaPlanEntry(
	mutations *kitDBMigrationMutationSet,
	entry *kitDBSchemaPlanEntry,
) error {
	if !entry.rewrite {
		return nil
	}
	// Delete every source index before adding any target index. Besides making
	// the final overlay easy to validate, this correctly handles key reuse.
	for _, row := range entry.rows {
		if err := deleteKitDBIndexesForPhysical(
			mutations, entry.stored, row.previous, row.key, entry.sourcePhysical,
		); err != nil {
			return err
		}
	}
	uniqueClaims := make(map[string][]byte)
	for _, row := range entry.rows {
		encoded, err := encodeKitDBValidatedRow(entry.current, row.values, row.unknown)
		if err != nil {
			return err
		}
		physicalKey, err := kitDBPhysicalRowKey(entry.current, row.key, entry.rowGeneration)
		if err != nil {
			return err
		}
		if err := mutations.Put(physicalKey, encoded); err != nil {
			return err
		}
		indexes, err := kitDBIndexEntriesForPhysical(
			entry.current, row.values, row.key, entry.targetPhysical,
		)
		if err != nil {
			return err
		}
		for _, index := range indexes {
			if len(index.key) != 0 && index.key[0] == kitDBUniqueNamespace {
				claim := string(index.key)
				if owner, found := uniqueClaims[claim]; found && !bytes.Equal(owner, row.key) {
					return fmt.Errorf("kitdb: struct %q migration creates duplicate unique values", entry.current.Name)
				}
				uniqueClaims[claim] = row.key
			}
			if err := mutations.Put(index.key, index.value); err != nil {
				return err
			}
		}
	}
	if !entry.layoutChanged {
		return nil
	}
	if entry.metadata.Epoch == ^uint64(0) {
		return fmt.Errorf("kitdb: index layout epoch space is exhausted")
	}
	entry.metadata.Epoch++
	entry.metadata.Active = make(map[string]uint64)
	for _, index := range collectKitDBIndexes(entry.current) {
		signature := kitDBIndexSignature(index)
		if generation := entry.targetGenerations[signature]; generation != 0 {
			entry.metadata.Active[signature] = generation
		}
	}
	metadataKey, err := kitDBIndexGenerationMetadataKey(entry.current)
	if err != nil {
		return err
	}
	encodedMetadata, err := encodeKitDBIndexGenerationMetadata(entry.metadata)
	if err != nil {
		return err
	}
	return mutations.Put(metadataKey, encodedMetadata)
}

func kitDBSchemaPlanNames(entries []*kitDBSchemaPlanEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry != nil && entry.current != nil {
			names = append(names, entry.current.Name)
		}
	}
	sort.Strings(names)
	return names
}

func kitDBMigrationBatchID(entries []*kitDBSchemaPlanEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.current.ID+":"+entry.from+":"+entry.current.Hash)
	}
	sort.Strings(parts)
	return stableSchemaID("migration-batch", strings.Join(parts, "|"))
}

type kitDBMigrationRow struct {
	key      []byte
	previous map[string]value.Value
	values   map[string]value.Value
	unknown  []kitDBRawField
}

func prepareKitDBMigrationRows(
	database *kitdbengine.DB,
	stored, current *StructDef,
	retain bool,
	migrationTime time.Time,
) ([]kitDBMigrationRow, error) {
	generation, _, err := loadKitDBActiveRowLayout(database, stored)
	if err != nil {
		return nil, err
	}
	prefix, err := kitDBPhysicalRowPrefix(stored, generation)
	if err != nil {
		return nil, err
	}
	snapshot, err := database.Snapshot()
	if err != nil {
		return nil, err
	}
	defer snapshot.Close()
	cursor, err := snapshot.Cursor(kitdbengine.RangeOptions{Prefix: prefix})
	if err != nil {
		return nil, err
	}
	defer cursor.Close()

	rows := make([]kitDBMigrationRow, 0)
	count := 0
	for cursor.Next() {
		count++
		if retain && count > kitDBAtomicMigrationRows {
			return nil, fmt.Errorf(
				"kitdb: struct %q migration needs to rewrite more than %d rows; an online segmented migration is required",
				current.Name, kitDBAtomicMigrationRows,
			)
		}
		decoded, err := decodeKitDBRow(stored, cursor.Value())
		if err != nil {
			return nil, err
		}
		migrated, err := transformKitDBMigrationRow(stored, current, decoded.values, migrationTime)
		if err != nil {
			return nil, err
		}
		if err := validateKitDBRow(current, migrated); err != nil {
			return nil, fmt.Errorf("kitdb: struct %q migration validation: %w", current.Name, err)
		}
		rowKey, err := kitDBRowKeyForRow(current, migrated)
		if err != nil {
			return nil, err
		}
		logicalKey, err := kitDBLogicalRowKey(stored, cursor.Key(), generation)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(rowKey, logicalKey) {
			return nil, fmt.Errorf("kitdb: struct %q migration would change a primary key", current.Name)
		}
		if retain {
			rows = append(rows, kitDBMigrationRow{
				key: logicalKey, previous: decoded.values, values: migrated,
				unknown: cloneKitDBRawFields(decoded.unknown),
			})
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return rows, nil
}

func transformKitDBMigrationRow(
	stored, current *StructDef,
	previous map[string]value.Value,
	migrationTime time.Time,
) (map[string]value.Value, error) {
	oldByID := make(map[string]StructFieldDef, len(stored.Fields))
	for _, field := range stored.Fields {
		oldByID[field.ID] = field
	}
	migrated := make(map[string]value.Value, len(current.Fields))
	for _, field := range current.Fields {
		if old, found := oldByID[field.ID]; found {
			item, exists := previous[old.Name]
			if !exists {
				item = value.NewNil()
			}
			if old.Kind != field.Kind {
				converted, err := castKitDBMigrationValue(old.Kind, field.Kind, item)
				if err != nil {
					return nil, fmt.Errorf(
						"field %q cannot cast %s to %s: %w",
						field.Name, old.Kind, field.Kind, err,
					)
				}
				item = converted
			}
			migrated[field.Name] = item
			continue
		}
		spec := current.columns[field.Name]
		if item, found := kitDBMigrationDefault(spec, migrationTime); found {
			migrated[field.Name] = coerceWrite(spec.kind, item)
		} else {
			migrated[field.Name] = value.NewNil()
		}
	}
	return migrated, nil
}

func castKitDBMigrationValue(from, to string, item value.Value) (value.Value, error) {
	if item.IsNil() || from == to {
		return item, nil
	}
	logical := coerceRead(from, item)
	converted := logical
	switch to {
	case "text", "varchar", "char":
		if (from == "decimal" || from == "json" || from == "jsonb" ||
			from == "array" || from == "vector") && item.K == value.String {
			converted = item
			break
		}
		switch logical.K {
		case value.String, value.Number, value.Bool, value.Time, value.Duration:
			converted = value.New(logical.Text())
		default:
			return value.Value{}, fmt.Errorf("%s is not scalar text", printableKitDBValue(logical))
		}
	case "kitid", "uuid", "date", "time", "ip", "mac", "enum":
		if logical.K != value.String {
			return value.Value{}, fmt.Errorf("%s is not text", printableKitDBValue(logical))
		}
	case "datetime":
		if logical.K != value.String && logical.K != value.Time {
			return value.Value{}, fmt.Errorf("%s is not datetime text", printableKitDBValue(logical))
		}
	case "integer", "smallint", "int32", "serial", "year", "month", "day":
		numericInput := logical
		if from == "decimal" && item.K == value.String {
			numericInput = item
		}
		number, err := castKitDBMigrationNumber(numericInput)
		if err != nil || number != math.Trunc(number) {
			if err == nil {
				err = fmt.Errorf("%s is not an integer", printableKitDBValue(logical))
			}
			return value.Value{}, err
		}
		if numericInput.K == value.String && math.Abs(number) > kitDBMaximumExactInteger {
			return value.Value{}, fmt.Errorf("%q exceeds the exact integer range", numericInput.String())
		}
		minimum, maximum, _ := kitDBVMIntegerBounds(to)
		if number < minimum || number > maximum {
			return value.Value{}, fmt.Errorf("%s exceeds %s range", printableKitDBValue(logical), to)
		}
		converted = value.New(number)
	case "float":
		number, err := castKitDBMigrationNumber(logical)
		if err != nil {
			return value.Value{}, err
		}
		converted = value.New(number)
	case "decimal":
		switch logical.K {
		case value.Number:
			converted = value.New(numText(logical.N))
		case value.String:
			if !validKitDBDecimal(strings.TrimSpace(logical.String())) {
				return value.Value{}, fmt.Errorf("%q is not a decimal", logical.String())
			}
			converted = value.New(strings.TrimSpace(logical.String()))
		default:
			return value.Value{}, fmt.Errorf("%s is not numeric", printableKitDBValue(logical))
		}
	case "bool":
		switch logical.K {
		case value.Bool:
			converted = logical
		case value.Number:
			if logical.N != 0 && logical.N != 1 {
				return value.Value{}, fmt.Errorf("%s is not 0 or 1", printableKitDBValue(logical))
			}
			converted = value.New(logical.N != 0)
		case value.String:
			switch strings.ToLower(strings.TrimSpace(logical.String())) {
			case "true", "1":
				converted = value.New(true)
			case "false", "0":
				converted = value.New(false)
			default:
				return value.Value{}, fmt.Errorf("%q is not a boolean", logical.String())
			}
		default:
			return value.Value{}, fmt.Errorf("%s is not a boolean", printableKitDBValue(logical))
		}
	case "json", "jsonb", "array", "vector":
		if logical.K != value.String && logical.K != value.Map && logical.K != value.Array {
			return value.Value{}, fmt.Errorf("%s is not JSON", printableKitDBValue(logical))
		}
	case "blob":
		if logical.K != value.Bytes {
			return value.Value{}, fmt.Errorf("%s is not bytes", printableKitDBValue(logical))
		}
	default:
		return value.Value{}, fmt.Errorf("target type %q is unsupported", to)
	}
	converted = coerceWrite(to, converted)
	if err := validateKitDBFieldValue(StructFieldDef{Kind: to}, converted); err != nil {
		return value.Value{}, err
	}
	return converted, nil
}

func castKitDBMigrationNumber(item value.Value) (float64, error) {
	var number float64
	switch item.K {
	case value.Number:
		number = item.N
	case value.Bool:
		if item.N != 0 {
			number = 1
		}
	case value.String:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(item.String()), 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not numeric", item.String())
		}
		number = parsed
	default:
		return 0, fmt.Errorf("%s is not numeric", printableKitDBValue(item))
	}
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return 0, fmt.Errorf("%s is not finite", printableKitDBValue(item))
	}
	return number, nil
}

func kitDBMigrationDefault(spec *ColumnSpec, migrationTime time.Time) (value.Value, bool) {
	if spec == nil {
		return value.Value{}, false
	}
	switch {
	case spec.kind == "kitid":
		return autoValue(spec)
	case spec.kind == "uuid":
		return autoValue(spec)
	case spec.defaultNow:
		return value.New(migrationTime.Format(time.RFC3339)), true
	case spec.kind == "year":
		return value.New(migrationTime.Year()), true
	case spec.kind == "month":
		return value.New(int(migrationTime.Month())), true
	case spec.kind == "day":
		return value.New(migrationTime.Day()), true
	case spec.hasDefault:
		return spec.def, true
	default:
		return value.Value{}, false
	}
}

func kitDBMigrationNeedsRewrite(stored, current *StructDef) bool {
	if !equalKitDBUniqueConstraints(stored.UniqueConstraints, current.UniqueConstraints) {
		return true
	}
	oldByID := make(map[string]StructFieldDef, len(stored.Fields))
	newByID := make(map[string]StructFieldDef, len(current.Fields))
	for _, field := range stored.Fields {
		oldByID[field.ID] = field
	}
	for _, field := range current.Fields {
		newByID[field.ID] = field
		previous, found := oldByID[field.ID]
		if !found {
			spec := current.columns[field.Name]
			if kitDBMigrationCanFill(spec) || len(field.Indexes) != 0 {
				return true
			}
			continue
		}
		if previous.Kind != field.Kind || previous.Unique != field.Unique || !reflect.DeepEqual(previous.Indexes, field.Indexes) ||
			previous.Primary != field.Primary || previous.PrimaryOrder != field.PrimaryOrder ||
			(previous.Name != field.Name && kitDBRenameTouchesIndex(stored, current, previous.Name, field.Name)) {
			return true
		}
	}
	for _, field := range stored.Fields {
		if _, found := newByID[field.ID]; !found {
			return true
		}
	}
	return false
}

func kitDBMigrationNeedsValidation(stored, current *StructDef) bool {
	if kitDBForeignConstraintsNeedValidation(stored.ForeignConstraints, current.ForeignConstraints) {
		return true
	}
	if structCheckConstraintsNeedValidation(stored, current) {
		return true
	}
	oldByID := make(map[string]StructFieldDef, len(stored.Fields))
	for _, field := range stored.Fields {
		oldByID[field.ID] = field
	}
	for _, field := range current.Fields {
		previous, found := oldByID[field.ID]
		if !found {
			continue
		}
		if !previous.NotNull && field.NotNull {
			return true
		}
		if kitDBEnumTightened(previous.Enum, field.Enum) {
			return true
		}
	}
	return false
}

func kitDBForeignConstraintsNeedValidation(
	stored, current []StructForeignConstraint,
) bool {
	previous := make(map[string]StructForeignConstraint, len(stored))
	for _, constraint := range stored {
		previous[constraint.ID] = constraint
	}
	for _, constraint := range current {
		if old, found := previous[constraint.ID]; !found || !reflect.DeepEqual(old, constraint) {
			return true
		}
	}
	return false
}

func kitDBEnumTightened(previous, current []string) bool {
	if len(current) == 0 {
		return false
	}
	if len(previous) == 0 {
		return true
	}
	allowed := make(map[string]struct{}, len(current))
	for _, candidate := range current {
		allowed[candidate] = struct{}{}
	}
	for _, candidate := range previous {
		if _, found := allowed[candidate]; !found {
			return true
		}
	}
	return false
}

func kitDBRenameTouchesIndex(stored, current *StructDef, previousName, currentName string) bool {
	for _, definition := range []*StructDef{stored, current} {
		if definition == nil {
			continue
		}
		for _, field := range definition.Fields {
			if (field.Name == previousName || field.Name == currentName) && len(field.Indexes) != 0 {
				return true
			}
			for _, member := range field.Indexes {
				for _, condition := range member.Filter {
					if condition.Field == previousName || condition.Field == currentName {
						return true
					}
				}
			}
		}
	}
	return false
}

func validateKitDBMigrationReferences(table *SchemaTable, reader kitDBReader, row map[string]value.Value) error {
	for _, field := range table.definition.Fields {
		if field.Reference == nil {
			continue
		}
		item, found := row[field.Name]
		if !found || item.IsNil() {
			continue
		}
		if err := table.validateKitDBReference(reader, field, item); err != nil {
			return err
		}
	}
	for _, constraint := range table.definition.ForeignConstraints {
		if err := table.validateKitDBCompositeReference(reader, constraint, row); err != nil {
			return err
		}
	}
	return nil
}

func commitKitDBCatalog(
	database *kitdbengine.DB,
	definition *StructDef,
	steps []planStep,
	from string,
) error {
	catalog, auditKey, audit, err := encodeKitDBMigration(definition, steps, from)
	if err != nil {
		return err
	}
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	if err := tx.DefineStruct(catalog); err != nil {
		_ = tx.Rollback()
		return err
	}
	if len(audit) != 0 {
		if err := tx.Put(auditKey, audit); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	_, err = tx.Commit()
	return err
}

func encodeKitDBMigration(
	definition *StructDef,
	steps []planStep,
	from string,
) (catalog, auditKey, audit []byte, err error) {
	return encodeKitDBMigrationAt(definition, steps, from, "", time.Now().UTC())
}

func encodeKitDBMigrationAt(
	definition *StructDef,
	steps []planStep,
	from string,
	batch string,
	appliedAt time.Time,
) (catalog, auditKey, audit []byte, err error) {
	catalog, err = json.Marshal(definition)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("kitdb: encode struct catalog %q: %w", definition.Name, err)
	}
	if _, contractErr := kitdbsql.DecodeSchema(catalog); contractErr != nil {
		return nil, nil, nil, fmt.Errorf("kitdb: schema contract %q: %w", definition.Name, contractErr)
	}
	if from == "" || len(steps) == 0 {
		return catalog, nil, nil, nil
	}
	auditSteps := make([]kitDBMigrationAuditStep, 0, len(steps))
	for _, step := range steps {
		auditSteps = append(auditSteps, kitDBMigrationAuditStep{
			Action: step.Action, Field: step.Column, From: step.From, To: step.To,
		})
	}
	version := 1
	if batch != "" {
		version = 2
	}
	record := kitDBMigrationAudit{
		Version: version, Batch: batch, StructID: definition.ID, Struct: definition.Name,
		From: from, To: definition.Hash, Steps: auditSteps,
		AppliedAt: appliedAt.UTC().Format(time.RFC3339Nano),
	}
	audit, err = json.Marshal(record)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("kitdb: encode migration audit: %w", err)
	}
	auditID := stableSchemaID("migration", definition.ID+":"+definition.Hash)
	auditKey, err = kitDBFixedKey(kitDBMigrationNamespace, definition.ID, auditID)
	if err != nil {
		return nil, nil, nil, err
	}
	return catalog, auditKey, audit, nil
}

func sortedKitDBMigrationSteps(steps []planStep) []planStep {
	ordered := append([]planStep(nil), steps...)
	sort.SliceStable(ordered, func(left, right int) bool {
		if ordered[left].Column != ordered[right].Column {
			return ordered[left].Column < ordered[right].Column
		}
		return ordered[left].Action < ordered[right].Action
	})
	return ordered
}
