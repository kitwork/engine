package stylesheet

import (
	"strings"
	"testing"
)

// A code block in a row, in a real browser, framed at two widths (a frame's media queries read its own
// width):
//   - a phone, 390px: a row holding a block whose longest line is wider than the screen stays inside
//     the frame and the block scrolls on its own. This is the card of the Kitwork site's
//     getting-started page: a badge, then a column holding the block.
//   - from small up, 900px: a code panel beside a long text keeps its lines whole; the text takes what
//     is left (the Kitwork home's manifest section). Letting the panel shrink there too would share the
//     shrink by size and cut its lines.
//
// Controls, each the same row with one inline style: on the phone, the item's minimum given back to
// the browser (min-inline-size:auto) makes the row as wide as the line; at 900px, a minimum of 0 makes
// the panel's block scroll. So each width's rule is what decides.
func TestBrowserCodeBlockScrollsInsideARow(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the code block browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	table := NewTable(examplePalette(t))
	var rules Rules
	class := func(in Intent) string {
		name, err := table.Class(&rules, in)
		if err != nil {
			t.Fatal(err)
		}
		return name
	}
	gap, padding := class(Intent{Name: "gap", Value: "4"}), class(Intent{Name: "padding", Value: "5"})
	fromMedium := class(Intent{Width: "medium", Name: "flex", Value: "row"})
	long := strings.Repeat("go run . --root=docs/examples ", 12)     // wider than a phone
	panel := strings.Repeat("app.database(path) ", 2)                // a manifest line: fits beside the text
	text := strings.Repeat("The same file boots the web host. ", 12) // one long paragraph
	step := func(id, itemStyle string) string {
		return `<div id="` + id + `" class="card flex-row ` + gap + ` ` + padding + `">` +
			`<span class="badge flex-row">1</span>` +
			`<div class="flex-column"` + itemStyle + `><h2>Chạy</h2>` +
			`<div class="flex-column"><pre id="` + id + `-code"><code>` + long + `</code></pre></div></div></div>`
	}
	manifest := func(id, panelStyle string) string {
		return `<section id="` + id + `" class="flex-column ` + fromMedium + ` ` + gap + `">` +
			`<div class="flex-column"><p>` + text + `</p></div>` +
			`<div class="card ` + padding + `"` + panelStyle + `><pre id="` + id + `-code"><code>` + panel + `</code></pre></div></section>`
	}
	frame := `<!doctype html><html lang="vi"><head><meta charset="utf-8">` +
		`<link rel="stylesheet" href="/reboot.css"><link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/page.css"></head>` +
		`<body class="flex-column">` +
		step("step", "") + step("step-control", ` style="min-inline-size:auto"`) +
		manifest("manifest", "") + manifest("manifest-control", ` style="min-inline-size:0"`) +
		`</body></html>`
	script := `<script>
	fetch("/hold");
	window.addEventListener("load", function () {
	  var r = {};
	  [["phone", "step"], ["phone", "step-control"], ["wide", "manifest"], ["wide", "manifest-control"]].forEach(function (at) {
	    var d = document.getElementById(at[0]).contentDocument, row = d.getElementById(at[1]), code = d.getElementById(at[1] + "-code");
	    r[at[0] + " " + at[1]] = (row.scrollWidth <= row.clientWidth ? "fits" : "wider") + " " +
	      (code.scrollWidth > code.clientWidth ? "scrolls" : "whole") + " " + d.defaultView.getComputedStyle(row).flexDirection;
	  });
	  fetch("/report", { method: "POST", body: JSON.stringify(r) });
	});
	</script>`
	html := `<!doctype html><html lang="vi"><head><meta charset="utf-8"><title>code</title></head><body>` +
		`<iframe id="phone" src="/frame.html" style="inline-size:390px;block-size:700px;border:0"></iframe>` +
		`<iframe id="wide" src="/frame.html" style="inline-size:900px;block-size:700px;border:0"></iframe>` +
		script + `</body></html>`
	got := runBrowserReport(t, browser, html, map[string][]byte{
		"/frame.html": []byte(frame),
		"/reboot.css": Reboot().CSS,
		"/colors.css": Colors(examplePalette(t)).CSS,
		"/page.css":   rules.CSS(),
	})
	want := map[string]string{
		"phone step":            "fits scrolls row", // inside the phone, the block scrolls
		"phone step-control":    "wider whole row",  // CONTROL: the browser's own minimum pushes the row
		"wide manifest":         "fits whole row",   // from small up the panel keeps its lines whole
		"wide manifest-control": "fits scrolls row", // CONTROL: a minimum of 0 there would cut them
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], value)
		}
	}
}
