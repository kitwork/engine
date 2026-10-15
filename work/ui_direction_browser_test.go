package work

import (
	"strings"
	"testing"
)

// directionFiles is a site in a language given by router.language with each side and corner the
// warnings of left and right speak of: a physical side and its logical neighbour, a corner and the
// twin the right-to-left warning advises, two pairs on one element.
func directionFiles(language string) map[string]string {
	return map[string]string{
		"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.language("` + language + `");
`,
		"localhost/page.ui": `<page><content /></page>`,
		"localhost/content.ui": `<content padding="4" gap="4">
  <box id="left" padding-left="4"><text>a</text></box>
  <box id="start" padding-start="4"><text>b</text></box>
  <box id="corner" radius-top-left="large"><text>c</text></box>
  <box id="end" radius-top-end="large"><text>d</text></box>
  <box id="twins" padding-left="1" padding-end="3"><text>e</text></box>
  <box id="apart" padding-left="1" padding-start="3"><text>f</text></box>
  <box id="bottom" radius-bottom-left="small" radius-bottom-start="large"><text>g</text></box>
</content>`,
	}
}

// directionProbe reads the direction of the document, the left and right padding of each box and its
// four corners (top left, top right, bottom left, bottom right).
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
    report.start = sides("start");
    report.corner = corners("corner");
    report.end = corners("end");
    report.twins = sides("twins");
    report.apart = sides("apart");
    report.bottom = corners("bottom");
  } catch (problem) { report.error = String(problem && problem.stack || problem); }
  var close = function () { window.close(); };
  fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
});
</script>`

// The page a router.language("ar") site serves runs right to left in a real browser, so what the
// compile warns of left and right is what the page does (review 13/10/2026):
//   - the served <html> carries dir="rtl" and the document's direction is rtl;
//   - padding-start is the right side, padding-left stays on the left;
//   - radius-top-left is told "Viết radius-top-end để giữ đúng chỗ này": radius-top-end rounds that
//     same corner;
//   - padding-left with padding-end is told the twin, padding-end winning: one left side, padding-end's;
//   - padding-left with padding-start, and radius-bottom-left with radius-bottom-start, are no twins
//     there and are not told so: each sets its own side or corner.
//
// CONTROL: the same markup on a router.language("en") site runs left to right: start is the left, the
// twins are padding-left with padding-start and radius-bottom-left with radius-bottom-start, each told
// and measured so, and nothing is said of the right-to-left direction.
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
		told           []string
		measured       map[string]string
	}{
		{"ar", `<html lang="ar" dir="rtl"`, []string{
			`content.ui:2:18: padding-left="4": trang viết từ phải sang trái (router.language "ar") mà left và right không lật theo hướng viết, nên cạnh trái ở đây là phía cuối dòng. Viết padding-end để giữ đúng chỗ này`,
			`content.ui:4:20: radius-top-left="large": trang viết từ phải sang trái (router.language "ar") mà left và right không lật theo hướng viết, nên góc trên bên trái ở đây là phía cuối dòng. Viết radius-top-end để giữ đúng chỗ này`,
			`content.ui:6:19: padding-left="1": trang viết từ phải sang trái`,
			`content.ui:6:19: padding-left="1" và padding-end="3" (content.ui:6:36) cùng là cạnh trái khi dòng viết từ phải sang trái (router.language "ar"): padding-end thắng`,
			`content.ui:7:19: padding-left="1": trang viết từ phải sang trái`,
			`content.ui:8:20: radius-bottom-left="small": trang viết từ phải sang trái`,
		}, map[string]string{
			"direction": "rtl",
			"left":      "16px 0px",
			"start":     "0px 16px",         // the start of a line right to left is its right
			"corner":    "12px 0px 0px 0px", // the left never flips
			"end":       "12px 0px 0px 0px", // the twin the warning advises: the same corner
			"twins":     "12px 0px",         // one left side: padding-end's, as told
			"apart":     "4px 12px",         // two sides, no twin
			"bottom":    "0px 0px 6px 12px", // two corners, no twin
		}},
		// CONTROL
		{"en", `<html lang="en"`, []string{
			`content.ui:7:19: padding-left="1" và padding-start="3" (content.ui:7:36) cùng là cạnh trái khi dòng viết từ trái sang phải: padding-start thắng`,
			`content.ui:8:20: radius-bottom-left="small" và radius-bottom-start="large" (content.ui:8:47) cùng là góc dưới bên trái khi dòng viết từ trái sang phải: radius-bottom-start thắng`,
		}, map[string]string{
			"direction": "ltr",
			"left":      "16px 0px",
			"start":     "16px 0px",
			"corner":    "12px 0px 0px 0px",
			"end":       "0px 12px 0px 0px",
			"twins":     "4px 12px",
			"apart":     "12px 0px",
			"bottom":    "0px 0px 12px 0px",
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
		lines := warningLines(printed)
		if len(lines) != len(run.told) {
			t.Errorf("%s: want %d warnings, got\n %s", run.language, len(run.told), strings.Join(lines, "\n "))
		} else {
			for i, said := range run.told {
				if !strings.HasPrefix(lines[i], "cảnh báo: "+said) {
					t.Errorf("%s: warning %d\n got  %s\n want %s…", run.language, i, lines[i], said)
				}
			}
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
