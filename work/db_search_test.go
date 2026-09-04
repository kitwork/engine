package work

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kitwork/engine/search"
)

// Cửa 2: db.<table>.search(text).limit(n).list() over .searchable() columns. Runs on the pure-Go
// immutable-segment sidecar while the SQLite table stays the source of truth. This exercises the
// SQLite schema engine and proves Vietnamese diacritic-blind matching, implicit AND across terms,
// and that .searchable({ weight: 3 }) ranks a title match above a body-only match.
func TestSchemaTableSearchE2E(t *testing.T) {
	tmp, err := os.MkdirTemp("", "kitwork-dbsearch-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)

	dir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}

	router := `import { router, database } from "kitwork";` + "\n" +
		`const { sqlite, text } = database;` + "\n" +
		`const db = sqlite("shop.db", {` + "\n" +
		`  products: {` + "\n" +
		`    id: text().key(),` + "\n" +
		`    title: text().searchable({ weight: 3 }),` + "\n" +
		`    body: text().searchable(),` + "\n" +
		`  }` + "\n" +
		`});` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  db.products.create({ id: "p1", title: "Áo thun cotton nam", body: "hàng ngày bền đẹp" });` + "\n" +
		`  db.products.create({ id: "p2", title: "Quần jean xanh", body: "chất cotton co giãn" });` + "\n" +
		`  db.products.create({ id: "p3", title: "Giày thể thao", body: "nhẹ êm chân" });` + "\n" +
		`  return ctx.json({` + "\n" +
		`    weighted: db.products.search("cotton").limit(10).list(),` + "\n" + // p1 title(w3) vs p2 body(w1)
		`    folded:   db.products.search("ao").limit(10).list(),` + "\n" + // "ao" → "Áo"
		`    conj:     db.products.search("cotton jean").limit(10).list(),` + "\n" + // AND: only p2 has both
		`  });` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0644); err != nil {
		t.Fatal(err)
	}

	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()

	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	body := rec.Body.String()

	// Weight: "cotton" is in p1's TITLE (weight 3) and p2's BODY (weight 1) → p1 must rank first.
	weighted := section(body, `"weighted":[`)
	p1 := strings.Index(weighted, `"id":"p1"`)
	p2 := strings.Index(weighted, `"id":"p2"`)
	if p1 == -1 || p2 == -1 {
		t.Fatalf("weighted search must return both p1 and p2 for 'cotton'; got: %s", weighted)
	}
	if p1 > p2 {
		t.Errorf("weight: title match (p1, weight 3) must outrank body match (p2, weight 1); got: %s", weighted)
	}
	if strings.Contains(weighted, `"id":"p3"`) {
		t.Errorf("'cotton' must not match p3; got: %s", weighted)
	}

	// Results carry relevance signals: a highlighted _snippet and a numeric _score (JSON HTML-escapes
	// the <b> tags to <b>).
	highlighted := strings.Contains(weighted, "<b>") || strings.Contains(weighted, "\\u003cb\\u003e")
	if !strings.Contains(weighted, `"_snippet":`) || !highlighted {
		t.Errorf("search rows must carry a highlighted _snippet; got: %s", weighted)
	}
	if !strings.Contains(weighted, `"_score":`) {
		t.Errorf("search rows must carry a _score; got: %s", weighted)
	}

	// Diacritics-blind: "ao" finds "Áo thun cotton nam" (p1).
	folded := section(body, `"folded":[`)
	if !strings.Contains(folded, `"id":"p1"`) {
		t.Errorf("folded search 'ao' must find 'Áo…' (p1); got: %s", folded)
	}

	// Implicit AND: only p2 has BOTH "cotton" (body) and "jean" (title).
	conj := section(body, `"conj":[`)
	if !strings.Contains(conj, `"id":"p2"`) {
		t.Errorf("AND search 'cotton jean' must find p2; got: %s", conj)
	}
	if strings.Contains(conj, `"id":"p1"`) || strings.Contains(conj, `"id":"p3"`) {
		t.Errorf("AND search 'cotton jean' must exclude rows missing a term; got: %s", conj)
	}

	// Standalone tenants own one bounded manager below .data/search. Production
	// tenants receive the host manager instead of opening one manager per site.
	if info, err := os.Stat(filepath.Join(dir, ".data", "search")); err != nil || !info.IsDir() {
		t.Errorf("search sidecar directory missing: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".data", "shop.fts.db")); !os.IsNotExist(err) {
		t.Errorf("legacy SQLite FTS sidecar must not be created: %v", err)
	}
}

// section returns the JSON substring starting at marker up to the next top-level close bracket, so
// assertions on one search result do not see ids from another.
func section(body, marker string) string {
	start := strings.Index(body, marker)
	if start == -1 {
		return ""
	}
	rest := body[start+len(marker):]
	if end := strings.Index(rest, "]"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestRenderSearchFragmentEscapesSourceHTML(t *testing.T) {
	got := renderSearchFragment(search.Fragment{
		Text:    "<script>cotton</script>",
		Matches: []search.TextRange{{Start: len("<script>"), End: len("<script>cotton")}},
	})
	want := "&lt;script&gt;<b>cotton</b>&lt;/script&gt;"
	if got != want {
		t.Fatalf("rendered fragment = %q, want %q", got, want)
	}
}

func TestSchemaTableSearchSkipsOversizedLexicalNoise(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "acme", "localhost")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	noise := strings.Repeat("x", 300)
	router := `import { router, database } from "kitwork";` + "\n" +
		`const { sqlite, text } = database;` + "\n" +
		`const db = sqlite("shop.db", { products: { id: text().key(), body: text().searchable() } });` + "\n" +
		`router.get((ctx) => {` + "\n" +
		`  db.products.create({ id: "p1", body: "useful ` + noise + ` searchable" });` + "\n" +
		`  return ctx.json(db.products.search("useful").list());` + "\n" +
		`});`
	if err := os.WriteFile(filepath.Join(dir, "router.kitwork.js"), []byte(router), 0o644); err != nil {
		t.Fatal(err)
	}
	tenant := NewTenant(tmp, "localhost")
	if err := tenant.Run(); err != nil {
		t.Fatal(err)
	}
	defer tenant.Close()
	recorder := httptest.NewRecorder()
	tenant.Serve(recorder, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"p1"`) {
		t.Fatalf("search with lexical noise: status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}
