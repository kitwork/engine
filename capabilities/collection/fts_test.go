package collection

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openSearchTestDB(tb testing.TB) *sql.DB {
	tb.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		tb.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	return db
}

func TestTokenizeFoldsVietnamese(t *testing.T) {
	// The last two words use decomposed Unicode: base letters followed by combining marks.
	input := "NGUYỄN, Kiến-thức 2026 Nguye\u0302\u0303n Đa\u0323\u0306ng"
	want := []string{"nguyen", "kien", "thuc", "2026", "nguyen", "dang"}
	if got := tokenize(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize() = %#v, want %#v", got, want)
	}
}

func TestTokenizeHandlesInvalidUTF8WithoutInvalidOffsets(t *testing.T) {
	input := string([]byte{'a', 0xff, 'b'})
	if got, want := tokenize(input), []string{"a", "b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tokenize(invalid UTF-8) = %#v, want %#v", got, want)
	}
}

func TestSnippetPreservesSourceAndHighlightsFoldedMatch(t *testing.T) {
	terms := map[string]struct{}{"nguyen": {}}
	got := makeSnippet("Một câu: Nguyễn, đang viết về Kitwork.", terms)
	if !strings.Contains(got, "câu: <b>Nguyễn</b>, đang") {
		t.Fatalf("snippet lost punctuation, spelling, or highlight: %q", got)
	}
}

func TestWalkPostingsRejectsCorruptPostingLists(t *testing.T) {
	tests := []struct {
		name string
		blob []byte
		df   int
	}{
		{name: "truncated varint", blob: []byte{0x80}, df: 1},
		{name: "zero frequency", blob: []byte{0, 0, 1}, df: 1},
		{name: "duplicate document", blob: encodePostings([]posting{{doc: 0, tf: 1, dl: 1}, {doc: 0, tf: 1, dl: 1}}), df: 2},
		{name: "wrong document frequency", blob: encodePostings([]posting{{doc: 0, tf: 1, dl: 1}}), df: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := walkPostings(test.blob, test.df, 2, 1, func(int, float64) {})
			if err == nil {
				t.Fatal("corrupt posting list was accepted")
			}
		})
	}
}

func TestFTSIndexBoundsQueries(t *testing.T) {
	db := openSearchTestDB(t)
	index := &ftsIndex{db: db, collection: "posts"}
	if err := index.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	if err := index.rebuild([]ftsSource{{slug: "one", body: "alpha"}}, "signature"); err != nil {
		t.Fatal(err)
	}

	if _, err := index.search(strings.Repeat("x", maxSearchQueryBytes+1), 20); err == nil {
		t.Fatal("oversized query was accepted")
	}
	terms := make([]string, maxSearchQueryTerms+1)
	for i := range terms {
		terms[i] = fmt.Sprintf("term%d", i)
	}
	if _, err := index.search(strings.Join(terms, " "), 20); err == nil {
		t.Fatal("query with too many terms was accepted")
	}
}

func TestFTSIndexSearchContract(t *testing.T) {
	db := openSearchTestDB(t)
	index := &ftsIndex{db: db, collection: "posts"}
	if err := index.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	documents := []ftsSource{
		{slug: "runtime", title: "Kitwork Database", description: "Go runtime", body: "Một hệ thống của Nguyễn. Database search chạy nhanh."},
		{slug: "database", title: "Database", body: "Database database cơ bản."},
		{slug: "react", title: "React", body: "Giao diện component."},
	}
	if err := index.rebuild(documents, "signature-1"); err != nil {
		t.Fatal(err)
	}

	hits, err := index.search("nguyen", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].slug != "runtime" || !strings.Contains(hits[0].snippet, "<b>Nguyễn</b>") {
		t.Fatalf("accent-folded search = %#v", hits)
	}

	// The previous FTS5 query used implicit AND between individually quoted terms.
	hits, err = index.search("kitwork database", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].slug != "runtime" {
		t.Fatalf("AND search = %#v, want runtime only", hits)
	}
	hits, err = index.search("database absent", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 0 {
		t.Fatalf("query with an absent term returned %#v", hits)
	}

	hits, err = index.search("database", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].score < hits[1].score {
		t.Fatalf("BM25 results are not descending: %#v", hits)
	}
	if signature, err := index.signature(); err != nil || signature != "signature-1" {
		t.Fatalf("signature = %q, %v", signature, err)
	}
}

func TestFTSIndexKeepsCollectionsIsolated(t *testing.T) {
	db := openSearchTestDB(t)
	first := &ftsIndex{db: db, collection: "first"}
	second := &ftsIndex{db: db, collection: "second"}
	if err := first.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	if err := first.rebuild([]ftsSource{{slug: "one", title: "Alpha"}}, "first-signature"); err != nil {
		t.Fatal(err)
	}
	if err := second.rebuild([]ftsSource{{slug: "two", title: "Beta"}}, "second-signature"); err != nil {
		t.Fatal(err)
	}

	firstHits, err := first.search("alpha", 20)
	if err != nil {
		t.Fatal(err)
	}
	secondHits, err := second.search("alpha", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstHits) != 1 || len(secondHits) != 0 {
		t.Fatalf("collection isolation failed: first=%#v second=%#v", firstHits, secondHits)
	}
}

func TestFTSIndexRebuildRollsBackAtomically(t *testing.T) {
	db := openSearchTestDB(t)
	index := &ftsIndex{db: db, collection: "posts"}
	if err := index.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	if err := index.rebuild([]ftsSource{{slug: "old", title: "Stable"}}, "stable-signature"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_bad_document BEFORE INSERT ON fts_doc
		WHEN NEW.slug = 'bad' BEGIN SELECT RAISE(ABORT, 'injected rebuild failure'); END`); err != nil {
		t.Fatal(err)
	}

	err := index.rebuild([]ftsSource{
		{slug: "new", title: "Replacement"},
		{slug: "bad", title: "Must fail"},
	}, "broken-signature")
	if err == nil {
		t.Fatal("injected rebuild failure was ignored")
	}

	hits, searchErr := index.search("stable", 20)
	if searchErr != nil {
		t.Fatal(searchErr)
	}
	if len(hits) != 1 || hits[0].slug != "old" {
		t.Fatalf("failed rebuild replaced the committed index: %#v", hits)
	}
	if signature, err := index.signature(); err != nil || signature != "stable-signature" {
		t.Fatalf("failed rebuild changed signature to %q, %v", signature, err)
	}
}

func TestFTSIndexMigratesTheLegacyFTS5Projection(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "collection.db")
	open := func() *sql.DB {
		db, err := sql.Open("sqlite", databasePath)
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(1)
		if err := db.Ping(); err != nil {
			db.Close()
			t.Fatal(err)
		}
		return db
	}

	db := open()
	if _, err := db.Exec(`CREATE VIRTUAL TABLE docs USING fts5(
		collection UNINDEXED, slug UNINDEXED, title, description, body)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE doc_state (
		collection TEXT NOT NULL, slug TEXT NOT NULL, sig TEXT NOT NULL,
		PRIMARY KEY (collection, slug))`); err != nil {
		t.Fatal(err)
	}

	index := &ftsIndex{db: db, collection: "posts"}
	if err := index.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	var legacyTables int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('docs', 'doc_state')`).Scan(&legacyTables); err != nil {
		t.Fatal(err)
	}
	if legacyTables != 0 {
		t.Fatalf("legacy FTS5 projection still has %d owned tables", legacyTables)
	}
	if err := index.rebuild([]ftsSource{{slug: "one", body: "searchable"}}, "persisted"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening the file simulates the next process. ensureSchema must preserve the current format and
	// directory signature rather than clearing or rebuilding it.
	db = open()
	defer db.Close()
	reopened := &ftsIndex{db: db, collection: "posts"}
	if err := reopened.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	if signature, err := reopened.signature(); err != nil || signature != "persisted" {
		t.Fatalf("reopened signature = %q, %v", signature, err)
	}
}

func TestFTSIndexReportsCorruptStoredPosting(t *testing.T) {
	db := openSearchTestDB(t)
	index := &ftsIndex{db: db, collection: "posts"}
	if err := index.ensureSchema(); err != nil {
		t.Fatal(err)
	}
	if err := index.rebuild([]ftsSource{{slug: "one", body: "alpha"}}, "signature"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE fts_term SET doclist = x'80' WHERE collection = 'posts' AND term = 'alpha'`); err != nil {
		t.Fatal(err)
	}
	if _, err := index.search("alpha", 20); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("corrupt posting search error = %v", err)
	}
}

func BenchmarkFTSIndexSearch(b *testing.B) {
	db := openSearchTestDB(b)
	index := &ftsIndex{db: db, collection: "benchmark"}
	if err := index.ensureSchema(); err != nil {
		b.Fatal(err)
	}
	documents := make([]ftsSource, 1000)
	for i := range documents {
		documents[i] = ftsSource{
			slug:  fmt.Sprintf("document-%04d", i),
			title: fmt.Sprintf("Kitwork document %d", i),
			body:  "A sovereign database runtime with fast local search and Vietnamese content.",
		}
	}
	if err := index.rebuild(documents, "benchmark-signature"); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := index.search("database search", 20); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkFTSIndexRebuild(b *testing.B) {
	db := openSearchTestDB(b)
	index := &ftsIndex{db: db, collection: "benchmark"}
	if err := index.ensureSchema(); err != nil {
		b.Fatal(err)
	}
	documents := make([]ftsSource, 1000)
	for i := range documents {
		documents[i] = ftsSource{
			slug:  fmt.Sprintf("document-%04d", i),
			title: fmt.Sprintf("Kitwork document %d", i),
			body:  "A sovereign database runtime with fast local search and Vietnamese content.",
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := index.rebuild(documents, fmt.Sprintf("signature-%d", i)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLegacyFTS5Search(b *testing.B) {
	db := openSearchTestDB(b)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE docs USING fts5(
		slug UNINDEXED, title, body, tokenize = "unicode61 remove_diacritics 2")`); err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`INSERT INTO docs (slug, title, body) VALUES (?, ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 1000; i++ {
		if _, err := statement.Exec(
			fmt.Sprintf("document-%04d", i),
			fmt.Sprintf("Kitwork document %d", i),
			"A sovereign database runtime with fast local search and Vietnamese content.",
		); err != nil {
			b.Fatal(err)
		}
	}
	if err := statement.Close(); err != nil {
		b.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rows, err := db.Query(`SELECT slug, title, snippet(docs, 2, '<b>', '</b>', '…', 12), -bm25(docs)
			FROM docs WHERE docs MATCH ? ORDER BY bm25(docs) LIMIT 20`, `"database" "search"`)
		if err != nil {
			b.Fatal(err)
		}
		for rows.Next() {
			var slug, title, snippet string
			var score float64
			if err := rows.Scan(&slug, &title, &snippet, &score); err != nil {
				rows.Close()
				b.Fatal(err)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			b.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
