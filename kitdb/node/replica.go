package node

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/kitwork/engine/kitdb"
)

var (
	ErrReplicaCatchUpHistoryRequired = errors.New(
		"kitdb node: replica catch-up requires retained source history",
	)
	ErrReplicaCatchUpSameDatabase = errors.New(
		"kitdb node: replica source and target are the same database",
	)
)

// ReplicaCatchUpRequest describes one bounded source-to-target history replay.
// SourceOptions must retain history. TargetOptions are forced into replica mode
// while preserving caller-selected cache and downstream-history settings.
type ReplicaCatchUpRequest struct {
	Source        string
	Target        string
	SourceOptions kitdb.OpenOptions
	TargetOptions kitdb.OpenOptions
	PinName       string
	Priority      MaintenancePriority
}

type replicaCatchUpMaintenance struct {
	targetKey string
	pinName   string
}

// ScheduleReplicaCatchUp schedules or joins one exact source, target, and pin
// operation. A verified backup produced by ScheduleBackup is the bootstrap;
// catch-up deliberately reuses that anchor rather than defining another copy
// or durability mechanism.
func (manager *Manager) ScheduleReplicaCatchUp(
	ctx context.Context,
	request ReplicaCatchUpRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: replica catch-up context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !request.SourceOptions.RetainHistory {
		return nil, ErrReplicaCatchUpHistoryRequired
	}
	if err := kitdb.ValidateHistoryPinName(request.PinName); err != nil {
		return nil, err
	}
	source, sourceKey, err := canonicalDatabasePath(request.Source)
	if err != nil {
		return nil, err
	}
	target, targetKey, err := canonicalDatabasePath(request.Target)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: resolve replica target: %w", err)
	}
	if sourceKey == targetKey {
		return nil, ErrReplicaCatchUpSameDatabase
	}
	if err := requireReplicaDatabasePath(source, "source"); err != nil {
		return nil, err
	}
	if err := requireReplicaDatabasePath(target, "target"); err != nil {
		return nil, err
	}
	request.TargetOptions.Replica = true
	operation := targetKey + "\x00" + request.PinName
	return manager.scheduleMaintenance(
		ctx,
		source,
		request.SourceOptions,
		MaintenanceReplicaCatchUp,
		operation,
		request.Priority,
		maintenancePayload{replicaCatchUp: &replicaCatchUpMaintenance{
			targetKey: targetKey,
			pinName:   request.PinName,
		}},
		[]maintenanceResourceRequest{{path: target, options: request.TargetOptions}},
	)
}

func requireReplicaDatabasePath(path, role string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("kitdb node: inspect replica %s %q: %w", role, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("kitdb node: replica %s %q is not a regular file", role, path)
	}
	return nil
}

func runReplicaCatchUp(
	ctx context.Context,
	source *kitdb.DB,
	target *kitdb.DB,
	operation *replicaCatchUpMaintenance,
) (kitdb.ReplicaCatchUp, error) {
	if operation == nil {
		return kitdb.ReplicaCatchUp{}, fmt.Errorf(
			"kitdb node: replica catch-up maintenance payload is unavailable",
		)
	}
	if target == nil {
		return kitdb.ReplicaCatchUp{Name: operation.pinName}, fmt.Errorf(
			"kitdb node: replica target maintenance handle is unavailable",
		)
	}
	return source.CatchUpReplica(ctx, operation.pinName, target)
}
