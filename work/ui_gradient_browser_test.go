package work

import (
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// gradientBrowserFiles is a site with the standard set's gradients written as a page would: a glow
// behind a hero, a fade at the end of a scroller (and the same inside a frame the probe turns
// right-to-left), a glow that a colour replaces from medium, a gradient headline, gradient text whose
// stops are color-mix(), an icon painted with a gradient (its background), and a switch written with the
// glow box's medium colour. A solid paper box gives the probe the paper's colour.
var gradientBrowserFiles = map[string]string{
	"localhost/router.work": `import { router } from "kitwork";

router.colors({ brand: "#c8742c" });
router.gradients({ ember: { type: "linear", direction: "to-end", stops: [ { color: "ink-10", at: 0 }, { color: "brand+15", at: 100 } ] } });
`,
	"localhost/page.ui": `<page><content /></page>`,
	"localhost/content.ui": `<content>
  <box id="glow" background="glow" padding="4"><text>Quán</text></box>
  <box id="end" background="fade-end" padding="4"><text>Cuộn tiếp</text></box>
  <box id="holder"><box id="end-rtl" background="fade-end" padding="4"><text>Cuộn tiếp</text></box></box>
  <box id="wide" background="glow" medium:background="paper" padding="4"><text>Rộng</text></box>
  <box id="paper" background="paper" padding="4"><text>paper</text></box>
  <text id="headline" heading="1" color="headline">Kitwork</text>
  <text id="tinted" heading="1" color="ember">Kitwork</text>
  <icon id="star" value="star" background="nebula" alt="Yêu thích" />
  <field><label>Bật</label><input id="switch" type="switch" name="on" medium:background="paper" /></field>
</content>`,
}

// gradientProbe reads, once the page has loaded, what the browser computed for each case, in words: the
// kind of image (radial, linear, none) and its direction, the clip and the fill of the headline, whether
// its fill is the colour of its text, and whether forced colours are on. It turns the holder
// right-to-left first, as dir="rtl" on a parent would.
const gradientProbe = `<script>
(function () {
  var byId = function (id) { return document.getElementById(id); };
  function frames(count, then) { if (count <= 0) { then(); return; } requestAnimationFrame(function () { frames(count - 1, then); }); }
  function send(report) {
    var close = function () { window.close(); };
    fetch("/report", { method: "POST", body: JSON.stringify(report) }).then(close, close);
  }
  function image(element) {
    var value = getComputedStyle(element).backgroundImage;
    if (value === "none") return "none";
    var kind = /^(\w+)-gradient\(/.exec(value);
    var words = kind ? kind[1] : value;
    if (/to right/.test(value)) words += " to-right";
    if (/to left/.test(value)) words += " to-left";
    if (/at (50% 0%|center top|top)/.test(value)) words += " at-top";
    return words;
  }
  window.addEventListener("load", function () {
    byId("holder").setAttribute("dir", "rtl");
    frames(3, function () {
      try {
        var report = {}, headline = getComputedStyle(byId("headline"));
        report.forced = String(matchMedia("(forced-colors: active)").matches);
        report.glow = image(byId("glow"));
        report.end = image(byId("end"));
        report["end rtl"] = image(byId("end-rtl"));
        report.wide = image(byId("wide")) + " paper=" + (getComputedStyle(byId("wide")).backgroundColor === getComputedStyle(byId("paper")).backgroundColor);
        report.headline = image(byId("headline")) + " clip=" + (headline.webkitBackgroundClip || headline.backgroundClip) +
          " fill=" + (headline.webkitTextFillColor === "rgba(0, 0, 0, 0)" ? "transparent" : "painted");
        report["headline colour"] = headline.color;
        var tinted = getComputedStyle(byId("tinted"));
        report.tinted = image(byId("tinted")) + " clip=" + (tinted.webkitBackgroundClip || tinted.backgroundClip) +
          " fill=" + (tinted.webkitTextFillColor === "rgba(0, 0, 0, 0)" ? "transparent" : "painted");
        report.icon = image(byId("star"));
        report["switch"] = image(byId("switch"));
        send(report);
      } catch (problem) { send({ error: String(problem && problem.stack || problem) }); } // a broken probe reports, not hangs
    });
  });
})();
</script>`

// TestBrowserGradients proves gradients (decision of 12/10/2026) in a real browser on a page the engine
// serves, in light and in forced colours (Windows contrast themes; blink's inForcedColors):
//   - background="glow" is a radial gradient at the top; background="fade-end" a linear one to the
//     right, and to the left inside a frame turned right-to-left (the :dir(rtl) rule);
//   - background="glow" medium:background="paper", in a window wider than medium, has no image and the
//     paper's colour: the colour written for medium takes the gradient away; a switch written with the
//     same medium:background="paper" keeps its thumb (reboot.css's radial image): the colour takes back
//     only the gradient of its own element;
//   - color="headline" is a linear gradient clipped to the text with a transparent fill, its colour the
//     ink of its first stop (the fallback where the browser cannot clip); the site's color="ember", whose
//     first stop is color-mix() (ink-10), is clipped too: the browser reads the @supports that also asks for
//     color-mix() (a browser without color-mix() keeps the solid first stop, never a transparent fill);
//   - an icon written background="nebula" is painted with the gradient (its mask over the image);
//   - in forced colours every gradient gives way: no image, and the headline's fill is no longer
//     transparent (getComputedStyle resolves a currentcolor fill against the colour before forcing, for
//     every element, so what is painted is TestBrowserGradientTextInForcedColours's to see). Edge takes
//     a gradient background away in forced colours by itself, except on an icon, which reboot.css
//     opts out of forced colours (forced-color-adjust:none) to paint it CanvasText: there only the
//     engine's own forced-colours rule takes the gradient away, so the icon is the system's colour.
func TestBrowserGradients(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the gradient browser proof in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, gradientBrowserFiles)
	if body := serveUI(t, tenant, "http://localhost/").Body.String(); !strings.Contains(body, "background-glow") || !strings.Contains(body, "color-headline") {
		t.Fatalf("the site is not the probe's: it must write the glow and the headline:\n%.1500s", body)
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
				body = body[:at] + gradientProbe + body[at:]
			}
			response.Header().Del("Content-Length")
		}
		response.WriteHeader(answer.Code)
		_, _ = io.WriteString(response, body)
	}))
	defer server.Close()

	profiles := []struct {
		name, settings string
		want           map[string]string
	}{
		{"light", "", map[string]string{
			"forced":          "false",
			"glow":            "radial at-top",
			"end":             "linear to-right",
			"end rtl":         "linear to-left",
			"wide":            "none paper=true",
			"headline":        "linear to-right clip=text fill=transparent",
			"headline colour": "rgb(10, 37, 64)", // ink, the first stop
			"tinted":          "linear to-right clip=text fill=transparent",
			"icon":            "linear to-right",
			"switch":          "radial", // its thumb, reboot.css's image, beside the wide box's taken-back glow
		}},
		{"forced colors", "inForcedColors=true", map[string]string{
			"forced":   "true",
			"glow":     "none",
			"end":      "none",
			"end rtl":  "none",
			"wide":     "none paper=true",
			"headline": "none clip=text fill=painted",
			"tinted":   "none clip=text fill=painted",
			"icon":     "none",
		}},
	}
	for _, profile := range profiles {
		got := runBrowser(t, browser, server.URL+"/", "gradients, "+profile.name, 1000, profile.settings, reports)
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

// TestBrowserGradientTextInForcedColours looks at the pixels a real browser paints for gradient text
// (color="headline", large, alone on the page), from a screenshot: in light, its letters carry the
// gradient's colours (ink warming into the brand: channels far apart); in forced colours they are the
// system's text colour, pure black on the system's white, every pixel of them grey, none of the
// gradient's colours. So the text is painted, never transparent over nothing, and never in a colour the
// contrast theme did not choose.
func TestBrowserGradientTextInForcedColours(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping the gradient screenshot in short mode")
	}
	browser := findHeadlessBrowserForGate()
	if browser == "" {
		t.Skip("Chrome, Chromium, or Edge is not installed")
	}
	tenant := buildUISite(t, map[string]string{
		"localhost/router.work": gradientBrowserFiles["localhost/router.work"],
		"localhost/page.ui":     `<page><content /></page>`,
		"localhost/content.ui":  `<content padding="4"><text heading="1" size="huge" color="headline">MMMMMMMM</text></content>`,
	})
	var serving sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		forward := request.Clone(request.Context())
		forward.Host = "localhost"
		serving.Lock()
		defer serving.Unlock()
		tenant.Serve(response, forward)
	}))
	defer server.Close()

	// letters are the pixels that are not the page's background (the top-left corner): how many, how
	// many are pure black, and the widest spread between two channels of one of them
	letters := func(name, settings string) (count, black, spread int) {
		shot := screenshotOf(t, browser, server.URL+"/", name, settings)
		bounds := shot.Bounds()
		background := shot.At(bounds.Min.X+1, bounds.Min.Y+1)
		for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
			for x := bounds.Min.X; x < bounds.Max.X; x++ {
				pixel := shot.At(x, y)
				if pixel == background {
					continue
				}
				r, g, b, _ := pixel.RGBA()
				r, g, b = r>>8, g>>8, b>>8
				count++
				if r == 0 && g == 0 && b == 0 {
					black++
				}
				spread = max(spread, int(max(r, g, b)-min(r, g, b)))
			}
		}
		return count, black, spread
	}
	if count, _, spread := letters("light", ""); count < 500 || spread < 30 {
		t.Errorf("light: %d letter pixels, their channels at most %d apart: want the gradient's colours (control)", count, spread)
	}
	if count, black, spread := letters("forced colors", "inForcedColors=true"); count < 500 || black < 500 || spread > 2 {
		t.Errorf("forced colours: %d letter pixels, %d pure black, channels at most %d apart: want the system's black text, no gradient", count, black, spread)
	}
}

// screenshotOf is the page at url as headless Chrome or Edge paints it, 800 by 400, in the light scheme
// with blink's settings, decoded from the PNG the browser writes once the page has loaded.
func screenshotOf(t *testing.T, browser, url, name, settings string) image.Image {
	t.Helper()
	file := filepath.Join(t.TempDir(), "shot.png")
	blink := "preferredColorScheme=1"
	if settings != "" {
		blink += "," + settings
	}
	arguments := []string{"--headless=new", "--disable-gpu", "--no-sandbox", "--disable-dev-shm-usage", "--hide-scrollbars",
		"--disable-background-networking", "--disable-default-apps", "--disable-extensions", "--disable-sync", "--no-first-run",
		"--window-size=800,400", "--user-data-dir=" + filepath.Join(t.TempDir(), "profile"), "--blink-settings=" + blink,
		"--screenshot=" + file, url}
	if runtime.GOOS == "windows" {
		arguments = append(arguments, "--disable-breakpad", "--disable-crashpad-for-testing")
	}
	deadline, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	if output, err := exec.CommandContext(deadline, browser, arguments...).CombinedOutput(); err != nil {
		t.Fatalf("%s: the screenshot: %v\n%.1500s", name, err, output)
	}
	f, err := os.Open(file)
	if err != nil {
		t.Fatalf("%s: no screenshot: %v", name, err)
	}
	defer f.Close()
	shot, err := png.Decode(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return shot
}
