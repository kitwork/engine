package stylesheet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"
)

// The text scope draws the grammar of 20/10/2026 in a real browser (1000px wide), with page.css first
// and reboot.css last as in TestBrowserStylesheetsHoldInABrowser:
//   - h4, h5 and h6 below h3 on the heading scale of a 16px richtext: 16px, 14px, 13px; 1.5em above a
//     heading after a paragraph, half an em above one right under a heading;
//   - u underlined in the text's own colour (its decoration is its ink, not the link's), s struck
//     through;
//   - code in a line in the code face on ink at 8% (the colour color-mix makes of the palette's ink, in
//     light and in dark) with 4px corners and room on its sides; code inside a code block, and inside a
//     link however deep (a > strong > code), without them;
//   - a code block on the panel (light and dark), 16px of room inside, 8px corners, its white space kept
//     and a line longer than the block scrolling sideways inside it, not widening the page;
//   - a rule: 1px of the line colour on its top edge only, 24px of room above and below it;
//   - align-center centred, align-right text-align end: its words at the right edge of a page written
//     left to right and at the left edge under dir="rtl". The page holds the scope alone, so what
//     aligns is the scope's rule (the page layer writes the same text-align for align="right" on a
//     <text>, which TestRichtextRulesStayInsideTheScope holds, and would win over it).
//
// CONTROL: with align-right written text-align:right (an overlaid copy of richtext.go), both rows of
// align-right failed on the word, and under dir="rtl" its words went to the right edge (569px from the
// left). (A first version of this test also recorded the
// page layer's .align-right, whose text-align:end won over the scope: the control passed, so the page
// layer is left out.) With the rule of code in a line written .richtext :where(code), the rows of code
// in a code block and code in a link failed: they took the fill and the room of code in a line.
func TestBrowserRichtextScopeDrawsTheGrammar(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the richtext scope browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	palette := examplePalette(t)
	table := NewTable(palette)
	var rules Rules
	if err := table.Richtext(&rules); err != nil {
		t.Fatal(err)
	}
	paint := func(value string, m Mode) string {
		c, ok := palette.Paint(value, m)
		if !ok {
			t.Fatalf("no colour %s", value)
		}
		return c.Hex()
	}
	long := "const một_dòng_rất_dài = [" + "1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30" + "];"
	body := `
<div class="richtext" id="scope" style="inline-size:600px">
<h3>Ba</h3>
<p>chữ</p>
<h4 id="h4">Bốn</h4>
<h5 id="h5">Năm</h5>
<h6 id="h6">Sáu</h6>
<p><u id="u">gạch chân</u> <s id="s">gạch ngang</s> <code id="code">go test</code> <a href="/x"><strong><code id="link-code">mã</code></strong></a></p>
<pre id="pre"><code id="pre-code">` + long + "\n  thụt   lề" + `</code></pre>
<p id="before-rule">trên</p>
<hr id="rule">
<p id="after-rule">dưới</p>
<p class="align-center" id="center"><span id="center-word">giữa</span></p>
<p class="align-right" id="right"><span id="right-word">phải</span></p>
</div>
<div class="richtext" dir="rtl" style="inline-size:600px"><p class="align-right" id="rtl"><span id="rtl-word">يمين</span></p></div>
<span id="code-light" style="background-color:color-mix(in srgb,` + paint("ink", LightMode) + ` 8%,transparent)"></span>
<span id="code-dark" style="background-color:color-mix(in srgb,` + paint("ink", DarkMode) + ` 8%,transparent)"></span>
`
	script := `<script>
	fetch("/hold");
	window.addEventListener("load", function () {
	  var q = function (id) { return document.getElementById(id); };
	  var style = function (id, property) { return getComputedStyle(q(id)).getPropertyValue(property).trim(); };
	  var edges = function (word, line) {
	    var w = q(word).getBoundingClientRect(), l = q(line).getBoundingClientRect();
	    return [Math.round(w.left - l.left), Math.round(l.right - w.right)].join(" ");
	  };
	  var r = {};
	  r.headings = [style("h4", "font-size"), style("h5", "font-size"), style("h6", "font-size"), style("h4", "margin-top"), style("h5", "margin-top")].join(" ");
	  r.underline = [style("u", "text-decoration-line"), String(style("u", "text-decoration-color") === style("u", "color"))].join(" ");
	  r.strike = style("s", "text-decoration-line");
	  r.code = [String(style("code", "font-family").indexOf("monospace") >= 0), String(style("code", "background-color") === style("code-light", "background-color")),
	    style("code", "border-top-left-radius"), style("code", "padding-left")].join(" ");
	  r.preCode = [style("pre-code", "background-color"), style("pre-code", "padding-left"), style("pre-code", "border-top-left-radius")].join(" ");
	  r.linkCode = [style("link-code", "background-color"), style("link-code", "padding-left"), String(style("link-code", "font-family").indexOf("monospace") >= 0)].join(" ");
	  r.pre = [style("pre", "background-color"), style("pre", "padding-top"), style("pre", "border-top-left-radius"), style("pre", "overflow-x"), style("pre", "white-space"),
	    String(q("pre").scrollWidth > q("pre").clientWidth), String(q("pre-code").textContent.indexOf("\n  thụt   lề") > 0),
	    String(document.documentElement.scrollWidth <= innerWidth)].join(" ");
	  r.rule = [style("rule", "border-top-width"), style("rule", "border-top-style"), style("rule", "border-top-color"), style("rule", "border-bottom-width"),
	    style("rule", "margin-top"), style("after-rule", "margin-top")].join(" ");
	  r.center = style("center", "text-align") + " " + String(Math.abs(q("center-word").getBoundingClientRect().left - q("center").getBoundingClientRect().left -
	    (q("center").getBoundingClientRect().right - q("center-word").getBoundingClientRect().right)) <= 1);
	  r.right = style("right", "text-align") + " " + edges("right-word", "right").split(" ")[1];
	  r.rtl = style("rtl", "text-align") + " " + edges("rtl-word", "rtl").split(" ")[0];
	  document.documentElement.setAttribute("data-theme", "dark");
	  r.dark = [String(style("code", "background-color") === style("code-dark", "background-color")), style("pre", "background-color")].join(" ");
	  fetch("/report", { method: "POST", body: JSON.stringify(r) });
	});
	</script>`
	files := map[string][]byte{"/page.css": rules.CSS(), "/colors.css": Colors(palette).CSS, "/reboot.css": Reboot().CSS}
	page := `<!doctype html><html lang="vi"><head><meta charset="utf-8"><title>richtext</title>` +
		`<link rel="stylesheet" href="/page.css"><link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/reboot.css"></head>` +
		`<body class="flex-column">` + body + script + `</body></html>`

	var mu sync.Mutex
	reported := make(chan struct{})
	var once sync.Once
	var report []byte
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if css, ok := files[request.URL.Path]; ok {
			response.Header().Set("Content-Type", "text/css; charset=utf-8")
			_, _ = response.Write(css)
			return
		}
		switch request.URL.Path {
		case "/":
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(response, page)
		case "/hold":
			select {
			case <-reported:
			case <-time.After(15 * time.Second):
			}
		case "/report":
			body, _ := io.ReadAll(request.Body)
			mu.Lock()
			report = body
			mu.Unlock()
			once.Do(func() { close(reported) })
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync",
		"--metrics-recording-only", "--no-first-run", "--run-all-compositor-stages-before-draw", "--window-size=1000,800",
		"--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--virtual-time-budget=10000", "--dump-dom", server.URL + "/"}
	if runtime.GOOS == "windows" {
		args = append([]string{"--disable-background-mode", "--disable-breakpad", "--disable-crashpad-for-testing"}, args...)
	}
	output, _ := exec.CommandContext(ctx, browser, args...).CombinedOutput()

	mu.Lock()
	defer mu.Unlock()
	if report == nil {
		t.Fatalf("the page never reported\n%.700s", output)
	}
	got := map[string]string{}
	if err := json.Unmarshal(report, &got); err != nil {
		t.Fatalf("report %s: %v", report, err)
	}
	want := map[string]string{
		"headings":  "16px 14px 13px 24px 7px", // h4 1em, h5 0.875em, h6 0.8125em; 1.5em above a heading, half an em right under one
		"underline": "underline true",
		"strike":    "line-through",
		"code":      "true true 4px 4px",
		"preCode":   "rgba(0, 0, 0, 0) 0px 0px",
		"linkCode":  "rgba(0, 0, 0, 0) 0px true", // in a link: the link's words in the code face, no fill
		"pre":       hexRGB(paint("panel", LightMode)) + " 16px 8px auto pre true true true",
		"rule":      "1px solid " + hexRGB(paint("line", LightMode)) + " 0px 24px 24px",
		"center":    "center true",
		"right":     "end 0", // the words at the right edge
		"rtl":       "end 0", // the words at the left edge: end mirrors
		"dark":      "true " + hexRGB(paint("panel", DarkMode)),
	}
	var names []string
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if got[name] != want[name] {
			t.Errorf("%s:\n got  %s\n want %s", name, got[name], want[name])
		}
	}
}
