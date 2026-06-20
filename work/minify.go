package work

import (
	"fmt"
	"regexp"
	"strings"
)

// Minification: conservative HTML + inline-CSS minify for rendered output. Whitespace-
// significant blocks (pre, textarea, script) are pulled out and restored verbatim, so
// only safe whitespace is collapsed.

// Go's RE2 has no backreferences, so each whitespace-significant tag gets its own regex.
var reProtectedTags = []*regexp.Regexp{
	regexp.MustCompile(`(?is)<pre\b[^>]*>.*?</pre>`),
	regexp.MustCompile(`(?is)<textarea\b[^>]*>.*?</textarea>`),
	regexp.MustCompile(`(?is)<script\b[^>]*>.*?</script>`),
}

var (
	reStyleBlock  = regexp.MustCompile(`(?is)(<style\b[^>]*>)(.*?)(</style>)`)
	reHTMLComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reWS          = regexp.MustCompile(`\s+`)
	reCSSComment  = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reCSSTokens   = regexp.MustCompile(`\s*([{}:;,>])\s*`)
)

// minifyOutput minifies a full HTML document: protects pre/textarea/script, minifies any
// <style> CSS, drops HTML comments, and collapses insignificant whitespace to a single
// space (kept — not removed — so inline text spacing is preserved).
func minifyOutput(s string) string {
	var protected []string
	for _, re := range reProtectedTags {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			protected = append(protected, m)
			return fmt.Sprintf("\x00P%d\x00", len(protected)-1)
		})
	}

	// Minify CSS inside <style>…</style>.
	s = reStyleBlock.ReplaceAllStringFunc(s, func(m string) string {
		g := reStyleBlock.FindStringSubmatch(m)
		return g[1] + minifyCSS(g[2]) + g[3]
	})

	s = reHTMLComment.ReplaceAllString(s, "")
	s = reWS.ReplaceAllString(s, " ")
	s = strings.TrimSpace(s)

	for i, p := range protected {
		s = strings.Replace(s, fmt.Sprintf("\x00P%d\x00", i), p, 1)
	}
	return s
}

// minifyCSS strips comments + collapses whitespace, removing it around CSS tokens while
// keeping the single spaces inside values (e.g. `0 1px 2px`).
func minifyCSS(s string) string {
	s = reCSSComment.ReplaceAllString(s, "")
	s = reWS.ReplaceAllString(s, " ")
	s = reCSSTokens.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, ";}", "}")
	return strings.TrimSpace(s)
}
