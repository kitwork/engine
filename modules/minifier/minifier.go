// Package minifier is the engine's content-type-aware minifier for everything that can be
// embedded in an HTML document — the markup itself plus inline CSS, JS, JSON(-LD), SVG and XML.
//
// Two implementations are selected at build time, exposing the SAME package-level functions
// (HTML, CSS, JS, JSON, SVG, XML, Type) so callers are build-tag-agnostic:
//   - tdewolff.go (default): real streaming parsers (tdewolff/minify); registers every media-type
//     spelling a browser accepts, so JSON-LD structured data and module scripts are covered too.
//   - std.go (`-tags stdminify`): pure-stdlib regex, zero external dependencies — a fully
//     sovereign build that trades speed/compression for no deps (js/json/svg/xml pass through).
//
// The service is stateless and concurrency-safe (one shared parser instance), so it is exposed as
// plain functions rather than a constructed object — the package name is the namespace. Every
// function returns the input unchanged on a parse error, so it can never emit corrupt output.
package minifier

// AllTypes is the full set turned on by render.minify() with no args, and by
// router.context({ minify: true }). render.minify("css", "js") selects a subset instead.
var AllTypes = []string{"html", "css", "js", "json", "svg"}
