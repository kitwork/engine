//go:build searchwork

package search

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func workTestIterator(t *testing.T, ctx context.Context) (*postingIterator, int) {
	t.Helper()
	postings := make([]posting, 385)
	for i := range postings {
		postings[i] = posting{document: uint32(i), frequency: 1, norm: 1}
	}
	var encoded bytes.Buffer
	info, err := writePostingList(&encoded, postings, segmentVersion, false)
	if err != nil {
		t.Fatal(err)
	}
	iterator, err := newPostingIterator(ctx, bytes.NewReader(encoded.Bytes()), segmentVersion,
		termRecord{documentFreq: 385, postingsLength: info.bytes, maximumTF: 1, minimumNorm: 1},
		385, sectionDescriptor{length: info.bytes}, false)
	if err != nil {
		t.Fatal(err)
	}
	return iterator, encoded.Len()
}

func TestPostingWorkExactDecodeAndReadAhead(t *testing.T) {
	ctx, observer := ObserveWork(context.Background())
	iterator, size := workTestIterator(t, ctx)
	for {
		more, err := iterator.Next()
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	want := WorkCounts{PostingLists: 1, NextCalls: 386, Headers: 4, DecodedBlocks: 4,
		DecodedPostings: 385, DecodedPayloadBytes: 770, ReadRequests: 8, RequestedBytes: uint64(size),
		ReadAheadHits: 7, ReaderCalls: 1, ReaderBytes: uint64(size), VarintWidths: [10]uint64{770}}
	if got := observer.Snapshot(); got != (WorkSnapshot{Ranking: want}) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestPostingWorkCountsTargetSkipWithoutPretendingZeroIO(t *testing.T) {
	ctx, observer := ObserveWork(context.Background())
	iterator, size := workTestIterator(t, ctx)
	for _, target := range []uint32{257, 257, 258, 1000} {
		_, err := iterator.Advance(target)
		if err != nil {
			t.Fatal(err)
		}
	}
	want := WorkCounts{PostingLists: 1, AdvanceCalls: 4, AdvanceCurrent: 1, AdvanceInBlock: 1,
		Headers: 4, DecodedBlocks: 1, DecodedPostings: 128, DecodedPayloadBytes: 256,
		SkippedTargetBlocks: 3, ReadRequests: 5, RequestedBytes: 384, ReadAheadHits: 4,
		ReaderCalls: 1, ReaderBytes: uint64(size), VarintWidths: [10]uint64{256}}
	if got := observer.Snapshot(); got != (WorkSnapshot{Ranking: want}) {
		t.Fatalf("got=%+v want=%+v", got, want)
	}
}

func TestPostingWorkResetDoesNotLeakCollector(t *testing.T) {
	ctx, observer := ObserveWork(context.Background())
	iterator, _ := workTestIterator(t, ctx)
	if _, err := iterator.Next(); err != nil {
		t.Fatal(err)
	}
	before := observer.Snapshot()
	if err := resetPostingIterator(iterator, context.Background(), iterator.file, iterator.version,
		termRecord{documentFreq: 385, postingsLength: iterator.end, maximumTF: 1, minimumNorm: 1},
		385, sectionDescriptor{length: iterator.end}, false); err != nil {
		t.Fatal(err)
	}
	if _, err := iterator.Next(); err != nil {
		t.Fatal(err)
	}
	if got := observer.Snapshot(); got != before {
		t.Fatal("reused iterator leaked work to old observer")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if frequencyWorkContext(canceled).Err() != context.Canceled {
		t.Fatal("phase change lost cancellation")
	}
}

func TestPostingWorkCountsSkippedAndRetainedPositions(t *testing.T) {
	var encoded bytes.Buffer
	info, err := writePostingList(&encoded, []posting{{document: 0, frequency: 3, norm: 3, positions: []uint32{0, 128, 16384}}}, segmentVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, retain := range []bool{false, true} {
		ctx, observer := ObserveWork(context.Background())
		iterator, err := newPostingIterator(ctx, bytes.NewReader(encoded.Bytes()), segmentVersion,
			termRecord{documentFreq: 1, postingsLength: info.bytes, maximumTF: 3, minimumNorm: 3},
			1, sectionDescriptor{length: info.bytes}, retain)
		if err != nil {
			t.Fatal(err)
		}
		if more, err := iterator.Next(); err != nil || !more {
			t.Fatalf("more=%t err=%v", more, err)
		}
		counts := observer.Snapshot().Ranking
		if counts.PositionVarints != 3 || counts.VarintWidths != [10]uint64{3, 2} || counts.DecodedPayloadBytes != 7 {
			t.Fatalf("retain=%t counts=%+v", retain, counts)
		}
	}
}

func TestMultiWorkPhaseIsolationAndConcurrentObservation(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 256 {
		if err := writer.Add(ctx, Document{ID: fmt.Sprintf("p/%03d", i), Fields: map[string]string{"title": "alpha", "body": "beta"}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	query := MatchQuery{Fields: []string{"title", "body"}, Text: "alpha beta"}
	coldCtx, cold := ObserveWork(ctx)
	want, err := index.Search(coldCtx, query, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if stats := cold.Snapshot(); stats.Frequency.DecodedPostings != 512 || stats.Ranking.DecodedPostings == 0 {
		t.Fatalf("cold=%+v", stats)
	}
	warmCtx, warm := ObserveWork(ctx)
	got, err := index.Search(warmCtx, query, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	assertOrderedHitsEqual(t, got, want)
	unit := warm.Snapshot()
	if unit.Frequency != (WorkCounts{}) || unit.Ranking.MultiMatches == 0 {
		t.Fatalf("warm=%+v", unit)
	}
	childCtx, child := ObserveWork(coldCtx)
	if _, err := index.Search(childCtx, query, SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	if child.Snapshot() != unit {
		t.Fatal("nested observer inherited parent counters")
	}
	coldBefore := cold.Snapshot()
	sharedCtx, shared := ObserveWork(ctx)
	const workers = 8
	var group sync.WaitGroup
	errorsFound := make(chan error, workers)
	for range workers {
		group.Go(func() {
			got, err := index.Search(sharedCtx, query, SearchOptions{})
			if err != nil {
				errorsFound <- err
			} else if !slices.Equal(got, want) {
				errorsFound <- fmt.Errorf("observer changed results")
			}
			_ = shared.Snapshot()
		})
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Error(err)
	}
	sum := shared.Snapshot()
	if sum.Frequency != (WorkCounts{}) || sum.Ranking.DecodedPostings != unit.Ranking.DecodedPostings*workers ||
		sum.Ranking.AdvanceCalls != unit.Ranking.AdvanceCalls*workers {
		t.Fatalf("aggregate=%+v unit=%+v", sum, unit)
	}
	if cold.Snapshot() != coldBefore {
		t.Fatal("independent query changed another observer")
	}
}
