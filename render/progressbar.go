package render

import (
	"html"
	"regexp"
	"strings"
)

// The bar between pages on a template page. Drive draws no bar of its own (10/10/2026): the bar is the
// kernel's progressbar component, and a Kitwork UI page always has one (<box component="progressbar" />, or the one
// the engine puts first in <page>). A template page (.kitwork.html) on a site that moves without a
// reload gets the same component here, at the top of <body>, unless it placed one itself.
//
// Its line takes --kitwork-progress when the site sets it (kitwork.io colours Drive's bar that way)
// and the brand otherwise, 1px, shown after 120ms: what <box component="progressbar" /> is by default.

// progressbarHostRe finds a progressbar host in either form: data-kit-component="progressbar[@version]"
// (a template page's own), or the engine's data-kitwork-component="progressbar[@version][: $alias]".
var (
	progressbarHostRe = regexp.MustCompile(`(?i)data-kit(?:work)?-component\s*=\s*["']?\s*progressbar(?:@[^"'\s>:]*)?\s*[:"'\s>]`)
	htmlLangRe        = regexp.MustCompile(`(?is)<html\b[^>]*\blang\s*=\s*["']?([a-z]{2})`)
)

// templateProgressbarLine is the line's classes on a template page: jit/css's, which a template page's
// CSS pass styles (a Kitwork UI page names its line in its own words, thickness-1 background-brand).
var templateProgressbarLine = []string{"h-[1px]", "[background-color:var(--kitwork-progress,var(--kitwork-brand))]"}

func withProgressbar(out string) string {
	if progressbarHostRe.MatchString(out) {
		return out
	}
	at := bodyContentStart(out)
	if at < 0 {
		return out
	}
	label := "Loading the next page"
	if m := htmlLangRe.FindStringSubmatch(out); m != nil && strings.EqualFold(m[1], "vi") {
		label = "Đang chuyển trang"
	}
	return out[:at] + templateProgressbar(templateProgressbarLine, label) + out[at:]
}

// templateProgressbar is the bar on a template page: the kernel's progressbar component on a host fixed
// to the top edge of the window that takes no room, holding the line whose classes are given. The
// component reads kit.progress, which Drive reports a move to, and puts the host under a title bar the
// page draws itself (its top getter). The directives are the grouped web form (KITWORK_UI.md §11);
// hidden is the first paint. A Kitwork UI page writes the same bar with the classes reboot.css styles.
func templateProgressbar(line []string, label string) string {
	var b strings.Builder
	b.WriteString(`<div class="fixed inset-x-0 top-0 z-50 h-0 pointer-events-none" data-kitwork-component="progressbar"`)
	b.WriteString(` data-kitwork-bind="show: active; aria-valuenow: value" hidden role="progressbar"`)
	b.WriteString(` aria-label="` + html.EscapeString(label) + `"`)
	b.WriteString(` aria-valuemin="0" aria-valuemax="100" data-kitwork-style="top: top">`)
	b.WriteString(`<div class="` + strings.Join(line, " ") + ` transition-all" data-kitwork-style="width: value + '%'"></div></div>`)
	return b.String()
}

// bodyContentStart is the index just after the <body …> start tag, or -1. The tag is found with no
// lower-cased copy of the page (lower-casing changes the byte length of some characters, which shifted
// every index after one), and its end is the first > outside quotes and outside {{ }}: a quoted value
// may hold > (class="[&>*]:m-0"), and so may a template's {{ if count > 1 }}.
func bodyContentStart(s string) int {
	for from := 0; ; {
		i := indexFoldASCII(s, from, "<body")
		if i < 0 {
			return -1
		}
		next := i + len("<body")
		if next < len(s) && (s[next] == '>' || s[next] == ' ' || s[next] == '\t' || s[next] == '\n' || s[next] == '\r' || s[next] == '/') {
			return startTagEnd(s, next)
		}
		from = next // <bodyguard> or the like: keep looking
	}
}

// indexFoldASCII is the byte index of needle (ASCII) in s at or after from, ignoring ASCII case.
func indexFoldASCII(s string, from int, needle string) int {
	for i := from; i+len(needle) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// startTagEnd is the index just after the > that ends a start tag whose attributes begin at from, or
// -1 when the tag never closes.
func startTagEnd(s string, from int) int {
	depth := 0
	var quote byte
	for j := from; j < len(s); j++ {
		c := s[j]
		switch {
		case strings.HasPrefix(s[j:], "{{"):
			depth++
			j++
		case depth > 0:
			if strings.HasPrefix(s[j:], "}}") {
				depth--
				j++
			}
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return j + 1
		}
	}
	return -1
}
