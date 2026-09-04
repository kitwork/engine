package node

import (
	"context"
	"errors"
	"fmt"

	"github.com/kitwork/engine/kitdb"
)

var (
	ErrBackupHistoryRequired = errors.New(
		"kitdb node: verified backup requires retained history",
	)
	ErrBackupSourceMismatch = errors.New(
		"kitdb node: backup anchor belongs to a different database",
	)
	ErrBackupVerification = errors.New(
		"kitdb node: published backup anchor failed verification",
	)
)

// BackupRequest describes one retryable verified backup. PinName is the
// durable source-history boundary needed to replay changes after the anchor.
type BackupRequest struct {
	Source      string
	Destination string
	Options     kitdb.OpenOptions
	PinName     string
	Priority    MaintenancePriority
}

// VerifiedBackupResult connects one standalone anchor to its durable source
// history pin. Resumed reports that Destination existed and was safely adopted
// after complete verification rather than being overwritten.
type VerifiedBackupResult struct {
	Anchor                kitdb.BackupAnchor
	Pin                   kitdb.HistoryPin
	CheckpointTransaction uint64
	Resumed               bool
}

type backupMaintenance struct {
	destination string
	pinName     string
}

// ScheduleBackup schedules or joins one verified backup operation. The source
// must retain history so the resulting anchor can be connected to subsequent
// transactions by a durable pin.
func (manager *Manager) ScheduleBackup(
	ctx context.Context,
	request BackupRequest,
) (*MaintenanceTicket, error) {
	if manager == nil {
		return nil, ErrClosed
	}
	if ctx == nil {
		return nil, fmt.Errorf("kitdb node: backup context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !request.Options.RetainHistory {
		return nil, ErrBackupHistoryRequired
	}
	if err := kitdb.ValidateHistoryPinName(request.PinName); err != nil {
		return nil, err
	}
	source, sourceKey, err := canonicalDatabasePath(request.Source)
	if err != nil {
		return nil, err
	}
	destination, destinationKey, err := canonicalDatabasePath(request.Destination)
	if err != nil {
		return nil, fmt.Errorf("kitdb node: resolve backup destination: %w", err)
	}
	if sourceKey == destinationKey {
		return nil, fmt.Errorf("kitdb node: backup destination matches the source database")
	}
	operation := destinationKey + "\x00" + request.PinName
	return manager.scheduleMaintenance(
		ctx,
		source,
		request.Options,
		MaintenanceBackup,
		operation,
		request.Priority,
		maintenancePayload{backup: &backupMaintenance{
			destination: destination,
			pinName:     request.PinName,
		}},
		[]maintenanceResourceRequest{{path: destination, reserveOnly: true}},
	)
}

func runVerifiedBackup(
	ctx context.Context,
	database *kitdb.DB,
	operation *backupMaintenance,
) (*VerifiedBackupResult, error) {
	if operation == nil {
		return nil, fmt.Errorf("kitdb node: backup maintenance payload is unavailable")
	}
	created, createErr := database.CreateBackupAnchor(ctx, operation.destination)
	anchor := created
	resumed := false
	if createErr != nil {
		if !errors.Is(createErr, kitdb.ErrBackupExists) {
			return nil, createErr
		}
		verified, verifyErr := kitdb.VerifyBackupAnchor(ctx, operation.destination)
		if verifyErr != nil {
			return nil, errors.Join(
				createErr,
				fmt.Errorf("kitdb node: existing backup cannot be resumed: %w", verifyErr),
			)
		}
		anchor = verified
		resumed = true
	} else {
		verified, verifyErr := kitdb.VerifyBackupAnchor(ctx, operation.destination)
		if verifyErr != nil {
			return nil, errors.Join(ErrBackupVerification, verifyErr)
		}
		if verified != created {
			return nil, errors.Join(
				ErrBackupVerification,
				fmt.Errorf("kitdb node: published backup metadata changed after creation"),
			)
		}
		anchor = verified
	}

	result := &VerifiedBackupResult{Anchor: anchor, Resumed: resumed}
	if anchor.DatabaseID != database.ID() {
		return result, errors.Join(
			ErrBackupSourceMismatch,
			fmt.Errorf("kitdb node: backup database %q, source database %q", anchor.DatabaseID, database.ID()),
		)
	}
	checkpointTransaction, err := database.Checkpoint()
	result.CheckpointTransaction = checkpointTransaction
	if err != nil {
		return result, err
	}
	if checkpointTransaction < anchor.Transaction {
		return result, errors.Join(
			ErrBackupVerification,
			fmt.Errorf(
				"kitdb node: source checkpoint %d precedes backup transaction %d",
				checkpointTransaction,
				anchor.Transaction,
			),
		)
	}
	pin, err := database.SetHistoryPin(ctx, operation.pinName, anchor.Cursor())
	result.Pin = pin
	if err != nil {
		return result, err
	}
	return result, nil
}
