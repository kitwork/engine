package http

import (
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// memStore is a test double for one tier: fresh map + a stale set for LoadStale.
type memStore struct {
	mu    sync.Mutex
	fresh map[string]Snapshot
	stale map[string]Snapshot
}

func newMemStore() *memStore {
	return &memStore{fresh: map[string]Snapshot{}, stale: map[string]Snapshot{}}
}
func (m *memStore) Load(key string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.fresh[key]
	return s, ok
}
func (m *memStore) LoadStale(key string) (Snapshot, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.fresh[key]; ok {
		return s, true
	}
	s, ok := m.stale[key]
	return s, ok
}
func (m *memStore) Save(key string, s Snapshot, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.fresh[key] = s
}

func respOf(t *testing.T, v value.Value) Response {
	t.Helper()
	// .get()/.post() are now lazy — they hand back a *Request that fires on demand.
	if req, ok := v.V.(*Request); ok {
		return req.Fire()
	}
	if r, ok := v.Interface().(Response); ok {
		return r
	}
	t.Fatalf("not a Response or *Request: %#v", v)
	return Response{}
}

func TestCacheReadThrough(t *testing.T) {
	savedLocal := IsLocalAllowed
	IsLocalAllowed = func() bool { return true } // httptest is loopback
	defer func() { IsLocalAllowed = savedLocal }()

	hits := 0
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		hits++
		fmt.Fprintf(w, `{"n":%d}`, hits)
	}))
	defer server.Close()

	ram := newMemStore()
	// First call goes to the network and stores; second is served from RAM.
	r1 := respOf(t, NewClient(ram, nil).Cache(value.New("5m")).Get(server.URL))
	r2 := respOf(t, NewClient(ram, nil).Cache(value.New("5m")).Get(server.URL))
	if hits != 1 {
		t.Fatalf("network hits = %d, want 1 (read-through)", hits)
	}
	if r1.Cached || !r2.Cached {
		t.Errorf("cached flags: first=%v second=%v, want false/true", r1.Cached, r2.Cached)
	}
	if r2.Text() != `{"n":1}` {
		t.Errorf("second body = %q, want the cached first body", r2.Text())
	}

	// A different URL is a different key.
	respOf(t, NewClient(ram, nil).Cache(value.New("5m")).Get(server.URL+"/other"))
	if hits != 2 {
		t.Errorf("distinct URL should miss the cache, hits = %d", hits)
	}

	// POST is never cached.
	respOf(t, NewClient(ram, nil).Cache(value.New("5m")).Post(server.URL, value.New("x")))
	respOf(t, NewClient(ram, nil).Cache(value.New("5m")).Post(server.URL, value.New("x")))
	if hits != 4 {
		t.Errorf("POST must not cache, hits = %d, want 4", hits)
	}
}

func TestPersistStaleOnError(t *testing.T) {
	savedLocal := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = savedLocal }()

	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		fmt.Fprint(w, "live-data")
	}))
	url := server.URL

	disk := newMemStore()
	r1 := respOf(t, NewClient(nil, disk).Persist(value.New("1h")).Get(url))
	if r1.Text() != "live-data" || r1.Cached {
		t.Fatalf("first fetch should be live: %+v", r1)
	}

	// The third party dies; the persisted copy is EXPIRED (moved to the stale set) — the client
	// must still serve it, flagged stale.
	server.Close()
	key := requestKey(url, nil)
	disk.mu.Lock()
	disk.stale[key] = disk.fresh[key]
	delete(disk.fresh, key)
	disk.mu.Unlock()

	r2 := respOf(t, NewClient(nil, disk).Persist(value.New("1h")).Get(url))
	if !r2.Stale || !r2.Cached || r2.Text() != "live-data" {
		t.Errorf("stale-on-error: got %+v, want stale copy of live-data", r2)
	}
}

func TestNoStoreIsQuietNoop(t *testing.T) {
	savedLocal := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = savedLocal }()

	hits := 0
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		hits++
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	// .cache() without an injected store must not break the request (tenantless builtin path).
	h := &HTTP{}
	r := respOf(t, h.Cache(value.New("5m")).Get(server.URL))
	if !r.Ok() || r.Text() != "ok" {
		t.Errorf("no-store chain broke the request: %+v", r)
	}
}

func TestFetchWithOptions(t *testing.T) {
	savedLocal := IsLocalAllowed
	IsLocalAllowed = func() bool { return true }
	defer func() { IsLocalAllowed = savedLocal }()

	hits := 0
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		hits++
		fmt.Fprint(w, "fetched")
	}))
	defer server.Close()

	ram := newMemStore()
	opts := value.New(map[string]value.Value{"cache": value.New("5m")})
	// fetch is lazy now — fire the first call (respOf) so it populates the cache tier.
	respOf(t, FetchWith(NewClient(ram, nil), value.New(server.URL), opts))
	r2 := respOf(t, FetchWith(NewClient(ram, nil), value.New(server.URL), opts))
	if hits != 1 || !r2.Cached {
		t.Errorf("fetch {cache} option: hits=%d cached=%v, want 1/true", hits, r2.Cached)
	}
	if !strings.Contains(r2.Text(), "fetched") {
		t.Errorf("body = %q", r2.Text())
	}
}
