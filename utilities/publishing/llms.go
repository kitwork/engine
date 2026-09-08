package publishing

import (
	"strings"

	"github.com/kitwork/engine/value"
)

// LlmsMediaType is what /llms.txt is served as. It is a Markdown document by
// convention, but plain text is what every client expects to read.
const LlmsMediaType = "text/plain; charset=utf-8"

// Llms renders an llms.txt document: a title, an optional summary blockquote,
// optional prose, then link sections. The shape follows the llmstxt.org
// convention, which is deliberately Markdown a model can read without parsing
// HTML.
//
//	router.llms({
//	  title: "Kitwork",
//	  summary: "One runtime for your whole stack.",
//	  details: "Written in Go. No V8, no Node.",
//	  sections: [
//	    { title: "Docs", links: [{ title: "Routing", url: "/docs/routing", description: "…" }] }
//	  ]
//	})
//
// A relative link is made absolute against base, because the readers of this
// file are usually not the browser that fetched it.
func Llms(data value.Value, base string) string {
	m := map[string]value.Value{}
	if data.IsMap() {
		m = data.Map()
	}

	text := func(source map[string]value.Value, names ...string) string {
		if v := field(source, names...); v.K == value.String {
			return strings.TrimSpace(v.Text())
		}
		return ""
	}

	absolute := func(link string) string {
		if link == "" || strings.Contains(link, "://") {
			return link
		}
		return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(link, "/")
	}

	var b strings.Builder

	title := text(m, "title", "name")
	if title == "" {
		title = "Site"
	}
	b.WriteString("# " + title + "\n")

	if summary := text(m, "summary", "description"); summary != "" {
		// One blockquote line: a summary that wraps stops being a blockquote.
		b.WriteString("\n> " + strings.Join(strings.Fields(summary), " ") + "\n")
	}
	if details := text(m, "details", "body"); details != "" {
		b.WriteString("\n" + details + "\n")
	}

	sections := field(m, "sections")
	if sections.K != value.Array {
		return b.String()
	}
	for _, entry := range sections.Array() {
		if !entry.IsMap() {
			continue
		}
		section := entry.Map()
		name := text(section, "title", "name")
		if name == "" {
			continue
		}
		b.WriteString("\n## " + name + "\n\n")

		links := field(section, "links", "items")
		if links.K != value.Array {
			continue
		}
		for _, item := range links.Array() {
			if !item.IsMap() {
				continue
			}
			link := item.Map()
			label := text(link, "title", "name")
			url := absolute(text(link, "url", "loc", "href"))
			if label == "" || url == "" {
				continue
			}
			b.WriteString("- [" + label + "](" + url + ")")
			if description := text(link, "description", "summary"); description != "" {
				b.WriteString(": " + strings.Join(strings.Fields(description), " "))
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}
