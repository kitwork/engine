package search

import (
	"context"
	"testing"
)

// OR and prefix queries walk postings through WAND; a tombstoned document, or
// the replaced version of an upserted one, must not be scored or returned.
func TestIndexAnyAndPrefixSkipDeletedDocuments(t *testing.T) {
	ctx := context.Background()
	schema := productSchema(t)
	directory := t.TempDir()
	writer := writeLiveIndex(t, directory, schema, []Document{
		{ID: "kept", Fields: map[string]string{"title": "áo thun", "body": "cotton"}},
		{ID: "deleted", Fields: map[string]string{"title": "áo khoác", "body": "thun"}},
		{ID: "replaced", Fields: map[string]string{"title": "áo thun đỏ", "body": "thun"}},
	}, 64)
	defer writer.Close()
	if _, err := writer.Delete(ctx, "deleted"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Upsert(ctx, Document{ID: "replaced", Fields: map[string]string{"title": "giày", "body": "dép"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	index, err := OpenIndex(directory, schema)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()

	both := []string{"title", "body"}
	for name, query := range map[string]MatchQuery{
		"field any":  {Field: "title", Text: "thun khoác", Operator: QueryAny},
		"fields any": {Fields: both, Text: "thun khoác", Operator: QueryAny},
		"prefix":     {Fields: both, Prefix: "th"},
	} {
		hits, err := index.Search(ctx, query, SearchOptions{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 || hits[0].ID != "kept" {
			var ids []string
			for _, hit := range hits {
				ids = append(ids, hit.ID)
			}
			t.Fatalf("%s returned %v; only kept still matches", name, ids)
		}
	}
}
