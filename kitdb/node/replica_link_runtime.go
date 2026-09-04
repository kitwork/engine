package node

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func (manager *Manager) startReplicaFileLinksLocked() {
	if manager.replicaFileLinkStarted || manager.replicaFileLinkClosed {
		return
	}
	manager.replicaFileLinkStarted = true
	workers := manager.limits.replicaFileLinks.maxConcurrent
	manager.replicaFileLinkWorkers.Add(workers + 1)
	go manager.replicaFileLinkDispatcher()
	for range workers {
		go manager.replicaFileLinkWorker()
	}
	go func() {
		manager.replicaFileLinkWorkers.Wait()
		close(manager.replicaFileLinkDone)
	}()
}

func (manager *Manager) signalReplicaFileLinksLocked() {
	select {
	case manager.replicaFileLinkNotify <- struct{}{}:
	default:
	}
}

func (manager *Manager) replicaFileLinkDispatcher() {
	defer manager.replicaFileLinkWorkers.Done()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for {
		work, wait, available := manager.nextReplicaFileLinkWork()
		if !available {
			return
		}
		if work != nil {
			select {
			case manager.replicaFileLinkWork <- *work:
				continue
			case <-manager.ctx.Done():
				manager.abandonReplicaFileLinkWork(*work, ErrClosed)
				return
			}
		}
		if wait < 0 {
			select {
			case <-manager.replicaFileLinkNotify:
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
		case <-manager.replicaFileLinkNotify:
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

func (manager *Manager) nextReplicaFileLinkWork() (
	*replicaFileLinkWork,
	time.Duration,
	bool,
) {
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkClosed || manager.ctx.Err() != nil {
		return nil, 0, false
	}
	now := time.Now()
	var selected *replicaFileLink
	var selectedName string
	var selectedAt time.Time
	for name, link := range manager.replicaFileLinks {
		if link.queued || link.running {
			continue
		}
		manual := link.run != nil && link.run.manual
		if link.paused && !manual {
			continue
		}
		due := link.nextRun
		if manual || due.IsZero() {
			due = now
		}
		if selected == nil || due.Before(selectedAt) ||
			(due.Equal(selectedAt) && name < selectedName) {
			selected = link
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
		run = &replicaFileLinkRun{done: make(chan struct{})}
		selected.run = run
	}
	selected.queued = true
	manager.replicaFileLinkQueued++
	return &replicaFileLinkWork{name: selectedName, run: run}, 0, true
}

func (manager *Manager) abandonReplicaFileLinkWork(
	work replicaFileLinkWork,
	err error,
) {
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	link := manager.replicaFileLinks[work.name]
	if link == nil || link.run != work.run || !link.queued {
		return
	}
	link.queued = false
	manager.replicaFileLinkQueued--
	link.run = nil
	work.run.err = err
	close(work.run.done)
}

func (manager *Manager) replicaFileLinkWorker() {
	defer manager.replicaFileLinkWorkers.Done()
	for {
		select {
		case work := <-manager.replicaFileLinkWork:
			config, started := manager.beginReplicaFileLinkWork(work)
			if !started {
				continue
			}
			cycle, err := manager.executeReplicaFileLinkCycle(manager.ctx, config.public)
			manager.finishReplicaFileLinkWork(work, cycle, err)
		case <-manager.ctx.Done():
			return
		}
	}
}

func (manager *Manager) beginReplicaFileLinkWork(
	work replicaFileLinkWork,
) (normalizedReplicaFileLinkConfig, bool) {
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	link := manager.replicaFileLinks[work.name]
	if link == nil || link.run != work.run || !link.queued {
		return normalizedReplicaFileLinkConfig{}, false
	}
	link.queued = false
	manager.replicaFileLinkQueued--
	if manager.replicaFileLinkClosed || (link.paused && !work.run.manual) {
		link.run = nil
		if manager.replicaFileLinkClosed {
			work.run.err = ErrClosed
		}
		close(work.run.done)
		manager.signalReplicaFileLinksLocked()
		return normalizedReplicaFileLinkConfig{}, false
	}
	link.running = true
	manager.replicaFileLinkRunning++
	link.attempts++
	link.lastStartedAt = time.Now()
	return link.config, true
}

func (manager *Manager) executeReplicaFileLinkCycle(
	ctx context.Context,
	config ReplicaFileLinkConfig,
) (cycle ReplicaFileLinkCycle, err error) {
	cycle.Name = config.Name
	cycle.StartedAt = time.Now()
	defer func() {
		cycle.FinishedAt = time.Now()
		cycle.Duration = cycle.FinishedAt.Sub(cycle.StartedAt)
	}()

	publishTicket, err := manager.ScheduleReplicaFilePublish(
		ctx,
		ReplicaFilePublishRequest{
			Source: config.Source, Mailbox: config.Mailbox,
			SourceOptions: config.SourceOptions, PinName: config.PinName,
			BatchLimits: config.BatchLimits, TransportLimits: config.TransportLimits,
			Priority: config.Priority,
		},
	)
	if err != nil {
		return cycle, err
	}
	publishResult, err := publishTicket.Wait(ctx)
	if publishResult.ReplicaFilePublish != nil {
		published := cloneReplicaFilePublishResult(*publishResult.ReplicaFilePublish)
		cycle.Publish = &published
		cycle.Progress = published.Published
	}
	if err != nil {
		return cycle, err
	}
	if cycle.Publish == nil {
		return cycle, fmt.Errorf("kitdb node: replica link publish returned no evidence")
	}

	applyTicket, err := manager.ScheduleReplicaFileApply(
		ctx,
		ReplicaFileApplyRequest{
			Target: config.Target, Mailbox: config.Mailbox,
			TargetOptions:   config.TargetOptions,
			TransportLimits: config.TransportLimits, Priority: config.Priority,
		},
	)
	if err != nil {
		return cycle, err
	}
	applyResult, err := applyTicket.Wait(ctx)
	if applyResult.ReplicaFileApply != nil {
		applied := cloneReplicaFileApplyResult(*applyResult.ReplicaFileApply)
		cycle.Apply = &applied
		cycle.Progress = cycle.Progress || applied.Found
	}
	if err != nil {
		return cycle, err
	}
	if cycle.Apply == nil {
		return cycle, fmt.Errorf("kitdb node: replica link apply returned no evidence")
	}

	acknowledgeTicket, err := manager.ScheduleReplicaFileAcknowledge(
		ctx,
		ReplicaFileAcknowledgeRequest{
			Source: config.Source, Mailbox: config.Mailbox,
			SourceOptions: config.SourceOptions, PinName: config.PinName,
			TransportLimits: config.TransportLimits, Priority: config.Priority,
		},
	)
	if err != nil {
		return cycle, err
	}
	acknowledgeResult, err := acknowledgeTicket.Wait(ctx)
	if acknowledgeResult.ReplicaFileAcknowledge != nil {
		acknowledged := cloneReplicaFileAcknowledgeResult(
			*acknowledgeResult.ReplicaFileAcknowledge,
		)
		cycle.Acknowledge = &acknowledged
		cycle.Progress = cycle.Progress || acknowledged.Found
	}
	if err != nil {
		return cycle, err
	}
	if cycle.Acknowledge == nil {
		return cycle, fmt.Errorf("kitdb node: replica link acknowledge returned no evidence")
	}
	cycle.CaughtUp = cycle.Publish.NoChanges ||
		(cycle.Acknowledge.Found &&
			cycle.Acknowledge.Pin.Cursor == cycle.Publish.SourceBoundary)
	return cycle, nil
}

func (manager *Manager) finishReplicaFileLinkWork(
	work replicaFileLinkWork,
	cycle ReplicaFileLinkCycle,
	err error,
) {
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	link := manager.replicaFileLinks[work.name]
	if link == nil || link.run != work.run || !link.running {
		work.run.cycle = cloneReplicaFileLinkCycle(cycle)
		work.run.err = err
		close(work.run.done)
		return
	}
	link.running = false
	manager.replicaFileLinkRunning--
	manager.replicaFileLinkCycles++
	link.lastCycle = cloneReplicaFileLinkCycle(cycle)
	link.lastFinishedAt = cycle.FinishedAt
	link.lastErr = err
	limits := manager.limits.replicaFileLinks
	switch {
	case err == nil:
		link.successes++
		manager.replicaFileLinkSuccesses++
		link.consecutiveFailures = 0
		link.lastSuccessAt = cycle.FinishedAt
		if cycle.Progress {
			link.nextRun = cycle.FinishedAt.Add(limits.activeInterval)
		} else {
			link.nextRun = cycle.FinishedAt.Add(limits.idleInterval)
		}
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, ErrClosed):
		link.cancellations++
		manager.replicaFileLinkCancellations++
		link.nextRun = time.Time{}
	default:
		link.failures++
		manager.replicaFileLinkFailures++
		link.consecutiveFailures++
		manager.replicaFileLinkBackoffs++
		delay := replicaFileLinkBackoff(
			limits, link.config.public.Name,
			link.consecutiveFailures, link.attempts,
		)
		link.nextRun = cycle.FinishedAt.Add(delay)
	}
	work.run.cycle = cloneReplicaFileLinkCycle(cycle)
	work.run.err = err
	link.run = nil
	close(work.run.done)
	manager.signalReplicaFileLinksLocked()
}

func replicaFileLinkBackoff(
	limits normalizedReplicaFileLinkLimits,
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
	span := uint64(quarter + 1)
	offset := time.Duration(hash % span)
	return delay - quarter + offset
}

func cloneReplicaFileLinkCycle(source ReplicaFileLinkCycle) ReplicaFileLinkCycle {
	cloned := source
	if source.Publish != nil {
		published := cloneReplicaFilePublishResult(*source.Publish)
		cloned.Publish = &published
	}
	if source.Apply != nil {
		applied := cloneReplicaFileApplyResult(*source.Apply)
		cloned.Apply = &applied
	}
	if source.Acknowledge != nil {
		acknowledged := cloneReplicaFileAcknowledgeResult(*source.Acknowledge)
		cloned.Acknowledge = &acknowledged
	}
	return cloned
}

func cloneReplicaFilePublishResult(
	source ReplicaFilePublishResult,
) ReplicaFilePublishResult {
	cloned := source
	if source.Publication != nil {
		publication := *source.Publication
		cloned.Publication = &publication
	}
	return cloned
}

func cloneReplicaFileApplyResult(source ReplicaFileApplyResult) ReplicaFileApplyResult {
	cloned := source
	if source.Apply != nil {
		applied := *source.Apply
		cloned.Apply = &applied
	}
	return cloned
}

func cloneReplicaFileAcknowledgeResult(
	source ReplicaFileAcknowledgeResult,
) ReplicaFileAcknowledgeResult {
	cloned := source
	if source.Acknowledge != nil {
		acknowledged := *source.Acknowledge
		cloned.Acknowledge = &acknowledged
	}
	return cloned
}

func (manager *Manager) addReplicaFileLinkStats(stats *Stats) {
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	stats.ReplicaFileLinks = len(manager.replicaFileLinks)
	for _, link := range manager.replicaFileLinks {
		if link.paused {
			stats.PausedReplicaFileLinks++
		}
	}
	stats.QueuedReplicaFileLinks = manager.replicaFileLinkQueued
	stats.RunningReplicaFileLinks = manager.replicaFileLinkRunning
	stats.MaxReplicaFileLinks = manager.limits.replicaFileLinks.maxLinks
	stats.MaxConcurrentReplicaFileLinks = manager.limits.replicaFileLinks.maxConcurrent
	stats.ReplicaFileLinkActiveInterval = manager.limits.replicaFileLinks.activeInterval
	stats.ReplicaFileLinkIdleInterval = manager.limits.replicaFileLinks.idleInterval
	stats.ReplicaFileLinkInitialBackoff = manager.limits.replicaFileLinks.initialBackoff
	stats.ReplicaFileLinkMaxBackoff = manager.limits.replicaFileLinks.maxBackoff
	stats.ReplicaFileLinkCycles = manager.replicaFileLinkCycles
	stats.ReplicaFileLinkSuccesses = manager.replicaFileLinkSuccesses
	stats.ReplicaFileLinkFailures = manager.replicaFileLinkFailures
	stats.ReplicaFileLinkCancellations = manager.replicaFileLinkCancellations
	stats.ReplicaFileLinkBackoffs = manager.replicaFileLinkBackoffs
}

func (manager *Manager) beginReplicaFileLinkClose() {
	manager.replicaFileLinkMu.Lock()
	defer manager.replicaFileLinkMu.Unlock()
	if manager.replicaFileLinkClosed {
		return
	}
	manager.replicaFileLinkClosed = true
	for _, link := range manager.replicaFileLinks {
		if link.running || link.run == nil {
			continue
		}
		if link.queued {
			link.queued = false
			manager.replicaFileLinkQueued--
		}
		link.run.err = ErrClosed
		close(link.run.done)
		link.run = nil
	}
	manager.signalReplicaFileLinksLocked()
	if !manager.replicaFileLinkStarted {
		close(manager.replicaFileLinkDone)
	}
}

func (manager *Manager) waitForReplicaFileLinkClose(ctx context.Context) error {
	select {
	case <-manager.replicaFileLinkDone:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
