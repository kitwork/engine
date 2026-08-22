package search

import (
	"container/heap"
	"context"
	"sort"
)

// Search executes one implicit-AND BM25 query across every committed segment.
// Document frequency and average field length are computed for the complete
// snapshot, so scores remain comparable across segments.
func (index *Index) Search(ctx context.Context, query MatchQuery, options SearchOptions) ([]Hit, error) {
	if err := index.ensureOpen(); err != nil {
		return nil, err
	}
	if query.Phrase != "" {
		prepared, err := preparePhraseQuery(ctx, index.schema, query, options)
		if err != nil {
			return nil, err
		}
		if index.documents == 0 {
			return nil, nil
		}
		return index.searchPhrase(ctx, prepared)
	}
	if query.Prefix != "" {
		prepared, err := preparePrefixQuery(ctx, index.schema, query, options)
		if err != nil {
			return nil, err
		}
		if index.documents == 0 {
			return nil, nil
		}
		return index.searchPrefix(ctx, prepared)
	}
	if query.Operator == QueryAny {
		if len(query.Fields) != 0 {
			prepared, err := prepareMultiMatchQuery(ctx, index.schema, query, options)
			if err != nil {
				return nil, err
			}
			if len(prepared.terms) == 0 || index.documents == 0 {
				return nil, nil
			}
			if len(prepared.terms) == 1 {
				return index.searchMultiMatch(ctx, prepared)
			}
			return index.searchMultiMatchAny(ctx, prepared)
		}
		prepared, err := prepareMatchQuery(ctx, index.schema, query, options)
		if err != nil {
			return nil, err
		}
		if len(prepared.terms) == 0 || index.documents == 0 {
			return nil, nil
		}
		if len(prepared.terms) == 1 {
			return index.searchMatch(ctx, prepared)
		}
		return index.searchMatchAny(ctx, prepared)
	}
	if len(query.Fields) != 0 {
		prepared, err := prepareMultiMatchQuery(ctx, index.schema, query, options)
		if err != nil {
			return nil, err
		}
		if len(prepared.terms) == 0 || index.documents == 0 {
			return nil, nil
		}
		return index.searchMultiMatch(ctx, prepared)
	}
	prepared, err := prepareMatchQuery(ctx, index.schema, query, options)
	if err != nil {
		return nil, err
	}
	if len(prepared.terms) == 0 || index.documents == 0 {
		return nil, nil
	}
	return index.searchMatch(ctx, prepared)
}

func (index *Index) searchMatch(ctx context.Context, prepared preparedMatchQuery) ([]Hit, error) {
	recordsBySegment := make([][]termRecord, len(index.segments))
	documentFrequencies := make([]uint64, len(prepared.terms))
	var dictionaryBuffer []byte
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		records := make([]termRecord, len(prepared.terms))
		allFound := true
		for termIndex, term := range prepared.terms {
			record, found, err := segment.lookupTermBuffered(prepared.fieldID, term, &dictionaryBuffer)
			if err != nil {
				return nil, err
			}
			if !found {
				allFound = false
				continue
			}
			frequency, err := liveDocumentFrequency(ctx, segment, record, index.deletions[position])
			if err != nil {
				return nil, err
			}
			if frequency == 0 {
				allFound = false
				continue
			}
			records[termIndex] = record
			documentFrequencies[termIndex] += uint64(frequency)
		}
		if allFound {
			recordsBySegment[position] = records
		}
	}

	idfs := make([]float64, len(documentFrequencies))
	for termIndex, frequency := range documentFrequencies {
		if frequency == 0 {
			return nil, nil
		}
		if frequency > index.documents {
			return nil, corruptIndexf("term %q document frequency exceeds the index", prepared.terms[termIndex])
		}
		idfs[termIndex] = inverseDocumentFrequency(float64(index.documents), float64(frequency))
	}
	averageLength := float64(index.fieldStats[prepared.fieldID]) / float64(index.documents)
	if averageLength <= 0 {
		return nil, nil
	}

	results := make(indexCandidateHeap, 0, prepared.options.Limit)
	var segmentScratch segmentSearchScratch
	for position, records := range recordsBySegment {
		if records == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		threshold := scoreThreshold{}
		if len(results) == prepared.options.Limit {
			threshold = scoreThreshold{
				full: true, score: results[0].score, ordinal: results[0].ordinal,
			}
		}
		candidates, err := index.segments[position].searchCandidatesWithScratch(
			ctx, prepared, records, idfs, averageLength, index.deletions[position], &segmentScratch,
			index.bases[position], threshold,
		)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			collectIndexCandidate(&results, indexCandidate{
				segment: position, document: candidate.document, score: candidate.score,
				ordinal: index.bases[position] + uint64(candidate.document),
			}, prepared.options.Limit)
		}
	}

	ordered := append(indexCandidateHeap(nil), results...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		return ordered[i].ordinal < ordered[j].ordinal
	})
	hits := make([]Hit, len(ordered))
	for position, candidate := range ordered {
		identifier, err := index.segments[candidate.segment].documentIdentifier(candidate.document)
		if err != nil {
			return nil, err
		}
		hits[position] = Hit{
			ID: identifier, Score: candidate.score, InternalID: candidate.document,
			Ordinal: candidate.ordinal,
		}
	}
	return hits, nil
}

func liveDocumentFrequency(
	ctx context.Context,
	segment *Segment,
	record termRecord,
	deleted *deletedDocuments,
) (uint32, error) {
	if deleted == nil || deleted.Count() == 0 {
		return record.documentFreq, nil
	}
	iterator, err := newPostingIterator(
		segment.file, segment.header.version, record, segment.header.documentN,
		segment.header.sections[sectionPostings], false,
	)
	if err != nil {
		return 0, err
	}
	frequency := record.documentFreq
	for position, document := range deleted.ids {
		if position&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		more, err := iterator.Advance(document)
		if err != nil {
			return 0, err
		}
		if !more {
			break
		}
		current, _, _ := iterator.Current()
		if current == document {
			frequency--
		}
	}
	return frequency, nil
}

type indexCandidate struct {
	segment  int
	document uint32
	score    float64
	ordinal  uint64
}

// indexCandidateHeap keeps the worst selected candidate at the root.
type indexCandidateHeap []indexCandidate

func (items indexCandidateHeap) Len() int { return len(items) }
func (items indexCandidateHeap) Less(i, j int) bool {
	if items[i].score != items[j].score {
		return items[i].score < items[j].score
	}
	return items[i].ordinal > items[j].ordinal
}
func (items indexCandidateHeap) Swap(i, j int)   { items[i], items[j] = items[j], items[i] }
func (items *indexCandidateHeap) Push(value any) { *items = append(*items, value.(indexCandidate)) }
func (items *indexCandidateHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func collectIndexCandidate(results *indexCandidateHeap, candidate indexCandidate, limit int) {
	if results.Len() < limit {
		heap.Push(results, candidate)
		return
	}
	worst := (*results)[0]
	if candidate.score > worst.score || (candidate.score == worst.score && candidate.ordinal < worst.ordinal) {
		(*results)[0] = candidate
		heap.Fix(results, 0)
	}
}
