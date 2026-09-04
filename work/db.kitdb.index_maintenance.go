package work

import (
	"context"
	"fmt"
	"reflect"

	kitdbengine "github.com/kitwork/engine/kitdb"
	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

var kitDBSharedSecondaryIndexDriver kitDBSecondaryIndexNodeDriver

// kitDBSecondaryIndexNodeDriver contains no resumable process state. Every
// dispatch discovers one KIBS record from KitDB, commits one bounded chunk,
// then asks the node governor to requeue only when durable work remains.
type kitDBSecondaryIndexNodeDriver struct{}

func (kitDBSecondaryIndexNodeDriver) SecondaryIndexDriverID() string {
	return "kitwork/relational-secondary-index/v1"
}

func (kitDBSecondaryIndexNodeDriver) AdvanceSecondaryIndex(
	ctx context.Context,
	database *kitdbengine.DB,
) (kitdbnode.SecondaryIndexProgress, error) {
	if database == nil {
		return kitdbnode.SecondaryIndexProgress{}, fmt.Errorf("kitdb: secondary-index database is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return kitdbnode.SecondaryIndexProgress{}, err
	}
	if _, err := checkpointKitDBBackgroundMaintenance(ctx, database); err != nil {
		return kitdbnode.SecondaryIndexProgress{}, err
	}
	catalog, err := database.Catalog()
	if err != nil {
		return kitdbnode.SecondaryIndexProgress{}, err
	}
	for _, catalogEntry := range catalog.Structs {
		stored, err := decodeKitDBCatalog(catalogEntry.Definition, catalogEntry.Name)
		if err != nil {
			return kitdbnode.SecondaryIndexProgress{}, err
		}
		advanced, err := advanceKitDBSecondaryIndexStateChunk(ctx, database, stored)
		if err != nil {
			return kitdbnode.SecondaryIndexProgress{}, err
		}
		if !advanced {
			continue
		}
		transaction, err := database.LastTransaction()
		if err != nil {
			return kitdbnode.SecondaryIndexProgress{}, err
		}
		pending, err := hasKitDBPendingSecondaryIndex(database)
		if err != nil {
			return kitdbnode.SecondaryIndexProgress{}, err
		}
		return kitdbnode.SecondaryIndexProgress{
			Transaction: transaction, Pending: pending, Advanced: true,
		}, nil
	}
	return kitdbnode.SecondaryIndexProgress{}, nil
}

func advanceKitDBSecondaryIndexStateChunk(
	ctx context.Context,
	database *kitdbengine.DB,
	stored *StructDef,
) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	codec, found, err := loadKitDBIndexBuildState(database, stored, kitDBIndexBuildCodec)
	if err != nil {
		return false, err
	}
	if found {
		if codec.SourceHash != stored.Hash || codec.TargetHash != stored.Hash {
			return false, fmt.Errorf(
				"kitdb: struct %q codec maintenance no longer matches its catalog",
				stored.Name,
			)
		}
		switch codec.Phase {
		case kitDBIndexBuildPhaseRows:
			indexes, matches := kitDBIndexesMatchingSignatures(
				collectKitDBIndexes(stored),
				codec.Indexes,
			)
			if !matches {
				return false, fmt.Errorf(
					"kitdb: struct %q codec maintenance no longer matches its indexes",
					stored.Name,
				)
			}
			_, err = advanceKitDBIndexRows(
				database, stored, stored, kitDBIndexBuildCodec, indexes, nil, nil,
			)
		case kitDBIndexBuildPhaseCleanup:
			markerKey, markerErr := kitDBSecondaryIndexCodecMarkerKey(stored)
			if markerErr != nil {
				return false, markerErr
			}
			marker, markerFound, markerErr := database.Get(markerKey)
			if markerErr != nil {
				return false, markerErr
			}
			if !markerFound || !kitDBKnownSecondaryIndexCodecMarker(marker) {
				return false, fmt.Errorf(
					"kitdb: struct %q codec cleanup has no published marker",
					stored.Name,
				)
			}
			_, err = advanceKitDBLegacyIndexCleanup(database, stored, codec)
		default:
			return false, fmt.Errorf(
				"kitdb: struct %q has invalid codec maintenance phase %d",
				stored.Name, codec.Phase,
			)
		}
		return err == nil, err
	}

	state, found, err := loadKitDBIndexBuildState(database, stored, kitDBIndexBuildSchema)
	if err != nil || !found {
		return false, err
	}
	target, err := decodeKitDBCatalog(state.TargetDefinition, stored.Name)
	if err != nil {
		return false, err
	}
	switch state.Phase {
	case kitDBIndexBuildPhaseRows:
		source := stored
		if len(state.SourceDefinition) != 0 {
			source, err = decodeKitDBCatalog(state.SourceDefinition, stored.Name)
			if err != nil {
				return false, err
			}
		}
		if stored.Hash != state.SourceHash || source.Hash != state.SourceHash ||
			target.Hash != state.TargetHash || source.ID != target.ID {
			return false, fmt.Errorf(
				"kitdb: struct %q index maintenance no longer matches its source catalog",
				stored.Name,
			)
		}
		steps := planKitDBMigration(source, target, true)
		indexes, retired, ok := kitDBOnlyChangesSecondaryIndexes(steps, source, target)
		if !ok || !reflect.DeepEqual(kitDBIndexSignatures(indexes), state.Indexes) {
			return false, fmt.Errorf(
				"kitdb: struct %q index maintenance no longer matches its accepted transition",
				stored.Name,
			)
		}
		_, err = advanceKitDBIndexRows(
			database, source, target, kitDBIndexBuildSchema, indexes, retired, steps,
		)
	case kitDBIndexBuildPhaseCleanup:
		if stored.Hash != state.TargetHash || target.Hash != state.TargetHash || stored.ID != target.ID {
			return false, fmt.Errorf(
				"kitdb: struct %q index cleanup no longer matches its target catalog",
				stored.Name,
			)
		}
		_, err = advanceKitDBRetiredIndexCleanup(database, stored, state)
	default:
		return false, fmt.Errorf(
			"kitdb: struct %q has invalid index maintenance phase %d",
			stored.Name, state.Phase,
		)
	}
	return err == nil, err
}

func kitDBDefinitionHasPendingSecondaryIndex(
	database *kitdbengine.DB,
	definition *StructDef,
) (bool, error) {
	if _, found, err := loadKitDBIndexBuildState(
		database, definition, kitDBIndexBuildCodec,
	); err != nil || found {
		return found, err
	}
	_, found, err := loadKitDBIndexBuildState(database, definition, kitDBIndexBuildSchema)
	return found, err
}

func hasKitDBPendingSecondaryIndex(database *kitdbengine.DB) (bool, error) {
	catalog, err := database.Catalog()
	if err != nil {
		return false, err
	}
	for _, catalogEntry := range catalog.Structs {
		definition, err := decodeKitDBCatalog(catalogEntry.Definition, catalogEntry.Name)
		if err != nil {
			return false, err
		}
		if pending, err := kitDBDefinitionHasPendingSecondaryIndex(database, definition); err != nil {
			return false, err
		} else if pending {
			return true, nil
		}
	}
	return false, nil
}

func wakeKitDBSecondaryIndexIfPending(managed *managedKitDB) (bool, error) {
	if managed == nil || managed.database == nil {
		return false, fmt.Errorf("kitdb: managed database is unavailable")
	}
	pending, err := hasKitDBPendingSecondaryIndex(managed.database)
	if err != nil || !pending {
		return pending, err
	}
	// Admission is best-effort after KIBS has committed. Reporting a scheduling
	// rejection as DDL failure would be misleading because the index transition
	// is already durable and restart/catalog hydration will wake it again.
	_, _ = scheduleKitDBSecondaryIndex(managed)
	return true, nil
}
