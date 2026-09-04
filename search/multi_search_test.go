package search

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

func TestSegmentMultiFieldCrossFieldANDAndBoost(t *testing.T) {
	schema := productSchema(t)
	segment := buildTestSegment(t, schema, []Document{
		{ID: "title-ranked", Fields: map[string]string{"title": "premium"}},
		{ID: "body-ranked", Fields: map[string]string{"body": "premium"}},
		{ID: "cross-field", Fields: map[string]string{"title": "Ao cotton", "body": "quan jean nam"}},
		{ID: "partial", Fields: map[string]string{"title": "cotton", "body": "lua nam"}},
		{ID: "vietnamese", Fields: map[string]string{
			"title": "\u00c1o kho\u00e1c", "body": "n\u1eef c\u00f4ng s\u1edf",
		}},
	})
	defer segment.Close()

	hits, err := segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Text: "premium",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].ID != "title-ranked" || hits[1].ID != "body-ranked" {
		t.Fatalf("boosted hits = %#v", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("title boost did not affect ranking: %#v", hits)
	}

	hits, err = segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Text: "cotton jean",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "cross-field" {
		t.Fatalf("cross-field hits = %#v", hits)
	}

	hits, err = segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Text: "ao cong so",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "vietnamese" {
		t.Fatalf("Vietnamese cross-field hits = %#v", hits)
	}

	single, err := segment.Search(context.Background(), MatchQuery{
		Field: "title", Text: "premium",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	multi, err := segment.Search(context.Background(), MatchQuery{
		Fields: []string{"title"}, Text: "premium",
	}, SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 || len(multi) != 1 || single[0].ID != multi[0].ID ||
		math.Abs(single[0].Score-multi[0].Score) > 1e-12 {
		t.Fatalf("single-field compatibility = %#v, multi = %#v", single, multi)
	}
}

func TestIndexMultiFieldIdentifierPrefixFiltersAcrossSegments(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer()),
		Text("body", StandardAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := NewIndexWriter(t.TempDir(), schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "74696b69:0001", Fields: map[string]string{
			"title": "highlands highlands coffee coffee", "body": "highlands coffee",
		}},
		{ID: "73686f706565:0001", Fields: map[string]string{
			"title": "highlands", "body": "coffee",
		}},
		{ID: "74696b69:0002", Fields: map[string]string{
			"title": "highlands highlands coffee coffee", "body": "highlands coffee",
		}},
		{ID: "73686f706565:0002", Fields: map[string]string{
			"title": "highlands coffee",
		}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(writer.directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	hits, err := index.Search(context.Background(), MatchQuery{
		Fields: []string{"title", "body"}, Text: "highlands coffee",
		IdentifierPrefix: "73686f706565:",
	}, SearchOptions{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || !strings.HasPrefix(hits[0].ID, "73686f706565:") ||
		!strings.HasPrefix(hits[1].ID, "73686f706565:") {
		t.Fatalf("multi-segment identifier-prefixed hits = %#v", hits)
	}
}

func TestMultiFieldQueryValidation(t *testing.T) {
	schema, err := NewSchema(
		Text("title", StandardAnalyzer()),
		Text("body", VietnameseAnalyzer()),
	)
	if err != nil {
		t.Fatal(err)
	}
	segment := buildTestSegment(t, schema, []Document{{
		ID: "document", Fields: map[string]string{"title": "alpha", "body": "beta"},
	}})
	defer segment.Close()

	tests := []struct {
		name  string
		query MatchQuery
	}{
		{name: "both selectors", query: MatchQuery{Field: "title", Fields: []string{"body"}, Text: "alpha"}},
		{name: "duplicate field", query: MatchQuery{Fields: []string{"title", "title"}, Text: "alpha"}},
		{name: "unknown field", query: MatchQuery{Fields: []string{"missing"}, Text: "alpha"}},
		{name: "mixed analyzers", query: MatchQuery{Fields: []string{"title", "body"}, Text: "alpha"}},
		{name: "too many fields", query: MatchQuery{Fields: repeatedFields("title", maximumQueryFields+1), Text: "alpha"}},
		{name: "invalid utf8", query: MatchQuery{Fields: []string{"title"}, Text: string([]byte{0xff})}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := segment.Search(context.Background(), test.query, SearchOptions{}); err == nil {
				t.Fatalf("query %#v succeeded", test.query)
			}
		})
	}
	if _, err := segment.Search(nil, MatchQuery{Fields: []string{"title"}, Text: "alpha"}, SearchOptions{}); err == nil {
		t.Fatal("nil context search succeeded")
	}
}

func TestIndexMultiFieldScoresMatchRebuiltLiveSnapshot(t *testing.T) {
	schema := productSchema(t)
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	documents := []Document{
		{ID: "a", Fields: map[string]string{"title": "alpha", "body": "beta alpha"}},
		{ID: "b", Fields: map[string]string{"title": "alpha beta"}},
		{ID: "c", Fields: map[string]string{"title": "gamma", "body": "alpha beta"}},
		{ID: "d", Fields: map[string]string{"title": "alpha", "body": "beta"}},
	}
	for _, document := range documents {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	updated := Document{ID: "c", Fields: map[string]string{"title": "alpha", "body": "beta beta"}}
	if err := writer.Update(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	if deleted, err := writer.Delete(context.Background(), "b"); err != nil || !deleted {
		t.Fatalf("delete b = %v, %v", deleted, err)
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}

	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	query := MatchQuery{Fields: []string{"title", "body"}, Text: "alpha beta"}
	got, err := index.Search(context.Background(), query, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rebuilt := buildTestSegment(t, schema, []Document{documents[0], updated, documents[3]})
	defer rebuilt.Close()
	want, err := rebuilt.Search(context.Background(), query, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertHitScoresByID(t, got, want)
	cached, err := index.Search(context.Background(), query, SearchOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	assertHitScoresByID(t, cached, want)
	if len(index.multiFreq.entries) == 0 {
		t.Fatal("multi-field document frequencies were not cached")
	}
}

func TestIndexMultiFieldMaxScoreKeepsLaterBetterSegment(t *testing.T) {
	schema := productSchema(t)
	writer, err := NewIndexWriter(t.TempDir(), schema, WriterOptions{
		Segment: BuildOptions{MaxDocuments: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for _, document := range []Document{
		{ID: "early-low", Fields: map[string]string{
			"title": "alpha", "body": "beta " + repeatedTerm("padding", 100),
		}},
		{ID: "later-high", Fields: map[string]string{
			"title": repeatedTerm("alpha", 5), "body": "beta",
		}},
	} {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(writer.directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	hits, err := index.Search(
		context.Background(), MatchQuery{Fields: []string{"title", "body"}, Text: "alpha beta"},
		SearchOptions{Limit: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "later-high" {
		t.Fatalf("MaxScore hits = %#v", hits)
	}
}

func TestIndexMultiFieldFrequencyCacheIsConcurrentAndBounded(t *testing.T) {
	schema := productSchema(t)
	documents := make([]Document, 256)
	for position := range documents {
		documents[position] = Document{
			ID: fmt.Sprintf("doc-%03d", position),
			Fields: map[string]string{
				"title": fmt.Sprintf("alpha term%d", position%8),
				"body":  "beta common",
			},
		}
	}
	directory := t.TempDir()
	writer, err := NewIndexWriter(directory, schema, WriterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, document := range documents {
		if err := writer.Add(context.Background(), document); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(context.Background()); err != nil {
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

	const workers = 16
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	for worker := 0; worker < workers; worker++ {
		worker := worker
		wait.Add(1)
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 20; iteration++ {
				query := fmt.Sprintf("alpha term%d", (worker+iteration)%8)
				hits, err := index.Search(
					context.Background(), MatchQuery{Fields: []string{"title", "body"}, Text: query},
					SearchOptions{},
				)
				if err != nil || len(hits) == 0 {
					errorsFound <- fmt.Errorf("query %q: hits=%d err=%w", query, len(hits), err)
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

	for position := 0; position < maximumMultiFrequencyCacheEntries+100; position++ {
		index.multiFreq.put(fmt.Sprintf("key-%05d", position), uint64(position+1))
	}
	if got := len(index.multiFreq.entries); got != maximumMultiFrequencyCacheEntries {
		t.Fatalf("frequency cache entries = %d, want %d", got, maximumMultiFrequencyCacheEntries)
	}
}

func TestManagerSearchesMultipleFields(t *testing.T) {
	schema := productSchema(t)
	manager, err := NewManager(t.TempDir(), ManagerOptions{DisableAutoCompact: true})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	for position, document := range []Document{
		{ID: "cross", Fields: map[string]string{"title": "cotton", "body": "jean"}},
		{ID: "partial", Fields: map[string]string{"title": "cotton", "body": "silk"}},
	} {
		if _, err := manager.Add(context.Background(), "tenant", schema, document); err != nil {
			t.Fatalf("add document %d: %v", position, err)
		}
	}
	hits, err := manager.Search(
		context.Background(), "tenant", schema,
		MatchQuery{Fields: []string{"title", "body"}, Text: "cotton jean"},
		SearchOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].ID != "cross" {
		t.Fatalf("manager multi-field hits = %#v", hits)
	}
}

func repeatedFields(field string, count int) []string {
	fields := make([]string, count)
	for position := range fields {
		fields[position] = field
	}
	return fields
}

func repeatedTerm(term string, count int) string {
	text := ""
	for position := 0; position < count; position++ {
		if position != 0 {
			text += " "
		}
		text += term
	}
	return text
}

func assertHitScoresByID(t testing.TB, got, want []Hit) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("hit count = %d, want %d; got %#v, want %#v", len(got), len(want), got, want)
	}
	wantScores := make(map[string]float64, len(want))
	for _, hit := range want {
		wantScores[hit.ID] = hit.Score
	}
	for _, hit := range got {
		score, exists := wantScores[hit.ID]
		if !exists {
			t.Fatalf("unexpected hit %q in %#v; want %#v", hit.ID, got, want)
		}
		if math.Abs(hit.Score-score) > 1e-12 {
			t.Fatalf("score for %q = %.15f, want %.15f", hit.ID, hit.Score, score)
		}
	}
}
