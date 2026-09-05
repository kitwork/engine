package search

import (
	"container/heap"
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	defaultBM25K1      = 1.2
	defaultBM25B       = 0.75
	defaultSearchLimit = 20
	// MaximumSearchLimit is a hard allocation ceiling. Product-facing limits
	// belong to the caller so one embedded engine can serve both small tenant
	// queries and bounded export/search workloads without a hidden limit of 200.
	MaximumSearchLimit    = 100_000
	maximumSearchLimit    = MaximumSearchLimit
	maximumQueryBytes     = 4 << 10
	maximumQueryTerms     = 32
	maximumQueryFields    = 32
	maximumQueryTermBytes = 1<<16 - 1
)

// QueryOperator selects whether all analyzed terms must match or any one may
// match. The zero value keeps the current implicit-AND behavior.
type QueryOperator uint8

const (
	QueryAll QueryOperator = iota
	QueryAny
)

// MatchQuery analyzes Text, Phrase, or Prefix with the configured field
// analyzer. The zero-value operator requires every unique query term to match.
// Set either Field for the optimized single-field path or Fields for
// cross-field matching.
type MatchQuery struct {
	Field            string
	Fields           []string
	Text             string
	Phrase           string
	Prefix           string
	IdentifierPrefix string
	Operator         QueryOperator
}

// SearchOptions control bounded Top-K collection and BM25 tuning.
type SearchOptions struct {
	Limit int
	K1    float64
	B     float64
	After *SearchAfter
}

// SearchAfter is an exclusive rank boundary. Scores sort descending and
// Ordinal breaks equal-score ties ascending, matching Hit ordering exactly.
// It is stable only while the caller keeps the same immutable index snapshot.
type SearchAfter struct {
	Score   float64
	Ordinal uint64
}

func normalizeSearchOptions(options SearchOptions) (SearchOptions, error) {
	if options.Limit == 0 {
		options.Limit = defaultSearchLimit
	}
	if options.K1 == 0 {
		options.K1 = defaultBM25K1
	}
	if options.B == 0 {
		options.B = defaultBM25B
	}
	if options.Limit < 1 || options.Limit > maximumSearchLimit {
		return SearchOptions{}, fmt.Errorf("search: limit must be between 1 and %d", maximumSearchLimit)
	}
	if math.IsNaN(options.K1) || math.IsInf(options.K1, 0) || options.K1 <= 0 {
		return SearchOptions{}, fmt.Errorf("search: BM25 K1 must be positive and finite")
	}
	if math.IsNaN(options.B) || math.IsInf(options.B, 0) || options.B < 0 || options.B > 1 {
		return SearchOptions{}, fmt.Errorf("search: BM25 B must be between 0 and 1")
	}
	if options.After != nil && (math.IsNaN(options.After.Score) ||
		math.IsInf(options.After.Score, 0) || options.After.Score < 0) {
		return SearchOptions{}, fmt.Errorf("search: after score must be non-negative and finite")
	}
	return options, nil
}

// Hit is one ranked external document identifier.
type Hit struct {
	ID         string
	Score      float64
	InternalID uint32
	// Ordinal is stable within one immutable snapshot and breaks equal-score ties.
	Ordinal uint64
}

type queryTerm struct {
	record       termRecord
	iterator     *postingIterator
	idf          float64
	maximumScore float64
}

type segmentSearchScratch struct {
	iterators  []postingIterator
	terms      []queryTerm
	results    candidateHeap
	identifier documentIdentifierPrefixReader
}

type blockMaxStatistics struct {
	leadBlocksDecoded uint64
	leadBlocksPruned  uint64
	candidatesScored  uint64
	segmentsPruned    uint64
}

type scoreThreshold struct {
	full    bool
	score   float64
	ordinal uint64
}

type preparedMatchQuery struct {
	fieldID          uint16
	field            Field
	terms            []string
	identifierPrefix string
	options          SearchOptions
	blockMax         bool
	statistics       *blockMaxStatistics
}

// Search executes a bounded implicit-AND BM25 query against this segment.
func (segment *Segment) Search(ctx context.Context, query MatchQuery, options SearchOptions) ([]Hit, error) {
	if err := segment.ensureOpen(); err != nil {
		return nil, err
	}
	if err := validateIdentifierPrefix(query); err != nil {
		return nil, err
	}
	if err := validateSearchAfterQuery(query, options); err != nil {
		return nil, err
	}
	if query.Phrase != "" {
		prepared, err := preparePhraseQuery(ctx, segment.schema, query, options)
		if err != nil {
			return nil, err
		}
		if segment.header.documentN == 0 {
			return nil, nil
		}
		return segment.searchPhrase(ctx, prepared)
	}
	if query.Prefix != "" {
		prepared, err := preparePrefixQuery(ctx, segment.schema, query, options)
		if err != nil {
			return nil, err
		}
		if segment.header.documentN == 0 {
			return nil, nil
		}
		return segment.searchPrefix(ctx, prepared)
	}
	if query.Operator == QueryAny {
		if len(query.Fields) != 0 {
			prepared, err := prepareMultiMatchQuery(ctx, segment.schema, query, options)
			if err != nil {
				return nil, err
			}
			if len(prepared.terms) == 0 || segment.header.documentN == 0 {
				return nil, nil
			}
			if len(prepared.terms) == 1 {
				return segment.searchMultiMatch(ctx, prepared)
			}
			return segment.searchMultiMatchAny(ctx, prepared)
		}
		prepared, err := prepareMatchQuery(ctx, segment.schema, query, options)
		if err != nil {
			return nil, err
		}
		if len(prepared.terms) == 0 || segment.header.documentN == 0 {
			return nil, nil
		}
		if len(prepared.terms) == 1 {
			return segment.searchMatch(ctx, prepared)
		}
		return segment.searchMatchAny(ctx, prepared)
	}
	if len(query.Fields) != 0 {
		prepared, err := prepareMultiMatchQuery(ctx, segment.schema, query, options)
		if err != nil {
			return nil, err
		}
		if len(prepared.terms) == 0 || segment.header.documentN == 0 {
			return nil, nil
		}
		return segment.searchMultiMatch(ctx, prepared)
	}
	prepared, err := prepareMatchQuery(ctx, segment.schema, query, options)
	if err != nil {
		return nil, err
	}
	if len(prepared.terms) == 0 || segment.header.documentN == 0 {
		return nil, nil
	}
	return segment.searchMatch(ctx, prepared)
}

func (segment *Segment) searchMatch(ctx context.Context, prepared preparedMatchQuery) ([]Hit, error) {
	records := make([]termRecord, 0, len(prepared.terms))
	idfs := make([]float64, 0, len(prepared.terms))
	var dictionaryBuffer []byte
	for _, term := range prepared.terms {
		record, found, err := segment.lookupTermBuffered(prepared.fieldID, term, &dictionaryBuffer)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, nil
		}
		documents := float64(segment.header.documentN)
		documentFrequency := float64(record.documentFreq)
		records = append(records, record)
		idfs = append(idfs, inverseDocumentFrequency(documents, documentFrequency))
	}
	averageLength := float64(segment.fieldStats[prepared.fieldID]) / float64(segment.header.documentN)
	return segment.searchPrepared(ctx, prepared, records, idfs, averageLength, 0)
}

func prepareMatchQuery(ctx context.Context, schema Schema, query MatchQuery, options SearchOptions) (preparedMatchQuery, error) {
	if ctx == nil {
		return preparedMatchQuery{}, fmt.Errorf("search: query context is nil")
	}
	if err := ctx.Err(); err != nil {
		return preparedMatchQuery{}, err
	}
	if len(query.Fields) != 0 {
		return preparedMatchQuery{}, fmt.Errorf("search: match query cannot set both Field and Fields")
	}
	if !utf8.ValidString(query.Text) || len(query.Text) > maximumQueryBytes {
		return preparedMatchQuery{}, fmt.Errorf("search: query is invalid or exceeds %d bytes", maximumQueryBytes)
	}
	fieldID, field, exists := schema.field(query.Field)
	if !exists {
		return preparedMatchQuery{}, fmt.Errorf("search: unknown field %q", query.Field)
	}
	normalized, err := normalizeSearchOptions(options)
	if err != nil {
		return preparedMatchQuery{}, err
	}
	terms, err := analyzeQuery(ctx, field.Analyzer, query.Text)
	if err != nil {
		return preparedMatchQuery{}, err
	}
	return preparedMatchQuery{
		fieldID: fieldID, field: field, terms: terms, identifierPrefix: query.IdentifierPrefix,
		options: normalized, blockMax: true,
	}, nil
}

func validateIdentifierPrefix(query MatchQuery) error {
	if query.IdentifierPrefix == "" {
		return nil
	}
	if !utf8.ValidString(query.IdentifierPrefix) || len(query.IdentifierPrefix) > defaultMaxIdentifier {
		return fmt.Errorf(
			"search: identifier prefix is invalid or exceeds %d bytes", defaultMaxIdentifier,
		)
	}
	if query.Phrase != "" || query.Prefix != "" || query.Operator == QueryAny {
		return fmt.Errorf("search: identifier prefix supports implicit-AND text queries only")
	}
	return nil
}

func (segment *Segment) searchPrepared(
	ctx context.Context,
	prepared preparedMatchQuery,
	records []termRecord,
	idfs []float64,
	averageLength float64,
	base uint64,
) ([]Hit, error) {
	candidates, err := segment.searchCandidates(ctx, prepared, records, idfs, averageLength)
	if err != nil {
		return nil, err
	}
	return segment.materializeHits(candidates, base)
}

func (segment *Segment) searchCandidates(
	ctx context.Context,
	prepared preparedMatchQuery,
	records []termRecord,
	idfs []float64,
	averageLength float64,
) (candidateHeap, error) {
	var scratch segmentSearchScratch
	return segment.searchCandidatesWithScratch(
		ctx, prepared, records, idfs, averageLength, nil, &scratch, 0, scoreThreshold{},
	)
}

func (segment *Segment) searchCandidatesWithScratch(
	ctx context.Context,
	prepared preparedMatchQuery,
	records []termRecord,
	idfs []float64,
	averageLength float64,
	deleted *deletedDocuments,
	scratch *segmentSearchScratch,
	base uint64,
	externalThreshold scoreThreshold,
) (candidateHeap, error) {
	if len(records) == 0 || len(records) != len(idfs) || segment.header.documentN == 0 || averageLength <= 0 {
		return nil, nil
	}
	if prepared.blockMax && externalThreshold.full {
		upperBound := 0.0
		for index, record := range records {
			upperBound += maximumTermScore(record, idfs[index], averageLength, prepared.options)
		}
		upperBound *= prepared.field.Boost
		if scoreCannotCompete(upperBound, base, externalThreshold) {
			if prepared.statistics != nil {
				prepared.statistics.segmentsPruned++
			}
			return nil, nil
		}
	}
	if cap(scratch.iterators) < len(records) {
		scratch.iterators = make([]postingIterator, len(records))
	} else {
		scratch.iterators = scratch.iterators[:len(records)]
	}
	if cap(scratch.terms) < len(records) {
		scratch.terms = make([]queryTerm, 0, len(records))
	} else {
		scratch.terms = scratch.terms[:0]
	}
	norms, err := segment.fieldNorms(prepared.fieldID)
	if err != nil {
		return nil, err
	}
	for index, record := range records {
		iterator := &scratch.iterators[index]
		if err := resetPostingIterator(
			iterator, ctx, segment.file, segment.header.version, record,
			segment.header.documentN, segment.header.sections[sectionPostings], false,
		); err != nil {
			return nil, err
		}
		iterator.norms = norms
		scratch.terms = append(scratch.terms, queryTerm{
			record: record, iterator: iterator, idf: idfs[index],
			maximumScore: maximumTermScore(record, idfs[index], averageLength, prepared.options),
		})
	}
	queryTerms := scratch.terms
	sort.Slice(queryTerms, func(i, j int) bool {
		return queryTerms[i].record.documentFreq < queryTerms[j].record.documentFreq
	})

	if cap(scratch.results) < prepared.options.Limit {
		scratch.results = make(candidateHeap, 0, prepared.options.Limit)
	} else {
		scratch.results = scratch.results[:0]
	}
	results := scratch.results
	seed := queryTerms[0]
	candidates := uint64(0)
	leadBlocks := uint64(0)
	scratch.identifier.reset(ctx, segment)
	for {
		if leadBlocks&63 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		header, more, err := seed.iterator.nextBlockHeader()
		if err != nil {
			return nil, err
		}
		if !more {
			break
		}
		leadBlocks++
		if prepared.blockMax && header.minimumNorm != 0 {
			upperBound := blockScoreUpperBound(seed, queryTerms[1:], header, averageLength, prepared)
			if blockCannotCompete(
				upperBound, header.firstDocument, results, prepared.options.Limit,
				base, externalThreshold,
			) {
				if err := seed.iterator.skipNextBlock(header); err != nil {
					return nil, err
				}
				if prepared.statistics != nil {
					prepared.statistics.leadBlocksPruned++
				}
				continue
			}
		}
		if _, err := seed.iterator.decodeNextBlock(header); err != nil {
			return nil, err
		}
		if prepared.statistics != nil {
			prepared.statistics.leadBlocksDecoded++
		}
		for blockIndex := 0; blockIndex < seed.iterator.blockCount; blockIndex++ {
			seed.iterator.blockIndex = blockIndex
			document, frequency, _ := seed.iterator.Current()
			if deleted.Contains(document) {
				continue
			}
			if candidates&63 == 0 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
			}
			candidates++
			length := float64(norms[document])
			score := bm25Score(float64(frequency), length, averageLength, seed.idf, prepared.options.K1, prepared.options.B)
			matched := true
			for index := 1; index < len(queryTerms); index++ {
				term := queryTerms[index]
				more, err := term.iterator.Advance(document)
				if err != nil {
					return nil, err
				}
				if !more {
					return results, nil
				}
				matchedDocument, matchedFrequency, _ := term.iterator.Current()
				if matchedDocument != document {
					matched = false
					break
				}
				score += bm25Score(float64(matchedFrequency), length, averageLength, term.idf, prepared.options.K1, prepared.options.B)
			}
			if matched {
				score *= prepared.field.Boost
				if prepared.statistics != nil {
					prepared.statistics.candidatesScored++
				}
				ordinal := base + uint64(document)
				if !rankIsAfter(score, ordinal, prepared.options.After) ||
					(externalThreshold.full && scoreCannotCompete(score, ordinal, externalThreshold)) ||
					!candidateCanCompete(results, rankedCandidate{document: document, score: score}, prepared.options.Limit) {
					continue
				}
				matchesPrefix, err := scratch.identifier.hasPrefix(document, prepared.identifierPrefix)
				if err != nil {
					return nil, err
				}
				if !matchesPrefix {
					continue
				}
				collectCandidate(&results, rankedCandidate{document: document, score: score}, prepared.options.Limit)
			}
		}
	}
	return results, nil
}

func validateSearchAfterQuery(query MatchQuery, options SearchOptions) error {
	if options.After == nil {
		return nil
	}
	if query.Phrase != "" || query.Prefix != "" || query.Operator == QueryAny {
		return fmt.Errorf("search: after supports implicit-AND text queries only")
	}
	return nil
}

func rankIsAfter(score float64, ordinal uint64, boundary *SearchAfter) bool {
	return boundary == nil || score < boundary.Score ||
		(score == boundary.Score && ordinal > boundary.Ordinal)
}

func maximumTermScore(record termRecord, idf, averageLength float64, options SearchOptions) float64 {
	if record.maximumTF == 0 || record.minimumNorm == 0 {
		return idf * (options.K1 + 1)
	}
	return bm25Score(
		float64(record.maximumTF), float64(record.minimumNorm), averageLength,
		idf, options.K1, options.B,
	)
}

func blockScoreUpperBound(
	seed queryTerm,
	other []queryTerm,
	header postingBlockHeader,
	averageLength float64,
	prepared preparedMatchQuery,
) float64 {
	upperBound := bm25Score(
		float64(header.maximumTF), float64(header.minimumNorm), averageLength,
		seed.idf, prepared.options.K1, prepared.options.B,
	)
	for _, term := range other {
		upperBound += term.maximumScore
	}
	return upperBound * prepared.field.Boost
}

func blockCannotCompete(
	upperBound float64,
	firstDocument uint32,
	local candidateHeap,
	limit int,
	base uint64,
	external scoreThreshold,
) bool {
	if len(local) == limit {
		worst := local[0]
		if upperBound < worst.score || (upperBound == worst.score && firstDocument >= worst.document) {
			return true
		}
	}
	if external.full {
		firstOrdinal := base + uint64(firstDocument)
		if scoreCannotCompete(upperBound, firstOrdinal, external) {
			return true
		}
	}
	return false
}

func scoreCannotCompete(upperBound float64, firstOrdinal uint64, threshold scoreThreshold) bool {
	return upperBound < threshold.score ||
		(upperBound == threshold.score && firstOrdinal >= threshold.ordinal)
}

func analyzeQuery(ctx context.Context, analyzer Analyzer, text string) ([]string, error) {
	seen := make(map[string]struct{})
	terms := make([]string, 0, 8)
	var tokenErr error
	err := analyzer.Analyze(ctx, text, func(token Token) bool {
		if token.Term == "" || !utf8.ValidString(token.Term) || len(token.Term) > maximumQueryTermBytes {
			tokenErr = fmt.Errorf("search: query analyzer emitted an invalid term")
			return false
		}
		if _, exists := seen[token.Term]; exists {
			return true
		}
		if len(terms) >= maximumQueryTerms {
			tokenErr = fmt.Errorf("search: query exceeds %d unique terms", maximumQueryTerms)
			return false
		}
		term := strings.Clone(token.Term)
		seen[term] = struct{}{}
		terms = append(terms, term)
		return true
	})
	if err != nil {
		return nil, fmt.Errorf("search: analyze query: %w", err)
	}
	if tokenErr != nil {
		return nil, tokenErr
	}
	return terms, nil
}

func bm25Score(termFrequency, documentLength, averageLength, idf, k1, b float64) float64 {
	denominator := termFrequency + k1*(1-b+b*documentLength/averageLength)
	return idf * (termFrequency * (k1 + 1)) / denominator
}

func inverseDocumentFrequency(documents, documentFrequency float64) float64 {
	return math.Log(1 + (documents-documentFrequency+0.5)/(documentFrequency+0.5))
}

type rankedCandidate struct {
	document uint32
	score    float64
}

// candidateHeap keeps the worst selected candidate at the root.
type candidateHeap []rankedCandidate

func (items candidateHeap) Len() int { return len(items) }
func (items candidateHeap) Less(i, j int) bool {
	if items[i].score != items[j].score {
		return items[i].score < items[j].score
	}
	return items[i].document > items[j].document
}
func (items candidateHeap) Swap(i, j int)   { items[i], items[j] = items[j], items[i] }
func (items *candidateHeap) Push(value any) { *items = append(*items, value.(rankedCandidate)) }
func (items *candidateHeap) Pop() any {
	old := *items
	last := old[len(old)-1]
	*items = old[:len(old)-1]
	return last
}

func collectCandidate(results *candidateHeap, candidate rankedCandidate, limit int) {
	if !candidateCanCompete(*results, candidate, limit) {
		return
	}
	if results.Len() < limit {
		heap.Push(results, candidate)
		return
	}
	(*results)[0] = candidate
	heap.Fix(results, 0)
}

func candidateCanCompete(results candidateHeap, candidate rankedCandidate, limit int) bool {
	if len(results) < limit {
		return true
	}
	worst := results[0]
	return candidate.score > worst.score ||
		(candidate.score == worst.score && candidate.document < worst.document)
}

func (segment *Segment) materializeHits(candidates candidateHeap, base uint64) ([]Hit, error) {
	ordered := append(candidateHeap(nil), candidates...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		return ordered[i].document < ordered[j].document
	})
	hits := make([]Hit, len(ordered))
	for index, candidate := range ordered {
		identifier, err := segment.documentIdentifier(candidate.document)
		if err != nil {
			return nil, err
		}
		hits[index] = Hit{
			ID: identifier, Score: candidate.score, InternalID: candidate.document,
			Ordinal: base + uint64(candidate.document),
		}
	}
	return hits, nil
}
