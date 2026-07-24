package publishing

import (
	"strings"
	"testing"
	"time"

	"github.com/kitwork/engine/value"
)

func TestRSSValidationEscapingAndMetadata(t *testing.T) {
	feed := value.New(map[string]any{
		"title":       "Systems & Notes",
		"description": "A <quiet> archive",
		"link":        "https://example.test",
		"items": []any{
			map[string]any{
				"title":       "Runtime & VM",
				"description": "One < two",
				"link":        "/articles/runtime",
				"published":   "2026-07-13",
			},
		},
	})

	body, err := RSS(feed, "https://example.test", "/feed.xml")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"<title>Systems &amp; Notes</title>",
		"<description>A &lt;quiet&gt; archive</description>",
		"https://example.test/articles/runtime",
		"Mon, 13 Jul 2026 00:00:00 +0000",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("RSS missing %q: %s", expected, body)
		}
	}
	if ETag(body) == "" || ETag(body) != ETag(body) {
		t.Fatal("ETag must be deterministic")
	}
	if got := LastModified(feed); !got.Equal(time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("last modified = %v", got)
	}

	_, err = RSS(value.New(map[string]any{"title": "Incomplete", "items": []any{}}), "https://example.test", "/rss.xml")
	if err == nil || !strings.Contains(err.Error(), "requires title, description, and link") {
		t.Fatalf("invalid feed error = %v", err)
	}
}

func TestSitemapAutomaticallyBuildsIndexAndPages(t *testing.T) {
	data := value.New([]any{
		map[string]any{"loc": "/one", "lastmod": "2026-07-11"},
		map[string]any{"loc": "/two", "lastmod": "2026-07-12"},
		map[string]any{"loc": "/three", "lastmod": "2026-07-13"},
	})

	index, err := sitemapWithLimit(data, "https://example.test", "/sitemap.xml", "/sitemap.xml", 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"<sitemapindex", "/sitemap-1.xml", "/sitemap-2.xml"} {
		if !strings.Contains(index, expected) {
			t.Fatalf("sitemap index missing %q: %s", expected, index)
		}
	}

	first, err := sitemapWithLimit(data, "https://example.test", "/sitemap.xml", "/sitemap-1.xml", 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(first, "<url>") != 2 || !strings.Contains(first, "https://example.test/two") {
		t.Fatalf("first sitemap page is wrong: %s", first)
	}
	second, err := sitemapWithLimit(data, "https://example.test", "/sitemap.xml", "/sitemap-2.xml", 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(second, "<url>") != 1 || !strings.Contains(second, "https://example.test/three") {
		t.Fatalf("second sitemap page is wrong: %s", second)
	}
	if _, err := sitemapWithLimit(data, "https://example.test", "/sitemap.xml", "/sitemap-3.xml", 2); err == nil {
		t.Fatal("out-of-range sitemap page must fail")
	}
}
