package search

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const maximumPrefixClauses = 256

type preparedPrefixQuery struct {
	fieldIDs []uint16
	fields   []Field
	prefix   string
	options  SearchOptions
}

type prefixClauseKey struct {
	fieldID uint16
	term    string
}

type prefixClause struct {
	fieldID uint16
	field   Field
	term    string
	record  termRecord
}

func preparePrefixQuery(
	ctx context.Context,
	schema Schema,
	query MatchQuery,
	options SearchOptions,
) (preparedPrefixQuery, error) {
	if ctx == nil {
		return preparedPrefixQuery{}, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return preparedPrefixQuery{}, err
	}
	if query.Field != "" && len(query.Fields) != 0 {
		return preparedPrefixQuery{}, fmt.Errorf("search: prefix query cannot set both Field and Fields")
	}
	if !utf8.ValidString(query.Prefix) || len(query.Prefix) > maximumQueryBytes {
		return preparedPrefixQuery{}, fmt.Errorf("search: prefix query is invalid or exceeds %d bytes", maximumQueryBytes)
	}
	normalized, err := normalizeSearchOptions(options)
	if err != nil {
		return preparedPrefixQuery{}, err
	}

	var fields []Field
	var fieldIDs []uint16
	if query.Field != "" {
		fieldID, field, exists := schema.field(query.Field)
		if !exists {
			return preparedPrefixQuery{}, fmt.Errorf("search: unknown field %q", query.Field)
		}
		fieldIDs = []uint16{fieldID}
		fields = []Field{field}
	} else {
		if len(query.Fields) == 0 || len(query.Fields) > maximumQueryFields {
			return preparedPrefixQuery{}, fmt.Errorf(
				"search: prefix query requires between 1 and %d fields", maximumQueryFields,
			)
		}
		seen := make(map[string]struct{}, len(query.Fields))
		analyzerID := ""
		fieldIDs = make([]uint16, 0, len(query.Fields))
		fields = make([]Field, 0, len(query.Fields))
		for _, name := range query.Fields {
			if _, duplicate := seen[name]; duplicate {
				return preparedPrefixQuery{}, fmt.Errorf("search: duplicate query field %q", name)
			}
			seen[name] = struct{}{}
			fieldID, field, exists := schema.field(name)
			if !exists {
				return preparedPrefixQuery{}, fmt.Errorf("search: unknown field %q", name)
			}
			if analyzerID == "" {
				analyzerID = field.Analyzer.Identifier()
			} else if field.Analyzer.Identifier() != analyzerID {
				return preparedPrefixQuery{}, fmt.Errorf(
					"search: prefix query fields must use the same analyzer",
				)
			}
			fieldIDs = append(fieldIDs, fieldID)
			fields = append(fields, field)
		}
	}

	prefix, err := analyzeSingleTerm(ctx, fields[0].Analyzer, query.Prefix)
	if err != nil {
		return preparedPrefixQuery{}, err
	}

	return preparedPrefixQuery{
		fieldIDs: fieldIDs, fields: fields, prefix: prefix, options: normalized,
	}, nil
}

func analyzeSingleTerm(ctx context.Context, analyzer Analyzer, text string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var term string
	count := 0
	err := analyzer.Analyze(ctx, text, func(token Token) bool {
		count++
		if count > 1 {
			return false
		}
		if token.Term == "" || !utf8.ValidString(token.Term) || len(token.Term) > maximumQueryTermBytes {
			term = ""
			count = 2
			return false
		}
		term = strings.Clone(token.Term)
		return true
	})
	if err != nil {
		return "", fmt.Errorf("search: analyze prefix: %w", err)
	}
	if count != 1 || term == "" {
		return "", fmt.Errorf("search: prefix query must analyze to exactly one term")
	}
	return term, nil
}

func (segment *Segment) searchPrefix(ctx context.Context, prepared preparedPrefixQuery) ([]Hit, error) {
	clauses, frequencies, err := segment.collectPrefixClauses(ctx, prepared.fieldIDs, prepared.fields, prepared.prefix, nil)
	if err != nil {
		return nil, err
	}
	if len(clauses) == 0 {
		return nil, nil
	}
	built := make([]disjunctiveClause, 0, len(clauses))
	for _, clause := range clauses {
		key := prefixClauseKey{fieldID: clause.fieldID, term: clause.term}
		frequency := frequencies[key]
		if frequency == 0 {
			continue
		}
		averageLength := float64(segment.fieldStats[clause.fieldID]) / float64(segment.header.documentN)
		built = append(built, disjunctiveClause{
			fieldID:       clause.fieldID,
			field:         clause.field,
			record:        clause.record,
			idf:           inverseDocumentFrequency(float64(segment.header.documentN), float64(frequency)),
			averageLength: averageLength,
		})
	}
	if len(built) == 0 {
		return nil, nil
	}
	return segment.searchDisjunctiveWAND(ctx, built, prepared.options)
}

func (index *Index) searchPrefix(ctx context.Context, prepared preparedPrefixQuery) ([]Hit, error) {
	if len(prepared.fieldIDs) == 0 || index.documents == 0 {
		return nil, nil
	}
	clausesBySegment := make([][]prefixClause, len(index.segments))
	frequencies := make(map[prefixClauseKey]uint64)
	for segmentIndex, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		clauses, localFrequencies, err := segment.collectPrefixClauses(
			ctx, prepared.fieldIDs, prepared.fields, prepared.prefix, index.deletions[segmentIndex],
		)
		if err != nil {
			return nil, err
		}
		clausesBySegment[segmentIndex] = clauses
		for key, frequency := range localFrequencies {
			frequencies[key] += frequency
		}
	}
	if len(frequencies) == 0 {
		return nil, nil
	}

	results := make(indexCandidateHeap, 0, prepared.options.Limit)
	for segmentIndex, segment := range index.segments {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		segmentClauses := clausesBySegment[segmentIndex]
		if len(segmentClauses) == 0 {
			continue
		}
		built := make([]disjunctiveClause, 0, len(segmentClauses))
		for _, clause := range segmentClauses {
			key := prefixClauseKey{fieldID: clause.fieldID, term: clause.term}
			frequency := frequencies[key]
			if frequency == 0 {
				continue
			}
			averageLength := float64(index.fieldStats[clause.fieldID]) / float64(index.documents)
			built = append(built, disjunctiveClause{
				fieldID:       clause.fieldID,
				field:         clause.field,
				record:        clause.record,
				idf:           inverseDocumentFrequency(float64(index.documents), float64(frequency)),
				averageLength: averageLength,
			})
		}
		if len(built) == 0 {
			continue
		}
		hits, err := segment.searchDisjunctiveWAND(ctx, built, prepared.options)
		if err != nil {
			return nil, err
		}
		for _, hit := range hits {
			collectIndexCandidate(&results, indexCandidate{
				segment:  segmentIndex,
				document: hit.InternalID,
				score:    hit.Score,
				ordinal:  index.bases[segmentIndex] + uint64(hit.InternalID),
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

func (segment *Segment) collectPrefixClauses(
	ctx context.Context,
	fieldIDs []uint16,
	fields []Field,
	prefix string,
	deleted *deletedDocuments,
) ([]prefixClause, map[prefixClauseKey]uint64, error) {
	if err := segment.ensureOpen(); err != nil {
		return nil, nil, err
	}
	if ctx == nil {
		return nil, nil, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if len(fieldIDs) == 0 || len(fieldIDs) != len(fields) {
		return nil, nil, fmt.Errorf("search: prefix query fields are invalid")
	}

	clauses := make([]prefixClause, 0, 16)
	frequencies := make(map[prefixClauseKey]uint64, 16)
	var dictionaryBuffer []byte
	for fieldIndex, fieldID := range fieldIDs {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		start := sort.Search(len(segment.dictionary), func(index int) bool {
			return segment.dictionary[index].field >= fieldID
		})
		for position := start; position < len(segment.dictionary); position++ {
			block := segment.dictionary[position]
			if block.field != fieldID {
				break
			}
			if block.firstTerm > prefix && !strings.HasPrefix(block.firstTerm, prefix) {
				break
			}
			if cap(dictionaryBuffer) < int(block.blockLength) {
				dictionaryBuffer = make([]byte, block.blockLength)
			} else {
				dictionaryBuffer = dictionaryBuffer[:block.blockLength]
			}
			data := dictionaryBuffer
			if err := readAtFull(segment.file, data, block.blockOffset); err != nil {
				return nil, nil, err
			}
			if got := dictionaryBlockChecksum(data); got != block.blockCRC {
				return nil, nil, corruptf("dictionary block checksum is %08x; expected %08x", got, block.blockCRC)
			}
			var recordErr error
			recordErr = decodeDictionaryBlock(data, segment.header.version, func(record termRecord) bool {
				if !strings.HasPrefix(record.key.term, prefix) {
					return true
				}
				if len(clauses) >= maximumPrefixClauses {
					recordErr = fmt.Errorf(
						"search: prefix query expands to more than %d terms", maximumPrefixClauses,
					)
					return false
				}
				frequency := record.documentFreq
				if deleted != nil {
					liveFrequency, err := liveDocumentFrequency(ctx, segment, record, deleted)
					if err != nil {
						recordErr = err
						return false
					}
					frequency = liveFrequency
				}
				if frequency == 0 {
					return true
				}
				key := prefixClauseKey{fieldID: fieldID, term: record.key.term}
				if _, exists := frequencies[key]; !exists {
					clauses = append(clauses, prefixClause{
						fieldID: fieldID, field: fields[fieldIndex], term: record.key.term, record: record,
					})
				}
				frequencies[key] += uint64(frequency)
				return true
			})
			if recordErr != nil {
				return nil, nil, recordErr
			}
		}
	}
	return clauses, frequencies, nil
}
