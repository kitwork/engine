package search

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

type preparedPhraseQuery struct {
	fieldID  uint16
	field    Field
	fieldIDs []uint16
	fields   []Field
	terms    []string
	options  SearchOptions
}

type phraseCursor struct {
	iterator postingIterator
	record   termRecord
	idf      float64
}

func preparePhraseQuery(
	ctx context.Context,
	schema Schema,
	query MatchQuery,
	options SearchOptions,
) (preparedPhraseQuery, error) {
	if ctx == nil {
		return preparedPhraseQuery{}, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return preparedPhraseQuery{}, err
	}
	if query.Text != "" || query.Prefix != "" {
		return preparedPhraseQuery{}, fmt.Errorf("search: phrase query cannot set Text or Prefix")
	}
	if !utf8.ValidString(query.Phrase) || len(query.Phrase) > maximumQueryBytes {
		return preparedPhraseQuery{}, fmt.Errorf("search: phrase query is invalid or exceeds %d bytes", maximumQueryBytes)
	}
	normalized, err := normalizeSearchOptions(options)
	if err != nil {
		return preparedPhraseQuery{}, err
	}

	var fieldIDs []uint16
	var fields []Field
	switch {
	case query.Field != "" && len(query.Fields) != 0:
		return preparedPhraseQuery{}, fmt.Errorf("search: phrase query cannot set both Field and Fields")
	case query.Field != "":
		fieldID, field, exists := schema.field(query.Field)
		if !exists {
			return preparedPhraseQuery{}, fmt.Errorf("search: unknown field %q", query.Field)
		}
		fieldIDs = []uint16{fieldID}
		fields = []Field{field}
	case len(query.Fields) != 0:
		if len(query.Fields) > maximumQueryFields {
			return preparedPhraseQuery{}, fmt.Errorf(
				"search: phrase query requires between 1 and %d fields", maximumQueryFields,
			)
		}
		seen := make(map[string]struct{}, len(query.Fields))
		analyzerID := ""
		fieldIDs = make([]uint16, 0, len(query.Fields))
		fields = make([]Field, 0, len(query.Fields))
		for _, name := range query.Fields {
			if _, duplicate := seen[name]; duplicate {
				return preparedPhraseQuery{}, fmt.Errorf("search: duplicate query field %q", name)
			}
			seen[name] = struct{}{}
			fieldID, field, exists := schema.field(name)
			if !exists {
				return preparedPhraseQuery{}, fmt.Errorf("search: unknown field %q", name)
			}
			if analyzerID == "" {
				analyzerID = field.Analyzer.Identifier()
			} else if field.Analyzer.Identifier() != analyzerID {
				return preparedPhraseQuery{}, fmt.Errorf(
					"search: phrase query fields must use the same analyzer",
				)
			}
			fieldIDs = append(fieldIDs, fieldID)
			fields = append(fields, field)
		}
	default:
		return preparedPhraseQuery{}, fmt.Errorf("search: phrase query requires Field or Fields")
	}

	terms, err := analyzePhraseQuery(ctx, fields[0].Analyzer, query.Phrase)
	if err != nil {
		return preparedPhraseQuery{}, err
	}
	return preparedPhraseQuery{
		fieldID: fieldIDs[0], field: fields[0], fieldIDs: fieldIDs, fields: fields,
		terms: terms, options: normalized,
	}, nil
}

func analyzePhraseQuery(ctx context.Context, analyzer Analyzer, text string) ([]string, error) {
	if ctx == nil {
		return nil, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	terms := make([]string, 0, 8)
	var tokenErr error
	err := analyzer.Analyze(ctx, text, func(token Token) bool {
		if token.Term == "" || !utf8.ValidString(token.Term) || len(token.Term) > maximumQueryTermBytes {
			tokenErr = fmt.Errorf("search: phrase analyzer emitted an invalid term")
			return false
		}
		if len(terms) >= maximumQueryTerms {
			tokenErr = fmt.Errorf("search: phrase exceeds %d terms", maximumQueryTerms)
			return false
		}
		terms = append(terms, strings.Clone(token.Term))
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("search: analyze phrase: %w", err)
	}
	if tokenErr != nil {
		return nil, tokenErr
	}
	return terms, nil
}

func (segment *Segment) searchPhrase(ctx context.Context, prepared preparedPhraseQuery) ([]Hit, error) {
	if err := segment.ensureOpen(); err != nil {
		return nil, err
	}
	if len(prepared.terms) == 1 {
		if len(prepared.fields) == 1 {
			return segment.searchMatch(ctx, preparedMatchQuery{
				fieldID: prepared.fieldID, field: prepared.field, terms: prepared.terms,
				options: prepared.options, blockMax: true,
			})
		}
		return segment.searchMultiMatch(ctx, preparedMultiMatchQuery{
			fieldIDs: prepared.fieldIDs, fields: prepared.fields, terms: prepared.terms,
			options: prepared.options,
		})
	}
	if segment.header.version < segmentVersion {
		return nil, fmt.Errorf("search: phrase search requires segment version %d or newer", segmentVersion)
	}
	if len(prepared.fields) == 1 {
		records, idfs, averageLength, err := segment.preparePhraseFieldQuery(prepared.fieldID, prepared.terms)
		if err != nil {
			return nil, err
		}
		candidates, err := segment.searchPhraseField(
			ctx, prepared.fieldID, prepared.field, records, idfs, averageLength, prepared.options, nil,
		)
		if err != nil {
			return nil, err
		}
		return segment.materializeHits(candidates, 0)
	}
	return segment.searchPhraseMultiField(ctx, prepared, nil)
}

func (index *Index) searchPhrase(ctx context.Context, prepared preparedPhraseQuery) ([]Hit, error) {
	if len(prepared.terms) == 1 {
		if len(prepared.fields) == 1 {
			return index.searchMatch(ctx, preparedMatchQuery{
				fieldID: prepared.fieldID, field: prepared.field, terms: prepared.terms,
				options: prepared.options, blockMax: true,
			})
		}
		return index.searchMultiMatch(ctx, preparedMultiMatchQuery{
			fieldIDs: prepared.fieldIDs, fields: prepared.fields, terms: prepared.terms,
			options: prepared.options,
		})
	}
	if len(prepared.fields) == 1 {
		return index.searchPhraseField(ctx, prepared.fieldID, prepared.field, prepared.terms, prepared.options)
	}
	return index.searchPhraseMultiField(ctx, prepared)
}

func (segment *Segment) preparePhraseFieldQuery(
	fieldID uint16,
	terms []string,
) ([]termRecord, []float64, float64, error) {
	if segment.header.documentN == 0 {
		return nil, nil, 0, nil
	}
	records := make([]termRecord, 0, len(terms))
	idfs := make([]float64, 0, len(terms))
	var dictionaryBuffer []byte
	for _, term := range terms {
		record, found, err := segment.lookupTermBuffered(fieldID, term, &dictionaryBuffer)
		if err != nil {
			return nil, nil, 0, err
		}
		if !found {
			return nil, nil, 0, nil
		}
		records = append(records, record)
		idfs = append(idfs, inverseDocumentFrequency(float64(segment.header.documentN), float64(record.documentFreq)))
	}
	averageLength := float64(segment.fieldStats[fieldID]) / float64(segment.header.documentN)
	if averageLength <= 0 {
		return nil, nil, 0, nil
	}
	return records, idfs, averageLength, nil
}

func (index *Index) preparePhraseFieldQuery(
	ctx context.Context,
	fieldID uint16,
	terms []string,
) ([][]termRecord, []float64, float64, error) {
	if index.documents == 0 {
		return nil, nil, 0, nil
	}
	recordsBySegment := make([][]termRecord, len(index.segments))
	documentFrequencies := make([]uint64, len(terms))
	var dictionaryBuffer []byte
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, nil, 0, err
		}
		records := make([]termRecord, len(terms))
		allFound := true
		for termIndex, term := range terms {
			record, found, err := segment.lookupTermBuffered(fieldID, term, &dictionaryBuffer)
			if err != nil {
				return nil, nil, 0, err
			}
			if !found {
				allFound = false
				continue
			}
			frequency, err := liveDocumentFrequency(ctx, segment, record, index.deletions[position])
			if err != nil {
				return nil, nil, 0, err
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
	idfs := make([]float64, len(terms))
	for termIndex, frequency := range documentFrequencies {
		if frequency == 0 {
			return nil, nil, 0, nil
		}
		if frequency > index.documents {
			return nil, nil, 0, corruptIndexf(
				"phrase term %q document frequency exceeds the index", terms[termIndex],
			)
		}
		idfs[termIndex] = inverseDocumentFrequency(float64(index.documents), float64(frequency))
	}
	averageLength := float64(index.fieldStats[fieldID]) / float64(index.documents)
	if averageLength <= 0 {
		return nil, nil, 0, nil
	}
	return recordsBySegment, idfs, averageLength, nil
}

func (segment *Segment) searchPhraseField(
	ctx context.Context,
	fieldID uint16,
	field Field,
	records []termRecord,
	idfs []float64,
	averageLength float64,
	options SearchOptions,
	deleted *deletedDocuments,
) (candidateHeap, error) {
	if len(records) == 0 || len(records) != len(idfs) || averageLength <= 0 || segment.header.documentN == 0 {
		return nil, nil
	}
	if segment.header.version < segmentVersion {
		return nil, fmt.Errorf("search: phrase search requires segment version %d or newer", segmentVersion)
	}
	norms, err := segment.fieldNorms(fieldID)
	if err != nil {
		return nil, err
	}

	cursors := make([]phraseCursor, len(records))
	seedIndex := 0
	for index, record := range records {
		iterator, err := newPostingIterator(
			ctx, segment.file, segment.header.version, record, segment.header.documentN,
			segment.header.sections[sectionPostings], true,
		)
		if err != nil {
			return nil, err
		}
		iterator.norms = norms
		cursors[index] = phraseCursor{
			iterator: *iterator,
			record:   record,
			idf:      idfs[index],
		}
		if index == 0 || record.documentFreq < cursors[seedIndex].record.documentFreq {
			seedIndex = index
		}
	}

	results := make(candidateHeap, 0, options.Limit)
	seed := &cursors[seedIndex]
	target := uint32(0)
	for scanned := uint64(0); ; scanned++ {
		if scanned&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		more, err := seed.iterator.Advance(target)
		if err != nil {
			return nil, err
		}
		if !more {
			return results, nil
		}
		document, frequency, _ := seed.iterator.Current()
		if deleted != nil && deleted.Contains(document) {
			if document == math.MaxUint32 {
				return results, nil
			}
			target = document + 1
			continue
		}
		if int(document) >= len(norms) {
			return nil, corruptf("search: phrase norm document is out of range")
		}
		length := float64(norms[document])
		if length == 0 {
			return nil, corruptf("search: phrase norm statistic is invalid")
		}
		score := bm25Score(float64(frequency), length, averageLength, seed.idf, options.K1, options.B)
		positionSets := make([][]uint32, len(cursors))
		positionSets[seedIndex] = seed.iterator.CurrentPositions()
		matched := true
		for index := range cursors {
			if index == seedIndex {
				continue
			}
			more, err := cursors[index].iterator.Advance(document)
			if err != nil {
				return nil, err
			}
			if !more {
				return results, nil
			}
			matchedDocument, matchedFrequency, _ := cursors[index].iterator.Current()
			if matchedDocument != document {
				matched = false
				break
			}
			if deleted != nil && deleted.Contains(matchedDocument) {
				matched = false
				break
			}
			score += bm25Score(float64(matchedFrequency), length, averageLength, cursors[index].idf, options.K1, options.B)
			positionSets[index] = cursors[index].iterator.CurrentPositions()
		}
		if matched && phrasePositionsMatch(positionSets) {
			collectCandidate(&results, rankedCandidate{
				document: document, score: score * field.Boost,
			}, options.Limit)
		}
		if document == math.MaxUint32 {
			return results, nil
		}
		target = document + 1
	}
}

func (segment *Segment) searchPhraseMultiField(
	ctx context.Context,
	prepared preparedPhraseQuery,
	deleted *deletedDocuments,
) ([]Hit, error) {
	results := make(candidateHeap, 0, prepared.options.Limit)
	fieldScores := make(map[uint32]float64, 16)
	for index, fieldID := range prepared.fieldIDs {
		records, idfs, averageLength, err := segment.preparePhraseFieldQuery(fieldID, prepared.terms)
		if err != nil {
			return nil, err
		}
		if len(records) == 0 {
			continue
		}
		candidates, err := segment.searchPhraseField(
			ctx, fieldID, prepared.fields[index], records, idfs, averageLength, prepared.options, deleted,
		)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			fieldScores[candidate.document] += candidate.score
		}
	}
	for document, score := range fieldScores {
		collectCandidate(&results, rankedCandidate{document: document, score: score}, prepared.options.Limit)
	}
	return segment.materializeHits(results, 0)
}

func (index *Index) searchPhraseField(
	ctx context.Context,
	fieldID uint16,
	field Field,
	terms []string,
	options SearchOptions,
) ([]Hit, error) {
	recordsBySegment, idfs, averageLength, err := index.preparePhraseFieldQuery(ctx, fieldID, terms)
	if err != nil {
		return nil, err
	}
	if len(recordsBySegment) == 0 || len(idfs) == 0 || averageLength <= 0 {
		return nil, nil
	}
	results := make(indexCandidateHeap, 0, options.Limit)
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		records := recordsBySegment[position]
		if records == nil {
			continue
		}
		candidates, err := segment.searchPhraseField(
			ctx, fieldID, field, records, idfs, averageLength, options, index.deletions[position],
		)
		if err != nil {
			return nil, err
		}
		for _, candidate := range candidates {
			collectIndexCandidate(&results, indexCandidate{
				segment: position, document: candidate.document, score: candidate.score,
				ordinal: index.bases[position] + uint64(candidate.document),
			}, options.Limit)
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

func (index *Index) searchPhraseMultiField(
	ctx context.Context,
	prepared preparedPhraseQuery,
) ([]Hit, error) {
	type phraseFieldQuery struct {
		fieldID          uint16
		field            Field
		recordsBySegment [][]termRecord
		idfs             []float64
		averageLength    float64
	}

	queries := make([]phraseFieldQuery, 0, len(prepared.fieldIDs))
	for indexField, fieldID := range prepared.fieldIDs {
		recordsBySegment, idfs, averageLength, err := index.preparePhraseFieldQuery(ctx, fieldID, prepared.terms)
		if err != nil {
			return nil, err
		}
		if len(recordsBySegment) == 0 || len(idfs) == 0 || averageLength <= 0 {
			continue
		}
		queries = append(queries, phraseFieldQuery{
			fieldID: fieldID, field: prepared.fields[indexField],
			recordsBySegment: recordsBySegment, idfs: idfs, averageLength: averageLength,
		})
	}

	results := make(indexCandidateHeap, 0, prepared.options.Limit)
	for position, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		segmentScores := make(map[uint32]float64, 16)
		for _, query := range queries {
			records := query.recordsBySegment[position]
			if records == nil {
				continue
			}
			candidates, err := segment.searchPhraseField(
				ctx, query.fieldID, query.field, records, query.idfs, query.averageLength,
				prepared.options, index.deletions[position],
			)
			if err != nil {
				return nil, err
			}
			for _, candidate := range candidates {
				segmentScores[candidate.document] += candidate.score
			}
		}
		for document, score := range segmentScores {
			collectIndexCandidate(&results, indexCandidate{
				segment: position, document: document, score: score,
				ordinal: index.bases[position] + uint64(document),
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

func phrasePositionsMatch(positionSets [][]uint32) bool {
	if len(positionSets) == 0 || len(positionSets[0]) == 0 {
		return false
	}
	current := positionSets[0]
	for shift := 1; shift < len(positionSets); shift++ {
		next := positionSets[shift]
		if len(next) == 0 {
			return false
		}
		current = intersectShiftedPositions(current, next, uint32(shift))
		if len(current) == 0 {
			return false
		}
	}
	return len(current) != 0
}

func intersectShiftedPositions(starts, positions []uint32, shift uint32) []uint32 {
	if len(starts) == 0 || len(positions) == 0 {
		return nil
	}
	matches := make([]uint32, 0, min(len(starts), len(positions)))
	left := 0
	right := 0
	for left < len(starts) && right < len(positions) {
		if starts[left] > math.MaxUint32-shift {
			left++
			continue
		}
		want := starts[left] + shift
		switch {
		case positions[right] < want:
			right++
		case positions[right] > want:
			left++
		default:
			matches = append(matches, starts[left])
			left++
			right++
		}
	}
	return matches
}
