package search

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestVietnameseAnalyzer(t *testing.T) {
	terms := collectAnalyzerTerms(t, VietnameseAnalyzer(), "Điện thoại Nguyễn, ÁO Nguye\u0302\u0303n")
	want := []string{"dien", "thoai", "nguyen", "ao", "nguyen"}
	if fmt.Sprint(terms) != fmt.Sprint(want) {
		t.Fatalf("terms = %v, want %v", terms, want)
	}

	standard := collectAnalyzerTerms(t, StandardAnalyzer(), "Điện thoại")
	if fmt.Sprint(standard) != fmt.Sprint([]string{"điện", "thoại"}) {
		t.Fatalf("standard terms = %v", standard)
	}
}

func TestSegmentRoundTripSearchAndVerify(t *testing.T) {
	schema := productSchema(t)
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	documents := []Document{
		{ID: "product-1", Fields: map[string]string{"title": "Áo thun cotton nam", "body": "Cotton mềm cho mùa hè"}},
		{ID: "product-2", Fields: map[string]string{"title": "Áo khoác nữ công sở", "body": "Thiết kế thanh lịch"}},
		{ID: "product-3", Fields: map[string]string{"title": "Áo thun nam thể thao", "body": "Vải nhanh khô"}},
	}
	for _, document := range documents {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}

	path := filepath.Join(t.TempDir(), "products.ks")
	info, err := builder.Write(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Documents != uint32(len(documents)) || info.Bytes <= segmentHeaderSize {
		t.Fatalf("unexpected info: %#v", info)
	}
	if err := builder.Add(Document{ID: "late"}); !errors.Is(err, ErrBuilderSealed) {
		t.Fatalf("add after write error = %v", err)
	}

	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	if err := segment.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}

	hits, err := segment.Search(context.Background(), MatchQuery{Field: "title", Text: "ao thun"}, SearchOptions{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "product-1" || hits[1].ID != "product-3" {
		t.Fatalf("hits = %#v", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores are not descending: %#v", hits)
	}

	hits, err = segment.Search(context.Background(), MatchQuery{Field: "title", Text: "ao thun cotton nam"}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "product-1" {
		t.Fatalf("unaccented hit = %#v", hits)
	}

	hits, err = segment.Search(context.Background(), MatchQuery{Field: "body", Text: "thanh lich"}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "product-2" {
		t.Fatalf("body hit = %#v", hits)
	}

	if hits, err := segment.Search(context.Background(), MatchQuery{Field: "title", Text: "khong-ton-tai"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("missing term hits = %#v, err = %v", hits, err)
	}
	if _, err := segment.Search(context.Background(), MatchQuery{Field: "missing", Text: "ao"}, SearchOptions{}); err == nil {
		t.Fatal("unknown field search succeeded")
	}

	wrongSchema, err := NewSchema(Text("title", StandardAnalyzer()), Text("body", VietnameseAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenSegment(path, wrongSchema); !errors.Is(err, ErrSchemaMismatch) {
		t.Fatalf("schema mismatch error = %v", err)
	}
}

func TestSegmentSearchAnyMatchesEitherTerm(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha"}},
		{ID: "doc-b", Fields: map[string]string{"text": "beta"}},
		{ID: "doc-c", Fields: map[string]string{"text": "alpha beta"}},
	} {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "any.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Field: "text", Text: "alpha beta", Operator: QueryAny,
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 {
		t.Fatalf("hits = %#v", hits)
	}
	if hits[0].ID != "doc-c" || hits[1].ID != "doc-a" || hits[2].ID != "doc-b" {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestSegmentSearchIdentifierPrefixFiltersBeforeTopK(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	segment := buildTestSegment(t, schema, []Document{
		{ID: "74696b69:0001", Fields: map[string]string{"text": "common common common"}},
		{ID: "74696b69:0002", Fields: map[string]string{"text": "common common common"}},
		{ID: "73686f706565:0001", Fields: map[string]string{"text": "common"}},
		{ID: "73686f706565:0002", Fields: map[string]string{"text": "common"}},
	})
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Field: "text", Text: "common", IdentifierPrefix: "73686f706565:",
	}, SearchOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "73686f706565:0001" || hits[1].ID != "73686f706565:0002" {
		t.Fatalf("identifier-prefixed hits = %#v", hits)
	}
}

func TestSearchIdentifierPrefixValidation(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	segment := buildTestSegment(t, schema, []Document{{
		ID: "tenant:1", Fields: map[string]string{"text": "alpha beta"},
	}})
	defer segment.Close()

	queries := []MatchQuery{
		{Field: "text", Text: "alpha", IdentifierPrefix: string([]byte{0xff})},
		{Field: "text", Text: "alpha", IdentifierPrefix: strings.Repeat("x", defaultMaxIdentifier+1)},
		{Field: "text", Phrase: "alpha beta", IdentifierPrefix: "tenant:"},
		{Field: "text", Prefix: "alp", IdentifierPrefix: "tenant:"},
		{Field: "text", Text: "alpha beta", IdentifierPrefix: "tenant:", Operator: QueryAny},
	}
	for _, query := range queries {
		if _, err := segment.Search(context.Background(), query, SearchOptions{}); err == nil {
			t.Fatalf("unsupported identifier-prefix query succeeded: %#v", query)
		}
	}
}

func TestSegmentMultiFieldSearchAnyMatchesEitherField(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer()),
		Text("body", StandardAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"title": "alpha"}},
		{ID: "doc-b", Fields: map[string]string{"body": "beta"}},
		{ID: "doc-c", Fields: map[string]string{"title": "alpha", "body": "beta"}},
	} {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "multi-any.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Text: "alpha beta", Operator: QueryAny,
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].ID != "doc-c" {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestSegmentSearchPrefixMatchesExpandedTerms(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha"}},
		{ID: "doc-b", Fields: map[string]string{"text": "alphabet"}},
		{ID: "doc-c", Fields: map[string]string{"text": "alpine"}},
		{ID: "doc-d", Fields: map[string]string{"text": "beta"}},
	} {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "prefix.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Field: "text", Prefix: "alp",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 3 || hits[0].ID != "doc-a" || hits[1].ID != "doc-b" || hits[2].ID != "doc-c" {
		t.Fatalf("hits = %#v", hits)
	}
}

func TestSegmentSearchPhraseMatchesAdjacentTermsOnly(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha beta"}},
		{ID: "doc-b", Fields: map[string]string{"text": "alpha x beta"}},
		{ID: "doc-c", Fields: map[string]string{"text": "beta alpha"}},
	} {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "phrase.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Field: "text", Phrase: "alpha beta",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "doc-a" {
		t.Fatalf("phrase hits = %#v", hits)
	}
}

func TestSegmentSearchPhraseMatchesMultipleFields(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer()),
		Text("body", StandardAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"title": "alpha beta"}},
		{ID: "doc-b", Fields: map[string]string{"body": "alpha beta"}},
		{ID: "doc-c", Fields: map[string]string{"title": "alpha", "body": "beta"}},
	} {
		if err := builder.Add(document); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "phrase-multi.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Phrase: "alpha beta",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "doc-a" || hits[1].ID != "doc-b" {
		t.Fatalf("phrase hits = %#v", hits)
	}
}

func TestIndexSearchPhraseMergesSegments(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "doc-a", Fields: map[string]string{"text": "alpha beta"}},
		{ID: "doc-b", Fields: map[string]string{"text": "alpha x beta"}},
		{ID: "doc-c", Fields: map[string]string{"text": "alpha beta"}},
		{ID: "doc-d", Fields: map[string]string{"text": "beta alpha"}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Commit(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	hits, err := index.Search(context.Background(), MatchQuery{
		Field: "text", Phrase: "alpha beta",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "doc-a" || hits[1].ID != "doc-c" {
		t.Fatalf("phrase hits = %#v", hits)
	}
}

func TestReindexStreamingReplacementSupportsPhraseSearch(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer()),
		Text("body", StandardAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	docs := []Document{
		{ID: "doc-a", Fields: map[string]string{"title": "alpha beta"}},
		{ID: "doc-b", Fields: map[string]string{"body": "alpha beta"}},
		{ID: "doc-c", Fields: map[string]string{"title": "alpha", "body": "beta"}},
	}

	t.Run("writer", func(t *testing.T) {
		directory := t.TempDir()
		writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 1}})
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		info, err := writer.Reindex(context.Background(), func(add func(Document) error) error {
			for _, document := range docs {
				if err := add(document); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if info.Documents != uint64(len(docs)) {
			t.Fatalf("reindex info = %#v", info)
		}
		index, err := OpenIndex(directory, schema)
		if err != nil {
			t.Fatal(err)
		}
		defer index.Close()
		hits, err := index.Search(context.Background(), MatchQuery{
			Fields: []string{"title", "body"}, Phrase: "alpha beta",
		}, SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 || hits[0].ID != "doc-a" || hits[1].ID != "doc-b" {
			t.Fatalf("hits = %#v", hits)
		}
	})

	t.Run("manager", func(t *testing.T) {
		manager, err := NewManager(t.TempDir(), ManagerOptions{DisableAutoCompact: true})
		if err != nil {
			t.Fatal(err)
		}
		defer manager.Close()
		info, err := manager.Reindex(context.Background(), "tenant", schema, func(add func(Document) error) error {
			for _, document := range docs {
				if err := add(document); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if info.Documents != uint64(len(docs)) {
			t.Fatalf("reindex info = %#v", info)
		}
		hits, err := manager.Search(context.Background(), "tenant", schema, MatchQuery{
			Fields: []string{"title", "body"}, Phrase: "alpha beta",
		}, SearchOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 2 || hits[0].ID != "doc-a" || hits[1].ID != "doc-b" {
			t.Fatalf("hits = %#v", hits)
		}
	})
}

func TestDictionarySpansBlocksAndFields(t *testing.T) {
	schema, err := NewSchema(Text("title", StandardAnalyzer()), Text("body", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	titleTerms := make([]string, 160)
	bodyTerms := make([]string, 90)
	for index := range titleTerms {
		titleTerms[index] = fmt.Sprintf("titleterm%03d", index)
	}
	for index := range bodyTerms {
		bodyTerms[index] = fmt.Sprintf("bodyterm%03d", index)
	}
	if err := builder.Add(Document{ID: "wide", Fields: map[string]string{
		"title": strings.Join(titleTerms, " "), "body": strings.Join(bodyTerms, " "),
	}}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wide.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	for _, query := range []MatchQuery{
		{Field: "title", Text: "titleterm000 titleterm159"},
		{Field: "title", Text: "titleterm064"},
		{Field: "body", Text: "bodyterm089"},
	} {
		hits, err := segment.Search(context.Background(), query, SearchOptions{})
		if err != nil {
			t.Fatalf("query %#v: %v", query, err)
		}
		if len(hits) != 1 || hits[0].ID != "wide" {
			t.Fatalf("query %#v hits = %#v", query, hits)
		}
	}
	if err := segment.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestPostingBlocksAdvanceAndDeterministicTies(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 400; index++ {
		text := "common"
		if index%17 == 0 {
			text += " rare"
		}
		if err := builder.Add(Document{ID: fmt.Sprintf("doc-%03d", index), Fields: map[string]string{"text": text}}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "blocks.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	hits, err := segment.Search(context.Background(), MatchQuery{Field: "text", Text: "common rare"}, SearchOptions{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 24 {
		t.Fatalf("hit count = %d, want 24", len(hits))
	}
	for index, hit := range hits {
		wantDocument := uint32(index * 17)
		if hit.InternalID != wantDocument {
			t.Fatalf("hit %d internal ID = %d, want %d", index, hit.InternalID, wantDocument)
		}
	}
	if err := segment.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBlockMaxMatchesExhaustiveAndPrunes(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	highScore := strings.TrimSpace(strings.Repeat("alpha beta ", 8))
	lowScore := "alpha beta " + strings.Repeat("padding ", 126)
	for document := 0; document < 4096; document++ {
		text := lowScore
		if document < postingBlockDocuments {
			text = highScore
		}
		if err := builder.Add(Document{
			ID: fmt.Sprintf("doc-%05d", document), Fields: map[string]string{"text": text},
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "block-max.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	if segment.header.version != segmentVersion {
		t.Fatalf("segment version = %d, want %d", segment.header.version, segmentVersion)
	}

	optimized, optimizedStats := runBlockMaxSegmentQuery(t, segment, schema, true)
	exhaustive, exhaustiveStats := runBlockMaxSegmentQuery(t, segment, schema, false)
	assertOrderedHitsEqual(t, optimized, exhaustive)
	if optimizedStats.leadBlocksDecoded > 2 || optimizedStats.leadBlocksPruned < 30 {
		t.Fatalf("block-max work = %#v, want <=2 decoded and >=30 pruned", optimizedStats)
	}
	if exhaustiveStats.leadBlocksDecoded != 32 || exhaustiveStats.leadBlocksPruned != 0 {
		t.Fatalf("exhaustive work = %#v, want 32 decoded and no pruning", exhaustiveStats)
	}
	if optimizedStats.candidatesScored >= exhaustiveStats.candidatesScored {
		t.Fatalf("block-max scored %d candidates; exhaustive scored %d", optimizedStats.candidatesScored, exhaustiveStats.candidatesScored)
	}
}

func TestBlockMaxDoesNotPruneLateWinner(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	highScore := strings.TrimSpace(strings.Repeat("alpha beta ", 8))
	lowScore := "alpha beta " + strings.Repeat("padding ", 126)
	for document := 0; document < 4096; document++ {
		text := lowScore
		if document >= 2048 && document < 2048+postingBlockDocuments {
			text = highScore
		}
		if err := builder.Add(Document{
			ID: fmt.Sprintf("late-%05d", document), Fields: map[string]string{"text": text},
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "block-max-late.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	optimized, optimizedStats := runBlockMaxSegmentQuery(t, segment, schema, true)
	exhaustive, _ := runBlockMaxSegmentQuery(t, segment, schema, false)
	assertOrderedHitsEqual(t, optimized, exhaustive)
	if len(optimized) == 0 || optimized[0].InternalID < 2048 || optimized[0].InternalID >= 2048+postingBlockDocuments {
		t.Fatalf("late winner was not retained: %#v", optimized)
	}
	if optimizedStats.leadBlocksDecoded != 17 || optimizedStats.leadBlocksPruned != 15 {
		t.Fatalf("late-winner work = %#v, want 17 decoded and 15 pruned", optimizedStats)
	}
}

func TestBlockMaxMatchesExhaustiveAcrossScoringOptions(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer(), Boost(1.7)))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	random := rand.New(rand.NewSource(7))
	for document := 0; document < 2048; document++ {
		text := strings.Repeat("alpha ", 1+random.Intn(8))
		if document%5 != 0 {
			text += strings.Repeat("beta ", 1+random.Intn(6))
		}
		text += strings.Repeat("padding ", random.Intn(80))
		if err := builder.Add(Document{
			ID: fmt.Sprintf("random-%05d", document), Fields: map[string]string{"text": text},
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "block-max-options.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	options := []SearchOptions{
		{Limit: 1, K1: 0.3, B: 0.25},
		{Limit: 7, K1: 1.2, B: 0.75},
		{Limit: 20, K1: 2.0, B: 1},
		{Limit: maximumSearchLimit, K1: 4.5, B: 0.5},
	}
	for _, queryText := range []string{"alpha beta", "beta alpha"} {
		for _, searchOptions := range options {
			prepared, err := prepareMatchQuery(
				context.Background(), schema,
				MatchQuery{Field: "text", Text: queryText}, searchOptions,
			)
			if err != nil {
				t.Fatal(err)
			}
			optimized, err := segment.searchMatch(context.Background(), prepared)
			if err != nil {
				t.Fatal(err)
			}
			prepared.blockMax = false
			exhaustive, err := segment.searchMatch(context.Background(), prepared)
			if err != nil {
				t.Fatal(err)
			}
			assertOrderedHitsEqual(t, optimized, exhaustive)
		}
	}
}

func TestSegmentV1SearchCompatibilityDisablesBlockMax(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	builder.version = segmentVersionV1
	for document := 0; document < 300; document++ {
		if err := builder.Add(Document{
			ID:     fmt.Sprintf("legacy-%03d", document),
			Fields: map[string]string{"text": "alpha beta compatibility"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "legacy-v1.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	if segment.header.version != segmentVersionV1 {
		t.Fatalf("legacy segment version = %d", segment.header.version)
	}

	hits, stats := runBlockMaxSegmentQuery(t, segment, schema, true)
	if len(hits) != defaultSearchLimit || stats.leadBlocksPruned != 0 || stats.leadBlocksDecoded != 3 {
		t.Fatalf("legacy search hits=%d stats=%#v", len(hits), stats)
	}
	if err := segment.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBlockMaxHeaderCorruptionIsRejected(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	path := buildSingleFieldSegment(t, schema, []string{"alpha beta", "alpha gamma"})
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := segment.lookupTerm(0, "alpha")
	if err != nil || !found {
		_ = segment.Close()
		t.Fatalf("lookup alpha: found=%v err=%v", found, err)
	}
	if err := segment.Close(); err != nil {
		t.Fatal(err)
	}
	flipFileByte(t, path, record.postingsOffset+12)

	segment, err = OpenSegment(path, schema)
	if err != nil {
		t.Fatalf("open should defer posting header verification: %v", err)
	}
	defer segment.Close()
	if _, err := segment.Search(context.Background(), MatchQuery{Field: "text", Text: "alpha"}, SearchOptions{}); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("posting header corruption search error = %v", err)
	}
}

func TestBlockMaxChecksCancellationWhilePruning(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	highScore := strings.TrimSpace(strings.Repeat("alpha beta ", 8))
	lowScore := "alpha beta " + strings.Repeat("padding ", 32)
	for document := 0; document < 10_000; document++ {
		text := lowScore
		if document < postingBlockDocuments {
			text = highScore
		}
		if err := builder.Add(Document{
			ID: fmt.Sprintf("cancel-%05d", document), Fields: map[string]string{"text": text},
		}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "block-max-cancel.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	prepared, err := prepareMatchQuery(
		context.Background(), schema, MatchQuery{Field: "text", Text: "alpha beta"}, SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	statistics := blockMaxStatistics{}
	prepared.statistics = &statistics
	// Posting read-ahead now checks the context on both sides of physical I/O,
	// so leave enough checks to enter the pruning loop before cancellation.
	ctx := &cancelAfterChecksContext{Context: context.Background(), after: 20, done: make(chan struct{})}
	if _, err := segment.searchMatch(ctx, prepared); !errors.Is(err, context.Canceled) {
		t.Fatalf("pruning cancellation error = %v", err)
	}
	if statistics.leadBlocksPruned == 0 || statistics.leadBlocksPruned >= 78 {
		t.Fatalf("pruning did not stop at a bounded context check: %#v", statistics)
	}
}

func runBlockMaxSegmentQuery(
	t *testing.T,
	segment *Segment,
	schema Schema,
	enabled bool,
) ([]Hit, blockMaxStatistics) {
	t.Helper()
	prepared, err := prepareMatchQuery(
		context.Background(), schema, MatchQuery{Field: "text", Text: "alpha beta"}, SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	statistics := blockMaxStatistics{}
	prepared.blockMax = enabled
	prepared.statistics = &statistics
	hits, err := segment.searchMatch(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	return hits, statistics
}

func assertOrderedHitsEqual(t testing.TB, got, want []Hit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("hits = %d, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("hit %d = %#v, want %#v", index, got[index], want[index])
		}
	}
}

type cancelAfterChecksContext struct {
	context.Context
	after    int
	checks   int
	canceled bool
	done     chan struct{}
}

func (ctx *cancelAfterChecksContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *cancelAfterChecksContext) Err() error {
	if ctx.canceled {
		return context.Canceled
	}
	ctx.checks++
	if ctx.checks < ctx.after {
		return nil
	}
	ctx.canceled = true
	close(ctx.done)
	return context.Canceled
}

func TestBuilderRejectsAtomicallyAndSignalsFlush(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	builder, err := NewBuilder(schema, BuildOptions{FlushThresholdBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := builder.Add(Document{ID: "first", Fields: map[string]string{"text": "alpha beta"}}); err != nil {
		t.Fatal(err)
	}
	before := builder.DocumentCount()
	if err := builder.Add(Document{ID: "second", Fields: map[string]string{"text": "gamma"}}); !errors.Is(err, ErrSegmentFull) {
		t.Fatalf("flush error = %v", err)
	}
	if builder.DocumentCount() != before {
		t.Fatalf("document count changed after flush rejection")
	}
	if err := builder.Add(Document{ID: "first", Fields: map[string]string{"text": "duplicate"}}); err == nil {
		t.Fatal("duplicate identifier was accepted")
	}
	if err := builder.Add(Document{ID: "bad", Fields: map[string]string{"unknown": "value"}}); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if builder.DocumentCount() != before {
		t.Fatalf("document count changed after rejected documents")
	}
}

func TestBuilderCanSkipLongLexicalNoise(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	strict, err := NewBuilder(schema, BuildOptions{MaxTermBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	document := Document{ID: "product", Fields: map[string]string{
		"text": "useful abcdefghijklmnop searchable",
	}}
	if err := strict.Add(document); err == nil {
		t.Fatal("strict builder accepted an oversized term")
	}

	bounded, err := NewBuilder(schema, BuildOptions{MaxTermBytes: 8, SkipLongTerms: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := bounded.Add(document); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bounded.ks")
	if _, err := bounded.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	hits, err := segment.Search(
		context.Background(), MatchQuery{Field: "text", Text: "useful"}, SearchOptions{},
	)
	if err != nil || len(hits) != 1 {
		t.Fatalf("search useful: hits=%d err=%v", len(hits), err)
	}
	hits, err = segment.Search(
		context.Background(), MatchQuery{Field: "text", Text: "abcdefghijklmnop"}, SearchOptions{},
	)
	if err != nil || len(hits) != 0 {
		t.Fatalf("search skipped term: hits=%d err=%v", len(hits), err)
	}
}

func TestEmptySegmentMetadataAndClose(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	if len(schema.Fields()) != 1 || schema.Fingerprint() == [32]byte{} {
		t.Fatalf("schema metadata is incomplete")
	}
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if builder.EstimatedBytes() != 0 {
		t.Fatalf("empty builder estimate = %d", builder.EstimatedBytes())
	}
	path := filepath.Join(t.TempDir(), "empty.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	if info := segment.Info(); info.Documents != 0 || info.Fields != 1 {
		t.Fatalf("empty segment info = %#v", info)
	}
	if err := segment.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits, err := segment.Search(context.Background(), MatchQuery{Field: "text", Text: "anything"}, SearchOptions{}); err != nil || len(hits) != 0 {
		t.Fatalf("empty search hits = %#v, err = %v", hits, err)
	}
	if err := segment.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := segment.Search(context.Background(), MatchQuery{Field: "text", Text: "anything"}, SearchOptions{}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed search error = %v", err)
	}
}

func TestSegmentDetectsHeaderAndPostingCorruption(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	path := buildSingleFieldSegment(t, schema, []string{"alpha beta", "alpha gamma"})
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := segment.lookupTerm(0, "alpha")
	if err != nil || !found {
		t.Fatalf("lookup alpha: found=%v err=%v", found, err)
	}
	if err := segment.Close(); err != nil {
		t.Fatal(err)
	}
	flipFileByte(t, path, record.postingsOffset+postingBlockHeaderSize)

	segment, err = OpenSegment(path, schema)
	if err != nil {
		t.Fatalf("open should defer posting verification: %v", err)
	}
	defer segment.Close()
	if _, err := segment.Search(context.Background(), MatchQuery{Field: "text", Text: "alpha"}, SearchOptions{}); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("posting corruption search error = %v", err)
	}
	if err := segment.Verify(context.Background()); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("posting corruption verify error = %v", err)
	}

	headerPath := buildSingleFieldSegment(t, schema, []string{"delta"})
	flipFileByte(t, headerPath, 24)
	if _, err := OpenSegment(headerPath, schema); !errors.Is(err, ErrCorruptSegment) {
		t.Fatalf("header corruption error = %v", err)
	}
}

func TestCanceledAndConcurrentSearch(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	texts := make([]string, 1000)
	for index := range texts {
		texts[index] = "alpha beta gamma"
	}
	path := buildSingleFieldSegment(t, schema, texts)
	segment, err := OpenSegment(path, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := segment.Search(ctx, MatchQuery{Field: "text", Text: "alpha"}, SearchOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled search error = %v", err)
	}

	var wait sync.WaitGroup
	errorsFound := make(chan error, 16)
	for worker := 0; worker < 16; worker++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 20; iteration++ {
				hits, err := segment.Search(context.Background(), MatchQuery{Field: "text", Text: "alpha beta"}, SearchOptions{})
				if err != nil {
					errorsFound <- err
					return
				}
				if len(hits) != defaultSearchLimit {
					errorsFound <- fmt.Errorf("got %d hits", len(hits))
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Fatal(err)
	}
}

type cancelOnRangeReaderAt struct {
	reader    *bytes.Reader
	cancel    context.CancelFunc
	target    int64
	armed     bool
	triggered bool
	reads     int
}

type countingReaderAt struct {
	reader interface {
		ReadAt([]byte, int64) (int, error)
	}
	reads int
	bytes int
}

type countedReadRange struct {
	start uint64
	end   uint64
}

type rangeCountingReaderAt struct {
	reader interface {
		ReadAt([]byte, int64) (int, error)
	}
	ranges []countedReadRange
	reads  int
}

func (reader *rangeCountingReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	read, err := reader.reader.ReadAt(destination, offset)
	if offset >= 0 && read > 0 {
		start := uint64(offset)
		end := start + uint64(read)
		for _, current := range reader.ranges {
			if start < current.end && end > current.start {
				reader.reads++
				break
			}
		}
	}
	return read, err
}

func (reader *countingReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	read, err := reader.reader.ReadAt(destination, offset)
	reader.reads++
	reader.bytes += read
	return read, err
}

func (reader *cancelOnRangeReaderAt) ReadAt(destination []byte, offset int64) (int, error) {
	read, err := reader.reader.ReadAt(destination, offset)
	reader.reads++
	if reader.armed && !reader.triggered && offset <= reader.target &&
		reader.target < offset+int64(read) {
		reader.triggered = true
		reader.cancel()
	}
	return read, err
}

func TestPostingReadAheadHonorsCancellationAfterPayloadRead(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	path := buildSingleFieldSegment(t, schema, []string{
		"alpha beta", "alpha gamma", "alpha delta", "alpha epsilon",
	})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader := &cancelOnRangeReaderAt{reader: bytes.NewReader(data), cancel: cancel}
	segment, err := openSegmentReader("<cancel-payload>", reader, int64(len(data)), schema)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := segment.lookupTerm(0, "alpha")
	if err != nil || !found {
		t.Fatalf("lookup alpha: found=%t err=%v", found, err)
	}
	iterator, err := newPostingIterator(
		ctx, segment.file, segment.header.version, record, segment.header.documentN,
		segment.header.sections[sectionPostings], false,
	)
	if err != nil {
		t.Fatal(err)
	}
	reader.target = int64(record.postingsOffset + iterator.headerSize)
	reader.armed = true
	readsBefore := reader.reads
	if _, err := iterator.Next(); !errors.Is(err, context.Canceled) {
		t.Fatalf("posting payload cancellation = %v", err)
	}
	if !reader.triggered {
		t.Fatal("posting payload read did not trigger cancellation")
	}
	if reads := reader.reads - readsBefore; reads != 1 {
		t.Fatalf("header and small payload used %d physical reads, want 1", reads)
	}
	if _, _, current := iterator.Current(); current {
		t.Fatal("canceled payload was published as the current posting")
	}
}

func TestIdentifierPrefixReadAheadCoalescesSequentialCandidates(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	documents := make([]Document, 512)
	for index := range documents {
		documents[index] = Document{
			ID: fmt.Sprintf("shopee/%06d", index),
			Fields: map[string]string{
				"text": "keyboard",
			},
		}
	}
	segment := buildTestSegment(t, schema, documents)
	defer segment.Close()
	counted := &countingReaderAt{reader: segment.file}
	segment.file = counted
	reader := newDocumentIdentifierPrefixReader(context.Background(), segment)
	for document := range uint32(len(documents)) {
		matched, err := reader.hasPrefix(document, "shopee/")
		if err != nil || !matched {
			t.Fatalf("document %d prefix: matched=%t err=%v", document, matched, err)
		}
	}
	if counted.reads > 6 {
		t.Fatalf("identifier prefix used %d physical reads for %d candidates, want at most 6", counted.reads, len(documents))
	}
	if counted.bytes > 6*identifierReadAheadBytes {
		t.Fatalf("identifier prefix read %d bytes, want at most %d", counted.bytes, 6*identifierReadAheadBytes)
	}
}

func TestIdentifierPrefixReadAheadHonorsCancellationAfterPhysicalRead(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	path := buildSingleFieldSegment(t, schema, []string{"keyboard"})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	wrapped := &cancelOnRangeReaderAt{reader: bytes.NewReader(data), cancel: cancel}
	segment, err := openSegmentReader(path, wrapped, int64(len(data)), schema)
	if err != nil {
		t.Fatal(err)
	}
	defer segment.Close()
	wrapped.target = int64(segment.header.sections[sectionStoredOffsets].offset)
	wrapped.armed = true
	reader := newDocumentIdentifierPrefixReader(ctx, segment)
	matched, err := reader.hasPrefix(0, "doc-")
	if !errors.Is(err, context.Canceled) || matched {
		t.Fatalf("identifier cancellation: matched=%t err=%v", matched, err)
	}
	if !wrapped.triggered {
		t.Fatal("identifier offset read did not trigger cancellation")
	}
}

func TestMultiFieldTopKChecksIdentifierOnlyForCompetitiveCandidates(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer(), Boost(3)),
		Text("body", StandardAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	documents := make([]Document, 512)
	padding := strings.Repeat("x", 512)
	for index := range documents {
		title := "alpha"
		body := "beta"
		if index < 10 {
			title = strings.Repeat("alpha ", 8)
			body = strings.Repeat("beta ", 8)
		}
		documents[index] = Document{
			ID: fmt.Sprintf("shopee/%06d/%s", index, padding),
			Fields: map[string]string{
				"title": title,
				"body":  body,
			},
		}
	}
	segment := buildTestSegment(t, schema, documents)
	defer segment.Close()
	offsets := segment.header.sections[sectionStoredOffsets]
	data := segment.header.sections[sectionStoredData]
	counted := &rangeCountingReaderAt{
		reader: segment.file,
		ranges: []countedReadRange{
			{start: offsets.offset, end: offsets.offset + offsets.length},
			{start: data.offset, end: data.offset + data.length},
		},
	}
	segment.file = counted
	hits, err := segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Text: "alpha beta", IdentifierPrefix: "shopee/",
	}, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 10 {
		t.Fatalf("hits = %d, want 10", len(hits))
	}
	for position, hit := range hits {
		want := fmt.Sprintf("shopee/%06d/", position)
		if !strings.HasPrefix(hit.ID, want) {
			t.Fatalf("hit %d = %q, want prefix %q", position, hit.ID, want)
		}
	}
	if counted.reads > 30 {
		t.Fatalf("Top-K identifier path used %d stored reads, want at most 30", counted.reads)
	}
}

func TestBoostedSingleFieldSearchAfterDoesNotRepeatBoundary(t *testing.T) {
	schema, err := NewSchema(Text("text", StandardAnalyzer(), Boost(4)))
	if err != nil {
		t.Fatal(err)
	}
	segment := buildTestSegment(t, schema, []Document{
		{ID: "first", Fields: map[string]string{"text": "alpha alpha"}},
		{ID: "second", Fields: map[string]string{"text": "alpha"}},
	})
	defer segment.Close()
	query := MatchQuery{Field: "text", Text: "alpha"}
	first, err := segment.Search(context.Background(), query, SearchOptions{Limit: 1})
	if err != nil || len(first) != 1 {
		t.Fatalf("first page = %#v, err=%v", first, err)
	}
	boundary := &SearchAfter{Score: first[0].Score, Ordinal: first[0].Ordinal}
	second, err := segment.Search(context.Background(), query, SearchOptions{Limit: 1, After: boundary})
	if err != nil || len(second) != 1 {
		t.Fatalf("second page = %#v, err=%v", second, err)
	}
	if second[0].ID == first[0].ID {
		t.Fatalf("SEARCH AFTER repeated boundary hit %q", first[0].ID)
	}
}

func productSchema(t testing.TB) Schema {
	t.Helper()
	schema, err := NewSchema(
		Text("title", VietnameseAnalyzer(), Boost(3)),
		Text("body", VietnameseAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

func collectAnalyzerTerms(t testing.TB, analyzer Analyzer, text string) []string {
	t.Helper()
	var terms []string
	if err := analyzer.Analyze(context.Background(), text, func(token Token) bool {
		terms = append(terms, token.Term)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	return terms
}

func buildSingleFieldSegment(t testing.TB, schema Schema, texts []string) string {
	t.Helper()
	builder, err := NewBuilder(schema, BuildOptions{})
	if err != nil {
		t.Fatal(err)
	}
	field := schema.fields[0].Name
	for index, text := range texts {
		if err := builder.Add(Document{ID: fmt.Sprintf("doc-%06d", index), Fields: map[string]string{field: text}}); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "segment.ks")
	if _, err := builder.Write(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	return path
}

func flipFileByte(t testing.TB, path string, offset uint64) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var value [1]byte
	if err := readAtFull(file, value[:], offset); err != nil {
		t.Fatal(err)
	}
	value[0] ^= 0xff
	if _, err := file.WriteAt(value[:], int64(offset)); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
}
