package work

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"

	collectionhelper "github.com/kitwork/engine/utilities/collection"
	"github.com/kitwork/engine/value"
)

// Full-text search over a collection — the ONE collection operation that leaves RAM.
//
//	posts.search("máy bay")            → [{ slug, title, snippet, score, meta }]
//	posts.search("embedded", 5)        → top 5
//
// The index is an FTS5 PROJECTION of the .md files in the tenant's .data/collection.db — the same law
// as every other Kitwork index (crons, caches): files are the truth, the projection is disposable and
// rebuilt from them. Sync is lazy + signature-gated: a search first compares the collection's directory
// signature (slug|mtime|size of every file) with the last synced one; unchanged collections pay zero
// writes, changed files are re-indexed individually. The tokenizer strips diacritics, so Vietnamese
// works accent-blind: "nguyen" finds "Nguyễn", "may bay" finds "máy bay".
func (h *CollectionHandle) Search(args ...value.Value) value.Value {
	if len(args) == 0 || strings.TrimSpace(args[0].String()) == "" {
		return value.Value{K: value.Invalid, V: "collection: search text is required"}
	}
	text := args[0].String()
	limit := 20
	if len(args) > 1 && args[1].N > 0 {
		limit = int(args[1].N)
	}

	match := escapeMatch(text)
	if match == "" {
		return collectionValue([]any{})
	}

	db := sqliteFor(h.tenant, "collection.db").db()
	if db == nil {
		return value.Value{K: value.Invalid, V: "collection: search index unavailable"}
	}

	index, err := h.collection.Index()
	if err != nil {
		return collectionInvalid(err)
	}
	if err := h.syncFTS(db, index); err != nil {
		return collectionInvalid(err)
	}

	// bm25() is smaller-is-better; negate so callers see bigger-is-better.
	rows, err := db.Query(`SELECT slug, title, snippet(docs, 4, '<b>', '</b>', '…', 12), -bm25(docs)
		FROM docs WHERE collection = ? AND docs MATCH ? ORDER BY bm25(docs) LIMIT ?`,
		h.collection.Path(), match, limit)
	if err != nil {
		return collectionInvalid(fmt.Errorf("collection: search: %w", err))
	}
	defer rows.Close()

	metaBySlug := make(map[string]map[string]any, len(index))
	for _, entry := range index {
		metaBySlug[entry.File.Slug] = entry.Meta
	}

	results := make([]map[string]any, 0, limit)
	for rows.Next() {
		var slug, title, snippet string
		var score float64
		if rows.Scan(&slug, &title, &snippet, &score) != nil {
			continue
		}
		results = append(results, map[string]any{
			"slug": slug, "title": title, "snippet": snippet, "score": score,
			"meta": metaBySlug[slug],
		})
	}
	return collectionValue(results)
}

// syncFTS brings the FTS projection up to date with the collection's files. Signature-gated: if the
// directory signature matches the last sync (per process), it is a no-op. Otherwise changed/new files
// are re-indexed one by one (per-file signature) and deleted files are dropped. Runs under the tenant's
// collectionMu so concurrent first-searches cannot double-insert.
func (h *CollectionHandle) syncFTS(db *sql.DB, index []collectionhelper.IndexEntry) error {
	t := h.tenant
	key := h.collection.Path()
	dirSig := collectionDirSignature(index)

	t.collectionMu.Lock()
	defer t.collectionMu.Unlock()
	if t.collectionFTS == nil {
		t.collectionFTS = make(map[string]string)
	}
	if t.collectionFTS[key] == dirSig {
		return nil // projection already reflects these exact files
	}

	stmts := []string{
		`CREATE VIRTUAL TABLE IF NOT EXISTS docs USING fts5(
			collection UNINDEXED, slug UNINDEXED, title, description, body,
			tokenize = "unicode61 remove_diacritics 2")`,
		`CREATE TABLE IF NOT EXISTS doc_state (
			collection TEXT NOT NULL, slug TEXT NOT NULL, sig TEXT NOT NULL,
			PRIMARY KEY (collection, slug))`,
	}
	for _, q := range stmts {
		if _, err := db.Exec(q); err != nil {
			return fmt.Errorf("collection: search schema: %w", err)
		}
	}

	// What the projection currently holds for this collection.
	known := map[string]string{}
	rows, err := db.Query(`SELECT slug, sig FROM doc_state WHERE collection = ?`, key)
	if err != nil {
		return err
	}
	for rows.Next() {
		var slug, sig string
		if rows.Scan(&slug, &sig) == nil {
			known[slug] = sig
		}
	}
	rows.Close()

	// Upsert changed/new documents.
	live := make(map[string]bool, len(index))
	for _, entry := range index {
		slug := entry.File.Slug
		live[slug] = true
		sig := entry.File.Signature() // size + mtime-nanos: catches even a same-second, same-size edit
		// An EMPTY signature means the entry came from a snapshot that lost it — never treat that as
		// "unchanged" (it would compare equal to the missing-row zero value and skip indexing forever).
		if sig != "" && known[slug] == sig {
			continue
		}
		doc, err := h.collection.Read(slug)
		if err != nil {
			fmt.Printf("[Collection] search index skip %s/%s: %v\n", key, slug, err)
			continue
		}
		title, _ := doc.Meta["title"].(string)
		description, _ := doc.Meta["description"].(string)
		if _, err := db.Exec(`DELETE FROM docs WHERE collection = ? AND slug = ?`, key, slug); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO docs (collection, slug, title, description, body) VALUES (?,?,?,?,?)`,
			key, slug, title, description, doc.Body); err != nil {
			return err
		}
		if _, err := db.Exec(`INSERT INTO doc_state (collection, slug, sig) VALUES (?,?,?)
			ON CONFLICT(collection, slug) DO UPDATE SET sig = excluded.sig`, key, slug, sig); err != nil {
			return err
		}
	}

	// Drop documents whose file is gone.
	for slug := range known {
		if !live[slug] {
			db.Exec(`DELETE FROM docs WHERE collection = ? AND slug = ?`, key, slug)
			db.Exec(`DELETE FROM doc_state WHERE collection = ? AND slug = ?`, key, slug)
		}
	}

	t.collectionFTS[key] = dirSig
	return nil
}

// collectionDirSignature condenses every file's identity+freshness into one comparable string.
func collectionDirSignature(index []collectionhelper.IndexEntry) string {
	hash := sha256.New()
	for _, entry := range index {
		fmt.Fprintf(hash, "%s|%s\n", entry.File.Slug, entry.File.Signature())
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

// escapeMatch turns raw user input into a safe FTS5 MATCH expression: each whitespace token is quoted
// (implicit AND between them), so FTS5 operators/syntax in user input can neither error nor inject.
func escapeMatch(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	quoted := make([]string, len(fields))
	for i, f := range fields {
		quoted[i] = `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
	}
	return strings.Join(quoted, " ")
}
