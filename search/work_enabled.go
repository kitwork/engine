//go:build searchwork

package search

import (
	"context"
	"sync/atomic"
)

// WorkCounts describe posting-iterator work, not physical disk traffic. Reads
// are requests to the iterator's ReaderAt, which may itself be cached. Varints
// count successful integer decodes, including positions read but not retained.
// Multi* counters cover the multi-field AND ranker, not every query operator.
type WorkCounts struct {
	PostingLists, AdvanceCalls, AdvanceCurrent, AdvanceInBlock, NextCalls uint64
	Headers, DecodedBlocks, DecodedPostings, DecodedPayloadBytes          uint64
	SkippedTargetBlocks, SkippedScoreBlocks                               uint64
	ReadRequests, RequestedBytes, ReadAheadHits, ReaderCalls, ReaderBytes uint64
	UnionAdvances, MultiCandidates, MultiTermScores, MultiFieldScores     uint64
	MultiMismatches, MultiMismatchDistance, MultiMatches                  uint64
	MultiRankRejected, MultiPrefixRejected, MultiCollected                uint64
	PositionVarints                                                       uint64
	VarintWidths                                                          [10]uint64
}

// WorkSnapshot separates exact cross-field document-frequency traversal from
// all other posting work. Snapshot during an active query is not transactional;
// take the final snapshot after Execute/Search returns. No identifiers are kept.
type WorkSnapshot struct {
	Ranking   WorkCounts
	Frequency WorkCounts
}

type workCounters [workEventCount]atomic.Uint64

// WorkObserver is a bounded, concurrency-safe diagnostic collector. Its API
// exists only with -tags searchwork; timings from that build are not benchmarks.
type WorkObserver struct {
	ranking   workCounters
	frequency workCounters
}

type workContextKey struct{}
type postingWork struct{ counters *workCounters }

// ObserveWork attaches a new collector without changing cancellation/deadlines.
// Child contexts inherit it; a second ObserveWork creates an isolated collector.
func ObserveWork(ctx context.Context) (context.Context, *WorkObserver) {
	observer := &WorkObserver{}
	if ctx == nil {
		return nil, observer
	}
	ctx = context.WithValue(ctx, frequencyWorkKey{}, observer)
	return context.WithValue(ctx, workContextKey{}, postingWork{&observer.ranking}), observer
}

func postingWorkFromContext(ctx context.Context) postingWork {
	if ctx == nil {
		return postingWork{}
	}
	work, _ := ctx.Value(workContextKey{}).(postingWork)
	return work
}

type frequencyWorkKey struct{}

func frequencyWorkContext(ctx context.Context) context.Context {
	if ctx == nil {
		return nil
	}
	// The observer is attached separately so changing phase never mutates a
	// shared context/collector used by another query goroutine.
	observer, _ := ctx.Value(frequencyWorkKey{}).(*WorkObserver)
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, workContextKey{}, postingWork{&observer.frequency})
}

func (work postingWork) add(event workEvent, value uint64) {
	if work.counters != nil {
		work.counters[event].Add(value)
	}
}

func (work postingWork) varint(width int, position bool) {
	if work.counters == nil || width <= 0 || width > 10 {
		return
	}
	work.add(workVarintWidth1+workEvent(width-1), 1)
	if position {
		work.add(workPositionVarints, 1)
	}
}

func (observer *WorkObserver) Snapshot() WorkSnapshot {
	if observer == nil {
		return WorkSnapshot{}
	}
	return WorkSnapshot{Ranking: snapshotWork(&observer.ranking), Frequency: snapshotWork(&observer.frequency)}
}

func snapshotWork(c *workCounters) WorkCounts {
	load := func(event workEvent) uint64 { return c[event].Load() }
	result := WorkCounts{
		PostingLists: load(workPostingLists), AdvanceCalls: load(workAdvanceCalls),
		AdvanceCurrent: load(workAdvanceCurrent), AdvanceInBlock: load(workAdvanceInBlock), NextCalls: load(workNextCalls),
		Headers: load(workHeaders), DecodedBlocks: load(workDecodedBlocks), DecodedPostings: load(workDecodedPostings),
		DecodedPayloadBytes: load(workDecodedPayloadBytes), SkippedTargetBlocks: load(workSkippedTargetBlocks), SkippedScoreBlocks: load(workSkippedScoreBlocks),
		ReadRequests: load(workReadRequests), RequestedBytes: load(workRequestedBytes), ReadAheadHits: load(workReadAheadHits),
		ReaderCalls: load(workReaderCalls), ReaderBytes: load(workReaderBytes), UnionAdvances: load(workUnionAdvances),
		MultiCandidates: load(workMultiCandidates), MultiTermScores: load(workMultiTermScores), MultiFieldScores: load(workMultiFieldScores),
		MultiMismatches: load(workMultiMismatches), MultiMismatchDistance: load(workMultiMismatchDistance), MultiMatches: load(workMultiMatches),
		MultiRankRejected: load(workMultiRankRejected), MultiPrefixRejected: load(workMultiPrefixRejected), MultiCollected: load(workMultiCollected),
		PositionVarints: load(workPositionVarints),
	}
	for i := range result.VarintWidths {
		result.VarintWidths[i] = load(workVarintWidth1 + workEvent(i))
	}
	return result
}
