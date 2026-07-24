package collection

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/kitwork/engine/capabilities"
	collectionhelper "github.com/kitwork/engine/utilities/collection"
	"github.com/kitwork/engine/value"
)

type collectionCapabilityStore struct {
	scope capabilities.Scope
}

func (s collectionCapabilityStore) Load(key string) ([]byte, bool) {
	return nil, false
}

func (s collectionCapabilityStore) Save(key string, body []byte, ttl time.Duration) error {
	return nil
}

// Manager provides tenant-scoped directory-backed markdown/frontmatter document collections.
type Manager struct {
	store *collectionhelper.Store
	scope capabilities.Scope
	err   error
}

type Handle struct {
	collection *collectionhelper.Collection
	scope      capabilities.Scope
}

func NewManager(scope capabilities.Scope) *Manager {
	store, err := collectionhelper.NewStore(scope.ResolvePath(), collectionCapabilityStore{scope: scope})
	return &Manager{store: store, scope: scope, err: err}
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
			return value.New(&Handle{collection: opened, scope: m.scope})
		}
	}
	opened, err := m.store.Open(name)
	if err != nil {
		return collectionInvalid(err)
	}
	return value.New(&Handle{collection: opened, scope: m.scope})
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

func init() {
	capabilities.DefaultRegistry.Register("collection", func(scope capabilities.Scope) value.Value {
		return value.New(NewManager(scope))
	})
}
