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

	ftsMu    sync.Mutex
	ftsReady bool
	ftsMap   map[string]string

	segmentCanary *segmentSearchCanary
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
	var telemetry *SegmentSearchCanaryTelemetry
	if provider, ok := scope.(collectionSegmentTelemetryScope); ok {
		telemetry = provider.CollectionSearchCanaryTelemetry()
	}
	manager := &Manager{
		store:         store,
		scope:         scope,
		err:           err,
		ftsMap:        make(map[string]string),
		segmentCanary: newSegmentSearchCanary(telemetry),
	}
	if provider, ok := scope.(collectionSegmentCanaryScope); ok && provider.CollectionSearchCanaryEnabled() {
		manager.EnableSegmentSearchCanary(true)
	}
	return manager
}

// Close stops generation-owned shadow work. The serving SQLite projection has
// no manager-owned resource to close here.
func (m *Manager) Close() error {
	if m != nil && m.segmentCanary != nil {
		m.segmentCanary.close()
	}
	return nil
}

func (m *Manager) Open(args ...value.Value) value.Value {
	if m.err != nil {
		return collectionInvalid(m.err)
	}
	if len(args) == 0 || args[0].String() == "" {
		return value.Value{K: value.Invalid, V: "collection: folder is required"}
	}
	name := args[0].String()
	targetPath := name
	if !strings.ContainsAny(name, `/\`) {
		targetPath = "_collection/" + name
	}

	if m.scope != nil {
		if _, err := capabilities.CleanPath(m.scope.ResolvePath(), targetPath); err != nil {
			return collectionInvalid(err)
		}
	}

	opened, err := m.store.Open(targetPath)
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

	idx := &ftsIndex{db: db, collection: h.collection.Path()}
	hits, err := idx.search(text, limit)
	if err != nil {
		return collectionInvalid(fmt.Errorf("collection: search: %w", err))
	}
	h.runSegmentSearchCanary(collectionDirSignature(index), text, limit, hits)

	metaBySlug := make(map[string]map[string]any, len(index))
	for _, entry := range index {
		metaBySlug[entry.File.Slug] = entry.Meta
	}

	results := make([]map[string]any, 0, len(hits))
	for _, hit := range hits {
		results = append(results, map[string]any{
			"slug": hit.slug, "title": hit.title, "snippet": hit.snippet, "score": hit.score,
			"meta": metaBySlug[hit.slug],
		})
	}
	return collectionValue(results)
}

func (h *Handle) syncFTS(db *sql.DB, index []collectionhelper.IndexEntry) error {
	key := h.collection.Path()
	dirSig := collectionDirSignature(index)

	h.manager.ftsMu.Lock()
	defer h.manager.ftsMu.Unlock()

	searchIndex := &ftsIndex{db: db, collection: key}
	if !h.manager.ftsReady {
		if err := searchIndex.ensureSchema(); err != nil {
			return fmt.Errorf("collection: search schema: %w", err)
		}
		h.manager.ftsReady = true
	}
	if h.manager.ftsMap[key] == dirSig {
		return nil
	}
	persistedSignature, err := searchIndex.signature()
	if err != nil {
		return err
	}
	if persistedSignature == dirSig {
		h.manager.ftsMap[key] = dirSig
		return nil
	}

	documents, complete := h.searchSources(index)

	committedSignature := dirSig
	if !complete {
		// Keep a usable partial projection, but leave it dirty so the next search retries files that
		// disappeared or became unreadable between Index and Read.
		committedSignature = ""
	}
	if err := searchIndex.rebuild(documents, committedSignature); err != nil {
		return fmt.Errorf("collection: rebuild search index: %w", err)
	}

	if complete {
		h.manager.ftsMap[key] = dirSig
	} else {
		delete(h.manager.ftsMap, key)
	}
	return nil
}

func collectionDirSignature(index []collectionhelper.IndexEntry) string {
	hash := sha256.New()
	for _, entry := range index {
		fmt.Fprintf(hash, "%s|%s\n", entry.File.Slug, entry.File.Signature())
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
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
