// Package theme is the JIT theme pre-paint: the SSR/first-paint half of the kernel's $app.theme.
//
// A tiny SYNCHRONOUS script must run in <head> BEFORE first paint to apply the saved theme, or the
// page flashes the wrong colours (the hydrate kernel loads too late to prevent it). Render injects
// that script AUTOMATICALLY when the page uses the theme system — the same zero-config rule as the
// rest of the JIT family (icons/fonts/js scan usage and emit only what's needed). "Uses the theme
// system" means any of: $app.toggleTheme() / $app.theme (the kernel API), or the legacy
// data-kit(work)-action/component="theme" (kept working through the transition).
//
// A page may instead place an explicit marker to control the exact position:
//
//	<script data-kitwork-jit="theme"></script>
//
// When the marker is present it is replaced in place (author wins); otherwise the script is injected
// at the very TOP of <head> — the earliest point, before any stylesheet. Either way it reads the
// same "theme" localStorage key the $app.theme setter writes, so pre-paint and toggle stay in sync.
// Dark = the `.dark` class on <html>, matching jitcss darkMode:['class'] and the dark: variant.
package theme

import (
	"regexp"
	"strings"
)

// prepaint runs before paint: the STORED theme wins (add/remove .dark); with no stored value the
// SSR default on <html> is kept untouched — so a site can ship `<html class="dark">` (dark by
// default) or `<html>` (light) and the toggle just overrides it. Kept inline + synchronous on
// purpose — a deferred runtime cannot prevent the flash.
const prepaint = `<script>(function(){try{var t=localStorage.getItem("theme"),c=document.documentElement.classList;` +
	`if(t==="dark")c.add("dark");else if(t==="light")c.remove("dark")}catch(e){}})();</script>`

// markerRe matches <script data-kitwork-jit="theme"></script> (also data-kit-jit, extra attrs,
// whitespace inside).
var markerRe = regexp.MustCompile(`(?is)<script[^>]*\bdata-kit(?:work)?-jit="theme"[^>]*>\s*</script>`)

// Render injects the pre-paint. An explicit marker is replaced in place; otherwise, if the page uses
// the theme system, the script is inserted at the top of <head>. A cheap no-op for pages that do
// neither.
func Render(html string) string {
	if strings.Contains(html, `-jit="theme"`) {
		return markerRe.ReplaceAllString(html, prepaint)
	}
	if usesTheme(html) {
		return injectHeadTop(html, prepaint)
	}
	return html
}

// usesTheme reports whether the page references the theme system. Cheap substring checks:
// `toggleTheme` catches $app.toggleTheme(); `$app.theme` catches reads/binds; the action/component
// forms catch the legacy jitjs verb + component (data-kit- and data-kitwork- both end this way).
func usesTheme(html string) bool {
	return strings.Contains(html, "toggleTheme") ||
		strings.Contains(html, "$app.theme") ||
		strings.Contains(html, `action="theme"`) ||
		strings.Contains(html, `component="theme"`)
}

// injectHeadTop inserts snippet immediately after the opening <head ...> tag (the earliest point,
// before any stylesheet). With no <head>, it prepends — still before body content.
func injectHeadTop(html, snippet string) string {
	i := strings.Index(html, "<head")
	if i < 0 {
		return snippet + html
	}
	j := strings.Index(html[i:], ">")
	if j < 0 {
		return snippet + html
	}
	pos := i + j + 1
	return html[:pos] + snippet + html[pos:]
}
