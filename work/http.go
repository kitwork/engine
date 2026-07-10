package work

import (
	"time"

	"github.com/kitwork/engine/helpers/cache"
	"github.com/kitwork/engine/helpers/http"
	"github.com/kitwork/engine/helpers/persist"
)

var AllowLocal bool
var ServerPort int

type HTTP = http.HTTP
type HTTPResponse = http.Response

// HTTP hands JS a client wired to THIS tenant's cache tiers, so
// http.cache("5m").persist("1d").get(url) lands in the tenant's own RAM store and
// <tenant>/.persist/fetch/ — never another tenant's.
func (w *KitWork) HTTP() *HTTP {
	return http.NewClient(w.tenant.fetchRAM(), w.tenant.fetchDisk())
}

// fetchRAM / fetchDisk adapt the tenant's stores to the pure http.ResponseStore interface.
// Keys are namespaced so outbound fetches never collide with route .cache()/.persist() entries.
func (t *Tenant) fetchRAM() http.ResponseStore  { return fetchRAMStore{t} }
func (t *Tenant) fetchDisk() http.ResponseStore { return fetchDiskStore{t} }

type fetchRAMStore struct{ t *Tenant }

func (s fetchRAMStore) Load(key string) (http.Snapshot, bool) {
	if e, ok := s.t.respCache.Get("fetch|" + key); ok {
		return http.Snapshot{Status: e.Status, Body: e.Body}, true
	}
	return http.Snapshot{}, false
}
func (s fetchRAMStore) LoadStale(string) (http.Snapshot, bool) { return http.Snapshot{}, false }
func (s fetchRAMStore) Save(key string, snap http.Snapshot, ttl time.Duration) {
	s.t.respCache.Set("fetch|"+key, cache.Entry{Body: snap.Body, Status: snap.Status}, ttl)
}

type fetchDiskStore struct{ t *Tenant }

func (s fetchDiskStore) Load(key string) (http.Snapshot, bool) {
	if r, ok := s.t.persistStore.Get("fetch/" + key); ok {
		return http.Snapshot{Status: r.Status, Body: r.Body}, true
	}
	return http.Snapshot{}, false
}
func (s fetchDiskStore) LoadStale(key string) (http.Snapshot, bool) {
	if r, _, ok := s.t.persistStore.GetStale("fetch/" + key); ok {
		return http.Snapshot{Status: r.Status, Body: r.Body}, true
	}
	return http.Snapshot{}, false
}
func (s fetchDiskStore) Save(key string, snap http.Snapshot, ttl time.Duration) {
	_ = s.t.persistStore.Set("fetch/"+key, persist.Record{Body: snap.Body, Status: snap.Status}, ttl)
}
