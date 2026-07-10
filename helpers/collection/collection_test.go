package collection

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type memorySnapshots struct {
	mu    sync.Mutex
	data  map[string][]byte
	loads int
	saves int
}

func newMemorySnapshots() *memorySnapshots {
	return &memorySnapshots{data: make(map[string][]byte)}
}

func (s *memorySnapshots) Load(key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loads++
	body, ok := s.data[key]
	return append([]byte(nil), body...), ok
}

func (s *memorySnapshots) Save(key string, body []byte, _ time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saves++
	s.data[key] = append([]byte(nil), body...)
	return nil
}

func TestSplitFrontMatterAndRenderMarkdown(t *testing.T) {
	source := []byte("\xef\xbb\xbf---\n" +
		"title: Runtime\n" +
		"group: core\n" +
		"topics:\n  - vm\n  - bytecode\n" +
		"custom:\n  nested: true\n" +
		"---\n" +
		"# Runtime\n\n" +
		"Intro with **strong**, *emphasis*, `code`, and [docs](https://example.com).\n\n" +
		"## The Simple Idea\n\n" +
		"- first\n- second\n\n" +
		"```go\nfmt.Println(\"<safe>\")\n```\n")

	meta, body, err := splitFrontMatter(source)
	if err != nil {
		t.Fatal(err)
	}
	if meta["title"] != "Runtime" || meta["group"] != "core" {
		t.Fatalf("unexpected meta: %#v", meta)
	}
	topics, ok := meta["topics"].([]any)
	if !ok || len(topics) != 2 {
		t.Fatalf("dynamic topics lost: %#v", meta["topics"])
	}
	custom, ok := meta["custom"].(map[string]any)
	if !ok || custom["nested"] != true {
		t.Fatalf("nested custom metadata lost: %#v", meta["custom"])
	}

	rendered, toc := renderMarkdown(body, "Runtime")
	if strings.Contains(rendered, "<h1") {
		t.Fatalf("frontmatter title should suppress the first markdown h1: %s", rendered)
	}
	for _, want := range []string{
		"<strong>strong</strong>",
		"<em>emphasis</em>",
		"<code>code</code>",
		`<a href="https://example.com">docs</a>`,
		`<h2 id="the-simple-idea">The Simple Idea</h2>`,
		`<code class="language-go">fmt.Println(&#34;&lt;safe&gt;&#34;)`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered markdown missing %q:\n%s", want, rendered)
		}
	}
	if len(toc) != 1 || toc[0].ID != "the-simple-idea" || toc[0].Level != 2 {
		t.Fatalf("unexpected toc: %#v", toc)
	}
}

func TestCollectionListIndexReadCacheAndPersist(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "_data", "concepts")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runtimePath := filepath.Join(dir, "runtime.md")
	write := func(summary, body string) {
		source := "---\ntitle: Runtime\nstatus: public\nsummary: " + summary + "\n---\n# Runtime\n\n## Idea\n\n" + body + "\n"
		if err := os.WriteFile(runtimePath, []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("first", "version one")
	if err := os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}

	disk := newMemorySnapshots()
	store, err := NewStore(root, disk)
	if err != nil {
		t.Fatal(err)
	}
	concepts, err := store.Open("_data/concepts")
	if err != nil {
		t.Fatal(err)
	}
	concepts.SetPersist(true, time.Hour)

	files, err := concepts.List()
	if err != nil || len(files) != 1 || files[0].Slug != "runtime" {
		t.Fatalf("list = %#v, err=%v", files, err)
	}
	index, err := concepts.Index()
	if err != nil {
		t.Fatal(err)
	}
	if len(index) != 1 || index[0].Meta["summary"] != "first" {
		t.Fatalf("index = %#v", index)
	}
	document, err := concepts.Read("runtime")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(document.HTML, "version one") || len(document.TOC) != 1 {
		t.Fatalf("document = %#v", document)
	}
	if disk.saves < 2 {
		t.Fatalf("persist should save index and document, saves=%d", disk.saves)
	}

	// A fresh Store has empty RAM and must load the matching document snapshot from disk.
	freshStore, err := NewStore(root, disk)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := freshStore.Open("_data/concepts")
	if err != nil {
		t.Fatal(err)
	}
	fresh.SetPersist(true, time.Hour)
	loadsBefore := disk.loads
	fromDisk, err := fresh.Read("runtime")
	if err != nil || !strings.Contains(fromDisk.HTML, "version one") {
		t.Fatalf("persisted read = %#v, err=%v", fromDisk, err)
	}
	if disk.loads <= loadsBefore {
		t.Fatal("fresh Store did not consult the persistent tier")
	}

	// Same slug, new signature: neither RAM nor persisted data may hide the source edit.
	time.Sleep(2 * time.Millisecond)
	write("second", "version two with a different size")
	updated, err := concepts.Read("runtime")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(updated.HTML, "version two") || updated.Meta["summary"] != "second" {
		t.Fatalf("cache did not invalidate: %#v", updated)
	}
}

func TestCollectionSecurityAndMissingDocument(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open("../outside"); err == nil {
		t.Fatal("expected traversal to be denied")
	}
	docs, err := store.Open("docs")
	if err != nil {
		t.Fatal(err)
	}
	for _, slug := range []string{"../secret", "nested/file", "", "file.json"} {
		if _, err := docs.Read(slug); err == nil {
			t.Fatalf("expected %q to be rejected", slug)
		}
	}
}
