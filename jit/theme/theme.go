// Package theme is the JIT appearance pre-paint: the first-paint half of the
// appearance service (kit.appearance) and its versioned theme adapter.
//
// A tiny synchronous script must run in <head> before first paint to apply the light/dark mode, or
// the deferred browser runtime can reveal the wrong palette briefly. Render injects that script when
// it sees the appearance or theme component or a supported legacy theme reference. Force is used
// when the site declared its dark at the root (router.css({ darkMode: ["class"] }) /
// router.themes({ dark: true })).
//
// A page may instead place an explicit marker to control the exact position:
//
//	<script data-kitwork-generated="appearance"></script>
//
// The marker was data-kitwork-generated="theme" until the light/dark service became kit.appearance
// (11/10/2026), and data-kitwork-jit="theme" before 10/10/2026. A template or a cached page may carry
// either, so all three pin the slot and are rewritten to today's name.
//
// When the marker is present it is filled in place (author wins); otherwise the script is injected
// at the very TOP of <head> -- the earliest point, before any stylesheet. Either way it decides the
// mode by the rule kit.appearance (alias kit.theme) follows, so pre-paint and runtime stay in sync:
// the visitor's choice in localStorage "appearance", or in the older "theme" key when "appearance"
// holds no choice (a choice saved before the rename survives it); with no choice stored, the mode
// the site shows before the visitor chooses — router.appearance(), written on the page as
// <html data-kitwork-appearance="light|dark"> (nothing for "system") — and with neither, the device.
// Dark = the `.dark` class on <html>, matching jitcss darkMode:['class'] and the dark: variant;
// color-scheme is applied at the same time so browser-native controls do not flash the wrong mode.
package theme

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/kitwork/engine/jit/internal/htmlattr"
)

// prepaint runs before paint and decides the mode in three steps — the rule the kernel's
// kit.appearance follows too (jit/hydrate/modules/web.js), so the first paint and the runtime never
// disagree:
//
//  1. the visitor's choice: localStorage "appearance", or the older "theme" key only when
//     "appearance" holds none of the three words (light, dark, system);
//  2. with no choice stored, the site's default: <html data-kitwork-appearance>, which the server
//     writes from router.appearance() when the route's mode is not "system";
//  3. otherwise "system".
//
// "system" — stored, defaulted, or neither — follows the OS preference. A stored "system" is a
// choice like the other two, so a visitor can follow the device on a site whose default is dark.
// A storage failure must skip neither the default nor matchMedia. The default is read off the page,
// never written into the script, so the body stays ONE constant and one CSP hash (csp.go) serves
// every site. Kept inline and synchronous on purpose: a deferred runtime cannot prevent the flash.
// The resolved mode lands twice on <html>: the `dark` class (Tailwind's darkMode: ['class'], every
// existing site) and data-theme="light|dark" — the attribute is the selector router.themes() modes
// are scoped under, so a site may write darkMode: ['class', '[data-theme="dark"]'] and later add a
// mode without a class per mode. A Kitwork UI page with a backdrop writes its <meta name="theme-color">
// before the pre-paint, each carrying the colour of both modes (data-kitwork-light, data-kitwork-dark):
// the mode shown sets its content and drops its media, so the browser's bar follows a visitor's choice
// of the other mode than the device's, as kit.appearance does again when the choice changes.
const prepaintBody = `(function(){var r=document.documentElement,c=r.classList,m="";` +
	`function v(t){t=t&&t.toLowerCase();return t==="light"||t==="dark"||t==="system"?t:""}` +
	`try{var s=localStorage;m=v(s.getItem("appearance"))||v(s.getItem("theme"))}catch(e){}` +
	`m=m||v(r.getAttribute("data-kitwork-appearance"))||"system";` +
	`if(m==="system"){try{m=typeof matchMedia==="function"&&matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"}catch(e){m="light"}}` +
	`if(m==="dark")c.add("dark");else c.remove("dark");try{r.setAttribute("data-theme",m);r.style.colorScheme=m;` +
	`document.querySelectorAll('meta[name="theme-color"][data-kitwork-'+m+']').forEach(function(n){n.setAttribute("content",n.getAttribute("data-kitwork-"+m));n.removeAttribute("media")})}catch(e){}})();`

// prepaintMarker is the attribute the engine writes on its pre-paint today.
const prepaintMarker = `data-kitwork-generated="appearance"`

const prepaint = `<script ` + prepaintMarker + `>` + prepaintBody + `</script>`

// prepaintMarkers is every spelling a rendered page may carry the engine's pre-paint under: today's,
// the one before the light/dark service became kit.appearance (11/10/2026), and the one before the
// data-kitwork-jit → data-kitwork-generated rename (10/10/2026).
var prepaintMarkers = []string{prepaintMarker, `data-kitwork-generated="theme"`, `data-kitwork-jit="theme"`}

// The matchers intentionally accept ordinary HTML spelling differences. Rendering must not flash
// merely because an author used single quotes, uppercase attribute names, or whitespace around an
// expression member operator. The marker's value is "appearance" (today) or "theme" (before
// 11/10/2026), under data-kitwork-generated or the older data-kitwork-jit.
var (
	markerRe    = regexp.MustCompile(`(?is)(?:<script[^>]*\bdata-kit(?:work)?-(?:generated|jit)\s*=\s*(?:"(?:appearance|theme)"|'(?:appearance|theme)')[^>]*>|<script[^>]*\bdata-kit(?:work)?-(?:generated|jit)\s*=\s*(?:appearance|theme)(?:\s+[^>]*)?/?>).*?</script>`)
	componentRe = regexp.MustCompile(`(?is)\bdata-kit(?:work)?-component\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
	apiRe       = regexp.MustCompile(`(?i)(?:\bkit|\$app)\s*\.\s*(?:appearance|theme)\b|\btoggleTheme\b`)
	headOpenRe  = regexp.MustCompile(`(?is)<head(?:\s[^>]*)?>`)
)

// Render injects the pre-paint. An explicit marker is replaced in place; otherwise, if the page uses
// the theme system, the script is inserted at the top of <head>. A cheap no-op for pages that do
// neither.
func Render(source string) string {
	if markerRe.MatchString(source) {
		return Canonicalize(source)
	}
	if usesTheme(source) {
		return injectHeadTop(source, prepaint)
	}
	return source
}

// Force injects the pre-paint unconditionally: a site that declared its dark at the root has theming
// once at the root instead of relying on the usage scan (e.g. the toggle lives on a page the scan
// can't see, or theming is applied by external scripts). A marker still pins the position.
func Force(source string) string {
	if markerRe.MatchString(source) {
		return Canonicalize(source)
	}
	return injectHeadTop(source, prepaint)
}

// Canonicalize restores the exact engine pre-paint after an HTML/JavaScript minifier has rewritten
// its inline body. It never introduces a marker that is not already present. Keeping one exact body
// lets Drive distinguish this engine-owned synchronous script from ordinary authored inline code.
func Canonicalize(source string) string {
	return markerRe.ReplaceAllString(source, prepaint)
}

// usesTheme reports whether the page references the appearance system. app@1 always closes
// appearance@1 into its exact graph, so an app host needs the same pre-paint even when no toggle is
// present in this particular document. appearance is the light/dark toggle of a Kitwork UI page
// (component="appearance", written data-kitwork-component="appearance"), theme its template-page component. The
// other forms keep the component adapter and historical trusted-JavaScript spellings working. (The
// jitjs verb `data-kit-action="theme"` is gone, 22/09.)
func usesTheme(source string) bool {
	return componentValueIs(componentRe, source, "app", "theme", "appearance") || apiRe.MatchString(source)
}

// componentValueIs accepts the client-owned unversioned spelling and the
// server-owned name@exact-version spelling, and the grouped form's alias tail
// ("appearance: $theme"). Exact SemVer validation belongs to the KitJS scanner;
// this pre-paint detector only needs the base identity and must not miss an
// already validated managed host.
func componentValueIs(pattern *regexp.Regexp, source string, expected ...string) bool {
	for _, match := range pattern.FindAllStringSubmatch(source, -1) {
		value := matchedAttributeValue(match)
		if separator := strings.IndexByte(value, ':'); separator >= 0 {
			value = strings.TrimFunc(value[:separator], isECMAScriptSpace)
		}
		if separator := strings.IndexByte(value, '@'); separator >= 0 {
			value = value[:separator]
		}
		for _, candidate := range expected {
			if strings.EqualFold(value, candidate) {
				return true
			}
		}
	}
	return false
}

func matchedAttributeValue(match []string) string {
	for index := 1; index < len(match); index++ {
		if match[index] != "" {
			return strings.TrimFunc(htmlattr.Decode(match[index]), isECMAScriptSpace)
		}
	}
	return ""
}

func isECMAScriptSpace(character rune) bool {
	return character == '\t' || character == '\v' || character == '\f' || character == ' ' ||
		character == '\u00a0' || character == '\ufeff' || character == '\n' || character == '\r' ||
		character == '\u2028' || character == '\u2029' || unicode.Is(unicode.Zs, character)
}

// injectHeadTop inserts snippet immediately after the opening <head ...> tag (the earliest point,
// before any stylesheet). With no <head>, it prepends -- still before body content.
func injectHeadTop(source, snippet string) string {
	match := headOpenRe.FindStringIndex(source)
	if match == nil {
		return snippet + source
	}
	pos := match[1]
	return source[:pos] + snippet + source[pos:]
}
