package search

import (
	"context"
	"fmt"
	"sort"
)

type wandCursor struct {
	iterator      postingIterator
	fieldID       uint16
	field         Field
	idf           float64
	averageLength float64
	maximumScore  float64
	norms         []uint32
	document      uint32
	frequency     uint32
	order         int
}

type wandCursorList []*wandCursor

func (items wandCursorList) Len() int { return len(items) }
func (items wandCursorList) Less(i, j int) bool {
	if items[i].document != items[j].document {
		return items[i].document < items[j].document
	}
	if items[i].fieldID != items[j].fieldID {
		return items[i].fieldID < items[j].fieldID
	}
	return items[i].order < items[j].order
}
func (items wandCursorList) Swap(i, j int) { items[i], items[j] = items[j], items[i] }

func newWANDCursor(ctx context.Context, segment *Segment, clause disjunctiveClause, options SearchOptions, order int) (*wandCursor, bool, error) {
	cursor := &wandCursor{
		fieldID:       clause.fieldID,
		field:         clause.field,
		idf:           clause.idf,
		averageLength: clause.averageLength,
		maximumScore:  maximumTermScore(clause.record, clause.idf, clause.averageLength, options) * clause.field.Boost,
		order:         order,
	}
	if err := resetPostingIterator(
		&cursor.iterator, ctx, segment.file, segment.header.version, clause.record,
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

func (cursor *wandCursor) advanceTo(target uint32) (bool, error) {
	more, err := cursor.iterator.Advance(target)
	if err != nil || !more {
		return more, err
	}
	document, frequency, _ := cursor.iterator.Current()
	cursor.document = document
	cursor.frequency = frequency
	return true, nil
}

func (cursor *wandCursor) score(options SearchOptions) (float64, error) {
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

func (segment *Segment) searchDisjunctiveWAND(
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

	cursors := make(wandCursorList, 0, len(clauses))
	for index, clause := range clauses {
		cursor, ok, err := newWANDCursor(ctx, segment, clause, options, index)
		if err != nil {
			return nil, err
		}
		if ok {
			cursors = append(cursors, cursor)
		}
	}
	if len(cursors) == 0 {
		return nil, nil
	}

	results := make(candidateHeap, 0, options.Limit)
	for steps := uint64(0); len(cursors) > 0; steps++ {
		if steps&63 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		sort.Sort(cursors)
		threshold := 0.0
		if len(results) == options.Limit {
			threshold = results[0].score
		}
		cumulative := 0.0
		pivot := 0
		for index, cursor := range cursors {
			cumulative += cursor.maximumScore
			pivot = index
			if cumulative >= threshold {
				break
			}
		}
		if len(results) == options.Limit && cumulative < threshold {
			break
		}

		pivotDoc := cursors[pivot].document
		if cursors[0].document < pivotDoc {
			cursor := cursors[0]
			more, err := cursor.advanceTo(pivotDoc)
			if err != nil {
				return nil, err
			}
			if !more {
				cursors = cursors[1:]
			}
			continue
		}

		candidateDoc := cursors[0].document
		score := 0.0
		next := cursors[:0]
		for _, cursor := range cursors {
			if cursor.document != candidateDoc {
				next = append(next, cursor)
				continue
			}
			clauseScore, err := cursor.score(options)
			if err != nil {
				return nil, err
			}
			score += clauseScore
			more, err := cursor.advanceTo(candidateDoc + 1)
			if err != nil {
				return nil, err
			}
			if more {
				next = append(next, cursor)
			}
		}
		collectCandidate(&results, rankedCandidate{document: candidateDoc, score: score}, options.Limit)
		cursors = next
	}
	return segment.materializeHits(results, 0)
}
