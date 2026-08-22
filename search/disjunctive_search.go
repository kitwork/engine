package search

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
)

type disjunctiveClause struct {
	fieldID       uint16
	field         Field
	record        termRecord
	idf           float64
	averageLength float64
}

type disjunctiveCursor struct {
	iterator      postingIterator
	fieldID       uint16
	field         Field
	idf           float64
	averageLength float64
	norms         []uint32
	document      uint32
	frequency     uint32
	order         int
}

type disjunctiveCursorHeap []*disjunctiveCursor

func (items disjunctiveCursorHeap) Len() int { return len(items) }
func (items disjunctiveCursorHeap) Less(i, j int) bool {
	if items[i].document != items[j].document {
		return items[i].document < items[j].document
	}
	return items[i].order < items[j].order
}
func (items disjunctiveCursorHeap) Swap(i, j int) { items[i], items[j] = items[j], items[i] }
func (items *disjunctiveCursorHeap) Push(value any) {
	*items = append(*items, value.(*disjunctiveCursor))
}
func (items *disjunctiveCursorHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func newDisjunctiveCursor(
	segment *Segment,
	clause disjunctiveClause,
	order int,
) (*disjunctiveCursor, bool, error) {
	cursor := &disjunctiveCursor{
		fieldID:       clause.fieldID,
		field:         clause.field,
		idf:           clause.idf,
		averageLength: clause.averageLength,
		order:         order,
	}
	if err := resetPostingIterator(
		&cursor.iterator, segment.file, segment.header.version, clause.record,
		segment.header.documentN, segment.header.sections[sectionPostings], false,
	); err != nil {
		return nil, false, err
	}
	norms, err := segment.fieldNorms(clause.fieldID)
	if err != nil {
		return nil, false, err
	}
	cursor.norms = norms
	more, err := cursor.advanceTo(0)
	if err != nil {
		return nil, false, err
	}
	if !more {
		return nil, false, nil
	}
	return cursor, true, nil
}

func (cursor *disjunctiveCursor) advanceTo(target uint32) (bool, error) {
	more, err := cursor.iterator.Advance(target)
	if err != nil || !more {
		return more, err
	}
	document, frequency, _ := cursor.iterator.Current()
	cursor.document = document
	cursor.frequency = frequency
	return true, nil
}

func (cursor *disjunctiveCursor) score(options SearchOptions) (float64, error) {
	if int(cursor.document) >= len(cursor.norms) {
		return 0, corruptf("search: disjunctive norm document is out of range")
	}
	length := float64(cursor.norms[cursor.document])
	if length == 0 || cursor.averageLength <= 0 {
		return 0, corruptf("search: disjunctive norm statistic is invalid")
	}
	return bm25Score(
		float64(cursor.frequency), length, cursor.averageLength,
		cursor.idf, options.K1, options.B,
	) * cursor.field.Boost, nil
}

func (segment *Segment) searchDisjunctive(
	ctx context.Context,
	clauses []disjunctiveClause,
	options SearchOptions,
) ([]Hit, error) {
	if err := segment.ensureOpen(); err != nil {
		return nil, err
	}
	if ctx == nil {
		return nil, fmt.Errorf("search: query context is nil")
	}
	if len(clauses) == 0 || segment.header.documentN == 0 {
		return nil, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cursors := make(disjunctiveCursorHeap, 0, len(clauses))
	for index, clause := range clauses {
		cursor, ok, err := newDisjunctiveCursor(segment, clause, index)
		if err != nil {
			return nil, err
		}
		if ok {
			heap.Push(&cursors, cursor)
		}
	}
	if len(cursors) == 0 {
		return nil, nil
	}

	results := make(candidateHeap, 0, options.Limit)
	batch := make([]*disjunctiveCursor, 0, len(clauses))
	for steps := uint64(0); len(cursors) > 0; steps++ {
		if steps&63 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		current := cursors[0].document
		batch = batch[:0]
		for len(cursors) > 0 && cursors[0].document == current {
			cursor := heap.Pop(&cursors).(*disjunctiveCursor)
			batch = append(batch, cursor)
		}
		score := 0.0
		for _, cursor := range batch {
			clauseScore, err := cursor.score(options)
			if err != nil {
				return nil, err
			}
			score += clauseScore
		}
		collectCandidate(&results, rankedCandidate{document: current, score: score}, options.Limit)
		if current == ^uint32(0) {
			break
		}
		target := current + 1
		for _, cursor := range batch {
			more, err := cursor.advanceTo(target)
			if err != nil {
				return nil, err
			}
			if more {
				heap.Push(&cursors, cursor)
			}
		}
	}
	return segment.materializeHits(results, 0)
}

func (segment *Segment) searchMatchAny(ctx context.Context, prepared preparedMatchQuery) ([]Hit, error) {
	if segment.header.documentN == 0 {
		return nil, nil
	}
	clauses := make([]disjunctiveClause, 0, len(prepared.terms))
	var dictionaryBuffer []byte
	averageLength := float64(segment.fieldStats[prepared.fieldID]) / float64(segment.header.documentN)
	for _, term := range prepared.terms {
		record, found, err := segment.lookupTermBuffered(prepared.fieldID, term, &dictionaryBuffer)
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		clauses = append(clauses, disjunctiveClause{
			fieldID:       prepared.fieldID,
			field:         prepared.field,
			record:        record,
			idf:           inverseDocumentFrequency(float64(segment.header.documentN), float64(record.documentFreq)),
			averageLength: averageLength,
		})
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	return segment.searchDisjunctiveWAND(ctx, clauses, prepared.options)
}

func (segment *Segment) searchMultiMatchAny(ctx context.Context, prepared preparedMultiMatchQuery) ([]Hit, error) {
	if segment.header.documentN == 0 {
		return nil, nil
	}
	clauses := make([]disjunctiveClause, 0, len(prepared.fields)*len(prepared.terms))
	var dictionaryBuffer []byte
	for fieldIndex, fieldID := range prepared.fieldIDs {
		averageLength := float64(segment.fieldStats[fieldID]) / float64(segment.header.documentN)
		for _, term := range prepared.terms {
			record, found, err := segment.lookupTermBuffered(fieldID, term, &dictionaryBuffer)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			clauses = append(clauses, disjunctiveClause{
				fieldID:       fieldID,
				field:         prepared.fields[fieldIndex],
				record:        record,
				idf:           inverseDocumentFrequency(float64(segment.header.documentN), float64(record.documentFreq)),
				averageLength: averageLength,
			})
		}
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	return segment.searchDisjunctiveWAND(ctx, clauses, prepared.options)
}

func (index *Index) searchMatchAny(ctx context.Context, prepared preparedMatchQuery) ([]Hit, error) {
	if len(prepared.terms) == 0 || index.documents == 0 {
		return nil, nil
	}
	frequencies := make([]uint64, len(prepared.terms))
	var dictionaryBuffer []byte
	for segmentIndex, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for termIndex, term := range prepared.terms {
			record, found, err := segment.lookupTermBuffered(prepared.fieldID, term, &dictionaryBuffer)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			frequency, err := liveDocumentFrequency(ctx, segment, record, index.deletions[segmentIndex])
			if err != nil {
				return nil, err
			}
			frequencies[termIndex] += uint64(frequency)
		}
	}
	for termIndex, frequency := range frequencies {
		if frequency > index.documents {
			return nil, corruptIndexf(
				"term %q document frequency exceeds the index", prepared.terms[termIndex],
			)
		}
	}

	results := make([]Hit, 0, prepared.options.Limit)
	for segmentIndex, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clauses := make([]disjunctiveClause, 0, len(prepared.terms))
		var dictionaryBuffer []byte
		averageLength := float64(index.fieldStats[prepared.fieldID]) / float64(index.documents)
		for termIndex, term := range prepared.terms {
			if frequencies[termIndex] == 0 {
				continue
			}
			record, found, err := segment.lookupTermBuffered(prepared.fieldID, term, &dictionaryBuffer)
			if err != nil {
				return nil, err
			}
			if !found {
				continue
			}
			clauses = append(clauses, disjunctiveClause{
				fieldID:       prepared.fieldID,
				field:         prepared.field,
				record:        record,
				idf:           inverseDocumentFrequency(float64(index.documents), float64(frequencies[termIndex])),
				averageLength: averageLength,
			})
		}
		if len(clauses) == 0 {
			continue
		}
		hits, err := segment.searchDisjunctiveWAND(ctx, clauses, prepared.options)
		if err != nil {
			return nil, err
		}
		for hitIndex := range hits {
			hits[hitIndex].Ordinal = index.bases[segmentIndex] + uint64(hits[hitIndex].InternalID)
			results = append(results, hits[hitIndex])
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Ordinal < results[j].Ordinal
	})
	if len(results) > prepared.options.Limit {
		results = append([]Hit(nil), results[:prepared.options.Limit]...)
	}
	return results, nil
}

func (index *Index) searchMultiMatchAny(ctx context.Context, prepared preparedMultiMatchQuery) ([]Hit, error) {
	if len(prepared.terms) == 0 || index.documents == 0 {
		return nil, nil
	}
	clauseCount := len(prepared.fields) * len(prepared.terms)
	frequencies := make([]uint64, clauseCount)
	var dictionaryBuffer []byte
	for segmentIndex, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clauseIndex := 0
		for _, fieldID := range prepared.fieldIDs {
			for _, term := range prepared.terms {
				record, found, err := segment.lookupTermBuffered(fieldID, term, &dictionaryBuffer)
				if err != nil {
					return nil, err
				}
				if !found {
					clauseIndex++
					continue
				}
				frequency, err := liveDocumentFrequency(ctx, segment, record, index.deletions[segmentIndex])
				if err != nil {
					return nil, err
				}
				frequencies[clauseIndex] += uint64(frequency)
				clauseIndex++
			}
		}
	}
	for clauseIndex, frequency := range frequencies {
		if frequency > index.documents {
			termIndex := clauseIndex % len(prepared.terms)
			return nil, corruptIndexf(
				"term %q document frequency exceeds the index", prepared.terms[termIndex],
			)
		}
	}

	results := make([]Hit, 0, prepared.options.Limit)
	for segmentIndex, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clauses := make([]disjunctiveClause, 0, clauseCount)
		var dictionaryBuffer []byte
		clauseIndex := 0
		for fieldIndex, fieldID := range prepared.fieldIDs {
			averageLength := float64(index.fieldStats[fieldID]) / float64(index.documents)
			for _, term := range prepared.terms {
				if frequencies[clauseIndex] == 0 {
					clauseIndex++
					continue
				}
				record, found, err := segment.lookupTermBuffered(fieldID, term, &dictionaryBuffer)
				if err != nil {
					return nil, err
				}
				if !found {
					clauseIndex++
					continue
				}
				clauses = append(clauses, disjunctiveClause{
					fieldID:       fieldID,
					field:         prepared.fields[fieldIndex],
					record:        record,
					idf:           inverseDocumentFrequency(float64(index.documents), float64(frequencies[clauseIndex])),
					averageLength: averageLength,
				})
				clauseIndex++
			}
		}
		if len(clauses) == 0 {
			continue
		}
		hits, err := segment.searchDisjunctiveWAND(ctx, clauses, prepared.options)
		if err != nil {
			return nil, err
		}
		for hitIndex := range hits {
			hits[hitIndex].Ordinal = index.bases[segmentIndex] + uint64(hits[hitIndex].InternalID)
			results = append(results, hits[hitIndex])
		}
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].Ordinal < results[j].Ordinal
	})
	if len(results) > prepared.options.Limit {
		results = append([]Hit(nil), results[:prepared.options.Limit]...)
	}
	return results, nil
}
