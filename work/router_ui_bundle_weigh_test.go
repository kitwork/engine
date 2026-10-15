package work

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
)

// The font preloads stand right after the pre-paint, before any stylesheet (fixed 14/10/2026): under
// router.bundle "auto" a page's CSS is in its head, and a page whose own block is large (the colours
// page of the kitwork example: 109 KB) put the preloads, written last until then, past the first window
// (initialWindowBytes), so the browser asked for the faces a round trip later than under
// router.bundle(false), where they followed five short links. Every way of delivering writes them
// first, on a small page and on one past the window. CONTROL: every page here preloads a face and
// carries CSS.
func TestFontPreloadsComeBeforeTheCSS(t *testing.T) {
	var long strings.Builder
	long.WriteString("<content>")
	for i := 0; i < 1500; i++ {
		sum := sha256.Sum256([]byte{byte(i), byte(i >> 8), 'p'})
		long.WriteString("<text>" + hex.EncodeToString(sum[:12]) + "</text>")
	}
	long.WriteString("</content>")
	changes := map[string]string{"localhost/dai/content.ui": long.String()}
	preload := regexp.MustCompile(`<link rel="?preload"? as="?font"?`)
	stylesheet := regexp.MustCompile(`<style data-kitwork-generated|<link rel="?stylesheet"?`)
	for _, written := range []string{"", `"inline"`, `"split"`, "false"} {
		tenant := bundleSite(t, written, changes)
		for _, page := range []string{"/", "/dai"} {
			head := headOf(serveUI(t, tenant, "http://localhost"+page).Body.String())
			face, css := preload.FindStringIndex(head), stylesheet.FindStringIndex(head)
			if face == nil || css == nil {
				t.Fatalf("bundle %s %s: CONTROL: the page preloads a face (%v) and carries CSS (%v):\n%.800s", written, page, face, css, head)
			}
			if face[0] > css[0] {
				t.Errorf("bundle %s %s: the font preload at %d stands after the first stylesheet at %d; it goes first:\n%.600s", written, page, face[0], css[0], head[css[0]:])
			}
			if prepaint := strings.Index(head, `data-kitwork-generated="appearance"`); prepaint > face[0] {
				t.Errorf("bundle %s %s: the pre-paint (%d) still goes before everything (preload at %d)", written, page, prepaint, face[0])
			}
		}
	}
}

// weighSite is a site whose root writes everything a response of the page carries besides its data:
// router.meta's title and description (description is given), router.favicon, router.appearance and
// router.transition. Its one page has no data.
func weighSite(description string) map[string]string {
	return map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.fonts({ body: "Outfit" });
router.meta({ site: "Quán", title: "Thực đơn của quán, mỗi ngày một món mới", description: "` + description + `" });
router.favicon("/assets/icon.svg");
router.appearance("dark");
router.reload(false).transition("fade");
`,
		"localhost/page.ui":    `<page><content /></page>`,
		"localhost/content.ui": `<content gap="4"><text heading="1">Thực đơn</text><text color="ink-30">Cà phê, trà, bánh.</text></content>`,
	}
}

// compiledHome is the compiled page of a site's root route.
func compiledHome(t *testing.T, tenant *Tenant) (*uiSite, *uiCompiled) {
	t.Helper()
	plan := tenant.renderPlan()
	if plan == nil || plan.ui == nil {
		t.Fatal("the site has no Kitwork UI pages")
	}
	compiled, err := plan.ui.page("", false)
	if err != nil || compiled.page == nil {
		t.Fatalf("the home page: %v", err)
	}
	return plan.ui, compiled
}

// "auto" weighs the document that travels (fixed 14/10/2026): the page respondUI writes for a request
// without data — its title and description from router.meta, its favicon, its appearance and its
// transition on <html> —, not the page with only its path, head and language, which was a few hundred
// bytes gzipped lighter. In production, for a page with no data, the document "auto" weighed is the
// response byte for byte. Then a description long enough to fill the first window by itself makes the
// page link its site part. CONTROL: the same page with a short description carries it inline.
func TestRouterBundleAutoWeighsTheDocumentThatTravels(t *testing.T) {
	tenant := buildUISiteMode(t, weighSite("Cà phê rang xay mỗi sáng, trà ủ lạnh và bánh nướng tại quán."), false)
	served := serveUI(t, tenant, "http://localhost/").Body.String()
	for _, part := range []string{`<title>Thực đơn của quán`, `Cà phê rang xay mỗi sáng`, `/assets/icon.svg`, `data-kitwork-appearance`, `data-kitwork-transition`} {
		if !strings.Contains(served, part) {
			t.Fatalf("CONTROL: the response carries %q:\n%.1200s", part, served)
		}
	}
	if !strings.Contains(served, "<style data-kitwork-generated=site>") && !strings.Contains(served, `<style data-kitwork-generated="site">`) {
		t.Fatalf("CONTROL: the small page carries its site part inline:\n%.1200s", served)
	}
	u, compiled := compiledHome(t, tenant)
	if weighed := u.weighedDocument(compiled, compiled.head); weighed != served {
		at := 0
		for at < len(weighed) && at < len(served) && weighed[at] == served[at] {
			at++
		}
		t.Errorf("the document \"auto\" weighs is the response, byte for byte; they part at byte %d:\n weighed %.160q\n served  %.160q",
			at, weighed[max(0, at-40):], served[max(0, at-40):])
	}

	// a description of 30 000 different hex digits fills the window by itself
	var long strings.Builder
	for i := 0; long.Len() < 30000; i++ {
		sum := sha256.Sum256([]byte{byte(i), byte(i >> 8), 'd'})
		long.WriteString(hex.EncodeToString(sum[:]))
	}
	heavy := buildUISiteMode(t, weighSite(long.String()), false)
	if head := headOf(serveUI(t, heavy, "http://localhost/").Body.String()); !siteStylesheetRe.MatchString(head) {
		t.Errorf("a page whose description alone is past the first window links its site part:\n%.600s", head)
	}
}

// While developing, "auto" decides on production's bytes (fixed 14/10/2026): the warnings file a page with
// warnings links only while developing is not weighed, so with MINIFY=true (production's head) a page
// at the edge of the window makes the decision production makes, both sides of the edge. The edge is
// found in production by the length of one line of different hex digits; the page has a warning (a
// colour short of contrast). CONTROL: the page carries its warnings file while developing; production
// links the site part one digit past the edge.
func TestRouterBundleAutoDecidesOnProductionsBytesWhileDeveloping(t *testing.T) {
	saved := Minify
	t.Cleanup(func() { Minify = saved })
	var digits strings.Builder
	for i := 0; digits.Len() < 40000; i++ {
		sum := sha256.Sum256([]byte{byte(i), byte(i >> 8), 'e'})
		digits.WriteString(hex.EncodeToString(sum[:]))
	}
	page := func(length int) map[string]string {
		return map[string]string{
			"localhost/router.work": "import { router } from \"kitwork\";\n\nrouter.colors({ brand: \"#c8742c\" });\n",
			"localhost/page.ui":     `<page><content /></page>`,
			"localhost/content.ui":  `<content><text color="ink-70">Chữ nhạt</text><text>` + digits.String()[:length] + `</text></content>`,
		}
	}
	type answer struct {
		inline   bool
		warnings bool
	}
	decide := func(length int, developing bool) answer {
		Minify = developing // production minifies anyway; while developing, MINIFY=true writes its head
		body := serveUI(t, buildUISiteMode(t, page(length), developing), "http://localhost/").Body.String()
		return answer{
			inline:   strings.Contains(body, "<style data-kitwork-generated=site>") || strings.Contains(body, `<style data-kitwork-generated="site">`),
			warnings: strings.Contains(body, "/_kitwork/warnings."),
		}
	}
	low, high := 0, 40000
	if !decide(low, false).inline || decide(high, false).inline {
		t.Fatalf("CONTROL: production carries the short page's site part inline and links the long one's")
	}
	for high-low > 1 {
		middle := (low + high) / 2
		if decide(middle, false).inline {
			low = middle
		} else {
			high = middle
		}
	}
	for _, length := range []int{low, high} {
		developing := decide(length, true)
		if !developing.warnings {
			t.Fatalf("CONTROL: while developing the page links its warnings file")
		}
		if want := length == low; developing.inline != want {
			t.Errorf("a line of %d digits (the edge is %d): production carries its site part inline: %v; while developing with MINIFY=true: %v",
				length, low, want, developing.inline)
		}
	}
}
