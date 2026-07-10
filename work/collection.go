package work

import (
	"encoding/json"
	"time"

	collectionhelper "github.com/kitwork/engine/helpers/collection"
	"github.com/kitwork/engine/helpers/persist"
	"github.com/kitwork/engine/value"
)

// CollectionManager is the tenant-scoped entry point exposed as collection in Kitwork JS.
type CollectionManager struct {
	store *collectionhelper.Store
	err   error
}

// CollectionHandle is one opened directory-backed collection.
type CollectionHandle struct {
	collection *collectionhelper.Collection
}

// Collection returns a manager backed by one shared per-tenant Store, so parsed documents survive
// across requests while remaining isolated from every other tenant.
func (w *KitWork) Collection() *CollectionManager {
	t := w.tenant
	t.collectionMu.Lock()
	defer t.collectionMu.Unlock()
	if t.collectionStore == nil {
		t.collectionStore, t.collectionErr = collectionhelper.NewStore(t.resolve(), collectionDiskStore{t: t})
	}
	return &CollectionManager{store: t.collectionStore, err: t.collectionErr}
}

func (m *CollectionManager) Open(args ...value.Value) value.Value {
	if m.err != nil {
		return collectionInvalid(m.err)
	}
	if len(args) == 0 || args[0].String() == "" {
		return value.Value{K: value.Invalid, V: "collection: folder is required"}
	}
	opened, err := m.store.Open(args[0].String())
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(&CollectionHandle{collection: opened})
}

// Cache keeps the automatic signature-aware RAM tier enabled. The optional duration controls idle
// eviction, never freshness: a changed mtime/size invalidates immediately.
func (h *CollectionHandle) Cache(args ...value.Value) *CollectionHandle {
	if len(args) > 0 && args[0].K == value.Bool && !args[0].Truthy() {
		h.collection.SetCache(false, 0)
		return h
	}
	h.collection.SetCache(true, collectionTTL(args...))
	return h
}

// Persist opts into the per-tenant .persist/collection disk tier.
func (h *CollectionHandle) Persist(args ...value.Value) *CollectionHandle {
	if len(args) > 0 && args[0].K == value.Bool && !args[0].Truthy() {
		h.collection.SetPersist(false, 0)
		return h
	}
	h.collection.SetPersist(true, collectionTTL(args...))
	return h
}

func (h *CollectionHandle) Path() value.Value {
	return value.New(h.collection.Path())
}

func (h *CollectionHandle) List() value.Value {
	files, err := h.collection.List()
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(files)
}

func (h *CollectionHandle) Index() value.Value {
	index, err := h.collection.Index()
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(index)
}

// All is the collection vocabulary alias for Index.
func (h *CollectionHandle) All() value.Value { return h.Index() }

func (h *CollectionHandle) Read(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "collection: document slug is required"}
	}
	document, err := h.collection.Read(args[0].String())
	if err != nil {
		return collectionInvalid(err)
	}
	return collectionValue(document)
}

// Find is the collection vocabulary alias for Read.
func (h *CollectionHandle) Find(args ...value.Value) value.Value { return h.Read(args...) }

func (h *CollectionHandle) Raw(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "collection: document slug is required"}
	}
	document, err := h.collection.Read(args[0].String())
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(document.Body)
}

func (h *CollectionHandle) Prewarm(args ...value.Value) value.Value {
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

func collectionTTL(args ...value.Value) time.Duration {
	if len(args) == 0 {
		return 0
	}
	return parseTTL(args[0])
}

func collectionValue(input any) value.Value {
	body, err := json.Marshal(input)
	if err != nil {
		return collectionInvalid(err)
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return collectionInvalid(err)
	}
	return value.New(decoded)
}

func collectionInvalid(err error) value.Value {
	return value.Value{K: value.Invalid, V: err.Error()}
}

type collectionDiskStore struct{ t *Tenant }

func (s collectionDiskStore) Load(key string) ([]byte, bool) {
	if s.t.persistStore == nil {
		return nil, false
	}
	record, ok := s.t.persistStore.Get("collection/" + key)
	return record.Body, ok
}

func (s collectionDiskStore) Save(key string, body []byte, ttl time.Duration) error {
	if s.t.persistStore == nil {
		return nil
	}
	return s.t.persistStore.Set("collection/"+key, persist.Record{Body: body}, ttl)
}
