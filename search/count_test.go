package search

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestIndexCountMatchesRankedAndPreservesSnapshots(t *testing.T) {
	ctx := context.Background()
	schema, err := NewSchema(Text("title", StandardAnalyzer()), Text("body", StandardAnalyzer()))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "index")
	writer, err := NewIndexWriter(directory, schema, WriterOptions{Segment: BuildOptions{MaxDocuments: 97}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for i := 0; i < 500; i++ {
		title, body := "alpha", "beta"
		if i%3 == 0 {
			title += " beta"
		}
		if i%5 == 0 {
			body += " gamma alpha"
		}
		if err := writer.Add(ctx, Document{ID: fmt.Sprintf("shop/%03d", i), Fields: map[string]string{"title": title, "body": body}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	old, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	for _, query := range []MatchQuery{
		{Fields: []string{"title", "body"}, Text: "alpha beta"},
		{Fields: []string{"title", "body"}, Text: "beta alpha gamma"},
		{Fields: []string{"title", "body"}, Text: "alpha alpha"},
		{Fields: []string{"title", "body"}, Text: "alpha", IdentifierPrefix: "shop/1"},
		{Field: "title", Text: "alpha beta"},
		{Field: "body", Text: "beta gamma"},
		{Field: "title", Text: "missing"},
		{Field: "title", Text: ""},
	} {
		hits, err := old.Search(ctx, query, SearchOptions{Limit: 1000})
		if err != nil {
			t.Fatal(err)
		}
		count, err := old.Count(ctx, query, nil)
		if err != nil || count != uint64(len(hits)) {
			t.Fatalf("%+v count=%d hits=%d err=%v", query, count, len(hits), err)
		}
		want := uint64(0)
		for _, hit := range hits {
			if strings.HasSuffix(hit.ID, "0") {
				want++
			}
		}
		count, err = old.Count(ctx, query, func(id string) (bool, error) { return strings.HasSuffix(id, "0"), nil })
		if err != nil || count != want {
			t.Fatalf("filtered %+v count=%d want=%d err=%v", query, count, want, err)
		}
	}
	query := MatchQuery{Fields: []string{"title", "body"}, Text: "alpha beta"}
	if _, err := writer.Delete(ctx, "shop/000"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Update(ctx, Document{ID: "shop/001", Fields: map[string]string{"title": "missing"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	current, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	for index, want := range map[*Index]uint64{old: 500, current: 498} {
		count, err := index.Count(ctx, query, nil)
		if err != nil || count != want {
			t.Fatalf("snapshot count=%d want=%d err=%v", count, want, err)
		}
	}
	canceled, cancel := context.WithCancel(ctx)
	_, err = old.Count(canceled, query, func(string) (bool, error) { cancel(); return true, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-count cancel=%v", err)
	}
	sentinel := errors.New("filter failed")
	count, err := old.Count(ctx, query, func(string) (bool, error) { return false, sentinel })
	if count != 0 || !errors.Is(err, sentinel) {
		t.Fatalf("partial count=%d err=%v", count, err)
	}
	for _, bad := range []MatchQuery{
		{Field: "title", Text: "alpha", Operator: QueryAny},
		{Field: "title", Phrase: "alpha beta"},
		{Field: "title", Prefix: "alp"},
		{Field: "title", Fields: []string{"body"}, Text: "alpha"},
		{Fields: []string{"title", "title"}, Text: "alpha"},
	} {
		if _, err := old.Count(ctx, bad, nil); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
