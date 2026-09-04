package node

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/kitwork/engine/kitdb"
)

var ErrHistoryRetentionPolicyRequired = errors.New(
	"kitdb node: history retention requires a bounded policy",
)

// HistoryRetentionRequest asks the fleet governor to evaluate the process-local
// policy carried by Options. The durable database still owns pins, history
// metadata, and crash-safe whole-segment publication.
type HistoryRetentionRequest struct {
	Path     string
	Options  kitdb.OpenOptions
	Priority MaintenancePriority
}

type historyRetentionMaintenance struct {
	policy kitdb.HistoryRetentionPolicy
}

// ScheduleHistoryRetention schedules or joins one exact policy evaluation.
func (manager *Manager) ScheduleHistoryRetention(
	ctx context.Context,
	request HistoryRetentionRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: history retention context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	policy := request.Options.HistoryRetention
	if !request.Options.RetainHistory || !policy.Enabled() {
		return nil, ErrHistoryRetentionPolicyRequired
	}
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	operation := strconv.FormatInt(int64(policy.MaxAge), 10) + ":" +
		strconv.FormatInt(policy.MaxBytes, 10)
	return manager.scheduleMaintenance(
		ctx,
		request.Path,
		request.Options,
		MaintenanceHistoryRetention,
		operation,
		request.Priority,
		maintenancePayload{historyRetention: &historyRetentionMaintenance{policy: policy}},
		nil,
	)
}

func runHistoryRetention(
	ctx context.Context,
	database *kitdb.DB,
	operation *historyRetentionMaintenance,
) (kitdb.HistoryRetentionResult, error) {
	if operation == nil {
		return kitdb.HistoryRetentionResult{}, fmt.Errorf(
			"kitdb node: history retention maintenance payload is unavailable",
		)
	}
	return database.EnforceHistoryRetention(ctx, operation.policy, time.Now())
}
