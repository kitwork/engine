package node

import (
	"context"
	"errors"
	"time"
)

type productionPolicyExecution struct {
	config      ProductionPolicyConfig
	publisher   ProductionAnchorPublisher
	forceBackup bool
	restoreDue  bool
}

func (manager *Manager) startProductionLocked() {
	if manager.productionStarted || manager.productionClosed {
		return
	}
	manager.productionStarted = true
	workers := manager.limits.production.maxConcurrent
	manager.productionWorkers.Add(workers + 1)
	go manager.productionDispatcher()
	for range workers {
		go manager.productionWorker()
	}
	go func() {
		manager.productionWorkers.Wait()
		close(manager.productionDone)
	}()
}

func (manager *Manager) signalProductionLocked() {
	select {
	case manager.productionNotify <- struct{}{}:
	default:
	}
}

func (manager *Manager) productionDispatcher() {
	defer manager.productionWorkers.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		work, wait, available := manager.nextProductionWork()
		if !available {
			return
		}
		if work != nil {
			select {
			case manager.productionWork <- *work:
				continue
			case <-manager.ctx.Done():
				manager.abandonProductionWork(*work, ErrClosed)
				return
			}
		}
		if wait < 0 {
			select {
			case <-manager.productionNotify:
			case <-manager.ctx.Done():
				return
			}
			continue
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		timer.Reset(wait)
		select {
		case <-timer.C:
		case <-manager.productionNotify:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		case <-manager.ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}
	}
}

func (manager *Manager) nextProductionWork() (
	*productionPolicyWork,
	time.Duration,
	bool,
) {
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed || manager.ctx.Err() != nil {
		return nil, 0, false
	}
	now := time.Now()
	var selected *productionPolicy
	var selectedName string
	var selectedAt time.Time
	for name, policy := range manager.productionPolicies {
		if policy.queued || policy.running {
			continue
		}
		manual := policy.run != nil && policy.run.manual
		if policy.paused && !manual {
			continue
		}
		due := policy.nextRun
		if manual || due.IsZero() {
			due = now
		}
		if selected == nil || due.Before(selectedAt) ||
			(due.Equal(selectedAt) && name < selectedName) {
			selected = policy
			selectedName = name
			selectedAt = due
		}
	}
	if selected == nil {
		return nil, -1, true
	}
	if selectedAt.After(now) {
		return nil, selectedAt.Sub(now), true
	}
	run := selected.run
	if run == nil {
		run = &productionPolicyRun{done: make(chan struct{})}
		selected.run = run
	}
	selected.queued = true
	manager.productionQueued++
	return &productionPolicyWork{name: selectedName, run: run}, 0, true
}

func (manager *Manager) abandonProductionWork(
	work productionPolicyWork,
	err error,
) {
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	policy := manager.productionPolicies[work.name]
	if policy == nil || policy.run != work.run || !policy.queued {
		return
	}
	policy.queued = false
	manager.productionQueued--
	policy.run = nil
	work.run.err = err
	close(work.run.done)
}

func (manager *Manager) productionWorker() {
	defer manager.productionWorkers.Done()
	for {
		select {
		case work := <-manager.productionWork:
			execution, started := manager.beginProductionWork(work)
			if !started {
				continue
			}
			ctx, cancel := context.WithTimeout(
				manager.ctx, manager.limits.production.cycleTimeout,
			)
			cycle, err := manager.executeProductionPolicyCycle(
				ctx,
				execution.config,
				execution.publisher,
				execution.forceBackup,
				execution.restoreDue,
			)
			cancel()
			manager.finishProductionWork(work, cycle, err)
		case <-manager.ctx.Done():
			return
		}
	}
}

func (manager *Manager) beginProductionWork(
	work productionPolicyWork,
) (productionPolicyExecution, bool) {
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	policy := manager.productionPolicies[work.name]
	if policy == nil || policy.run != work.run || !policy.queued {
		return productionPolicyExecution{}, false
	}
	policy.queued = false
	manager.productionQueued--
	if manager.productionClosed || (policy.paused && !work.run.manual) {
		policy.run = nil
		if manager.productionClosed {
			work.run.err = ErrClosed
		}
		close(work.run.done)
		manager.signalProductionLocked()
		return productionPolicyExecution{}, false
	}
	policy.running = true
	manager.productionRunning++
	policy.attempts++
	manager.productionAttempts++
	policy.lastStartedAt = time.Now()
	restoreDue := work.run.manual || policy.lastUnsafe || policy.lastRestore == nil ||
		policy.lastStartedAt.Sub(policy.lastRestore.VerifiedAt) >=
			policy.config.public.RestoreInterval
	return productionPolicyExecution{
		config:      policy.config.public,
		publisher:   manager.productionPublishers[policy.config.public.Publisher],
		forceBackup: work.run.manual,
		restoreDue:  restoreDue,
	}, true
}

func (manager *Manager) finishProductionWork(
	work productionPolicyWork,
	cycle ProductionPolicyCycle,
	err error,
) {
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	policy := manager.productionPolicies[work.name]
	if policy == nil || policy.run != work.run || !policy.running {
		work.run.cycle = cloneProductionPolicyCycle(cycle)
		work.run.err = err
		close(work.run.done)
		return
	}
	policy.running = false
	manager.productionRunning--
	policy.lastCycle = cloneProductionPolicyCycle(cycle)
	policy.lastFinishedAt = cycle.FinishedAt
	policy.lastErr = err
	if err == nil {
		policy.lastUnsafe = false
	} else if errors.Is(err, ErrProductionUnsafe) {
		policy.lastUnsafe = true
	}
	if cycle.Backup != nil {
		policy.lastBackup = cloneProductionBackupEvidence(cycle.Backup)
		if cycle.Backup.Resumed {
			manager.productionResumptions++
		} else {
			manager.productionBackups++
		}
	}
	if cycle.publicationAttempted {
		manager.productionPublicationAttempts++
	}
	if cycle.publicationFailed {
		manager.productionPublicationFailures++
	}
	if cycle.Publication != nil {
		policy.lastPublication = cloneProductionPublicationEvidence(cycle.Publication)
		if cycle.Publication.Resumed {
			manager.productionAnchorsResumed++
		} else {
			manager.productionAnchorsPublished++
			manager.productionPublishedBytes += uint64(max(0, cycle.Publication.Bytes))
		}
		manager.productionPublishedAnchorsPruned += uint64(
			max(0, cycle.Publication.PrunedAnchors),
		)
	}
	if cycle.Restore != nil {
		policy.lastRestore = cloneProductionRestoreEvidence(cycle.Restore)
		manager.productionRestoreDrills++
	}
	manager.productionPruned += uint64(max(0, cycle.PrunedBackups))
	limits := manager.limits.production
	switch {
	case err == nil:
		policy.successes++
		manager.productionSuccesses++
		policy.consecutiveFailures = 0
		policy.lastSuccessAt = cycle.FinishedAt
		policy.nextRun = nextProductionPolicyRun(policy)
		if policy.nextRun.IsZero() || !policy.nextRun.After(cycle.FinishedAt) {
			policy.nextRun = cycle.FinishedAt.Add(time.Millisecond)
		}
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrClosed):
		policy.cancellations++
		manager.productionCancellations++
		policy.nextRun = time.Time{}
	default:
		policy.failures++
		manager.productionFailures++
		policy.consecutiveFailures++
		manager.productionBackoffs++
		policy.nextRun = cycle.FinishedAt.Add(productionBackoff(
			limits,
			policy.config.public.Name,
			policy.consecutiveFailures,
			policy.attempts,
		))
	}
	work.run.cycle = cloneProductionPolicyCycle(cycle)
	work.run.err = err
	policy.run = nil
	close(work.run.done)
	manager.signalProductionLocked()
}

func nextProductionPolicyRun(policy *productionPolicy) time.Time {
	if policy == nil {
		return time.Time{}
	}
	var next time.Time
	if policy.lastBackup != nil {
		next = policy.lastBackup.CreatedAt.Add(
			policy.config.public.BackupInterval,
		)
	}
	if policy.lastRestore != nil {
		restore := policy.lastRestore.VerifiedAt.Add(
			policy.config.public.RestoreInterval,
		)
		if next.IsZero() || restore.Before(next) {
			next = restore
		}
	}
	return next
}

func productionBackoff(
	limits normalizedProductionSupervisorLimits,
	name string,
	failures uint32,
	attempt uint64,
) time.Duration {
	delay := limits.initialBackoff
	for remaining := failures; remaining > 1 && delay < limits.maxBackoff; remaining-- {
		if delay > limits.maxBackoff/2 {
			delay = limits.maxBackoff
			break
		}
		delay *= 2
	}
	if delay > limits.maxBackoff {
		delay = limits.maxBackoff
	}
	hash := uint64(1469598103934665603)
	for index := 0; index < len(name); index++ {
		hash ^= uint64(name[index])
		hash *= 1099511628211
	}
	for shift := uint(0); shift < 64; shift += 8 {
		hash ^= (attempt >> shift) & 0xff
		hash *= 1099511628211
	}
	quarter := delay / 4
	if quarter == 0 {
		return delay
	}
	return delay - quarter + time.Duration(hash%uint64(quarter+1))
}

func (manager *Manager) addProductionStats(stats *Stats) {
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	stats.ProductionPolicies = len(manager.productionPolicies)
	now := time.Now()
	for _, policy := range manager.productionPolicies {
		if policy.paused {
			stats.PausedProductionPolicies++
		}
		readiness, _, _ := productionReadinessLocked(policy, now)
		switch readiness {
		case ProductionReady:
			stats.ReadyProductionPolicies++
		case ProductionDegraded:
			stats.DegradedProductionPolicies++
		case ProductionUnsafe:
			stats.UnsafeProductionPolicies++
		}
	}
	stats.QueuedProductionPolicies = manager.productionQueued
	stats.RunningProductionPolicies = manager.productionRunning
	stats.MaxProductionPolicies = manager.limits.production.maxPolicies
	stats.MaxConcurrentProductionPolicies = manager.limits.production.maxConcurrent
	stats.ProductionPolicyAttempts = manager.productionAttempts
	stats.ProductionPolicySuccesses = manager.productionSuccesses
	stats.ProductionPolicyFailures = manager.productionFailures
	stats.ProductionPolicyCancellations = manager.productionCancellations
	stats.ProductionPolicyBackoffs = manager.productionBackoffs
	stats.ProductionBackupsCreated = manager.productionBackups
	stats.ProductionBackupsResumed = manager.productionResumptions
	stats.ProductionPublishers = len(manager.productionPublishers)
	stats.ProductionPublicationAttempts = manager.productionPublicationAttempts
	stats.ProductionPublicationFailures = manager.productionPublicationFailures
	stats.ProductionAnchorsPublished = manager.productionAnchorsPublished
	stats.ProductionAnchorsResumed = manager.productionAnchorsResumed
	stats.ProductionPublishedBytes = manager.productionPublishedBytes
	stats.ProductionPublishedAnchorsPruned = manager.productionPublishedAnchorsPruned
	stats.ProductionRestoreDrills = manager.productionRestoreDrills
	stats.ProductionBackupsPruned = manager.productionPruned
}

func (manager *Manager) beginProductionClose() {
	manager.productionMu.Lock()
	defer manager.productionMu.Unlock()
	if manager.productionClosed {
		return
	}
	manager.productionClosed = true
	for _, policy := range manager.productionPolicies {
		if policy.running || policy.run == nil {
			continue
		}
		if policy.queued {
			policy.queued = false
			manager.productionQueued--
		}
		policy.run.err = ErrClosed
		close(policy.run.done)
		policy.run = nil
	}
	manager.signalProductionLocked()
	if !manager.productionStarted {
		close(manager.productionDone)
	}
}

func (manager *Manager) waitForProductionClose(ctx context.Context) error {
	select {
	case <-manager.productionDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
