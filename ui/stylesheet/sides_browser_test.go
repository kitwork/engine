package stylesheet

import (
	"strings"
	"testing"
)

// The sides and corners in a real browser (decision 13/10/2026), each read from the computed style:
//   - radius="large" radius-bottom="none" (written side first) rounds the top corners and squares the
//     bottom ones: a side beats radius whatever order they are written in; a corner beats a side
//     beats radius (radius-top-left="full" radius-top="none" radius="large");
//   - in dir="rtl" radius-top-left stays on the left, radius-top-start moves to the right, and
//     padding-left stays on the left while padding-start moves; in a line left to right
//     radius-top-start is on the left;
//   - a twin and its physical word on one element: the logical one, later in the fixed order, wins.
//
// CONTROL: the same radius rule written again after the side (the order the class names would sort
// in, radius-large after radius-bottom-none) rounds the bottom again, so the measurement tells the two
// orders apart.
func TestBrowserSidesAndCornersHoldInABrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the sides browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	table := NewTable(examplePalette(t))
	var rules Rules
	classes := func(written string) string {
		var out []string
		for _, field := range strings.Fields(written) {
			name, value, _ := strings.Cut(field, "=")
			class, err := table.Class(&rules, Intent{Name: name, Value: value})
			if err != nil {
				t.Fatalf("%s: %v", field, err)
			}
			out = append(out, class)
		}
		return strings.Join(out, " ")
	}
	box := "width=100px height=50px background=brand "
	body := `
<div id="top-rounded" class="flex-column ` + classes(box+"radius-bottom=none radius=large") + `"></div>
<div id="corner" class="flex-column ` + classes(box+"radius-top-left=full radius-top=none radius=large") + `"></div>
<div dir="rtl" id="rtl-physical" class="flex-column ` + classes(box+"radius-top-left=large") + `"></div>
<div dir="rtl" id="rtl-logical" class="flex-column ` + classes(box+"radius-top-start=large") + `"></div>
<div dir="ltr" id="ltr-logical" class="flex-column ` + classes(box+"radius-top-start=large") + `"></div>
<div dir="rtl" id="rtl-padding-left" class="flex-column ` + classes("padding-left=4") + `"></div>
<div dir="rtl" id="rtl-padding-start" class="flex-column ` + classes("padding-start=4") + `"></div>
<div id="twins" class="flex-column ` + classes("padding-start=3 padding-left=1") + `"></div>
<div dir="rtl" id="twins-rtl" class="flex-column ` + classes("padding-start=3 padding-left=1") + `"></div>
`
	script := `<script>
	fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	window.addEventListener("load", function () {
	  var q = function (id) { return document.getElementById(id); };
	  var style = function (id, property) { return getComputedStyle(q(id)).getPropertyValue(property).trim(); };
	  var corners = function (id) {
	    return ["top-left", "top-right", "bottom-left", "bottom-right"].map(function (corner) { return style(id, "border-" + corner + "-radius"); }).join(" ");
	  };
	  var sides = function (id) { return style(id, "padding-left") + " " + style(id, "padding-right"); };
	  var r = {};
	  r.topRounded = corners("top-rounded");
	  r.corner = corners("corner");
	  r.rtlPhysical = corners("rtl-physical");
	  r.rtlLogical = corners("rtl-logical");
	  r.ltrLogical = corners("ltr-logical");
	  r.rtlPaddingLeft = sides("rtl-padding-left");
	  r.rtlPaddingStart = sides("rtl-padding-start");
	  r.twins = sides("twins");
	  r.twinsRtl = sides("twins-rtl");
	  // CONTROL: radius written again after the side, as the class names would sort
	  var later = document.createElement("style");
	  later.textContent = "@layer page{.radius-large{border-radius:.75rem}}";
	  document.head.appendChild(later);
	  r.control = corners("top-rounded");
	  fetch("/report", { method: "POST", body: JSON.stringify(r) });
	});
	</script>`
	html := `<!doctype html><html lang="vi"><head><meta charset="utf-8"><title>sides</title>` +
		`<link rel="stylesheet" href="/reboot.css"><link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/page.css"></head>` +
		`<body class="flex-column">` + body + script + `</body></html>`
	got := runBrowserReport(t, browser, html, map[string][]byte{
		"/reboot.css": Reboot().CSS,
		"/colors.css": Colors(examplePalette(t)).CSS,
		"/page.css":   rules.CSS(),
	})
	want := map[string]string{
		"topRounded":      "12px 12px 0px 0px",   // radius="large" radius-bottom="none": rounded top, square bottom
		"corner":          "999px 0px 12px 12px", // a corner over a side over radius
		"rtlPhysical":     "12px 0px 0px 0px",    // left never flips
		"rtlLogical":      "0px 12px 0px 0px",    // start is the right in a line right to left
		"ltrLogical":      "12px 0px 0px 0px",    // and the left in a line left to right
		"rtlPaddingLeft":  "16px 0px",            // padding-left never flips
		"rtlPaddingStart": "0px 16px",            // padding-start does
		"twins":           "12px 0px",            // padding-start, later in the fixed order, wins
		"twinsRtl":        "4px 12px",            // right to left the twins are two sides
		"control":         "12px 12px 12px 12px", // CONTROL: radius after the side rounds the bottom again
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], value)
		}
	}
}
