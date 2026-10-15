package work

import (
	"encoding/json"
	"fmt"
	"image"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// backdropBrowserFiles is a site whose page has a backdrop (<page background="cirrus">, decision D4 of
// 13/10/2026) and a document 4000px tall, with an anchor in its middle a URL can scroll to, the text
// in it padded away from the corners. Its colours have a dark look, and the page follows the device.
var backdropBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c", canvas: { light: "#f6f9fc", dark: "#0f1822" } });
`,
	"localhost/page.ui":    `<page background="cirrus"><content /></page>`,
	"localhost/content.ui": `<content><box height="2000px"></box><box id="middle" padding="16"><text>Giữa trang</text></box><box height="2000px"></box></content>`,
}

// backdropProbe reads, once the page has loaded and again scrolled 1800px down, where the backdrop
// layer is on the screen, what it is painted with, the ground of <html> and of <body>, and the
// theme-color the browser's bar would take (the first meta whose media holds, as a browser picks it:
// the pre-paint gives both the mode shown's colour and drops their media).
const backdropProbe = `<script>
(function () {
  function frames(count, then) { if (count <= 0) { then(); return; } requestAnimationFrame(function () { frames(count - 1, then); }); }
  function send(report) {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
  function place(layer) {
    var box = layer.getBoundingClientRect();
    var screen = document.documentElement; // the screen a fixed element covers: the window less its scrollbar
    var at = Math.round(box.left) + "," + Math.round(box.top);
    if (box.width === screen.clientWidth && box.height === screen.clientHeight) return at + " the screen";
    return at + " " + box.width + "x" + box.height + ", the screen " + screen.clientWidth + "x" + screen.clientHeight;
  }
  window.addEventListener("load", function () {
    frames(3, function () {
      try {
        var report = {}, layer = document.querySelector("[data-kitwork-backdrop]"), style = getComputedStyle(layer);
        report.scheme = "dark=" + matchMedia("(prefers-color-scheme: dark)").matches + " theme=" + document.documentElement.getAttribute("data-theme");
        report.document = String(document.documentElement.scrollHeight >= 4000);
        report.layer = style.position + " z=" + style.zIndex + " pointer=" + style.pointerEvents + " first=" + (document.body.firstElementChild === layer);
        report.image = style.backgroundImage;
        report.html = getComputedStyle(document.documentElement).backgroundColor;
        report.body = getComputedStyle(document.body).backgroundImage + " " + getComputedStyle(document.body).backgroundColor;
        var bar = "";
        document.querySelectorAll('meta[name="theme-color"]').forEach(function (meta) { if (!bar && (!meta.media || matchMedia(meta.media).matches)) bar = meta.content; });
        report.bar = bar;
        report.top = place(layer);
        window.scrollTo(0, 1800);
        frames(3, function () {
          report.scrolled = "y=" + Math.round(scrollY) + " " + place(layer);
          send(report);
        });
      } catch (problem) { send({ error: String(problem && problem.stack || problem) }); } // a broken probe reports, not hangs
    });
  });
})();
</script>`

// TestBrowserPageBackdrop proves a page backdrop in a real browser on a page the engine serves, in light
// and in dark (the device's mode; the pre-paint shows it):
//   - the layer is first in <body>, position fixed, under everything (z-index -1), taking no pointer,
//     covering the screen exactly, and still covering it exactly with the document scrolled 1800px;
//   - it is painted with cirrus in the mode's colours, the pairs' custom properties resolved (to bottom
//     right, the corner, the middle at 34%, the neutral from 67%); <body> paints nothing over it;
//   - <html>'s ground is the first stop of the mode, and the theme-color whose media holds is too.
func TestBrowserPageBackdrop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the page backdrop browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, backdropBrowserFiles)
	if body := serveUI(t, tenant, "http://localhost/").Body.String(); !strings.Contains(body, "data-kitwork-backdrop") {
		t.Fatalf("the site is not the probe's: it must write the backdrop layer:\n%.1500s", body)
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
				body = body[:at] + backdropProbe + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	const screen = "0,0 the screen"
	for _, profile := range []struct {
		name, settings string
		want           map[string]string
	}{
		{"light", "", map[string]string{
			"scheme":   "dark=false theme=light",
			"document": "true",
			"layer":    "fixed z=-1 pointer=none first=true",
			"image":    "linear-gradient(to right bottom, rgb(132, 156, 221) 0%, rgb(169, 187, 224) 34%, rgb(205, 217, 226) 67%)",
			"html":     "rgb(132, 156, 221)",
			"body":     "none rgba(0, 0, 0, 0)",
			"bar":      "#849cdd",
			"top":      screen,
			"scrolled": "y=1800 " + screen,
		}},
		{"dark", "preferredColorScheme=0", map[string]string{
			"scheme":   "dark=true theme=dark",
			"layer":    "fixed z=-1 pointer=none first=true",
			"image":    "linear-gradient(to right bottom, rgb(32, 47, 94) 0%, rgb(24, 25, 72) 34%, rgb(13, 15, 21) 67%)",
			"html":     "rgb(32, 47, 94)",
			"body":     "none rgba(0, 0, 0, 0)",
			"bar":      "#202f5e",
			"scrolled": "y=1800 " + screen,
		}},
	} {
		got := runBrowser(t, browser, server.URL+"/", "backdrop, "+profile.name, 1000, profile.settings, reports)
		if message, broke := got["error"]; broke {
			t.Fatalf("%s: the probe broke: %s", profile.name, message)
		}
		failed := false
		for key, want := range profile.want {
			if got[key] != want {
				t.Errorf("%s, %s:\n got  %s\n want %s", profile.name, key, got[key], want)
				failed = true
			}
		}
		if failed {
			t.Logf("%s: %v", profile.name, got)
		}
	}
}

// TestBrowserPageBackdropStaysOnTheScreen looks at the pixels a real browser paints (the screen as it
// composited it, through the DevTools protocol: devtools_browser_test.go), at the top of the 4000px
// document, scrolled 1800px to its middle and scrolled to its end: each time the screen's top-left
// corner is the gradient's first stop (#849cdd) and its bottom-right corner the far end (#cdd9e2, held
// from 67%), so the gradient spans the screen and stays while the document moves under it. CONTROL: the
// same gradient on a box as tall as the document (background="cirrus" on a frame of its own) shows a
// colour from the middle of the gradient at the bottom of the first screen, where the backdrop shows its
// far end, and at the top of the middle screen, where the backdrop shows its first stop, so the
// screenshots tell the two apart.
func TestBrowserPageBackdropStaysOnTheScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the page backdrop screenshots in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	serve := func(files map[string]string) *httptest.Server {
		tenant := buildUISite(t, files)
		var serving sync.Mutex
		server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			forward := request.Clone(request.Context())
			forward.Host = "localhost"
			serving.Lock()
			defer serving.Unlock()
			tenant.Serve(response, forward)
		}))
		t.Cleanup(server.Close)
		return server
	}
	corners := func(shot image.Image) (topStart, bottomEnd string) {
		b := shot.Bounds()
		hex := func(x, y int) string {
			r, g, bl, _ := shot.At(x, y).RGBA()
			return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, bl>>8)
		}
		return hex(b.Min.X+2, b.Min.Y+2), hex(b.Max.X-3, b.Max.Y-3)
	}
	const settle = `new Promise(function (done) { requestAnimationFrame(function () { requestAnimationFrame(function () { done(Math.round(scrollY) + " of " + document.documentElement.scrollHeight); }); }); })`
	// screens is the screen at the top, scrolled 1800px and scrolled to the end, with where each is
	screens := func(server *httptest.Server) (shots []image.Image, places []string) {
		d := openDevtools(t, browser, server.URL+"/", 800, 400, "")
		places = append(places, d.loaded(settle))
		shots = append(shots, d.screenshot())
		for _, scroll := range []string{"1800", "document.documentElement.scrollHeight"} {
			places = append(places, d.evaluate(`window.scrollTo(0, `+scroll+`); `+settle))
			shots = append(shots, d.screenshot())
		}
		return shots, places
	}
	shots, places := screens(serve(backdropBrowserFiles))
	for i, shot := range shots {
		top, bottom := corners(shot)
		if !nearHex(top, "#849cdd") || !nearHex(bottom, "#cdd9e2") {
			t.Errorf("scrolled to %s: the screen's corners are %s and %s, want the first stop #849cdd and the far end #cdd9e2", places[i], top, bottom)
		}
	}
	if !strings.HasPrefix(places[1], "1800 of ") || places[0] == places[2] {
		t.Errorf("the screens are not the top, the middle and the end of the document: %v", places)
	}
	stretched := map[string]string{
		"localhost/router.work": backdropBrowserFiles["localhost/router.work"],
		"localhost/page.ui":     `<page><content /></page>`,
		"localhost/content.ui":  `<content><box background="cirrus"><box height="2000px"></box><box id="middle" padding="16"><text>Giữa trang</text></box><box height="2000px"></box></box></content>`,
	}
	control, where := screens(serve(stretched))
	if _, bottom := corners(control[0]); nearHex(bottom, "#cdd9e2") {
		t.Errorf("control: a gradient as tall as the document, at %s, shows its far end %s at the bottom of the screen", where[0], bottom)
	}
	if top, _ := corners(control[1]); nearHex(top, "#849cdd") {
		t.Errorf("control: a gradient as tall as the document, scrolled to %s, shows its first stop %s at the top of the screen", where[1], top)
	}
}

// nearHex reports whether two #rrggbb colours are within 3 of each other in every channel: a browser's
// gradient may round a channel differently.
func nearHex(a, b string) bool {
	var x, y [3]int
	if _, err := fmt.Sscanf(a, "#%02x%02x%02x", &x[0], &x[1], &x[2]); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(b, "#%02x%02x%02x", &y[0], &y[1], &y[2]); err != nil {
		return false
	}
	for i := range x {
		if d := x[i] - y[i]; d > 3 || d < -3 {
			return false
		}
	}
	return true
}
