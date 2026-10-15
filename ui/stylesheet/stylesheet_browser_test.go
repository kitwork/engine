package stylesheet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The engine's stylesheets of a .ui page, in a real browser at 1000px (medium holds, large does not):
//   - every rule of reboot.css, colors.css, page.css and the layered icons parses, and every
//     declaration it writes is one the browser keeps (a bad escape or a typo drops a rule silently);
//   - an escaped class reaches the element that carries it as written (: + , . / %), the colour
//     operators among them (color-brand+15, color-ink-30/50, color-brand.on-20);
//   - the layers decide, whatever order the <link>s stand in: they are linked page first, reboot
//     last, so without the layer statement at the top of page.css its layer would come first and
//     lose to every element look;
//   - the fixed order: a state beats a width, active beats open, disabled beats active;
//   - align and space through the variables, reset on every container; open: follows the nearest
//     element that opens; show's own layer gives back the display a class set; the accessibility
//     floor of [hidden]; an image with a ratio covers its box unless fit says otherwise;
//   - colors.css: the tokens, the lighter colours and alphas, and the dark look under data-theme;
//     a stronger colour is the custom property page.css sets for each mode.
//
// Control: an icon rule outside every layer beats show-false (the reason Elements layers the icons);
// the same rule inside the elements layer loses to it.
func TestBrowserStylesheetsHoldInABrowser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the stylesheet browser proof in short mode")
	}
	browser := findHeadlessBrowser()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}

	palette := examplePalette(t)
	table := NewTable(palette)
	var rules Rules
	// classes reads "medium:padding=6 large+hover:background=brand" with a tag (badge: color=success)
	// and returns the classes the table makes, recording their rules.
	classes := func(written string) string {
		tag := ""
		if head, rest, ok := strings.Cut(written, ": "); ok && !strings.ContainsAny(head, " =") {
			tag, written = head, rest
		}
		var out []string
		for _, field := range strings.Fields(written) {
			name, value, _ := strings.Cut(field, "=")
			in := Intent{Tag: tag, Name: name, Value: value}
			if i := strings.LastIndexByte(name, ':'); i >= 0 {
				in.Name = name[i+1:]
				for _, part := range strings.Split(name[:i], "+") {
					if indexOf(widthWords, strings.Split(part, ",")[0]) >= 0 {
						in.Width = part
					} else {
						in.State = part
					}
				}
			}
			class, err := table.Class(&rules, in)
			if err != nil {
				t.Fatalf("%s: %v", field, err)
			}
			out = append(out, class)
		}
		return strings.Join(out, " ")
	}
	// Every attribute and value of the table goes into page.css too, unprefixed, under a width and
	// under a state, so the parse check below covers every declaration the table can write.
	for _, name := range Attributes() {
		for _, value := range valuesToTry(table, name) {
			for _, in := range []Intent{{Name: name, Value: value}, {Width: "large", Name: name, Value: value}, {State: "focus", Name: name, Value: value}} {
				if (name == "show" || name == "pin") && in.State != "" { // neither changes with a state
					continue
				}
				if _, err := table.Class(&rules, in); err != nil {
					t.Fatalf("%+v: %v", in, err)
				}
			}
		}
	}
	for _, tag := range []string{"badge", "text"} {
		if _, err := table.Class(&rules, Intent{Tag: tag, Name: "color", Value: "brand"}); err != nil {
			t.Fatal(err)
		}
		if _, err := table.Class(&rules, Intent{Tag: tag, Name: "align", Value: "end"}); err != nil {
			t.Fatal(err)
		}
	}

	onHex, _, _ := palette.Value("brand.on")
	_, badgeText, _ := table.BadgeTint("success")
	// a line long enough to wrap many times in 160px: clamped to two lines it is 48px tall (1.5 × 16px × 2)
	long := strings.Repeat("Một dòng chữ dài của quán cà phê. ", 8)
	const image = `data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='40' height='10'/%3E`
	body := `
<div id="padding" class="` + classes("medium:padding=6 large:padding=2") + `"></div>
<div class="flex-column ` + classes("width=400px") + `"><div id="width" class="` + classes("width=50%") + `"></div></div>
<div id="ratio" class="flex-column ` + classes("width=160px aspect-ratio=16/9") + `"></div>
<div id="sides" class="flex-column ` + classes("padding=4 padding-y=2 padding-top=8") + `"></div>
<div dir="rtl" id="start" class="flex-column ` + classes("padding-start=4") + `"></div>
<p id="token" class="` + classes("color=danger") + `">x</p>
<p id="shade" class="` + classes("color=ink-30") + `">x</p>
<p id="shade-reference" style="color:color-mix(in oklab,#2b211b 70%,#f6f3ee)">x</p>
<p id="lighter-alpha" class="` + classes("color=ink-30/50") + `">x</p>
<p id="lighter-alpha-reference" style="color:color-mix(in srgb,color-mix(in oklab,#2b211b 70%,#f6f3ee) 50%,transparent)">x</p>
<p id="on-lighter" class="` + classes("color=brand.on-20") + `">x</p>
<p id="on-lighter-reference" style="color:color-mix(in oklab,` + onHex + ` 80%,#f6f3ee)">x</p>
<p id="strong" class="` + classes("color=brand+15 medium+hover:background=brand+15") + `">x</p>
<p id="strong-medium" class="` + classes("medium:color=brand+40") + `">x</p>
<div id="alpha" class="` + classes("background=paper/80") + `"></div>
<div id="alpha-reference" style="background-color:color-mix(in srgb,#ffffff 80%,transparent)"></div>
<div class="flex-column ` + classes("width=160px") + `">
  <p id="lines" class="` + classes("lines=2 show=false medium:show=true") + `">` + long + `</p>
  <p id="lines-reference" class="` + classes("lines=2") + `">` + long + `</p>
</div>
<div id="show-box" class="flex-row ` + classes("show=false medium:show=true") + `"></div>
<div id="show-large" class="flex-row ` + classes("show=false large:show=true") + `"></div>
<div id="card" class="card flex-column ` + classes("background=brand radius=full") + `"></div>
<div id="card-plain" class="card flex-column"></div>
<span id="badge" class="badge flex-row ` + classes("badge: color=success") + `">x</span>
<span id="badge-reference" style="background-color:color-mix(in oklab,#2f9e63 15%,#f6f3ee)">x</span>
<span id="badge-written" class="badge flex-row ` + classes("badge: color=success background=paper") + `">x</span>
<a id="disabled-over-active" aria-current="page" aria-disabled="true" class="` + classes("medium:color=success active:color=ink disabled:color=danger") + `">x</a>
<a id="active-over-width" aria-current="page" class="` + classes("medium:color=success active:color=ink") + `">x</a>
<a id="inactive" aria-current="false" class="` + classes("medium:color=success active:color=ink") + `">x</a>
<div id="combined" aria-disabled="true" class="` + classes("medium+disabled:opacity=50") + `"></div>
<div id="either" aria-disabled="true" class="` + classes("active,disabled:opacity=40") + `"></div>
<details open><summary><a id="active-over-open" aria-current="page" class="` + classes("open:color=brand active:color=ink") + `">x</a><i id="outer" class="` + classes("open:rotate=180") + `"></i></summary>
  <details><summary><i id="inner" class="` + classes("open:rotate=180") + `"></i></summary></details></details>
<button id="expanded" aria-expanded="true" class="flex-row"><i id="expanded-icon" class="` + classes("open:rotate=180") + `"></i></button>
<i id="closed-icon" class="` + classes("open:rotate=180") + `"></i>
<div id="align" class="flex-column ` + classes("medium:flex=row align=top") + `"><div id="align-child" class="flex-column"></div></div>
<div id="space" class="flex-row ` + classes("align=center space=between") + `"></div>
<div><button id="button" class="flex-row ` + classes("border=line") + `">x</button></div>
<button id="button-written" class="flex-row ` + classes("align=start") + `">x</button>
<p id="text" class="` + classes("text: align=center") + `">x</p>
<div id="box-center" class="flex-column ` + classes("align=center") + `"></div>
<div class="flex-column place-anchor ` + classes("width=200px height=100px") + `"><div id="placed" class="` + classes("place=bottom-end width=20px height=10px") + `"></div></div>
<div id="hidden" class="flex-row" hidden></div>
<img id="cover" alt="" src="` + image + `" class="` + classes("image: aspect-ratio=4/3") + `">
<img id="contain" alt="" src="` + image + `" class="` + classes("image: aspect-ratio=4/3 fit=contain") + `">
<img id="plain-image" alt="" src="` + image + `">
<div><i id="icon" class="icon-test ` + classes("show=false") + `"></i><i id="icon-raw" class="icon-raw ` + classes("show=false") + `"></i></div>
`
	reboot, colors := Reboot(), Colors(palette)
	pageCSS := rules.CSS()
	icons := Elements([]byte(".icon-test{display:inline-block;width:1em;height:1em;background-color:currentColor}"))
	files := map[string][]byte{
		"/page.css":   pageCSS,
		"/icons.css":  icons,
		"/raw.css":    []byte(".icon-raw{display:inline-block}"),
		"/colors.css": colors.CSS,
		"/reboot.css": reboot.CSS,
	}
	// What the browser must keep: per file, the properties of each style rule in source order. A
	// desktop browser does not know text-size-adjust (mobile browsers do), and Chromium does not know
	// -webkit-backdrop-filter (Safari 15.4 to 17 knows only it): neither is asked for; the unprefixed
	// backdrop-filter beside it is.
	expected := map[string][][]string{}
	for path, css := range files {
		if path == "/raw.css" {
			continue
		}
		for _, rule := range rulesWithBodies(string(css)) {
			var properties []string
			for _, declaration := range strings.Split(rule.body, ";") {
				property, _, _ := strings.Cut(declaration, ":")
				if property == "text-size-adjust" || property == "-webkit-text-size-adjust" || property == "-webkit-backdrop-filter" {
					continue
				}
				properties = append(properties, property)
			}
			expected[path] = append(expected[path], properties)
		}
	}
	expectedJSON, _ := json.Marshal(expected)

	script := `<script>
	fetch("/hold"); // a request waiting on its answer keeps the browser's clock running in real time
	var expected = ` + string(expectedJSON) + `;
	function flatten(list, out) {
	  for (var i = 0; i < list.length; i++) {
	    if (list[i] instanceof CSSStyleRule) out.push(list[i]); else if (list[i].cssRules) flatten(list[i].cssRules, out);
	  }
	  return out;
	}
	window.addEventListener("load", function () {
	  var q = function (id) { return document.getElementById(id); };
	  var style = function (id, property) { return getComputedStyle(q(id)).getPropertyValue(property).trim(); };
	  var box = function (id) { var r = q(id).getBoundingClientRect(), p = q(id).parentElement.getBoundingClientRect(); return [r.x - p.x, r.y - p.y, r.width, r.height].map(Math.round).join(","); };
	  var r = {};
	  r.width = String(innerWidth >= 768 && innerWidth < 1024);
	  Array.prototype.forEach.call(document.styleSheets, function (sheet) {
	    var path = new URL(sheet.href).pathname, want = expected[path];
	    if (!want) return;
	    var got = flatten(sheet.cssRules, []), missing = [];
	    got.forEach(function (rule, i) {
	      (want[i] || []).forEach(function (property) {
	        var kept = rule.style.getPropertyValue(property) !== "" ||
	          Array.prototype.some.call(rule.style, function (name) { return name.indexOf(property + "-") === 0; });
	        if (!kept) missing.push(rule.selectorText + " " + property);
	      });
	    });
	    r["parse " + path] = "rules=" + got.length + " missing=" + missing.join("|");
	  });
	  r.padding = style("padding", "padding-top");
	  r.width50 = String(q("width").getBoundingClientRect().width);
	  r.ratio = String(q("ratio").getBoundingClientRect().height);
	  r.sides = ["top", "right", "bottom", "left"].map(function (side) { return style("sides", "padding-" + side); }).join(" ");
	  r.start = style("start", "padding-right") + " " + style("start", "padding-left");
	  r.token = style("token", "color");
	  r.shade = String(style("shade", "color") === style("shade-reference", "color"));
	  r.lighterAlpha = String(style("lighter-alpha", "color") === style("lighter-alpha-reference", "color"));
	  r.onLighter = String(style("on-lighter", "color") === style("on-lighter-reference", "color"));
	  r.strong = style("strong", "color") + " " + style("strong-medium", "color");
	  r.alpha = String(style("alpha", "background-color") === style("alpha-reference", "background-color"));
	  r.lines = [style("lines", "display") === style("lines-reference", "display"), q("lines").getBoundingClientRect().height, q("lines-reference").getBoundingClientRect().height].join(" ");
	  r.showBox = style("show-box", "display");
	  r.showLarge = style("show-large", "display");
	  r.card = style("card", "background-color") + " " + style("card", "border-top-left-radius");
	  r.cardPlain = style("card-plain", "background-color") + " " + style("card-plain", "border-top-width") + " " + style("card-plain", "box-shadow") + " " + style("card-plain", "border-top-left-radius");
	  r.badge = String(style("badge", "background-color") === style("badge-reference", "background-color")) + " " + style("badge", "color");
	  r.badgeWritten = style("badge-written", "background-color");
	  r.disabledOverActive = style("disabled-over-active", "color");
	  r.activeOverWidth = style("active-over-width", "color");
	  r.inactive = style("inactive", "color");
	  r.combined = style("combined", "opacity");
	  r.either = style("either", "opacity");
	  r.activeOverOpen = style("active-over-open", "color");
	  r.open = [style("outer", "rotate"), style("inner", "rotate"), style("expanded-icon", "rotate"), style("closed-icon", "rotate")].join(" ");
	  r.align = [style("align", "flex-direction"), style("align", "justify-content"), style("align", "align-items")].join(" ");
	  r.alignChild = style("align-child", "justify-content") + " " + style("align-child", "align-items");
	  r.space = style("space", "justify-content") + " " + style("space", "align-items");
	  r.button = [style("button", "display"), style("button", "justify-content"), style("button", "align-items"), style("button", "border-top-color")].join(" ");
	  r.buttonWritten = style("button-written", "justify-content") + " " + style("button-written", "align-items");
	  r.text = style("text", "text-align") + " " + style("box-center", "text-align");
	  r.placed = style("placed", "position") + " " + box("placed");
	  r.hidden = style("hidden", "display");
	  r.fit = [style("cover", "object-fit"), style("contain", "object-fit"), style("plain-image", "object-fit")].join(" ");
	  r.icon = style("icon", "display") + " " + style("icon-raw", "display");
	  r.focus = getComputedStyle(document.documentElement).getPropertyValue("--kitwork-focus").trim();
	  r.light = getComputedStyle(document.documentElement).backgroundColor + " " + getComputedStyle(document.documentElement).color;
	  document.documentElement.setAttribute("data-theme", "dark");
	  r.dark = getComputedStyle(document.documentElement).backgroundColor + " " + getComputedStyle(document.documentElement).color + " " + style("token", "color") + " " + style("strong", "color");
	  fetch("/report", { method: "POST", body: JSON.stringify(r) });
	});
	</script>`
	// page.css first and reboot.css last: the layer statement at the top of each makes the order moot
	html := `<!doctype html><html lang="vi"><head><meta charset="utf-8"><title>stylesheet</title>` +
		`<link rel="stylesheet" href="/page.css"><link rel="stylesheet" href="/icons.css"><link rel="stylesheet" href="/raw.css">` +
		`<link rel="stylesheet" href="/colors.css"><link rel="stylesheet" href="/reboot.css"></head>` +
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
			_, _ = io.WriteString(response, html)
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

	// rgb and night are a colour value as the browser computes it, in light and in dark
	rgb := func(value string) string {
		c, ok := palette.Paint(value, LightMode)
		if !ok {
			t.Fatalf("no colour %s", value)
		}
		return hexRGB(c.Hex())
	}
	night := func(value string) string {
		c, _ := palette.Paint(value, DarkMode)
		return hexRGB(c.Hex())
	}
	want := map[string]string{
		"width":              "true", // medium (768px) holds, large (1024px) does not
		"padding":            "24px",
		"width50":            "200",
		"ratio":              "90",
		"sides":              "32px 16px 8px 16px", // a side over an axis over padding
		"start":              "16px 0px",           // start is the right edge when the writing runs right to left
		"token":              rgb("danger"),
		"shade":              "true",
		"lighterAlpha":       "true",
		"onLighter":          "true",
		"strong":             rgb("brand+15") + " " + rgb("brand+40"), // the custom property of page.css
		"alpha":              "true",
		"lines":              "true 48 48", // revert-layer gives back the clamp of lines, not a guessed block
		"showBox":            "flex",
		"showLarge":          "none",
		"card":               rgb("brand") + " 999px", // written beats the element's look
		"cardPlain":          rgb("paper") + " 0px none 12px",
		"badge":              "true " + rgb(badgeText),
		"badgeWritten":       rgb("paper"),
		"disabledOverActive": rgb("danger"),
		"activeOverWidth":    rgb("ink"), // a state beats a width while it holds
		"inactive":           rgb("success"),
		"combined":           "0.5",
		"either":             "0.4",
		"activeOverOpen":     rgb("ink"),
		"open":               "180deg none 180deg none", // the nearest that opens: the closed inner one keeps its chevron
		"align":              "row center flex-start",   // align="top" re-read for the row of medium
		"alignChild":         "normal normal",           // the child container reads none of its parent's align
		"space":              "space-between center",
		"button":             "inline-flex center center " + rgb("line"),
		"buttonWritten":      "flex-start center",
		"text":               "center start",
		"placed":             "absolute 180,90,20,10",
		"hidden":             "none",
		"fit":                "cover contain fill",
		"icon":               "none inline-block", // control: the unlayered icon rule beats show-false
		"focus":              "#c8742c",           // the brand reaches 3:1 on canvas and paper: the ring is the brand
		"light":              rgb("canvas") + " " + rgb("ink"),
		"dark":               night("canvas") + " " + night("ink") + " " + night("danger") + " " + night("brand+15"),
	}
	for path, rulesOfFile := range expected {
		want["parse "+path] = "rules=" + strconv.Itoa(len(rulesOfFile)) + " missing="
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
	if len(expected["/page.css"]) < 1000 {
		t.Fatalf("page.css has %d rules: the table no longer goes into it", len(expected["/page.css"]))
	}
}

// hexRGB is #rrggbb as a browser serializes a computed colour: rgb(r, g, b).
func hexRGB(hex string) string {
	n, _ := strconv.ParseUint(hex[1:], 16, 32)
	return "rgb(" + strconv.Itoa(int(n>>16&0xff)) + ", " + strconv.Itoa(int(n>>8&0xff)) + ", " + strconv.Itoa(int(n&0xff)) + ")"
}

func findHeadlessBrowser() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KITWORK_BROWSER_TESTS"))) {
	case "0", "false", "off", "no":
		return ""
	}
	for _, name := range []string{"google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if runtime.GOOS != "windows" {
		return ""
	}
	for _, path := range []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}
