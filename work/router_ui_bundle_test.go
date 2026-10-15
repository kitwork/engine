package work

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"
	"testing"

	jitjs "github.com/kitwork/engine/jit/js"
	"github.com/kitwork/engine/ui/stylesheet"
)

// router.bundle (14/10/2026) decides how a .ui page delivers its CSS and its JavaScript. These tests
// read the head each value writes; the browser walks are ui_drive_bundle_browser_test.go.

// bundleSite is the shared site with router.bundle(written) at its root ("" writes none: the default).
func bundleSite(t *testing.T, written string, changes map[string]string) *Tenant {
	t.Helper()
	line := sitePalette
	if written != "" {
		line += "\nrouter.bundle(" + written + ");"
	}
	return buildUISite(t, withRootRouter(t, line, changes))
}

// headOf is the <head> of a page, the text of its scripts but a part's first line left out: a part
// written in the page (router.bundle javascript "inline") is a file's JavaScript, whose comments may
// spell a link; its first line is the guard runOnce writes, which says which part it is.
func headOf(page string) string {
	start, end := strings.Index(page, "<head>"), strings.Index(page, "</head>")
	if start < 0 || end < start {
		return ""
	}
	return scriptTextRe.ReplaceAllString(page[start:end], "<script$1>$2</script>")
}

// scriptTextRe is a script of a head: its attributes, the guard line of a part, the rest.
var scriptTextRe = regexp.MustCompile(`(?s)<script([^>]*)>(;\(function\(parts\)\{[^\n]*\n)?.*?</script>`)

// joinedSite is the site part as one stylesheet: the reboot, colors and fonts files in their order, a
// line break between two where the first does not end with one (joinCSS).
func joinedSite(reboot, colors, fonts string) string {
	out := ""
	for _, css := range []string{reboot, colors, fonts} {
		if out != "" && !strings.HasSuffix(out, "\n") {
			out += "\n"
		}
		out += css
	}
	return out
}

var (
	anyStylesheetLinkRe = regexp.MustCompile(`<link rel="?stylesheet"?[^>]*>`)
	scriptFileRe        = regexp.MustCompile(`<script data-kitwork-generated="?([a-z]+)"? src="?(/_kitwork/[^" >]+\.js)"?[^>]*></script>`)
	inlinePartRe        = regexp.MustCompile(`(?s)<script type="module" data-kitwork-generated="([a-z]+)" data-kitwork-part="([^"]+)">(.*?)</script>`)
)

// filesSiteCSS is the site part of the shared site's home as router.bundle(false) links it: reboot,
// colors and fonts, each file fetched.
func filesSiteCSS(t *testing.T) (reboot, colors, fonts string) {
	t.Helper()
	tenant := bundleSite(t, "false", nil)
	byRole := sheetsByRole(stylesheetsOf(t, tenant, serveUI(t, tenant, "http://localhost/").Body.String()))
	return byRole["reboot"].css, byRole["colors"].css, byRole["fonts"].css
}

// The default is "auto": a page that fits the first window (the shared site's home) carries all its
// CSS in its head, the site part (reboot, colours, fonts) in one block and the page's own in another,
// and links no stylesheet: no request stands between the HTML and the first paint. The site block is
// the three files router.bundle(false) links, joined in their order, byte for byte. JavaScript stays
// one deferred file per part, as before. CONTROL: router.bundle(false) links the files and carries no
// block; writing "auto" is the default.
func TestRouterBundleDefaultsToAutoAndInlinesASmallPage(t *testing.T) {
	reboot, colors, fonts := filesSiteCSS(t)
	for _, written := range []string{"", `"auto"`} {
		tenant := bundleSite(t, written, nil)
		page := serveUI(t, tenant, "http://localhost/").Body.String()
		head := headOf(page)
		if links := anyStylesheetLinkRe.FindAllString(head, -1); len(links) != 0 {
			t.Errorf("bundle %s: a page that fits links no stylesheet, got %v", written, links)
		}
		blocks := map[string]string{}
		var order []string
		for _, block := range inlineStylesheetRe.FindAllStringSubmatch(head, -1) {
			blocks[block[1]] = block[2]
			order = append(order, block[1])
		}
		if strings.Join(order, ",") != "site,css" {
			t.Errorf("bundle %s: the blocks in the cascade's order, site then the page's own: %v", written, order)
		}
		if blocks["site"] != joinedSite(reboot, colors, fonts) {
			t.Errorf("bundle %s: the site block is reboot, colors and fonts as their files are:\n%.300s", written, blocks["site"])
		}
		if !strings.Contains(blocks["css"], ".color-ink-30{") {
			t.Errorf("bundle %s: the page's own block has its rules:\n%s", written, blocks["css"])
		}
		// JavaScript as before: one deferred file per part, the kernel first
		var roles []string
		for _, script := range scriptFileRe.FindAllStringSubmatch(head, -1) {
			roles = append(roles, script[1])
		}
		if strings.Join(roles, ",") != "runtime,drive,component,component" || inlinePartRe.MatchString(head) {
			t.Errorf("bundle %s: JavaScript stays one file per part: %v", written, roles)
		}
		// the appearance pre-paint stands before the CSS, which stands before the scripts
		if prepaint, site, kernel := strings.Index(head, `data-kitwork-generated="appearance"`), strings.Index(head, `<style data-kitwork-generated="site"`), strings.Index(head, `data-kitwork-generated="runtime"`); !(site < kernel) || (prepaint >= 0 && prepaint > site) {
			t.Errorf("bundle %s: pre-paint (%d), CSS (%d), scripts (%d) in that order", written, prepaint, site, kernel)
		}
	}

	// CONTROL: router.bundle(false) links each role and writes no block
	tenant := bundleSite(t, "false", nil)
	head := headOf(serveUI(t, tenant, "http://localhost/").Body.String())
	if blocks := inlineStylesheetRe.FindAllString(head, -1); len(blocks) != 0 {
		t.Errorf("router.bundle(false) writes no CSS in the page, got %d blocks", len(blocks))
	}
	var roles []string
	for _, link := range pageStylesheetRe.FindAllStringSubmatch(head, -1) {
		roles = append(roles, link[2])
	}
	if strings.Join(roles, ",") != "reboot,colors,fonts,page" {
		t.Errorf("router.bundle(false) links one file per role, got %v", roles)
	}
}

// "auto" weighs the page with all its CSS inline, gzipped, against initialWindowBytes: a page past it
// links its site part as one file, site.<hash>.css, named by its bytes and kept for a year, and keeps
// its own part inline. CONTROL: the small page of the same site carries its site part inline; and the
// same large page under router.bundle("inline") carries everything.
func TestRouterBundleAutoLinksTheSitePartOfAPagePastTheFirstWindow(t *testing.T) {
	// a page of 1500 different lines: no compression folds it under 14 KB
	var long strings.Builder
	long.WriteString("<content>")
	for i := 0; i < 1500; i++ {
		sum := sha256.Sum256([]byte{byte(i), byte(i >> 8)})
		long.WriteString("<text>" + hex.EncodeToString(sum[:12]) + "</text>")
	}
	long.WriteString("</content>")
	changes := map[string]string{"localhost/dai/content.ui": long.String()}
	tenant := bundleSite(t, "", changes)

	head := headOf(serveUI(t, tenant, "http://localhost/dai").Body.String())
	site := siteStylesheetRe.FindStringSubmatch(head)
	if site == nil {
		t.Fatalf("a page past the first window links its site part:\n%.1500s", head)
	}
	answer := serveUI(t, tenant, "http://localhost"+site[1])
	sum := sha256.Sum256(answer.Body.Bytes())
	if answer.Code != http.StatusOK || answer.Header().Get("Content-Type") != "text/css; charset=utf-8" ||
		answer.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || hex.EncodeToString(sum[:])[:16] != site[3] {
		t.Errorf("%s = %d %q %q, hash of its bytes %s: the site file is the engine's, named by its bytes, kept a year",
			site[1], answer.Code, answer.Header().Get("Content-Type"), answer.Header().Get("Cache-Control"), hex.EncodeToString(sum[:])[:16])
	}
	reboot, colors, fonts := filesSiteCSS(t)
	if body := answer.Body.String(); body != joinedSite(reboot, colors, fonts) {
		t.Errorf("the site file is reboot, colors and fonts joined:\n%.300s", body)
	}
	if strings.Contains(head, `<style data-kitwork-generated="site"`) || !strings.Contains(head, `<style data-kitwork-generated="css"`) {
		t.Errorf("past the first window the site part is the file and the page's own stays inline:\n%.800s", head)
	}
	if links := anyStylesheetLinkRe.FindAllString(head, -1); len(links) != 1 {
		t.Errorf("one render-blocking stylesheet, got %v", links)
	}
	// CONTROL: the home of the same site fits
	if home := headOf(serveUI(t, tenant, "http://localhost/").Body.String()); siteStylesheetRe.MatchString(home) || !strings.Contains(home, `<style data-kitwork-generated="site"`) {
		t.Errorf("CONTROL: the small home carries its site part inline:\n%.600s", home)
	}
	// CONTROL: "inline" carries everything, however large
	inline := bundleSite(t, `"inline"`, changes)
	if head := headOf(serveUI(t, inline, "http://localhost/dai").Body.String()); anyStylesheetLinkRe.MatchString(head) || !strings.Contains(head, `<style data-kitwork-generated="site"`) {
		t.Errorf(`router.bundle("inline") links nothing, however large the page:%.600s`, head)
	}
}

// "split" links the site part as one file and writes the page's own in the page whatever the size, and
// makes the JavaScript parts after the kernel (Drive, the components) one deferred bundle.<hash>.js,
// each part inside it the bytes of its own file, run once per page (window.kitworkParts). "inline"
// writes every part of the CSS in the page, and every JavaScript part but the kernel as a deferred
// module script naming the file it stands for. The kernel is always its own file. CONTROL: the parts'
// files are the ones router.bundle(false) links.
func TestRouterBundleSplitAndInlineDeliverTheJavaScriptParts(t *testing.T) {
	files := headOf(serveUI(t, bundleSite(t, "false", nil), "http://localhost/").Body.String())
	parts := map[string]string{} // path → role, after the kernel
	var kernel string
	var order []string
	for _, script := range scriptFileRe.FindAllStringSubmatch(files, -1) {
		if script[1] == "runtime" {
			kernel = script[2]
			continue
		}
		parts[script[2]] = script[1]
		order = append(order, script[2])
	}
	if kernel == "" || len(order) != 3 {
		t.Fatalf("CONTROL: router.bundle(false) links the kernel, Drive and two components:\n%s", files)
	}
	body := func(path string) string {
		asset, _ := jitjs.AssetAt(path, nil)
		if asset == nil {
			t.Fatalf("%s names no engine file", path)
		}
		return string(asset.Body)
	}

	split := bundleSite(t, `"split"`, nil)
	head := headOf(serveUI(t, split, "http://localhost/").Body.String())
	scripts := scriptFileRe.FindAllStringSubmatch(head, -1)
	if len(scripts) != 2 || scripts[0][1] != "runtime" || scripts[0][2] != kernel || scripts[1][1] != "bundle" {
		t.Fatalf(`router.bundle("split"): the kernel's own file, then one bundle, got %v`, scripts)
	}
	answer := serveUI(t, split, "http://localhost"+scripts[1][2])
	bundle := answer.Body.String()
	sum := sha256.Sum256(answer.Body.Bytes())
	if answer.Code != http.StatusOK || answer.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.Contains(scripts[1][2], "/_kitwork/bundle."+hex.EncodeToString(sum[:])[:16]+".js") {
		t.Errorf("%s = %d %q: the bundle is the engine's, named by its bytes, kept a year", scripts[1][2], answer.Code, answer.Header().Get("Cache-Control"))
	}
	at := -1
	for _, path := range order {
		guard := `;(function(parts){if(parts["` + path + `"])return;parts["` + path + `"]=1;` + "\n" + body(path) + "\n})(window.kitworkParts||(window.kitworkParts={}));\n"
		next := strings.Index(bundle, guard)
		if next <= at {
			t.Errorf("the bundle holds %s, run once, after the part before it (at %d, previous %d)", path, next, at)
		}
		at = next
	}
	if strings.Contains(bundle, body(kernel)) {
		t.Error("the kernel is never in a bundle")
	}
	if !siteStylesheetRe.MatchString(head) || !strings.Contains(head, `<style data-kitwork-generated="css"`) {
		t.Errorf(`router.bundle("split"): the site part linked, the page's own inline:%.600s`, head)
	}
	// A bundle name the generation never made (a page from before a deploy) answers the loader of the
	// parts its tag names, each then its own file (staleBundle), not kept (fixed 14/10/2026: until then a
	// 404, and the page lost its components and Drive). CONTROL: a warnings name it never made is still
	// the engine's 404.
	if tag := regexp.MustCompile(`data-kitwork-parts="([^"]+)"`).FindStringSubmatch(head); tag == nil || tag[1] != strings.Join(order, " ") {
		t.Errorf("the bundle's tag names the files of its parts in their order: %v, want %v", tag, order)
	}
	stale := serveUI(t, split, "http://localhost/_kitwork/bundle.0123456789abcdef.js")
	if stale.Code != http.StatusOK || stale.Header().Get("Cache-Control") != "no-cache" || stale.Body.String() != string(staleBundle().body) {
		t.Errorf("an unknown bundle = %d %q: the loader of the parts its tag names, not kept:\n%s", stale.Code, stale.Header().Get("Cache-Control"), stale.Body.String())
	}
	if missing := serveUI(t, split, "http://localhost/_kitwork/warnings.0123456789abcdef.js"); missing.Code != http.StatusNotFound {
		t.Errorf("an unknown warnings file = %d, want 404", missing.Code)
	}

	inline := bundleSite(t, `"inline"`, nil)
	whole := serveUI(t, inline, "http://localhost/").Body.String()
	head = headOf(whole)
	scripts = scriptFileRe.FindAllStringSubmatch(head, -1)
	if len(scripts) != 1 || scripts[0][2] != kernel {
		t.Errorf(`router.bundle("inline"): the kernel is the one file, got %v`, scripts)
	}
	written := inlinePartRe.FindAllStringSubmatch(whole[:strings.Index(whole, "</head>")], -1)
	if len(written) != len(order) {
		t.Fatalf(`router.bundle("inline"): one module script per part, got %d`, len(written))
	}
	for i, part := range written {
		// written so it stays in its element ("</script" and "<!--" escaped), the same text to JavaScript
		text := strings.NewReplacer(`<\/`, `</`, `<\!--`, `<!--`).Replace(part[3])
		if part[2] != order[i] || part[1] != parts[order[i]] || !strings.Contains(text, body(order[i])) {
			t.Errorf("part %d: %s (%s) stands for %s (%s) and holds its bytes", i, part[2], part[1], order[i], parts[order[i]])
		}
	}
	if anyStylesheetLinkRe.MatchString(head) {
		t.Errorf(`router.bundle("inline") links no stylesheet:%.600s`, head)
	}
	if kernelAt, first := strings.Index(head, kernel), strings.Index(head, `<script type="module"`); kernelAt > first {
		t.Errorf("the parts come after the kernel (%d, %d)", kernelAt, first)
	}
}

// Each part of router.bundle is decided by the nearest folder that names it: a string names both, an
// object only what it writes. A branch writes { javascript: "split" } under a root that writes
// "inline": its pages keep the root's inline CSS and bundle their JavaScript; a branch that writes
// false has every part a file; a folder that writes nothing has the root's.
func TestRouterBundleNearestFolderWinsPerPart(t *testing.T) {
	tenant := bundleSite(t, `"inline"`, map[string]string{
		"localhost/tach/router.work":    "import { router } from \"kitwork\";\nrouter.bundle({ javascript: \"split\" });\n",
		"localhost/tach/content.ui":     `<content><text>Tách</text></content>`,
		"localhost/tep/router.work":     "import { router } from \"kitwork\";\nrouter.bundle(false);\n",
		"localhost/tep/content.ui":      `<content><text>Tệp</text></content>`,
		"localhost/tach/con/content.ui": `<content><text>Con</text></content>`,
	})
	read := func(path string) (css, javascript string) {
		head := headOf(serveUI(t, tenant, "http://localhost"+path).Body.String())
		switch {
		case pageStylesheetRe.MatchString(head):
			css = "files"
		case siteStylesheetRe.MatchString(head):
			css = "split"
		case strings.Contains(head, `<style data-kitwork-generated="site"`):
			css = "inline"
		}
		switch {
		case inlinePartRe.MatchString(head):
			javascript = "inline"
		case strings.Contains(head, `data-kitwork-generated="bundle"`):
			javascript = "split"
		default:
			javascript = "files"
		}
		return css, javascript
	}
	for path, want := range map[string]string{
		"/":         "inline inline",
		"/tinh":     "inline inline", // writes nothing: the root's
		"/tach":     "inline split",  // { javascript }: the CSS stays the root's
		"/tach/con": "inline split",  // below it: the nearest that names each part
		"/tep":      "files files",
	} {
		if css, javascript := read(path); css+" "+javascript != want {
			t.Errorf("%s: css %s, javascript %s; want %s", path, css, javascript, want)
		}
	}
}

// A wrong router.bundle stops the boot where it is written (router.work:line:column), in Vietnamese,
// with every value and the nearest one: a word that is none of them suggests "auto", the default, or
// the value it was a typo of; true suggests "auto"; a key of the object that is not css or javascript
// suggests the one it meant. CONTROL: every value boots.
func TestRouterBundleIsValidated(t *testing.T) {
	for written, want := range map[string]string{
		`router.bundle("one");`:                `router.work:6:7: call bundle: router.bundle("one"): không có cách gói "one"; các cách là "auto" (mặc định`,
		`router.bundle(true);`:                 `router.bundle(true): true không phải một cách gói; gói theo cỡ trang là "auto", cũng là mặc định: viết router.bundle("auto")`,
		`router.bundle("inlin");`:              `Có phải ý là router.bundle("inline")?`,
		`router.bundle("Split");`:              `Có phải ý là router.bundle("split")?`,
		`router.bundle({ js: "split" });`:      `router.bundle({ js: … }): không có phần "js"; router.bundle nhận css và javascript. Có phải ý là javascript?`,
		`router.bundle({ styles: "inline" });`: `Có phải ý là css?`,
		`router.bundle({ css: "one" });`:       `router.bundle({ css: "one" }): không có cách gói "one"`,
		`router.bundle({ javascript: true });`: `router.bundle({ javascript: true }): true không phải một cách gói`,
		`router.bundle({});`:                   `router.bundle({}): nêu ít nhất một phần, css hay javascript`,
		`router.bundle(3);`:                    `router.bundle(3): cách gói là một chữ hay false`,
		`router.bundle();`:                     `router.bundle nhận một giá trị: "auto" (mặc định), "inline", "split", false`,
	} {
		files := withRootRouter(t, sitePalette+"\n"+written, nil)
		_, err := bootUISite(t, files, true)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want a boot error with %q, got %v", written, want, err)
		}
		if err != nil && !strings.Contains(err.Error(), "router.work:6:7: call bundle: ") {
			t.Errorf("%s: the error names where it is written, got %v", written, err)
		}
	}
	for _, written := range []string{`"auto"`, `"inline"`, `"split"`, `false`, `{ css: "inline" }`, `{ javascript: "split" }`, `{ css: false, javascript: "inline" }`} {
		files := withRootRouter(t, sitePalette+"\nrouter.bundle("+written+");", nil)
		if _, err := bootUISite(t, files, true); err != nil {
			t.Errorf("router.bundle(%s) boots, got %v", written, err)
		}
	}
}

// Icons only a response's data names (<icon value="{{ … }}" />) are one set with the page's own; a
// page that writes its icons in a <style> writes that set the same way, the page's own icons and the
// data's in one block where the page's own stood. CONTROL: under router.bundle(false) the set is a
// file, linked (TestIconsFromDataAreOneFilePerSet).
func TestRouterBundleIconsFromDataAreInlineToo(t *testing.T) {
	changes := map[string]string{
		"localhost/bieu-tuong/router.work": "import { router } from \"kitwork\";\nrouter.view((ctx) => {\n  const name = \"mug\";\n});\n",
		"localhost/bieu-tuong/content.ui":  `<content><icon value="coffee" alt="" /><icon value="{{ name }}" alt="" /></content>`,
	}
	for _, written := range []string{"", "false"} {
		tenant := bundleSite(t, written, changes)
		head := headOf(serveUI(t, tenant, "http://localhost/bieu-tuong").Body.String())
		var icons string
		blocks := 0
		for _, block := range inlineStylesheetRe.FindAllStringSubmatch(head, -1) {
			if block[1] == "icons" {
				icons = block[2]
				blocks++
			}
		}
		if written == "false" {
			if blocks != 0 || !regexp.MustCompile(`href="/_kitwork/icons\.[0-9a-f]{16}\.css" data-kitwork-generated="icons"`).MatchString(head) {
				t.Errorf("CONTROL: router.bundle(false) links the set as a file:\n%.800s", head)
			}
			continue
		}
		if blocks != 1 || !strings.Contains(icons, ".icon-coffee") || !strings.Contains(icons, ".icon-mug") {
			t.Errorf("the page's own icon and the data's in one block:\n%.800s", head)
		}
		if regexp.MustCompile(`/_kitwork/icons\.`).MatchString(head) {
			t.Errorf("no icons file is linked:\n%.800s", head)
		}
	}
}

// The site block's CSS is the engine's stylesheet bytes: reboot's own bytes come first, as
// stylesheet.Reboot() makes them (the joined file's hash follows them).
func TestRouterBundleSiteBlockStartsWithReboot(t *testing.T) {
	head := headOf(serveUI(t, bundleSite(t, "", nil), "http://localhost/").Body.String())
	block := inlineStylesheetRe.FindStringSubmatch(head)
	if block == nil || block[1] != "site" || !strings.HasPrefix(block[2], string(stylesheet.Reboot().CSS)) {
		t.Errorf("the first block is the site's, reboot first:\n%.400s", head)
	}
}
