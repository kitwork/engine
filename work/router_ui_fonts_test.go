package work

import (
	"bytes"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	jitcss "github.com/kitwork/engine/jit/css"
	"github.com/kitwork/engine/jit/fonts"
)

// A Kitwork UI page's typefaces: each role is the router.fonts typeface, or the engine's default for
// a role the site leaves out — Google Sans Flex for body, Google Sans Code for code — and every
// typeface the binary carries is resolved by its name (fonts.Find, FaceCSS, PreloadFace): its
// @font-face in fonts.<hash>.css, its woff2 served from /_kitwork/fonts/, its face preloaded.

// siteFonts is the line of the shared site's router.work that declares its typefaces.
const siteFonts = `router.fonts({ body: "Outfit" });`

// withFontsLine is the shared site with its router.fonts line replaced ("" drops it: the site names no
// typeface).
func withFontsLine(t *testing.T, line string, changes map[string]string) map[string]string {
	t.Helper()
	files := withFiles(changes)
	router := files["localhost/router.work"]
	if strings.Count(router, siteFonts) != 1 {
		t.Fatalf("the shared router.work no longer declares %s", siteFonts)
	}
	// the fonts file of its own is what these tests read: router.bundle(false), one file per role (the
	// default writes the site's CSS, fonts among it, in the page: router_ui_bundle_test.go)
	files["localhost/router.work"] = strings.Replace(router, siteFonts, line+"\nrouter.bundle(false);", 1)
	return files
}

// codePage sets text in the code face: a code block.
const codePage = `<content><text heading="1">Cài đặt</text><code>go run .</code></content>`

// fontFace is one @font-face of a fonts file.
type fontFace struct {
	family, style, weight, src string
}

var (
	fontFaceRe = regexp.MustCompile(`@font-face\{font-family:'([^']+)';font-style:([a-z]+);font-weight:([0-9]+(?: [0-9]+)?);font-display:swap;src:url\(([^)]+)\) format\('woff2'\)`)
	preloadRe  = regexp.MustCompile(`<link rel="preload" as="font" type="font/woff2" href="([^"]+)" crossorigin>`)
)

// facesOf is every @font-face of a fonts file by family, in order.
func facesOf(css string) map[string][]fontFace {
	out := map[string][]fontFace{}
	for _, m := range fontFaceRe.FindAllStringSubmatch(css, -1) {
		out[m[1]] = append(out[m[1]], fontFace{family: m[1], style: m[2], weight: m[3], src: m[4]})
	}
	return out
}

// preloadsOf is every font a page preloads.
func preloadsOf(page string) []string {
	var out []string
	for _, m := range preloadRe.FindAllStringSubmatch(page, -1) {
		out = append(out, m[1])
	}
	return out
}

// fontsSheet is the page at target and its fonts.<hash>.css (href and CSS).
func fontsSheet(t *testing.T, tenant *Tenant, target string) (page string, sheet linkedStylesheet) {
	t.Helper()
	page = serveUI(t, tenant, target).Body.String()
	sheet, ok := sheetsByRole(stylesheetsOf(t, tenant, page))["fonts"]
	if !ok {
		t.Fatalf("%s links no fonts.<hash>.css:\n%.1500s", target, page)
	}
	return page, sheet
}

// checkServedFaces says every face of a family is one subset's embedded woff2, served by the engine
// as it is embedded: /_kitwork/fonts/<slug>-<weight range>-<subset>.<hash>.woff2, font/woff2.
func checkServedFaces(t *testing.T, tenant *Tenant, faces []fontFace, slug, weight string) {
	t.Helper()
	name := regexp.MustCompile(`^/_kitwork/fonts/` + regexp.QuoteMeta(slug) + `-` + strings.ReplaceAll(weight, " ", "-") + `-(vietnamese|latin-ext|latin)\.[0-9a-f]{16}\.woff2$`)
	subsets := map[string]bool{}
	for _, face := range faces {
		m := name.FindStringSubmatch(face.src)
		if face.style != "normal" || face.weight != weight || m == nil {
			t.Errorf("%s: want one upright face per subset, weight %s, from /_kitwork/fonts/%s-…; got %+v", slug, weight, slug, face)
			continue
		}
		subsets[m[1]] = true
		file, exact := fonts.FileAt(face.src)
		response := serveEngine(tenant, http.MethodGet, face.src)
		if file == nil || !exact || response.Code != http.StatusOK || response.Header().Get("Content-Type") != "font/woff2" ||
			!bytes.HasPrefix(response.Body.Bytes(), []byte("wOF2")) || !bytes.Equal(response.Body.Bytes(), file.Body) {
			t.Errorf("%s: %d %q — the engine serves the embedded woff2 under its own name", face.src, response.Code, response.Header().Get("Content-Type"))
		}
	}
	if len(faces) != 3 || len(subsets) != 3 {
		t.Errorf("%s: one face for each of latin, latin-ext and vietnamese, got %d faces: %+v", slug, len(faces), faces)
	}
}

// hasPreload reports whether a page preloads a font whose URL starts with prefix.
func hasPreload(page, prefix string) bool {
	for _, href := range preloadsOf(page) {
		if strings.HasPrefix(href, prefix) {
			return true
		}
	}
	return false
}

// A site that names no typeface sets its pages in the engine's defaults: running text in Google Sans
// Flex, code in Google Sans Code, both embedded. A page with code gets both @font-face — one face per
// subset over each family's weight axis, served from /_kitwork/fonts/ — and preloads both latin faces;
// a page with no code preloads only the body's, and shares the site's one fonts file.
func TestUIFontsDefaultToGoogleSansFlexAndGoogleSansCode(t *testing.T) {
	tenant := buildUISite(t, withFontsLine(t, "", map[string]string{"localhost/content.ui": codePage}))
	home, sheet := fontsSheet(t, tenant, "http://localhost/")
	roles := ":root{--font-body:'Google Sans Flex', " + jitcss.DefaultConfig.FontFamily["sans"] +
		";--font-code:'Google Sans Code', " + jitcss.DefaultConfig.FontFamily["mono"] + "}"
	if !strings.HasPrefix(sheet.css, roles) {
		t.Errorf("the roles are the defaults, each before its system stack: want %q, got:\n%.300s", roles, sheet.css)
	}
	faces := facesOf(sheet.css)
	checkServedFaces(t, tenant, faces["Google Sans Code"], "google-sans-code", "300 800")
	checkServedFaces(t, tenant, faces["Google Sans Flex"], "google-sans-flex", "1 1000")
	if len(faces) != 2 {
		t.Errorf("the fonts file ships the two defaults and nothing else: %v", faces)
	}
	if !hasPreload(home, "/_kitwork/fonts/google-sans-flex-1-1000-latin.") || !hasPreload(home, "/_kitwork/fonts/google-sans-code-300-800-latin.") || len(preloadsOf(home)) != 2 {
		t.Errorf("a page with code preloads the latin face of each default: %v", preloadsOf(home))
	}
	if !strings.Contains(home, "<pre><code>go run .</code></pre>") {
		t.Errorf("the code block is on the page:\n%.1500s", home)
	}

	plain, plainSheet := fontsSheet(t, tenant, "http://localhost/tinh")
	if plainSheet.href != sheet.href {
		t.Errorf("every page of the site shares one fonts file: %s and %s", sheet.href, plainSheet.href)
	}
	if got := preloadsOf(plain); len(got) != 1 || !hasPreload(plain, "/_kitwork/fonts/google-sans-flex-1-1000-latin.") {
		t.Errorf("a page with no code preloads the body face only: %v", got)
	}
}

// A site none of whose pages sets code ships no code face: --font-code still names Google Sans Code,
// but no @font-face of it is written and no page preloads it. CONTROL: one page with code ships it.
func TestUIFontsShipTheCodeFaceOnlyForASiteWithCode(t *testing.T) {
	tenant := buildUISite(t, withFontsLine(t, "", nil))
	page, sheet := fontsSheet(t, tenant, "http://localhost/")
	if !strings.Contains(sheet.css, "--font-code:'Google Sans Code', ") {
		t.Errorf("the code role is set whatever the pages: %.300s", sheet.css)
	}
	faces := facesOf(sheet.css)
	if len(faces["Google Sans Code"]) != 0 || strings.Contains(page, "google-sans-code") {
		t.Errorf("no page sets code: no code face ships, none is preloaded:\n%s\n%v", sheet.css, preloadsOf(page))
	}
	if len(faces["Google Sans Flex"]) != 3 {
		t.Errorf("the body face ships: %v", faces)
	}

	control := buildUISite(t, withFontsLine(t, "", map[string]string{"localhost/tinh/content.ui": codePage}))
	_, controlSheet := fontsSheet(t, control, "http://localhost/")
	if len(facesOf(controlSheet.css)["Google Sans Code"]) != 3 {
		t.Errorf("CONTROL: a site with one page of code ships the code face in its one fonts file:\n%s", controlSheet.css)
	}
}

// router.fonts({ code: "JetBrains Mono" }) sets code in JetBrains Mono, embedded too: its faces and its
// preload take the place of Google Sans Code's, and the stack names it as Google spells it however the
// site wrote it. The body keeps the default.
func TestUIFontsCodeSwitchesToJetBrainsMono(t *testing.T) {
	for _, written := range []string{"JetBrains Mono", "jetbrains mono"} {
		tenant := buildUISite(t, withFontsLine(t, `router.fonts({ code: "`+written+`" });`, map[string]string{"localhost/content.ui": codePage}))
		page, sheet := fontsSheet(t, tenant, "http://localhost/")
		if !strings.Contains(sheet.css, ";--font-code:'JetBrains Mono', "+jitcss.DefaultConfig.FontFamily["mono"]+"}") ||
			!strings.Contains(sheet.css, "--font-body:'Google Sans Flex', ") {
			t.Errorf("%q: the code role is JetBrains Mono, the body the default:\n%.300s", written, sheet.css)
		}
		faces := facesOf(sheet.css)
		checkServedFaces(t, tenant, faces["JetBrains Mono"], "jetbrains-mono", "100 800")
		if len(faces["Google Sans Code"]) != 0 || strings.Contains(sheet.css+page, "google-sans-code") {
			t.Errorf("%q: Google Sans Code is not shipped once code is JetBrains Mono", written)
		}
		if !hasPreload(page, "/_kitwork/fonts/jetbrains-mono-100-800-latin.") {
			t.Errorf("%q: the code face preloaded is JetBrains Mono's: %v", written, preloadsOf(page))
		}
	}
}

// The defaults are a Kitwork UI page's: a template page keeps what it had — Outfit and Fira Code by
// name, from the template path: the woff2 its @font-face and preloads name are exactly fonts.Render's
// for the same markup (the production minifier rewrites the rest of the CSS), and no Google Sans face.
// CONTROL: a .ui site that names no body face gets Google Sans Flex.
func TestTemplatePageFontsAreUnchanged(t *testing.T) {
	const markup = `<p class="font-outfit">x</p><pre><code style="font-family: 'Fira Code'">y</code></pre>`
	tenant := componentFixture(t, componentPage(markup))
	page := serveEngine(tenant, http.MethodGet, "/").Body.String()
	urls := func(html string) []string {
		var out []string
		for _, m := range engineFontRe.FindAllStringSubmatch(html, -1) {
			out = append(out, m[1])
		}
		sort.Strings(out)
		return out
	}
	got, want := urls(page), urls(fonts.Render("<head></head><body>"+markup+"</body>"))
	if strings.Join(got, " ") != strings.Join(want, " ") || !strings.Contains(strings.Join(got, " "), "/_kitwork/fonts/outfit-400-latin.") ||
		!strings.Contains(strings.Join(got, " "), "/_kitwork/fonts/fira-code-400-latin.") {
		t.Errorf("the template page names the fonts the template path names, Outfit and Fira Code:\ngot  %v\nwant %v", got, want)
	}
	if lower := strings.ToLower(page); strings.Contains(lower, "google sans") || strings.Contains(lower, "google-sans") {
		t.Errorf("a template page gets no default face:\n%.2000s", page)
	}

	control := buildUISite(t, withFontsLine(t, "", nil))
	if _, sheet := fontsSheet(t, control, "http://localhost/"); len(facesOf(sheet.css)["Google Sans Flex"]) != 3 {
		t.Errorf("CONTROL: a .ui site that names no body face ships Google Sans Flex:\n%s", sheet.css)
	}
}
