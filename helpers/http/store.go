package http

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/kitwork/engine/value"
)

// Outbound response caching — the same .cache()/.persist() the router speaks, applied to
// fetch()/http.get(). The chain is BUILDER-position (before the verb) because the verb executes
// eagerly:
//
//	http.cache("5m").get(url)                  // RAM, read-through
//	http.persist("1d").get(url)                // disk — survives restarts
//	http.cache("10m").persist("1d").get(url)   // both tiers
//	fetch(url, { cache: "5m", persist: "1d" }) // the fetch spelling of the same thing
//
// Only GET responses are stored (2xx only). STALE-ON-ERROR: when the live request fails and a
// persisted copy exists — even an expired one — that copy is served with `stale: true`, so a page
// never breaks because a third party is down.
//
// The stores are INJECTED by the host per tenant (NewClient); this package stays pure. With no
// store injected, .cache()/.persist() are quiet no-ops.

// Snapshot is the storable form of a response: just status + raw body bytes.
type Snapshot struct {
	Status int
	Body   []byte
}

// ResponseStore is one storage tier. Load returns a FRESH snapshot only; LoadStale may return an
// expired-but-present snapshot (disk tier) for fail-open serving — a RAM tier just returns false.
type ResponseStore interface {
	Load(key string) (Snapshot, bool)
	LoadStale(key string) (Snapshot, bool)
	Save(key string, s Snapshot, ttl time.Duration)
}

// NewClient returns an HTTP builder wired to a tenant's cache tiers (either may be nil).
func NewClient(cacheStore, persistStore ResponseStore) *HTTP {
	return &HTTP{cacheStore: cacheStore, persistStore: persistStore}
}

// Cache opts this request chain into the RAM tier: http.cache("5m").get(url).
// No argument (or true) = no expiry; a number = seconds; a string = duration ("5m", "1h", "1d").
func (h *HTTP) Cache(args ...value.Value) *HTTP {
	h.cacheOn = true
	h.cacheTTL = ttlOf(args...)
	return h
}

// Persist opts this request chain into the DISK tier (survives restarts): http.persist("1d").get(url).
func (h *HTTP) Persist(args ...value.Value) *HTTP {
	h.persistOn = true
	h.persistTTL = ttlOf(args...)
	return h
}

// ttlOf: no arg / true = 0 (forever), number = seconds, string = duration via parseDuration.
func ttlOf(args ...value.Value) time.Duration {
	if len(args) == 0 {
		return 0
	}
	v := args[0]
	if v.IsNumeric() {
		return time.Duration(v.N) * time.Second
	}
	if v.K == value.String {
		if d, err := parseDuration(v.String()); err == nil {
			return d
		}
	}
	return 0
}

// requestKey identifies a GET by URL + the headers the caller set (sorted, so order is irrelevant).
func requestKey(url string, headers map[string]string) string {
	hash := sha256.New()
	hash.Write([]byte(url))
	if len(headers) > 0 {
		keys := make([]string, 0, len(headers))
		for k := range headers {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			hash.Write([]byte("|" + k + ":" + headers[k]))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)[:16])
}

// storedResponse rebuilds a Response from a snapshot, flagged with its provenance.
func storedResponse(s Snapshot, stale bool) value.Value {
	return value.New(Response{Status: s.Status, Body: value.New(s.Body), Cached: true, Stale: stale})
}
