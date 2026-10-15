// Package fonts is Kitwork's JIT web-font engine — "jitfonts", a sibling of jit/css|icons|logo.
// Self-hosted Google Fonts: the woff2 (already subset by Google, e.g. latin / latin-ext) are
// vendored + embedded; Render scans a page for the font FAMILIES it actually uses — a
// `font-family: <Name>` declaration OR a `font-<slug>` class — and injects ONE
// <style data-kitwork-generated="fonts"> with @font-face for ONLY those families (plus a `.font-<slug>`
// utility when the class form is used) and a <link rel="preload"> for each family's primary face.
// The woff2 are served from RoutePrefix straight off the embed, hard-cached. No Google at runtime,
// no third-party CDN — sovereign typography that "just works" with zero per-tenant config.
//
// Any Google font by its name (google.go, url.go, download.go, faces.go): the package carries Google's
// family list (google_catalog.json — Find, Suggest), reads a pasted Google Fonts address (ParseURL),
// downloads a family ONCE from Google into a server-side cache and serves it from RoutePrefix like an
// embedded one (Ensure, FileAt), and writes its @font-face for the weights a page uses (FaceCSS).
// Visitors never contact Google. The default typefaces (DefaultBody, DefaultCode) are embedded, so
// the default works offline.
//
// Fonts are OFL/Apache (see FONTS_LICENSE). Add a family / refresh the files with vendor.py, which
// regenerates catalog_gen.go (the `catalog` map) from Google's CSS; refresh Google's family list with
// google_catalog.py.
package fonts

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/kitwork/engine/jit/hydrate"
)

//go:embed families
var FS embed.FS

// RoutePrefix is where the vendored woff2 are served from (the engine maps it onto FS). It sits
// under /_kitwork/, the one prefix everything the engine serves lives under; the @font-face `src`
// and the preload links both point here.
//
// Each file is named by its face and its bytes, flat in the folder, like every file the engine
// serves (<role>.<hash>.<ext>):
//
//	/_kitwork/fonts/outfit-400-latin.9c1e0b7a44d2f816.woff2
//	/_kitwork/fonts/<family>-<weight>-<subset>[-<style>].<hash>.woff2
//
// The face part is the vendored file's own name (families/<family>/<weight>-<subset>[-<style>].woff2,
// vendor.py), so an italic — or any axis vendor.py ever writes into a file name — gets its word in
// the served name the same way. The hash is what makes keeping the file forever safe: re-vendor a
// face and its name changes.
const RoutePrefix = "/_kitwork/fonts/"

// LegacyRoutePrefix is where they were served until 11/10/2026, as /jitfonts/<family>/<weight>-
// <subset>.woff2. The engine still answers it — and the same unhashed form under RoutePrefix,
// which a stopped run wrote for a while — with today's file, never as its exact name (FileAt).
const LegacyRoutePrefix = "/jitfonts/"

// hashLength is how much of the SHA-256 a served name carries, as for the engine's JavaScript: 64
// bits tell two versions of one face apart.
const hashLength = 16

// File is one served woff2.
type File struct {
	Path   string // the URL: /_kitwork/fonts/<face>.<hash>.woff2
	Body   []byte
	Digest string // the whole SHA-256, hex — the ETag
}

var (
	filesOnce  sync.Once
	fileByFS   map[string]*File // "families/outfit/400-latin.woff2" → its file
	fileByFace map[string]*File // "outfit-400-latin" → its file
)

// files names every face of the catalog by its bytes, once.
func files() {
	filesOnce.Do(func() {
		fileByFS = map[string]*File{}
		fileByFace = map[string]*File{}
		for slug, fam := range catalog {
			for _, f := range fam.faces {
				body, err := FS.ReadFile(f.file)
				if err != nil {
					continue
				}
				sum := sha256.Sum256(body)
				digest := hex.EncodeToString(sum[:])
				face := slug + "-" + strings.TrimSuffix(f.file[strings.LastIndexByte(f.file, '/')+1:], ".woff2")
				file := &File{Path: RoutePrefix + face + "." + digest[:hashLength] + ".woff2", Body: body, Digest: digest}
				fileByFS[f.file] = file
				fileByFace[face] = file
			}
		}
	})
}

// FileAt is the woff2 a path names. exact says whether the path is that file's current name; when
// it is not — an old hash, /jitfonts/<family>/<weight>-<subset>.woff2, or that unhashed form under
// RoutePrefix — the file is still today's, and the caller must not let it be cached under the old
// name. nil when the path names no face: only a catalog face can ever be answered, so a path cannot
// reach anything else in FS.
//
// An embedded face is found first; then a face of a family downloaded into the cache (Ensure), under
// the same name form — /_kitwork/fonts/itim-400-latin.<hash>.woff2 — and only by its hashed name.
// The path is only ever a key: the file read is the one the family's manifest names (cachedFileAt).
func FileAt(path string) (file *File, exact bool) {
	files()
	rest, current := strings.CutPrefix(path, RoutePrefix)
	if !current {
		var legacy bool
		if rest, legacy = strings.CutPrefix(path, LegacyRoutePrefix); !legacy {
			return nil, false
		}
	}
	if len(rest) > 160 || !strings.HasSuffix(rest, ".woff2") {
		return nil, false
	}
	if strings.Contains(rest, "/") {
		// <family>/<weight>-<subset>.woff2: the vendored file's own path, as the names before were.
		if file = fileByFS["families/"+rest]; file == nil {
			return nil, false
		}
		return file, false
	}
	if !current {
		return nil, false
	}
	name := strings.TrimSuffix(rest, ".woff2")
	dot := strings.LastIndexByte(name, '.')
	if dot < 0 || len(name)-dot-1 != hashLength {
		return nil, false
	}
	if file = fileByFace[name[:dot]]; file == nil {
		return cachedFileAt(name[:dot], path)
	}
	return file, file.Path == path
}

const styleMarker = `data-kitwork-generated="fonts"`

// face is one weight×style×subset of a family — a single vendored woff2. weight is the CSS numeric
// weight; file is the path inside FS; unicode is the subset's unicode-range (may be ""). through is
// the heavy end of a face that is a whole weight range — Google's answer to a range request
// (wght@1..1000) is one file per subset, "font-weight: 1 1000", vendored as weight 1, through 1000
// and the file <family>/1-1000-<subset>.woff2; 0 for a face of one weight.
type face struct {
	weight  int
	through int
	style   string
	subset  string
	file    string
	unicode string
}

// cssWeight is the face's font-weight as CSS writes it: "400", or "1 1000" for a range.
func (f face) cssWeight() string {
	if f.through > f.weight {
		return strconv.Itoa(f.weight) + " " + strconv.Itoa(f.through)
	}
	return strconv.Itoa(f.weight)
}

// draws reports whether the face draws a weight: its own, or one inside its range.
func (f face) draws(weight int) bool {
	return f.weight == weight || (f.through > f.weight && weight >= f.weight && weight <= f.through)
}

// family is a vendored font: its CSS family name, a fallback stack, and the faces we ship. weights
// is the weight axis of a variable font, low and high: Google serves one file per subset for every
// weight of such a family, so any weight in the range is that file (vendor.py writes the range).
// Zero for a family with one file per weight.
//
// template marks the families the template path finds by name — Render's scan, and Lookup: Outfit
// and Fira Code, vendored before any Google font could be named. The families embedded since (Google
// Sans Flex and Google Sans Code, the engine's defaults, and JetBrains Mono) are reached by name
// through Find, FaceCSS, PreloadFace and FileAt only — what a Kitwork UI page reads for every embedded
// family — so embedding them changed no template page.
type family struct {
	name     string
	fallback string
	weights  [2]int
	template bool
	faces    []face
}

// catalog (slug → family) is generated in catalog_gen.go by vendor.py.

// classAttrRe matches the static class attribute only. The leading white space keeps it off every
// attribute that ENDS in class — data-kitwork-class="font-outfit: wide" is a list of pairs whose keys
// hydrate.ToggledClasses reads and whose values are conditions, not classes (as jit/css's classAttrRe).
var classAttrRe = regexp.MustCompile(`(?:^|\s)class="([^"]*)"`)

// familyNameRe[slug] detects a family by name inside a `font-family:` value (so the existing
// `font-family: Outfit, …` just works without any class). Precompiled once from the catalog.
var familyNameRe = map[string]*regexp.Regexp{}

func init() {
	for slug, fam := range catalog {
		if fam.template {
			familyNameRe[slug] = regexp.MustCompile(`font-family\s*:\s*['"]?` + regexp.QuoteMeta(fam.name) + `\b`)
		}
	}
}

// Has reports whether slug is a vendored family.
func Has(slug string) bool { _, ok := catalog[slug]; return ok }

// scanned reports whether the template path finds slug on a page (family.template).
func scanned(slug string) bool { return catalog[slug].template }

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
// ("/_kitwork/fonts/outfit-400-latin.<hash>.woff2"), or "" for a face whose file is not embedded.
func routePath(fsPath string) string {
	files()
	if file := fileByFS[fsPath]; file != nil {
		return file.Path
	}
	return ""
}

// scan finds the families a page uses. A slug is "via class" when `font-<slug>` appears (→ also
// emit the `.font-<slug>` utility); a slug found only through a `font-family: <Name>` value still
// gets its @font-face (so plain CSS that names the font works untouched).
func scan(html string) (slugs []string, viaClass map[string]bool) {
	viaClass = map[string]bool{}
	seen := map[string]bool{}
	class := func(tok string) {
		if !strings.HasPrefix(tok, "font-") {
			return
		}
		slug := tok[len("font-"):]
		if scanned(slug) {
			if !seen[slug] {
				seen[slug] = true
				slugs = append(slugs, slug)
			}
			viaClass[slug] = true
		}
	}
	for _, m := range classAttrRe.FindAllStringSubmatch(html, -1) {
		for _, tok := range strings.Fields(m[1]) {
			class(tok)
		}
	}
	// A family the kernel toggles in — data-kitwork-class="font-outfit: wide" — needs its @font-face
	// and its .font-<slug> on the page before the class arrives, or the switch falls back to the
	// system font.
	for _, tok := range hydrate.ToggledClasses(html) {
		class(tok)
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

// primaryFace picks the face to preload: weight 400 (its own, or inside its range) / normal / latin
// if present, else the first.
func primaryFace(fam family) *face {
	var first *face
	for i := range fam.faces {
		f := &fam.faces[i]
		if first == nil {
			first = f
		}
		if f.draws(400) && f.style == "normal" && f.subset == "latin" {
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
			src := routePath(f.file)
			if src == "" {
				continue
			}
			b.WriteString("@font-face{font-family:'" + fam.name + "';font-style:" + f.style +
				";font-weight:" + f.cssWeight() + ";font-display:swap;src:url(" +
				src + ") format('woff2')")
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

// Lookup is the slug of a vendored family by the name a site writes in router.fonts ("Outfit",
// "Fira Code"), quotes and case ignored — one the template path knows (family.template). Any Google
// family by its name is Find.
func Lookup(name string) (string, bool) {
	name = strings.Trim(strings.TrimSpace(name), `"'`)
	for slug, fam := range catalog {
		if fam.template && strings.EqualFold(fam.name, name) {
			return slug, true
		}
	}
	return "", false
}

// Covers reports whether a vendored family has a face at a weight: a weight it lists, or any
// weight on the axis of its variable font.
func Covers(slug string, weight int) bool {
	fam, ok := catalog[slug]
	if !ok {
		return false
	}
	if fam.weights[0] > 0 && weight >= fam.weights[0] && weight <= fam.weights[1] {
		return true
	}
	for _, f := range fam.faces {
		if f.weight == weight {
			return true
		}
	}
	return false
}

// Faces is the @font-face block of a vendored family at exactly the weights given, ascending: a
// Kitwork UI page ships the weights its tree uses, not every face vendored. On a variable font every
// weight of one subset points at the same file (the primary face's), so the browser fetches each
// subset once whatever the weights. A weight the family has no face for is left out: the browser
// draws it from the nearest face, as it would without the rule. "" when nothing is shipped.
func Faces(slug string, weights []int) string {
	fam, ok := catalog[slug]
	if !ok {
		return ""
	}
	sorted := append([]int(nil), weights...)
	sort.Ints(sorted)
	type subset struct{ style, name string }
	var order []subset
	file := map[subset]*face{}
	for i := range fam.faces {
		f := &fam.faces[i]
		key := subset{f.style, f.subset}
		current, seen := file[key]
		if !seen {
			order = append(order, key)
		}
		// the primary face of the subset: weight 400 if there is one, else the first
		if !seen || (fam.weights[0] > 0 && f.draws(400) && !current.draws(400)) {
			file[key] = f
		}
	}
	var b strings.Builder
	last := -1
	for _, weight := range sorted {
		if weight == last || !Covers(slug, weight) {
			continue
		}
		last = weight
		for _, key := range order {
			f := file[key]
			if fam.weights[0] == 0 {
				f = nil // one file per weight: the face of exactly this weight, if any
				for i := range fam.faces {
					if g := &fam.faces[i]; g.weight == weight && g.style == key.style && g.subset == key.name {
						f = g
					}
				}
				if f == nil {
					continue
				}
			}
			src := routePath(f.file)
			if src == "" {
				continue
			}
			b.WriteString("@font-face{font-family:'" + fam.name + "';font-style:" + f.style +
				";font-weight:" + strconv.Itoa(weight) + ";font-display:swap;src:url(" + src + ") format('woff2')")
			if f.unicode != "" {
				b.WriteString(";unicode-range:" + f.unicode)
			}
			b.WriteString("}")
		}
	}
	return b.String()
}

// Preload is the <link rel="preload"> of a vendored family's primary face (what a page fetches
// first), "" when the family is not vendored.
func Preload(slug string) string {
	if _, ok := catalog[slug]; !ok {
		return ""
	}
	return preloadLinks([]string{slug})
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
		if f := primaryFace(fam); f != nil && routePath(f.file) != "" {
			b.WriteString(`<link rel="preload" as="font" type="font/woff2" href="` +
				routePath(f.file) + `" crossorigin>`)
		}
	}
	return b.String()
}

// Render scans html for the fonts it uses and injects the preload links + ONE
// <style data-kitwork-generated="fonts"> with their @font-face (and `.font-<slug>` utilities) before
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
