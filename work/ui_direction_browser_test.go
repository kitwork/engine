package work

import (
	"strings"
	"testing"
)

// directionFiles is a site in a language given by router.language with a box for each side and corner
// left and right name: the left side, the right one, the top left corner, the top right one, both sides
// on one element and both bottom corners on one element.
func directionFiles(language string) map[string]string {
	return map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.language("` + language + `");
`,
		"localhost/view.ui": `<view><content /></view>`,
		"localhost/content.ui": `<content padding="4" gap="4">
  <box id="left" padding-left="4"><text>a</text></box>
  <box id="right" padding-right="4"><text>b</text></box>
  <box id="corner" radius-top-left="large"><text>c</text></box>
  <box id="other" radius-top-right="large"><text>d</text></box>
  <box id="sides" padding-left="1" padding-right="3"><text>e</text></box>
  <box id="bottom" radius-bottom-left="small" radius-bottom-right="large"><text>f</text></box>
</content>`,
	}
}

// directionProbe reads the direction of the document, the left and right padding of each box and its
// four corners (top left, top right, bottom left, bottom right), as the screen has them.
const directionProbe = `<script>
window.addEventListener("load", function () {
  var report = {};
  try {
    var style = function (id, property) { return getComputedStyle(document.getElementById(id)).getPropertyValue(property).trim(); };
    var sides = function (id) { return style(id, "padding-left") + " " + style(id, "padding-right"); };
    var corners = function (id) {
      return ["top-left", "top-right", "bottom-left", "bottom-right"].map(function (corner) { return style(id, "border-" + corner + "-radius"); }).join(" ");
    };
    report.direction = getComputedStyle(document.documentElement).direction;
    report.left = sides("left");
    report.right = sides("right");
    report.corner = corners("corner");
    report.other = corners("other");
    report.sides = sides("sides");
    report.bottom = corners("bottom");
  } catch (problem) { report.error = String(problem && problem.stack || problem); }
  var close = function () { window.close(); };
  fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
});
</script>`

// The page a router.language("ar") site serves runs right to left in a real browser, and the left and
// right it writes mirror there (Quốc, 19/10/2026: start and end became left and right, and the
// stylesheet writes them as logical CSS), so the left of a line written left to right is the right of
// one written right to left:
//   - the served <html> carries dir="rtl" and the document's direction is rtl;
//   - padding-left is the right side of the screen, padding-right the left;
//   - radius-top-left rounds the top right corner, radius-top-right the top left;
//   - padding-left with padding-right on one element, and radius-bottom-left with radius-bottom-right,
//     each set a side or a corner of their own, mirrored;
//   - nothing is said of left and right while developing: no warning.
//
// CONTROL: the same markup on a router.language("en") site runs left to right: each side and corner is
// where its name says, and nothing is said either, so the measurements tell the two directions apart.
func TestBrowserRightToLeftSiteRunsRightToLeft(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the direction browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	for _, run := range []struct {
		language, html string
		measured       map[string]string
	}{
		{"ar", `<html lang="ar" dir="rtl"`, map[string]string{
			"direction": "rtl",
			"left":      "0px 16px",         // the left of a line right to left is the right of the screen
			"right":     "16px 0px",         // and its right the left
			"corner":    "0px 12px 0px 0px", // top left, mirrored: the top right corner
			"other":     "12px 0px 0px 0px", // top right, mirrored: the top left corner
			"sides":     "12px 4px",         // padding-left="1" on the right, padding-right="3" on the left
			"bottom":    "0px 0px 12px 6px", // bottom-left small on the right, bottom-right large on the left
		}},
		// CONTROL
		{"en", `<html lang="en"`, map[string]string{
			"direction": "ltr",
			"left":      "16px 0px",
			"right":     "0px 16px",
			"corner":    "12px 0px 0px 0px",
			"other":     "0px 12px 0px 0px",
			"sides":     "4px 12px",
			"bottom":    "0px 0px 6px 12px",
		}},
	} {
		var tenant *Tenant
		var page string
		printed := captureBootOutput(t, func() {
			tenant = buildUISite(t, directionFiles(run.language))
			page = serveUI(t, tenant, "http://localhost/").Body.String()
		})
		if !strings.Contains(page, run.html+">") {
			t.Errorf("%s: the served document:\n got  %s\n want %s>", run.language, linesWith(page, "<html"), run.html)
		}
		if lines := warningLines(printed); len(lines) != 0 {
			t.Errorf("%s: nothing is said of left and right, got\n %s", run.language, strings.Join(lines, "\n "))
		}
		server, reports := serveWithProbe(t, tenant, directionProbe)
		got := runBrowser(t, browser, server.URL+"/", "direction, "+run.language, 1000, "", reports)
		if message, broke := got["error"]; broke {
			t.Fatalf("%s: the probe broke: %s", run.language, message)
		}
		for key, want := range run.measured {
			if got[key] != want {
				t.Errorf("%s, %s:\n got  %s\n want %s", run.language, key, got[key], want)
			}
		}
	}
}
