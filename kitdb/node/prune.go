package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/kitwork/engine/kitdb"
)

var ErrHistoryPruneHistoryRequired = errors.New(
	"kitdb node: history prune requires retained history",
)

// HistoryPruneRequest describes one explicit whole-segment retention advance.
// Through is part of the operation identity; requests for different boundaries
// remain independent and are never widened by coalescing.
type HistoryPruneRequest struct {
	Path     string
	Through  uint64
	Options  kitdb.OpenOptions
	Priority MaintenancePriority
}

type historyPruneMaintenance struct {
	through uint64
}

// ScheduleHistoryPrune schedules or joins one exact retained-history prune.
// The source must opt into retained history. Kernel pins remain authoritative
// and prevent the published base from passing a protected cursor.
func (manager *Manager) ScheduleHistoryPrune(
	ctx context.Context,
	request HistoryPruneRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: history prune context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !request.Options.RetainHistory {
		return nil, ErrHistoryPruneHistoryRequired
	}
	return manager.scheduleMaintenance(
		ctx,
		request.Path,
		request.Options,
		MaintenanceHistoryPrune,
		strconv.FormatUint(request.Through, 10),
		request.Priority,
		maintenancePayload{historyPrune: &historyPruneMaintenance{through: request.Through}},
		nil,
	)
}

func runHistoryPrune(
	ctx context.Context,
	database *kitdb.DB,
	operation *historyPruneMaintenance,
) (kitdb.HistoryPruneResult, error) {
	if operation == nil {
		return kitdb.HistoryPruneResult{}, fmt.Errorf(
			"kitdb node: history prune maintenance payload is unavailable",
		)
	}
	return database.PruneHistory(ctx, operation.through)
}
