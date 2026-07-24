package collection

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kitwork/engine/capabilities"
	collectionhelper "github.com/kitwork/engine/utilities/collection"
	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/value"
)

type collectionCapabilityStore struct {
	scope capabilities.Scope
	store *persist.Store
}

func newCollectionStore(scope capabilities.Scope) collectionCapabilityStore {
	persistDir := scope.ResolvePath(".persist")
	return collectionCapabilityStore{
		scope: scope,
		store: persist.New(persistDir),
	}
}

func (s collectionCapabilityStore) Load(key string) ([]byte, bool) {
	if s.store == nil {
		return nil, false
	}
	if r, ok := s.store.Get("collection/" + key); ok {
		return r.Body, true
	}
	return nil, false
}

func (s collectionCapabilityStore) Save(key string, body []byte, ttl time.Duration) error {
	if s.store == nil {
		return nil
	}
	return s.store.Set("collection/"+key, persist.Record{Body: body}, ttl)
}

// Manager provides tenant-scoped directory-backed markdown/frontmatter document collections.
type Manager struct {
	store capabilitiesStore
	scope capabilities.Scope
	err   error

	ftsMu  sync.Mutex
	ftsMap map[string]string
}

type capabilitiesStore interface {
	Open(name string) (*collectionhelper.Collection, error)
}

type Handle struct {
	manager    *Manager
	collection *collectionhelper.Collection
	scope      capabilities.Scope
}

type CollectionQuery struct {
	handle *Handle
	spec   collectionhelper.Query
}

func NewManager(scope capabilities.Scope) *Manager {
	cs := newCollectionStore(scope)
	store, err := collectionhelper.NewStore(scope.ResolvePath(), cs)
	return &Manager{
		store:  store,
		scope:  scope,
		err:    err,
		ftsMap: make(map[string]string),
	}
}

func (m *Manager) Open(args ...value.Value) value.Value {
	if m.err != nil {
		return collectionInvalid(m.err)
	}
	if len(args) == 0 || args[0].String() == "" {
		return value.Value{K: value.Invalid, V: "collection: folder is required"}
	}
	name := args[0].String()
	if !strings.ContainsAny(name, `/\`) {
		if opened, err := m.store.Open("_collection/" + name); err == nil {
			return value.New(&Handle{manager: m, collection: opened, scope: m.scope})
		}
	}
	opened, err := m.store.Open(name)
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(&Handle{manager: m, collection: opened, scope: m.scope})
}

func (h *Handle) Cache(args ...value.Value) *Handle {
	if len(args) > 0 && args[0].K == value.Bool && !args[0].Truthy() {
		h.collection.SetCache(false, 0)
		return h
	}
	h.collection.SetCache(true, collectionTTL(args...))
	return h
}

func (h *Handle) Persist(args ...value.Value) *Handle {
	if len(args) > 0 && args[0].K == value.Bool && !args[0].Truthy() {
		h.collection.SetPersist(false, 0)
		return h
	}
	h.collection.SetPersist(true, collectionTTL(args...))
	return h
}

func (h *Handle) Path() value.Value {
	return value.New(h.collection.Path())
}

func (h *Handle) List() value.Value {
	files, err := h.collection.List()
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(files)
}

func (h *Handle) Index() value.Value {
	index, err := h.collection.Index()
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(index)
}

func (h *Handle) All() value.Value { return h.Index() }

func (h *Handle) Read(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "collection: document slug is required"}
	}
	document, err := h.collection.Read(args[0].String())
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(document)
}

func (h *Handle) Find(args ...value.Value) value.Value { return h.Read(args...) }

func (h *Handle) Raw(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "collection: document slug is required"}
	}
	document, err := h.collection.Read(args[0].String())
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(document.Body)
}

func (h *Handle) Prewarm(args ...value.Value) value.Value {
	mode := "index"
	if len(args) > 0 && args[0].String() != "" {
		mode = args[0].String()
	}
	count, err := h.collection.Prewarm(mode)
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(count)
}

func (h *Handle) Where(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).Where(args...)
}

func (h *Handle) OrderBy(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).OrderBy(args...)
}

func (h *Handle) Limit(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).Limit(args...)
}

func (h *Handle) Skip(args ...value.Value) *CollectionQuery {
	return (&CollectionQuery{handle: h}).Skip(args...)
}

func (h *Handle) Search(args ...value.Value) value.Value {
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

	db := h.scope.DB("collection.db")
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

func (h *Handle) syncFTS(db *sql.DB, index []collectionhelper.IndexEntry) error {
	key := h.collection.Path()
	dirSig := collectionDirSignature(index)

	h.manager.ftsMu.Lock()
	defer h.manager.ftsMu.Unlock()
	if h.manager.ftsMap[key] == dirSig {
		return nil
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

	live := make(map[string]bool, len(index))
	for _, entry := range index {
		slug := entry.File.Slug
		live[slug] = true
		sig := entry.File.Signature()
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

	for slug := range known {
		if !live[slug] {
			db.Exec(`DELETE FROM docs WHERE collection = ? AND slug = ?`, key, slug)
			db.Exec(`DELETE FROM doc_state WHERE collection = ? AND slug = ?`, key, slug)
		}
	}

	h.manager.ftsMap[key] = dirSig
	return nil
}

func collectionDirSignature(index []collectionhelper.IndexEntry) string {
	hash := sha256.New()
	for _, entry := range index {
		fmt.Fprintf(hash, "%s|%s\n", entry.File.Slug, entry.File.Signature())
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

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

func (cq *CollectionQuery) Where(args ...value.Value) *CollectionQuery {
	switch len(args) {
	case 2:
		cq.spec.Filters = append(cq.spec.Filters, collectionhelper.Filter{
			Field: args[0].String(), Op: "=", Value: args[1].Interface(),
		})
	case 3:
		cq.spec.Filters = append(cq.spec.Filters, collectionhelper.Filter{
			Field: args[0].String(), Op: args[1].String(), Value: args[2].Interface(),
		})
	}
	return cq
}

func (cq *CollectionQuery) OrderBy(args ...value.Value) *CollectionQuery {
	if len(args) > 0 {
		cq.spec.OrderField = args[0].String()
	}
	cq.spec.OrderDesc = len(args) > 1 && args[1].String() == "desc"
	return cq
}

func (cq *CollectionQuery) Limit(args ...value.Value) *CollectionQuery {
	if len(args) > 0 {
		cq.spec.LimitN = int(args[0].N)
	}
	return cq
}

func (cq *CollectionQuery) Skip(args ...value.Value) *CollectionQuery {
	if len(args) > 0 {
		cq.spec.SkipN = int(args[0].N)
	}
	return cq
}

func (cq *CollectionQuery) All() value.Value {
	entries, err := cq.run()
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(entries)
}

func (cq *CollectionQuery) First() value.Value {
	entries, err := cq.run()
	if err != nil {
		return collectionInvalid(err)
	}
	if len(entries) == 0 {
		return value.Value{K: value.Nil}
	}
	return collectionValue(entries[0])
}

func (cq *CollectionQuery) Count() value.Value {
	entries, err := cq.run()
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(len(entries))
}

func (cq *CollectionQuery) run() ([]collectionhelper.IndexEntry, error) {
	index, err := cq.handle.collection.Index()
	if err != nil {
		return nil, err
	}
	return cq.spec.Apply(index), nil
}

func collectionTTL(args ...value.Value) time.Duration {
	if len(args) == 0 {
		return 0
	}
	return parseTTL(args[0])
}

func parseTTL(v value.Value) time.Duration {
	if v.K == value.Number {
		return time.Duration(v.N) * time.Second
	}
	if v.K == value.String {
		if d, err := time.ParseDuration(v.Text()); err == nil {
			return d
		}
	}
	return 0
}

func collectionValue(input any) value.Value {
	body, _ := json.Marshal(input)
	var decoded any
	_ = json.Unmarshal(body, &decoded)
	return value.New(decoded)
}

func collectionInvalid(err error) value.Value {
	return value.Value{K: value.Invalid, V: err.Error()}
}

func Register(registry *capabilities.Registry) {
	registry.Register("collection", func(scope capabilities.Scope) value.Value {
		return value.New(NewManager(scope))
	})
}

func init() {
	Register(capabilities.DefaultRegistry)
}
