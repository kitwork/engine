package work

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	jittheme "github.com/kitwork/engine/jit/theme"
	"github.com/kitwork/engine/value"
	"github.com/kitwork/engine/version"
)

// buildUISite writes a Kitwork UI site (router.work + *.ui) under apps-like root/acme/localhost and
// starts its tenant. Files are given relative to the identity folder (acme/). The site runs in
// development mode (unminified) so the assertions read like the HTML the engine writes.
func buildUISite(t *testing.T, files map[string]string) *Tenant {
	t.Helper()
	return buildUISiteMode(t, files, true)
}

func buildUISiteMode(t *testing.T, files map[string]string, local bool) *Tenant {
	t.Helper()
	tenant, err := bootUISite(t, files, local)
	if err != nil {
		t.Fatal(err)
	}
	return tenant
}

// bootUISite writes the site and starts its tenant, returning the boot's error instead of stopping the
// test: a router a site must refuse is tested through it.
func bootUISite(t *testing.T, files map[string]string, local bool) (*Tenant, error) {
	t.Helper()
	saved := AllowLocal
	AllowLocal = local
	t.Cleanup(func() { AllowLocal = saved })
	root := t.TempDir()
	for name, body := range files {
		file := filepath.Join(root, "acme", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(file), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	tenant := NewTenant(root, "localhost")
	return tenant, tenant.Run()
}

// withFiles is a copy of the shared site with some files replaced or added.
func withFiles(changes map[string]string) map[string]string {
	files := make(map[string]string, len(uiSiteFiles)+len(changes))
	for name, source := range uiSiteFiles {
		files[name] = source
	}
	for name, source := range changes {
		files[name] = source
	}
	return files
}

// sitePalette is the line of the shared site's router.work that declares its colours.
const sitePalette = `router.colors({ brand: "#c8742c" });`

// withRootRouter is the shared site with its root router.work's colours line replaced.
func withRootRouter(t *testing.T, replacement string, changes map[string]string) map[string]string {
	t.Helper()
	files := withFiles(changes)
	router := files["localhost/router.work"]
	if strings.Count(router, sitePalette) != 1 {
		t.Fatalf("the shared router.work no longer declares %s", sitePalette)
	}
	files["localhost/router.work"] = strings.Replace(router, sitePalette, replacement, 1)
	return files
}

func serveUI(t *testing.T, tenant *Tenant, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	tenant.Serve(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

// pageStylesheetRe finds the stylesheets a Kitwork UI page links: one per role, each named by its role
// and its bytes, /_kitwork/<role>.<hash>.css, its link marked with data-kitwork-generated (the role;
// "css" for the page's own). The minifier may drop the quotes.
var pageStylesheetRe = regexp.MustCompile(`<link rel="?stylesheet"? href="?(/_kitwork/(reboot|colors|fonts|icons|logo|page)\.([0-9a-f]{16})\.css)"? data-kitwork-generated="?([a-z]+)"?>`)

// inlineStylesheetRe finds the stylesheets a Kitwork UI page carries in its HTML (router.bundle, by
// default "auto": the page's own, its icons, and the site's when the page fits the first window), each
// a <style> marked with the role's data-kitwork-generated value ("css" for the page's own, "site" for
// reboot, colours and fonts together).
var inlineStylesheetRe = regexp.MustCompile(`(?s)<style data-kitwork-generated="?([a-z]+)"?>(.*?)</style>`)

// siteStylesheetRe finds the site part a page links as one file when it is not inline
// (router.bundle "split", or "auto" for a page past the first window): /_kitwork/site.<hash>.css.
var siteStylesheetRe = regexp.MustCompile(`<link rel="?stylesheet"? href="?(/_kitwork/(site)\.([0-9a-f]{16})\.css)"? data-kitwork-generated="?([a-z]+)"?>`)

// linkedStylesheet is one stylesheet a page has: its role, its href ("" for one written in the page),
// the data-kitwork-generated value of its link or block and its CSS as the browser reads it.
type linkedStylesheet struct {
	role, href, generated, css string
}

// stylesheetsOf is every stylesheet a page has, in the order it has them: each file it links, fetched
// from the tenant (each must answer), and each block it carries.
func stylesheetsOf(t *testing.T, tenant *Tenant, page string) []linkedStylesheet {
	t.Helper()
	type found struct {
		at    int
		sheet linkedStylesheet
	}
	var all []found
	for _, re := range []*regexp.Regexp{pageStylesheetRe, siteStylesheetRe} {
		for _, at := range re.FindAllStringSubmatchIndex(page, -1) {
			href, role, generated := page[at[2]:at[3]], page[at[4]:at[5]], page[at[8]:at[9]]
			rec := serveUI(t, tenant, "http://localhost"+href)
			if rec.Code != http.StatusOK {
				t.Fatalf("%s = %d, want the page's stylesheet", href, rec.Code)
			}
			all = append(all, found{at[0], linkedStylesheet{role: role, href: href, generated: generated, css: rec.Body.String()}})
		}
	}
	for _, at := range inlineStylesheetRe.FindAllStringSubmatchIndex(page, -1) {
		generated := page[at[2]:at[3]]
		role := generated
		if role == "css" {
			role = "page"
		}
		all = append(all, found{at[0], linkedStylesheet{role: role, generated: generated, css: page[at[4]:at[5]]}})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].at < all[j].at })
	sheets := make([]linkedStylesheet, 0, len(all))
	for _, f := range all {
		sheets = append(sheets, f.sheet)
	}
	return sheets
}

// stylesheetOf is the page's own stylesheet (exactly one: /_kitwork/page.<hash>.css under
// router.bundle(false), else its <style data-kitwork-generated="css">, whose href is then "inline") and
// the CSS of every stylesheet the page has joined in their order: the cascade the browser has.
func stylesheetOf(t *testing.T, tenant *Tenant, page string) (href, css string) {
	t.Helper()
	var all []string
	for _, sheet := range stylesheetsOf(t, tenant, page) {
		if sheet.role == "page" {
			if href != "" {
				t.Fatalf("a .ui page has one stylesheet of its own, found %s and %s", href, sheet.href)
			}
			href = sheet.href
			if href == "" {
				href = "inline"
			}
		}
		all = append(all, sheet.css)
	}
	if href == "" {
		t.Fatalf("a .ui page has a stylesheet of its own, /_kitwork/page.<hash>.css or <style data-kitwork-generated=\"css\">:\n%.1500s", page)
	}
	return href, strings.Join(all, "\n")
}

// pageWithStylesheet is a page as the browser has it: its HTML (with the blocks router.bundle writes in
// it), then the CSS of the files it links.
func pageWithStylesheet(t *testing.T, tenant *Tenant, target string) string {
	t.Helper()
	page := serveUI(t, tenant, target).Body.String()
	stylesheetOf(t, tenant, page) // it has a stylesheet of its own
	var linked []string
	for _, sheet := range stylesheetsOf(t, tenant, page) {
		if sheet.href != "" { // a block written in the page is in page already
			linked = append(linked, sheet.css)
		}
	}
	return page + "\n" + strings.Join(linked, "\n")
}

var uiSiteFiles = map[string]string{
	".shared/words.work": `export const greet = (name) => "Chào " + name;`,
	"localhost/router.work": `
import { router } from "kitwork";
import { greet } from "../.shared/words.work";

router.colors({ brand: "#c8742c" });
router.fonts({ body: "Outfit" });
router.meta({ site: "Quán" });
router.view((ctx) => {
  const items = [{ name: "Cà phê" }, { name: "Trà <đào>" }];
  var count = 2;
  function twice(n) { return n * 2; }
  ctx.title("Thực đơn");
});
`,
	"localhost/page.ui":     `<page><content /></page>`,
	"localhost/content.ui":  `<content><text heading="1">{{ greet("bạn") }}</text><list><item for="item of items"><text color="ink-30">{{ item.name }}</text></item></list><text>{{ count }} món, gấp đôi {{ twice(count) }}</text></content>`,
	"localhost/notfound.ui": `<content><text>Không thấy trang</text></content>`,
	"localhost/mon/{slug}/router.work": `
import { router } from "kitwork";
router.view((ctx) => {
  if (ctx.params("slug") != "ca-phe") return ctx.notFound();
  const name = "Cà phê sữa";
});
`,
	"localhost/mon/{slug}/content.ui": `<content><text>{{ name }}</text></content>`,
	"localhost/tinh/content.ui":       `<content><text>Trang tĩnh</text></content>`,
}

// A router.work site serves its .ui pages: the view's const/var, its functions and the file's
// imports are what the page reads; ctx.title reaches <head>; router.colors and router.fonts reach the
// CSS.
func TestUISiteServesViewNames(t *testing.T) {
	tenant := buildUISite(t, uiSiteFiles)
	rec := serveUI(t, tenant, "http://localhost/")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / = %d\n%s", rec.Code, rec.Body.String())
	}
	body := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{
		"<title>Thực đơn</title>",
		"<h1>Chào bạn</h1>",       // an import of router.work, called from the page; a heading is sized by its level (reboot.css)
		"Cà phê</p>",              // a const of the view
		"Trà &lt;đào&gt;",         // escaped
		"<p>2 món, gấp đôi 4</p>", // a var and a function of the view
	} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / is missing %q\n%s", want, body)
		}
	}
	// the stylesheets it links: the body typeface of router.fonts, the site's token, the rule of the
	// class the page uses
	for _, want := range []string{"--font-body:Outfit", "--brand:#c8742c", `.color-ink-30{color:color-mix(in oklab,var(--ink) 70%,var(--canvas))}`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / CSS is missing %q", want)
		}
	}
	if strings.Contains(body, "{{") {
		t.Errorf("GET / leaked template source:\n%s", body)
	}
}

func TestUISiteNotFound(t *testing.T) {
	tenant := buildUISite(t, uiSiteFiles)

	ok := serveUI(t, tenant, "http://localhost/mon/ca-phe")
	if ok.Code != http.StatusOK || !strings.Contains(ok.Body.String(), "<p>Cà phê sữa</p>") {
		t.Fatalf("GET /mon/ca-phe = %d\n%s", ok.Code, ok.Body.String())
	}
	// ctx.notFound() in a view, and a path with no folder, both show the nearest notfound.ui at 404
	for _, target := range []string{"http://localhost/mon/tra", "http://localhost/khong-co"} {
		rec := serveUI(t, tenant, target)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "<p>Không thấy trang</p>") {
			t.Errorf("GET %s = %d, want 404 with notfound.ui\n%s", target, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "Cà phê sữa") {
			t.Errorf("GET %s rendered the page's content instead of notfound.ui", target)
		}
	}
}

// A folder with content.ui and no router.view is a page with no data.
func TestUISiteFolderWithoutView(t *testing.T) {
	tenant := buildUISite(t, uiSiteFiles)
	rec := serveUI(t, tenant, "http://localhost/tinh")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<p>Trang tĩnh</p>") {
		t.Fatalf("GET /tinh = %d\n%s", rec.Code, rec.Body.String())
	}
}

// In production what the engine makes for a .ui page is minified once, when the page compiles: its
// stylesheets and the head built from its skeleton. The response is the page as it renders — the
// renderer writes no whitespace between tags — and no pass reads it again; it is complete.
// Control: while developing neither is minified.
func TestUISiteProductionIsMinified(t *testing.T) {
	tenant := buildUISiteMode(t, uiSiteFiles, false)
	rec := serveUI(t, tenant, "http://localhost/")
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / in production = %d: %s", rec.Code, body)
	}
	for _, want := range []string{">Chào bạn</h1>", "Trà &lt;đào&gt;", "2 món, gấp đôi 4", `<meta name=generator content="Kitwork `} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / in production is missing %q\n%.1200s", want, body)
		}
	}
	// the engine writes a .ui page's stylesheets compact itself, so production serves the bytes
	// development does: no minifier runs on them
	_, css := stylesheetOf(t, tenant, body)
	if !strings.Contains(css, "--font-body:Outfit") || strings.Contains(css, "{\n  ") || strings.Contains(css, "/*") {
		t.Errorf("the stylesheets are compact in production:\n%.400s", css)
	}

	dev := buildUISite(t, uiSiteFiles)
	page := serveUI(t, dev, "http://localhost/").Body.String()
	_, devCSS := stylesheetOf(t, dev, page)
	if devCSS != css {
		t.Errorf("the stylesheets are the same bytes while developing and in production:\n%.400s\n---\n%.400s", devCSS, css)
	}
	if !strings.Contains(page, `<meta name="generator"`) {
		t.Errorf("control: while developing the head is written out:\n%.400s", page)
	}
}

// A mistake in a .ui file is reported with its place while developing, never as a half page.
func TestUISiteCompileErrorNamesThePlace(t *testing.T) {
	files := map[string]string{}
	for name, body := range uiSiteFiles {
		files[name] = body
	}
	files["localhost/content.ui"] = "<content>\n  <box gap=4>x</box>\n</content>"
	tenant := buildUISite(t, files)
	rec := serveUI(t, tenant, "http://localhost/")
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "content.ui:2:12") {
		t.Fatalf("GET / with a broken content.ui = %d, want 500 naming content.ui:2:12\n%s", rec.Code, rec.Body.String())
	}
}

// A router.work site moves between pages without reloading by default: every page, static ones
// included, gets the kernel and Drive. router.reload(true) loads every page whole instead.
func TestUISiteDoesNotReloadByDefault(t *testing.T) {
	tenant := buildUISite(t, uiSiteFiles)
	body := serveUI(t, tenant, "http://localhost/tinh").Body.String() // a static page, no router.view
	if !strings.Contains(body, `data-kitwork-generated="runtime"`) || !driveFile.MatchString(body) {
		t.Errorf("page is missing the kernel or Drive\n%s", body)
	}

	off := map[string]string{}
	for name, source := range uiSiteFiles {
		off[name] = source
	}
	off["localhost/router.work"] = strings.Replace(off["localhost/router.work"], `router.meta(`, `router.reload(true);
router.meta(`, 1)
	body = serveUI(t, buildUISite(t, off), "http://localhost/tinh").Body.String()
	if strings.Contains(body, "<script") {
		t.Errorf("router.reload(true) still ships scripts:\n%s", body)
	}
	if !strings.Contains(body, "<p>Trang tĩnh</p>") {
		t.Errorf("router.reload(true) broke the page itself:\n%s", body)
	}
}

// router.transition says how <content /> changes on a move; the page carries its folder's value on
// <html> for Drive to read, inherited down the folders and replaced by a nearer one. "none" (the
// default) writes nothing, and a word outside none/fade/slide is refused at boot.
func TestUISiteTransitionFollowsTheFolders(t *testing.T) {
	files := map[string]string{}
	for name, source := range uiSiteFiles {
		files[name] = source
	}
	// control first: no transition anywhere writes nothing
	if body := serveUI(t, buildUISite(t, files), "http://localhost/tinh").Body.String(); strings.Contains(body, "data-kitwork-transition") {
		t.Fatalf("a site with no router.transition must write nothing:\n%.400s", body)
	}

	files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `router.meta(`, `router.reload(false).transition("fade");
router.meta(`, 1)
	files["localhost/mon/{slug}/router.work"] = strings.Replace(files["localhost/mon/{slug}/router.work"], `router.view(`, `router.transition("slide");
router.view(`, 1)
	files["localhost/tinh/router.work"] = `import { router } from "kitwork";
router.transition("none");`
	tenant := buildUISite(t, files)
	for target, want := range map[string]string{
		"http://localhost/":           `<html lang="vi" data-kitwork-transition="fade">`,  // the root's own
		"http://localhost/mon/ca-phe": `<html lang="vi" data-kitwork-transition="slide">`, // a nearer folder wins
	} {
		if body := serveUI(t, tenant, target).Body.String(); !strings.Contains(body, want) {
			t.Errorf("GET %s is missing %s\n%.400s", target, want, body)
		}
	}
	// "none" on a branch turns the root's fade off there
	if body := serveUI(t, tenant, "http://localhost/tinh").Body.String(); strings.Contains(body, "data-kitwork-transition") {
		t.Errorf(`router.transition("none") on /tinh must write nothing:\n%.400s`, body)
	}

	if _, err := (&FolderRouter{}).Transition(value.NewString("fable")); err == nil || !strings.Contains(err.Error(), `"none", "fade" and "slide"`) {
		t.Errorf(`router.transition("fable") must name the transitions there are, got %v`, err)
	}
}

// A Kitwork UI page is finished by the same passes as a template page, so it names the engine too,
// also once minified for production.
func TestUISiteNamesTheGenerator(t *testing.T) {
	want := `name="generator" content="Kitwork ` + version.Number + `"`
	if body := serveUI(t, buildUISite(t, uiSiteFiles), "http://localhost/").Body.String(); !strings.Contains(body, want) {
		t.Errorf("a .ui page is missing %s\n%.400s", want, body)
	}
	body := serveUI(t, buildUISiteMode(t, uiSiteFiles, false), "http://localhost/").Body.String()
	// the value holds a space, so minifying must keep its quotes: content=Kitwork 0.9.0 would be broken
	if !strings.Contains(body, `content="Kitwork `+version.Number+`"`) || !strings.Contains(strings.ToLower(body), "generator") {
		t.Errorf("a minified .ui page lost its generator, or its quotes\n%.400s", body)
	}
}

// router.colors: a { light, dark } pair gives the site a dark look (the dark value re-values the same
// token, the anti-flash pre-paint runs), and colours declared in a branch re-value tokens for that
// branch only. Control: the site's own pages keep the site's brand.
func TestUISiteColorsDarkAndBranch(t *testing.T) {
	files := withRootRouter(t,
		`router.colors({ brand: "#c8742c", canvas: { light: "#f6f3ee", dark: "#141110" } });`,
		map[string]string{
			"localhost/content.ui": `<content background="canvas"><text color="brand+15">{{ count }}</text></content>`,
			"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.colors({ brand: "#2563eb" });`,
			"localhost/quan-ly/content.ui": `<content background="canvas"><text color="brand+15">Quản lý</text></content>`,
		})
	var tenant *Tenant
	printed := captureBootOutput(t, func() { tenant = buildUISite(t, files) })

	home := pageWithStylesheet(t, tenant, "http://localhost/")
	branch := pageWithStylesheet(t, tenant, "http://localhost/quan-ly")
	for _, want := range []string{"--brand:#c8742c", "--canvas:#f6f3ee", darkBlock, "--canvas:#141110", canonicalPrepaint(t)} {
		if !strings.Contains(home, want) {
			t.Errorf("/ is missing %q", want)
		}
	}
	// brand has no dark value of its own: the site's dark derives one
	if block := linesWith(home, darkBlock); !strings.Contains(block, "--brand:") {
		t.Errorf("the dark look must derive the tokens the palette gave no dark value; / dark block:\n%s", block)
	}
	if !strings.Contains(branch, "--brand:#2563eb") {
		t.Errorf("/quan-ly must take its branch's brand:\n%s", linesWith(branch, "--brand"))
	}
	if strings.Contains(home, "#2563eb") {
		t.Error("the branch's brand leaked into the site's own page")
	}
	if !strings.Contains(branch, "--canvas:#f6f3ee") || !strings.Contains(branch, "--canvas:#141110") {
		t.Error("a branch keeps every token it did not re-value, light and dark")
	}
	if strings.Contains(printed, "router.theme()") {
		t.Errorf("a site written with router.colors gets no deprecation line:\n%s", printed)
	}
}

// router.colors and router.fonts add up: at the site root in either order and over several calls, and
// in a branch over the site's, the nearest folder winning per token and per role. A branch may name a
// typeface role the site does not: its own pages then use it, the site's pages keep the engine's
// default (Google Sans Code for code).
func TestUISiteColorsAndFontsMergeDownTheFolders(t *testing.T) {
	const content = `<content><text color="brand+15">a</text><text color="chart.muted">b</text><text font="code">29.000đ</text></content>`
	files := withRootRouter(t,
		`router.fonts({ body: "Outfit" });
router.colors({ brand: "#c8742c" });
router.colors({ "chart.muted": "#6b5d52" });`,
		map[string]string{
			"localhost/content.ui": content,
			"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.fonts({ code: "Fira Code" });
router.colors({ brand: "#2563eb" });`,
			"localhost/quan-ly/content.ui": content,
			"localhost/tinh/router.work": `import { router } from "kitwork";
router.fonts({ body: "Inter" });`,
			"localhost/tinh/content.ui": content,
		})
	// the shared router.work also calls router.fonts({ body: "Outfit" }) after the colours: drop it, so
	// the root's only typeface call is the one written before its colours
	files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], "\nrouter.fonts({ body: \"Outfit\" });\nrouter.meta(", "\nrouter.meta(", 1)
	if strings.Count(files["localhost/router.work"], "router.fonts(") != 1 {
		t.Fatalf("the root must call router.fonts once, before router.colors:\n%s", files["localhost/router.work"])
	}
	tenant := buildUISite(t, files)

	home := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{"--brand:#c8742c", "--chart-muted:#6b5d52", "--font-body:Outfit", `<p class="font-code">29.000đ</p>`,
		"--font-code:'Google Sans Code', ui-monospace"} { // a role the site does not name is the engine's default
		if !strings.Contains(home, want) {
			t.Errorf("/ must have both colour calls and the typeface written before them; missing %q", want)
		}
	}
	if strings.Contains(home, "Fira Code") {
		t.Error("the branch's code typeface leaked into the site's own page")
	}

	branch := pageWithStylesheet(t, tenant, "http://localhost/quan-ly")
	for _, want := range []string{"--brand:#2563eb", "--chart-muted:#6b5d52", "--font-body:Outfit", "--font-code:'Fira Code'", "@font-face{font-family:'Fira Code'", `<p class="font-code">29.000đ</p>`} {
		if !strings.Contains(branch, want) {
			t.Errorf("/quan-ly must merge its colours and typefaces over the site's; missing %q", want)
		}
	}
	if strings.Contains(branch, "--brand:#c8742c") {
		t.Error("/quan-ly must take its own brand, not the site's")
	}

	other := pageWithStylesheet(t, tenant, "http://localhost/tinh")
	if !strings.Contains(other, "--font-body:Inter,") || strings.Contains(other, "--font-body:Outfit") {
		t.Errorf("/tinh writes its own body typeface over the site's; its font roles:\n%s", linesWith(other, "--font-body"))
	}
	if !strings.Contains(other, "--brand:#c8742c") || strings.Contains(other, "#2563eb") {
		t.Error("control: /tinh keeps the site's brand, and a sibling branch's colours never reach it")
	}
}

// A dark pair written only in a branch gives that branch a dark look and its pre-paint; the site's own
// pages, which declared no dark, get neither.
func TestUISiteDarkPairInABranchPrepaintsTheBranch(t *testing.T) {
	files := withFiles(map[string]string{
		"localhost/content.ui": `<content background="canvas"><text color="brand+15">{{ count }}</text></content>`,
		"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.colors({ canvas: { light: "#f6f3ee", dark: "#141110" } });`,
		"localhost/quan-ly/content.ui": `<content background="canvas"><text color="brand+15">Quản lý</text></content>`,
	})
	tenant := buildUISite(t, files)
	prepaint := canonicalPrepaint(t)

	branch := pageWithStylesheet(t, tenant, "http://localhost/quan-ly")
	for _, want := range []string{"--canvas:#f6f3ee", darkBlock, "--canvas:#141110", prepaint} {
		if !strings.Contains(branch, want) {
			t.Errorf("/quan-ly is missing %q", want)
		}
	}
	home := pageWithStylesheet(t, tenant, "http://localhost/")
	if strings.Contains(home, darkBlock) || strings.Contains(home, prepaint) || strings.Contains(home, "#141110") {
		t.Error("control: the site's own page declared no dark, so it has no dark block and no pre-paint")
	}
}

// router.appearance is the mode a page shows before the visitor chooses: inherited down the folders, a
// branch may set its own ("system" included), and a .ui page writes it on <html> only when it is
// light or dark. A route shown dark first gets a dark look derived from its tokens, and its pre-paint,
// even though no colour has a dark value.
func TestUISiteAppearanceIsInheritedPerFolder(t *testing.T) {
	// control first: a site that never calls router.appearance is "system" and writes nothing
	plain := buildUISite(t, uiSiteFiles)
	for _, target := range []string{"http://localhost/", "http://localhost/tinh", "http://localhost/khong-co"} {
		if body := serveUI(t, plain, target).Body.String(); strings.Contains(body, "data-kitwork-appearance") {
			t.Fatalf("GET %s: a site with no router.appearance must write nothing:\n%.300s", target, body)
		}
	}

	files := withFiles(map[string]string{
		"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.appearance("light");`,
		"localhost/quan-ly/content.ui": `<content><text>Quản lý</text></content>`,
		"localhost/tinh/router.work": `import { router } from "kitwork";
router.appearance("system");`,
	})
	files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `router.meta(`, `router.appearance("dark");
router.meta(`, 1)
	tenant := buildUISite(t, files)
	for target, want := range map[string]string{
		"http://localhost/":           `<html lang="vi" data-kitwork-appearance="dark">`,  // the root's own
		"http://localhost/mon/ca-phe": `<html lang="vi" data-kitwork-appearance="dark">`,  // inherited
		"http://localhost/khong-co":   `<html lang="vi" data-kitwork-appearance="dark">`,  // the root's notfound.ui too
		"http://localhost/quan-ly":    `<html lang="vi" data-kitwork-appearance="light">`, // a nearer folder wins
	} {
		if body := serveUI(t, tenant, target).Body.String(); !strings.Contains(body, want) {
			t.Errorf("GET %s is missing %s\n%.300s", target, want, body)
		}
	}
	other := serveUI(t, tenant, "http://localhost/tinh").Body.String()
	if strings.Contains(other, "data-kitwork-appearance") {
		t.Errorf(`router.appearance("system") on /tinh follows the device and must write nothing:\n%.300s`, other)
	}

	// the palette has no dark value: "dark" derives one and pre-paints it; light and system do not
	prepaint := canonicalPrepaint(t)
	home := pageWithStylesheet(t, tenant, "http://localhost/")
	if !strings.Contains(home, darkBlock) {
		t.Errorf("a route shown dark first must have a dark look; its colours:\n%s", linesWith(home, ":root"))
	}
	if !strings.Contains(home, prepaint) {
		t.Errorf("a route shown dark first must have the pre-paint; its scripts:\n%s", linesWith(home, "<script"))
	}
	for _, target := range []string{"http://localhost/quan-ly", "http://localhost/tinh"} {
		if body := pageWithStylesheet(t, tenant, target); strings.Contains(body, darkBlock) || strings.Contains(body, prepaint) {
			t.Errorf("control: GET %s is light or system with no dark declared, so no dark block and no pre-paint", target)
		}
	}
}

// router.appearance takes "system", "light" or "dark", and a boot that meets anything else stops,
// naming the modes there are. The object router.appearance took for a few hours on 11/10/2026 points
// at router.colors and router.fonts.
func TestUISiteAppearanceIsValidated(t *testing.T) {
	for written, want := range map[string]string{
		`router.appearance("blue");`:                           `router.appearance("blue"): the modes are "system", "light" and "dark"`,
		`router.appearance("Dark");`:                           `the modes are "system", "light" and "dark"`,
		`router.appearance({ colors: { brand: "#c8742c" } });`: `colours are router.colors({ … }) and typefaces router.fonts({ … })`,
		`router.appearance();`:                                 `router.appearance expects "system", "light" or "dark"`,
	} {
		files := withFiles(nil)
		files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `router.meta(`, written+"\nrouter.meta(", 1)
		if _, err := bootUISite(t, files, true); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want a boot error naming %q, got %v", written, want, err)
		}
	}
	// control: each valid mode boots
	for _, mode := range []string{"system", "light", "dark"} {
		files := withFiles(nil)
		files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `router.meta(`, `router.appearance("`+mode+`");
router.meta(`, 1)
		if _, err := bootUISite(t, files, true); err != nil {
			t.Errorf("router.appearance(%q) must boot, got %v", mode, err)
		}
	}
}

// router.colors and router.fonts take one object; anything else stops the boot with the shape they
// want, instead of a palette that silently never applied.
func TestRouterColorsAndFontsTakeAnObject(t *testing.T) {
	palette := value.New(map[string]any{"brand": "#c8742c"})
	if !palette.IsMap() {
		t.Fatalf("control: the test's palette is not an object: %v", palette.K)
	}
	for name, call := range map[string]func(...value.Value) (*FolderRouter, error){
		"router.colors expects one object of tokens": (&FolderRouter{}).Colors,
		"router.fonts expects one object of roles":   (&FolderRouter{}).Fonts,
	} {
		for _, args := range [][]value.Value{{value.NewString("#c8742c")}, {}, {palette, palette}} {
			if _, err := call(args...); err == nil || !strings.Contains(err.Error(), name) {
				t.Errorf("%d argument(s) %v: want an error naming %q, got %v", len(args), args, name, err)
			}
		}
		object := palette
		if strings.HasPrefix(name, "router.fonts") {
			object = value.New(map[string]any{"body": "Outfit"}) // roles, not tokens
		}
		if _, err := call(object); err != nil {
			t.Errorf("control: one object is what %q takes, got %v", name, err)
		}
	}
}

// router.theme() is the old name of router.colors() and router.fonts(): its colors and fonts still
// apply, at the site root and in a branch (merged over the site's), and the boot names it once per file
// that calls it — once however many calls the file makes, and never for a file written with
// router.colors (the control branch, tinh/).
func TestUISiteThemeIsTheDeprecatedColorsAndFonts(t *testing.T) {
	files := withRootRouter(t,
		`router.theme({ colors: { brand: "#c8742c", canvas: { light: "#f6f3ee", dark: "#141110" } }, fonts: { code: "Fira Code" } });`,
		map[string]string{
			"localhost/content.ui": `<content background="canvas"><text color="brand+15">{{ count }}</text><text font="code">1</text></content>`,
			"localhost/quan-ly/router.work": `import { router } from "kitwork";
router.theme({ colors: { brand: "#16a34a" } });
router.theme({ colors: { brand: "#2563eb" } });`,
			"localhost/quan-ly/content.ui": `<content background="canvas"><text color="brand+15">Quản lý</text></content>`,
			"localhost/tinh/router.work": `import { router } from "kitwork";
router.colors({ brand: "#9333ea" });`,
			"localhost/tinh/content.ui": `<content background="canvas"><text color="brand+15">Trang tĩnh</text></content>`,
		})
	var tenant *Tenant
	printed := captureBootOutput(t, func() { tenant = buildUISite(t, files) })

	home := pageWithStylesheet(t, tenant, "http://localhost/")
	for _, want := range []string{"--brand:#c8742c", "--canvas:#141110", darkBlock, canonicalPrepaint(t), "--font-code:'Fira Code'", `<p class="font-code">1</p>`, "--font-body:Outfit"} {
		if !strings.Contains(home, want) {
			t.Errorf("router.theme() at the site root must apply its colors and fonts as router.colors() and router.fonts() do; / is missing %q", want)
		}
	}
	branch := pageWithStylesheet(t, tenant, "http://localhost/quan-ly")
	if !strings.Contains(branch, "--brand:#2563eb") || strings.Contains(branch, "--brand:#16a34a") {
		t.Errorf("router.theme() in a branch must re-value its brand (the later call wins):\n%s", linesWith(branch, "--brand"))
	}
	if !strings.Contains(branch, "--canvas:#f6f3ee") || !strings.Contains(branch, "--canvas:#141110") {
		t.Error("router.theme() in a branch merges over the site's: the canvas it did not write stays, light and dark")
	}
	if control := pageWithStylesheet(t, tenant, "http://localhost/tinh"); !strings.Contains(control, "--brand:#9333ea") {
		t.Error("control: the branch written with router.colors must take its own brand")
	}

	const said = "router.theme() is now router.colors() and router.fonts() — applied"
	for _, file := range []string{"router.work", filepath.Join("quan-ly", "router.work")} {
		line := "[router] " + file + ": " + said + "\n"
		if got := strings.Count(printed, line); got != 1 {
			t.Errorf("want the line %q exactly once at boot, got %d:\n%s", strings.TrimSpace(line), got, printed)
		}
	}
	if got := strings.Count(printed, said); got != 2 {
		t.Errorf("two files call router.theme(), so two lines, got %d (the router.colors branch must say nothing):\n%s", got, printed)
	}
}

// darkBlock opens the dark tokens of a colours file: screens only (print stays light), on the
// data-theme the pre-paint writes.
const darkBlock = "@media screen{:root[data-theme=dark]{color-scheme:dark;"

// linesWith lists the lines of a page that hold needle, each cut short: what a failure shows.
func linesWith(body, needle string) string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, needle) {
			if len(line) > 240 {
				line = line[:240] + "…"
			}
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

// canonicalPrepaint is the engine's anti-flash script exactly as jit/theme writes it, its marker
// included: a page that has it whole has the pre-paint, whatever the marker is spelled.
func canonicalPrepaint(t *testing.T) string {
	t.Helper()
	head := jittheme.Force("<head></head>")
	script := strings.TrimSuffix(strings.TrimPrefix(head, "<head>"), "</head>")
	if !strings.HasPrefix(script, "<script ") || !strings.HasSuffix(script, "</script>") {
		t.Fatalf("jit/theme no longer writes one script into <head>: %q", head)
	}
	return script
}

// captureBootOutput runs fn with the process's standard output captured: the boot prints its router
// lines there. Read while fn runs, so a long boot never blocks on a full pipe.
func captureBootOutput(t *testing.T, fn func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	var printed bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&printed, read)
		close(done)
	}()
	saved := os.Stdout
	os.Stdout = write
	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		os.Stdout = saved
		_ = write.Close()
		<-done
		_ = read.Close()
	}
	defer restore() // also when fn stops the test
	fn()
	restore()
	return printed.String()
}

// An accent in router.colors grows one rung from its hue, on, the label written on its solid fill
// (13/10/2026): lighter and stronger are operators the page writes, never tokens, so the colours carry
// no deep, soft or wash. A badge is the accent 85% lighter under its text, an engine colour stronger by
// the smallest step of 5 that reads 4.5:1 on that fill in each mode (13/10/2026: from 15 in light, from 0
// in dark), each a value of its own in light and in dark (the custom property --kitwork-badge-X of the
// page), so the pill changes with the light/dark look instead of staying a light pastel on a dark page,
// and its text keeps the accent's hue in dark. An on the palette writes is kept; surfaces and text grow
// nothing.
func TestUISiteAccentsGrowOnlyTheirLabel(t *testing.T) {
	files := withRootRouter(t,
		`router.colors({ brand: "#c8742c", "brand.on": "#ffeedd", warning: "#c98a1e", canvas: { light: "#f6f3ee", dark: "#141110" }, "ink.muted": "#6b5d52" });`,
		map[string]string{
			"localhost/content.ui": `<content><badge color="warning"><text>Đang pha</text></badge><badge color="brand"><text>Chờ lấy</text></badge></content>`,
		})
	tenant := buildUISite(t, files)
	// warning+60 in light, the smallest step of 5 from 15 at which warning reads 4.5:1 on warning-85 over
	// this canvas, and warning itself in dark, the smallest from 0 (stylesheet engine_colour.go)
	const light, night = "#8a5b00", "#daa965"

	home := pageWithStylesheet(t, tenant, "http://localhost/")
	at := strings.Index(home, darkBlock)
	if at < 0 {
		t.Fatalf("/ has no dark look:\n%s", linesWith(home, ":root"))
	}
	dark := home[at:]
	dark = dark[:strings.Index(dark, "}")]
	for _, want := range []string{
		".color-warning:where(.badge){--kitwork-tint:color-mix(in oklab,var(--warning) 15%,var(--canvas));color:var(--kitwork-badge-warning)}",
		"--brand-on:#ffeedd", "--warning-on:",
	} {
		if !strings.Contains(home, want) {
			t.Errorf("/ is missing %q", want)
		}
	}
	if !strings.Contains(dark, "--warning-on:") {
		t.Errorf("the dark block has no grown --warning-on: %s", dark)
	}
	// the badge's text is a custom property of the page with a value in each mode
	values := regexp.MustCompile(`--kitwork-badge-warning:(#[0-9a-f]{6})`).FindAllStringSubmatch(home, -1)
	if len(values) != 2 || values[0][1] != light || values[1][1] != night {
		t.Errorf("--kitwork-badge-warning wants %s in light and %s in dark, got %v:\n%s", light, night, values, linesWith(home, "--kitwork-badge-"))
	}
	if strings.Contains(home, "--kitwork-strong-") {
		t.Errorf("a badge's text is the engine's colour, not an author's stronger one:\n%s", linesWith(home, "--kitwork-strong-"))
	}
	for _, gone := range []string{"--warning-soft", "--warning-deep", "--warning-wash", "--brand-soft", "--brand-deep", "--brand-wash", "--ink-muted-on", "--canvas-on"} {
		if strings.Contains(home, gone) {
			t.Errorf("/ carries %s: an accent grows only on, surfaces and text grow nothing:\n%s", gone, linesWith(home, gone))
		}
	}
}

// router.favicon: a path starting with / is the site's URL path. The root's file answers /favicon.ico;
// a branch's favicon is only what its pages link, never the site's /favicon.ico. Each .ui page links
// the icon of the nearest folder that declares one.
func TestUISiteFavicon(t *testing.T) {
	files := map[string]string{}
	for name, source := range uiSiteFiles {
		files[name] = source
	}
	files["localhost/router.work"] = strings.Replace(files["localhost/router.work"], `router.meta(`, `router.favicon("/assets/favicon.svg");
router.meta(`, 1)
	files["localhost/assets/favicon.svg"] = `<svg>gốc</svg>`
	files["localhost/assets/favicon-quan-ly.svg"] = `<svg>quản lý</svg>`
	files["localhost/quan-ly/router.work"] = `import { router } from "kitwork";
router.favicon("/assets/favicon-quan-ly.svg");`
	files["localhost/quan-ly/content.ui"] = `<content><text>Quản lý</text></content>`
	tenant := buildUISite(t, files)

	if rec := serveUI(t, tenant, "http://localhost/favicon.ico"); rec.Code != http.StatusOK || rec.Body.String() != `<svg>gốc</svg>` {
		t.Errorf("/favicon.ico = %d %q, want the site root's icon", rec.Code, rec.Body.String())
	}
	if home := serveUI(t, tenant, "http://localhost/").Body.String(); !strings.Contains(home, `<link rel="icon" href="/assets/favicon.svg" type="image/svg+xml">`) {
		t.Errorf("/ does not link the site's icon:\n%.900s", home)
	}
	if branch := serveUI(t, tenant, "http://localhost/quan-ly").Body.String(); !strings.Contains(branch, `<link rel="icon" href="/assets/favicon-quan-ly.svg" type="image/svg+xml">`) {
		t.Errorf("/quan-ly does not link its branch's icon:\n%.900s", branch)
	}
}
