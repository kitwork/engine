package collection

import (
	"database/sql"
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A hand-written full-text index, kept in the tenant's collection.db (modernc SQLite) as an ordinary
// table — NOT fts5. The clever part (tokenizing, Vietnamese diacritic folding, BM25 ranking) lives in
// Go, where we control and test it; SQLite is used only as a boring, crash-safe blob store on its most
// bulletproof path (a primary-key point lookup). One row per TERM holds that term's whole posting list
// packed as a varint blob, so a query reads only the rows for the words it asks about, never the whole
// corpus. Search memory is bounded by the matching posting lists; rebuild memory is proportional to the
// collection and remains acceptable because Markdown is the rebuildable source of truth.
//
// Storage (all rows scoped by collection path so one collection.db serves every collection of a tenant):
//
//	fts_term(collection, term, df, doclist)   one row per term; doclist = ⟨docid-gap, tf, doclen⟩* varint
//	fts_doc (collection, docid, slug, title, body)  dense docid → source, for hydration + snippets
//	fts_stat(collection, signature, ndoc, totallen)  source freshness and BM25 corpus statistics
//
// doclen is inlined in each posting so scoring needs no second lookup; docids ascend within a list so
// gaps stay small. The index is rebuilt wholesale whenever the collection's directory signature changes
// (the caller gates on that), which for a site's worth of documents is cheap and obviously correct.

// BM25 tuning mirrors SQLite FTS5. Scores are positive here because callers sort higher scores first.
const (
	bm25K1              = 1.2
	bm25B               = 0.75
	ftsSchemaVersion    = 1
	maxSearchQueryBytes = 4 << 10
	maxSearchQueryTerms = 32
	maxSearchResults    = 200
	maxSearchTermBytes  = 128
)

// ---- Vietnamese / Latin diacritic folding (hand-written, stdlib-only) ----
//
// So "kien thuc" finds "Kiến thức" and "nguyen" finds "Nguyễn". We lowercase first, then map the
// accented Vietnamese and common Latin-1 letters back to their base. đ/Đ is the one letter Unicode NFD
// would miss (U+0111 does not decompose), so it is in the table like the rest.
var foldTable = buildFoldTable()

func buildFoldTable() map[rune]rune {
	groups := map[rune]string{
		'a': "áàảãạăắằẳẵặâấầẩẫậ",
		'e': "éèẻẽẹêếềểễệ",
		'i': "íìỉĩị",
		'o': "óòỏõọôốồổỗộơớờởỡợ",
		'u': "úùủũụưứừửữự",
		'y': "ýỳỷỹỵ",
		'd': "đ",
		'c': "ç",
		'n': "ñ",
	}
	m := make(map[rune]rune, 128)
	for base, variants := range groups {
		for _, r := range variants {
			m[r] = base
		}
	}
	return m
}

func foldRune(r rune) rune {
	r = unicode.ToLower(r)
	if b, ok := foldTable[r]; ok {
		return b
	}
	return r
}

// keptToken retains byte offsets so snippets preserve the source punctuation and accented spelling.
type keptToken struct {
	start       int
	end         int
	foldedStart int
	foldedEnd   int
}

type tokenScan struct {
	tokens []keptToken
	folded string
}

// Vietnamese may arrive either as precomposed runes (ễ) or a base rune followed by combining marks
// (e + circumflex + tilde). The latter already contains the base letter, so folding means ignoring only
// the marks Vietnamese orthography uses. Other combining marks remain token boundaries rather than
// silently changing unrelated scripts.
func isVietnameseMark(r rune) bool {
	switch r {
	case '\u0300', // grave
		'\u0301', // acute
		'\u0302', // circumflex
		'\u0303', // tilde
		'\u0306', // breve
		'\u0309', // hook above
		'\u031b', // horn
		'\u0323', // dot below
		'\u0327': // cedilla for the small common-Latin compatibility set
		return true
	default:
		return false
	}
}

func scanTokens(text string) tokenScan {
	out := make([]keptToken, 0, min(len(text)/8, 256))
	var folded strings.Builder
	folded.Grow(min(len(text), 4<<10))
	start := -1
	end := 0
	foldedStart := 0
	discard := false
	flush := func() {
		if start >= 0 && !discard && folded.Len() > foldedStart {
			out = append(out, keptToken{
				start: start, end: end, foldedStart: foldedStart, foldedEnd: folded.Len(),
			})
		}
		start = -1
		end = 0
		foldedStart = folded.Len()
		discard = false
	}
	for offset := 0; offset < len(text); {
		r, size := utf8.DecodeRuneInString(text[offset:])
		runeEnd := offset + size
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = offset
				foldedStart = folded.Len()
			}
			end = runeEnd
			if !discard {
				foldedRune := foldRune(r)
				if folded.Len()-foldedStart+utf8.RuneLen(foldedRune) > maxSearchTermBytes {
					discard = true
				} else {
					folded.WriteRune(foldedRune)
				}
			}
			offset = runeEnd
			continue
		}
		if start >= 0 && isVietnameseMark(r) {
			end = runeEnd
			offset = runeEnd
			continue
		}
		flush()
		offset = runeEnd
	}
	flush()
	return tokenScan{tokens: out, folded: folded.String()}
}

// tokenize folds text and splits it into lowercase alphanumeric terms.
func tokenize(text string) []string {
	scan := scanTokens(text)
	out := make([]string, len(scan.tokens))
	for i, token := range scan.tokens {
		out[i] = scan.folded[token.foldedStart:token.foldedEnd]
	}
	return out
}

func uniqueTerms(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; !ok {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}

// ---- posting list encoding ----

type posting struct {
	doc int
	tf  int
	dl  int
}

// encodePostings packs a term's posting list. The list MUST be in ascending docid order so the
// docid deltas stay small; each posting contributes ⟨gap, tf, doclen⟩ as unsigned varints.
func encodePostings(list []posting) []byte {
	buf := make([]byte, 0, len(list)*3)
	var tmp [binary.MaxVarintLen64]byte
	prev := 0
	put := func(v int) {
		n := binary.PutUvarint(tmp[:], uint64(v))
		buf = append(buf, tmp[:n]...)
	}
	for _, p := range list {
		put(p.doc - prev)
		prev = p.doc
		put(p.tf)
		put(p.dl)
	}
	return buf
}

// walkPostings decodes one term's blob and yields each document's BM25 contribution in ascending
// document order. Blob corruption must become an ordinary search error, never an infinite loop or
// slice panic.
func walkPostings(blob []byte, df, ndoc int, avgdl float64, visit func(document int, score float64)) error {
	if df <= 0 || df > ndoc || ndoc <= 0 || avgdl <= 0 {
		return fmt.Errorf("invalid corpus statistics")
	}
	idf := math.Log(1 + (float64(ndoc)-float64(df)+0.5)/(float64(df)+0.5))
	prev := 0
	decoded := 0
	read := func(label string) (uint64, error) {
		value, n := binary.Uvarint(blob)
		if n == 0 {
			return 0, fmt.Errorf("truncated %s", label)
		}
		if n < 0 {
			return 0, fmt.Errorf("overflowed %s", label)
		}
		blob = blob[n:]
		return value, nil
	}
	for len(blob) > 0 {
		gap, err := read("document gap")
		if err != nil {
			return err
		}
		tf, err := read("term frequency")
		if err != nil {
			return err
		}
		dl, err := read("document length")
		if err != nil {
			return err
		}
		if decoded > 0 && gap == 0 {
			return fmt.Errorf("document ids are not strictly increasing")
		}
		if gap > uint64(maxInt()-prev) || tf == 0 || tf > uint64(maxInt()) || dl == 0 || dl > uint64(maxInt()) {
			return fmt.Errorf("posting value is out of range")
		}
		doc := prev + int(gap)
		if doc >= ndoc || tf > dl {
			return fmt.Errorf("posting exceeds corpus bounds")
		}
		prev = doc
		f := float64(tf)
		norm := f + bm25K1*(1-bm25B+bm25B*float64(dl)/avgdl)
		visit(doc, idf*(f*(bm25K1+1))/norm)
		decoded++
	}
	if decoded != df {
		return fmt.Errorf("document frequency is %d but posting list contains %d", df, decoded)
	}
	return nil
}

func maxInt() int {
	return int(^uint(0) >> 1)
}

// ---- the index over one collection.db ----

type ftsIndex struct {
	db         *sql.DB
	collection string
}

// ftsSource is one document to index: terms come from title+description+body, snippets from body.
type ftsSource struct {
	slug        string
	title       string
	description string
	body        string
}

type ftsHit struct {
	slug    string
	title   string
	snippet string
	score   float64
}

func (x *ftsIndex) ensureSchema() error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS fts_schema (
		id INTEGER PRIMARY KEY CHECK (id = 1), version INTEGER NOT NULL) WITHOUT ROWID`); err != nil {
		return err
	}

	var version int
	reset := false
	switch err := tx.QueryRow(`SELECT version FROM fts_schema WHERE id = 1`).Scan(&version); err {
	case nil:
		reset = version != ftsSchemaVersion
	case sql.ErrNoRows:
		reset = true
	default:
		return err
	}

	if reset {
		// collection.db is a disposable projection. A format change is safer as an atomic rebuild than
		// a partial migration, and this also removes the FTS5 tables owned by the previous backend.
		for _, statement := range []string{
			`DROP TABLE IF EXISTS fts_term`,
			`DROP TABLE IF EXISTS fts_doc`,
			`DROP TABLE IF EXISTS fts_stat`,
			`DROP TABLE IF EXISTS docs`,
			`DROP TABLE IF EXISTS doc_state`,
		} {
			if _, err := tx.Exec(statement); err != nil {
				return err
			}
		}
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS fts_term (
			collection TEXT NOT NULL, term TEXT NOT NULL,
			df INTEGER NOT NULL, doclist BLOB NOT NULL,
			PRIMARY KEY (collection, term)) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS fts_doc (
			collection TEXT NOT NULL, docid INTEGER NOT NULL,
			slug TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
			PRIMARY KEY (collection, docid)) WITHOUT ROWID`,
		`CREATE TABLE IF NOT EXISTS fts_stat (
			collection TEXT PRIMARY KEY, signature TEXT NOT NULL,
			ndoc INTEGER NOT NULL, totallen INTEGER NOT NULL) WITHOUT ROWID`,
	}
	for _, statement := range statements {
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	if reset {
		if _, err := tx.Exec(`DELETE FROM fts_schema`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO fts_schema (id, version) VALUES (1, ?)`, ftsSchemaVersion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (x *ftsIndex) signature() (string, error) {
	var signature string
	switch err := x.db.QueryRow(`SELECT signature FROM fts_stat WHERE collection = ?`, x.collection).Scan(&signature); err {
	case nil:
		return signature, nil
	case sql.ErrNoRows:
		return "", nil
	default:
		return "", err
	}
}

// rebuild replaces this collection's whole index in one transaction. Correct by construction: the old
// rows are deleted, so a term that no longer appears simply disappears, and a stale document cannot
// linger.
func (x *ftsIndex) rebuild(docs []ftsSource, signature string) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM fts_term WHERE collection = ?`, x.collection); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fts_doc WHERE collection = ?`, x.collection); err != nil {
		return err
	}

	insDoc, err := tx.Prepare(`INSERT INTO fts_doc (collection, docid, slug, title, body) VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insDoc.Close()

	post := map[string]*[]posting{}
	totalLen := 0
	for docid, d := range docs {
		terms := tokenize(d.title + "\n" + d.description + "\n" + d.body)
		dl := len(terms)
		totalLen += dl
		freq := make(map[string]int, len(terms))
		for _, t := range terms {
			freq[t]++
		}
		if _, err := insDoc.Exec(x.collection, docid, d.slug, d.title, d.body); err != nil {
			return err
		}
		for term, f := range freq {
			list := post[term]
			if list == nil {
				// tokenize returns slices of one folded document buffer. Clone only vocabulary terms that
				// become long-lived keys, otherwise one short term would retain the whole document.
				term = strings.Clone(term)
				created := make([]posting, 0, 1)
				list = &created
				post[term] = list
			}
			*list = append(*list, posting{doc: docid, tf: f, dl: dl})
		}
	}

	insTerm, err := tx.Prepare(`INSERT INTO fts_term (collection, term, df, doclist) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insTerm.Close()
	for term, list := range post {
		if _, err := insTerm.Exec(x.collection, term, len(*list), encodePostings(*list)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT INTO fts_stat (collection, signature, ndoc, totallen) VALUES (?,?,?,?)
		ON CONFLICT(collection) DO UPDATE SET
			signature = excluded.signature, ndoc = excluded.ndoc, totallen = excluded.totallen`,
		x.collection, signature, len(docs), totalLen); err != nil {
		return err
	}
	return tx.Commit()
}

// rebuildWeighted is rebuild for the schema-DSL door: each document's indexable text arrives as a set
// of weighted fields (a .searchable({ weight }) column). A field's weight multiplies both the term
// frequency it contributes AND its share of the document length, so the BM25 length normalization stays
// consistent and the walkPostings invariant tf <= dl still holds. With every weight = 1 this is exactly
// the pooled behavior of rebuild.
func (x *ftsIndex) rebuildWeighted(docs []SearchDoc, signature string) error {
	tx, err := x.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`DELETE FROM fts_term WHERE collection = ?`, x.collection); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM fts_doc WHERE collection = ?`, x.collection); err != nil {
		return err
	}

	insDoc, err := tx.Prepare(`INSERT INTO fts_doc (collection, docid, slug, title, body) VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insDoc.Close()

	post := map[string]*[]posting{}
	totalLen := 0
	for docid, d := range docs {
		freq := map[string]int{}
		dl := 0
		for _, field := range d.Fields {
			weight := field.Weight
			if weight <= 0 {
				weight = 1
			}
			terms := tokenize(field.Text)
			dl += weight * len(terms)
			for _, t := range terms {
				freq[t] += weight
			}
		}
		totalLen += dl
		if _, err := insDoc.Exec(x.collection, docid, d.Key, d.Title, d.Snippet); err != nil {
			return err
		}
		for term, f := range freq {
			list := post[term]
			if list == nil {
				term = strings.Clone(term)
				created := make([]posting, 0, 1)
				list = &created
				post[term] = list
			}
			*list = append(*list, posting{doc: docid, tf: f, dl: dl})
		}
	}

	insTerm, err := tx.Prepare(`INSERT INTO fts_term (collection, term, df, doclist) VALUES (?,?,?,?)`)
	if err != nil {
		return err
	}
	defer insTerm.Close()
	for term, list := range post {
		if _, err := insTerm.Exec(x.collection, term, len(*list), encodePostings(*list)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec(`INSERT INTO fts_stat (collection, signature, ndoc, totallen) VALUES (?,?,?,?)
		ON CONFLICT(collection) DO UPDATE SET
			signature = excluded.signature, ndoc = excluded.ndoc, totallen = excluded.totallen`,
		x.collection, signature, len(docs), totalLen); err != nil {
		return err
	}
	return tx.Commit()
}

// search intersects every query term, preserving the previous FTS5 implicit-AND contract, then ranks
// matching documents by summed BM25. It reads only the requested posting rows, then hydrates the
// bounded result set with one batched primary-key query.
func (x *ftsIndex) search(queryText string, limit int) ([]ftsHit, error) {
	if len(queryText) > maxSearchQueryBytes {
		return nil, fmt.Errorf("query exceeds %d bytes", maxSearchQueryBytes)
	}
	terms := uniqueTerms(tokenize(queryText))
	if len(terms) == 0 || limit <= 0 {
		return nil, nil
	}
	if len(terms) > maxSearchQueryTerms {
		return nil, fmt.Errorf("query exceeds %d terms", maxSearchQueryTerms)
	}
	if limit > maxSearchResults {
		limit = maxSearchResults
	}

	tx, err := x.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var ndoc, totalLen int
	switch err := tx.QueryRow(`SELECT ndoc, totallen FROM fts_stat WHERE collection = ?`, x.collection).Scan(&ndoc, &totalLen); err {
	case nil:
	case sql.ErrNoRows:
		return nil, nil
	default:
		return nil, err
	}
	if ndoc == 0 || totalLen == 0 {
		return nil, nil
	}
	avgdl := float64(totalLen) / float64(ndoc)

	termStmt, err := tx.Prepare(`SELECT df, doclist FROM fts_term WHERE collection = ? AND term = ?`)
	if err != nil {
		return nil, err
	}
	defer termStmt.Close()

	type termPostings struct {
		term string
		df   int
		blob []byte
	}
	postingLists := make([]termPostings, 0, len(terms))
	for _, t := range terms {
		var df int
		var blob []byte
		switch err := termStmt.QueryRow(x.collection, t).Scan(&df, &blob); err {
		case nil:
			postingLists = append(postingLists, termPostings{term: t, df: df, blob: blob})
		case sql.ErrNoRows:
			// The old FTS5 query joined quoted terms with implicit AND. Preserve that contract: one
			// absent term means no document can satisfy the query.
			return nil, nil
		default:
			return nil, err
		}
	}
	// Seed from the rarest term. Later terms only update documents already in that smallest posting
	// list. Since every posting list is sorted by document id, subsequent terms intersect by a linear
	// merge rather than allocating a map for the corpus.
	sort.Slice(postingLists, func(i, j int) bool { return postingLists[i].df < postingLists[j].df })
	type ranked struct {
		doc   int
		score float64
	}
	order := make([]ranked, 0, postingLists[0].df)
	first := postingLists[0]
	if err := walkPostings(first.blob, first.df, ndoc, avgdl, func(document int, score float64) {
		order = append(order, ranked{doc: document, score: score})
	}); err != nil {
		return nil, fmt.Errorf("term %q: %w", first.term, err)
	}
	for _, postingList := range postingLists[1:] {
		candidate := 0
		write := 0
		err := walkPostings(postingList.blob, postingList.df, ndoc, avgdl, func(document int, score float64) {
			for candidate < len(order) && order[candidate].doc < document {
				candidate++
			}
			if candidate < len(order) && order[candidate].doc == document {
				current := order[candidate]
				current.score += score
				order[write] = current
				write++
				candidate++
			}
		})
		if err != nil {
			return nil, fmt.Errorf("term %q: %w", postingList.term, err)
		}
		order = order[:write]
		if len(order) == 0 {
			return nil, nil
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].score != order[j].score {
			return order[i].score > order[j].score
		}
		return order[i].doc < order[j].doc // stable tiebreak: earlier document first
	})
	if len(order) > limit {
		order = order[:limit]
	}

	termSet := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		termSet[t] = struct{}{}
	}

	var hydrationQuery strings.Builder
	hydrationQuery.WriteString(`SELECT docid, slug, title, body FROM fts_doc WHERE collection = ? AND docid IN (`)
	arguments := make([]any, 0, len(order)+1)
	arguments = append(arguments, x.collection)
	positions := make(map[int]int, len(order))
	for i, rankedDocument := range order {
		if i > 0 {
			hydrationQuery.WriteByte(',')
		}
		hydrationQuery.WriteByte('?')
		arguments = append(arguments, rankedDocument.doc)
		positions[rankedDocument.doc] = i
	}
	hydrationQuery.WriteByte(')')
	rows, err := tx.Query(hydrationQuery.String(), arguments...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type hydratedDocument struct {
		slug  string
		title string
		body  string
		found bool
	}
	documents := make([]hydratedDocument, len(order))
	for rows.Next() {
		var document int
		var hydrated hydratedDocument
		if err := rows.Scan(&document, &hydrated.slug, &hydrated.title, &hydrated.body); err != nil {
			return nil, err
		}
		position, ok := positions[document]
		if !ok {
			return nil, fmt.Errorf("hydrate returned unexpected document %d", document)
		}
		hydrated.found = true
		documents[position] = hydrated
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	hits := make([]ftsHit, 0, len(order))
	for i, rankedDocument := range order {
		document := documents[i]
		if !document.found {
			return nil, fmt.Errorf("hydrate document %d: %w", rankedDocument.doc, sql.ErrNoRows)
		}
		hits = append(hits, ftsHit{
			slug:    document.slug,
			title:   document.title,
			snippet: makeSnippet(document.body, termSet),
			score:   rankedDocument.score,
		})
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return hits, nil
}

// makeSnippet returns a short window of the body around the first query-term match, with matches
// wrapped in <b></b> and elided ends marked with an ellipsis. Falls back to the body head when no
// token matches (a title-only match still deserves context).
func makeSnippet(body string, termSet map[string]struct{}) string {
	scan := scanTokens(body)
	toks := scan.tokens
	if len(toks) == 0 {
		return ""
	}
	const window = 12
	match := -1
	for i, t := range toks {
		term := scan.folded[t.foldedStart:t.foldedEnd]
		if _, ok := termSet[term]; ok {
			match = i
			break
		}
	}
	start, end := 0, len(toks)
	if match >= 0 {
		start = match - 4
		if start < 0 {
			start = 0
		}
	}
	if end > start+window {
		end = start + window
	}

	startByte := toks[start].start
	if start == 0 {
		startByte = 0
	}
	endByte := toks[end-1].end
	if end == len(toks) {
		endByte = len(body)
	}

	var b strings.Builder
	if start > 0 {
		b.WriteString("… ")
	}
	cursor := startByte
	for i := start; i < end; i++ {
		b.WriteString(body[cursor:toks[i].start])
		term := scan.folded[toks[i].foldedStart:toks[i].foldedEnd]
		if _, ok := termSet[term]; ok {
			b.WriteString("<b>")
			b.WriteString(body[toks[i].start:toks[i].end])
			b.WriteString("</b>")
		} else {
			b.WriteString(body[toks[i].start:toks[i].end])
		}
		cursor = toks[i].end
	}
	b.WriteString(body[cursor:endByte])
	if end < len(toks) {
		b.WriteString(" …")
	}
	return b.String()
}
