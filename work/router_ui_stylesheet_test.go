package work

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/kitwork/engine/ui"
	"github.com/kitwork/engine/ui/stylesheet"
)

// A Kitwork UI page's CSS is in files of its own (KITWORK_UI §18), one per role, each named by its
// role and its bytes and linked from the page in the cascade's order: /_kitwork/reboot.<hash>.css (the
// ground of every page, the same bytes for every site), colors.<hash>.css (router.colors of the
// route's chain), fonts.<hash>.css (@font-face and the font roles), icons.<hash>.css (the icons it
// shows, layered), logo.<hash>.css, page.<hash>.css (the rule of every class the page can carry). The
// head is built once, when the page compiles; a request runs none of the passes and writes no CSS.

// These tests read that delivery: router.bundle(false), one file per role (buildFilesSite). The
// default, router.bundle("auto"), writes the page's own CSS and, when the page fits the first window,
// the site's in the page instead (router_ui_bundle_test.go).

// buildFilesSite is buildUISite with router.bundle(false) at the site root: every role a file.
func buildFilesSite(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	files = copyFiles(files)
	router, ok := files["localhost/router.work"]
	if !ok {
		t.Fatal("the site has no root router.work to write router.bundle(false) in")
	}
	files["localhost/router.work"] = router + "\nrouter.bundle(false);\n"
	return buildUISite(t, files)
}

// copyFiles is a copy of a site's files, so writing in it leaves the caller's map as it was.
func copyFiles(files map[string]string) map[string]string {
	out := make(map[string]string, len(files))
	for name, source := range files {
		out[name] = source
	}
	return out
}

// stylesheetFiles is the shared site with a richer page: a dark pair, both typefaces, an icon, a badge,
// a card. /giau and /cung-lop write the same classes in other words; /khac writes one class more.
// /du-lieu shows a badge whose colour comes from its data.
func stylesheetFiles(t *testing.T) map[string]string {
	t.Helper()
	const rich = `<content gap="4" padding="6" background="canvas">
  <text heading="2" color="brand">%s</text>
  <icon value="coffee" alt="" color="brand"></icon>
  <text font="code">29.000đ</text>
  <badge color="warning"><text>Đang pha</text></badge>
  <card gap="3" padding="5"><text size="small" color="ink-30">%s</text></card>
</content>`
	words := func(a, b string) string { return strings.Replace(strings.Replace(rich, "%s", a, 1), "%s", b, 1) }
	return withRootRouter(t,
		`router.colors({ brand: "#c8742c", canvas: { light: "#f6f3ee", dark: "#141110" }, warning: "#c98a1e" });
router.fonts({ code: "Fira Code" });`,
		map[string]string{
			"localhost/giau/content.ui":     words("Giàu", "một"),
			"localhost/cung-lop/content.ui": words("Cùng lớp", "hai"),
			"localhost/khac/content.ui":     strings.Replace(words("Khác", "ba"), `<card gap="3"`, `<card gap="3" border="line"`, 1),
			"localhost/du-lieu/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const tone = "danger";
});`,
			"localhost/du-lieu/content.ui": `<content><badge color="{{ tone }}"><text>Hết món</text></badge></content>`,
		})
}

// generatedStyleRe finds every <style> a page carries.
var generatedStyleRe = regexp.MustCompile(`(?s)<style[^>]*>(.*?)</style>`)

// sheetsByRole is a page's linked stylesheets by role.
func sheetsByRole(sheets []linkedStylesheet) map[string]linkedStylesheet {
	byRole := map[string]linkedStylesheet{}
	for _, sheet := range sheets {
		byRole[sheet.role] = sheet
	}
	return byRole
}

// A .ui page links its five roles in the cascade's order — reboot, colors, fonts, icons, page —
// together, after the pre-paint and before the scripts, and carries no <style>. Each file holds its
// role's CSS and nothing of another's: reboot is the engine's own bytes, the colours are tokens only,
// the faces and roles are the fonts file's, the icon masks are layered, and the page's file holds the
// rules of its classes and no token. A colour from data needs no CSS of its own: every token the
// palette declares has its rule in the page's file already.
func TestUIPageLinksTheFiveRolesInOrder(t *testing.T) {
	tenant := buildFilesSite(t, stylesheetFiles(t))
	page := serveUI(t, tenant, "http://localhost/giau").Body.String()
	sheets := stylesheetsOf(t, tenant, page)
	var roles, links []string
	for _, sheet := range sheets {
		roles = append(roles, sheet.role+"="+sheet.generated)
		links = append(links, `<link rel="stylesheet" href="`+sheet.href+`" data-kitwork-generated="`+sheet.generated+`">`)
	}
	if got := strings.Join(roles, " "); got != "reboot=reboot colors=colors fonts=fonts icons=icons page=css" {
		t.Fatalf("the page links reboot, colors, fonts, icons and its own stylesheet, in that order, got %q:\n%.1500s", got, page)
	}
	link := strings.Join(links, "")
	if strings.Count(page, link) != 1 {
		t.Fatalf("the five links stand together, once:\n%.1500s", page)
	}
	if styles := generatedStyleRe.FindAllString(page, -1); len(styles) != 0 {
		t.Errorf("a .ui page carries no <style>, found %d:\n%.300s", len(styles), styles[0])
	}
	prepaint := canonicalPrepaint(t)
	if at, before, after := strings.Index(page, link), strings.Index(page, prepaint), strings.Index(page, `<script data-kitwork-generated="runtime"`); before < 0 || after < 0 || !(before < at && at < after) {
		t.Errorf("the pre-paint stays inline before the stylesheets, the scripts after them (pre-paint %d, links %d, kernel %d)", before, at, after)
	}
	byRole := sheetsByRole(sheets)
	if byRole["reboot"].css != string(stylesheet.Reboot().CSS) {
		t.Errorf("the reboot file is the engine's reboot.css, as it is")
	}
	for role, want := range map[string][]string{
		"colors": {":root{color-scheme:light;", "--brand:#c8742c;", "--canvas:#f6f3ee;", ":root[data-theme=dark]{color-scheme:dark;", "--canvas:#141110;"},
		"fonts":  {"--font-code:'Fira Code'", "@font-face{font-family:'Fira Code'"},
		"icons":  {stylesheet.LayerStatement + "\n@layer elements{\n", ".icon-coffee"},
		// the badge's text is an engine colour, a custom property of the page with its value in each mode
		// (13/10/2026), unlayered before the page's layer: warning+60 #8a5b00 in light, the smallest step
		// from 15 that reads on warning-85 here, and warning itself #daa965 in dark (one step for both
		// modes made it #ffffff there)
		"page": {stylesheet.LayerStatement + "\n:root{--kitwork-badge-warning:#8a5b00}\n@media screen{:root[data-theme=dark]{--kitwork-badge-warning:#daa965}}\n@layer page{\n",
			".color-brand{color:var(--brand)}", ".color-ink-30{color:color-mix(in oklab,var(--ink) 70%,var(--canvas))}",
			".color-warning:where(.badge){--kitwork-tint:color-mix(in oklab,var(--warning) 15%,var(--canvas));color:var(--kitwork-badge-warning)}"},
	} {
		for _, rule := range want {
			if !strings.Contains(byRole[role].css, rule) {
				t.Errorf("the %s stylesheet misses %q:\n%.600s", role, rule, byRole[role].css)
			}
		}
	}
	for role, unwanted := range map[string][]string{
		"colors": {".icon-coffee", "@font-face", ".color-brand", "@layer"},
		"fonts":  {"--brand", ".icon-coffee", ".color-brand"},
		"icons":  {"--brand:", ".color-brand", "@font-face"},
		"page":   {"--brand:", ".icon-coffee", "@font-face", "box-sizing"},
	} {
		for _, rule := range unwanted {
			if strings.Contains(byRole[role].css, rule) {
				t.Errorf("the %s stylesheet holds another role's %q", role, rule)
			}
		}
	}

	data := serveUI(t, tenant, "http://localhost/du-lieu").Body.String()
	_, dataCSS := stylesheetOf(t, tenant, data)
	if styles := generatedStyleRe.FindAllString(data, -1); len(styles) != 0 {
		t.Fatalf("a colour from data needs no <style>, found %d:\n%.2000s", len(styles), data)
	}
	if !strings.Contains(data, `<span class="badge flex-row color-danger">`) ||
		!strings.Contains(dataCSS, ".color-danger:where(.badge){--kitwork-tint:color-mix(in oklab,var(--danger) 15%,var(--canvas));color:var(--kitwork-badge-danger)}") ||
		!strings.Contains(dataCSS, "--kitwork-badge-danger:#") {
		t.Errorf("the badge's colour from data is a class whose rule the page's stylesheet has:\n%.1500s", data)
	}
}

// The name is the role and the bytes: two pages whose classes are the same share every stylesheet; a
// page with one class more has its own page stylesheet but shares the rest. The same source booted
// again names the same files, and the new boot answers them before any page was asked for: the pages
// compiled with the generation, not on first use.
func TestUIPagesShareTheirStylesheetByItsBytes(t *testing.T) {
	files := stylesheetFiles(t)
	tenant := buildFilesSite(t, files)
	roles := []string{"reboot", "colors", "fonts", "icons", "page"}
	hrefs := func(tenant *Tenant, target string) map[string]string {
		byRole := map[string]string{}
		for _, sheet := range stylesheetsOf(t, tenant, serveUI(t, tenant, target).Body.String()) {
			byRole[sheet.role] = sheet.href
		}
		if len(byRole) != len(roles) {
			t.Fatalf("%s must link the five roles: %v", target, byRole)
		}
		return byRole
	}
	rich, same, other := hrefs(tenant, "http://localhost/giau"), hrefs(tenant, "http://localhost/cung-lop"), hrefs(tenant, "http://localhost/khac")
	for _, role := range roles {
		if rich[role] != same[role] {
			t.Errorf("two pages with the same classes must share the %s stylesheet: %s and %s", role, rich[role], same[role])
		}
	}
	if rich["page"] == other["page"] {
		t.Errorf("a page with a class more must have its own page stylesheet, both are %s", rich["page"])
	}
	for _, role := range []string{"reboot", "colors", "fonts", "icons"} {
		if rich[role] != other[role] {
			t.Errorf("pages whose classes differ share the %s stylesheet: %v and %v", role, rich, other)
		}
	}

	restarted := buildFilesSite(t, files)
	for _, href := range other {
		if rec := serveUI(t, restarted, "http://localhost"+href); rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
			t.Fatalf("a new boot of the same source must answer %s before any page is asked for: %d %q", href, rec.Code, rec.Header().Get("Cache-Control"))
		}
	}
	if again := hrefs(restarted, "http://localhost/giau"); again["page"] != rich["page"] || again["icons"] != rich["icons"] || again["fonts"] != rich["fonts"] {
		t.Errorf("the same source must name the same stylesheets after a restart: %v, then %v", rich, again)
	}
}

// A .ui-only edit changes what the page's classes are, nothing else: the reboot and the colours keep
// their URL, so a browser keeps them across the deploy; the page's own file is a new one.
// CONTROL: an edit to router.colors gives the colours a new URL and leaves the reboot's.
func TestRebootAndColorsKeepTheirURLAcrossAUIEdit(t *testing.T) {
	hrefs := func(files map[string]string) map[string]string {
		tenant := buildFilesSite(t, files)
		byRole := map[string]string{}
		for _, sheet := range stylesheetsOf(t, tenant, serveUI(t, tenant, "http://localhost/giau").Body.String()) {
			byRole[sheet.role] = sheet.href
		}
		return byRole
	}
	files := stylesheetFiles(t)
	before := hrefs(files)
	edited := stylesheetFiles(t)
	edited["localhost/giau/content.ui"] = strings.Replace(edited["localhost/giau/content.ui"], `padding="6"`, `padding="8"`, 1)
	after := hrefs(edited)
	if before["reboot"] == "" || before["reboot"] != after["reboot"] || before["colors"] != after["colors"] || before["fonts"] != after["fonts"] {
		t.Errorf("a .ui edit must keep the reboot, colours and fonts URLs: %v, then %v", before, after)
	}
	if before["page"] == after["page"] {
		t.Errorf("the page's own stylesheet changes with its classes: %s both times", before["page"])
	}
	recoloured := stylesheetFiles(t)
	recoloured["localhost/router.work"] = strings.Replace(recoloured["localhost/router.work"], `brand: "#c8742c"`, `brand: "#2563eb"`, 1)
	if control := hrefs(recoloured); control["colors"] == before["colors"] || control["reboot"] != before["reboot"] {
		t.Errorf("CONTROL: a router.colors edit changes the colours file and only it: %v, then %v", before, control)
	}
}

// Each stylesheet is an engine file like the scripts: text/css, kept for a year and never revalidated
// (a different byte is a different name), its ETag the whole SHA-256 of its bytes, its name's hash the
// start of it. A name this generation never made is the engine's 404, not kept, and never a file of
// the tenant's — not one in a folder named _kitwork at that very name, however the request spells it:
// an unknown hash, a known hash under another role, a page stylesheet of a build before. The fonts
// stylesheet, /_kitwork/fonts.<hash>.css, and the woff2 folder, /_kitwork/fonts/…, are two names: each
// answers its own, neither the other's. CONTROL: the same file in a folder of another name IS served
// from the disk, so the fixture would leak if the engine did not answer first.
func TestPageStylesheetIsAnEngineFile(t *testing.T) {
	stale := strings.Repeat("0", 16)
	files := stylesheetFiles(t)
	files["localhost/_kitwork/page."+stale+".css"] = "TENANT-SECRET"
	files["localhost/_kitwork/fonts."+stale+".css"] = "TENANT-SECRET"
	files["localhost/_kitwork/reboot."+stale+".css"] = "TENANT-SECRET"
	files["localhost/_kitworks/page."+stale+".css"] = "TENANT-SECRET control"
	tenant := buildFilesSite(t, files)
	page := serveUI(t, tenant, "http://localhost/giau").Body.String()
	sheets := stylesheetsOf(t, tenant, page)
	if len(sheets) != 5 {
		t.Fatalf("the page must link five stylesheets, found %d", len(sheets))
	}

	for _, sheet := range sheets {
		response := serveEngine(tenant, http.MethodGet, sheet.href)
		sum := sha256.Sum256([]byte(sheet.css))
		if response.Header().Get("Content-Type") != "text/css; charset=utf-8" ||
			response.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
			response.Header().Get("ETag") != `"`+hex.EncodeToString(sum[:])+`"` ||
			sheet.href != "/_kitwork/"+sheet.role+"."+hex.EncodeToString(sum[:])[:16]+".css" {
			t.Errorf("%s: Content-Type %q, Cache-Control %q, ETag %q — want text/css, kept for good, named by its role and its bytes, tagged by its bytes", sheet.href,
				response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"), response.Header().Get("ETag"))
		}
		if again := serveEngine(tenant, http.MethodGet, sheet.href, "If-None-Match", response.Header().Get("ETag")); again.Code != http.StatusNotModified {
			t.Errorf("%s: a matching ETag is %d, want 304", sheet.href, again.Code)
		}
		if head := serveEngine(tenant, http.MethodHead, sheet.href); head.Code != http.StatusOK || head.Body.Len() != 0 {
			t.Errorf("HEAD %s: %d with %d bytes", sheet.href, head.Code, head.Body.Len())
		}
		if post := serveEngine(tenant, http.MethodPost, sheet.href); post.Code != http.StatusNotFound {
			t.Errorf("POST %s: %d, want 404", sheet.href, post.Code)
		}
	}

	byRole := sheetsByRole(sheets)
	hashOf := func(role string) string {
		return strings.TrimSuffix(strings.TrimPrefix(byRole[role].href, "/_kitwork/"+role+"."), ".css")
	}
	for _, path := range []string{
		"/_kitwork/page." + stale + ".css",
		"/_kitwork/icons." + stale + ".css",
		"/_kitwork/logo." + stale + ".css",
		"/_kitwork/fonts." + stale + ".css",
		"/_kitwork/reboot." + stale + ".css",
		"/_kitwork/colors." + stale + ".css",
		"/_KITWORK/page." + stale + ".css",
		"/_kitwork./page." + stale + ".css",
		"/_kitwork/page." + stale[:15] + ".css",
		"/_kitwork/page." + strings.ToUpper(hashOf("page")) + ".css",
		"/_kitwork/icons." + hashOf("page") + ".css", // a real hash under another role
		"/_kitwork/fonts." + hashOf("icons") + ".css",
		"/_kitwork/page." + hashOf("fonts") + ".css",
		"/_kitwork/colors." + hashOf("reboot") + ".css",
		"/_kitwork/reboot." + hashOf("colors") + ".css",
		"/_kitwork/fonts/" + hashOf("fonts") + ".css", // the woff2 folder knows no stylesheet
		"/_kitwork/fonts/fonts." + hashOf("fonts") + ".css",
		"/_kitwork/page.css",
		"/_kitwork/css/page." + stale + ".css",
	} {
		response := serveEngine(tenant, http.MethodGet, path)
		if response.Code != http.StatusNotFound || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "TENANT-SECRET") {
			t.Errorf("%s: status %d, Cache-Control %q, body %.40q — want the engine's 404, not kept", path, response.Code, response.Header().Get("Cache-Control"), response.Body.String())
		}
	}
	// "fonts." is the stylesheet, "fonts/" the woff2 files: the face the stylesheet names is served
	// from the folder, a font file and not CSS.
	face := regexp.MustCompile(`url\(['"]?(/_kitwork/fonts/[^)'"]+\.woff2)`).FindStringSubmatch(byRole["fonts"].css)
	if face == nil {
		t.Fatalf("the fonts stylesheet names no woff2:\n%.300s", byRole["fonts"].css)
	}
	if response := serveEngine(tenant, http.MethodGet, face[1]); response.Code != http.StatusOK || response.Header().Get("Content-Type") != "font/woff2" {
		t.Errorf("%s: %d %q — the folder beside fonts.<hash>.css serves the face", face[1], response.Code, response.Header().Get("Content-Type"))
	}
	if control := serveEngine(tenant, http.MethodGet, "/_kitworks/page."+stale+".css"); control.Code != http.StatusOK || control.Body.String() != "TENANT-SECRET control" {
		t.Fatalf("CONTROL: a folder of another name is the tenant's own and is served: %d %.40q", control.Code, control.Body.String())
	}
}

// A request runs no presentation pass over its document. Data text that a pass would read as
// something to serve — a font-family naming a vendored typeface, the name of the appearance API —
// changes nothing: no @font-face, no preload, no pre-paint. CONTROL: the per-request passes, run over
// that same document, add both. (The shared site has no dark look, so nothing else pre-paints.)
func TestUIPageRunsNoPassPerRequest(t *testing.T) {
	const note = "font-family: Fira Code — toggleTheme"
	tenant := buildFilesSite(t, withFiles(map[string]string{
		"localhost/du-lieu/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const note = "` + note + `";
});`,
		"localhost/du-lieu/content.ui": `<content><text>{{ note }}</text></content>`,
	}))
	rec := serveUI(t, tenant, "http://localhost/du-lieu")
	page := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(page, note) {
		t.Fatalf("GET /du-lieu = %d, want the data's sentence on the page:\n%.1500s", rec.Code, page)
	}
	_, css := stylesheetOf(t, tenant, page)
	whole := page + css
	for _, unwanted := range []string{"fira-code", "font-family:'Fira Code'", `data-kitwork-generated="appearance"`} {
		if strings.Contains(whole, unwanted) {
			t.Errorf("a pass read the request's data: the page or its stylesheet has %q", unwanted)
		}
	}

	plan := tenant.renderPlan()
	compiled, err := plan.ui.page("du-lieu", false)
	if err != nil {
		t.Fatal(err)
	}
	document := compiled.page.Document(ui.Request{Path: "/du-lieu", Scope: map[string]any{"note": note}})
	old := plan.renderer("du-lieu").base.Present(document)
	for _, want := range []string{"fira-code-400", `data-kitwork-generated="appearance"`} {
		if !strings.Contains(old, want) {
			t.Errorf("CONTROL: the per-request passes over the same document add %q; without it this test proves nothing", want)
		}
	}
}

// A class from data keeps its place in the cascade: it is in the page's stylesheet with the page's
// own, in the fixed order, so on a large screen the box keeps large:background="paper" over the
// background its data gives it — the width rule comes after every unprefixed one, whatever the
// value. No <style> is written for it.
func TestUIPageDataClassKeepsItsPlaceInTheCascade(t *testing.T) {
	tenant := buildFilesSite(t, withRootRouter(t,
		`router.colors({ brand: "#c8742c", canvas: "#f6f3ee", paper: "#ffffff", danger: "#e5484d" });`,
		map[string]string{
			"localhost/muon/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const tone = "danger";
});`,
			"localhost/muon/content.ui": `<content background="canvas"><box background="{{ tone }}" large:background="paper"><text>Hết món</text></box></content>`,
		}))
	page := serveUI(t, tenant, "http://localhost/muon").Body.String()
	if !strings.Contains(page, `class="flex-column large:background-paper background-danger"`) {
		t.Fatalf("the box carries the data's background and the page's large one:\n%.2500s", page)
	}
	if styles := generatedStyleRe.FindAllString(page, -1); len(styles) != 0 {
		t.Fatalf("no <style> for a class from data, found %d:\n%.2500s", len(styles), page)
	}
	_, css := stylesheetOf(t, tenant, page)
	data, large := strings.Index(css, ".background-danger{background-color:var(--danger)}"), strings.Index(css, `.large\:background-paper{background-color:var(--paper)}`)
	if data < 0 || large < 0 || data > large {
		t.Errorf("large:background-paper comes after the data's background in the page's stylesheet (data %d, large %d):\n%s", data, large, css)
	}
}

// A branch with its own router.colors (/quan-ly) links a colours file of its own and sends no colour
// CSS with any response: its badge colour from data is a class of its page's stylesheet, the branch's
// brand a token of its colours file. CONTROL: the site root's colours file is another.
func TestBranchSendsNoInlineColourCSS(t *testing.T) {
	tenant := buildFilesSite(t, withFiles(map[string]string{
		"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.colors({ brand: "#2563eb" });
router.view((ctx) => {
  const orders = [{ tone: "warning" }, { tone: "success" }, { tone: "danger" }];
});`,
		"localhost/quan-ly/content.ui": `<content><list><item for="order of orders"><badge color="{{ order.tone }}"><text>{{ order.tone }}</text></badge></item></list></content>`,
	}))
	branch := serveUI(t, tenant, "http://localhost/quan-ly").Body.String()
	if styles := generatedStyleRe.FindAllString(branch, -1); len(styles) != 0 {
		t.Fatalf("/quan-ly sends no inline CSS, found %d:\n%.2000s", len(styles), branch)
	}
	for _, tone := range []string{"warning", "success", "danger"} {
		if !strings.Contains(branch, `<span class="badge flex-row color-`+tone+`">`) {
			t.Errorf("the %s badge is a class:\n%.2000s", tone, branch)
		}
	}
	byRole := sheetsByRole(stylesheetsOf(t, tenant, branch))
	if !strings.Contains(byRole["colors"].css, "--brand:#2563eb") || !strings.Contains(byRole["page"].css, ".color-success:where(.badge)") {
		t.Errorf("the branch's brand is in its colours file, its badge colours in its page file:\ncolors %.300s\npage %.600s", byRole["colors"].css, byRole["page"].css)
	}
	root := sheetsByRole(stylesheetsOf(t, tenant, serveUI(t, tenant, "http://localhost/").Body.String()))
	if root["colors"].href == byRole["colors"].href || strings.Contains(root["colors"].css, "#2563eb") {
		t.Errorf("CONTROL: the root's colours are its own file: root %s, branch %s", root["colors"].href, byRole["colors"].href)
	}
}

// Icons only a response's data names (<icon value="{{ … }}" />) are no inline <style>: the page's own
// icons and the data's are one set, linked as ONE icons file in place of the page's own, built once
// per distinct set. The same set is the same file; another set another file; an icon the page shows
// already changes nothing (the page's own file).
func TestIconsFromDataAreOneFilePerSet(t *testing.T) {
	tenant := buildFilesSite(t, withFiles(map[string]string{
		"localhost/bieu-tuong/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const glyph = ctx.query("glyph") || "coffee";
});`,
		"localhost/bieu-tuong/content.ui": `<content><icon value="coffee" alt=""></icon><icon value="{{ glyph }}" alt=""></icon></content>`,
	}))
	icons := func(glyph string) (string, linkedStylesheet) {
		page := serveUI(t, tenant, "http://localhost/bieu-tuong?glyph="+glyph).Body.String()
		if styles := generatedStyleRe.FindAllString(page, -1); len(styles) != 0 {
			t.Fatalf("?glyph=%s: no inline <style>, found %d:\n%.2000s", glyph, len(styles), page)
		}
		byRole := sheetsByRole(stylesheetsOf(t, tenant, page))
		if strings.Count(page, `data-kitwork-generated="icons"`) != 1 {
			t.Fatalf("?glyph=%s: one icons link:\n%.2000s", glyph, page)
		}
		return page, byRole["icons"]
	}
	_, own := icons("coffee")
	_, sun := icons("sun")
	_, sunAgain := icons("sun")
	_, moon := icons("moon")
	if sun.href == own.href || !strings.Contains(sun.css, ".icon-sun") || !strings.Contains(sun.css, ".icon-coffee") ||
		!strings.HasPrefix(sun.css, stylesheet.LayerStatement+"\n@layer elements{\n") {
		t.Errorf("the data's sun and the page's coffee are one layered file of their own:\nown %s\nsun %s\n%.400s", own.href, sun.href, sun.css)
	}
	if sunAgain.href != sun.href {
		t.Errorf("the same set is the same file: %s, then %s", sun.href, sunAgain.href)
	}
	if moon.href == sun.href || strings.Contains(moon.css, ".icon-sun") {
		t.Errorf("another set is another file: sun %s, moon %s", sun.href, moon.href)
	}
	if strings.Contains(own.css, ".icon-sun") {
		t.Errorf("the page's own icons file holds its own icons only:\n%.300s", own.css)
	}
	// an unknown name from data changes nothing: the page's own file, no class, no CSS
	if _, unknown := icons("not-an-icon-at-all"); unknown.href != own.href {
		t.Errorf("an icon the engine does not have keeps the page's own file: %s, want %s", unknown.href, own.href)
	}
}

// The files made for icons from data are kept with a bound: the recently used sets. A set let go
// answers like any name never made (the engine's 404), and the next response that needs it builds the
// same file again, under the same name. CONTROL: a set still kept answers.
func TestIconsFromDataKeepTheRecentlyUsedSets(t *testing.T) {
	var store dataIconsStore
	store.bound = 2
	built := 0
	file := func(set string) string {
		return store.href(set, func() []byte { built++; return []byte("/* " + set + " */") })
	}
	a, b := file("a"), file("b")
	if a == b || built != 2 {
		t.Fatalf("two sets, two files: %s %s (%d built)", a, b, built)
	}
	if file("a") != a || built != 2 {
		t.Errorf("a set kept is not built again (%d built)", built)
	}
	c := file("c") // a was used last before c: b is the least recently used
	name := func(href string) string {
		return strings.TrimSuffix(strings.TrimPrefix(href, "/_kitwork/"), ".css")
	}
	if _, ok := store.stylesheet(name(b)); ok {
		t.Errorf("the least recently used set is let go past the bound")
	}
	for _, kept := range []string{a, c} {
		if _, ok := store.stylesheet(name(kept)); !ok {
			t.Errorf("CONTROL: %s is still kept", kept)
		}
	}
	if again := file("b"); again != b || built != 4 {
		t.Errorf("a set let go is built again under the same name: %s, then %s (%d built)", b, again, built)
	}

	// through the tenant: an evicted name is the engine's 404
	tenant := buildFilesSite(t, withFiles(map[string]string{
		"localhost/bieu-tuong/router.work": `import { router } from "kitwork";
router.view((ctx) => {
  const glyph = ctx.query("glyph");
});`,
		"localhost/bieu-tuong/content.ui": `<content><icon value="{{ glyph }}" alt=""></icon></content>`,
	}))
	tenant.renderPlan().ui.dataIcons.bound = 1
	first := sheetsByRole(stylesheetsOf(t, tenant, serveUI(t, tenant, "http://localhost/bieu-tuong?glyph=sun").Body.String()))["icons"].href
	serveUI(t, tenant, "http://localhost/bieu-tuong?glyph=moon")
	if response := serveEngine(tenant, http.MethodGet, first); first == "" || response.Code != http.StatusNotFound {
		t.Errorf("%s, let go past the bound, must answer the engine's 404, got %d", first, response.Code)
	}
}

// fonts.<hash>.css is one file for every page of a site, built for the weights the site's pages use —
// running text's 400, a badge's 500 (on one page only), a heading's 600, weight="bold"'s 700, gathered
// when the generation compiles. Outfit is a variable font: its file draws every weight of its axis, so
// it ships one face per subset over the whole axis, font-weight 100 900 (fonts.FaceCSS) — the 600
// that a face per weight never shipped is drawn from it, and nothing is written for the 300 and 900 the
// site does not use. CONTROL: a site whose pages show no badge gathers no 500, and ships the same file.
func TestUIFontsFileShipsTheWeightsTheSiteUses(t *testing.T) {
	outfit := regexp.MustCompile(`@font-face\{font-family:'Outfit';font-style:normal;font-weight:([0-9 ]+);font-display:swap;src:url\(/_kitwork/fonts/outfit-400-(latin|latin-ext)\.[0-9a-f]{16}\.woff2\)`)
	files := withFiles(map[string]string{
		"localhost/content.ui":      `<content><text heading="1">Quán</text><text weight="bold">29k</text></content>`,
		"localhost/tinh/content.ui": `<content><badge color="brand"><text>mới</text></badge></content>`,
	})
	tenant := buildFilesSite(t, files)
	home := sheetsByRole(stylesheetsOf(t, tenant, serveUI(t, tenant, "http://localhost/").Body.String()))["fonts"]
	other := sheetsByRole(stylesheetsOf(t, tenant, serveUI(t, tenant, "http://localhost/tinh").Body.String()))["fonts"]
	if got := tenant.renderPlan().ui.weights; fmt.Sprint(got) != "[400 500 600 700]" {
		t.Errorf("the site's weights are 400 500 600 700, gathered %v", got)
	}
	faces := outfit.FindAllStringSubmatch(home.css, -1)
	if len(faces) != 2 || faces[0][1] != "100 900" || faces[1][1] != "100 900" || faces[0][2] == faces[1][2] ||
		strings.Count(home.css, "font-family:'Outfit'") != 2 {
		t.Errorf("Outfit ships one face per subset over its axis, 100 900:\n%s", home.css)
	}
	if home.href != other.href {
		t.Errorf("every page of the site shares one fonts file: %s and %s", home.href, other.href)
	}
	files["localhost/tinh/content.ui"] = `<content><text>tĩnh</text></content>`
	control := buildFilesSite(t, files)
	if got := control.renderPlan().ui.weights; fmt.Sprint(got) != "[400 600 700]" {
		t.Errorf("CONTROL: no badge on any page, no 500: gathered %v", got)
	}
	if sheet := sheetsByRole(stylesheetsOf(t, control, serveUI(t, control, "http://localhost/").Body.String()))["fonts"]; sheet.css != home.css {
		t.Errorf("CONTROL: a variable family ships the same faces whatever the weights:\n%s\n%s", sheet.css, home.css)
	}
}
