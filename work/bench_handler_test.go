package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type handlerBenchmarkFixture struct {
	name                 string
	router               string
	files                map[string]string
	maxAllocations       float64
	maxEngineAllocations float64
}

type discardResponseWriter struct {
	header http.Header
	status int
	bytes  int
}

func newDiscardResponseWriter() *discardResponseWriter {
	return &discardResponseWriter{header: make(http.Header)}
}

func (w *discardResponseWriter) Header() http.Header {
	return w.header
}

func (w *discardResponseWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *discardResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	w.bytes += len(data)
	return len(data), nil
}

func (w *discardResponseWriter) Reset() {
	clear(w.header)
	w.status = 0
	w.bytes = 0
}

func benchmarkHandlerFixtures() []handlerBenchmarkFixture {
	return []handlerBenchmarkFixture{
		{
			name:                 "text",
			maxAllocations:       60,
			maxEngineAllocations: 45,
			router: `
import { router } from "kitwork";
router.get((ctx) => ctx.text("ok"));
`,
		},
		{
			name:                 "json-compute",
			maxAllocations:       100,
			maxEngineAllocations: 82,
			router: `
import { router } from "kitwork";

const calculate = (values) => values
	.map((item) => item * 3)
	.filter((item) => item > 12)
	.reduce((total, item) => total + item, 0);

router.get((ctx) => {
	const total = calculate([1, 2, 3, 4, 5, 6, 7, 8, 9, 10]);
	return ctx.json({ total: total, ready: true });
});
`,
		},
		{
			name:                 "native-import",
			maxAllocations:       90,
			maxEngineAllocations: 72,
			router: `
import { router } from "kitwork";
import { summarize } from "./_core/summary.kitwork.js";

router.get((ctx) => ctx.json(summarize([4, 8, 15, 16, 23, 42])));
`,
			files: map[string]string{
				"_core/summary.kitwork.js": `
export const summarize = (items) => ({
	count: items.len(),
	total: items.reduce((sum, item) => sum + item, 0)
});
`,
			},
		},
		{
			name:                 "guarded-view",
			maxAllocations:       115,
			maxEngineAllocations: 96,
			router: `
import { router } from "kitwork";

router.guard((ctx) =>
	ctx.query("blocked") == "1"
		? ctx.status(403).text("blocked")
		: true
);
router.get((ctx) => ctx.view({
	title: "Verified handler",
	answer: 42
}));
`,
			files: map[string]string{
				"index.kitwork.html": `<main><h1>{{ title }}</h1><p>{{ answer }}</p></main>`,
			},
		},
		{
			name:                 "prepared-ssr",
			maxAllocations:       110,
			maxEngineAllocations: 100,
			router: `
import { router } from "kitwork";

router.get((ctx) => ctx.view({
	title: "Generation-prepared SSR",
	count: 3,
	ready: true
}));
`,
			files: map[string]string{
				"index.kitwork.html": `<html data-kit-hydrate="v1"><head><title>{{ title }}</title></head><body class="min-h-dvh bg-white text-zinc-900 dark:bg-zinc-950 dark:text-white">{{ @page }}</body></html>`,
				"page.kitwork.html": `<main class="mx-auto flex max-w-5xl gap-6 p-6">
	<section class="card flex-1 border border-zinc-200 p-6 dark:border-zinc-800">
		<i class="icon-rocket text-red-500"></i>
		<h1 class="text-3xl font-bold">{{ title }}</h1>
		<input type="number" data-kit-model="count" value="3">
		<b data-kit-text="count &gt; 0 ? 'Ready' : 'Waiting'">Waiting</b>
		<button class="button mt-4" data-kit-click="count = count + 1">Ship</button>
	</section>
	<aside class="w-72 border-l border-zinc-200 pl-6 dark:border-zinc-800">{{ ready }}</aside>
</main>`,
			},
		},
		{
			name: "collection",
			router: `
import { router, collection } from "kitwork";

const posts = collection.open("posts").cache("30m");
router.get((ctx) => ctx.json({ posts: posts.all() }));
`,
			files: map[string]string{
				"_collection/posts/welcome.md": `---
title: Welcome
status: published
---
Hello from the benchmark corpus.
`,
			},
		},
		{
			name: "sqlite-callback",
			router: `
import { router, sqlite } from "kitwork";

sqlite.exec("CREATE TABLE IF NOT EXISTS items (id INTEGER PRIMARY KEY, name TEXT, price INTEGER)");
sqlite.table("items").create({ name: "camera", price: 900 });

router.get((ctx) => {
	const item = sqlite
		.table("items")
		.where((row) => row.name == "camera")
		.first();
	return ctx.json({ name: item.name, price: item.price });
});
`,
		},
	}
}

func benchHandlerTenant(
	tb testing.TB,
	root string,
	fixture handlerBenchmarkFixture,
) *Tenant {
	tb.Helper()
	domain := "localhost"
	directory := filepath.Join(root, "acme", domain)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		tb.Fatal(err)
	}
	for relative, contents := range fixture.files {
		file := filepath.Join(directory, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			tb.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(contents), 0o644); err != nil {
			tb.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(directory, RouterFileName),
		[]byte(fixture.router),
		0o644,
	); err != nil {
		tb.Fatal(err)
	}

	tenant := NewTenant(root, domain)
	if err := tenant.Run(); err != nil {
		tb.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
	tenant.Serve(response, request)
	if response.Code != http.StatusOK {
		tenant.Close()
		tb.Fatalf(
			"fixture %s returned %d: %s",
			fixture.name,
			response.Code,
			response.Body.String(),
		)
	}
	return tenant
}

func TestHandlerCorpusAllocationBudgets(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation budgets are not comparable under race instrumentation")
	}
	for _, fixture := range benchmarkHandlerFixtures() {
		if fixture.maxAllocations == 0 {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			tenant := benchHandlerTenant(t, t.TempDir(), fixture)
			defer tenant.Close()
			request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			var status int

			allocations := testing.AllocsPerRun(100, func() {
				response := httptest.NewRecorder()
				tenant.Serve(response, request.Clone(request.Context()))
				status = response.Code
			})
			if status != http.StatusOK {
				t.Fatalf("response status = %d", status)
			}
			if allocations > fixture.maxAllocations {
				t.Fatalf(
					"allocations/request = %.2f, budget is %.2f",
					allocations,
					fixture.maxAllocations,
				)
			}
		})
	}
}

func TestHandlerEngineAllocationBudgets(t *testing.T) {
	if raceEnabled {
		t.Skip("allocation budgets are not comparable under race instrumentation")
	}
	for _, fixture := range benchmarkHandlerFixtures() {
		if fixture.maxEngineAllocations == 0 {
			continue
		}
		t.Run(fixture.name, func(t *testing.T) {
			tenant := benchHandlerTenant(t, t.TempDir(), fixture)
			defer tenant.Close()
			request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			response := newDiscardResponseWriter()

			allocations := testing.AllocsPerRun(100, func() {
				response.Reset()
				tenant.Serve(response, request)
			})
			if response.status != http.StatusOK {
				t.Fatalf("response status = %d", response.status)
			}
			if allocations > fixture.maxEngineAllocations {
				t.Fatalf(
					"engine allocations/request = %.2f, budget is %.2f",
					allocations,
					fixture.maxEngineAllocations,
				)
			}
		})
	}
}

// BenchmarkServeHandlerCorpus measures representative request paths through
// the production router, VM pool, capabilities, response lifecycle, and render
// plan. It is intentionally serial so comparisons emphasize per-request work.
func BenchmarkServeHandlerCorpus(b *testing.B) {
	for _, fixture := range benchmarkHandlerFixtures() {
		b.Run(fixture.name, func(b *testing.B) {
			tenant := benchHandlerTenant(b, b.TempDir(), fixture)
			defer tenant.Close()

			request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				response := httptest.NewRecorder()
				tenant.Serve(response, request.Clone(request.Context()))
				if response.Code != http.StatusOK {
					b.Fatalf("response status = %d", response.Code)
				}
			}
		})
	}
}

// BenchmarkServeHandlerEngine removes httptest.ResponseRecorder allocation and
// request.Clone from the measured loop. It still runs the complete production
// Tenant.Serve lifecycle and therefore isolates engine-owned request work.
func BenchmarkServeHandlerEngine(b *testing.B) {
	for _, fixture := range benchmarkHandlerFixtures() {
		b.Run(fixture.name, func(b *testing.B) {
			tenant := benchHandlerTenant(b, b.TempDir(), fixture)
			defer tenant.Close()

			request := httptest.NewRequest(http.MethodGet, "http://localhost/", nil)
			response := newDiscardResponseWriter()
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				response.Reset()
				tenant.Serve(response, request)
				if response.status != http.StatusOK {
					b.Fatalf("response status = %d", response.status)
				}
			}
		})
	}
}
