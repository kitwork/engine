// Package logo is Kitwork's JIT brand-logo engine — a sibling of jit/icons that does for BRAND
// LOGOS what jit/icons does for UI icons. Write `<i class="logo-github"></i>`; Render scans used
// `logo-<name>` classes and injects ONE inline `<style data-kitwork-jit="logo">` with a CSS-mask
// rule for only the logos the page uses — monoline single-colour, themeable with `color`.
//
// Same mask technique as jit/icons, but logos are FILLED single-path silhouettes (Simple Icons),
// so the data-URI wrapper bakes `fill` (not `stroke`). Two sources: a small built-in/brand set
// below (always present), and the full Simple Icons set (CC0) dropped into ./simple/*.svg and
// embedded at build time. Built-in names win; Simple Icons fills the long tail. NOTE: brand names
// and logos are trademarks of their owners — use them nominatively (to refer to the brand), not to
// imply endorsement.
package logo

import (
	"embed"
	"regexp"
	"sort"
	"strings"
	"sync"

	css "github.com/kitwork/engine/jit/css"
)

// set maps a logo name → inner SVG (a filled <path>, 0 0 24 24). Brand/fallback set; Simple Icons
// (embedded below) supplies the rest. Paths are from Simple Icons (CC0).
var set = map[string]string{
	"github":   `<path d="M12 .297c-6.63 0-12 5.373-12 12 0 5.303 3.438 9.8 8.205 11.385.6.113.82-.258.82-.577 0-.285-.01-1.04-.015-2.04-3.338.724-4.042-1.61-4.042-1.61C4.422 18.07 3.633 17.7 3.633 17.7c-1.087-.744.084-.729.084-.729 1.205.084 1.838 1.236 1.838 1.236 1.07 1.835 2.809 1.305 3.495.998.108-.776.417-1.305.76-1.605-2.665-.3-5.466-1.332-5.466-5.93 0-1.31.465-2.38 1.235-3.22-.135-.303-.54-1.523.105-3.176 0 0 1.005-.322 3.3 1.23.96-.267 1.98-.399 3-.405 1.02.006 2.04.138 3 .405 2.28-1.552 3.285-1.23 3.285-1.23.645 1.653.24 2.873.12 3.176.765.84 1.23 1.91 1.23 3.22 0 4.61-2.805 5.625-5.475 5.92.42.36.81 1.096.81 2.22 0 1.606-.015 2.896-.015 3.286 0 .315.21.69.825.57C20.565 22.092 24 17.592 24 12.297c0-6.627-5.373-12-12-12"/>`,
	"x":        `<path d="M14.234 10.162 22.977 0h-2.072l-7.591 8.824L7.251 0H.258l9.168 13.343L.258 24H2.33l8.016-9.318L16.749 24h6.993zm-2.837 3.299-.929-1.329L3.076 1.56h3.182l5.965 8.532.929 1.329 7.754 11.09h-3.182z"/>`,
	"apple":    `<path d="M12.152 6.896c-.948 0-2.415-1.078-3.96-1.04-2.04.027-3.91 1.183-4.961 3.014-2.117 3.675-.546 9.103 1.519 12.09 1.013 1.454 2.208 3.09 3.792 3.039 1.52-.065 2.09-.987 3.935-.987 1.831 0 2.35.987 3.96.948 1.637-.026 2.676-1.48 3.676-2.948 1.156-1.688 1.636-3.325 1.662-3.415-.039-.013-3.182-1.221-3.22-4.857-.026-3.04 2.48-4.494 2.597-4.559-1.429-2.09-3.623-2.324-4.39-2.376-2-.156-3.675 1.09-4.61 1.09zM15.53 3.83c.843-1.012 1.4-2.427 1.245-3.83-1.207.052-2.662.805-3.532 1.818-.78.896-1.454 2.338-1.273 3.714 1.338.104 2.715-.688 3.559-1.701"/>`,
	"google":   `<path d="M12.48 10.92v3.28h7.84c-.24 1.84-.853 3.187-1.787 4.133-1.147 1.147-2.933 2.4-6.053 2.4-4.827 0-8.6-3.893-8.6-8.72s3.773-8.72 8.6-8.72c2.6 0 4.507 1.027 5.907 2.347l2.307-2.307C18.747 1.44 16.133 0 12.48 0 5.867 0 .307 5.387.307 12s5.56 12 12.173 12c3.573 0 6.267-1.173 8.373-3.36 2.16-2.16 2.84-5.213 2.84-7.667 0-.76-.053-1.467-.173-2.053H12.48z"/>`,
	"youtube":  `<path d="M23.498 6.186a3.016 3.016 0 0 0-2.122-2.136C19.505 3.545 12 3.545 12 3.545s-7.505 0-9.377.505A3.017 3.017 0 0 0 .502 6.186C0 8.07 0 12 0 12s0 3.93.502 5.814a3.016 3.016 0 0 0 2.122 2.136c1.871.505 9.376.505 9.376.505s7.505 0 9.377-.505a3.015 3.015 0 0 0 2.122-2.136C24 15.93 24 12 24 12s0-3.93-.502-5.814zM9.545 15.568V8.432L15.818 12l-6.273 3.568z"/>`,
	"gmail":    `<path d="M24 5.457v13.909c0 .904-.732 1.636-1.636 1.636h-3.819V11.73L12 16.64l-6.545-4.91v9.273H1.636A1.636 1.636 0 0 1 0 19.366V5.457c0-2.023 2.309-3.178 3.927-1.964L5.455 4.64 12 9.548l6.545-4.91 1.528-1.145C21.69 2.28 24 3.434 24 5.457z"/>`,
	"substack": `<path d="M22.539 8.242H1.46V5.406h21.08v2.836zM1.46 10.812V24L12 18.11 22.54 24V10.812H1.46zM22.54 0H1.46v2.836h21.08V0z"/>`,
	"rss":      `<path d="M19.199 24C19.199 13.467 10.533 4.8 0 4.8V0c13.165 0 24 10.835 24 24h-4.801zM3.291 17.415c1.814 0 3.293 1.479 3.293 3.295 0 1.813-1.485 3.29-3.301 3.29C1.47 24 0 22.526 0 20.71s1.475-3.294 3.291-3.295zM15.909 24h-4.665c0-6.169-5.075-11.245-11.244-11.245V8.09c8.727 0 15.909 7.184 15.909 15.91z"/>`,
	"vercel":   `<path d="m12 1.608 12 20.784H0Z"/>`,
}

// brandColor maps a logo slug → its official Simple Icons brand hex. Two consumers: the `-brand`
// sugar here (`logo-github-brand` = the shape painted in its own brand colour) and jit/css's
// `text-brand-<slug>` / `bg-brand-<slug>` colour utilities (via BrandHex, registered below). The
// map is GENERATED in colors_gen.go from Simple Icons' data/simple-icons.json; a slug absent from
// it just stays currentColor (monochrome).

// BrandHex returns a logo slug's official brand hex ("#181717", true) or ("", false). It backs
// jit/css's `text-brand-<slug>` / `bg-brand-<slug>` colour utilities — registered into the css
// colour resolver at init so `text-brand-github`, `hover:border-brand-stripe`, gradient stops,
// alpha and every variant resolve, without jit/css importing jit/logo.
func BrandHex(slug string) (string, bool) {
	hex, ok := brandColor[slug]
	return hex, ok
}

func init() { css.RegisterBrandPalette(BrandHex) }

// simpleFS holds the vendored Simple Icons (CC0) logos. Drop the files into ./simple/ and rebuild;
// `all:` keeps the directory embeddable even before any .svg is added (only .gitkeep).
//
//go:embed all:simple
var simpleFS embed.FS

var simpleCache sync.Map // memoizes parsed Simple Icons (and misses as "")

// lookup resolves a logo name → its inner SVG (a filled <path>). Built-in/brand logos win; otherwise
// it lazily reads simple/<name>.svg from the embed and caches. name is a validated slug.
func lookup(name string) (string, bool) {
	if inner, ok := set[name]; ok {
		return inner, true
	}
	if v, ok := simpleCache.Load(name); ok {
		s := v.(string)
		return s, s != ""
	}
	inner := ""
	if data, err := simpleFS.ReadFile("simple/" + name + ".svg"); err == nil {
		inner = svgInner(string(data))
	}
	simpleCache.Store(name, inner)
	return inner, inner != ""
}

// svgInner pulls the drawable content out of a Simple Icons <svg>…</svg> file — drops the <svg>
// wrapper and the <title> (Simple Icons puts the brand name there).
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
	if t := strings.Index(inner, "<title>"); t >= 0 {
		if e := strings.Index(inner, "</title>"); e > t {
			inner = inner[:t] + inner[e+len("</title>"):]
		}
	}
	return strings.TrimSpace(inner)
}

// Has reports whether the named logo exists (built-in or vendored Simple Icons).
func Has(name string) bool { _, ok := lookup(name); return ok }

// Names returns every available logo name (built-in + vendored), sorted — for a gallery.
func Names() []string {
	seen := make(map[string]bool)
	var out []string
	for n := range set {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if entries, err := simpleFS.ReadDir("simple"); err == nil {
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
// CSS GENERATION (mask technique — same as jit/icons, but FILLED)
// ----------------------------------------------------------------------------

const (
	classPrefix = "logo-"
	brandSuffix = "-brand" // `logo-<slug>-brand` sugar: the shape painted in its own brand colour.
	styleMarker = `data-kitwork-jit="logo"`
)

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)

// uriEncode escapes the characters that matter inside a CSS url("data:image/svg+xml,…").
var uriEncode = strings.NewReplacer("%", "%25", "#", "%23", "<", "%3C", ">", "%3E")

// dataURI wraps a filled <path> in a standalone <svg fill='#000'> and returns a CSS url("data:…").
// A mask reads only alpha (#000 = opaque); the visible colour comes from background:currentColor.
func dataURI(inner string) string {
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="#000">` + inner + `</svg>`
	svg = strings.ReplaceAll(svg, "currentColor", "#000")
	svg = strings.ReplaceAll(svg, `"`, `'`)
	svg = strings.Join(strings.Fields(svg), " ")
	return `url("data:image/svg+xml,` + uriEncode.Replace(svg) + `")`
}

// CSS returns the JIT stylesheet for the given logo class tokens — "" if none resolve. A token is
// either a logo slug (`github` → monochrome, inherits currentColor) or `<slug>-brand` (the `-brand`
// sugar → the same shape painted in its own official brand colour). One shared base rule (in
// :where() so utilities win) lists every used selector; then the mask `--i` is emitted once per
// logo (shared by both variants) and the brand colour only on the `-brand` selector.
func CSS(tokens []string) string {
	type use struct{ mono, brand bool } // which variants of a slug appear
	uses := make(map[string]*use)
	var order []string
	add := func(slug string, brand bool) {
		u := uses[slug]
		if u == nil {
			u = &use{}
			uses[slug] = u
			order = append(order, slug)
		}
		if brand {
			u.brand = true
		} else {
			u.mono = true
		}
	}
	for _, tok := range tokens {
		if Has(tok) { // a real logo (a slug that happens to end in -brand still wins here)
			add(tok, false)
			continue
		}
		if slug := strings.TrimSuffix(tok, brandSuffix); slug != tok && Has(slug) {
			add(slug, true)
		}
	}
	if len(order) == 0 {
		return ""
	}
	sort.Strings(order)

	var b strings.Builder
	// 1) Shared base rule listing every used selector (both variants of each slug that appear).
	b.WriteString(":where(")
	first := true
	sel := func(s string) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(s)
	}
	for _, slug := range order {
		u := uses[slug]
		if u.mono {
			sel("." + classPrefix + slug)
		}
		if u.brand {
			sel("." + classPrefix + slug + brandSuffix)
		}
	}
	b.WriteString(`){display:inline-block;width:1em;height:1em;vertical-align:-.125em;flex:none;` +
		`background:currentColor;-webkit-mask:var(--i) center/contain no-repeat;mask:var(--i) center/contain no-repeat}`)

	// 2) Per logo: the mask data-URI once (shared by both variants), then the official brand colour
	// on the `-brand` sugar only. The default (no suffix) inherits currentColor — monochrome.
	for _, slug := range order {
		u := uses[slug]
		inner, _ := lookup(slug)
		var sels []string
		if u.mono {
			sels = append(sels, "."+classPrefix+slug)
		}
		if u.brand {
			sels = append(sels, "."+classPrefix+slug+brandSuffix)
		}
		b.WriteString(strings.Join(sels, ",") + "{--i:" + dataURI(inner) + "}")
		if u.brand {
			if hex, ok := brandColor[slug]; ok {
				b.WriteString("." + classPrefix + slug + brandSuffix + "{color:" + hex + "}")
			}
		}
	}
	return b.String()
}

// scan collects the distinct logo class tokens used in html — a logo slug (`logo-github`) or the
// `-brand` sugar (`logo-github-brand`). The underlying slug must resolve to a known logo.
func scan(html string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, tok := range strings.Fields(m[1]) {
			if !strings.HasPrefix(tok, classPrefix) {
				continue
			}
			name := tok[len(classPrefix):]
			if name == "" || seen[name] || !nameRe.MatchString(name) {
				continue
			}
			// A logo slug wins outright; otherwise accept `<slug>-brand` if the slug is a logo.
			if Has(name) || (strings.HasSuffix(name, brandSuffix) && Has(strings.TrimSuffix(name, brandSuffix))) {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// SiteCSS is the whole-tenant form — the union of logos used across many templates (for /jitlogo).
func SiteCSS(htmls ...string) string {
	seen := make(map[string]bool)
	var all []string
	for _, h := range htmls {
		for _, n := range scan(h) {
			if !seen[n] {
				seen[n] = true
				all = append(all, n)
			}
		}
	}
	return CSS(all)
}

// Render scans html for used logo-<name> classes and injects ONE <style data-kitwork-jit="logo">
// before </head>. The markup is left untouched. A cheap no-op when no logos are used.
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
