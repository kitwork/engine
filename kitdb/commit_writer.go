package kitdb

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"
)

const (
	defaultCommitQueueSize = 256
	defaultMaxCommitBatch  = 64
	maximumCommitQueueSize = 4096
	maximumCommitBatch     = 1024
	maximumGroupCommitWait = 100 * time.Millisecond
)

type commitRequest struct {
	snapshot         *uint64
	sequenceOnly     bool
	operations       []operation
	committedAt      int64
	preserveCommitAt bool
	result           chan commitResult
}

type commitResult struct {
	transaction uint64
	err         error
}

type committedFrame struct {
	transaction uint64
	checksum    uint32
	committedAt int64
}

func normalizeCommitOptions(options OpenOptions) (queueSize, maxBatch int, delay time.Duration, err error) {
	queueSize = options.CommitQueueSize
	if queueSize == 0 {
		queueSize = defaultCommitQueueSize
	}
	if queueSize < 0 || queueSize > maximumCommitQueueSize {
		return 0, 0, 0, fmt.Errorf("kitdb: commit queue size must be between 1 and %d", maximumCommitQueueSize)
	}

	maxBatch = options.MaxCommitBatch
	if maxBatch == 0 {
		maxBatch = min(defaultMaxCommitBatch, queueSize)
	}
	if maxBatch < 0 || maxBatch > maximumCommitBatch || maxBatch > queueSize {
		return 0, 0, 0, fmt.Errorf("kitdb: maximum commit batch must be between 1 and the commit queue size")
	}

	delay = options.GroupCommitDelay
	if delay < 0 || delay > maximumGroupCommitWait {
		return 0, 0, 0, fmt.Errorf("kitdb: group commit delay must be between 0 and %s", maximumGroupCommitWait)
	}
	return queueSize, maxBatch, delay, nil
}

func (db *DB) commit(operations []operation) (uint64, error) {
	return db.commitAt(operations, 0, false)
}

func (db *DB) commitReplica(operations []operation, committedAt int64) (uint64, error) {
	return db.commitAt(operations, committedAt, true)
}

func (db *DB) commitAt(operations []operation, committedAt int64, preserveCommitAt bool) (uint64, error) {
	return db.submitCommit(&commitRequest{operations: operations, committedAt: committedAt, preserveCommitAt: preserveCommitAt})
}

func (db *DB) submitCommit(request *commitRequest) (uint64, error) {
	if len(request.operations) == 0 {
		return 0, ErrEmptyTransaction
	}
	if request.sequenceOnly {
		op := request.operations[0]
		if len(request.operations) != 1 || op.kind != operationPut || len(op.key) != 17 || op.key[0] != 0x02 || len(op.value) != 10 {
			return 0, fmt.Errorf("kitdb: invalid internal sequence reservation")
		}
	}
	if request.preserveCommitAt && request.committedAt < 0 {
		return 0, fmt.Errorf("kitdb: replica commit timestamp must not be negative")
	}
	request.result = make(chan commitResult, 1)

	// Admission and queue closure share one lock, so Close cannot race a send
	// into a closed channel. The active-transaction ceiling guarantees enough
	// queue capacity for every transaction that can reach this point.
	db.submitMu.Lock()
	db.mu.RLock()
	err := db.stateErrorLocked()
	queue := db.commitQueue
	db.mu.RUnlock()
	if err != nil {
		db.submitMu.Unlock()
		return 0, err
	}
	queue <- request
	db.submitMu.Unlock()

	result := <-request.result
	return result.transaction, result.err
}

func (db *DB) runCommitWriter() {
	defer close(db.commitWriterDone)
	for {
		first, open := <-db.commitQueue
		if !open {
			return
		}
		batch, closed := db.collectCommitBatch(first)
		db.commitBatch(batch)
		if closed {
			return
		}
	}
}

func (db *DB) collectCommitBatch(first *commitRequest) ([]*commitRequest, bool) {
	batch := make([]*commitRequest, 1, db.maxCommitBatch)
	batch[0] = first
	if db.maxCommitBatch == 1 {
		return batch, false
	}

	if db.groupCommitDelay == 0 {
		for len(batch) < db.maxCommitBatch {
			select {
			case request, open := <-db.commitQueue:
				if !open {
					return batch, true
				}
				batch = append(batch, request)
			default:
				return batch, false
			}
		}
		return batch, false
	}

	timer := time.NewTimer(db.groupCommitDelay)
	defer func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
	}()
	for len(batch) < db.maxCommitBatch {
		select {
		case request, open := <-db.commitQueue:
			if !open {
				return batch, true
			}
			batch = append(batch, request)
		case <-timer.C:
			return batch, false
		}
	}
	return batch, false
}

func (db *DB) commitBatch(requests []*commitRequest) {
	valid := requests[:0]
	for _, request := range requests {
		if err := validateCommitOperations(request.operations); err != nil {
			request.result <- commitResult{err: err}
			continue
		}
		valid = append(valid, request)
	}
	requests = valid
	if len(requests) == 0 {
		return
	}
	touchesCatalog := false
	for _, request := range requests {
		if requestTouchesCatalog(request) {
			touchesCatalog = true
			break
		}
	}
	if touchesCatalog {
		if err := db.ensureCatalogLoadedForCommit(); err != nil {
			valid = requests[:0]
			for _, request := range requests {
				if requestTouchesCatalog(request) {
					request.result <- commitResult{err: err}
					continue
				}
				valid = append(valid, request)
			}
			requests = valid
			if len(requests) == 0 {
				return
			}
			touchesCatalog = false
		}
	}

	db.commitMu.Lock()
	db.mu.RLock()
	if err := db.commitStateErrorLocked(); err != nil {
		db.mu.RUnlock()
		db.commitMu.Unlock()
		completeCommitBatch(requests, nil, err)
		return
	}
	lastTransaction := db.lastTx
	lastDataTransaction := db.lastDataTx
	lastCommitTime := db.lastCommitTime
	catalog := db.catalog
	wal := db.wal
	expectedEnd := db.walEnd
	db.mu.RUnlock()

	valid = requests[:0]
	for _, request := range requests {
		if request.snapshot != nil && (*request.snapshot > lastTransaction || lastDataTransaction > *request.snapshot) {
			request.result <- commitResult{err: ErrTransactionConflict}
			continue
		}
		if touchesCatalog {
			next, err := catalog.applyOperations(request.operations)
			if err != nil {
				request.result <- commitResult{err: err}
				continue
			}
			catalog = next
		}
		valid = append(valid, request)
		if !request.sequenceOnly {
			lastDataTransaction = lastTransaction + uint64(len(valid))
		}
	}
	requests = valid
	if len(requests) == 0 {
		db.commitMu.Unlock()
		return
	}
	if uint64(len(requests)) > math.MaxUint64-lastTransaction {
		db.commitMu.Unlock()
		completeCommitBatch(requests, nil, ErrTransactionIDOverflow)
		return
	}
	firstTransaction := lastTransaction + 1

	frames := make([]committedFrame, len(requests))
	now := time.Now().UTC().UnixNano()
	for index := range requests {
		frames[index].transaction = firstTransaction + uint64(index)
		request := requests[index]
		if request.preserveCommitAt {
			frames[index].committedAt = request.committedAt
		} else {
			if now <= lastCommitTime {
				if lastCommitTime == math.MaxInt64 {
					db.commitMu.Unlock()
					completeCommitBatch(requests, frames, fmt.Errorf("kitdb: commit timestamp overflow"))
					return
				}
				now = lastCommitTime + 1
			}
			frames[index].committedAt = now
			now++
		}
		if frames[index].committedAt != 0 {
			if lastCommitTime != 0 && frames[index].committedAt <= lastCommitTime {
				db.commitMu.Unlock()
				completeCommitBatch(requests, frames, errors.Join(
					ErrReplicaDiverged,
					fmt.Errorf("kitdb: commit timestamp %d does not follow %d", frames[index].committedAt, lastCommitTime),
				))
				return
			}
			lastCommitTime = frames[index].committedAt
		}
	}

	walPosition, err := wal.Seek(0, io.SeekEnd)
	if err != nil {
		db.markUnavailable(err)
		db.commitMu.Unlock()
		completeCommitBatch(requests, frames, errors.Join(ErrUnavailable, err))
		return
	}
	if walPosition != expectedEnd {
		err := fmt.Errorf("kitdb: WAL end changed from %d to %d outside the database owner", expectedEnd, walPosition)
		db.markUnavailable(err)
		db.commitMu.Unlock()
		completeCommitBatch(requests, frames, errors.Join(ErrUnavailable, err))
		return
	}

	end := walPosition
	for index, request := range requests {
		frame, err := encodeFrameAt(frames[index].transaction, frames[index].committedAt, request.operations)
		if err != nil {
			durabilityErr := errors.Join(ErrDurabilityUncertain, err)
			db.markUnavailable(durabilityErr)
			db.commitMu.Unlock()
			completeCommitBatch(requests, frames, durabilityErr)
			return
		}
		if _, err := writeAll(wal, frame); err != nil {
			durabilityErr := errors.Join(ErrDurabilityUncertain, err)
			db.markUnavailable(durabilityErr)
			db.commitMu.Unlock()
			completeCommitBatch(requests, frames, durabilityErr)
			return
		}
		frames[index].checksum = frameChecksum(frame)
		end += int64(len(frame))
	}
	if err := wal.Sync(); err != nil {
		durabilityErr := errors.Join(ErrDurabilityUncertain, err)
		db.markUnavailable(durabilityErr)
		db.commitMu.Unlock()
		completeCommitBatch(requests, frames, durabilityErr)
		return
	}

	db.mu.Lock()
	for index, request := range requests {
		applyOperations(db.overlay, request.operations)
		db.lastTx = frames[index].transaction
		if !request.sequenceOnly {
			db.lastDataTx = frames[index].transaction
		}
		if frames[index].committedAt != 0 {
			db.lastCommitTime = frames[index].committedAt
		}
	}
	if touchesCatalog {
		db.catalog = catalog
	}
	db.walEnd = end
	db.walChecksum = frames[len(frames)-1].checksum
	db.commitBatches++
	db.committedTransactions += uint64(len(requests))
	db.commitSyncs++
	if len(requests) > db.largestCommitBatch {
		db.largestCommitBatch = len(requests)
	}
	db.mu.Unlock()
	db.commitMu.Unlock()

	for index, request := range requests {
		db.dispatchCommit(frames[index].transaction, frames[index].checksum, frames[index].committedAt, request.operations)
		request.result <- commitResult{transaction: frames[index].transaction}
	}
}

func (db *DB) commitStateErrorLocked() error {
	if db.closed {
		return ErrClosed
	}
	if db.fatal != nil {
		return errors.Join(ErrUnavailable, db.fatal)
	}
	return nil
}

func completeCommitBatch(requests []*commitRequest, frames []committedFrame, err error) {
	for index, request := range requests {
		transaction := uint64(0)
		if index < len(frames) {
			transaction = frames[index].transaction
		}
		request.result <- commitResult{transaction: transaction, err: err}
	}
}

func validateCommitOperations(operations []operation) error {
	if len(operations) == 0 {
		return ErrEmptyTransaction
	}
	if len(operations) > maxOperations {
		return ErrTransactionTooLarge
	}
	payloadSize := timedPayloadPrefixSize
	for _, operation := range operations {
		if err := validateOperation(operation); err != nil {
			return err
		}
		size := operationHeaderSize + len(operation.key) + len(operation.value)
		if size > maxPayloadSize-payloadSize {
			return ErrTransactionTooLarge
		}
		payloadSize += size
	}
	return nil
}
