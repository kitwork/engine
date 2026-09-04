package collection

import "database/sql"

// SearchIndex is the exported surface over the hand-written BM25 full-text engine, so a caller OUTSIDE
// this capability — schema-aware tables using db.table.search — reuses ONE engine instead of a
// second copy. "One engine, two doors": the collection door indexes Markdown documents, the schema door
// indexes rows of a SQLite table, both landing in the same fts_term/fts_doc/fts_stat tables of whatever
// modernc SQLite database the caller passes. The internals
// (tokenizer, Vietnamese folding, posting encoding, BM25) stay unexported; this is the stable seam.
type SearchIndex struct{ inner *ftsIndex }

// SearchField is one weighted piece of a document's indexable text. Weight multiplies the term
// frequency at INDEX time — a column declared .searchable({ weight: 3 }) makes each of its terms count
// three times — so higher-weighted fields rank their matches higher with ZERO query-time cost. A weight
// of 0 or less is treated as 1.
type SearchField struct {
	Text   string
	Weight int
}

// SearchDoc is one document to index. Key is its stable identity (a slug, or a row's primary-key value).
// Title is stored for display; Snippet is the text a highlighted excerpt is cut from; Fields are the
// weighted texts tokenized into the index.
type SearchDoc struct {
	Key     string
	Title   string
	Snippet string
	Fields  []SearchField
}

// SearchHit is one ranked result. Key identifies the source row/document so the caller can hydrate the
// full record; Snippet is the highlighted excerpt; Score is BM25 (higher = more relevant).
type SearchHit struct {
	Key     string
	Title   string
	Snippet string
	Score   float64
}

// NewSearchIndex opens the index for one logical scope (a collection path, or a table name) inside a
// modernc SQLite database. Rows of different scopes never collide — every row is keyed by scope.
func NewSearchIndex(db *sql.DB, scope string) *SearchIndex {
	return &SearchIndex{inner: &ftsIndex{db: db, collection: scope}}
}

// EnsureSchema creates the projection tables (idempotent, versioned) — call once before Rebuild/Search.
func (s *SearchIndex) EnsureSchema() error { return s.inner.ensureSchema() }

// Signature returns the freshness token stored with the last Rebuild (empty when never built). The
// caller compares it against a cheap signature of the source to decide whether a rebuild is due.
func (s *SearchIndex) Signature() (string, error) { return s.inner.signature() }

// Rebuild replaces the scope's whole index from docs, stamping it with signature. Weighted fields let a
// title outrank a body. Correct by construction: old rows are deleted first.
func (s *SearchIndex) Rebuild(docs []SearchDoc, signature string) error {
	return s.inner.rebuildWeighted(docs, signature)
}

// Search ranks the scope's documents for a query (implicit AND, BM25), returning at most limit hits.
func (s *SearchIndex) Search(query string, limit int) ([]SearchHit, error) {
	hits, err := s.inner.search(query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SearchHit, len(hits))
	for i, h := range hits {
		out[i] = SearchHit{Key: h.slug, Title: h.title, Snippet: h.snippet, Score: h.score}
	}
	return out, nil
}
