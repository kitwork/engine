package work

import (
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kitwork/engine/ui/stylesheet"
)

// highlightBrowserFiles is a site with a dark look whose router.highlight gives keyword a pair and string
// a named colour, and a page with a block of Go: keyword (package, func), string, function (left to the
// engine) and comment.
var highlightBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: { light: "#f6f9fc", dark: "#16161e" }, paper: { light: "#ffffff", dark: "#1e2030" } });
router.highlight({ keyword: { light: "#7c3aed", dark: "#bb9af7" }, string: "violet" });
`,
	"localhost/page.ui":    `<page><content /></page>`,
	"localhost/content.ui": "<content><code language=\"go\">package main\nfunc main() { println(\"x\") }</code></content>",
}

// highlightProbe paints the computed colour of the first token of each kind on a 1×1 canvas and reads it
// back as 8-bit sRGB, in light and then with the dark look on (data-theme="dark", as the pre-paint and
// the appearance component write it).
const highlightProbe = `<script>
(function () {
  window.addEventListener("load", function () {
    var canvas = document.createElement("canvas");
    canvas.width = canvas.height = 1;
    var context = canvas.getContext("2d", { willReadFrequently: true });
    function pixel(css) {
      context.clearRect(0, 0, 1, 1);
      context.fillStyle = "#123456";
      context.fillStyle = css;
      context.fillRect(0, 0, 1, 1);
      var d = context.getImageData(0, 0, 1, 1).data;
      return [d[0], d[1], d[2], d[3]].join(",");
    }
    function read() {
      var out = {};
      ["keyword", "string", "function"].forEach(function (kind) {
        var token = document.querySelector(".code-" + kind);
        out[kind] = token ? pixel(getComputedStyle(token).color) : "none";
      });
      return out;
    }
    var report = {};
    try {
      document.documentElement.setAttribute("data-theme", "light");
      var light = read();
      document.documentElement.setAttribute("data-theme", "dark");
      var dark = read();
      ["keyword", "string", "function"].forEach(function (kind) {
        report["light " + kind] = light[kind];
        report["dark " + kind] = dark[kind];
      });
    } catch (problem) { report = { error: String(problem && problem.stack || problem) }; }
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  });
})();
</script>`

// TestBrowserHighlightPerMode proves router.highlight in a real browser on a page the engine serves: a
// kind's { light, dark } pair is its light colour in light and its dark colour once the page is dark
// (--kitwork-highlight-keyword, set per mode in page.<hash>.css), a named colour the same in both, each
// within 1/255 of the colour the engine measures (Palette.Paint). CONTROL: function, which the call
// leaves to the engine, changes with the mode too (its engine colour, --kitwork-code-info), so the dark
// look is really on.
func TestBrowserHighlightPerMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the router.highlight browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, highlightBrowserFiles)
	if body := serveUI(t, tenant, "http://localhost/").Body.String(); !strings.Contains(body, `class="code-keyword"`) || !strings.Contains(body, `class="code-function"`) {
		t.Fatalf("the site is not the probe's: it must show keyword and function:\n%.1500s", body)
	}
	palette := tenant.renderPlan().ui.styles[""].palette
	if palette == nil || !palette.Dark() {
		t.Fatal("the probe's palette has a dark look")
	}

	var serving sync.Mutex
	reports := make(chan map[string]string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/report" {
			var report map[string]string
			body, _ := io.ReadAll(request.Body)
			if err := json.Unmarshal(body, &report); err != nil {
				t.Errorf("report %s: %v", body, err)
			}
			select {
			case reports <- report:
			default:
			}
			return
		}
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		forward.Header.Del("Accept-Encoding") // the probe goes into the document as text
		answer := httptest.NewRecorder()
		serving.Lock()
		tenant.Serve(answer, forward)
		serving.Unlock()
		for name, values := range answer.Header() {
			response.Header()[name] = values
		}
		body := answer.Body.String()
		if request.URL.Path == "/" {
			at := strings.LastIndex(body, "</body>")
			if at < 0 {
				t.Errorf("/ has no </body>:\n%.500s", body)
			} else {
				body = body[:at] + highlightProbe + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	got := runBrowser(t, browser, server.URL+"/", "router.highlight", 1000, "", reports)
	if message, broke := got["error"]; broke {
		t.Fatalf("the probe broke: %s", message)
	}
	t.Logf("the browser read: %v", got)
	pinned := map[string]string{
		"light keyword": "#7c3aed", "dark keyword": "#bb9af7",
		"light string": "#ee82ee", "dark string": "#ee82ee",
	}
	value := map[string]string{"keyword": stylesheet.HighlightVariable("keyword"), "string": "violet", "function": stylesheet.CodeColour("info")}
	channels := regexp.MustCompile(`^(\d+),(\d+),(\d+),(\d+)$`)
	for _, m := range []stylesheet.Mode{stylesheet.LightMode, stylesheet.DarkMode} {
		word := map[stylesheet.Mode]string{stylesheet.LightMode: "light", stylesheet.DarkMode: "dark"}[m]
		for _, kind := range []string{"keyword", "string", "function"} {
			key := word + " " + kind
			want, ok := palette.Paint(value[kind], m)
			if !ok {
				t.Fatalf("%s: the palette cannot paint %s", key, value[kind])
			}
			if pin, ok := pinned[key]; ok && want.Hex() != pin {
				t.Errorf("%s: Paint says %s, the call writes %s", key, want.Hex(), pin)
			}
			c := channels.FindStringSubmatch(got[key])
			if c == nil {
				t.Errorf("%s: the browser gave %q", key, got[key])
				continue
			}
			for i, v := range []float64{want.R, want.G, want.B} {
				px, _ := strconv.Atoi(c[i+1])
				if d := math.Abs(v*255 - float64(px)); d > 1 {
					t.Errorf("%s: browser %s, Paint %s: channel %d is %.2f/255 away", key, got[key], want.Hex(), i, d)
				}
			}
			if c[4] != "255" {
				t.Errorf("%s: not opaque: %s", key, got[key])
			}
		}
	}
	if got["light function"] == got["dark function"] {
		t.Errorf("CONTROL: the engine's function changes with the mode: %s in both", got["light function"])
	}
	if got["light keyword"] == got["dark keyword"] {
		t.Errorf("the pair of keyword changes with the mode: %s in both", got["light keyword"])
	}
}
