package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/value"
)

// insights.search() logs queries; gaps() returns the ones that currently return NOTHING, most-searched
// first; a query that later starts returning results drops off the gap list.
func TestInsightsSearchGaps(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-insights-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router } from "kitwork"; router.get((ctx) => ctx.text("ok"));`), 0644)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	in := tenant.Kitwork().Insights()

	// "vector search" searched 3x, never found → the top gap. "wasm" once, no results → a gap.
	// "runtime" found results → NOT a gap. Variants of case/space fold into one row.
	in.Search(value.NewString("Vector Search"), value.New(0))
	in.Search(value.NewString("vector   search"), value.New(0))
	in.Search(value.NewString("vector search"), value.New(0))
	in.Search(value.NewString("wasm"), value.New(0))
	in.Search(value.NewString("runtime"), value.New(5))
	in.Search(value.NewString("x"), value.New(0)) // too short → ignored

	gaps := in.Gaps().Interface()
	list, ok := gaps.([]any)
	if !ok {
		t.Fatalf("gaps not a list: %T %v", gaps, gaps)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 gaps (vector search, wasm), got %d: %v", len(list), list)
	}
	top := list[0].(map[string]any)
	if top["query"] != "vector search" {
		t.Errorf("top gap = %v, want 'vector search'", top["query"])
	}
	if top["total"].(float64) != 3 {
		t.Errorf("'vector search' total = %v, want 3 (variants folded)", top["total"])
	}

	// Add content: now "vector search" returns results → it leaves the gap list.
	in.Search(value.NewString("vector search"), value.New(2))
	gaps = in.Gaps().Interface()
	list, _ = gaps.([]any)
	for _, g := range list {
		if g.(map[string]any)["query"] == "vector search" {
			t.Errorf("'vector search' still a gap after it returned results: %v", list)
		}
	}
	if len(list) != 1 || list[0].(map[string]any)["query"] != "wasm" {
		t.Errorf("gaps after fill = %v, want just [wasm]", list)
	}
}

// End to end: hitting the real search route logs the query; a miss shows up in gaps().
func TestInsightsThroughRoute(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-insights2-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	dir := filepath.Join(tmp, "acme", "localhost")
	os.MkdirAll(filepath.Join(dir, "_collection", "posts"), 0755)
	os.WriteFile(filepath.Join(dir, "_collection", "posts", "runtime.md"),
		[]byte("---\ntitle: Runtime\n---\nabout goroutines and the vm"), 0644)
	os.WriteFile(filepath.Join(dir, "router.kitwork.js"),
		[]byte(`import { router, collection, insights } from "kitwork";`+"\n"+
			`router.get((ctx) => {`+"\n"+
			`  const q = ctx.query("q").trim();`+"\n"+
			`  const hits = q.length > 1 ? collection.open("posts").search(q) : [];`+"\n"+
			`  if (q.length > 1) insights.search(q, hits.length);`+"\n"+
			`  return ctx.json({ q: q, found: hits.length });`+"\n"+
			`});`), 0644)

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	hit := func(q string) {
		rec := httptest.NewRecorder()
		tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/?q="+q, nil))
	}
	hit("goroutines") // matches runtime.md body → found
	hit("kubernetes") // no doc → gap
	hit("kubernetes")

	gaps := tenant.Kitwork().Insights().Gaps().Interface().([]any)
	if len(gaps) != 1 {
		t.Fatalf("want 1 gap (kubernetes), got %d: %v", len(gaps), gaps)
	}
	g := gaps[0].(map[string]any)
	if g["query"] != "kubernetes" {
		t.Errorf("gap query = %v, want kubernetes", g["query"])
	}
	if g["total"].(float64) != 2 {
		t.Errorf("kubernetes total = %v, want 2", g["total"])
	}
	if !strings.Contains(tenant.Kitwork().Insights().Searches().Interface().([]any)[0].(map[string]any)["query"].(string), "kubernetes") {
		t.Errorf("top overall search should be kubernetes (2x)")
	}
}
