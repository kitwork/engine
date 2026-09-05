package search

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// Count counts every live implicit-AND text match in this immutable snapshot.
// It does not rank, retain hits, or apply SearchOptions/Top-K limits. When accept
// is non-nil, identifiers are passed one at a time for an additional predicate.
// Without accept or an identifier prefix, no document identifiers are read.
// Phrase, prefix-term, and OR queries are deliberately not supported here.
func (index *Index) Count(ctx context.Context, query MatchQuery, accept func(string) (bool, error)) (uint64, error) {
	if err := index.ensureOpen(); err != nil {
		return 0, err
	}
	if err := validateIdentifierPrefix(query); err != nil {
		return 0, err
	}
	if query.Phrase != "" || query.Prefix != "" || query.Operator != QueryAll {
		return 0, fmt.Errorf("search: Count supports implicit-AND text queries only")
	}
	if query.Field != "" {
		if len(query.Fields) != 0 {
			return 0, fmt.Errorf("search: match query cannot set both Field and Fields")
		}
		query.Fields, query.Field = []string{query.Field}, ""
	}
	prepared, err := prepareMultiMatchQuery(ctx, index.schema, query, SearchOptions{})
	if err != nil {
		return 0, err
	}
	if len(prepared.terms) == 0 {
		return 0, nil
	}
	// Approximate frequencies only choose traversal order; exact BM25 union
	// frequencies are unnecessary when neither scoring nor pruning by score.
	exact := make([]bool, len(prepared.terms))
	var count uint64
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		records, _, complete, err := segment.prepareMultiTermRecords(ctx, prepared, index.deletions[position], exact)
		if err != nil {
			return 0, err
		}
		if !complete {
			continue
		}
		n, err := segment.countMatches(ctx, prepared, records, index.deletions[position], accept)
		if err != nil {
			return 0, err
		}
		count += n
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return count, nil
}

func (segment *Segment) countMatches(ctx context.Context, prepared preparedMultiMatchQuery, records []multiTermRecords, deleted *deletedDocuments, accept func(string) (bool, error)) (uint64, error) {
	sort.Slice(records, func(i, j int) bool { return records[i].documentFrequency < records[j].documentFrequency })
	unions := make([]multiPostingUnion, len(records))
	for i, record := range records {
		union, err := newMultiPostingUnion(ctx, segment, record.fields)
		if err != nil {
			return 0, err
		}
		unions[i] = union
	}
	var identifier documentIdentifierPrefixReader
	identifier.reset(ctx, segment)
	var count uint64
	target := uint32(0)
	for steps := uint64(0); ; steps++ {
		if steps&255 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		more, err := unions[0].Advance(target)
		if err != nil {
			return 0, err
		}
		if !more {
			return count, nil
		}
		document := unions[0].document
		matched := true
		for i := 1; i < len(unions); i++ {
			more, err := unions[i].Advance(document)
			if err != nil {
				return 0, err
			}
			if !more {
				return count, nil
			}
			if unions[i].document != document {
				target, matched = unions[i].document, false
				break
			}
		}
		if !matched {
			continue
		}
		matched = !deleted.Contains(document)
		if matched && prepared.identifierPrefix != "" {
			matched, err = identifier.hasPrefix(document, prepared.identifierPrefix)
			if err != nil {
				return 0, err
			}
		}
		if matched && accept != nil {
			id, err := segment.documentIdentifier(document)
			if err != nil {
				return 0, err
			}
			matched, err = accept(id)
			if err != nil {
				return 0, err
			}
		}
		if matched {
			count++
		}
		if document == math.MaxUint32 {
			return count, nil
		}
		target = document + 1
	}
}
