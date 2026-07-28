package ratelimit

import (
	"strconv"
	"sync"
	"testing"
	"time"
)

// Allow runs on the request hot path — once per rule, and a folder chain can carry several. It
// takes ONE mutex for the whole limiter, and holds it across time.Now() plus a map lookup. These
// benchmarks measure what that costs as concurrency rises, which is the question that matters for
// "many tenants, many requests": a single lock in front of every request is a throughput ceiling
// no amount of tenant isolation can lift.

// Different keys, i.e. different clients. Ideally these never contend — they touch different map
// entries. With one lock they contend fully, which is what this measures.
func BenchmarkAllowDistinctKeys(b *testing.B) {
	l := New()
	var n int64
	b.RunParallel(func(pb *testing.PB) {
		mu := &sync.Mutex{}
		mu.Lock()
		n++
		id := strconv.FormatInt(n, 10)
		mu.Unlock()
		i := 0
		for pb.Next() {
			l.Allow(id+"-"+strconv.Itoa(i%64), 1<<30, time.Minute)
			i++
		}
	})
}

// The same key from everywhere — a server-scope rule ("global" dimension) is exactly this: one
// bucket shared by every request to every site in the process.
func BenchmarkAllowSharedKey(b *testing.B) {
	l := New()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Allow("server", 1<<30, time.Minute)
		}
	})
}

// A request typically clears several rules (folder chain + method). This is the per-request cost,
// not the per-call one.
func BenchmarkAllowFourRulesPerRequest(b *testing.B) {
	l := New()
	keys := []string{"ip|/", "ip|/dashboard", "global", "path|/dashboard/insights"}
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			for _, k := range keys {
				l.Allow(k, 1<<30, time.Minute)
			}
		}
	})
}

// Single-goroutine baseline: the floor the parallel numbers should be compared against.
func BenchmarkAllowSerial(b *testing.B) {
	l := New()
	for i := 0; i < b.N; i++ {
		l.Allow("ip|/", 1<<30, time.Minute)
	}
}
