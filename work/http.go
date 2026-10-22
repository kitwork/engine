package work

import (
	"time"

	"github.com/kitwork/engine/utilities/cache"
	httputil "github.com/kitwork/engine/utilities/http"
	"github.com/kitwork/engine/utilities/persist"
)

var AllowLocal bool

// Minify is MINIFY=true of the host env, set once at boot: templates and the head of a .ui page are
// minified as in production while AllowLocal stays true (the engine's minifies). Only minification:
// what else developing means (a .ui page's warnings, a 500's diagnostic) still follows AllowLocal.
var Minify bool

// NativeShell is true when this process serves an app that declared app.desktop() or app.mobile().
// Only then can a page reach a WebView handle, so only then is it worth sending the bridge and the
// capability proxies that ride it. It is a property of the PROCESS, not of one site: the shell is
// what the app was launched into.
var NativeShell bool
var ServerPort int

// HTTP is the http module of this site (KITWORK_HTTP.md): `import * as http from "kitwork/http"`, and
// the http of `import { http } from "kitwork"`, the same object. It is given both cache tiers of the
// site, and the site's domain as the Host of a self-call ("/path"), so the call reaches this site.
func (w *KitWork) HTTP() *httputil.Module {
	if w == nil || w.tenant == nil {
		return httputil.NewModule(nil, nil, "")
	}
	domain := ""
	if w.tenant.entity != nil {
		domain = w.tenant.entity.Domain
	}
	return httputil.NewModule(w.tenant.fetchRAM(), w.tenant.fetchDisk(), domain)
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
