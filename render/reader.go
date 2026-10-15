package render

import (
	"regexp"
)

// What a screen reader says after a move on a template page. Drive keeps no live region of its own
// (11/10/2026): it says the new page's title through kit.reader, and the reader component is the live
// region that speaks it. A Kitwork UI page always has one (<reader />, or the one the engine adds to
// <page>); a template page (.kitwork.html) on a site that moves without a reload gets the same
// component here, at the top of <body>, unless it placed one itself.

// readerHostRe finds a reader host in either form: data-kit-component="reader[@version]" (a template
// page's own) or the engine's data-kitwork-component="reader[@version][: $alias]".
var readerHostRe = regexp.MustCompile(`(?i)data-kit(?:work)?-component\s*=\s*["']?\s*reader(?:@[^"'\s>:]*)?\s*[:"'\s>]`)

func withReader(out string) string {
	if readerHostRe.MatchString(out) {
		return out
	}
	at := bodyContentStart(out)
	if at < 0 {
		return out
	}
	return out[:at] + templateReader + out[at:]
}

// templateReader is the reader on a template page: the kernel's reader component on an invisible live
// region (seen by a screen reader, not by the eye: 1px and clipped, never display:none, which would hide
// it from the screen reader too). A template page has no reboot.css, so the region hides itself with a
// style attribute; a Kitwork UI page writes the same region with the class reboot.css styles.
const templateReader = `<div data-kitwork-component="reader" data-kitwork-bind="text: text" aria-live="polite" aria-atomic="true"` +
	` style="position:absolute;width:1px;height:1px;margin:-1px;padding:0;border:0;overflow:hidden;clip:rect(0 0 0 0);white-space:nowrap"></div>`
