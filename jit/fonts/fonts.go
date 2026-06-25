// Package fonts is Kitwork's JIT web-font engine — "jitfonts", a sibling of jit/css|icons|logo.
// Self-hosted Google Fonts: the woff2 (already subset by Google, e.g. latin / latin-ext) are
// vendored + embedded; Render scans a page for the font FAMILIES it actually uses — a
// `font-family: <Name>` declaration OR a `font-<slug>` class — and injects ONE
// <style data-kitwork-jit="fonts"> with @font-face for ONLY those families (plus a `.font-<slug>`
// utility when the class form is used) and a <link rel="preload"> for each family's primary face.
// The woff2 are served from RoutePrefix straight off the embed, hard-cached. No Google at runtime,
// no third-party CDN — sovereign typography that "just works" with zero per-tenant config.
//
// Fonts are OFL/Apache (see FONTS_LICENSE). Add a family / refresh the files with vendor.py, which
// regenerates catalog_gen.go (the `catalog` map) from Google's CSS.
package fonts

import (
	"embed"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed families
var FS embed.FS

// RoutePrefix is where the vendored woff2 are served from (the engine maps it onto FS). Stable so
// the URLs stay cacheable; the @font-face `src` and the preload links both point here.
const RoutePrefix = "/jitfonts/"

const styleMarker = `data-kitwork-jit="fonts"`

// face is one weight×style×subset of a family — a single vendored woff2. weight is the CSS numeric
// weight; file is the path inside FS; unicode is the subset's unicode-range (may be "").
type face struct {
	weight  int
	style   string
	subset  string
	file    string
	unicode string
}

// family is a vendored font: its CSS family name, a fallback stack, and the faces we ship.
type family struct {
	name     string
	fallback string
	faces    []face
}

// catalog (slug → family) is generated in catalog_gen.go by vendor.py.

var classAttrRe = regexp.MustCompile(`class="([^"]*)"`)

// familyNameRe[slug] detects a family by name inside a `font-family:` value (so the existing
// `font-family: Outfit, …` just works without any class). Precompiled once from the catalog.
var familyNameRe = map[string]*regexp.Regexp{}

func init() {
	for slug, fam := range catalog {
		familyNameRe[slug] = regexp.MustCompile(`font-family\s*:\s*['"]?` + regexp.QuoteMeta(fam.name) + `\b`)
	}
}

// Has reports whether slug is a vendored family.
func Has(slug string) bool { _, ok := catalog[slug]; return ok }

// Names lists every vendored family slug, sorted (for a gallery / docs).
func Names() []string {
	out := make([]string, 0, len(catalog))
	for s := range catalog {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// routePath turns an FS path ("families/outfit/400-latin.woff2") into its served URL
// ("/jitfonts/outfit/400-latin.woff2").
func routePath(fsPath string) string {
	return RoutePrefix + strings.TrimPrefix(fsPath, "families/")
}

// scan finds the families a page uses. A slug is "via class" when `font-<slug>` appears (→ also
// emit the `.font-<slug>` utility); a slug found only through a `font-family: <Name>` value still
// gets its @font-face (so plain CSS that names the font works untouched).
func scan(html string) (slugs []string, viaClass map[string]bool) {
	viaClass = map[string]bool{}
	seen := map[string]bool{}
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, tok := range strings.Fields(m[1]) {
			if !strings.HasPrefix(tok, "font-") {
				continue
			}
			slug := tok[len("font-"):]
			if Has(slug) {
				if !seen[slug] {
					seen[slug] = true
					slugs = append(slugs, slug)
				}
				viaClass[slug] = true
			}
		}
	}
	for slug, re := range familyNameRe {
		if seen[slug] {
			continue
		}
		if re.MatchString(html) {
			seen[slug] = true
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	return slugs, viaClass
}

// primaryFace picks the face to preload: weight 400 / normal / latin if present, else the first.
func primaryFace(fam family) *face {
	var first *face
	for i := range fam.faces {
		f := &fam.faces[i]
		if first == nil {
			first = f
		}
		if f.weight == 400 && f.style == "normal" && f.subset == "latin" {
			return f
		}
	}
	return first
}

// CSS builds the @font-face block(s) for the used families (+ the `.font-<slug>` utility for the
// ones referenced by class). "" if none.
func CSS(slugs []string, viaClass map[string]bool) string {
	var b strings.Builder
	for _, slug := range slugs {
		fam, ok := catalog[slug]
		if !ok {
			continue
		}
		for _, f := range fam.faces {
			b.WriteString("@font-face{font-family:'" + fam.name + "';font-style:" + f.style +
				";font-weight:" + strconv.Itoa(f.weight) + ";font-display:swap;src:url(" +
				routePath(f.file) + ") format('woff2')")
			if f.unicode != "" {
				b.WriteString(";unicode-range:" + f.unicode)
			}
			b.WriteString("}")
		}
		if viaClass[slug] {
			b.WriteString(".font-" + slug + "{font-family:'" + fam.name + "'," + fam.fallback + "}")
		}
	}
	return b.String()
}

// preloadLinks emits a <link rel="preload"> for each used family's primary face — fonts need
// crossorigin even same-origin.
func preloadLinks(slugs []string) string {
	var b strings.Builder
	for _, slug := range slugs {
		fam, ok := catalog[slug]
		if !ok {
			continue
		}
		if f := primaryFace(fam); f != nil {
			b.WriteString(`<link rel="preload" as="font" type="font/woff2" href="` +
				routePath(f.file) + `" crossorigin>`)
		}
	}
	return b.String()
}

// Render scans html for the fonts it uses and injects the preload links + ONE
// <style data-kitwork-jit="fonts"> with their @font-face (and `.font-<slug>` utilities) before
// </head>. A cheap no-op when the page references no vendored font.
func Render(html string) string {
	if !strings.Contains(html, "font") {
		return html
	}
	slugs, viaClass := scan(html)
	if len(slugs) == 0 {
		return html
	}
	css := CSS(slugs, viaClass)
	if css == "" {
		return html
	}
	inject := preloadLinks(slugs) + "<style " + styleMarker + ">" + css + "</style>"
	if i := strings.LastIndex(html, "</head>"); i >= 0 {
		return html[:i] + inject + html[i:]
	}
	return inject + html
}
