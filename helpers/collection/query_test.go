package collection

import (
	"os"
	"path/filepath"
	"testing"
)

func entry(slug string, meta map[string]any) IndexEntry {
	return IndexEntry{File: File{Slug: slug, Name: slug + ".md"}, Meta: meta}
}

func slugs(entries []IndexEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.File.Slug
	}
	return out
}

func TestQueryFilterSortSlice(t *testing.T) {
	index := []IndexEntry{
		entry("a", map[string]any{"status": "published", "views": 10, "publishedAt": "2026-01-03", "tags": []any{"go", "vm"}}),
		entry("b", map[string]any{"status": "draft", "views": 99, "publishedAt": "2026-01-01"}),
		entry("c", map[string]any{"status": "published", "views": 50, "publishedAt": "2026-01-02", "tags": []any{"web"}}),
		entry("d", map[string]any{"status": "published", "views": 5, "publishedAt": "2026-01-04"}),
	}

	// equality + orderBy desc (ISO date as string) + limit
	got := Query{
		Filters:    []Filter{{Field: "status", Value: "published"}},
		OrderField: "publishedAt", OrderDesc: true, LimitN: 2,
	}.Apply(index)
	if s := slugs(got); len(s) != 2 || s[0] != "d" || s[1] != "a" {
		t.Errorf("published desc limit2 = %v, want [d a]", s)
	}

	// numeric operator
	got = Query{Filters: []Filter{{Field: "views", Op: ">", Value: 20}}, OrderField: "views"}.Apply(index)
	if s := slugs(got); len(s) != 2 || s[0] != "c" || s[1] != "b" {
		t.Errorf("views>20 asc = %v, want [c b]", s)
	}

	// contains on list frontmatter (tags)
	got = Query{Filters: []Filter{{Field: "tags", Op: "contains", Value: "go"}}}.Apply(index)
	if s := slugs(got); len(s) != 1 || s[0] != "a" {
		t.Errorf("tags contains go = %v, want [a]", s)
	}

	// skip + limit paging
	got = Query{OrderField: "slug", SkipN: 1, LimitN: 2}.Apply(index)
	if s := slugs(got); len(s) != 2 || s[0] != "b" || s[1] != "c" {
		t.Errorf("page skip1 limit2 = %v, want [b c]", s)
	}

	// file-fact field fallback: orderBy("slug") works with zero frontmatter
	got = Query{OrderField: "slug", OrderDesc: true, LimitN: 1}.Apply(index)
	if s := slugs(got); len(s) != 1 || s[0] != "d" {
		t.Errorf("slug desc first = %v, want [d]", s)
	}
}

// One document with broken frontmatter must be SKIPPED, not take down the whole index.
func TestIndexSurvivesBrokenFrontmatter(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "good.md"), []byte("---\ntitle: Good\n---\nbody"), 0644)
	os.WriteFile(filepath.Join(dir, "bad.md"), []byte("---\ntitle: [unclosed\n---\nbody"), 0644)
	os.WriteFile(filepath.Join(dir, "also-good.md"), []byte("---\ntitle: Also\n---\nbody"), 0644)

	store, err := NewStore(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Open(".") // the temp dir itself
	if err != nil {
		// "." is rejected by Open — use a subdir layout instead
		sub := filepath.Join(dir, "posts")
		os.MkdirAll(sub, 0755)
		for _, f := range []string{"good.md", "bad.md", "also-good.md"} {
			os.Rename(filepath.Join(dir, f), filepath.Join(sub, f))
		}
		c, err = store.Open("posts")
		if err != nil {
			t.Fatal(err)
		}
	}
	index, err := c.Index()
	if err != nil {
		t.Fatalf("index must not fail on one broken file: %v", err)
	}
	if len(index) != 2 {
		t.Fatalf("index len = %d, want 2 (bad.md skipped, good ones kept)", len(index))
	}
}
