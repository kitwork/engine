package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"unicode/utf8"
)

const (
	maximumMultiFrequencyCacheEntries = 4096
	maximumMultiFrequencyCacheTerm    = 512
)

type preparedMultiMatchQuery struct {
	fieldIDs         []uint16
	fields           []Field
	terms            []string
	identifierPrefix string
	options          SearchOptions
}

type multiFieldTermRecord struct {
	fieldIndex int
	fieldID    uint16
	record     termRecord
}

type multiTermRecords struct {
	fields            []multiFieldTermRecord
	documentFrequency uint32
}

type multiFrequencyCache struct {
	mu      sync.Mutex
	entries map[string]uint64
	order   []string
	next    int
}

func prepareMultiMatchQuery(
	ctx context.Context,
	schema Schema,
	query MatchQuery,
	options SearchOptions,
) (preparedMultiMatchQuery, error) {
	if ctx == nil {
		return preparedMultiMatchQuery{}, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return preparedMultiMatchQuery{}, err
	}
	if query.Field != "" {
		return preparedMultiMatchQuery{}, fmt.Errorf("search: match query cannot set both Field and Fields")
	}
	if len(query.Fields) == 0 || len(query.Fields) > maximumQueryFields {
		return preparedMultiMatchQuery{}, fmt.Errorf(
			"search: multi-field query requires between 1 and %d fields", maximumQueryFields,
		)
	}
	if !utf8.ValidString(query.Text) || len(query.Text) > maximumQueryBytes {
		return preparedMultiMatchQuery{}, fmt.Errorf("search: query is invalid or exceeds %d bytes", maximumQueryBytes)
	}
	normalized, err := normalizeSearchOptions(options)
	if err != nil {
		return preparedMultiMatchQuery{}, err
	}

	prepared := preparedMultiMatchQuery{
		fieldIDs:         make([]uint16, 0, len(query.Fields)),
		fields:           make([]Field, 0, len(query.Fields)),
		identifierPrefix: query.IdentifierPrefix,
		options:          normalized,
	}
	seen := make(map[string]struct{}, len(query.Fields))
	analyzerID := ""
	for _, name := range query.Fields {
		if _, duplicate := seen[name]; duplicate {
			return preparedMultiMatchQuery{}, fmt.Errorf("search: duplicate query field %q", name)
		}
		seen[name] = struct{}{}
		fieldID, field, exists := schema.field(name)
		if !exists {
			return preparedMultiMatchQuery{}, fmt.Errorf("search: unknown field %q", name)
		}
		if analyzerID == "" {
			analyzerID = field.Analyzer.Identifier()
		} else if field.Analyzer.Identifier() != analyzerID {
			return preparedMultiMatchQuery{}, fmt.Errorf(
				"search: multi-field query fields must use the same analyzer",
			)
		}
		prepared.fieldIDs = append(prepared.fieldIDs, fieldID)
		prepared.fields = append(prepared.fields, field)
	}
	prepared.terms, err = analyzeQuery(ctx, prepared.fields[0].Analyzer, query.Text)
	if err != nil {
		return preparedMultiMatchQuery{}, err
	}
	return prepared, nil
}

func (segment *Segment) searchMultiMatch(
	ctx context.Context,
	prepared preparedMultiMatchQuery,
) ([]Hit, error) {
	records, frequencies, complete, err := segment.prepareMultiTermRecords(ctx, prepared, nil, nil)
	if err != nil {
		return nil, err
	}
	if !complete {
		return nil, nil
	}
	idfs := make([]float64, len(frequencies))
	for position, frequency := range frequencies {
		idfs[position] = inverseDocumentFrequency(
			float64(segment.header.documentN), float64(frequency),
		)
	}
	averages, err := segment.multiFieldAverageLengths(prepared, uint64(segment.header.documentN), nil)
	if err != nil {
		return nil, err
	}
	candidates, err := segment.searchMultiCandidates(
		ctx, prepared, records, idfs, averages, nil, 0, scoreThreshold{},
	)
	if err != nil {
		return nil, err
	}
	return segment.materializeHits(candidates, 0)
}

func (index *Index) searchMultiMatch(
	ctx context.Context,
	prepared preparedMultiMatchQuery,
) ([]Hit, error) {
	recordsBySegment := make([][]multiTermRecords, len(index.segments))
	documentFrequencies := make([]uint64, len(prepared.terms))
	exactFrequencies := make([]bool, len(prepared.terms))
	frequencyKeys := make([]string, len(prepared.terms))
	for term, value := range prepared.terms {
		key := multiFrequencyKey(prepared.fieldIDs, value)
		frequencyKeys[term] = key
		frequency, cached := index.multiFreq.get(key)
		if cached {
			documentFrequencies[term] = frequency
		} else {
			exactFrequencies[term] = true
		}
	}
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		records, frequencies, complete, err := segment.prepareMultiTermRecords(
			ctx, prepared, index.deletions[position], exactFrequencies,
		)
		if err != nil {
			return nil, err
		}
		for term, frequency := range frequencies {
			if exactFrequencies[term] {
				documentFrequencies[term] += uint64(frequency)
			}
		}
		if complete {
			recordsBySegment[position] = records
		}
	}

	idfs := make([]float64, len(documentFrequencies))
	for term, frequency := range documentFrequencies {
		if frequency == 0 {
			return nil, nil
		}
		if frequency > index.documents {
			return nil, corruptIndexf(
				"multi-field term %q document frequency exceeds the index", prepared.terms[term],
			)
		}
		idfs[term] = inverseDocumentFrequency(float64(index.documents), float64(frequency))
		if exactFrequencies[term] {
			index.multiFreq.put(frequencyKeys[term], frequency)
		}
	}
	averages, err := index.segments[0].multiFieldAverageLengths(prepared, index.documents, index.fieldStats)
	if err != nil {
		return nil, err
	}

	results := make(indexCandidateHeap, 0, prepared.options.Limit)
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
		candidates, err := index.segments[position].searchMultiCandidates(
			ctx, prepared, records, idfs, averages, index.deletions[position],
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
	return index.materializeMultiHits(results)
}

func (segment *Segment) prepareMultiTermRecords(
	ctx context.Context,
	prepared preparedMultiMatchQuery,
	deleted *deletedDocuments,
	exactFrequencies []bool,
) ([]multiTermRecords, []uint32, bool, error) {
	records := make([]multiTermRecords, len(prepared.terms))
	frequencies := make([]uint32, len(prepared.terms))
	complete := true
	var dictionaryBuffer []byte
	for termIndex, term := range prepared.terms {
		fields := make([]multiFieldTermRecord, 0, len(prepared.fieldIDs))
		for fieldIndex, fieldID := range prepared.fieldIDs {
			record, found, err := segment.lookupTermBuffered(fieldID, term, &dictionaryBuffer)
			if err != nil {
				return nil, nil, false, err
			}
			if found {
				fields = append(fields, multiFieldTermRecord{
					fieldIndex: fieldIndex, fieldID: fieldID, record: record,
				})
			}
		}
		if len(fields) == 0 {
			complete = false
			continue
		}
		frequency := approximateMultiDocumentFrequency(fields, segment.header.documentN)
		if exactFrequencies == nil || exactFrequencies[termIndex] {
			exactFrequency, err := segment.liveMultiDocumentFrequency(ctx, fields, deleted)
			if err != nil {
				return nil, nil, false, err
			}
			frequency = exactFrequency
		}
		if frequency == 0 {
			complete = false
			continue
		}
		records[termIndex] = multiTermRecords{
			fields: fields, documentFrequency: frequency,
		}
		frequencies[termIndex] = frequency
	}
	return records, frequencies, complete, nil
}

func approximateMultiDocumentFrequency(records []multiFieldTermRecord, documents uint32) uint32 {
	frequency := uint64(0)
	for _, record := range records {
		frequency += uint64(record.record.documentFreq)
		if frequency >= uint64(documents) {
			return documents
		}
	}
	return uint32(frequency)
}

func (segment *Segment) multiFieldAverageLengths(
	prepared preparedMultiMatchQuery,
	documents uint64,
	fieldTotals []uint64,
) ([]float64, error) {
	if documents == 0 {
		return nil, nil
	}
	averages := make([]float64, len(prepared.fieldIDs))
	for position, fieldID := range prepared.fieldIDs {
		var total uint64
		if fieldTotals == nil {
			if int(fieldID) >= len(segment.fieldStats) {
				return nil, corruptf("multi-field statistic is out of range")
			}
			total = segment.fieldStats[fieldID]
		} else {
			if int(fieldID) >= len(fieldTotals) {
				return nil, corruptIndexf("multi-field statistic is out of range")
			}
			total = fieldTotals[fieldID]
		}
		averages[position] = float64(total) / float64(documents)
	}
	return averages, nil
}

type multiPostingUnion struct {
	iterators []postingIterator
	document  uint32
	current   bool
}

func newMultiPostingUnion(segment *Segment, records []multiFieldTermRecord) (multiPostingUnion, error) {
	union := multiPostingUnion{iterators: make([]postingIterator, len(records))}
	for position, record := range records {
		if err := resetPostingIterator(
			&union.iterators[position], segment.file, segment.header.version, record.record,
			segment.header.documentN, segment.header.sections[sectionPostings], false,
		); err != nil {
			return multiPostingUnion{}, err
		}
	}
	return union, nil
}

func (union *multiPostingUnion) Advance(target uint32) (bool, error) {
	minimum := uint32(math.MaxUint32)
	found := false
	for position := range union.iterators {
		more, err := union.iterators[position].Advance(target)
		if err != nil {
			return false, err
		}
		if !more {
			continue
		}
		document, _, _ := union.iterators[position].Current()
		if !found || document < minimum {
			minimum = document
			found = true
		}
	}
	union.current = found
	if found {
		union.document = minimum
	}
	return found, nil
}

func (segment *Segment) liveMultiDocumentFrequency(
	ctx context.Context,
	records []multiFieldTermRecord,
	deleted *deletedDocuments,
) (uint32, error) {
	union, err := newMultiPostingUnion(segment, records)
	if err != nil {
		return 0, err
	}
	frequency := uint32(0)
	target := uint32(0)
	for scanned := uint64(0); ; scanned++ {
		if scanned&8191 == 0 {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
		}
		more, err := union.Advance(target)
		if err != nil {
			return 0, err
		}
		if !more {
			return frequency, nil
		}
		if !deleted.Contains(union.document) {
			frequency++
		}
		if union.document == math.MaxUint32 {
			return frequency, nil
		}
		target = union.document + 1
	}
}

type multiTermCursor struct {
	records  multiTermRecords
	union    multiPostingUnion
	norms    [][]uint32
	averages []float64
	fields   []Field
	idf      float64
}

func newMultiTermCursor(
	segment *Segment,
	prepared preparedMultiMatchQuery,
	records multiTermRecords,
	idf float64,
	averages []float64,
) (multiTermCursor, error) {
	union, err := newMultiPostingUnion(segment, records.fields)
	if err != nil {
		return multiTermCursor{}, err
	}
	cursor := multiTermCursor{
		records: records, union: union, averages: averages, fields: prepared.fields, idf: idf,
		norms: make([][]uint32, len(records.fields)),
	}
	for position, record := range records.fields {
		norms, err := segment.fieldNorms(record.fieldID)
		if err != nil {
			return multiTermCursor{}, err
		}
		cursor.norms[position] = norms
	}
	return cursor, nil
}

func (cursor *multiTermCursor) score(document uint32, options SearchOptions) (float64, error) {
	score := 0.0
	for position := range cursor.union.iterators {
		iterator := &cursor.union.iterators[position]
		matched, frequency, current := iterator.Current()
		if !current || matched != document {
			continue
		}
		record := cursor.records.fields[position]
		if int(document) >= len(cursor.norms[position]) {
			return 0, corruptf("multi-field norm document is out of range")
		}
		average := cursor.averages[record.fieldIndex]
		length := cursor.norms[position][document]
		if average <= 0 || length == 0 {
			return 0, corruptf("multi-field norm statistic is invalid")
		}
		score += bm25Score(
			float64(frequency), float64(length), average, cursor.idf, options.K1, options.B,
		) * cursor.fields[record.fieldIndex].Boost
	}
	return score, nil
}

func (segment *Segment) searchMultiCandidates(
	ctx context.Context,
	prepared preparedMultiMatchQuery,
	records []multiTermRecords,
	idfs []float64,
	averages []float64,
	deleted *deletedDocuments,
	base uint64,
	externalThreshold scoreThreshold,
) (candidateHeap, error) {
	if len(records) == 0 || len(records) != len(idfs) || segment.header.documentN == 0 {
		return nil, nil
	}
	cursors := make([]multiTermCursor, len(records))
	for position := range records {
		cursor, err := newMultiTermCursor(segment, prepared, records[position], idfs[position], averages)
		if err != nil {
			return nil, err
		}
		cursors[position] = cursor
	}
	maximumScore := multiMaximumScore(prepared, records, idfs, averages)
	if externalThreshold.full && scoreCannotCompete(maximumScore, base, externalThreshold) {
		return nil, nil
	}
	sort.Slice(cursors, func(left, right int) bool {
		return cursors[left].records.documentFrequency < cursors[right].records.documentFrequency
	})

	results := make(candidateHeap, 0, prepared.options.Limit)
	seed := &cursors[0]
	target := uint32(0)
	var identifierBuffer []byte
	for candidates := uint64(0); ; candidates++ {
		if candidates&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if len(results) == prepared.options.Limit {
			worst := results[0]
			if maximumScore < worst.score ||
				(maximumScore == worst.score && target >= worst.document) {
				return results, nil
			}
		}
		more, err := seed.union.Advance(target)
		if err != nil {
			return nil, err
		}
		if !more {
			return results, nil
		}
		document := seed.union.document
		if !deleted.Contains(document) {
			score, err := seed.score(document, prepared.options)
			if err != nil {
				return nil, err
			}
			matched := true
			for position := 1; position < len(cursors); position++ {
				cursor := &cursors[position]
				more, err := cursor.union.Advance(document)
				if err != nil {
					return nil, err
				}
				if !more {
					return results, nil
				}
				if cursor.union.document != document {
					matched = false
					break
				}
				termScore, err := cursor.score(document, prepared.options)
				if err != nil {
					return nil, err
				}
				score += termScore
			}
			if matched {
				matchesPrefix, err := segment.documentIdentifierHasPrefix(
					document, prepared.identifierPrefix, &identifierBuffer,
				)
				if err != nil {
					return nil, err
				}
				if !matchesPrefix {
					if document == math.MaxUint32 {
						return results, nil
					}
					target = document + 1
					continue
				}
				if !rankIsAfter(score, base+uint64(document), prepared.options.After) {
					if document == math.MaxUint32 {
						return results, nil
					}
					target = document + 1
					continue
				}
				collectCandidate(&results, rankedCandidate{document: document, score: score}, prepared.options.Limit)
			}
		}
		if document == math.MaxUint32 {
			return results, nil
		}
		target = document + 1
	}
}

func multiMaximumScore(
	prepared preparedMultiMatchQuery,
	records []multiTermRecords,
	idfs []float64,
	averages []float64,
) float64 {
	maximum := 0.0
	for term, termRecords := range records {
		for _, record := range termRecords.fields {
			maximum += maximumTermScore(
				record.record, idfs[term], averages[record.fieldIndex], prepared.options,
			) * prepared.fields[record.fieldIndex].Boost
		}
	}
	return maximum
}

func multiFrequencyKey(fields []uint16, term string) string {
	if len(term) > maximumMultiFrequencyCacheTerm {
		return ""
	}
	ordered := append([]uint16(nil), fields...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left] < ordered[right] })
	key := make([]byte, len(ordered)*2+1+len(term))
	for position, field := range ordered {
		key[position*2] = byte(field)
		key[position*2+1] = byte(field >> 8)
	}
	key[len(ordered)*2] = 0xff
	copy(key[len(ordered)*2+1:], term)
	return string(key)
}

func (cache *multiFrequencyCache) get(key string) (uint64, bool) {
	if key == "" {
		return 0, false
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	frequency, exists := cache.entries[key]
	return frequency, exists
}

func (cache *multiFrequencyCache) put(key string, frequency uint64) {
	if key == "" || frequency == 0 {
		return
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]uint64, maximumMultiFrequencyCacheEntries)
	}
	if _, exists := cache.entries[key]; exists {
		cache.entries[key] = frequency
		return
	}
	if len(cache.order) < maximumMultiFrequencyCacheEntries {
		cache.order = append(cache.order, key)
	} else {
		delete(cache.entries, cache.order[cache.next])
		cache.order[cache.next] = key
		cache.next++
		if cache.next == maximumMultiFrequencyCacheEntries {
			cache.next = 0
		}
	}
	cache.entries[key] = frequency
}

func (index *Index) materializeMultiHits(results indexCandidateHeap) ([]Hit, error) {
	ordered := append(indexCandidateHeap(nil), results...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left].score != ordered[right].score {
			return ordered[left].score > ordered[right].score
		}
		return ordered[left].ordinal < ordered[right].ordinal
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
