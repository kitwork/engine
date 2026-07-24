package work

import (
	"time"

	"github.com/kitwork/engine/utilities/cache"
	httputil "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/utilities/persist"
)

var AllowLocal bool
var ServerPort int

type HTTP = httputil.HTTP
type HTTPResponse = httputil.Response

func (w *KitWork) HTTP() *HTTP {
	if w == nil || w.tenant == nil {
		return httputil.NewClient(nil, nil)
	}
	return httputil.NewClient(w.tenant.fetchRAM(), w.tenant.fetchDisk())
}

func (t *Tenant) fetchRAM() httputil.ResponseStore  { return fetchRAMStore{t} }
func (t *Tenant) fetchDisk() httputil.ResponseStore { return fetchDiskStore{t} }

type fetchRAMStore struct{ t *Tenant }

func (s fetchRAMStore) Load(key string) (httputil.Snapshot, bool) {
	if s.t == nil || s.t.respCache == nil {
		return httputil.Snapshot{}, false
	}
	if e, ok := s.t.respCache.Get("fetch|" + key); ok {
		return httputil.Snapshot{Status: e.Status, Body: e.Body, ContentType: e.ContentType}, true
	}
	return httputil.Snapshot{}, false
}
func (s fetchRAMStore) LoadStale(string) (httputil.Snapshot, bool) { return httputil.Snapshot{}, false }
func (s fetchRAMStore) Save(key string, snap httputil.Snapshot, ttl time.Duration) {
	if s.t == nil || s.t.respCache == nil {
		return
	}
	s.t.respCache.Set("fetch|"+key, cache.Entry{
		Body: snap.Body, Status: snap.Status, ContentType: snap.ContentType,
	}, ttl)
}

type fetchDiskStore struct{ t *Tenant }

func (s fetchDiskStore) Load(key string) (httputil.Snapshot, bool) {
	if s.t == nil || s.t.persistStore == nil {
		return httputil.Snapshot{}, false
	}
	if r, ok := s.t.persistStore.Get("fetch/" + key); ok {
		return httputil.Snapshot{Status: r.Status, Body: r.Body, ContentType: r.ContentType}, true
	}
	return httputil.Snapshot{}, false
}
func (s fetchDiskStore) LoadStale(key string) (httputil.Snapshot, bool) {
	if s.t == nil || s.t.persistStore == nil {
		return httputil.Snapshot{}, false
	}
	if r, _, ok := s.t.persistStore.GetStale("fetch/" + key); ok {
		return httputil.Snapshot{Status: r.Status, Body: r.Body, ContentType: r.ContentType}, true
	}
	return httputil.Snapshot{}, false
}
func (s fetchDiskStore) Save(key string, snap httputil.Snapshot, ttl time.Duration) {
	if s.t == nil || s.t.persistStore == nil {
		return
	}
	_ = s.t.persistStore.Set("fetch/"+key, persist.Record{
		Body: snap.Body, Status: snap.Status, ContentType: snap.ContentType,
	}, ttl)
}
