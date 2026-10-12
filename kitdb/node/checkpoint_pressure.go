package node

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kitwork/engine/kitdb"
)

var ErrCheckpointBackoff = errors.New("kitdb node: checkpoint pressure is in failure backoff")

// Positive thresholds are ORed; zero disables that threshold. This policy is
// process-local scheduling, not a durability setting or a total RAM cap.
type CheckpointPressurePolicy struct {
	WALBytes, OverlayBytes                          int64
	Mutations                                       int
	MaxAge, MinInterval, InitialBackoff, MaxBackoff time.Duration
}

type checkpointPressureState struct {
	ticket        *MaintenanceTicket
	submitting    bool
	next          time.Time
	failures      uint32
	readerBlocked bool
	readerRetryAt time.Time
}

func (p CheckpointPressurePolicy) normalize() (CheckpointPressurePolicy, error) {
	if p.WALBytes < 0 || p.OverlayBytes < 0 || p.Mutations < 0 || p.MaxAge < 0 || p.MinInterval < 0 || p.InitialBackoff < 0 || p.MaxBackoff < 0 {
		return p, fmt.Errorf("kitdb node: negative checkpoint pressure policy")
	}
	if p.WALBytes == 0 && p.OverlayBytes == 0 && p.Mutations == 0 && p.MaxAge == 0 {
		return p, fmt.Errorf("kitdb node: empty checkpoint pressure policy")
	}
	if p.MinInterval == 0 {
		p.MinInterval = time.Second
	}
	if p.InitialBackoff == 0 {
		p.InitialBackoff = time.Second
	}
	if p.MaxBackoff == 0 {
		p.MaxBackoff = time.Minute
	}
	if p.MaxBackoff < p.InitialBackoff || p.MaxBackoff > 24*time.Hour || p.MinInterval > 24*time.Hour || p.MaxAge > 365*24*time.Hour {
		return p, fmt.Errorf("kitdb node: invalid checkpoint pressure timing")
	}
	return p, nil
}

func (p CheckpointPressurePolicy) due(s kitdb.Stats, now time.Time) bool {
	if s.OverlayMutations == 0 {
		return p.WALBytes > 0 && s.LastTransaction > s.CheckpointTransaction && s.WALBytes >= p.WALBytes
	}
	return (p.WALBytes > 0 && s.WALBytes >= p.WALBytes) || (p.OverlayBytes > 0 && s.SnapshotOverlayEstimateBytes >= p.OverlayBytes) || (p.Mutations > 0 && s.OverlayMutations >= p.Mutations) || (p.MaxAge > 0 && !s.OverlaySince.IsZero() && now.Sub(s.OverlaySince) >= p.MaxAge)
}

// EvaluateCheckpoint uses a normal lease and the existing typed maintenance
// queue. It never checkpoints inline or starts a timer/goroutine. Hosts call
// it on traffic or their existing cadence; idle files are not polled.
func (manager *Manager) EvaluateCheckpoint(ctx context.Context, path string, options kitdb.OpenOptions, policy CheckpointPressurePolicy, priority MaintenancePriority) (*MaintenanceTicket, error) {
	policy, err := policy.normalize()
	if err != nil {
		return nil, err
	}
	if !validMaintenancePriority(priority) {
		return nil, fmt.Errorf("kitdb node: invalid checkpoint priority")
	}
	lease, err := manager.Acquire(ctx, path, options)
	if err != nil {
		return nil, err
	}
	defer lease.Release()
	stats, err := lease.DB().Stats()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	manager.mu.Lock()
	state := &lease.entry.checkpointPressure
	if state.ticket != nil {
		select {
		case <-state.ticket.Done():
			result, failure := state.ticket.Wait(context.Background())
			state.ticket = nil
			finished := result.FinishedAt
			if finished.IsZero() {
				finished = now
			}
			if failure != nil {
				if state.failures < 32 {
					state.failures++
				}
				state.next = finished.Add(checkpointPressureBackoff(policy, state.failures))
				state.readerBlocked = errors.Is(failure, kitdb.ErrSnapshotsActive)
				state.readerRetryAt = finished.Add(policy.InitialBackoff)
			} else {
				state.failures = 0
				state.readerBlocked = false
				state.next = finished.Add(policy.MinInterval)
			}
		default:
			ticket := state.ticket
			manager.mu.Unlock()
			return ticket, nil
		}
	}
	if state.submitting {
		manager.mu.Unlock()
		if priority == MaintenanceUrgent {
			return nil, ErrCheckpointBackoff
		}
		return nil, nil
	}
	if !policy.due(stats, now) {
		manager.mu.Unlock()
		return nil, nil
	}
	// A reader-blocked full checkpoint can recover after quiescence without
	// waiting out an accumulated exponential delay. The initial interval still
	// bounds retries if a new reader races this point-in-time observation.
	if state.readerRetryReady(stats, now) {
		state.next = now
	}
	if now.Before(state.next) && (state.failures != 0 || priority != MaintenanceUrgent) {
		failed := state.failures != 0
		manager.mu.Unlock()
		if failed && priority == MaintenanceUrgent {
			return nil, ErrCheckpointBackoff
		}
		return nil, nil
	}
	state.submitting = true
	manager.mu.Unlock()
	ticket, err := manager.ScheduleCheckpoint(ctx, path, options, priority)
	manager.mu.Lock()
	state.submitting = false
	if err == nil {
		state.ticket = ticket
	} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
		state.readerBlocked = false
		if state.failures < 32 {
			state.failures++
		}
		state.next = now.Add(checkpointPressureBackoff(policy, state.failures))
	}
	manager.mu.Unlock()
	return ticket, err
}

func (state *checkpointPressureState) readerRetryReady(stats kitdb.Stats, now time.Time) bool {
	return state.readerBlocked && stats.ActiveSnapshots == 0 && stats.SnapshotReservations == 0 && !now.Before(state.readerRetryAt)
}

func checkpointPressureBackoff(p CheckpointPressurePolicy, failures uint32) time.Duration {
	delay := p.InitialBackoff
	for i := uint32(1); i < failures && delay < p.MaxBackoff; i++ {
		if delay > p.MaxBackoff/2 {
			return p.MaxBackoff
		}
		delay *= 2
	}
	if delay > p.MaxBackoff {
		return p.MaxBackoff
	}
	return delay
}
