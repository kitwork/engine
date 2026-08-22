package search

import (
	"context"
	"errors"
	"fmt"
	"time"
)

type mutationKind uint8

const (
	mutationAdd mutationKind = iota
	mutationUpdate
	mutationDelete
	mutationBeginReplacement
)

type mutationRequest struct {
	kind          mutationKind
	document      Document
	identifier    string
	reservedBytes int64
	response      chan mutationResponse
	replacement   *managedReplacementState
}

func (request mutationRequest) identity() string {
	if request.kind == mutationDelete {
		return request.identifier
	}
	return request.document.ID
}

func (request mutationRequest) isReplacement() bool {
	return request.kind == mutationBeginReplacement
}

type mutationResponse struct {
	info    IndexInfo
	deleted bool
	err     error
}

type maintenanceRequest struct {
	response chan error
}

type appliedMutation struct {
	request mutationRequest
	result  mutationResponse
	applied bool
}

func (managed *managedIndex) runWriter() {
	defer close(managed.done)
	defer func() {
		managed.closeErr = errors.Join(managed.closeErr, managed.writer.Close())
	}()

	var carry *mutationRequest
	inputClosed := false
	for {
		if carry == nil && inputClosed {
			return
		}
		var first mutationRequest
		if carry != nil {
			first = *carry
			carry = nil
		} else {
			select {
			case request, open := <-managed.requests:
				if !open {
					inputClosed = true
					continue
				}
				managed.markDequeued(request)
				first = request
			case request := <-managed.maintain:
				err := managed.runMaintenance()
				if request.response != nil {
					request.response <- err
				}
				continue
			}
		}
		if first.isReplacement() {
			mutated, terminal := managed.runManagedReplacement(first.replacement)
			if terminal != nil {
				managed.drainFailedMutations(terminal, carry, inputClosed)
				return
			}
			if mutated {
				_ = managed.runMaintenance()
			}
			continue
		}

		batch := make([]mutationRequest, 0, managed.manager.options.MutationBatchSize)
		batch = append(batch, first)
		identities := map[string]struct{}{first.identity(): {}}
		timer := time.NewTimer(managed.manager.options.MutationBatchDelay)
	gather:
		for len(batch) < managed.manager.options.MutationBatchSize {
			select {
			case request, open := <-managed.requests:
				if !open {
					inputClosed = true
					break gather
				}
				managed.markDequeued(request)
				if request.isReplacement() {
					carry = &request
					break gather
				}
				identity := request.identity()
				if _, duplicate := identities[identity]; duplicate {
					carry = &request
					break gather
				}
				identities[identity] = struct{}{}
				batch = append(batch, request)
			case <-timer.C:
				break gather
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}

		mutated, terminal := managed.runMutationBatch(batch)
		if terminal != nil {
			managed.drainFailedMutations(terminal, carry, inputClosed)
			return
		}
		if mutated {
			_ = managed.runMaintenance()
		}
	}
}

func (managed *managedIndex) markDequeued(request mutationRequest) {
	if request.isReplacement() {
		managed.manager.queuedReplacements.Add(-1)
		return
	}
	managed.manager.queuedMutations.Add(-1)
	managed.queuedMutations.Add(-1)
}

func (managed *managedIndex) runMutationBatch(batch []mutationRequest) (bool, error) {
	managed.manager.mutationSlots <- struct{}{}
	managed.manager.activeMutationBatches.Add(1)
	defer func() {
		managed.manager.activeMutationBatches.Add(-1)
		<-managed.manager.mutationSlots
	}()
	return managed.processMutationBatch(batch)
}

func (managed *managedIndex) processMutationBatch(batch []mutationRequest) (bool, error) {
	operationCtx, cancel := context.WithTimeout(context.Background(), managed.manager.options.CommitTimeout)
	defer cancel()

	applied := make([]appliedMutation, len(batch))
	appliedCount := 0
	for position, request := range batch {
		result := appliedMutation{request: request}
		switch request.kind {
		case mutationAdd:
			result.result.err = managed.writer.Add(operationCtx, request.document)
			result.applied = result.result.err == nil
		case mutationUpdate:
			result.result.err = managed.writer.Update(operationCtx, request.document)
			result.applied = result.result.err == nil
		case mutationDelete:
			result.result.deleted, result.result.err = managed.writer.Delete(operationCtx, request.identifier)
			result.applied = result.result.err == nil && result.result.deleted
		default:
			result.result.err = fmt.Errorf("search: unknown managed mutation")
		}
		if result.applied {
			appliedCount++
		}
		applied[position] = result
	}

	info := managed.currentInfo()
	if appliedCount != 0 {
		committed, err := managed.writer.Commit(operationCtx)
		if err != nil {
			terminal := managed.fail(err)
			for position := range applied {
				if applied[position].applied {
					applied[position].result.err = terminal
				}
			}
			managed.respondMutations(applied, committed)
			return false, terminal
		}
		info, err = managed.refreshSnapshot()
		if err != nil {
			terminal := managed.fail(fmt.Errorf("committed generation %d cannot be opened: %w", committed.Generation, err))
			for position := range applied {
				if applied[position].applied {
					applied[position].result.err = terminal
				}
			}
			managed.respondMutations(applied, committed)
			return false, terminal
		}
		managed.manager.commits.Add(1)
	}
	managed.respondMutations(applied, info)
	return appliedCount != 0, nil
}

func (managed *managedIndex) respondMutations(applied []appliedMutation, info IndexInfo) {
	var releasedBytes int64
	for _, mutation := range applied {
		mutation.result.info = info
		mutation.request.response <- mutation.result
		managed.pendingMutations.Add(-1)
		releasedBytes += mutation.request.reservedBytes
	}
	managed.releaseMutationBytes(releasedBytes)
}

func (managed *managedIndex) drainFailedMutations(terminal error, carry *mutationRequest, inputClosed bool) {
	var releasedBytes int64
	respond := func(request mutationRequest) {
		if request.isReplacement() {
			request.replacement.rejectStart(terminal)
			return
		}
		request.response <- mutationResponse{info: managed.currentInfo(), err: terminal}
		managed.pendingMutations.Add(-1)
		releasedBytes += request.reservedBytes
	}
	if carry != nil {
		respond(*carry)
	}
	if inputClosed {
		managed.releaseMutationBytes(releasedBytes)
		return
	}
	for request := range managed.requests {
		managed.markDequeued(request)
		respond(request)
	}
	managed.releaseMutationBytes(releasedBytes)
}

func (managed *managedIndex) runMaintenance() error {
	if managed.manager.options.DisableAutoCompact && !managed.manager.options.AutoGarbageCollect {
		managed.recordMaintenance(nil)
		return nil
	}
	lifetime, releaseLifetime := linkContexts(managed.ctx, managed.manager.ctx)
	defer releaseLifetime()
	operationCtx, cancel := context.WithTimeout(lifetime, managed.manager.options.MaintenanceTimeout)
	defer cancel()
	if err := acquireBoundedSlot(operationCtx, managed.manager.maintenanceSlots); err != nil {
		managed.recordMaintenance(err)
		return err
	}
	managed.manager.activeMaintenance.Add(1)
	defer func() {
		managed.manager.activeMaintenance.Add(-1)
		<-managed.manager.maintenanceSlots
	}()

	var maintenanceErrors []error
	if !managed.manager.options.DisableAutoCompact {
		_, merged, err := managed.writer.Compact(operationCtx, managed.manager.options.compact)
		if err != nil {
			maintenanceErrors = append(maintenanceErrors, err)
		} else if merged {
			if _, err := managed.refreshSnapshot(); err != nil {
				maintenanceErrors = append(maintenanceErrors, err)
			} else {
				managed.manager.compactions.Add(1)
			}
		}
	}
	if managed.manager.options.AutoGarbageCollect && operationCtx.Err() == nil {
		oldest := managed.oldestActiveGeneration()
		if oldest != 0 {
			_, err := managed.writer.GarbageCollect(operationCtx, GarbageCollectOptions{
				OldestRetainedGeneration: oldest,
			})
			if err != nil {
				maintenanceErrors = append(maintenanceErrors, err)
			} else {
				managed.manager.garbageCollections.Add(1)
			}
		}
	}
	err := errors.Join(maintenanceErrors...)
	managed.recordMaintenance(err)
	return err
}
