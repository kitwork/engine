package hydrate

import "html"

// authoredAttribute returns the value the browser exposes through getAttribute.
// Server-side verification and pre-rendering operate on source HTML, where
// reserved characters may be encoded as named or numeric character references.
func authoredAttribute(raw string) string {
	return html.UnescapeString(raw)
}

func compileAuthoredAttribute(raw string) (any, error) {
	return Compile(authoredAttribute(raw))
}
