package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

type handlerBenchmarkFixture struct {
	name string
	// file is the router file router is written to: router.work, or "" for a router.kitwork.js (no row
	// since collection went, 01/11/2026; the rows that drew a template page draw a .ui page since the
	// template pages went, 27/10/2026)
	file                 string
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
			file:                 UIRouterFileName,
			maxAllocations:       60,
			maxEngineAllocations: 45,
			router: `
import * as router from "kitwork/router";
router.get((ctx) => ctx.text("ok"));
`,
		},
		{
			name:                 "json-compute",
			file:                 UIRouterFileName,
			maxAllocations:       100,
			maxEngineAllocations: 82,
			router: `
import * as router from "kitwork/router";

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
			file:                 UIRouterFileName,
			maxAllocations:       90,
			maxEngineAllocations: 72,
			router: `
import * as router from "kitwork/router";
import { summarize } from "app/.shared/summary.work";

router.get((ctx) => ctx.json(summarize([4, 8, 15, 16, 23, 42])));
`,
			files: map[string]string{
				// the app's .shared/, beside the site's folder
				"../.shared/summary.work": `
export const summarize = (items) => ({
	count: items.len(),
	total: items.reduce((sum, item) => sum + item, 0)
});
`,
			},
		},
		{
			name:                 "guarded-view",
			file:                 UIRouterFileName,
			maxAllocations:       115,
			maxEngineAllocations: 104,
			router: `
import * as router from "kitwork/router";

router.guard((context) => context.query("blocked") != "1");
router.get((context) => context.view({
	title: "Verified handler",
	answer: 42
}));
`,
			files: map[string]string{
				"view.ui":    `<view><content /></view>`,
				"content.ui": `<content><text heading="1">{{ title }}</text><text>{{ answer }}</text></content>`,
			},
		},
		{
			name:                 "prepared-ssr",
			file:                 UIRouterFileName,
			maxAllocations:       152,
			maxEngineAllocations: 140,
			router: `
import * as router from "kitwork/router";

router.get((context) => context.title("Generation-prepared SSR").view({
	title: "Generation-prepared SSR",
	count: 3,
	ready: true
}));
`,
			files: map[string]string{
				"view.ui": `<view><content /></view>`,
				"content.ui": `<content>
	<card padding="6">
		<icon value="rocket" alt="" />
		<text heading="1">{{ title }}</text>
		<dropdown><button click="open = !open">Ship {{ count }}</button></dropdown>
		<text>{{ count > 0 ? "Ready" : "Waiting" }}</text>
	</card>
	<text>{{ ready }}</text>
</content>`,
			},
		},
		{
			name: "sqlite-callback",
			file: UIRouterFileName,
			router: `
import * as router from "kitwork/router";
import { internals } from "kitwork";

// a query lambda on a SQLite file of the site, through the test-only module (storage_internals_test.go)
const { sqlite, struct, int, text } = internals;
const db = sqlite("app.db", { items: struct({ id: int().primaryKey(), name: text(), price: int() }) });
db.items.create({ name: "camera", price: 900 });

router.get((ctx) => {
	const item = db.items
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
	file := fixture.file
	if file == "" {
		file = RouterFileName
	}
	if err := os.WriteFile(
		filepath.Join(directory, file),
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
