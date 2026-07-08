// Package theme is the JIT theme pre-paint. A page opts in by placing an empty marker in <head>:
//
//	<script data-kitwork-jit="theme"></script>
//
// Render replaces it with a tiny SYNCHRONOUS script that applies the saved theme (or the OS
// preference) BEFORE first paint — so there is no flash of the wrong theme. It reads the same
// "theme" localStorage key the toggle writes (the jitjs `data-kitwork-action="theme"` verb / the
// `theme` component), so pre-paint and toggle stay in sync. The dark theme is the `.dark` class on
// <html> — matching jitcss darkMode:['class'] and the dark: variant.
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

// Render swaps the theme marker for the pre-paint script. A cheap no-op when the marker is absent.
func Render(html string) string {
	if !strings.Contains(html, `-jit="theme"`) {
		return html
	}
	return markerRe.ReplaceAllString(html, prepaint)
}
