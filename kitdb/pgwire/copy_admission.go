package pgwire

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"sync"
	"time"
)

const (
	copyAdmissionKeyLimit  = 512
	copyAdmissionMaxWeight = 64
	copyAdmissionStride    = uint64(copyAdmissionMaxWeight)
)

var errCopyAdmissionQueueFull = errors.New("pgwire: COPY admission queue is full")

type copyAdmissionScheduler struct {
	mu              sync.Mutex
	maxActive       int
	maxPerKey       int
	maxQueued       int
	maxQueuedPerKey int
	active          int
	queued          int
	nextRequest     uint64
	nextGrant       uint64
	virtualTime     uint64
	states          map[string]*copyAdmissionState
	metrics         *CopyMetrics
}

type copyAdmissionState struct {
	key        string
	weight     int
	active     int
	virtualRun uint64
	lastGrant  uint64
	waiters    []*copyAdmissionRequest
}

type copyAdmissionRequest struct {
	sequence uint64
	started  time.Time
	ready    chan struct{}
	granted  bool
	measured bool
}

func newCopyAdmissionScheduler(
	maxActive int,
	maxPerKey int,
	maxQueued int,
	maxQueuedPerKey int,
	metrics *CopyMetrics,
) *copyAdmissionScheduler {
	return &copyAdmissionScheduler{
		maxActive: maxActive, maxPerKey: maxPerKey,
		maxQueued: maxQueued, maxQueuedPerKey: maxQueuedPerKey,
		states: make(map[string]*copyAdmissionState), metrics: metrics,
	}
}

func copyAdmissionFor(session Session) CopyAdmission {
	admission := CopyAdmission{Key: "default", Weight: 1}
	if provider, ok := session.(CopyAdmissionSession); ok {
		admission = provider.CopyAdmission()
	}
	admission.Key, admission.Weight = normalizeAdmission(admission.Key, admission.Weight)
	return admission
}

func queryAdmissionFor(session Session) QueryAdmission {
	admission := QueryAdmission{Key: "default", Weight: 1}
	if provider, ok := session.(QueryAdmissionSession); ok {
		admission = provider.QueryAdmission()
	}
	admission.Key, admission.Weight = normalizeAdmission(admission.Key, admission.Weight)
	return admission
}

func normalizeAdmission(key string, weight int) (string, int) {
	key = strings.TrimSpace(key)
	if key == "" {
		key = "default"
	}
	if len(key) > copyAdmissionKeyLimit {
		digest := sha256.Sum256([]byte(key))
		key = "sha256:" + hex.EncodeToString(digest[:])
	}
	if weight < 1 {
		weight = 1
	}
	if weight > copyAdmissionMaxWeight {
		weight = copyAdmissionMaxWeight
	}
	return key, weight
}

func (scheduler *copyAdmissionScheduler) acquire(
	ctx context.Context,
	admission CopyAdmission,
) (func(int64, bool, error), error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	started := time.Now()
	request := &copyAdmissionRequest{started: started, ready: make(chan struct{})}

	scheduler.mu.Lock()
	scheduler.nextRequest++
	request.sequence = scheduler.nextRequest
	state := scheduler.states[admission.Key]
	if state == nil {
		state = &copyAdmissionState{
			key: admission.Key, weight: admission.Weight, virtualRun: scheduler.virtualTime,
		}
		scheduler.states[admission.Key] = state
	}
	globalFull := scheduler.queued >= scheduler.maxQueued
	keyFull := len(state.waiters) >= scheduler.maxQueuedPerKey
	canRunNow := scheduler.active < scheduler.maxActive && state.active < scheduler.maxPerKey
	if (globalFull || keyFull) && !canRunNow {
		scheduler.cleanupStateLocked(state)
		scheduler.metrics.recordCopyRejected()
		scheduler.mu.Unlock()
		return nil, errCopyAdmissionQueueFull
	}
	state.waiters = append(state.waiters, request)
	scheduler.queued++
	scheduler.dispatchLocked()
	if !request.granted && (scheduler.queued > scheduler.maxQueued || len(state.waiters) > scheduler.maxQueuedPerKey) {
		scheduler.removeRequestLocked(state, request)
		scheduler.cleanupStateLocked(state)
		scheduler.dispatchLocked()
		scheduler.metrics.recordCopyRejected()
		scheduler.mu.Unlock()
		return nil, errCopyAdmissionQueueFull
	}
	if !request.granted {
		request.measured = true
		scheduler.metrics.recordCopyQueued()
	}
	granted := request.granted
	scheduler.mu.Unlock()

	if granted {
		return scheduler.releaseFunc(admission.Key), nil
	}
	select {
	case <-request.ready:
		return scheduler.releaseFunc(admission.Key), nil
	case <-ctx.Done():
		scheduler.mu.Lock()
		if request.granted {
			scheduler.mu.Unlock()
			return scheduler.releaseFunc(admission.Key), nil
		}
		scheduler.removeRequestLocked(state, request)
		if request.measured {
			scheduler.metrics.recordCopyDequeued()
		}
		scheduler.metrics.recordCopyWaitTimeout(time.Since(started))
		scheduler.cleanupStateLocked(state)
		scheduler.dispatchLocked()
		scheduler.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (scheduler *copyAdmissionScheduler) releaseFunc(key string) func(int64, bool, error) {
	var once sync.Once
	return func(bytes int64, handled bool, err error) {
		once.Do(func() {
			scheduler.mu.Lock()
			state := scheduler.states[key]
			if state != nil && state.active > 0 && scheduler.active > 0 {
				state.active--
				scheduler.active--
				scheduler.metrics.recordCopyRelease(bytes, handled, err)
				scheduler.cleanupStateLocked(state)
				scheduler.recomputeVirtualTimeLocked()
				scheduler.dispatchLocked()
			}
			scheduler.mu.Unlock()
		})
	}
}

func (scheduler *copyAdmissionScheduler) dispatchLocked() {
	for scheduler.active < scheduler.maxActive {
		state := scheduler.nextStateLocked()
		if state == nil {
			return
		}
		request := state.waiters[0]
		state.waiters[0] = nil
		state.waiters = state.waiters[1:]
		scheduler.queued--
		state.active++
		scheduler.active++
		request.granted = true
		if request.measured {
			scheduler.metrics.recordCopyDequeued()
		}
		stride := copyAdmissionStride / uint64(state.weight)
		if state.virtualRun > math.MaxUint64-stride {
			scheduler.scaleVirtualTimeLocked()
		}
		state.virtualRun += stride
		scheduler.nextGrant++
		state.lastGrant = scheduler.nextGrant
		scheduler.recomputeVirtualTimeLocked()
		scheduler.metrics.recordCopyAcquire(time.Since(request.started))
		close(request.ready)
	}
}

func (scheduler *copyAdmissionScheduler) nextStateLocked() *copyAdmissionState {
	var selected *copyAdmissionState
	for _, state := range scheduler.states {
		if len(state.waiters) == 0 || state.active >= scheduler.maxPerKey {
			continue
		}
		if selected == nil || copyAdmissionStateBefore(state, selected) {
			selected = state
		}
	}
	return selected
}

func copyAdmissionStateBefore(left, right *copyAdmissionState) bool {
	if left.virtualRun != right.virtualRun {
		return left.virtualRun < right.virtualRun
	}
	if left.lastGrant != right.lastGrant {
		return left.lastGrant < right.lastGrant
	}
	if left.waiters[0].sequence != right.waiters[0].sequence {
		return left.waiters[0].sequence < right.waiters[0].sequence
	}
	return left.key < right.key
}

func (scheduler *copyAdmissionScheduler) removeRequestLocked(
	state *copyAdmissionState,
	request *copyAdmissionRequest,
) {
	for index, candidate := range state.waiters {
		if candidate != request {
			continue
		}
		copy(state.waiters[index:], state.waiters[index+1:])
		state.waiters[len(state.waiters)-1] = nil
		state.waiters = state.waiters[:len(state.waiters)-1]
		scheduler.queued--
		return
	}
}

func (scheduler *copyAdmissionScheduler) cleanupStateLocked(state *copyAdmissionState) {
	if state != nil && state.active == 0 && len(state.waiters) == 0 {
		delete(scheduler.states, state.key)
	}
}

func (scheduler *copyAdmissionScheduler) recomputeVirtualTimeLocked() {
	var minimum uint64
	found := false
	for _, state := range scheduler.states {
		if state.active == 0 && len(state.waiters) == 0 {
			continue
		}
		if !found || state.virtualRun < minimum {
			minimum, found = state.virtualRun, true
		}
	}
	if found {
		scheduler.virtualTime = minimum
	}
}

func (scheduler *copyAdmissionScheduler) scaleVirtualTimeLocked() {
	for _, state := range scheduler.states {
		state.virtualRun /= 2
	}
	scheduler.virtualTime /= 2
}

func (metrics *CopyMetrics) recordCopyQueued() {
	queued := metrics.queued.Add(1)
	for {
		peak := metrics.peakQueued.Load()
		if queued <= peak || metrics.peakQueued.CompareAndSwap(peak, queued) {
			return
		}
	}
}

func (metrics *CopyMetrics) recordCopyDequeued() {
	metrics.queued.Add(-1)
}

func (metrics *CopyMetrics) recordCopyRejected() {
	metrics.rejected.Add(1)
}
