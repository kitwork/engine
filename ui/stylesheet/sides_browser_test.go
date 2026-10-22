package stylesheet

import (
	"strings"
	"testing"
)

// The sides and corners in a real browser (decided 13/10/2026; start and end replaced by left and
// right, which mirror right to left, 19/10/2026), each read from the computed style:
//   - radius="large" radius-bottom="none" (written side first) rounds the top corners and squares the
//     bottom ones: a side beats radius whatever order they are written in; a corner beats a side
//     beats radius (radius-top-left="full" radius-top="none" radius="large");
//   - in dir="rtl" left and right mirror: radius-top-left rounds the top right corner, radius-left the
//     two right corners, padding-left pads the right edge; top and bottom stay where they are;
//   - the fixed order holds mirrored: in dir="rtl" a corner (logical) still beats its side (radius-top,
//     physical), a side beats another side it shares a corner with (radius-left over radius-bottom),
//     and padding-left beats padding-x.
//
// CONTROL: the same classes in dir="ltr" round the top left corner and pad the left edge, so the
// mirroring is the page's direction and not the classes'; and the same radius rule written again after
// the side (the order the class names would sort in, radius-large after radius-bottom-none) rounds the
// bottom again, so the measurement tells the two orders apart.
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
<div dir="rtl" id="rtl-corner" class="flex-column ` + classes(box+"radius-top-left=large") + `"></div>
<div dir="ltr" id="ltr-corner" class="flex-column ` + classes(box+"radius-top-left=large") + `"></div>
<div dir="rtl" id="rtl-side" class="flex-column ` + classes(box+"radius-left=large") + `"></div>
<div dir="rtl" id="rtl-top" class="flex-column ` + classes(box+"radius-top=large") + `"></div>
<div dir="rtl" id="rtl-corner-over-side" class="flex-column ` + classes(box+"radius-top-left=full radius-top=none radius=large") + `"></div>
<div dir="rtl" id="rtl-side-over-side" class="flex-column ` + classes(box+"radius-left=large radius-bottom=none radius=full") + `"></div>
<div dir="rtl" id="rtl-padding-left" class="flex-column ` + classes("padding-left=4") + `"></div>
<div dir="ltr" id="ltr-padding-left" class="flex-column ` + classes("padding-left=4") + `"></div>
<div dir="rtl" id="rtl-padding-right" class="flex-column ` + classes("padding-right=2") + `"></div>
<div dir="rtl" id="rtl-side-over-axis" class="flex-column ` + classes("padding-left=4 padding-x=2") + `"></div>
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
	  r.rtlCorner = corners("rtl-corner");
	  r.ltrCorner = corners("ltr-corner");
	  r.rtlSide = corners("rtl-side");
	  r.rtlTop = corners("rtl-top");
	  r.rtlCornerOverSide = corners("rtl-corner-over-side");
	  r.rtlSideOverSide = corners("rtl-side-over-side");
	  r.rtlPaddingLeft = sides("rtl-padding-left");
	  r.ltrPaddingLeft = sides("ltr-padding-left");
	  r.rtlPaddingRight = sides("rtl-padding-right");
	  r.rtlSideOverAxis = sides("rtl-side-over-axis");
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
	// each value lists the corners top-left top-right bottom-left bottom-right, or the padding left right
	want := map[string]string{
		"topRounded":        "12px 12px 0px 0px",   // radius="large" radius-bottom="none": rounded top, square bottom
		"corner":            "999px 0px 12px 12px", // a corner over a side over radius
		"rtlCorner":         "0px 12px 0px 0px",    // radius-top-left is the top right corner right to left
		"ltrCorner":         "12px 0px 0px 0px",    // CONTROL: and the top left left to right
		"rtlSide":           "0px 12px 0px 12px",   // radius-left is the two right corners right to left
		"rtlTop":            "12px 12px 0px 0px",   // top is the top either way
		"rtlCornerOverSide": "0px 999px 12px 12px", // the corner, mirrored, still beats radius-top
		"rtlSideOverSide":   "999px 12px 0px 12px", // radius-left over radius-bottom at their shared corner
		"rtlPaddingLeft":    "0px 16px",            // padding-left pads the right edge right to left
		"ltrPaddingLeft":    "16px 0px",            // CONTROL: and the left edge left to right
		"rtlPaddingRight":   "8px 0px",             // padding-right pads the left edge right to left
		"rtlSideOverAxis":   "8px 16px",            // padding-left over padding-x, mirrored
		"control":           "12px 12px 12px 12px", // CONTROL: radius after the side rounds the bottom again
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], value)
		}
	}
}
