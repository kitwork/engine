package work

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

// driveFocusFiles is a site of two pages of one look under the default router.bundle ("auto"): / is
// small, so its site part (reboot, colours, fonts) is a block in its head; /dai writes the same
// elements 1500 times, past the first window, so its site part is site.<hash>.css. Both carry the same
// page block, byte for byte, and Drive keeps it across a move. The page's backdrop (cirrus) asks the
// keyboard focus ring for ink: the page block writes :root{--kitwork-focus:var(--ink)}, unlayered,
// after the colours' own :root{--kitwork-focus:var(--brand)}, unlayered too, with the same specificity
// — so the ring is ink only while the site part stands before the page block.
var driveFocusFiles = func() map[string]string {
	var long strings.Builder
	for i := 0; i < 1500; i++ {
		sum := sha256.Sum256([]byte{byte(i), byte(i >> 8), 'f'})
		long.WriteString("\n  <text>" + hex.EncodeToString(sum[:12]) + "</text>")
	}
	return map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#f5225f", canvas: "#f6f9fc", paper: "#ffffff", line: "#e6ebf1", ink: "#242442" });
router.fonts({ body: "Outfit" });
router.reload(false);
`,
		"localhost/page.ui": `<page background="cirrus-70" color="ink"><content /></page>`,
		"localhost/content.ui": `<content gap="4">
  <text id="title">Một</text>
  <link id="next" value="/dai">Dài</link>
</content>`,
		"localhost/dai/content.ui": `<content gap="4">
  <text id="title">Dài</text>` + long.String() + `
  <link id="next" value="/">Một</link>
</content>`,
	}
}()

// driveFocusController walks / → /dai → / → /dai by the links (Drive moves) and, on the first load
// and at each arrival, reads the focus ring's colour (--kitwork-focus on :root, as the browser computes
// it) and the order of the engine's stylesheets ("file:" a link).
const driveFocusController = `<script>
(function () {
  var log = {}, arrivals = ["dai", "back", "again"];
  function order() {
    return Array.prototype.map.call(document.head.querySelectorAll('style[data-kitwork-generated],link[rel="stylesheet"][data-kitwork-generated]'), function (n) {
      return (n.nodeName === "LINK" ? "file:" : "") + n.getAttribute("data-kitwork-generated");
    }).filter(function (role) { return role !== "transition"; }).join(",");
  }
  function read() { return "focus=" + getComputedStyle(document.documentElement).getPropertyValue("--kitwork-focus").trim() + " order=" + order(); }
  function send() {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(log) }).then(close, close);
  }
  function move() { setTimeout(function () { document.getElementById("next").click(); }, 50); }
  document.addEventListener("kitwork:load", function () {
    var at = arrivals.shift();
    if (!at) return;
    log[at] = read();
    if (arrivals.length) move(); else send();
  });
  window.addEventListener("load", function () { log.start = read(); move(); });
})();
</script>`

// TestBrowserDriveKeepsTheFocusRingOfAPageWhoseSitePartBecomesAFile walks two pages of one look whose
// site part is a block on one and a file on the other (router.bundle "auto" by size) and checks every
// arrival has the cascade of a whole load: the site part before the page block, so the page's ring
// (ink, #242442) wins over the palette's (brand, #f5225f). Until the fix of 14/10/2026 Drive put a new file at
// the end of <head> when the page on screen linked none, and a new block at the end of it, after the
// page block it kept: the ring went back to brand on /dai and stayed brand. CONTROL: the first load is
// a whole load; the pages are the walk's (the same page block, the site part inline on /, a file on
// /dai).
func TestBrowserDriveKeepsTheFocusRingOfAPageWhoseSitePartBecomesAFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the Drive focus ring browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, driveFocusFiles)
	blocks := map[string]string{}
	for _, page := range []string{"/", "/dai"} {
		head := headOf(serveUI(t, tenant, "http://localhost"+page).Body.String())
		if inline := strings.Contains(head, `<style data-kitwork-generated="site"`); inline != (page == "/") || siteStylesheetRe.MatchString(head) != (page == "/dai") {
			t.Fatalf("the site is not the walk's: %s carries its site part inline: %v\n%.600s", page, inline, head)
		}
		for _, block := range inlineStylesheetRe.FindAllStringSubmatch(head, -1) {
			if block[1] == "css" {
				blocks[page] = block[2]
			}
		}
	}
	if blocks["/"] == "" || blocks["/"] != blocks["/dai"] || !strings.Contains(blocks["/"], ":root{--kitwork-focus:var(--ink)}") {
		t.Fatalf("the site is not the walk's: both pages carry the same page block, which sets the ring to ink:\n%.400s\n%.400s", blocks["/"], blocks["/dai"])
	}
	reports := make(chan map[string]string, 1)
	server, hits, moved, lock := driveBundleServer(t, tenant, driveFocusController, reports)
	got := runBrowser(t, browser, server.URL+"/", "drive focus ring", 1000, "", reports)

	inline, linked := "focus=#242442 order=site,css", "focus=#242442 order=file:site,css"
	want := map[string]string{"start": inline, "dai": linked, "back": inline, "again": linked}
	for _, step := range []string{"start", "dai", "back", "again"} {
		if got[step] != want[step] {
			t.Errorf("%s:\n got  %s\n want %s (a whole load's)", step, got[step], want[step])
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if moved["/dai"] == "" {
		t.Errorf("/dai was loaded whole, not moved to by Drive (requests %v)", hits)
	}
}
