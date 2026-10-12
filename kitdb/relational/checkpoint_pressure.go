package relational

import (
	"context"
	"time"

	kitdbnode "github.com/kitwork/engine/kitdb/node"
)

func (engine *Engine) evaluateCheckpoint(ctx context.Context, beforeWrite bool) error {
	if engine.checkpointManager == nil {
		return nil
	}
	engine.mu.RLock()
	defer engine.mu.RUnlock()
	if err := engine.readyLocked(); err != nil {
		return err
	}
	return engine.evaluateCheckpointLocked(ctx, beforeWrite)
}

// The caller holds engine.mu for the kernel lifetime, but not writeMu.
func (engine *Engine) evaluateCheckpointLocked(ctx context.Context, beforeWrite bool) error {
	if engine.checkpointManager == nil {
		return nil
	}
	stats, err := engine.database.Stats()
	if err != nil {
		return err
	}
	priority := kitdbnode.MaintenanceNormal
	if beforeWrite {
		if stats.WALBytes < 8<<20 && stats.OverlayMutations < 4096 &&
			stats.SnapshotOverlayEstimateBytes < max(1, stats.SnapshotMemoryLimitBytes/2) {
			return nil
		}
		priority = kitdbnode.MaintenanceUrgent
	} else if stats.WALBytes < 4<<20 && stats.OverlayMutations < 2048 &&
		(stats.OverlayMutations == 0 || stats.SnapshotOverlayEstimateBytes < max(1, stats.SnapshotMemoryLimitBytes/4)) &&
		(stats.OverlaySince.IsZero() || time.Since(stats.OverlaySince) < 30*time.Second) {
		return nil
	}
	ticket, err := engine.checkpointManager.EvaluateCheckpoint(ctx, engine.database.Path(), engine.kernelOptions,
		kitdbnode.CheckpointPressurePolicy{
			WALBytes: 4 << 20, Mutations: 2048,
			OverlayBytes: max(1, stats.SnapshotMemoryLimitBytes/4), MaxAge: 30 * time.Second,
		}, priority)
	if err != nil {
		return err
	}
	if beforeWrite && ticket != nil {
		_, err = ticket.Wait(ctx)
	}
	return err
}
