// Package publishing serializes semantic site outputs. Router APIs provide structured values;
// this package owns standards details such as escaping, absolute URLs, dates, and media types.
package publishing

import (
	"bytes"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

const (
	RSSMediaType     = "application/rss+xml; charset=utf-8"
	SitemapMediaType = "application/xml; charset=utf-8"
	SitemapURLLimit  = 50000
	SitemapByteLimit = 50 * 1024 * 1024
)

func field(m map[string]value.Value, names ...string) value.Value {
	for _, name := range names {
		if v, ok := m[name]; ok {
			return v
		}
	}
	return value.Value{K: value.Nil}
}

func text(v value.Value) string {
	if v.K == value.Nil || v.K == value.Invalid {
		return ""
	}
	return v.String()
}

func escaped(s string) string {
	var out bytes.Buffer
	_ = xml.EscapeText(&out, []byte(s))
	return out.String()
}

func absolute(base, ref string) string {
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ref
	}
	if u.IsAbs() {
		return u.String()
	}
	b, err := url.Parse(strings.TrimSuffix(base, "/") + "/")
	if err != nil {
		return ref
	}
	return b.ResolveReference(u).String()
}

func parsedDate(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", time.RFC1123Z, time.RFC1123} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}

func rssDate(raw string) string {
	if parsed, ok := parsedDate(raw); ok {
		return parsed.Format(time.RFC1123Z)
	}
	return strings.TrimSpace(raw)
}

// ETag returns a deterministic strong validator for a generated document.
func ETag(body string) string { return fmt.Sprintf(`"%x"`, sha256.Sum256([]byte(body))) }

// LastModified finds the newest date exposed by a feed or sitemap provider.
func LastModified(data value.Value) time.Time {
	entries := data.Array()
	if data.IsMap() {
		m := data.Map()
		entries = field(m, "items", "entries", "pages").Array()
	}
	var latest time.Time
	for _, entry := range entries {
		if !entry.IsMap() {
			continue
		}
		raw := text(field(entry.Map(), "lastmod", "updated", "published", "date", "pubDate"))
		if parsed, ok := parsedDate(raw); ok && parsed.After(latest) {
			latest = parsed
		}
	}
	return latest
}

// RSS renders a version 2.0 feed from a map containing channel fields and an items array.
func RSS(data value.Value, requestBase, selfPath string) (string, error) {
	if !data.IsMap() {
		return "", fmt.Errorf("rss provider must return a channel object")
	}
	config := data.Map()
	title := text(field(config, "title"))
	description := text(field(config, "description", "summary"))
	channelLink := absolute(requestBase, text(field(config, "link", "url")))
	if title == "" || description == "" || channelLink == "" {
		return "", fmt.Errorf("rss channel requires title, description, and link")
	}
	itemsValue := field(config, "items", "entries")
	if itemsValue.K != value.Array {
		return "", fmt.Errorf("rss channel items must be an array")
	}
	self := absolute(requestBase, selfPath)

	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	body.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>`)
	body.WriteString(`<title>` + escaped(title) + `</title>`)
	body.WriteString(`<link>` + escaped(channelLink) + `</link>`)
	body.WriteString(`<description>` + escaped(description) + `</description>`)
	if language := text(field(config, "language")); language != "" {
		body.WriteString(`<language>` + escaped(language) + `</language>`)
	}
	body.WriteString(`<atom:link href="` + escaped(self) + `" rel="self" type="application/rss+xml"/>`)

	items := itemsValue.Array()
	latest := ""
	for index, item := range items {
		if !item.IsMap() {
			return "", fmt.Errorf("rss item %d must be an object", index)
		}
		m := item.Map()
		link := absolute(requestBase, text(field(m, "link", "url")))
		itemTitle := text(field(m, "title"))
		itemDescription := text(field(m, "description", "summary", "subtitle"))
		if link == "" || (itemTitle == "" && itemDescription == "") {
			return "", fmt.Errorf("rss item %d requires link and either title or description", index)
		}
		published := rssDate(text(field(m, "published", "date", "pubDate")))
		if latest == "" && published != "" {
			latest = published
		}
		guid := text(field(m, "guid", "id"))
		if guid == "" {
			guid = link
		}

		body.WriteString(`<item>`)
		body.WriteString(`<title>` + escaped(itemTitle) + `</title>`)
		body.WriteString(`<link>` + escaped(link) + `</link>`)
		body.WriteString(`<guid isPermaLink="true">` + escaped(guid) + `</guid>`)
		if published != "" {
			body.WriteString(`<pubDate>` + escaped(published) + `</pubDate>`)
		}
		body.WriteString(`<description>` + escaped(itemDescription) + `</description>`)
		body.WriteString(`</item>`)
	}
	if latest != "" {
		body.WriteString(`<lastBuildDate>` + escaped(latest) + `</lastBuildDate>`)
	}
	body.WriteString(`</channel></rss>`)
	return body.String(), nil
}

type sitemapEntry struct {
	loc, lastmod, changefreq, priority string
}

func sitemapEntries(data value.Value, requestBase string) ([]sitemapEntry, error) {
	entries := data.Array()
	if data.IsMap() {
		entries = field(data.Map(), "pages", "entries", "items").Array()
	}
	if data.K != value.Array && !data.IsMap() {
		return nil, fmt.Errorf("sitemap provider must return an array or a pages object")
	}

	seen := map[string]struct{}{}
	result := make([]sitemapEntry, 0, len(entries))
	for index, entry := range entries {
		item := sitemapEntry{}
		if entry.IsMap() {
			m := entry.Map()
			item.loc = text(field(m, "loc", "path", "url"))
			item.lastmod = text(field(m, "lastmod", "updated", "date"))
			item.changefreq = text(field(m, "changefreq"))
			item.priority = text(field(m, "priority"))
		} else {
			item.loc = text(entry)
		}
		item.loc = absolute(requestBase, item.loc)
		if item.loc == "" {
			return nil, fmt.Errorf("sitemap entry %d requires loc, path, or url", index)
		}
		if _, exists := seen[item.loc]; exists {
			continue
		}
		seen[item.loc] = struct{}{}
		result = append(result, item)
	}
	return result, nil
}

func renderURLSet(entries []sitemapEntry) (string, error) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	body.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, item := range entries {
		body.WriteString(`<url><loc>` + escaped(item.loc) + `</loc>`)
		if item.lastmod != "" {
			body.WriteString(`<lastmod>` + escaped(item.lastmod) + `</lastmod>`)
		}
		if item.changefreq != "" {
			body.WriteString(`<changefreq>` + escaped(item.changefreq) + `</changefreq>`)
		}
		if item.priority != "" {
			body.WriteString(`<priority>` + escaped(item.priority) + `</priority>`)
		}
		body.WriteString(`</url>`)
	}
	body.WriteString(`</urlset>`)
	if body.Len() > SitemapByteLimit {
		return "", fmt.Errorf("sitemap page exceeds the 50 MB uncompressed limit")
	}
	return body.String(), nil
}

func sitemapPagePath(outputPath string, pageNumber int) string {
	extension := path.Ext(outputPath)
	base := strings.TrimSuffix(outputPath, extension)
	return base + "-" + strconv.Itoa(pageNumber) + extension
}

func requestedSitemapPage(outputPath, requestPath string) int {
	extension := path.Ext(outputPath)
	base := strings.TrimSuffix(outputPath, extension)
	pageText := strings.TrimSuffix(strings.TrimPrefix(requestPath, base+"-"), extension)
	pageNumber, _ := strconv.Atoi(pageText)
	return pageNumber
}

func newestLastmod(entries []sitemapEntry) string {
	newest := ""
	for _, entry := range entries {
		if entry.lastmod > newest {
			newest = entry.lastmod
		}
	}
	return newest
}

// Sitemap renders a URL set or, above 50,000 URLs, a sitemap index whose numbered children are
// served from the same provider (sitemap-1.xml, sitemap-2.xml, ...).
func Sitemap(data value.Value, requestBase, outputPath, requestPath string) (string, error) {
	return sitemapWithLimit(data, requestBase, outputPath, requestPath, SitemapURLLimit)
}

func sitemapWithLimit(data value.Value, requestBase, outputPath, requestPath string, urlLimit int) (string, error) {
	entries, err := sitemapEntries(data, requestBase)
	if err != nil {
		return "", err
	}
	outputPath = path.Clean("/" + outputPath)
	requestPath = path.Clean("/" + requestPath)
	if urlLimit < 1 {
		return "", fmt.Errorf("sitemap URL limit must be positive")
	}
	pageCount := (len(entries) + urlLimit - 1) / urlLimit
	if pageCount <= 1 {
		if requestPath != outputPath {
			return "", fmt.Errorf("sitemap page does not exist")
		}
		return renderURLSet(entries)
	}
	if requestPath == outputPath {
		var body strings.Builder
		body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
		body.WriteString(`<sitemapindex xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
		for pageNumber := 1; pageNumber <= pageCount; pageNumber++ {
			start := (pageNumber - 1) * urlLimit
			end := start + urlLimit
			if end > len(entries) {
				end = len(entries)
			}
			body.WriteString(`<sitemap><loc>` + escaped(absolute(requestBase, sitemapPagePath(outputPath, pageNumber))) + `</loc>`)
			if lastmod := newestLastmod(entries[start:end]); lastmod != "" {
				body.WriteString(`<lastmod>` + escaped(lastmod) + `</lastmod>`)
			}
			body.WriteString(`</sitemap>`)
		}
		body.WriteString(`</sitemapindex>`)
		return body.String(), nil
	}
	pageNumber := requestedSitemapPage(outputPath, requestPath)
	if pageNumber < 1 || pageNumber > pageCount {
		return "", fmt.Errorf("sitemap page does not exist")
	}
	start := (pageNumber - 1) * urlLimit
	end := start + urlLimit
	if end > len(entries) {
		end = len(entries)
	}
	return renderURLSet(entries[start:end])
}
