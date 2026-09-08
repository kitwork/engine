// Package manifest links a site's web app manifest from <head>.
//
// router.manifest() publishes the document, but a manifest a browser never sees
// a <link> for does nothing at all — so unlike the other JIT passes this one has
// to reach into <head>. It follows jit/theme's rule exactly:
//
//	<link data-kitwork-jit="manifest">   an author-placed marker is filled IN
//	                                     PLACE, so the author owns the position
//	no marker                            the link is injected at the end of
//	                                     <head>
//
// A site that never declared a manifest is never touched: Render is a no-op on
// an empty path, so no page gains a link to a document that would 404.
package manifest

import (
	"html"
	"regexp"
	"strings"
)

// markerRe matches an author-placed slot. The word boundary belongs ONLY on
// the unquoted form: after a closing quote both neighbours are non-word
// characters, so  there could never match.
// markerRe matches an author-placed slot. Both prefixes are accepted, matching
// how jit/theme reads its own marker.
var markerRe = regexp.MustCompile(`(?is)<link[^>]*\bdata-kit(?:work)?-jit\s*=\s*(?:"manifest"|'manifest'|manifest\b)[^>]*>`)

var headCloseRe = regexp.MustCompile(`(?i)</head>`)

// Render links the manifest published at path. An empty path means the site
// declared none, and the document is returned untouched.
func Render(source, path string) string {
	if source == "" || path == "" {
		return source
	}
	link := `<link data-kitwork-jit="manifest" rel="manifest" href="` + html.EscapeString(path) + `">`

	if markerRe.MatchString(source) {
		// Filled in place. Replacing rather than appending keeps the pass
		// idempotent: a second run rewrites the same slot to the same bytes.
		return markerRe.ReplaceAllString(source, link)
	}

	// No marker: inject at the end of <head>, where a link belongs and where
	// nothing depends on ordering.
	if location := headCloseRe.FindStringIndex(source); location != nil {
		return source[:location[0]] + link + source[location[0]:]
	}
	if strings.TrimSpace(source) == "" {
		return source
	}
	return link + source
}
