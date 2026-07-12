// Package publishing serializes semantic site outputs. Router APIs provide structured values;
// this package owns standards details such as escaping, absolute URLs, dates, and media types.
package publishing

import (
	"bytes"
	"encoding/xml"
	"net/url"
	"strings"
	"time"

	"github.com/kitwork/engine/value"
)

const (
	RSSMediaType     = "application/rss+xml; charset=utf-8"
	SitemapMediaType = "application/xml; charset=utf-8"
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

func rssDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02", time.RFC1123Z, time.RFC1123} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC().Format(time.RFC1123Z)
		}
	}
	return raw
}

// RSS renders a version 2.0 feed from a map containing channel fields and an items array.
func RSS(data value.Value, requestBase, selfPath string) string {
	config := data.Map()
	channelLink := absolute(requestBase, text(field(config, "link", "url")))
	if channelLink == "" {
		channelLink = requestBase
	}
	self := absolute(requestBase, selfPath)

	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	body.WriteString(`<rss version="2.0" xmlns:atom="http://www.w3.org/2005/Atom"><channel>`)
	body.WriteString(`<title>` + escaped(text(field(config, "title"))) + `</title>`)
	body.WriteString(`<link>` + escaped(channelLink) + `</link>`)
	body.WriteString(`<description>` + escaped(text(field(config, "description", "summary"))) + `</description>`)
	if language := text(field(config, "language")); language != "" {
		body.WriteString(`<language>` + escaped(language) + `</language>`)
	}
	body.WriteString(`<atom:link href="` + escaped(self) + `" rel="self" type="application/rss+xml"/>`)

	items := field(config, "items", "entries").Array()
	latest := ""
	for _, item := range items {
		m := item.Map()
		link := absolute(requestBase, text(field(m, "link", "url")))
		published := rssDate(text(field(m, "published", "date", "pubDate")))
		if latest == "" && published != "" {
			latest = published
		}
		guid := text(field(m, "guid", "id"))
		if guid == "" {
			guid = link
		}

		body.WriteString(`<item>`)
		body.WriteString(`<title>` + escaped(text(field(m, "title"))) + `</title>`)
		body.WriteString(`<link>` + escaped(link) + `</link>`)
		body.WriteString(`<guid isPermaLink="true">` + escaped(guid) + `</guid>`)
		if published != "" {
			body.WriteString(`<pubDate>` + escaped(published) + `</pubDate>`)
		}
		body.WriteString(`<description>` + escaped(text(field(m, "description", "summary", "subtitle"))) + `</description>`)
		body.WriteString(`</item>`)
	}
	if latest != "" {
		body.WriteString(`<lastBuildDate>` + escaped(latest) + `</lastBuildDate>`)
	}
	body.WriteString(`</channel></rss>`)
	return body.String()
}

// Sitemap renders a standard URL set. The provider may return an array directly or a map with
// pages/entries/items. Relative locations are resolved against the current request origin.
func Sitemap(data value.Value, requestBase string) string {
	entries := data.Array()
	if data.IsMap() {
		entries = field(data.Map(), "pages", "entries", "items").Array()
	}

	seen := map[string]struct{}{}
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	body.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`)
	for _, entry := range entries {
		loc := ""
		lastmod := ""
		changefreq := ""
		priority := ""
		if entry.IsMap() {
			m := entry.Map()
			loc = text(field(m, "loc", "path", "url"))
			lastmod = text(field(m, "lastmod", "updated", "date"))
			changefreq = text(field(m, "changefreq"))
			priority = text(field(m, "priority"))
		} else {
			loc = text(entry)
		}
		loc = absolute(requestBase, loc)
		if loc == "" {
			continue
		}
		if _, exists := seen[loc]; exists {
			continue
		}
		seen[loc] = struct{}{}
		body.WriteString(`<url><loc>` + escaped(loc) + `</loc>`)
		if lastmod != "" {
			body.WriteString(`<lastmod>` + escaped(lastmod) + `</lastmod>`)
		}
		if changefreq != "" {
			body.WriteString(`<changefreq>` + escaped(changefreq) + `</changefreq>`)
		}
		if priority != "" {
			body.WriteString(`<priority>` + escaped(priority) + `</priority>`)
		}
		body.WriteString(`</url>`)
	}
	body.WriteString(`</urlset>`)
	return body.String()
}
