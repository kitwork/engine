package work

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// End-to-end serve benchmarks. The isolated lock benchmarks say what a primitive costs; these say
// whether that cost matters, which is the only question worth acting on. A lock that contends at
// 300ns is irrelevant beside a 50µs request and is the whole ceiling beside a 500ns one.
//
// Two axes, because "multi-tenant with heavy traffic" is two different questions:
//   TENANT COUNT       does serving 1 site cost the same per request as serving 64?
//   CONCURRENCY        does throughput rise with cores, or flatten on a shared lock?

func benchTenant(b *testing.B, root, domain, body string) *Tenant {
	b.Helper()
	dir := filepath.Join(root, "acme", domain)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(body), 0o644); err != nil {
		b.Fatal(err)
	}
	t := NewTenant(root, domain)
	if err := t.Run(); err != nil {
		b.Fatal(err)
	}
	return t
}

const benchRouterPlain = `
import { router } from "kitwork";
router.get((ctx) => ctx.text("ok"));
`

// A rule on the folder plus one on the method: the limiter runs twice per request, which is a
// modest, realistic configuration rather than a worst case.
const benchRouterLimited = `
import { router } from "kitwork";
router.ratelimit({ rate: 1000000, per: "1m" });
router.get((ctx) => ctx.text("ok")).limit({ rate: 1000000, per: "1m" });
`

func serveOnce(t *Tenant, domain string) {
	req := httptest.NewRequest(http.MethodGet, "http://"+domain+"/", nil)
	t.Serve(httptest.NewRecorder(), req)
}

// The headline number: one site, requests in parallel. Whatever this reaches is the per-request
// budget every lock has to be judged against.
func BenchmarkServeSingleTenant(b *testing.B) {
	root := b.TempDir()
	t := benchTenant(b, root, "localhost", benchRouterPlain)
	defer t.Close()

	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			serveOnce(t, "localhost")
		}
	})
}

// The multi-tenancy question. Per-request cost must not grow with the number of loaded sites — if
// it does, density is capped by something other than memory.
func BenchmarkServeManyTenants(b *testing.B) {
	for _, count := range []int{1, 8, 64} {
		b.Run(fmt.Sprintf("tenants=%d", count), func(b *testing.B) {
			root := b.TempDir()
			tenants := make([]*Tenant, count)
			domains := make([]string, count)
			for i := 0; i < count; i++ {
				domains[i] = fmt.Sprintf("site%d.test", i)
				tenants[i] = benchTenant(b, root, domains[i], benchRouterPlain)
			}
			defer func() {
				for _, t := range tenants {
					t.Close()
				}
			}()

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					k := i % count
					serveOnce(tenants[k], domains[k])
					i++
				}
			})
		})
	}
}

// The limiter's share of a real request: the same route with and without rate rules. The delta is
// what optimising ratelimit.Limiter could buy, measured rather than guessed.
func BenchmarkServeRateLimitCost(b *testing.B) {
	for _, c := range []struct {
		name   string
		router string
	}{
		{"no-rules", benchRouterPlain},
		{"two-rules", benchRouterLimited},
	} {
		b.Run(c.name, func(b *testing.B) {
			root := b.TempDir()
			t := benchTenant(b, root, "localhost", c.router)
			defer t.Close()

			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					serveOnce(t, "localhost")
				}
			})
		})
	}
}
