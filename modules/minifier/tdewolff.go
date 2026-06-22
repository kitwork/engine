//go:build !stdminify

package minifier

// DEFAULT minifier — tdewolff/minify, a real streaming parser for every content type that can be
// embedded in an HTML document. Opt out with `-tags stdminify` to get the pure-stdlib regex
// variant (std.go).
//
// One shared, concurrency-safe *minify.M backs every call. The HTML minifier cascades into
// everything inline — <style>, <script> (incl. type="module" and on* event-handler attributes),
// <script type="application/ld+json"> structured data, and inline <svg> — minifying each with the
// matching sub-minifier registered below. Any parse error returns the input unchanged, so a single
// odd block can never corrupt a page.
//
// Media types are registered with AddFuncRegexp (not exact strings) for the js/json/xml families,
// so EVERY spelling a browser accepts is covered — e.g. JS as text/javascript |
// application/javascript | x-javascript | ecmascript, and JSON as application/json |
// application/ld+json | *+json. Exact-match would silently skip ld+json (SEO structured data) and
// module scripts.

import (
	"regexp"

	"github.com/tdewolff/minify/v2"
	"github.com/tdewolff/minify/v2/css"
	"github.com/tdewolff/minify/v2/html"
	"github.com/tdewolff/minify/v2/js"
	"github.com/tdewolff/minify/v2/json"
	"github.com/tdewolff/minify/v2/svg"
	"github.com/tdewolff/minify/v2/xml"
)

// media maps the short type names used by render.minify([...]) to a canonical media type. Each
// canonical value matches the corresponding registration (regexp) on shared.
var media = map[string]string{
	"html": "text/html",
	"css":  "text/css",
	"js":   "application/javascript",
	"json": "application/json",
	"svg":  "image/svg+xml",
	"xml":  "application/xml",
}

// Canonical tdewolff configuration: exact funcs for html/css/svg, regexp funcs for the families
// (js / json / xml) so every media-type spelling that can appear inside HTML is minified. Exact
// literals win over regexps in tdewolff, so image/svg+xml resolves to svg.Minify (not xml).
var shared = func() *minify.M {
	m := minify.New()
	m.AddFunc("text/html", html.Minify)
	m.AddFunc("text/css", css.Minify)
	m.AddFunc("image/svg+xml", svg.Minify)
	m.AddFuncRegexp(regexp.MustCompile(`^(application|text)/(x-)?(java|ecma)script$`), js.Minify)
	m.AddFuncRegexp(regexp.MustCompile(`[/+]json$`), json.Minify)
	m.AddFuncRegexp(regexp.MustCompile(`[/+]xml$`), xml.Minify)
	return m
}()

// Type minifies s as the given short type name ("html"/"css"/"js"/"json"/"svg"/"xml"). An unknown
// type or a parse error returns s unchanged.
func Type(t, s string) string {
	mediatype, ok := media[t]
	if !ok {
		return s
	}
	out, err := shared.String(mediatype, s)
	if err != nil {
		return s
	}
	return out
}

func HTML(s string) string { return Type("html", s) }
func CSS(s string) string  { return Type("css", s) }
func JS(s string) string   { return Type("js", s) }
func JSON(s string) string { return Type("json", s) }
func SVG(s string) string  { return Type("svg", s) }
func XML(s string) string  { return Type("xml", s) }
