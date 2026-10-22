package http

import (
	"fmt"
	stdhttp "net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

// memStore is a test double for one tier: fresh map + a stale set for LoadStale, and the expiry each
// save asked for.
type memStore struct {
	mu    sync.Mutex
	fresh map[string]Snapshot
	stale map[string]Snapshot
	ttls  map[string]time.Duration
}

func newMemStore() *memStore {
	return &memStore{fresh: map[string]Snapshot{}, stale: map[string]Snapshot{}, ttls: map[string]time.Duration{}}
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
	m.ttls[key] = ttl
}
func (m *memStore) size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.fresh)
}

// expire moves every fresh copy to the stale set, as time passing would.
func (m *memStore) expire() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, s := range m.fresh {
		m.stale[key] = s
		delete(m.fresh, key)
	}
}

// RAM read-through: the first send goes to the network and stores an ok answer, the second is served
// from RAM, flagged cached, with the stored Content-Type as its header. The key is the final URL (query
// included) and the headers, so a different query or token is another entry.
func TestCacheReadThrough(t *testing.T) {
	openLoopback(t)
	var hits int32
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		n := atomic.AddInt32(&hits, 1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"n":%d}`, n)
	}))
	defer server.Close()

	ram := newMemStore()
	module := value.New(NewModule(ram, nil, ""))
	send := func(query map[string]any, token string) value.Value {
		request := invoke(invoke(module, "get", server.URL), "cache", "5m")
		if query != nil {
			request = invoke(request, "query", query)
		}
		if token != "" {
			request = invoke(request, "header", "Authorization", token)
		}
		return invoke(request, "send")
	}

	first, second := send(nil, ""), send(nil, "")
	if hits != 1 {
		t.Fatalf("network hits = %d, want 1 (read-through)", hits)
	}
	if first.Get("cached").Truthy() || !second.Get("cached").Truthy() || second.Get("stale").Truthy() {
		t.Errorf("cached: first %v second %v, want false then true", first.Get("cached").Truthy(), second.Get("cached").Truthy())
	}
	if got := invoke(second, "json").Get("n"); got.Int() != 1 {
		t.Errorf("the cached body = %s, want the first", invoke(second, "text").Text())
	}
	if got := second.Get("headers").Get("content-type"); got.Text() != "application/json" {
		t.Errorf("a cached result's content-type = %s", got.Text())
	}
	for _, ttl := range ram.ttls {
		if ttl != 5*time.Minute {
			t.Errorf(`.cache("5m") stored for %s`, ttl)
		}
	}

	send(map[string]any{"page": 2}, "")
	send(nil, "Bearer a")
	send(nil, "Bearer b")
	if hits != 4 {
		t.Errorf("another query or token should miss: hits = %d, want 4", hits)
	}
}

// Only an ok answer is stored: a 404 is sent each time.
func TestCacheKeepsOnlyOkAnswers(t *testing.T) {
	openLoopback(t)
	var hits int32
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(stdhttp.StatusNotFound)
	}))
	defer server.Close()
	ram := newMemStore()
	module := value.New(NewModule(ram, nil, ""))
	for i := 0; i < 2; i++ {
		invoke(invoke(invoke(module, "get", server.URL), "cache", "5m"), "send")
	}
	if hits != 2 || ram.size() != 0 {
		t.Fatalf("a 404 with .cache(): hits %d, stored %d, want 2 and 0", hits, ram.size())
	}
}

// Disk: a hit refills RAM; when the live request fails (no answer or a 5xx) an expired disk copy is the
// result, flagged stale.
func TestPersistRefillsRAMAndServesStaleOnFailure(t *testing.T) {
	openLoopback(t)
	var failing atomic.Bool
	server := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if failing.Load() {
			w.WriteHeader(stdhttp.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, "live-data")
	}))
	url := server.URL

	ram, disk := newMemStore(), newMemStore()
	module := value.New(NewModule(ram, disk, ""))
	send := func() value.Value {
		return invoke(invoke(invoke(invoke(module, "get", url), "cache", "1m"), "persist", "1h"), "send")
	}
	if first := send(); invoke(first, "text").Text() != "live-data" || first.Get("cached").Truthy() {
		t.Fatalf("the first send should be live: %s", readOutcome(first))
	}
	if disk.ttls[requestKey(url, stdhttp.Header{})] != time.Hour {
		t.Errorf(`.persist("1h") stored for %v`, disk.ttls)
	}

	// RAM empty (a restart): the disk copy answers and goes back into RAM
	ram = newMemStore()
	module = value.New(NewModule(ram, disk, ""))
	if again := send(); !again.Get("cached").Truthy() || ram.size() != 1 {
		t.Fatalf("a disk hit: cached %v, RAM holds %d, want true and 1", again.Get("cached").Truthy(), ram.size())
	}

	// the copies expire and the upstream answers 503: the expired disk copy is the result, stale
	ram.expire()
	disk.expire()
	failing.Store(true)
	if stale := send(); !stale.Get("ok").Truthy() || !stale.Get("stale").Truthy() || invoke(stale, "text").Text() != "live-data" {
		t.Errorf("stale on a 503: %s stale %v", readOutcome(stale), stale.Get("stale").Truthy())
	}
	// and when nothing answers at all
	server.Close()
	if stale := send(); !stale.Get("stale").Truthy() || invoke(stale, "text").Text() != "live-data" {
		t.Errorf("stale without an answer: %s", readOutcome(stale))
	}
}

// .cache() and .persist() are for GET; their expiry reads as a route's: a number is milliseconds, an
// unreadable one is an error, never "forever".
func TestCacheModifierRules(t *testing.T) {
	ram := newMemStore()
	module := value.New(NewModule(ram, nil, ""))
	for _, method := range []string{"post", "put", "delete", "head"} {
		for _, modifier := range []string{"cache", "persist"} {
			wantFailure(t, method+"."+modifier, invoke(invoke(module, method, "https://api.example.test"), modifier, "5m"),
				value.InvalidArgumentCode, "."+modifier+"() chỉ cho GET")
		}
	}
	for _, expiry := range []any{"soon", "hourly", true, -5} {
		wantFailure(t, fmt.Sprintf(".cache(%v)", expiry), invoke(invoke(module, "get", "https://api.example.test"), "cache", expiry),
			value.InvalidArgumentCode, "không đọc được hạn", `"5m"`)
	}
	request := invoke(invoke(module, "get", "https://api.example.test"), "cache", 1500).V.(*Request)
	if request.cacheTTL != 1500*time.Millisecond {
		t.Errorf(".cache(1500) = %s, want 1.5s (a number is milliseconds)", request.cacheTTL)
	}
	if request := invoke(invoke(module, "get", "https://api.example.test"), "cache").V.(*Request); !request.cache || request.cacheTTL != 0 {
		t.Errorf(".cache() = %v %s, want no expiry", request.cache, request.cacheTTL)
	}
}
