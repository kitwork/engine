package search

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

const searchLatencyBucketCount = 14

var searchLatencyBounds = [searchLatencyBucketCount]time.Duration{
	100 * time.Microsecond,
	250 * time.Microsecond,
	500 * time.Microsecond,
	time.Millisecond,
	2500 * time.Microsecond,
	5 * time.Millisecond,
	10 * time.Millisecond,
	25 * time.Millisecond,
	50 * time.Millisecond,
	100 * time.Millisecond,
	250 * time.Millisecond,
	500 * time.Millisecond,
	time.Second,
	5 * time.Second,
}

// SearchLatencyBucket is one exclusive latency range. The lower bound is the
// preceding bucket's upper bound and Count is not cumulative.
type SearchLatencyBucket struct {
	UpperBoundMicroseconds uint64
	Count                  uint64
}

// SearchLatencyStats is a bounded latency histogram. Its shape never depends
// on traffic volume, tenant keys, queries, or document identifiers.
type SearchLatencyStats struct {
	Count            uint64
	TotalNanoseconds uint64
	MaxNanoseconds   uint64
	Buckets          []SearchLatencyBucket
	Overflow         uint64
}

// SearchRuntimeStats describe bounded process-local search health. Total
// latency includes index opening and admission; QueueLatency covers only the
// wait for tenant and process slots; ExecutionLatency covers immutable search.
type SearchRuntimeStats struct {
	Started    uint64
	Completed  uint64
	Succeeded  uint64
	Failed     uint64
	Canceled   uint64
	Overloaded uint64
	Waiting    int64
	MaxWaiting int64
	Active     int64
	MaxActive  int64

	TotalLatency     SearchLatencyStats
	QueueLatency     SearchLatencyStats
	ExecutionLatency SearchLatencyStats
}

type searchLatencyHistogram struct {
	count    atomic.Uint64
	total    atomic.Uint64
	maximum  atomic.Uint64
	buckets  [searchLatencyBucketCount]atomic.Uint64
	overflow atomic.Uint64
}

type searchRuntimeHealth struct {
	started    atomic.Uint64
	succeeded  atomic.Uint64
	failed     atomic.Uint64
	canceled   atomic.Uint64
	overloaded atomic.Uint64
	waiting    atomic.Int64
	maxWaiting atomic.Int64
	active     atomic.Int64
	maxActive  atomic.Int64

	totalLatency     searchLatencyHistogram
	queueLatency     searchLatencyHistogram
	executionLatency searchLatencyHistogram
}

func (health *searchRuntimeHealth) start() {
	if health != nil {
		health.started.Add(1)
	}
}

func (health *searchRuntimeHealth) finish(elapsed time.Duration, err error) {
	if health == nil {
		return
	}
	health.totalLatency.record(elapsed)
	switch {
	case err == nil:
		health.succeeded.Add(1)
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		health.canceled.Add(1)
	case errors.Is(err, ErrSearchOverloaded):
		health.overloaded.Add(1)
	default:
		health.failed.Add(1)
	}
}

func (health *searchRuntimeHealth) waitStarted() {
	if health == nil {
		return
	}
	current := health.waiting.Add(1)
	updateSearchMaximum(&health.maxWaiting, current)
}

func (health *searchRuntimeHealth) waitFinished(elapsed time.Duration) {
	if health == nil {
		return
	}
	health.waiting.Add(-1)
	health.queueLatency.record(elapsed)
}

func (health *searchRuntimeHealth) executionStarted() {
	if health == nil {
		return
	}
	current := health.active.Add(1)
	updateSearchMaximum(&health.maxActive, current)
}

func (health *searchRuntimeHealth) executionFinished(elapsed time.Duration) {
	if health == nil {
		return
	}
	health.active.Add(-1)
	health.executionLatency.record(elapsed)
}

func (health *searchRuntimeHealth) snapshot() SearchRuntimeStats {
	if health == nil {
		return SearchRuntimeStats{}
	}
	succeeded := health.succeeded.Load()
	failed := health.failed.Load()
	canceled := health.canceled.Load()
	overloaded := health.overloaded.Load()
	return SearchRuntimeStats{
		Started:          health.started.Load(),
		Completed:        succeeded + failed + canceled + overloaded,
		Succeeded:        succeeded,
		Failed:           failed,
		Canceled:         canceled,
		Overloaded:       overloaded,
		Waiting:          health.waiting.Load(),
		MaxWaiting:       health.maxWaiting.Load(),
		Active:           health.active.Load(),
		MaxActive:        health.maxActive.Load(),
		TotalLatency:     health.totalLatency.snapshot(),
		QueueLatency:     health.queueLatency.snapshot(),
		ExecutionLatency: health.executionLatency.snapshot(),
	}
}

func (histogram *searchLatencyHistogram) record(elapsed time.Duration) {
	if histogram == nil {
		return
	}
	if elapsed < 0 {
		elapsed = 0
	}
	nanoseconds := uint64(elapsed)
	histogram.count.Add(1)
	histogram.total.Add(nanoseconds)
	updateSearchMaximumUint(&histogram.maximum, nanoseconds)
	for index, upper := range searchLatencyBounds {
		if elapsed <= upper {
			histogram.buckets[index].Add(1)
			return
		}
	}
	histogram.overflow.Add(1)
}

func (histogram *searchLatencyHistogram) snapshot() SearchLatencyStats {
	stats := SearchLatencyStats{
		Count:            histogram.count.Load(),
		TotalNanoseconds: histogram.total.Load(),
		MaxNanoseconds:   histogram.maximum.Load(),
		Buckets:          make([]SearchLatencyBucket, searchLatencyBucketCount),
		Overflow:         histogram.overflow.Load(),
	}
	for index, upper := range searchLatencyBounds {
		stats.Buckets[index] = SearchLatencyBucket{
			UpperBoundMicroseconds: uint64(upper / time.Microsecond),
			Count:                  histogram.buckets[index].Load(),
		}
	}
	return stats
}

func updateSearchMaximum(target *atomic.Int64, candidate int64) {
	for current := target.Load(); candidate > current; current = target.Load() {
		if target.CompareAndSwap(current, candidate) {
			return
		}
	}
}

func updateSearchMaximumUint(target *atomic.Uint64, candidate uint64) {
	for current := target.Load(); candidate > current; current = target.Load() {
		if target.CompareAndSwap(current, candidate) {
			return
		}
	}
}
