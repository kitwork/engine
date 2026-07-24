package http

import (
	"time"

	"github.com/kitwork/engine/capabilities"
	httputil "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/utilities/persist"
	"github.com/kitwork/engine/value"
)

type scopeDiskStore struct {
	scope capabilities.Scope
	store *persist.Store
}

func (s scopeDiskStore) Load(key string) (httputil.Snapshot, bool) {
	if s.store == nil {
		return httputil.Snapshot{}, false
	}
	if r, ok := s.store.Get("fetch/" + key); ok {
		return httputil.Snapshot{Status: r.Status, Body: r.Body, ContentType: r.ContentType}, true
	}
	return httputil.Snapshot{}, false
}

func (s scopeDiskStore) LoadStale(key string) (httputil.Snapshot, bool) {
	if s.store == nil {
		return httputil.Snapshot{}, false
	}
	if r, _, ok := s.store.GetStale("fetch/" + key); ok {
		return httputil.Snapshot{Status: r.Status, Body: r.Body, ContentType: r.ContentType}, true
	}
	return httputil.Snapshot{}, false
}

func (s scopeDiskStore) Save(key string, snap httputil.Snapshot, ttl time.Duration) {
	if s.store == nil {
		return
	}
	_ = s.store.Set("fetch/"+key, persist.Record{
		Body: snap.Body, Status: snap.Status, ContentType: snap.ContentType,
	}, ttl)
}

type HTTPAdapter struct {
	scope  capabilities.Scope
	client *httputil.HTTP
}

func NewHTTPAdapter(scope capabilities.Scope) *HTTPAdapter {
	var diskStore httputil.ResponseStore
	if scope != nil {
		pStore := persist.New(scope.ResolvePath(".persist"))
		diskStore = scopeDiskStore{scope: scope, store: pStore}
	}
	return &HTTPAdapter{
		scope:  scope,
		client: httputil.NewClient(nil, diskStore),
	}
}

func (h *HTTPAdapter) Cache(args ...value.Value) *HTTPAdapter {
	h.client.Cache(args...)
	return h
}

func (h *HTTPAdapter) Persist(args ...value.Value) *HTTPAdapter {
	h.client.Persist(args...)
	return h
}

func (h *HTTPAdapter) Get(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "http: url required"}
	}
	url := args[0].Text()
	resp := h.client.Get(url)
	return value.New(resp)
}

func (h *HTTPAdapter) Post(args ...value.Value) value.Value {
	if len(args) == 0 {
		return value.Value{K: value.Invalid, V: "http: url required"}
	}
	urlVal := args[0]
	var bodyVal value.Value
	if len(args) > 1 {
		bodyVal = args[1]
	}
	resp := h.client.Post(urlVal.Text(), bodyVal)
	return value.New(resp)
}

func (h *HTTPAdapter) Fetch(args ...value.Value) value.Value {
	return h.Get(args...)
}

func Register(registry *capabilities.Registry) {
	registry.Register("http", func(scope capabilities.Scope) value.Value {
		return value.New(NewHTTPAdapter(scope))
	})
}

func init() {
	Register(capabilities.DefaultRegistry)
}
