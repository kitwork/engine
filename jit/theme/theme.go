// Package theme is the JIT appearance pre-paint: the first-paint half of the
// appearance service and its versioned theme adapter.
//
// A tiny synchronous script must run in <head> before first paint to apply the saved theme, or the
// deferred browser runtime can reveal the wrong palette briefly. Render injects that script when
// it sees the theme component or a supported legacy theme reference. Force is used by
// router.jittheme(true) when a site declares theming at its root.
//
// A page may instead place an explicit marker to control the exact position:
//
//	<script data-kitwork-jit="theme"></script>
//
// When the marker is present it is filled in place (author wins); otherwise the script is injected
// at the very TOP of <head> -- the earliest point, before any stylesheet. Either way it reads the
// same bare "theme" localStorage key appearance@1.0.0 owns, so pre-paint and runtime stay in sync.
// Dark = the `.dark` class on <html>, matching jitcss darkMode:['class'] and the dark: variant;
// color-scheme is applied at the same time so browser-native controls do not flash the wrong mode.
package theme

import (
	htmlstd "html"
	"regexp"
	"strings"
	"unicode"
)

// prepaint runs before paint: an explicit stored light/dark mode wins; missing, invalid, or
// "system" follows the OS preference. Storage failures must not skip matchMedia. Kept inline and
// synchronous on purpose: a deferred runtime cannot prevent the flash.
const prepaintBody = `(function(){var r=document.documentElement,c=r.classList,m="system";` +
	`try{var t=localStorage.getItem("theme");t=t&&t.toLowerCase();if(t==="light"||t==="dark"||t==="system")m=t}catch(e){}` +
	`if(m==="system"){try{m=typeof matchMedia==="function"&&matchMedia("(prefers-color-scheme: dark)").matches?"dark":"light"}catch(e){m="light"}}` +
	`if(m==="dark")c.add("dark");else c.remove("dark");try{r.style.colorScheme=m}catch(e){}})();`

const prepaint = `<script data-kitwork-jit="theme">` + prepaintBody + `</script>`

// The matchers intentionally accept ordinary HTML spelling differences. Rendering must not flash
// merely because an author used single quotes, uppercase attribute names, or whitespace around an
// expression member operator.
var (
	markerRe    = regexp.MustCompile(`(?is)(?:<script[^>]*\bdata-kit(?:work)?-jit\s*=\s*(?:"theme"|'theme')[^>]*>|<script[^>]*\bdata-kit(?:work)?-jit\s*=\s*theme(?:\s+[^>]*)?/?>).*?</script>`)
	componentRe = regexp.MustCompile(`(?is)\bdata-kit(?:work)?-component\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
	actionRe    = regexp.MustCompile(`(?is)\bdata-kit(?:work)?-action\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>]+))`)
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

// Force injects the pre-paint unconditionally: router.jittheme(true) declares theming
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
// present in this particular document. The other forms keep the component adapter and historical
// trusted-JavaScript spellings working.
func usesTheme(source string) bool {
	decoded := htmlstd.UnescapeString(source)
	return componentValueIs(componentRe, decoded, "app", "theme") ||
		attributeValueIs(actionRe, decoded, "theme") || apiRe.MatchString(decoded)
}

// componentValueIs accepts the client-owned unversioned spelling and the
// server-owned name@exact-version spelling. Exact SemVer validation belongs
// to the KitJS scanner; this pre-paint detector only needs the base identity
// and must not miss an already validated managed host.
func componentValueIs(pattern *regexp.Regexp, source string, expected ...string) bool {
	for _, match := range pattern.FindAllStringSubmatch(source, -1) {
		value := matchedAttributeValue(match)
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

func attributeValueIs(pattern *regexp.Regexp, source string, expected ...string) bool {
	for _, match := range pattern.FindAllStringSubmatch(source, -1) {
		value := matchedAttributeValue(match)
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
			return strings.TrimFunc(match[index], isECMAScriptSpace)
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
