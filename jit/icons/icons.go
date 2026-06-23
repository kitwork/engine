// Package icons is Kitwork's JIT icon engine — a sovereign, in-binary icon set that the render
// pipeline emits just-in-time as CSS. It is a sibling of the JIT CSS engine (jit/css) and works the
// same way: an icon name is just a class. Write `<i class="icon-shield-check"></i>` in a template,
// and Render scans the page for used `icon-<name>` classes and injects ONE inline
// `<style data-kitwork-jit="icons">` holding a mask rule for only the icons the page actually used — no
// Font Awesome, no CDN, no full-library payload, and no markup rewriting (the <i> stays as-is).
//
// Delivery technique is CSS mask: each rule sets `mask-image` to a data-URI SVG and paints it with
// `background:currentColor`. So icons are monoline, single-colour, and theme for free with `color`
// and `font-size` — and because it's plain CSS it works inline today and cross-origin tomorrow
// (a future jiticons.kitwork.io), where an inline `<svg><use>` sprite never could.
//
// Two sources feed the set: a small built-in/brand set defined below (always present), and the full
// Tabler Icons set (MIT) dropped into ./tabler/*.svg and embedded at build time. Built-in names win,
// so brand icons stay stable; Tabler fills in everything else.
package icons

import (
	"embed"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// set holds Kitwork's only built-in icon: the brand network glyph "mesh", which has no clean Tabler
// equivalent. EVERYTHING else resolves from the vendored Tabler set (below), so the whole site
// shares ONE visual style. Built-in names still win in lookup, so mesh stays stable. (Earlier this
// also carried hand-drawn lock/shield/key/… approximations; those were dropped once Tabler landed,
// so those names now render in Tabler's consistent style instead.)
var set = map[string]string{
	"mesh": `<path d="M12 12V5.5M12 12 6 18M12 12 18 18"/><circle cx="12" cy="5" r="2"/><circle cx="5.5" cy="18.2" r="2"/><circle cx="18.5" cy="18.2" r="2"/><circle cx="12" cy="12" r="1.5" fill="currentColor" stroke="none"/>`,
}

// tablerFS holds the vendored Tabler Icons (MIT) outline SVGs. Drop the files into ./tabler/ and
// rebuild; `all:` keeps the directory embeddable even before any .svg is added (only .gitkeep).
//
//go:embed all:tabler
var tablerFS embed.FS

// tablerCache memoizes parsed Tabler icons (and misses, stored as "") so we read+parse each file
// at most once — no preloading 5,800 files at startup.
var tablerCache sync.Map

// lookup resolves an icon name → its inner SVG. Built-in/brand icons win; otherwise it lazily
// reads tabler/<name>.svg from the embed and caches the result. name is a validated slug
// ([a-z0-9-]) — see scan/nameRe — so it can never escape the embedded directory.
func lookup(name string) (string, bool) {
	if inner, ok := set[name]; ok {
		return inner, true
	}
	if v, ok := tablerCache.Load(name); ok {
		s := v.(string)
		return s, s != ""
	}
	inner := ""
	if data, err := tablerFS.ReadFile("tabler/" + name + ".svg"); err == nil {
		inner = svgInner(string(data))
	}
	tablerCache.Store(name, inner)
	return inner, inner != ""
}

// svgInner pulls the drawable content out of a full Tabler <svg>…</svg> file (dropping the outer
// <svg> wrapper and Tabler's invisible 24×24 bounding-box spacer path). Tabler files open with an
// HTML comment header (<!-- tags: … -->) and a multi-line <svg> tag, so we locate the real "<svg"
// element and the FIRST '>' after it — not the first '>' in the file (that '>' is the comment's).
func svgInner(svg string) string {
	lo := strings.Index(svg, "<svg")
	if lo < 0 {
		return ""
	}
	gt := strings.IndexByte(svg[lo:], '>')
	if gt < 0 {
		return ""
	}
	open := lo + gt + 1
	j := strings.LastIndex(svg, "</svg>")
	if j <= open {
		return ""
	}
	inner := svg[open:j]
	inner = strings.ReplaceAll(inner, `<path stroke="none" d="M0 0h24v24H0z" fill="none"/>`, "")
	return strings.TrimSpace(inner)
}

// Has reports whether the named icon exists (built-in or vendored Tabler).
func Has(name string) bool { _, ok := lookup(name); return ok }

// Names returns every available icon name (built-in + vendored Tabler), sorted — for a gallery.
func Names() []string {
	seen := make(map[string]bool)
	var out []string
	for n := range set {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if entries, err := tablerFS.ReadDir("tabler"); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".svg") {
				continue
			}
			n := strings.TrimSuffix(e.Name(), ".svg")
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Strings(out)
	return out
}

// ----------------------------------------------------------------------------
// CSS GENERATION (mask technique)
// ----------------------------------------------------------------------------

const (
	// classPrefix is the icon class namespace: <i class="icon-<name>">.
	classPrefix = "icon-"
	// strokeWidth is baked into the mask SVG. Mask alpha can't be re-stroked from CSS, so weight is
	// fixed at generation time; a future `icon-w-*` token would generate a heavier variant.
	strokeWidth = "1.75"
	// styleMarker tags our injected <style> so the client (Kitwork Drive) can swap it per page. It
	// shares the data-kitwork-jit namespace with jit/css under a distinct VALUE ("icons" vs "css"),
	// so one `style[data-kitwork-jit]` selector handles every JIT stylesheet — icons, css, and
	// whatever comes next (components, …) — while each engine still owns its own block.
	styleMarker = `data-kitwork-jit="icons"`
)

// nameRe validates an icon slug — lowercase, digits, dashes. Anchored, so a class token like
// `my-icon-thing` (which only contains the prefix mid-string) can never slip through scan, and a
// name can never traverse out of the embedded tabler/ directory.
var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// classAttrRe captures the value of each class="…" attribute so scan can tokenise it.
var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)

// uriEncode escapes the characters that matter inside a CSS url("data:image/svg+xml,…"). The SVG is
// authored with single quotes (see dataURI) so double quotes never appear. strings.Replacer is a
// single pass, so the `%` it emits for `#`/`<`/`>` is not re-encoded.
var uriEncode = strings.NewReplacer(
	"%", "%25",
	"#", "%23",
	"<", "%3C",
	">", "%3E",
)

// dataURI wraps inner SVG content in a standalone <svg> and returns a CSS url("data:…") for it.
// stroke is baked to #000 because a mask only reads alpha (#000 = fully opaque); the visible colour
// comes from background:currentColor on the element. currentColor inside the data-URI won't resolve
// (it's an isolated image), so it's pinned to #000 too.
func dataURI(inner string) string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="#000" stroke-width="` +
		strokeWidth + `" stroke-linecap="round" stroke-linejoin="round">` + inner + `</svg>`
	svg = strings.ReplaceAll(svg, "currentColor", "#000")
	svg = strings.ReplaceAll(svg, `"`, `'`)    // single quotes so the data-URI fits in url("…")
	svg = strings.Join(strings.Fields(svg), " ") // collapse whitespace/newlines
	return `url("data:image/svg+xml,` + uriEncode.Replace(svg) + `")`
}

// CSS returns the JIT stylesheet for the given icon names — a no-op "" if none exist. It emits ONE
// shared base rule enumerating the exact selectors (so it never substring-matches a tenant's own
// `*-icon-*` class), then one `--i` mask variable per icon. Names are deduped and sorted for a
// stable, cacheable output.
func CSS(names []string) string {
	type ic struct{ name, inner string }
	seen := make(map[string]bool)
	var list []ic
	for _, n := range names {
		if seen[n] {
			continue
		}
		inner, ok := lookup(n)
		if !ok {
			continue
		}
		seen[n] = true
		list = append(list, ic{n, inner})
	}
	if len(list) == 0 {
		return ""
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })

	// Base rule wrapped in :where() so it has ZERO specificity — any utility class the author puts
	// on the <i> (w-4, h-4, a font-size, or `hidden` to toggle it) overrides these defaults, while
	// an icon with no sizing still falls back to 1em. Without :where() this block (injected after
	// the jitcss block) would win at equal specificity and pin every icon to 1em / break .hidden.
	var b strings.Builder
	b.WriteString(":where(")
	for i, it := range list {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("." + classPrefix + it.name)
	}
	b.WriteString(`){display:inline-block;width:1em;height:1em;vertical-align:-.125em;flex:none;` +
		`background:currentColor;-webkit-mask:var(--i) center/contain no-repeat;mask:var(--i) center/contain no-repeat}`)
	for _, it := range list {
		b.WriteString("." + classPrefix + it.name + "{--i:" + dataURI(it.inner) + "}")
	}
	return b.String()
}

// SiteCSS is the site-wide form of CSS: it scans many HTML sources (a whole tenant's templates) for
// used icon-<name> classes and returns ONE stylesheet covering every icon any of them reference —
// for the shared /jiticons service route (vs CSS, which is per-page). Empty when no icons are used.
func SiteCSS(htmls ...string) string {
	seen := make(map[string]bool)
	var names []string
	for _, h := range htmls {
		for _, n := range scan(h) {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	return CSS(names)
}

// scan collects the distinct `icon-<name>` class tokens used in html that resolve to a known icon.
// It tokenises real class="…" attributes (not the raw text), so `icon-` mid-string never matches.
func scan(html string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, tok := range strings.Fields(m[1]) {
			if !strings.HasPrefix(tok, classPrefix) {
				continue
			}
			name := tok[len(classPrefix):]
			if name == "" || seen[name] || !nameRe.MatchString(name) || !Has(name) {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Render is the JIT entry point. It scans html for used `icon-<name>` classes and, if any resolve
// to a real icon, injects ONE `<style data-kitwork-jit="icons">` (its own block in the shared JIT
// namespace) right before </head>. The markup itself is left untouched — the CSS does all the
// work. A cheap no-op when the page references no icons.
func Render(html string) string {
	if !strings.Contains(html, classPrefix) {
		return html
	}
	names := scan(html)
	if len(names) == 0 {
		return html
	}
	css := CSS(names)
	if css == "" {
		return html
	}
	style := "<style " + styleMarker + ">" + css + "</style>"
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + style + html[i:]
	}
	return style + html
}
